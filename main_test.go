package main

import "testing"

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
