package main

// The link preview fetcher against a stub server: what it fetches, what it refuses.

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// pngBytes makes a w by h picture.
func pngBytes(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 120, 255})
		}
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

// stub answers as the three sites would, under /<host><path>, and counts the requests it gets.
func stub(t *testing.T) (*httptest.Server, *int64) {
	var n int64
	thumb := pngBytes(1280, 720)
	mux := http.NewServeMux()
	mux.HandleFunc("/www.youtube.com/oembed", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&n, 1)
		w.Write([]byte(`{"title":"Figure-8 drill","author_name":"Flowa","thumbnail_url":"https://i.ytimg.com/vi/x/hqdefault.jpg"}`))
	})
	mux.HandleFunc("/i.ytimg.com/vi/x/hqdefault.jpg", func(w http.ResponseWriter, r *http.Request) { atomic.AddInt64(&n, 1); w.Write(thumb) })
	mux.HandleFunc("/www.tiktok.com/oembed", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&n, 1)
		if strings.Contains(r.URL.Query().Get("url"), "9999999999") {
			time.Sleep(5 * time.Second) // slower than the fetcher waits
		}
		if strings.Contains(r.URL.Query().Get("url"), "8888888888") {
			w.Write(bytes.Repeat([]byte("x"), 300<<10)) // larger than it reads
			return
		}
		w.Write([]byte(`{"title":"Poi flowers","author_name":"sam","thumbnail_url":"https://p16.tiktokcdn.com/thumb.png"}`))
	})
	mux.HandleFunc("/p16.tiktokcdn.com/thumb.png", func(w http.ResponseWriter, r *http.Request) { w.Write(pngBytes(300, 400)) })
	var srv *httptest.Server
	mux.HandleFunc("/vm.tiktok.com/ZMgood123/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv.URL+"/www.tiktok.com/@sam/video/1234567890123", http.StatusFound)
	})
	mux.HandleFunc("/www.tiktok.com/@sam/video/1234567890123", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<html></html>")) })
	mux.HandleFunc("/vm.tiktok.com/ZMevil123/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv.URL+"/evil.example/steal", http.StatusFound)
	})
	mux.HandleFunc("/www.instagram.com/reel/C9xYz_-AbCd/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><head><meta property="og:title" content="A reel &amp; more"><meta property="og:image" content="https://scontent.cdninstagram.com/x.png"></head></html>`))
	})
	mux.HandleFunc("/scontent.cdninstagram.com/x.png", func(w http.ResponseWriter, r *http.Request) { w.Write(pngBytes(640, 1136)) })
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &n
}

func TestFetcherLookups(t *testing.T) {
	srv, _ := stub(t)
	f := newFetcher(srv.URL, true)
	ctx := context.Background()
	l, _ := parseLink("https://youtu.be/dQw4w9WgXcQ")
	m, _, err := f.lookup(ctx, l)
	if err != nil || m.Title != "Figure-8 drill" || m.Author != "Flowa" {
		t.Fatalf("youtube: %+v %v", m, err)
	}
	raw, _, err := f.get(ctx, m.Thumb, thumbHosts, maxThumbIn)
	if err != nil {
		t.Fatal(err)
	}
	jpg, w, h, err := shrinkThumb(raw)
	if err != nil || w != 640 || h != 360 || len(jpg) > maxThumbOut || !bytes.HasPrefix(jpg, []byte{0xFF, 0xD8}) {
		t.Fatalf("thumbnail: %dx%d, %d bytes, %v", w, h, len(jpg), err)
	}
	// A short link is followed to its video, within the allowed hosts.
	sl, _ := parseLink("https://vm.tiktok.com/ZMgood123/")
	m, resolved, err := f.lookup(ctx, sl)
	if err != nil || resolved.URL != "https://www.tiktok.com/@sam/video/1234567890123" || m.Title != "Poi flowers" {
		t.Fatalf("short link: %+v %+v %v", m, resolved, err)
	}
	// Instagram: Open Graph tags from the page.
	ig, _ := parseLink("https://www.instagram.com/reel/C9xYz_-AbCd/")
	if m, _, err := f.lookup(ctx, ig); err != nil || m.Title != "A reel & more" || !strings.HasSuffix(m.Thumb, "/x.png") {
		t.Fatalf("instagram: %+v %v", m, err)
	}
}

func TestFetcherRefusals(t *testing.T) {
	srv, _ := stub(t)
	f := newFetcher(srv.URL, true)
	ctx := context.Background()
	// A redirect off the allowed hosts is refused.
	evil, _ := parseLink("https://vm.tiktok.com/ZMevil123/")
	if _, _, err := f.lookup(ctx, evil); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("redirect off the allowlist: %v", err)
	}
	// Slow answers time out at about 3 s; large ones are refused.
	slow, _ := parseLink("https://www.tiktok.com/@a/video/9999999999")
	start := time.Now()
	if _, _, err := f.lookup(ctx, slow); err == nil || time.Since(start) > 4500*time.Millisecond {
		t.Fatalf("timeout: %v after %v", err, time.Since(start))
	}
	big, _ := parseLink("https://www.tiktok.com/@a/video/8888888888")
	if _, _, err := f.lookup(ctx, big); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("oversize: %v", err)
	}
	// Only allowed hosts, only https.
	if _, _, err := f.get(ctx, "https://example.com/x", previewHosts, maxMeta); err == nil {
		t.Fatal("a host off the allowlist was fetched")
	}
	if _, _, err := f.get(ctx, "http://www.youtube.com/oembed", previewHosts, maxMeta); err == nil {
		t.Fatal("plain http was fetched")
	}
	// Without the test override, private and loopback addresses are refused at connect time.
	real := newFetcher("", false)
	tr := real.client.Transport.(*http.Transport)
	for _, addr := range []string{"127.0.0.1:80", "10.0.0.1:443", "192.168.1.10:443", "[::1]:443", "169.254.169.254:80"} {
		if c, err := tr.DialContext(ctx, "tcp", addr); err == nil {
			c.Close()
			t.Errorf("%s: connected, should be refused", addr)
		} else if !strings.Contains(err.Error(), "refusing") {
			t.Errorf("%s: %v", addr, err)
		}
	}
}
