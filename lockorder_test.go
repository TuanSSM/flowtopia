package main

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// Every statement that locks several rows of the delivery queue locks them in the order of their ids (small point 5 of
// the third review of FWR 6 and point 2 of the fourth, deliveryLockOrder): in each plan, every row lock is taken over
// rows of ap_deliveries sorted by their id (the sort's key is one of the names the statement gives ap_deliveries), so
// two workers never each hold a row the other waits for.
func TestDBDeliveryLockOrder(t *testing.T) {
	e := newTalkEnv(t)
	type node struct {
		Type    string   `json:"Node Type"`
		SortKey []string `json:"Sort Key"`
		Index   string   `json:"Index Name"`
		Parent  string   `json:"Parent Relationship"`
		Plans   []node   `json:"Plans"`
	}
	// ordered says the rows of n come out in the order of the ids of ap_deliveries (keys: its names in the statement
	// with .id): a sort by such an id, a merge join of inputs in that order, or a scan of its primary key.
	var keys []string
	var ordered func(n node) bool
	ordered = func(n node) bool {
		switch n.Type {
		case "Sort":
			return len(n.SortKey) == 1 && slices.Contains(keys, n.SortKey[0])
		case "Merge Join":
			for _, p := range n.Plans {
				if !ordered(p) {
					return false
				}
			}
			return len(n.Plans) > 0
		case "Index Scan", "Index Only Scan":
			return n.Index == "ap_deliveries_pkey"
		}
		return false
	}
	now := e.clock.now()
	for _, c := range []struct {
		name string
		sql  string
		args []any
		keys []string // the names of ap_deliveries in the statement, with .id
	}{
		{"the 410's rows of an inbox", inboxGoneSQL, []any{"a.example", "https://a.example/inbox", now, "gone"}, []string{"ap_deliveries.id"}},
		{"a 429's or an opened circuit's rows of a host", hostHeldSQL, []any{"a.example", now.Add(time.Hour), now}, []string{"ap_deliveries.id"}},
		{"an unavailable host's rows", hostGoneSQL, []any{"a.example", now}, []string{"ap_deliveries.id"}},
		{"the pull of rows due too far ahead", deliveryPullDueSQL, []any{now, now.Add(-time.Hour), deliveryBatch}, []string{"ap_deliveries.id"}},
		{"the pull of rows written in the future", deliveryPullAgeSQL, []any{now.Add(-time.Hour), now, deliveryBatch}, []string{"ap_deliveries.id"}},
		{"a takeback's rows of a line", takeBackSQL, []any{1, 2, now}, []string{"d.id", "d_1.id"}},
		{"the send-time question over a line", heldBackSQL, []any{1, "https://a.example/inbox", now, true}, []string{"d.id", "k.id"}},
		// FWR 7: a member's rows before they go with the member, and a gone account's own inbox in its own Delete.
		{"a member's rows before the account or its activities go", memberRowsLockSQL, []any{int64(1)}, []string{"ap_deliveries.id"}},
		{"a gone account's own inbox", actorGoneSQL, []any{int64(1), "https://a.example/users/x/inbox", "a.example", now}, []string{"ap_deliveries.id"}},
	} {
		keys = c.keys
		var raw []byte
		if err := e.a.db.QueryRow(`EXPLAIN (FORMAT JSON) `+c.sql, c.args...).Scan(&raw); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		var out []struct{ Plan node }
		if err := json.Unmarshal(raw, &out); err != nil || len(out) != 1 {
			t.Fatalf("%s: %v", c.name, err)
		}
		locks := 0
		var walk func(n node)
		walk = func(n node) {
			if n.Type == "LockRows" {
				locks++
				// What the lock reads (its InitPlans, such as an ARRAY of ids, are worked out before it and lock nothing).
				var rows []node
				for _, p := range n.Plans {
					if p.Parent != "InitPlan" && p.Parent != "SubPlan" {
						rows = append(rows, p)
					}
				}
				if len(rows) != 1 || !ordered(rows[0]) {
					t.Errorf("%s: a row lock over rows not sorted by id: %s", c.name, raw)
				}
			}
			for _, p := range n.Plans {
				walk(p)
			}
		}
		walk(out[0].Plan)
		if locks == 0 {
			t.Errorf("%s: no row lock in order: %s", c.name, raw)
		}
	}
}

// No statement in main.go changes several queued rows of ap_deliveries outside the ones TestDBDeliveryLockOrder reads:
// each statement that updates or removes rows of it either names one row by its id, locks in order (FOR UPDATE, read
// above, or the claim's SKIP LOCKED), or touches only failed rows (housekeeping), which nothing else locks.
func TestDeliveryStatementsLockInOrder(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, q := range regexp.MustCompile("(?s)`[^`]*`").FindAllString(string(src), -1) {
		if !strings.Contains(q, "UPDATE ap_deliveries") && !strings.Contains(q, "DELETE FROM ap_deliveries") {
			continue
		}
		seen++
		switch {
		case strings.Contains(q, "WHERE id = $1"), strings.Contains(q, "FOR UPDATE"), strings.Contains(q, "SKIP LOCKED"),
			strings.Contains(q, "WHERE state = 'failed'"):
		default:
			t.Errorf("a statement changes rows of ap_deliveries without taking them in the order of their ids: %s", q)
		}
	}
	if seen < 10 {
		t.Errorf("only %d statements over ap_deliveries found in main.go: the test no longer reads them", seen)
	}
}
