package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Every statement that locks several rows of the delivery queue locks them in the order of their ids (small point 5 of
// the third review of FWR 6, deliveryLockOrder): in each plan, every row lock is taken over rows sorted by id, so two
// workers never each hold a row the other waits for.
func TestDBDeliveryLockOrder(t *testing.T) {
	e := newTalkEnv(t)
	type node struct {
		Type    string   `json:"Node Type"`
		SortKey []string `json:"Sort Key"`
		Index   string   `json:"Index Name"`
		Plans   []node   `json:"Plans"`
	}
	// ordered says the rows of n come out in the order of their ids: a sort by id, a merge join of inputs in that order,
	// or a scan of the primary key.
	var ordered func(n node) bool
	ordered = func(n node) bool {
		switch n.Type {
		case "Sort":
			return len(n.SortKey) == 1 && (strings.HasSuffix(n.SortKey[0], ".id") || n.SortKey[0] == "id")
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
	}{
		{"the 410's rows of an inbox", inboxGoneSQL, []any{"a.example", "https://a.example/inbox", now, "gone"}},
		{"a 429's rows of a host", hostHeldSQL, []any{"a.example", now.Add(time.Hour), now}},
		{"a takeback's rows of a line", takeBackSQL, []any{1, 2, now}},
		{"the send-time question over a line", heldBackSQL, []any{1, "https://a.example/inbox", now, true}},
	} {
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
				if len(n.Plans) != 1 || !ordered(n.Plans[0]) {
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
