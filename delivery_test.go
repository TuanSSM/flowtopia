package main

// The delivery queue (docs/federation.md 7.1, 8, 9.1, 12 and 14.2; FWR item 2): activities enqueued by a federated
// instance at https://here.example, signed by its instance actor and sent through the guarded fetcher to a stub
// that stands for other servers' inboxes, verifies every signature as a receiver would, and answers as each test
// asks. The queue's clock is the test's (a tclock), the pacer moves that clock instead of waiting, and the workers
// are driven by the test (deliveryQueue.step) unless a test starts them to stop them, so no test waits on a
// goroutine that polls the database or on the wall clock.

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fanNames is the wildcard the delivery stub also answers: h0.fan.example, h1.fan.example and so on, for tests
// that need more hosts than actorNames has.
const fanNames = "*.fan.example"

var deliveryCert = sync.OnceValues(func() (tls.Certificate, *x509.CertPool) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(3), Subject: pkix.Name{CommonName: "flowa delivery stub"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true, DNSNames: append(slices.Clone(actorNames), fanNames),
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

// deliveryStub starts h behind TLS and returns a production fetcher (the guard on) whose test-only dial override
// resolves actorNames and every name under fan.example to it, and every other name to nothing.
func deliveryStub(t testing.TB, h http.Handler) *fetcher {
	t.Helper()
	cert, pool := deliveryCert()
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
		if slices.Contains(actorNames, host) || strings.HasSuffix(host, ".fan.example") && strings.Count(host, ".") == 2 {
			return []string{stub}
		}
		return nil
	}}
	f.tr.TLSClientConfig.RootCAs = pool
	t.Cleanup(f.tr.CloseIdleConnections)
	return f
}

// stubReq is one request the stub took.
type stubReq struct {
	host, path, form string // form: "cavage" or "rfc9421", by the headers sent
	sig              string // "" when the signature verified as a receiver would check it, the refusal otherwise
	id               string // the activity's id
}

// inboxStub stands for other servers' inboxes: it verifies each delivery with the instance actor's key, answers
// with answer (202 when nil), can hold every request to a host until the test lets it go, and counts the requests
// in flight per host.
type inboxStub struct {
	mu       sync.Mutex
	now      func() time.Time
	pub      *rsa.PublicKey
	reqs     []stubReq
	answer   func(host, path, form string) (int, string) // status and Retry-After
	hold     map[string]chan struct{}
	holdAll  chan struct{} // when set, every request waits for it
	arrived  chan string
	inFlight map[string]int
	most     map[string]int // the most requests in flight to one host at once
	total    int            // the most in flight at once, all hosts
	cur      int
}

func newInboxStub(now func() time.Time, pub *rsa.PublicKey) *inboxStub {
	return &inboxStub{now: now, pub: pub, hold: map[string]chan struct{}{}, arrived: make(chan string, 64), inFlight: map[string]int{}, most: map[string]int{}}
}

func (s *inboxStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	host := strings.ToLower(r.Host)
	form := "cavage"
	if r.Header.Get("Signature-Input") != "" {
		form = "rfc9421"
	}
	sig := "not a delivery"
	if r.Method == "POST" {
		v, err := newSigVerifier("https://"+r.Host, func(_ context.Context, id string) (*rsa.PublicKey, error) {
			if id == hereKeyID {
				return s.pub, nil
			}
			return nil, errors.New("not the instance actor's key")
		})
		if err == nil {
			v.built, v.now = time.Time{}, s.now
			if _, err = v.verifyRequest(r.Context(), r, body); err == nil {
				sig = ""
			} else {
				sig = err.Error()
			}
		}
	}
	var act struct {
		ID string `json:"id"`
	}
	json.Unmarshal(body, &act)
	s.mu.Lock()
	s.reqs = append(s.reqs, stubReq{host: host, path: r.URL.Path, form: form, sig: sig, id: act.ID})
	s.inFlight[host]++
	s.cur++
	s.most[host] = max(s.most[host], s.inFlight[host])
	s.total = max(s.total, s.cur)
	ch, all, answer := s.hold[host], s.holdAll, s.answer
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.inFlight[host]--
		s.cur--
		s.mu.Unlock()
	}()
	if all != nil {
		ch = all
	}
	if ch != nil {
		s.arrived <- host
		select {
		case <-ch:
		case <-r.Context().Done():
			return
		}
	}
	code, ra := 202, ""
	if answer != nil {
		code, ra = answer(host, r.URL.Path, form)
	}
	if ra != "" {
		w.Header().Set("Retry-After", ra)
	}
	if r.Method == "GET" && code == 200 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"links":[]}`))
		return
	}
	w.WriteHeader(code)
}

func (s *inboxStub) requests() []stubReq {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.reqs)
}

// holdAt makes every request to host wait until the returned function is called.
func (s *inboxStub) holdAt(host string) func() {
	ch := make(chan struct{})
	s.mu.Lock()
	s.hold[host] = ch
	s.mu.Unlock()
	return sync.OnceFunc(func() { close(ch) })
}

// delivEnv is a federated here.example whose delivery queue is driven by the test and sends to the stub.
type delivEnv struct {
	*remoteEnv
	q *deliveryQueue
	s *inboxStub
	n atomic.Int64
}

func newDelivEnv(t *testing.T) *delivEnv {
	t.Helper()
	r := newRemoteEnv(t)
	r.setFlag(fedFlag, true)
	q := r.a.fed.out
	q.manual = true
	q.now, q.built = r.clock.now, time.Time{}
	q.mu.Lock()
	q.floor = time.Time{}
	q.mu.Unlock()
	q.jitter = func() float64 { return 0 }
	q.pace.wait = func(ctx context.Context, d time.Duration) error { r.clock.add(d); return ctx.Err() }
	s := newInboxStub(r.clock.now, r.pub)
	q.fetch = deliveryStub(t, s)
	q.fetch.origin = r.a.fed.origin
	return &delivEnv{remoteEnv: r, q: q, s: s}
}

// activityBody is an activity of the instance actor with its own id.
func (e *delivEnv) activityBody() (string, []byte) {
	id := fmt.Sprintf("https://here.example/activities/%d", e.n.Add(1))
	b, _ := json.Marshal(map[string]any{"@context": "https://www.w3.org/ns/activitystreams", "id": id, "type": "Update", "actor": "https://here.example/actor", "object": "https://here.example/actor"})
	return id, b
}

// send enqueues one activity for the inboxes given as host/path ("a.example/inbox").
func (e *delivEnv) send(inboxes ...string) (string, enqueued) {
	e.t.Helper()
	id, body := e.activityBody()
	to := make([]deliveryTarget, len(inboxes))
	for i, in := range inboxes {
		host, _, _ := strings.Cut(in, "/")
		to[i] = deliveryTarget{Actor: "https://" + host + "/users/someone", Inbox: "https://" + in}
	}
	out, err := e.q.enqueue(context.Background(), instanceActor, id, body, to)
	if err != nil {
		e.t.Fatal(err)
	}
	return id, out
}

// drain steps until nothing is claimed, and fails rather than hangs when that takes more than most steps: a
// regression that keeps a sent row (or claims one forever) must fail the test, not stall it.
func (e *delivEnv) drain(most int) int {
	e.t.Helper()
	for n := 0; ; n++ {
		if !e.step() {
			return n
		}
		if n >= most {
			e.t.Fatalf("still claiming rows after %d steps", most)
		}
	}
}

func (e *delivEnv) step() bool {
	e.t.Helper()
	worked, err := e.q.step(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	return worked
}

// row is a delivery's state, by its inbox and its activity.
func (e *delivEnv) row(id, inbox string) (state string, attempts int, next time.Time, lastError string) {
	e.t.Helper()
	err := e.a.db.QueryRow(`SELECT d.state, d.attempts, d.next_at, d.last_error FROM ap_deliveries d JOIN ap_outbox o ON o.id = d.outbox_id WHERE o.ap_id = $1 AND d.inbox = $2`, id, "https://"+inbox).Scan(&state, &attempts, &next, &lastError)
	if err != nil {
		return "gone", 0, time.Time{}, ""
	}
	return
}

func (e *delivEnv) answer(f func(host, path, form string) (int, string)) {
	e.s.mu.Lock()
	e.s.answer = f
	e.s.mu.Unlock()
}

// hits counts the requests the stub took for an activity, with the forms they came in.
func (e *delivEnv) hits(id string) []string {
	var out []string
	for _, r := range e.s.requests() {
		if r.id == id {
			if r.sig != "" {
				e.t.Errorf("a delivery of %s did not verify: %s", id, r.sig)
			}
			out = append(out, r.form)
		}
	}
	return out
}

// TestDBDeliveryQueue: the schedule on the test's clock, the horizon, two workers never sending a row twice, a
// lease that ran out, the other signature form, a refusal in both, 410 and the other statuses (8.3, 7.1, F9).
func TestDBDeliveryQueue(t *testing.T) {
	t.Run("schedule", func(t *testing.T) {
		e := newDelivEnv(t)
		e.answer(func(string, string, string) (int, string) { return 503, "" })
		id, out := e.send("a.example/inbox")
		if out.Queued != 1 {
			t.Fatalf("enqueued: %+v", out)
		}
		for i, wait := range deliveryRetries {
			if !e.step() {
				t.Fatalf("attempt %d: nothing claimed", i+1)
			}
			st, at, next, why := e.row(id, "a.example/inbox")
			if st != "queued" || at != i+1 || !next.Equal(e.clock.now().Add(wait)) || !strings.Contains(why, "503") {
				t.Fatalf("after attempt %d: %s, %d attempts, due %v (now %v), %q; want due in %v", i+1, st, at, next, e.clock.now(), why, wait)
			}
			if e.step() {
				t.Fatalf("a row was claimed before it was due (after attempt %d)", i+1)
			}
			e.clock.add(wait)
		}
		if !e.step() {
			t.Fatal("the last attempt was not made")
		}
		if st, at, _, why := e.row(id, "a.example/inbox"); st != "failed" || at != len(deliveryRetries)+1 || !strings.HasPrefix(why, "given up after 11 attempts") {
			t.Errorf("after the last attempt: %s %d %q", st, at, why)
		}
		if n := len(e.hits(id)); n != 11 {
			t.Errorf("%d requests, want 11 attempts (8.3)", n)
		}
		// The jitter moves a wait by at most a fifth either way.
		for _, j := range []float64{deliveryJitter, -deliveryJitter} {
			e.q.jitter = func() float64 { return j }
			id, _ := e.send("b.example/inbox")
			e.step()
			if _, _, next, _ := e.row(id, "b.example/inbox"); !next.Equal(e.clock.now().Add(time.Duration(float64(time.Minute) * (1 + j)))) {
				t.Errorf("jitter %v: due %v, now %v", j, next, e.clock.now())
			}
		}
	})

	t.Run("horizon", func(t *testing.T) {
		e := newDelivEnv(t)
		id, _ := e.send("a.example/inbox")
		e.mustExec(`UPDATE ap_deliveries SET created_at = created_at - interval '49 hours'`)
		if !e.step() {
			t.Fatal("nothing claimed")
		}
		if st, _, _, why := e.row(id, "a.example/inbox"); st != "failed" || why != "not delivered within 48 hours" || len(e.hits(id)) != 0 {
			t.Errorf("a row past the horizon: %s %q, %d requests", st, why, len(e.hits(id)))
		}
		// A retry that would fall past the horizon gives up instead of waiting for it.
		e.answer(func(string, string, string) (int, string) { return 500, "" })
		id, _ = e.send("b.example/inbox")
		e.mustExec(`UPDATE ap_deliveries SET created_at = created_at - interval '47 hours 59 minutes 30 seconds' WHERE inbox = 'https://b.example/inbox'`)
		e.step()
		if st, _, _, why := e.row(id, "b.example/inbox"); st != "failed" || !strings.HasPrefix(why, "given up after 1 attempts") {
			t.Errorf("a retry past the horizon: %s %q", st, why)
		}
	})

	t.Run("two workers", func(t *testing.T) {
		e := newDelivEnv(t)
		hosts := []string{"a.example", "b.example", "social.example", "remote.example", "other.example", "moved.example", "big.example", "example.com"}
		var ids []string
		for range 5 {
			in := make([]string, len(hosts))
			for i, h := range hosts {
				in[i] = h + "/inbox"
			}
			id, out := e.send(in...)
			if out.Queued != len(hosts) {
				t.Fatalf("enqueued %+v", out)
			}
			ids = append(ids, id)
		}
		var wg sync.WaitGroup
		var sent atomic.Int32
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for n := 0; ; n++ {
					if n > 200 { // a regression that keeps a sent row must fail, not hang
						t.Error("still claiming rows after 200 steps")
						return
					}
					worked, err := e.q.step(context.Background())
					if err != nil {
						t.Error(err)
						return
					}
					if !worked {
						return
					}
					sent.Add(1)
				}
			}()
		}
		wg.Wait()
		seen := map[string]int{}
		for _, r := range e.s.requests() {
			seen[r.host+" "+r.id]++
			if r.sig != "" {
				t.Errorf("a delivery did not verify: %s", r.sig)
			}
		}
		for _, id := range ids {
			for _, h := range hosts {
				if seen[h+" "+id] != 1 {
					t.Errorf("%s to %s was sent %d times", id, h, seen[h+" "+id])
				}
			}
		}
		if n := e.count(`SELECT count(*) FROM ap_deliveries`); n != 0 || sent.Load() < 40 {
			t.Errorf("%d rows left, %d steps sent", n, sent.Load())
		}
		for h, n := range e.s.most {
			if n != 1 {
				t.Errorf("%d requests at once to %s, want one at a time (8.2)", n, h)
			}
		}
		if e.count(`SELECT count(*) FROM ap_hosts WHERE delivered = 5 AND state = 'ok' AND last_success IS NOT NULL`) != len(hosts) {
			t.Error("the hosts' counters do not keep what was sent (8.5)")
		}
	})

	t.Run("lease", func(t *testing.T) {
		e := newDelivEnv(t)
		// A row whose worker took longer than half its lease before sending has the lease renewed first.
		id, _ := e.send("a.example/inbox")
		e.q.claimed = func(int64) { e.clock.add(deliveryLease/2 + time.Second) }
		release := e.s.holdAt("a.example")
		done := make(chan bool)
		go func() { done <- e.step() }()
		<-e.s.arrived
		var lease time.Time
		if err := e.a.db.QueryRow(`SELECT locked_until FROM ap_deliveries`).Scan(&lease); err != nil || !lease.Equal(e.clock.now().Add(deliveryLease)) {
			t.Errorf("the lease during the send: %v (now %v) %v", lease, e.clock.now(), err)
		}
		release()
		<-done
		if len(e.hits(id)) != 1 || e.count(`SELECT count(*) FROM ap_deliveries`) != 0 {
			t.Errorf("the row was not sent once: %v", e.hits(id))
		}
		// A row whose lease ran out before its send, and which another worker claimed meanwhile, is not sent by the
		// first: only the second sends it.
		id, _ = e.send("b.example/inbox")
		var second *claimedRow
		e.q.claimed = func(int64) {
			e.clock.add(deliveryLease + time.Second)
			c, err := e.q.claim(context.Background(), nil)
			if err != nil || c == nil {
				t.Errorf("another worker could not claim the row after its lease: %v", err)
			}
			second = c
		}
		e.step()
		e.q.claimed = nil
		if n := len(e.hits(id)); n != 0 {
			t.Fatalf("the worker that lost its lease sent the row (%d requests)", n)
		}
		if second == nil {
			t.FailNow()
		}
		if err := e.q.deliver(context.Background(), second); err != nil {
			t.Fatal(err)
		}
		if n := len(e.hits(id)); n != 1 || e.count(`SELECT count(*) FROM ap_deliveries`) != 0 {
			t.Errorf("the row was sent %d times", n)
		}
		// A lease that runs out during the request itself: another worker claims the row meanwhile. The first, when
		// its answer comes, writes nothing about a row that is no longer its own; the second sends it again, which
		// the receiver's de-duplication by id makes harmless (8.6).
		id, _ = e.send("social.example/inbox")
		release = e.s.holdAt("social.example")
		done = make(chan bool)
		go func() { worked, _ := e.q.step(context.Background()); done <- worked }()
		<-e.s.arrived
		e.clock.add(deliveryLease + time.Second)
		third, err := e.q.claim(context.Background(), nil)
		if err != nil || third == nil {
			t.Fatalf("the row could not be claimed after its lease ran out: %v", err)
		}
		release()
		<-done
		if e.count(`SELECT count(*) FROM ap_deliveries WHERE locked_until = $1`, third.lease) != 1 {
			t.Error("the worker whose lease ran out during its request changed the row another worker holds")
		}
		if err := e.q.deliver(context.Background(), third); err != nil || e.count(`SELECT count(*) FROM ap_deliveries`) != 0 || len(e.hits(id)) != 2 {
			t.Errorf("the second worker's send: %v, %d requests in all", err, len(e.hits(id)))
		}
	})

	t.Run("signature forms", func(t *testing.T) {
		e := newDelivEnv(t)
		// b.example takes only RFC 9421 and answers 401 to the draft; other.example answers 400.
		e.answer(func(host, _, form string) (int, string) {
			switch {
			case host == "b.example" && form == "cavage":
				return 401, ""
			case host == "other.example" && form == "cavage":
				return 400, ""
			case host == "remote.example":
				return 400, ""
			case host == "social.example":
				return 401, ""
			}
			return 202, ""
		})
		for _, h := range []string{"b.example", "other.example"} {
			id, _ := e.send(h + "/inbox")
			if !e.step() {
				t.Fatal("nothing claimed")
			}
			if got := e.hits(id); !slices.Equal(got, []string{"cavage", "rfc9421"}) || e.count(`SELECT count(*) FROM ap_deliveries`) != 0 {
				t.Errorf("%s: requests %v, %d rows left", h, got, e.count(`SELECT count(*) FROM ap_deliveries`))
			}
			if e.count(`SELECT count(*) FROM ap_hosts WHERE host = $1 AND sig = 'rfc9421'`, h) != 1 {
				t.Errorf("%s: the form that worked is not recorded", h)
			}
			// From then on that form comes first, and only it.
			id, _ = e.send(h + "/inbox")
			e.step()
			if got := e.hits(id); !slices.Equal(got, []string{"rfc9421"}) {
				t.Errorf("%s afterwards: %v", h, got)
			}
		}
		// A 400 in both forms is given up; the form recorded stays the draft.
		id, _ := e.send("remote.example/inbox")
		e.step()
		if st, at, _, why := e.row(id, "remote.example/inbox"); st != "failed" || at != 1 || !strings.Contains(why, "400 in both signature forms") || !slices.Equal(e.hits(id), []string{"cavage", "rfc9421"}) {
			t.Errorf("a 400 in both forms: %s %d %q %v", st, at, why, e.hits(id))
		}
		if e.count(`SELECT count(*) FROM ap_hosts WHERE host = 'remote.example' AND sig = 'cavage'`) != 1 {
			t.Error("a host that refused both forms has another form recorded")
		}
		// A 401 in both is tried again later, and the host's circuit is not touched: it answered.
		id, _ = e.send("social.example/inbox")
		e.step()
		if st, at, next, why := e.row(id, "social.example/inbox"); st != "queued" || at != 1 || !next.Equal(e.clock.now().Add(time.Minute)) || !strings.Contains(why, "401 in both") {
			t.Errorf("a 401 in both forms: %s %d %v %q", st, at, next, why)
		}
		if e.count(`SELECT count(*) FROM ap_hosts WHERE host = 'social.example' AND failures = 0`) != 1 {
			t.Error("a 401 counted towards the host's circuit")
		}
	})

	t.Run("410 and other statuses", func(t *testing.T) {
		e := newDelivEnv(t)
		var gone []string
		e.q.goneInbox = func(_ context.Context, inbox string) { gone = append(gone, inbox) }
		e.answer(func(host, path, _ string) (int, string) {
			switch {
			case host == "moved.example" && path == "/users/bo/inbox":
				return 410, ""
			case host == "moved.example":
				return 503, ""
			case host == "a.example":
				return 404, ""
			case host == "b.example":
				return 403, ""
			case host == "social.example":
				return 429, "120"
			case host == "other.example":
				return 429, "10"
			}
			return 202, ""
		})
		first, _ := e.send("moved.example/users/bo/inbox")
		second, _ := e.send("moved.example/users/bo/inbox", "moved.example/users/cy/inbox")
		e.step()
		if !slices.Equal(gone, []string{"https://moved.example/users/bo/inbox"}) {
			t.Errorf("410: the hook heard %v", gone)
		}
		for _, id := range []string{first, second} {
			if st, _, _, why := e.row(id, "moved.example/users/bo/inbox"); st != "failed" || !strings.Contains(why, "410") {
				t.Errorf("410: the row of %s for that inbox is %s %q", id, st, why)
			}
		}
		if st, _, _, _ := e.row(second, "moved.example/users/cy/inbox"); st != "queued" {
			t.Errorf("410: another inbox of the same host: %s", st)
		}
		e.step() // that other inbox answers 503: tried again in a minute, out of the way of what follows
		for _, c := range []struct{ inbox, want string }{{"a.example/inbox", "failed"}, {"b.example/inbox", "failed"}} {
			id, _ := e.send(c.inbox)
			e.step()
			if st, _, _, _ := e.row(id, c.inbox); st != c.want {
				t.Errorf("%s: %s, want %s", c.inbox, st, c.want)
			}
		}
		// A 429 waits its Retry-After when that is between a minute and 12 hours, else the schedule.
		for host, wait := range map[string]time.Duration{"social.example": 2 * time.Minute, "other.example": time.Minute} {
			id, _ := e.send(host + "/inbox")
			e.step()
			if st, _, next, _ := e.row(id, host+"/inbox"); st != "queued" || !next.Equal(e.clock.now().Add(wait)) {
				t.Errorf("429 (%s): %s, due %v, now %v", host, st, next, e.clock.now())
			}
		}
	})

	t.Run("circuit", func(t *testing.T) {
		e := newDelivEnv(t)
		code := atomic.Int32{}
		code.Store(503)
		e.answer(func(string, string, string) (int, string) { return int(code.Load()), "" })
		for range hostFailuresOpen + 1 {
			e.send("a.example/inbox")
		}
		for range hostFailuresOpen {
			if !e.step() {
				t.Fatal("nothing claimed")
			}
		}
		var state string
		var until time.Time
		if err := e.a.db.QueryRow(`SELECT state, until FROM ap_hosts WHERE host = 'a.example'`).Scan(&state, &until); err != nil || state != "open" || !until.Equal(e.clock.now().Add(hostOpenFor)) {
			t.Fatalf("after %d failures: %s until %v (now %v) %v", hostFailuresOpen, state, until, e.clock.now(), err)
		}
		// The host's rows are moved to the circuit's end, so the claim does not pass over them while it is open.
		e.send("a.example/inbox") // written while the circuit is open: due when it ends
		if n := e.count(`SELECT count(*) FROM ap_deliveries WHERE host = 'a.example' AND state = 'queued' AND next_at < $1`, until); n != 0 {
			t.Errorf("%d rows of the open host are due before its circuit closes", n)
		}
		e.clock.add(2 * time.Minute) // the circuit is open
		if e.step() {
			t.Error("a row was claimed while its host's circuit is open")
		}
		e.clock.add(hostOpenFor)
		if !e.step() {
			t.Fatal("no row was tried when the circuit closed")
		}
		if err := e.a.db.QueryRow(`SELECT state, until FROM ap_hosts WHERE host = 'a.example'`).Scan(&state, &until); err != nil || state != "open" || !until.Equal(e.clock.now().Add(hostOpenFor)) {
			t.Errorf("the one row after the circuit closed failed: %s until %v", state, until)
		}
		code.Store(202)
		e.clock.add(hostOpenFor + time.Second)
		e.drain(20)
		if err := e.a.db.QueryRow(`SELECT state FROM ap_hosts WHERE host = 'a.example'`).Scan(&state); err != nil || state != "ok" || e.count(`SELECT count(*) FROM ap_deliveries`) != 0 {
			t.Errorf("after a success: %s, %d rows", state, e.count(`SELECT count(*) FROM ap_deliveries`))
		}
	})

	t.Run("half-open", func(t *testing.T) {
		// A circuit whose time is up is half-open: the first answer decides. A 401 (or 429, a redirect, a refusal) is
		// an answer, so it closes the circuit, and the next 5xx counts one failure, not a reopening.
		e := newDelivEnv(t)
		code := atomic.Int32{}
		code.Store(401)
		e.answer(func(string, string, string) (int, string) { return int(code.Load()), "" })
		e.send("a.example/inbox")
		e.send("a.example/inbox")
		e.mustExec(`INSERT INTO ap_hosts (host, created_at, state, until) VALUES ('a.example', $1, 'open', $1) ON CONFLICT (host) DO UPDATE SET state = 'open', until = $1`, e.clock.now())
		e.step()
		var state string
		var failures int
		if e.a.db.QueryRow(`SELECT state, failures FROM ap_hosts WHERE host = 'a.example'`).Scan(&state, &failures); state != "ok" {
			t.Fatalf("after a 401 on a half-open circuit: %s", state)
		}
		code.Store(503)
		e.step()
		if e.a.db.QueryRow(`SELECT state, failures FROM ap_hosts WHERE host = 'a.example'`).Scan(&state, &failures); state != "ok" || failures != 1 {
			t.Errorf("a 503 after the circuit closed: %s with %d failures, want one failure counted", state, failures)
		}
	})

	t.Run("a redirect", func(t *testing.T) {
		// A redirect is the inbox address's fault, not its host's: tried again later, and the circuit untouched, so
		// rows to /redirects never keep the host's other rows from being sent.
		e := newDelivEnv(t)
		e.answer(func(_, p, _ string) (int, string) {
			if p == "/redirects" {
				return 302, ""
			}
			return 202, ""
		})
		var bad []string
		for range hostFailuresOpen + 1 {
			id, _ := e.send("a.example/redirects")
			bad = append(bad, id)
			e.step()
		}
		if e.count(`SELECT count(*) FROM ap_hosts WHERE host = 'a.example' AND state = 'ok' AND failures = 0`) != 1 {
			t.Error("redirects counted against the host's circuit")
		}
		if st, at, next, why := e.row(bad[0], "a.example/redirects"); st != "queued" || at != 1 || !next.After(e.clock.now()) || !strings.Contains(why, "302") {
			t.Errorf("a redirected row: %s %d %v %q", st, at, next, why)
		}
		id, _ := e.send("a.example/inbox")
		if !e.step() || len(e.hits(id)) != 1 {
			t.Error("a row to the host's real inbox was not sent")
		}
	})

	t.Run("unavailable", func(t *testing.T) {
		e := newDelivEnv(t)
		code := atomic.Int32{}
		code.Store(202)
		e.answer(func(_, path, _ string) (int, string) {
			if path == "/.well-known/nodeinfo" {
				return 200, ""
			}
			return int(code.Load()), ""
		})
		// A host that took our last delivery 8 days ago and answers one 503 now: one failure, still available.
		e.send("a.example/inbox")
		e.step()
		e.clock.add(8 * 24 * time.Hour)
		code.Store(503)
		blip, _ := e.send("a.example/inbox")
		e.step()
		if st, _, _, _ := e.row(blip, "a.example/inbox"); st != "queued" || e.count(`SELECT count(*) FROM ap_hosts WHERE host = 'a.example' AND state = 'ok' AND failures = 1`) != 1 {
			t.Fatalf("one 503 after 8 quiet days: the row is %s and the host not ok with one failure", st)
		}
		// Failures with a gap longer than the horizon between them are not one streak: the week starts again.
		e.mustExec(`UPDATE ap_hosts SET down_since = $1, last_failure = $2 WHERE host = 'a.example'`, e.clock.now().Add(-8*24*time.Hour), e.clock.now().Add(-hostStreakGap-time.Hour))
		e.clock.add(time.Minute)
		e.step()
		if e.count(`SELECT count(*) FROM ap_hosts WHERE host = 'a.example' AND state = 'ok' AND down_since = $1`, e.clock.now()) != 1 {
			t.Fatal("a failure after a long silence did not start a new streak")
		}
		// Failures spanning 7 days, none further apart than the horizon, with no sign of life between: unavailable.
		e.mustExec(`DELETE FROM ap_deliveries`)
		a, _ := e.send("a.example/inbox")
		b, _ := e.send("a.example/users/x/inbox")
		e.mustExec(`UPDATE ap_hosts SET down_since = $1, last_failure = $2 WHERE host = 'a.example'`, e.clock.now().Add(-hostUnavailableAfter), e.clock.now().Add(-time.Hour))
		e.step()
		for _, c := range [][2]string{{a, "a.example/inbox"}, {b, "a.example/users/x/inbox"}} {
			if st, _, _, _ := e.row(c[0], c[1]); st != "failed" {
				t.Errorf("a row of a host with no sign of life in 7 days: %s", st)
			}
		}
		if e.count(`SELECT count(*) FROM ap_hosts WHERE host = 'a.example' AND state = 'unavailable'`) != 1 {
			t.Fatal("the host is not marked unavailable")
		}
		if _, out := e.send("a.example/inbox", "b.example/inbox"); out.Queued != 1 || out.Unavailable != 1 {
			t.Errorf("an activity for an unavailable host: %+v", out)
		}
		// A valid delivery from it marks it available (8.4), and so does an answer to the daily probe.
		e.q.heardFrom(context.Background(), "A.example", sigCavage)
		if e.count(`SELECT count(*) FROM ap_hosts WHERE host = 'a.example' AND state = 'ok' AND down_since IS NULL`) != 1 {
			t.Error("a delivery from the host did not mark it available")
		}
		e.mustExec(`UPDATE ap_hosts SET state = 'unavailable' WHERE host = 'a.example'`)
		n, err := e.a.housekeep()
		if err != nil {
			t.Fatal(err)
		}
		if e.count(`SELECT count(*) FROM ap_hosts WHERE host = 'a.example' AND state = 'ok' AND probed_at IS NOT NULL`) != 1 {
			t.Errorf("the probe did not mark the host available (%v)", n)
		}
		probes := 0
		for _, r := range e.s.requests() {
			if r.path == "/.well-known/nodeinfo" {
				probes++
			}
		}
		e.mustExec(`UPDATE ap_hosts SET state = 'unavailable' WHERE host = 'a.example'`)
		e.a.housekeep()
		for _, r := range e.s.requests() {
			if r.path == "/.well-known/nodeinfo" {
				probes--
			}
		}
		if probes != 0 {
			t.Error("a host was probed again within a day")
		}
		// A delivery signed with RFC 9421 records that form for its host.
		e.q.heardFrom(context.Background(), "b.example", sigRFC9421)
		if e.count(`SELECT count(*) FROM ap_hosts WHERE host = 'b.example' AND sig = 'rfc9421'`) != 1 {
			t.Error("a host that signs with RFC 9421 is not marked")
		}
	})

	t.Run("signs of life", func(t *testing.T) {
		// A verified delivery from a host, in either form, is a sign of life: its streak towards "unavailable" ends.
		// It is written at most once an hour per host, so a busy sender costs no write per delivery.
		e := newDelivEnv(t)
		e.mustExec(`INSERT INTO ap_hosts (host, created_at, alive_at, down_since, last_failure) VALUES ('a.example', $1, $1, $1, $2)`, e.clock.now().Add(-6*24*time.Hour), e.clock.now().Add(-time.Hour))
		e.q.heardFrom(context.Background(), "a.example", sigCavage)
		var alive time.Time
		if err := e.a.db.QueryRow(`SELECT alive_at FROM ap_hosts WHERE host = 'a.example' AND down_since IS NULL`).Scan(&alive); err != nil || !alive.Equal(e.clock.now()) {
			t.Fatalf("a verified draft-signed delivery did not refresh the host's sign of life: %v %v", alive, err)
		}
		e.mustExec(`UPDATE ap_hosts SET alive_at = $1 WHERE host = 'a.example'`, e.clock.now().Add(-time.Minute))
		e.clock.add(hostAliveEvery - time.Second)
		e.q.heardFrom(context.Background(), "a.example", sigCavage)
		if e.count(`SELECT count(*) FROM ap_hosts WHERE host = 'a.example' AND alive_at < $1`, e.clock.now().Add(-time.Hour)) != 1 {
			t.Error("a second delivery within the hour wrote again")
		}
		e.clock.add(2 * time.Second)
		e.q.heardFrom(context.Background(), "a.example", sigCavage)
		if e.count(`SELECT count(*) FROM ap_hosts WHERE host = 'a.example' AND alive_at = $1`, e.clock.now()) != 1 {
			t.Error("a delivery after the hour did not write the sign of life")
		}
		// What a send learned wins over what an inbound signature suggests: a host that signs with RFC 9421 but took
		// only the draft keeps the draft.
		e.answer(func(_, _, form string) (int, string) {
			if form == "rfc9421" {
				return 401, ""
			}
			return 202, ""
		})
		e.mustExec(`INSERT INTO ap_hosts (host, created_at, sig) VALUES ('b.example', $1, 'rfc9421')`, e.clock.now())
		id, _ := e.send("b.example/inbox")
		e.step()
		if got := e.hits(id); !slices.Equal(got, []string{"rfc9421", "cavage"}) {
			t.Fatalf("requests %v", got)
		}
		e.clock.add(2 * hostAliveEvery)
		e.q.heardFrom(context.Background(), "b.example", sigRFC9421)
		if e.count(`SELECT count(*) FROM ap_hosts WHERE host = 'b.example' AND sig = 'cavage' AND sig_learned`) != 1 {
			t.Error("an inbound RFC 9421 signature overwrote the form a send learned")
		}
	})
}

// classify reads every status of 8.3 as it says, and this server's own failures as its own.
func TestDeliveryClassify(t *testing.T) {
	st := func(code int, ra time.Duration) error {
		return &fetchError{Fail: failStatus, Status: code, RetryAfter: ra, Reason: fmt.Sprintf("answered %d", code)}
	}
	for name, c := range map[string]struct {
		err   error
		out   deliveryOutcome
		after time.Duration
	}{
		"taken":             {nil, outSent, 0},
		"410":               {&fetchError{Fail: failGone}, outGone, 0},
		"404":               {&fetchError{Fail: failNotFound}, outGiveUp, 0},
		"400":               {st(400, 0), outGiveUp, 0},
		"401":               {st(401, 0), outRefused, 0},
		"403":               {st(403, 0), outGiveUp, 0},
		"408":               {st(408, 0), outRetry, 0},
		"429 with 2 min":    {st(429, 2*time.Minute), outRefused, 2 * time.Minute},
		"429 with 30 s":     {st(429, 30*time.Second), outRefused, 0},
		"500":               {st(500, 0), outRetry, 0},
		"302, no Location":  {st(302, 0), outRefused, 0},
		"a redirect":        {&fetchError{Fail: failRedirect}, outRefused, 0},
		"no connection":     {&fetchError{Fail: failConnect}, outRetry, 0},
		"timed out":         {&fetchError{Fail: failTimeout}, outRetry, 0},
		"our own turn":      {&fetchError{Fail: failBusy}, outOwn, 0},
		"a private host":    {&fetchError{Fail: failRefused}, outGiveUp, 0},
		"the clock":         {&fetchError{Fail: failRefused, err: errClockUntrusted}, outOwn, 0},
		"not a fetch error": {errors.New("x"), outOwn, 0},
	} {
		out, after, _ := classify(c.err)
		if out != c.out || after != c.after {
			t.Errorf("%s: %v %v, want %v %v", name, out, after, c.out, c.after)
		}
	}
	if !otherForm(st(400, 0)) || !otherForm(st(401, 0)) || otherForm(st(403, 0)) || otherForm(nil) {
		t.Error("the other form is tried on a 400 and a 401 only")
	}
	for v, want := range map[string]int{"": deliveryWorkersDefault, "1": 1, " 8 ": 8} {
		if n, err := parseWorkers(v); err != nil || n != want {
			t.Errorf("FLOWA_FEDERATION_WORKERS=%q: %d %v", v, n, err)
		}
	}
	for _, v := range []string{"0", "9", "four", "-1", "2.5"} {
		if _, err := parseWorkers(v); err == nil || !strings.Contains(err.Error(), "FLOWA_FEDERATION_WORKERS") {
			t.Errorf("FLOWA_FEDERATION_WORKERS=%q: %v", v, err)
		}
	}
	// Read at the start in a mode that federates, as FLOWA_ORIGIN is, and nowhere else.
	if s, err := parseFederation(modeFederated, fedEnv(nil)); err != nil || s.workers != deliveryWorkersDefault {
		t.Errorf("federated, unset: %d %v", s.workers, err)
	}
	if _, err := parseFederation(modeFederated, fedEnv(map[string]string{"FLOWA_FEDERATION_WORKERS": "9"})); err == nil || !strings.Contains(err.Error(), "FLOWA_FEDERATION_WORKERS") {
		t.Errorf("federated with 9 workers: %v", err)
	}
	if _, err := parseFederation(modeSingle, fedEnv(map[string]string{"FLOWA_FEDERATE": "off", "FLOWA_FEDERATION_WORKERS": "9"})); err != nil {
		t.Errorf("single mode that does not federate read the setting: %v", err)
	}
}

// One request at a time per host, proven with requests the stub holds; and at most deliveryPerSecond requests a
// second in all, proven with the pacer's places on the test's clock (8.2, section 12).
func TestDBDeliveryPacing(t *testing.T) {
	e := newDelivEnv(t)
	first, _ := e.send("a.example/inbox")
	second, _ := e.send("a.example/inbox")
	third, _ := e.send("b.example/inbox")
	ra, rb := e.s.holdAt("a.example"), e.s.holdAt("b.example")
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; ; n++ {
				if n > 200 { // a regression that keeps a sent row must fail, not hang
					t.Error("still claiming rows after 200 steps")
					return
				}
				if worked, err := e.q.step(context.Background()); err != nil || !worked {
					return
				}
			}
		}()
	}
	got := []string{<-e.s.arrived, <-e.s.arrived}
	slices.Sort(got)
	if !slices.Equal(got, []string{"a.example", "b.example"}) {
		t.Fatalf("the two workers sent to %v", got)
	}
	// Both hosts are being sent to, and the one row left due is a.example's: a third worker takes nothing.
	if e.step() {
		t.Error("a second request to a host was claimed while one was in flight")
	}
	ra()
	rb()
	wg.Wait()
	if len(e.hits(first)) != 1 || len(e.hits(second)) != 1 || len(e.hits(third)) != 1 || e.count(`SELECT count(*) FROM ap_deliveries`) != 0 {
		t.Error("each row was not sent once")
	}
	for h, n := range e.s.most {
		if n != 1 {
			t.Errorf("%d requests at once to %s", n, h)
		}
	}

	// 30 rows on 30 hosts, four workers: every request has its place, the places are a tenth of a second apart, and
	// the clock moved by the waits alone.
	e2 := newDelivEnv(t)
	var mu sync.Mutex
	var places []time.Time
	e2.q.pace.took = func(at time.Time) { mu.Lock(); places = append(places, at); mu.Unlock() }
	var in []string
	for i := range 30 {
		in = append(in, fmt.Sprintf("h%d.fan.example/inbox", i))
	}
	_, out := e2.send(in...)
	if out.Queued != 30 {
		t.Fatalf("enqueued %+v", out)
	}
	start := e2.clock.now()
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; ; n++ {
				if n > 200 { // a regression that keeps a sent row must fail, not hang
					t.Error("still claiming rows after 200 steps")
					return
				}
				worked, err := e2.q.step(context.Background())
				if err != nil || !worked {
					return
				}
			}
		}()
	}
	wg.Wait()
	if n := len(e2.s.requests()); n != 30 || len(places) != 30 {
		t.Fatalf("%d requests, %d places", n, len(places))
	}
	slices.SortFunc(places, func(a, b time.Time) int { return a.Compare(b) })
	gap := time.Second / deliveryPerSecond
	for i := 1; i < len(places); i++ {
		if places[i].Sub(places[i-1]) < gap {
			t.Errorf("places %d and %d are %v apart, less than %v", i-1, i, places[i].Sub(places[i-1]), gap)
		}
	}
	for i := deliveryPerSecond; i < len(places); i++ {
		if places[i].Sub(places[i-deliveryPerSecond]) < time.Second {
			t.Errorf("%d requests within a second (places %d to %d)", deliveryPerSecond+1, i-deliveryPerSecond, i)
		}
	}
	if moved := e2.clock.now().Sub(start); moved < 29*gap {
		t.Errorf("the clock moved %v; the waits for 30 places are at least %v", moved, 29*gap)
	}
	// A clock that stepped back makes nobody wait a second or more.
	p := pacer{gap: gap, now: e2.clock.now, wait: func(context.Context, time.Duration) error { t.Error("waited after the clock stepped back"); return nil }}
	p.next = e2.clock.now().Add(time.Hour)
	p.take(context.Background())
}

// The domain policy is asked at the send, not only when the row was written; the pause keeps the rows; refused
// federation sends nothing (section 10, F1, 8.2).
func TestDBDeliveryPolicyAndPause(t *testing.T) {
	e := newDelivEnv(t)
	id, out := e.send("social.example/inbox")
	if out.Queued != 1 {
		t.Fatalf("%+v", out)
	}
	e.mustExec(`INSERT INTO domain_policies (domain, policy) VALUES ('social.example', 'suspend')`)
	if err := e.a.fed.loadPolicy(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.step()
	if st, _, _, why := e.row(id, "social.example/inbox"); st != "failed" || why != "the domain policy refuses the host" || len(e.hits(id)) != 0 {
		t.Errorf("a host suspended after the row was written: %s %q, %d requests", st, why, len(e.hits(id)))
	}
	if _, out := e.send("social.example/inbox", "sub.social.example/inbox"); out.Refused != 2 || out.Queued != 0 {
		t.Errorf("an activity for a suspended host: %+v", out)
	}
	// Allowlist mode: a host not listed gets nothing, though its row was written while the instance was open.
	id, _ = e.send("a.example/inbox")
	e.mustExec(`UPDATE instance_settings SET value = '"allowlist"' WHERE key = 'federation_policy'`)
	e.a.fed.loadPolicy(context.Background())
	e.step()
	if st, _, _, why := e.row(id, "a.example/inbox"); st != "failed" || why != "the domain policy refuses the host" || len(e.hits(id)) != 0 {
		t.Errorf("a host not on the allowlist: %s %q", st, why)
	}
	e.mustExec(`UPDATE instance_settings SET value = '"open"' WHERE key = 'federation_policy'`)
	e.a.fed.loadPolicy(context.Background())

	// The Federation switch off: nothing is claimed and the row is as it was; on again, it is sent.
	id, _ = e.send("b.example/inbox")
	e.setFlag(fedFlag, false)
	if e.step() {
		t.Error("a row was claimed while the Federation switch is off")
	}
	if st, at, _, _ := e.row(id, "b.example/inbox"); st != "queued" || at != 0 || e.count(`SELECT count(*) FROM ap_deliveries WHERE locked_until IS NOT NULL`) != 0 {
		t.Errorf("paused: %s, %d attempts", st, at)
	}
	e.setFlag(fedFlag, true)
	if !e.step() || len(e.hits(id)) != 1 {
		t.Error("the row was not sent once the switch was on")
	}
	// Federation refused (a moved origin): nothing is claimed, and nothing is enqueued.
	id, _ = e.send("b.example/inbox")
	e.a.fed.refuse("federation refused: a test")
	if e.step() || len(e.hits(id)) != 0 {
		t.Error("a row was sent while federation is refused")
	}
	if _, err := e.q.enqueue(context.Background(), instanceActor, "https://here.example/activities/x", []byte(`{}`), []deliveryTarget{{Inbox: "https://b.example/inbox"}}); !errors.Is(err, errRefused) {
		t.Errorf("enqueue while refused: %v", err)
	}
}

// No connection and no turn at the database is held while a worker waits on a slow host (8.2, section 12). The
// start's key work is settled first (remoteOn's settleKeys), so the count is the queue's own; nothing polls the
// database while it is counted.
func TestDBDeliveryNoConnectionHeld(t *testing.T) {
	e := newDelivEnv(t)
	id, _ := e.send("a.example/inbox")
	release := e.s.holdAt("a.example")
	done := make(chan bool)
	go func() { done <- e.step() }()
	<-e.s.arrived
	if n := e.a.db.Stats().InUse; n != 0 {
		t.Errorf("%d connections in use while the request is in flight", n)
	}
	if n := len(e.a.fed.conns); n != 0 {
		t.Errorf("%d of federation's turns held while the request is in flight", n)
	}
	release()
	if !<-done || len(e.hits(id)) != 1 {
		t.Error("the row was not sent")
	}
	// On a pool of one connection a claim, a send and its record all finish.
	e.a.db.SetMaxOpenConns(1)
	defer e.a.db.SetMaxOpenConns(8)
	id, _ = e.send("b.example/inbox")
	if !e.step() || len(e.hits(id)) != 1 {
		t.Error("on one connection the row was not sent")
	}
}

// TestClockGuard: nothing is claimed or signed while the clock is earlier than the build, or than the newest time
// the database holds; the dashboard and /healthz say so; once the clock is set the row goes, signed with a date
// the receiver takes (8.3).
func TestClockGuard(t *testing.T) {
	e := newDelivEnv(t)
	id, _ := e.send("a.example/inbox")
	e.q.built = e.clock.now().Add(time.Hour)
	if e.step() {
		t.Error("a row was claimed with the clock before the build")
	}
	if st, at, _, _ := e.row(id, "a.example/inbox"); st != "queued" || at != 0 || len(e.hits(id)) != 0 {
		t.Errorf("before the build: %s, %d attempts, %d requests", st, at, len(e.hits(id)))
	}
	_, admin := e.user("boss", "admin")
	if d := e.must(200, admin, "GET", "/api/admin/dashboard", nil)["federation"].(map[string]any)["deliveries"].(map[string]any); d["clockWaiting"] != true {
		t.Errorf("the dashboard does not say it waits for the clock: %v", d)
	}
	if code, out := probe(t, e.h, "127.0.0.1:4000", nil); code != 200 || out["federation"].(map[string]any)["clockWaiting"] != true {
		t.Errorf("/healthz: %d %v", code, out["federation"])
	}
	// The signer refuses on its own as well.
	key, keyID, _ := e.a.fed.privateKey(context.Background(), instanceActor)
	req, _ := http.NewRequest("POST", "https://a.example/inbox", bytes.NewReader([]byte("{}")))
	if err := httpSigner(key, e.a.fed.origin.url+keyID, sigCavage, e.clock.now, e.q.built)(req, []byte("{}")); !errors.Is(err, errClockUntrusted) {
		t.Errorf("the signer before the build: %v", err)
	}

	// The newest time the database holds, read at the start, is the other bound.
	e.q.built = time.Time{}
	e.mustExec(`UPDATE instance_settings SET updated_at = $1 WHERE key = 'federation_policy'`, e.clock.now().Add(2*time.Hour))
	if err := e.q.readStart(e.a.db); err != nil {
		t.Fatal(err)
	}
	if e.step() {
		t.Error("a row was claimed with the clock before the newest time the database holds")
	}
	e.clock.add(2*time.Hour + time.Second)
	if !e.step() {
		t.Fatal("the row was not claimed once the clock was set")
	}
	if got := e.hits(id); len(got) != 1 {
		t.Errorf("after the clock was set: %v", got)
	}
	// A floor that was written while some clock was ahead: believed for the first hour after the start, then dropped,
	// said in the log, and the deliveries go on.
	logs := logged(t)
	id, _ = e.send("a.example/inbox")
	e.mustExec(`UPDATE instance_settings SET updated_at = $1 WHERE key = 'federation_policy'`, e.clock.now().Add(30*24*time.Hour))
	if err := e.q.readStart(e.a.db); err != nil {
		t.Fatal(err)
	}
	up := floorTrust - time.Minute
	e.q.since = func() time.Duration { return up }
	if e.step() {
		t.Error("a row was claimed before the clock's floor, within the first hour")
	}
	up = floorTrust + time.Minute
	if !e.step() || len(e.hits(id)) != 1 {
		t.Error("a floor still ahead an hour after the start held the deliveries")
	}
	if !strings.Contains(logs.String(), "is not believed") {
		t.Error("the dropped floor was not logged")
	}
}

// TestDBDeliveryBackPressure: shared inboxes and the de-duplication of 8.1; 2,000 rows per host and 50,000 in all,
// then the activity waits in ap_outbox and is fanned out once the queue is under 40,000; enqueue refuses when too
// many wait (8.5, section 12).
func TestDBDeliveryBackPressure(t *testing.T) {
	e := newDelivEnv(t)
	// Three followers on social.example with its shared inbox are one row; one with no shared inbox gets its own;
	// the same inbox twice is one; this instance is never a recipient; a bad address is refused.
	id, body := e.activityBody()
	// An inbox must be on its actor's host: one naming another server is refused, and a shared inbox on another host
	// is passed over for the actor's own inbox.
	out, err := e.q.enqueue(context.Background(), instanceActor, id, body, []deliveryTarget{
		{Actor: "https://social.example/users/a", Inbox: "https://social.example/users/a/inbox", SharedInbox: "https://social.example/inbox"},
		{Actor: "https://social.example/users/b", Inbox: "https://social.example/users/b/inbox", SharedInbox: "https://social.example/inbox"},
		{Actor: "https://social.example/users/c", Inbox: "https://social.example/users/c/inbox", SharedInbox: "https://social.example/inbox"},
		{Actor: "https://social.example/users/d", Inbox: "https://social.example/users/d/inbox"},
		{Actor: "https://b.example/users/b", Inbox: "https://b.example/inbox"}, {Actor: "https://b.example/users/b", Inbox: "https://b.example/inbox"},
		{Actor: "https://other.example/users/o", Inbox: "https://other.example/users/o/inbox", SharedInbox: "https://b.example/inbox"},
		{Actor: "https://here.example/users/ana", Inbox: "https://here.example/users/ana/inbox"}, {Actor: "https://c.example/u", Inbox: "http://c.example/inbox"},
		{Actor: "https://10.0.0.1/u", Inbox: "https://10.0.0.1/inbox"}, {Actor: "https://c.example/u", Inbox: "https://c.example:8443/inbox"},
		{Actor: "https://evil.example/users/x", Inbox: "https://a.example/inbox"}, {Inbox: "https://a.example/inbox"},
	})
	if err != nil || out.Queued != 4 || out.Refused != 6 {
		t.Errorf("recipients: %+v %v", out, err)
	}
	if e.count(`SELECT count(*) FROM ap_deliveries WHERE inbox = 'https://other.example/users/o/inbox'`) != 1 || e.count(`SELECT count(*) FROM ap_deliveries WHERE host = 'a.example'`) != 0 {
		t.Error("the actor's own inbox was not used for a shared inbox on another host, or an inbox off its actor's host was written")
	}
	if again, err := e.q.enqueue(context.Background(), instanceActor, id, body, []deliveryTarget{{Actor: "https://a.example/u", Inbox: "https://a.example/inbox"}}); err != nil || !again.Duplicate || e.count(`SELECT count(*) FROM ap_deliveries`) != 4 {
		t.Errorf("the same activity again: %+v %v", again, err)
	}
	for _, bad := range []struct {
		id   string
		body []byte
	}{{"https://elsewhere.example/a/1", body}, {"https://here.example/a/2", []byte("not json")}, {"https://here.example/a/3", bytes.Repeat([]byte(" "), inboxBodyMax+1)}} {
		if _, err := e.q.enqueue(context.Background(), instanceActor, bad.id, bad.body, []deliveryTarget{{Actor: "https://a.example/u", Inbox: "https://a.example/inbox"}}); err == nil {
			t.Errorf("enqueue took %s", bad.id)
		}
	}
	many := make([]deliveryTarget, deliveryTargetsMax+1)
	for i := range many {
		many[i] = deliveryTarget{Actor: fmt.Sprintf("https://h%d.fan.example/u", i), Inbox: fmt.Sprintf("https://h%d.fan.example/inbox", i)}
	}
	if _, err := e.q.enqueue(context.Background(), instanceActor, "https://here.example/a/4", body, many); err == nil {
		t.Error("enqueue took more inboxes than one activity may have")
	}
	e.mustExec(`DELETE FROM ap_outbox`)

	// A host with 2,000 rows queued gets no more; the rest are counted on its row.
	e.mustExec(`INSERT INTO ap_outbox (ap_id, body, fanned_out, created_at) VALUES ('https://here.example/a/fill', '{}', true, now())`)
	e.mustExec(`INSERT INTO ap_deliveries (outbox_id, inbox, host, next_at, created_at) SELECT (SELECT id FROM ap_outbox), 'https://a.example/u/' || g, 'a.example', now() + interval '1 day', now() FROM generate_series(1, $1) g`, deliveryHostMax)
	if _, out := e.send("a.example/inbox", "b.example/inbox"); out.Queued != 1 || out.Dropped != 1 {
		t.Errorf("past a host's cap: %+v", out)
	}
	if e.count(`SELECT count(*) FROM ap_hosts WHERE host = 'a.example' AND dropped = 1`) != 1 {
		t.Error("the row past the cap is not counted on the host")
	}
	// Within one activity the cap holds too.
	e.mustExec(`DELETE FROM ap_deliveries WHERE host = 'a.example' AND inbox LIKE 'https://a.example/u/%' AND split_part(inbox, '/', 5)::int <= 3`)
	if _, out := e.send("a.example/x/1", "a.example/x/2", "a.example/x/3", "a.example/x/4", "a.example/x/5"); out.Queued != 3 || out.Dropped != 2 {
		t.Errorf("one activity past a host's cap: %+v", out)
	}

	// 50,000 rows in all: a new activity waits, one row in ap_outbox, and so does the next, behind it.
	e.mustExec(`INSERT INTO ap_deliveries (outbox_id, inbox, host, next_at, created_at) SELECT (SELECT id FROM ap_outbox WHERE ap_id = 'https://here.example/a/fill'), 'https://h' || (g % 100) || '.fan.example/u/' || g, 'h' || (g % 100) || '.fan.example', now() + interval '1 day', now()
		FROM generate_series(1, $1 - (SELECT count(*) FROM ap_deliveries WHERE state = 'queued')) g`, deliveryQueueMax)
	e.mustExec(`UPDATE ap_deliveries SET next_at = $1::timestamptz + interval '1 day'`, e.clock.now()) // only what is fanned out below is due
	waitA, out := e.send("other.example/inbox")
	if !out.Waiting || out.Queued != 0 {
		t.Errorf("with the queue full: %+v", out)
	}
	waitB, out := e.send("social.example/inbox")
	if !out.Waiting {
		t.Errorf("behind a waiting activity: %+v", out)
	}
	// A third waits for moved.example, which is suspended before its turn comes: its fan-out writes no row there.
	waitC, out := e.send("moved.example/inbox", "big.example/inbox")
	if !out.Waiting {
		t.Errorf("a third waiting activity: %+v", out)
	}
	e.mustExec(`INSERT INTO domain_policies (domain, policy) VALUES ('moved.example', 'suspend')`)
	if err := e.a.fed.loadPolicy(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.count(`SELECT count(*) FROM ap_deliveries WHERE state = 'queued'`) != deliveryQueueMax {
		t.Errorf("%d rows queued, want the cap of %d", e.count(`SELECT count(*) FROM ap_deliveries WHERE state = 'queued'`), deliveryQueueMax)
	}
	// Under 40,000 rows, a step fans out the oldest that waits, then the next.
	e.mustExec(`DELETE FROM ap_deliveries WHERE host LIKE '%.fan.example' AND id IN (SELECT id FROM ap_deliveries WHERE host LIKE '%.fan.example' ORDER BY id LIMIT $1)`,
		e.count(`SELECT count(*) FROM ap_deliveries WHERE state = 'queued'`)-deliveryQueueResume+3)
	e.step()
	if st, _, _, _ := e.row(waitA, "other.example/inbox"); st != "gone" || len(e.hits(waitB)) != 0 {
		t.Errorf("the first waiting activity: its row is %s, want sent, and only it", st)
	}
	e.step()
	if st, _, _, _ := e.row(waitB, "social.example/inbox"); st != "gone" {
		t.Errorf("the second waiting activity: %s", st)
	}
	e.step()
	if st, _, _, _ := e.row(waitC, "big.example/inbox"); st != "gone" || e.count(`SELECT count(*) FROM ap_deliveries WHERE host = 'moved.example'`) != 0 {
		t.Errorf("the third waiting activity: big.example's row is %s, moved.example has %d rows (the domain policy at the fan-out)", st, e.count(`SELECT count(*) FROM ap_deliveries WHERE host = 'moved.example'`))
	}
	if len(e.hits(waitA)) != 1 || len(e.hits(waitB)) != 1 || e.count(`SELECT count(*) FROM ap_outbox WHERE NOT fanned_out`) != 0 {
		t.Errorf("after the fan-out: %v %v", e.hits(waitA), e.hits(waitB))
	}
	// Past deliveryWaitingMax activities waiting, enqueue refuses, and writes nothing.
	e.mustExec(`INSERT INTO ap_outbox (ap_id, body, targets, created_at) SELECT 'https://here.example/w/' || g, '{}', '{https://a.example/inbox}', now() FROM generate_series(1, $1) g`, deliveryWaitingMax)
	before := e.count(`SELECT count(*) FROM ap_outbox`)
	if _, err := e.q.enqueue(context.Background(), instanceActor, "https://here.example/a/full", body, []deliveryTarget{{Actor: "https://b.example/u", Inbox: "https://b.example/inbox"}}); !errors.Is(err, errDeliveryFull) || e.count(`SELECT count(*) FROM ap_outbox`) != before {
		t.Errorf("with %d activities waiting: %v", deliveryWaitingMax, err)
	}
}

// TestDeliveryShutdown: a row claimed and not yet sent when the stop comes is given back; a request in flight is let
// finish, through serve's wait, and its answer written; one that outlasts the stop keeps its lease (8.6).
func TestDeliveryShutdown(t *testing.T) {
	t.Run("claimed, not sent", func(t *testing.T) {
		e := newDelivEnv(t)
		id, _ := e.send("a.example/inbox")
		waiting := make(chan struct{})
		e.q.pace.next = e.clock.now().Add(500 * time.Millisecond) // the place is ahead: the worker waits for it
		// A worker stopping may claim the row again just after the first gave it back (its claim was under way when
		// the stop came) and reach its place too; it gives the row back as well and sends nothing, which the checks
		// below hold. So the signal is closed once, and the places taken are counted only to show it.
		var places atomic.Int32
		signal := sync.OnceFunc(func() { close(waiting) })
		e.q.pace.wait = func(ctx context.Context, _ time.Duration) error {
			places.Add(1)
			signal()
			<-ctx.Done()
			return ctx.Err()
		}
		e.q.manual, e.q.workers = false, 2
		e.q.kick()
		<-waiting
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := e.a.fed.stop(c); err != nil {
			t.Fatal(err)
		}
		if st, at, _, _ := e.row(id, "a.example/inbox"); st != "queued" || at != 0 || len(e.hits(id)) != 0 || e.count(`SELECT count(*) FROM ap_deliveries WHERE locked_until IS NOT NULL`) != 0 {
			t.Errorf("after the stop: %s, %d attempts, %d requests (%d places taken)", st, at, len(e.hits(id)), places.Load())
		}
		if n := places.Load(); n < 1 || n > 2 {
			t.Errorf("%d places taken by 2 workers stopping on one row", n)
		}
		e.q.kick() // after a stop nothing starts again
		if worked, _ := e.q.step(e.q.ctx); worked {
			t.Error("a worker claimed after the stop")
		}
	})

	t.Run("in flight, through serve", func(t *testing.T) {
		e := newDelivEnv(t)
		id, _ := e.send("a.example/inbox")
		release := e.s.holdAt("a.example")
		t.Cleanup(release)
		e.q.manual, e.q.workers = false, 1
		e.q.kick()
		<-e.s.arrived
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		served := make(chan error, 1)
		go func() { served <- e.a.serve(ctx, &http.Server{Handler: http.NotFoundHandler()}, ln, func() {}) }()
		cancel()
		select {
		case err := <-served:
			t.Fatalf("serve returned (%v) while a delivery was in flight", err)
		case <-time.After(300 * time.Millisecond): // fixed: showing that serve does not return early
		}
		release()
		select {
		case err := <-served:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("serve did not return after the request ended")
		}
		if len(e.hits(id)) != 1 || e.count(`SELECT count(*) FROM ap_deliveries`) != 0 {
			t.Errorf("the request in flight: %d requests, %d rows left", len(e.hits(id)), e.count(`SELECT count(*) FROM ap_deliveries`))
		}
	})

	t.Run("outlasting the stop", func(t *testing.T) {
		e := newDelivEnv(t)
		e.send("a.example/inbox")
		release := e.s.holdAt("a.example")
		e.q.manual, e.q.workers = false, 1
		e.q.kick()
		<-e.s.arrived
		c, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		if err := e.a.fed.stop(c); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("a stop with a request still in flight: %v", err)
		}
		if e.count(`SELECT count(*) FROM ap_deliveries WHERE locked_until > $1 AND attempts = 1`, e.clock.now()) != 1 {
			t.Error("the row in flight does not keep its lease")
		}
		release()
		e.q.wg.Wait()
	})
}

// BenchmarkClaimDelivery: one claim with 50,000 rows queued on 300 hosts, four of them being sent to and 25 with an
// open circuit. Budget: under 2 ms (section 12).
func BenchmarkClaimDelivery(b *testing.B) {
	db := testDB(b)
	o, _ := parseOrigin("https://here.example")
	s, _ := parseKeySecret(testSecret)
	f := newFederation(db, fedSettings{on: true, origin: o, secret: s})
	q := newDeliveryQueue(f, nil, 4, func() bool { return false })
	for _, stmt := range []string{
		`INSERT INTO ap_outbox (ap_id, body, fanned_out, created_at) SELECT 'https://here.example/a/' || g, '{}', true, now() FROM generate_series(1, 50) g`,
		`INSERT INTO ap_deliveries (outbox_id, inbox, host, next_at, created_at) SELECT 1 + g % 50, 'https://h' || (g % 300) || '.fan.example/u/' || g, 'h' || (g % 300) || '.fan.example', now() - interval '1 minute', now() FROM generate_series(1, 50000) g`,
		`INSERT INTO ap_hosts (host, created_at) SELECT 'h' || g || '.fan.example', now() FROM generate_series(0, 299) g`,
		`ANALYZE`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			b.Fatal(err)
		}
	}
	busy := []string{"h0.fan.example", "h1.fan.example", "h2.fan.example", "h3.fan.example"}
	// 25 hosts with an open circuit, their rows moved past its end as hostFailed does: the claim passes over none.
	for _, stmt := range []string{
		`UPDATE ap_hosts SET state = 'open', until = now() + interval '10 minutes' WHERE host IN (SELECT 'h' || g || '.fan.example' FROM generate_series(10, 34) g)`,
		`UPDATE ap_deliveries SET next_at = now() + interval '10 minutes' WHERE host IN (SELECT host FROM ap_hosts WHERE state = 'open')`,
		`ANALYZE`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			b.Fatal(err)
		}
	}
	ctx := context.Background()
	// Each row claimed is given back, untimed, so the rows ahead of the next claim are as a running queue has
	// them: a lease held only by the rows in flight.
	for b.Loop() {
		c, err := q.claim(ctx, busy)
		if err != nil || c == nil {
			b.Fatal(c, err)
		}
		b.StopTimer()
		if err := q.giveBack(c, 0); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
	}
	if per := b.Elapsed() / time.Duration(b.N); per > 2*time.Millisecond {
		b.Errorf("a claim took %v, over the budget of 2 ms", per)
	}
}

// BenchmarkDeliveryFanOut: a post to 1,000 followers on 300 hosts (section 12's small instance), each host answering
// at once, with the real pacer and four workers: the time is the fan-out's, about 30 s, bound by 10 requests a
// second. It is the measurement the owner makes on the Pi (FWR 2's todo says how); it takes that long anywhere.
func BenchmarkDeliveryFanOut(b *testing.B) {
	db := testDB(b)
	o, _ := parseOrigin("https://here.example")
	s, _ := parseKeySecret(testSecret)
	f := newFederation(db, fedSettings{on: true, origin: o, secret: s})
	f.policy.Store(&fedPolicy{mode: policyOpen})
	if _, err := f.ensureKey(context.Background(), instanceActor); err != nil {
		b.Fatal(err)
	}
	q := newDeliveryQueue(f, nil, deliveryWorkersDefault, func() bool { return false })
	q.manual, q.built = true, time.Time{}
	q.fetch = deliveryStub(b, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.Copy(io.Discard, r.Body); w.WriteHeader(202) }))
	q.fetch.origin = o
	to := make([]deliveryTarget, 1000)
	for i := range to {
		to[i] = deliveryTarget{Actor: fmt.Sprintf("https://h%d.fan.example/users/u%d", i%300, i), Inbox: fmt.Sprintf("https://h%d.fan.example/users/u%d/inbox", i%300, i), SharedInbox: fmt.Sprintf("https://h%d.fan.example/inbox", i%300)}
	}
	body := bytes.Repeat([]byte(" "), 2000)
	copy(body, `{"type":"Create"}`)
	for i := 0; b.Loop(); i++ {
		out, err := q.enqueue(context.Background(), instanceActor, fmt.Sprintf("https://here.example/a/%d", i), body, to)
		if err != nil || out.Queued != 300 {
			b.Fatal(out, err)
		}
		var wg sync.WaitGroup
		for range q.workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					worked, err := q.step(context.Background())
					if err != nil {
						b.Error(err)
						return
					}
					if !worked {
						return
					}
				}
			}()
		}
		wg.Wait()
	}
}

// TestFederationMemory: section 12's budget, 32 MB of Go heap with 50,000 rows queued, 4 workers sending and the
// inbox at its concurrency. The rows live in Postgres, not in the Go heap: the queue holds in memory only the rows
// its workers claimed. So what is measured is the heap in use (after a collection) while four workers each hold a
// claimed 256 KB activity in a request the stub holds (the claims made with 50,000 rows queued, so whatever the
// claim costs at that size is in it), against the heap before the workers started; the Postgres server's memory is
// not Go heap and not counted. To it are added the inbox's share as bounds, not as a simultaneous measurement: its 8
// bodies of 256 KB being read, and 4 decodes, each the most one decode of a 256 KB body was measured to allocate here
// (TestDecodeMemory's inputs). The Pi's figure and the container's (docker stats, FWR 8) are not measured here.
func TestFederationMemory(t *testing.T) {
	e := newDelivEnv(t)
	big := map[string]any{"@context": "https://www.w3.org/ns/activitystreams", "type": "Create", "actor": "https://here.example/actor", "content": strings.Repeat("x", 250<<10)}
	for a := range 50 {
		big["id"] = fmt.Sprintf("https://here.example/activities/big%d", a)
		body, _ := json.Marshal(big)
		e.mustExec(`INSERT INTO ap_outbox (ap_id, body, fanned_out, created_at) VALUES ($1, $2, true, $3)`, big["id"], string(body), e.clock.now())
	}
	e.mustExec(`INSERT INTO ap_deliveries (outbox_id, inbox, host, next_at, created_at) SELECT (SELECT min(id) FROM ap_outbox) + g % 50, 'https://h' || (g % 300) || '.fan.example/u/' || g, 'h' || (g % 300) || '.fan.example', $1, $1 FROM generate_series(1, 50000) g`, e.clock.now())
	all := make(chan struct{})
	e.s.mu.Lock()
	e.s.holdAll = all
	e.s.mu.Unlock()
	var before, during runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	e.q.manual, e.q.workers = false, deliveryWorkersDefault
	e.q.kick()
	for range deliveryWorkersDefault {
		<-e.s.arrived
	}
	runtime.GC()
	runtime.ReadMemStats(&during)
	e.q.stop()
	close(all)
	e.q.wg.Wait()
	queue := int64(during.HeapInuse) - int64(before.HeapInuse)
	// The inbox's share: the decode that allocates the most of TestDecodeMemory's inputs, four at once, and the
	// bodies being read.
	var decode uint64
	for _, doc := range apAdversarial(256 << 10) {
		var a, b runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&a)
		decodeNote(doc, 256<<10)
		runtime.ReadMemStats(&b)
		decode = max(decode, b.TotalAlloc-a.TotalAlloc)
	}
	inbox := int64(inboxAtOnce)*int64(decode) + int64(inboxReadsAtOnce)*inboxBodyMax
	t.Logf("the queue's workers, %d requests in flight with 50,000 rows queued in Postgres: %.1f MB of heap; the inbox's bound: %d decodes of %.1f MB and %d bodies of 256 KB, %.1f MB; in all %.1f MB of the 32 MB budget",
		deliveryWorkersDefault, float64(queue)/(1<<20), inboxAtOnce, float64(decode)/(1<<20), inboxReadsAtOnce, float64(inbox)/(1<<20), float64(queue+inbox)/(1<<20))
	if queue+inbox > 32<<20 {
		t.Errorf("federation's heap at peak: %.1f MB, over the budget of 32 MB", float64(queue+inbox)/(1<<20))
	}
}

// Two Flowa instances: b.example enqueues a signed Update of its member's profile for here.example; its worker
// sends it through the real fetcher, in the draft form and then in RFC 9421 (as recorded for the host), and
// here.example's real inbox fetches the key from b.example, verifies, queues both and records b.example's form.
func TestDBTwoFlowaDelivery(t *testing.T) {
	be := newEnv(t)
	if err := be.fedStart(map[string]string{"FLOWA_ORIGIN": "https://b.example"}); err != nil {
		t.Fatal(err)
	}
	anaID, anaTok := be.user("ana", "user")
	be.must(200, anaTok, "PUT", "/api/me", map[string]any{"federates": true, "displayName": "Ana B"})
	for _, o := range []keyOwner{keyOwner(anaID), instanceActor} {
		if _, err := be.a.fed.ensureKey(context.Background(), o); err != nil {
			t.Fatal(err)
		}
	}
	settleKeys(t, be.a.fed)
	be.setFlag(fedFlag, true)
	be.mustExec(`INSERT INTO instance_settings (key, value) VALUES ('federation_policy', '"open"') ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
	if err := be.a.fed.loadPolicy(context.Background()); err != nil {
		t.Fatal(err)
	}
	e := newInboxEnv(t)
	e.allow("b.example")
	ft := actorStub(t, be.h)
	ft.origin = e.a.fed.origin
	e.c.fetch = ft
	q := be.a.fed.out
	q.manual, q.now, q.built, q.jitter = true, e.clock.now, time.Time{}, func() float64 { return 0 }
	q.mu.Lock()
	q.floor = time.Time{}
	q.mu.Unlock()
	q.fetch = actorStub(t, e.h)
	q.fetch.origin = be.a.fed.origin
	anaURL := "https://b.example/users/ana"
	for i, form := range []string{"cavage", "rfc9421"} {
		if form == "rfc9421" {
			be.mustExec(`UPDATE ap_hosts SET sig = 'rfc9421' WHERE host = 'here.example'`)
		}
		body := activity(anaURL, "Update", map[string]any{"id": anaURL, "type": "Person", "inbox": anaURL + "/inbox"}, nil)
		var act struct{ ID string }
		json.Unmarshal(body, &act)
		out, err := q.enqueue(context.Background(), keyOwner(anaID), act.ID, body, []deliveryTarget{{Actor: "https://here.example/users/cyd", Inbox: "https://here.example/users/cyd/inbox", SharedInbox: "https://here.example/inbox"}})
		if err != nil || out.Queued != 1 {
			t.Fatalf("%s: enqueue %+v %v", form, out, err)
		}
		if worked, err := q.step(context.Background()); !worked || err != nil {
			t.Fatalf("%s: step %v %v", form, worked, err)
		}
		if e.last() != inAccepted || e.queued() != i+1 {
			t.Errorf("%s: here.example answered %v and has %d rows", form, e.last(), e.queued())
		}
	}
	if be.count(`SELECT count(*) FROM ap_hosts WHERE host = 'here.example' AND delivered = 2 AND state = 'ok'`) != 1 || be.count(`SELECT count(*) FROM ap_deliveries`) != 0 {
		t.Error("b.example's record of here.example is not two delivered")
	}
	if e.count(`SELECT count(*) FROM ap_hosts WHERE host = 'b.example' AND sig = 'rfc9421'`) != 1 {
		t.Error("here.example did not record that b.example signs with RFC 9421")
	}
}

// /healthz in a mode that federates: the queue's fields to a local probe only; the public answer is as it was
// (14.2). Local and plain single mode are TestDBHealthz's and TestDBLocalModeUnchanged's.
func TestDBHealthzFederation(t *testing.T) {
	e := newDelivEnv(t)
	e.answer(func(string, string, string) (int, string) { return 503, "" })
	e.send("a.example/inbox", "b.example/inbox")
	e.step()
	e.clock.add(90 * time.Second)
	e.a.healthMu.Lock()
	e.a.healthAt, e.a.fedHealthAt = time.Time{}, time.Time{}
	e.a.healthMu.Unlock()
	code, out := probe(t, e.h, "127.0.0.1:4000", nil)
	fd, _ := out["federation"].(map[string]any)
	if code != 200 || out["ok"] != true || fd["mode"] != "federated" || fd["queued"] != 2.0 || fd["oldestQueuedSeconds"] != 90.0 || fd["failingHosts"] != 1.0 {
		t.Errorf("a local probe: %d %v", code, out)
	}
	if code, out := probe(t, e.h, "203.0.113.9:5000", nil); code != 200 || len(out) != 1 || out["ok"] != true {
		t.Errorf("a visitor: %d %v", code, out)
	}
	// A remote probe runs no statement of the queue's, however many come; local probes within fedHealthEvery share
	// one answer.
	ran := e.q.numbersRun.Load()
	for range 5 {
		e.a.healthMu.Lock()
		e.a.healthAt, e.a.fedHealthAt = time.Time{}, time.Time{}
		e.a.healthMu.Unlock()
		probe(t, e.h, "203.0.113.9:5000", nil)
		probe(t, e.h, "127.0.0.1:4000", map[string]string{"X-Forwarded-For": "198.51.100.1"})
	}
	if n := e.q.numbersRun.Load() - ran; n != 0 {
		t.Errorf("remote probes asked the queue's numbers %d times", n)
	}
	e.a.healthMu.Lock()
	e.a.fedHealthAt = time.Now().Add(time.Hour) // the last answer is fresh, whatever the wall clock does meanwhile
	e.a.healthMu.Unlock()
	probe(t, e.h, "127.0.0.1:4000", nil)
	if n := e.q.numbersRun.Load() - ran; n != 0 {
		t.Errorf("a local probe within %v of the last asked again (%d)", fedHealthEvery, n)
	}
	e.a.healthMu.Lock()
	e.a.fedHealthAt = time.Time{}
	e.a.healthMu.Unlock()
	probe(t, e.h, "127.0.0.1:4000", nil)
	if n := e.q.numbersRun.Load() - ran; n != 1 {
		t.Errorf("a local probe after the cache asked %d times, want once", n)
	}
	// The Federation tab shows the queue's numbers to staff.
	_, mod := e.user("mo", "moderator")
	d, _ := e.must(200, mod, "GET", "/api/admin/federation", nil)["deliveries"].(map[string]any)
	if d == nil || d["queued"] != 2.0 || d["queueMax"] != float64(deliveryQueueMax) || d["failing"] != 1.0 || d["failedMax"] != float64(deliveryFailedKeep) {
		t.Errorf("the Federation tab: %v", d)
	}
}

// Housekeeping removes failed rows and activities with no row left after 7 days, and keeps the rest (8.5, 9.3).
func TestDBDeliveryRetention(t *testing.T) {
	e := newDelivEnv(t)
	e.answer(func(host, _, _ string) (int, string) {
		if host == "a.example" {
			return 403, ""
		}
		return 202, ""
	})
	failed, _ := e.send("a.example/inbox")
	e.step()
	sent, _ := e.send("b.example/inbox")
	e.step()
	waits, _ := e.send("c.example/inbox")
	e.clock.add(deliveryKeep + time.Hour)
	e.mustExec(`UPDATE ap_deliveries SET next_at = $1::timestamptz + interval '1 day', created_at = $1 WHERE state = 'queued'`, e.clock.now()) // still waiting, and young
	n, err := e.a.housekeep()
	if err != nil {
		t.Fatal(err)
	}
	if n["deliveries"] != 1 || n["outbox"] != 2 {
		t.Errorf("housekeeping removed %d rows and %d activities: %v", n["deliveries"], n["outbox"], n)
	}
	if e.count(`SELECT count(*) FROM ap_outbox WHERE ap_id = $1`, waits) != 1 || e.count(`SELECT count(*) FROM ap_outbox WHERE ap_id IN ($1, $2)`, failed, sent) != 0 {
		t.Error("the activity with a row queued went, or a finished one stayed")
	}
	// More failed rows than deliveryFailedKeep, all younger than 7 days: the oldest past the cap go, in batches of
	// deliveryBatch, each a statement of its own with the turn given back between them.
	extra := deliveryFailedKeep + 2*deliveryBatch + 17
	e.mustExec(`INSERT INTO ap_outbox (ap_id, body, fanned_out, created_at) VALUES ('https://here.example/a/old', '{}', true, $1)`, e.clock.now())
	e.mustExec(`INSERT INTO ap_deliveries (outbox_id, inbox, host, state, next_at, created_at, failed_at) SELECT (SELECT id FROM ap_outbox WHERE ap_id = 'https://here.example/a/old'), 'https://h' || (g % 50) || '.fan.example/u/' || g, 'h' || (g % 50) || '.fan.example', 'failed', $1, $1, $1::timestamptz - g * interval '1 second' FROM generate_series(1, $2) g`, e.clock.now(), extra)
	n, err = e.a.housekeep()
	if err != nil {
		t.Fatal(err)
	}
	if got := e.count(`SELECT count(*) FROM ap_deliveries WHERE state = 'failed'`); got != deliveryFailedKeep || n["deliveries"] != int64(extra-deliveryFailedKeep) {
		t.Errorf("after the cap: %d failed rows kept (want %d), %d removed", got, deliveryFailedKeep, n["deliveries"])
	}
	if e.count(`SELECT count(*) FROM ap_deliveries WHERE state = 'failed' AND failed_at < $1::timestamptz - make_interval(secs => $2)`, e.clock.now(), deliveryFailedKeep) != 0 {
		t.Error("the rows kept are not the newest")
	}
}

// A clock that jumps forward does not empty the queue (8.3): rows too old for the schedule are held for an hour on
// the monotonic clock, nothing is cleared meanwhile, and the page says so. Set right again, rows dated in the future
// are pulled back and sent; a server that was really off that long gives them up after the hour, as 8.7 says.
func TestDBDeliveryClockJump(t *testing.T) {
	t.Run("forward, then corrected", func(t *testing.T) {
		e := newDelivEnv(t)
		e.answer(func(string, string, string) (int, string) { return 503, "" })
		var ids []string
		for range 3 {
			id, _ := e.send("a.example/inbox", "b.example/inbox")
			ids = append(ids, id)
		}
		e.mustExec(`INSERT INTO ap_outbox (ap_id, body, fanned_out, created_at) VALUES ('https://here.example/a/f', '{}', true, $1)`, e.clock.now())
		e.mustExec(`INSERT INTO ap_deliveries (outbox_id, inbox, host, state, next_at, created_at, failed_at) SELECT id, 'https://c.example/inbox', 'c.example', 'failed', $1, $1, $1 FROM ap_outbox WHERE ap_id = 'https://here.example/a/f'`, e.clock.now())
		e.clock.add(365 * 24 * time.Hour)
		e.drain(20)
		if n := len(e.s.requests()); n != 0 || e.count(`SELECT count(*) FROM ap_deliveries WHERE state = 'queued' AND attempts = 0`) != 6 {
			t.Fatalf("after the jump: %d requests, %d rows held of 6", n, e.count(`SELECT count(*) FROM ap_deliveries WHERE state = 'queued' AND attempts = 0`))
		}
		_, admin := e.user("boss", "admin")
		if d := e.must(200, admin, "GET", "/api/admin/federation", nil)["deliveries"].(map[string]any); d["clockJumped"] != true || d["clockWaiting"] != true {
			t.Errorf("the page does not say the clock may have jumped: %v", d)
		}
		// Written while the clock is ahead: dated a year on.
		during, _ := e.send("b.example/inbox")
		if n, err := e.a.housekeep(); err != nil || e.count(`SELECT count(*) FROM ap_deliveries WHERE state = 'failed'`) != 1 {
			t.Errorf("housekeeping cleared on a doubted clock: %v %v", n, err)
		}
		// The clock is set right: the doubt ends, the rows dated a year ahead are pulled back, and all are sent.
		e.clock.add(-365 * 24 * time.Hour)
		if _, err := e.a.housekeep(); err != nil {
			t.Fatal(err)
		}
		if n := e.count(`SELECT count(*) FROM ap_deliveries WHERE state = 'queued' AND (next_at > $1 OR created_at > $2)`, e.clock.now().Add(deliveryFutureMax), e.clock.now()); n != 0 {
			t.Errorf("%d rows still dated ahead after the clock was set right", n)
		}
		e.answer(func(string, string, string) (int, string) { return 202, "" })
		e.clock.add(deliveryFutureMax + time.Minute)
		e.drain(20)
		for _, id := range append(ids, during) {
			if len(e.hits(id)) == 0 {
				t.Errorf("%s was not sent after the clock was set right", id)
			}
		}
		if e.count(`SELECT count(*) FROM ap_deliveries WHERE state = 'queued'`) != 0 {
			t.Error("rows are left queued")
		}
	})

	t.Run("off for five days", func(t *testing.T) {
		e := newDelivEnv(t)
		id, _ := e.send("a.example/inbox")
		e.mustExec(`UPDATE ap_deliveries SET created_at = created_at - interval '5 days', next_at = next_at - interval '5 days'`)
		up := time.Minute
		e.q.since = func() time.Duration { return up }
		e.step()
		if st, at, _, _ := e.row(id, "a.example/inbox"); st != "queued" || at != 0 {
			t.Fatalf("a row five days old within the hour: %s %d", st, at)
		}
		up += deliveryClockDoubt + time.Minute
		e.clock.add(2 * time.Minute)
		e.step()
		if st, _, _, why := e.row(id, "a.example/inbox"); st != "failed" || why != "not delivered within 48 hours" || len(e.hits(id)) != 0 {
			t.Errorf("after the hour: %s %q", st, why)
		}
	})
}

// A host marked unavailable just after it proved alive is not: the mark is written only if the host is still due
// for it, so a valid delivery from it between the counted failure and the mark wins (8.4).
func TestDBDeliveryUnavailableRace(t *testing.T) {
	e := newDelivEnv(t)
	e.answer(func(string, string, string) (int, string) { return 503, "" })
	id, _ := e.send("a.example/inbox")
	e.mustExec(`UPDATE ap_hosts SET down_since = $1, last_failure = $2 WHERE host = 'a.example'`, e.clock.now().Add(-hostUnavailableAfter), e.clock.now().Add(-time.Hour))
	heard := false
	e.q.beforeUnavailable = func() { heard = true; e.q.heardFrom(context.Background(), "a.example", sigCavage) }
	e.step()
	if !heard {
		t.Fatal("the hook between the two statements was not reached")
	}
	if st, _, _, _ := e.row(id, "a.example/inbox"); st != "queued" || e.count(`SELECT count(*) FROM ap_hosts WHERE host = 'a.example' AND state = 'ok'`) != 1 {
		t.Errorf("a host that proved alive in between was marked unavailable: its row is %s", st)
	}
}

// A 429 is the host asking us to slow down (8.3, 8.4): its Retry-After, bounded to an hour, holds the whole host, and
// it does not reset the count of failures, so a host answering 503 and 429 in turn still opens its circuit.
func TestDBDelivery429(t *testing.T) {
	e := newDelivEnv(t)
	k := 0
	e.answer(func(string, string, string) (int, string) {
		k++
		if k%2 == 0 {
			return 429, ""
		}
		return 503, ""
	})
	for range 20 {
		e.send("a.example/inbox")
	}
	e.drain(40)
	if e.count(`SELECT count(*) FROM ap_hosts WHERE host = 'a.example' AND state = 'open'`) != 1 {
		t.Errorf("503 and 429 in turn did not open the circuit (%d requests)", len(e.s.requests()))
	}
	for ra, hold := range map[string]time.Duration{"120": 2 * time.Minute, "7200": hostRetryAfterMax} {
		f := newDelivEnv(t)
		f.answer(func(string, string, string) (int, string) { return 429, ra })
		f.send("b.example/inbox")
		f.send("b.example/inbox")
		f.step()
		var until time.Time
		if err := f.a.db.QueryRow(`SELECT until FROM ap_hosts WHERE host = 'b.example' AND state = 'open'`).Scan(&until); err != nil || !until.Equal(f.clock.now().Add(hold)) {
			t.Errorf("Retry-After %s: the host is held until %v (now %v), want %v later: %v", ra, until, f.clock.now(), hold, err)
		}
		if f.step() {
			t.Errorf("Retry-After %s: a row was claimed while the host is held", ra)
		}
	}
}

// One spelling per address: the scheme and host in lower case, no :443, so two followers naming their server's shared
// inbox in different letter cases are one row (8.1).
func TestDBDeliveryAddressSpelling(t *testing.T) {
	e := newDelivEnv(t)
	in, _, refused := e.q.collect([]deliveryTarget{
		{Actor: "https://a.example/u1", Inbox: "https://a.example/u1/inbox", SharedInbox: "https://a.example/inbox"},
		{Actor: "https://a.example/u2", Inbox: "https://a.example/u2/inbox", SharedInbox: "https://A.EXAMPLE/inbox"},
		{Actor: "https://a.example/u3", Inbox: "https://a.example/u3/inbox", SharedInbox: "HTTPS://a.example:443/inbox"},
	})
	if !slices.Equal(in, []string{"https://a.example/inbox"}) || refused != 0 {
		t.Errorf("one shared inbox in three spellings: %v, %d refused", in, refused)
	}
}
