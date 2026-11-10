package main

// Unit tests for the hardening in docs/hardening-plan.md that needs no database.

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
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
	const visitor, proxy = "203.0.113.9:5000", "172.18.0.2:5000"
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
