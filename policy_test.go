package main

// Domain policies and the allowlist (docs/federation.md, F8, section 10; FMD 1, as far as the inbox needs it).

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
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
		{"/api/admin/federation/domains/x.example", map[string]any{"policy": "limit"}, 400},
		{"/api/admin/federation/domains/x.example", map[string]any{"policy": "allow", "note": strings.Repeat("n", domainNoteMax+1)}, 400},
		{"/api/admin/federation/domains/x.example", map[string]any{"policy": "allow", "note": "a\u202eb"}, 400},
		{"/api/admin/federation/policy", map[string]any{"policy": "closed"}, 400},
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
}
