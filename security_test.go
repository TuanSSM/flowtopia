package main

// Unit tests for the hardening in docs/hardening-plan.md that needs no database.

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net"
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
	// A full limiter drops expired keys, and refuses new keys while every slot is live.
	small := newLimiter(5, time.Hour)
	small.maxKeys = 2
	small.add("x")
	small.add("y")
	if small.add("z") {
		t.Fatal("a new key in a full limiter counts as over")
	}
	small.m["x"].reset = time.Now().Add(-time.Second)
	if !small.add("z") || len(small.m) != 2 {
		t.Fatal("expired keys make room")
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
