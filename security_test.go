package main

// Unit tests for the hardening in docs/hardening-plan.md that needs no database.

import (
	"bytes"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func dataURL(t *testing.T, kind string, w, h int, extra []byte) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		img.Set(x, x%h, color.RGBA{200, 80, 40, 255})
	}
	var buf bytes.Buffer
	if kind == "png" {
		png.Encode(&buf, img)
	} else {
		jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90})
	}
	buf.Write(extra)
	return "data:image/" + kind + ";base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestCleanImage(t *testing.T) {
	if v, err := cleanImage("", 100, 1000); v != "" || err != nil {
		t.Fatal("an empty picture stays empty")
	}
	for _, kind := range []string{"jpeg", "png"} {
		in := dataURL(t, kind, 64, 48, nil)
		out, err := cleanImage(in, 100, maxImage)
		if err != nil || !strings.HasPrefix(out, "data:image/"+kind+";base64,") {
			t.Fatalf("%s: %v", kind, err)
		}
		raw, _ := base64.StdEncoding.DecodeString(out[strings.IndexByte(out, ',')+1:])
		cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
		if err != nil || format != kind || cfg.Width != 64 || cfg.Height != 48 {
			t.Fatalf("%s: re-encoded picture is %v %v %v", kind, cfg, format, err)
		}
	}
	// Anything after the image data (or inside it, like camera metadata) does not survive.
	secret := []byte("GPS 51.5007,-0.1246 <script>alert(1)</script>")
	out, err := cleanImage(dataURL(t, "jpeg", 32, 32, secret), 100, maxImage)
	raw, _ := base64.StdEncoding.DecodeString(out[strings.IndexByte(out, ',')+1:])
	if err != nil || bytes.Contains(raw, []byte("GPS")) || bytes.Contains(raw, []byte("script")) {
		t.Fatalf("trailing data kept: %v", err)
	}
	pngAsJPEG := strings.Replace(dataURL(t, "png", 8, 8, nil), "image/png", "image/jpeg", 1)
	svg := "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(`<svg onload="alert(1)"/>`))
	for name, v := range map[string]string{
		"svg":               svg,
		"PNG labelled JPEG": pngAsJPEG,
		"not base64":        "data:image/jpeg;base64,***",
		"not an image":      "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString([]byte("hello")),
		"too wide":          dataURL(t, "png", 300, 10, nil),
	} {
		if _, err := cleanImage(v, 200, maxImage); err == nil {
			t.Errorf("%s should be refused", name)
		}
	}
	if _, err := cleanImage(dataURL(t, "png", 150, 150, nil), 200, 200); err == nil {
		t.Error("a picture over the length limit should be refused")
	}
}

func TestAllowedHost(t *testing.T) {
	for _, h := range []string{"127.0.0.1:3169", "192.168.1.188:3169", "[::1]:3169", "localhost:3170", "raspi:3169", "raspi.local", "pi.lan:3169", "flowa.home.arpa", "raspi.tail1234.ts.net", "flowa.example.org"} {
		if !allowedHost(h, []string{"flowa.example.org"}) {
			t.Errorf("%s should be allowed", h)
		}
	}
	for _, h := range []string{"evil.example.com", "rebind.attacker.net:3169", "", "localhost.attacker.com"} {
		if allowedHost(h, []string{"flowa.example.org"}) {
			t.Errorf("%s should be refused", h)
		}
	}
}

func TestPublicIPReserved(t *testing.T) {
	for _, s := range []string{"100.64.1.2", "100.127.255.1", "192.0.0.8", "198.18.0.1", "0.1.2.3", "240.0.0.1", "64:ff9b::7f00:1", "10.0.0.1", "127.0.0.1", "169.254.169.254", "::1", "fd00::1"} {
		if publicIP(net.ParseIP(s)) {
			t.Errorf("%s is not public", s)
		}
	}
	for _, s := range []string{"142.250.180.14", "157.240.1.35", "2a03:2880:f12f:83:face:b00c::25de"} {
		if !publicIP(net.ParseIP(s)) {
			t.Errorf("%s is public", s)
		}
	}
}

func TestLimiter(t *testing.T) {
	l := newLimiter(3, 50*time.Millisecond)
	for i := 0; i < 3; i++ {
		if l.over("a") || !l.add("a") {
			t.Fatalf("event %d should be allowed", i+1)
		}
	}
	if !l.over("a") || l.add("a") || l.over("b") {
		t.Fatal("the fourth event is over the limit, for that key only")
	}
	time.Sleep(60 * time.Millisecond)
	if l.over("a") || !l.add("a") {
		t.Fatal("a new window starts afresh")
	}
	// A full limiter drops expired keys first, and then forgets the oldest: the cap bounds memory
	// and never refuses a new visitor (SEC-10).
	small := newLimiter(1, time.Hour)
	small.maxKeys = 2
	small.add("x")
	small.add("y")
	if !small.add("z") || len(small.m) != 2 || small.m["x"] != nil || small.m["y"] == nil {
		t.Fatalf("a new key in a full limiter is taken and the oldest is forgotten: %v", small.m)
	}
	small.m["y"].reset = time.Now().Add(-time.Second)
	if !small.add("w") || len(small.m) != 2 || small.m["y"] != nil || small.m["z"] == nil {
		t.Fatal("an expired key makes room before a live one is forgotten")
	}
	small.forget("z")
	if !small.add("v") || !small.add("u") || len(small.m) != 2 || small.m["w"] != nil {
		t.Fatalf("a forgotten key leaves its place in the queue: %v", small.m)
	}
	if len(small.order) > 3 {
		t.Fatalf("the queue keeps %d entries for 2 keys", len(small.order))
	}
	// Tenths: ten taken names cost one event, and part of a count can be given back.
	w := newLimiter(2, time.Hour)
	for i := 0; i < 10; i++ {
		w.addWeight("k", 1)
	}
	if w.over("k") || !w.add("k") || !w.over("k") {
		t.Fatal("ten tenths and one whole event use up an allowance of two")
	}
	w.addWeight("k", 1-tenths)
	if w.over("k") {
		t.Fatal("giving nine tenths back leaves room")
	}
	// A tenth from a request that was turned down does not cost the last whole event, and a count
	// that is full stops growing.
	e := newLimiter(2, time.Hour)
	e.add("k")
	e.addWeight("k", 1)
	if !e.add("k") || e.add("k") || e.add("k") || e.m["k"].n != 2*tenths+1 {
		t.Fatalf("one and a tenth, then one more and no further: count %d", e.m["k"].n)
	}
	w.addWeight("gone", -5)
	if w.m["gone"] != nil {
		t.Fatal("giving back makes no counter")
	}
}

func TestLimitKey(t *testing.T) {
	for _, c := range [][2]string{
		{"203.0.113.9", "203.0.113.9"},
		{"::ffff:203.0.113.9", "203.0.113.9"},         // IPv4 written as IPv6 is IPv4
		{"2001:db8:1:2:aaaa::1", "2001:db8:1:2::/64"}, // one /64, one key
		{"2001:db8:1:2:bbbb::2", "2001:db8:1:2::/64"},
		{"2001:db8:1:3::1", "2001:db8:1:3::/64"}, // the next /64 is someone else
		{"fe80::1%eth0", "fe80::/64"},
		{"::1", "::/64"},
		{"not an address", "not an address"},
	} {
		if got := limitKey(c[0]); got != c[1] {
			t.Errorf("limitKey(%q) = %q, want %q", c[0], got, c[1])
		}
	}
}

// A write must come from Flowa's own pages, and a page served over plain HTTP may not write to the
// HTTPS site (SEC-16).
func TestSameOrigin(t *testing.T) {
	def, _ := parseProxies(defaultProxies)
	a := &app{proxies: def}
	req := func(remote, origin, proto string, direct bool) *http.Request {
		r := httptest.NewRequest("POST", "/api/posts", nil)
		r.RemoteAddr = remote
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if proto != "" {
			r.Header.Set("X-Forwarded-Proto", proto)
		}
		if direct {
			r.TLS = &tls.ConnectionState{}
		}
		return r
	}
	// With the proxy profile of compose.yml only its own network is trusted (10.169.31.0/29).
	edge, _ := parseProxies("10.169.31.0/29")
	behind := &app{proxies: edge}
	const visitor, proxy = "203.0.113.9:5000", "172.18.0.2:5000"
	const caddy, other = "10.169.31.3:5000", "172.18.0.2:5000"
	for _, c := range []struct {
		name string
		r    *http.Request
		want bool
	}{
		{"no Origin (not a browser page)", req(visitor, "", "", false), true},
		{"plain HTTP at home", req(visitor, "http://example.com", "", false), true},
		{"HTTPS directly", req(visitor, "https://example.com", "", true), true},
		{"an HTTP page writing to the HTTPS site", req(visitor, "http://example.com", "", true), false},
		{"HTTPS through a trusted proxy", req(proxy, "https://example.com", "https", false), true},
		{"an HTTP page through a trusted proxy that saw HTTPS", req(proxy, "http://example.com", "https", false), false},
		{"a proxy that ends TLS without saying so", req(proxy, "https://example.com", "", false), true},
		{"a proxy behind a tunnel reports http", req(proxy, "https://example.com", "http", false), true},
		{"a visitor's own X-Forwarded-Proto is not believed", req(visitor, "http://example.com", "https", false), true},
		{"another site", req(visitor, "https://attacker.example", "", true), false},
		{"another port", req(visitor, "http://example.com:8080", "", false), false},
		{"an opaque origin", req(visitor, "null", "", false), false},
	} {
		if got := a.sameOrigin(c.r); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	host := func(r *http.Request) *http.Request { r.Host = "flowa.example.org"; return r }
	for _, c := range []struct {
		name string
		r    *http.Request
		want bool
	}{
		{"HTTPS through the compose proxy", host(req(caddy, "https://flowa.example.org", "https", false)), true},
		{"an HTTP page through the compose proxy", host(req(caddy, "http://flowa.example.org", "https", false)), false},
		{"another site through the compose proxy", host(req(caddy, "https://attacker.example", "https", false)), false},
		{"the name with another port through the compose proxy", host(req(caddy, "https://flowa.example.org:8443", "https", false)), false},
		{"no Origin through the compose proxy", host(req(caddy, "", "https", false)), true},
		// Only the proxy's network says what is HTTPS: another container saying so is a plain HTTP request.
		{"another container claiming HTTPS is not believed", host(req(other, "http://flowa.example.org", "https", false)), true},
	} {
		if got := behind.sameOrigin(c.r); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	if !behind.viaHTTPS(host(req(caddy, "", "https", false))) || behind.viaHTTPS(host(req(other, "", "https", false))) || behind.viaHTTPS(host(req("127.0.0.1:5000", "", "https", false))) {
		t.Error("with the proxy network alone trusted, only the proxy says a request came over HTTPS")
	}
}

func TestMentionsAndRanks(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 15; i++ {
		b.WriteString(" @user" + string(rune('a'+i)) + "x")
	}
	if got := mentions(b.String()); len(got) != maxMentions {
		t.Fatalf("mentions capped at %d, got %d", maxMentions, len(got))
	}
	if strings.Join(below("admin"), ",") != "moderator,user" || strings.Join(below("moderator"), ",") != "user" || len(below("user")) != 0 {
		t.Fatalf("below: %v %v %v", below("admin"), below("moderator"), below("user"))
	}
}

func TestClientIP(t *testing.T) {
	def, _ := parseProxies(defaultProxies)
	cf, _ := parseProxies("173.245.48.0/20, 172.16.0.0/12")
	edge, _ := parseProxies("10.169.31.0/29") // what compose.yml sets once FLOWA_DOMAIN is set
	none, _ := parseProxies("none")
	req := func(remote, xff, real string) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		if real != "" {
			r.Header.Set("X-Real-IP", real)
		}
		return r
	}
	cases := []struct {
		name    string
		proxies []*net.IPNet
		r       *http.Request
		want    string
	}{
		{"direct visitor, header ignored", def, req("203.0.113.9:5000", "1.2.3.4", ""), "203.0.113.9"},
		{"through a proxy in Docker", def, req("172.18.0.2:5000", "203.0.113.5", ""), "203.0.113.5"},
		{"through a proxy on the host", def, req("127.0.0.1:5000", "198.51.100.7", ""), "198.51.100.7"},
		{"a visitor's own header is left of the proxy's", def, req("172.18.0.2:5000", "1.1.1.1, 203.0.113.5", ""), "203.0.113.5"},
		{"two proxies", def, req("172.18.0.2:5000", "203.0.113.5, 172.18.0.3", ""), "203.0.113.5"},
		{"header split over two lines", def, func() *http.Request {
			r := req("172.18.0.2:5000", "", "")
			r.Header.Add("X-Forwarded-For", "1.1.1.1")
			r.Header.Add("X-Forwarded-For", "203.0.113.5")
			return r
		}(), "203.0.113.5"},
		{"a device on the home LAN cannot pick its address", def, req("192.168.1.20:5000", "8.8.8.8", ""), "192.168.1.20"},
		{"X-Real-IP from a proxy", def, req("172.18.0.2:5000", "", "203.0.113.8"), "203.0.113.8"},
		{"X-Real-IP from a visitor is ignored", def, req("203.0.113.9:5000", "", "1.2.3.4"), "203.0.113.9"},
		{"a visitor's X-Real-IP that a proxy passed on beside its own X-Forwarded-For", def, req("172.18.0.2:5000", "172.18.0.1", "1.2.3.4"), "172.18.0.1"},
		{"X-Real-IP beside a visitor's address in X-Forwarded-For", def, req("172.18.0.2:5000", "203.0.113.5", "1.2.3.4"), "203.0.113.5"},
		{"the compose proxy network alone: its proxy is believed", edge, req("10.169.31.3:5000", "203.0.113.5", ""), "203.0.113.5"},
		{"the compose proxy network alone: another container is not", edge, req("172.18.0.2:5000", "203.0.113.5", ""), "172.18.0.2"},
		{"the compose proxy network alone: nor the machine itself", edge, req("127.0.0.1:5000", "203.0.113.5", ""), "127.0.0.1"},
		{"malformed header", def, req("172.18.0.2:5000", "not-an-address", ""), "172.18.0.2"},
		{"no header from a proxy", def, req("172.18.0.2:5000", "", ""), "172.18.0.2"},
		{"IPv6 visitor", def, req("[::1]:5000", "2001:db8::42", ""), "2001:db8::42"},
		{"trusting none", none, req("172.18.0.2:5000", "203.0.113.5", ""), "172.18.0.2"},
		{"behind Cloudflare and a Docker proxy", cf, req("172.18.0.2:5000", "203.0.113.5, 173.245.48.10", ""), "203.0.113.5"},
	}
	for _, c := range cases {
		a := &app{proxies: c.proxies}
		if got := a.clientIP(c.r); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
	a := &app{proxies: def}
	https := req("172.18.0.2:5000", "", "")
	https.Header.Set("X-Forwarded-Proto", "https")
	spoof := req("203.0.113.9:5000", "", "")
	spoof.Header.Set("X-Forwarded-Proto", "https")
	if !a.viaHTTPS(https) || a.viaHTTPS(spoof) {
		t.Error("X-Forwarded-Proto counts only from a trusted proxy")
	}
	if _, err := parseProxies("10.0.0.0/8, bogus"); err == nil {
		t.Error("a bad entry in FLOWA_TRUSTED_PROXIES must be refused")
	}
}

// probe asks /healthz from the given address with the given headers and returns the status and the answer.
func probe(t *testing.T, h http.Handler, remote string, hdr map[string]string) (int, map[string]any) {
	t.Helper()
	r := httptest.NewRequest("GET", "/healthz", nil)
	r.RemoteAddr = remote
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("the health answer may be kept by a cache: Cache-Control %q", cc)
	}
	out := map[string]any{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("the health answer is not JSON: %q", w.Body.String())
	}
	return w.Code, out
}

// TestHealthz covers what needs no database: an instance whose database does not answer says so with
// 503, and only a probe from the machine itself is told more than that. TestDBHealthz covers a working one.
func TestHealthz(t *testing.T) {
	// Nothing listens on port 1, so every query fails at once, as with a database that is down.
	db, err := sql.Open("postgres", "postgres://x:x@127.0.0.1:1/x?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a := newApp(db, t.TempDir())
	a.hosts = []string{"example.com"}
	a.version = "v9.9.9"
	h := a.routes(staticOf(t, nil))

	code, out := probe(t, h, "127.0.0.1:4000", nil)
	if code != 503 || out["ok"] != false || out["database"] != false {
		t.Fatalf("with the database down a local probe gets %d %v, want 503 with ok and database false", code, out)
	}
	if out["version"] != "v9.9.9" || out["migrations"] != float64(len(migrations)) || out["schema"] != float64(0) {
		t.Fatalf("a local probe is told %v, want the version, %d migrations and schema 0", out, len(migrations))
	}
	if _, ok := out["tlsRoots"].(bool); !ok {
		t.Fatalf("a local probe is not told whether TLS roots are present: %v", out)
	}

	// Everyone else learns only whether it works.
	strangers := []struct {
		what, remote string
		hdr          map[string]string
	}{
		{"a visitor", "203.0.113.9:5000", nil},
		{"someone on the home network", "192.168.1.20:5000", nil},
		{"the Docker host", "172.18.0.1:5000", nil},
		{"a visitor behind a proxy in Docker", "172.18.0.2:5000", map[string]string{"X-Forwarded-For": "203.0.113.9"}},
		{"a visitor behind a proxy on this machine", "127.0.0.1:5000", map[string]string{"X-Forwarded-For": "203.0.113.9"}},
		{"a visitor behind a proxy that sends X-Real-IP", "127.0.0.1:5000", map[string]string{"X-Real-IP": "203.0.113.9"}},
		{"a visitor behind a proxy that sends Forwarded", "[::1]:5000", map[string]string{"Forwarded": "for=203.0.113.9"}},
		{"a visitor behind a proxy that sends Via", "127.0.0.1:5000", map[string]string{"Via": "1.1 caddy"}},
	}
	for _, c := range strangers {
		code, out := probe(t, h, c.remote, c.hdr)
		if code != 503 || len(out) != 1 || out["ok"] != false {
			t.Fatalf("%s gets %d %v, want 503 and only ok", c.what, code, out)
		}
	}
	if code, out := probe(t, h, "[::1]:4000", nil); code != 503 || out["version"] != "v9.9.9" {
		t.Fatalf("a local probe over IPv6 gets %d %v, want the full answer", code, out)
	}
}

func TestProbeURL(t *testing.T) {
	for listen, want := range map[string]string{
		":3169":             "http://127.0.0.1:3169/healthz",
		"0.0.0.0:3169":      "http://127.0.0.1:3169/healthz",
		"[::]:8080":         "http://127.0.0.1:8080/healthz",
		"localhost:3169":    "http://127.0.0.1:3169/healthz",
		"127.0.0.1:3170":    "http://127.0.0.1:3170/healthz",
		"192.168.1.5:3169":  "http://192.168.1.5:3169/healthz",
		"[fd00::5]:3169":    "http://[fd00::5]:3169/healthz",
		"flowa.example:443": "http://flowa.example:443/healthz",
	} {
		if got := probeURL(listen); got != want {
			t.Errorf("probeURL(%q) = %q, want %q", listen, got, want)
		}
	}
}

// logLines sends requests through h and returns what the request log wrote for them, one decoded line each.
func logLines(t *testing.T, a *app, h http.Handler, reqs ...*http.Request) []map[string]any {
	t.Helper()
	var buf bytes.Buffer
	a.reqLog.mu.Lock()
	a.reqLog.out = &buf
	a.reqLog.mu.Unlock()
	for _, r := range reqs {
		h.ServeHTTP(httptest.NewRecorder(), r)
	}
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if l == "" {
			continue
		}
		m := map[string]any{}
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("a request log line is not JSON: %q", l)
		}
		m["_raw"] = l
		out = append(out, m)
	}
	return out
}

// TestRequestLog: one line per request with the method, the route's pattern, the status, the time taken
// and the bytes answered, plus what was refused when a limit said no. Never a path, a query or a body.
func TestRequestLog(t *testing.T) {
	db, err := sql.Open("postgres", "postgres://x:x@127.0.0.1:1/x?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a := newApp(db, t.TempDir())
	a.hosts = []string{"example.com"}
	h := a.routes(staticOf(t, map[string]string{"app.js": "let x=1"}))
	req := func(method, path, body string, hdr map[string]string) *http.Request {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.RemoteAddr = "203.0.113.9:5000"
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		return r
	}
	one := func(what string, r *http.Request) map[string]any {
		t.Helper()
		lines := logLines(t, a, h, r)
		if len(lines) != 1 {
			t.Fatalf("%s: %d lines, want 1: %v", what, len(lines), lines)
		}
		return lines[0]
	}
	is := func(what string, l map[string]any, method, route string, status int) {
		t.Helper()
		if l["method"] != method || l["route"] != route || l["status"] != float64(status) {
			t.Fatalf("%s: got %s, want method %s, route %q, status %d", what, l["_raw"], method, route, status)
		}
		if ms, ok := l["ms"].(float64); !ok || ms < 0 || ms > 5000 {
			t.Fatalf("%s: the time taken is %v", what, l["ms"])
		}
		if _, err := time.Parse("2006-01-02T15:04:05.000Z", fmt.Sprint(l["time"])); err != nil {
			t.Fatalf("%s: the time is %v", what, l["time"])
		}
	}

	// The fields, on a request that fails: the database is down, so /healthz answers 503.
	l := one("a failed probe", req("GET", "/healthz", "", nil))
	is("a failed probe", l, "GET", "/healthz", 503)
	if l["bytes"] != float64(len("{\"ok\":false}\n")) {
		t.Fatalf("a failed probe: bytes %v, want the length of the answer", l["bytes"])
	}
	if _, has := l["refused"]; has {
		t.Fatalf("a request no limit refused says refused: %s", l["_raw"])
	}

	// The pattern, never the path: no name, no id, no query string, no body.
	l = one("a route with a name in it", req("GET", "/api/previews/secret-name.jpg?token=hunter2", "", nil))
	is("a route with a name in it", l, "GET", "/api/previews/{file}", 401)
	l = one("a cross-origin write", req("POST", "/api/signin", `{"username":"sandy","password":"hunter2"}`,
		map[string]string{"Origin": "https://attacker.example", "Content-Type": "application/json"}))
	is("a cross-origin write", l, "POST", "/api/signin", 403)
	for _, secret := range []string{"secret-name", "hunter2", "sandy", "attacker", "203.0.113.9"} {
		for _, r := range []*http.Request{
			req("GET", "/api/previews/secret-name.jpg?token=hunter2", "", map[string]string{"Cookie": "flowa=hunter2", "User-Agent": "attacker"}),
			req("POST", "/api/signin?next=secret-name", `{"username":"sandy","password":"hunter2"}`, map[string]string{"Origin": "https://attacker.example"}),
			req("GET", "/secret-name/hunter2", "", nil),
		} {
			if l := one("a request with things to keep out of the log", r); strings.Contains(fmt.Sprint(l["_raw"]), secret) {
				t.Fatalf("the log line carries %q: %s", secret, l["_raw"])
			}
		}
	}

	// A request turned away before the routes are reached still says where it was going.
	r := req("GET", "/api/me", "", nil)
	r.Host = "flowa.attacker.example"
	is("a foreign Host", one("a foreign Host", r), "GET", "/api/me", 421)
	// A method no route uses is a word the visitor chose: it is not written out.
	l = one("an invented method", req("BREW", "/api/me", "", nil))
	if l["method"] != "other" || l["route"] != "" || strings.Contains(fmt.Sprint(l["_raw"]), "BREW") {
		t.Fatalf("an invented method: got %s, want method other and no route", l["_raw"])
	}
	is("HEAD on a GET route", one("HEAD on a GET route", req("HEAD", "/api/previews/x.jpg", "", nil)), "HEAD", "/api/previews/{file}", 401)

	// A refusal names itself on the request's line. Thirty failed sign-ins from one address use up its allowance.
	for i := 0; i < 30; i++ {
		a.lim.signinIP.add("203.0.113.9")
	}
	l = one("a refused sign-in", req("POST", "/api/signin", `{"username":"sandy","password":"x"}`, map[string]string{"Content-Type": "application/json"}))
	is("a refused sign-in", l, "POST", "/api/signin", 429)
	if l["refused"] != "too many sign-in attempts from 203.0.113.9" {
		t.Fatalf("a refused sign-in: refused is %v", l["refused"])
	}

	// Lines that say nothing are left out: a page file served, and a file not changed.
	if lines := logLines(t, a, h, req("GET", "/app.js", "", nil), req("GET", "/app.js", "", map[string]string{"If-None-Match": `"e"`})); len(lines) != 0 {
		t.Fatalf("page files served without an error were logged: %v", lines)
	}
	is("a page file that is not there", one("a page file that is not there", req("GET", "/nothing.js", "", nil)), "GET", "/", 404)

	// A minute's lines run out; what came after is counted, and the count is written when the next minute begins.
	a.reqLog.mu.Lock()
	a.reqLog.since, a.reqLog.n, a.reqLog.dropped = time.Now(), maxRequestLines-1, 0
	a.reqLog.mu.Unlock()
	if lines := logLines(t, a, h, req("GET", "/nothing.js", "", nil), req("GET", "/nothing.js", "", nil), req("GET", "/nothing.js", "", nil)); len(lines) != 1 {
		t.Fatalf("with one line left in the minute, three requests wrote %d lines, want 1", len(lines))
	}
	a.reqLog.mu.Lock()
	a.reqLog.since = a.reqLog.since.Add(-2 * time.Minute)
	a.reqLog.mu.Unlock()
	lines := logLines(t, a, h, req("GET", "/nothing.js", "", nil))
	if len(lines) != 2 || lines[0]["unlisted"] != float64(2) || lines[0]["since"] == nil || lines[1]["route"] != "/" {
		t.Fatalf("after a full minute: got %v, want a line counting 2 unlisted requests and then the request's own", lines)
	}
}

// TestRequestLogHandlers covers what a handler can do that the routes of TestRequestLog do not: write
// nothing, panic, and refuse through a writer that is wrapped.
func TestRequestLogHandlers(t *testing.T) {
	a := newApp(nil, t.TempDir())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /quiet/{id}", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("GET /boom", func(http.ResponseWriter, *http.Request) { panic("boom") })
	mux.HandleFunc("GET /late", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte("12345"))
		w.WriteHeader(http.StatusTeapot) // a second status is not the one that was sent
		w.Write([]byte("678"))
	})
	mux.HandleFunc("POST /wrapped", func(w http.ResponseWriter, _ *http.Request) {
		rw := &refunding{ResponseWriter: w, l: a.lim.posts, key: "7"}
		logRefusal(rw, "too many test posts", "by member 7")
		fail(rw, http.StatusTooManyRequests, "no")
	})
	h := a.logged(mux, mux)
	get := func(method, path string) map[string]any {
		t.Helper()
		lines := logLines(t, a, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() { recover() }() // net/http does this for a real connection
			h.ServeHTTP(w, r)
		}), httptest.NewRequest(method, path, nil))
		if len(lines) != 1 {
			t.Fatalf("%s %s: %d lines, want 1", method, path, len(lines))
		}
		return lines[0]
	}
	if l := get("GET", "/quiet/42"); l["status"] != float64(200) || l["bytes"] != float64(0) || l["route"] != "/quiet/{id}" {
		t.Fatalf("a handler that writes nothing: %s", l["_raw"])
	}
	if l := get("GET", "/boom"); l["status"] != float64(500) || l["route"] != "/boom" {
		t.Fatalf("a handler that panics: %s", l["_raw"])
	}
	if l := get("GET", "/late"); l["status"] != float64(202) || l["bytes"] != float64(8) {
		t.Fatalf("a handler that sets the status twice: %s", l["_raw"])
	}
	if l := get("POST", "/wrapped"); l["status"] != float64(429) || l["refused"] != "too many test posts by member 7" {
		t.Fatalf("a refusal through a wrapped writer: %s", l["_raw"])
	}
}

// FLOWA_COUNTS_TOKEN is the one thing between anyone and the shared counts, so a short one stops the
// server at start, and the message does not repeat it.
func TestCountsToken(t *testing.T) {
	long := strings.Repeat("ab12", 8) // 32 characters
	for _, c := range []struct {
		name, in, want string
		ok             bool
	}{
		{"unset", "", "", true},
		{"only spaces", "   ", "", true},
		{"32 characters", long, long, true},
		{"spaces around it", "  " + long + "\n", long, true},
		{"longer", long + long, long + long, true},
		{"31 characters", long[:31], "", false},
		{"a word", "secret", "", false},
		{"a space inside", long[:16] + " " + long[16:], "", false},
	} {
		got, err := countsToken(c.in)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("%s: got %q, %v; want %q, ok %v", c.name, got, err, c.want, c.ok)
		}
		if err != nil && strings.TrimSpace(c.in) != "" && strings.Contains(err.Error(), strings.TrimSpace(c.in)) {
			t.Errorf("%s: the message repeats the token: %v", c.name, err)
		}
	}
}
