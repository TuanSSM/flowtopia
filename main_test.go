package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

func TestCleanTags(t *testing.T) {
	tags, ok := cleanTags([]postTag{{"nunchaku", "F7"}, {"poi", " L2 "}, {"nunchaku", "F7"}})
	if !ok || len(tags) != 2 || tags[1].Skill != "L2" {
		t.Errorf("cleanTags should keep two distinct trimmed tags, got %v %v", tags, ok)
	}
	for _, bad := range [][]postTag{{{"drums", "F1"}}, {{"poi", ""}}, {{"poi", "<b>"}}} {
		if _, ok := cleanTags(bad); ok {
			t.Errorf("cleanTags(%v) should fail", bad)
		}
	}
	many := make([]postTag, 11)
	for i := range many {
		many[i] = postTag{"poi", "F" + string(rune('0'+i%10)) + string(rune('a'+i))}
	}
	if _, ok := cleanTags(many); ok {
		t.Errorf("more than %d tags should fail", maxTags)
	}
}

func TestCleanCombo(t *testing.T) {
	good := postCombo{ID: "abc", Name: " Morning loop ", Role: "reliable", Prop: "nunchaku", Seq: []string{"F5", "F7"}, Reps: []int{1, 3}}
	if !cleanCombo(&good) || good.Name != "Morning loop" {
		t.Errorf("a valid combo should pass and be trimmed: %+v", good)
	}
	bad := []postCombo{
		{Name: "x", Prop: "nunchaku"},
		{Name: "x", Prop: "nunchaku", Seq: []string{"F5"}, Role: "epic"},
		{Name: "x", Prop: "piano", Seq: []string{"F5"}},
		{Name: "x", Prop: "nunchaku", Seq: []string{"F5"}, Reps: []int{0}},
		{Name: "x", Prop: "nunchaku", Seq: []string{"F5", "F7"}, Reps: []int{1}},
	}
	for _, c := range bad {
		if cleanCombo(&c) {
			t.Errorf("cleanCombo(%+v) should fail", c)
		}
	}
}

func TestCatalogFixtures(t *testing.T) {
	files, _ := filepath.Glob("test/catalog-fixtures/*.json")
	if len(files) < 20 {
		t.Fatalf("expected at least 20 fixtures, found %d", len(files))
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		errs, _ := validateCatalog(raw)
		want := strings.HasPrefix(filepath.Base(f), "good-")
		if (len(errs) == 0) != want {
			t.Errorf("%s: want pass=%v, got errors %v", f, want, errs)
		}
	}
}

func TestShippedCatalogsValid(t *testing.T) {
	files, _ := filepath.Glob("web/catalog/*.json")
	if len(files) != 5 {
		t.Fatalf("expected 5 catalogs, found %d", len(files))
	}
	for _, f := range files {
		raw, _ := os.ReadFile(f)
		if errs, _ := validateCatalog(raw); len(errs) > 0 {
			t.Errorf("%s: %v", f, errs[:min(len(errs), 5)])
		}
	}
}

func TestParseLink(t *testing.T) {
	good := map[string]string{
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ":                       "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		"https://youtube.com/watch?v=dQw4w9WgXcQ&t=42s&si=abc":              "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		"http://m.youtube.com/watch?feature=share&v=dQw4w9WgXcQ":            "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		"youtube.com/watch?v=dQw4w9WgXcQ":                                   "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		"https://youtu.be/dQw4w9WgXcQ":                                      "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		"https://youtu.be/dQw4w9WgXcQ?si=XYZ123":                            "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		"https://www.youtube.com/shorts/aBcDeFgHiJk":                        "https://www.youtube.com/shorts/aBcDeFgHiJk",
		"https://youtube.com/shorts/aBcDeFgHiJk?feature=share":              "https://www.youtube.com/shorts/aBcDeFgHiJk",
		"https://www.youtube.com/live/aBcDeFgHiJk":                          "https://www.youtube.com/watch?v=aBcDeFgHiJk",
		"https://www.youtube.com/embed/aBcDeFgHiJk":                         "https://www.youtube.com/watch?v=aBcDeFgHiJk",
		"https://music.youtube.com/watch?v=aBcDeFgHiJk":                     "https://www.youtube.com/watch?v=aBcDeFgHiJk",
		"https://www.instagram.com/p/C1a2B3c4D5e/":                          "https://www.instagram.com/p/C1a2B3c4D5e/",
		"https://instagram.com/p/C1a2B3c4D5e":                               "https://www.instagram.com/p/C1a2B3c4D5e/",
		"https://www.instagram.com/p/C1a2B3c4D5e/?igsh=MWQ1ZGUxMzBkMA==":    "https://www.instagram.com/p/C1a2B3c4D5e/",
		"https://www.instagram.com/reel/C9xYz_-AbCd/":                       "https://www.instagram.com/reel/C9xYz_-AbCd/",
		"https://www.instagram.com/reels/C9xYz_-AbCd/":                      "https://www.instagram.com/reel/C9xYz_-AbCd/",
		"https://www.instagram.com/tv/C9xYz_-AbCd/":                         "https://www.instagram.com/p/C9xYz_-AbCd/",
		"https://www.instagram.com/flowa.spins/p/C1a2B3c4D5e/":              "https://www.instagram.com/p/C1a2B3c4D5e/",
		"https://www.instagram.com/flowa.spins/reel/C9xYz_-AbCd/?hl=en":     "https://www.instagram.com/reel/C9xYz_-AbCd/",
		"instagram.com/reel/C9xYz_-AbCd":                                    "https://www.instagram.com/reel/C9xYz_-AbCd/",
		"https://www.tiktok.com/@flowa.spins/video/7301234567890123456":     "https://www.tiktok.com/@flowa.spins/video/7301234567890123456",
		"https://tiktok.com/@flowa_spins/video/7301234567890123456?lang=en": "https://www.tiktok.com/@flowa_spins/video/7301234567890123456",
		"https://m.tiktok.com/v/7301234567890123456.html":                   "https://m.tiktok.com/v/7301234567890123456.html",
		"https://www.tiktok.com/@flowa/video/7301234567890123456/":          "https://www.tiktok.com/@flowa/video/7301234567890123456",
		"https://vm.tiktok.com/ZMabc1234/":                                  "https://vm.tiktok.com/ZMabc1234/",
		"https://vt.tiktok.com/ZSxyz9876/":                                  "https://vt.tiktok.com/ZSxyz9876/",
		"vm.tiktok.com/ZMabc1234":                                           "https://vm.tiktok.com/ZMabc1234/",
		"https://www.tiktok.com/t/ZTRabcde1/":                               "https://www.tiktok.com/t/ZTRabcde1/",
		"  https://youtu.be/dQw4w9WgXcQ  ":                                  "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		"https://WWW.YOUTUBE.COM/watch?v=dQw4w9WgXcQ":                       "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ#comments":              "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
	}
	for in, want := range good {
		l, ok := parseLink(in)
		if !ok || l.URL != want {
			t.Errorf("parseLink(%q) = %q, %v; want %q", in, l.URL, ok, want)
		}
	}
	bad := []string{
		"https://www.youtube.com/watch?v=short",
		"https://www.youtube.com/@FlowaSpins",
		"https://www.youtube.com/playlist?list=PL123",
		"https://www.instagram.com/flowa.spins/",
		"https://www.instagram.com/stories/flowa/123/",
		"https://www.tiktok.com/@flowa.spins",
		"https://www.tiktok.com/@flowa/video/abc",
		"https://example.com/watch?v=dQw4w9WgXcQ",
		"https://youtube.com.evil.example/watch?v=dQw4w9WgXcQ",
		"ftp://youtu.be/dQw4w9WgXcQ",
		"https://user:pass@youtu.be/dQw4w9WgXcQ",
		"https://youtu.be:8443/dQw4w9WgXcQ",
		"javascript:alert(1)",
		"",
	}
	for _, in := range bad {
		if l, ok := parseLink(in); ok {
			t.Errorf("parseLink(%q) should be refused, got %v", in, l)
		}
	}
	if l, _ := parseLink("https://youtube.com/shorts/aBcDeFgHiJk"); l.Kind != "short" || l.Site != "youtube" || l.ID != "aBcDeFgHiJk" {
		t.Errorf("short: %+v", l)
	}
	if l, _ := parseLink("https://vm.tiktok.com/ZMabc1234/"); l.Kind != "shortlink" {
		t.Errorf("tiktok short link kind: %+v", l)
	}
}

func TestCSP(t *testing.T) {
	off, on := csp(false), csp(true)
	if strings.Contains(off, "frame-src") || !strings.HasPrefix(on, off) || strings.TrimPrefix(on, off) != "; frame-src https://www.youtube-nocookie.com" {
		t.Fatalf("policies differ by more than the YouTube frame:\n%s\n%s", off, on)
	}
}

func TestValidAvatar(t *testing.T) {
	raw, _ := os.ReadFile("web/avatar-def.json")
	var def struct {
		Defaults map[string]any `json:"defaults"`
	}
	json.Unmarshal(raw, &def)
	good, _ := json.Marshal(def.Defaults)
	if err := validAvatar(good); err != nil {
		t.Fatalf("the defaults must be valid: %v", err)
	}
	withProps := map[string]any{}
	json.Unmarshal(good, &withProps)
	withProps["props"] = map[string]any{"poi": "#e0533a"}
	b, _ := json.Marshal(withProps)
	if err := validAvatar(b); err != nil {
		t.Fatalf("a valid prop colour was refused: %v", err)
	}
	bad := []map[string]any{
		{"hair": "mohawk"}, {"skin": "#00ff00"}, {"topColor": "red"}, {"v": 2}, {"wings": "yes"},
		{"props": map[string]any{"poi": "#000000"}}, {"props": map[string]any{"drums": "#3987e5"}}, {"props": "blue"},
	}
	for _, change := range bad {
		c := map[string]any{}
		json.Unmarshal(good, &c)
		for k, v := range change {
			c[k] = v
		}
		b, _ := json.Marshal(c)
		if validAvatar(b) == nil {
			t.Errorf("%v should be refused", change)
		}
	}
	if validAvatar(json.RawMessage(`[]`)) == nil || validAvatar(json.RawMessage(`{"v":1,"note":"`+strings.Repeat("x", 3000)+`"}`)) == nil {
		t.Error("a list or an oversized object should be refused")
	}
	if validAvatar(json.RawMessage(`{}`)) != nil || validAvatar(json.RawMessage(`null`)) == nil {
		t.Error("an empty object removes the avatar; null is not an avatar")
	}
	if validAvatar(json.RawMessage(`{"v":1,"props":{"rope":"#3987e5"}}`)) == nil || validAvatar(json.RawMessage(`{"v":1,"props":{"poi":"#000000"}}`)) == nil {
		t.Error("an unknown prop or a colour off the prop palette should be refused")
	}
}
