package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestCleanLink(t *testing.T) {
	cases := []struct {
		kind, in, want string
		ok             bool
	}{
		{"instagram", "flowa_spins", "flowa_spins", true},
		{"instagram", "@flowa.spins", "flowa.spins", true},
		{"instagram", "https://www.instagram.com/flowa_spins/?hl=en", "flowa_spins", true},
		{"tiktok", "https://www.tiktok.com/@flowa.spins", "flowa.spins", true},
		{"tiktok", "http://m.tiktok.com/@flowa.spins/", "flowa.spins", true},
		{"youtube", "https://youtube.com/@FlowaSpins", "FlowaSpins", true},
		{"youtube", "https://www.youtube.com/channel/UC1234abcd", "channel/UC1234abcd", true},
		{"youtube", "youtu.be/abc", "", false},
		{"youtube", "https://www.youtube.com/watch?v=abc", "", false},
		{"youtube", "youtube.com/shorts/xyz", "", false},
		{"instagram", "", "", true},
		{"instagram", "   ", "", true},
		{"instagram", "has space", "", false},
		{"instagram", "a/b", "", false},
		{"instagram", "<script>", "", false},
	}
	for _, c := range cases {
		got, ok := cleanLink(c.kind, c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("cleanLink(%q, %q) = %q, %v; want %q, %v", c.kind, c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestMentions(t *testing.T) {
	got := mentions("Hey @Sam and @sam, @jo-2 did F7! mail me@example.com @x")
	want := []string{"sam", "jo-2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mentions = %v, want %v", got, want)
	}
	if n := len(mentions("nobody here")); n != 0 {
		t.Errorf("expected no mentions, got %d", n)
	}
}

func TestValidStats(t *testing.T) {
	good := []string{`{}`, `{"xp": 120, "level": 3, "streak": 7, "shared": true}`}
	bad := []string{`[]`, `"x"`, `{"xp": "120"}`, `{"nested": {"a": 1}}`, `{"list": [1]}`}
	for _, s := range good {
		if !validStats(json.RawMessage(s)) {
			t.Errorf("validStats(%s) should pass", s)
		}
	}
	for _, s := range bad {
		if validStats(json.RawMessage(s)) {
			t.Errorf("validStats(%s) should fail", s)
		}
	}
}
