package main

// Discovery: WebFinger and NodeInfo (docs/federation.md 5.3 and 5.4), the member's opt-in, and
// FLOWA_FEDERATE. The tests without DB in their name need no database: they build a federating app
// whose answers never reach one (the instance actor, refusals, errors, cached counts). The DB tests
// ask through the whole server, as another server would.

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

// discoveryApp is a federated app with no database, its origin recorded, for the tests that never
// reach the database.
func discoveryApp(t testing.TB, origin string) (*app, http.Handler) {
	t.Helper()
	o, err := parseOrigin(origin)
	if err != nil {
		t.Fatal(err)
	}
	a := newApp(nil, t.TempDir())
	a.mode = modeFederated
	a.hosts = []string{"example.com", o.host}
	a.fed = newFederation(nil, fedSettings{on: true, origin: o})
	a.fed.recorded.Store(true)
	return a, a.routes(staticOf(t, nil))
}

// ask sends one request and returns the recorder.
func ask(h http.Handler, method, target, from string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	if from != "" {
		r.RemoteAddr = from
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func wf(resource string) string {
	return "/.well-known/webfinger?resource=" + url.QueryEscape(resource)
}

// FLOWA_FEDERATE: read in single mode only, on or off in any case with spaces around, unset is off,
// anything else stops the start with a message naming the setting; federated mode always federates
// and local mode never does, whatever it says.
func TestFederateSetting(t *testing.T) {
	for _, c := range []struct {
		mode instanceMode
		v    string
		want bool
	}{
		{modeFederated, "", true}, {modeFederated, "off", true}, {modeFederated, "garbage", true},
		{modeLocal, "", false}, {modeLocal, "on", false}, {modeLocal, "garbage", false},
		{modeSingle, "", false}, {modeSingle, " ", false}, {modeSingle, "off", false}, {modeSingle, "OFF", false}, {modeSingle, " Off\n", false},
		{modeSingle, "on", true}, {modeSingle, "ON", true}, {modeSingle, "\tOn ", true},
	} {
		if got, err := modeFederates(c.mode, c.v); err != nil || got != c.want {
			t.Errorf("%s with FLOWA_FEDERATE=%q: %v, %v; want %v", c.mode, c.v, got, err, c.want)
		}
	}
	for _, v := range []string{"yes", "true", "1", "enabled", "onn", "o n", "on,off", "'on'", strings.Repeat("x", 5000)} {
		got, err := modeFederates(modeSingle, v)
		if err == nil || got || !strings.Contains(err.Error(), "FLOWA_FEDERATE must be on or off") || len(err.Error()) > 200 {
			t.Errorf("single with FLOWA_FEDERATE=%.20q: %v, %v; want a short error naming the setting", v, got, err)
		}
	}
	// In single mode with it on, the origin and the secret are needed, and the message says which setting asked.
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if _, err := parseFederation(modeSingle, env(map[string]string{"FLOWA_FEDERATE": "on"})); err == nil || !strings.Contains(err.Error(), "FLOWA_MODE=single with FLOWA_FEDERATE=on") || !strings.Contains(err.Error(), "FLOWA_ORIGIN") {
		t.Errorf("single, on, no origin: %v", err)
	}
	if s, err := parseFederation(modeSingle, env(map[string]string{"FLOWA_FEDERATE": "on", "FLOWA_ORIGIN": "https://ana.example.org", "FLOWA_KEY_SECRET": testSecret})); err != nil || !s.on || s.origin.host != "ana.example.org" {
		t.Errorf("single, on: %+v, %v", s.origin, err)
	}
	if s, err := parseFederation(modeSingle, env(map[string]string{"FLOWA_ORIGIN": "nonsense", "FLOWA_KEY_SECRET": "short"})); err != nil || s.on {
		t.Errorf("single, off, with values it does not read: %v, %v", s.on, err)
	}
}

// parseResource, row by row: every form docs/federation.md 5.3 lists, and the edges of each.
func TestWebFinger(t *testing.T) {
	o, _ := parseOrigin("https://flowa.example.org")
	o8443, _ := parseOrigin("https://flowa.example.org:8443")
	const ana, actor, none, bad = "ana", "(actor)", "(404)", "(400)"
	long := "acct:" + strings.Repeat("a", resourceMax-len("acct:@flowa.example.org")) + "@flowa.example.org"
	for _, c := range []struct {
		v    string
		o    publicOrigin
		want string
	}{
		{"acct:ana@flowa.example.org", o, ana},
		{"ACCT:ana@flowa.example.org", o, ana},
		{"Acct:ana@flowa.example.org", o, ana},
		{"acct:Ana@FLOWA.Example.org", o, ana},
		{"acct:@ana@flowa.example.org", o, ana},
		{"@ana@flowa.example.org", o, ana},
		{"ana@flowa.example.org", o, ana},
		{"acct:ana@flowa.example.org.", o, ana},
		{"acct:ana@flowa.example.org..", o, none},
		{"acct:ana@flowa.example.org:443", o, none}, // an acct host has no port (RFC 7565)
		{"acct:ana@other.example.org", o, none},
		{"acct:ana@sub.flowa.example.org", o, none},
		{"acct:ana@example.org", o, none},
		{"acct:ab@flowa.example.org", o, none},          // too short to be anyone's name
		{"acct:ana!@flowa.example.org", o, none},        // a character no name has
		{"acct:-ana@flowa.example.org", o, none},        // no name starts with a dash
		{"acct:ana.@flowa.example.org", o, "ana."},      // a name that ends with a dot is a name (finding 11)
		{"acct:bo-@flowa.example.org", o, "bo-"},        // and so is one with a dash
		{"acct:ana@flowa.example.org", o8443, ana},      // the handle's host is the host, whatever the port
		{"https://flowa.example.org/users/ana", o, ana}, // the actor's id
		{"HTTPS://FLOWA.EXAMPLE.ORG/users/ana", o, ana},
		{"https://flowa.example.org./users/ana", o, ana},
		{"https://flowa.example.org:443/users/ana", o, ana},
		{"https://flowa.example.org:8443/users/ana", o, none},
		{"https://flowa.example.org:8443/users/ana", o8443, ana},
		{"https://flowa.example.org/users/ana", o8443, none},
		{"https://flowa.example.org:443/users/ana", o8443, none},
		{"https://flowa.example.org/users/Ana", o, none}, // an id is compared as written
		{"https://flowa.example.org/users/ana/", o, none},
		{"https://flowa.example.org/users/ana?x=1", o, none},
		{"https://flowa.example.org/users/ana?", o, none},
		{"https://flowa.example.org/users/ana#main-key", o, none},
		{"https://ana@flowa.example.org/users/ana", o, none},
		{"https://flowa.example.org/users/%61na", o, none},
		{"https://flowa.example.org/users/ana/outbox", o, none},
		{"https://flowa.example.org/@ana", o, none},
		{"https://other.example.org/users/ana", o, none},
		{"http://flowa.example.org/users/ana", o, none},
		{"mailto:ana@flowa.example.org", o, none},
		{"https://flowa.example.org/actor", o, actor},
		{"acct:flowa.example.org@flowa.example.org", o, actor},
		{"acct:FLOWA.example.org.@flowa.example.org.", o, actor},
		{"flowa.example.org@flowa.example.org", o, actor},
		{"acct:flowa.example.org@flowa.example.org", o8443, actor},
		{"acct:other.example.org@flowa.example.org", o, "other.example.org"}, // a name usernameRE allows
		{long, o, none},
		{long + "x", o, bad},
		{"", o, bad},
		{"acct:", o, bad},
		{"acct:ana", o, bad},
		{"ana", o, bad},
		{"acct:ana@", o, bad},
		{"acct:@flowa.example.org", o, bad},
		{"acct:@@ana@flowa.example.org", o, bad},
		{"acct:ana@bo@flowa.example.org", o, bad},
		{"acct:ana @flowa.example.org", o, bad},
		{"acct:ana@flowa.example.org ", o, bad},
		{"acct:ana\t@flowa.example.org", o, bad},
		{"acct:ana\x00@flowa.example.org", o, bad},
		{"acct:anä@flowa.example.org", o, bad},
		{"acct:ana@flоwa.example.org", o, bad}, // a Cyrillic o
		{"https:", o, bad},
		{"https:///users/ana", o, bad},
		{"https://[::1/users/ana", o, bad},
	} {
		tg, ok, err := parseResource(c.v, c.o)
		got := tg.name
		switch {
		case err != nil:
			got = bad
		case !ok:
			got = none
		case tg.actor:
			got = actor
		}
		if got != c.want {
			t.Errorf("parseResource(%q) against %s = %q, want %q", c.v, c.o, got, c.want)
		}
	}

	// Through the server: the instance actor needs no database, so its answer is checked whole here.
	a, h := discoveryApp(t, "https://flowa.example.org")
	w := ask(h, "GET", wf("acct:flowa.example.org@flowa.example.org"), "", nil)
	want := `{"subject":"acct:flowa.example.org@flowa.example.org","aliases":["https://flowa.example.org/actor"],"links":[{"rel":"self","type":"application/activity+json","href":"https://flowa.example.org/actor"}]}`
	if w.Code != 200 || w.Body.String() != want {
		t.Errorf("the instance actor: %d %s", w.Code, w.Body)
	}
	hd := w.Result().Header
	for k, v := range map[string]string{"Content-Type": "application/jrd+json", "Access-Control-Allow-Origin": "*", "Cache-Control": "public, max-age=300", "Content-Length": fmt.Sprint(len(want)), "X-Content-Type-Options": "nosniff"} {
		if hd.Get(k) != v {
			t.Errorf("the instance actor: %s is %q, want %q", k, hd.Get(k), v)
		}
	}
	if w := ask(h, "GET", wf("https://flowa.example.org/actor"), "", nil); w.Body.String() != want {
		t.Errorf("the instance actor by its URL: %d %s", w.Code, w.Body)
	}
	// An Accept header asking for something else is ignored (RFC 7033 4.2).
	if w := ask(h, "GET", wf("acct:flowa.example.org@flowa.example.org"), "", map[string]string{"Accept": "application/xrd+xml"}); w.Code != 200 || w.Body.String() != want {
		t.Errorf("with Accept for XRD: %d %s", w.Code, w.Body)
	}
	// HEAD: the headers of GET (the server sends no body with them; test/browser/federated.py asks a
	// real one, since a recorder keeps what the handler writes).
	if w := ask(h, "HEAD", wf("acct:flowa.example.org@flowa.example.org"), "", nil); w.Code != 200 || w.Result().Header.Get("Content-Type") != jrdType || w.Result().Header.Get("Access-Control-Allow-Origin") != "*" || w.Result().Header.Get("Content-Length") != fmt.Sprint(len(want)) {
		t.Errorf("HEAD: %d, %v", w.Code, w.Result().Header)
	}

	// rel: only the links of the relations asked for, the rest of the answer as it was (RFC 7033 4.3).
	q := "/.well-known/webfinger?resource=" + url.QueryEscape("acct:flowa.example.org@flowa.example.org")
	noLinks := strings.Replace(want, `[{"rel":"self","type":"application/activity+json","href":"https://flowa.example.org/actor"}]`, `[]`, 1)
	for rels, body := range map[string]string{
		"&rel=self": want,
		"&rel=" + url.QueryEscape("http://webfinger.net/rel/profile-page"):               noLinks,
		"&rel=" + url.QueryEscape("http://webfinger.net/rel/profile-page") + "&rel=self": want,
		"&rel=SELF": noLinks, // relation types are compared as plain strings (4.4.4.1)
		"&rel=":     noLinks,
	} {
		if w := ask(h, "GET", q+rels, "", nil); w.Code != 200 || w.Body.String() != body {
			t.Errorf("rel %q: %d %s", rels, w.Code, w.Body)
		}
	}

	// Errors carry the CORS header too: 400 for a missing, repeated, malformed or long resource or query,
	// 404 for what names nobody here, all of them the same 404.
	notFound := ask(h, "GET", wf("acct:ana@other.example.org"), "", nil)
	for target, code := range map[string]int{
		"/.well-known/webfinger":           400,
		"/.well-known/webfinger?rel=self":  400,
		"/.well-known/webfinger?resource=": 400,
		q + "&resource=" + url.QueryEscape("acct:flowa.example.org@flowa.example.org"): 400,
		"/.well-known/webfinger?resource=acct:ana%zz@flowa.example.org":                400,
		"/.well-known/webfinger?resource=acct:ana@flowa.example.org;x=1":               400,
		wf(long + "x"): 400,
		q + "&rel=" + strings.Repeat("x", wfQueryMax):                         400,
		wf("acct:ab@flowa.example.org"):                                       404,
		wf("https://flowa.example.org/users/ana?x"):                           404,
		wf("mailto:ana@flowa.example.org"):                                    404,
		wf("acct:ana@flowa.example.org:443"):                                  404,
		"/.well-known/webfinger?resource=acct:ana@other.example.org&rel=self": 404,
	} {
		w := ask(h, "GET", target, "", nil)
		hd := w.Result().Header
		if w.Code != code || hd.Get("Access-Control-Allow-Origin") != "*" || hd.Get("Content-Type") != "application/json" {
			t.Errorf("%.80s: %d %v, want %d with the CORS header", target, w.Code, hd, code)
		}
		if code == 404 && (w.Body.String() != notFound.Body.String() || hd.Get("Cache-Control") != notFoundKeep) {
			t.Errorf("%.80s: a 404 unlike the others: %s %v", target, w.Body, hd)
		}
		if code == 400 && hd.Get("Cache-Control") != "no-store" {
			t.Errorf("%.80s: a 400 that may be kept: %v", target, hd)
		}
	}

	// Any other method: 405 with Allow and the CORS header, before the write guard would refuse it as a
	// write, whatever Origin and Content-Type it carries.
	for _, path := range []string{"/.well-known/webfinger", "/.well-known/nodeinfo", "/nodeinfo/2.1"} {
		for _, m := range []string{"POST", "PUT", "DELETE", "PATCH", "OPTIONS"} {
			for _, hdr := range []map[string]string{nil, {"Content-Type": "application/json"}, {"Origin": "https://elsewhere.example", "Content-Type": "text/plain"}} {
				w := ask(h, m, path+"?resource=acct:ana@flowa.example.org", "", hdr)
				hd := w.Result().Header
				if w.Code != 405 || hd.Get("Allow") != "GET, HEAD" || hd.Get("Access-Control-Allow-Origin") != "*" {
					t.Errorf("%s %s %v: %d %v", m, path, hdr, w.Code, hd)
				}
			}
		}
	}
	// The gate is for those three addresses only: a write anywhere else meets the guard as before.
	if w := ask(h, "POST", "/.well-known/webfinger/x", "", nil); w.Code != 415 {
		t.Errorf("POST to a neighbouring address: %d, want the guard's 415", w.Code)
	}

	// The allowance per address: over it, 429 with the CORS header and Retry-After; another address is
	// not affected, nor is the app.
	a.lim.discovery = newLimiter(3, time.Hour)
	for i := range 3 {
		if w := ask(h, "GET", wf("acct:flowa.example.org@flowa.example.org"), "198.51.100.7:1000", nil); w.Code != 200 {
			t.Fatalf("request %d: %d", i+1, w.Code)
		}
	}
	for _, path := range []string{wf("acct:flowa.example.org@flowa.example.org"), "/.well-known/nodeinfo", "/nodeinfo/2.1"} {
		w := ask(h, "GET", path, "198.51.100.7:1000", nil)
		if hd := w.Result().Header; w.Code != 429 || hd.Get("Access-Control-Allow-Origin") != "*" || hd.Get("Retry-After") == "" {
			t.Errorf("over the allowance, %s: %d %v", path, w.Code, hd)
		}
	}
	if w := ask(h, "GET", wf("acct:flowa.example.org@flowa.example.org"), "198.51.100.8:1000", nil); w.Code != 200 {
		t.Errorf("another address: %d", w.Code)
	}
	a.instCache, a.instAt = &instanceInfo{}, time.Now().Add(time.Hour) // so the about page needs no database
	if w := ask(h, "GET", "/api/about", "198.51.100.7:1000", nil); w.Code == 429 {
		t.Error("the discovery allowance stopped an app route")
	}
	a.lim.discovery = newLimiter(600, time.Hour)

	// Federation refused: every discovery answer is the 404 of an unknown name.
	a.fed.refuse("federation refused: a test")
	for _, path := range []string{wf("acct:flowa.example.org@flowa.example.org"), "/.well-known/nodeinfo", "/nodeinfo/2.1"} {
		w := ask(h, "GET", path, "", nil)
		if w.Code != 404 || w.Body.String() != notFound.Body.String() || w.Result().Header.Get("Access-Control-Allow-Origin") != "*" {
			t.Errorf("refused, %s: %d %s", path, w.Code, w.Body)
		}
	}
}

// nodeInfoApp is discoveryApp with the counts, the flags and the instance information already in
// memory, so that NodeInfo answers without a database.
func nodeInfoApp(t *testing.T, signups bool, reg string) (*app, http.Handler) {
	a, h := discoveryApp(t, "https://flowa.example.org")
	a.version = "v1.4.0"
	a.nodeUse, a.nodeAt = nodeUsage{Users: nodeUsers{Total: 12, ActiveMonth: 7, ActiveHalfyear: 10}, LocalPosts: 340}, time.Now()
	a.flagCache, a.flagsAt = map[string]bool{"signups": signups}, time.Now().Add(time.Hour)
	a.instCache, a.instAt = &instanceInfo{Registration: reg}, time.Now().Add(time.Hour)
	return a, h
}

// NodeInfo: the discovery JRD, every member the 2.1 schema requires and no other, the patterns and
// lists the schema gives, the content types the protocol names, and openRegistrations as the
// registration state is.
func TestNodeInfo(t *testing.T) {
	a, h := nodeInfoApp(t, true, regOpen)
	w := ask(h, "GET", "/.well-known/nodeinfo", "", nil)
	if want := `{"links":[{"rel":"http://nodeinfo.diaspora.software/ns/schema/2.1","href":"https://flowa.example.org/nodeinfo/2.1"}]}`; w.Code != 200 || w.Body.String() != want {
		t.Errorf("the discovery document: %d %s", w.Code, w.Body)
	}
	if hd := w.Result().Header; hd.Get("Content-Type") != "application/json" || hd.Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("the discovery document's headers: %v", hd)
	}

	w = ask(h, "GET", "/nodeinfo/2.1", "", map[string]string{"Accept": "application/json"})
	if hd := w.Result().Header; w.Code != 200 || hd.Get("Content-Type") != `application/json; profile="http://nodeinfo.diaspora.software/ns/schema/2.1#"` || hd.Get("Access-Control-Allow-Origin") != "*" || hd.Get("Cache-Control") != discoveryKeep {
		t.Fatalf("the document: %d %v", w.Code, hd)
	}
	want := `{"version":"2.1","software":{"name":"flowa","version":"v1.4.0","repository":"https://github.com/TuanSSM/flowtopia"},"protocols":["activitypub"],"services":{"inbound":[],"outbound":[]},"openRegistrations":true,"usage":{"users":{"total":12,"activeMonth":7,"activeHalfyear":10},"localPosts":340},"metadata":{"mode":"federated","federationBuilt":["webfinger","nodeinfo"]}}`
	if w.Body.String() != want {
		t.Errorf("the document:\n%s\nwant\n%s", w.Body, want)
	}
	checkNodeInfoSchema(t, w.Body.Bytes())

	// openRegistrations: the flag off, or sign-up by approval, or single mode, each make it false.
	for _, c := range []struct {
		signups bool
		reg     string
		mode    instanceMode
		want    bool
	}{{true, regOpen, modeFederated, true}, {false, regOpen, modeFederated, false}, {true, regApproval, modeFederated, false}, {true, regOpen, modeSingle, false}} {
		a, h := nodeInfoApp(t, c.signups, c.reg)
		a.mode = c.mode
		var doc nodeInfo
		w := ask(h, "GET", "/nodeinfo/2.1", "", nil)
		if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil || doc.OpenRegistrations != c.want || doc.Metadata.Mode != string(c.mode) {
			t.Errorf("signups %v, %s, %s: %s", c.signups, c.reg, c.mode, w.Body)
		}
	}
	// Every version this build can be named with is a string the schema takes.
	for _, v := range []string{"dev", "main.841edbe", "v1.4.0"} {
		a.version = v
		checkNodeInfoSchema(t, ask(h, "GET", "/nodeinfo/2.1", "", nil).Body.Bytes())
	}
}

// checkNodeInfoSchema checks a document against what the NodeInfo 2.1 schema requires (as
// docs/federation.md 5.4 records it): exactly the seven members, version "2.1", software.name matching
// ^[a-z0-9-]+$ and a version string, at least one protocol from the schema's list, the two service
// arrays with entries from theirs, a boolean openRegistrations, usage.users an object of whole numbers
// that are not negative, and metadata an object.
func checkNodeInfoSchema(t *testing.T, body []byte) {
	t.Helper()
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	keys := slices.Sorted(func(yield func(string) bool) {
		for k := range doc {
			if !yield(k) {
				return
			}
		}
	})
	if !slices.Equal(keys, []string{"metadata", "openRegistrations", "protocols", "services", "software", "usage", "version"}) {
		t.Errorf("members %v", keys)
	}
	var d struct {
		Version  string
		Software struct {
			Name       string
			Version    *string
			Repository string
		}
		Protocols []string
		Services  struct {
			Inbound  *[]string
			Outbound *[]string
		}
		OpenRegistrations *bool
		Usage             struct {
			Users      *map[string]int
			LocalPosts *int
		}
		Metadata *map[string]any
	}
	if err := json.Unmarshal(body, &d); err != nil {
		t.Fatal(err)
	}
	protocols := []string{"activitypub", "buddycloud", "dfrn", "diaspora", "libertree", "ostatus", "pumpio", "tent", "xmpp", "zot"}
	inbound := []string{"atom1.0", "gnusocial", "imap", "pnut", "pop3", "pumpio", "rss2.0", "twitter"}
	outbound := []string{"atom1.0", "blogger", "buddycloud", "diaspora", "dreamwidth", "drupal", "facebook", "friendica", "gnusocial", "google", "insanejournal", "libertree", "linkedin", "livejournal", "mediagoblin", "myspace", "pinterest", "pnut", "posterous", "pumpio", "redmatrix", "rss2.0", "smtp", "tent", "tumblr", "twitter", "wordpress", "xmpp"}
	switch {
	case d.Version != "2.1":
		t.Errorf("version %q", d.Version)
	case !regexp.MustCompile(`^[a-z0-9-]+$`).MatchString(d.Software.Name) || d.Software.Version == nil:
		t.Errorf("software %+v", d.Software)
	case len(d.Protocols) == 0:
		t.Error("no protocol")
	case d.Services.Inbound == nil || d.Services.Outbound == nil:
		t.Error("services without their arrays")
	case d.OpenRegistrations == nil:
		t.Error("no openRegistrations")
	case d.Usage.Users == nil || d.Metadata == nil:
		t.Error("no usage.users or no metadata")
	}
	for _, p := range d.Protocols {
		if !slices.Contains(protocols, p) {
			t.Errorf("protocol %q is not in the schema's list", p)
		}
	}
	for _, s := range *d.Services.Inbound {
		if !slices.Contains(inbound, s) {
			t.Errorf("inbound service %q", s)
		}
	}
	for _, s := range *d.Services.Outbound {
		if !slices.Contains(outbound, s) {
			t.Errorf("outbound service %q", s)
		}
	}
	if d.Usage.Users != nil {
		for k, n := range *d.Usage.Users {
			if !slices.Contains([]string{"total", "activeMonth", "activeHalfyear"}, k) || n < 0 {
				t.Errorf("usage.users.%s = %d", k, n)
			}
		}
	}
	if d.Usage.LocalPosts != nil && *d.Usage.LocalPosts < 0 {
		t.Errorf("localPosts %d", *d.Usage.LocalPosts)
	}
}

// fedSingleStart starts a single-profile instance with FLOWA_FEDERATE=on, and stops its key worker
// before the test's database goes.
func (e *tenv) fedSingleStart(more map[string]string) error {
	e.t.Helper()
	all := map[string]string{"FLOWA_MODE": "single", "FLOWA_FEDERATE": "on", "FLOWA_ORIGIN": "https://ana.example.org", "FLOWA_KEY_SECRET": testSecret,
		"ADMIN_USER": "ana", "ADMIN_PASSWORD": "flowa-test-only", "FLOWA_PREVIEW_FETCH": "off"}
	for k, v := range more {
		all[k] = v
	}
	err := e.a.start(func(k string) string { return all[k] })
	if f := e.a.fed; f != nil {
		e.t.Cleanup(func() {
			c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			f.stop(c)
		})
	}
	e.h = e.a.routes(staticOf(e.t, nil))
	return err
}

// patternsOf is the route table of an app as a sorted list.
func patternsOf(t *testing.T, a *app) []string {
	return slices.Sorted(slices.Values(a.table(staticOf(t, nil)).patterns))
}

// A mode that federates has the routes of its mode and the three of testdata/routes-federated.txt, no
// more; single mode without FLOWA_FEDERATE has none of them (TestDBSingleRoutes holds that table).
func TestDBFederatedRoutes(t *testing.T) {
	extra := routesFile(t, "testdata/routes-federated.txt")
	if !sort.StringsAreSorted(extra) {
		t.Error("testdata/routes-federated.txt is not sorted")
	}
	local := routesFile(t, "testdata/routes-local.txt")
	single := routesFile(t, "testdata/routes-single.txt")
	e := newEnv(t)
	if err := e.fedStart(nil); err != nil {
		t.Fatal(err)
	}
	if got, want := patternsOf(t, e.a), slices.Sorted(slices.Values(slices.Concat(local, extra))); !slices.Equal(got, want) {
		t.Errorf("federated mode:\n got %v\nwant %v", got, want)
	}
	s := newEnv(t)
	if err := s.fedSingleStart(nil); err != nil {
		t.Fatal(err)
	}
	if got, want := patternsOf(t, s.a), slices.Sorted(slices.Values(slices.Concat(local, single, extra))); !slices.Equal(got, want) {
		t.Errorf("single mode with FLOWA_FEDERATE=on:\n got %v\nwant %v", got, want)
	}
	// FLOWA_FEDERATE off, or a stray value in local mode: nothing of federation.
	for _, env := range []map[string]string{{"FLOWA_FEDERATE": "off"}, {"FLOWA_FEDERATE": ""}} {
		x := newEnv(t)
		if err := x.fedSingleStart(env); err != nil || x.a.fed != nil {
			t.Fatalf("single with %v: %v, federation %v", env, err, x.a.fed)
		}
		if got, want := patternsOf(t, x.a), slices.Sorted(slices.Values(slices.Concat(local, single))); !slices.Equal(got, want) {
			t.Errorf("single with %v: %v", env, got)
		}
	}
	l := newEnv(t)
	if err := l.a.start(fedEnv(map[string]string{"FLOWA_MODE": "local", "FLOWA_FEDERATE": "on"})); err != nil || l.a.fed != nil || !slices.Equal(patternsOf(t, l.a), local) {
		t.Errorf("local with FLOWA_FEDERATE=on: %v, %v", err, l.a.fed)
	}
	// A value FLOWA_FEDERATE cannot be stops a single-profile start before anything is written.
	b := newEnv(t)
	if err := b.fedSingleStart(map[string]string{"FLOWA_FEDERATE": "yes"}); err == nil || !strings.Contains(err.Error(), "FLOWA_FEDERATE") {
		t.Errorf("FLOWA_FEDERATE=yes: %v", err)
	}
	if n := b.count(`SELECT count(*) FROM users`) + b.count(`SELECT count(*) FROM instance_settings`); n != 0 || b.a.fed != nil {
		t.Errorf("a refused start wrote %d rows", n)
	}
}

// WebFinger through the whole server, for members: one who opted in is found by every form of her
// handle; one who did not, one who does not exist, one who is suspended and one who was deleted all get
// the same 404, byte for byte and header for header; the owner of a single-profile instance with
// FLOWA_FEDERATE=on is found without the column; the origin is recorded before the first id is given
// out; and a remote server's lookups never lock a member out.
func TestDBWebFinger(t *testing.T) {
	e := newEnv(t)
	anaID, anaTok := e.user("ana", "user")
	e.user("bob", "user")
	cyID, _ := e.user("cyd", "user")
	goneID, _ := e.user("gone", "user")
	for _, id := range []int64{anaID, cyID, goneID} {
		if _, err := e.a.db.Exec(`UPDATE users SET federates = true WHERE id = $1`, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.a.db.Exec(`UPDATE users SET suspended_until = now() + interval '1 day' WHERE id = $1`, cyID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.db.Exec(`DELETE FROM users WHERE id = $1`, goneID); err != nil {
		t.Fatal(err)
	}
	if err := e.fedStart(nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the origin recorded", func() bool { return e.count(`SELECT count(*) FROM instance_settings WHERE key = 'origin'`) == 1 })

	want := `{"subject":"acct:ana@flowa.example.org","aliases":["https://flowa.example.org/users/ana"],"links":[{"rel":"self","type":"application/activity+json","href":"https://flowa.example.org/users/ana"}]}`
	for _, r := range []string{"acct:ana@flowa.example.org", "ACCT:Ana@Flowa.Example.ORG.", "@ana@flowa.example.org", "ana@flowa.example.org", "https://flowa.example.org/users/ana", "https://flowa.example.org:443/users/ana"} {
		w := ask(e.h, "GET", wf(r), "", nil)
		if w.Code != 200 || w.Body.String() != want || w.Result().Header.Get("Content-Type") != jrdType || w.Result().Header.Get("Access-Control-Allow-Origin") != "*" {
			t.Errorf("%s: %d %s %v", r, w.Code, w.Body, w.Result().Header)
		}
	}
	nf := ask(e.h, "GET", wf("acct:nobody@flowa.example.org"), "", nil)
	if nf.Code != 404 {
		t.Fatalf("an unknown name: %d", nf.Code)
	}
	for _, name := range []string{"bob", "cyd", "gone"} {
		w := ask(e.h, "GET", wf("acct:"+name+"@flowa.example.org"), "", nil)
		if w.Code != 404 || w.Body.String() != nf.Body.String() || !headersEqual(w.Result().Header, nf.Result().Header) {
			t.Errorf("%s: %d %s %v; an unknown name: %s %v", name, w.Code, w.Body, w.Result().Header, nf.Body, nf.Result().Header)
		}
	}
	// A suspension that has ended finds her again; opting out hides her at once.
	if _, err := e.a.db.Exec(`UPDATE users SET suspended_until = now() - interval '1 minute' WHERE id = $1`, cyID); err != nil {
		t.Fatal(err)
	}
	if w := ask(e.h, "GET", wf("acct:cyd@flowa.example.org"), "", nil); w.Code != 200 {
		t.Errorf("after the suspension: %d", w.Code)
	}
	if code, _ := e.do(anaTok, "PUT", "/api/me", map[string]any{"federates": false}); code != 200 {
		t.Fatalf("opting out: %d", code)
	}
	if w := ask(e.h, "GET", wf("acct:ana@flowa.example.org"), "", nil); w.Code != 404 || w.Body.String() != nf.Body.String() {
		t.Errorf("after opting out: %d %s", w.Code, w.Body)
	}

	// The lookup is one statement on the unique username index, whoever is asked for.
	rows, err := e.a.db.Query(`EXPLAIN (COSTS OFF) `+webFingerSQL, "ana", false)
	if err != nil {
		t.Fatal(err)
	}
	var plan []string
	for rows.Next() {
		var l string
		rows.Scan(&l)
		plan = append(plan, l)
	}
	rows.Close()
	if p := strings.Join(plan, "\n"); !strings.Contains(p, "Index Scan using users_username_key") {
		t.Errorf("the lookup does not use the username index:\n%s", p)
	}

	// The origin is recorded before an id is given out, when the instance actor's key has not done it yet.
	if _, err := e.a.db.Exec(`DELETE FROM instance_settings`); err != nil {
		t.Fatal(err)
	}
	e.a.fed.recorded.Store(false)
	if w := ask(e.h, "GET", wf("acct:cyd@flowa.example.org"), "", nil); w.Code != 200 {
		t.Errorf("with no origin recorded: %d", w.Code)
	}
	if was, err := recordedOrigin(e.a.db); err != nil || was != "https://flowa.example.org" || e.count(`SELECT count(*) FROM instance_settings WHERE key = 'first_federated_at'`) != 1 {
		t.Errorf("after the first answer the origin recorded is %q (%v)", was, err)
	}
	// Another origin recorded meanwhile, by another server on the database: refused, and the 404.
	if _, err := e.a.db.Exec(`UPDATE instance_settings SET value = '"https://elsewhere.example.org"' WHERE key = 'origin'`); err != nil {
		t.Fatal(err)
	}
	e.a.fed.recorded.Store(false)
	if w := ask(e.h, "GET", wf("acct:cyd@flowa.example.org"), "", nil); w.Code != 404 || w.Body.String() != nf.Body.String() || e.a.federating() {
		t.Errorf("under a moved origin: %d %s, federating %v", w.Code, w.Body, e.a.federating())
	}
	if !strings.Contains(e.a.fed.refusal(), "the origin was https://elsewhere.example.org and is now https://flowa.example.org") {
		t.Errorf("the refusal: %q", e.a.fed.refusal())
	}

	// The allowance: a remote server over it gets 429, and a member at the same address still uses the
	// app and signs in.
	f := newEnv(t)
	f.user("dee", "user")
	if err := f.fedStart(nil); err != nil {
		t.Fatal(err)
	}
	f.a.lim.discovery = newLimiter(5, time.Hour)
	const from = "198.51.100.9:4000"
	for i := range 8 {
		w := ask(f.h, "GET", wf(fmt.Sprintf("acct:guess%d@flowa.example.org", i)), from, nil)
		if (i < 5 && w.Code != 404) || (i >= 5 && w.Code != 429) {
			t.Errorf("lookup %d: %d", i+1, w.Code)
		}
	}
	_, tok := f.user("eve", "user")
	r := httptest.NewRequest("GET", "/api/me", nil)
	r.RemoteAddr = from
	r.AddCookie(&http.Cookie{Name: cookieName, Value: tok})
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"username":"eve"`) {
		t.Errorf("a member at the address that used up the discovery allowance: %d", w.Code)
	}

	// Single mode with FLOWA_FEDERATE=on: the owner is found though the column says no; nobody else exists.
	s := newEnv(t)
	if err := s.fedSingleStart(nil); err != nil {
		t.Fatal(err)
	}
	if s.count(`SELECT count(*) FROM users WHERE federates`) != 0 {
		t.Fatal("the owner's column was written")
	}
	if w := ask(s.h, "GET", wf("acct:ana@ana.example.org"), "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"href":"https://ana.example.org/users/ana"`) {
		t.Errorf("the owner of a single-profile instance: %d %s", w.Code, w.Body)
	}
	// The owner's key is asked for at start, after the instance actor's (5.7).
	waitFor(t, "the owner's key", func() bool { return s.count(`SELECT count(*) FROM ap_keys WHERE user_id IS NOT NULL`) == 1 })
}

// headersEqual compares two sets of answer headers.
func headersEqual(a, b http.Header) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if !slices.Equal(v, b[k]) {
			return false
		}
	}
	return true
}

// NodeInfo's counts through the whole server: every account, those seen in 30 and in 180 days, every
// post; kept for a few minutes; and openRegistrations as the admin sets sign-up.
func TestDBNodeInfo(t *testing.T) {
	e := newEnv(t)
	if err := e.fedStart(nil); err != nil {
		t.Fatal(err)
	}
	_, admin := e.user("boss", "admin")
	for i, ago := range []string{"1 day", "29 days", "31 days", "179 days", "181 days", ""} {
		id, _ := e.user(fmt.Sprintf("member%d", i), "user")
		if ago != "" {
			e.a.db.Exec(`UPDATE users SET last_seen = now() - $2::interval WHERE id = $1`, id, ago)
		} else {
			e.a.db.Exec(`UPDATE users SET last_seen = NULL WHERE id = $1`, id)
		}
		e.a.db.Exec(`INSERT INTO posts (user_id, body) VALUES ($1, 'one')`, id)
	}
	// root (ADMIN_USER) and boss have never been seen, as made here.
	e.a.db.Exec(`UPDATE users SET last_seen = NULL WHERE username IN ('root', 'boss')`)
	read := func() nodeInfo {
		t.Helper()
		w := ask(e.h, "GET", "/nodeinfo/2.1", "", nil)
		if w.Code != 200 {
			t.Fatalf("/nodeinfo/2.1: %d %s", w.Code, w.Body)
		}
		checkNodeInfoSchema(t, w.Body.Bytes())
		var d nodeInfo
		json.Unmarshal(w.Body.Bytes(), &d)
		return d
	}
	d := read()
	if want := (nodeUsage{Users: nodeUsers{Total: 8, ActiveMonth: 2, ActiveHalfyear: 4}, LocalPosts: 6}); d.Usage != want || !d.OpenRegistrations || d.Software.Version != e.a.version {
		t.Errorf("usage %+v, want %+v; open %v", d.Usage, want, d.OpenRegistrations)
	}
	// Kept: a new account is counted once the counts are read again.
	e.user("late", "user")
	if read().Usage.Users.Total != 8 {
		t.Error("the counts were read again at once")
	}
	e.a.nodeMu.Lock()
	e.a.nodeAt = time.Time{}
	e.a.nodeMu.Unlock()
	if read().Usage.Users.Total != 9 {
		t.Error("the counts were not read again")
	}
	// The signups flag off, then sign-up by approval: closed either way.
	if code, _ := e.do(admin, "PUT", "/api/flags", map[string]any{"flags": map[string]bool{"signups": false}}); code != 200 {
		t.Fatal(code)
	}
	if read().OpenRegistrations {
		t.Error("open with the signups flag off")
	}
	e.do(admin, "PUT", "/api/flags", map[string]any{"flags": map[string]bool{"signups": true}})
	_, cur := e.do(admin, "GET", "/api/admin/instance", nil)
	if code, out := e.do(admin, "PUT", "/api/admin/instance", map[string]any{"description": "", "contact": "", "rules": []any{}, "registration": "approval", "updatedAt": cur["updatedAt"]}); code != 200 {
		t.Fatalf("approval: %d %v", code, out["_raw"])
	}
	if read().OpenRegistrations {
		t.Error("open while sign-up is by approval")
	}
	// The dashboard says what this version of federation does.
	if _, out := e.do(admin, "GET", "/api/admin/dashboard", nil); out["federation"] == nil || out["federation"].(map[string]any)["built"] != discoveryOnly || out["federation"].(map[string]any)["origin"] != "https://flowa.example.org" {
		t.Errorf("the dashboard: %v", out["federation"])
	}

	s := newEnv(t)
	if err := s.fedSingleStart(nil); err != nil {
		t.Fatal(err)
	}
	w := ask(s.h, "GET", "/nodeinfo/2.1", "", nil)
	var sd nodeInfo
	if json.Unmarshal(w.Body.Bytes(), &sd); w.Code != 200 || sd.OpenRegistrations || sd.Metadata.Mode != "single" || sd.Usage.Users.Total != 1 {
		t.Errorf("single: %d %s", w.Code, w.Body)
	}
	if w := ask(s.h, "GET", "/.well-known/nodeinfo", "", nil); !strings.Contains(w.Body.String(), `"href":"https://ana.example.org/nodeinfo/2.1"`) {
		t.Errorf("single, discovery: %s", w.Body)
	}
}

// The member's switch: absent and refused where the instance does not federate; in federated mode off
// for everyone at first, saved, read back, with the warning for a name Mastodon cannot read and the
// member's key asked for on opting in; implied and refused as a field in single mode with
// FLOWA_FEDERATE=on.
func TestDBFederateOptIn(t *testing.T) {
	l := newEnv(t)
	if err := l.a.start(func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	_, ltok := l.user("ana", "user")
	if _, out := l.do(ltok, "GET", "/api/me", nil); out["federation"] != nil {
		t.Errorf("local mode tells of federation: %v", out["federation"])
	}
	if code, out := l.do(ltok, "PUT", "/api/me", map[string]any{"federates": true}); code != 400 || !strings.Contains(out["error"].(string), "does not share anything beyond itself") {
		t.Errorf("local mode takes the switch: %d %v", code, out["_raw"])
	}
	if l.count(`SELECT count(*) FROM users WHERE federates`) != 0 {
		t.Error("local mode wrote the switch")
	}

	e := newEnv(t)
	boID, bo := e.user("bob", "user")
	_, dot := e.user("ana.", "user")
	_, dash := e.user("cy-", "user")
	if err := e.fedStart(nil); err != nil {
		t.Fatal(err)
	}
	fedOf := func(tok string) map[string]any {
		t.Helper()
		_, out := e.do(tok, "GET", "/api/me", nil)
		f, _ := out["federation"].(map[string]any)
		return f
	}
	if f := fedOf(bo); f == nil || f["optedIn"] != false || f["handle"] != "@bob@flowa.example.org" || f["warning"] != nil || f["implied"] != nil {
		t.Errorf("bo before: %v", f)
	}
	if _, out := e.do("", "GET", "/api/me", nil); out["federation"] != nil {
		t.Error("a visitor is told of federation")
	}
	code, out := e.do(bo, "PUT", "/api/me", map[string]any{"federates": true})
	if f, _ := out["federation"].(map[string]any); code != 200 || f["optedIn"] != true {
		t.Errorf("bo opts in: %d %v", code, out["_raw"])
	}
	if f := fedOf(bo); f["optedIn"] != true || e.count(`SELECT count(*) FROM users WHERE federates`) != 1 {
		t.Errorf("bo after: %v", f)
	}
	waitFor(t, "bo's key", func() bool { return e.count(`SELECT count(*) FROM ap_keys WHERE user_id = $1`, boID) == 1 })
	if strings.Contains(out["_raw"].(string), `"federates"`) {
		t.Error("the opt-in is in the user object, which other answers carry too")
	}
	// A name that ends with a dot or a dash is warned, before and after opting in, and can still opt in.
	for _, tok := range []string{dot, dash} {
		if f := fedOf(tok); f["warning"] != handleWarning {
			t.Errorf("no warning: %v", f)
		}
		if code, out := e.do(tok, "PUT", "/api/me", map[string]any{"federates": true}); code != 200 || out["federation"].(map[string]any)["warning"] != handleWarning {
			t.Errorf("opting in: %d %v", code, out["_raw"])
		}
	}
	if w := ask(e.h, "GET", wf("acct:ana.@flowa.example.org"), "", nil); w.Code != 200 {
		t.Errorf("ana. is not found: %d", w.Code)
	}
	// Back off; a save of anything else leaves the switch as it is.
	e.do(bo, "PUT", "/api/me", map[string]any{"federates": false})
	e.do(dot, "PUT", "/api/me", map[string]any{"bio": "spinning"})
	if fedOf(bo)["optedIn"] != false || fedOf(dot)["optedIn"] != true {
		t.Error("the switch did not hold")
	}
	if code, _ := e.do(bo, "PUT", "/api/me", map[string]any{"federates": "yes"}); code != 400 {
		t.Errorf("a switch that is not a boolean: %d", code)
	}

	s := newEnv(t)
	if err := s.fedSingleStart(nil); err != nil {
		t.Fatal(err)
	}
	var ownerID int64
	s.a.db.QueryRow(`SELECT id FROM users`).Scan(&ownerID)
	owner := s.session(ownerID)
	_, out = s.do(owner, "GET", "/api/me", nil)
	if f, _ := out["federation"].(map[string]any); f == nil || f["implied"] != true || f["optedIn"] != true || f["handle"] != "@ana@ana.example.org" {
		t.Errorf("the owner: %v", out["federation"])
	}
	if code, out := s.do(owner, "PUT", "/api/me", map[string]any{"federates": false}); code != 400 || !strings.Contains(out["error"].(string), "FLOWA_FEDERATE") {
		t.Errorf("the owner's switch: %d %v", code, out["_raw"])
	}
}

// testDB opens a database of its own for a benchmark, as newEnv does for a test.
func testDB(b *testing.B) *sql.DB {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		b.Skip("TEST_DATABASE_URL is not set")
	}
	raw := make([]byte, 6)
	rand.Read(raw)
	name := "b_" + hex.EncodeToString(raw)
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := admin.Exec(`CREATE SCHEMA ` + name); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { admin.Exec(`DROP SCHEMA ` + name + ` CASCADE`); admin.Close() })
	u, _ := url.Parse(dsn)
	q := u.Query()
	q.Set("search_path", name)
	u.RawQuery = q.Encode()
	db, err := openDB(u.String())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { db.Close() })
	return db
}

// BenchmarkWebFinger is one lookup through the whole server: the instance actor (no query), and a
// member who opted in (one query on the unique index, against the test database).
func BenchmarkWebFinger(b *testing.B) {
	run := func(b *testing.B, a *app, target string) {
		a.lim.discovery = newLimiter(1<<30, time.Hour)
		h := a.routes(staticOf(b, nil))
		r := httptest.NewRequest("GET", target, nil)
		if w := ask(h, "GET", target, "", nil); w.Code != 200 {
			b.Fatalf("%d %s", w.Code, w.Body)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
		}
	}
	b.Run("actor", func(b *testing.B) {
		a, _ := discoveryApp(b, "https://flowa.example.org")
		run(b, a, wf("acct:flowa.example.org@flowa.example.org"))
	})
	b.Run("member", func(b *testing.B) {
		db := testDB(b)
		if _, err := db.Exec(`INSERT INTO users (username, display_name, pw_hash, props, federates) VALUES ('ana', 'ana', 'x', '["nunchaku"]', true)`); err != nil {
			b.Fatal(err)
		}
		a, _ := discoveryApp(b, "https://flowa.example.org")
		a.db, a.fed.db = db, db
		run(b, a, wf("acct:ana@flowa.example.org"))
	})
}
