package main

import (
	"database/sql"
	"strings"
	"testing"
)

// The name of a member here that opens a comment is read alike in Go (leadName, startsWithMention, at the write) and in
// Postgres (leadNamePattern in commentOutSQL, at the send): one table of texts through both (point 1 of the fourth review
// of FWR 6), as test/mentions.js does for the server and the page. A handle of another server never reads as a name here.
func TestDBLeadNameAgrees(t *testing.T) {
	e := newEnv(t)
	for _, c := range []struct{ text, want string }{
		{"@abc hi", "abc"},
		{"@abc", "abc"},
		{"@ABC hi", "abc"},
		{"@abc, hi", "abc"},
		{"@abc. hi", "abc"},
		{"@abc.", "abc"},
		{"@abc.def hi", "abc.def"},
		{"@abc-x hi", "abc-x"},
		{"@abc@host hi", ""},
		{"@abc.def@host x", ""},
		{"@abc-x@host", ""},
		{"@fan-x@third.example hi", ""},
		{"@fan.x@third.example hi", ""},
		{"@fan_x@third.example hi", ""},
		{"@fan.@third.example", ""},
		{"@ab hi", ""},
		{"@.abc hi", ""},
		{"@_abc hi", ""},
		{"hello @abc", ""},
		{"@" + strings.Repeat("a", 24) + " hi", strings.Repeat("a", 24)},
		{"@" + strings.Repeat("a", 30) + " hi", ""},
		{"@" + strings.Repeat("a", 22) + ".b hi", strings.Repeat("a", 22) + ".b"},
		{"@" + strings.Repeat("a", 23) + ".b hi", strings.Repeat("a", 23)},
		{"@abc_ hi", "abc_"},
		// A letter or a digit of another script straight after the name does not hide it from either side.
		{"@aliceさん、ありがとう", "alice"},
		{"@aliceé hi", "alice"},
		{"@alice١", "alice"},
		{"@aliceß@host", "alice"},
		{"@abc\nhi", "abc"},
		{"@abc's", "abc"},
		{"@", ""},
		{"", ""},
	} {
		var got sql.NullString
		if err := e.a.db.QueryRow(`SELECT substring(lower($1) from '`+leadNamePattern+`')`, c.text).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if goName := leadName(c.text); goName != got.String || goName != c.want {
			t.Errorf("%q: Go reads %q, Postgres %q, want %q", c.text, goName, got.String, c.want)
		}
	}
}
