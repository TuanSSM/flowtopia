package main

// Domain policies and the allowlist (docs/federation.md, F8, section 10; FMD 1).

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
)

func TestPolicyDecide(t *testing.T) {
	rows := map[string]domainPolicy{
		"allowed.example":     {Policy: "allow"},
		"bad.allowed.example": {Policy: "suspend"},
		"limited.example":     {Policy: "limit"},
		"evil.example":        {Policy: "suspend"},
		"ok.evil.example":     {Policy: "allow"},
	}
	for _, mode := range []string{policyAllowlist, policyOpen} {
		p := &fedPolicy{mode: mode, rows: rows}
		listed := func(v policyVerdict) policyVerdict {
			if mode == policyOpen && v == policyNotListed {
				return policyIn
			}
			return v
		}
		for host, want := range map[string]policyVerdict{
			"allowed.example": policyIn, "Allowed.Example.": policyIn, "a.b.allowed.example": policyIn,
			"bad.allowed.example": policySuspended, "x.bad.allowed.example": policySuspended,
			"limited.example": policyIn, "evil.example": policySuspended, "ok.evil.example": policySuspended,
			"stranger.example": listed(policyNotListed), "example": policyUnreadable, "10.0.0.1": policyUnreadable,
			"allowed.example.evil.test": listed(policyNotListed), "xallowed.example": listed(policyNotListed),
		} {
			if got := p.decide(host); got != want {
				t.Errorf("%s: %s: %d, want %d", mode, host, got, want)
			}
		}
	}
	if (&federation{}).policyNow().decide("anyone.example") != policyNotListed {
		t.Error("before the policy is read, a host is let in")
	}
	// Limit and reject media walk up as a suspension does, the strictest row winning: a subdomain of a limited domain is
	// limited even when allowed, a limited domain is listed in allowlist mode, and an allow alone limits nothing.
	p := &fedPolicy{mode: policyAllowlist, rows: map[string]domainPolicy{
		"limited.example": {Policy: "limit"}, "ok.limited.example": {Policy: "allow"},
		"pics.example": {Policy: "allow", RejectMedia: true}, "allowed.example": {Policy: "allow"},
	}}
	for host, want := range map[string][2]bool{
		"limited.example": {true, false}, "a.ok.limited.example": {true, false}, "Limited.Example.": {true, false},
		"pics.example": {false, true}, "cdn.pics.example": {false, true}, "allowed.example": {false, false},
		"xlimited.example": {false, false}, "stranger.example": {false, false}, "10.0.0.1": {false, false},
	} {
		if got := [2]bool{p.limited(host), p.rejectsMedia(host)}; got != want {
			t.Errorf("%s: limited, rejects media %v; want %v", host, got, want)
		}
	}
	if p.decide("a.ok.limited.example") != policyIn || p.decide("limited.example") != policyIn {
		t.Error("allowlist mode refuses a limited domain, which is listed")
	}
	for in, want := range map[string]string{
		" Social.Example. ": "social.example", "xn--bcher-kva.example": "xn--bcher-kva.example",
		"": "", "https://social.example": "", "social.example/x": "", "@a@social.example": "", "social.example:443": "",
		"bücher.example": "", "localhost": "", "single": "", "10.0.0.1": "", "a.local": "",
		"flowa.example.org": "", "example.org": "", "sub.flowa.example.org": "sub.flowa.example.org",
	} {
		got, why := cleanDomain(in, "flowa.example.org")
		if got != want || (want == "") == (why == "") {
			t.Errorf("cleanDomain(%q) = %q, %q; want %q", in, got, why, want)
		}
	}
}

// The admin's API: who may read and change, each change audited, the policy in memory at once, the fetcher's
// refused list following it, and nothing of it in local mode.
func TestDBDomainPolicies(t *testing.T) {
	e := newEnv(t)
	if err := e.fedStart(nil); err != nil {
		t.Fatal(err)
	}
	_, admin := e.user("boss", "admin")
	_, mod := e.user("mo", "moderator")
	_, ana := e.user("ana", "user")
	f := e.a.fed
	out := e.must(200, mod, "GET", "/api/admin/federation", nil)
	if out["policy"] != policyAllowlist || len(out["domains"].([]any)) != 0 {
		t.Fatalf("with nothing saved: %v", out["_raw"])
	}
	e.must(403, ana, "GET", "/api/admin/federation", nil)
	e.must(403, mod, "PUT", "/api/admin/federation/domains/social.example", map[string]any{"policy": "allow"})
	e.must(403, mod, "PUT", "/api/admin/federation/policy", map[string]any{"policy": "open"})
	for _, perms := range []struct {
		tok  string
		want []string
	}{{admin, []string{permFedRead, permFedManage}}, {mod, []string{permFedRead}}, {ana, nil}} {
		got := fmt.Sprint(e.must(200, perms.tok, "GET", "/api/me", nil)["perms"])
		for _, p := range []string{permFedRead, permFedManage} {
			if strings.Contains(got, p) != slices.Contains(perms.want, p) {
				t.Errorf("perms %s: %s", got, p)
			}
		}
	}

	out = e.must(200, admin, "PUT", "/api/admin/federation/domains/Social.Example", map[string]any{"policy": "allow", "note": "  friends  "})
	if d := out["domains"].([]any); len(d) != 1 || d[0].(map[string]any)["domain"] != "social.example" || d[0].(map[string]any)["note"] != "friends" || d[0].(map[string]any)["createdBy"] != "boss" {
		t.Fatalf("after an allow: %v", out["_raw"])
	}
	e.must(200, admin, "PUT", "/api/admin/federation/domains/evil.example", map[string]any{"policy": "suspend", "note": "spam"})
	if f.policyNow().decide("a.social.example") != policyIn || f.policyNow().decide("evil.example") != policySuspended || f.policyNow().decide("other.example") != policyNotListed {
		t.Error("the policy in memory does not follow the changes")
	}
	if got := f.refusedByPolicy(); !slices.Equal(got, []string{"evil.example"}) {
		t.Errorf("the fetcher's refused list: %v", got)
	}
	e.must(200, admin, "PUT", "/api/admin/federation/policy", map[string]any{"policy": "open"})
	if f.policyNow().decide("other.example") != policyIn {
		t.Error("open mode refuses an unlisted host")
	}
	e.must(200, admin, "DELETE", "/api/admin/federation/domains/evil.example", nil)
	e.must(404, admin, "DELETE", "/api/admin/federation/domains/evil.example", nil)
	if f.policyNow().decide("evil.example") != policyIn || len(f.refusedByPolicy()) != 0 {
		t.Error("an unsuspended domain is still refused")
	}
	for _, c := range []struct {
		path string
		body map[string]any
		code int
	}{
		{"/api/admin/federation/domains/flowa.example.org", map[string]any{"policy": "suspend"}, 400},
		{"/api/admin/federation/domains/" + url.PathEscape("https://x.example"), map[string]any{"policy": "allow"}, 400},
		{"/api/admin/federation/domains/x.example", map[string]any{"policy": "silence"}, 400},
		{"/api/admin/federation/domains/x.example", map[string]any{"policy": "allow", "note": strings.Repeat("n", domainNoteMax+1)}, 400},
		{"/api/admin/federation/domains/x.example", map[string]any{"policy": "allow", "note": "a\u202eb"}, 400},
		{"/api/admin/federation/domains/x.example", map[string]any{"policy": "limit", "comment": strings.Repeat("c", domainCommentMax+1)}, 400},
		{"/api/admin/federation/domains/x.example", map[string]any{"policy": "limit", "comment": "a\u202eb"}, 400},
		{"/api/admin/federation/policy", map[string]any{"policy": "closed"}, 400},
		{"/api/admin/federation/publish", map[string]any{}, 400},
	} {
		method := "PUT"
		e.must(c.code, admin, method, c.path, c.body)
	}
	// A fresh start reads what was saved.
	g := e.again()
	if err := g.a.start(fedEnv(nil)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.a.fed.stop(context.Background()) })
	if p := g.a.fed.policyNow(); p.mode != policyOpen || p.decide("social.example") != policyIn || len(p.rows) != 1 {
		t.Errorf("after a restart: %+v", p)
	}
	audit := map[string]int{}
	rows, err := e.a.db.Query(`SELECT action FROM mod_actions`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var a string
		rows.Scan(&a)
		audit[a]++
	}
	rows.Close()
	if audit["domain_policy"] != 2 || audit["domain_policy_remove"] != 1 || audit["federation_policy"] != 1 {
		t.Errorf("the audit log: %v", audit)
	}

	// Local mode: no route, no permission listed, no policy read.
	l := newEnv(t)
	if err := l.a.start(func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	l.h = l.a.routes(staticOf(t, nil))
	_, ladmin := l.user("boss", "admin")
	if got := fmt.Sprint(l.must(200, ladmin, "GET", "/api/me", nil)["perms"]); strings.Contains(got, "federation") {
		t.Errorf("local mode lists %s", got)
	}
	l.must(404, ladmin, "GET", "/api/admin/federation", nil)
	l.must(404, "", "GET", "/api/about/servers", nil)
	if _, ok := l.must(200, ladmin, "GET", "/api/instance", nil)["moderatedServers"]; ok {
		t.Error("local mode's about page names moderated servers")
	}

	// Each policy in and out (FMD 1's DoD): allow and suspend above; the values, the pages and the about page; limit,
	// with a Follow from a limited domain waiting for approval; reject media. The allowlist both ways, with a limited
	// domain let in, is TestDBPolicyBothWays (the inbox's side of it TestInboxRefusals).
	t.Run("values, pages and the about page", testDomainValues)
	t.Run("limit in and out", testDomainLimit)
	t.Run("reject media in and out", testDomainRejectMedia)
	t.Run("a host with a trailing dot", testDomainTrailingDot)
}

// A host with a trailing dot names the same server (DNS, fedHost and the policy read both alike) but is another id. An
// actor whose id has one is not kept, so no account is stored under a domain the policy's statements could miss, and
// the same person cannot be kept twice, with and without the dot; the account without it is limited, and its
// pictures rejected, as its domain's row says.
func testDomainTrailingDot(t *testing.T) {
	r := newRemoteEnv(t)
	r.mustExec(`INSERT INTO domain_policies (domain, policy, reject_media) VALUES ('social.example', 'limit', true)`)
	if err := r.a.fed.loadPolicy(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := actorHost("https://Social.Example./users/dot"); got != "social.example" {
		t.Errorf("actorHost keeps the dot: %q", got)
	}
	dotted, plain := "https://social.example./users/dot", "https://social.example/users/dot"
	r.w.put(dotted, worldDoc{body: actorDoc(dotted, "dot", "Dot", r.pub, nil)})
	r.w.put(plain, worldDoc{body: actorDoc(plain, "dot", "Dot", r.pub, nil)})
	if _, err := r.actor(dotted); reason(err) != actorWrongID {
		t.Errorf("an actor id with a trailing dot: %v", err)
	}
	if n := r.count(`SELECT count(*) FROM users WHERE domain LIKE '%.' OR domain LIKE 'social.example%'`); n != 0 {
		t.Fatalf("%d accounts kept for an id with a trailing dot", n)
	}
	ra, err := r.actor(plain)
	if err != nil {
		t.Fatal(err)
	}
	r.actor(dotted)
	if n := r.count(`SELECT count(*) FROM users WHERE domain LIKE 'social.example%'`); n != 1 || ra.Domain != "social.example" {
		t.Errorf("the account is kept %d times, under %q", n, ra.Domain)
	}
	var limited, rejected bool
	if err := r.a.db.QueryRow(`SELECT NOT `+limitFree("$1::bigint", "0")+`, `+policyWalk("(SELECT domain FROM users WHERE id = $1)", "dw.reject_media"), ra.UserID).Scan(&limited, &rejected); err != nil {
		t.Fatal(err)
	}
	if !limited || !rejected {
		t.Errorf("the account of social.example: limited %v, pictures rejected %v", limited, rejected)
	}
}

// The values an admin saves with a domain, the list a page at a time with its search, the switch that publishes the
// list on the about page, and the audit of each.
func testDomainValues(t *testing.T) {
	e := newEnv(t)
	if err := e.fedStart(nil); err != nil {
		t.Fatal(err)
	}
	_, admin := e.user("boss", "admin")
	_, mod := e.user("mo", "moderator")
	_, ana := e.user("ana", "user")
	f := e.a.fed
	out := e.must(200, admin, "PUT", "/api/admin/federation/domains/loud.example", map[string]any{"policy": "limit", "comment": " Too <b>loud</b> ", "note": "many reports", "rejectMedia": true, "rejectReports": true})
	d := out["domains"].([]any)[0].(map[string]any)
	if d["policy"] != "limit" || d["comment"] != "Too <b>loud</b>" || d["note"] != "many reports" || d["rejectMedia"] != true || d["rejectReports"] != true {
		t.Fatalf("a limit with its values: %v", d)
	}
	if !f.policyNow().limited("a.loud.example") || !f.policyNow().rejectsMedia("loud.example") || f.policyNow().decide("loud.example") != policyIn {
		t.Error("the policy in memory does not hold the limit")
	}
	// A suspension stores its two switches off: it rejects everything already.
	d = e.must(200, admin, "PUT", "/api/admin/federation/domains/bad.example", map[string]any{"policy": "suspend", "rejectMedia": true, "rejectReports": true})["domains"].([]any)[0].(map[string]any)
	if d["domain"] != "bad.example" || d["rejectMedia"] != false || d["rejectReports"] != false {
		t.Errorf("a suspension's switches: %v", d)
	}
	var detail string
	e.a.db.QueryRow(`SELECT detail::text FROM mod_actions WHERE action = 'domain_policy' ORDER BY id LIMIT 1`).Scan(&detail)
	if !strings.Contains(detail, `"rejectMedia": true`) || !strings.Contains(detail, `"rejectReports": true`) || !strings.Contains(detail, `"comment": "Too <b>loud</b>"`) {
		t.Errorf("the audit of a limit: %s", detail)
	}

	// The list a page at a time: 250 rows more, three pages of 100; a search and a policy narrow it, and a page names
	// the domain the next begins after.
	e.mustExec(`INSERT INTO domain_policies (domain, policy) SELECT 'd' || lpad(g::text, 3, '0') || '.example', CASE WHEN g % 5 = 0 THEN 'suspend' ELSE 'allow' END FROM generate_series(1, 250) g`)
	if err := f.loadPolicy(context.Background()); err != nil {
		t.Fatal(err)
	}
	var seen []string
	for after, pages := "", 0; ; pages++ {
		if pages > 3 {
			t.Fatal("the pages do not end")
		}
		p := e.must(200, mod, "GET", "/api/admin/federation?after="+url.QueryEscape(after), nil)
		rows := p["domains"].([]any)
		if len(rows) > domainPageSize || num(p["total"]) != 252 {
			t.Fatalf("a page: %d rows, total %v", len(rows), p["total"])
		}
		for _, r := range rows {
			seen = append(seen, r.(map[string]any)["domain"].(string))
		}
		if after = p["next"].(string); after == "" {
			break
		}
	}
	if len(seen) != 252 || !slices.IsSorted(seen) || slices.Compact(slices.Clone(seen))[0] != seen[0] || len(slices.Compact(slices.Clone(seen))) != 252 {
		t.Errorf("the pages hold %d domains, in order %v", len(seen), slices.IsSorted(seen))
	}
	p := e.must(200, mod, "GET", "/api/admin/federation?q=D01&policy=suspend", nil)
	if num(p["matched"]) != 2 || len(p["domains"].([]any)) != 2 || p["next"] != "" || num(p["counts"].(map[string]any)["suspend"]) != 51 {
		t.Errorf("a search: %v matched, %v", p["matched"], p["domains"])
	}

	// The about page: nothing is listed until an admin publishes it; then a member reads the limited and suspended
	// domains with their public comments (never the note), and a visitor only once the about page is open to visitors.
	e.must(404, ana, "GET", "/api/about/servers", nil)
	if _, ok := e.must(200, ana, "GET", "/api/instance", nil)["moderatedServers"]; ok {
		t.Error("the about page names moderated servers before an admin publishes them")
	}
	e.must(403, mod, "PUT", "/api/admin/federation/publish", map[string]any{"publish": true})
	if e.must(200, admin, "PUT", "/api/admin/federation/publish", map[string]any{"publish": true})["publish"] != true {
		t.Error("publishing is not read back")
	}
	if e.must(200, ana, "GET", "/api/instance", nil)["moderatedServers"] != true {
		t.Error("the about page does not say the list is published")
	}
	s := e.must(200, ana, "GET", "/api/about/servers", nil)
	list := s["servers"].([]any)
	if len(list) != 52 || s["next"] != "" || strings.Contains(s["_raw"].(string), "many reports") || strings.Contains(s["_raw"].(string), "allow") {
		t.Errorf("the published list: %d servers, %v", len(list), s["_raw"])
	}
	if l := list[slices.IndexFunc(list, func(v any) bool { return v.(map[string]any)["domain"] == "loud.example" })].(map[string]any); l["policy"] != "limit" || l["comment"] != "Too <b>loud</b>" {
		t.Errorf("loud.example on the about page: %v", l)
	}
	e.must(404, "", "GET", "/api/about/servers", nil)
	e.must(200, admin, "PUT", "/api/admin/instance", map[string]any{"description": "A Flowa.", "contact": "", "rules": []any{}, "updatedAt": ""})
	if n := len(e.must(200, "", "GET", "/api/about/servers", nil)["servers"].([]any)); n != 52 {
		t.Errorf("a visitor reads %d servers", n)
	}
	e.must(200, admin, "PUT", "/api/admin/federation/publish", map[string]any{"publish": false})
	e.must(404, ana, "GET", "/api/about/servers", nil)
	if e.count(`SELECT count(*) FROM mod_actions WHERE action = 'federation_publish'`) != 2 {
		t.Error("publishing is not audited")
	}
}

// Limit (section 10, Mastodon's domain Limit), in an instance in allowlist mode, where a limited domain is listed and
// so let in: social.example's accounts are shown here only to the members who follow them. Ana follows bo and not cat,
// both of social.example; dee follows cat and not bo.Hidden from ana: cat's reply under her post (listed and counted alike),
// the notifications cat left, a post of cat's that only names her, cat's bio. A Follow from cat waits for ana's
// approval though she approves nobody by hand; a follow that existed stays; lifting the limit shows what was hidden
// and leaves the request waiting.
func testDomainLimit(t *testing.T) {
	e := newRepliesEnv(t)
	_, admin := e.user("boss", "admin")
	deeID, dee := e.member("dee", false)
	e.mustExec(`INSERT INTO follows (follower_id, followee_id, state) VALUES ($1, $2, 'accepted'), ($2, $1, 'accepted')`, deeID, e.catID)
	e.mustExec(`DELETE FROM follows WHERE follower_id = $1 AND followee_id = $2`, e.catID, e.anaID) // so cat's Follow of ana below is new
	e.mustExec(`UPDATE ap_actors SET summary = 'cat bio' WHERE user_id = $1`, e.catID)
	limit := func(policy string) {
		t.Helper()
		e.must(200, admin, "PUT", "/api/admin/federation/domains/social.example", map[string]any{"policy": policy})
	}
	limit("limit")

	// In: a limited domain is listed, so its deliveries are taken in allowlist mode.
	if why := e.send(e.cat(), catID, "Create", reply(catID, 1, e.postID, "hi from cat")); why != inAccepted || e.outcome() != "done:" {
		t.Fatalf("cat's reply from a limited domain: %v %s", why, e.outcome())
	}
	e.send(e.bo(), boID, "Create", reply(boID, 2, e.postID, "hi from bo"))
	count := func(tok string) int64 {
		t.Helper()
		return num(e.must(200, tok, "GET", fmt.Sprintf("/api/posts/%d", e.post), nil)["posts"].([]any)[0].(map[string]any)["commentCount"])
	}
	if got := bodies(e.thread(e.ana, e.post)); !slices.Equal(got, []string{"hi from bo"}) || count(e.ana) != 1 {
		t.Errorf("ana, who does not follow cat, reads %v, counted %d", got, count(e.ana))
	}
	if got := bodies(e.thread(dee, e.post)); !slices.Equal(got, []string{"hi from cat"}) || count(dee) != 1 {
		t.Errorf("dee, who follows cat and not bo, reads %v, counted %d", got, count(dee))
	}
	bell := func(tok string) (kinds []string, unread int64) {
		t.Helper()
		out := e.must(200, tok, "GET", "/api/notifications", nil)
		for _, it := range out["items"].([]any) {
			kinds = append(kinds, it.(map[string]any)["kind"].(string))
		}
		return kinds, num(out["unread"])
	}
	if k, n := bell(e.ana); !slices.Equal(k, []string{"comment"}) || n != 1 {
		t.Errorf("ana's bell while social.example is limited: %v, %d unread", k, n)
	}

	// A post of cat's that only names ana does not reach her; dee, who follows cat, is not named and does not read it.
	e.send(e.cat(), catID, "Create", map[string]any{"id": catID + "/statuses/3", "type": "Note", "attributedTo": catID, "content": "<p>@ana look</p>",
		"to": []string{asPublic}, "cc": []string{"https://here.example/users/ana"}, "tag": []any{map[string]any{"type": "Mention", "href": "https://here.example/users/ana", "name": "@ana"}}})
	named := e.remotePostID(catID + "/statuses/3")
	e.must(404, e.ana, "GET", fmt.Sprintf("/api/posts/%d", named), nil)

	// A Follow from cat: a request, though ana approves nobody by hand, and she is told of it. A Follow from bo, followed by
	// ana, is one too: the limit is the server's, whoever follows whom. The follow cat had of dee stays.
	e.deliver(activity(catID, "Follow", "https://here.example/users/ana", nil), e.cat())
	if e.state(e.catID, e.anaID) != "pending" {
		t.Fatalf("cat's Follow of ana: %q (%s)", e.state(e.catID, e.anaID), e.outcome())
	}
	if k, _ := bell(e.ana); !slices.Contains(k, "follow_request") {
		t.Errorf("ana is not told of cat's request: %v", k)
	}
	if reqs := e.must(200, e.ana, "GET", "/api/fed/requests", nil)["requests"].([]any); len(reqs) != 1 {
		t.Errorf("ana's requests: %v", reqs)
	}
	e.deliver(activity(boID, "Follow", "https://here.example/users/ana", nil), e.bo())
	if e.state(e.boUser, e.anaID) != "pending" || e.state(e.catID, deeID) != "accepted" {
		t.Errorf("bo's Follow of ana: %q; cat's follow of dee: %q", e.state(e.boUser, e.anaID), e.state(e.catID, deeID))
	}
	// The profile here says so, and its bio is for followers only.
	if a := e.must(200, e.ana, "GET", fmt.Sprintf("/api/fed/accounts/%d", e.catID), nil)["account"].(map[string]any); a["limited"] != fedLimitedNotice || a["bio"] != nil {
		t.Errorf("cat's profile for ana: %v", a)
	}
	if a := e.must(200, dee, "GET", fmt.Sprintf("/api/fed/accounts/%d", e.catID), nil)["account"].(map[string]any); a["limited"] != fedLimitedNotice || a["bio"] != "cat bio" {
		t.Errorf("cat's profile for dee: %v", a)
	}

	// Out: lifted (allowed again), everything shows; the requests stay requests until ana answers them.
	limit("allow")
	if got := bodies(e.thread(e.ana, e.post)); !slices.Equal(got, []string{"hi from cat", "hi from bo"}) || count(e.ana) != 2 {
		t.Errorf("after the limit, ana reads %v, counted %d", got, count(e.ana))
	}
	if k, _ := bell(e.ana); !slices.Contains(k, "mention") || len(slices.DeleteFunc(slices.Clone(k), func(s string) bool { return s != "comment" })) != 2 {
		t.Errorf("after the limit, ana's bell: %v", k)
	}
	e.must(200, e.ana, "GET", fmt.Sprintf("/api/posts/%d", named), nil)
	if e.state(e.catID, e.anaID) != "pending" || e.state(e.boUser, e.anaID) != "pending" {
		t.Errorf("lifting the limit answered the requests: cat %q, bo %q", e.state(e.catID, e.anaID), e.state(e.boUser, e.anaID))
	}
	if a := e.must(200, e.ana, "GET", fmt.Sprintf("/api/fed/accounts/%d", e.catID), nil)["account"].(map[string]any); a["limited"] != nil || a["bio"] != "cat bio" {
		t.Errorf("cat's profile after the limit: %v", a)
	}
	// A Follow after the limit is lifted is accepted at once again.
	e.mustExec(`DELETE FROM follows WHERE follower_id = $1 AND followee_id = $2`, e.catID, e.anaID)
	e.deliver(activity(catID, "Follow", "https://here.example/users/ana", nil), e.cat())
	if e.state(e.catID, e.anaID) != "accepted" {
		t.Errorf("cat's Follow once the limit is lifted: %q", e.state(e.catID, e.anaID))
	}
	// The statement that keeps a Follow asks the stored policy, not the one in memory: a limit written and not yet read
	// into memory (as when it is set while the Follow waits in the inbox's queue) holds it.
	e.mustExec(`DELETE FROM follows WHERE follower_id = $1 AND followee_id = $2`, e.boUser, e.anaID)
	e.mustExec(`UPDATE domain_policies SET policy = 'limit' WHERE domain = 'social.example'`)
	e.deliver(activity(boID, "Follow", "https://here.example/users/ana", nil), e.bo())
	if e.state(e.boUser, e.anaID) != "pending" || e.a.fed.policyNow().limited("social.example") {
		t.Errorf("a Follow under a stored limit not yet in memory: %q", e.state(e.boUser, e.anaID))
	}
	// An admin's change is read into memory though the admin's request ended before the answer (the connection
	// dropped after the change was committed): the request's own context is not what the reading waits on.
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	e.a.fedPolicyChanged(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/admin/federation", nil).WithContext(gone), nil)
	if !e.a.fed.policyNow().limited("social.example") {
		t.Error("a change whose request ended early was not read into memory")
	}
}

// Every look at a limited domain the statements over posts, comments and the bell carry is one limitFragments names, so
// an instance that limits no domain runs none of them (limitStatements), and one that does runs them all.
func TestLimitFragments(t *testing.T) {
	for name, q := range map[string]string{
		"feed": feedSQL, "one post": postOneSQL, "home": homeSQL, "preview": commentsSQL(feedCommentsSQL), "replies": commentsSQL(repliesSQL),
		"comment page": commentsSQL(commentPageSQL), "reply page": commentsSQL(replyPageSQL), "bell": notificationsFedSQL, "unread": unreadFedSQL,
	} {
		if strings.Contains(unlimited(q), "domain_policies") {
			t.Errorf("%s: a look at a limited domain is left in by unlimited", name)
		}
		if !strings.Contains(q, "dw.policy = 'limit'") {
			t.Errorf("%s: no look at a limited domain at all", name)
		}
	}
	a := &app{fed: &federation{}}
	a.fed.policy.Store(&fedPolicy{mode: policyOpen, rows: map[string]domainPolicy{"x.example": {Policy: "allow"}}})
	if strings.Contains(a.localStatements(feedSQL), "domain_policies") {
		t.Error("an instance that limits no domain asks about limits")
	}
	a.fed.policy.Store(&fedPolicy{mode: policyOpen, anyLimit: true, rows: map[string]domainPolicy{"x.example": {Policy: "limit"}}})
	if a.localStatements(feedSQL) != feedSQL {
		t.Error("an instance that limits a domain runs the statements without the limit")
	}
}

// The walk of the stored policy (policyWalk) is the walk of the one in memory: a domain and every domain above it, by
// whole labels, never a top-level name alone.
func TestDBPolicyWalkSQL(t *testing.T) {
	e := newEnv(t)
	e.mustExec(`INSERT INTO domain_policies (domain, policy, reject_media) VALUES ('limited.example', 'limit', false), ('ok.limited.example', 'allow', false), ('pics.example', 'allow', true)`)
	for host, want := range map[string][2]bool{
		"limited.example": {true, false}, "a.ok.limited.example": {true, false}, "pics.example": {false, true}, "cdn.pics.example": {false, true},
		"xlimited.example": {false, false}, "example": {false, false}, "": {false, false}, "limited.example.evil": {false, false},
		// A trailing dot is read as fedHost reads it, so a domain stored with one is still held to its rows.
		"a.ok.limited.example.": {true, false}, "cdn.pics.example.": {false, true}, ".": {false, false},
	} {
		var got [2]bool
		if err := e.a.db.QueryRow(`SELECT `+policyWalk("$1::text", "dw.policy = 'limit'")+`, `+policyWalk("$1::text", "dw.reject_media"), host).Scan(&got[0], &got[1]); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%q: limited, rejects media %v; want %v", host, got, want)
		}
	}
}

// Reject media (section 10, 9.4): set on social.example, the pictures kept of its posts become links, their rows and
// their files both (the files after the commit); a post that comes after keeps its pictures as links; a picture
// wanted before the policy was set is never fetched (the worker asks the stored policy at its claim); the page shows
// each as a link to the original, never as a picture. Lifted, nothing is fetched again.
func testDomainRejectMedia(t *testing.T) {
	e := newNotesInEnv(t)
	_, admin := e.user("boss", "admin")
	m := e.a.fed.media
	e.allow("pics.example") // the pictures' own host is let in: only reject media keeps them from being fetched
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	att := func(urls ...string) []any {
		var out []any
		for _, u := range urls {
			out = append(out, map[string]any{"type": "Document", "mediaType": "image/png", "url": u, "name": "alt"})
		}
		return out
	}
	e.send(e.bo(), boID, "Create", noteObj(1, "two", map[string]any{"attachment": att("https://pics.example/1.png", "https://pics.example/2.png")}))
	post := e.remotePostID(boID + "/statuses/1")
	// The first picture was fetched and kept (as the worker would leave it), the second still waits.
	kept := strings.Repeat("ab", 16) + ".jpg"
	if err := os.WriteFile(filepath.Join(m.dir, kept), []byte("jpeg"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.mustExec(`UPDATE ap_media SET state = 'ok', file = $2, bytes = 4 WHERE post_id = $1 AND n = 0`, post, kept)
	states := func(p int64) []string {
		t.Helper()
		var s []string
		e.a.db.QueryRow(`SELECT COALESCE(array_agg(state || ':' || file ORDER BY n), '{}') FROM ap_media WHERE post_id = $1`, p).Scan(pq.Array(&s))
		return s
	}
	if got := states(post); !slices.Equal(got, []string{"ok:" + kept, "wanted:"}) {
		t.Fatalf("before the policy: %v", got)
	}

	e.must(200, admin, "PUT", "/api/admin/federation/domains/social.example", map[string]any{"policy": "allow", "rejectMedia": true})
	if got := states(post); !slices.Equal(got, []string{"link:", "link:"}) {
		t.Errorf("after reject media: %v", got)
	}
	if _, err := os.Stat(filepath.Join(m.dir, kept)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the kept picture's file: %v", err)
	}
	var detail string
	e.a.db.QueryRow(`SELECT detail::text FROM mod_actions WHERE action = 'domain_policy' ORDER BY id DESC LIMIT 1`).Scan(&detail)
	if !strings.Contains(detail, `"picturesRemoved": 2`) {
		t.Errorf("the audit: %s", detail)
	}
	// A post after it: its pictures are links from the start.
	e.send(e.bo(), boID, "Create", noteObj(2, "one", map[string]any{"attachment": att("https://pics.example/3.png")}))
	if got := states(e.remotePostID(boID + "/statuses/2")); !slices.Equal(got, []string{"link:"}) {
		t.Errorf("a post after reject media: %v", got)
	}
	// A picture still wanted from before (written as a worker that read the old policy would have): claimed, kept as
	// a link, not fetched (a fetch, refused or failed, would leave it failed or wanted again).
	e.mustExec(`INSERT INTO ap_media (post_id, n, kind, url, state, next_at) VALUES ($1, 5, 'picture', 'https://pics.example/5.png', 'wanted', now() - interval '1 minute')`, post)
	if worked, err := m.step(context.Background()); !worked || err != nil {
		t.Fatalf("the worker: %v %v", worked, err)
	}
	if e.count(`SELECT count(*) FROM ap_media WHERE post_id = $1 AND n = 5 AND state = 'link' AND locked_until IS NULL`, post) != 1 {
		t.Errorf("a picture wanted from before: %v", states(post))
	}
	// The page shows links to the originals and no picture of this server.
	p := e.must(200, e.ana, "GET", fmt.Sprintf("/api/posts/%d", post), nil)["posts"].([]any)[0].(map[string]any)
	media := p["remote"].(map[string]any)["media"].([]any)
	if len(media) != 3 || media[0].(map[string]any)["src"] != nil || media[0].(map[string]any)["link"] != "https://pics.example/1.png" {
		t.Errorf("the post as shown: %v", media)
	}

	// Lifted: nothing is wanted again; an edit of the post brings its pictures back with the edit.
	e.must(200, admin, "PUT", "/api/admin/federation/domains/social.example", map[string]any{"policy": "allow"})
	if e.count(`SELECT count(*) FROM ap_media WHERE state = 'wanted'`) != 0 {
		t.Error("lifting reject media wants the pictures again")
	}
	e.send(e.bo(), boID, "Update", noteObj(2, "one, edited", map[string]any{"attachment": att("https://pics.example/3.png"), "updated": "2030-01-01T00:00:00Z"}))
	if got := states(e.remotePostID(boID + "/statuses/2")); !slices.Equal(got, []string{"wanted:"}) {
		t.Errorf("an edit after reject media was lifted: %v (%s)", got, e.outcome())
	}
}

// A suspended domain is refused by the fetcher for every actor of it, before any connection.
func TestDBPolicyRefusesFetch(t *testing.T) {
	r := newRemoteEnv(t)
	r.w.put("https://social.example/users/bo", worldDoc{body: actorDoc("https://social.example/users/bo", "bo", "", r.pub, nil)})
	if _, err := r.a.db.Exec(`INSERT INTO domain_policies (domain, policy) VALUES ('social.example', 'suspend')`); err != nil {
		t.Fatal(err)
	}
	if err := r.a.fed.loadPolicy(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.actor("https://social.example/users/bo"); reason(err) != actorRefused {
		t.Errorf("an actor of a suspended domain: %v", err)
	}
	if n := r.w.total(); n != 0 {
		t.Errorf("%d requests reached a suspended domain", n)
	}
}

// The allowlist both ways (F8, section 10, 7.6; FMD 1's DoD): on an instance in allowlist mode no request leaves
// for a host no row allows, whatever asks for it. A member's search by handle is told so in its own words and
// costs no connection; a key or an actor is not fetched; a WebFinger redirect to an unlisted host is refused by
// the fetcher itself. Allowing the domain lets each through.
func TestDBPolicyBothWays(t *testing.T) {
	r := newRemoteEnv(t)
	r.mustExec(`UPDATE instance_settings SET value = '"allowlist"' WHERE key = 'federation_policy'`)
	if err := r.a.fed.loadPolicy(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.a.fed.lookupPause = func(context.Context, time.Duration) {}
	r.setFlag("community", true)
	_, ana := r.user("ana", "user")
	bo := "https://social.example/users/bo"
	person(r.w, bo, "bo", nil)
	outbox(r.w, bo)
	code, out, _ := r.doH(ana, "POST", "/api/fed/lookup", map[string]any{"q": "@bo@social.example"}, nil)
	if code != 403 || out["reason"] != "notAllowed" || r.w.total() != 0 {
		t.Fatalf("a search for an unlisted server: %d %v, %d requests", code, out["_raw"], r.w.total())
	}
	if _, err := r.a.fed.remoteKey(context.Background(), bo+"#main-key", keyToVerify); reason(err) != actorRefused || r.w.total() != 0 {
		t.Errorf("a key on an unlisted server: %v, %d requests", err, r.w.total())
	}
	if _, err := r.actor(bo); reason(err) != actorRefused || r.w.total() != 0 {
		t.Errorf("an actor on an unlisted server: %v, %d requests", err, r.w.total())
	}
	// Allowed: the search goes through. A handle whose WebFinger redirects to an unlisted host stops there.
	r.mustExec(`INSERT INTO domain_policies (domain, policy) VALUES ('social.example', 'allow')`)
	if err := r.a.fed.loadPolicy(context.Background()); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := r.doH(ana, "POST", "/api/fed/lookup", map[string]any{"q": "@bo@social.example"}, nil); code != 200 {
		t.Errorf("a search for an allowed server: %d %v", code, out["_raw"])
	}
	moved := wfURL("other.example", "acct:moved@social.example")
	r.w.put(wfURL("social.example", "acct:moved@social.example"), worldDoc{status: 301, location: moved})
	putWF(r.w, "other.example", "acct:moved@social.example", "acct:moved@social.example", "https://other.example/users/moved")
	if code, out, _ := r.doH(ana, "POST", "/api/fed/lookup", map[string]any{"q": "@moved@social.example"}, nil); code != 404 || r.w.hit(moved) != 0 {
		t.Errorf("a WebFinger redirect to an unlisted host: %d %v, %d requests there", code, out["_raw"], r.w.hit(moved))
	}
	// Limited, the domain is still listed: the search goes through, says the server is limited, and reads none of
	// its posts (FMD 1); its outbox is not asked for.
	r.mustExec(`UPDATE domain_policies SET policy = 'limit' WHERE domain = 'social.example'`)
	if err := r.a.fed.loadPolicy(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := r.w.hit(bo + "/outbox")
	code, out, _ = r.doH(ana, "POST", "/api/fed/lookup", map[string]any{"q": "@bo@social.example"}, nil)
	if res, _ := out["result"].(map[string]any); code != 200 || res["limited"] != fedLimitedNotice || res["posts"] != nil || res["postsNote"] != fedLimitedPosts || r.w.hit(bo+"/outbox") != before {
		t.Errorf("a search for a limited server: %d %v", code, out["_raw"])
	}
}

// Every way out passes the domain policy (FMD 1: "checked in the fetcher, the inbox and the fan-out"), read from the
// code, path by path. An activity leaves only through the delivery queue: every producer named below calls one of its
// enqueue functions, they all end in enqueueOrdered, which collects the inboxes through collect (asking admits) before
// the one statement that writes ap_outbox, and the rows of ap_deliveries are written by fanOut alone, called after
// collect or, for an activity that waited for room, after asking admits again; each row asks admits once more at the
// send (TestDBDeliveryPolicyAndPause) and the fetcher a last time. Every other request (keys, actors, WebFinger,
// NodeInfo, pictures, the send itself) is a fetchReq that carries admits, which the fetcher asks on the first URL and
// every redirect (TestDBPolicyBothWays, TestDBPolicyRefusesFetch). A new path that skips any of these fails here.
func TestFanOutPassesAdmits(t *testing.T) {
	b, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	// funcOf is the name of the function the text at i is in (its "func ..." line).
	funcOf := func(i int) string {
		j := strings.LastIndex(src[:i], "\nfunc ")
		if j < 0 {
			return ""
		}
		line, _, _ := strings.Cut(src[j+1:], "\n")
		return line
	}
	body := func(name string) string {
		i := strings.Index(src, "\nfunc "+name)
		if i < 0 {
			t.Fatalf("no function %s in main.go", name)
		}
		j := strings.Index(src[i+1:], "\n}\n")
		return src[i : i+1+j]
	}
	// The producers, each with the enqueue it must reach (the paths the item names).
	enqueueRE := regexp.MustCompile(`\.(enqueue|enqueueThen|enqueueUndo|enqueueOrdered)\(`)
	for _, p := range []struct{ path, fn string }{
		{"a follow, its answers and its Undo", "(f *federation) sendTakingBack("},
		{"a post's Create and its Delete", "(a *app) postOut("},
		{"Update(Person)", "(a *app) personOut("},
		{"Delete(Person)", "(a *app) personGone("},
		{"a forward of a reply", "(f *federation) forward("},
		{"a like of a post of another server", "(a *app) likeRemote("},
		{"its Undo", "(a *app) unlikeRemote("},
		{"a comment's Create, Update and Delete", "(a *app) commentOut("},
	} {
		if !enqueueRE.MatchString(body(p.fn)) {
			t.Errorf("%s (%s) does not go through the delivery queue's enqueue", p.path, p.fn)
		}
	}
	for _, m := range enqueueRE.FindAllStringIndex(src, -1) {
		if f := funcOf(m[0]); !strings.Contains(f, "(q *deliveryQueue) enqueue") && !strings.Contains(src[m[0]-20:m[0]], "f.out") && !strings.Contains(src[m[0]-20:m[0]], "a.fed.out") {
			t.Errorf("an enqueue not on the federation's queue, in %s", f)
		}
	}
	for name, want := range map[string]string{
		"(q *deliveryQueue) enqueue(":     "q.enqueueThen(",
		"(q *deliveryQueue) enqueueThen(": "q.enqueueUndo(",
		"(q *deliveryQueue) enqueueUndo(": "q.enqueueOrdered(",
	} {
		if !strings.Contains(body(name), want) {
			t.Errorf("%s does not end in enqueueOrdered (%s)", name, want)
		}
	}
	ordered := body("(q *deliveryQueue) enqueueOrdered(")
	if c, w := strings.Index(ordered, "q.collect(to)"), strings.Index(ordered, "INSERT INTO ap_outbox"); c < 0 || w < c {
		t.Error("enqueueOrdered writes ap_outbox without collecting its inboxes through collect first")
	}
	if !strings.Contains(body("(q *deliveryQueue) collect("), "!q.f.admits(h)") {
		t.Error("collect does not ask the domain policy")
	}
	if n := strings.Count(src, "INSERT INTO ap_outbox"); n != 1 {
		t.Errorf("%d statements write ap_outbox; only enqueueOrdered may", n)
	}
	// The one statement that writes ap_deliveries (deliveryFanOutSQL) is run by fanOut alone.
	if n := strings.Count(src, "INSERT INTO ap_deliveries"); n != 1 || !strings.Contains(src, "const deliveryFanOutSQL = `WITH") {
		t.Errorf("ap_deliveries is written by %d statements, or not by deliveryFanOutSQL", n)
	}
	runs := regexp.MustCompile(`[(,] *deliveryFanOutSQL\b`).FindAllStringIndex(src, -1)
	if len(runs) == 0 {
		t.Error("deliveryFanOutSQL is run nowhere: the test no longer reads it")
	}
	for _, m := range runs {
		if !strings.Contains(funcOf(m[0]), "(q *deliveryQueue) fanOut(") {
			t.Errorf("deliveryFanOutSQL is run in %s, not in fanOut", funcOf(m[0]))
		}
	}
	calls := 0
	for _, m := range regexp.MustCompile(`q\.fanOut\(`).FindAllStringIndex(src, -1) {
		calls++
		start := strings.LastIndex(src[:m[0]], "\nfunc ")
		if before := src[start:m[0]]; !strings.Contains(before, "q.collect(") && !strings.Contains(before, "q.f.admits(h)") {
			t.Errorf("%s fans out without asking the domain policy first", funcOf(m[0]))
		}
	}
	if calls < 2 {
		t.Errorf("only %d calls of fanOut found: the test no longer reads them", calls)
	}
	if !strings.Contains(body("(q *deliveryQueue) deliver("), "case !f.admits(c.host):") {
		t.Error("the send does not ask the domain policy again")
	}
	// Every request to another server carries the policy, whatever asks for it; only a link preview, which fetches
	// what a member linked to and is no federation request, has its own list of hosts.
	seen := map[string]bool{}
	for _, m := range regexp.MustCompile(`fetchReq\{kind: (fetch\w+)`).FindAllStringSubmatchIndex(src, -1) {
		kind := src[m[2]:m[3]]
		lit := src[m[0]:]
		lit = lit[:strings.Index(lit, "})")]
		seen[kind] = true
		if kind != "fetchPreview" && !strings.Contains(lit, "admits: ") {
			t.Errorf("a %s request in %s does not carry the domain policy", kind, funcOf(m[0]))
		}
	}
	for _, k := range []string{"fetchDocument", "fetchWebFinger", "fetchDelivery", "fetchNodeInfo", "fetchPicture"} {
		if !seen[k] {
			t.Errorf("no %s request found: the test no longer reads them", k)
		}
	}
}

// An activity that waited for room (its fan-out held while the queue was full) is fanned out under the policy as it
// is then: a host suspended meanwhile gets no row, another does (fanOutDue).
func TestDBFanOutDueAsksPolicy(t *testing.T) {
	e := newDelivEnv(t)
	id, body := e.activityBody()
	e.mustExec(`INSERT INTO ap_outbox (ap_id, body, targets, created_at) VALUES ($1, $2, $3, $4)`, id, string(body),
		pq.Array([]string{"https://social.example/inbox", "https://a.example/inbox"}), e.clock.now())
	e.mustExec(`INSERT INTO domain_policies (domain, policy) VALUES ('social.example', 'suspend')`)
	if err := e.a.fed.loadPolicy(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.q.waiting.Store(true)
	if err := e.q.fanOutDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	var hosts []string
	e.a.db.QueryRow(`SELECT COALESCE(array_agg(d.host ORDER BY d.host), '{}') FROM ap_deliveries d JOIN ap_outbox o ON o.id = d.outbox_id WHERE o.ap_id = $1`, id).Scan(pq.Array(&hosts))
	if !slices.Equal(hosts, []string{"a.example"}) || e.count(`SELECT count(*) FROM ap_outbox WHERE ap_id = $1 AND fanned_out`, id) != 1 {
		t.Errorf("the fan-out of an activity that waited: %v", hosts)
	}
}
