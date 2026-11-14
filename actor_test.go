package main

// The actor documents, the Notes, the public pages and their pictures (docs/federation.md, F16, 5.5, 5.6,
// 6.5, 6.7 and 9.4). The tests without DB in their name need no database; the others ask through the whole
// server, as another server or a browser would.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// The Accept header decides between the ActivityStreams document and the HTML page (RFC 9110 12.5.1,
// ActivityPub 3.2, federation.md 5.6).
func TestContentNegotiation(t *testing.T) {
	const ld = `application/ld+json; profile="https://www.w3.org/ns/activitystreams"`
	for _, c := range []struct {
		accept []string // the request's Accept lines; nil for none
		want   bool     // true: the document
	}{
		{nil, false},
		{[]string{""}, false},
		{[]string{"application/activity+json"}, true},
		{[]string{ld}, true},
		{[]string{"application/ld+json"}, true}, // with or without the profile (5.6)
		{[]string{`application/ld+json;profile="https://www.w3.org/ns/activitystreams"`}, true},
		{[]string{`Application/LD+JSON; PROFILE="https://www.w3.org/ns/activitystreams"`}, true},
		{[]string{`application/ld+json; q=0.9; profile="https://www.w3.org/ns/activitystreams"`}, true}, // what follows q is not the type's
		{[]string{`application/ld+json; charset=utf-8; profile="https://www.w3.org/ns/activitystreams"`}, true},
		{[]string{`application/ld+json; profile="https://example.com/a https://www.w3.org/ns/activitystreams"`}, true},
		{[]string{`application/ld+json; profile="https://example.com/other"`}, false},
		{[]string{`application/ld+json; profile=https://www.w3.org/ns/activitystreams`}, false},        // a URI is not a token: the element does not parse
		{[]string{`application/ld+json; profile="https://www.w3.org/ns/activitystreams`}, false},       // an unclosed quote
		{[]string{`application/activity+json; profile="https://www.w3.org/ns/activitystreams"`}, true}, // a profile on activity+json changes nothing
		{[]string{`application/activity+json; profile="https://example.com/other"`}, true},
		{[]string{`application/activity+json; profile="https://example.com/other", text/html;q=0.5`}, true},
		{[]string{"application/activity+json, application/ld+json"}, true}, // what Mastodon sends
		{[]string{`application/activity+json, ` + ld + `, text/html;q=0.1`}, true},
		{[]string{"*/*"}, false},
		{[]string{"application/*"}, true}, // text/html is not acceptable, a JSON type is
		{[]string{"application/activity+json, */*"}, true},
		{[]string{"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"}, false}, // a browser
		{[]string{"text/html;q=0, application/activity+json"}, true},
		{[]string{"text/html, application/activity+json;q=0"}, false},
		{[]string{"application/activity+json;q=0"}, false},
		{[]string{"application/activity+json;q=0.5, text/html;q=0.4"}, true},
		{[]string{"application/activity+json;q=0.4, text/html;q=0.5"}, false},
		{[]string{"application/activity+json;q=0.5, text/html;q=0.5"}, false}, // equal and as specific: the page
		{[]string{"application/activity+json;q=1.0"}, true},
		{[]string{"application/activity+json;Q=0.7, text/html;q=0.5"}, true},
		{[]string{"application/activity+json;q=1.5"}, false}, // a weight that does not parse drops the element
		{[]string{"application/activity+json;q=0.1234"}, false},
		{[]string{"application/activity+json;q=-1"}, false},
		{[]string{"application/activity+json;q="}, false},
		{[]string{"application/json"}, false},
		{[]string{"image/png"}, false},
		{[]string{"text/html", "application/activity+json;q=0.5"}, false}, // two lines are one list
		{[]string{"text/html;q=0.1", "application/activity+json"}, true},
		{[]string{"*/activity+json"}, false},
		{[]string{"application/activity+json;;"}, false},
		{[]string{"application/activity+json garbage"}, false},
		{[]string{",,,application/activity+json,,"}, true},
		{[]string{strings.Repeat("x/y, ", 63) + "application/activity+json"}, true},
		{[]string{strings.Repeat("x/y, ", 64) + "application/activity+json"}, false},      // more than 64 elements: no header
		{[]string{"application/activity+json, " + strings.Repeat("a", acceptMax)}, false}, // too long: no header
		{[]string{`application/activity+json; x="a\"b,c"`}, true},                         // an escaped quote and a comma inside a quoted string
	} {
		h := http.Header{}
		for _, v := range c.accept {
			h.Add("Accept", v)
		}
		if got := wantsActivity(h); got != c.want {
			t.Errorf("Accept %q: the document %v, want %v", c.accept, got, c.want)
		}
	}
}

// FuzzAccept: no Accept header makes the reader panic.
func FuzzAccept(f *testing.F) {
	for _, s := range []string{"application/activity+json", `application/ld+json; profile="https://www.w3.org/ns/activitystreams"`, "text/html;q=0.9, */*", `a/b; x="\"`, ";;,,\"\\"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		wantsActivity(http.Header{"Accept": {s}})
	})
}

// The content of a Note: escaped text, paragraphs and line breaks, links with their rel, nothing else.
func TestNoteHTML(t *testing.T) {
	for _, c := range []struct {
		body string
		more []string
		want string
	}{
		{"", nil, ""},
		{"hello", nil, "<p>hello</p>"},
		{"a\nb\n\n\nc", nil, "<p>a<br>b</p><p>c</p>"},
		{`<script>alert("x")</script> & 'q'`, nil, `<p>&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt; &amp; &#39;q&#39;</p>`},
		{"see https://example.com/a?b=1&c=2.", nil, `<p>see <a href="https://example.com/a?b=1&amp;c=2" rel="nofollow noopener">https://example.com/a?b=1&amp;c=2</a>.</p>`},
		{"javascript:alert(1) http://x.example/\"><b>", nil, `<p>javascript:alert(1) <a href="http://x.example/" rel="nofollow noopener">http://x.example/</a>&#34;&gt;&lt;b&gt;</p>`},
		{"x", []string{"Tricks: A (Poi)"}, "<p>x</p><p>Tricks: A (Poi)</p>"},
		{"\u202eabc", nil, "<p>\u202eabc</p>"}, // escaping only: the page shows a post's text as the app does
	} {
		if got := noteHTML(c.body, c.more); got != c.want {
			t.Errorf("noteHTML(%q) = %q, want %q", c.body, got, c.want)
		}
	}
}

// cavageKeyPEM is the public key of draft-cavage-http-signatures-12, Appendix C
// (testdata/federation/vectors/cavage12-test-key.txt): a fixed key, so the golden files hold still.
const cavageKeyPEM = "-----BEGIN PUBLIC KEY-----\nMIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQDCFENGw33yGihy92pDjZQhl0C3\n6rPJj+CvfSC8+q28hxA161QFNUd13wuCTUcq0Qd2qsBe/2hFyc2DCJJg0h1L78+6\nZ4UMR7EOcpfdUE9Hf3m/hs+FUR45uBJeDK1HSFHD8bHKD6kv8FPGfJTotc+2xjJw\noYi+1hqp1fIekaxsyQIDAQAB\n-----END PUBLIC KEY-----\n"

const (
	apAccept = "application/activity+json"
	ldAccept = `application/ld+json; profile="https://www.w3.org/ns/activitystreams"`
	browser  = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"
)

// jpegData is a small JPEG as a data URL, as cleanImage stores a post's picture.
func jpegData(t testing.TB, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = byte(i * 7)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

// pubFixture is a federated instance with members in every case the public surfaces must tell apart:
// ana (opted in, a key, a Public post with a picture, a chip and a combo, a This server post, a hidden
// Public post, a milestone), bob (never opted in, a Public post left from before), cyd (opted in,
// suspended), dee (opted in once, now out), and the retired name eve.
type pubFixture struct {
	e                                                     *tenv
	ana, bob, cyd, dee                                    int64
	anaTok                                                string
	pub, local, hidden, milestone, bobPub, cydPub, deePub int64
}

func newPubFixture(t *testing.T) *pubFixture {
	e := newEnv(t)
	if err := e.fedStart(nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the instance actor's key", func() bool { return e.count(`SELECT count(*) FROM ap_keys WHERE user_id IS NULL`) == 1 })
	f := &pubFixture{e: e}
	f.ana, f.anaTok = e.user("ana", "user")
	f.bob, _ = e.user("bob", "user")
	f.cyd, _ = e.user("cyd", "user")
	f.dee, _ = e.user("dee", "user")
	e.mustExec(`UPDATE users SET display_name = 'Ana', bio = $2, links = '{"instagram":"ana.flow"}', props = '["poi","staff"]',
		created_at = '2026-11-02T18:00:00Z' WHERE id = $1`, f.ana, "Poi and staff, mostly in the park.\n\nSay hi: https://example.com/ana & <friends>")
	e.mustExec(`UPDATE users SET federates = true WHERE id IN ($1, $2, $3)`, f.ana, f.cyd, f.dee)
	e.mustExec(`UPDATE users SET avatar = $1`, pngData(t))
	e.mustExec(`UPDATE users SET suspended_until = now() + interval '1 day' WHERE id = $1`, f.cyd)
	for _, id := range []int64{f.ana, f.cyd, f.dee} {
		e.mustExec(`INSERT INTO ap_keys (user_id, key_id, public_key, private_sealed) SELECT id, '/users/' || username || '#main-key', $2, 'v1.x' FROM users WHERE id = $1`, id, cavageKeyPEM)
	}
	e.mustExec(`UPDATE users SET federates = false WHERE id = $1`, f.dee)
	e.mustExec(`INSERT INTO retired_usernames (username) VALUES ('eve')`)
	pic := jpegData(t, 32, 24)
	post := func(uid int64, kind, audience, body string, hidden bool) int64 {
		var id int64
		data := `{"tags":[{"prop":"nunchaku","skill":"F7"}],"combo":{"id":"c1","name":"Morning","role":"","prop":"nunchaku","seq":["F7","F5"],"reps":[1,1]}}`
		if err := e.a.db.QueryRow(`INSERT INTO posts (user_id, kind, body, image, data, audience, created_at, hidden_at)
			VALUES ($1, $2, $3, $4, $5, $6, '2026-11-12T18:04:05Z', CASE WHEN $7 THEN now() END) RETURNING id`,
			uid, kind, body, pic, data, audience, hidden).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	f.pub = post(f.ana, "post", "public", "Finally clean! <b>Thanks</b> & see https://example.com/drill.\n\nSecond line", false)
	f.local = post(f.ana, "post", "local", "only here", false)
	f.hidden = post(f.ana, "post", "public", "hidden by a moderator", true)
	f.milestone = post(f.ana, "milestone", "public", "", false)
	f.bobPub = post(f.bob, "post", "public", "bob never opted in", false)
	f.cydPub = post(f.cyd, "post", "public", "cyd is suspended", false)
	f.deePub = post(f.dee, "post", "public", "dee opted out", false)
	e.h = e.a.routes(staticOf(t, map[string]string{"app.css": "body{}", "icon.svg": "<svg/>"}))
	return f
}

// golden compares a JSON answer, indented, with a file under testdata/federation; UPDATE_GOLDEN=1 writes it.
func golden(t *testing.T, file string, body []byte) {
	t.Helper()
	var ind bytes.Buffer
	if err := json.Indent(&ind, body, "", "  "); err != nil {
		t.Fatalf("%s: not JSON: %v\n%s", file, err, body)
	}
	ind.WriteByte('\n')
	path := "testdata/federation/" + file
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, ind.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ind.Bytes(), want) {
		t.Errorf("%s differs from the golden file:\n%s", file, ind.Bytes())
	}
}

// strictShape reads a document as a strict reader would: no name twice in any object, every type a
// single non-empty string, and every id an https URL.
func strictShape(t *testing.T, what string, b []byte) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(b))
	var walk func() error
	walk = func() error {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch tok {
		case json.Delim('{'):
			seen := map[string]bool{}
			for dec.More() {
				k, err := dec.Token()
				if err != nil {
					return err
				}
				name := k.(string)
				if seen[name] {
					return fmt.Errorf("the name %q twice", name)
				}
				seen[name] = true
				if name == "id" || name == "type" {
					v, err := dec.Token()
					if err != nil {
						return err
					}
					s, ok := v.(string)
					if !ok || name == "id" && !strings.HasPrefix(s, "https://") || s == "" {
						return fmt.Errorf("%s is %v", name, v)
					}
					continue
				}
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = dec.Token()
			return err
		case json.Delim('['):
			for dec.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = dec.Token()
			return err
		}
		return nil
	}
	if err := walk(); err != nil {
		t.Errorf("%s: %v", what, err)
	}
}

// The actor documents and the Note against golden files: the properties and the @context of 5.5 (the
// member's key, indexable false), the instance actor of 5.5, and the Note of 6.5 as far as this version
// serves it; each with the content type of 5.6, Vary: Accept, a validator and its 304; and a shape a strict
// reader takes. The member's document froze the origin before it went out; the instance actor's did not.
func TestActorDocument(t *testing.T) {
	f := newPubFixture(t)
	e := f.e
	e.mustExec(`UPDATE ap_keys SET public_key = $1 WHERE user_id IS NULL`, cavageKeyPEM)
	e.a.fed.dropActor(instanceActor)
	for _, accept := range []string{apAccept, ldAccept} {
		w := pubAsk(e, "GET", "/actor", accept)
		golden(t, "actor-instance.json", w.Body.Bytes())
	}
	if e.count(`SELECT count(*) FROM instance_settings WHERE key = 'origin'`) != 0 {
		t.Error("the instance actor's document froze the origin")
	}
	for _, c := range []struct{ path, file string }{
		{"/actor", "actor-instance.json"},
		{"/users/ana", "actor-ana.json"},
		{fmt.Sprintf("/users/ana/posts/%d", f.pub), "note-ana.json"},
	} {
		for _, accept := range []string{apAccept, ldAccept} {
			w := pubAsk(e, "GET", c.path, accept)
			h := w.Result().Header
			if w.Code != 200 || h.Get("Content-Type") != activityType || h.Get("Vary") != "Accept" || h.Get("Cache-Control") != "public, max-age=180" || h.Get("ETag") == "" {
				t.Fatalf("%s (%s): %d %v %s", c.path, accept, w.Code, h, w.Body)
			}
			golden(t, c.file, w.Body.Bytes())
			strictShape(t, c.path, w.Body.Bytes())
			if again := ask(e.h, "GET", c.path, "", map[string]string{"Accept": accept, "If-None-Match": h.Get("ETag")}); again.Code != 304 || again.Body.Len() != 0 {
				t.Errorf("%s with its ETag: %d", c.path, again.Code)
			}
		}
	}
	if e.count(`SELECT count(*) FROM instance_settings WHERE key = 'origin'`) != 1 {
		t.Error("the origin is not recorded after a member's actor document went out")
	}
	// Under a moved origin the member's document is not given out: the 404, and federation refused.
	f2 := newPubFixture(t)
	f2.e.mustExec(`INSERT INTO instance_settings (key, value) VALUES ('origin', '"https://elsewhere.example.org"'), ('first_federated_at', to_jsonb(now()))`)
	if w := pubAsk(f2.e, "GET", "/users/ana", apAccept); w.Code != 404 || f2.e.a.federating() {
		t.Errorf("under a moved origin: %d, federating %v", w.Code, f2.e.a.federating())
	}
}

// pubAsk asks an address as a visitor: with the Accept given, from an address of its own.
func pubAsk(e *tenv, method, path, accept string) *httptest.ResponseRecorder {
	hdr := map[string]string{}
	if accept != "" {
		hdr["Accept"] = accept
	}
	return ask(e.h, method, path, "198.51.100.20:4000", hdr)
}

// Every public address refuses what is not public with one 404 per representation: the same status,
// bytes and headers for a This server post, a hidden one, a milestone, a post or a member of someone who
// never opted in, opted out or is suspended, an id or a name nobody has, another spelling of an id, and
// anything while federation is refused. A retired name is the one 410. No other picture is reachable.
func TestDBPublicSurfaces(t *testing.T) {
	f := newPubFixture(t)
	e := f.e
	post := func(name string, id int64) string { return fmt.Sprintf("/users/%s/posts/%d", name, id) }
	shown := []string{"/users/ana", post("ana", f.pub)}
	hiddenPages := []string{
		"/users/bob", "/users/cyd", "/users/dee", "/users/nobody", "/users/Ana", "/users/%61na", "/users/a",
		post("ana", f.local), post("ana", f.hidden), post("ana", f.milestone), post("ana", 999999), post("bob", f.pub),
		post("bob", f.bobPub), post("cyd", f.cydPub), post("dee", f.deePub), post("nobody", f.pub), post("eve", f.pub),
		fmt.Sprintf("/users/ana/posts/0%d", f.pub), fmt.Sprintf("/users/ana/posts/+%d", f.pub), "/users/ana/posts/x", "/users/ana/posts/-1",
	}
	pageOnly := []string{"/users/ana?before=x", "/users/ana?before=1&before=2", "/users/ana?before=0"}
	type answer struct {
		code int
		body string
		head http.Header
	}
	of := func(w *httptest.ResponseRecorder) answer {
		return answer{w.Code, w.Body.String(), w.Result().Header.Clone()}
	}
	same := func(a, b answer) bool { return a.code == b.code && a.body == b.body && headersEqual(a.head, b.head) }
	for _, accept := range []string{apAccept, ldAccept, browser, ""} {
		want := of(pubAsk(e, "GET", "/users/nobody", accept))
		if want.code != 404 || want.head.Get("Vary") != "Accept" || want.head.Get("Cache-Control") != "public, max-age=60" {
			t.Fatalf("Accept %q, a name nobody has: %+v", accept, want)
		}
		list := hiddenPages
		if !wantsActivity(http.Header{"Accept": {accept}}) {
			list = append(list, pageOnly...) // the actor's document takes no query
		}
		for _, p := range list {
			if got := of(pubAsk(e, "GET", p, accept)); !same(got, want) {
				t.Errorf("Accept %q: GET %s answers %d %q %v, a name nobody has %d %q %v", accept, p, got.code, got.body, got.head, want.code, want.body, want.head)
			}
		}
		for _, p := range shown {
			if w := pubAsk(e, "GET", p, accept); w.Code != 200 {
				t.Errorf("Accept %q: GET %s answers %d", accept, p, w.Code)
			}
		}
	}
	// A retired name: 410, a Tombstone or a page that says so.
	if w := pubAsk(e, "GET", "/users/eve", apAccept); w.Code != 410 || w.Body.String() != `{"@context":"https://www.w3.org/ns/activitystreams","id":"https://flowa.example.org/users/eve","type":"Tombstone","formerType":"Person"}` || w.Result().Header.Get("Vary") != "Accept" {
		t.Errorf("a retired name as a document: %d %s", w.Code, w.Body)
	}
	if w := pubAsk(e, "GET", "/users/eve", browser); w.Code != 410 || !strings.Contains(w.Body.String(), "Account deleted") {
		t.Errorf("a retired name as a page: %d", w.Code)
	}

	// The pictures: ana's, and her Public post's, under the extension of their type; nothing else, one 404.
	if w := pubAsk(e, "GET", "/users/ana/avatar.png?v=whatever", ""); w.Code != 200 || w.Result().Header.Get("Content-Type") != "image/png" || w.Result().Header.Get("Cache-Control") != "public, max-age=180" {
		t.Errorf("ana's picture: %d %v", w.Code, w.Result().Header)
	}
	if w := pubAsk(e, "GET", post("ana", f.pub)+"/picture.jpg", ""); w.Code != 200 || w.Result().Header.Get("Content-Type") != "image/jpeg" {
		t.Errorf("the Public post's picture: %d %v", w.Code, w.Result().Header)
	}
	noPic := of(pubAsk(e, "GET", "/users/nobody/avatar.png", ""))
	if noPic.code != 404 {
		t.Fatalf("a picture nobody has: %+v", noPic)
	}
	hiddenPics := []string{"/users/ana/avatar.jpg", post("ana", f.pub) + "/picture.png", "/users/Ana/avatar.png", "/users/%61na/avatar.png"}
	for _, name := range []string{"bob", "cyd", "dee", "eve"} {
		hiddenPics = append(hiddenPics, "/users/"+name+"/avatar.png", "/users/"+name+"/avatar.jpg")
	}
	for _, p := range hiddenPics {
		if got := of(pubAsk(e, "GET", p, "")); !same(got, noPic) {
			t.Errorf("GET %s answers %d %q, a picture nobody has %d %q", p, got.code, got.body, noPic.code, noPic.body)
		}
	}
	// Every post id there is, under every name, in both extensions: only ana's one Public post answers.
	rows, err := e.a.db.Query(`SELECT id FROM posts ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	var all []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		all = append(all, id)
	}
	rows.Close()
	for _, id := range append(all, 0, 999999) {
		for _, name := range []string{"ana", "bob", "cyd", "dee", "eve"} {
			for _, ext := range []string{"jpg", "png"} {
				w := pubAsk(e, "GET", post(name, id)+"/picture."+ext, "")
				if open := name == "ana" && id == f.pub && ext == "jpg"; open != (w.Code == 200) || !open && !same(of(w), noPic) {
					t.Errorf("the picture of post %d under %s, .%s: %d", id, name, ext, w.Code)
				}
			}
		}
	}
	// The app's own pictures stay behind a session.
	for _, p := range []string{fmt.Sprintf("/api/users/%d/avatar", f.bob), "/api/previews/x.jpg"} {
		if w := pubAsk(e, "GET", p, ""); w.Code != 401 {
			t.Errorf("GET %s without a session: %d", p, w.Code)
		}
	}

	// HEAD answers with the headers of GET; other methods 405 with Allow, before the write guard; no CORS
	// header anywhere here.
	for _, p := range append([]string{"/actor", "/users/ana/avatar.png", "/users/nobody", post("ana", f.pub) + "/picture.jpg"}, shown...) {
		get, head := pubAsk(e, "GET", p, apAccept), pubAsk(e, "HEAD", p, apAccept)
		if head.Code != get.Code || head.Result().Header.Get("Content-Type") != get.Result().Header.Get("Content-Type") {
			t.Errorf("HEAD %s: %d, GET %d", p, head.Code, get.Code)
		}
		if get.Result().Header.Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("GET %s carries a CORS header", p)
		}
		for _, m := range []string{"POST", "PUT", "DELETE", "PATCH", "OPTIONS"} {
			for _, ctype := range []string{"", "application/json", "application/activity+json"} {
				w := ask(e.h, m, p, "198.51.100.20:4000", map[string]string{"Content-Type": ctype})
				if w.Code != 405 || w.Result().Header.Get("Allow") != "GET, HEAD" || w.Result().Header.Get("Access-Control-Allow-Origin") != "" {
					t.Errorf("%s %s (%q): %d %v", m, p, ctype, w.Code, w.Result().Header)
				}
			}
		}
	}

	// The allowance per address: counted before anything is read, other methods too; a member at the
	// same address still uses the app.
	e.a.lim.public = newLimiter(3, time.Hour)
	const from = "203.0.113.77:5000"
	for i, m := range []string{"GET", "POST", "GET", "GET", "POST"} {
		w := ask(e.h, m, "/users/ana", from, nil)
		if (i < 3) == (w.Code == 429) {
			t.Errorf("request %d (%s): %d", i+1, m, w.Code)
		}
	}
	r := httptest.NewRequest("GET", "/api/me", nil)
	r.RemoteAddr = from
	r.AddCookie(&http.Cookie{Name: cookieName, Value: f.anaTok})
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("a member at an address over the allowance: %d", w.Code)
	}
	e.a.lim.public = newLimiter(1<<30, time.Hour)

	// While federation is refused, every address is the 404 (or the picture's).
	e.a.fed.refuse("federation refused: a test")
	for _, accept := range []string{apAccept, browser} {
		want := of(pubAsk(e, "GET", "/users/nobody", accept))
		for _, p := range append([]string{"/actor", "/users/eve"}, shown...) {
			if got := of(pubAsk(e, "GET", p, accept)); !same(got, want) {
				t.Errorf("refused, Accept %q: GET %s answers %d", accept, p, got.code)
			}
		}
	}
	if got := of(pubAsk(e, "GET", "/users/ana/avatar.png", "")); !same(got, noPic) {
		t.Errorf("refused: ana's picture answers %d", got.code)
	}
}

// The actor document is kept in memory and made again after every change of what it says: the name,
// the bio, the links, the props, the picture, the key and the opt-in; and a member whose key is still
// being made is told to come back, not given a document without one.
func TestDBActorCache(t *testing.T) {
	f := newPubFixture(t)
	e := f.e
	doc := func() string {
		t.Helper()
		w := pubAsk(e, "GET", "/users/ana", apAccept)
		if w.Code != 200 {
			t.Fatalf("the actor: %d %s", w.Code, w.Body)
		}
		return w.Body.String()
	}
	was := doc()
	// A change behind the server's back is not seen: the document is kept.
	e.mustExec(`UPDATE users SET display_name = 'Hand edit' WHERE id = $1`, f.ana)
	if doc() != was {
		t.Fatal("the document is not kept")
	}
	e.mustExec(`UPDATE users SET display_name = 'Ana' WHERE id = $1`, f.ana)
	for _, c := range []struct {
		what   string
		change map[string]any
		saw    string
	}{
		{"the name", map[string]any{"displayName": "Ana Flow"}, `"name":"Ana Flow"`},
		{"the bio", map[string]any{"bio": "New bio"}, `"summary":"<p>New bio</p>"`},
		{"the links", map[string]any{"links": map[string]string{"tiktok": "anaflow"}}, `"name":"TikTok"`},
		{"the props", map[string]any{"props": []string{"buugeng"}}, `"value":"Buugeng"`},
		{"the picture", map[string]any{"avatar": jpegData(t, 40, 40)}, `"mediaType":"image/jpeg"`},
	} {
		e.must(200, f.anaTok, "PUT", "/api/me", c.change)
		if got := doc(); !strings.Contains(got, c.saw) {
			t.Errorf("after a change of %s the document says: %s", c.what, got)
		}
	}
	// The key: replaced by a rotation in this process (-rotate-keys runs in a process of its own, and the
	// server is restarted after it, which forgets every document; the drop is there all the same).
	before := doc()
	if _, _, err := e.a.fed.rotateKeys(context.Background()); err != nil {
		t.Fatal(err)
	}
	if after := doc(); after == before || strings.Contains(after, cavageKeyPEM[30:80]) {
		t.Error("the document kept the key a rotation replaced")
	}
	// An opted-in member whose key is still being made is told to come back; the worker's key is in the
	// next document.
	e.mustExec(`DELETE FROM ap_keys WHERE user_id = $1`, f.ana)
	e.a.fed.dropActor(keyOwner(f.ana))
	if w := pubAsk(e, "GET", "/users/ana", apAccept); w.Code != 503 || w.Result().Header.Get("Retry-After") == "" {
		t.Errorf("an opted-in member without a key yet: %d", w.Code)
	}
	if !e.a.fed.requestKey(keyOwner(f.ana)) {
		t.Fatal("the key was not asked for")
	}
	waitFor(t, "ana's key", func() bool { return pubAsk(e, "GET", "/users/ana", apAccept).Code == 200 })
	// The opt-in: off is the 404 at once, on again the document.
	e.must(200, f.anaTok, "PUT", "/api/me", map[string]any{"federates": false})
	if w := pubAsk(e, "GET", "/users/ana", apAccept); w.Code != 404 {
		t.Errorf("after opting out: %d", w.Code)
	}
	e.must(200, f.anaTok, "PUT", "/api/me", map[string]any{"federates": true})
	if w := pubAsk(e, "GET", "/users/ana", apAccept); w.Code != 200 {
		t.Errorf("after opting in again: %d", w.Code)
	}
	// A suspension hides the kept document at once: the gate is asked every time.
	e.mustExec(`UPDATE users SET suspended_until = now() + interval '1 hour' WHERE id = $1`, f.ana)
	if w := pubAsk(e, "GET", "/users/ana", apAccept); w.Code != 404 {
		t.Errorf("after a suspension: %d", w.Code)
	}
}

// The pages people read: no script, the strict policy of 5.6, noindex, links that say they are the
// member's, pictures from this server only, a way to sign in, Public posts newest first and paged; the
// post's page; the instance actor's page; and a single-profile instance's page at /, with the owner's
// Public posts only with FLOWA_FEDERATE=on.
func TestDBPublicPages(t *testing.T) {
	f := newPubFixture(t)
	e := f.e
	for i := range pubPage + 3 {
		e.mustExec(`INSERT INTO posts (user_id, body, audience) VALUES ($1, $2, 'public')`, f.ana, fmt.Sprintf("practice %d", i))
	}
	w := pubAsk(e, "GET", "/users/ana", browser)
	page, h := w.Body.String(), w.Result().Header
	if w.Code != 200 || h.Get("Content-Type") != "text/html; charset=utf-8" || h.Get("Content-Security-Policy") != visitorCSP || h.Get("Vary") != "Accept" || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("the profile page: %d %v", w.Code, h)
	}
	for _, want := range []string{`<meta name="robots" content="noindex">`, `<h1 id="pub-name">Ana</h1>`, `@ana@flowa.example.org`, `href="/index.html#/signin"`,
		`href="https://www.instagram.com/ana.flow/" rel="nofollow noopener ugc"`, `src="/users/ana/avatar.png?v=`, `&lt;friends&gt;`,
		fmt.Sprintf(`practice %d`, pubPage+2), `href="/users/ana?before=`, `Open in Flowa`} {
		if !strings.Contains(page, want) {
			t.Errorf("the profile page has no %q", want)
		}
	}
	if strings.Contains(page, "<script") || strings.Contains(page, " on") && regexp.MustCompile(`\son[a-z]+=`).MatchString(page) {
		t.Error("the profile page has a script")
	}
	for _, src := range regexp.MustCompile(`(?:src|href)="([^"]*)"`).FindAllStringSubmatch(page, -1) {
		if u := src[1]; strings.HasPrefix(u, "http") && !strings.HasPrefix(u, "https://www.instagram.com/") && !strings.HasPrefix(u, "https://example.com/") {
			t.Errorf("the profile page links off this server: %s", u)
		}
		if strings.HasPrefix(src[0], "src") && !strings.HasPrefix(src[1], "/") {
			t.Errorf("a picture not from this server: %s", src[1])
		}
	}
	if strings.Contains(page, "only here") || strings.Contains(page, "hidden by a moderator") || strings.Contains(page, "practice 0<") {
		t.Error("the first page shows a post it should not")
	}
	// The next page: the older posts, ana's first Public post among them, and no further link.
	older := regexp.MustCompile(`href="(/users/ana\?before=\d+)"`).FindStringSubmatch(page)
	if older == nil {
		t.Fatal("no link to older posts")
	}
	next := pubAsk(e, "GET", older[1], browser).Body.String()
	if !strings.Contains(next, "practice 0") || !strings.Contains(next, "Finally clean!") || strings.Contains(next, "?before=") || strings.Contains(next, fmt.Sprintf("practice %d", pubPage+2)) {
		t.Errorf("the second page: %s", next)
	}
	// The post's page: the text, its link, its chips and combo as text, its picture from this server.
	w = pubAsk(e, "GET", fmt.Sprintf("/users/ana/posts/%d", f.pub), "")
	post := w.Body.String()
	for _, want := range []string{"Finally clean! &lt;b&gt;Thanks&lt;/b&gt; &amp; see", `<a href="https://example.com/drill" rel="nofollow noopener ugc">`, "Tricks: ", "(Nunchaku)", "Combo: Morning: ",
		fmt.Sprintf(`src="/users/ana/posts/%d/picture.jpg"`, f.pub), `alt="Picture in the post"`, `href="/users/ana"`, fmt.Sprintf(`href="/#/post/%d"`, f.pub), `<meta name="robots" content="noindex">`, "12 November 2026"} {
		if !strings.Contains(post, want) {
			t.Errorf("the post's page has no %q", want)
		}
	}
	if w.Result().Header.Get("Content-Security-Policy") != visitorCSP {
		t.Error("the post's page has another policy")
	}
	// The instance actor's page.
	if w := pubAsk(e, "GET", "/actor", browser); w.Code != 200 || !strings.Contains(w.Body.String(), "server's own account") {
		t.Errorf("the instance actor's page: %d", w.Code)
	}

	// Single mode: the owner's page at / shows their Public posts with FLOWA_FEDERATE=on, and none without.
	for _, on := range []bool{true, false} {
		s := newEnv(t)
		var err error
		if on {
			err = s.fedSingleStart(nil)
		} else {
			err = s.singleStart(map[string]string{"ADMIN_USER": "ana", "ADMIN_PASSWORD": "flowa-test-only"}, staticOf(t, nil))
		}
		if err != nil {
			t.Fatal(err)
		}
		var owner int64
		if err := s.a.db.QueryRow(`SELECT id FROM users WHERE owner`).Scan(&owner); err != nil {
			t.Fatal(err)
		}
		s.mustExec(`INSERT INTO posts (user_id, body, audience) VALUES ($1, 'public words', 'public'), ($1, 'private words', 'local')`, owner)
		s.h = s.a.routes(staticOf(t, map[string]string{"index.html": "<!doctype html><title>Flowa</title>"}))
		root := ask(s.h, "GET", "/", "", nil).Body.String()
		if strings.Contains(root, "private words") || strings.Contains(root, "public words") != on || strings.Contains(root, "Public posts") != on {
			t.Errorf("single mode, FLOWA_FEDERATE %v: the page at /: %s", on, root)
		}
		// A post made through the app shows at once: the page is made again.
		if on {
			s.must(200, s.session(owner), "POST", "/api/posts", map[string]any{"body": "just now", "audience": "public"})
			if root := ask(s.h, "GET", "/", "", nil).Body.String(); !strings.Contains(root, "just now") {
				t.Error("single mode: a new Public post is not on the page at /")
			}
		}
	}
}

// BenchmarkActorDocument is one actor document through the whole server, kept in memory (the gate's one
// query to the test database), and BenchmarkPublicPost one Note (one query and the document made).
func BenchmarkActorDocument(b *testing.B) { benchPublic(b, "/users/ana") }

func BenchmarkPublicPost(b *testing.B) { benchPublic(b, "/users/ana/posts/1") }

func benchPublic(b *testing.B, target string) {
	db := testDB(b)
	for _, q := range []string{
		`INSERT INTO users (username, display_name, pw_hash, props, federates, bio) VALUES ('ana', 'Ana', 'x', '["poi"]', true, 'Poi in the park.')`,
		`INSERT INTO ap_keys (user_id, key_id, public_key, private_sealed) VALUES (1, '/users/ana#main-key', '` + cavageKeyPEM + `', 'v1.x')`,
		`INSERT INTO posts (user_id, body, audience) VALUES (1, 'A clean figure-8, finally. https://example.com/x', 'public')`,
	} {
		if _, err := db.Exec(q); err != nil {
			b.Fatal(err)
		}
	}
	a, _ := discoveryApp(b, "https://flowa.example.org")
	a.db, a.fed.db = db, db
	a.lim.public = newLimiter(1<<30, time.Hour)
	h := a.routes(staticOf(b, nil))
	r := httptest.NewRequest("GET", target, nil)
	r.Header.Set("Accept", apAccept)
	if w := ask(h, "GET", target, "", map[string]string{"Accept": apAccept}); w.Code != 200 {
		b.Fatalf("%d %s", w.Code, w.Body)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
	}
}

// Flowa's own documents are shapes the strict reader of what other servers send accepts (federation.md 6.3):
// apScan takes each whole, decodeActor finds the key by its id, decodeNote and decodeTombstone read the rest,
// and what they read is what was written. Every golden file, and documents served for awkward members and
// posts: a name with dots, a bio and a post with characters that need escaping, a post with a picture, the
// instance actor and a retired name's Tombstone.
func TestDBOwnDocumentsDecode(t *testing.T) {
	const max = 1 << 20
	actor := func(what string, b []byte, id string) *apActor {
		t.Helper()
		if r := apScan(b); r != 0 {
			t.Fatalf("%s: apScan: %v", what, r)
		}
		a, err := decodeActor(b, max)
		if err != nil {
			t.Fatalf("%s: decodeActor: %v\n%s", what, err, b)
		}
		k, err := a.key(id + "#main-key")
		if err != nil || k.Owner != id || !strings.HasPrefix(k.PEM, "-----BEGIN PUBLIC KEY-----") || a.ID != id || a.Inbox != id+"/inbox" {
			t.Errorf("%s: %+v, key %+v %v", what, a, k, err)
		}
		return a
	}
	note := func(what string, b []byte, id, by string) *apNote {
		t.Helper()
		if r := apScan(b); r != 0 {
			t.Fatalf("%s: apScan: %v", what, r)
		}
		n, err := decodeNote(b, max)
		if err != nil {
			t.Fatalf("%s: decodeNote: %v\n%s", what, err, b)
		}
		if n.ID != id || !n.isNote() || !n.by(by) || !n.Audience.public() || n.Published.IsZero() || n.BadDate || n.Content == "" || n.Cut {
			t.Errorf("%s: %+v", what, n)
		}
		return n
	}
	const origin = "https://flowa.example.org"
	for _, c := range []struct{ file, id string }{{"actor-ana.json", origin + "/users/ana"}, {"actor-instance.json", origin + "/actor"}} {
		b, err := os.ReadFile("testdata/federation/" + c.file)
		if err != nil {
			t.Fatal(err)
		}
		actor(c.file, b, c.id)
	}
	b, err := os.ReadFile("testdata/federation/note-ana.json")
	if err != nil {
		t.Fatal(err)
	}
	if n := note("note-ana.json", b, origin+"/users/ana/posts/1", origin+"/users/ana"); len(n.Attachments) != 1 || n.Attachments[0].MediaType != "image/jpeg" {
		t.Errorf("note-ana.json: the picture: %+v", n.Attachments)
	}

	// Served documents for awkward members and posts.
	f := newPubFixture(t)
	e := f.e
	odd, _ := e.user("a.b-c_d.e", "user")
	bio := "<script>alert('x')</script> & \"quoted\" été \U0001F938\n\nhttps://example.com/?a=1&b=<2>"
	e.mustExec(`UPDATE users SET federates = true, display_name = $2, bio = $3, avatar = $4, links = '{"youtube":"@odd.one"}' WHERE id = $1`,
		odd, "O'Dd & <Co>", bio, jpegData(t, 20, 20))
	e.mustExec(`INSERT INTO ap_keys (user_id, key_id, public_key, private_sealed) VALUES ($1, '/users/a.b-c_d.e#main-key', $2, 'v1.x')`, odd, cavageKeyPEM)
	body := "</p><b>\"bold\"</b> & 'single' \\   tab\there\n\nsee https://example.com/x?y=1&z=\"2\". \U0001F525"
	var pid int64
	if err := e.a.db.QueryRow(`INSERT INTO posts (user_id, body, image, audience, data) VALUES ($1, $2, $3, 'public', '{"tags":[{"prop":"poi","skill":"F1"}]}') RETURNING id`,
		odd, body, jpegData(t, 64, 48)).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	get := func(path string, code int) []byte {
		t.Helper()
		w := pubAsk(e, "GET", path, ldAccept)
		if w.Code != code {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
		return w.Body.Bytes()
	}
	oddID := origin + "/users/a.b-c_d.e"
	a := actor("a member with dots", get("/users/a.b-c_d.e", 200), oddID)
	if a.PreferredUsername != "a.b-c_d.e" || a.Name != "O'Dd & <Co>" || !strings.Contains(a.Summary, "&lt;script&gt;") || len(a.Icon) != 1 || !strings.HasPrefix(a.Icon[0].Href, oddID+"/avatar.jpg?v=") {
		t.Errorf("a member with dots: %+v", a)
	}
	actor("ana served", get("/users/ana", 200), origin+"/users/ana")
	if a := actor("the instance actor", get("/actor", 200), origin+"/actor"); a.Kind != "Application" || a.PreferredUsername != "flowa.example.org" {
		t.Errorf("the instance actor: %+v", a)
	}
	postID := fmt.Sprintf("%s/posts/%d", oddID, pid)
	n := note("an awkward post", get(fmt.Sprintf("/users/a.b-c_d.e/posts/%d", pid), 200), postID, oddID)
	// The text escaped, and the link ending at the quote, as a link in a post's text does.
	if !strings.Contains(n.Content, "&lt;b&gt;&#34;bold&#34;&lt;/b&gt;") || !strings.Contains(n.Content, `<a href="https://example.com/x?y=1&amp;z=" rel="nofollow noopener">`) ||
		len(n.Attachments) != 1 || n.Attachments[0].Kind != "Image" || len(n.Attachments[0].URL) != 1 || n.Attachments[0].URL[0].Href != postID+"/picture.jpg" {
		t.Errorf("an awkward post: %+v", n)
	}
	note("ana's Public post served", get(fmt.Sprintf("/users/ana/posts/%d", f.pub), 200), fmt.Sprintf("%s/users/ana/posts/%d", origin, f.pub), origin+"/users/ana")
	tb := get("/users/eve", 410)
	if r := apScan(tb); r != 0 {
		t.Fatalf("the Tombstone: apScan: %v", r)
	}
	if ts, err := decodeTombstone(tb, max); err != nil || ts.ID != origin+"/users/eve" || len(ts.FormerType) != 1 || ts.FormerType[0] != "Person" || ts.BadDate {
		t.Errorf("the Tombstone: %+v %v", ts, err)
	}
}

// A reason reads as what it is: zero is apScan's answer for a document it accepts, never a refusal, and a
// value outside the list reads as a refusal.
func TestAPReasonString(t *testing.T) {
	if got := apReason(0).String(); got != "accepted" {
		t.Errorf("apReason(0) = %q", got)
	}
	if got := apDuplicate.String(); got != "a member named twice in one object" {
		t.Errorf("apDuplicate = %q", got)
	}
	if got := apReason(250).String(); got != "refused" {
		t.Errorf("apReason(250) = %q", got)
	}
}

// No page shows a post it must not: a hidden one, a removed (deleted) one, a This server one or a
// milestone, wherever it falls among the pages. Such posts are mixed among the Public ones from the newest
// to the oldest, and every page of the profile is walked by its cursor, and single mode's / and the pages
// after it: each Public post shows exactly once, newest first, and none of the others ever does.
func TestDBPublicPagesHidden(t *testing.T) {
	type made struct {
		public []string // the bodies that must show, oldest first
		secret []string // the bodies that must never show
	}
	fill := func(e *tenv, uid int64, tok string) made {
		var m made
		for i := range 72 { // 24 Public, so more than one page
			body := fmt.Sprintf("shown %02d", i)
			kind, audience, hidden, remove := "post", "public", false, false
			switch i % 6 {
			case 1:
				body, hidden = fmt.Sprintf("hidden %02d", i), true
			case 2:
				body, audience = fmt.Sprintf("local %02d", i), "local"
			case 3:
				body, kind = fmt.Sprintf("milestone %02d", i), "milestone"
			case 4:
				body, remove = fmt.Sprintf("removed %02d", i), true
			}
			var id int64
			if err := e.a.db.QueryRow(`INSERT INTO posts (user_id, kind, body, audience, hidden_at) VALUES ($1, $2, $3, $4, CASE WHEN $5 THEN now() END) RETURNING id`,
				uid, kind, body, audience, hidden).Scan(&id); err != nil {
				t.Fatal(err)
			}
			if remove {
				e.must(200, tok, "DELETE", fmt.Sprintf("/api/posts/%d", id), nil)
			}
			if strings.HasPrefix(body, "shown") {
				m.public = append(m.public, body)
			} else {
				m.secret = append(m.secret, body)
			}
		}
		return m
	}
	older := regexp.MustCompile(`href="(/users/[^"?]+\?before=\d+)"`)
	shown := regexp.MustCompile(`>((?:shown|hidden|local|milestone|removed) \d\d)<`)
	// walk reads pages from the first, following the link to older posts, and returns the bodies in order.
	walk := func(e *tenv, first string) []string {
		var seen []string
		for path, n := first, 0; path != ""; n++ {
			if n > 10 {
				t.Fatal("more pages than there are posts")
			}
			page := pubAsk(e, "GET", path, browser).Body.String()
			for _, m := range shown.FindAllStringSubmatch(page, -1) {
				seen = append(seen, m[1])
			}
			path = ""
			if m := older.FindStringSubmatch(page); m != nil {
				path = strings.ReplaceAll(m[1], "&amp;", "&")
			}
		}
		return seen
	}
	check := func(what string, seen []string, m made) {
		t.Helper()
		want := slices.Clone(m.public)
		slices.Reverse(want)
		if !slices.Equal(seen, want) {
			t.Errorf("%s shows %v, want %v", what, seen, want)
		}
	}

	f := newPubFixture(t)
	f.e.mustExec(`DELETE FROM posts WHERE user_id = $1`, f.ana)
	m := fill(f.e, f.ana, f.anaTok)
	check("the profile page, walked by its cursor", walk(f.e, "/users/ana"), m)

	// Single mode with FLOWA_FEDERATE=on: / lists the newest Public posts, and its link leads to the rest.
	s := newEnv(t)
	if err := s.fedSingleStart(nil); err != nil {
		t.Fatal(err)
	}
	s.h = s.a.routes(staticOf(t, map[string]string{"index.html": "<!doctype html><title>Flowa</title>"}))
	var owner int64
	var name string
	if err := s.a.db.QueryRow(`SELECT id, username FROM users WHERE owner`).Scan(&owner, &name); err != nil {
		t.Fatal(err)
	}
	sm := fill(s, owner, s.session(owner))
	s.a.dropVisitorPage()
	root := ask(s.h, "GET", "/", "", nil).Body.String()
	var top []string
	for _, x := range shown.FindAllStringSubmatch(root, -1) {
		top = append(top, x[1])
	}
	check("single mode's /, its first 20", top, made{public: sm.public[len(sm.public)-pubPage:]})
	if !strings.Contains(root, `href="/users/`+name+`"`) {
		t.Error("single mode's / has no link to the rest of the posts")
	}
	check("single mode's profile page, walked by its cursor", walk(s, "/users/"+name), sm)
}

// The inboxes the actors name are not built yet: in a mode that federates every method there answers 405
// with an empty Allow (RFC 9110 15.5.6 and 10.2.1), whatever the name, the content type or an Origin, before
// the write guard would say 415 or 403, counted against the public allowance. Local and plain single mode
// answer them as any unknown address (TestDBDefaultsUnchanged).
func TestDBInboxNotYet(t *testing.T) {
	f := newPubFixture(t)
	e := f.e
	var want *httptest.ResponseRecorder
	for _, p := range []string{"/users/ana/inbox", "/users/nobody/inbox", "/users/eve/inbox", "/actor/inbox"} {
		for _, m := range []string{"POST", "GET", "HEAD", "PUT", "DELETE", "OPTIONS"} {
			for _, hdr := range []map[string]string{
				{"Content-Type": `application/ld+json; profile="https://www.w3.org/ns/activitystreams"`},
				{"Content-Type": "application/activity+json", "Origin": "https://social.example"},
				{"Content-Type": "application/json"}, nil,
			} {
				w := ask(e.h, m, p, "198.51.100.20:4000", hdr)
				allow, has := w.Result().Header["Allow"]
				if w.Code != 405 || !has || len(allow) != 1 || allow[0] != "" || w.Result().Header.Get("Cache-Control") != "no-store" {
					t.Errorf("%s %s %v: %d %v", m, p, hdr, w.Code, w.Result().Header)
				}
				if want == nil {
					want = w
				} else if m != "HEAD" && w.Body.String() != want.Body.String() {
					t.Errorf("%s %s: another body: %q", m, p, w.Body)
				}
			}
		}
	}
	if !strings.Contains(want.Body.String(), "does not take deliveries yet") {
		t.Errorf("the inbox's words: %q", want.Body)
	}
	e.a.lim.public = newLimiter(1, time.Hour)
	ask(e.h, "POST", "/users/ana/inbox", "203.0.113.9:1", nil)
	if w := ask(e.h, "POST", "/users/ana/inbox", "203.0.113.9:1", nil); w.Code != 429 {
		t.Errorf("the inbox past the allowance: %d", w.Code)
	}
}

// A single-profile instance's page at / is made again whenever a post leaves or rejoins public view: the
// owner deletes it, a moderator hides it or restores it. On a real instance the owner is the one account and
// an admin, whom nobody outranks, so only the delete can happen there; the moderator is put in by hand to show
// that the page follows a moderator's action too, should one ever reach the owner's posts.
func TestDBVisitorPageFollows(t *testing.T) {
	s := newEnv(t)
	if err := s.fedSingleStart(nil); err != nil {
		t.Fatal(err)
	}
	s.h = s.a.routes(staticOf(t, map[string]string{"index.html": "<!doctype html><title>Flowa</title>"}))
	var owner int64
	if err := s.a.db.QueryRow(`SELECT id FROM users WHERE owner`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	tok := s.session(owner)
	var keep, gone int64
	for body, id := range map[string]*int64{"kept words": &keep, "deleted words": &gone} {
		out := s.must(200, tok, "POST", "/api/posts", map[string]any{"body": body, "audience": "public"})
		*id = num(out["posts"].([]any)[0].(map[string]any)["id"])
	}
	root := func() string { return ask(s.h, "GET", "/", "", nil).Body.String() }
	if r := root(); !strings.Contains(r, "kept words") || !strings.Contains(r, "deleted words") {
		t.Fatalf("the page at / before: %s", r)
	}
	s.must(200, tok, "DELETE", fmt.Sprintf("/api/posts/%d", gone), nil)
	if strings.Contains(root(), "deleted words") {
		t.Error("a deleted post is still on the page at /")
	}
	s.mustExec(`INSERT INTO users (username, display_name, pw_hash, role) VALUES ('mod', 'Mod', 'x', 'moderator')`)
	var mod int64
	if err := s.a.db.QueryRow(`SELECT id FROM users WHERE username = 'mod'`).Scan(&mod); err != nil {
		t.Fatal(err)
	}
	// users_owner_is_admin keeps the owner an admin, whom nobody outranks; this test's own schema drops it so
	// that a moderator can act on the owner's post and the handler's drop of the page can be seen.
	s.mustExec(`ALTER TABLE users DROP CONSTRAINT users_owner_is_admin`)
	s.mustExec(`UPDATE users SET role = 'user' WHERE id = $1`, owner)
	s.mustExec(`UPDATE users SET role = 'admin' WHERE id = $1`, mod)
	s.setFlag("moderation", true)
	mtok := s.session(mod)
	s.must(200, mtok, "POST", "/api/mod/actions", map[string]any{"action": "hide", "kind": "post", "id": keep, "reason": "test"})
	if strings.Contains(root(), "kept words") {
		t.Error("a hidden post is still on the page at /")
	}
	s.must(200, mtok, "POST", "/api/mod/actions", map[string]any{"action": "restore", "kind": "post", "id": keep, "reason": "test"})
	if !strings.Contains(root(), "kept words") {
		t.Error("a restored post is not back on the page at /")
	}
}
