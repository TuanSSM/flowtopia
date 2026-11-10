package main

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"os/exec"
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

// TestParseLink reads test/links-fixtures.json, the cases the server and the page (web/links.js,
// checked by node test/links.js) must agree on.
func TestParseLink(t *testing.T) {
	raw, err := os.ReadFile("test/links-fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var fx struct {
		Good  map[string]string    `json:"good"`
		Bad   []string             `json:"bad"`
		Kinds map[string][3]string `json:"kinds"`
	}
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatal(err)
	}
	if len(fx.Good) < 30 || len(fx.Bad) < 30 || len(fx.Kinds) < 4 {
		t.Fatalf("the fixtures look cut short: %d good, %d bad, %d kinds", len(fx.Good), len(fx.Bad), len(fx.Kinds))
	}
	for in, want := range fx.Good {
		l, ok := parseLink(in)
		if !ok || l.URL != want {
			t.Errorf("parseLink(%q) = %q, %v; want %q", in, l.URL, ok, want)
		}
		// A canonical link reads as itself.
		if again, ok := parseLink(want); !ok || again.URL != want {
			t.Errorf("parseLink(%q) = %q, %v; a canonical link must not change", want, again.URL, ok)
		}
	}
	for _, in := range fx.Bad {
		if l, ok := parseLink(in); ok {
			t.Errorf("parseLink(%q) should be refused, got %v", in, l)
		}
	}
	for in, want := range fx.Kinds {
		if l, _ := parseLink(in); l.Site != want[0] || l.Kind != want[1] || l.ID != want[2] {
			t.Errorf("parseLink(%q) = %+v; want site, kind, id %v", in, l, want)
		}
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

// The container is built FROM scratch, so the binary must carry root certificates itself. The system
// roots are read once per process, so the check runs in a second copy of the test binary that is told
// to look for certificates in an empty directory, which is what the container has.
func TestRootsPresent(t *testing.T) {
	if os.Getenv("FLOWA_ROOTS_CHILD") == "1" {
		if !rootsPresent() {
			t.Fatal("no root certificates with an empty system store: is x509roots/fallback still imported?")
		}
		// Two roots the link previews depend on: Let's Encrypt, and Google for YouTube.
		for _, name := range []string{"isrg-root-x1.pem", "gts-root-r1.pem"} {
			raw, err := os.ReadFile(filepath.Join("testdata", name))
			if err != nil {
				t.Fatal(err)
			}
			block, _ := pem.Decode(raw)
			if block == nil {
				t.Fatalf("%s: not a PEM file", name)
			}
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if _, err := cert.Verify(x509.VerifyOptions{}); err != nil {
				t.Errorf("%s is not trusted with an empty system store: %v", name, err)
			}
		}
		return
	}
	empty := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestRootsPresent$", "-test.v")
	cmd.Env = append(os.Environ(), "FLOWA_ROOTS_CHILD=1",
		"SSL_CERT_FILE="+filepath.Join(empty, "none.pem"), "SSL_CERT_DIR="+empty)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("with an empty certificate store: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "PASS: TestRootsPresent") {
		t.Fatalf("the check did not run in the second process:\n%s", out)
	}
}
