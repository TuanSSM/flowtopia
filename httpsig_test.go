package main

// HTTP signatures (docs/federation.md 7.1 to 7.3): the published vectors, the policy, the replay
// memory, the clock, and a round trip through the fetcher. Every time is a fixed one handed to the
// code under test; nothing here depends on how fast the machine is. Keys are made once, before any
// request is sent.

import (
	"bufio"
	"bytes"
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	tOrigin     = "https://flowa.example"
	tKeyID      = "https://remote.example/users/ana#main-key"
	tOtherKeyID = "https://remote.example/users/bo#main-key"
)

// t0 is the time every signature in these tests is made and checked at, unless a test moves it.
var t0 = time.Date(2026, 11, 12, 18, 4, 6, 0, time.UTC)

// sigTestKeys are two RSA 2048 keys, made once for the run.
var sigTestKeys = sync.OnceValues(func() (*rsa.PrivateKey, *rsa.PrivateKey) {
	a, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	b, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return a, b
})

// sigKit is a verifier for tOrigin with a clock the test sets, and the keys it knows.
type sigKit struct {
	key, other *rsa.PrivateKey
	v          *sigVerifier
	now        time.Time
}

func newSigKit(t testing.TB) *sigKit {
	t.Helper()
	a, b := sigTestKeys()
	k := &sigKit{key: a, other: b, now: t0}
	v, err := newSigVerifier(tOrigin, func(_ context.Context, id string) (*rsa.PublicKey, error) {
		switch id {
		case tKeyID:
			return &a.PublicKey, nil
		case tOtherKeyID:
			return &b.PublicKey, nil
		}
		return nil, errors.New("no such key")
	})
	if err != nil {
		t.Fatal(err)
	}
	v.now = func() time.Time { return k.now }
	v.built = time.Time{}
	k.v = v
	return k
}

func bodyReader(b []byte) io.Reader {
	if b == nil {
		return nil
	}
	return bytes.NewReader(b)
}

// request is an unsigned client request with Date set, as fetcher.send makes it.
func (k *sigKit) request(t testing.TB, method, rawURL string, body []byte) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, rawURL, bodyReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Date", k.now.Format(http.TimeFormat))
	return req
}

// sign is a client request signed by Flowa's signer.
func (k *sigKit) sign(t testing.TB, form sigForm, method, rawURL string, body []byte) *http.Request {
	t.Helper()
	req := k.request(t, method, rawURL, body)
	if err := httpSigner(k.key, tKeyID, form, func() time.Time { return k.now }, time.Time{})(req, body); err != nil {
		t.Fatal(err)
	}
	return req
}

// received is the request a handler sees for a client request: the request target as sent, the same
// header fields, the Host the client sent.
func received(req *http.Request, body []byte) *http.Request {
	r := httptest.NewRequest(req.Method, req.URL.RequestURI(), bytes.NewReader(body))
	r.Header = req.Header.Clone()
	r.Host = req.Host
	return r
}

func (k *sigKit) verify(r *http.Request, body []byte) (*sigResult, error) {
	return k.v.verifyRequest(context.Background(), r, body)
}

func msgOf(req *http.Request) *sigMessage {
	return &sigMessage{method: req.Method, scheme: "https", authority: urlAuthority(req.URL), target: req.URL.RequestURI(), header: req.Header}
}

// cavageWith signs a client request with any header list, algorithm and extra parameters, as
// another server might.
func cavageWith(t testing.TB, req *http.Request, key *rsa.PrivateKey, keyID, alg string, headers []string, extra string) {
	t.Helper()
	s := &cavageSig{keyID: keyID, algorithm: alg, headers: headers}
	for _, p := range strings.Split(extra, ",") {
		if n, v, ok := strings.Cut(p, "="); ok {
			switch n {
			case "created":
				s.created = v
			case "expires":
				s.expires = v
			}
		}
	}
	str, err := cavageString(s, msgOf(req))
	if err != nil {
		t.Fatalf("signing string for %v: %v", headers, err)
	}
	sig, err := rsaSign(key, str)
	if err != nil {
		t.Fatal(err)
	}
	h := `keyId="` + keyID + `"`
	if alg != "" {
		h += `,algorithm="` + alg + `"`
	}
	if extra != "" {
		h += "," + extra
	}
	h += `,headers="` + strings.Join(headers, " ") + `",signature="` + base64.StdEncoding.EncodeToString(sig) + `"`
	req.Header.Set("Signature", h)
}

// rfc9421With signs a client request with any Signature-Input member, as another server might.
func rfc9421With(t testing.TB, req *http.Request, key *rsa.PrivateKey, input string) {
	t.Helper()
	ins, err := parseSignatureInput(input)
	if err != nil {
		t.Fatalf("Signature-Input %s: %v", input, err)
	}
	base, err := rfc9421Base(&ins[0], msgOf(req))
	if err != nil {
		t.Fatalf("base for %s: %v", input, err)
	}
	sig, err := rsaSign(key, base)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Signature-Input", input)
	req.Header.Set("Signature", ins[0].label+"=:"+base64.StdEncoding.EncodeToString(sig)+":")
}

// outcome says what a verification came to, for a failure message.
func outcome(err error) string {
	if err == nil {
		return "accepted"
	}
	return "refused (" + err.Error() + ")"
}

func sigReasonError(r sigReason) error {
	if r == 0 {
		return nil
	}
	return sigFail(r)
}

func reasonOf(err error) sigReason {
	var se *sigError
	if errors.As(err, &se) {
		return se.Reason
	}
	return 0
}

/* ---------- the published vectors ---------- */

// readVector reads a file of testdata/federation/vectors: comment lines first, then sections, each
// a line "=== name" and the lines up to the next, with the last newline dropped.
func readVector(t testing.TB, name string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile("testdata/federation/vectors/" + name)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	cur := ""
	var lines []string
	flush := func() {
		if cur != "" {
			out[cur] = strings.Join(lines, "\n")
		}
	}
	for _, l := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		if name, ok := strings.CutPrefix(l, "=== "); ok {
			flush()
			cur, lines = name, nil
			continue
		}
		if cur == "" {
			if !strings.HasPrefix(l, "#") {
				t.Fatalf("%s: a line before the first section that is not a comment: %q", name, l)
			}
			continue
		}
		lines = append(lines, l)
	}
	flush()
	return out
}

// vectorRequest parses a request written with LF line ends and a blank line before the body.
func vectorRequest(t testing.TB, raw string) (*http.Request, []byte) {
	t.Helper()
	head, body, ok := strings.Cut(raw, "\n\n")
	if !ok {
		t.Fatal("a vector request with no blank line")
	}
	r, err := http.ReadRequest(bufio.NewReader(strings.NewReader(strings.ReplaceAll(head, "\n", "\r\n") + "\r\n\r\n" + body)))
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return r, b
}

// vectorMessage is what a server with the given authority covers for a request read from a vector.
func vectorMessage(r *http.Request, authority string) *sigMessage {
	return &sigMessage{method: r.Method, scheme: "https", authority: authority, target: r.RequestURI, header: r.Header}
}

func pemPrivate(t testing.TB, s string) *rsa.PrivateKey {
	t.Helper()
	b, _ := pem.Decode([]byte(s))
	if b == nil {
		t.Fatal("no PEM block")
	}
	k, err := x509.ParsePKCS1PrivateKey(b.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func verifies(pub *rsa.PublicKey, s string, sig []byte) bool {
	h := sha256.Sum256([]byte(s))
	return rsa.VerifyPKCS1v15(pub, crypto.SHA256, h[:], sig) == nil
}

func TestSignatureVectors(t *testing.T) {
	ck := readVector(t, "cavage12-test-key.txt")
	cpub, err := parsePublicKeyPEM(ck["public key"], 1024)
	if err != nil {
		t.Fatal(err)
	}
	if cpub.N.BitLen() != 1024 {
		t.Errorf("the cavage test key has %d bits; the appendix says 2048, and it was 1024 when read", cpub.N.BitLen())
	}
	if _, err := parsePublicKeyPEM(ck["public key"], sigKeyMin); err == nil {
		t.Error("the 1024-bit cavage key passed the production minimum")
	}
	cpriv := pemPrivate(t, ck["private key"])
	if !cpriv.PublicKey.Equal(cpub) {
		t.Fatal("the cavage private key is not the public key's")
	}

	t.Run("cavage C.1 and C.2", func(t *testing.T) {
		for _, c := range []struct {
			file    string
			headers []string // as the vector reads them: C.1 signs date alone
		}{
			{"cavage12-c1-default.txt", []string{"date"}},
			{"cavage12-c2-basic.txt", []string{"(request-target)", "host", "date"}},
		} {
			v := readVector(t, c.file)
			r, _ := vectorRequest(t, v["request"])
			s, err := parseCavage(v["signature header"])
			if err != nil {
				t.Fatalf("%s: %v", c.file, err)
			}
			if c.file == "cavage12-c1-default.txt" && (len(s.headers) != 1 || s.headers[0] != "(created)") {
				t.Errorf("C.1 without headers read as %v, want the (created) of 2.1.6", s.headers)
			}
			s.headers = c.headers
			got, err := cavageString(s, vectorMessage(r, "example.com"))
			if err != nil || got != v["signing string"] {
				t.Fatalf("%s: signing string %q, %v; want %q", c.file, got, err, v["signing string"])
			}
			sig, ok := b64Decode(s.signature)
			if !ok || !verifies(cpub, got, sig) {
				t.Fatalf("%s: the published signature does not verify", c.file)
			}
			mine, err := rsaSign(cpriv, got)
			if err != nil || base64.StdEncoding.EncodeToString(mine) != s.signature {
				t.Errorf("%s: signing again gave %s, want %s", c.file, base64.StdEncoding.EncodeToString(mine), s.signature)
			}
		}
	})

	t.Run("cavage C.3 is a documented negative", func(t *testing.T) {
		v := readVector(t, "cavage12-c3-all-headers.txt")
		r, _ := vectorRequest(t, v["request"])
		s, err := parseCavage(v["signature header"])
		if err != nil {
			t.Fatal(err)
		}
		sig, _ := b64Decode(s.signature)
		if !verifies(cpub, v["printed signing string"], sig) {
			t.Error("C.3's signature should verify against the string it prints")
		}
		m := vectorMessage(r, "example.com")
		if _, err := cavageString(s, m); reasonOf(err) != sigMalformed {
			t.Errorf("(created) with rsa-sha256 (2.3, rule 2): %v", err)
		}
		s.algorithm = "" // the string its headers list gives when the algorithm allows (created)
		fromList, err := cavageString(s, m)
		if err != nil || verifies(cpub, fromList, sig) {
			t.Errorf("C.3's signature verified against the string from its own headers list (%v)", err)
		}
	})

	t.Run("RFC 9421 4.3 proxy_sig", func(t *testing.T) {
		keys := readVector(t, "rfc9421-b1-keys.txt")
		pub, err := parsePublicKeyPEM(keys["test-key-rsa public key"], sigKeyMin)
		if err != nil {
			t.Fatal(err)
		}
		priv := pemPrivate(t, keys["test-key-rsa private key"])
		if !priv.PublicKey.Equal(pub) || pub.N.BitLen() != 2048 {
			t.Fatal("test-key-rsa's halves do not match, or it is not 2048-bit")
		}
		v := readVector(t, "rfc9421-4.3-proxy-sig.txt")
		r, _ := vectorRequest(t, v["request"])
		base, sig := vectorBase(t, r, v["label"], "origin.host.internal.example")
		if base != v["signature base"] {
			t.Fatalf("base:\n%s\nwant:\n%s", base, v["signature base"])
		}
		if base64.StdEncoding.EncodeToString(sig) != v["signature"] {
			t.Fatal("the Signature field's proxy_sig is not the printed signature")
		}
		if !verifies(pub, base, sig) {
			t.Fatal("the published proxy_sig does not verify with test-key-rsa")
		}
		mine, err := rsaSign(priv, base)
		if err != nil || !bytes.Equal(mine, sig) {
			t.Error("signing the base again did not reproduce proxy_sig")
		}
	})

	t.Run("RFC 9421 bases of 2.5 and B.2.1 to B.2.3", func(t *testing.T) {
		keys := readVector(t, "rfc9421-b1-keys.txt")
		pss, err := parsePublicKeyPEM(keys["test-key-rsa-pss public key"], sigKeyMin)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"rfc9421-2.5-base.txt", "rfc9421-b2.1-minimal.txt", "rfc9421-b2.2-selective.txt", "rfc9421-b2.3-full.txt"} {
			v := readVector(t, f)
			r, _ := vectorRequest(t, v["request"])
			base, sig := vectorBase(t, r, v["label"], "example.com")
			if base != v["signature base"] {
				t.Errorf("%s: base:\n%s\nwant:\n%s", f, base, v["signature base"])
				continue
			}
			// Only to show the copy is exact: the RFC's own RSA-PSS signature over the printed base
			// (3.3.1: SHA-512, MGF1 with SHA-512, a 64-byte salt). Flowa's verifier never does this.
			h := sha512.Sum512([]byte(base))
			if err := rsa.VerifyPSS(pss, crypto.SHA512, h[:], sig, &rsa.PSSOptions{SaltLength: 64, Hash: crypto.SHA512}); err != nil {
				t.Errorf("%s: the RFC's RSA-PSS signature does not verify over the copied base: %v", f, err)
			}
		}
	})

	t.Run("digests of RFC 9530, RFC 9421 and the cavage draft", func(t *testing.T) {
		v := readVector(t, "digests.txt")
		withLF, without := []byte("{\"hello\": \"world\"}\n"), []byte(`{"hello": "world"}`)
		if got := contentDigestHeader(withLF); got != v["RFC 9530 B.1 Content-Digest, with LF"] {
			t.Errorf("Content-Digest of the 19 bytes: %s", got)
		}
		if got := digestHeader(without); got != v["draft-cavage-12 Appendix C Digest, without LF"] {
			t.Errorf("Digest of the 18 bytes: %s", got)
		}
		for name, body := range map[string][]byte{
			"RFC 9530 B.1 Content-Digest, with LF": withLF, "RFC 9530 2 Content-Digest, with LF": withLF,
			"RFC 9421 B.2 Content-Digest, without LF": without,
		} {
			s256, s512, err := parseContentDigest(v[name])
			if err != nil || digestMatches(body, s256, s512) != 0 {
				t.Errorf("%s: %v", name, err)
			}
		}
		s256, s512, err := parseDigest(v["draft-cavage-12 Appendix C Digest, without LF"])
		if err != nil || s512 != nil || digestMatches(without, s256, nil) != 0 {
			t.Errorf("the cavage Digest: %v", err)
		}
	})
}

// vectorBase builds the base of the labelled signature of a vector request, and returns it with the
// signature bytes from its Signature field.
func vectorBase(t testing.TB, r *http.Request, label, authority string) (string, []byte) {
	t.Helper()
	ins, err := parseSignatureInput(strings.Join(r.Header.Values("Signature-Input"), ", "))
	if err != nil {
		t.Fatal(err)
	}
	labels, sigs, err := parseSignatureField(strings.Join(r.Header.Values("Signature"), ", "))
	if err != nil {
		t.Fatal(err)
	}
	for i, in := range ins {
		if in.label != label {
			continue
		}
		base, err := rfc9421Base(&ins[i], vectorMessage(r, authority))
		if err != nil {
			t.Fatal(err)
		}
		for j, l := range labels {
			if l == label {
				return base, sigs[j]
			}
		}
	}
	t.Fatalf("no signature labelled %s", label)
	return "", nil
}

/* ---------- what Flowa sends ---------- */

// The two forms exactly as federation.md 7.1 shows them, for its example delivery.
func TestSignatureSigner(t *testing.T) {
	k := newSigKit(t)
	body := []byte(`{"hello": "world"}`)
	const key = "https://flowa.example.org/users/ana#main-key"
	signWith := func(form sigForm, method string, b []byte) *http.Request {
		req := k.request(t, method, "https://social.example/users/bo/inbox", b)
		if err := httpSigner(k.key, key, form, func() time.Time { return k.now }, time.Time{})(req, b); err != nil {
			t.Fatal(err)
		}
		return req
	}

	req := signWith(sigCavage, "POST", body)
	s, err := parseCavage(req.Header.Get("Signature"))
	if err != nil || s.keyID != key || s.algorithm != "rsa-sha256" || strings.Join(s.headers, " ") != "(request-target) host date digest" {
		t.Fatalf("cavage POST header %q: %v", req.Header.Get("Signature"), err)
	}
	str, _ := cavageString(s, msgOf(req))
	want := "(request-target): post /users/bo/inbox\nhost: social.example\ndate: Thu, 12 Nov 2026 18:04:06 GMT\ndigest: SHA-256=X48E9qOokqqrvdts8nOJRJN3OWDUoyWxBf7kbu9DBPE="
	if str != want || req.Header.Get("Digest") != "SHA-256=X48E9qOokqqrvdts8nOJRJN3OWDUoyWxBf7kbu9DBPE=" || req.Host != "social.example" {
		t.Errorf("cavage POST signs %q with Digest %q", str, req.Header.Get("Digest"))
	}
	if sig, _ := b64Decode(s.signature); !verifies(&k.key.PublicKey, str, sig) {
		t.Error("cavage POST: the signature does not verify")
	}

	req = signWith(sigCavage, "GET", nil)
	if s, err = parseCavage(req.Header.Get("Signature")); err != nil || strings.Join(s.headers, " ") != "(request-target) host date" || req.Header.Get("Digest") != "" {
		t.Errorf("cavage GET: %q, Digest %q", req.Header.Get("Signature"), req.Header.Get("Digest"))
	}

	req = signWith(sigRFC9421, "POST", body)
	if got := req.Header.Get("Signature-Input"); got != `sig1=("@method" "@target-uri" "content-digest");created=1794506646;keyid="`+key+`"` {
		t.Errorf("Signature-Input %s", got)
	}
	if got := req.Header.Get("Content-Digest"); got != "sha-256=:X48E9qOokqqrvdts8nOJRJN3OWDUoyWxBf7kbu9DBPE=:" {
		t.Errorf("Content-Digest %s", got)
	}
	ins, _ := parseSignatureInput(req.Header.Get("Signature-Input"))
	base, _ := rfc9421Base(&ins[0], msgOf(req))
	wantBase := "\"@method\": POST\n\"@target-uri\": https://social.example/users/bo/inbox\n\"content-digest\": sha-256=:X48E9qOokqqrvdts8nOJRJN3OWDUoyWxBf7kbu9DBPE=:\n" +
		`"@signature-params": ("@method" "@target-uri" "content-digest");created=1794506646;keyid="` + key + `"`
	if base != wantBase {
		t.Errorf("RFC 9421 base:\n%s\nwant:\n%s", base, wantBase)
	}
	_, sigs, err := parseSignatureField(req.Header.Get("Signature"))
	if err != nil || !verifies(&k.key.PublicKey, base, sigs[0]) {
		t.Errorf("RFC 9421 POST: the signature does not verify (%v)", err)
	}

	// A host marked for RFC 9421 still gets its GETs in cavage (Mastodon's list wants a digest).
	req = signWith(sigRFC9421, "GET", nil)
	if req.Header.Get("Signature-Input") != "" || req.Header.Get("Content-Digest") != "" || !strings.Contains(req.Header.Get("Signature"), `headers="(request-target) host date"`) {
		t.Errorf("an RFC 9421 GET was signed as %v", req.Header)
	}

	// What the signer refuses.
	plain := k.request(t, "GET", "http://social.example/x", nil)
	if err := httpSigner(k.key, key, sigCavage, func() time.Time { return k.now }, time.Time{})(plain, nil); !errors.Is(err, errSignURL) {
		t.Errorf("an http URL: %v", err)
	}
	if err := httpSigner(nil, key, sigCavage, time.Now, time.Time{})(k.request(t, "GET", "https://social.example/x", nil), nil); !errors.Is(err, errSignKey) {
		t.Errorf("no key: %v", err)
	}
	if err := httpSigner(k.key, "Test", sigCavage, time.Now, time.Time{})(k.request(t, "GET", "https://social.example/x", nil), nil); !errors.Is(err, errSignKey) {
		t.Errorf("a key id that is not a URL: %v", err)
	}
}

// What (request-target), host and @target-uri are for the URLs a signer meets, each from the texts.
func TestSignatureRequestTarget(t *testing.T) {
	k := newSigKit(t)
	body := []byte(`{}`)
	for _, c := range []struct{ why, url, host, target, uri string }{
		{"a plain path", "https://social.example/users/bo/inbox", "social.example", "/users/bo/inbox", "https://social.example/users/bo/inbox"},
		{"a query, as cavage C.2 signs it", "https://example.com/foo?param=value&pet=dog", "example.com", "/foo?param=value&pet=dog", "https://example.com/foo?param=value&pet=dog"},
		{"percent-encoding kept as written (RFC 9421 2.2.6, 2.2.7)", "https://social.example/a%2Fb/c%20d?baz=bat%2Dman", "social.example", "/a%2Fb/c%20d?baz=bat%2Dman", "https://social.example/a%2Fb/c%20d?baz=bat%2Dman"},
		{"an empty path is / (RFC 9110 4.2.3, RFC 9421 2.2.6)", "https://social.example", "social.example", "/", "https://social.example/"},
		{"capitals in the path kept (cavage 2.3: lower case only for the method)", "https://social.example/Users/Bo", "social.example", "/Users/Bo", "https://social.example/Users/Bo"},
		{"the host in lower case, the default port left out (RFC 9421 2.2.3)", "https://Social.Example:443/x", "social.example", "/x", "https://social.example/x"},
		{"another port kept", "https://social.example:8443/x", "social.example:8443", "/x", "https://social.example:8443/x"},
		{"an empty query keeps its ?", "https://social.example/x?", "social.example", "/x?", "https://social.example/x?"},
	} {
		req := k.sign(t, sigCavage, "GET", c.url, nil)
		s, _ := parseCavage(req.Header.Get("Signature"))
		str, _ := cavageString(s, msgOf(req))
		if want := "(request-target): get " + c.target + "\nhost: " + c.host + "\ndate: "; req.Host != c.host || !strings.HasPrefix(str, want) {
			t.Errorf("%s: Host %q, cavage string %q; want it to start %q", c.why, req.Host, str, want)
		}
		req = k.sign(t, sigRFC9421, "POST", c.url, body)
		ins, _ := parseSignatureInput(req.Header.Get("Signature-Input"))
		base, _ := rfc9421Base(&ins[0], msgOf(req))
		if want := "\"@method\": POST\n\"@target-uri\": " + c.uri + "\n"; !strings.HasPrefix(base, want) {
			t.Errorf("%s: base %q; want it to start %q", c.why, base, want)
		}
		// And what a server at that origin builds from the request as it arrives is the same.
		v, _ := newSigVerifier("https://"+c.host, k.v.keyFor)
		v.now, v.built = k.v.now, time.Time{}
		if _, err := v.verifyRequest(context.Background(), received(req, body), body); err != nil {
			t.Errorf("%s: the server refused it: %v", c.why, err)
		}
	}
}

// Derived components and field values against the examples of RFC 9421 2.1 and 2.2 and the signing
// string of draft-cavage-12 2.3.
func TestSignatureComponents(t *testing.T) {
	m := func(method, target string) *sigMessage {
		return &sigMessage{method: method, scheme: "https", authority: "www.example.com", target: target, header: http.Header{}}
	}
	derive := func(msg *sigMessage, comp string) (string, error) {
		ins, err := parseSignatureInput("s=(" + comp + ")")
		if err != nil {
			t.Fatalf("%s: %v", comp, err)
		}
		return derivedValue(ins[0].covered[0], msg)
	}
	for _, c := range []struct {
		msg        *sigMessage
		comp, want string
	}{
		{m("POST", "/path?param=value"), `"@method"`, "POST"},                                                                         // 2.2.1
		{m("POST", "/path?param=value"), `"@target-uri"`, "https://www.example.com/path?param=value"},                                 // 2.2.2
		{m("POST", "/path?param=value"), `"@authority"`, "www.example.com"},                                                           // 2.2.3
		{&sigMessage{method: "POST", scheme: "http", authority: "www.example.com", target: "/path?param=value"}, `"@scheme"`, "http"}, // 2.2.4
		{m("POST", "/path?param=value"), `"@request-target"`, "/path?param=value"},                                                    // 2.2.5
		{m("GET", "/path?param=value"), `"@path"`, "/path"},                                                                           // 2.2.6
		{m("GET", "/path?param=value&foo=bar&baz=bat%2Dman"), `"@query"`, "?param=value&foo=bar&baz=bat%2Dman"},                       // 2.2.7
		{m("POST", "/path?queryString"), `"@query"`, "?queryString"},
		{m("GET", "/path"), `"@query"`, "?"},
		{m("GET", "/path?param=value&foo=bar&baz=batman&qux="), `"@query-param";name="baz"`, "batman"}, // 2.2.8
		{m("GET", "/path?param=value&foo=bar&baz=batman&qux="), `"@query-param";name="qux"`, ""},
		{m("GET", "/path?param=value&foo=bar&baz=batman&qux="), `"@query-param";name="param"`, "value"},
		{m("GET", "/parameters?var=this%20is%20a%20big%0Amultiline%20value&bar=with+plus+whitespace&fa%C3%A7ade%22%3A%20=something"), `"@query-param";name="var"`, "this%20is%20a%20big%0Amultiline%20value"},
		{m("GET", "/parameters?var=this%20is%20a%20big%0Amultiline%20value&bar=with+plus+whitespace&fa%C3%A7ade%22%3A%20=something"), `"@query-param";name="bar"`, "with%20plus%20whitespace"},
		{m("GET", "/parameters?var=this%20is%20a%20big%0Amultiline%20value&bar=with+plus+whitespace&fa%C3%A7ade%22%3A%20=something"), `"@query-param";name="fa%C3%A7ade%22%3A%20"`, "something"},
	} {
		if got, err := derive(c.msg, c.comp); err != nil || got != c.want {
			t.Errorf("%s of %s: %q, %v; want %q", c.comp, c.msg.target, got, err, c.want)
		}
	}
	for comp, want := range map[string]sigReason{
		`"@status"`: sigComponentUnknown, `"@signature-params"`: sigComponentUnknown, `"@foo"`: sigComponentUnknown,
		`"@method";req`: sigComponentUnknown, `"@query-param";name="nope"`: sigHeaderMissing, `"@query-param";name="a"`: sigMalformed,
		`"@query-param"`: sigComponentUnknown,
	} {
		if _, err := derive(m("GET", "/p?a=1&a=2"), comp); reasonOf(err) != want {
			t.Errorf("%s: %v, want %v", comp, err, want)
		}
	}

	// RFC 9421 2.1: field values, through Go's HTTP/1.1 reader as a request arrives (it replaces the
	// obsolete line folding with a space, as RFC 9112 5.2 allows).
	raw := "GET /x HTTP/1.1\r\nHost: www.example.com\r\nDate: Tue, 20 Apr 2021 02:07:56 GMT\r\nX-OWS-Header:   Leading and trailing whitespace.   \r\n" +
		"X-Obs-Fold-Header: Obsolete\r\n    line folding.\r\nCache-Control: max-age=60\r\nCache-Control:    must-revalidate\r\nExample-Dict:  a=1,    b=2;x=1;y=2,   c=(a   b   c)\r\nX-Empty-Header: \r\n\r\n"
	r, err := http.ReadRequest(bufio.NewReader(strings.NewReader(raw)))
	if err != nil {
		t.Fatal(err)
	}
	msg := &sigMessage{method: "GET", scheme: "https", authority: "www.example.com", target: "/x", header: r.Header}
	for name, want := range map[string]string{
		"host": "www.example.com", "date": "Tue, 20 Apr 2021 02:07:56 GMT", "x-ows-header": "Leading and trailing whitespace.",
		"x-obs-fold-header": "Obsolete line folding.", "cache-control": "max-age=60, must-revalidate",
		"example-dict": "a=1,    b=2;x=1;y=2,   c=(a   b   c)", "x-empty-header": "",
	} {
		if got, err := fieldValue(msg, name, true); err != nil || got != want {
			t.Errorf("%s: %q, %v; want %q", name, got, err, want)
		}
	}
	// The same values set directly, as a test or a caller might: spaces trimmed, folding refused.
	h := http.Header{}
	h.Set("X-Ows-Header", "  Leading and trailing whitespace.  ")
	h.Set("X-Obs-Fold-Header", "Obsolete\r\n line folding.")
	h.Set("X-Nul", "a\x00b")
	direct := &sigMessage{header: h}
	if got, _ := fieldValue(direct, "x-ows-header", true); got != "Leading and trailing whitespace." {
		t.Errorf("trailing spaces: %q", got)
	}
	for _, n := range []string{"x-obs-fold-header", "x-nul"} {
		for _, ascii := range []bool{true, false} {
			if _, err := fieldValue(direct, n, ascii); reasonOf(err) != sigMalformed {
				t.Errorf("%s (ascii %v): %v", n, ascii, err)
			}
		}
	}

	// draft-cavage-12 2.3: the signing string of its example, through Go's reader the same way.
	raw = "GET /foo HTTP/1.1\r\nHost: example.org\r\nDate: Tue, 07 Jun 2014 20:51:35 GMT\r\nX-Example: Example header\r\n    with some whitespace.\r\n" +
		"X-EmptyHeader:\r\nCache-Control: max-age=60\r\nCache-Control: must-revalidate\r\n\r\n"
	if r, err = http.ReadRequest(bufio.NewReader(strings.NewReader(raw))); err != nil {
		t.Fatal(err)
	}
	s := &cavageSig{algorithm: "hs2019", created: "1402170695", headers: strings.Fields("(request-target) (created) host date cache-control x-emptyheader x-example")}
	got, err := cavageString(s, &sigMessage{method: "GET", scheme: "https", authority: "example.org", target: r.RequestURI, header: r.Header})
	want := "(request-target): get /foo\n(created): 1402170695\nhost: example.org\ndate: Tue, 07 Jun 2014 20:51:35 GMT\ncache-control: max-age=60, must-revalidate\nx-emptyheader: \nx-example: Example header with some whitespace."
	if err != nil || got != want {
		t.Errorf("cavage 2.3:\n%q, %v\nwant:\n%q", got, err, want)
	}
}

/* ---------- the policy ---------- */

// sigCase is one request for the policy table: made by mk, refused for want (0: accepted).
type sigCase struct {
	name string
	want sigReason
	mk   func(t *testing.T, k *sigKit) (*http.Request, []byte)
}

var inboxBody = []byte(`{"type":"Create","actor":"https://remote.example/users/ana"}`)

// flowaPost is a delivery to the inbox signed by Flowa's signer, as it arrives.
func flowaPost(form sigForm) func(*testing.T, *sigKit) (*http.Request, []byte) {
	return func(t *testing.T, k *sigKit) (*http.Request, []byte) {
		return received(k.sign(t, form, "POST", tOrigin+"/inbox", inboxBody), inboxBody), inboxBody
	}
}

// handPost is a delivery to the inbox prepared by prep and signed by sign, as another server might.
func handPost(prep func(*http.Request), sign func(*testing.T, *sigKit, *http.Request)) func(*testing.T, *sigKit) (*http.Request, []byte) {
	return func(t *testing.T, k *sigKit) (*http.Request, []byte) {
		req := k.request(t, "POST", tOrigin+"/inbox?page=1", inboxBody)
		req.Header.Set("Digest", digestHeader(inboxBody))
		req.Header.Set("Content-Digest", contentDigestHeader(inboxBody))
		if prep != nil {
			prep(req)
		}
		sign(t, k, req)
		return received(req, inboxBody), inboxBody
	}
}

func cav(alg string, headers, extra string) func(*testing.T, *sigKit, *http.Request) {
	return func(t *testing.T, k *sigKit, req *http.Request) {
		cavageWith(t, req, k.key, tKeyID, alg, strings.Fields(headers), extra)
	}
}

func rfc(input string) func(*testing.T, *sigKit, *http.Request) {
	return func(t *testing.T, k *sigKit, req *http.Request) {
		rfc9421With(t, req, k.key, strings.ReplaceAll(input, "KEY", `"`+tKeyID+`"`))
	}
}

// after changes a request once it is signed and has arrived.
func after(mk func(*testing.T, *sigKit) (*http.Request, []byte), change func(r *http.Request, body []byte) []byte) func(*testing.T, *sigKit) (*http.Request, []byte) {
	return func(t *testing.T, k *sigKit) (*http.Request, []byte) {
		r, b := mk(t, k)
		return r, change(r, b)
	}
}

const created = "created=1794506646" // t0

func TestSignaturePolicy(t *testing.T) {
	full := "(request-target) host date digest"
	cases := []sigCase{
		{"Flowa's cavage delivery", 0, flowaPost(sigCavage)},
		{"Flowa's RFC 9421 delivery", 0, flowaPost(sigRFC9421)},
		{"cavage hs2019", 0, handPost(nil, cav("hs2019", full, ""))},
		{"cavage without algorithm, as Mastodon's example", 0, handPost(nil, cav("", full, ""))},
		{"cavage (created) with hs2019 and no date", 0, handPost(nil, cav("hs2019", "(request-target) host (created) digest", created))},
		{"cavage header names in capitals", 0, after(handPost(nil, cav("rsa-sha256", full, "")), func(r *http.Request, b []byte) []byte {
			r.Header.Set("Signature", strings.Replace(r.Header.Get("Signature"), full, "(Request-Target) Host Date Digest", 1))
			return b
		})},
		{"cavage covering more headers", 0, handPost(func(r *http.Request) { r.Header.Set("Content-Type", asType) }, cav("rsa-sha256", full+" content-type", ""))},
		{"RFC 9421 with alg", 0, handPost(nil, rfc(`sig1=("@method" "@target-uri" "content-digest");`+created+`;keyid=KEY;alg="rsa-v1_5-sha256"`))},
		{"RFC 9421 as @authority, @path and @query", 0, handPost(nil, rfc(`sig1=("@method" "@authority" "@path" "@query" "content-digest");`+created+`;keyid=KEY`))},
		// The base written out by hand, so the signer does not share the code under test (RFC 9421 2.3,
		// 4.1: the parameters' order as received, which is not the order Flowa writes its own in).
		{"RFC 9421 parameters in another order", 0, handPost(nil, func(t *testing.T, k *sigKit, req *http.Request) {
			params := `;keyid="` + tKeyID + `";nonce="x";` + created
			base := "\"@method\": POST\n\"@target-uri\": " + tOrigin + "/inbox?page=1\n\"content-digest\": " + contentDigestHeader(inboxBody) +
				"\n\"@signature-params\": (\"@method\" \"@target-uri\" \"content-digest\")" + params
			sig, _ := rsaSign(k.key, base)
			req.Header.Set("Signature-Input", `sig1=("@method" "@target-uri" "content-digest")`+params)
			req.Header.Set("Signature", "sig1=:"+base64.StdEncoding.EncodeToString(sig)+":")
		})},
		{"RFC 9421 with date beside created", 0, handPost(nil, rfc(`sig1=("date" "@method" "@target-uri" "content-digest");`+created+`;keyid=KEY`))},
		{"RFC 9421 with sha-512 beside sha-256", 0, handPost(func(r *http.Request) {
			h := sha512.Sum512(inboxBody)
			r.Header.Set("Content-Digest", contentDigestHeader(inboxBody)+", sha-512=:"+base64.StdEncoding.EncodeToString(h[:])+":, md5=:AAAA:")
		}, rfc(`sig1=("@method" "@target-uri" "content-digest");`+created+`;keyid=KEY`))},
		{"Digest with another algorithm beside, names in lower case", 0, handPost(func(r *http.Request) {
			r.Header.Set("Digest", "md5=HUXZLQLMuI/KZ5KDcJPcOA==, sha-256="+strings.TrimPrefix(digestHeader(inboxBody), "SHA-256="))
		}, cav("rsa-sha256", full, ""))},
		{"a Host header that lies", 0, after(flowaPost(sigCavage), func(r *http.Request, b []byte) []byte { r.Host = "evil.example"; return b })},

		{"no signature", sigMissing, after(flowaPost(sigCavage), func(r *http.Request, b []byte) []byte { r.Header.Del("Signature"); return b })},
		{"Signature-Input with no Signature", sigMalformed, after(flowaPost(sigRFC9421), func(r *http.Request, b []byte) []byte { r.Header.Del("Signature"); return b })},
		{"a garbled Signature", sigMalformed, after(flowaPost(sigCavage), func(r *http.Request, b []byte) []byte { r.Header.Set("Signature", `keyId="x",signature=`); return b })},
		{"two cavage Signature lines", sigMultiple, after(flowaPost(sigCavage), func(r *http.Request, b []byte) []byte {
			r.Header.Add("Signature", r.Header.Get("Signature"))
			return b
		})},
		{"cavage without (request-target)", sigNoRequestLine, handPost(nil, cav("rsa-sha256", "host date digest", ""))},
		{"cavage without a headers parameter (2.1.6: (created) alone)", sigNoRequestLine, after(flowaPost(sigCavage), func(r *http.Request, b []byte) []byte {
			r.Header.Set("Signature", strings.Replace(r.Header.Get("Signature"), `headers="(request-target) host date digest",`, "", 1))
			return b
		})},
		{"cavage without host", sigNoHost, handPost(nil, cav("rsa-sha256", "(request-target) date digest", ""))},
		{"cavage without date or (created)", sigNoTime, handPost(nil, cav("rsa-sha256", "(request-target) host digest", ""))},
		{"cavage POST without digest", sigNoDigest, handPost(nil, cav("rsa-sha256", "(request-target) host date", ""))},
		{"cavage digest covered, header gone", sigDigestMissing, after(flowaPost(sigCavage), func(r *http.Request, b []byte) []byte { r.Header.Del("Digest"); return b })},
		{"cavage body changed", sigDigestMismatch, after(flowaPost(sigCavage), func(r *http.Request, b []byte) []byte { return []byte(`{"type":"Delete"}`) })},
		{"cavage Digest of an algorithm not checked", sigDigestUnknown, handPost(func(r *http.Request) { r.Header.Set("Digest", "MD5=HUXZLQLMuI/KZ5KDcJPcOA==") }, cav("rsa-sha256", full, ""))},
		{"cavage algorithm ecdsa-sha256 on an RSA key", sigAlgorithm, handPost(nil, cav("ecdsa-sha256", full, ""))},
		{"cavage algorithm rsa-sha512", sigAlgorithm, handPost(nil, cav("rsa-sha512", full, ""))},
		{"cavage algorithm hmac-sha256", sigAlgorithm, handPost(nil, cav("hmac-sha256", full, ""))},
		{"cavage key id over http", sigKeyID, after(flowaPost(sigCavage), func(r *http.Request, b []byte) []byte {
			r.Header.Set("Signature", strings.Replace(r.Header.Get("Signature"), "https://", "http://", 1))
			return b
		})},
		{"cavage (created) with rsa-sha256 (2.3, rule 2)", sigMalformed, handPost(nil, func(t *testing.T, k *sigKit, req *http.Request) {
			cavageWith(t, req, k.key, tKeyID, "hs2019", strings.Fields("(request-target) host (created) digest"), created)
			req.Header.Set("Signature", strings.Replace(req.Header.Get("Signature"), "hs2019", "rsa-sha256", 1))
		})},
		{"cavage created in the future (2.1.4)", sigFuture, handPost(nil, cav("hs2019", "(request-target) host (created) digest", "created=1794506647"))},
		{"cavage expires in the past (2.1.5)", sigExpired, handPost(nil, cav("hs2019", full, created+",expires=1794506645.5"))},
		{"cavage a header named twice", sigComponentTwice, handPost(nil, func(t *testing.T, k *sigKit, req *http.Request) {
			cavageWith(t, req, k.key, tKeyID, "rsa-sha256", strings.Fields(full), "")
			req.Header.Set("Signature", strings.Replace(req.Header.Get("Signature"), full, full+" date", 1))
		})},
		{"cavage an unknown (name)", sigComponentUnknown, handPost(nil, func(t *testing.T, k *sigKit, req *http.Request) {
			cavageWith(t, req, k.key, tKeyID, "rsa-sha256", strings.Fields(full), "")
			req.Header.Set("Signature", strings.Replace(req.Header.Get("Signature"), full, full+" (nonce)", 1))
		})},
		{"a covered header missing", sigHeaderMissing, after(handPost(func(r *http.Request) { r.Header.Set("X-Extra", "1") }, cav("rsa-sha256", full+" x-extra", "")), func(r *http.Request, b []byte) []byte {
			r.Header.Del("X-Extra")
			return b
		})},
		{"a covered Date sent twice", sigHeaderTwice, after(flowaPost(sigCavage), func(r *http.Request, b []byte) []byte { r.Header.Add("Date", r.Header.Get("Date")); return b })},
		{"a signature that is not base64", sigBadSignature, after(flowaPost(sigCavage), func(r *http.Request, b []byte) []byte {
			r.Header.Set("Signature", r.Header.Get("Signature")[:strings.Index(r.Header.Get("Signature"), `signature="`)]+`signature="not*base64"`)
			return b
		})},
		{"a signature of the wrong length", sigBadSignature, after(flowaPost(sigCavage), func(r *http.Request, b []byte) []byte {
			r.Header.Set("Signature", r.Header.Get("Signature")[:strings.Index(r.Header.Get("Signature"), `signature="`)]+`signature="AAAA"`)
			return b
		})},
		{"signed by another key", sigInvalid, handPost(nil, func(t *testing.T, k *sigKit, req *http.Request) {
			cavageWith(t, req, k.other, tKeyID, "rsa-sha256", strings.Fields(full), "")
		})},
		{"a key that cannot be had", sigKeyUnavailable, handPost(nil, func(t *testing.T, k *sigKit, req *http.Request) {
			cavageWith(t, req, k.key, "https://remote.example/nobody#key", "rsa-sha256", strings.Fields(full), "")
		})},
		{"another method than signed", sigInvalid, after(flowaPost(sigCavage), func(r *http.Request, b []byte) []byte { r.Method = "PUT"; return b })},
		{"another path than signed", sigInvalid, after(flowaPost(sigCavage), func(r *http.Request, b []byte) []byte { r.RequestURI = "/inbox2"; return b })},
		{"another query than signed", sigInvalid, after(handPost(nil, cav("rsa-sha256", full, "")), func(r *http.Request, b []byte) []byte { r.RequestURI = "/inbox?page=2"; return b })},
		{"the same path percent-encoded otherwise", sigInvalid, after(flowaPost(sigCavage), func(r *http.Request, b []byte) []byte { r.RequestURI = "/in%62ox"; return b })},
		{"a request target in absolute form", sigMalformed, after(flowaPost(sigCavage), func(r *http.Request, b []byte) []byte { r.RequestURI = tOrigin + "/inbox"; return b })},
		{"signed for another origin", sigInvalid, func(t *testing.T, k *sigKit) (*http.Request, []byte) {
			r := received(k.sign(t, sigCavage, "POST", "https://other.example/inbox", inboxBody), inboxBody)
			r.Host = "flowa.example"
			return r, inboxBody
		}},
		{"RFC 9421 for another origin with a matching Host", sigInvalid, func(t *testing.T, k *sigKit) (*http.Request, []byte) {
			return received(k.sign(t, sigRFC9421, "POST", "https://other.example/inbox", inboxBody), inboxBody), inboxBody
		}},

		{"RFC 9421 without created", sigNoTime, handPost(nil, rfc(`sig1=("@method" "@target-uri" "content-digest");keyid=KEY`))},
		{"RFC 9421 alg rsa-pss-sha512", sigAlgorithm, handPost(nil, rfc(`sig1=("@method" "@target-uri" "content-digest");`+created+`;keyid=KEY;alg="rsa-pss-sha512"`))},
		{"RFC 9421 alg ed25519", sigAlgorithm, handPost(nil, rfc(`sig1=("@method" "@target-uri" "content-digest");`+created+`;keyid=KEY;alg="ed25519"`))},
		{"RFC 9421 alg as a token", sigMalformed, after(flowaPost(sigRFC9421), func(r *http.Request, b []byte) []byte {
			r.Header.Set("Signature-Input", r.Header.Get("Signature-Input")+";alg=rsa-v1_5-sha256")
			return b
		})},
		{"RFC 9421 key id that is not a URL", sigKeyID, handPost(nil, func(t *testing.T, k *sigKit, req *http.Request) {
			rfc9421With(t, req, k.key, `sig1=("@method" "@target-uri" "content-digest");`+created+`;keyid="test-key-rsa"`)
		})},
		{"RFC 9421 two signatures", sigMultiple, after(flowaPost(sigRFC9421), func(r *http.Request, b []byte) []byte {
			r.Header.Set("Signature-Input", r.Header.Get("Signature-Input")+", "+strings.Replace(r.Header.Get("Signature-Input"), "sig1=", "sig2=", 1))
			r.Header.Set("Signature", r.Header.Get("Signature")+", "+strings.Replace(r.Header.Get("Signature"), "sig1=", "sig2=", 1))
			return b
		})},
		{"RFC 9421 labels that differ", sigMalformed, after(flowaPost(sigRFC9421), func(r *http.Request, b []byte) []byte {
			r.Header.Set("Signature", strings.Replace(r.Header.Get("Signature"), "sig1=", "sig2=", 1))
			return b
		})},
		{"RFC 9421 without @method", sigNoRequestLine, handPost(nil, rfc(`sig1=("@target-uri" "content-digest");`+created+`;keyid=KEY`))},
		{"RFC 9421 without @target-uri or @path", sigNoRequestLine, handPost(nil, rfc(`sig1=("@method" "@authority" "content-digest");`+created+`;keyid=KEY`))},
		{"RFC 9421 @path without @query on a request with one", sigNoRequestLine, handPost(nil, rfc(`sig1=("@method" "@authority" "@path" "content-digest");`+created+`;keyid=KEY`))},
		{"RFC 9421 @request-target", sigNoRequestLine, handPost(nil, rfc(`sig1=("@method" "@authority" "@request-target" "content-digest");`+created+`;keyid=KEY`))},
		{"RFC 9421 POST without content-digest", sigNoDigest, handPost(nil, rfc(`sig1=("@method" "@target-uri");`+created+`;keyid=KEY`))},
		{"RFC 9421 body changed", sigDigestMismatch, after(flowaPost(sigRFC9421), func(r *http.Request, b []byte) []byte { return append([]byte{}, b[1:]...) })},
		{"RFC 9421 a sha-512 that does not match beside a sha-256 that does", sigDigestMismatch, handPost(func(r *http.Request) {
			r.Header.Set("Content-Digest", contentDigestHeader(inboxBody)+", sha-512=:"+base64.StdEncoding.EncodeToString(make([]byte, 64))+":")
		}, rfc(`sig1=("@method" "@target-uri" "content-digest");`+created+`;keyid=KEY`))},
		{"RFC 9421 Content-Digest of no algorithm checked", sigDigestUnknown, handPost(func(r *http.Request) { r.Header.Set("Content-Digest", "md5=:HUXZLQLMuI/KZ5KDcJPcOA==:") }, rfc(`sig1=("@method" "@target-uri" "content-digest");`+created+`;keyid=KEY`))},
		{"RFC 9421 a component named twice", sigComponentTwice, handPost(nil, func(t *testing.T, k *sigKit, req *http.Request) {
			rfc9421With(t, req, k.key, `sig1=("@method" "@target-uri" "content-digest");`+created+`;keyid="`+tKeyID+`"`)
			req.Header.Set("Signature-Input", strings.Replace(req.Header.Get("Signature-Input"), `"@method"`, `"@method" "@method"`, 1))
		})},
		{"RFC 9421 an unknown derived component", sigComponentUnknown, handPost(nil, func(t *testing.T, k *sigKit, req *http.Request) {
			rfc9421With(t, req, k.key, `sig1=("@method" "@target-uri" "content-digest");`+created+`;keyid="`+tKeyID+`"`)
			req.Header.Set("Signature-Input", strings.Replace(req.Header.Get("Signature-Input"), `"@method"`, `"@method" "@nonce"`, 1))
		})},
		{"RFC 9421 a header with a parameter not understood", sigComponentUnknown, handPost(nil, func(t *testing.T, k *sigKit, req *http.Request) {
			rfc9421With(t, req, k.key, `sig1=("@method" "@target-uri" "content-digest");`+created+`;keyid="`+tKeyID+`"`)
			req.Header.Set("Signature-Input", strings.Replace(req.Header.Get("Signature-Input"), `"@method"`, `"@method" "date";sf`, 1))
		})},
		{"RFC 9421 a header name in capitals", sigMalformed, handPost(nil, func(t *testing.T, k *sigKit, req *http.Request) {
			rfc9421With(t, req, k.key, `sig1=("@method" "@target-uri" "content-digest");`+created+`;keyid="`+tKeyID+`"`)
			req.Header.Set("Signature-Input", strings.Replace(req.Header.Get("Signature-Input"), `"@method"`, `"@method" "Date"`, 1))
		})},
		{"RFC 9421 expires in the past", sigExpired, handPost(nil, rfc(`sig1=("@method" "@target-uri" "content-digest");`+created+`;keyid=KEY;expires=1794506645`))},
		{"RFC 9421 created too long ago", sigTooOld, handPost(nil, rfc(`sig1=("@method" "@target-uri" "content-digest");created=1794463445;keyid=KEY`))},
		{"RFC 9421 created too far ahead", sigFuture, handPost(nil, rfc(`sig1=("@method" "@target-uri" "content-digest");created=1794510247;keyid=KEY`))},
		{"RFC 9421 a signed date out of the window beside a good created", sigTooOld, handPost(func(r *http.Request) {
			r.Header.Set("Date", t0.Add(-13*time.Hour).Format(http.TimeFormat))
		}, rfc(`sig1=("date" "@method" "@target-uri" "content-digest");`+created+`;keyid=KEY`))},

		{"both forms with the same key", 0, handPost(nil, func(t *testing.T, k *sigKit, req *http.Request) {
			cavageWith(t, req, k.key, tKeyID, "rsa-sha256", strings.Fields(full), "")
			c := req.Header.Get("Signature")
			rfc9421With(t, req, k.key, `sig1=("@method" "@target-uri" "content-digest");`+created+`;keyid="`+tKeyID+`"`)
			req.Header.Add("Signature", c)
		})},
		{"both forms naming different keys", sigBothDisagree, handPost(nil, func(t *testing.T, k *sigKit, req *http.Request) {
			cavageWith(t, req, k.other, tOtherKeyID, "rsa-sha256", strings.Fields(full), "")
			c := req.Header.Get("Signature")
			rfc9421With(t, req, k.key, `sig1=("@method" "@target-uri" "content-digest");`+created+`;keyid="`+tKeyID+`"`)
			req.Header.Add("Signature", c)
		})},
		{"both forms, the cavage one invalid", sigInvalid, handPost(nil, func(t *testing.T, k *sigKit, req *http.Request) {
			cavageWith(t, req, k.other, tKeyID, "rsa-sha256", strings.Fields(full), "")
			c := req.Header.Get("Signature")
			rfc9421With(t, req, k.key, `sig1=("@method" "@target-uri" "content-digest");`+created+`;keyid="`+tKeyID+`"`)
			req.Header.Add("Signature", c)
		})},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			k := newSigKit(t)
			r, b := c.mk(t, k)
			res, err := k.verify(r, b)
			if got := reasonOf(err); got != c.want || (c.want == 0 && err != nil) {
				t.Fatalf("got %s, want %s", outcome(err), outcome(sigReasonError(c.want)))
			}
			if c.want == 0 && res.KeyID != tKeyID {
				t.Errorf("accepted for %q", res.KeyID)
			}
			if c.want == sigKeyUnavailable && !strings.Contains(errors.Unwrap(err).Error(), "no such key") {
				t.Errorf("the key function's error is not wrapped: %v", err)
			}
		})
	}
}

// The time window at its edges, the clock, and the form reported.
func TestSignatureWindow(t *testing.T) {
	k := newSigKit(t)
	for _, c := range []struct {
		age  time.Duration // how long before the check it was signed; negative: in the future
		want sigReason
	}{
		{12 * time.Hour, 0}, {12*time.Hour + time.Second, sigTooOld}, {-time.Hour, 0}, {-time.Hour - time.Second, sigFuture}, {0, 0},
	} {
		for _, form := range []sigForm{sigCavage, sigRFC9421} {
			k.now = t0
			r := received(k.sign(t, form, "POST", tOrigin+"/inbox", inboxBody), inboxBody)
			k.now = t0.Add(c.age)
			k.v.seen = newReplaySet(sigSeenMax, sigKeep)
			if _, err := k.verify(r, inboxBody); reasonOf(err) != c.want {
				t.Errorf("form %d signed %v before: %v, want %v", form, c.age, err, c.want)
			}
		}
	}
	k.now = t0
	k.v.built = t0.Add(time.Second)
	if _, err := k.verify(received(k.sign(t, sigCavage, "POST", tOrigin+"/inbox", inboxBody), inboxBody), inboxBody); reasonOf(err) != sigClock {
		t.Errorf("a clock behind the build: %v", err)
	}
	k.v.built = time.Time{}
	k.v.seen = newReplaySet(sigSeenMax, sigKeep)
	res, err := k.verify(received(k.sign(t, sigRFC9421, "POST", tOrigin+"/inbox", inboxBody), inboxBody), inboxBody)
	if err != nil || res.Form != sigRFC9421 || !res.At.Equal(t0) {
		t.Errorf("RFC 9421: %+v %v", res, err)
	}
}

// Keys of a size the verifier refuses, whatever the signature.
func TestSignatureKeySize(t *testing.T) {
	ck := readVector(t, "cavage12-test-key.txt")
	small := pemPrivate(t, ck["private key"])
	huge := &rsa.PublicKey{N: new(big.Int).Lsh(big.NewInt(1), 8200), E: 65537}
	huge.N.Add(huge.N, big.NewInt(1))
	k := newSigKit(t)
	k.v.keyFor = func(_ context.Context, id string) (*rsa.PublicKey, error) {
		if strings.Contains(id, "small") {
			return &small.PublicKey, nil
		}
		return huge, nil
	}
	for _, id := range []string{"https://remote.example/small#key", "https://remote.example/huge#key"} {
		req := k.request(t, "POST", tOrigin+"/inbox", inboxBody)
		req.Header.Set("Digest", digestHeader(inboxBody))
		cavageWith(t, req, small, id, "rsa-sha256", strings.Fields("(request-target) host date digest"), "")
		if _, err := k.verify(received(req, inboxBody), inboxBody); reasonOf(err) != sigKeyRefused {
			t.Errorf("%s: %v", id, err)
		}
	}
	// The same 1024-bit signature passes a verifier given the lowered floor, as the vector tests use.
	k.v.minBits = 1024
	req := k.request(t, "POST", tOrigin+"/inbox", inboxBody)
	req.Header.Set("Digest", digestHeader(inboxBody))
	cavageWith(t, req, small, "https://remote.example/small#key", "rsa-sha256", strings.Fields("(request-target) host date digest"), "")
	if _, err := k.verify(received(req, inboxBody), inboxBody); err != nil {
		t.Errorf("a 1024-bit key with the floor lowered: %v", err)
	}
}

/* ---------- what must never happen ---------- */

func TestSignatureNever(t *testing.T) {
	for _, form := range []sigForm{sigCavage, sigRFC9421} {
		k := newSigKit(t)
		r, b := flowaPost(form)(t, k)
		if _, err := k.verify(r, b); err != nil {
			t.Fatalf("form %d: %v", form, err)
		}
		// Accepted twice.
		if _, err := k.verify(r, b); reasonOf(err) != sigReplay {
			t.Errorf("form %d: a second use: %v", form, err)
		}
		// The same signature written without its base64 padding is the same signature.
		r2 := r.Clone(context.Background())
		r2.Header = r.Header.Clone()
		h := r.Header.Get("Signature")
		unpadded := strings.TrimRight(h[:len(h)-1], "=") + h[len(h)-1:]
		if unpadded == h {
			t.Fatalf("form %d: the signature has no padding to leave out: %s", form, h)
		}
		r2.Header.Set("Signature", unpadded)
		if _, err := k.verify(r2, b); reasonOf(err) != sigReplay {
			t.Errorf("form %d: the signature re-encoded: %v", form, err)
		}
		// A replayed signed GET is let through (see verify).
		g := received(k.sign(t, form, "GET", tOrigin+"/users/ana", nil), nil)
		for i := range 2 {
			if _, err := k.verify(g, nil); err != nil {
				t.Errorf("form %d: GET %d: %v", form, i, err)
			}
		}
	}

	// The verifier reads no body: it is given the bytes the caller read within its cap.
	k := newSigKit(t)
	r, b := flowaPost(sigCavage)(t, k)
	var reads atomic.Int32
	r.Body = io.NopCloser(readCounter{&reads})
	if _, err := k.verify(r, b); err != nil || reads.Load() != 0 {
		t.Errorf("verify read the request body %d times (%v)", reads.Load(), err)
	}

	// No error text carries a header value, a body, a signing string, a key id or a key.
	k = newSigKit(t)
	secret := "s3cr3t-marker"
	body := []byte(`{"content":"` + secret + `"}`)
	req := k.request(t, "POST", tOrigin+"/inbox?"+secret, body)
	req.Header.Set("Digest", "SHA-256="+base64.StdEncoding.EncodeToString(make([]byte, 32)))
	req.Header.Set("X-Secret", secret)
	cavageWith(t, req, k.key, tKeyID+secret, "rsa-sha256", strings.Fields("(request-target) host date digest x-secret"), "")
	keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k.key)}))
	var errs []error
	_, err := k.verify(received(req, body), body)
	errs = append(errs, err)
	for _, c := range []string{`keyId="` + secret + `"`, secret, `keyId="https://x/` + secret + `",headers="` + secret + `",signature="` + secret + `"`} {
		r := received(req, body)
		r.Header.Set("Signature", c)
		_, err := k.verify(r, body)
		errs = append(errs, err)
	}
	for _, s := range []string{secret, "-----BEGIN PUBLIC KEY-----\n" + secret + "\n-----END PUBLIC KEY-----\n"} {
		_, err := parsePublicKeyPEM(s, sigKeyMin)
		errs = append(errs, err)
	}
	errs = append(errs, httpSigner(k.key, tKeyID, sigCavage, func() time.Time { return t0 }, t0.Add(time.Hour))(req, body))
	for _, err := range errs {
		if err == nil {
			t.Fatal("a refusal was expected")
		}
		msg := err.Error()
		if strings.Contains(msg, secret) || strings.Contains(msg, "BEGIN") || strings.Contains(msg, keyPEM[40:80]) || strings.Contains(msg, "(request-target)") {
			t.Errorf("an error carries what it must not: %q", msg)
		}
	}
}

type readCounter struct{ n *atomic.Int32 }

func (r readCounter) Read([]byte) (int, error) { r.n.Add(1); return 0, io.EOF }

/* ---------- the replay memory ---------- */

func TestReplayWindow(t *testing.T) {
	key := func(i int) replayKey { return replayKeyOf([]byte(strconv.Itoa(i))) }
	at := t0.Unix()

	// A second use is refused; the memory lasts 13 hours from arrival, through the last second.
	s := newReplaySet(sigSeenMax, sigKeep)
	if s.add(key(1), at+3600, at) != 0 {
		t.Fatal("a first use was refused")
	}
	if r := s.add(key(1), at+3600, at+1); r != sigReplay {
		t.Errorf("a second use: %v", r)
	}
	if r := s.check(key(1), at+3600, at+13*3600); r != sigReplay {
		t.Errorf("13 hours after: %v, want still remembered", r)
	}
	if r := s.check(key(1), at+3600, at+13*3600+1); r != 0 || s.size() != 0 {
		t.Errorf("13 hours and a second after: %v with %d kept, want forgotten", r, s.size())
	}
	// The two windows interlock: by the time an entry is forgotten, even a signature dated the full
	// hour ahead of its arrival is past the 12 hours the time window allows.
	if r := inWindow(time.Unix(at+3600, 0), time.Unix(at+13*3600+1, 0)); r != sigTooOld {
		t.Errorf("the forgotten signature against the window: %v", r)
	}
	if r := inWindow(time.Unix(at+3600, 0), time.Unix(at+13*3600, 0)); r != 0 {
		t.Errorf("the last second it is remembered, it is still in the window: %v", r)
	}
	// Through the verifier: a signature an hour ahead, replayed at 13 hours, then a second later.
	k := newSigKit(t)
	k.now = t0.Add(time.Hour)
	r, b := flowaPost(sigCavage)(t, k)
	k.now = t0
	if _, err := k.verify(r, b); err != nil {
		t.Fatal(err)
	}
	k.now = t0.Add(13 * time.Hour)
	if _, err := k.verify(r, b); reasonOf(err) != sigReplay {
		t.Errorf("replayed at 13 hours: %v", err)
	}
	k.now = t0.Add(13*time.Hour + time.Second)
	if _, err := k.verify(r, b); reasonOf(err) != sigTooOld {
		t.Errorf("replayed at 13 hours and a second: %v", err)
	}

	// When full, the oldest half by arrival goes, and anything vouching for a time at or before the
	// latest time among those dropped is stale. Arrival order is not time order: the third dropped
	// signature vouches for the latest time, later than the oldest one kept, so "older than the
	// oldest kept" would let it be replayed.
	s = newReplaySet(10, sigKeep)
	ats := []int64{100, 200, 900, 300, 400, 150, 500, 600, 700, 800}
	for i, a := range ats {
		if r := s.add(key(i), at+a, at+int64(i)); r != 0 {
			t.Fatalf("entry %d: %v", i, r)
		}
	}
	if r := s.add(key(10), at+1000, at+10); r != 0 || s.size() != 6 {
		t.Fatalf("the add that fills it: %v, %d kept", r, s.size())
	}
	if r := s.check(key(2), at+900, at+11); r != sigStale {
		t.Errorf("a dropped signature replayed: %v", r)
	}
	if r := s.check(key(99), at+899, at+11); r != sigStale {
		t.Errorf("a new signature older than the latest dropped: %v", r)
	}
	if r := s.check(key(99), at+901, at+11); r != 0 {
		t.Errorf("a new signature later than every dropped one: %v", r)
	}
	if r := s.check(key(5), at+150, at+11); r != sigStale {
		t.Errorf("a kept signature older than the floor: %v", r)
	}

	// No growth past the cap under 200,000 distinct signatures, and the memory each entry takes.
	s = newReplaySet(sigSeenMax, sigKeep)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for i := range sigSeenMax {
		s.add(key(i), at+int64(i), at)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	perEntry := float64(after.HeapAlloc-before.HeapAlloc) / sigSeenMax
	t.Logf("replay memory: %.0f bytes an entry, %.1f MB for %d", perEntry, perEntry*sigSeenMax/1e6, sigSeenMax)
	if perEntry > 80 {
		t.Errorf("%.0f bytes an entry; federation.md 7.2 and section 12 allow 6 MB for 100,000", perEntry)
	}
	for i := sigSeenMax; i < 2*sigSeenMax; i++ {
		s.add(key(i), at+int64(i), at)
		if s.size() > sigSeenMax || len(s.q) > sigSeenMax || len(s.m) > sigSeenMax {
			t.Fatalf("grew to %d entries, %d in the ring, %d in the map", s.size(), len(s.q), len(s.m))
		}
	}

	// Safe for concurrent use: of 32 goroutines adding one signature, exactly one wins.
	s = newReplaySet(sigSeenMax, sigKeep)
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			if s.add(key(7), at, at) == 0 {
				wins.Add(1)
			}
		})
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Errorf("%d goroutines added the same signature", wins.Load())
	}
}

/* ---------- the clock ---------- */

func TestClockTrusted(t *testing.T) {
	built := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		now, built time.Time
		want       bool
	}{
		{built.Add(time.Hour), built, true}, {built, built, true}, {built.Add(-time.Second), built, false},
		{time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC), built, false},      // a Pi that booted with no clock
		{time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC), time.Time{}, true}, // build time unknown: skipped
	} {
		if got := clockTrusted(c.now, c.built); got != c.want {
			t.Errorf("clockTrusted(%v, %v) = %v", c.now, c.built, got)
		}
	}
	k := newSigKit(t)
	req := k.request(t, "POST", "https://social.example/inbox", inboxBody)
	err := httpSigner(k.key, tKeyID, sigCavage, func() time.Time { return built.Add(-time.Minute) }, built)(req, inboxBody)
	var refusal *signRefusal
	if !errors.Is(err, errClockUntrusted) || !errors.As(err, &refusal) || req.Header.Get("Signature") != "" {
		t.Errorf("signing with the clock behind the build: %v, Signature %q", err, req.Header.Get("Signature"))
	}
	if err := httpSigner(k.key, tKeyID, sigCavage, func() time.Time { return built }, built)(req, inboxBody); err != nil {
		t.Errorf("signing at the build time: %v", err)
	}
}

/* ---------- public keys and origins ---------- */

func TestSignaturePublicKeyPEM(t *testing.T) {
	a, _ := sigTestKeys()
	spki, _ := x509.MarshalPKIXPublicKey(&a.PublicKey)
	spkiPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: spki}))
	pkcs1PEM := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: x509.MarshalPKCS1PublicKey(&a.PublicKey)}))
	edPub, _, _ := ed25519.GenerateKey(rand.Reader)
	edDER, _ := x509.MarshalPKIXPublicKey(edPub)
	edPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: edDER}))
	huge := &rsa.PublicKey{N: new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 8200), big.NewInt(1)), E: 65537}
	hugePEM := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: x509.MarshalPKCS1PublicKey(huge)}))
	ck := readVector(t, "cavage12-test-key.txt")

	for _, s := range []string{spkiPEM, pkcs1PEM, "\n  " + spkiPEM + "\n\n"} {
		if k, err := parsePublicKeyPEM(s, sigKeyMin); err != nil || !k.Equal(&a.PublicKey) {
			t.Errorf("%.30q: %v", s, err)
		}
	}
	for s, want := range map[string]string{
		edPEM:                 "an Ed25519 key, which is not accepted yet",
		ck["public key"]:      "an RSA key under the minimum size",
		hugePEM:               "an RSA key over 8192 bits",
		spkiPEM + "trailing":  "data after the PEM block",
		spkiPEM + pkcs1PEM:    "more than one PEM block",
		"leading\n" + spkiPEM: "not a PEM block",
		"":                    "not a PEM block",
		strings.Replace(spkiPEM, "PUBLIC KEY", "CERTIFICATE", 2):                    "a PEM block that is not a public key",
		strings.Replace(spkiPEM, "-----\n", "-----\nProc-Type: 4,ENCRYPTED\n\n", 1): "a PEM block with headers",
		strings.Replace(spkiPEM, "PUBLIC KEY", "RSA PUBLIC KEY", 2):                 "not a key",
		strings.Repeat("A", sigPEMMax+1):                                            "longer than a public key",
	} {
		_, err := parsePublicKeyPEM(s, sigKeyMin)
		var pe *pubKeyError
		if !errors.As(err, &pe) || pe.Reason != want {
			t.Errorf("%.40q: %v, want %q", s, err, want)
		}
	}
	if _, err := parsePublicKeyPEM(ck["public key"], 1024); err != nil {
		t.Errorf("the cavage key with the floor lowered: %v", err)
	}
}

func TestSignatureSameOrigin(t *testing.T) {
	for _, c := range []struct {
		key, actor string
		want       bool
	}{
		{"https://social.example/users/bo#main-key", "https://social.example/users/bo", true},
		{"https://social.example/keys/1", "https://SOCIAL.example:443/users/bo", true},
		{"https://social.example:8443/users/bo#k", "https://social.example/users/bo", false},
		{"https://evil.example/users/bo#k", "https://social.example/users/bo", false},
		{"https://sub.social.example/k", "https://social.example/users/bo", false},
		{"http://social.example/k", "https://social.example/users/bo", false},
		{"https://user@social.example/k", "https://social.example/users/bo", false},
		{"Test", "https://social.example/users/bo", false},
	} {
		if got := sameOrigin(c.key, c.actor); got != c.want {
			t.Errorf("sameOrigin(%s, %s) = %v", c.key, c.actor, got)
		}
	}
}

/* ---------- Structured Fields ---------- */

func TestSignatureStructuredFields(t *testing.T) {
	// Valid examples of RFC 8941 3.2 and of RFC 9421, written back in strict form.
	for in, want := range map[string]string{
		`en="Applepie", da=:w4ZibGV0w6ZydGU=:`:                    `en="Applepie", da=:w4ZibGV0w6ZydGU=:`,
		`a=?0, b, c; foo=bar`:                                     `a=?0, b, c;foo=bar`,
		`rating=1.5, feelings=(joy sadness)`:                      `rating=1.5, feelings=(joy sadness)`,
		`a=(1 2), b=3, c=4;aa=bb, d=(5 6);valid`:                  `a=(1 2), b=3, c=4;aa=bb, d=(5 6);valid`,
		`  sig1=(  "@method"   "@path" );created=1;  keyid="k"  `: `sig1=("@method" "@path");created=1;keyid="k"`,
		`x=-12.250, y="a\"b\\c", z=:YQ:`:                          `x=-12.25, y="a\"b\\c", z=:YQ==:`,
		`t=*tok/en:x`:                                             `t=*tok/en:x`,
	} {
		d, err := sfDictionary(in)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		var parts []string
		for _, m := range d {
			var b strings.Builder
			b.WriteString(m.key)
			switch {
			case m.inner:
				b.WriteString("=" + sfInnerList(m.list, m.params))
			case m.item.val.kind == sfBoolean && m.item.val.n == 1:
				sfWriteParams(&b, m.item.params)
			default:
				b.WriteByte('=')
				sfWriteBare(&b, m.item.val)
				sfWriteParams(&b, m.item.params)
			}
			parts = append(parts, b.String())
		}
		if got := strings.Join(parts, ", "); got != want {
			t.Errorf("%q: written back as %q, want %q", in, got, want)
		}
	}
	for _, in := range []string{
		`a=1,`, `a=1,,b=2`, `A=1`, `a=1, a=2`, `a=:not base64*:`, `a=:YQ`, `a="open`, `a="bad \x"`, `a=1234567890123456`,
		`a=1234567890123.1`, `a=1.1234`, `a=1.`, `a=?2`, `a=(1 2`, `a=(1;x=1;x=2)`, `a=1 b=2`, `a=é`, `a=#`, "a=:YQ\n==:", `a=:YQ===:`,
		`a=("x")` + strings.Repeat(";p", 1) + `;p`,
	} {
		if _, err := sfDictionary(in); err == nil {
			t.Errorf("%q was accepted", in)
		}
	}
	// The caps on members, items and parameters.
	many := func(n int, f func(i int) string, sep string) string {
		var p []string
		for i := range n {
			p = append(p, f(i))
		}
		return strings.Join(p, sep)
	}
	for _, c := range []struct {
		in string
		ok bool
	}{
		{many(sigParamsMax, func(i int) string { return "k" + strconv.Itoa(i) + "=1" }, ", "), true},
		{many(sigParamsMax+1, func(i int) string { return "k" + strconv.Itoa(i) + "=1" }, ", "), false},
		{"a=(" + many(sigCoveredMax, func(i int) string { return `"h` + strconv.Itoa(i) + `"` }, " ") + ")", true},
		{"a=(" + many(sigCoveredMax+1, func(i int) string { return `"h` + strconv.Itoa(i) + `"` }, " ") + ")", false},
		{"a=()" + many(sigParamsMax, func(i int) string { return ";p" + strconv.Itoa(i) }, ""), true},
		{"a=()" + many(sigParamsMax+1, func(i int) string { return ";p" + strconv.Itoa(i) }, ""), false},
	} {
		if _, err := sfDictionary(c.in); (err == nil) != c.ok {
			t.Errorf("%.60q: %v, want ok %v", c.in, err, c.ok)
		}
	}
}

// Long inputs are refused at their caps, and the parsers' work grows with the input, not faster:
// each is given 64 KB and must stop at a cap with a handful of allocations whatever the size.
func TestSignatureLongInputs(t *testing.T) {
	long := strings.Repeat("a", 64<<10)
	if _, err := parseCavage(`keyId="` + long + `",signature="x"`); reasonOf(err) != sigTooLarge {
		t.Errorf("a 64 KB cavage header: %v", err)
	}
	if _, err := parseSignatureInput(`sig1=("` + long + `")`); reasonOf(err) != sigTooLarge {
		t.Errorf("a 64 KB Signature-Input: %v", err)
	}
	k := newSigKit(t)
	r, b := flowaPost(sigCavage)(t, k)
	r.Header.Set("Digest", long)
	if _, err := k.verify(r, b); reasonOf(err) != sigTooLarge {
		t.Errorf("a 64 KB Digest: %v", err)
	}
	r, b = after(handPost(func(r *http.Request) { r.Header.Set("X-Big", "small") }, cav("rsa-sha256", "(request-target) host date digest x-big", "")), func(r *http.Request, b []byte) []byte {
		r.Header.Set("X-Big", long)
		return b
	})(t, k)
	if _, err := k.verify(r, b); reasonOf(err) != sigTooLarge {
		t.Errorf("a 64 KB covered header: %v", err)
	}
	// The structured field parser on its own, without the field cap in front of it.
	for name, in := range map[string]string{
		"a long string":   `a="` + long + `"`,
		"many members":    strings.Repeat("a=1, ", 64<<10/5),
		"many items":      "a=(" + strings.Repeat(`"x" `, 64<<10/4) + ")",
		"many parameters": "a=1" + strings.Repeat(";p", 64<<10/2),
		"a long token":    "a=" + long,
	} {
		allocs := testing.AllocsPerRun(5, func() { sfDictionary(in) })
		if allocs > 64 {
			t.Errorf("%s: %.0f allocations for 64 KB", name, allocs)
		}
	}
}

/* ---------- a round trip through the fetcher ---------- */

// Flowa's signer, run by fetcher.do over real TLS, and Flowa's verifier in the stub at the other
// end: both forms, GET and POST, and the signer run again for the request a redirect leads to.
func TestSignatureThroughFetcher(t *testing.T) {
	a, _ := sigTestKeys()
	type seen struct {
		path string
		form sigForm
		err  error
	}
	var mu sync.Mutex
	var got []seen
	v, err := newSigVerifier("https://remote.example", func(_ context.Context, id string) (*rsa.PublicKey, error) {
		if id == tKeyID {
			return &a.PublicKey, nil
		}
		return nil, errors.New("no such key")
	})
	if err != nil {
		t.Fatal(err)
	}
	v.built = time.Time{}
	f, _ := fedStub(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 256<<10))
		res, err := v.verifyRequest(r.Context(), r, body)
		s := seen{path: r.URL.Path, err: err}
		if res != nil {
			s.form = res.Form
		}
		mu.Lock()
		got = append(got, s)
		mu.Unlock()
		switch r.URL.Path {
		case "/users/ana/moved":
			http.Redirect(w, r, "/users/ana", http.StatusFound)
		case "/inbox":
			w.WriteHeader(http.StatusAccepted)
		default:
			asDoc(w, "https://remote.example"+r.URL.Path)
		}
	}))
	for _, form := range []sigForm{sigCavage, sigRFC9421} {
		got = nil
		sign := httpSigner(a, tKeyID, form, time.Now, time.Time{})
		if _, err := f.do(context.Background(), fetchReq{kind: fetchDocument, url: "https://remote.example/users/ana/moved?x=%41", sign: sign}); err != nil {
			t.Fatalf("form %d GET: %v", form, err)
		}
		if _, err := f.do(context.Background(), fetchReq{kind: fetchDelivery, url: "https://remote.example/inbox", body: inboxBody, sign: sign}); err != nil {
			t.Fatalf("form %d POST: %v", form, err)
		}
		want := []seen{{"/users/ana/moved", sigCavage, nil}, {"/users/ana", sigCavage, nil}, {"/inbox", form, nil}}
		if len(got) != len(want) {
			t.Fatalf("form %d: the stub saw %+v", form, got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("form %d request %d: %+v, want %+v", form, i, got[i], want[i])
			}
		}
	}
}

/* ---------- fuzzing ---------- */

func FuzzParseSignature(f *testing.F) {
	for _, s := range []string{
		`keyId="Test",algorithm="rsa-sha256",headers="(request-target) host date",signature="qdx+"`,
		`keyId="a", signature="b", created=1402170695, expires=1402170699.5, headers="(created)"`,
		`keyId="a\"b",signature="",x=y,,`, `keyId=a`, `=`, `keyId="` + strings.Repeat("x", 100),
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		sig, err := parseCavage(s)
		if err != nil {
			if reasonOf(err) == 0 {
				t.Fatalf("an untyped error: %v", err)
			}
			return
		}
		if len(sig.headers) == 0 || len(sig.headers) > sigCoveredMax {
			t.Fatalf("%d headers", len(sig.headers))
		}
		for _, h := range sig.headers {
			if h != strings.ToLower(h) || h == "" {
				t.Fatalf("header %q", h)
			}
		}
	})
}

func FuzzParseSignatureInput(f *testing.F) {
	for _, s := range []string{
		`sig1=("@method" "@target-uri" "content-digest");created=1794506646;keyid="https://a/b#c"`,
		`sig-b22=("@authority" "content-digest" "@query-param";name="Pet");created=1618884473;keyid="test-key-rsa-pss";tag="header-example"`,
		`sig1=();created=1, proxy_sig=("a");alg="rsa-v1_5-sha256";expires=2`, `a=(1.5 ?0 :YQ==: tok)`, `a=("`, `a=1`,
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		ins, err := parseSignatureInput(s)
		if err != nil {
			if reasonOf(err) == 0 {
				t.Fatalf("an untyped error: %v", err)
			}
			return
		}
		// What parses writes back in a form that parses to the same thing.
		for _, in := range ins {
			line := sfInnerList(in.covered, in.params)
			again, err := parseSignatureInput(in.label + "=" + line)
			if err != nil || sfInnerList(again[0].covered, again[0].params) != line {
				t.Fatalf("%q wrote back as %q, which reads %v", s, line, err)
			}
		}
	})
}

// FuzzVerifyRequest: a request with any signature fields, Date, body and request target, against
// one fixed key. It must never panic, every refusal must be typed, and nothing but the two requests
// that were signed may be accepted.
func FuzzVerifyRequest(f *testing.F) {
	k := newSigKit(f)
	req := k.sign(f, sigCavage, "POST", tOrigin+"/inbox", inboxBody)
	r9421 := k.sign(f, sigRFC9421, "POST", tOrigin+"/inbox", inboxBody)
	f.Add(req.Header.Get("Signature"), "", req.Header.Get("Digest"), "", req.Header.Get("Date"), string(inboxBody), "/inbox")
	f.Add(r9421.Header.Get("Signature"), r9421.Header.Get("Signature-Input"), "", r9421.Header.Get("Content-Digest"), r9421.Header.Get("Date"), string(inboxBody), "/inbox")
	f.Add(`keyId="https://a/b",headers="(request-target) host date digest",signature="AAAA"`, "", "SHA-256=x", "", "x", "", "/")
	f.Fuzz(func(t *testing.T, sig, input, digest, cdigest, date, body, target string) {
		if !strings.HasPrefix(target, "/") || strings.ContainsAny(target, " \r\n") {
			target = "/inbox"
		}
		r := httptest.NewRequest("POST", "/inbox", nil)
		r.RequestURI = target
		for name, v := range map[string]string{"Signature": sig, "Signature-Input": input, "Digest": digest, "Content-Digest": cdigest, "Date": date} {
			if v != "" {
				r.Header.Set(name, v)
			}
		}
		k.v.seen = newReplaySet(sigSeenMax, sigKeep)
		res, err := k.verify(r, []byte(body))
		if err != nil && reasonOf(err) == 0 {
			t.Fatalf("an untyped error: %v", err)
		}
		if err == nil && (res.KeyID != tKeyID || body != string(inboxBody) || target != "/inbox") {
			t.Fatalf("accepted something it was not signed for: %+v, body %q, target %q", res, body, target)
		}
	})
}

/* ---------- benchmarks (federation.md 12) ---------- */

// BenchmarkInboxVerify: an inbox delivery of a 2 KB body with the key in hand (parse, base, digest,
// RSA 2048 verify, replay check). The replay memory forgets the one signature after each run, so
// every run does the whole check. Budget: under 2 ms CPU on the runner, 10 ms on the Pi, at most 400
// allocations.
func BenchmarkInboxVerify(b *testing.B) {
	body := bytes.Repeat([]byte("x"), 2048)
	for _, form := range []sigForm{sigCavage, sigRFC9421} {
		b.Run(map[sigForm]string{sigCavage: "cavage", sigRFC9421: "rfc9421"}[form], func(b *testing.B) {
			k := newSigKit(b)
			r := received(k.sign(b, form, "POST", tOrigin+"/inbox", body), body)
			ctx := context.Background()
			b.ReportAllocs()
			for b.Loop() {
				if _, err := k.v.verifyRequest(ctx, r, body); err != nil {
					b.Fatal(err)
				}
				s := k.v.seen
				s.mu.Lock()
				clear(s.m)
				s.n, s.head = 0, 0
				s.mu.Unlock()
			}
		})
	}
}

// BenchmarkSignDelivery: signing a delivery of a 2 KB body (digest, base, RSA 2048 sign). Budget:
// under 15 ms on the Pi.
func BenchmarkSignDelivery(b *testing.B) {
	body := bytes.Repeat([]byte("x"), 2048)
	a, _ := sigTestKeys()
	for _, form := range []sigForm{sigCavage, sigRFC9421} {
		b.Run(map[sigForm]string{sigCavage: "cavage", sigRFC9421: "rfc9421"}[form], func(b *testing.B) {
			sign := httpSigner(a, tKeyID, form, time.Now, time.Time{})
			b.ReportAllocs()
			for b.Loop() {
				req, _ := http.NewRequest("POST", "https://social.example/users/bo/inbox", bytes.NewReader(body))
				req.Header.Set("Date", "Thu, 12 Nov 2026 18:04:06 GMT")
				if err := sign(req, body); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkReplayAdd: one addition to a full replay memory, the drop of half included.
func BenchmarkReplayAdd(b *testing.B) {
	s := newReplaySet(sigSeenMax, sigKeep)
	at := t0.Unix()
	i := 0
	b.ReportAllocs()
	for b.Loop() {
		s.add(replayKeyOf([]byte(strconv.Itoa(i))), at+int64(i), at)
		i++
	}
}

// BenchmarkParseLong: the structured field parser on 16 KB and 64 KB of one long string; the time
// a byte should stay the same.
func BenchmarkParseLong(b *testing.B) {
	for _, n := range []int{16 << 10, 64 << 10} {
		in := `a="` + strings.Repeat("a", n) + `"`
		b.Run(strconv.Itoa(n>>10)+"KB", func(b *testing.B) {
			b.SetBytes(int64(n))
			for b.Loop() {
				sfDictionary(in)
			}
		})
	}
}
