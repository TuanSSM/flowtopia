package main

// People elsewhere, found by their handle (docs/federation.md, section 11 and 5.3; FRD 8): the query read
// strictly, WebFinger answers read strictly, the reasons a member is told, and the whole lookup against stub
// servers serving documents written from Mastodon's WebFinger page and the SWICG WebFinger report (none
// recorded: section 13), reached through the fetcher's test-only dial override.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"
)

func TestParseLookup(t *testing.T) {
	o, err := parseOrigin("https://here.example")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		in, want string // want: "name host" for a handle, "url <address>", or "bad" / "http"
		local    string // "" when not local; the member's name, or "-" for a local query that names nobody
	}{
		{"@bo@social.example", "bo social.example", ""},
		{"bo@social.example", "bo social.example", ""},
		{"  @bo@social.example  ", "bo social.example", ""},
		{"acct:bo@social.example", "bo social.example", ""},
		{"ACCT:bo@social.example", "bo social.example", ""},
		{"acct:@bo@social.example", "bo social.example", ""},
		{"@Gargron@Mastodon.Social.", "Gargron mastodon.social", ""},
		{"@%61lice@social.example", "alice social.example", ""},
		{"@a.b-c_d~e!$&'()*+,;=@social.example", "a.b-c_d~e!$&'()*+,;= social.example", ""},
		{"@bo@xn--bcher-kva.example", "bo xn--bcher-kva.example", ""},
		{"https://social.example/@bo", "url https://social.example/@bo", ""},
		{"HTTPS://social.example/users/bo#main", "url https://social.example/users/bo", ""},
		{"https://social.example/users/bo?x=1", "url https://social.example/users/bo?x=1", ""},
		{"http://social.example/@bo", "http", ""},
		{"", "bad", ""},
		{"bo", "bad", ""},
		{"@bo", "bad", ""},
		{"bo@", "bad", ""},
		{"@@social.example", "bad", ""},
		{"bo@social@example", "bad", ""},
		{"bo @social.example", "bad", ""},
		{"bo@social.example:443", "bad", ""},
		{"acct:bo@social.example:443", "bad", ""},
		{"bo@bücher.example", "bad", ""},
		{"bö@social.example", "bad", ""},
		{"b/o@social.example", "bad", ""},
		{"b%40o@social.example", "bad", ""},
		{"b%2Fo@social.example", "bad", ""},
		{"b%00o@social.example", "bad", ""},
		{"b%zzo@social.example", "bad", ""},
		{"bo@social_example", "bad", ""},
		{"mailto:bo@social.example", "bad", ""},
		{"ftp://social.example/bo", "bad", ""},
		{"https://user@social.example/bo", "bad", ""},
		{"https:social.example/bo", "bad", ""},
		{"https://", "bad", ""},
		{"bo\x00@social.example", "bad", ""},
		{"@" + strings.Repeat("a", 300) + "@social.example", "bad", ""},
		{"https://social.example/" + strings.Repeat("a", lookupMax), "bad", ""},
		// This instance's own host: a local member, answered from the database, never fetched.
		{"@ana@here.example", "ana here.example", "ana"},
		{"@Ana@HERE.example.", "Ana here.example", "ana"},
		{"acct:ana@here.example", "ana here.example", "ana"},
		{"@here.example@here.example", "here.example here.example", "-"},
		{"@x@here.example", "x here.example", "-"},
		{"https://here.example/users/ana", "url https://here.example/users/ana", "ana"},
		{"https://HERE.example/actor", "url https://HERE.example/actor", "-"},
		{"https://here.example/@ana", "url https://here.example/@ana", "-"},
		{"https://here.example:8443/users/ana", "url https://here.example:8443/users/ana", "-"},
	} {
		in, bad := parseLookup(c.in)
		got := in.name + " " + in.host
		switch {
		case bad == lookupHTTPWord:
			got = "http"
		case bad != "":
			got = "bad"
		case in.url != "":
			got = "url " + in.url
		}
		if got != c.want {
			t.Errorf("%q: %q, want %q", c.in, got, c.want)
			continue
		}
		if bad != "" {
			continue
		}
		name, local := localTarget(in, o)
		gotLocal := ""
		if local {
			gotLocal = name
			if name == "" {
				gotLocal = "-"
			}
		}
		if gotLocal != c.local {
			t.Errorf("%q: local %q, want %q", c.in, gotLocal, c.local)
		}
	}
	// What the fetcher would never ask is refused before any connection, by the fetcher's own rules.
	for in, want := range map[string]bool{"bo@social.example": true, "bo@10.0.0.1": false, "bo@localhost": false, "bo@nas.local": false,
		"bo@2130706433": false, "bo@intranet": false, "https://social.example/@bo": true, "https://social.example:443/@bo": true,
		"https://social.example:8443/@bo": false, "https://127.0.0.1/@bo": false, "https://[::1]/@bo": false} {
		q, bad := parseLookup(in)
		if bad != "" {
			t.Errorf("%q: %s", in, bad)
			continue
		}
		if got := lookupHostOK(q); got != want {
			t.Errorf("%q: may be asked %v, want %v", in, got, want)
		}
	}
}

func TestAcct(t *testing.T) {
	for _, c := range []struct{ name, host, want string }{
		{"bo", "social.example", "acct:bo@social.example"},
		{"Gargron", "mastodon.social", "acct:Gargron@mastodon.social"},
		{"a b@c", "x.example", "acct:a%20b%40c@x.example"},
		{"ü", "x.example", "acct:%C3%BC@x.example"},
	} {
		if got := acctURI(c.name, c.host); got != c.want {
			t.Errorf("%q: %q, want %q", c.name, got, c.want)
		}
	}
	for _, c := range []struct {
		a, b string
		same bool
	}{
		{"acct:bo@social.example", "ACCT:bo@SOCIAL.example.", true},
		{"acct:%62o@social.example", "acct:bo@social.example", true},
		{"acct:Bo@social.example", "acct:bo@social.example", false}, // case of a remote name is kept (SWICG report)
		{"acct:bo@social.example", "acct:bo@other.example", false},
		{"acct:bo@social.example", "https://social.example/users/bo", false},
		{"acct:bo@10.0.0.1", "acct:bo@10.0.0.1", false},
		{"acct:b@o@social.example", "acct:b@o@social.example", false},
	} {
		if got := sameAcct(c.a, c.b); got != c.same {
			t.Errorf("%q and %q: same %v, want %v", c.a, c.b, got, c.same)
		}
	}
}

func TestParseJRD(t *testing.T) {
	self := `{"rel":"self","type":"application/activity+json","href":"https://social.example/users/bo"}`
	for _, c := range []struct{ doc, want string }{
		{`{"subject":"acct:bo@social.example","links":[` + self + `]}`, "acct:bo@social.example https://social.example/users/bo"},
		// The ld+json form with the Activity Streams profile (Mastodon's page), and the first of two (SWICG).
		{`{"subject":"acct:bo@social.example","links":[{"rel":"http://webfinger.net/rel/profile-page","type":"text/html","href":"https://social.example/@bo"},
			{"rel":"self","type":"application/ld+json; profile=\"https://www.w3.org/ns/activitystreams\"","href":"https://social.example/actors/1"},` + self + `]}`,
			"acct:bo@social.example https://social.example/actors/1"},
		{`{"subject":"acct:bo@social.example","links":[{"rel":"self","type":"application/ld+json","href":"https://social.example/x"},` + self + `]}`, "acct:bo@social.example https://social.example/users/bo"},
		{`{"subject":"acct:bo@social.example","links":[{"rel":"self","href":"https://social.example/x"}]}`, "refused"},
		{`{"subject":"acct:bo@social.example","links":[{"rel":"self","type":"application/activity+json","href":"http://social.example/x"}]}`, "refused"},
		{`{"subject":"acct:bo@social.example","links":[{"rel":"self","type":"application/activity+json","href":"https://10.0.0.1/x"}]}`, "refused"},
		{`{"subject":"acct:bo@social.example","links":[{"rel":"Self","type":"application/activity+json","href":"https://social.example/x"}]}`, "refused"},
		{`{"links":[` + self + `]}`, "refused"},
		{`{"subject":"","links":[` + self + `]}`, "refused"},
		{`{"subject":7,"links":[` + self + `]}`, "refused"},
		{`{"Subject":"acct:bo@social.example","links":[` + self + `]}`, "refused"},
		{`{"subject":"acct:bo@social.example","subject":"acct:eve@social.example","links":[` + self + `]}`, "refused"},
		{`{"subject":"acct:bo@social.example","links":` + self + `}`, "refused"},
		{`{"subject":"acct:bo@social.example","links":[` + self + `]`, "refused"},
		{`["subject"]`, "refused"},
		{"{\"subject\":\"acct:bo@social.example\xff\",\"links\":[" + self + "]}", "refused"},
	} {
		j, err := parseJRD([]byte(c.doc))
		got := j.subject + " " + j.self
		if err != nil {
			got = "refused"
		}
		if got != c.want {
			t.Errorf("%s: %q, want %q", c.doc, got, c.want)
		}
	}
}

// Every failure the fetcher and the actor cache report is told in one of the fixed reasons, and every
// reason has its words, code and status, none naming a host, a status or another server's text.
func TestLookupReasons(t *testing.T) {
	for r := lookupBadInput; r <= lookupUnavailable; r++ {
		w := lookupReasons[r]
		if w.code == "" || w.words == "" || lookupHTTP[r] < 400 || strings.ContainsAny(w.words, "–—") {
			t.Errorf("reason %d: %+v %d", r, w, lookupHTTP[r])
		}
	}
	for f, want := range map[fetchFail]lookupReason{failRefused: lookupNotFound, failConnect: lookupNotFound, failTimeout: lookupNotFound,
		failCanceled: lookupNotFound, failTooLarge: lookupNotAccount, failType: lookupNotAccount, failGone: lookupGone, failNotFound: lookupNotFound,
		failStatus: lookupNotFound, failRedirect: lookupNotFound, failBusy: lookupBusy} {
		if got := lookupReasonOf(&fetchError{Fail: f, Host: "secret.internal", Reason: "x"}); got != want {
			t.Errorf("fetch failure %d: %d, want %d", f, got, want)
		}
	}
	for r := actorBadID; r <= actorNoKey; r++ {
		got := lookupReasonOf(&actorError{Reason: r, err: &fetchError{Fail: failGone}})
		if got == 0 || got == lookupBadInput {
			t.Errorf("actor reason %v: %d", r, got)
		}
	}
	if got := lookupReasonOf(errors.New("pq: the database said something")); got != lookupUnavailable {
		t.Errorf("the database: %d", got)
	}
	if e := (&lookupError{Reason: lookupRefused, Step: "webfinger", err: &fetchError{Host: "10.0.0.1", Reason: "refusing to connect to 10.0.0.1:443"}}); strings.Contains(e.Error(), "10.0.0.1") {
		t.Errorf("the error text names the address: %s", e)
	}
}

/* ---------- the stub servers ---------- */

// wfURL is where a WebFinger request for resource goes on host, as the lookup writes it.
func wfURL(host, resource string) string {
	return "https://" + host + "/.well-known/webfinger?resource=" + url.QueryEscape(resource)
}

// putWF serves a WebFinger answer naming subject and self.
func putWF(w *actorWorld, host, resource, subject, self string) {
	b, _ := json.Marshal(map[string]any{"subject": subject, "aliases": []string{self}, "links": []map[string]string{
		{"rel": "http://webfinger.net/rel/profile-page", "type": "text/html", "href": self},
		{"rel": "self", "type": "application/activity+json", "href": self},
		{"rel": "http://ostatus.org/schema/1.0/subscribe", "template": "https://" + host + "/authorize_interaction?uri={uri}"},
	}})
	w.put(wfURL(host, resource), worldDoc{ctype: "application/jrd+json", body: b})
}

// person serves an actor of preferredUsername pref with an outbox, and a WebFinger answer for it on its
// own host, as a Mastodon server answers.
func person(w *actorWorld, id, pref string, more map[string]any) {
	m := map[string]any{"outbox": id + "/outbox", "summary": "<p>Juggler &amp; <a href=\"https://example.com/site\">my site</a></p>",
		"url": strings.Replace(id, "/users/", "/@", 1)}
	for k, v := range more {
		m[k] = v
	}
	w.put(id, worldDoc{body: actorDoc(id, pref, strings.ToUpper(pref[:1])+pref[1:], nil, m)})
	host := actorHost(id)
	putWF(w, host, acctURI(pref, host), acctURI(pref, host), id)
}

// noteItem is a Create of a Note as an outbox holds it.
func noteItem(actor, id, content string, to, cc []string, more map[string]any) map[string]any {
	n := map[string]any{"id": id, "type": "Note", "attributedTo": actor, "content": content, "to": to, "cc": cc,
		"published": "2026-11-12T18:04:05Z", "url": id + "/html"}
	for k, v := range more {
		n[k] = v
	}
	return map[string]any{"id": id + "/activity", "type": "Create", "actor": actor, "to": to, "cc": cc, "object": n}
}

// outbox serves an actor's outbox with its first page at a URL of its own, holding items.
func outbox(w *actorWorld, actor string, items ...any) {
	ob := actor + "/outbox"
	b, _ := json.Marshal(map[string]any{"@context": asContext, "id": ob, "type": "OrderedCollection", "totalItems": len(items), "first": ob + "?page=true"})
	w.put(ob, worldDoc{body: b})
	b, _ = json.Marshal(map[string]any{"@context": asContext, "id": ob + "?page=true", "type": "OrderedCollectionPage", "partOf": ob, "orderedItems": items})
	w.put(ob+"?page=true", worldDoc{body: b})
}

func lookupOf(t *testing.T, r *remoteEnv, q string) (*lookupProfile, error) {
	t.Helper()
	in, bad := parseLookup(q)
	if bad != "" {
		t.Fatalf("%q: %s", q, bad)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return r.a.fed.findRemote(ctx, in)
}

func lookupCode(err error) string {
	if err == nil {
		return ""
	}
	return lookupReasons[lookupReasonOf(err)].code
}

// The whole lookup against stub servers: forward and reverse WebFinger, a redirect to another host, a handle
// domain apart from the server's, a profile address, the posts of the outbox, every failure a member can
// meet, and the count of requests each sends.
func TestDBResolveHandle(t *testing.T) {
	r := newRemoteEnv(t)
	w := r.w
	pub := []string{publicCollection}
	bo := "https://social.example/users/bo"
	person(w, bo, "bo", map[string]any{"manuallyApprovesFollowers": true})
	followers := []string{bo + "/followers"}
	outbox(w, bo,
		noteItem(bo, "https://social.example/users/bo/statuses/1", `<p>Hi <span class="h-card"><a href="https://social.example/@ana">@<span>ana</span></a></span> <a href="https://social.example/tags/poi" rel="tag">#<span>poi</span></a><script>x</script></p><p>&lt;b&gt;two&lt;/b&gt;</p>`,
			pub, followers, map[string]any{"tag": []map[string]string{{"type": "Mention", "href": "https://social.example/users/ana", "name": "@ana@social.example"}, {"type": "Hashtag", "href": "https://social.example/tags/poi", "name": "#poi"}},
				"attachment": []map[string]string{{"type": "Image", "mediaType": "image/png", "url": "https://social.example/media/1.png"}}}),
		noteItem(bo, "https://social.example/users/bo/statuses/2", "<p>unlisted</p>", followers, pub, map[string]any{"summary": "spoiler", "sensitive": true}),
		noteItem(bo, "https://social.example/users/bo/statuses/3", "<p>followers only</p>", followers, nil, nil),
		noteItem(bo, "https://other.example/statuses/4", "<p>a Note of another origin</p>", pub, nil, nil),
		noteItem(bo, "https://social.example/users/bo/statuses/5", "<p>by someone else</p>", pub, nil, map[string]any{"attributedTo": "https://social.example/users/eve"}),
		map[string]any{"id": "https://social.example/users/bo/statuses/6/activity", "type": "Announce", "actor": bo, "to": pub, "object": "https://else.example/1"},
		"https://social.example/users/bo/statuses/7/activity",
		map[string]any{"id": "https://social.example/users/bo/statuses/8/activity", "type": "Create", "actor": "https://social.example/users/eve", "to": pub,
			"object": map[string]any{"id": "https://social.example/users/bo/statuses/8", "type": "Note", "attributedTo": bo, "content": "x", "to": pub}},
		noteItem(bo, "https://social.example/users/bo/statuses/9", strings.Repeat("long ", 500), pub, nil, nil),
		noteItem(bo, "https://social.example/users/bo/statuses/10", "<p>a question</p>", pub, nil, map[string]any{"type": "Question"}),
	)

	// 1. A handle as Mastodon resolves it: WebFinger, the actor, the reverse lookup (the same request, not
	// sent twice), the outbox and its first page. Four requests.
	before := w.total()
	p, err := lookupOf(t, r, "@bo@social.example")
	if err != nil {
		t.Fatal(err)
	}
	if w.total()-before != 4 || w.hit(wfURL("social.example", "acct:bo@social.example")) != 1 {
		t.Errorf("%d requests, %d to the WebFinger, want 4 and 1", w.total()-before, w.hit(wfURL("social.example", "acct:bo@social.example")))
	}
	if p.Kind != "remote" || p.Handle != "bo@social.example" || p.Name != "Bo" || p.Bio != "Juggler & my site (https://example.com/site)" || !p.Locked ||
		p.Link != "https://social.example/@bo" || p.Notice != lookupNotice || p.PostsNote != "" {
		t.Errorf("profile %+v", p)
	}
	got := []string{}
	for _, x := range p.Posts {
		got = append(got, fmt.Sprintf("%s|%.40s|%v|%s|%v|%d|%s", x.Link, x.Text, x.More, x.Summary, x.Sensitive, x.Pictures, x.Published))
	}
	want := []string{
		"https://social.example/users/bo/statuses/1/html|Hi @ana@social.example #poi\n\n<b>two</b>|false||false|1|2026-11-12T18:04:05Z",
		"https://social.example/users/bo/statuses/2/html|unlisted|false|spoiler|true|0|2026-11-12T18:04:05Z",
		"https://social.example/users/bo/statuses/9/html|long long long long long long long long |true||false|0|2026-11-12T18:04:05Z",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("posts:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if n := r.count(`SELECT count(*) FROM posts`); n != 0 {
		t.Errorf("%d posts stored: the outbox is read, never kept", n)
	}

	// 2. The handle in another case and with a trailing dot: asked as typed (the name's case kept), the
	// server's subject names the account, and the reverse lookup is a request of its own.
	before = w.total()
	putWF(w, "social.example", "acct:BO@social.example", "acct:bo@social.example", bo)
	if p, err := lookupOf(t, r, "@BO@Social.Example."); err != nil || p.Handle != "bo@social.example" {
		t.Errorf("another case: %+v %v", p, err)
	}
	if n := w.total() - before; n != 4 { // the forward WebFinger, the reverse one, the outbox and its page; the actor is cached
		t.Errorf("another case: %d requests, want 4", n)
	}

	// 3. A handle domain apart from the server's (Mastodon's WEB_DOMAIN; the SWICG report's alice@example.com):
	// a.example's WebFinger redirects to b.example's, the actor is on b.example, its reverse lookup names
	// acct:alice@a.example as subject, and that canonical account leads to the same actor.
	alice := "https://b.example/users/alice"
	person(w, alice, "alice", nil)
	outbox(w, alice)
	w.put(wfURL("a.example", "acct:alice@a.example"), worldDoc{status: 301, location: wfURL("b.example", "acct:alice@a.example")})
	putWF(w, "b.example", "acct:alice@a.example", "acct:alice@a.example", alice)
	putWF(w, "b.example", "acct:alice@b.example", "acct:alice@a.example", alice)
	before = w.total()
	if p, err := lookupOf(t, r, "alice@a.example"); err != nil || p.Handle != "alice@a.example" || p.PostsNote != lookupNoPosts {
		t.Errorf("a handle domain apart: %+v %v", p, err)
	}
	// The redirect and its answer, the actor, the reverse lookup, the outbox and its page: the canonical
	// account's lookup is the first one, already answered.
	if n := w.total() - before; n != 6 {
		t.Errorf("a handle domain apart: %d requests, want 6", n)
	}
	// Asked by the server's own name, the subject names the canonical account, which is then asked too.
	if p, err := lookupOf(t, r, "alice@b.example"); err != nil || p.Handle != "alice@a.example" {
		t.Errorf("by the server's name: %+v %v", p, err)
	}

	// 4. A WebFinger self link on another host than the handle: remote.example says eve is bo. The actor
	// and its reverse lookup are bo's, true, but the handle asked for is not the account's.
	putWF(w, "remote.example", "acct:eve@remote.example", "acct:eve@remote.example", bo)
	if p, err := lookupOf(t, r, "eve@remote.example"); lookupCode(err) != "notAccount" {
		t.Errorf("a self link to someone else's actor: %+v %v", p, err)
	}
	// Naming bo's own handle as the subject is no lie: the answer is bo, under the handle bo's server proves.
	putWF(w, "remote.example", "acct:alias@remote.example", "acct:bo@social.example", bo)
	if p, err := lookupOf(t, r, "alias@remote.example"); err != nil || p.Handle != "bo@social.example" {
		t.Errorf("a server pointing at bo under bo's handle: %+v %v", p, err)
	}

	// 5. An actor whose reverse lookup does not match: its own server names another actor for its name.
	mal := "https://other.example/users/mallory"
	person(w, mal, "mallory", nil)
	putWF(w, "other.example", "acct:mallory@other.example", "acct:mallory@other.example", "https://other.example/users/someone-else")
	putWF(w, "remote.example", "acct:mallory@remote.example", "acct:mallory@remote.example", mal)
	if p, err := lookupOf(t, r, "mallory@remote.example"); lookupCode(err) != "notAccount" {
		t.Errorf("a reverse lookup naming another actor: %+v %v", p, err)
	}
	// Its reverse lookup answers 404: the handle does not check out.
	ghost := "https://moved.example/users/ghost"
	w.put(ghost, worldDoc{body: actorDoc(ghost, "ghost", "", nil, nil)})
	putWF(w, "remote.example", "acct:ghost@remote.example", "acct:ghost@remote.example", ghost)
	if p, err := lookupOf(t, r, "ghost@remote.example"); lookupCode(err) != "notAccount" {
		t.Errorf("a reverse lookup that finds nothing: %+v %v", p, err)
	}
	// Its subject names a canonical account that leads to another actor.
	tom := "https://big.example/users/tom"
	person(w, tom, "tom", nil)
	putWF(w, "big.example", "acct:tom@big.example", "acct:tom@kenzoishii.example.com", tom)
	putWF(w, "kenzoishii.example.com", "acct:tom@kenzoishii.example.com", "acct:tom@kenzoishii.example.com", "https://kenzoishii.example.com/")
	if p, err := lookupOf(t, r, "tom@big.example"); lookupCode(err) != "notAccount" {
		t.Errorf("a canonical account leading elsewhere: %+v %v", p, err)
	}
	// Its subject names an account on this instance: never asked, never believed.
	putWF(w, "big.example", "acct:tom@big.example", "acct:ana@here.example", tom)
	if p, err := lookupOf(t, r, "tom@big.example"); lookupCode(err) != "notAccount" || w.hit(wfURL("here.example", "acct:ana@here.example")) != 0 {
		t.Errorf("a canonical account on this instance: %+v %v", p, err)
	}

	// 6. A profile address. An actor's id is the actor; a page that is not where its id says is asked of its
	// host's WebFinger as a resource, and goes on as a handle.
	if p, err := lookupOf(t, r, bo); err != nil || p.Handle != "bo@social.example" {
		t.Errorf("an actor's id: %+v %v", p, err)
	}
	g := "https://mastodon.social/users/Gargron"
	person(w, g, "Gargron", nil)
	outbox(w, g)
	w.put("https://mastodon.social/@Gargron", worldDoc{body: actorDoc(g, "Gargron", "", nil, nil)}) // answers the actor of another id
	putWF(w, "mastodon.social", "https://mastodon.social/@Gargron", "acct:Gargron@mastodon.social", g)
	before = w.total()
	if p, err := lookupOf(t, r, "https://mastodon.social/@Gargron"); err != nil || p.Handle != "Gargron@mastodon.social" || p.Name != "Gargron" {
		t.Errorf("a profile page: %+v %v", p, err)
	}
	// The address asked as an actor, the WebFinger of the address, the actor, the reverse lookup, the
	// outbox and its page.
	if n := w.total() - before; n != 6 {
		t.Errorf("a profile page: %d requests, want 6", n)
	}

	// 7. Failures, each told in its fixed words. An address that is not an actor and that its host's
	// WebFinger does not know; a name that does not resolve; a private address, refused at connect time; a
	// WebFinger answer of the wrong type, with no self link, gone; an actor gone, suspended, not an actor.
	w.put("https://social.example/about", worldDoc{ctype: "text/html", body: []byte("<html>")})
	w.put(wfURL("social.example", "acct:html@social.example"), worldDoc{ctype: "text/html", body: []byte("<p>no</p>")})
	w.put(wfURL("social.example", "acct:noself@social.example"), worldDoc{ctype: "application/jrd+json", body: []byte(`{"subject":"acct:noself@social.example","links":[]}`)})
	w.put(wfURL("social.example", "acct:left@social.example"), worldDoc{status: 410, body: []byte("gone")})
	sus := "https://social.example/users/sus"
	w.put(sus, worldDoc{body: actorDoc(sus, "sus", "", nil, map[string]any{"suspended": true})})
	putWF(w, "social.example", "acct:sus@social.example", "acct:sus@social.example", sus)
	deleted := "https://social.example/users/deleted"
	w.put(deleted, worldDoc{status: 410})
	putWF(w, "social.example", "acct:deleted@social.example", "acct:deleted@social.example", deleted)
	w.put("https://social.example/users/note", worldDoc{body: []byte(`{"id":"https://social.example/users/note","type":"Note","attributedTo":"https://social.example/users/bo"}`)})
	putWF(w, "social.example", "acct:note@social.example", "acct:note@social.example", "https://social.example/users/note")
	for q, want := range map[string]string{
		"https://social.example/about": "notFound",
		"bo@nowhere.example":           "notFound",
		"bo@private.example":           "notFound",
		"html@social.example":          "notAccount",
		"noself@social.example":        "notAccount",
		"left@social.example":          "gone",
		"sus@social.example":           "suspended",
		"deleted@social.example":       "gone",
		"note@social.example":          "notAccount",
		"nobody@social.example":        "notFound",
	} {
		if p, err := lookupOf(t, r, q); lookupCode(err) != want {
			t.Errorf("%s: %+v %v, want %s", q, p, err, want)
		}
	}

	// 8. The posts, when they cannot be read: an outbox on another origin is not asked; one that fails gives
	// the profile with a sentence in place of the posts.
	far := "https://social.example/users/far"
	person(w, far, "far", map[string]any{"outbox": "https://other.example/outboxes/far"})
	if p, err := lookupOf(t, r, "far@social.example"); err != nil || p.PostsNote != lookupNoPosts || w.hit("https://other.example/outboxes/far") != 0 {
		t.Errorf("an outbox on another origin: %+v %v", p, err)
	}
	broken := "https://social.example/users/broken"
	person(w, broken, "broken", nil)
	w.put(broken+"/outbox", worldDoc{status: 500})
	if p, err := lookupOf(t, r, "broken@social.example"); err != nil || p.PostsNote != lookupPostsFailed || len(p.Posts) != 0 {
		t.Errorf("an outbox that fails: %+v %v", p, err)
	}
	wrongID := "https://social.example/users/wrongid"
	person(w, wrongID, "wrongid", nil)
	b, _ := json.Marshal(map[string]any{"id": "https://social.example/users/bo/outbox", "type": "OrderedCollection", "orderedItems": []any{noteItem(wrongID, wrongID+"/s/1", "x", pub, nil, nil)}})
	w.put(wrongID+"/outbox", worldDoc{body: b})
	if p, err := lookupOf(t, r, "wrongid@social.example"); err != nil || p.PostsNote != lookupPostsFailed {
		t.Errorf("an outbox whose id is not where it came from: %+v %v", p, err)
	}
	// An outbox that holds its items itself, with no page.
	inline := "https://social.example/users/inline"
	person(w, inline, "inline", nil)
	b, _ = json.Marshal(map[string]any{"id": inline + "/outbox", "type": "OrderedCollection", "orderedItems": []any{noteItem(inline, inline+"/s/1", "<p>inline</p>", pub, nil, nil)}})
	w.put(inline+"/outbox", worldDoc{body: b})
	before = w.total()
	if p, err := lookupOf(t, r, "inline@social.example"); err != nil || len(p.Posts) != 1 || p.Posts[0].Text != "inline" || w.total()-before != 3 {
		t.Errorf("an outbox with its items: %+v %v, %d requests", p, err, w.total()-before)
	}

	// 9. The bound: a lookup that takes every step there is (a profile page, the WebFinger of the address
	// redirected twice, the actor, the reverse lookup, the canonical account redirected, the outbox and its
	// page, each with a same-origin redirect where the document kind allows one) sends at most
	// lookupFetches requests through the fetcher and at most four requests each.
	z := "https://remote.example/users/zed"
	person(w, z, "zed", nil)
	putWF(w, "remote.example", "acct:zed@remote.example", "acct:zed@moved.example", z)
	w.put(wfURL("moved.example", "acct:zed@moved.example"), worldDoc{status: 302, location: wfURL("other.example", "acct:zed@moved.example")})
	w.put(wfURL("other.example", "acct:zed@moved.example"), worldDoc{status: 307, location: wfURL("remote.example", "acct:zed@moved.example")})
	putWF(w, "remote.example", "acct:zed@moved.example", "acct:zed@moved.example", z)
	w.put("https://remote.example/@zed", worldDoc{status: 301, location: "https://remote.example/@zed/"})
	w.put("https://remote.example/@zed/", worldDoc{body: []byte(`<html>`), ctype: "text/html"})
	w.put(wfURL("remote.example", "https://remote.example/@zed"), worldDoc{status: 301, location: wfURL("remote.example", "acct:zed@moved.example")})
	w.put(z+"/outbox", worldDoc{status: 301, location: z + "/outbox/"})
	b, _ = json.Marshal(map[string]any{"id": z + "/outbox/", "type": "OrderedCollection", "first": z + "/outbox/1"})
	w.put(z+"/outbox/", worldDoc{body: b})
	w.put(z+"/outbox/1", worldDoc{status: 308, location: z + "/outbox/p1"})
	b, _ = json.Marshal(map[string]any{"id": z + "/outbox/p1", "type": "OrderedCollectionPage", "orderedItems": []any{noteItem(z, z+"/s/1", "zed", pub, nil, nil)}})
	w.put(z+"/outbox/p1", worldDoc{body: b})
	before = w.total()
	if p, err := lookupOf(t, r, "https://remote.example/@zed"); err != nil || p.Handle != "zed@moved.example" || len(p.Posts) != 1 {
		t.Errorf("every step: %+v %v", p, err)
	}
	if n := w.total() - before; n > lookupFetches*(1+maxRedirects) || n != 13 {
		t.Errorf("every step: %d requests, want 13 (at most %d)", n, lookupFetches*(1+maxRedirects))
	}
	// The longest way of all (the review's M1): the same, with the actor's key at an address of its own, which
	// the cache fetches inside remoteActor. lookupFetches requests through the fetcher, and no more.
	keyA, _ := sigTestKeys()
	k := "https://remote.example/users/kay"
	person(w, k, "kay", map[string]any{"publicKey": k + "/key#main"})
	kb, _ := json.Marshal(map[string]any{"id": k + "/key#main", "owner": k, "publicKeyPem": pemOf(&keyA.PublicKey)})
	w.put(k+"/key", worldDoc{body: kb})
	putWF(w, "remote.example", "acct:kay@remote.example", "acct:kay@moved.example", k)
	putWF(w, "moved.example", "acct:kay@moved.example", "acct:kay@moved.example", k)
	w.put("https://remote.example/@kay", worldDoc{body: actorDoc(k, "kay", "", nil, nil)}) // the actor under another id
	putWF(w, "remote.example", "https://remote.example/@kay", "acct:kay@moved.example", k)
	outbox(w, k, noteItem(k, k+"/s/1", "kay", pub, nil, nil))
	before = w.total()
	if p, err := lookupOf(t, r, "https://remote.example/@kay"); err != nil || p.Handle != "kay@moved.example" || len(p.Posts) != 1 || w.hit(k+"/key") != 1 {
		t.Errorf("the longest way: %+v %v, the key asked %d times", p, err, w.hit(k+"/key"))
	}
	if n := w.total() - before; n != lookupFetches {
		t.Errorf("the longest way: %d requests, want %d", n, lookupFetches)
	}
	// spend stops a lookup past lookupFetches requests, whatever asks.
	run := &lookupRun{f: r.a.fed, jrds: map[string]wfAnswer{}}
	for i := 0; i < lookupFetches; i++ {
		if run.spend("x", 1) != nil {
			t.Fatalf("request %d refused", i+1)
		}
	}
	if err := run.spend("x", 1); lookupCode(err) != "notAccount" {
		t.Errorf("request %d: %v", lookupFetches+1, err)
	}

	// 10. A profile address that redirects to the actor's id is the actor (the cache follows a same-origin
	// redirect and keeps the address as one of the actor's names): the redirect and the actor, the reverse
	// lookup, the outbox and its page. Asked again, the address is the cached actor, with no request for it.
	pip := "https://social.example/users/pip"
	person(w, pip, "pip", nil)
	outbox(w, pip)
	w.put("https://social.example/@pip", worldDoc{status: 301, location: pip})
	before = w.total()
	if p, err := lookupOf(t, r, "https://social.example/@pip"); err != nil || p.Handle != "pip@social.example" {
		t.Errorf("a profile address that redirects: %+v %v", p, err)
	}
	if n := w.total() - before; n != 5 {
		t.Errorf("a profile address that redirects: %d requests, want 5", n)
	}
	before = w.total()
	if p, err := lookupOf(t, r, "https://social.example/@pip"); err != nil || p.Handle != "pip@social.example" || w.hit("https://social.example/@pip") != 1 {
		t.Errorf("the same address again: %+v %v", p, err)
	}
	if n := w.total() - before; n != 3 {
		t.Errorf("the same address again: %d requests, want 3 (the reverse lookup, the outbox and its page)", n)
	}

	// 11. An actor whose server stops answering is shown as the cache kept it, saying so and when; one
	// whose server says it is gone is told as gone.
	r.clock.add(actorTTL + time.Minute)
	w.put(pip, worldDoc{status: 503})
	if p, err := lookupOf(t, r, "pip@social.example"); err != nil || p.Stale == "" || p.Handle != "pip@social.example" {
		t.Errorf("an actor whose server does not answer: %+v %v", p, err)
	}
	r.clock.add(actorFailFor + time.Minute)
	w.put(pip, worldDoc{status: 410})
	if p, err := lookupOf(t, r, "pip@social.example"); lookupCode(err) != "gone" {
		t.Errorf("a stored actor now gone: %+v %v", p, err)
	}

	// 12. Too slow: a WebFinger held at the stub fails with the member's own deadline, as timed out.
	release := w.holdAt(wfURL("social.example", "acct:slow@social.example"))
	defer release()
	in, _ := parseLookup("slow@social.example")
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	var slowErr error
	go func() { defer wg.Done(); _, slowErr = r.a.fed.findRemote(ctx, in) }()
	<-w.arrived
	cancel() // the member's request ended
	wg.Wait()
	if lookupCode(slowErr) != "notFound" {
		t.Errorf("a lookup whose member left: %v", slowErr)
	}
}

// The route: who may use it, what it costs, and what a member learns. A visitor, a suspended member, a write
// from another page and one that is not JSON are refused before anything; the allowance is counted before
// any connection; a handle on this instance is answered from the database with no connection; an address
// Flowa refuses is refused before any connection, in the words of one refused at connect time; another
// server's words never reach the member; staff are answered as anyone else.
func TestDBLookupRoute(t *testing.T) {
	r := newRemoteEnv(t)
	w := r.w
	r.a.fed.lookupPause = func(context.Context, time.Duration) {} // lookupFloor is TestDBLookupTimingFloor's
	r.setFlag("community", true)
	_, ana := r.user("ana", "user")
	_, boss := r.user("boss", "admin")
	susID, sus := r.user("sus", "user")
	bo := "https://social.example/users/bo"
	person(w, bo, "bo", map[string]any{"name": "<b>Bo</b> <script>alert(1)</script>", "summary": `<p onclick="x()">Hello <img src="https://social.example/x.png"> &lt;i&gt;</p>`})
	outbox(w, bo, noteItem(bo, bo+"/s/1", `<p>a <a href="javascript:alert(1)">link</a><iframe src="https://social.example/"></iframe></p>`, []string{publicCollection}, nil, nil))
	w.put(wfURL("social.example", "acct:broken@social.example"), worldDoc{status: 500, ctype: "text/plain", body: []byte("internal error at 10.1.2.3: db01.internal refused")})
	ask := func(tok string, q any, hdr map[string]string) (int, map[string]any) {
		t.Helper()
		code, out, _ := r.doH(tok, "POST", "/api/fed/lookup", map[string]any{"q": q}, hdr)
		return code, out
	}

	// A remote account: the profile as plain text, its post, the notice of what is not built.
	before := w.total()
	code, out := ask(ana, "@bo@social.example", nil)
	res, _ := out["result"].(map[string]any)
	if code != 200 || res["handle"] != "bo@social.example" || res["kind"] != "remote" || res["bio"] != "Hello <i>" || res["notice"] != lookupNotice {
		t.Fatalf("a remote account: %d %v", code, out["_raw"])
	}
	// The name is the actor's as the cache keeps it, plain text: the page escapes it like any other.
	if res["name"] != "<b>Bo</b> <script>alert(1)</script>" {
		t.Errorf("the name: %q", res["name"])
	}
	posts, _ := res["posts"].([]any)
	if len(posts) != 1 || posts[0].(map[string]any)["text"] != "a link" {
		t.Errorf("the post: %v", posts)
	}
	if strings.Contains(out["_raw"].(string), "<p") || strings.Contains(out["_raw"].(string), "onclick") || strings.Contains(out["_raw"].(string), "iframe") || strings.Contains(out["_raw"].(string), "javascript") {
		t.Errorf("remote HTML in the answer: %s", out["_raw"])
	}
	// Staff are answered the same, byte for byte (the actor is cached now: the same rows, the same posts).
	_, a1 := ask(ana, "@bo@social.example", nil)
	_, a2 := ask(boss, "@bo@social.example", nil)
	if a1["_raw"] != a2["_raw"] {
		t.Errorf("staff see more:\n%s\n%s", a2["_raw"], a1["_raw"])
	}
	if w.total() == before {
		t.Fatal("nothing was asked")
	}

	// Another server's error: the fixed words, nothing of its text, its status or an address.
	code, out = ask(ana, "broken@social.example", nil)
	if code != 404 || out["reason"] != "notFound" || strings.Contains(out["_raw"].(string), "10.1.2.3") || strings.Contains(out["_raw"].(string), "internal") || strings.Contains(out["_raw"].(string), "500") {
		t.Errorf("another server's error: %d %s", code, out["_raw"])
	}

	// No connection for: a handle on this instance (a member, nobody, the instance actor, an address on
	// it), a query that is not one, an address Flowa refuses, a visitor, a suspended member, a write from
	// another page, a body that is not JSON.
	before = w.total()
	code, out = ask(ana, "@Boss@here.example", nil)
	if res, _ := out["result"].(map[string]any); code != 200 || res["kind"] != "local" || res["handle"] != "boss" {
		t.Errorf("a local member: %d %s", code, out["_raw"])
	}
	if code, out := ask(ana, "https://here.example/users/ana", nil); code != 200 || out["result"].(map[string]any)["handle"] != "ana" {
		t.Errorf("a local member by address: %d %s", code, out["_raw"])
	}
	for q, want := range map[string]string{"@nobody@here.example": "notFound", "here.example@here.example": "notFound", "https://here.example/actor": "notFound",
		"https://here.example:8443/users/x": "notFound", "bo": "badInput", "http://social.example/@bo": "badInput", "": "badInput",
		"bo@10.0.0.1": "refused", "bo@localhost": "refused", "bo@nas.local": "refused", "https://social.example:8443/@bo": "refused", "https://[::1]/x": "refused"} {
		if code, out := ask(ana, q, nil); out["reason"] != want || code != lookupHTTP[lookupReasonOfCode(want)] {
			t.Errorf("%q: %d %s, want %s", q, code, out["_raw"], want)
		}
	}
	if code, out := ask(ana, 7, nil); code != 400 {
		t.Errorf("a query that is not text: %d %s", code, out["_raw"])
	}
	if code, _ := ask("", "@bo@social.example", nil); code != 401 {
		t.Errorf("a visitor: %d", code)
	}
	r.mustExec(`UPDATE users SET suspended_until = now() + interval '1 day' WHERE id = $1`, susID)
	if code, _ := ask(sus, "@bo@social.example", nil); code != 401 {
		t.Errorf("a suspended member: %d", code)
	}
	if code, _ := ask(ana, "@bo@social.example", map[string]string{"Origin": "https://evil.example"}); code != 403 {
		t.Errorf("a write from another page: %d", code)
	}
	if code, _ := ask(ana, "@bo@social.example", map[string]string{"Content-Type": "text/plain"}); code != 415 {
		t.Errorf("a body that is not JSON: %d", code)
	}
	if code, _ := r.do(ana, "GET", "/api/fed/lookup?q=@bo@social.example", nil); code == 200 {
		t.Errorf("a GET (a link or a picture could send one) answers 200")
	}
	if n := w.total() - before; n != 0 {
		t.Errorf("%d requests to other servers for queries that needed none", n)
	}

	// The allowance is counted before any connection: past it, nothing is asked.
	r.a.lim.lookups = newLimiter(2, time.Hour)
	before = w.total()
	ask(ana, "@bo@social.example", nil)
	ask(ana, "@bo@social.example", nil)
	mid := w.total()
	if code, out := ask(ana, "@bo@social.example", nil); code != 429 || w.total() != mid {
		t.Errorf("past the allowance: %d %s, %d requests", code, out["_raw"], w.total()-mid)
	}
	if mid == before {
		t.Error("the allowed lookups asked nothing")
	}
	// A refused address costs its whole count, so trying addresses is no cheaper refused than asked.
	r.a.lim.lookups = newLimiter(2, time.Hour)
	ask(ana, "bo@10.0.0.1", nil)
	ask(ana, "bo@10.0.0.2", nil)
	if code, _ := ask(ana, "bo@10.0.0.3", nil); code != 429 {
		t.Errorf("refused addresses did not use the allowance: %d", code)
	}
	r.a.lim.lookups = newLimits().lookups

	// At most lookupsAtOnce at once: a lookup beyond them is told to try again, and asks nothing.
	for i := 0; i < lookupsAtOnce; i++ {
		r.a.fed.lookups <- struct{}{}
	}
	before = w.total()
	if code, out := ask(boss, "@bo@social.example", nil); code != 503 || out["reason"] != "busy" || w.total() != before {
		t.Errorf("past the lookups at once: %d %s", code, out["_raw"])
	}
	for i := 0; i < lookupsAtOnce; i++ {
		<-r.a.fed.lookups
	}

	// While federation is refused, nobody is looked up, and Settings offers no search.
	r.a.fed.refuse("a test refuses federation")
	if code, out := ask(ana, "@bo@social.example", nil); code != 503 || out["reason"] != "unavailable" {
		t.Errorf("federation refused: %d %s", code, out["_raw"])
	}
	if _, out := r.do(ana, "GET", "/api/me", nil); out["federation"].(map[string]any)["lookup"] != nil {
		t.Errorf("/api/me offers the search while federation is refused: %v", out["federation"])
	}
}

// lookupReasonOfCode is the reason a code names.
func lookupReasonOfCode(code string) lookupReason {
	for r, w := range lookupReasons {
		if w.code == code {
			return lookupReason(r)
		}
	}
	return 0
}

// /api/me offers the search in a mode that federates, to every member, and nowhere else.
func TestDBLookupOffered(t *testing.T) {
	f := newEnv(t)
	if err := f.fedStart(nil); err != nil {
		t.Fatal(err)
	}
	_, tok := f.user("ana", "user")
	if _, out := f.do(tok, "GET", "/api/me", nil); out["federation"].(map[string]any)["lookup"] != true {
		t.Errorf("federated: %v", out["federation"])
	}
	l := newEnv(t)
	if err := l.a.start(func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	l.h = l.a.routes(staticOf(t, nil))
	_, ltok := l.user("ana", "user")
	if _, out := l.do(ltok, "GET", "/api/me", nil); out["federation"] != nil || strings.Contains(out["_raw"].(string), "lookup") {
		t.Errorf("local: %s", out["_raw"])
	}
	s := newEnv(t)
	if err := s.fedSingleStart(map[string]string{"FLOWA_FEDERATE": "off"}); err != nil {
		t.Fatal(err)
	}
	var owner int64
	s.a.db.QueryRow(`SELECT id FROM users WHERE owner`).Scan(&owner)
	if _, out := s.do(s.session(owner), "GET", "/api/me", nil); out["federation"] != nil || strings.Contains(out["_raw"].(string), "lookup") {
		t.Errorf("plain single mode: %s", out["_raw"])
	}
	// With the community flag off there is no timeline and no card: /api/me offers no search, and the route
	// answers as the other community routes do then.
	f.setFlag("community", false)
	if _, out := f.do(tok, "GET", "/api/me", nil); out["federation"].(map[string]any)["lookup"] != nil {
		t.Errorf("community off: %v", out["federation"])
	}
	if code, _ := f.do(tok, "POST", "/api/fed/lookup", map[string]any{"q": "@ana@flowa.example.org"}); code != 404 {
		t.Errorf("community off: the route answers %d", code)
	}
}

// A handle's name part (the review's H1): what RFC 7565's userpart allows, narrowed to letters, digits, marks
// after them and "-._~", with no control, format, private-use, separator or unseen character.
func TestHandleNameOK(t *testing.T) {
	for n, want := range map[string]bool{
		"bo": true, "Gargron": true, "a.b-c_d~e": true, "ana.": true, "_x": true, "\u540d\u524d": true, "Zo\u00eb": true, "e\u0301": true, "x1": true,
		"": false, "a b": false, "a\nb": false, "a\x00": false, "a@b": false, "a/b": false, "a!b": false, "a=b": false, "a+b": false,
		"x\u202eelpmaxe.doog": false, "x\u2066y": false, "a\u200bb": false, "a\u200db": false, "a\ufeffb": false, "a\u00adb": false,
		"a\u2028b": false, "a\ue000b": false, "\u3164": false, "a\u034fb": false, "a\ufe0fb": false, "\u0301a": false, "._\u0301": false,
		"a\u00a0b": false, "a\u3000b": false, "\U0001F600": false, strings.Repeat("a", remoteNameMax+1): false, "\xff": false,
	} {
		if got := handleNameOK(n); got != want {
			t.Errorf("%q: %v, want %v", n, got, want)
		}
	}
	// The same rule for the query, a subject and an acct URI built from an actor.
	for _, q := range []string{"x%E2%80%AEelpmaxe.doog@other.example", "a%E2%80%8Bb@other.example", "a%0Ab@other.example"} {
		in, bad := parseLookup(q)
		if bad != "" {
			continue // refused as a query already
		}
		if handleNameOK(in.name) {
			t.Errorf("%q: the name %q passes", q, in.name)
		}
	}
	if _, _, ok := parseAcct("acct:x%E2%80%AEelpmaxe.doog@other.example"); ok {
		t.Error("a subject with a direction mark is read")
	}
}

// What holds of every name handleNameOK takes, as the page shows it: "@" name "@" host has no direction
// control, no line break, no format or control character, and percent-encoding it into an acct URI and
// reading that back gives the same name.
func FuzzHandleName(f *testing.F) {
	for _, s := range []string{"bo", "x\u202eelpmaxe.doog", "\u540d\u524d", "e\u0301", "a\u200db", "a%20b", "\u0301"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, n string) {
		if !handleNameOK(n) {
			return
		}
		shown := "@" + n + "@other.example"
		for _, r := range shown {
			if bidiOverride(r) || r == '\n' || r == '\r' || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.In(r, unicode.Zl, unicode.Zp, unicode.Zs) || r == 0x200E || r == 0x200F || r == 0x061C {
				t.Fatalf("%U in a name taken: %q", r, n)
			}
		}
		if got, h, ok := parseAcct(acctURI(n, "other.example")); !ok || got != n || h != "other.example" {
			t.Fatalf("%q does not read back: %q %q %v", n, got, h, ok)
		}
	})
}

// The review's probes (H1, M1, LOW 1), kept as tests: a name that turns the text's direction is no account,
// whether the actor has a name of its own or not; and links go only to the actor's origin or its handle's.
func TestDBLookupReviewProbes(t *testing.T) {
	r := newRemoteEnv(t)
	w := r.w
	pub := []string{publicCollection}
	// H1: preferredUsername "x" U+202E "elpmaxe.doog", no name, a WebFinger that agrees, asked by another name.
	ev := "https://other.example/users/x"
	pref := "x\u202eelpmaxe.doog"
	w.put(ev, worldDoc{body: actorDoc(ev, pref, "", nil, map[string]any{"outbox": ev + "/outbox"})})
	putWF(w, "other.example", acctURI(pref, "other.example"), acctURI(pref, "other.example"), ev)
	putWF(w, "other.example", "acct:q@other.example", acctURI(pref, "other.example"), ev)
	for _, q := range []string{"q@other.example", "https://other.example/users/x", "x%E2%80%AEelpmaxe.doog@other.example"} {
		if p, err := lookupOf(t, r, q); lookupCode(err) != "notAccount" {
			t.Errorf("%s: %+v %v, want notAccount", q, p, err)
		}
	}
	// The same with a name of its own: the handle is what is refused.
	ev2 := "https://other.example/users/y"
	pref2 := "y\u200bz"
	w.put(ev2, worldDoc{body: actorDoc(ev2, pref2, "Why", nil, nil)})
	putWF(w, "other.example", acctURI(pref2, "other.example"), acctURI(pref2, "other.example"), ev2)
	if p, err := lookupOf(t, r, "https://other.example/users/y"); lookupCode(err) != "notAccount" {
		t.Errorf("a zero-width space in the handle: %+v %v", p, err)
	}
	// LOW 1: links elsewhere than the actor's origin or its handle's host are not given.
	cc := "https://big.example/users/c"
	person(w, cc, "c", map[string]any{"url": "https://bank.example/login"})
	outbox(w, cc, noteItem(cc, cc+"/s/1", "hi", pub, nil, map[string]any{"url": "https://bank.example/phish"}))
	p, err := lookupOf(t, r, "c@big.example")
	if err != nil || p.Link != cc || len(p.Posts) != 1 || p.Posts[0].Link != cc+"/s/1" {
		t.Errorf("links elsewhere: %+v %v", p, err)
	}
}

// The review's M2: a public name that resolves to a private address (refused at connect time) and a name that
// does not resolve are told the same, with the same status and body, and neither is answered sooner than
// lookupFloor after the lookup began. The clock is the test's: nothing waits.
func TestDBLookupTimingFloor(t *testing.T) {
	r := newRemoteEnv(t)
	_, ana := r.user("ana", "user")
	var mu sync.Mutex
	var paused []time.Duration
	t0 := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	elapsed := time.Duration(0)
	r.a.fed.lookupNow = func() time.Time { mu.Lock(); defer mu.Unlock(); e := elapsed; return t0.Add(e) }
	r.a.fed.lookupPause = func(_ context.Context, d time.Duration) { mu.Lock(); paused = append(paused, d); mu.Unlock() }
	ask := func(q string) (int, string, []time.Duration) {
		t.Helper()
		mu.Lock()
		paused = nil
		mu.Unlock()
		code, out := r.do(ana, "POST", "/api/fed/lookup", map[string]any{"q": q})
		mu.Lock()
		defer mu.Unlock()
		return code, out["_raw"].(string), append([]time.Duration(nil), paused...)
	}
	c1, b1, p1 := ask("bo@private.example") // resolves to 10.0.0.1, refused at connect time
	c2, b2, p2 := ask("bo@nowhere.example") // does not resolve
	if c1 != 404 || c1 != c2 || b1 != b2 {
		t.Errorf("refused at connect %d %s, no such name %d %s: told apart", c1, b1, c2, b2)
	}
	if len(p1) != 1 || len(p2) != 1 || p1[0] != lookupFloor || p2[0] != lookupFloor {
		t.Errorf("pauses %v and %v, want %v each", p1, p2, lookupFloor)
	}
	// A lookup that took part of the floor waits the rest; one that took longer does not wait.
	r.a.fed.lookupNow = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		elapsed += 300 * time.Millisecond
		return t0.Add(elapsed)
	}
	if _, _, p := ask("bo@nowhere.example"); len(p) != 1 || p[0] != lookupFloor-300*time.Millisecond {
		t.Errorf("after 300 ms: pause %v", p)
	}
	r.a.fed.lookupNow = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		elapsed += 2 * lookupFloor
		return t0.Add(elapsed)
	}
	if _, _, p := ask("bo@nowhere.example"); len(p) != 0 {
		t.Errorf("a slow lookup waited again: %v", p)
	}
	// What is refused from the typed text alone is refused at once, with its own reason; an answer that is
	// no account and one that finds someone do not wait.
	if code, b, p := ask("bo@10.0.0.1"); code != 422 || !strings.Contains(b, `"refused"`) || len(p) != 0 {
		t.Errorf("an IP address typed: %d %s %v", code, b, p)
	}
	r.w.put(wfURL("social.example", "acct:html@social.example"), worldDoc{ctype: "text/html", body: []byte("<p>no</p>")})
	if code, _, p := ask("html@social.example"); code != 422 || len(p) != 0 {
		t.Errorf("not an account: %d %v", code, p)
	}
}
