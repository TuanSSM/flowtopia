package main

// The fetcher's federation kinds against a local TLS stub: where a request may go, what it sends, and
// what comes back. The stub is reached through the test-only dial override, which stands in for DNS;
// every address it gives still goes through the dialer's guard, so a name that "resolves" to 10.0.0.1
// is refused exactly as a real one would be.

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// stubNames are the names the stub's certificate covers and the override resolves to it.
var stubNames = []string{"remote.example", "other.example", "flowa.example", "blocked.example", "sub.blocked.example", "xn--bcher-kva.example", "pics.example", "mixed.example"}

// stubCert is a self-signed certificate for stubNames, made once for the whole run.
var stubCert = sync.OnceValues(func() (tls.Certificate, *x509.CertPool) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "flowa test stub"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true, DNSNames: stubNames,
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

// fedStub starts h behind TLS and returns a production fetcher (no link card stub, the address guard
// on) that reaches it: stubNames resolve to the stub, private.example to 10.0.0.1, mixed.example to
// 10.0.0.1 and then the stub, port.example to a public address on port 8443, anything else to nothing.
func fedStub(t testing.TB, h http.Handler) (*fetcher, *httptest.Server) {
	t.Helper()
	cert, pool := stubCert()
	srv := httptest.NewUnstartedServer(h)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.Config.ErrorLog = log.New(io.Discard, "", 0) // the untrusted certificate case makes the stub complain
	srv.StartTLS()
	t.Cleanup(srv.Close)
	stub := srv.Listener.Addr().String()
	f := newFetcher("", false)
	f.version = "test"
	f.over = &dialOverride{stub: stub, resolve: func(host string) []string {
		host = strings.TrimSuffix(strings.ToLower(host), ".") // as DNS reads a name
		switch host {
		case "private.example":
			return []string{"10.0.0.1:443"}
		case "mixed.example":
			return []string{"10.0.0.1:443", stub}
		case "port.example":
			return []string{"93.184.216.34:8443"}
		}
		for _, n := range stubNames {
			if host == n {
				return []string{stub}
			}
		}
		return nil
	}}
	f.tr.TLSClientConfig.RootCAs = pool
	t.Cleanup(f.tr.CloseIdleConnections)
	return f, srv
}

func asDoc(w http.ResponseWriter, id string) {
	w.Header().Set("Content-Type", "application/activity+json")
	w.Write([]byte(`{"id":"` + id + `","type":"Person"}`))
}

// fedHandler is the remote world the refusal table asks.
func fedHandler() http.Handler {
	mux := http.NewServeMux()
	doc := func(w http.ResponseWriter, r *http.Request) { asDoc(w, "https://"+r.Host+r.URL.Path) }
	typed := func(ct string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", ct)
			w.Write([]byte(`{"id":"https://remote.example` + r.URL.Path + `"}`))
		}
	}
	redirect := func(to string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, to, http.StatusFound) }
	}
	mux.HandleFunc("remote.example/users/ana", doc)
	mux.HandleFunc("other.example/users/ana", doc)
	mux.HandleFunc("mixed.example/users/ana", doc)
	mux.HandleFunc("flowa.example/actor", doc)
	mux.HandleFunc("remote.example/users/ld", typed(`application/ld+json; profile="https://www.w3.org/ns/activitystreams"`))
	mux.HandleFunc("remote.example/users/ldplain", typed(`application/ld+json`))
	mux.HandleFunc("remote.example/users/ldother", typed(`application/ld+json; profile="https://example.org/other"`))
	mux.HandleFunc("remote.example/users/json", typed(`application/json`))
	mux.HandleFunc("remote.example/users/html", typed(`text/html; charset=utf-8`))
	mux.HandleFunc("remote.example/users/notype", func(w http.ResponseWriter, r *http.Request) {
		w.Header()["Content-Type"] = nil
		w.Write([]byte(`{}`))
	})
	mux.HandleFunc("remote.example/users/liar", func(w http.ResponseWriter, r *http.Request) { asDoc(w, "https://remote.example/users/someone-else") })
	mux.HandleFunc("remote.example/users/moved", redirect("/users/ana"))
	mux.HandleFunc("remote.example/r/other", redirect("https://other.example/users/ana"))
	mux.HandleFunc("remote.example/r/private", redirect("https://private.example/users/ana"))
	mux.HandleFunc("remote.example/r/http", redirect("http://remote.example/users/ana"))
	mux.HandleFunc("remote.example/r/blocked", redirect("https://sub.blocked.example/users/ana"))
	mux.HandleFunc("remote.example/r/port", redirect("https://remote.example:8443/users/ana"))
	mux.HandleFunc("remote.example/r/ip", redirect("https://127.0.0.1/users/ana"))
	mux.HandleFunc("remote.example/r/self", redirect("https://flowa.example/actor"))
	mux.HandleFunc("remote.example/r/loop", redirect("/r/loop"))
	mux.HandleFunc("remote.example/.well-known/webfinger", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://other.example/.well-known/webfinger?"+r.URL.RawQuery, http.StatusMovedPermanently)
	})
	mux.HandleFunc("other.example/.well-known/webfinger", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/jrd+json")
		w.Write([]byte(`{"subject":"` + r.URL.Query().Get("resource") + `"}`))
	})
	mux.HandleFunc("remote.example/nodeinfo/2.1", typed(`application/json; profile="http://nodeinfo.diaspora.software/ns/schema/2.1#"`))
	mux.HandleFunc("pics.example/a.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(pngBytes(8, 8))
	})
	mux.HandleFunc("remote.example/big-length", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/activity+json")
		w.Header().Set("Content-Length", "2097152")
		w.Write(make([]byte, 2<<20))
	})
	mux.HandleFunc("remote.example/big-chunked", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/activity+json")
		w.(http.Flusher).Flush() // no Content-Length: only the read can find the size
		for i := 0; i < 17; i++ {
			if _, err := w.Write(make([]byte, 64<<10)); err != nil {
				return
			}
		}
	})
	mux.HandleFunc("remote.example/bomb", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/activity+json")
		w.Header().Set("Content-Encoding", "gzip")
		w.Write(bomb())
	})
	mux.HandleFunc("remote.example/small-gzip", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/activity+json")
		w.Header().Set("Content-Encoding", "gzip")
		var b bytes.Buffer
		z := gzip.NewWriter(&b)
		z.Write([]byte(`{"id":"https://remote.example/small-gzip"}`))
		z.Close()
		w.Write(b.Bytes())
	})
	mux.HandleFunc("remote.example/slow", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/activity+json")
		w.(http.Flusher).Flush()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(time.Second):
				w.Write([]byte(" "))
				w.(http.Flusher).Flush()
			}
		}
	})
	mux.HandleFunc("remote.example/gone", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusGone) })
	mux.HandleFunc("remote.example/missing", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	mux.HandleFunc("remote.example/busy", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write(make([]byte, 1<<20)) // an error page larger than anything read of it
	})
	mux.HandleFunc("remote.example/inbox", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Content-Type") != asType {
			http.Error(w, "no", 400)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		w.Write(make([]byte, 1<<20)) // a reply nobody reads
	})
	mux.HandleFunc("remote.example/inbox-moved", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/inbox", http.StatusTemporaryRedirect)
	})
	// The host is matched as a server matches its own name: any case, a trailing dot, :443 written.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Host = strings.TrimSuffix(strings.TrimSuffix(strings.ToLower(r.Host), ":443"), ".")
		mux.ServeHTTP(w, r)
	})
}

// bomb is about 64 KB of gzip that inflates to 64 MB.
var bomb = sync.OnceValue(func() []byte {
	var b bytes.Buffer
	z, _ := gzip.NewWriterLevel(&b, gzip.BestCompression)
	zero := make([]byte, 1<<20)
	for i := 0; i < 64; i++ {
		z.Write(zero)
	}
	z.Close()
	return b.Bytes()
})

func fakeSign(req *http.Request, body []byte) error {
	req.Header.Set("Signature", "fake for "+req.URL.Path)
	return nil
}

func TestFederationFetcherRefusals(t *testing.T) {
	// The bomb is made before any request: packing its 64 MB takes longer than a request's 5 s on a
	// slow machine under the race detector, and the row is about the reader's cap, not the stub's speed.
	bomb()
	f, _ := fedStub(t, fedHandler())
	o, err := parseOrigin("https://flowa.example")
	if err != nil {
		t.Fatal(err)
	}
	f.origin = o
	refused := []string{"Blocked.Example.", "xn--bcher-kva.example"}
	type tc struct {
		name   string
		kind   fetchKind
		url    string
		fail   fetchFail // 0: a success
		reason string    // part of the reason, for a failure
		unsent bool      // refused before any connection was tried
		self   bool
		check  func(t *testing.T, r *fetchResult)
	}
	idIs := func(want bool) func(t *testing.T, r *fetchResult) {
		return func(t *testing.T, r *fetchResult) {
			id := strings.TrimSuffix(strings.TrimPrefix(string(r.Body), `{"id":"`), `","type":"Person"}`)
			if sameID(id, r.URL) != want {
				t.Errorf("id %q fetched from %s: same %v, want %v", id, r.URL, !want, want)
			}
		}
	}
	long := "https://remote.example/users/" + strings.Repeat("a", 2048)
	cases := []tc{
		// What succeeds.
		{name: "a document", kind: fetchDocument, url: "https://remote.example/users/ana", check: idIs(true)},
		{name: "ld+json with the profile", kind: fetchDocument, url: "https://remote.example/users/ld"},
		{name: "ld+json without a profile", kind: fetchDocument, url: "https://remote.example/users/ldplain"},
		{name: "a same-origin redirect, the id checked against where it ended", kind: fetchDocument, url: "https://remote.example/users/moved", check: func(t *testing.T, r *fetchResult) {
			if r.URL.String() != "https://remote.example/users/ana" {
				t.Errorf("final URL %s", r.URL)
			}
			idIs(true)(t, r)
		}},
		{name: "a host written in capitals with a trailing dot and :443", kind: fetchDocument, url: "https://REMOTE.example.:443/users/ana"},
		{name: "a name with a private address first and then a public one", kind: fetchDocument, url: "https://mixed.example/users/ana"},
		{name: "webfinger redirected to another host", kind: fetchWebFinger, url: "https://remote.example/.well-known/webfinger?resource=acct:ana@remote.example", check: func(t *testing.T, r *fetchResult) {
			if r.URL.Host != "other.example" || r.Type != "application/jrd+json" {
				t.Errorf("final %s, type %s", r.URL, r.Type)
			}
		}},
		{name: "nodeinfo with its profile", kind: fetchNodeInfo, url: "https://remote.example/nodeinfo/2.1"},
		{name: "a picture", kind: fetchPicture, url: "https://pics.example/a.png"},
		{name: "a small gzip answer is inflated", kind: fetchDocument, url: "https://remote.example/small-gzip"},
		{name: "the own origin when the caller says so", kind: fetchDocument, url: "https://flowa.example/actor", self: true},

		// A mismatched id is the caller's check, made with sameID on the final URL.
		{name: "a mismatched id", kind: fetchDocument, url: "https://remote.example/users/liar", check: idIs(false)},

		// Redirects.
		{name: "a redirect to a private address", kind: fetchWebFinger, url: "https://remote.example/r/private", fail: failRefused, reason: "refusing to connect to 10.0.0.1"},
		{name: "a redirect to http", kind: fetchWebFinger, url: "https://remote.example/r/http", fail: failRefused, reason: "redirect to not https"},
		{name: "a redirect to a subdomain of a refused host", kind: fetchWebFinger, url: "https://remote.example/r/blocked", fail: failRefused, reason: "redirect to a refused host"},
		{name: "a redirect to another port", kind: fetchWebFinger, url: "https://remote.example/r/port", fail: failRefused, reason: "redirect to a port other than 443"},
		{name: "a redirect to an IP address", kind: fetchPicture, url: "https://remote.example/r/ip", fail: failRefused, reason: "redirect to an IP address"},
		{name: "a redirect to the own origin", kind: fetchWebFinger, url: "https://remote.example/r/self", fail: failRefused, reason: "redirect to this instance's own origin"},
		{name: "a document redirected to another origin", kind: fetchDocument, url: "https://remote.example/r/other", fail: failRedirect, reason: "another origin"},
		{name: "a picture redirected to another origin", kind: fetchPicture, url: "https://remote.example/r/other", fail: failType},
		{name: "too many redirects", kind: fetchDocument, url: "https://remote.example/r/loop", fail: failRedirect, reason: "too many"},

		// The first URL.
		{name: "http", kind: fetchDocument, url: "http://remote.example/users/ana", fail: failRefused, reason: "not https", unsent: true},
		{name: "another port", kind: fetchDocument, url: "https://remote.example:8443/users/ana", fail: failRefused, reason: "port", unsent: true},
		{name: "port 0443 written", kind: fetchDocument, url: "https://remote.example:0443/users/ana", fail: failRefused, reason: "port", unsent: true},
		{name: "a refused host", kind: fetchDocument, url: "https://blocked.example/users/ana", fail: failRefused, reason: "refused host", unsent: true},
		{name: "a refused host in capitals with a trailing dot", kind: fetchDocument, url: "https://BLOCKED.example./users/ana", fail: failRefused, reason: "refused host", unsent: true},
		{name: "a subdomain of a refused host", kind: fetchDocument, url: "https://sub.blocked.example/users/ana", fail: failRefused, reason: "refused host", unsent: true},
		{name: "a refused xn-- host", kind: fetchDocument, url: "https://xn--bcher-kva.example/", fail: failRefused, reason: "refused host", unsent: true},
		{name: "the same host in Unicode", kind: fetchDocument, url: "https://bücher.example/", fail: failRefused, reason: "not a plain DNS name", unsent: true},
		{name: "the own origin", kind: fetchDocument, url: "https://flowa.example/actor", fail: failRefused, reason: "own origin", unsent: true},
		{name: "the own origin in capitals", kind: fetchDocument, url: "https://FLOWA.example./actor", fail: failRefused, reason: "own origin", unsent: true},
		{name: "a user name", kind: fetchDocument, url: "https://ana:pw@remote.example/users/ana", fail: failRefused, reason: "user name", unsent: true},
		{name: "a URL over 2048 bytes", kind: fetchDocument, url: long, fail: failRefused, reason: "2048", unsent: true},
		{name: "localhost", kind: fetchDocument, url: "https://localhost/x", fail: failRefused, reason: "localhost", unsent: true},
		{name: "a subdomain of localhost", kind: fetchDocument, url: "https://a.localhost/x", fail: failRefused, reason: "localhost", unsent: true},
		{name: "a name with no dot", kind: fetchDocument, url: "https://raspi/x", fail: failRefused, reason: "no dot", unsent: true},
		{name: "a private name", kind: fetchDocument, url: "https://nas.local/x", fail: failRefused, reason: "private name", unsent: true},
		{name: "an empty label", kind: fetchDocument, url: "https://a..example/x", fail: failRefused, reason: "empty label", unsent: true},
		{name: "a delivery without a signer", kind: fetchDelivery, url: "https://remote.example/inbox", fail: failRefused, reason: "unsigned", unsent: true},

		// The answer.
		{name: "a large body with its length", kind: fetchDocument, url: "https://remote.example/big-length", fail: failTooLarge},
		{name: "a large body without a length", kind: fetchDocument, url: "https://remote.example/big-chunked", fail: failTooLarge},
		{name: "a compression bomb", kind: fetchDocument, url: "https://remote.example/bomb", fail: failTooLarge},
		{name: "a wrong type", kind: fetchDocument, url: "https://remote.example/users/html", fail: failType},
		{name: "plain json for a document", kind: fetchDocument, url: "https://remote.example/users/json", fail: failType},
		{name: "ld+json with another profile", kind: fetchDocument, url: "https://remote.example/users/ldother", fail: failType},
		{name: "no type", kind: fetchDocument, url: "https://remote.example/users/notype", fail: failType},
		{name: "a document for a picture", kind: fetchPicture, url: "https://remote.example/users/ana", fail: failType},
		{name: "gone", kind: fetchDocument, url: "https://remote.example/gone", fail: failGone},
		{name: "not found", kind: fetchDocument, url: "https://remote.example/missing", fail: failNotFound},
		{name: "a name that does not resolve", kind: fetchDocument, url: "https://nowhere.example/x", fail: failConnect, reason: "did not resolve"},
	}
	// Every IP literal form a parser or a resolver accepts is refused before a dial.
	for _, h := range []string{"127.0.0.1", "[::1]", "[::ffff:127.0.0.1]", "[::ffff:7f00:1]", "[fe80::1%25eth0]", "2130706433", "0x7f000001", "0177.0.0.1", "127.1", "0x7f.1", "93.184.216.34", "[2606:4700::1]", "10.0.0.1"} {
		cases = append(cases, tc{name: "the address " + h, kind: fetchDocument, url: "https://" + h + "/x", fail: failRefused, unsent: true})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := f.dials.Load()
			res, err := f.do(context.Background(), fetchReq{kind: c.kind, url: c.url, refused: refused, self: c.self})
			if c.fail == 0 {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				if c.check != nil {
					c.check(t, res)
				}
				return
			}
			var fe *fetchError
			if !errors.As(err, &fe) {
				if res != nil {
					t.Fatalf("got %d from %s, %s, %d bytes; want a fetchError", res.Status, res.URL, res.Type, len(res.Body))
				}
				t.Fatalf("got %v; want a fetchError", err)
			}
			if fe.Fail != c.fail || !strings.Contains(fe.Reason, c.reason) {
				t.Errorf("failure %d %q, want %d with %q", fe.Fail, fe.Reason, c.fail, c.reason)
			}
			if strings.Contains(fe.Error(), "?") {
				t.Errorf("the error shows a query: %q", fe.Error())
			}
			if n := f.dials.Load() - before; c.unsent && n != 0 {
				t.Errorf("%d dials for a request refused before any connection", n)
			}
		})
	}
	if len(f.busy) != 0 {
		t.Errorf("%d host slots left after every request ended", len(f.busy))
	}
}

// A slow body is cut at the kind's deadline: the clock covers the body, not only the headers.
func TestFederationFetcherSlowBody(t *testing.T) {
	f, _ := fedStub(t, fedHandler())
	start := time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := f.do(ctx, fetchReq{kind: fetchDocument, url: "https://remote.example/slow"})
		done <- err
	}()
	var err error
	select {
	case err = <-done:
	case <-time.After(8 * time.Second):
		cancel()
		<-done
		t.Fatal("a slow body was still being read after 8 s; the deadline does not cover the body")
	}
	took := time.Since(start)
	var fe *fetchError
	if !errors.As(err, &fe) || fe.Fail != failTimeout || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a slow body: %v", err)
	}
	if took < 4500*time.Millisecond || took > 6500*time.Millisecond {
		t.Errorf("cut after %v, want about 5 s", took)
	}
}

func TestFederationFetcherStatus(t *testing.T) {
	f, _ := fedStub(t, fedHandler())
	_, err := f.do(context.Background(), fetchReq{kind: fetchDocument, url: "https://remote.example/busy"})
	var fe *fetchError
	if !errors.As(err, &fe) || fe.Fail != failStatus || fe.Status != 503 || fe.RetryAfter != 2*time.Minute {
		t.Fatalf("503 with Retry-After: %+v", err)
	}
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	for v, want := range map[string]time.Duration{
		"": 0, "0": 0, "-5": 0, "120": 2 * time.Minute, "99999999999999999999": 0, "9999999999": 12 * time.Hour,
		"Fri, 13 Nov 2026 12:10:00 GMT": 10 * time.Minute, "Thu, 12 Nov 2026 12:00:00 GMT": 0, "Sun, 13 Dec 2026 12:00:00 GMT": 12 * time.Hour, "soon": 0,
	} {
		if got := retryAfter(v, now); got != want {
			t.Errorf("Retry-After %q: %v, want %v", v, got, want)
		}
	}
}

func TestFederationFetcherDelivery(t *testing.T) {
	var hits atomic.Int32
	var seen http.Header
	var body []byte
	inner := fedHandler()
	f, _ := fedStub(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/inbox" {
			seen = r.Header.Clone()
			body = make([]byte, r.ContentLength)
			r.Body.Read(body)
		}
		inner.ServeHTTP(w, r)
	}))
	act := []byte(`{"type":"Create"}`)
	res, err := f.do(context.Background(), fetchReq{kind: fetchDelivery, url: "https://remote.example/inbox", body: act, sign: fakeSign})
	if err != nil || res.Status != 202 || res.Body != nil {
		t.Fatalf("delivery: %+v %v", res, err)
	}
	if !bytes.Equal(body, act) || seen.Get("Content-Type") != asType || seen.Get("Signature") != "fake for /inbox" || seen.Get("Date") == "" || seen.Get("Accept") != "" {
		t.Errorf("delivery sent %q with %v", body, seen)
	}
	// A delivery answered with a redirect is a failure, sent once.
	hits.Store(0)
	_, err = f.do(context.Background(), fetchReq{kind: fetchDelivery, url: "https://remote.example/inbox-moved", body: act, sign: fakeSign})
	var fe *fetchError
	if !errors.As(err, &fe) || fe.Fail != failRedirect || fe.Status != 307 || hits.Load() != 1 {
		t.Fatalf("a redirected delivery: %v, %d requests", err, hits.Load())
	}
	if _, err := f.do(context.Background(), fetchReq{kind: fetchDocument, url: "https://remote.example/users/ana", body: act}); err == nil {
		t.Error("a GET with a body was sent")
	}
}

// The signer runs on every request sent, with Date set, the redirect included, and nothing it added
// is carried to the next request.
func TestFederationFetcherSigner(t *testing.T) {
	type got struct{ path, sig, auth string }
	var mu sync.Mutex
	var seen []got
	f, _ := fedStub(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, got{r.URL.Path, r.Header.Get("Signature"), r.Header.Get("Authorization")})
		mu.Unlock()
		if r.URL.Path == "/signed" {
			http.Redirect(w, r, "/signed2", http.StatusFound)
			return
		}
		asDoc(w, "https://remote.example/signed2")
	}))
	calls := 0
	sign := func(req *http.Request, body []byte) error {
		calls++
		if req.Header.Get("Date") == "" || req.Host != "" && req.Host != req.URL.Host {
			t.Errorf("signer called without a Date or with another host: %v", req.Header)
		}
		if calls == 1 {
			req.Header.Set("Authorization", "secret")
		}
		return fakeSign(req, body)
	}
	if _, err := f.do(context.Background(), fetchReq{kind: fetchDocument, url: "https://remote.example/signed", sign: sign}); err != nil {
		t.Fatal(err)
	}
	want := []got{{"/signed", "fake for /signed", "secret"}, {"/signed2", "fake for /signed2", ""}}
	if calls != 2 || len(seen) != 2 || seen[0] != want[0] || seen[1] != want[1] {
		t.Errorf("signer called %d times; the stub saw %+v, want %+v", calls, seen, want)
	}
	// A signer may not move the request.
	moving := func(req *http.Request, _ []byte) error { req.URL.Path = "/elsewhere"; return nil }
	_, err := f.do(context.Background(), fetchReq{kind: fetchDocument, url: "https://remote.example/signed", sign: moving})
	var fe *fetchError
	if !errors.As(err, &fe) || fe.Fail != failRefused {
		t.Errorf("a signer that changed the URL: %v", err)
	}
	failing := func(*http.Request, []byte) error { return errors.New("no key") }
	if _, err := f.do(context.Background(), fetchReq{kind: fetchDocument, url: "https://remote.example/signed", sign: failing}); !errors.As(err, &fe) || fe.Fail != failRefused {
		t.Errorf("a signer that failed: %v", err)
	}
}

func TestFederationFetcherHeaders(t *testing.T) {
	var mu sync.Mutex
	var seen http.Header
	f, _ := fedStub(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = r.Header.Clone()
		mu.Unlock()
		ct := map[string]string{"/doc": "application/activity+json", "/wf": "application/jrd+json", "/ni": "application/json"}[r.URL.Path]
		w.Header().Set("Content-Type", ct)
		http.SetCookie(w, &http.Cookie{Name: "c", Value: "1"})
		w.Write([]byte("{}"))
	}))
	ask := func(k fetchKind, path string) http.Header {
		t.Helper()
		if _, err := f.do(context.Background(), fetchReq{kind: k, url: "https://remote.example" + path}); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		defer mu.Unlock()
		return seen
	}
	h := ask(fetchDocument, "/doc")
	if h.Get("User-Agent") != "Flowa/test" || h.Get("Accept") != `application/ld+json; profile="https://www.w3.org/ns/activitystreams", application/activity+json` || h.Get("Accept-Encoding") != "gzip" || h.Get("Date") == "" {
		t.Errorf("a document GET without an origin sent %v", h)
	}
	o, _ := parseOrigin("https://flowa.example")
	f.origin = o
	h = ask(fetchDocument, "/doc")
	if h.Get("User-Agent") != "Flowa/test (+https://flowa.example/)" || h.Get("Referer") != "" || h.Get("Cookie") != "" {
		t.Errorf("a second document GET sent %v (no cookie, no referer)", h)
	}
	if h = ask(fetchWebFinger, "/wf"); h.Get("Accept") != "application/jrd+json, application/json" || h.Get("Date") != "" {
		t.Errorf("WebFinger sent %v", h)
	}
	if h = ask(fetchNodeInfo, "/ni"); h.Get("Accept") != "application/json" {
		t.Errorf("NodeInfo sent %v", h)
	}
}

// The transport is the guard's: no proxy (a proxy would dial for it), verified TLS 1.2 or later, no
// cookie jar, bounded pooling.
func TestFederationFetcherTransport(t *testing.T) {
	f := newFetcher("", false)
	tr := f.tr
	if tr.Proxy != nil {
		t.Error("the transport takes a proxy")
	}
	if c := tr.TLSClientConfig; c == nil || c.MinVersion < tls.VersionTLS12 || c.InsecureSkipVerify {
		t.Errorf("TLS: %+v", c)
	}
	if f.client.Jar != nil || f.client.Transport != tr {
		t.Error("the client has a cookie jar or another transport")
	}
	if tr.MaxIdleConns == 0 || tr.MaxIdleConnsPerHost == 0 || tr.IdleConnTimeout == 0 || tr.MaxResponseHeaderBytes == 0 {
		t.Errorf("pooling unbounded: %+v", tr)
	}
	// A proxy in the environment is not used: the request still reaches the stub directly.
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:9")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:9")
	fs, _ := fedStub(t, fedHandler())
	if _, err := fs.do(context.Background(), fetchReq{kind: fetchDocument, url: "https://remote.example/users/ana"}); err != nil {
		t.Errorf("with a proxy in the environment: %v", err)
	}
	// A certificate nobody vouches for is a failed connection.
	fs.tr.TLSClientConfig.RootCAs = x509.NewCertPool()
	fs.tr.CloseIdleConnections()
	_, err := fs.do(context.Background(), fetchReq{kind: fetchDocument, url: "https://remote.example/users/ana"})
	var fe *fetchError
	if !errors.As(err, &fe) || fe.Fail != failConnect || fe.Reason != "could not connect: TLS failed" {
		t.Errorf("an untrusted certificate: %v", err)
	}
}

// The link card stub lets private addresses through, so no federation request is sent while it is set.
func TestFederationFetcherNotWithPreviewStub(t *testing.T) {
	f := newFetcher("http://127.0.0.1:3199", true)
	_, err := f.do(context.Background(), fetchReq{kind: fetchDocument, url: "https://remote.example/users/ana"})
	var fe *fetchError
	if !errors.As(err, &fe) || fe.Fail != failRefused || f.dials.Load() != 0 {
		t.Errorf("a federation request with the link card stub set: %v", err)
	}
	if _, err := f.do(context.Background(), fetchReq{kind: fetchDocument, url: "https://remote.example/users/ana", allow: previewHosts}); err == nil {
		t.Error("an allowlist on a federation kind was taken")
	}
	if _, err := f.do(context.Background(), fetchReq{kind: fetchKinds, url: "https://remote.example/"}); err == nil {
		t.Error("an unknown kind was sent")
	}
}

// The guard on each address family, called as the dialer calls it. These need no IPv6 on the machine.
func TestDialGuard(t *testing.T) {
	for addr, ok := range map[string]bool{
		"93.184.216.34:443":            true,
		"[2606:4700::1]:443":           true,
		"[::ffff:93.184.216.34]:443":   true, // a mapped public IPv4 is that IPv4
		"93.184.216.34:80":             false,
		"93.184.216.34:8443":           false,
		"[2606:4700::1]:80":            false,
		"127.0.0.1:443":                false,
		"10.0.0.1:443":                 false,
		"169.254.169.254:443":          false,
		"100.64.0.1:443":               false,
		"0.0.0.0:443":                  false,
		"[::1]:443":                    false,
		"[::]:443":                     false,
		"[::ffff:127.0.0.1]:443":       false,
		"[::ffff:7f00:1]:443":          false,
		"[::ffff:10.0.0.1]:443":        false,
		"[::127.0.0.1]:443":            false,
		"[64:ff9b::a00:1]:443":         false,
		"[64:ff9b:1::a00:1]:443":       false,
		"[2002:a00:1::1]:443":          false,
		"[2001:0:4136:e378::1]:443":    false,
		"[fe80::1]:443":                false,
		"[fe80::1%eth0]:443":           false,
		"[fd00::1]:443":                false,
		"[ff02::1]:443":                false,
		"[2001:db8::1]:443":            false,
		"not-an-address:443":           false,
		"[::ffff:169.254.169.254]:443": false,
	} {
		err := dialGuard(addr, false, nil)
		var ar *addrRefused
		if ok && err != nil || !ok && !errors.As(err, &ar) {
			t.Errorf("%s: %v, want allowed %v", addr, err, ok)
		}
	}
	// Only the exact stub address passes the test override, and only with it.
	over := &dialOverride{stub: "127.0.0.1:41234"}
	if dialGuard("127.0.0.1:41234", false, over) != nil || dialGuard("127.0.0.1:41235", false, over) == nil || dialGuard("127.0.0.1:41234", false, nil) == nil {
		t.Error("the override lets through more or less than its stub")
	}
}

func TestSameID(t *testing.T) {
	base, _ := url.Parse("https://remote.example/users/ana")
	for id, want := range map[string]bool{
		"https://remote.example/users/ana":              true,
		"https://remote.example/users/ana#main-key":     true,
		"https://REMOTE.Example/users/ana":              true,
		"HTTPS://remote.example/users/ana":              true,
		"https://remote.example:443/users/ana":          true,
		"https://remote.example/users/ana/":             false,
		"https://remote.example/users/Ana":              false,
		"https://remote.example/users/ana?x=1":          false,
		"http://remote.example/users/ana":               false,
		"https://remote.example:8443/users/ana":         false,
		"https://other.example/users/ana":               false,
		"https://sub.remote.example/users/ana":          false,
		"https://ana@remote.example/users/ana":          false,
		"/users/ana":                                    false,
		"":                                              false,
		"https://remote.example/users/someone-else":     false,
		"https://remote.example/users/ana\x00":          false,
		"https:remote.example/users/ana":                false,
		"https://remote.example.evil.example/users/ana": false,
	} {
		if got := sameID(id, base); got != want {
			t.Errorf("id %q fetched from %s: %v, want %v", id, base, got, want)
		}
	}
	q, _ := url.Parse("https://remote.example/objects?id=7")
	if !sameID("https://remote.example/objects?id=7", q) || sameID("https://remote.example/objects?id=8", q) {
		t.Error("the query is part of the id")
	}
	if sameID("https://remote.example/users/ana", nil) {
		t.Error("nothing fetched matches nothing")
	}
}

// One request at a time to one host; the others wait, and the slot goes when nobody holds it.
func TestFederationFetcherPerHost(t *testing.T) {
	var now, most atomic.Int32
	f, _ := fedStub(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := now.Add(1)
		for m := most.Load(); n > m && !most.CompareAndSwap(m, n); m = most.Load() {
		}
		time.Sleep(20 * time.Millisecond) // fixed: long enough for requests to overlap if they could
		now.Add(-1)
		asDoc(w, "https://remote.example/users/ana")
	}))
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.do(context.Background(), fetchReq{kind: fetchDocument, url: "https://remote.example/users/ana"}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	f.mu.Lock()
	left := len(f.busy)
	f.mu.Unlock()
	if most.Load() != 1 || left != 0 {
		t.Errorf("%d requests at once to one host, %d slots left", most.Load(), left)
	}
}

// Cancelled requests, waiting or in flight, leave no goroutine behind.
func TestFederationFetcherLeaks(t *testing.T) {
	block := make(chan struct{})
	f, _ := fedStub(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-block:
		}
	}))
	defer close(block)
	settle := func() int {
		f.tr.CloseIdleConnections()
		runtime.GC()
		return runtime.NumGoroutine()
	}
	before := settle()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	var canceled atomic.Int32
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.do(ctx, fetchReq{kind: fetchDocument, url: "https://remote.example/users/ana"})
			var fe *fetchError
			if errors.As(err, &fe) && fe.Fail == failCanceled && errors.Is(err, context.Canceled) {
				canceled.Add(1)
			}
		}()
	}
	time.Sleep(200 * time.Millisecond) // fixed: let one request reach the stub and the rest wait for the host
	cancel()
	wg.Wait()
	if canceled.Load() != 20 {
		t.Errorf("%d of 20 cancelled requests said so", canceled.Load())
	}
	deadline := time.Now().Add(5 * time.Second)
	n := settle()
	for n > before && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond) // fixed: goroutines end on their own; there is nothing to wait on
		n = settle()
	}
	if n > before {
		buf := make([]byte, 1<<16)
		t.Errorf("%d goroutines before, %d after\n%s", before, n, buf[:runtime.Stack(buf, true)])
	}
}

// What one document fetch costs, over a kept-alive TLS connection to a local stub.
func BenchmarkFetchDocument(b *testing.B) {
	f, _ := fedStub(b, fedHandler())
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := f.do(ctx, fetchReq{kind: fetchDocument, url: "https://remote.example/users/ana", refused: []string{"blocked.example"}}); err != nil {
			b.Fatal(err)
		}
	}
}
