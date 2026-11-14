package main

// The inbox and its queue (docs/federation.md, 6.3, 7, 8.1, 8.5, 8.6, 9.3 and 12; FWR item 1): deliveries
// signed as other servers sign them, to a federated instance at https://here.example whose actor cache asks the
// stub world of remote_test.go, on a clock the test moves. The workers are driven by the test (inboxQueue.step),
// never started, so no test waits on a goroutine that polls the database.

import (
	"bytes"
	"container/list"
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// inboxEnv is a federated instance taking deliveries: the Federation switch on, social.example and its
// subdomains allowed, the verifier and the queue on the test's clock, and bo@social.example an actor the stub
// world serves with the first test key.
type inboxEnv struct {
	*remoteEnv
	q     *inboxQueue
	key   *rsa.PrivateKey // bo's
	other *rsa.PrivateKey // another key, for a rotation or a stranger
	heard []inboxReason
	mu    sync.Mutex
	from  string // the client address deliveries come from, when not the usual one
	// onServer, when set, changes the request as the server receives it (its body, its length), after post has
	// made it from the signed client request.
	onServer func(*http.Request)
}

const (
	boID    = "https://social.example/users/bo"
	boKeyID = boID + "#main-key"
)

func newInboxEnv(t *testing.T) *inboxEnv {
	t.Helper()
	r := newRemoteEnv(t)
	r.setFlag(fedFlag, true)
	f := r.a.fed
	f.verifier.now, f.verifier.built = r.clock.now, time.Time{}
	f.inbox.now, f.inbox.manual = r.clock.now, true
	a, b := sigTestKeys()
	e := &inboxEnv{remoteEnv: r, q: f.inbox, key: a, other: b}
	f.inbox.heard = func(why inboxReason) { e.mu.Lock(); e.heard = append(e.heard, why); e.mu.Unlock() }
	e.mustExec(`UPDATE instance_settings SET value = '"allowlist"' WHERE key = 'federation_policy'`)
	e.allow("social.example")
	r.w.put(boID, worldDoc{body: actorDoc(boID, "bo", "Bo", &a.PublicKey, nil)})
	return e
}

func (e *inboxEnv) allow(domains ...string) {
	e.t.Helper()
	for _, d := range domains {
		e.mustExec(`INSERT INTO domain_policies (domain, policy) VALUES ($1, 'allow') ON CONFLICT (domain) DO UPDATE SET policy = 'allow'`, d)
	}
	if err := e.a.fed.loadPolicy(context.Background()); err != nil {
		e.t.Fatal(err)
	}
}

// last is how the latest delivery was answered.
func (e *inboxEnv) last() inboxReason {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.heard) == 0 {
		return 255
	}
	return e.heard[len(e.heard)-1]
}

// activity is a JSON body with an id under the actor, a type and an object; more adds or (with nil) removes.
func activity(actor, kind string, object any, more map[string]any) []byte {
	d := map[string]any{"@context": "https://www.w3.org/ns/activitystreams", "id": actor + "/activities/" + strconv.FormatInt(time.Now().UnixNano(), 36),
		"type": kind, "actor": actor, "object": object}
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

// post sends a delivery as another server would: to path on here.example, signed by sign (nil: unsigned), with
// more applied last, from one address.
func (e *inboxEnv) post(path string, body []byte, sign signer, more func(*http.Request)) *httptest.ResponseRecorder {
	e.t.Helper()
	from := "198.51.100.7:4000"
	if e.from != "" {
		from = e.from
	}
	return e.postFrom(from, path, body, sign, more)
}

// postFrom is post from a given client address ("203.0.113.9:1").
func (e *inboxEnv) postFrom(from, path string, body []byte, sign signer, more func(*http.Request)) *httptest.ResponseRecorder {
	e.t.Helper()
	req, err := http.NewRequest("POST", "https://here.example"+path, bytes.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Content-Type", asType)
	if sign != nil {
		if err := sign(req, body); err != nil {
			e.t.Fatal(err)
		}
	}
	if more != nil {
		more(req)
	}
	in := received(req, body)
	in.RemoteAddr = from
	if e.onServer != nil {
		e.onServer(in)
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, in)
	return w
}

func (e *inboxEnv) signer(key *rsa.PrivateKey, keyID string, form sigForm) signer {
	return httpSigner(key, keyID, form, e.clock.now, time.Time{})
}

// bo signs as bo, in the draft form.
func (e *inboxEnv) bo() signer { return e.signer(e.key, boKeyID, sigCavage) }

func (e *inboxEnv) queued() int {
	return e.count(`SELECT count(*) FROM ap_inbox WHERE state = 'queued'`)
}

// Every refusal of the inbox, with its status and its reason (federation.md 7.1 to 7.5 and 7.7, FWR 1's DoD),
// and what each costs: the refusals before the signature read nothing and fetch nothing.
func TestInboxRefusals(t *testing.T) {
	e := newInboxEnv(t)
	now := func() string { return e.clock.now().UTC().Format(http.TimeFormat) }
	body := activity(boID, "Like", "https://here.example/users/ana", nil)
	// The first delivery fetches bo's key, signed by the instance actor; the rest find it kept.
	if w := e.post("/inbox", body, e.bo(), nil); w.Code != 202 || e.last() != inDropped {
		t.Fatalf("a Follow from bo: %d %s (%v)", w.Code, w.Body, e.last())
	}
	if n := e.w.hit(boID); n != 1 {
		t.Fatalf("bo's actor was fetched %d times", n)
	}
	cavage := func(headers string) signer {
		return func(req *http.Request, b []byte) error {
			req.Header.Set("Date", now())
			req.Header.Set("Digest", digestHeader(b))
			req.Host = "here.example"
			cavageWith(t, req, e.key, boKeyID, "rsa-sha256", strings.Fields(headers), "")
			return nil
		}
	}
	rfc := func(input string) signer {
		return func(req *http.Request, b []byte) error {
			req.Header.Set("Content-Digest", contentDigestHeader(b))
			rfc9421With(t, req, e.key, input)
			return nil
		}
	}
	created := func() string { return strconv.FormatInt(e.clock.now().Unix(), 10) }
	deep := `{"a":` + strings.Repeat(`[`, 30) + strings.Repeat(`]`, 30) + `}`
	wide := `[` + strings.Repeat(`"x",`, 1100) + `"x"]`
	replayed := activity(boID, "Like", "https://here.example/users/ana/posts/1", nil)
	var replayReq *http.Request
	for _, c := range []struct {
		name  string
		path  string
		body  []byte
		sign  signer
		more  func(*http.Request)
		code  int
		why   inboxReason
		retry bool
	}{
		{"unsigned", "/inbox", body, nil, nil, 401, inUnsigned, false},
		{"a signature that does not parse", "/inbox", body, nil, func(r *http.Request) { r.Header.Set("Signature", "not a signature") }, 401, inSignature, false},
		{"the request line not signed", "/inbox", body, cavage("host date digest"), nil, 401, inSignature, false},
		{"no headers parameter", "/inbox", body, func(req *http.Request, b []byte) error {
			req.Header.Set("Date", now())
			req.Header.Set("Digest", digestHeader(b))
			req.Header.Set("Signature", `keyId="`+boKeyID+`",signature="AAAA"`)
			return nil
		}, nil, 401, inSignature, false},
		{"a bad digest", "/inbox", body, e.bo(), func(r *http.Request) { r.Header.Set("Digest", digestHeader([]byte("other"))) }, 401, inSignature, false},
		// A digest of another body, signed as it is: the signature verifies, and only the digest refuses it.
		{"a signed digest of another body", "/inbox", body, func(req *http.Request, b []byte) error {
			req.Header.Set("Date", now())
			req.Header.Set("Digest", digestHeader([]byte("another body")))
			req.Host = "here.example"
			cavageWith(t, req, e.key, boKeyID, "rsa-sha256", strings.Fields("(request-target) host date digest"), "")
			return nil
		}, nil, 401, inSignature, false},
		{"a signed Content-Digest of another body", "/inbox", body, func(req *http.Request, b []byte) error {
			req.Header.Set("Content-Digest", contentDigestHeader([]byte("another body")))
			rfc9421With(t, req, e.key, `sig1=("@method" "@target-uri" "content-digest");created=`+created()+`;keyid="`+boKeyID+`"`)
			return nil
		}, nil, 401, inSignature, false},
		// A body the decoder refuses, under a signature that does not verify: refused for the signature, so
		// nothing was decoded before it (7.10).
		{"an undecodable body whose signature does not verify", "/inbox", []byte(deep), e.bo(), func(r *http.Request) { r.Header.Set("Digest", digestHeader([]byte("other"))) }, 401, inSignature, false},
		{"an RFC 9421 signature without created", "/inbox", body, rfc(`sig1=("@method" "@target-uri" "content-digest");keyid="` + boKeyID + `"`), nil, 401, inSignature, false},
		{"an RFC 9421 signature without content-digest", "/inbox", body, rfc(`sig1=("@method" "@target-uri");created=` + created() + `;keyid="` + boKeyID + `"`), nil, 401, inSignature, false},
		{"two RFC 9421 signatures", "/inbox", body, func(req *http.Request, b []byte) error {
			rfc(`sig1=("@method" "@target-uri" "content-digest");created=`+created()+`;keyid="`+boKeyID+`"`)(req, b)
			req.Header.Set("Signature-Input", req.Header.Get("Signature-Input")+`, sig2=("@method");created=1;keyid="x"`)
			req.Header.Set("Signature", req.Header.Get("Signature")+`, sig2=:AAAA:`)
			return nil
		}, nil, 401, inSignature, false},
		{"an alg that is not rsa-v1_5-sha256", "/inbox", body, rfc(`sig1=("@method" "@target-uri" "content-digest");created=` + created() + `;keyid="` + boKeyID + `";alg="rsa-pss-sha512"`), nil, 401, inSignature, false},
		{"an activity id of another origin", "/inbox", activity(boID, "Like", "https://here.example/users/ana", map[string]any{"id": "https://other.example/activities/1"}), e.bo(), nil, 400, inForeignID, false},
		{"too large", "/inbox", append(append([]byte(`{"x":"`), bytes.Repeat([]byte("x"), inboxBodyMax)...), `"}`...), e.bo(), nil, 413, inTooLarge, false},
		{"too deep", "/inbox", []byte(deep), e.bo(), nil, 400, inUndecodable, false},
		{"too many array elements", "/inbox", activity(boID, "Like", "https://here.example/users/ana", map[string]any{"to": json.RawMessage(wide)}), e.bo(), nil, 400, inUndecodable, false},
		{"not an Activity Streams type", "/inbox", body, e.bo(), func(r *http.Request) { r.Header.Set("Content-Type", "application/json") }, 415, inType, false},
		{"ld+json of another profile", "/inbox", body, e.bo(), func(r *http.Request) {
			r.Header.Set("Content-Type", `application/ld+json; profile="https://example.com/other"`)
		}, 415, inType, false},
		{"a key of another domain that is not allowed", "/inbox", activity("https://unlisted.example/users/x", "Like", "https://here.example/users/ana", nil), e.signer(e.key, "https://unlisted.example/users/x#main-key", sigCavage), nil, 403, inNotListed, false},
		{"a key on an address", "/inbox", body, e.signer(e.key, "https://10.0.0.1/users/x#main-key", sigCavage), nil, 403, inNotListed, false},
		// The key is bo's, and the body names someone else on bo's server: not bo's activity (7.3).
		{"key owner not the actor", "/inbox", activity("https://social.example/users/eve", "Like", "https://here.example/users/ana", nil), e.bo(), nil, 202, inForward, false},
		// A key on another port of bo's host is never fetched (port 443 only) and so never vouches for bo.
		{"a key on another port of the actor's host", "/inbox", body, e.signer(e.key, "https://social.example:8443/users/bo#main-key", sigCavage), nil, 401, inSignature, false},
		{"the personal inbox of a name nobody has", "/users/nobody/inbox", activity(boID, "Like", "https://here.example/users/nobody", nil), e.bo(), nil, 202, inDropped, false},
		{"the instance actor's inbox", "/actor/inbox", activity(boID, "Like", "https://here.example/actor", nil), e.bo(), nil, 202, inDropped, false},
		{"replayed, first", "/inbox", replayed, e.bo(), func(r *http.Request) { replayReq = r.Clone(context.Background()) }, 202, inDropped, false},
		// Unsigned, from an actor nobody has seen: nothing is fetched for it (the count below).
		{"unsigned, naming an actor never seen", "/inbox", activity("https://social.example/users/zed", "Like", "https://here.example/users/ana", nil), nil, nil, 401, inUnsigned, false},
	} {
		w := e.post(c.path, c.body, c.sign, c.more)
		if w.Code != c.code || e.last() != c.why {
			t.Errorf("%s: %d %s (%v), want %d (%v)", c.name, w.Code, w.Body, e.last(), c.code, c.why)
		}
		if c.code >= 400 && !strings.Contains(w.Body.String(), inboxReasons[c.why].words) {
			t.Errorf("%s: the answer %q does not say %q", c.name, w.Body, inboxReasons[c.why].words)
		}
	}
	// None of those cost a fetch: each was refused before a key was asked for, or had bo's key kept.
	if n := e.w.total(); n != 1 {
		t.Errorf("the refusals cost %d fetches, want none after bo's first", n-1)
	}
	// A delivery signed under a key id not on the actor's origin: other.example, which is allowed, serves bo's
	// public key as its own actor's (public keys are public), and the body still names bo. A cavage signature
	// does not cover its keyId, so once the replay memory has let a signature go this is how it comes back; the
	// origin rule refuses it (7.2, TestReplayAcrossDomains).
	e.allow("other.example")
	e.w.put("https://other.example/users/bo", worldDoc{body: actorDoc("https://other.example/users/bo", "bo", "", &e.key.PublicKey, nil)})
	if w := e.post("/inbox", activity(boID, "Like", "https://here.example/users/ana", nil), e.signer(e.key, "https://other.example/users/bo#main-key", sigCavage), nil); w.Code != 202 || e.last() != inForward {
		t.Errorf("bo's activity under a copy of bo's key on another origin: %d (%v)", w.Code, e.last())
	}
	if n := e.queued(); n != 0 {
		t.Errorf("%d rows after the refusals", n)
	}
	// Replayed: the same request again, byte for byte.
	w := httptest.NewRecorder()
	in := received(replayReq, replayed)
	e.h.ServeHTTP(w, in)
	if w.Code != 401 || e.last() != inSignature {
		t.Errorf("replayed: %d (%v)", w.Code, e.last())
	}
	// The window: 12 hours back, an hour ahead (7.1).
	for _, c := range []struct {
		name string
		by   time.Duration
	}{{"an old date", -13 * time.Hour}, {"a future date", 2 * time.Hour}} {
		at := e.clock.now().Add(c.by)
		s := httpSigner(e.key, boKeyID, sigCavage, func() time.Time { return at }, time.Time{})
		if w := e.post("/inbox", activity(boID, "Like", "x", nil), s, nil); w.Code != 401 || e.last() != inSignature {
			t.Errorf("%s: %d (%v)", c.name, w.Code, e.last())
		}
	}

	// A suspended domain: 403 before anything is read; and the fetcher refuses it too.
	e.mustExec(`INSERT INTO domain_policies (domain, policy) VALUES ('bad.social.example', 'suspend')`)
	e.a.fed.loadPolicy(context.Background())
	before := e.w.total()
	if w := e.post("/inbox", body, e.signer(e.key, "https://bad.social.example/users/x#main-key", sigCavage), nil); w.Code != 403 || e.last() != inBlocked {
		t.Errorf("blocked host: %d (%v)", w.Code, e.last())
	}
	if e.w.total() != before {
		t.Error("a blocked host's delivery fetched something")
	}

	// The switch off: 503 with Retry-After, before the body is read.
	e.setFlag(fedFlag, false)
	if w := e.post("/inbox", body, e.bo(), nil); w.Code != 503 || w.Result().Header.Get("Retry-After") == "" || e.last() != inPaused {
		t.Errorf("paused: %d %v (%v)", w.Code, w.Result().Header, e.last())
	}
	e.setFlag(fedFlag, true)

	// The queue full: 503 with Retry-After; and one sender group's share of it full: 429.
	e.mustExec(`INSERT INTO ap_inbox (activity_id, actor_id, host, kind, received_at, next_at) SELECT 'https://filler.example/' || g, u.id, 'filler.example', 'Delete', now(), now() + interval '1 day'
		FROM generate_series(1, $1) g, (SELECT id FROM users WHERE domain <> '' LIMIT 1) u`, inboxQueueMax)
	del := activity(boID, "Delete", boID, nil)
	if w := e.post("/inbox", del, e.bo(), nil); w.Code != 503 || w.Result().Header.Get("Retry-After") != "120" || e.last() != inQueueFull {
		t.Errorf("queue full: %d %v (%v)", w.Code, w.Result().Header, e.last())
	}
	e.mustExec(`DELETE FROM ap_inbox`)
	e.mustExec(`INSERT INTO ap_inbox (activity_id, actor_id, host, kind, received_at, next_at) SELECT 'https://social.example/x/' || g, u.id, 'social.example', 'Delete', now(), now() + interval '1 day'
		FROM generate_series(1, $1) g, (SELECT id FROM users WHERE domain <> '' LIMIT 1) u`, inboxGroupQueued)
	if w := e.post("/inbox", activity(boID, "Delete", boID, nil), e.bo(), nil); w.Code != 429 || e.last() != inGroupQueued {
		t.Errorf("the sender group's share full: %d (%v)", w.Code, e.last())
	}
	e.mustExec(`DELETE FROM ap_inbox`)

	// Allowances: per host and per sender group once the signature verified, and per address before.
	e.a.lim.inbox = newLimiter(1, time.Hour)
	e.post("/inbox", activity(boID, "Like", "https://here.example/users/ana", nil), e.bo(), nil)
	if w := e.post("/inbox", activity(boID, "Like", "https://here.example/users/ana", nil), e.bo(), nil); w.Code != 429 || e.last() != inGroupLimit {
		t.Errorf("past the sender group's allowance: %d (%v)", w.Code, e.last())
	}
	e.a.lim.inbox = newLimiter(inboxGroupRate, inboxGroupWindow)
	e.a.lim.inboxHost = newLimiter(1, time.Hour)
	e.post("/inbox", activity(boID, "Like", "https://here.example/users/ana", nil), e.bo(), nil)
	if w := e.post("/inbox", activity(boID, "Like", "https://here.example/users/ana", nil), e.bo(), nil); w.Code != 429 || e.last() != inGroupLimit {
		t.Errorf("past the host's allowance: %d (%v)", w.Code, e.last())
	}
	e.a.lim.inboxHost = newLimiter(inboxHostRate, inboxGroupWindow)
	e.a.lim.inboxAddr = newLimiter(1, time.Hour)
	e.post("/inbox", body, nil, nil)
	if w := e.post("/inbox", body, nil, nil); w.Code != 429 || e.last() != inAddrLimit {
		t.Errorf("past the address's allowance: %d (%v)", w.Code, e.last())
	}
	e.a.lim.inboxAddr = newLimiter(inboxAddrRate, time.Hour)

	// The clock earlier than the build: 503, never 401 (8.3).
	e.a.fed.verifier.built = e.clock.now().Add(time.Hour)
	if w := e.post("/inbox", body, e.bo(), nil); w.Code != 503 || e.last() != inClock {
		t.Errorf("the clock before the build: %d (%v)", w.Code, e.last())
	}
	e.a.fed.verifier.built = time.Time{}

	// Every refusal so far is counted by its phrase, and the numbers say so.
	n := e.q.stats.numbers(time.Now())
	for _, why := range []inboxReason{inUnsigned, inSignature, inForeignID, inTooLarge, inUndecodable, inType, inNotListed, inBlocked, inPaused, inQueueFull, inGroupQueued, inGroupLimit, inAddrLimit, inClock} {
		if n.Refused[why.String()] == 0 {
			t.Errorf("%q is not counted: %v", why, n.Refused)
		}
	}
	if n.Dropped["Like"] == 0 || n.Answers[inForward.String()] == 0 {
		t.Errorf("the 202s are not counted: %v %v", n.Dropped, n.Answers)
	}
}

// The keys a delivery can be verified with (7.4, 6.3, FWR 1's "required"): only a fresh row's of an actor
// neither gone nor suspended, except an actor's own Delete against the cached key; a rotated key taken after one
// refetch; a Delete from an actor never seen dropped with no fetch.
func TestInboxKeys(t *testing.T) {
	e := newInboxEnv(t)
	follow := func() []byte { return activity(boID, "Like", "https://here.example/users/ana", nil) }
	if w := e.post("/inbox", follow(), e.bo(), nil); w.Code != 202 {
		t.Fatalf("bo's first delivery: %d %s", w.Code, w.Body)
	}
	// A rotated key: the stub now serves bo with the other key. The kept key fails, one refetch takes the new
	// one, and the delivery is accepted; within the 10 minutes a second failure fetches nothing more.
	e.clock.add(actorRecheck)
	e.w.put(boID, worldDoc{body: actorDoc(boID, "bo", "Bo", &e.other.PublicKey, nil)})
	if w := e.post("/inbox", follow(), e.signer(e.other, boKeyID, sigCavage), nil); w.Code != 202 || e.w.hit(boID) != 2 {
		t.Errorf("a rotated key: %d (%v), %d fetches", w.Code, e.last(), e.w.hit(boID))
	}
	if w := e.post("/inbox", follow(), e.bo(), nil); w.Code != 401 || e.w.hit(boID) != 2 {
		t.Errorf("the old key after the rotation: %d, %d fetches", w.Code, e.w.hit(boID))
	}
	e.w.put(boID, worldDoc{body: actorDoc(boID, "bo", "Bo", &e.key.PublicKey, nil)})
	e.clock.add(actorRecheck)
	if w := e.post("/inbox", follow(), e.bo(), nil); w.Code != 202 {
		t.Fatalf("the key rotated back: %d", w.Code)
	}

	// A stale row (its refetch failed): no key to verify with, whatever the cause.
	e.clock.add(actorTTL + time.Minute)
	e.w.put(boID, worldDoc{status: 500})
	if w := e.post("/inbox", follow(), e.bo(), nil); w.Code != 401 || e.last() != inSignature {
		t.Errorf("signed with the key of a stale row: %d (%v)", w.Code, e.last())
	}
	// But bo's own Delete verifies with the cached key (6.3), and the worker marks bo gone.
	if w := e.post("/inbox", activity(boID, "Delete", boID, nil), e.bo(), nil); w.Code != 202 || e.last() != inAccepted {
		t.Fatalf("bo's own Delete with the cached key of a stale row: %d %s (%v)", w.Code, w.Body, e.last())
	}
	if worked, err := e.q.step(context.Background()); !worked || err != nil {
		t.Fatalf("the worker: %v %v", worked, err)
	}
	if st := e.count(`SELECT count(*) FROM ap_actors WHERE ap_id = $1 AND state = 'gone'`, boID); st != 1 {
		t.Error("bo is not marked gone")
	}
	// A gone actor's key verifies nothing else, and its own Delete again is still taken (and is a no-op).
	if w := e.post("/inbox", follow(), e.bo(), nil); w.Code != 401 {
		t.Errorf("signed with the key of a gone actor: %d (%v)", w.Code, e.last())
	}
	// A key given for the actor's own Delete only, on something else in a body that peeks as a Delete: refused.
	// (Its object is the actor, but the decoded type is not one the peek saw: impossible by construction, so
	// what is held here is the plain rule: an Update of a gone actor is not taken.)
	if w := e.post("/inbox", activity(boID, "Update", map[string]any{"id": boID, "type": "Person", "inbox": boID + "/inbox"}, nil), e.bo(), nil); w.Code != 401 {
		t.Errorf("an Update by a gone actor: %d (%v)", w.Code, e.last())
	}

	// A suspended actor: no key to verify with.
	ev := "https://social.example/users/eve"
	e.w.put(ev, worldDoc{body: actorDoc(ev, "eve", "", &e.other.PublicKey, map[string]any{"suspended": true})})
	if w := e.post("/inbox", activity(ev, "Like", "x", nil), e.signer(e.other, ev+"#main-key", sigCavage), nil); w.Code != 401 {
		t.Errorf("signed with the key of a suspended actor: %d (%v)", w.Code, e.last())
	}

	// A Delete from an actor never seen: 202, nothing fetched, nothing stored (7.4, FWR 7).
	ghost := "https://social.example/users/ghost"
	before := e.w.total()
	if w := e.post("/inbox", activity(ghost, "Delete", ghost, nil), e.signer(e.other, ghost+"#main-key", sigCavage), nil); w.Code != 202 || e.last() != inUnknownDelete {
		t.Errorf("a Delete from an actor never seen: %d (%v)", w.Code, e.last())
	}
	if e.w.total() != before || e.count(`SELECT count(*) FROM ap_inbox WHERE activity_id LIKE $1`, ghost+"%") != 0 {
		t.Error("a Delete from an actor never seen fetched or stored something")
	}
	// A Delete of another actor from a known one is not the actor's own: dropped, and not a reason to use a
	// stale key.
	if w := e.post("/inbox", activity(boID, "Delete", ev, nil), e.bo(), nil); w.Code == 202 && e.last() == inAccepted {
		t.Error("a Delete of another actor was queued")
	}
}

// The key fetches a stranger can cause are bounded (7.4): distinct key ids on one domain cost at most the
// allowance of key fetches of its sender group, and the failures are remembered, so asking again costs nothing.
func TestInboxKeyFetchBound(t *testing.T) {
	e := newInboxEnv(t)
	e.allow("big.example")
	c := e.a.fed.actors
	// One address inventing key ids on one host: at most its own allowance of fetches, then 503, no fetch.
	c.addrFetches = newLimiter(3, time.Hour)
	for i := range 10 {
		id := fmt.Sprintf("https://big.example/users/x%d", i)
		e.post("/inbox", activity(id, "Like", "x", nil), e.signer(e.key, id+"#main-key", sigCavage), nil)
	}
	if n := e.w.total(); n != 3 {
		t.Errorf("10 distinct key ids from one address cost %d fetches, want its allowance of 3", n)
	}
	if e.last() != inKeyBusy {
		t.Errorf("past the address's allowance: %v, want a 503", e.last())
	}
	// None of those verified, so none was charged to big.example: another address still gets its keys.
	if c.keyFetches.over("big.example") {
		t.Error("forged requests spent the sender group's allowance of key fetches")
	}
	e.from = "198.51.100.8:4000"
	if w := e.post("/inbox", activity("https://big.example/users/y", "Like", "x", nil), e.signer(e.key, "https://big.example/users/y#main-key", sigCavage), nil); e.w.total() != 4 {
		t.Errorf("another address, after the first ran out: %d (%v), %d fetches", w.Code, e.last(), e.w.total())
	}
	// The same failing key id again within 10 minutes: remembered, no fetch.
	e.from = "198.51.100.9:4000"
	id := "https://big.example/users/x0"
	e.post("/inbox", activity(id, "Like", "x", nil), e.signer(e.key, id+"#main-key", sigCavage), nil)
	if n := e.w.total(); n != 4 {
		t.Errorf("a remembered failure was fetched again: %d", n)
	}
	// A key fetched and then verified is charged to its sender group: with the group's allowance used up, a new
	// key of that group is not fetched, from any address.
	c.keyFetches = newLimiter(1, time.Hour)
	ev := "https://social.example/users/eve"
	e.w.put(ev, worldDoc{body: actorDoc(ev, "eve", "", &e.other.PublicKey, nil)})
	if w := e.post("/inbox", activity(ev, "Like", "https://here.example/users/ana", nil), e.signer(e.other, ev+"#main-key", sigCavage), nil); w.Code != 202 || !c.keyFetches.over("social.example") {
		t.Fatalf("eve's first delivery: %d (%v); the group charged %v", w.Code, e.last(), c.keyFetches.over("social.example"))
	}
	zed := "https://social.example/users/zed"
	e.w.put(zed, worldDoc{body: actorDoc(zed, "zed", "", &e.other.PublicKey, nil)})
	if w := e.post("/inbox", activity(zed, "Like", "x", nil), e.signer(e.other, zed+"#main-key", sigCavage), nil); w.Code != 503 || e.w.hit(zed) != 0 {
		t.Errorf("a new key of a group past its allowance: %d, %d fetches", w.Code, e.w.hit(zed))
	}
	// The unit: the address pays every request; the group is only looked at.
	u := newActorCache(&federation{}, nil)
	u.addrFetches, u.keyFetches = newLimiter(1, time.Hour), newLimiter(1, time.Hour)
	var n int
	o := lookupOpts{payer: "192.0.2.1", fetched: func() { n++ }}
	if !u.spend("https://a.wild.example/k", o) || u.spend("https://b.wild.example/k", o) || n != 1 || u.keyFetches.over("wild.example") {
		t.Errorf("spend: the address's second request passed, or the group was charged (%d heard)", n)
	}
	u.keyFetches.add("wild2.example")
	if u.spend("https://wild2.example/k", lookupOpts{payer: "192.0.2.2"}) {
		t.Error("a group past its allowance of verified fetches was fetched for")
	}
}

// A key that is not cached is fetched with no database connection held while the network is waited on: the
// inbox's request waits on the stub with the pool idle, and answers once the stub does (7.4; the fetch's own
// time, 5 s for a document, bounds it).
func TestDBInboxSlowKey(t *testing.T) {
	e := newInboxEnv(t)
	release := e.w.holdAt(boID)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- e.post("/inbox", activity(boID, "Like", "https://here.example/users/ana", nil), e.bo(), nil)
	}()
	<-e.w.arrived
	if n := e.a.db.Stats().InUse; n != 0 {
		t.Errorf("%d connections held while the key is fetched", n)
	}
	release()
	if w := <-done; w.Code != 202 {
		t.Errorf("after the slow key: %d %s", w.Code, w.Body)
	}
}

// The queue (8.1, 8.5, 8.6, 9.3): one row per activity id however often and however concurrently it comes; two
// workers never handle a row twice; a failure for this server's reason retried on the schedule and then given
// up; a row claimed when the federation stops given back; a row whose worker died claimed again after its
// lease; handled rows kept 7 days without their body.
func TestDBInboxQueue(t *testing.T) {
	e := newInboxEnv(t)
	upd := func(id string) []byte {
		return activity(boID, "Update", map[string]any{"id": boID, "type": "Person", "inbox": boID + "/inbox"}, map[string]any{"id": id})
	}
	// The same activity, sent 8 times at once and once more after: one row, every answer 202.
	same := upd(boID + "/activities/same")
	if w := e.post("/inbox", same, e.bo(), nil); w.Code != 202 || e.last() != inAccepted {
		t.Fatalf("the first copy: %d %s (%v)", w.Code, w.Body, e.last())
	}
	// Each copy is signed a second apart, as a sender retrying would: the same signature twice is a replay,
	// which the signature check refuses before the queue sees it.
	var copies []*http.Request
	for range 8 {
		e.clock.add(time.Second)
		req, _ := http.NewRequest("POST", "https://here.example/inbox", bytes.NewReader(same))
		req.Header.Set("Content-Type", asType)
		if err := e.bo()(req, same); err != nil {
			t.Fatal(err)
		}
		in := received(req, same)
		in.RemoteAddr = fmt.Sprintf("198.51.100.%d:4000", 20+len(copies)) // each from its own address: one holds at most inboxReadsPerAddr read turns
		copies = append(copies, in)
	}
	var wg sync.WaitGroup
	var bad atomic.Int32
	for _, req := range copies {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			e.h.ServeHTTP(w, req)
			if w.Code != 202 {
				bad.Add(1)
			}
		}()
	}
	wg.Wait()
	if bad.Load() != 0 || e.queued() != 1 {
		t.Fatalf("the same activity: %d refusals, %d rows", bad.Load(), e.queued())
	}
	// Two workers at once on 20 rows: each handled once.
	e.mustExec(`DELETE FROM ap_inbox`)
	var bo int64
	if err := e.a.db.QueryRow(`SELECT user_id FROM ap_actors WHERE ap_id = $1`, boID).Scan(&bo); err != nil {
		t.Fatal(err)
	}
	e.mustExec(`INSERT INTO ap_inbox (activity_id, actor_id, host, kind, body, received_at, next_at) SELECT $2 || '/a/' || g, $1, 'social.example', 'Update', '{}', $3, $3
		FROM generate_series(1, 20) g`, bo, boID, e.clock.now())
	var handled atomic.Int32
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				worked, err := e.q.step(context.Background())
				if err != nil {
					t.Error(err)
					return
				}
				if !worked {
					return
				}
				handled.Add(1)
			}
		}()
	}
	wg.Wait()
	if handled.Load() != 20 || e.count(`SELECT count(*) FROM ap_inbox WHERE state = 'done' AND attempts = 1 AND body IS NULL`) != 20 {
		t.Errorf("two workers handled %d rows; %d done once", handled.Load(), e.count(`SELECT count(*) FROM ap_inbox WHERE state = 'done' AND attempts = 1`))
	}

	// This server's own failure (the actor's fetch busy) is retried on the schedule, then given up.
	e.mustExec(`DELETE FROM ap_inbox`)
	e.mustExec(`INSERT INTO ap_inbox (activity_id, actor_id, host, kind, body, received_at, next_at) VALUES ($2 || '/retry', $1, 'social.example', 'Update', '{}', $3, $3)`, bo, boID, e.clock.now())
	e.clock.add(actorTTL) // bo is due to be fetched again
	e.a.fed.actors.wait = 0
	for range actorFetchers {
		e.a.fed.actors.slots <- struct{}{}
	}
	for i, wait := range inboxRetries {
		if worked, err := e.q.step(context.Background()); !worked || err != nil {
			t.Fatalf("attempt %d: %v %v", i+1, worked, err)
		}
		if e.count(`SELECT count(*) FROM ap_inbox WHERE state = 'queued' AND attempts = $1 AND next_at = $2`, i+1, e.clock.now().Add(wait)) != 1 {
			var st, oc string
			var at int
			var nx time.Time
			e.a.db.QueryRow(`SELECT state, outcome, attempts, next_at FROM ap_inbox`).Scan(&st, &oc, &at, &nx)
			t.Logf("%s %q %d %v (now %v)", st, oc, at, nx, e.clock.now())
			t.Fatalf("after attempt %d the row is not due in %v", i+1, wait)
		}
		if worked, _ := e.q.step(context.Background()); worked {
			t.Fatalf("a row was claimed before it was due (attempt %d)", i+1)
		}
		e.clock.add(wait)
	}
	if worked, err := e.q.step(context.Background()); !worked || err != nil {
		t.Fatal(worked, err)
	}
	if e.count(`SELECT count(*) FROM ap_inbox WHERE state = 'failed' AND body IS NULL`) != 1 {
		t.Error("the row was not given up after its last attempt")
	}
	for range actorFetchers {
		<-e.a.fed.actors.slots
	}
	e.a.fed.actors.wait = actorWait

	// A worker that died with a row claimed: another claims it once the lease runs out.
	e.mustExec(`DELETE FROM ap_inbox`)
	e.mustExec(`INSERT INTO ap_inbox (activity_id, actor_id, host, kind, body, received_at, next_at, locked_until, attempts) VALUES ($2 || '/lease', $1, 'social.example', 'Delete', '{}', $3, $3, $4, 1)`,
		bo, boID, e.clock.now(), e.clock.now().Add(inboxLease))
	if worked, _ := e.q.step(context.Background()); worked {
		t.Error("a row was claimed while another worker's lease ran")
	}
	e.clock.add(inboxLease + time.Second)
	if worked, err := e.q.step(context.Background()); !worked || err != nil || e.count(`SELECT count(*) FROM ap_inbox WHERE state = 'done'`) != 1 {
		t.Errorf("after the lease: %v %v", worked, err)
	}
	e.mustExec(`UPDATE ap_actors SET state = 'ok' WHERE user_id = $1`, bo)

	// Stopping while a row is handled: it is given back as it was, not lost and not counted as an attempt.
	e.mustExec(`DELETE FROM ap_inbox`)
	e.mustExec(`INSERT INTO ap_inbox (activity_id, actor_id, host, kind, body, received_at, next_at) VALUES ($2 || '/stop', $1, 'social.example', 'Update', '{}', $3, $3)`, bo, boID, e.clock.now())
	e.clock.add(actorTTL)
	release := e.w.holdAt(boID)
	ctx, cancel := context.WithCancel(context.Background())
	stepped := make(chan error, 1)
	go func() { _, err := e.q.step(ctx); stepped <- err }()
	<-e.w.arrived
	cancel()
	if err := <-stepped; err != nil {
		t.Fatal(err)
	}
	release()
	if e.count(`SELECT count(*) FROM ap_inbox WHERE state = 'queued' AND locked_until IS NULL AND attempts = 0`) != 1 {
		t.Error("the row claimed when the worker stopped was not given back")
	}
	if worked, err := e.q.step(context.Background()); !worked || err != nil || e.count(`SELECT count(*) FROM ap_inbox WHERE state = 'done'`) != 1 {
		t.Errorf("the row given back is handled after: %v %v", worked, err)
	}

	// Retention: handled rows go after 7 days; queued ones stay.
	e.mustExec(`UPDATE ap_inbox SET handled_at = now() - interval '8 days'`)
	e.mustExec(`INSERT INTO ap_inbox (activity_id, actor_id, host, kind, body, received_at, next_at) VALUES ($2 || '/waits', $1, 'social.example', 'Update', '{}', now(), now() + interval '1 day')`, bo, boID)
	n, err := e.a.housekeep()
	if err != nil {
		t.Fatal(err)
	}
	if n["inbox"] != 1 || e.count(`SELECT count(*) FROM ap_inbox`) != 1 {
		t.Errorf("housekeeping removed %d inbox rows, %d left", n["inbox"], e.count(`SELECT count(*) FROM ap_inbox`))
	}

	// While paused, nothing is claimed.
	e.setFlag(fedFlag, false)
	e.mustExec(`UPDATE ap_inbox SET next_at = now() - interval '1 hour'`)
	if worked, _ := e.q.step(context.Background()); worked {
		t.Error("a row was handled while the Federation switch is off")
	}
}

// What the inbox stores and what it does not (7.2, 7.3): only the two types a worker acts on make a row, a
// dropped type makes none, and an Update of a Person by another actor, or of a type that is no actor, is
// dropped.
func TestDBInboxHandlers(t *testing.T) {
	e := newInboxEnv(t)
	ev := "https://social.example/users/eve"
	for _, c := range []struct {
		name string
		body []byte
		why  inboxReason
	}{
		{"a Follow of an account on another server", activity(boID, "Follow", "https://elsewhere.example/users/z", nil), inDropped},
		{"Create", activity(boID, "Create", map[string]any{"id": boID + "/notes/1", "type": "Note", "attributedTo": boID, "content": "hi"}, nil), inDropped},
		{"Like", activity(boID, "Like", "https://here.example/users/ana/posts/1", nil), inDropped},
		{"an Undo of a Like", activity(boID, "Undo", map[string]any{"id": boID + "/likes/1", "type": "Like", "actor": boID, "object": "https://here.example/users/ana/posts/1"}, nil), inDropped},
		{"an extension type", activity(boID, "EmojiReact", "https://here.example/users/ana/posts/1", nil), inDropped},
		{"an Update of a Person by another actor", activity(boID, "Update", map[string]any{"id": ev, "type": "Person", "inbox": ev + "/inbox"}, nil), inDropped},
		{"an Update of a Note", activity(boID, "Update", map[string]any{"id": boID, "type": "Note"}, nil), inDropped},
		{"an Update of the actor by id only", activity(boID, "Update", boID, nil), inDropped},
		{"an Update of the actor itself", activity(boID, "Update", map[string]any{"id": boID, "type": "Person", "inbox": boID + "/inbox"}, nil), inAccepted},
		{"a Delete of the actor itself", activity(boID, "Delete", boID, nil), inAccepted},
	} {
		if w := e.post("/inbox", c.body, e.bo(), nil); w.Code != 202 || e.last() != c.why {
			t.Errorf("%s: %d (%v), want %v", c.name, w.Code, e.last(), c.why)
		}
	}
	if n := e.queued(); n != 2 {
		t.Errorf("%d rows, want the Update and the Delete", n)
	}
	n := e.q.stats.numbers(time.Now())
	if n.Dropped["Follow"] != 1 || n.Dropped["EmojiReact"] != 1 || n.Dropped["Update"] != 3 {
		t.Errorf("dropped per type: %v", n.Dropped)
	}
	// The Update fetches bo again once the 10 minutes have passed; then the Delete marks bo gone.
	e.clock.add(actorRecheck)
	for range 2 {
		if worked, err := e.q.step(context.Background()); !worked || err != nil {
			t.Fatal(worked, err)
		}
	}
	if e.w.hit(boID) != 2 || e.count(`SELECT count(*) FROM ap_actors WHERE ap_id = $1 AND state = 'gone'`, boID) != 1 {
		t.Errorf("after the two rows: %d fetches of bo, gone %d", e.w.hit(boID), e.count(`SELECT count(*) FROM ap_actors WHERE state = 'gone'`))
	}
	if h := e.q.stats.numbers(time.Now()).Handled; h["Update"] != 1 || h["Delete"] != 1 {
		t.Errorf("handled: %v", h)
	}
	// The federation page shows the numbers to staff.
	_, mod := e.user("mo", "moderator")
	out := e.must(200, mod, "GET", "/api/admin/federation", nil)
	in, _ := out["inbox"].(map[string]any)
	if in == nil || in["queueMax"] != float64(inboxQueueMax) || in["dropped"].(map[string]any)["Follow"] != 1.0 {
		t.Errorf("the federation page's numbers: %v", out["_raw"])
	}
	dash := e.must(200, mod, "GET", "/api/admin/dashboard", nil)
	if fd, _ := dash["federation"].(map[string]any); fd == nil || fd["inbox"] == nil {
		t.Errorf("the dashboard's federation line: %v", dash["federation"])
	}
}

// The inbox's other methods, and the write guard (FWR 1): every method but POST answers 405 with Allow: POST,
// whatever the name, counted against the public allowance; a POST from another server passes guard (an Origin
// of another site, no session, an Activity Streams type) and reaches the inbox's own checks, which refuse plain
// JSON with 415, so no other address loses guard's check. Local and plain single mode answer every inbox
// address as an unknown one (TestDBDefaultsUnchanged, TestDBLocalModeUnchanged).
func TestDBInboxMethods(t *testing.T) {
	f := newPubFixture(t)
	e := f.e
	for _, p := range []string{"/inbox", "/users/ana/inbox", "/users/nobody/inbox", "/actor/inbox"} {
		for _, m := range []string{"GET", "HEAD", "PUT", "DELETE", "OPTIONS", "PATCH"} {
			w := ask(e.h, m, p, "198.51.100.20:4000", nil)
			if w.Code != 405 || w.Result().Header.Get("Allow") != "POST" || w.Result().Header.Get("Cache-Control") != "no-store" {
				t.Errorf("%s %s: %d %v", m, p, w.Code, w.Result().Header)
			}
		}
		for ctype, want := range map[string]int{"application/activity+json": 401, asType: 401, "application/json": 415, "text/plain": 415} {
			w := ask(e.h, "POST", p, "198.51.100.20:4000", map[string]string{"Content-Type": ctype, "Origin": "https://social.example"})
			if w.Code != want {
				t.Errorf("POST %s (%s, from another site): %d, want %d", p, ctype, w.Code, want)
			}
		}
	}
	// Elsewhere guard still refuses a write from another site.
	if w := ask(e.h, "POST", "/api/posts", "", map[string]string{"Content-Type": "application/json", "Origin": "https://social.example"}); w.Code != 403 {
		t.Errorf("guard elsewhere: %d", w.Code)
	}
	e.a.lim.public = newLimiter(1, time.Hour)
	ask(e.h, "GET", "/inbox", "203.0.113.9:1", nil)
	if w := ask(e.h, "GET", "/inbox", "203.0.113.9:1", nil); w.Code != 429 {
		t.Errorf("the inbox past the public allowance: %d", w.Code)
	}
	// While federation is refused, a delivery gets the 404 of every federation address.
	e.a.fed.refuse("federation refused: a test")
	if w := ask(e.h, "POST", "/inbox", "", map[string]string{"Content-Type": "application/activity+json"}); w.Code != 404 {
		t.Errorf("refused: %d", w.Code)
	}
}

// Two Flowa instances: a member of b.example sends a signed Update of her own profile to here.example's inbox,
// through the real fetcher's delivery kind and the delivery signer, over TLS; here.example fetches her key from
// b.example's real routes, verifies, queues, and its worker handles the row.
func TestDBTwoFlowaInbox(t *testing.T) {
	be := newEnv(t)
	if err := be.fedStart(map[string]string{"FLOWA_ORIGIN": "https://b.example"}); err != nil {
		t.Fatal(err)
	}
	anaID, anaTok := be.user("ana", "user")
	be.must(200, anaTok, "PUT", "/api/me", map[string]any{"federates": true, "displayName": "Ana B"})
	if _, err := be.a.fed.ensureKey(context.Background(), keyOwner(anaID)); err != nil {
		t.Fatal(err)
	}
	if _, err := be.a.fed.ensureKey(context.Background(), instanceActor); err != nil {
		t.Fatal(err)
	}
	// here.example takes deliveries from b.example and fetches from its real routes.
	e := newInboxEnv(t)
	e.allow("b.example")
	ft := actorStub(t, be.h)
	ft.origin = e.a.fed.origin
	e.c.fetch = ft
	// b.example sends, with its own fetcher, to here.example's real routes.
	out := actorStub(t, e.h)
	out.origin = be.a.fed.origin
	key, keyID, err := be.a.fed.privateKey(context.Background(), keyOwner(anaID))
	if err != nil {
		t.Fatal(err)
	}
	anaURL := "https://b.example/users/ana"
	for _, form := range []sigForm{sigCavage, sigRFC9421} {
		body := activity(anaURL, "Update", map[string]any{"id": anaURL, "type": "Person", "inbox": anaURL + "/inbox"}, nil)
		sign := httpSigner(key, be.a.fed.origin.url+keyID, form, e.clock.now, time.Time{})
		if _, err := out.do(context.Background(), fetchReq{kind: fetchDelivery, url: "https://here.example/users/cyd/inbox", body: body, sign: sign}); err != nil {
			t.Fatalf("the delivery (%v): %v", form, err)
		}
	}
	if n := e.queued(); n != 2 {
		t.Fatalf("%d rows queued at here.example", n)
	}
	e.clock.add(actorRecheck)
	for range 2 {
		if worked, err := e.q.step(context.Background()); !worked || err != nil {
			t.Fatal(worked, err)
		}
	}
	if e.count(`SELECT count(*) FROM ap_inbox WHERE state = 'done'`) != 2 {
		t.Error("the rows were not handled")
	}
	var name string
	if err := e.a.db.QueryRow(`SELECT u.display_name FROM users u JOIN ap_actors a ON a.user_id = u.id WHERE a.ap_id = $1`, anaURL).Scan(&name); err != nil || name != "Ana B" {
		t.Errorf("ana as here.example keeps her: %q %v", name, err)
	}
	// A Like of an actor from b.example is taken and dropped: nothing is answered.
	body := activity(anaURL, "Like", "https://here.example/users/cyd", nil)
	sign := httpSigner(key, be.a.fed.origin.url+keyID, sigCavage, e.clock.now, time.Time{})
	if _, err := out.do(context.Background(), fetchReq{kind: fetchDelivery, url: "https://here.example/inbox", body: body, sign: sign}); err != nil || e.last() != inDropped {
		t.Errorf("a Like: %v (%v)", err, e.last())
	}
}

// No connection is held while the network is waited on, and the inbox's work stays within the federation's
// turns at the database (section 12: at most 3 of the pool's 8): on a pool of one connection a delivery whose key
// must be fetched, the queue's statements and a worker's step all finish.
func TestDBInboxOneConnection(t *testing.T) {
	e := newInboxEnv(t)
	e.a.db.SetMaxOpenConns(1)
	defer e.a.db.SetMaxOpenConns(8)
	within := func(what string, f func()) {
		t.Helper()
		done := make(chan struct{})
		go func() { f(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatalf("%s did not finish in 10 s on one connection", what)
		}
	}
	within("a delivery with its key fetched", func() {
		if w := e.post("/inbox", activity(boID, "Update", map[string]any{"id": boID, "type": "Person", "inbox": boID + "/inbox"}, nil), e.bo(), nil); w.Code != 202 || e.last() != inAccepted {
			t.Errorf("%d %s (%v)", w.Code, w.Body, e.last())
		}
	})
	within("a worker's step", func() {
		if worked, err := e.q.step(context.Background()); !worked || err != nil {
			t.Error(worked, err)
		}
	})
	within("the federation page", func() {
		_, admin := e.user("boss", "admin")
		e.must(200, admin, "GET", "/api/admin/federation", nil)
	})
	if cap(e.a.fed.conns) != fedConnsMax || fedConnsMax > 3 {
		t.Errorf("federation's turns at the database: %d", cap(e.a.fed.conns))
	}
}

// The turns at the database bound federation's connections: with every turn taken, a delivery waits for one
// (and its request's end), and takes no connection meanwhile.
func TestDBFedConnsBound(t *testing.T) {
	e := newInboxEnv(t)
	for range fedConnsMax {
		e.a.fed.conns <- struct{}{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := e.a.fed.dbTurn(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a turn past the bound: %v", err)
	}
	if n := e.a.db.Stats().InUse; n != 0 {
		t.Errorf("%d connections in use while waiting for a turn", n)
	}
	for range fedConnsMax {
		<-e.a.fed.conns
	}
}

// At most inboxAtOnce deliveries are handled at once, so at most that many bodies are decoded at once (FWR 1's
// "required": 4 times 4 MB at worst), and at most inboxWaitMax wait for a turn; the rest are 503 at once.
func TestDBInboxTurns(t *testing.T) {
	e := newInboxEnv(t)
	e.q.waitFor = 20 * time.Millisecond
	follow := func() []byte { return activity(boID, "Like", "https://here.example/users/ana", nil) }
	for range inboxAtOnce - 1 {
		e.q.slots <- struct{}{}
	}
	if w := e.post("/inbox", follow(), e.bo(), nil); w.Code != 202 {
		t.Errorf("with %d turns taken: %d (%v)", inboxAtOnce-1, w.Code, e.last())
	}
	e.q.slots <- struct{}{}
	if w := e.post("/inbox", follow(), e.bo(), nil); w.Code != 503 || e.last() != inBusy || w.Result().Header.Get("Retry-After") != "30" {
		t.Errorf("with all %d turns taken: %d (%v)", inboxAtOnce, w.Code, e.last())
	}
	e.q.waiting.Store(inboxWaitMax)
	e.q.waitFor = time.Hour // a delivery past the waiting room is refused at once, not after the wait
	if w := e.post("/inbox", follow(), e.bo(), nil); w.Code != 503 || e.last() != inBusy {
		t.Errorf("with %d waiting: %d (%v)", inboxWaitMax, w.Code, e.last())
	}
	e.q.waiting.Store(0)
	if cap(e.q.slots) != 4 {
		t.Errorf("%d deliveries handled at once, want 4 (federation.md 7.5)", cap(e.q.slots))
	}
	for range inboxAtOnce {
		<-e.q.slots
	}
}

// countingBody is a request body that counts the bytes read from it.
type countingBody struct {
	r io.Reader
	n *atomic.Int64
}

func (c countingBody) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}
func (c countingBody) Close() error { return nil }

// A flood of deliveries nobody vouched for costs bounded work and starves nobody (7.4, 7.5, 7.10):
//   - unsigned requests with 256 KB bodies naming actors never seen are refused before a byte of the body is
//     read, with no fetch and no row;
//   - deliveries with a signature under key ids nobody keeps take at most inboxKeyAtOnce key turns while their
//     keys are looked up, and the next is 503 at once, with no fetch;
//   - meanwhile a delivery whose key is kept is accepted.
func TestDBInboxFlood(t *testing.T) {
	e := newInboxEnv(t)
	follow := func(actor string, more map[string]any) []byte {
		return activity(actor, "Like", "https://here.example/users/ana", more)
	}
	if w := e.post("/inbox", follow(boID, nil), e.bo(), nil); w.Code != 202 {
		t.Fatalf("bo's first delivery: %d", w.Code)
	}
	fetched := e.w.total()
	var read atomic.Int64
	pad := strings.Repeat("x", inboxBodyMax-400)
	for i := range 50 {
		zed := fmt.Sprintf("https://social.example/users/zed%d", i)
		e.onServer = func(r *http.Request) { r.Body = countingBody{r: r.Body, n: &read} }
		w := e.post("/inbox", follow(zed, map[string]any{"padding": pad}), nil, nil)
		e.onServer = nil
		if w.Code != 401 {
			t.Fatalf("unsigned %d: %d", i, w.Code)
		}
	}
	if read.Load() != 0 || e.w.total() != fetched || e.queued() != 0 || len(e.q.reads) != 0 || len(e.q.keyTurns) != 0 {
		t.Errorf("50 unsigned deliveries of 256 KB: %d bytes read, %d fetches, %d rows, %d read and %d key turns held", read.Load(), e.w.total()-fetched, e.queued(), len(e.q.reads), len(e.q.keyTurns))
	}
	// Bogus signatures under keys nobody keeps: the first inboxKeyAtOnce hold the key turns while their key is
	// fetched (the stub holds them), the next is refused at once without a fetch, and bo goes on.
	// (Each on a host of its own: the cache fetches one at a time per host.)
	e.allow("other.example", "big.example", "a.example")
	hosts := []string{"other.example", "big.example"}
	var held []func()
	done := make(chan int, inboxKeyAtOnce)
	for i := range inboxKeyAtOnce {
		id := fmt.Sprintf("https://%s/users/slow%d", hosts[i], i)
		held = append(held, e.w.holdAt(id))
		go func() {
			done <- e.postFrom(fmt.Sprintf("203.0.113.%d:1", 30+i), "/inbox", follow(id, nil), e.signer(e.other, id+"#main-key", sigCavage), nil).Code
		}()
		<-e.w.arrived
	}
	before := e.w.total()
	late := "https://a.example/users/late"
	if w := e.post("/inbox", follow(late, map[string]any{"padding": pad}), e.signer(e.other, late+"#main-key", sigCavage), nil); w.Code != 503 || e.last() != inKeyBusy || e.w.total() != before {
		t.Errorf("a key lookup past the key turns: %d (%v), %d fetches", w.Code, e.last(), e.w.total()-before)
	}
	if w := e.post("/inbox", follow(boID, nil), e.bo(), nil); w.Code != 202 || e.last() != inDropped {
		t.Errorf("bo while the key turns are held: %d (%v)", w.Code, e.last())
	}
	for _, release := range held {
		release()
	}
	for range inboxKeyAtOnce {
		if code := <-done; code != 401 {
			t.Errorf("a delivery whose key was not found: %d", code)
		}
	}
	if e.queued() != 0 || len(e.q.keyTurns) != 0 || len(e.q.reads) != 0 || len(e.q.slots) != 0 {
		t.Errorf("after the flood: %d rows, %d key, %d read and %d decode turns held", e.queued(), len(e.q.keyTurns), len(e.q.reads), len(e.q.slots))
	}
}

// A forward (signed by bo, naming an actor of other.example) is answered 202 and not acted on (7.3). Whose
// allowances does it spend? The signer's: the sender group of the key id that signed it pays the delivery and
// any key fetch; the named actor's domain is never counted and never fetched from.
func TestDBInboxForwardPays(t *testing.T) {
	e := newInboxEnv(t)
	e.allow("other.example")
	e.a.lim.inbox = newLimiter(1, time.Hour)
	x := "https://other.example/users/x"
	if w := e.post("/inbox", activity(x, "Create", map[string]any{"id": x + "/notes/1", "type": "Note", "attributedTo": x}, map[string]any{"id": x + "/activities/1"}), e.bo(), nil); w.Code != 202 || e.last() != inForward {
		t.Fatalf("a forward: %d (%v)", w.Code, e.last())
	}
	if !e.a.lim.inbox.over("social.example") || e.a.lim.inbox.over("other.example") {
		t.Errorf("the forward was counted against social.example %v, other.example %v", e.a.lim.inbox.over("social.example"), e.a.lim.inbox.over("other.example"))
	}
	if e.w.hit(x) != 0 || e.a.fed.actors.keyFetches.over("other.example") {
		t.Error("the named actor's server was fetched from, or its allowance of key fetches spent")
	}
	if e.queued() != 0 {
		t.Error("a forward was stored")
	}
}

// An activity id is kept to the actor that sent it (the review's R3; 7.2): mal, on bo's server, sends an Update
// of itself under the id Mastodon gives bo's own Delete (<actor>#delete); it is taken as mal's, and bo's real
// Delete under that id is still taken and handled. A repeat by the same actor is still "accepted before".
func TestInboxIDSquat(t *testing.T) {
	e := newInboxEnv(t)
	mal := "https://social.example/users/mal"
	e.w.put(mal, worldDoc{body: actorDoc(mal, "mal", "Mal", &e.other.PublicKey, nil)})
	e.post("/inbox", activity(boID, "Like", "https://here.example/users/ana", nil), e.bo(), nil)
	upd := activity(mal, "Update", map[string]any{"id": mal, "type": "Person", "inbox": mal + "/inbox"}, map[string]any{"id": boID + "#delete"})
	if w := e.post("/inbox", upd, e.signer(e.other, mal+"#main-key", sigCavage), nil); w.Code != 202 {
		t.Fatalf("mal's update: %d %s %v", w.Code, w.Body, e.last())
	}
	e.clock.add(time.Second)
	if w := e.post("/inbox", upd, e.signer(e.other, mal+"#main-key", sigCavage), nil); w.Code != 202 || e.last() != inDuplicate {
		t.Errorf("mal's own repeat: %d (%v), want accepted before", w.Code, e.last())
	}
	for {
		ok, err := e.q.step(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
	}
	del := activity(boID, "Delete", boID, map[string]any{"id": boID + "#delete"})
	if w := e.post("/inbox", del, e.bo(), nil); w.Code != 202 || e.last() != inAccepted {
		t.Errorf("bo's own Delete under the id mal used: %d (%v)", w.Code, e.last())
	}
	for {
		ok, _ := e.q.step(context.Background())
		if !ok {
			break
		}
	}
	if e.count(`SELECT count(*) FROM ap_actors WHERE ap_id = $1 AND state = 'gone'`, boID) != 1 {
		t.Error("bo's own Delete was swallowed as a repeat of mal's activity")
	}
}

// The answer to an own Delete nobody can verify does not tell whether its actor is cached here (the review's R2;
// 7.10): a garbage signature on an own-Delete-shaped body is 202 for an actor never seen, for a cached one (under
// a key id nobody keeps, and under its own kept key), and nothing is done for either. An actor on another origin
// than the key id is not an own Delete at all, and goes on as any delivery (401 here).
func TestInboxPeekOracle(t *testing.T) {
	e := newInboxEnv(t)
	e.post("/inbox", activity(boID, "Like", "https://here.example/users/ana", nil), e.bo(), nil) // bo cached now
	probe := func(x, keyID string) (int, inboxReason, string) {
		w := e.post("/inbox", activity(x, "Delete", x, nil), e.signer(e.other, keyID, sigCavage), nil)
		return w.Code, e.last(), w.Body.String()
	}
	type answer struct {
		code int
		body string
	}
	c1, r1, b1 := probe("https://social.example/users/ghost", "https://social.example/probe1#k")
	c2, r2, b2 := probe(boID, "https://social.example/probe2#k")
	c3, r3, b3 := probe(boID, boKeyID)
	if (answer{c1, b1}) != (answer{c2, b2}) || (answer{c2, b2}) != (answer{c3, b3}) || c1 != 202 {
		t.Errorf("never seen: %d %v; cached bo, another key id: %d %v; cached bo, its own key id: %d %v", c1, r1, c2, r2, c3, r3)
	}
	if e.queued() != 0 || e.count(`SELECT count(*) FROM ap_actors WHERE state = 'gone'`) != 0 {
		t.Error("an unverified Delete was acted on")
	}
	if c, r, _ := probe("https://elsewhere.example/users/z", "https://social.example/probe3#k"); c != 401 {
		t.Errorf("an actor on another origin than the key id: %d %v", c, r)
	}
	// This server's own failures keep their 503, and answer alike for both: every key turn taken, the replay
	// memory full. A key fetch that cannot be made (the address's allowance used up) is met only by a cached
	// actor, so it is answered 202 like the never-seen one, not 503 (ownFailure's comment).
	same := func(what string, want int) {
		t.Helper()
		e.clock.add(time.Second)
		c1, r1, b1 := probe("https://social.example/users/ghost", "https://social.example/probe4#k")
		e.clock.add(time.Second)
		c2, r2, b2 := probe(boID, "https://social.example/probe5#k")
		e.clock.add(time.Second)
		c3, r3, b3 := probe(boID, boKeyID)
		if c1 != want || (answer{c1, b1}) != (answer{c2, b2}) || (answer{c2, b2}) != (answer{c3, b3}) {
			t.Errorf("%s: never seen %d %v; cached bo %d %v; cached bo, its own key %d %v; want %d for all", what, c1, r1, c2, r2, c3, r3, want)
		}
	}
	for range inboxKeyAtOnce {
		e.q.keyTurns <- struct{}{}
	}
	same("every key turn taken", 503)
	for range inboxKeyAtOnce {
		<-e.q.keyTurns
	}
	was := e.a.fed.verifier.seen
	full := newReplaySet(sigSeenMax, sigPast)
	full.maxGroups = 0
	e.a.fed.verifier.seen = full
	same("the replay memory full", 503)
	e.a.fed.verifier.seen = was
	e.clock.add(actorTTL) // bo is due to be fetched again, and the address may fetch nothing more
	e.a.fed.actors.addrFetches = newLimiter(1, time.Hour)
	e.a.fed.actors.addrFetches.add(limitKey("198.51.100.7"))
	same("the address's key fetches used up", 202)
}

// An own-Delete-shaped body signed by another actor is that actor's forward, never the named actor's Delete (the
// review's m1: the origin rule on the keyForOwnDelete path). mal, on bo's server, signs "bo deletes bo": mal's key
// verifies, so the actor is mal's, not bo's: 202, nothing stored, bo untouched. A key of another origin naming bo
// is not an own Delete at all, and is a forward too.
func TestInboxOwnDeleteByAnother(t *testing.T) {
	e := newInboxEnv(t)
	e.post("/inbox", activity(boID, "Like", "https://here.example/users/ana", nil), e.bo(), nil) // bo known
	mal := "https://social.example/users/mal"
	e.w.put(mal, worldDoc{body: actorDoc(mal, "mal", "Mal", &e.other.PublicKey, nil)})
	if w := e.post("/inbox", activity(boID, "Delete", boID, map[string]any{"id": boID + "#delete"}), e.signer(e.other, mal+"#main-key", sigCavage), nil); w.Code != 202 || e.last() != inForward {
		t.Errorf("bo's Delete signed by mal, same origin: %d (%v)", w.Code, e.last())
	}
	e.allow("other.example")
	x := "https://other.example/users/x"
	e.w.put(x, worldDoc{body: actorDoc(x, "x", "", &e.other.PublicKey, nil)})
	e.clock.add(time.Second)
	if w := e.post("/inbox", activity(boID, "Delete", boID, map[string]any{"id": boID + "#delete"}), e.signer(e.other, x+"#main-key", sigCavage), nil); w.Code != 202 || e.last() != inForward {
		t.Errorf("bo's Delete signed on another origin: %d (%v)", w.Code, e.last())
	}
	if e.queued() != 0 || e.count(`SELECT count(*) FROM ap_actors WHERE ap_id = $1 AND state = 'gone'`, boID) != 0 {
		t.Error("an own Delete signed by another actor was taken as bo's")
	}
}

// A kept key follows its actor's row (the review's low 1): the cache's bound removing actors forgets their keys,
// so the actor's next delivery fetches it anew and is taken, not refused by a foreign key; a row removed beside
// the server (-purge-remote) is told to try again; and a key looked up while a drop ran is not kept after it.
func TestDBInboxKeyFollowsRow(t *testing.T) {
	e := newInboxEnv(t)
	c := e.a.fed.actors
	upd := func() []byte {
		return activity(boID, "Update", map[string]any{"id": boID, "type": "Person", "inbox": boID + "/inbox"}, nil)
	}
	if w := e.post("/inbox", upd(), e.bo(), nil); w.Code != 202 {
		t.Fatalf("bo first: %d", w.Code)
	}
	e.mustExec(`DELETE FROM ap_inbox`)
	// The bound removes bo (nothing points at bo once the row is gone).
	c.max, c.count = 0, -1
	c.added(context.Background(), 0)
	if e.count(`SELECT count(*) FROM ap_actors WHERE ap_id = $1`, boID) != 0 {
		t.Fatal("the bound did not remove bo")
	}
	if _, ok := c.keys.get(boKeyID, c.now()); ok {
		t.Error("bo's key is still kept after the bound removed bo")
	}
	c.max = remoteActorsMax
	e.clock.add(time.Second)
	if w := e.post("/inbox", upd(), e.bo(), nil); w.Code != 202 || e.last() != inAccepted {
		t.Errorf("bo after the bound removed it: %d (%v)", w.Code, e.last())
	}
	// Removed beside the server, with the key still kept: 503, and the key forgotten.
	e.mustExec(`DELETE FROM users WHERE domain = 'social.example'`)
	e.clock.add(time.Second)
	if w := e.post("/inbox", upd(), e.bo(), nil); w.Code != 503 || e.last() != inKeyBusy {
		t.Errorf("bo removed beside the server: %d (%v)", w.Code, e.last())
	}
	if _, ok := c.keys.get(boKeyID, c.now()); ok {
		t.Error("bo's key is still kept after its row went")
	}
	// A drop between the lookup and the put wins.
	gen := c.keys.generation()
	c.keys.drop(boID)
	c.keys.putAt(&keyHeld{keyID: boKeyID, actor: boID, until: c.now().Add(time.Hour)}, gen)
	if _, ok := c.keys.get(boKeyID, c.now()); ok {
		t.Error("a key looked up before a drop was kept after it")
	}
}

// The workers (the review's low 3 and 4): a row claimed more often than its attempts allow is failed at the claim,
// not handled again; a row of a domain suspended since it was taken is dropped, not handled or retried.
func TestDBInboxWorkerLimits(t *testing.T) {
	e := newInboxEnv(t)
	e.post("/inbox", activity(boID, "Like", "https://here.example/users/ana", nil), e.bo(), nil)
	var bo int64
	if err := e.a.db.QueryRow(`SELECT user_id FROM ap_actors WHERE ap_id = $1`, boID).Scan(&bo); err != nil {
		t.Fatal(err)
	}
	e.mustExec(`INSERT INTO ap_inbox (activity_id, actor_id, host, kind, body, received_at, next_at, attempts) VALUES ($2 || '/crashes', $1, 'social.example', 'Delete', '{}', $3, $3, $4)`,
		bo, boID, e.clock.now(), len(inboxRetries)+1)
	if worked, err := e.q.step(context.Background()); !worked || err != nil {
		t.Fatal(worked, err)
	}
	if e.count(`SELECT count(*) FROM ap_inbox WHERE state = 'failed'`) != 1 || e.count(`SELECT count(*) FROM ap_actors WHERE state = 'gone'`) != 0 {
		t.Error("a row past its attempts was handled, not failed")
	}
	e.mustExec(`DELETE FROM ap_inbox`)
	e.mustExec(`INSERT INTO ap_inbox (activity_id, actor_id, host, kind, body, received_at, next_at) VALUES ($2 || '/later', $1, 'social.example', 'Update', '{}', $3, $3)`, bo, boID, e.clock.now())
	e.mustExec(`INSERT INTO domain_policies (domain, policy) VALUES ('social.example', 'suspend') ON CONFLICT (domain) DO UPDATE SET policy = 'suspend'`)
	if err := e.a.fed.loadPolicy(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.clock.add(actorRecheck)
	before := e.w.total()
	if worked, err := e.q.step(context.Background()); !worked || err != nil {
		t.Fatal(worked, err)
	}
	if e.count(`SELECT count(*) FROM ap_inbox WHERE state = 'done' AND outcome LIKE '%suspended%'`) != 1 || e.w.total() != before {
		t.Errorf("a row of a suspended domain: %d done, %d fetches", e.count(`SELECT count(*) FROM ap_inbox WHERE state = 'done'`), e.w.total()-before)
	}
}

// A read turn is held only while the body is read (the second review's 1): a delivery waiting for its key to be
// fetched, or for a decode turn, holds none, so its address's later deliveries are read and taken meanwhile.
func TestInboxReadTurnFreed(t *testing.T) {
	e := newInboxEnv(t)
	e.post("/inbox", activity(boID, "Like", "https://here.example/users/ana", nil), e.bo(), nil) // bo's key kept
	slow := "https://social.example/users/slow"
	release := e.w.holdAt(slow)
	done := make(chan int, 1)
	go func() {
		done <- e.post("/inbox", activity(slow, "Like", "x", nil), e.signer(e.other, slow+"#main-key", sigCavage), nil).Code
	}()
	<-e.w.arrived
	if len(e.q.reads) != 0 {
		t.Errorf("%d read turns held while a key is fetched", len(e.q.reads))
	}
	for range inboxReadsPerAddr + 1 {
		if w := e.post("/inbox", activity(boID, "Like", "https://here.example/users/ana", nil), e.bo(), nil); w.Code != 202 {
			t.Errorf("the same address while its key fetch waits: %d (%v)", w.Code, e.last())
		}
	}
	release()
	<-done
	// Waiting for a decode turn holds no read turn either.
	for range inboxAtOnce {
		e.q.slots <- struct{}{}
	}
	e.q.waitFor = time.Hour
	go func() {
		done <- e.post("/inbox", activity(boID, "Like", "https://here.example/users/ana", nil), e.bo(), nil).Code
	}()
	for end := time.Now().Add(10 * time.Second); e.q.waiting.Load() != 1; runtime.Gosched() {
		if time.Now().After(end) {
			t.Fatal("the delivery never waited for a decode turn")
		}
	}
	if len(e.q.reads) != 0 {
		t.Errorf("%d read turns held while waiting for a decode turn", len(e.q.reads))
	}
	for range inboxAtOnce {
		<-e.q.slots
	}
	if code := <-done; code != 202 {
		t.Errorf("after the decode turns came back: %d", code)
	}
}

// A delivery straight from a trusted proxy that names no sender is said once in the log: every server behind it
// then shares one address's allowances (the second review's 1).
func TestInboxUnforwardedSaidOnce(t *testing.T) {
	e := newInboxEnv(t)
	logs := logged(t)
	for _, c := range []struct {
		from string
		xff  string
		want bool
	}{{"127.0.0.1:1", "", true}, {"172.17.0.1:1", "", true}, {"127.0.0.1:1", "203.0.113.5", false}, {"203.0.113.5:1", "", false}} {
		r := httptest.NewRequest("POST", "/inbox", nil)
		r.RemoteAddr = c.from
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := forwardMissing(e.a, r); got != c.want {
			t.Errorf("%s with X-Forwarded-For %q: %v", c.from, c.xff, got)
		}
	}
	for range 3 {
		e.postFrom("127.0.0.1:1", "/inbox", activity(boID, "Like", "https://here.example/users/ana", nil), e.bo(), nil)
		e.clock.add(time.Second)
	}
	if n := strings.Count(logs.String(), "a trusted proxy, with no X-Forwarded-For"); n != 1 {
		t.Errorf("said %d times", n)
	}
}

// The kept-key memo (the second review's 2): a drop that races a put is never missed, and a key read from a row
// before the cache wrote the actor anew is not kept after that write, while the key of the row it wrote is.
func TestKeyMemoRaces(t *testing.T) {
	missed := 0
	for range 20000 {
		k := &keyMemo{max: 10, m: map[string]*list.Element{}, l: list.New()}
		h := &keyHeld{keyID: "https://x.example/a#k", actor: "https://x.example/a", until: time.Now().Add(time.Hour)}
		gen := k.generation()
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); k.putAt(h, gen) }()
		go func() { defer wg.Done(); k.drop(h.actor) }()
		wg.Wait()
		if _, ok := k.get(h.keyID, time.Now()); ok {
			missed++
		}
	}
	if missed > 0 {
		t.Errorf("a key kept after its drop in %d of 20000 races", missed)
	}
	k := &keyMemo{max: 10, m: map[string]*list.Element{}, l: list.New()}
	t0 := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	old := &keyHeld{keyID: "https://x.example/a#k", actor: "https://x.example/a", until: t0.Add(time.Hour), fetched: t0}
	gen := k.generation()                       // a lookup reads the cached row
	k.rewritten(old.actor, t0.Add(time.Minute)) // another's fetch writes the actor anew (suspended, a new key)
	k.putAt(old, gen)
	if _, ok := k.get(old.keyID, t0); ok {
		t.Error("the key read before the actor's row was rewritten is kept after it")
	}
	fresh := &keyHeld{keyID: old.keyID, actor: old.actor, until: t0.Add(time.Hour), fetched: t0.Add(time.Minute)}
	k.putAt(fresh, k.generation())
	if _, ok := k.get(old.keyID, t0); !ok {
		t.Error("the key of the row the write wrote was not kept")
	}
}

// limiterCount is what a limiter has counted for a key, in whole events.
func limiterCount(l *limiter, key string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	if f := l.m[key]; f != nil {
		return f.n / tenths
	}
	return 0
}

// The inbox's stages in their order (7.10, and the README's "What a delivery must pass"): each refusal is reached
// with every earlier stage satisfied, and none of a later stage's work done: the body not read before its stage,
// no key fetched before the signature's, nothing charged to the sender's host before it verified, nothing decoded
// or stored before those stages.
func TestInboxStageOrder(t *testing.T) {
	e := newInboxEnv(t)
	e.post("/inbox", activity(boID, "Like", "https://here.example/users/ana", nil), e.bo(), nil) // bo's key kept
	e.a.lim.inboxHost = newLimiter(1000, time.Hour)
	own := func() []byte { return activity(boID, "Delete", boID, nil) } // stored when it passes everything
	type work struct{ read, fetched, charged, rows int }
	var read atomic.Int64
	measure := func() work {
		return work{int(read.Load()), e.w.total(), limiterCount(e.a.lim.inboxHost, "social.example"), e.count(`SELECT count(*) FROM ap_inbox`)}
	}
	e.onServer = func(r *http.Request) { r.Body = countingBody{r: r.Body, n: &read} }
	defer func() { e.onServer = nil }()
	body := func(*http.Request) {}
	for _, s := range []struct {
		stage string
		setup func() func() // returns the undo
		sign  signer
		more  func(*http.Request)
		code  int
		why   inboxReason
		did   work // what this request may have done: body bytes read (any), fetches, charges, rows
	}{
		{"the address's allowance", func() func() {
			e.a.lim.inboxAddr = newLimiter(1, time.Hour)
			e.a.lim.inboxAddr.add(limitKey("198.51.100.7"))
			return func() { e.a.lim.inboxAddr = newLimiter(inboxAddrRate, time.Hour) }
		}, e.bo(), body, 429, inAddrLimit, work{}},
		{"the content type", nil, e.bo(), func(r *http.Request) { body(r); r.Header.Set("Content-Type", "text/plain") }, 415, inType, work{}},
		{"a signature that parses", nil, nil, body, 401, inUnsigned, work{}},
		{"the domain policy", nil, e.signer(e.key, "https://unlisted.example/users/x#k", sigCavage), body, 403, inNotListed, work{}},
		{"the switch", func() func() { e.setFlag(fedFlag, false); return func() { e.setFlag(fedFlag, true) } }, e.bo(), body, 503, inPaused, work{}},
		{"the size", func() func() {
			e.onServer = func(r *http.Request) { r.Body = countingBody{r: r.Body, n: &read}; r.ContentLength = inboxBodyMax + 1 }
			return func() { e.onServer = func(r *http.Request) { r.Body = countingBody{r: r.Body, n: &read} } }
		}, e.bo(), body, 413, inTooLarge, work{}},
		{"a read turn", func() func() {
			for range inboxReadsAtOnce {
				e.q.reads <- struct{}{}
			}
			return func() {
				for range inboxReadsAtOnce {
					<-e.q.reads
				}
			}
		}, e.bo(), body, 503, inBusy, work{}},
		{"the body's time", func() func() {
			// The deadline has passed and the body has not arrived: it arrives (ends) only once the inbox has given
			// its read turn back, after the deadline won. A body that ended at once would race the deadline in the
			// read's select, and either answer would be right for a body that arrived as its time ran out.
			gone := make(chan time.Time)
			close(gone)
			arrive := make(chan struct{})
			e.q.readDeadline = func() <-chan time.Time { return gone }
			e.q.readGiven = sync.OnceFunc(func() { close(arrive) })
			e.onServer = func(r *http.Request) { r.Body = blockBody{ch: arrive} }
			return func() {
				e.q.readDeadline = func() <-chan time.Time { return time.After(inboxReadFor) }
				e.q.readGiven = nil
				e.onServer = func(r *http.Request) { r.Body = countingBody{r: r.Body, n: &read} }
			}
		}, e.bo(), body, 408, inSlowBody, work{}},
		{"the signature", nil, e.bo(), func(r *http.Request) { body(r); r.Header.Set("Digest", digestHeader([]byte("other"))) }, 401, inSignature, work{read: 1}},
		{"the host's allowance, once verified", func() func() {
			e.a.lim.inboxHost = newLimiter(1, time.Hour)
			e.a.lim.inboxHost.add("social.example")
			return func() { e.a.lim.inboxHost = newLimiter(1000, time.Hour) }
		}, e.bo(), body, 429, inGroupLimit, work{read: 1}},
		{"a decode turn", func() func() {
			for range inboxAtOnce {
				e.q.slots <- struct{}{}
			}
			e.q.waiting.Store(inboxWaitMax)
			return func() {
				e.q.waiting.Store(0)
				for range inboxAtOnce {
					<-e.q.slots
				}
			}
		}, e.bo(), body, 503, inBusy, work{read: 1, charged: 1}},
		{"the queue", func() func() {
			e.mustExec(`INSERT INTO ap_inbox (activity_id, actor_id, host, kind, received_at, next_at) SELECT 'https://filler.example/' || g, u.id, 'filler.example', 'Delete', now(), now() + interval '1 day'
				FROM generate_series(1, $1) g, (SELECT id FROM users WHERE domain <> '' LIMIT 1) u`, inboxQueueMax)
			return func() { e.mustExec(`DELETE FROM ap_inbox`) }
		}, e.bo(), body, 503, inQueueFull, work{read: 1, charged: 1}},
		{"everything passed", nil, e.bo(), body, 202, inAccepted, work{read: 1, charged: 1, rows: 1}},
	} {
		undo := func() {}
		if s.setup != nil {
			undo = s.setup()
		}
		read.Store(0)
		before := measure()
		e.clock.add(time.Second) // a fresh signature each time
		b := own()
		if s.stage == "the signature" { // not an own Delete, whose failure is a 202 (TestInboxPeekOracle)
			b = activity(boID, "Like", "https://here.example/users/ana", nil)
		}
		w := e.post("/inbox", b, s.sign, s.more)
		after := measure()
		undo()
		if w.Code != s.code || e.last() != s.why {
			t.Errorf("%s: %d (%v), want %d (%v)", s.stage, w.Code, e.last(), s.code, s.why)
		}
		got := work{min(after.read-before.read, 1), after.fetched - before.fetched, after.charged - before.charged, after.rows - before.rows}
		if s.stage == "the queue" {
			got.rows = 0 // the filler rows went in and out around it
		}
		if got != s.did {
			t.Errorf("%s: did %+v, want %+v", s.stage, got, s.did)
		}
	}
}

// closedCh is a channel already closed: a blockBody on it ends at once.
func closedCh() chan struct{} { c := make(chan struct{}); close(c); return c }

// A forged signature charges nothing to the server it names (the review's R1; 7.10): 300 requests with a
// well-formed Signature header naming a key id on social.example and no valid signature leave social.example's
// allowance of deliveries whole, so bo is still taken; requests naming made-up key ids there spend the sending
// address's allowance of key fetches and never the domain's, so bo's rotated key is still fetched after them.
func TestInboxForgedSpendsGroup(t *testing.T) {
	e := newInboxEnv(t)
	if w := e.post("/inbox", activity(boID, "Like", "https://here.example/users/ana", nil), e.bo(), nil); w.Code != 202 {
		t.Fatalf("bo first: %d", w.Code)
	}
	codes := map[int]int{}
	for range inboxHostRate {
		w := e.postFrom("203.0.113.9:1", "/inbox", []byte(`{}`), nil, func(r *http.Request) {
			r.Header.Set("Signature", `keyId="https://social.example/nobody#k",algorithm="rsa-sha256",headers="(request-target) host date",signature="AAAA"`)
		})
		codes[w.Code]++
	}
	if w := e.post("/inbox", activity(boID, "Like", "https://here.example/users/ana", nil), e.bo(), nil); w.Code != 202 {
		t.Errorf("after %v forged requests, bo's server: %d %s", codes, w.Code, w.Body)
	}
	// Made-up key ids on bo's host, with signatures that parse: each costs the sending address a fetch, at most
	// keyFetchesPerAddr, and none is charged to social.example.
	for i := range keyFetchesPerAddr + 10 {
		id := fmt.Sprintf("https://social.example/made-up/%d", i)
		e.postFrom("203.0.113.10:1", "/inbox", activity(id, "Like", "x", nil), e.signer(e.other, id+"#k", sigCavage), nil)
	}
	if e.a.fed.actors.keyFetches.over("social.example") {
		t.Error("forged requests spent social.example's allowance of key fetches")
	}
	e.clock.add(actorRecheck)
	e.w.put(boID, worldDoc{body: actorDoc(boID, "bo", "Bo", &e.other.PublicKey, nil)})
	if w := e.post("/inbox", activity(boID, "Like", "https://here.example/users/ana", nil), e.signer(e.other, boKeyID, sigCavage), nil); w.Code != 202 {
		t.Errorf("bo's rotated key after the made-up ones: %d (%v)", w.Code, e.last())
	}
}

// blockBody is a request body that never arrives until the test lets it go.
type blockBody struct{ ch chan struct{} }

func (b blockBody) Read(p []byte) (int, error) { <-b.ch; return 0, io.EOF }
func (b blockBody) Close() error               { return nil }

// Bodies that never arrive cannot hold the inbox (the review's R4; 7.10): one address holds at most
// inboxReadsPerAddr read turns, the rest of its requests are 503 at once, and a real delivery from elsewhere is
// taken meanwhile; eight addresses can hold every turn only until a body's time runs out, when each is answered
// 408 and its turn given back. The deadline is the test's (q.readDeadline), so nothing waits on the clock.
func TestInboxSlowBodies(t *testing.T) {
	e := newInboxEnv(t)
	e.post("/inbox", activity(boID, "Like", "https://here.example/users/ana", nil), e.bo(), nil)
	expire := make(chan time.Time)
	e.q.readDeadline = func() <-chan time.Time { return expire }
	started := make(chan struct{}, 2*inboxReadsAtOnce)
	e.q.reading = func() { started <- struct{}{} }
	given := make(chan struct{}, 2*inboxReadsAtOnce)
	e.q.readGiven = func() { given <- struct{}{} }
	trickle := func(addrs []string) (chan int, chan struct{}) {
		ch, done := make(chan struct{}), make(chan int, len(addrs))
		for _, from := range addrs {
			go func() {
				req := httptest.NewRequest("POST", "https://here.example/inbox", blockBody{ch})
				req.Header.Set("Content-Type", asType)
				req.Header.Set("Signature", `keyId="https://social.example/nobody#k",algorithm="rsa-sha256",headers="(request-target) host date",signature="AAAA"`)
				req.RemoteAddr = from
				w := httptest.NewRecorder()
				e.h.ServeHTTP(w, req)
				done <- w.Code
			}()
		}
		return done, ch
	}
	// From one address: two read, six are refused at once.
	one := make([]string, inboxReadsAtOnce)
	for i := range one {
		one[i] = "203.0.113.9:1"
	}
	done, ch := trickle(one)
	for range inboxReadsPerAddr {
		<-started
	}
	refused := 0
	for range inboxReadsAtOnce - inboxReadsPerAddr {
		if <-done == 503 {
			refused++
		}
	}
	if w := e.post("/inbox", activity(boID, "Like", "https://here.example/users/ana", nil), e.bo(), nil); w.Code != 202 || refused != inboxReadsAtOnce-inboxReadsPerAddr {
		t.Errorf("bo while one address trickles: %d (%v); %d of that address's requests refused at once", w.Code, e.last(), refused)
	}
	<-started // bo's own read
	// The time runs out: each turn is given back at once (the answer is written before the handler waits for its
	// read to end, which on a real connection the read deadline ends, and here the test, by letting the bodies go).
	expire <- time.Time{}
	expire <- time.Time{}
	for range inboxReadsPerAddr {
		<-given
	}
	if len(e.q.reads) != 0 {
		t.Errorf("%d read turns held after the time ran out", len(e.q.reads))
	}
	close(ch)
	for range inboxReadsPerAddr {
		if code := <-done; code != 408 {
			t.Errorf("a body that did not arrive: %d", code)
		}
	}
	// From eight addresses: every turn held until the time runs out, then given back.
	var eight []string
	for i := range inboxReadsAtOnce {
		eight = append(eight, fmt.Sprintf("203.0.113.%d:1", 40+i))
	}
	done, ch = trickle(eight)
	for range inboxReadsAtOnce {
		<-started
	}
	if w := e.post("/inbox", activity(boID, "Like", "https://here.example/users/ana", nil), e.bo(), nil); w.Code != 503 || e.last() != inBusy {
		t.Errorf("bo while eight addresses hold every read turn: %d (%v)", w.Code, e.last())
	}
	for range inboxReadsAtOnce {
		expire <- time.Time{}
	}
	for range inboxReadsAtOnce {
		<-given
	}
	if w := e.post("/inbox", activity(boID, "Like", "https://here.example/users/ana", nil), e.bo(), nil); w.Code != 202 {
		t.Errorf("bo once the time ran out: %d (%v)", w.Code, e.last())
	}
	close(ch)
	for range inboxReadsAtOnce {
		if code := <-done; code != 408 {
			t.Errorf("a body that did not arrive: %d", code)
		}
	}
	if len(e.q.reads) != 0 {
		t.Errorf("%d read turns still held", len(e.q.reads))
	}
}

// The replay memory's clock step is said once in the log (7.2).
func TestDBInboxClockStep(t *testing.T) {
	e := newInboxEnv(t)
	logs := logged(t)
	e.post("/inbox", activity(boID, "Like", "x", nil), e.bo(), nil)
	e.clock.add(-3 * time.Hour)
	for range 2 {
		e.post("/inbox", activity(boID, "Like", "x", nil), e.bo(), nil)
	}
	if n := strings.Count(logs.String(), "the clock stepped back"); n != 1 {
		t.Errorf("the clock step was logged %d times", n)
	}
}

// BenchmarkInboxRequest is a delivery through the whole server with its key kept (keyMemo): the checks before
// the body, reading it, the signature, the decode, the origin rule and storing the row, one statement against
// the test database. Budget (section 12): under 2 ms and 400 allocations on the runner. Each run sends its own
// activity, signed before the timer starts.
func BenchmarkInboxRequest(b *testing.B) {
	db := testDB(b)
	a, _ := discoveryApp(b, "https://here.example")
	a.db, a.fed.db = db, db
	if err := a.setFlags(map[string]bool{fedFlag: true}); err != nil {
		b.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO domain_policies (domain, policy) VALUES ('social.example', 'allow')`); err != nil {
		b.Fatal(err)
	}
	f := a.fed
	if f.verifier == nil {
		v, _ := newSigVerifier(f.origin.url, f.inboxKeyFor)
		f.verifier = v
		f.inbox = newInboxQueue(f, func() bool { return false })
		f.actors = newActorCache(f, nil)
	}
	f.loadPolicy(context.Background())
	f.inbox.manual = true
	f.verifier.built = time.Time{}
	key, _ := sigTestKeys()
	var uid int64
	if err := db.QueryRow(`INSERT INTO users (username, display_name, pw_hash, domain) VALUES ('bo@social.example', 'bo', '!', 'social.example') RETURNING id`).Scan(&uid); err != nil {
		b.Fatal(err)
	}
	f.actors.keys.put(&keyHeld{keyID: boKeyID, actor: boID, domain: "social.example", user: uid, key: &key.PublicKey, until: time.Now().Add(time.Hour)})
	a.lim.inbox, a.lim.inboxAddr = newLimiter(1<<30, time.Hour), newLimiter(1<<30, time.Hour)
	h := a.routes(staticOf(b, nil))
	// stored is an actor's own Delete, which is written to the queue; dropped a Follow, counted and dropped
	// with no statement: the request's own cost without the database. unsigned-256KB is a request with no
	// signature and a body at the cap, refused before the body is read; unknown-delete-256KB a Delete at the cap
	// from an actor never seen, under a key nobody keeps: the body read and digested, peekOwnDelete, its one
	// indexed lookup, and 202 with no fetch.
	ghost := "https://social.example/users/ghost"
	pad := strings.Repeat("x", inboxBodyMax-400)
	for _, c := range []struct {
		kind string
		code int
	}{{"stored", 202}, {"dropped", 202}, {"unsigned-256KB", 401}, {"unknown-delete-256KB", 202}} {
		kind := c.kind
		b.Run(kind, func(b *testing.B) {
			// The two 256 KB cases send one request again and again (its body read anew from a reader reset each
			// run): the unsigned one is refused before its body is read, and the unknown actor's Delete before its
			// signature is remembered, so a repeat is the same work, and b.N bodies of 256 KB would not fit.
			big := strings.HasSuffix(kind, "256KB")
			n := b.N
			if big {
				n = 1
			}
			reqs := make([]*http.Request, n)
			var again *bytes.Reader
			var bigBody []byte
			for i := range reqs {
				id := fmt.Sprintf("%s/activities/%s-%d-%d", boID, kind, b.N, i)
				body := activity(boID, "Delete", boID, map[string]any{"id": id, "to": []string{"https://www.w3.org/ns/activitystreams#Public"}, "padding": strings.Repeat("x", 1500)})
				sign, keyID := true, boKeyID
				switch kind {
				case "dropped":
					body = activity(boID, "Like", "https://here.example/users/ana", map[string]any{"id": id, "padding": strings.Repeat("x", 1500)})
				case "unsigned-256KB":
					body, sign = activity(ghost, "Like", "https://here.example/users/ana", map[string]any{"id": id, "padding": pad}), false
				case "unknown-delete-256KB":
					body, keyID = activity(ghost, "Delete", ghost, map[string]any{"id": ghost + "/" + id[len(boID):], "padding": pad}), ghost+"#main-key"
				}
				req, _ := http.NewRequest("POST", "https://here.example/inbox", bytes.NewReader(body))
				req.Header.Set("Content-Type", asType)
				if sign {
					if err := httpSigner(key, keyID, sigCavage, time.Now, time.Time{})(req, body); err != nil {
						b.Fatal(err)
					}
				}
				reqs[i] = received(req, body)
				if big {
					again, bigBody = bytes.NewReader(body), body
					reqs[i].Body = io.NopCloser(again)
				}
			}
			if _, err := db.Exec(`DELETE FROM ap_inbox`); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if i%(inboxGroupQueued-1) == inboxGroupQueued-2 { // the sender group's share of the queue, emptied untimed
					b.StopTimer()
					if _, err := db.Exec(`DELETE FROM ap_inbox`); err != nil {
						b.Fatal(err)
					}
					b.StartTimer()
				}
				r := reqs[0]
				if big {
					again.Reset(bigBody)
				} else {
					r = reqs[i]
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != c.code {
					b.Fatalf("%d %s", w.Code, w.Body)
				}
			}
		})
	}
}
