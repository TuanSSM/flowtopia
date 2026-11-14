package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

// The outbox, its pages, the Creates and the collections (docs/federation.md 5.2, 6.2, 6.4, 6.7, F10).

// docOf asks for an address as another server would, wants 200 and the headers of 5.6, and decodes it.
func docOf(t *testing.T, e *tenv, path string) map[string]any {
	t.Helper()
	w := pubAsk(e, "GET", path, apAccept)
	h := w.Result().Header
	if w.Code != 200 || h.Get("Content-Type") != activityType || h.Get("Vary") != "Accept" || h.Get("Cache-Control") != "public, max-age=180" || h.Get("ETag") == "" {
		t.Fatalf("GET %s: %d %v %s", path, w.Code, h, w.Body)
	}
	strictShape(t, path, w.Body.Bytes())
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// walkOutbox reads a member's outbox the way a reader does: the collection, its first page, then next
// until there is none. It returns the count, the post ids in the order read, and the sizes of the pages.
func walkOutbox(t *testing.T, e *tenv, name string) (total int, ids []int64, sizes []int) {
	t.Helper()
	const origin = "https://flowa.example.org"
	box := origin + "/users/" + name + "/outbox"
	c := docOf(t, e, "/users/"+name+"/outbox")
	if c["id"] != box || c["type"] != "OrderedCollection" || c["first"] != box+"?page=true" || c["@context"] != asContext {
		t.Fatalf("the outbox of %s: %v", name, c)
	}
	total = int(c["totalItems"].(float64))
	seen := map[string]bool{}
	for next := c["first"].(string); next != ""; {
		if seen[next] || len(seen) > 100 {
			t.Fatalf("the pages of %s loop at %s", name, next)
		}
		seen[next] = true
		p := docOf(t, e, strings.TrimPrefix(next, origin))
		if p["id"] != next || p["type"] != "OrderedCollectionPage" || p["partOf"] != box {
			t.Fatalf("a page of %s: %v", name, p)
		}
		items := p["orderedItems"].([]any)
		sizes = append(sizes, len(items))
		for _, it := range items {
			cr := it.(map[string]any)
			n := cr["object"].(map[string]any)
			var id int64
			fmt.Sscanf(strings.TrimPrefix(n["id"].(string), origin+"/users/"+name+"/posts/"), "%d", &id)
			if cr["type"] != "Create" || cr["id"] != n["id"].(string)+"/activity" || cr["actor"] != origin+"/users/"+name || cr["@context"] != nil || n["@context"] != nil {
				t.Errorf("an item of %s: %v", name, cr)
			}
			ids = append(ids, id)
		}
		next, _ = p["next"].(string)
	}
	return total, ids, sizes
}

// The outbox pages a member's Public posts newest first, 20 a page, by the profile page's cursor: empty,
// one page, many pages; a This server post, a hidden one and a milestone are never in it and never counted,
// a deleted post leaves it and its count; the cursor reads only the member's own Public posts, whatever
// post it names; a query that names no page is the 404.
func TestDBOutboxPaging(t *testing.T) {
	f := newPubFixture(t)
	e := f.e
	// One page: ana's one Public post; her This server post, hidden post and milestone are absent.
	total, ids, sizes := walkOutbox(t, e, "ana")
	if total != 1 || !slices.Equal(ids, []int64{f.pub}) || !slices.Equal(sizes, []int{1}) {
		t.Fatalf("one page: %d %v %v", total, ids, sizes)
	}
	// Empty: a member who opted in and has posted nothing Public (one This server post).
	fay, _ := e.user("fay", "user")
	e.mustExec(`UPDATE users SET federates = true WHERE id = $1`, fay)
	e.mustExec(`INSERT INTO posts (user_id, body, audience) VALUES ($1, 'only here', 'local')`, fay)
	if total, ids, sizes := walkOutbox(t, e, "fay"); total != 0 || len(ids) != 0 || !slices.Equal(sizes, []int{0}) {
		t.Errorf("empty: %d %v %v", total, ids, sizes)
	}
	if b := pubAsk(e, "GET", "/users/fay/outbox?page=true", apAccept).Body.String(); !strings.Contains(b, `"orderedItems":[]`) || strings.Contains(b, `"next"`) {
		t.Errorf("an empty page: %s", b)
	}
	// Many pages: 45 more Public posts between This server ones; 46 in all, read 20, 20 and 6.
	want := []int64{}
	for i := range 90 {
		var id int64
		aud := []string{"public", "local"}[i%2]
		if err := e.a.db.QueryRow(`INSERT INTO posts (user_id, body, audience) VALUES ($1, $2, $3) RETURNING id`, f.ana, fmt.Sprint("post ", i), aud).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if aud == "public" {
			want = append([]int64{id}, want...)
		}
	}
	want = append(want, f.pub)
	total, ids, sizes = walkOutbox(t, e, "ana")
	if total != 46 || !slices.Equal(ids, want) || !slices.Equal(sizes, []int{20, 20, 6}) {
		t.Fatalf("many pages: %d %v %v, want 46 %v", total, ids, sizes, want)
	}
	// A deleted post leaves the outbox and its count; so does a post hidden by a moderator.
	e.mustExec(`DELETE FROM posts WHERE id = $1`, want[3])
	e.mustExec(`UPDATE posts SET hidden_at = now() WHERE id = $1`, want[30])
	total, ids, sizes = walkOutbox(t, e, "ana")
	gone := slices.DeleteFunc(slices.Clone(want), func(id int64) bool { return id == want[3] || id == want[30] })
	if total != 44 || !slices.Equal(ids, gone) || !slices.Equal(sizes, []int{20, 20, 4}) {
		t.Errorf("after a delete and a hide: %d %v %v", total, ids, sizes)
	}
	for _, id := range []int64{f.local, f.hidden, f.milestone, want[3], want[30]} {
		if slices.Contains(ids, id) {
			t.Errorf("post %d is in the outbox", id)
		}
	}
	// The cursor bounds ana's own Public posts and nothing else: naming bob's post, a This server post or
	// a post that does not exist reads ana's Public posts older than that id. A post id is a BIGSERIAL, so a
	// cursor beyond 32 bits is an id like any other, on the profile page too.
	for _, cur := range []int64{f.bobPub, f.local, f.deePub, 1 << 40, 1<<63 - 1} {
		if w := pubAsk(e, "GET", fmt.Sprintf("/users/ana?before=%d", cur), browser); w.Code != 200 {
			t.Errorf("the profile page before %d: %d", cur, w.Code)
		}
		p := docOf(t, e, fmt.Sprintf("/users/ana/outbox?page=true&before=%d", cur))
		for _, it := range p["orderedItems"].([]any) {
			n := it.(map[string]any)["object"].(map[string]any)
			var id int64
			fmt.Sscanf(n["id"].(string), "https://flowa.example.org/users/ana/posts/%d", &id)
			if n["attributedTo"] != "https://flowa.example.org/users/ana" || id >= cur || !slices.Contains(gone, id) {
				t.Errorf("before=%d gave %v", cur, n["id"])
			}
		}
	}
	// Every query that names no page is the one 404, before the database is asked.
	nobody := pubAsk(e, "GET", "/users/nobody/outbox", apAccept)
	for _, q := range []string{"?page=false", "?page=1", "?page=true&page=true", "?before=5", "?page=true&before=x", "?page=true&before=0",
		"?page=true&before=-3", "?page=true&before=1&before=2", "?page=true&max_id=5", "?min_id=1", "?page=true&limit=500", "?x"} {
		w := pubAsk(e, "GET", "/users/ana/outbox"+q, apAccept)
		if w.Code != 404 || w.Body.String() != nobody.Body.String() || !headersEqual(w.Result().Header, nobody.Result().Header) {
			t.Errorf("GET /users/ana/outbox%s: %d %s", q, w.Code, w.Body)
		}
	}
	// A browser is sent to the profile page, the cursor kept.
	for path, to := range map[string]string{
		"/users/ana/outbox": "/users/ana", "/users/ana/outbox?page=true": "/users/ana", "/users/ana/outbox?page=true&before=7": "/users/ana?before=7",
		"/users/nobody/outbox": "/users/nobody",
	} {
		w := pubAsk(e, "GET", path, browser)
		if h := w.Result().Header; w.Code != 303 || h.Get("Location") != to || h.Get("Vary") != "Accept" || h.Get("Cache-Control") != "public, max-age=180" {
			t.Errorf("a browser at %s: %d %v", path, w.Code, h)
		}
	}
}

// A Create is the same wherever it is read: at its own id, and in the outbox; its Note is the Note at the
// post's address; to and cc are the Note's (6.4, 6.7). A browser at its id is sent to the post.
func TestDBCreateActivity(t *testing.T) {
	f := newPubFixture(t)
	e := f.e
	post := fmt.Sprintf("/users/ana/posts/%d", f.pub)
	c := docOf(t, e, post+"/activity")
	note := docOf(t, e, post)
	if c["@context"] == nil || !jsonEqual(c["@context"], note["@context"]) {
		t.Errorf("the Create's context: %v", c["@context"])
	}
	delete(note, "@context")
	id := "https://flowa.example.org" + post
	if c["id"] != id+"/activity" || c["type"] != "Create" || c["actor"] != "https://flowa.example.org/users/ana" || c["published"] != note["published"] ||
		!jsonEqual(c["to"], []string{publicCollection}) || !jsonEqual(c["cc"], []string{"https://flowa.example.org/users/ana/followers"}) ||
		!jsonEqual(c["to"], note["to"]) || !jsonEqual(c["cc"], note["cc"]) || !jsonEqual(c["object"], note) {
		t.Errorf("the Create: %v\nthe Note: %v", c, note)
	}
	page := docOf(t, e, "/users/ana/outbox?page=true")
	delete(c, "@context")
	if items := page["orderedItems"].([]any); len(items) != 1 || !jsonEqual(items[0], c) {
		t.Errorf("the Create in the outbox differs from the one at its id:\n%v\n%v", items, c)
	}
	w := pubAsk(e, "GET", post+"/activity", browser)
	if w.Code != 303 || w.Result().Header.Get("Location") != post {
		t.Errorf("a browser at the Create: %d %v", w.Code, w.Result().Header)
	}
	// What is not shown has no Create either.
	for _, p := range []int64{f.local, f.hidden, f.milestone, f.bobPub, f.cydPub, f.deePub} {
		for _, name := range []string{"ana", "bob", "cyd", "dee"} {
			if w := pubAsk(e, "GET", fmt.Sprintf("/users/%s/posts/%d/activity", name, p), apAccept); w.Code != 404 {
				t.Errorf("the Create of post %d under %s: %d", p, name, w.Code)
			}
		}
	}
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	var u, v any
	json.Unmarshal(x, &u)
	json.Unmarshal(y, &v)
	x, _ = json.Marshal(u)
	y, _ = json.Marshal(v)
	return string(x) == string(y)
}

// Followers and following answer their counts and never who (F10): no first, no page, no item, no name, for
// anyone who asks here; the count is the app's (a follow across a block is not counted), and while the
// follows flag is off, as the app shows none, there is no count either. Featured and the instance actor's
// outbox are empty. A member's collection freezes the origin; the instance actor's outbox does not.
func TestDBCollections(t *testing.T) {
	f := newPubFixture(t)
	e := f.e
	if e.count(`SELECT count(*) FROM instance_settings WHERE key = 'origin'`) != 0 {
		t.Fatal("the origin is recorded before anything was asked")
	}
	inst := docOf(t, e, "/actor/outbox")
	if inst["id"] != "https://flowa.example.org/actor/outbox" || inst["type"] != "OrderedCollection" || inst["totalItems"] != 0.0 || !jsonEqual(inst["orderedItems"], []any{}) || inst["first"] != nil {
		t.Errorf("the instance actor's outbox: %v", inst)
	}
	if e.count(`SELECT count(*) FROM instance_settings WHERE key = 'origin'`) != 0 {
		t.Error("the instance actor's outbox froze the origin")
	}
	fol := docOf(t, e, "/users/ana/followers")
	if e.count(`SELECT count(*) FROM instance_settings WHERE key = 'origin'`) != 1 {
		t.Error("a member's followers collection went out without freezing the origin")
	}
	only := func(m map[string]any, keys ...string) bool {
		for k := range m {
			if !slices.Contains(keys, k) {
				return false
			}
		}
		return len(m) == len(keys)
	}
	// The flag off: the collection, nothing counted.
	fing := docOf(t, e, "/users/ana/following")
	if fol["id"] != "https://flowa.example.org/users/ana/followers" || !only(fol, "@context", "id", "type") || fol["type"] != "OrderedCollection" ||
		fing["id"] != "https://flowa.example.org/users/ana/following" || !only(fing, "@context", "id", "type") {
		t.Errorf("with the follows flag off: %v %v", fol, fing)
	}
	// The flag on: bob and dee follow ana, cyd follows her across a block, ana follows bob.
	e.setFlag("follows", true)
	e.mustExec(`INSERT INTO follows (follower_id, followee_id) VALUES ($1, $4), ($2, $4), ($3, $4), ($4, $1)`, f.bob, f.cyd, f.dee, f.ana)
	e.mustExec(`INSERT INTO blocks (user_id, target_id) VALUES ($1, $2)`, f.ana, f.cyd)
	for path, n := range map[string]float64{"/users/ana/followers": 2, "/users/ana/following": 1} {
		w := pubAsk(e, "GET", path, apAccept)
		d := docOf(t, e, path)
		if !only(d, "@context", "id", "type", "totalItems") || d["totalItems"] != n {
			t.Errorf("%s: %v", path, d)
		}
		for _, who := range []string{"bob", "cyd", "dee", "orderedItems", "first", `"items"`} {
			if strings.Contains(w.Body.String(), who) {
				t.Errorf("%s names %s: %s", path, who, w.Body)
			}
		}
		// Nothing a request carries opens the list: not a member's session, not a page parameter.
		r := httptest.NewRequest("GET", path+"?page=true", nil)
		r.Header.Set("Accept", apAccept)
		r.RemoteAddr = "198.51.100.20:4000"
		r.AddCookie(&http.Cookie{Name: cookieName, Value: f.anaTok})
		w2 := httptest.NewRecorder()
		e.h.ServeHTTP(w2, r)
		if w2.Code != 200 || w2.Body.String() != w.Body.String() {
			t.Errorf("%s signed in as ana, with ?page=true: %d %s", path, w2.Code, w2.Body)
		}
	}
	feat := docOf(t, e, "/users/ana/featured")
	if !only(feat, "@context", "id", "type", "totalItems", "orderedItems") || feat["id"] != "https://flowa.example.org/users/ana/featured" || feat["totalItems"] != 0.0 || !jsonEqual(feat["orderedItems"], []any{}) {
		t.Errorf("featured: %v", feat)
	}
	// The actor names each, and each answers.
	actor := docOf(t, e, "/users/ana")
	for _, k := range []string{"outbox", "followers", "following", "featured"} {
		if actor[k] != "https://flowa.example.org/users/ana/"+k {
			t.Errorf("the actor's %s: %v", k, actor[k])
		}
		docOf(t, e, strings.TrimPrefix(actor[k].(string), "https://flowa.example.org"))
	}
	if _, ok := actor["endpoints"]; ok {
		t.Error("the actor names a shared inbox, which does not answer")
	}
	inst = docOf(t, e, "/actor")
	if _, ok := inst["followers"]; ok || inst["outbox"] != "https://flowa.example.org/actor/outbox" {
		t.Errorf("the instance actor: %v", inst)
	}
}

// Every new address follows the rules of the public surfaces (TestDBPublicSurfaces): one 404 per
// representation, the same bytes and headers, for everyone not shown and every spelling that is not an id;
// a browser is sent on for any name written as one, since that reads nothing; HEAD, 405 with Allow, no
// CORS header; the allowance counted first; everything the 404 while federation is refused.
func TestDBCollectionSurfaces(t *testing.T) {
	f := newPubFixture(t)
	e := f.e
	e.setFlag("follows", true)
	of := func(w *httptest.ResponseRecorder) string {
		return fmt.Sprint(w.Code, " ", w.Body.String(), " ", w.Result().Header)
	}
	subs := []string{"outbox", "outbox?page=true", "outbox?page=true&before=99999", "followers", "following", "featured", fmt.Sprintf("posts/%d/activity", f.pub)}
	var shown, hidden, spelled []string
	for _, s := range subs {
		shown = append(shown, "/users/ana/"+s)
		for _, name := range []string{"bob", "cyd", "dee", "eve", "nobody"} {
			hidden = append(hidden, "/users/"+name+"/"+s)
		}
		spelled = append(spelled, "/users/Ana/"+s, "/users/%61na/"+s, "/users/a/"+s)
	}
	for _, p := range []int64{f.local, f.hidden, f.milestone, f.bobPub, 999999} {
		hidden = append(hidden, fmt.Sprintf("/users/ana/posts/%d/activity", p))
	}
	spelled = append(spelled, fmt.Sprintf("/users/ana/posts/0%d/activity", f.pub), "/users/ana/posts/x/activity", "/actor/%6futbox")
	for _, accept := range []string{apAccept, ldAccept, browser, ""} {
		want := of(pubAsk(e, "GET", "/users/nobody", accept))
		activity := wantsActivity(http.Header{"Accept": {accept}})
		for _, p := range spelled {
			if got := of(pubAsk(e, "GET", p, accept)); got != want {
				t.Errorf("Accept %q: GET %s: %s, a name nobody has: %s", accept, p, got, want)
			}
		}
		for _, p := range hidden {
			w := pubAsk(e, "GET", p, accept)
			if activity && of(w) != want || !activity && w.Code != 303 {
				t.Errorf("Accept %q: GET %s: %s", accept, p, of(w))
			}
		}
		for _, p := range append([]string{"/actor/outbox"}, shown...) {
			w := pubAsk(e, "GET", p, accept)
			if activity && w.Code != 200 || !activity && w.Code != 303 {
				t.Errorf("Accept %q: GET %s: %d", accept, p, w.Code)
			}
		}
	}
	for _, p := range append([]string{"/actor/outbox", "/users/nobody/outbox"}, shown...) {
		get, head := pubAsk(e, "GET", p, apAccept), pubAsk(e, "HEAD", p, apAccept)
		if head.Code != get.Code || head.Body.Len() != 0 || head.Result().Header.Get("Content-Type") != get.Result().Header.Get("Content-Type") ||
			head.Result().Header.Get("ETag") != get.Result().Header.Get("ETag") {
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
	// The allowance is counted before anything is read, and the 429 is all an address over it gets.
	e.a.lim.public = newLimiter(1, time.Hour)
	for _, p := range append([]string{"/actor/outbox"}, shown...) {
		from := "203.0.113.9:1"
		ask(e.h, "GET", "/users/ana", from, nil)
		if w := ask(e.h, "GET", p, from, map[string]string{"Accept": apAccept}); w.Code != 429 || w.Result().Header.Get("Vary") != "Accept" {
			t.Errorf("GET %s over the allowance: %d", p, w.Code)
		}
		e.a.lim.public = newLimiter(1, time.Hour)
	}
	e.a.lim.public = newLimiter(1<<30, time.Hour)
	// Refused federation: every address is the 404, in either representation.
	e.a.fed.refuse("federation refused: a test")
	for _, accept := range []string{apAccept, browser} {
		want := of(pubAsk(e, "GET", "/users/nobody", accept))
		for _, p := range append([]string{"/actor/outbox"}, shown...) {
			if got := of(pubAsk(e, "GET", p, accept)); got != want {
				t.Errorf("refused, Accept %q: GET %s: %s", accept, p, got)
			}
		}
	}
}

// BenchmarkOutboxPage is a full page of the outbox (20 Creates) through the whole server: one statement to
// a local Postgres, the Creates made.
func BenchmarkOutboxPage(b *testing.B) {
	db := testDB(b)
	for _, q := range []string{
		`INSERT INTO users (username, display_name, pw_hash, props, federates) VALUES ('ana', 'Ana', 'x', '["poi"]', true)`,
		`INSERT INTO posts (user_id, body, audience, data) SELECT 1, 'A clean figure-8, finally. https://example.com/x ' || g, CASE WHEN g % 3 = 0 THEN 'local' ELSE 'public' END,
			'{"tags":[{"prop":"poi","skill":"F1"}]}' FROM generate_series(1, 200) g`,
		`ANALYZE`,
	} {
		if _, err := db.Exec(q); err != nil {
			b.Fatal(err)
		}
	}
	a, _ := discoveryApp(b, "https://flowa.example.org")
	a.db, a.fed.db = db, db
	a.lim.public = newLimiter(1<<30, time.Hour)
	h := a.routes(staticOf(b, nil))
	const target = "/users/ana/outbox?page=true"
	w := ask(h, "GET", target, "", map[string]string{"Accept": apAccept})
	if w.Code != 200 || strings.Count(w.Body.String(), `"type":"Create"`) != pubPage {
		b.Fatalf("%d %s", w.Code, w.Body)
	}
	r := httptest.NewRequest("GET", target, nil)
	r.Header.Set("Accept", apAccept)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		h.ServeHTTP(httptest.NewRecorder(), r)
	}
}
