package main

// Remote actors (docs/federation.md, F5, 6.3, 7.3, 7.4, 9.1, 9.2 and section 12): the cache that fetches
// them, signed by the instance actor, from local TLS stubs reached through the fetcher's test-only dial
// override; the rows it writes; the surfaces that must not see those rows; and -purge-remote.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

/* ---------- the parts that need no database ---------- */

func TestRemoteUsername(t *testing.T) {
	id := "https://social.example/users/x"
	tag := remoteUsername("", "social.example", id, false) // "+" and 12 hex digits
	if !strings.HasPrefix(tag, "+") || len(tag) != len("+")+12+len("@social.example") {
		t.Fatalf("the fallback name is %q", tag)
	}
	for _, c := range []struct {
		pref  string
		taken bool
		want  string
	}{
		{"alyssa", false, "alyssa@social.example"},
		{"Gargron", false, "Gargron@social.example"}, // case kept: remote names are not assumed to ignore it
		{"a.b-c_d~e", false, "a.b-c_d~e@social.example"},
		{"alyssa", true, "alyssa" + tag},      // another actor of the host holds the name
		{"", false, tag},                      // no name
		{"ana@other.example", false, tag},     // an "@" in the name
		{"石井", false, tag},                    // another script
		{"has space", false, tag},             // not plain
		{"a+b", false, tag},                   // "+" is the fallback's own mark
		{strings.Repeat("x", 65), false, tag}, // over 64 bytes
		{strings.Repeat("x", 64), false, strings.Repeat("x", 64) + "@social.example"},
	} {
		got := remoteUsername(c.pref, "social.example", id, c.taken)
		if got != c.want {
			t.Errorf("%q (taken %v): got %q, want %q", c.pref, c.taken, got, c.want)
		}
		if usernameRE.MatchString(strings.ToLower(got)) {
			t.Errorf("%q is a name a member could sign up with", got)
		}
	}
	if remoteUsername("", "social.example", id+"/2", false) == tag {
		t.Error("two ids share a fallback name")
	}
}

func TestRemoteDisplay(t *testing.T) {
	for _, c := range []struct{ name, pref, want string }{
		{"Alyssa P. Hacker", "alyssa", "Alyssa P. Hacker"},
		{"  two\n\tlines  ", "", "two lines"},
		{"", "alyssa", "alyssa"},
		{"", "ana@x", ""},
		{"evil\u202egnp.exe", "eve", "eve"}, // turns the text's direction: dropped whole, the handle stands in
		{"zero\u200bwidth", "", "zerowidth"},
		{strings.Repeat("é", 70), "", strings.Repeat("é", remoteDisplayMax)},
	} {
		if got := remoteDisplay(c.name, c.pref); got != c.want {
			t.Errorf("remoteDisplay(%q, %q) = %q, want %q", c.name, c.pref, got, c.want)
		}
	}
	if got := remoteText("a\x00b"+strings.Repeat("é", 10), 7); got != "abéé" {
		t.Errorf("remoteText cut to %q", got)
	}
}

func TestActorReasons(t *testing.T) {
	for fail, want := range map[fetchFail]actorReason{
		failRefused: actorRefused, failConnect: actorUnreachable, failTimeout: actorTimeout, failCanceled: actorCanceled,
		failTooLarge: actorTooLarge, failType: actorWrongType, failGone: actorGone, failNotFound: actorNotFound,
		failStatus: actorUnreachable, failRedirect: actorRedirect, failBusy: actorBusy,
	} {
		if got := fetchReason(&fetchError{Fail: fail}); got != want {
			t.Errorf("fetch failure %d: %v, want %v", fail, got, want)
		}
	}
	// What the remote server answered is remembered; this server's own state is not.
	for r := actorBadID; r <= actorTooManyNames; r++ {
		mine := r <= actorSuspended
		if r.cached() == mine {
			t.Errorf("%v: remembered %v", r, r.cached())
		}
		if r.String() == "refused" && r != actorRefused {
			t.Errorf("reason %d has no phrase", r)
		}
	}
	c := newActorCache(&federation{}, nil)
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	failed := func(id string, at time.Time) bool { m, ok := c.recall(id, at); return ok && m.reason != 0 }
	c.remember("https://a.example/1", actorNotFound, now)
	c.remember("https://a.example/2", actorGone, now)
	c.remember("https://a.example/3", actorBusy, now)
	for _, s := range []struct {
		id    string
		after time.Duration
		want  bool
	}{
		{"https://a.example/1", actorFailFor - time.Second, true}, {"https://a.example/1", actorFailFor, false},
		{"https://a.example/2", actorGoneFor - time.Second, true}, {"https://a.example/2", actorGoneFor, false},
		{"https://a.example/3", 0, false},
	} {
		if got := failed(s.id, now.Add(s.after)); got != s.want {
			t.Errorf("%s after %v: remembered %v, want %v", s.id, s.after, got, s.want)
		}
	}
	// The memory of failures is bounded: a flood of failing ids costs refetches, not memory.
	for i := range actorMemoMax + 500 {
		c.remember(fmt.Sprintf("https://flood.example/%d", i), actorNotFound, now)
	}
	if n := len(c.memo); n > actorMemoMax {
		t.Errorf("%d failures remembered, more than %d", n, actorMemoMax)
	}
	e := &actorError{Reason: actorNotFound, Host: strings.Repeat("h", 10), Cached: true, err: errors.New("secret detail")}
	if s := e.Error(); s != "remote actor on hhhhhhhhhh: not found (remembered)" {
		t.Errorf("the error says %q", s)
	}
}

// TestDialOverrideTestOnly holds that a build of the server cannot reach a private address through the
// fetcher's dial override. fetcher.over is an interface whose methods are unexported, so only a type of
// this package can implement it, and the one that does is declared in override_test.go, which a build of
// the server does not compile. So the guarantee is that no file of the server declares a stubAddr method
// (a type that could fill the field); the other checks are tripwires on top (the field set, the test's type
// named), which aliasing could slip past but the first cannot.
func TestDialOverrideTestOnly(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fs := token.NewFileSet()
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fs, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.FuncDecl:
				if n.Recv != nil && n.Name.Name == "stubAddr" {
					t.Errorf("%s: a file of the server implements the dial override", fs.Position(n.Pos()))
				}
			case *ast.TypeSpec:
				if n.Name.Name == "dialOverride" {
					t.Errorf("%s: a file of the server declares the dial override", fs.Position(n.Pos()))
				}
			case *ast.AssignStmt:
				for _, l := range n.Lhs {
					if s, ok := l.(*ast.SelectorExpr); ok && s.Sel.Name == "over" {
						t.Errorf("%s: a file of the server sets a dial override", fs.Position(n.Pos()))
					}
				}
			case *ast.KeyValueExpr:
				if id, ok := n.Key.(*ast.Ident); ok && id.Name == "over" {
					t.Errorf("%s: a file of the server makes a fetcher with a dial override", fs.Position(n.Pos()))
				}
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("no file of the server was read")
	}
}

// fixtureFiles are every document under testdata/federation that a server could send: the shapes copied
// from the texts, the actors written from them, and Flowa's own golden documents.
func fixtureFiles(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, g := range []string{"testdata/federation/shapes/*.json", "testdata/federation/actors/*.json", "testdata/federation/*.json"} {
		m, err := filepath.Glob(g)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range m {
			if !strings.Contains(p, "schema") {
				out = append(out, p)
			}
		}
	}
	return out
}

// fixtureOutcome reads one document as the inbox and the actor cache would: the scan, then the reader its
// type names, and for an actor what the cache would keep (its key, its name, its state).
func fixtureOutcome(t *testing.T, b []byte, keyDocs map[string]apKey) string {
	if r := apScan(b); r != 0 {
		return "scan: " + r.String()
	}
	var head struct {
		Type json.RawMessage `json:"type"`
	}
	json.Unmarshal(b, &head)
	var types []string
	if json.Unmarshal(head.Type, &types) != nil {
		var one string
		json.Unmarshal(head.Type, &one)
		types = []string{one}
	}
	kind := ""
	for _, ty := range types {
		if apActorKinds[ty] || slices.Contains([]string{"Note", "Question", "Tombstone", "Collection", "OrderedCollection", "Key"}, ty) {
			kind = ty
		}
	}
	or := func(err error, ok string) string {
		if err != nil {
			return err.Error()
		}
		return ok
	}
	switch {
	case apActorKinds[kind]:
		doc, err := decodeActor(b, actorDocMax)
		if err != nil {
			return err.Error()
		}
		key := actorKey(doc, nil, nil)
		for _, k := range doc.Keys {
			if kd, ok := keyDocs[k.ID]; ok && k.ByID {
				key = actorKey(doc, []string{k.ID}, &kd)
			}
		}
		state := "ok"
		if doc.Suspended {
			state = "suspended"
		}
		host := actorHost(doc.ID)
		return fmt.Sprintf("actor %s as %s, key %q, shown as %q", state, remoteUsername(doc.PreferredUsername, host, doc.ID, false), key.ID, remoteDisplay(doc.Name, doc.PreferredUsername))
	case kind == "Key":
		f, err := apParse(b, actorDocMax)
		if err != nil {
			return err.Error()
		}
		id, _ := f.id("id")
		k, ok := apKeyOf(idOrObject{ID: id, Obj: f})
		if !ok {
			return "key unreadable"
		}
		if _, err := parsePublicKeyPEM(k.PEM, sigKeyMin); err != nil {
			return "key of " + k.Owner + ": " + err.Error()
		}
		keyDocs[k.ID] = k
		return "key of " + k.Owner
	case kind == "Note" || kind == "Question":
		_, err := decodeNote(b, apMax)
		return or(err, "post read")
	case kind == "Tombstone":
		_, err := decodeTombstone(b, apMax)
		return or(err, "tombstone read")
	case kind == "Collection" || kind == "OrderedCollection":
		_, err := decodeCollection(b, apMax)
		return or(err, "collection read")
	}
	a, err := decodeActivity(b, apMax)
	if err == nil && len(a.Object) == 1 && a.Object[0].Obj != nil && a.Object[0].Kind == "Note" {
		_, err = a.Object[0].note()
	}
	return or(err, "activity read")
}

// TestFixturesParse reads every fixture through the decoder (the inbox's parser) and, for actors, through
// the actor cache's choices: the key it would keep and the name it would write. None is recorded (each
// folder's README says from where), so the outcomes below are what the texts' shapes give, and a new
// fixture must be added here with its outcome before it counts. TestDBRemoteFixtures fetches the actors
// through the cache itself.
func TestFixturesParse(t *testing.T) {
	gargron := `"https://mastodon.social/users/Gargron#main-key"`
	want := map[string]string{
		"actor-ana.json":                    `actor ok as ana@flowa.example.org, key "", shown as "Ana"`, // the golden key is 1024 bits, under the minimum
		"actor-instance.json":               `actor ok as flowa.example.org@flowa.example.org, key "", shown as "flowa.example.org"`,
		"note-ana.json":                     "post read",
		"ap-ex1-actor.json":                 `actor ok as alyssa@social.example, key "", shown as "Alyssa P. Hacker"`,
		"ap-ex3-create.json":                "activity read",
		"ap-ex4-create-reply.json":          "scan: a document that is not well-formed JSON",
		"ap-ex5-like.json":                  "activity read",
		"ap-ex6-create-public.json":         "activity read",
		"ap-ex7-like-embedded.json":         "activity refused: an object without an id (id)",
		"ap-ex9-actor.json":                 `actor ok as kenzoishii@kenzoishii.example.com, key "", shown as "石井健蔵"`,
		"ap-ex15-note-no-id.json":           "post refused: an object without an id (id)",
		"ap-ex16-create.json":               "activity read",
		"ap-ex17-tombstone.json":            "tombstone read",
		"as-core-ex1-context-string.json":   "post refused: an object without an id (id)",
		"as-core-ex2-context-object.json":   "post refused: an object without an id (id)",
		"as-core-ex3-context-array.json":    "post refused: an object without an id (id)",
		"as-core-ex16-like-http.json":       "activity refused: an id that is not an https URL that may be fetched (id)",
		"as-core-ex20-collection-http.json": "collection refused: an id that is not an https URL that may be fetched (id)",
		"flowa-5.5-actor.json":              `actor ok as ana@flowa.example.org, key "", shown as "Ana"`, // the note shortens its PEM
		"flowa-6.5-create.json":             "activity read",
		"mastodon-block.json":               "activity read",
		"mastodon-emoji.json":               "scan: a document that is not well-formed JSON",
		"mastodon-flag.json":                "activity read",
		"mastodon-focal-point.json":         "post read",
		"mastodon-hashtag.json":             "post read",
		"mastodon-memorial.json":            "actor refused: an actor without an inbox (inbox)",
		"mastodon-move.json":                "activity read",
		"mastodon-public-key.json":          "actor refused: an actor without an inbox (inbox)",
		"mastodon-question.json":            "post read",
		"mastodon-suspended.json":           `actor suspended as ` + remoteUsername("", "example.com", "https://example.com/@eve", false) + `, key "", shown as ""`,
		"mastodon-vote.json":                "activity read",
		"mastodon-actor.json":               `actor ok as Gargron@mastodon.social, key ` + gargron + `, shown as "Gargron"`,
		"swicg-key-object.json":             "key of https://social.example/alyssa/",
		"swicg-key-owner.json":              `actor ok as alyssa@social.example, key "https://social.example/keys/alyssa", shown as "Alyssa P. Hacker"`,
	}
	files := fixtureFiles(t)
	// Key documents first, so the actor naming one by id finds it, as the cache would after fetching it.
	slices.SortStableFunc(files, func(a, b string) int {
		return boolInt(!strings.Contains(a, "key-object")) - boolInt(!strings.Contains(b, "key-object"))
	})
	keyDocs := map[string]apKey{}
	for _, p := range files {
		name := filepath.Base(p)
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		w, ok := want[name]
		if !ok {
			t.Errorf("%s: not in the table: %s", name, fixtureOutcome(t, b, keyDocs))
			continue
		}
		delete(want, name)
		if got := fixtureOutcome(t, b, keyDocs); got != w {
			t.Errorf("%s:\n got %s\nwant %s", name, got, w)
		}
	}
	for name := range want {
		t.Errorf("%s: in the table, not on disk", name)
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

/* ---------- the stub world ---------- */

// actorNames are the names the actor stub's certificate covers and the override resolves to it.
// here.example is the instance's own origin in these tests: resolving it to the stub means a request
// that should never be sent would be seen and counted, not merely fail.
var actorNames = []string{"mastodon.social", "social.example", "kenzoishii.example.com", "example.com", "flowa.example.org",
	"remote.example", "other.example", "xn--bcher-kva.example", "a.example", "b.example", "moved.example", "here.example", "big.example"}

var actorCert = sync.OnceValues(func() (tls.Certificate, *x509.CertPool) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "flowa actor stub"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true, DNSNames: actorNames,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		panic(err)
	}
	c, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(c)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
})

// actorStub starts h behind TLS and returns a production fetcher (the address guard on, no link card
// stub) whose test-only dial override resolves actorNames to it and every other name to nothing.
func actorStub(t testing.TB, h http.Handler) *fetcher {
	t.Helper()
	cert, pool := actorCert()
	srv := httptest.NewUnstartedServer(h)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	stub := srv.Listener.Addr().String()
	f := newFetcher("", false)
	f.version = "test"
	f.over = &dialOverride{stub: stub, resolve: func(host string) []string {
		host = strings.TrimSuffix(strings.ToLower(host), ".")
		if host == "private.example" {
			return []string{"10.0.0.1:443"}
		}
		if slices.Contains(actorNames, host) {
			return []string{stub}
		}
		return nil
	}}
	f.tr.TLSClientConfig.RootCAs = pool
	t.Cleanup(f.tr.CloseIdleConnections)
	return f
}

// tclock is a clock a test moves.
type tclock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *tclock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *tclock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// worldDoc is what the stub answers at one address.
type worldDoc struct {
	status   int
	ctype    string
	body     []byte
	location string
}

// actorWorld is the remote servers: documents by host and path, the requests each address got, whether
// each request's signature verified as the receiving server would verify it (verifyRequest, with the
// instance actor's public key), and addresses held until the test lets them answer.
type actorWorld struct {
	mu      sync.Mutex
	docs    map[string]worldDoc
	hits    map[string]int
	sigs    []string // "" for a signature that verified, the refusal otherwise
	hold    map[string]chan struct{}
	arrived chan string
	keyFor  func(context.Context, string) (*rsa.PublicKey, error)
	now     func() time.Time
}

func newWorld(now func() time.Time) *actorWorld {
	return &actorWorld{docs: map[string]worldDoc{}, hits: map[string]int{}, hold: map[string]chan struct{}{}, arrived: make(chan string, 64), now: now}
}

func worldKey(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return u
	}
	return strings.ToLower(p.Host) + p.RequestURI()
}

func (w *actorWorld) put(id string, d worldDoc) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if d.status == 0 {
		d.status = 200
	}
	if d.ctype == "" {
		d.ctype = "application/activity+json"
	}
	w.docs[worldKey(id)] = d
}

func (w *actorWorld) hit(id string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.hits[worldKey(id)]
}

func (w *actorWorld) total() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := 0
	for _, v := range w.hits {
		n += v
	}
	return n
}

func (w *actorWorld) signatures() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.sigs)
}

// holdAt makes the address wait until the returned function is called; each request there is announced
// on arrived first.
func (w *actorWorld) holdAt(id string) func() {
	ch := make(chan struct{})
	w.mu.Lock()
	w.hold[worldKey(id)] = ch
	w.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { close(ch) }) }
}

func (w *actorWorld) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	k := strings.ToLower(r.Host) + r.URL.RequestURI()
	sig := "no key to check with"
	if w.keyFor != nil {
		v, err := newSigVerifier("https://"+r.Host, w.keyFor)
		if err == nil {
			v.built, v.now = time.Time{}, w.now
			if _, err = v.verifyRequest(r.Context(), r, nil); err == nil {
				sig = ""
			} else {
				sig = err.Error()
			}
		}
	}
	w.mu.Lock()
	w.hits[k]++
	w.sigs = append(w.sigs, sig)
	d, ok := w.docs[k]
	ch := w.hold[k]
	w.mu.Unlock()
	if ch != nil {
		w.arrived <- k
		select {
		case <-ch:
		case <-r.Context().Done():
			return
		}
	}
	if !ok {
		http.NotFound(rw, r)
		return
	}
	if d.location != "" {
		rw.Header().Set("Location", d.location)
	}
	rw.Header().Set("Content-Type", d.ctype)
	rw.WriteHeader(d.status)
	rw.Write(d.body)
}

func pemOf(k *rsa.PublicKey) string {
	der, _ := x509.MarshalPKIXPublicKey(k)
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// actorDoc is an actor document at id; key, when given, is its own at id#main-key; more adds or (with
// nil) removes properties.
func actorDoc(id, pref, name string, key *rsa.PublicKey, more map[string]any) []byte {
	d := map[string]any{"@context": []string{"https://www.w3.org/ns/activitystreams", "https://w3id.org/security/v1"},
		"id": id, "type": "Person", "inbox": id + "/inbox", "followers": id + "/followers"}
	if pref != "" {
		d["preferredUsername"] = pref
	}
	if name != "" {
		d["name"] = name
	}
	if key != nil {
		d["publicKey"] = map[string]any{"id": id + "#main-key", "owner": id, "publicKeyPem": pemOf(key)}
	}
	for k, v := range more {
		if v == nil {
			delete(d, k)
		} else {
			d[k] = v
		}
	}
	b, _ := json.Marshal(d)
	return b
}

// remoteEnv is a federated instance at https://here.example whose actor cache asks the stub world, on a
// clock the test moves, with the instance actor's key made.
type remoteEnv struct {
	*tenv
	c     *actorCache
	w     *actorWorld
	ft    *fetcher
	clock *tclock
	pub   *rsa.PublicKey // the instance actor's
}

const hereKeyID = "https://here.example/actor#main-key"

func newRemoteEnv(t *testing.T) *remoteEnv {
	t.Helper()
	e := newEnv(t)
	if err := e.fedStart(map[string]string{"FLOWA_ORIGIN": "https://here.example"}); err != nil {
		t.Fatal(err)
	}
	return remoteOn(t, e)
}

// remoteOn points a started federated instance's actor cache at a new stub world.
func remoteOn(t *testing.T, e *tenv) *remoteEnv {
	t.Helper()
	f := e.a.fed
	if _, err := f.ensureKey(context.Background(), instanceActor); err != nil {
		t.Fatal(err)
	}
	var p string
	if err := e.a.db.QueryRow(`SELECT public_key FROM ap_keys WHERE user_id IS NULL`).Scan(&p); err != nil {
		t.Fatal(err)
	}
	pub, err := parsePublicKeyPEM(p, sigKeyMin)
	if err != nil {
		t.Fatal(err)
	}
	clock := &tclock{t: time.Now().UTC().Truncate(time.Second)}
	w := newWorld(clock.now)
	w.keyFor = func(_ context.Context, id string) (*rsa.PublicKey, error) {
		if id == f.origin.url+instanceKeyID {
			return pub, nil
		}
		return nil, errors.New("not the instance actor's key")
	}
	ft := actorStub(t, w)
	ft.origin = f.origin
	c := f.actors
	c.fetch, c.now, c.built = ft, clock.now, time.Time{}
	return &remoteEnv{tenv: e, c: c, w: w, ft: ft, clock: clock, pub: pub}
}

func (r *remoteEnv) actor(id string) (*remoteActor, error) {
	return r.a.fed.remoteActor(context.Background(), id)
}

// reason is the actorReason of an error, 0 for none or another kind.
func reason(err error) actorReason {
	var ae *actorError
	if errors.As(err, &ae) {
		return ae.Reason
	}
	return 0
}

/* ---------- the cache ---------- */

func TestDBRemoteActorCache(t *testing.T) {
	r := newRemoteEnv(t)
	a, _ := sigTestKeys()
	db := r.a.db
	gid := "https://mastodon.social/users/Gargron"
	r.w.put(gid, worldDoc{body: actorDoc(gid, "Gargron", "Eugen \u202e", &a.PublicKey, map[string]any{
		"summary": "<p>Founder</p>\u0000", "url": "https://mastodon.social/@Gargron", "manuallyApprovesFollowers": true,
		"icon": map[string]any{"type": "Image", "url": "https://mastodon.social/avatar.png"}, "endpoints": map[string]any{"sharedInbox": "https://mastodon.social/inbox"},
		"outbox": gid + "/outbox",
	})})

	// The first lookup fetches, signed by the instance actor, after freezing the origin (4.3).
	if e := r.count(`SELECT count(*) FROM instance_settings WHERE key = 'origin'`); e != 0 {
		t.Fatal("the origin is frozen before anything was signed")
	}
	got, err := r.actor(gid)
	if err != nil {
		t.Fatal(err)
	}
	if r.count(`SELECT count(*) FROM instance_settings WHERE key = 'origin' AND value #>> '{}' = 'https://here.example'`) != 1 {
		t.Error("the first signed request did not freeze the origin")
	}
	if sigs := r.w.signatures(); len(sigs) != 1 || sigs[0] != "" {
		t.Fatalf("the fetch was not signed as the receiver verifies it: %q", sigs)
	}
	want := remoteActor{UserID: got.UserID, ID: gid, Kind: "Person", Username: "Gargron@mastodon.social", Domain: "mastodon.social", Name: "Gargron",
		PreferredUsername: "Gargron", Summary: "<p>Founder</p>", URL: "https://mastodon.social/@Gargron", Icon: "https://mastodon.social/avatar.png",
		Inbox: gid + "/inbox", SharedInbox: "https://mastodon.social/inbox", Outbox: gid + "/outbox", Followers: gid + "/followers", Locked: true, KeyID: gid + "#main-key",
		State: "ok", FetchedAt: r.clock.now(), ExpiresAt: r.clock.now().Add(actorTTL), keyPEM: pemOf(&a.PublicKey)}
	got.FetchedAt, got.ExpiresAt = got.FetchedAt.UTC(), got.ExpiresAt.UTC()
	if *got != want {
		t.Errorf("stored:\n%+v\nwant\n%+v", *got, want)
	}
	if k, err := got.publicKey(keyToVerify); err != nil || !k.Equal(&a.PublicKey) {
		t.Errorf("the kept key does not parse back: %v", err)
	}
	// The users row is inert: no password that can match, a plain role, never the owner, never opted in.
	var pw, role string
	var owner, federates bool
	if err := db.QueryRow(`SELECT pw_hash, role, owner, federates FROM users WHERE id = $1`, got.UserID).Scan(&pw, &role, &owner, &federates); err != nil {
		t.Fatal(err)
	}
	if pw != "!" || role != "user" || owner || federates {
		t.Errorf("the remote row is %q %q owner %v federates %v", pw, role, owner, federates)
	}
	for _, q := range []string{`UPDATE users SET role = 'admin' WHERE id = $1`, `UPDATE users SET pw_hash = 'pbkdf2-sha256$1$x$y' WHERE id = $1`,
		`UPDATE users SET federates = true WHERE id = $1`, `UPDATE users SET username = 'gargron' WHERE id = $1`} {
		if _, err := db.Exec(q, got.UserID); err == nil {
			t.Errorf("%s: the check let it through", q)
		}
	}
	if _, err := r.a.fed.ensureKey(context.Background(), keyOwner(got.UserID)); err == nil {
		t.Error("a remote row was given a key")
	}

	// Within 24 hours the row is the answer; after them it is fetched again.
	r.clock.add(actorTTL - time.Second)
	if _, err := r.actor(gid); err != nil || r.w.hit(gid) != 1 {
		t.Fatalf("a fresh row was fetched again: %d requests, %v", r.w.hit(gid), err)
	}
	r.clock.add(time.Second)
	if got, err = r.actor(gid); err != nil || r.w.hit(gid) != 2 || !got.FetchedAt.Equal(r.clock.now()) {
		t.Fatalf("an expired row was served without a refetch: %d requests, %v", r.w.hit(gid), err)
	}

	// A failed refetch: the stored row, marked stale, and no second try for 10 minutes.
	r.clock.add(actorTTL)
	r.w.put(gid, worldDoc{status: 503, body: []byte("down")})
	got, err = r.actor(gid)
	if err != nil || !got.Stale || r.w.hit(gid) != 3 || got.KeyID != gid+"#main-key" {
		t.Fatalf("a failed refetch: %+v %v, %d requests", got, err, r.w.hit(gid))
	}
	r.clock.add(actorFailFor - time.Second)
	if got, err = r.actor(gid); err != nil || !got.Stale || r.w.hit(gid) != 3 {
		t.Fatalf("the failure was not remembered: %d requests, %v", r.w.hit(gid), err)
	}
	r.clock.add(time.Second)
	r.w.put(gid, worldDoc{body: actorDoc(gid, "Gargron", "Eugen", &a.PublicKey, nil)})
	if got, err = r.actor(gid); err != nil || got.Stale || r.w.hit(gid) != 4 || got.Name != "Eugen" || got.Locked {
		t.Fatalf("after 10 minutes: %+v %v, %d requests", got, err, r.w.hit(gid))
	}

	// A failed verification fetches the key again at most once in 10 minutes per actor (7.4), and a key that
	// changed is taken: the document vouches for it.
	_, b := sigTestKeys()
	r.w.put(gid, worldDoc{body: actorDoc(gid, "Gargron", "Eugen", &b.PublicKey, nil)})
	r.clock.add(actorRecheck - time.Second)
	for range 3 {
		k, err := r.a.fed.remoteKeyFailed(context.Background(), gid+"#main-key")
		if err != nil || r.w.hit(gid) != 4 || k.keyPEM != pemOf(&a.PublicKey) {
			t.Fatalf("a failed verification within 10 minutes fetched again: %d requests, %v", r.w.hit(gid), err)
		}
	}
	r.clock.add(time.Second)
	k, err := r.a.fed.remoteKeyFailed(context.Background(), gid+"#main-key")
	if err != nil || r.w.hit(gid) != 5 || k.keyPEM != pemOf(&b.PublicKey) {
		t.Fatalf("after 10 minutes the rotated key was not taken: %d requests, %v", r.w.hit(gid), err)
	}
	if k, err := r.a.fed.remoteKey(context.Background(), gid+"#main-key", keyToVerify); err != nil || k.UserID != got.UserID || r.w.hit(gid) != 5 {
		t.Fatalf("a cached key was fetched: %d requests, %v", r.w.hit(gid), err)
	}
	if _, err := r.a.fed.remoteKey(context.Background(), gid+"#other-key", keyToVerify); reason(err) != actorNoKey {
		t.Errorf("a key the actor does not name: %v", err)
	}

	// 410 marks a stored actor gone for good; the row stays (its cached key is FWR's for a Delete).
	r.clock.add(actorTTL)
	r.w.put(gid, worldDoc{status: 410})
	before := r.w.hit(gid)
	if got, err = r.actor(gid); err != nil || got.State != "gone" || r.w.hit(gid) != before+1 {
		t.Fatalf("410: %+v %v", got, err)
	}
	r.clock.add(30 * 24 * time.Hour)
	if got, err = r.actor(gid); err != nil || got.State != "gone" || r.w.hit(gid) != before+1 {
		t.Fatalf("a gone actor was fetched again: %+v %v", got, err)
	}

	// Ids never stored: each failure remembered by its kind, nothing written.
	users := r.count(`SELECT count(*) FROM users`)
	big := strings.Repeat("x", actorDocMax)
	for _, c := range []struct {
		id   string
		doc  *worldDoc
		want actorReason
		hits int // requests the stub sees: 0 for an id refused before any is sent
	}{
		{"https://remote.example/nobody", nil, actorNotFound, 1},
		{"https://remote.example/deleted", &worldDoc{status: 410}, actorGone, 1},
		{"https://remote.example/big", &worldDoc{body: []byte(`{"id":"https://remote.example/big","x":"` + big + `"}`)}, actorTooLarge, 1},
		{"https://remote.example/html", &worldDoc{ctype: "text/html", body: []byte("<p>hi</p>")}, actorWrongType, 1},
		{"https://remote.example/json", &worldDoc{ctype: "application/json", body: actorDoc("https://remote.example/json", "j", "", nil, nil)}, actorWrongType, 1},
		{"https://remote.example/broken", &worldDoc{body: []byte(`{"id":`)}, actorUndecodable, 1},
		{"https://remote.example/note", &worldDoc{body: []byte(`{"id":"https://remote.example/note","type":"Note"}`)}, actorUndecodable, 1},
		{"https://remote.example/noinbox", &worldDoc{body: actorDoc("https://remote.example/noinbox", "n", "", nil, map[string]any{"inbox": nil})}, actorUndecodable, 1},
		{"https://remote.example/liar", &worldDoc{body: actorDoc("https://remote.example/other", "l", "", nil, nil)}, actorWrongID, 1},
		{"https://remote.example/away", &worldDoc{status: 302, location: "https://other.example/away"}, actorRedirect, 1},
		{"https://remote.example/down", &worldDoc{status: 500}, actorUnreachable, 1},
		{"https://private.example/x", nil, actorRefused, 0},
		{"https://unknown.example/x", nil, actorUnreachable, 0},
		{"http://remote.example/x", nil, actorBadID, 0},
		{"https://remote.example:8443/x", nil, actorBadID, 0},
		{"https://localhost/x", nil, actorBadID, 0},
		{"https://here.example/users/ana", nil, actorLocal, 0}, // this instance's own origin, never fetched
		{"https://HERE.example:443/actor", nil, actorLocal, 0},
	} {
		if c.doc != nil {
			r.w.put(c.id, *c.doc)
		}
		before := r.w.total()
		_, err := r.actor(c.id)
		if reason(err) != c.want || r.w.total()-before != c.hits {
			t.Errorf("%s: %v (%d requests), want %v (%d)", c.id, err, r.w.total()-before, c.want, c.hits)
			continue
		}
		if !c.want.cached() {
			continue
		}
		// Asked again at once: remembered, no request. After its time: asked again.
		_, err = r.actor(c.id)
		var ae *actorError
		if !errors.As(err, &ae) || !ae.Cached || ae.Reason != c.want || r.w.total()-before != c.hits {
			t.Errorf("%s asked again: %v, %d requests", c.id, err, r.w.total()-before)
		}
	}
	if n := r.count(`SELECT count(*) FROM users`); n != users {
		t.Errorf("failed fetches wrote %d users rows", n-users)
	}
	r.clock.add(actorFailFor)
	before = r.w.hit("https://remote.example/nobody")
	if _, err := r.actor("https://remote.example/nobody"); reason(err) != actorNotFound || r.w.hit("https://remote.example/nobody") != before+1 {
		t.Errorf("after 10 minutes a 404 was not asked again: %v", err)
	}
	if _, err := r.actor("https://remote.example/deleted"); reason(err) != actorGone || r.w.hit("https://remote.example/deleted") != 1 {
		t.Errorf("a 410 was asked again within a day: %v", err)
	}
	// The instance's own origin is refused by name before anything, a key id on it too, and nothing was dialled.
	dials := r.ft.dials.Load()
	if _, err := r.a.fed.remoteKey(context.Background(), "https://here.example/actor#main-key", keyToVerify); reason(err) != actorLocal {
		t.Errorf("our own key id: %v", err)
	}
	if r.w.hit("https://here.example/users/ana") != 0 || r.w.hit("https://here.example/actor") != 0 || r.ft.dials.Load() != dials {
		t.Error("a request went to this instance's own origin")
	}

	// A refused host, from the hook FMD's domain policy fills.
	r.c.refused = func() []string { return []string{"blocked-by-policy.example", "b.example"} }
	r.w.put("https://b.example/u", worldDoc{body: actorDoc("https://b.example/u", "u", "", nil, nil)})
	if _, err := r.actor("https://b.example/u"); reason(err) != actorRefused || r.w.hit("https://b.example/u") != 0 {
		t.Errorf("a refused host was asked: %v", err)
	}
	r.c.refused = nil

	// A same-origin redirect is followed, and the actor is stored under the id it names (the address it came
	// from after the redirect, 7.3).
	r.w.put("https://remote.example/@bo", worldDoc{status: 302, location: "/users/bo"})
	r.w.put("https://remote.example/users/bo", worldDoc{body: actorDoc("https://remote.example/users/bo", "bo", "Bo", &a.PublicKey, nil)})
	if got, err = r.actor("https://remote.example/@bo"); err != nil || got.ID != "https://remote.example/users/bo" {
		t.Fatalf("a same-origin redirect: %+v %v", got, err)
	}

	// Names: the same preferredUsername on one host twice (a name is not an identity), case, a name no
	// username can hold, NUL, another script, a host in its xn-- form.
	for _, c := range []struct{ id, pref, name, user, shown string }{
		{"https://remote.example/users/bo2", "bo", "Bo again", remoteUsername("bo", "remote.example", "https://remote.example/users/bo2", true), "Bo again"},
		{"https://remote.example/users/BO", "BO", "", "BO@remote.example", "BO"},
		{"https://remote.example/users/at", "ana@flowa.example.org", "", remoteUsername("", "remote.example", "https://remote.example/users/at", false), ""},
		{"https://remote.example/users/nul", "n\u0000l", "N", remoteUsername("", "remote.example", "https://remote.example/users/nul", false), "N"},
		{"https://xn--bcher-kva.example/u/ana", "ana", "Bücher Ana", "ana@xn--bcher-kva.example", "Bücher Ana"},
		{"https://remote.example/users/ishii", "石井", "石井健蔵", remoteUsername("", "remote.example", "https://remote.example/users/ishii", false), "石井健蔵"},
	} {
		r.w.put(c.id, worldDoc{body: actorDoc(c.id, c.pref, c.name, nil, nil)})
		got, err := r.actor(c.id)
		if err != nil || got.Username != c.user || got.Name != c.shown || got.Domain != actorHost(c.id) {
			t.Errorf("%s: %+v %v, want %q shown as %q", c.id, got, err, c.user, c.shown)
		}
	}
	if pref := r.count(`SELECT count(*) FROM ap_actors WHERE ap_id = 'https://remote.example/users/nul' AND preferred_username = ''`); pref != 1 {
		t.Error("a preferredUsername holding NUL was kept")
	}

	// An actor that moved: ids never move, so the new id is a new actor, and the old one says where it went.
	r.w.put("https://remote.example/users/old", worldDoc{body: actorDoc("https://remote.example/users/old", "old", "", nil, map[string]any{"movedTo": "https://moved.example/users/new"})})
	r.w.put("https://moved.example/users/new", worldDoc{body: actorDoc("https://moved.example/users/new", "old", "", nil, nil)})
	o, err1 := r.actor("https://remote.example/users/old")
	n, err2 := r.actor("https://moved.example/users/new")
	if err1 != nil || err2 != nil || o.MovedTo != n.ID || o.UserID == n.UserID || n.Username != "old@moved.example" {
		t.Errorf("a moved actor: %+v %+v %v %v", o, n, err1, err2)
	}

	// Suspended (6.3): a known actor keeps what it had and only changes state; an unknown one is stored
	// without its name or summary; an actor that lifts it is read again.
	r.clock.add(actorTTL)
	bo := "https://remote.example/users/bo"
	r.w.put(bo, worldDoc{body: actorDoc(bo, "bo", "Bo the new", nil, map[string]any{"suspended": true, "summary": "new"})})
	if got, err = r.actor(bo); err != nil || got.State != "suspended" || got.Name != "Bo" || got.KeyID == "" {
		t.Errorf("a known actor suspended: %+v %v", got, err)
	}
	r.w.put("https://remote.example/users/eve", worldDoc{body: actorDoc("https://remote.example/users/eve", "eve", "Eve", nil, map[string]any{"suspended": true, "inbox": nil, "summary": "x"})})
	if got, err = r.actor("https://remote.example/users/eve"); err != nil || got.State != "suspended" || got.Name != "" || got.Summary != "" {
		t.Errorf("an unknown actor suspended: %+v %v", got, err)
	}
	r.clock.add(actorTTL)
	r.w.put(bo, worldDoc{body: actorDoc(bo, "bo", "Bo the new", nil, nil)})
	if got, err = r.actor(bo); err != nil || got.State != "ok" || got.Name != "Bo the new" || got.KeyID != "" {
		t.Errorf("an actor whose suspension was lifted: %+v %v", got, err)
	}

	// Keys (7.3): a key owned by another actor, or on another origin, is never kept; a key at a path of its
	// own is followed to its owner, which must name it back.
	for _, c := range []struct {
		id   string
		key  map[string]any
		kept string
	}{
		{"https://remote.example/users/k1", map[string]any{"id": "https://remote.example/users/k1#main-key", "owner": "https://remote.example/users/someone", "publicKeyPem": pemOf(&a.PublicKey)}, ""},
		{"https://remote.example/users/k2", map[string]any{"id": "https://other.example/users/k2#main-key", "owner": "https://remote.example/users/k2", "publicKeyPem": pemOf(&a.PublicKey)}, ""},
		{"https://remote.example/users/k3", map[string]any{"id": "https://remote.example/users/k3#main-key", "controller": "https://remote.example/users/k3", "publicKeyPem": pemOf(&a.PublicKey)}, "https://remote.example/users/k3#main-key"},
		{"https://remote.example/users/k4", map[string]any{"id": "https://remote.example/users/k4#main-key", "owner": "https://remote.example/users/k4", "publicKeyPem": "not a key"}, ""},
	} {
		r.w.put(c.id, worldDoc{body: actorDoc(c.id, "k", "", nil, map[string]any{"publicKey": c.key})})
		if got, err := r.actor(c.id); err != nil || got.KeyID != c.kept {
			t.Errorf("%s: kept %q, want %q (%v)", c.id, got.KeyID, c.kept, err)
		}
	}
	kdoc, _ := os.ReadFile("testdata/federation/actors/swicg-key-object.json")
	odoc, _ := os.ReadFile("testdata/federation/actors/swicg-key-owner.json")
	r.w.put("https://social.example/keys/alyssa", worldDoc{body: kdoc})
	r.w.put("https://social.example/alyssa/", worldDoc{body: odoc})
	if k, err := r.a.fed.remoteKey(context.Background(), "https://social.example/keys/alyssa", keyToVerify); err != nil || k.ID != "https://social.example/alyssa/" || k.KeyID != "https://social.example/keys/alyssa" {
		t.Errorf("a key at a path of its own: %+v %v", k, err)
	}
	// A key document whose owner does not name it back, or is on another origin, vouches for nobody.
	r.w.put("https://remote.example/keys/stray", worldDoc{body: []byte(`{"id":"https://remote.example/keys/stray","type":"Key","owner":"https://remote.example/users/bo","publicKeyPem":` + strings.TrimSpace(string(must(json.Marshal(pemOf(&a.PublicKey))))) + `}`)})
	r.w.put("https://remote.example/keys/far", worldDoc{body: []byte(`{"id":"https://remote.example/keys/far","type":"Key","owner":"https://other.example/u","publicKeyPem":` + string(must(json.Marshal(pemOf(&a.PublicKey)))) + `}`)})
	for _, kid := range []string{"https://remote.example/keys/stray", "https://remote.example/keys/far"} {
		if _, err := r.a.fed.remoteKey(context.Background(), kid, keyToVerify); reason(err) != actorNoKey {
			t.Errorf("%s: %v", kid, err)
		}
	}

	// What a cached actor costs in the database: its two rows, against the 2 KB of section 12.
	var bytes float64
	if err := db.QueryRow(`SELECT pg_column_size(a.*) + pg_column_size(u.*) FROM ap_actors a JOIN users u ON u.id = a.user_id WHERE a.ap_id = $1`, gid).Scan(&bytes); err != nil {
		t.Fatal(err)
	}
	t.Logf("a cached actor with a 2048-bit key: %.0f bytes in its two rows", bytes)
	if bytes > 2048 {
		t.Errorf("a cached actor takes %.0f bytes, more than the 2 KB section 12 budgets", bytes)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// One fetch per actor at a time: every caller of an id waits for the fetch in flight, however many ask at
// once, and a caller that gives up does not fail the others.
func TestDBRemoteActorFlight(t *testing.T) {
	r := newRemoteEnv(t)
	id := "https://remote.example/users/herd"
	r.w.put(id, worldDoc{body: actorDoc(id, "herd", "Herd", nil, nil)})
	release := r.w.holdAt(id)
	defer release()
	joined := make(chan bool, 16)
	r.c.joined = func(_ string, lead bool) { joined <- lead }
	const callers = 8
	type answer struct {
		row *remoteActor
		err error
	}
	answers := make(chan answer, callers)
	gaveUp, giveUp := context.WithCancel(context.Background())
	go func() { _, err := r.a.fed.remoteActor(gaveUp, id); answers <- answer{nil, err} }()
	<-r.w.arrived // the fetch is at the stub, held
	for range callers - 1 {
		go func() { row, err := r.actor(id); answers <- answer{row, err} }()
	}
	leads := 0
	for range callers {
		if <-joined {
			leads++
		}
	}
	giveUp()
	if a := <-answers; reason(a.err) != actorCanceled {
		t.Fatalf("the caller that gave up: %v", a.err)
	}
	release()
	for range callers - 1 {
		a := <-answers
		if a.err != nil || a.row.Name != "Herd" {
			t.Errorf("a caller waiting for the fetch: %+v %v", a.row, a.err)
		}
	}
	if leads != 1 || r.w.hit(id) != 1 {
		t.Errorf("%d callers started a fetch and the stub saw %d requests, want 1 and 1", leads, r.w.hit(id))
	}
}

// The cache holds at most max actors: the least recently fetched go first, never one something points at.
func TestDBRemoteActorBound(t *testing.T) {
	r := newRemoteEnv(t)
	r.c.max = 4
	r.user("ana", "user")
	ids := make([]string, 8)
	for i := range ids {
		ids[i] = fmt.Sprintf("https://remote.example/users/u%d", i)
		r.w.put(ids[i], worldDoc{body: actorDoc(ids[i], fmt.Sprintf("u%d", i), "", nil, nil)})
	}
	got := map[string]*remoteActor{}
	for i, id := range ids[:4] {
		row, err := r.actor(id)
		if err != nil {
			t.Fatal(err)
		}
		got[id] = row
		r.clock.add(time.Minute)
		if i == 0 {
			// The oldest is followed by a local member: it stays, whatever its age.
			if _, err := r.a.db.Exec(`INSERT INTO follows (follower_id, followee_id) SELECT id, $1 FROM users WHERE username = 'ana'`, row.UserID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if n := r.count(`SELECT count(*) FROM ap_actors`); n != 4 {
		t.Fatalf("%d actors cached, want 4", n)
	}
	// The fifth is one over: one over the bound and a hundredth of it (at least one) go, the oldest first,
	// skipping the followed one.
	if _, err := r.actor(ids[4]); err != nil {
		t.Fatal(err)
	}
	var left []string
	rows, err := r.a.db.Query(`SELECT ap_id FROM ap_actors ORDER BY fetched_at`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		rows.Scan(&s)
		left = append(left, s)
	}
	rows.Close()
	if want := []string{ids[0], ids[3], ids[4]}; !slices.Equal(left, want) {
		t.Errorf("left %v, want %v", left, want)
	}
	if n := r.count(`SELECT count(*) FROM users WHERE domain <> ''`); n != 3 {
		t.Errorf("%d remote users rows for 3 cached actors", n)
	}
	for _, id := range ids[5:] {
		if _, err := r.actor(id); err != nil {
			t.Fatal(err)
		}
	}
	if n := r.count(`SELECT count(*) FROM ap_actors`); n > r.c.max {
		t.Errorf("%d actors cached, over the bound of %d", n, r.c.max)
	}
}

// Nothing is signed or fetched before the instance actor has its key, or while the clock is earlier than
// the build; neither is remembered as the actor's failure.
func TestDBRemoteActorNotReady(t *testing.T) {
	hold := make(chan struct{})
	real := generateKey
	generateKey = func() (*rsa.PrivateKey, error) { <-hold; return real() }
	t.Cleanup(func() { generateKey = real })
	e := newEnv(t)
	if err := e.fedStart(map[string]string{"FLOWA_ORIGIN": "https://here.example"}); err != nil {
		t.Fatal(err)
	}
	f := e.a.fed
	clock := &tclock{t: time.Now().UTC().Truncate(time.Second)}
	w := newWorld(clock.now)
	ft := actorStub(t, w)
	ft.origin = f.origin
	f.actors.fetch, f.actors.now, f.actors.built = ft, clock.now, time.Time{}
	id := "https://remote.example/users/x"
	w.put(id, worldDoc{body: actorDoc(id, "x", "", nil, nil)})
	if _, err := f.remoteActor(context.Background(), id); reason(err) != actorNotReady || w.total() != 0 {
		t.Fatalf("without the instance actor's key: %v, %d requests", err, w.total())
	}
	done := make(chan struct{})
	f.handled = func(keyOwner, bool, error) { close(done) }
	close(hold)
	<-done
	f.actors.built = clock.now().Add(time.Hour)
	if _, err := f.remoteActor(context.Background(), id); reason(err) != actorClock || w.total() != 0 {
		t.Fatalf("with the clock before the build: %v, %d requests", err, w.total())
	}
	f.actors.built = time.Time{}
	if row, err := f.remoteActor(context.Background(), id); err != nil || w.total() != 1 {
		t.Fatalf("once ready: %+v %v", row, err)
	}
	// Federation refused (a moved origin): nothing more is fetched.
	f.refuse("federation refused: a test")
	clock.add(actorTTL)
	if row, err := f.remoteActor(context.Background(), id); err != nil || !row.Stale || w.total() != 1 {
		t.Fatalf("while refused: %+v %v, %d requests", row, err, w.total())
	}
}

// Every actor fixture, served at its own id, through the cache.
func TestDBRemoteFixtures(t *testing.T) {
	r := newRemoteEnv(t)
	for _, p := range fixtureFiles(t) {
		b, _ := os.ReadFile(p)
		var head struct{ ID string }
		if json.Unmarshal(b, &head) == nil && head.ID != "" {
			r.w.put(head.ID, worldDoc{body: b})
		}
	}
	for id, want := range map[string]string{
		"https://mastodon.social/users/Gargron":  "Gargron@mastodon.social key https://mastodon.social/users/Gargron#main-key ok",
		"https://social.example/alyssa/":         "alyssa@social.example key https://social.example/keys/alyssa ok", // its key is a document of its own, followed at every fetch
		"https://kenzoishii.example.com/":        "kenzoishii@kenzoishii.example.com key  ok",
		"https://example.com/@eve":               remoteUsername("", "example.com", "https://example.com/@eve", false) + " key  suspended",
		"https://flowa.example.org/users/ana":    "ana@flowa.example.org key  ok",
		"https://flowa.example.org/actor":        "flowa.example.org@flowa.example.org key  ok",
		"https://mastodon.social/users/Gargron2": "not found",
	} {
		row, err := r.actor(id)
		got := ""
		if err != nil {
			got = reason(err).String()
		} else {
			got = row.Username + " key " + row.KeyID + " " + row.State
		}
		if got != want {
			t.Errorf("%s: %s, want %s", id, got, want)
		}
	}
	if k, err := r.a.fed.remoteKey(context.Background(), "https://social.example/keys/alyssa", keyToVerify); err != nil || k.KeyID != "https://social.example/keys/alyssa" {
		t.Errorf("the key at a path of its own: %+v %v", k, err)
	}
	if sigs := r.w.signatures(); slices.ContainsFunc(sigs, func(s string) bool { return s != "" }) {
		t.Errorf("a fetch was not signed as the receiver verifies it: %q", sigs)
	}
}

// One Flowa instance fetches the actor of a member of another, signed, through the real fetcher, and the
// other's server checks the signature as it would (verifyRequest with the first instance's key; requiring
// one is authorized fetch, F11, which is not this item's), then answers from its real routes.
func TestDBTwoFlowaActors(t *testing.T) {
	// The second instance, at https://b.example, with a member who opted in.
	be := newEnv(t)
	if err := be.fedStart(map[string]string{"FLOWA_ORIGIN": "https://b.example"}); err != nil {
		t.Fatal(err)
	}
	anaID, anaTok := be.user("ana", "user")
	be.must(200, anaTok, "PUT", "/api/me", map[string]any{"federates": true, "displayName": "Ana B"})
	if _, err := be.a.fed.ensureKey(context.Background(), keyOwner(anaID)); err != nil {
		t.Fatal(err)
	}
	var anaPEM string
	if err := be.a.db.QueryRow(`SELECT public_key FROM ap_keys WHERE user_id = $1`, anaID).Scan(&anaPEM); err != nil {
		t.Fatal(err)
	}
	// The first instance, at https://here.example.
	r := newRemoteEnv(t)
	var verified []string
	var mu sync.Mutex
	check := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		v, err := newSigVerifier("https://b.example", r.w.keyFor)
		if err != nil {
			t.Error(err)
			return
		}
		v.built, v.now = time.Time{}, r.clock.now
		res, err := v.verifyRequest(req.Context(), req, nil)
		mu.Lock()
		if err != nil {
			verified = append(verified, err.Error())
		} else {
			verified = append(verified, res.KeyID)
		}
		mu.Unlock()
		be.h.ServeHTTP(w, req)
	})
	ft := actorStub(t, check)
	ft.origin = r.a.fed.origin
	r.c.fetch = ft
	got, err := r.actor("https://b.example/users/ana")
	if err != nil {
		t.Fatal(err)
	}
	if len(verified) != 1 || verified[0] != hereKeyID {
		t.Fatalf("b.example verified %q, want the signature of %s", verified, hereKeyID)
	}
	if got.Username != "ana@b.example" || got.Domain != "b.example" || got.KeyID != "https://b.example/users/ana#main-key" || got.keyPEM != anaPEM ||
		got.Inbox != "https://b.example/users/ana/inbox" || got.Kind != "Person" || got.Name != "Ana B" {
		t.Errorf("stored %+v", got)
	}
	// Its key verifies a request ana's server signs, as the inbox will check it.
	if k, err := r.a.fed.remoteKey(context.Background(), "https://b.example/users/ana#main-key", keyToVerify); err != nil || k.UserID != got.UserID {
		t.Errorf("the key by its id: %+v %v", k, err)
	}
	// The second instance's own lists know nothing of the first, and the first lists ana nowhere.
	if n := r.count(`SELECT count(*) FROM users WHERE domain = 'b.example'`); n != 1 {
		t.Errorf("%d rows for ana@b.example", n)
	}
}

/* ---------- surfaces that must not see remote rows ---------- */

// remoteRows inserts remote accounts as the cache writes them, chosen to be as visible as a row can be:
// names that are local names with a host, a capital, a host in its xn-- form, the most XP, signed in
// just now, a picture, and a session.
func remoteRows(t *testing.T, db *sql.DB) []int64 {
	t.Helper()
	var ids []int64
	for i, n := range []struct{ name, host string }{
		{"ana", "remote.example"}, {"Bo", "remote.example"}, {"boss", "remote.example"}, {"ana", "xn--bcher-kva.example"}, {"", "other.example"},
	} {
		id := fmt.Sprintf("https://%s/users/%s%d", n.host, n.name, i)
		var uid int64
		err := db.QueryRow(`INSERT INTO users (username, display_name, pw_hash, domain, stats, last_seen, avatar, bio, props, onboarded)
			VALUES ($1, $2, '!', $3, '{"xp": 999999}', now(), 'data:image/png;base64,AAAA', 'remote', '["poi"]', true) RETURNING id`,
			remoteUsername(n.name, n.host, id, false), "Remote "+n.name, n.host).Scan(&uid)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO ap_actors (user_id, ap_id, kind, inbox, fetched_at, expires_at) VALUES ($1, $2, 'Person', $2 || '/inbox', now(), now() + interval '1 day')`, uid, id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, uid)
	}
	return ids
}

// TestDBRemoteRowsStayRemote holds every surface that lists or looks up members to local ones: with
// remote rows present, each answers exactly what it answered without them, byte for byte, and a remote row
// can never sign in, hold a session, be acted on as an account by an admin, be mentioned or followed.
func TestDBRemoteRowsStayRemote(t *testing.T) {
	e := newEnv(t)
	for _, f := range []string{"community", "follows", "moderation"} {
		e.setFlag(f, true)
	}
	bossID, boss := e.user("boss", "admin")
	if _, err := e.a.db.Exec(`UPDATE users SET owner = true WHERE id = $1`, bossID); err != nil {
		t.Fatal(err)
	}
	anaID, ana := e.user("ana", "user")
	_, bo := e.user("bo", "user")
	e.must(200, ana, "PUT", "/api/me", map[string]any{"bio": "local ana"})
	e.post(bo, "a local post")
	e.must(200, ana, "PUT", "/api/users/bo/follow", nil)
	type req struct {
		tok, method, path string
		body              any
	}
	none := int64(987654)
	surfaces := func(remote []int64) []req {
		rid := none
		if len(remote) > 0 {
			rid = remote[0]
		}
		return []req{
			{ana, "GET", "/api/users", nil},
			{boss, "GET", "/api/users", nil},
			{ana, "GET", "/api/users/ana", nil},
			{ana, "GET", "/api/users/ana@remote.example", nil},
			{ana, "GET", "/api/users/Bo@remote.example", nil},
			{ana, "GET", "/api/users/bo@remote.example", nil},
			{ana, "GET", "/api/posts?user=ana@remote.example", nil},
			{ana, "GET", "/api/posts?user=boss@remote.example", nil},
			{ana, "GET", "/api/followers", nil},
			{ana, "GET", "/api/following", nil},
			{ana, "GET", "/api/blocks", nil},
			{ana, "PUT", "/api/users/ana@remote.example/follow", nil},
			{ana, "PUT", "/api/users/boss@remote.example/block", nil},
			{ana, "DELETE", "/api/users/ana@remote.example/follow", nil},
			{boss, "GET", "/api/admin/users", nil},
			{boss, "GET", "/api/admin/dashboard", nil},
			{boss, "DELETE", fmt.Sprintf("/api/admin/users/%d", rid), nil},
			{boss, "PUT", fmt.Sprintf("/api/admin/users/%d/role", rid), map[string]any{"role": "moderator"}},
			{boss, "PUT", fmt.Sprintf("/api/admin/users/%d/password", rid), map[string]any{"next": "a fine long password"}},
			{boss, "PUT", fmt.Sprintf("/api/admin/users/%d/owner", rid), nil},
			{ana, "GET", fmt.Sprintf("/api/users/%d/avatar", rid), nil},
			{"", "POST", "/api/signup", map[string]any{"username": "ana@remote.example", "password": "a fine long password"}},
			{"", "POST", "/api/signin", map[string]any{"username": "ana@remote.example", "password": "a fine long password"}},
			{"", "POST", "/api/signin", map[string]any{"username": "Bo@remote.example", "password": "!"}},
		}
	}
	// Each answer is labelled with its path, the account id in it written ID: the one thing that differs
	// between the two runs is which id is asked for (none, then a remote row's), and the answer must not
	// show either.
	answers := func(remote []int64) []string {
		var out []string
		rid := none
		if len(remote) > 0 {
			rid = remote[0]
		}
		for _, q := range surfaces(remote) {
			runs := pbkdf2Runs.Load()
			code, body := e.do(q.tok, q.method, q.path, q.body)
			out = append(out, fmt.Sprintf("%s %s: %d %s (hashes %d)", q.method, strings.Replace(q.path, fmt.Sprint(rid), "ID", 1), code, body["_raw"], pbkdf2Runs.Load()-runs))
		}
		return out
	}
	without := answers(nil)
	remote := remoteRows(t, e.a.db)
	with := answers(remote)
	for i := range without {
		if with[i] != without[i] {
			t.Errorf("with remote rows the answer changed:\nwithout %s\n   with %s", without[i], with[i])
		}
	}
	// A session for a remote row, as an older build or a bug might write: never a signed-in member, the
	// same answer as a token nobody has.
	tok := e.session(remote[0])
	_, nobody := e.do(randomToken(), "GET", "/api/me", nil)
	if _, me := e.do(tok, "GET", "/api/me", nil); me["_raw"] != nobody["_raw"] {
		t.Errorf("a session of a remote row is signed in: %s", me["_raw"])
	}
	// A mention never reaches one, whatever the name.
	e.must(200, ana, "POST", "/api/posts", map[string]any{"body": "hi @ana@remote.example @Bo@remote.example @boss"})
	if n := e.count(`SELECT count(*) FROM notifications n JOIN users u ON u.id = n.user_id WHERE u.domain <> ''`); n != 0 {
		t.Errorf("%d notifications for remote rows", n)
	}
	// Reports and moderation by id take a remote account as F5 and 9.2 say they will (moderating a remote
	// account works unchanged once one can be met): a member's report of one is taken and a moderator can mute
	// it, as for a local account; the muted row is then never removed by the cache's bound
	// (TestDBEvictionCoversReferences). No remote account has a post or a comment in this version, so the
	// comment routes, which take a post's or a comment's id, cannot meet one; FWR's items, which store them,
	// hold those routes.
	_, mo := e.user("mo", "moderator")
	e.must(200, ana, "POST", "/api/reports", map[string]any{"kind": "user", "id": remote[1], "reason": "spam"})
	e.must(200, mo, "POST", "/api/mod/actions", map[string]any{"action": "mute", "id": remote[1], "reason": "spam", "until": time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)})
	if n := e.count(`SELECT count(*) FROM users WHERE id = $1 AND muted_until > now() AND domain <> ''`, remote[1]); n != 1 {
		t.Error("a moderator's mute of a remote account was not kept")
	}
	// Housekeeping leaves them, and touches nothing else differently.
	if _, err := e.a.housekeep(); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM users WHERE domain <> ''`); n != len(remote) {
		t.Errorf("housekeeping removed %d remote rows", len(remote)-n)
	}
	// Keys: the start asks for none, and none can be made.
	if _, err := e.a.db.Exec(`UPDATE users SET federates = true WHERE id = $1`, anaID); err != nil {
		t.Fatal(err)
	}
	rows, err := e.a.db.Query(missingKeysSQL, 100)
	if err != nil {
		t.Fatal(err)
	}
	var asked []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		asked = append(asked, id)
	}
	rows.Close()
	if !slices.Equal(asked, []int64{anaID}) {
		t.Errorf("keys asked for %v, want only ana %d", asked, anaID)
	}

	// In a mode that federates: NodeInfo's counts, WebFinger, the actor documents, the outbox, the public
	// pages; each as without remote rows.
	fe := newEnv(t)
	if err := fe.fedStart(nil); err != nil {
		t.Fatal(err)
	}
	fanaID, fana := fe.user("ana", "user")
	fe.must(200, fana, "PUT", "/api/me", map[string]any{"federates": true})
	if _, err := fe.a.fed.ensureKey(context.Background(), keyOwner(fanaID)); err != nil {
		t.Fatal(err)
	}
	fed := func() []string {
		var out []string
		fe.a.nodeAt = time.Time{} // read again, not the five minutes kept
		for _, p := range []string{"/nodeinfo/2.1", "/.well-known/webfinger?resource=acct:ana@flowa.example.org", "/.well-known/webfinger?resource=acct:ana@remote.example",
			"/users/ana", "/users/ana@remote.example", "/users/ana/outbox", "/users/ana@remote.example/outbox", "/users/ana%40remote.example"} {
			w := ask(fe.h, "GET", p, "", map[string]string{"Accept": apAccept})
			out = append(out, fmt.Sprintf("%s: %d %s", p, w.Code, w.Body))
		}
		return out
	}
	fwithout := fed()
	remoteRows(t, fe.a.db)
	if fwith := fed(); !slices.Equal(fwith, fwithout) {
		for i := range fwith {
			if fwith[i] != fwithout[i] {
				t.Errorf("in federated mode with remote rows:\nwithout %s\n   with %s", fwithout[i], fwith[i])
			}
		}
	}

	// Single mode starts with one local account and remote rows beside it (F15 counts local accounts), and
	// the owner's page is the owner's.
	se := newEnv(t)
	seOwner, _ := se.user("solo", "admin")
	if _, err := se.a.db.Exec(`UPDATE users SET owner = true WHERE id = $1`, seOwner); err != nil {
		t.Fatal(err)
	}
	remoteRows(t, se.a.db)
	if err := se.singleStart(map[string]string{"FLOWA_MODE": "single"}, nil); err != nil {
		t.Fatalf("single mode with remote rows: %v", err)
	}
	if code, body, _ := se.visit("GET", "/", "", 1, nil); code != 200 || !strings.Contains(body, "solo") || strings.Contains(body, "Remote") {
		t.Errorf("the owner's page: %d %.200s", code, body)
	}
}

/* ---------- -purge-remote ---------- */

func TestDBPurgeRemote(t *testing.T) {
	e := newEnv(t)
	bossID, boss := e.user("boss", "admin")
	anaID, ana := e.user("ana", "user")
	p := e.post(ana, "a local post")
	remote := remoteRows(t, e.a.db)
	r0, r1 := remote[0], remote[1]
	// What later items let hang off a remote account, written as they would be.
	for _, q := range []string{
		`INSERT INTO posts (user_id, body) VALUES (%[1]d, 'a remote post')`,
		`INSERT INTO comments (post_id, user_id, body) VALUES (%[3]d, %[1]d, 'a remote comment')`,
		`INSERT INTO likes (post_id, user_id) VALUES (%[3]d, %[1]d)`,
		`INSERT INTO follows (follower_id, followee_id) VALUES (%[2]d, %[1]d)`,
		`INSERT INTO follows (follower_id, followee_id) VALUES (%[1]d, %[2]d)`,
		`INSERT INTO blocks (user_id, target_id) VALUES (%[2]d, %[4]d)`,
		`INSERT INTO notifications (user_id, actor_id, kind, post_id) VALUES (%[2]d, %[1]d, 'like', %[3]d)`,
		`INSERT INTO reports (reporter_id, target_kind, target_id, reason) VALUES (%[1]d, 'post', %[3]d, 'spam')`,
		`INSERT INTO mod_actions (actor_id, action, target_kind, target_id, target_user_id, reason) VALUES (%[5]d, 'mute', 'user', %[1]d, %[1]d, 'test')`,
	} {
		q = fmt.Sprintf(q, r0, anaID, p, r1, bossID)
		if _, err := e.a.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	local := func() string {
		var s string
		if err := e.a.db.QueryRow(`SELECT concat_ws(' ', (SELECT count(*) FROM users WHERE domain = ''), (SELECT count(*) FROM posts p JOIN users u ON u.id = p.user_id WHERE u.domain = ''),
			(SELECT count(*) FROM sessions), (SELECT count(*) FROM mod_actions))`).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	before := local()
	var out strings.Builder
	if err := purgeWith(e.a.db, &out); err != nil {
		t.Fatal(err)
	}
	want := "removed 5 remote accounts, and with them 5 cached actor documents, 1 posts, 1 comments, 1 likes, 0 comment likes, 2 follows, 1 blocks, 1 notifications, 1 reports. Local accounts and their posts are as they were.\n"
	if out.String() != want {
		t.Errorf("printed %q\nwant    %q", out.String(), want)
	}
	if n := e.count(`SELECT count(*) FROM users WHERE domain <> ''`) + e.count(`SELECT count(*) FROM ap_actors`) + e.count(`SELECT count(*) FROM follows`) +
		e.count(`SELECT count(*) FROM likes`) + e.count(`SELECT count(*) FROM comments`) + e.count(`SELECT count(*) FROM notifications`) + e.count(`SELECT count(*) FROM reports`) + e.count(`SELECT count(*) FROM blocks`); n != 0 {
		t.Errorf("%d rows left behind", n)
	}
	if got := local(); got != before {
		t.Errorf("local rows changed: %s, were %s", got, before)
	}
	if e.count(`SELECT count(*) FROM mod_actions WHERE target_user_id IS NULL AND action = 'mute'`) != 1 {
		t.Error("the audit line went, or still names the account")
	}
	e.must(200, ana, "GET", "/api/posts", nil)
	e.must(200, boss, "GET", "/api/admin/users", nil)
	// Again: nothing to remove, and it says so.
	out.Reset()
	if err := purgeWith(e.a.db, &out); err != nil || !strings.HasPrefix(out.String(), "removed 0 remote accounts, and with them 0 cached actor documents, 0 posts") {
		t.Errorf("the second run: %q %v", out.String(), err)
	}
	if err := purgeCommand(func(string) string { return "" }, &out); err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("without a database: %v", err)
	}
}

/* ---------- budgets ---------- */

// benchRemote is a federation on the test database with the instance actor's key, and a stub serving one
// actor, for the benchmarks: no app, no routes.
func benchRemote(b *testing.B) (*actorCache, *tclock, string) {
	db := testDB(b)
	o, _ := parseOrigin("https://here.example")
	s, _ := parseKeySecret(testSecret)
	f := newFederation(db, fedSettings{on: true, origin: o, secret: s})
	b.Cleanup(f.cancel)
	a, _ := sigTestKeys()
	f.gen = func() (*rsa.PrivateKey, error) { return a, nil }
	if _, err := f.ensureKey(context.Background(), instanceActor); err != nil {
		b.Fatal(err)
	}
	f.recorded.Store(true)
	clock := &tclock{t: time.Now().UTC()}
	w := newWorld(clock.now)
	id := "https://mastodon.social/users/Gargron"
	w.put(id, worldDoc{body: actorDoc(id, "Gargron", "Eugen", &a.PublicKey, map[string]any{"summary": "<p>Founder of Mastodon.</p>", "url": "https://mastodon.social/@Gargron"})})
	ft := actorStub(b, w)
	ft.origin = o
	f.actors = newActorCache(f, ft)
	f.actors.now, f.actors.built = clock.now, time.Time{}
	if _, err := f.remoteActor(context.Background(), id); err != nil {
		b.Fatal(err)
	}
	return f.actors, clock, id
}

// BenchmarkRemoteActorFetch is a lookup of an expired actor: the row read, the document fetched signed
// over a kept-alive TLS connection to the local stub (whose own work is counted), decoded, checked and
// written back.
func BenchmarkRemoteActorFetch(b *testing.B) {
	c, clock, id := benchRemote(b)
	b.ReportAllocs()
	for b.Loop() {
		clock.add(actorTTL)
		if _, err := c.fed.remoteActor(context.Background(), id); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRemoteActorCached is a lookup the cache answers: one indexed read.
func BenchmarkRemoteActorCached(b *testing.B) {
	c, _, id := benchRemote(b)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := c.fed.remoteActor(context.Background(), id); err != nil {
			b.Fatal(err)
		}
	}
}
