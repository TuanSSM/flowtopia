package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"testing/quick"
	"time"
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

func TestCleanName(t *testing.T) {
	const refused = "\x00"
	for in, want := range map[string]string{
		"Pat Spins":             "Pat Spins",
		"  Pat \t  Spins \n":    "Pat Spins",
		"Pat\u200b Spi\u2060ns": "Pat Spins", // zero-width space and word joiner go
		"\ufeffPat":             "Pat",       // a byte order mark from a paste
		"\u200b\u200b\u200b":    refused,     // nothing to see
		"\u200d\u200c":          refused,     // joiners with nothing to join
		"\u3164\u3164":          refused,     // Hangul fillers: letters to Unicode, blank to a reader
		"\u2800\u2800\u2800":    refused,     // blank braille
		"   ":                   refused,
		"":                      refused,
		"\U0001F525\U0001F525":  refused, // no letter or digit
		"...":                   refused,
		"\u202enimda":           refused,              // right-to-left override: reads as "admin"
		"Pat\u2066x\u2069":      refused,              // isolates
		"Pat\u200e Spins\u200f": "Pat Spins",          // plain direction marks are dropped
		"\u0639\u0644\u064a":    "\u0639\u0644\u064a", // Arabic needs no controls
		"\u0645\u06cc\u200c\u062e\u0648\u0627\u0647\u0645":                          "\u0645\u06cc\u200c\u062e\u0648\u0627\u0647\u0645",                          // Persian: the joiner between letters stays
		"Pat \U0001F468\u200d\U0001F469\u200d\U0001F467":                            "Pat \U0001F468\u200d\U0001F469\u200d\U0001F467",                            // a family emoji stays whole
		"Jo \U0001F3F4\U000E0067\U000E0062\U000E0073\U000E0063\U000E0074\U000E007F": "Jo \U0001F3F4\U000E0067\U000E0062\U000E0073\U000E0063\U000E0074\U000E007F", // the flag of Scotland
		"Jo\U000E0041\U000E0042":                                                    "Jo",                                                                        // tags with no flag before them carry hidden text
		"7":                                                                         "7",
		strings.Repeat("d", 40):                                                     strings.Repeat("d", 40),
		strings.Repeat("d", 41):                                                     refused,
		strings.Repeat("\u200b", 60) + "Pat":                                        "Pat", // counted after cleaning
		"Zo\u00eb":                                                                  "Zo\u00eb",
	} {
		got, msg := cleanName(in)
		if want == refused {
			if msg == "" {
				t.Errorf("cleanName(%q) = %q, want it refused", in, got)
			}
			continue
		}
		if msg != "" || got != want {
			t.Errorf("cleanName(%q) = %q, %q; want %q", in, got, msg, want)
		}
	}
	// The bio keeps its lines; what cannot be seen goes, and a direction override is refused.
	if got, ok := visibleText("\u200b\u200b\r\nPoi since 2019.\n\tStaff too.\x07"); !ok || strings.TrimSpace(got) != "Poi since 2019.\n Staff too." {
		t.Errorf("bio: %q, %v", got, ok)
	}
	if _, ok := visibleText("fine\u202e reversed"); ok {
		t.Error("a bio with a direction override must be refused")
	}
}

func TestPasswordPolicy(t *testing.T) {
	for _, c := range []struct{ pw, user, want string }{
		{"short", "pat", "8 to 200"},
		{strings.Repeat("x", 201), "pat", "8 to 200"},
		{"password", "pat", "most used"},
		{"PassWord1", "pat", "most used"},  // whatever the case
		{" iloveyou ", "pat", "most used"}, // spaces around it do not make it another password
		{"1q2w3e4r5t", "pat", "most used"},
		{"patricia.spins", "patricia.spins", "username"},
		{"Patricia.Spins", "patricia.spins", "username"},
		{"correct horse battery staple", "pat", ""},
		{"flowa-test-only", "admin", ""},
		{"patricia.spins1", "patricia.spins", ""},
	} {
		got := passwordProblem(c.pw, c.user)
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("passwordProblem(%q, %q) = %q, want it to mention %q", c.pw, c.user, got, c.want)
		}
	}
	// The list: 1,000 entries, each one that the length rule would let through, each lower case.
	words := strings.Fields(commonPasswordList)
	if len(words) != 1000 || len(commonPasswords) != 1000 {
		t.Fatalf("%d words, %d distinct; want 1000 of each", len(words), len(commonPasswords))
	}
	for _, w := range words {
		if len(w) < 8 || w != strings.ToLower(w) {
			t.Errorf("%q does not belong in the list: under 8 characters or not lower case", w)
		}
	}
	// New hashes get the full count; an older hash still checks, and says how many rounds it has.
	if pwRounds < 600000 {
		t.Errorf("pwRounds is %d, want at least 600000", pwRounds)
	}
	if h := hashPassword("a new password 9"); hashRounds(h) != pwRounds || !checkPassword(h, "a new password 9") || checkPassword(h, "a new password 8") {
		t.Errorf("a new hash: %d rounds", hashRounds(h))
	}
	if h := hashWith("an older password 7", 200000); hashRounds(h) != 200000 || !checkPassword(h, "an older password 7") {
		t.Errorf("an older hash: %d rounds", hashRounds(h))
	}
	if hashRounds("x") != 0 || hashRounds("md5$1$a$b") != 0 {
		t.Error("a hash that cannot be read has no round count")
	}
}

func TestHomeLink(t *testing.T) {
	for in, want := range map[string]string{
		"":                           "",
		"  ":                         "",
		" http://pi.local:8080/ ":    "http://pi.local:8080/",
		"https://home.example/start": "https://home.example/start",
	} {
		if got, err := homeLink(in); err != nil || got != want {
			t.Errorf("homeLink(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"pi.local:8080", "//pi.local/", "javascript:alert(1)", "ftp://pi.local/", "http://", "http://admin:secret@pi.local/", "/start"} {
		if got, err := homeLink(in); err == nil {
			t.Errorf("homeLink(%q) = %q, want it refused", in, got)
		}
	}
}

// The dashboard's count toward the fit of the suggestion weights: the number of pairs is the one
// sim/fit-suggest.js waits for, and the date is honest about what it knows.
func TestGate(t *testing.T) {
	src, err := os.ReadFile("sim/fit-suggest.js")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`MIN_PAIRS = (\d+)`).FindSubmatch(src)
	if m == nil || string(m[1]) != strconv.Itoa(gatePairs) {
		t.Fatalf("gatePairs is %d, but sim/fit-suggest.js has MIN_PAIRS = %s: the dashboard would count toward another number than the fit", gatePairs, m)
	}
	wed := time.Date(2026, 11, 11, 15, 30, 0, 0, time.UTC) // a Wednesday; its week began on Monday 9 November
	for _, c := range []struct {
		name          string
		pairs, recent int
		now           time.Time
		want          string
	}{
		{"enough pairs", 30, 0, wed, "now"},
		{"more than enough", 41, 7, wed, "now"},
		{"nothing marked lately: no date", 12, 0, wed, ""},
		{"none at all", 0, 0, wed, ""},
		{"a pair a week, eighteen missing", 12, 4, wed, "2027-03-15"},
		{"two a week, one missing: next week", 29, 8, wed, "2026-11-16"},
		{"one in four weeks, one missing: four weeks", 29, 1, wed, "2026-12-07"},
		{"three in four weeks, four missing: rounded up to six weeks", 26, 3, wed, "2026-12-21"},
		{"on a Monday the week is this one", 29, 4, time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC), "2026-11-16"},
		{"on a Sunday too", 29, 4, time.Date(2026, 11, 15, 23, 59, 0, 0, time.UTC), "2026-11-16"},
		{"across a new year", 20, 4, time.Date(2026, 12, 30, 12, 0, 0, 0, time.UTC), "2027-03-08"},
	} {
		if got := gateOpens(c.pairs, c.recent, c.now); got != c.want {
			t.Errorf("%s: gateOpens(%d, %d) = %q, want %q", c.name, c.pairs, c.recent, got, c.want)
		}
	}
}

// staticOf builds the table of static files from files given by name, without brotli copies.
func staticOf(t testing.TB, files map[string]string) *static {
	t.Helper()
	web := fstest.MapFS{}
	for name, body := range files {
		web[name] = &fstest.MapFile{Data: []byte(body)}
	}
	st, err := newStatic(web, nil)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// gunzip unpacks a gzip body, or fails the test.
func gunzip(t testing.TB, what string, b []byte) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("%s: not gzip: %v", what, err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("%s: gzip that does not unpack: %v", what, err)
	}
	return out
}

// sha1Name is the name sim/bake.js gives a clip with this JSON.
func sha1Name(body string) string {
	sum := sha1.Sum([]byte(body))
	return hex.EncodeToString(sum[:6])
}

// TestStaticHashed: the page's files answer under a name with the hash of their bytes in it, for good
// and compressed; index.html names them so and is asked for each time; a changed file gets a new name,
// and the name it had goes on answering, with the bytes it had, for an hour after the next build starts.
func TestStaticHashed(t *testing.T) {
	// Nothing listens on port 1: the feature flags cannot be read, so the page's CSP is the plain one.
	db, err := sql.Open("postgres", "postgres://x:x@127.0.0.1:1/x?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a := newApp(db, t.TempDir())
	a.hosts = []string{"example.com"} // the Host httptest gives requests
	const forGood = "public, max-age=31536000, immutable"
	const forNow = "private, max-age=300" // an old name: five minutes in the one browser, never for good
	ask := func(st *static, method, path string, hdr ...string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, nil)
		for i := 0; i+1 < len(hdr); i += 2 {
			r.Header.Set(hdr[i], hdr[i+1])
		}
		w := httptest.NewRecorder()
		a.routes(st).ServeHTTP(w, r)
		return w
	}
	// answers checks the status and the headers every file carries, and that the length said is the length sent.
	answers := func(what string, w *httptest.ResponseRecorder, status int, cache, encoding string) {
		t.Helper()
		h := w.Header()
		if w.Code != status || h.Get("Cache-Control") != cache || h.Get("Content-Encoding") != encoding {
			t.Fatalf("%s: status %d, Cache-Control %q, Content-Encoding %q; want %d, %q, %q", what, w.Code, h.Get("Cache-Control"), h.Get("Content-Encoding"), status, cache, encoding)
		}
		if h.Get("X-Content-Type-Options") != "nosniff" || h.Get("X-Frame-Options") != "DENY" || h.Get("Vary") != "Accept-Encoding" || h.Get("ETag") == "" {
			t.Fatalf("%s: headers %v; want nosniff, no framing, Vary: Accept-Encoding and an ETag", what, h)
		}
		if status == http.StatusOK && h.Get("Content-Length") != strconv.Itoa(w.Body.Len()) {
			t.Fatalf("%s: Content-Length %q for a body of %d bytes", what, h.Get("Content-Length"), w.Body.Len())
		}
	}
	gone := func(what string, w *httptest.ResponseRecorder) {
		t.Helper()
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s: status %d with %d bytes, want 404", what, w.Code, w.Body.Len())
		}
	}
	refs := func(page []byte) (out []string) {
		for _, m := range regexp.MustCompile(`\b(?:src|href)="([^"]*)"`).FindAllSubmatch(page, -1) {
			out = append(out, string(m[1]))
		}
		return out
	}
	hash16 := func(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:8]) }

	// A page that names four files, and files it does not name, with brotli copies named as brotli.js
	// names them. The server never unpacks brotli, so any bytes stand in for it here; node brotli.js
	// --check is what shows that the real copies unpack to their files.
	long := func(s string) string { return strings.Repeat(s+"\n", 40) }
	page := `<!doctype html><link rel="icon" href="icon.svg"><link rel="manifest" href="manifest.webmanifest">` +
		`<link rel="stylesheet" href="app.css"><script src="app.js"></script><a href="https://example.org/">out</a>`
	clip := long(`{"clip":1}`)
	files := func(appJS string) fstest.MapFS {
		return fstest.MapFS{
			"index.html":                        {Data: []byte(page)},
			"app.js":                            {Data: []byte(appJS)},
			"app.css":                           {Data: []byte(long("main { color: red }"))},
			"icon.svg":                          {Data: []byte(long(`<svg xmlns="http://www.w3.org/2000/svg"/>`))},
			"manifest.webmanifest":              {Data: []byte(`{"name":"Flowa"}`)},
			"tiny.js":                           {Data: []byte("1")},
			"photo.png":                         {Data: []byte(long("named like a picture"))},
			"clips/index.json":                  {Data: []byte(long(`{"a":1}`))},
			"clips/" + sha1Name(clip) + ".json": {Data: []byte(clip)},
			"clips/0123456789ab.json":           {Data: []byte(long(`{"clip":2}`))},
			"catalog/poi.json":                  {Data: []byte(`{}`)},
		}
	}
	js1, js2 := long("let build = 1"), long("let build = 2")
	web1, web2 := files(js1), files(js2)
	brotli := fstest.MapFS{
		"app.js." + hash16([]byte(js1)) + ".br":               {Data: []byte("brotli of build 1")},
		"app.js." + hash16([]byte(js2)) + ".br":               {Data: []byte("brotli of build 2")},
		"app.css.0000000000000000.br":                         {Data: []byte("brotli of an app.css that was")},
		"photo.png." + hash16(web1["photo.png"].Data) + ".br": {Data: []byte("brotli of a picture")},
		"tiny.js." + hash16([]byte("1")) + ".br":              {Data: []byte("longer than the file")},
		"index.html." + hash16([]byte(page)) + ".br":          {Data: []byte("brotli of the page as it is in web/")},
	}
	build := func(web, br fs.FS) *static {
		t.Helper()
		st, err := newStatic(web, br)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	st1 := build(web1, brotli)
	name := func(st *static, plain string) string {
		t.Helper()
		f, ok := st.plain["/"+plain]
		if !ok {
			t.Fatalf("no file %s in the table", plain)
		}
		return "/" + hashedName(plain, f.hash)
	}

	t.Run("the page names its files by their hash", func(t *testing.T) {
		w := ask(st1, "GET", "/")
		answers("the page", w, 200, "no-cache", "")
		if ct := w.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Fatalf("the page is %q", ct)
		}
		if w.Header().Get("Content-Security-Policy") != csp(false) {
			t.Fatalf("the page's CSP is %q", w.Header().Get("Content-Security-Policy"))
		}
		want := []string{name(st1, "icon.svg")[1:], "manifest.webmanifest", name(st1, "app.css")[1:], name(st1, "app.js")[1:], "https://example.org/"}
		if got := refs(w.Body.Bytes()); !reflect.DeepEqual(got, want) {
			t.Fatalf("the page names %v, want %v", got, want)
		}
		if again := ask(st1, "GET", "/index.html"); !bytes.Equal(again.Body.Bytes(), w.Body.Bytes()) || again.Header().Get("Cache-Control") != "no-cache" {
			t.Fatal("/index.html is not the page / is")
		}
		if !regexp.MustCompile(`^/app\.[0-9a-f]{16}\.js$`).MatchString(name(st1, "app.js")) {
			t.Fatalf("the hashed name of app.js is %s", name(st1, "app.js"))
		}
		for plain, ctype := range map[string]string{"app.js": "text/javascript; charset=utf-8", "app.css": "text/css; charset=utf-8", "icon.svg": "image/svg+xml"} {
			w := ask(st1, "GET", name(st1, plain))
			answers(plain+" by its hashed name", w, 200, forGood, "")
			if w.Header().Get("Content-Type") != ctype || !bytes.Equal(w.Body.Bytes(), web1[plain].Data) {
				t.Fatalf("%s by its hashed name: type %q, %d bytes", plain, w.Header().Get("Content-Type"), w.Body.Len())
			}
		}
		// The manifest keeps its address, and is asked for each time like the page.
		w = ask(st1, "GET", "/manifest.webmanifest")
		answers("the manifest", w, 200, "no-cache", "")
		if w.Header().Get("Content-Type") != "application/manifest+json" {
			t.Fatalf("the manifest is %q", w.Header().Get("Content-Type"))
		}
	})

	t.Run("brotli, then gzip, then the file as it is", func(t *testing.T) {
		app := name(st1, "app.js")
		w := ask(st1, "GET", app, "Accept-Encoding", "gzip, deflate, br, zstd")
		answers("a browser that takes brotli", w, 200, forGood, "br")
		if w.Body.String() != "brotli of build 1" {
			t.Fatalf("the brotli sent is %q, not the copy the build made of this file", w.Body.String())
		}
		w = ask(st1, "GET", app, "Accept-Encoding", "gzip, deflate")
		answers("a browser that takes gzip", w, 200, forGood, "gzip")
		if !bytes.Equal(gunzip(t, "app.js", w.Body.Bytes()), []byte(js1)) || w.Body.Len() >= len(js1) {
			t.Fatal("the gzip sent does not unpack to the file, or is no smaller")
		}
		for _, c := range []struct{ accept, encoding string }{
			{"", ""}, {"identity", ""}, {"gzip;q=0", ""}, {"gzip; q=0.0, br;q=0", ""}, {"*;q=0", ""}, {"deflate, zstd", ""},
			{"gzip;q=0, br", "br"}, {"br;q=0, gzip", "gzip"}, {"GZIP", "gzip"}, {"x-gzip", "gzip"}, {"*", "br"}, {"br;q=0, *", "gzip"},
			{"gzip;q=0.001", "gzip"}, {"gzip;q=nonsense", ""}, {" br ; Q=0.5 , gzip", "br"},
		} {
			var hdr []string
			if c.accept != "" {
				hdr = []string{"Accept-Encoding", c.accept}
			}
			w := ask(st1, "GET", app, hdr...)
			answers("Accept-Encoding: "+c.accept, w, 200, forGood, c.encoding)
			if c.encoding == "" && w.Body.String() != js1 {
				t.Fatalf("Accept-Encoding: %s: the body is not the file", c.accept)
			}
		}
		// Each form has its own ETag.
		tags := map[string]bool{}
		for _, accept := range []string{"identity", "gzip", "br"} {
			tags[ask(st1, "GET", app, "Accept-Encoding", accept).Header().Get("ETag")] = true
		}
		if len(tags) != 3 {
			t.Fatalf("the three forms share an ETag: %v", tags)
		}
		// A brotli copy of other bytes, of a kind that is compressed already, or bigger than the file is not used.
		for plain, encoding := range map[string]string{"app.css": "gzip", "photo.png": "", "tiny.js": "", "clips/index.json": "gzip"} {
			answers(plain+" with brotli asked for", ask(st1, "GET", "/"+plain, "Accept-Encoding", "br, gzip"), 200, "no-cache", encoding)
		}
		if w := ask(st1, "GET", "/", "Accept-Encoding", "br"); w.Header().Get("Content-Encoding") != "" {
			t.Fatal("the page was sent as the brotli of the page before its names were written in")
		}
		// The build after has brotli for its own app.js and never the other build's.
		st2 := build(web2, brotli)
		if w := ask(st2, "GET", name(st2, "app.js"), "Accept-Encoding", "br"); w.Body.String() != "brotli of build 2" {
			t.Fatalf("the second build sent %q", w.Body.String())
		}
	})

	t.Run("HEAD, Range and a browser that asks again", func(t *testing.T) {
		app := name(st1, "app.js")
		for _, accept := range []string{"identity", "gzip", "br"} {
			full := ask(st1, "GET", app, "Accept-Encoding", accept)
			head := ask(st1, "HEAD", app, "Accept-Encoding", accept)
			if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("Content-Length") != strconv.Itoa(full.Body.Len()) || head.Header().Get("ETag") != full.Header().Get("ETag") {
				t.Fatalf("HEAD with %s: status %d, %d bytes of body, Content-Length %q; GET sent %d bytes", accept, head.Code, head.Body.Len(), head.Header().Get("Content-Length"), full.Body.Len())
			}
			// Under the plain name, asked again with the ETag: not sent again.
			first := ask(st1, "GET", "/app.js", "Accept-Encoding", accept)
			answers("app.js by its own name, "+accept, first, 200, "no-cache", map[string]string{"identity": "", "gzip": "gzip", "br": "br"}[accept])
			again := ask(st1, "GET", "/app.js", "Accept-Encoding", accept, "If-None-Match", first.Header().Get("ETag"))
			answers("app.js asked again, "+accept, again, 304, "no-cache", "")
			if again.Body.Len() != 0 {
				t.Fatalf("a 304 with a body of %d bytes", again.Body.Len())
			}
			if other := ask(st1, "GET", "/app.js", "Accept-Encoding", accept, "If-None-Match", `"0000000000000000"`); other.Code != 200 {
				t.Fatalf("an ETag that is not the file's got %d", other.Code)
			}
			if list := ask(st1, "GET", "/app.js", "Accept-Encoding", accept, "If-None-Match", `"x", W/`+first.Header().Get("ETag")); list.Code != 304 {
				t.Fatalf("the file's ETag in a list got %d", list.Code)
			}
		}
		// A part of the file is a part of the file as it is, whatever the browser would take.
		w := ask(st1, "GET", app, "Range", "bytes=4-8", "Accept-Encoding", "gzip, br")
		if w.Code != http.StatusPartialContent || w.Body.String() != js1[4:9] || w.Header().Get("Content-Encoding") != "" ||
			w.Header().Get("Content-Range") != "bytes 4-8/"+strconv.Itoa(len(js1)) || w.Header().Get("Content-Length") != "5" {
			t.Fatalf("Range: status %d, body %q, headers %v", w.Code, w.Body.String(), w.Header())
		}
		if w := ask(st1, "GET", app, "Range", "bytes=999999-"); w.Code != http.StatusRequestedRangeNotSatisfiable {
			t.Fatalf("a range past the end got %d", w.Code)
		}
		if w := ask(st1, "GET", app, "Range", "bytes=0-3", "If-Range", `"another"`); w.Code != 200 || w.Body.String() != js1 {
			t.Fatalf("a range of a file that changed got %d and %d bytes, want the whole file", w.Code, w.Body.Len())
		}
		if w := ask(st1, "POST", app, "Content-Type", "application/json", "Origin", "http://example.com"); w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("POST to a file got %d", w.Code)
		}
	})

	t.Run("a clip named by its own hash is kept for good", func(t *testing.T) {
		answers("a clip whose name is its hash", ask(st1, "GET", "/clips/"+sha1Name(clip)+".json", "Accept-Encoding", "gzip"), 200, forGood, "gzip")
		answers("a clip whose name is not its hash", ask(st1, "GET", "/clips/0123456789ab.json"), 200, "no-cache", "")
		answers("the list of clips", ask(st1, "GET", "/clips/index.json"), 200, "no-cache", "")
	})

	t.Run("what is not a file is not answered", func(t *testing.T) {
		app := name(st1, "app.js")
		for _, p := range []string{"/app.0000000000000000.js", "/app..js", "/app.js.br", app + ".br", "/app.js." + hash16([]byte(js1)) + ".br",
			"/clips/", "/clips", "/.gitignore", "/brotli/.gitignore", "/catalog/", "/index." + st1.build + ".html", "/web/app.js", "/nothing.js",
			strings.Replace(app, ".js", ".css", 1), strings.ToUpper(app)} {
			gone(p, ask(st1, "GET", p))
		}
		// A path that climbs out is cleaned by the router before it is looked up, and names no file then.
		for _, p := range []string{"/../main.go", "/clips/../../main.go", "/clips/%2e%2e/%2e%2e/main.go", "//app.js/..", "/..%2fmain.go"} {
			w := ask(st1, "GET", p)
			if loc := w.Header().Get("Location"); loc != "" && w.Code >= 300 && w.Code < 400 {
				w = ask(st1, "GET", loc)
			}
			if w.Code == 200 && !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
				t.Fatalf("%s was answered with a file of type %q", p, w.Header().Get("Content-Type"))
			}
			if strings.Contains(w.Body.String(), "package main") {
				t.Fatalf("%s sent the source", p)
			}
		}
	})

	t.Run("a changed file gets a new name", func(t *testing.T) {
		st2 := build(web2, nil)
		if name(st1, "app.js") == name(st2, "app.js") {
			t.Fatalf("app.js changed and is still %s", name(st2, "app.js"))
		}
		for _, same := range []string{"app.css", "icon.svg", "clips/index.json"} {
			if name(st1, same) != name(st2, same) {
				t.Fatalf("%s did not change and went from %s to %s", same, name(st1, same), name(st2, same))
			}
		}
		p1, p2 := ask(st1, "GET", "/"), ask(st2, "GET", "/")
		if p1.Header().Get("ETag") == p2.Header().Get("ETag") || st1.build == st2.build {
			t.Fatal("the page names another app.js and has the ETag it had")
		}
		if got := refs(p2.Body.Bytes()); !slices.Contains(got, name(st2, "app.js")[1:]) || slices.Contains(got, name(st1, "app.js")[1:]) {
			t.Fatalf("the second build's page names %v", got)
		}
		// A browser that has the first page is told it changed.
		if w := ask(st2, "GET", "/", "If-None-Match", p1.Header().Get("ETag")); w.Code != 200 {
			t.Fatalf("the first page's ETag got %d from the second build", w.Code)
		}
		// A file the page does not name changes no name the page holds, so the page is the same page.
		web3 := files(js1)
		web3["clips/index.json"] = &fstest.MapFile{Data: []byte(long(`{"a":2}`))}
		st3 := build(web3, nil)
		if st3.build != st1.build || name(st3, "clips/index.json") == name(st1, "clips/index.json") {
			t.Fatal("a change to a file the page does not name changed the page, or did not change the file's name")
		}
		// The same files give the same table.
		if again := build(web1, brotli); again.build != st1.build || !reflect.DeepEqual(again.told, st1.told) {
			t.Fatal("the same files built twice gave two different pages")
		}
	})

	t.Run("the old name serves for an hour", func(t *testing.T) {
		dir := t.TempDir()
		now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
		// start is a build starting on the data folder: its table, and how many old names it answers.
		start := func(web fs.FS) (*static, int) {
			t.Helper()
			st := build(web, nil)
			st.now = func() time.Time { return now }
			n, err := st.keepOld(dir)
			if err != nil {
				t.Fatal(err)
			}
			return st, n
		}
		first, n := start(web1)
		if n != 0 {
			t.Fatalf("the first build on a data folder answers %d old names", n)
		}
		old := name(first, "app.js")
		now = now.Add(24 * time.Hour)
		second, n := start(web2)
		if n != 1 {
			t.Fatalf("the second build answers %d old names, want 1: only app.js changed", n)
		}
		// The old name gets the old file, not the new one: an old page's scripts belong together.
		w := ask(second, "GET", old)
		answers("the old name, just after the deploy", w, 200, forNow, "")
		if w.Body.String() != js1 || w.Header().Get("Content-Type") != "text/javascript; charset=utf-8" {
			t.Fatalf("the old name sent %d bytes of type %q, want the old file", w.Body.Len(), w.Header().Get("Content-Type"))
		}
		// It did not come out of the binary, so it is not kept for good, by no shared cache, and it
		// carries the CSP of an API answer, which allows nothing, not the page's.
		if cc := w.Header().Get("Cache-Control"); strings.Contains(cc, "immutable") || strings.Contains(cc, "public") {
			t.Fatalf("the old name is sent with Cache-Control %q", cc)
		}
		if got := w.Header().Get("Content-Security-Policy"); got != "default-src 'none'; frame-ancestors 'none'" {
			t.Fatalf("the old name is sent with the CSP %q", got)
		}
		w = ask(second, "GET", old, "Accept-Encoding", "gzip")
		answers("the old name, compressed", w, 200, forNow, "gzip")
		if !bytes.Equal(gunzip(t, "the old app.js", w.Body.Bytes()), []byte(js1)) {
			t.Fatal("the old name's gzip does not unpack to the old file")
		}
		if w := ask(second, "GET", name(second, "app.js")); w.Body.String() != js2 {
			t.Fatal("the new name does not send the new file")
		}
		answers("a name both builds have", ask(second, "GET", name(first, "app.css")), 200, forGood, "")
		gone("a hash no build had", ask(second, "GET", "/app.0000000000000000.js"))

		// A restart half an hour in does not start the hour again.
		now = now.Add(30 * time.Minute)
		if second, n = start(web2); n != 1 {
			t.Fatalf("after a restart the second build answers %d old names, want 1", n)
		}
		now = now.Add(29 * time.Minute)
		answers("the old name after 59 minutes", ask(second, "GET", old), 200, forNow, "")
		now = now.Add(2 * time.Minute)
		gone("the old name after 61 minutes", ask(second, "GET", old))
		answers("the new name after 61 minutes", ask(second, "GET", name(second, "app.js")), 200, forGood, "")
		if second, n = start(web2); n != 0 {
			t.Fatalf("a start after the hour still answers %d old names", n)
		}
		gone("the old name after a start past the hour", ask(second, "GET", old))

		// Going back (deploy.sh --rollback) is a deploy like any other: the build that ran is the old one now.
		now = now.Add(24 * time.Hour)
		back, n := start(web1)
		if n != 1 {
			t.Fatalf("after going back, %d old names are answered, want 1", n)
		}
		if w := ask(back, "GET", name(second, "app.js")); w.Code != 200 || w.Body.String() != js2 {
			t.Fatalf("after going back, the name of the build that ran got %d", w.Code)
		}
		if w := ask(back, "GET", old); w.Code != 200 || w.Body.String() != js1 {
			t.Fatalf("after going back, the build's own name got %d", w.Code)
		}

		// Five deploys ten minutes apart: the pages of the three before the last still get their files.
		now = now.Add(24 * time.Hour)
		var names []string
		for i := 3; i <= 6; i++ {
			st, _ := start(files(long(fmt.Sprintf("let build = %d", i))))
			names = append(names, name(st, "app.js"))
			now = now.Add(10 * time.Minute)
		}
		last, n := start(files(long("let build = 7")))
		if n != keptBuilds {
			t.Fatalf("after five deploys in under an hour, %d old names are answered, want the %d the record keeps", n, keptBuilds)
		}
		for i, p := range names {
			if w := ask(last, "GET", p); (w.Code == 200) != (i > 0) {
				t.Fatalf("the name of deploy %d of 5 got %d", i+1, w.Code)
			}
		}

		// What a build writes for the next one is the scripts and the style sheet its page names, not the icon.
		written, err := readKept(filepath.Join(dir, keptName))
		if err != nil || len(written) == 0 {
			t.Fatalf("the record cannot be read back: %v", err)
		}
		for _, b := range written {
			if len(b.Files) != 2 {
				t.Fatalf("the record of build %s holds %d files, want its app.js and its app.css", b.Build, len(b.Files))
			}
			for f := range b.Files {
				if !strings.HasSuffix(f, ".js") && !strings.HasSuffix(f, ".css") {
					t.Fatalf("the record holds %s", f)
				}
			}
		}

		// The record is a file in the data folder. What it says is checked, not believed: a file whose
		// bytes do not hash to its name is not sent, nothing but a script or a style sheet is taken from
		// it (a page, or a picture that is a document, could run a script from the app's own address),
		// and no name is answered for longer than the hour.
		now = now.Add(24 * time.Hour)
		planted := "/evil." + hash16([]byte("something else")) + ".js"
		honest := "/app." + hash16([]byte("let old = 1")) + ".js"
		html := []byte(`<script src="` + honest[1:] + `"></script>`)
		svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script href="` + honest[1:] + `"/></svg>`)
		record := []keptBuild{{Build: "0123456789abcdef", Until: now.Add(1000 * time.Hour), Files: map[string][]byte{
			planted[1:]: []byte("alert(1)"), honest[1:]: []byte("let old = 1"), "../../etc/passwd": []byte("x"), "app.js": []byte("let planted = 1"),
			"x." + hash16(html) + ".html": html, "icon." + hash16(svg) + ".svg": svg, "x." + hash16(html): html, "x." + hash16(html) + ".js.html": html}}}
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		if err := json.NewEncoder(zw).Encode(record); err != nil {
			t.Fatal(err)
		}
		zw.Close()
		if err := os.WriteFile(filepath.Join(dir, keptName), buf.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		st, n := start(web1)
		if n != 1 {
			t.Fatalf("of a record with one honest file, %d are answered", n)
		}
		gone("a file under a name its bytes do not hash to", ask(st, "GET", planted))
		gone("a page under the name its bytes hash to", ask(st, "GET", "/x."+hash16(html)+".html"))
		gone("a picture under the name its bytes hash to", ask(st, "GET", "/icon."+hash16(svg)+".svg"))
		gone("a page under a name with no kind", ask(st, "GET", "/x."+hash16(html)))
		gone("a page under a name that only has .js in it", ask(st, "GET", "/x."+hash16(html)+".js.html"))
		answers("a script under the name its bytes hash to", ask(st, "GET", honest), 200, forNow, "")
		if w := ask(st, "GET", "/app.js"); w.Body.String() != js1 {
			t.Fatal("the record replaced a file of the build")
		}
		now = now.Add(61 * time.Minute)
		gone("a record that says a thousand hours, after 61 minutes", ask(st, "GET", honest))

		// A record that cannot be read costs the old names and nothing else, and is written anew.
		if err := os.WriteFile(filepath.Join(dir, keptName), []byte("not gzip"), 0o600); err != nil {
			t.Fatal(err)
		}
		st = build(web1, nil)
		if n, err := st.keepOld(dir); err == nil || n != 0 {
			t.Fatalf("a record that is not gzip: %d names, error %v", n, err)
		}
		answers("the page, with a record that cannot be read", ask(st, "GET", "/"), 200, "no-cache", "")
		if _, n := start(web2); n != 1 {
			t.Fatalf("the build after a record that could not be read answers %d old names, want 1", n)
		}
		// A data folder that cannot be written to: the same.
		st = build(web1, nil)
		if _, err := st.keepOld(filepath.Join(dir, "missing")); err == nil {
			t.Fatal("a data folder that is not there gave no error")
		}
		answers("the page, with no data folder", ask(st, "GET", "/"), 200, "no-cache", "")
	})

	// The files compiled into this binary: what a browser gets from a deployed Flowa.
	t.Run("the files compiled in", func(t *testing.T) {
		web, _ := fs.Sub(webFS, "web")
		br, _ := fs.Sub(brotliFS, "brotli")
		st := build(web, br)
		raw := func(plain string) []byte {
			t.Helper()
			b, err := webFS.ReadFile("web/" + plain)
			if err != nil {
				t.Fatal(err)
			}
			return b
		}
		pageW := ask(st, "GET", "/", "Accept-Encoding", "gzip")
		answers("the page", pageW, 200, "no-cache", "gzip")
		served := gunzip(t, "the page", pageW.Body.Bytes())
		embedded, got := refs(raw("index.html")), refs(served)
		if len(got) != len(embedded) || len(got) < 10 {
			t.Fatalf("the page as served names %d files, the page in web/ %d", len(got), len(embedded))
		}
		hashed := 0
		for i, ref := range got {
			plain := embedded[i]
			if plain == "manifest.webmanifest" {
				if ref != plain {
					t.Fatalf("the manifest is named %s; it keeps its address", ref)
				}
				continue
			}
			if strings.HasPrefix(plain, "#") { // a place on the page (the link that skips the menu), not a file
				if ref != plain {
					t.Fatalf("the link to %s was rewritten as %s; a place on the page keeps its name", plain, ref)
				}
				continue
			}
			// Every script, the style sheet and the icon: named by hash, and each form of it is the file.
			if ref != hashedName(plain, hash16(raw(plain))) {
				t.Fatalf("the page names %s as %s, want its hashed name: write it as a plain relative path in index.html", plain, ref)
			}
			hashed++
			f := st.plain["/"+plain]
			w := ask(st, "GET", "/"+ref)
			answers(ref, w, 200, forGood, "")
			if !bytes.Equal(w.Body.Bytes(), raw(plain)) || w.Header().Get("Content-Type") != staticTypes[path.Ext(plain)] {
				t.Fatalf("%s is not %s, or has the type %q", ref, plain, w.Header().Get("Content-Type"))
			}
			if f.gz != nil {
				w = ask(st, "GET", "/"+ref, "Accept-Encoding", "gzip")
				answers(ref+" with gzip", w, 200, forGood, "gzip")
				if !bytes.Equal(gunzip(t, ref, w.Body.Bytes()), raw(plain)) {
					t.Fatalf("the gzip of %s does not unpack to %s", ref, plain)
				}
			}
			// With brotli asked for: the build's copy when it made one, else what gzip gives.
			w = ask(st, "GET", "/"+ref, "Accept-Encoding", "gzip, br")
			switch {
			case f.br != nil:
				answers(ref+" with brotli", w, 200, forGood, "br")
				if !bytes.Equal(w.Body.Bytes(), f.br) {
					t.Fatalf("the brotli of %s is not the copy the build made", ref)
				}
			case f.gz != nil:
				answers(ref+" with brotli asked for and none made", w, 200, forGood, "gzip")
			default:
				answers(ref+" with brotli asked for and nothing smaller", w, 200, forGood, "")
			}
		}
		if hashed != len(st.told) || hashed < 10 {
			t.Fatalf("%d names in the page are hashed; the table says it told %d", hashed, len(st.told))
		}
		// Every file under its own name: sent once, then 304. The clips sim/bake.js wrote are named by
		// the SHA-1 of their bytes and kept for good; if this fails, bake.js names them another way now
		// and selfNamed in main.go must follow.
		clips, plainNames := 0, 0
		err := fs.WalkDir(web, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || strings.HasPrefix(p, "catalog/") {
				return err
			}
			plainNames++
			cache := "no-cache"
			if strings.HasPrefix(p, "clips/") && !slices.Contains([]string{"clips/index.json", "clips/features.json", "clips/presets.json"}, p) {
				cache = forGood
				clips++
			}
			want := raw(p)
			if p == "index.html" {
				want = served
			}
			w := ask(st, "GET", "/"+p)
			answers(p, w, 200, cache, "")
			if !bytes.Equal(w.Body.Bytes(), want) {
				t.Fatalf("/%s is not the file", p)
			}
			if again := ask(st, "GET", "/"+p, "If-None-Match", w.Header().Get("ETag")); again.Code != 304 || again.Body.Len() != 0 {
				t.Fatalf("/%s asked again with its ETag got %d and %d bytes", p, again.Code, again.Body.Len())
			}
			if st.plain["/"+p].gz != nil {
				z := ask(st, "GET", "/"+p, "Accept-Encoding", "gzip")
				answers(p+" with gzip", z, 200, cache, "gzip")
				if !bytes.Equal(gunzip(t, p, z.Body.Bytes()), want) {
					t.Fatalf("the gzip of /%s does not unpack to the file", p)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if clips == 0 || clips != len(st.fixed) || plainNames != len(st.plain) {
			t.Fatalf("%d clips, %d of them kept for good; %d files, %d in the table", clips, len(st.fixed), plainNames, len(st.plain))
		}
		// The 3D figure's model is the largest file: it must be worth compressing, and say what it is.
		if f := st.plain["/assets/human.glb"]; f == nil || f.gz == nil || f.ctype != "model/gltf-binary" {
			t.Fatal("assets/human.glb is not in the table with a gzip copy and its type")
		}
		t.Logf("%d files, %d KB as they are, %d KB with gzip, %d KB with brotli (%d files have a brotli copy)", len(st.plain), st.saved[0]/1000, st.saved[1]/1000, st.saved[2]/1000, st.nBr)

		// Brotli is made by brotli.js before a release is compiled, not in a plain checkout. CI makes
		// it and sets FLOWA_WANT_BROTLI, so that there this cannot be skipped.
		t.Run("brotli from the build", func(t *testing.T) {
			if st.nBr == 0 {
				if os.Getenv("FLOWA_WANT_BROTLI") != "" {
					t.Fatal("FLOWA_WANT_BROTLI is set and no file has a brotli copy: run node brotli.js before go test")
				}
				t.Skip("this build has no brotli copies (node brotli.js makes them); gzip is what it serves")
			}
			// Some copies and not all: web/ was edited since node brotli.js ran, as in any checkout that
			// built once. Those files are sent as gzip, which is right, so that is said and not failed,
			// except where FLOWA_WANT_BROTLI says the copies were just made.
			problems := staleBrotli(st)
			for _, p := range problems {
				if os.Getenv("FLOWA_WANT_BROTLI") != "" {
					t.Errorf("%s; run node brotli.js", p)
				} else {
					t.Logf("%s (sent as gzip; node brotli.js makes the copies again)", p)
				}
			}
			t.Logf("%d of %d files have a brotli copy; %d that could have one do not", st.nBr, len(st.plain), len(problems))
		})
	})

	// What "brotli from the build" reports, on a build whose copies are known: one for app.js, one of
	// an app.css that was, none for the rest.
	t.Run("a file changed since its brotli copy was made is found", func(t *testing.T) {
		got := staleBrotli(st1)
		for _, want := range []string{"/app.css", "/icon.svg", "/clips/index.json"} {
			if !slices.ContainsFunc(got, func(p string) bool { return strings.HasPrefix(p, want+" ") }) {
				t.Errorf("%s has a gzip copy and no brotli copy, and is not reported: %v", want, got)
			}
		}
		for _, p := range got {
			for _, not := range []string{"/app.js ", "/index.html ", "/photo.png ", "/tiny.js "} {
				if strings.HasPrefix(p, not) {
					t.Errorf("reported, though nothing is wrong with it: %s", p)
				}
			}
		}
	})
}

// staleBrotli lists what is wrong with a build's brotli copies, one line per file: a file that gzip
// makes smaller and that has no brotli copy (it changed since node brotli.js ran), or a copy that is
// larger than the gzip. index.html never has one: the server rewrites it.
func staleBrotli(st *static) (out []string) {
	for p, f := range st.plain {
		switch {
		case p == "/index.html" || f.gz == nil:
		case f.br == nil:
			out = append(out, p+" has a gzip copy and no brotli copy")
		case len(f.br) > len(f.gz):
			out = append(out, fmt.Sprintf("%s has a brotli copy of %d bytes, larger than its gzip of %d bytes", p, len(f.br), len(f.gz)))
		}
	}
	slices.Sort(out)
	return out
}

// FLOWA_MODE: unset is local, capitals and spaces are forgiven, an unknown word names the three, and
// a mode that is planned and not built stops the start as an unknown word does.
func TestInstanceMode(t *testing.T) {
	for _, v := range []string{"", " ", "\t\n", "local", "LOCAL", "Local", "  local\n", "\tLocal "} {
		if m, err := parseMode(v); err != nil || m != modeLocal {
			t.Errorf("parseMode(%q) = %q, %v; want local", v, m, err)
		}
	}
	// Single-profile mode is built; federated is planned and not built yet.
	for _, v := range []string{"single", " Single", "SINGLE", "SİNGLE", "\tsingle\n"} {
		if m, err := parseMode(v); err != nil || m != modeSingle {
			t.Errorf("parseMode(%q) = %q, %v; want single", v, m, err)
		}
	}
	if !modesBuilt[modeSingle] || modesBuilt[modeFederated] || len(modesBuilt) != 2 {
		t.Errorf("modesBuilt = %v; want local and single, not federated", modesBuilt)
	}
	// The modes of the plan are known words: the message says one that is not built is not, and
	// does not tell the operator the word is wrong. The capital I with a dot of a Turkish keyboard
	// lower-cases to a plain i, so SINGLE typed there is the word too.
	for _, v := range []string{"federated", "single", " Single", "FEDERATED\n", "SINGLE", "S\u0130NGLE"} {
		m, err := parseMode(v)
		if modesBuilt[instanceMode(strings.ToLower(strings.TrimSpace(v)))] {
			if err != nil {
				t.Errorf("parseMode(%q): %v, for a mode that is built", v, err)
			}
			continue
		}
		if err == nil || m != "" || !strings.Contains(err.Error(), "not built") || strings.Contains(err.Error(), "must be") {
			t.Errorf("parseMode(%q) = %q, %v; want an error that says the mode is not built yet", v, m, err)
		}
	}
	// Anything else is refused by name. Among them: two words, a word with something inside it, a
	// number, a look-alike (a Cyrillic a, an i without its dot), a NUL, a zero-width space (which
	// is not white space), and the quotes and semicolons of a setting copied from a shell.
	for _, v := range []string{"fed", "locale", "localx", "on", "local,single", "local single", "lo cal", "0", "true", "off", "none", "federate", "loc\u0430l", "s\u0131ngle", "local\x00", "\u200blocal", "'local'", `"local"`, "local;", "LOCAL=1"} {
		m, err := parseMode(v)
		if err == nil || m != "" {
			t.Errorf("parseMode(%q) = %q, nil; want an error", v, m)
			continue
		}
		for _, name := range []string{"local", "federated", "single"} {
			if !strings.Contains(err.Error(), name) {
				t.Errorf("parseMode(%q): %q does not name %s", v, err, name)
			}
		}
	}
	if m, err := parseMode("local\u00a0"); err != nil || m != modeLocal {
		t.Errorf("a non-breaking space after the word: %q, %v; want local", m, err)
	}
	// A long value is cut in the message, so a file pasted into the setting does not fill the log.
	long := strings.Repeat("x", 5000)
	if _, err := parseMode(long); err == nil || len(err.Error()) > 200 {
		t.Errorf("a 5000 character value: error of %d characters, want one under 200", len(fmt.Sprint(err)))
	}
	// Every mode the plan names is one parseMode knows, and local is always built.
	if !modesBuilt[modeLocal] {
		t.Error("local is not among the modes built")
	}
	for m := range modesBuilt {
		if m != modeLocal && m != modeFederated && m != modeSingle {
			t.Errorf("modesBuilt names %q, which parseMode does not know", m)
		}
	}
}

// The parts of the instance information are plain text: what kind a contact is, and how each part is
// cleaned or refused.
func TestInstanceText(t *testing.T) {
	for c, want := range map[string]string{
		"":                                     "",
		"admins@example.org":                   "email",
		"ana.b+flowa@mail.example.co":          "email",
		"https://forum.example.org/x":          "link",
		"HTTP://example.org":                   "link",
		"Ask at the desk":                      "text",
		"Phone: 0123 456":                      "text",
		"@admin on this Flowa":                 "text",
		"<admins@example.org>":                 "text",
		"javascript: is not a link":            "text", // a word, a colon and a space is a sentence, and is never made a link
		"matrix:#flowa:example.org":            "!",
		"javascript:alert(1)":                  "!",
		"javascript://example.org/%0Aalert(1)": "!", // a host does not make another kind of link an http one
		"JAVASCRIPT:alert(1)":                  "!",
		"data:text/html,hi":                    "!",
		"vbscript:x":                           "!",
		"mailto:admins@example.org":            "!",
		"https://a@b.example":                  "!",
		"https://":                             "!",
		"https://example.org/a\tb":             "!",
		// Nothing that means something in a mailto: link passes as an address: kept as text, never a link.
		"a?cc=victim@evil.example":       "text",
		"a&b=c@example.org":              "text",
		"a%3Fcc=victim@evil.example":     "text",
		"a/b#c@example.org":              "text",
		"o'neil@example.org":             "text",
		"a.b+c-d_e@mail.example.co":      "email",
		"https://\u0430pple.com":         "!",    // a Cyrillic а: a look-alike host is refused
		"https://xn--pple-43d.com/":      "link", // the same host in its xn-- form shows what it is
		"https://example.org:8443/flowa": "link",
		"https://exa_mple.org/":          "!",
		"//evil.example":                 "text", // no scheme: text, which the page never makes a link
	} {
		k, ok := contactKind(c)
		if !ok {
			k = "!"
		}
		if k != want {
			t.Errorf("contactKind(%q) = %q, want %q", c, k, want)
		}
	}
	for _, c := range []struct {
		in, want string
		multi    bool
		bad      bool
	}{
		{"  Hello  ", "Hello", false, false},
		{"a\r\nb", "a\nb", true, false},
		{"a\n\n\n\n\nb", "a\n\nb", true, false},
		{"a   \nb", "a\nb", true, false},
		{"tab\there", "tab here", false, false},
		{"zero\u200bwidth", "zerowidth", false, false},
		{"<b>bo</b>", "<b>bo</b>", false, false},
		{"a\nb", "", false, true},
		{"nul\x00", "", true, true},
		{"esc\x1b", "", true, true},
		{"cr\ronly", "", true, true},
		{"del\x7f", "", true, true},
		{"\u202eevil", "", true, true},
		{strings.Repeat("x", 11), "", false, true},
		{strings.Repeat("é", 10), strings.Repeat("é", 10), false, false},
		// The line and paragraph separators break a line in a browser: line breaks where lines are kept,
		// refused in one line. NEL, the vertical tab and the form feed are control characters, refused.
		{"a\u2028b", "a\nb", true, false},
		{"a\u2029\u2029\u2029\u2029b", "a\n\nb", true, false},
		{"a\u2028b", "", false, true},
		{"a\u2029b", "", false, true},
		{"a\u0085b", "", true, true},
		{"a\u0085b", "", false, true},
		{"a\vb", "", true, true},
		{"a\fb", "", false, true},
	} {
		got, msg := plainText(c.in, "It", c.multi, 10)
		if (msg != "") != c.bad || (!c.bad && got != c.want) {
			t.Errorf("plainText(%q, multi %v) = %q, %q", c.in, c.multi, got, msg)
		}
	}
}

// Two key secrets for the tests, made with openssl rand -hex 32 for them and used nowhere else.
const (
	testSecret  = "b82c1c78dcaa3cf12753e68c6a9c50be5bc8f21cd528b1f3dc75dfc9aa138978"
	testSecret2 = "824ae9c7db7bda59dd2aafa6728bf7862560a11cbb0ee9a767f2b4fbad625b1b"
)

func mustSecret(t testing.TB, v string) keySecret {
	t.Helper()
	s, err := parseKeySecret(v)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// FLOWA_ORIGIN: https and a host, optionally a port, in one written form; everything else is refused
// with a message that names the setting.
func TestOrigin(t *testing.T) {
	good := []struct{ in, url, host string }{
		{"https://flowa.example.org", "https://flowa.example.org", "flowa.example.org"},
		{"HTTPS://Flowa.Example.ORG", "https://flowa.example.org", "flowa.example.org"},  // capitals: the same origin
		{"https://flowa.example.org/", "https://flowa.example.org", "flowa.example.org"}, // one trailing slash, as an address bar shows it
		{"https://flowa.example.org:443", "https://flowa.example.org", "flowa.example.org"},
		{"https://flowa.example.org:443/", "https://flowa.example.org", "flowa.example.org"},
		{"https://flowa.example.org:00443", "https://flowa.example.org", "flowa.example.org"},
		{"https://flowa.example.org:8443", "https://flowa.example.org:8443", "flowa.example.org"},
		{"https://flowa.example.org:08443", "https://flowa.example.org:8443", "flowa.example.org"},
		{"https://flowa.example.org:1", "https://flowa.example.org:1", "flowa.example.org"},
		{"https://flowa.example.org:65535", "https://flowa.example.org:65535", "flowa.example.org"},
		{"  https://flowa.example.org\n", "https://flowa.example.org", "flowa.example.org"}, // a line from a file
		{"https://a.b", "https://a.b", "a.b"},
		{"https://xn--flw-ula.example", "https://xn--flw-ula.example", "xn--flw-ula.example"},
		{"https://a-b.c-d.example", "https://a-b.c-d.example", "a-b.c-d.example"},
		{"https://3com.example", "https://3com.example", "3com.example"},
		{"https://" + strings.Repeat("a", 63) + ".example", "https://" + strings.Repeat("a", 63) + ".example", strings.Repeat("a", 63) + ".example"},
	}
	for _, c := range good {
		o, err := parseOrigin(c.in)
		if err != nil || o.url != c.url || o.host != c.host || o.String() != c.url {
			t.Errorf("parseOrigin(%q) = %+v, %v; want %s, %s", c.in, o, err, c.url, c.host)
		}
	}
	label := strings.Repeat("a", 63) + "."
	host253 := strings.Repeat(label, 3) + strings.Repeat("a", 61) // 253 characters
	if o, err := parseOrigin("https://" + host253); err != nil || len(o.host) != 253 {
		t.Errorf("a host of 253 characters: %+v, %v", o, err)
	}
	bad := []struct{ in, says string }{
		{"", "not set"},
		{"   ", "not set"},
		{"http://flowa.example.org", "http"},
		{"HTTP://flowa.example.org", "http"},
		{"flowa.example.org", "https://"},
		{"//flowa.example.org", "https://"},
		{"ftp://flowa.example.org", "scheme"},
		{"https:/flowa.example.org", "https://"},
		{"https://", "no host"},
		{"https:///", "no host"},
		{"https://:443", "no host"},
		{"https://flowa.example.org/flowa", "path"},
		{"https://flowa.example.org//", "path"},
		{"https://flowa.example.org\\x", "path"},
		{"https://flowa.example.org/?a=1", "path"},
		{"https://flowa.example.org?a=1", "query"},
		{"https://flowa.example.org#top", "fragment"},
		{"https://ana@flowa.example.org", "user name"},
		{"https://ana:pw@flowa.example.org", "user name"},
		{"https://192.168.1.10", "IP address"},
		{"https://203.0.113.5:8443", "IP address"},
		{"https://127.1", "IP address"},
		{"https://10.1.2", "IP address"},
		{"https://[2001:db8::1]", "IP address"},
		{"https://[::1]:443", "IP address"},
		{"https://2001:db8::1", "port"},
		{"https://localhost", "home network"},
		{"https://localhost:3169", "home network"},
		{"https://flowa.localhost", "home network"},
		{"https://raspi", "home network"},
		{"https://raspi.local", "private name"},
		{"https://flowa.lan", "private name"},
		{"https://flowa.internal", "private name"},
		{"https://flowa.home.arpa", "private name"},
		{"https://pi.tail1234.ts.net", "private name"},
		{"https://flöwa.example", "xn--"},
		{"https://флоуа.example", "xn--"},
		{"https://flowa.example.org.", "dot"},
		{"https://flowa..example.org", "empty label"},
		{"https://.flowa.example.org", "empty label"},
		{"https://" + strings.Repeat("a", 64) + ".example", "63"},
		{"https://" + host253 + "a", "253"},
		{"https://-flowa.example", "dash"},
		{"https://flowa-.example", "dash"},
		{"https://flo_wa.example", "character"},
		{"https://flo*wa.example", "character"},
		{"https://flowa.example.org:0", "port"},
		{"https://flowa.example.org:65536", "port"},
		{"https://flowa.example.org:99999", "port"},
		{"https://flowa.example.org:", "port"},
		{"https://flowa.example.org:+443", "port"},
		{"https://flowa.example.org:-1", "port"},
		{"https://flowa.example.org:443:443", "port"},
		{"https://flowa.example.org:44 3", "space"},
		{"https://flowa example.org", "space"},
		{"https://flowa.example.org\x00", "control"},
		{"https://flowa.exam\tple.org", "space"},
		{"https://flowa.example.org\x7f", "control"},
		{"https://flowa.example.org\u200b", "xn--"},
		{"https://flowa.example.org\u00a0x", "space"},
		{"https://" + strings.Repeat("a", 5000), "too long"},
	}
	for _, c := range bad {
		o, err := parseOrigin(c.in)
		if err == nil {
			t.Errorf("parseOrigin(%q) = %+v; want it refused (%s)", c.in, o, c.says)
			continue
		}
		if !strings.Contains(err.Error(), "FLOWA_ORIGIN") || !strings.Contains(err.Error(), c.says) {
			t.Errorf("parseOrigin(%q): %q does not name the setting and say %q", c.in, err, c.says)
		}
		if len(err.Error()) > 300 {
			t.Errorf("parseOrigin of %d characters: a message of %d characters, want it cut", len(c.in), len(err.Error()))
		}
		if o != (publicOrigin{}) {
			t.Errorf("parseOrigin(%q) refused with a value: %+v", c.in, o)
		}
	}
}

// FLOWA_KEY_SECRET: 64 hexadecimal characters that look random; no message and no way of printing it
// shows anything of the value.
func TestKeySecret(t *testing.T) {
	for _, v := range []string{testSecret, strings.ToUpper(testSecret), " " + testSecret + "\n"} {
		if s, err := parseKeySecret(v); err != nil || !s.set() {
			t.Errorf("a good secret (%d characters) is refused: %v", len(v), err)
		}
	}
	for _, c := range []struct{ in, says string }{
		{"", "not set"},
		{" \n", "not set"},
		{"flowa-test-only", "wrong length"},
		{testSecret[:63], "wrong length"},
		{testSecret + "0", "wrong length"},
		{testSecret[:62] + "zz", "hexadecimal"},
		{testSecret[:62] + "  ", "wrong length"}, // trimmed, it is short
		{testSecret[:31] + " " + testSecret[32:], "hexadecimal"},
		{"0x" + testSecret[:62], "hexadecimal"},
		{strings.Repeat("0", 64), "not random"},
		{strings.Repeat("f", 64), "not random"},
		{strings.Repeat("ab", 32), "not random"},
		{strings.Repeat("0123456789abcdef", 4), "not random"},
		{strings.Repeat(testSecret[:32], 2), "not random"}, // 16 bytes twice
		{"000102030405060708090a0b0c0d0e0f000102030405060708090a0b0c0d0e0f", "not random"},
		{strings.Repeat("é", 32), "hexadecimal"}, // 64 bytes, not 64 hexadecimal characters
	} {
		_, err := parseKeySecret(c.in)
		if err == nil || !strings.Contains(err.Error(), "FLOWA_KEY_SECRET") || !strings.Contains(err.Error(), c.says) || !strings.Contains(err.Error(), "openssl rand -hex 32") {
			t.Errorf("parseKeySecret(a value that is %s): %v", c.says, err)
			continue
		}
		if v := strings.TrimSpace(c.in); len(v) >= 6 && strings.Contains(err.Error(), v[:6]) {
			t.Errorf("the message shows the value: %q", err)
		}
		for _, n := range []string{"15", "62", "63", "65"} {
			if strings.Contains(err.Error(), n) {
				t.Errorf("the message says more of the value than that it is wrong: %q", err)
			}
		}
	}
	// Thirty-two different bytes in a run that does not repeat is accepted: the checks are for what a
	// person types, not a claim to measure randomness.
	if _, err := parseKeySecret("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"); err != nil {
		t.Errorf("32 different bytes: %v", err)
	}

	// Printing: every verb, JSON, and as a field of the structs that hold it.
	s := mustSecret(t, testSecret)
	raw, _ := hex.DecodeString(testSecret)
	derived, err := hkdf.Key(sha256.New, raw, nil, sealInfo, 32)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%T"} {
		fmt.Fprintf(&out, verb+"\n", s)
	}
	fmt.Fprint(&out, s, "\n")
	fmt.Fprintln(&out, s.String(), s.GoString())
	j, err := json.Marshal(map[string]any{"secret": s, "in": struct{ S keySecret }{s}})
	if err != nil {
		t.Fatal(err)
	}
	out.Write(j)
	settings := fedSettings{secret: s}
	a := &app{fed: &federation{secret: s}}
	fmt.Fprintf(&out, "\n%v %+v %#v %v %+v %+v %#v\n", settings, settings, settings, a, a, a.fed, a.fed)
	fmt.Fprint(&out, fmt.Errorf("wrapped: %v", s))
	for _, leak := range [][]byte{[]byte(testSecret), []byte(strings.ToUpper(testSecret)), raw, derived, []byte(hex.EncodeToString(derived)), []byte(fmt.Sprint(raw)[1:20]), []byte(fmt.Sprint(derived)[1:20])} {
		if bytes.Contains(out.Bytes(), leak) {
			t.Fatalf("the secret, or the key made from it, is printed:\n%s", out.String())
		}
	}
	if !strings.Contains(out.String(), `"secret":"[key secret]"`) || strings.Count(out.String(), secretShown) < 10 {
		t.Errorf("a key secret prints as something other than %s:\n%s", secretShown, out.String())
	}
}

// Sealing: AES-256-GCM under a key made from the secret, bound to its row; anything else does not
// open, never panics, and the error says only that.
func TestSealKey(t *testing.T) {
	s1, s2 := mustSecret(t, testSecret), mustSecret(t, testSecret2)
	plain := make([]byte, 1218) // about the size of a PKCS#8 RSA 2048 key
	rand.Read(plain)
	const id = "/actor#main-key"
	sealed, err := s1.seal(plain, id, instanceActor)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sealed, "v1.") || strings.ContainsAny(sealed[3:], "+/=.") {
		t.Errorf("sealed is not v1. and base64url: %.20s", sealed)
	}
	if got, err := s1.open(sealed, id, instanceActor); err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("the round trip: %v", err)
	}
	// A fresh nonce each time: two seals of the same key differ, and so do their nonces.
	again, _ := s1.seal(plain, id, instanceActor)
	if again == sealed || again[:19] == sealed[:19] {
		t.Error("two seals of the same key are alike")
	}
	refused := func(what, in string, s keySecret, keyID string, o keyOwner) {
		t.Helper()
		got, err := s.open(in, keyID, o)
		if err == nil || got != nil {
			t.Errorf("%s: it opened", what)
			return
		}
		if !errors.Is(err, errOpenKey) || !strings.HasPrefix(err.Error(), "cannot open the key") {
			t.Errorf("%s: %q, want cannot open the key", what, err)
		}
		for _, leak := range []string{testSecret, testSecret2, hex.EncodeToString(plain[:16]), string(plain[:16])} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("%s: the error carries a secret or the key", what)
			}
		}
	}
	refused("the wrong secret", sealed, s2, id, instanceActor)
	refused("no secret", sealed, keySecret{}, id, instanceActor)
	refused("moved to another key id", sealed, s1, "/users/ana#main-key", instanceActor)
	refused("moved to another owner", sealed, s1, id, keyOwner(1))
	member, _ := s1.seal(plain, "/users/ana#main-key", keyOwner(7))
	refused("a member's key on another member", member, s1, "/users/ana#main-key", keyOwner(8))
	refused("a member's key on the instance actor", member, s1, id, instanceActor)
	refused("empty", "", s1, id, instanceActor)
	refused("the prefix alone", "v1.", s1, id, instanceActor)
	refused("an unknown version", "v2."+sealed[3:], s1, id, instanceActor)
	refused("the version in capitals", "V1."+sealed[3:], s1, id, instanceActor)
	refused("no version", sealed[3:], s1, id, instanceActor)
	refused("padded base64", sealed+"==", s1, id, instanceActor)
	refused("standard base64", "v1."+base64.StdEncoding.EncodeToString(plain), s1, id, instanceActor)
	raw, err := base64.RawURLEncoding.DecodeString(sealed[3:])
	if err != nil || len(raw) != 12+len(plain)+16 {
		t.Fatalf("v1 is nonce, ciphertext and tag: %d bytes, %v", len(raw), err)
	}
	// One flipped bit anywhere: in the nonce, the ciphertext or the tag.
	for i := range len(raw) * 8 {
		b := slices.Clone(raw)
		b[i/8] ^= 1 << (i % 8)
		if _, err := s1.open("v1."+base64.RawURLEncoding.EncodeToString(b), id, instanceActor); !errors.Is(err, errOpenKey) {
			t.Fatalf("bit %d flipped: it opened (%v)", i, err)
		}
	}
	// Cut short anywhere, as text and as bytes.
	for n := range len(sealed) {
		if _, err := s1.open(sealed[:n], id, instanceActor); err == nil {
			t.Fatalf("cut to %d characters: it opened", n)
		}
	}
	for n := range len(raw) {
		if _, err := s1.open("v1."+base64.RawURLEncoding.EncodeToString(raw[:n]), id, instanceActor); err == nil {
			t.Fatalf("cut to %d bytes: it opened", n)
		}
	}
	if _, err := (keySecret{}).seal(plain, id, instanceActor); err == nil {
		t.Error("sealing with no secret worked")
	}
	// Arbitrary input, as it is and as v1 base64, never opens and never panics.
	never := func(b []byte, prefixed bool) bool {
		in := string(b)
		if prefixed {
			in = "v1." + base64.RawURLEncoding.EncodeToString(b)
		}
		got, err := s1.open(in, id, instanceActor)
		return err != nil && got == nil
	}
	if err := quick.Check(never, &quick.Config{MaxCount: 5000}); err != nil {
		t.Error(err)
	}
}

// FuzzOpenKey: whatever is stored in the column, opening it never panics, and only the sealed key opens.
func FuzzOpenKey(f *testing.F) {
	s := mustSecret(f, testSecret)
	plain := []byte("a private key")
	sealed, _ := s.seal(plain, "/actor#main-key", instanceActor)
	for _, seed := range []string{sealed, "", "v1.", "v1.AAAA", "v2." + sealed[3:], sealed[:len(sealed)-1], sealed + "A", "v1." + strings.Repeat("A", 40)} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		got, err := s.open(in, "/actor#main-key", instanceActor)
		if err == nil && !bytes.Equal(got, plain) {
			t.Fatalf("%q opened to something that was never sealed", in)
		}
		if err != nil && (got != nil || !errors.Is(err, errOpenKey)) {
			t.Fatalf("%q: %v with %d bytes", in, err, len(got))
		}
	})
}

// serve waits for a request in flight before it returns, where main used to return the moment
// Shutdown began (docs/federation.md, 8.6).
func TestServeWaits(t *testing.T) {
	a := newApp(nil, t.TempDir())
	entered, release := make(chan struct{}), make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		io.WriteString(w, "done")
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- a.serve(ctx, srv, ln) }()
	answered := make(chan string, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/")
		if err != nil {
			answered <- err.Error()
			return
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		answered <- string(b)
	}()
	<-entered
	cancel()
	select {
	case err := <-served:
		t.Fatalf("serve returned (%v) while a request was still being answered", err)
	case <-time.After(300 * time.Millisecond): // fixed: showing that serve does not return early
	}
	close(release)
	select {
	case got := <-answered:
		if got != "done" {
			t.Errorf("the request in flight got %q, want done", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the request in flight was not answered")
	}
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("serve: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not return after the request finished")
	}
}

// What a key costs: making one (in the background, one at a time), sealing it, and opening and
// parsing it (once per owner; then it is kept in memory).
func BenchmarkGenerateKey(b *testing.B) {
	for b.Loop() {
		if _, err := rsa.GenerateKey(rand.Reader, keyBits); err != nil {
			b.Fatal(err)
		}
	}
}

func benchKey(b *testing.B) (keySecret, []byte, string) {
	s := mustSecret(b, testSecret)
	k, err := rsa.GenerateKey(rand.Reader, keyBits)
	if err != nil {
		b.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		b.Fatal(err)
	}
	sealed, err := s.seal(der, instanceKeyID, instanceActor)
	if err != nil {
		b.Fatal(err)
	}
	return s, der, sealed
}

func BenchmarkSealKey(b *testing.B) {
	s, der, _ := benchKey(b)
	for b.Loop() {
		if _, err := s.seal(der, instanceKeyID, instanceActor); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOpenKey(b *testing.B) {
	s, _, sealed := benchKey(b)
	for b.Loop() {
		der, err := s.open(sealed, instanceKeyID, instanceActor)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := x509.ParsePKCS8PrivateKey(der); err != nil {
			b.Fatal(err)
		}
	}
}
