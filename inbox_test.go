package main

// The inbox and its queue (docs/federation.md, 6.3, 7, 8.1, 8.5, 8.6, 9.3 and 12; FWR item 1): deliveries
// signed as other servers sign them, to a federated instance at https://here.example whose actor cache asks the
// stub world of remote_test.go, on a clock the test moves. The workers are driven by the test (inboxQueue.step),
// never started, so no test waits on a goroutine that polls the database.

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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
	in.RemoteAddr = "198.51.100.7:4000"
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
	body := activity(boID, "Follow", "https://here.example/users/ana", nil)
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
		{"an activity id of another origin", "/inbox", activity(boID, "Follow", "https://here.example/users/ana", map[string]any{"id": "https://other.example/activities/1"}), e.bo(), nil, 400, inForeignID, false},
		{"too large", "/inbox", append(append([]byte(`{"x":"`), bytes.Repeat([]byte("x"), inboxBodyMax)...), `"}`...), e.bo(), nil, 413, inTooLarge, false},
		{"too deep", "/inbox", []byte(deep), e.bo(), nil, 400, inUndecodable, false},
		{"too many array elements", "/inbox", activity(boID, "Follow", "https://here.example/users/ana", map[string]any{"to": json.RawMessage(wide)}), e.bo(), nil, 400, inUndecodable, false},
		{"not an Activity Streams type", "/inbox", body, e.bo(), func(r *http.Request) { r.Header.Set("Content-Type", "application/json") }, 415, inType, false},
		{"ld+json of another profile", "/inbox", body, e.bo(), func(r *http.Request) {
			r.Header.Set("Content-Type", `application/ld+json; profile="https://example.com/other"`)
		}, 415, inType, false},
		{"a key of another domain that is not allowed", "/inbox", activity("https://unlisted.example/users/x", "Follow", "https://here.example/users/ana", nil), e.signer(e.key, "https://unlisted.example/users/x#main-key", sigCavage), nil, 403, inNotListed, false},
		{"a key on an address", "/inbox", body, e.signer(e.key, "https://10.0.0.1/users/x#main-key", sigCavage), nil, 403, inNotListed, false},
		// The key is bo's, and the body names someone else on bo's server: not bo's activity (7.3).
		{"key owner not the actor", "/inbox", activity("https://social.example/users/eve", "Follow", "https://here.example/users/ana", nil), e.bo(), nil, 202, inForward, false},
		// A key on another port of bo's host is never fetched (port 443 only) and so never vouches for bo.
		{"a key on another port of the actor's host", "/inbox", body, e.signer(e.key, "https://social.example:8443/users/bo#main-key", sigCavage), nil, 401, inSignature, false},
		{"the personal inbox of a name nobody has", "/users/nobody/inbox", activity(boID, "Follow", "https://here.example/users/nobody", nil), e.bo(), nil, 202, inDropped, false},
		{"the instance actor's inbox", "/actor/inbox", activity(boID, "Follow", "https://here.example/actor", nil), e.bo(), nil, 202, inDropped, false},
		{"replayed, first", "/inbox", replayed, e.bo(), func(r *http.Request) { replayReq = r.Clone(context.Background()) }, 202, inDropped, false},
		// Unsigned, from an actor nobody has seen: nothing is fetched for it (the count below).
		{"unsigned, naming an actor never seen", "/inbox", activity("https://social.example/users/zed", "Follow", "https://here.example/users/ana", nil), nil, nil, 401, inUnsigned, false},
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
	if w := e.post("/inbox", activity(boID, "Follow", "https://here.example/users/ana", nil), e.signer(e.key, "https://other.example/users/bo#main-key", sigCavage), nil); w.Code != 202 || e.last() != inForward {
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
		if w := e.post("/inbox", activity(boID, "Follow", "x", nil), s, nil); w.Code != 401 || e.last() != inSignature {
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

	// Allowances: per sender group, and per address before the signature is read.
	e.a.lim.inbox = newLimiter(1, time.Hour)
	e.post("/inbox", body, e.bo(), nil)
	if w := e.post("/inbox", body, e.bo(), nil); w.Code != 429 || e.last() != inGroupLimit {
		t.Errorf("past the sender group's allowance: %d (%v)", w.Code, e.last())
	}
	e.a.lim.inbox = newLimiter(inboxGroupRate, inboxGroupWindow)
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
	if n.Dropped["Follow"] == 0 || n.Answers[inForward.String()] == 0 {
		t.Errorf("the 202s are not counted: %v %v", n.Dropped, n.Answers)
	}
}

// The keys a delivery can be verified with (7.4, 6.3, FWR 1's "required"): only a fresh row's of an actor
// neither gone nor suspended, except an actor's own Delete against the cached key; a rotated key taken after one
// refetch; a Delete from an actor never seen dropped with no fetch.
func TestInboxKeys(t *testing.T) {
	e := newInboxEnv(t)
	follow := func() []byte { return activity(boID, "Follow", "https://here.example/users/ana", nil) }
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
	if w := e.post("/inbox", activity(ev, "Follow", "x", nil), e.signer(e.other, ev+"#main-key", sigCavage), nil); w.Code != 401 {
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
	e.a.fed.actors.keyFetches = newLimiter(3, time.Hour)
	for i := range 10 {
		id := fmt.Sprintf("https://big.example/users/x%d", i)
		e.post("/inbox", activity(id, "Follow", "x", nil), e.signer(e.key, id+"#main-key", sigCavage), nil)
	}
	if n := e.w.total(); n != 3 {
		t.Errorf("10 distinct key ids on one host cost %d fetches, want the allowance of 3", n)
	}
	if e.last() != inKeyBusy {
		t.Errorf("past the allowance: %v, want a 503", e.last())
	}
	// The same failing key id again within 10 minutes: remembered, no fetch.
	e.a.fed.actors.keyFetches = newLimiter(100, time.Hour)
	id := "https://big.example/users/x0"
	e.post("/inbox", activity(id, "Follow", "x", nil), e.signer(e.key, id+"#main-key", sigCavage), nil)
	if n := e.w.total(); n != 3 {
		t.Errorf("a remembered failure was fetched again: %d", n)
	}
	// Another domain has an allowance of its own.
	e.allow("other.example")
	oid := "https://other.example/users/x"
	e.post("/inbox", activity(oid, "Follow", "x", nil), e.signer(e.key, oid+"#main-key", sigCavage), nil)
	if n := e.w.hit(oid); n != 1 {
		t.Errorf("another domain's key: %d fetches", n)
	}
	// Subdomains share their domain's allowance: a wildcard name gets one, not one per name (senderGroup).
	c := newActorCache(&federation{}, nil)
	c.keyFetches = newLimiter(1, time.Hour)
	if !c.spend("https://a.wild.example/k") || c.spend("https://b.wild.example/k") || !c.spend("https://wild2.example/k") {
		t.Error("the allowance of key fetches is not per sender group")
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
		done <- e.post("/inbox", activity(boID, "Follow", "https://here.example/users/ana", nil), e.bo(), nil)
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
		copies = append(copies, received(req, same))
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
		{"Follow", activity(boID, "Follow", "https://here.example/users/ana", nil), inDropped},
		{"Create", activity(boID, "Create", map[string]any{"id": boID + "/notes/1", "type": "Note", "attributedTo": boID, "content": "hi"}, nil), inDropped},
		{"Like", activity(boID, "Like", "https://here.example/users/ana/posts/1", nil), inDropped},
		{"Undo", activity(boID, "Undo", boID+"/activities/x", nil), inDropped},
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
	// A Follow from b.example is taken and dropped: nothing is answered (no Accept or Reject goes out).
	body := activity(anaURL, "Follow", "https://here.example/users/cyd", nil)
	sign := httpSigner(key, be.a.fed.origin.url+keyID, sigCavage, e.clock.now, time.Time{})
	if _, err := out.do(context.Background(), fetchReq{kind: fetchDelivery, url: "https://here.example/inbox", body: body, sign: sign}); err != nil || e.last() != inDropped {
		t.Errorf("a Follow: %v (%v)", err, e.last())
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
	follow := func() []byte { return activity(boID, "Follow", "https://here.example/users/ana", nil) }
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
		return activity(actor, "Follow", "https://here.example/users/ana", more)
	}
	if w := e.post("/inbox", follow(boID, nil), e.bo(), nil); w.Code != 202 {
		t.Fatalf("bo's first delivery: %d", w.Code)
	}
	fetched := e.w.total()
	var read atomic.Int64
	pad := strings.Repeat("x", inboxBodyMax-400)
	for i := range 50 {
		zed := fmt.Sprintf("https://social.example/users/zed%d", i)
		w := e.post("/inbox", follow(zed, map[string]any{"padding": pad}), nil, func(r *http.Request) {
			r.Body = countingBody{r: r.Body, n: &read}
		})
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
			done <- e.post("/inbox", follow(id, nil), e.signer(e.other, id+"#main-key", sigCavage), nil).Code
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

// The replay memory's clock step is said once in the log (7.2).
func TestDBInboxClockStep(t *testing.T) {
	e := newInboxEnv(t)
	logs := logged(t)
	e.post("/inbox", activity(boID, "Follow", "x", nil), e.bo(), nil)
	e.clock.add(-3 * time.Hour)
	for range 2 {
		e.post("/inbox", activity(boID, "Follow", "x", nil), e.bo(), nil)
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
					body = activity(boID, "Follow", "https://here.example/users/ana", map[string]any{"id": id, "padding": strings.Repeat("x", 1500)})
				case "unsigned-256KB":
					body, sign = activity(ghost, "Follow", "https://here.example/users/ana", map[string]any{"id": id, "padding": pad}), false
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
