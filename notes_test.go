package main

// Public posts go out as notes (docs/federation.md 6.4, 6.5, 6.7 and "As built (FWR 4)" under 6.5; FWR item 4): the
// Note and its Create against golden files and against each other, the HTML of 6.5 under hostile text, the fan-out
// through the delivery queue (one delivery per shared inbox, the policy, the switches, the audiences, milestones),
// Delete on deletion and on a moderator's hide to the servers the Create went to, Update(Person) coalesced on the
// test's clock, what never leaves, a second Flowa that receives the Create and the Delete, and the pool of one
// connection. The workers are stepped by hand (sendOut, deliveryQueue.step) and the clocks are the test's: nothing
// waits on the wall clock or polls.

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lib/pq"
)

/* ---------- the HTML of a Note ---------- */

// noteTagRE is every tag noteHTMLWith may write, whole: 6.5's p, br, a and span with the attributes and classes
// Mastodon's sanitiser keeps, an href always http or https and quoted, with nothing in it that ends the quote.
var noteTagRE = regexp.MustCompile(`^<(?:p|/p|br|/a|span|/span|span class="h-card"|a href="https?://[^"<>\s]*" (?:rel="nofollow noopener"|class="u-url mention"|class="mention hashtag" rel="tag"))>`)

// noteEntityRE is every entity html.EscapeString writes.
var noteEntityRE = regexp.MustCompile(`^&(?:amp|lt|gt|#34|#39);`)

// allowedNoteHTML says why s is not HTML of the allowed subset: a tag that is not one of noteTagRE's, a tag left
// open or closed twice, a < or > or quote outside a tag (a character of the text not escaped), an & that starts no
// entity, or a control or direction-turning character.
func allowedNoteHTML(s string) error {
	var open []string
	for i := 0; i < len(s); {
		switch c := s[i]; {
		case c == '<':
			m := noteTagRE.FindString(s[i:])
			if m == "" {
				return fmt.Errorf("a tag that is not allowed at %d: %.40q", i, s[i:])
			}
			name := strings.TrimPrefix(strings.Fields(strings.Trim(m, "<>"))[0], "/")
			switch {
			case name == "br":
			case strings.HasPrefix(m, "</"):
				if len(open) == 0 || open[len(open)-1] != name {
					return fmt.Errorf("</%s> closes nothing open at %d", name, i)
				}
				open = open[:len(open)-1]
			default:
				if name == "p" && len(open) > 0 || name != "p" && len(open) == 0 {
					return fmt.Errorf("<%s> where it cannot be at %d", name, i)
				}
				open = append(open, name)
			}
			i += len(m)
		case c == '&':
			m := noteEntityRE.FindString(s[i:])
			if m == "" {
				return fmt.Errorf("an & that starts no entity at %d", i)
			}
			i += len(m)
		case c == '>' || c == '"' || c == '\'':
			return fmt.Errorf("a %q of the text not escaped at %d", c, i)
		default:
			if len(open) == 0 {
				return fmt.Errorf("text outside a paragraph at %d", i)
			}
			r := []rune(s[i:])[0]
			if r < 0x20 || r == 0x7f || bidiOverride(r) {
				return fmt.Errorf("the character %U reaches the HTML at %d", r, i)
			}
			i += len(string(r))
		}
	}
	if len(open) != 0 {
		return fmt.Errorf("left open: %v", open)
	}
	return nil
}

// hostileMentions are what the escaping tests resolve: a remote account, a local member, and one whose stored
// addresses and name are themselves hostile (the cache holds only what the decoder took, but the HTML must not
// trust even that).
var hostileMentions = map[string]noteMention{
	"bo@social.example": {Key: "bo@social.example", ID: "https://social.example/users/bo", URL: "https://social.example/@bo", Name: "@bo@social.example"},
	"cyd":               {Key: "cyd", ID: "https://here.example/users/cyd", URL: "https://here.example/users/cyd", Name: "@cyd@here.example"},
	"ev@evil.example":   {Key: "ev@evil.example", ID: `https://evil.example/u/"><script>`, URL: `https://evil.example/@ev" onclick="x`, Name: `@<b>ev</b>@evil.example`},
	"mal@evil.example":  {Key: "mal@evil.example", ID: "https://evil.example/u/mal", URL: "javascript:alert(1)", Name: `@<b>"mal'</b>@evil.example`},
}

// Hostile text through the HTML of 6.5: every character of the member's text escaped, links only for http and
// https, mentions and tag-like words inside a link left in the link, control and direction characters dropped,
// and an href nobody can break out of. Each output is held to the allowed subset (allowedNoteHTML), and the cases
// that matter are held whole.
func TestNoteContentEscaping(t *testing.T) {
	tags := []outTag{{Type: "Hashtag", Href: "https://here.example/#/prop/poi", Name: "#poi"}}
	for _, c := range []struct{ body, want string }{
		{`<script>alert("x")</script>`, `<p>&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt;</p>`},
		{`a "quote" & 'apostrophe' <b onmouseover=x>`, `<p>a &#34;quote&#34; &amp; &#39;apostrophe&#39; &lt;b onmouseover=x&gt;</p>`},
		{`javascript:alert(1) data:text/html,<script>x</script> vbscript:x`, `<p>javascript:alert(1) data:text/html,&lt;script&gt;x&lt;/script&gt; vbscript:x</p>`},
		{`<a href="javascript:alert(1)">x</a>`, `<p>&lt;a href=&#34;javascript:alert(1)&#34;&gt;x&lt;/a&gt;</p>`},
		{`see https://example.com/"onmouseover="x`, `<p>see <a href="https://example.com/" rel="nofollow noopener">https://example.com/</a>&#34;onmouseover=&#34;x</p>`},
		{"https://example.com/a?b=1&c=<2>", `<p><a href="https://example.com/a?b=1&amp;c=" rel="nofollow noopener">https://example.com/a?b=1&amp;c=</a>&lt;2&gt;</p>`},
		{"thanks @bo@social.example!", `<p>thanks <span class="h-card"><a href="https://social.example/@bo" class="u-url mention">@<span>bo</span></a></span>!</p>`},
		{"@cyd and @cyd@here.example", `<p><span class="h-card"><a href="https://here.example/users/cyd" class="u-url mention">@<span>cyd</span></a></span> and <span class="h-card"><a href="https://here.example/users/cyd" class="u-url mention">@<span>cyd</span></a></span></p>`},
		// A mention whose stored addresses could end an attribute is not made a link at all.
		{"@ev@evil.example", `<p>@ev@evil.example</p>`},
		// One whose page is not https is linked by its id, and its stored name is escaped.
		{"@mal@evil.example", `<p><span class="h-card"><a href="https://evil.example/u/mal" class="u-url mention">@<span>&lt;b&gt;&#34;mal&#39;&lt;/b&gt;</span></a></span></p>`},
		// Not resolved: an unknown handle, a local name nobody resolved, and the same names inside a link.
		{"@zed@social.example @nobody", `<p>@zed@social.example @nobody</p>`},
		{"https://social.example/@bo@social.example/#poi", `<p><a href="https://social.example/@bo@social.example/#poi" rel="nofollow noopener">https://social.example/@bo@social.example/#poi</a></p>`},
		{"x https://h.example/?q=@cyd y", `<p>x <a href="https://h.example/?q=@cyd" rel="nofollow noopener">https://h.example/?q=@cyd</a> y</p>`},
		{"#poi is a word, not a tag", `<p>#poi is a word, not a tag</p>`},
		{"\u202eevil\u202c \u2067x\u2069 \x00\x07\x1b[31m red\ttab", "<p>evil x [31m red tab</p>"},
		{"a\r\nb\n\n\n\nc", `<p>a<br>b</p><p>c</p>`},
		{strings.Repeat("W", 3000), "<p>" + strings.Repeat("W", 3000) + "</p>"},
	} {
		got := noteHTMLWith(c.body, nil, hostileMentions, "here.example", nil)
		if got != c.want {
			t.Errorf("noteHTMLWith(%q)\n got %s\nwant %s", c.body, got, c.want)
		}
		if err := allowedNoteHTML(got); err != nil {
			t.Errorf("noteHTMLWith(%q): %v\n%s", c.body, err, got)
		}
	}
	// The chips' and the combo's lines carry the hashtags and never a mention link, whatever a combo's name holds.
	got := noteHTMLWith("x", []string{"Tricks: A (Poi)", `Combo: <i>@cyd</i> "q": A`}, hostileMentions, "here.example", tags)
	want := `<p>x</p><p>Tricks: A (Poi)</p><p>Combo: &lt;i&gt;@cyd&lt;/i&gt; &#34;q&#34;: A <a href="https://here.example/#/prop/poi" class="mention hashtag" rel="tag">#<span>poi</span></a></p>`
	if got != want {
		t.Errorf("the lines after the text:\n got %s\nwant %s", got, want)
	}
	if err := allowedNoteHTML(got); err != nil {
		t.Error(err)
	}
	// The checker itself refuses what a missing escape would let through.
	for _, bad := range []string{`<p><script></p>`, `<p>a "b"</p>`, `<p><a href="javascript:x" rel="nofollow noopener">x</a></p>`, `<p>&x</p>`, "<p>\u202e</p>", `<p><span>`, `x`} {
		if allowedNoteHTML(bad) == nil {
			t.Errorf("the checker takes %q", bad)
		}
	}
}

// Any text through the HTML of 6.5 is the allowed subset, and no < of the input reaches it unescaped (every < in
// the output starts an allowed tag, and the text between the tags, unescaped, is the input with only what noteText
// drops taken out).
func FuzzNoteContent(f *testing.F) {
	for _, s := range []string{"", "hi", "<script>", `"'&<>`, "@bo@social.example @cyd https://x.example/@cyd", "\u202e\x00", "a\n\nb\nc", "@ev@evil.example", "https://a.example/<b>"} {
		f.Add(s)
	}
	tagRE := regexp.MustCompile(`<[^>]*>`)
	f.Fuzz(func(t *testing.T, s string) {
		if !utf8Valid(s) {
			return
		}
		got := noteHTMLWith(s, []string{"Tricks: " + s}, hostileMentions, "here.example", []outTag{{Type: "Hashtag", Href: "https://here.example/#/prop/poi", Name: "#poi"}})
		if err := allowedNoteHTML(got); err != nil {
			t.Fatalf("%q: %v\n%s", s, err, got)
		}
		// Unescaped, the text left between the tags holds no < or > that the input did not.
		text := html.UnescapeString(tagRE.ReplaceAllString(got, ""))
		if strings.Count(text, "<") > strings.Count(s, "<")*2 || strings.Count(text, ">") > strings.Count(s, ">")*2 {
			t.Fatalf("%q: more angle brackets in the text than the input had: %q", s, text)
		}
	})
}

func utf8Valid(s string) bool { return strings.ToValidUTF8(s, "�") == s }

/* ---------- the environment ---------- */

// notesEnv is followEnv with the domain policy open and ana, a member who shares her profile, as the author.
type notesEnv struct {
	*followEnv
	anaID int64
	ana   string
}

func newNotesEnv(t *testing.T) *notesEnv {
	t.Helper()
	e := newFollowEnv(t)
	e.mustExec(`UPDATE instance_settings SET value = '"open"' WHERE key = 'federation_policy'`)
	if err := e.a.fed.loadPolicy(context.Background()); err != nil {
		t.Fatal(err)
	}
	id, tok := e.member("ana", false)
	return &notesEnv{followEnv: e, anaID: id, ana: tok}
}

// remote makes an account of another server with its cached actor: its inbox under its id, its server's shared
// inbox when shared, its profile page at /@name. It returns its id here and its actor id.
func (e *notesEnv) remote(name, host string, shared bool) (int64, string) {
	e.t.Helper()
	ap := "https://" + host + "/users/" + name
	var id int64
	if err := e.a.db.QueryRow(`INSERT INTO users (username, pw_hash, domain, display_name) VALUES ($1, '!', $2, $3) RETURNING id`, name+"@"+host, host, name).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	si := ""
	if shared {
		si = "https://" + host + "/inbox"
	}
	e.mustExec(`INSERT INTO ap_actors (user_id, ap_id, kind, preferred_username, inbox, shared_inbox, url, key_id, public_key, fetched_at, expires_at)
		VALUES ($1, $2, 'Person', $3, $4, $5, $6, $7, 'pem', now(), now() + interval '1 day')`, id, ap, name, ap+"/inbox", si, "https://"+host+"/@"+name, ap+"#main-key")
	return id, ap
}

// follows makes the remote account follow ana, accepted.
func (e *notesEnv) follows(remote int64) {
	e.t.Helper()
	e.mustExec(`INSERT INTO follows (follower_id, followee_id, state) VALUES ($1, $2, 'accepted')`, remote, e.anaID)
}

// postAs makes a post through the API and returns its id.
func (e *notesEnv) postAs(tok string, body map[string]any) int64 {
	e.t.Helper()
	out := e.must(200, tok, "POST", "/api/posts", body)
	return num(out["posts"].([]any)[0].(map[string]any)["id"])
}

// produce runs the producer until nothing waits: what a worker's steps do before they claim.
func (e *notesEnv) produce() {
	e.t.Helper()
	for i := 0; ; i++ {
		did, err := e.a.sendOut(context.Background())
		if err != nil {
			e.t.Fatal(err)
		}
		if !did {
			return
		}
		if i > 100 {
			e.t.Fatal("the producer does not settle")
		}
	}
}

// outRows are the activities enqueued with their type and the inboxes their rows were written for, oldest first.
type outRow struct {
	id      string
	kind    string
	body    string
	inboxes []string
}

func (e *notesEnv) outRows() []outRow {
	e.t.Helper()
	rows, err := e.a.db.Query(`SELECT o.ap_id, o.body, COALESCE(array_agg(d.inbox ORDER BY d.inbox) FILTER (WHERE d.inbox IS NOT NULL), '{}') FROM ap_outbox o
		LEFT JOIN ap_deliveries d ON d.outbox_id = o.id GROUP BY o.id ORDER BY o.id`)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []outRow
	for rows.Next() {
		var r outRow
		if err := rows.Scan(&r.id, &r.body, pq.Array(&r.inboxes)); err != nil {
			e.t.Fatal(err)
		}
		var d struct{ Type string }
		json.Unmarshal([]byte(r.body), &d)
		r.kind = d.Type
		out = append(out, r)
	}
	return out
}

/* ---------- the Note and its Create ---------- */

// The Note of 6.5, every field: the text with a remote and a local mention as h-card links and a link, the chips and
// the combo as text with a hashtag per prop, the picture with its alt text as both name and summary, cc the
// followers and the actors mentioned, the Mention and Hashtag tags, flowa:tricks and flowa:combo. The Note its address
// serves and the Note inside the Create that is enqueued are the same bytes (one function, noteOf), held to one golden
// file each.
func TestNoteFromPost(t *testing.T) {
	e := newNotesEnv(t)
	bo, boAP := e.remote("bo", "social.example", true)
	e.follows(bo)
	_, cyd := e.user("cyd", "user") // a member here who shares her profile: a local mention
	e.must(200, cyd, "PUT", "/api/me", map[string]any{"federates": true})
	pic := jpegData(t, 32, 24)
	id := e.postAs(e.ana, map[string]any{"body": "Finally clean! Thanks @bo@social.example and @cyd for the drill: https://example.com/drill\n\nNext: @zed@unknown.example",
		"image": pic, "alt": "Ana spinning a nunchaku in a figure-8 in the park", "audience": "public",
		"tags":  []map[string]string{{"prop": "nunchaku", "skill": "F7"}, {"prop": "poi", "skill": "L2"}},
		"combo": map[string]any{"id": "c1", "name": "Morning", "role": "signature", "prop": "nunchaku", "seq": []string{"F7", "F5"}, "reps": []int{2, 1}}})
	if id != 1 {
		t.Fatalf("the post's id is %d: the golden files name post 1", id)
	}
	e.mustExec(`UPDATE posts SET created_at = '2026-11-12T18:04:05Z' WHERE id = $1`, id)
	e.produce()
	out := e.outRows()
	if len(out) != 1 || out[0].kind != "Create" || out[0].id != "https://here.example/users/ana/posts/1/activity" || !slices.Equal(out[0].inboxes, []string{"https://social.example/inbox"}) {
		t.Fatalf("enqueued: %+v", out)
	}
	golden(t, "create-ana.json", []byte(out[0].body))
	w := ask(e.h, "GET", "/users/ana/posts/1", "", map[string]string{"Accept": apAccept})
	if w.Code != 200 {
		t.Fatalf("the Note at its address: %d %s", w.Code, w.Body)
	}
	golden(t, "note-full.json", w.Body.Bytes())
	strictShape(t, "the Note", w.Body.Bytes())
	strictShape(t, "the Create", []byte(out[0].body))
	// The Create at its own address is the bytes enqueued.
	if c := ask(e.h, "GET", "/users/ana/posts/1/activity", "", map[string]string{"Accept": apAccept}); c.Body.String() != out[0].body {
		t.Errorf("the Create at its address differs from the one sent:\n%s\n%s", c.Body, out[0].body)
	}
	// The Note sent (the Create's object) is the Note served, without its @context, byte for byte.
	var sent struct {
		Object json.RawMessage `json:"object"`
	}
	if err := json.Unmarshal([]byte(out[0].body), &sent); err != nil {
		t.Fatal(err)
	}
	ctx, _ := apJSON(noteContext)
	served := "{" + strings.TrimPrefix(w.Body.String(), `{"@context":`+string(ctx)+`,`)
	if served != string(sent.Object) {
		t.Errorf("the Note sent differs from the Note served:\nsent   %s\nserved %s", sent.Object, served)
	}
	// What a reader needs, read back through Flowa's own decoder (as another Flowa reads it).
	n, err := decodeNote(sent.Object, inboxBodyMax)
	if err != nil {
		t.Fatal(err)
	}
	if len(n.Attachments) != 1 || n.Attachments[0].Name != "Ana spinning a nunchaku in a figure-8 in the park" || len(n.Mentions) != 2 || n.Mentions[0].Href != boAP || len(n.Hashtags) != 2 {
		t.Errorf("read back: %+v", n)
	}
	if err := allowedNoteHTML(n.text()); err != nil {
		t.Errorf("the content: %v", err)
	}
	// The member's own page, and the public page, show the alt text as the picture's alt.
	if page := ask(e.h, "GET", "/users/ana/posts/1", "", map[string]string{"Accept": browser}).Body.String(); !strings.Contains(page, `alt="Ana spinning a nunchaku in a figure-8 in the park"`) {
		t.Error("the public page does not show the alt text")
	}
	if got := e.must(200, e.ana, "GET", "/api/posts/1", nil)["posts"].([]any)[0].(map[string]any)["alt"]; got != "Ana spinning a nunchaku in a figure-8 in the park" {
		t.Errorf("the app's post: alt %v", got)
	}
}

/* ---------- the fan-out ---------- */

// One delivery per shared inbox, through the delivery queue, with every rule of FWR 4: 600 followers on 300
// servers with a shared inbox get 300 deliveries; a server with no shared inbox gets one per follower; a suspended
// domain gets nothing; a remote account mentioned who does not follow gets one; a member who stops sharing, the
// switches off, a This server post, a milestone and an audience that is not offered send nothing; the recipients
// are found by one bounded statement; and a Create made twice (a record put back as if the transaction that
// enqueued it had been lost) is one activity with one set of deliveries.
func TestDBPostFanOut(t *testing.T) {
	e := newNotesEnv(t)
	ctx := context.Background()
	for h := 0; h < 300; h++ {
		for k := 0; k < 2; k++ {
			id, _ := e.remote(fmt.Sprintf("f%d", k), fmt.Sprintf("h%d.example", h), true)
			e.follows(id)
		}
	}
	for k := 0; k < 3; k++ {
		id, _ := e.remote(fmt.Sprintf("solo%d", k), "noshared.example", false)
		e.follows(id)
	}
	for k := 0; k < 2; k++ {
		id, _ := e.remote(fmt.Sprintf("x%d", k), "bad.example", true)
		e.follows(id)
	}
	e.mustExec(`INSERT INTO domain_policies (domain, policy) VALUES ('bad.example', 'suspend')`)
	if err := e.a.fed.loadPolicy(ctx); err != nil {
		t.Fatal(err)
	}
	// A follower whose actor is gone, one still pending, and one suspended here are not sent to.
	gone, _ := e.remote("gone", "h1.example", false)
	e.follows(gone)
	e.mustExec(`UPDATE ap_actors SET state = 'gone' WHERE user_id = $1`, gone)
	pend, _ := e.remote("pend", "pending.example", false)
	e.mustExec(`INSERT INTO follows (follower_id, followee_id, state) VALUES ($1, $2, 'pending')`, pend, e.anaID)
	sus, _ := e.remote("sus", "suspended.example", false)
	e.follows(sus)
	e.mustExec(`UPDATE users SET suspended_until = now() + interval '1 day' WHERE id = $1`, sus)
	_, menAP := e.remote("men", "mentioned.example", false)

	id := e.postAs(e.ana, map[string]any{"body": "hello @men@mentioned.example", "audience": "public"})
	if e.count(`SELECT count(*) FROM ap_post_out WHERE post_id = $1 AND state = 'create'`, id) != 1 {
		t.Fatal("the post has no record of its way out")
	}
	e.produce()
	out := e.outRows()
	if len(out) != 1 || out[0].kind != "Create" {
		t.Fatalf("enqueued: %d activities", len(out))
	}
	ins := out[0].inboxes
	want := 300 + 3 + 1
	if len(ins) != want || slices.Compact(slices.Clone(ins)) == nil || len(slices.Compact(slices.Clone(ins))) != want {
		t.Errorf("%d deliveries, want %d", len(ins), want)
	}
	for _, in := range ins {
		switch {
		case strings.HasPrefix(in, "https://h") && strings.HasSuffix(in, ".example/inbox"):
		case strings.HasPrefix(in, "https://noshared.example/users/solo"):
		case in == menAP+"/inbox":
		default:
			t.Errorf("a delivery to %s", in)
		}
	}
	if e.count(`SELECT count(*) FROM ap_post_out WHERE post_id = $1 AND state = 'sent'`, id) != 1 {
		t.Error("the record is not sent")
	}
	var targets string
	e.a.db.QueryRow(`SELECT targets FROM ap_post_out WHERE post_id = $1`, id).Scan(&targets)
	var rec []deliveryTarget
	if json.Unmarshal([]byte(targets), &rec) != nil || len(rec) != want+1 { // the suspended domain's shared inbox is recorded; enqueue refused it
		t.Errorf("the record names %d recipients: %s", len(rec), targets)
	}
	// Made again, as if the transaction that enqueued it had been lost: one activity, the same deliveries.
	e.mustExec(`UPDATE ap_post_out SET state = 'create', targets = '' WHERE post_id = $1`, id)
	e.a.fed.postsDue.Store(true)
	e.produce()
	if again := e.outRows(); len(again) != 1 || len(again[0].inboxes) != want {
		t.Errorf("made twice: %d activities", len(again))
	}
	if e.count(`SELECT count(*) FROM ap_post_out WHERE post_id = $1 AND state = 'sent'`, id) != 1 {
		t.Error("made twice: the record is not sent")
	}

	// Nothing else goes out.
	before := e.count(`SELECT count(*) FROM ap_outbox`)
	nothing := func(what string) {
		t.Helper()
		e.produce()
		if n := e.count(`SELECT count(*) FROM ap_outbox`); n != before {
			t.Errorf("%s: %d activities enqueued", what, n-before)
		}
		if n := e.count(`SELECT count(*) FROM ap_post_out WHERE state = 'create'`); n != 0 {
			t.Errorf("%s: %d records wait", what, n)
		}
	}
	e.postAs(e.ana, map[string]any{"body": "only here", "audience": "local"})
	nothing("a This server post")
	e.postAs(e.ana, map[string]any{"body": "no audience named"})
	nothing("a post naming no audience")
	for _, aud := range []string{"unlisted", "followers", "direct"} {
		e.must(400, e.ana, "POST", "/api/posts", map[string]any{"body": "x", "audience": aud})
	}
	e.must(200, e.ana, "PUT", "/api/me", map[string]any{"prefs": map[string]bool{"autoPost": true}})
	if out := e.must(200, e.ana, "POST", "/api/achievements", map[string]any{"keys": []string{"first-post"}}); num(out["postId"]) == 0 {
		t.Fatal("no milestone was posted")
	}
	nothing("a milestone")
	// A milestone made Public by hand, with a record, is still not sent: the worker asks publicPost (F13).
	e.mustExec(`UPDATE posts SET audience = 'public' WHERE kind = 'milestone'`)
	e.mustExec(`INSERT INTO ap_post_out (post_id, user_id, state) SELECT id, user_id, 'create' FROM posts WHERE kind = 'milestone'`)
	e.a.fed.postsDue.Store(true)
	nothing("a milestone with a record")
	for _, flag := range []string{fedFlag, "follows"} {
		e.setFlag(flag, false)
		e.postAs(e.ana, map[string]any{"body": "while " + flag + " is off", "audience": "public"})
		e.setFlag(flag, true)
		nothing("a post made while " + flag + " was off")
	}
	// A member who does not share cannot post publicly; one who stops sharing before the worker runs sends nothing.
	_, bob := e.user("bob", "user")
	e.must(400, bob, "POST", "/api/posts", map[string]any{"body": "x", "audience": "public"})
	e.postAs(e.ana, map[string]any{"body": "then she stops sharing", "audience": "public"})
	e.must(200, e.ana, "PUT", "/api/me", map[string]any{"federates": false})
	nothing("a post whose author stopped sharing")
	e.must(200, e.ana, "PUT", "/api/me", map[string]any{"federates": true})
	// Federation refused (a moved origin, a secret that opens no key): Public is refused, nothing is written.
	e.a.fed.refuse("federation refused: a test")
	e.must(400, e.ana, "POST", "/api/posts", map[string]any{"body": "refused", "audience": "public"})
	if e.count(`SELECT count(*) FROM ap_post_out`) != 1 {
		t.Errorf("records: %d, want the one sent", e.count(`SELECT count(*) FROM ap_post_out`))
	}
}

// A deletion, a moderator's hide, and a post deleted before its Create was made: the Delete goes to the inboxes the
// Create went to, recorded when it was enqueued, not to the followers of the moment (one who followed since gets
// nothing, one who left since still gets it); a hide sends it once, and the restore and a deletion after send
// nothing; a post deleted before the worker came sends nothing at all. The Note's address answers the 404 at once.
func TestDBPostDeleteAndHide(t *testing.T) {
	e := newNotesEnv(t)
	a1, _ := e.remote("a", "one.example", true)
	a2, _ := e.remote("b", "two.example", true)
	e.follows(a1)
	e.follows(a2)
	p1 := e.postAs(e.ana, map[string]any{"body": "to be deleted", "audience": "public"})
	p2 := e.postAs(e.ana, map[string]any{"body": "to be hidden", "audience": "public"})
	e.produce()
	e.mustExec(`DELETE FROM ap_deliveries`) // the Creates were taken: a Delete has something to take back (8.8)
	// Followers change: two.example's account leaves, three.example's arrives.
	e.mustExec(`DELETE FROM follows WHERE follower_id = $1`, a2)
	a3, _ := e.remote("c", "three.example", true)
	e.follows(a3)
	e.must(200, e.ana, "DELETE", fmt.Sprintf("/api/posts/%d", p1), nil)
	if w := ask(e.h, "GET", fmt.Sprintf("/users/ana/posts/%d", p1), "", map[string]string{"Accept": apAccept}); w.Code != 404 {
		t.Errorf("a deleted post's Note: %d", w.Code)
	}
	e.produce()
	del := func(post int64) []outRow {
		var out []outRow
		for _, r := range e.outRows() {
			if r.kind == "Delete" && r.id == fmt.Sprintf("https://here.example/users/ana/posts/%d#delete", post) {
				out = append(out, r)
			}
		}
		return out
	}
	want := []string{"https://one.example/inbox", "https://two.example/inbox"}
	if d := del(p1); len(d) != 1 || !slices.Equal(d[0].inboxes, want) {
		t.Fatalf("the Delete of a deleted post: %+v", d)
	} else if !strings.Contains(d[0].body, `"object":{"id":"https://here.example/users/ana/posts/1","type":"Tombstone"}`) || !strings.Contains(d[0].body, `"actor":"https://here.example/users/ana"`) {
		t.Errorf("the Delete: %s", d[0].body)
	}
	if e.count(`SELECT count(*) FROM ap_post_out WHERE post_id = $1`, p1) != 0 {
		t.Error("the deleted post's record is still there")
	}
	// A moderator hides p2: the Delete goes to where its Create went; the restore sends nothing.
	_, mod := e.user("mo", "moderator")
	e.must(200, mod, "POST", "/api/mod/actions", map[string]any{"action": "hide", "kind": "post", "id": p2, "reason": "test"})
	if w := ask(e.h, "GET", fmt.Sprintf("/users/ana/posts/%d", p2), "", map[string]string{"Accept": apAccept}); w.Code != 404 {
		t.Errorf("a hidden post's Note: %d", w.Code)
	}
	e.produce()
	if d := del(p2); len(d) != 1 || !slices.Equal(d[0].inboxes, want) {
		t.Fatalf("the Delete of a hidden post: %+v", d)
	}
	n := len(e.outRows())
	e.must(200, mod, "POST", "/api/mod/actions", map[string]any{"action": "restore", "kind": "post", "id": p2})
	e.must(200, e.ana, "DELETE", fmt.Sprintf("/api/posts/%d", p2), nil)
	e.produce()
	if len(e.outRows()) != n {
		t.Error("a restore, or a deletion after a hide, sent something")
	}
	// Deleted before the worker came: nothing at all.
	p3 := e.postAs(e.ana, map[string]any{"body": "gone at once", "audience": "public"})
	e.must(200, e.ana, "DELETE", fmt.Sprintf("/api/posts/%d", p3), nil)
	e.produce()
	if len(e.outRows()) != n || e.count(`SELECT count(*) FROM ap_post_out`) != 0 {
		t.Errorf("a post deleted before its Create: %d activities, %d records", len(e.outRows())-n, e.count(`SELECT count(*) FROM ap_post_out`))
	}
	// Hidden before the worker came: nothing either.
	p4 := e.postAs(e.ana, map[string]any{"body": "hidden at once", "audience": "public"})
	e.must(200, mod, "POST", "/api/mod/actions", map[string]any{"action": "hide", "kind": "post", "id": p4, "reason": "test"})
	e.produce()
	if len(e.outRows()) != n || e.count(`SELECT count(*) FROM ap_post_out`) != 0 {
		t.Errorf("a post hidden before its Create: %d activities, %d records", len(e.outRows())-n, e.count(`SELECT count(*) FROM ap_post_out`))
	}
	// The post and its record are one statement: a record that cannot be written leaves no post.
	e.mustExec(`CREATE FUNCTION no_out() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'no'; END $$`)
	e.mustExec(`CREATE TRIGGER no_out BEFORE INSERT ON ap_post_out FOR EACH ROW EXECUTE FUNCTION no_out()`)
	posts := e.count(`SELECT count(*) FROM posts`)
	e.must(500, e.ana, "POST", "/api/posts", map[string]any{"body": "never half", "audience": "public"})
	if e.count(`SELECT count(*) FROM posts`) != posts {
		t.Error("a post was written without its record")
	}
}

// Update(Person): a change the actor shows marks it due; it is sent at once when none went in the last five
// minutes, and changes after it wait for the five minutes and go as one, with the newest state; a change made while
// one is being made leaves the row due; a change of nothing the actor shows, and a member who stopped sharing, send
// nothing. All on the delivery queue's clock, which the test moves.
func TestDBPersonUpdateCoalesced(t *testing.T) {
	e := newNotesEnv(t)
	f1, _ := e.remote("a", "one.example", true)
	e.follows(f1)
	updates := func() []outRow {
		var out []outRow
		for _, r := range e.outRows() {
			if r.kind == "Update" {
				out = append(out, r)
			}
		}
		return out
	}
	e.produce()
	e.must(200, e.ana, "PUT", "/api/me", map[string]any{"displayName": "Ana One"})
	e.produce()
	u := updates()
	if len(u) != 1 || !strings.Contains(u[0].body, `"name":"Ana One"`) || !slices.Equal(u[0].inboxes, []string{"https://one.example/inbox"}) || !strings.Contains(u[0].body, `"type":"Person"`) {
		t.Fatalf("the first Update: %+v", u)
	}
	// Two changes inside the five minutes: nothing yet, then one Update with the newest name.
	e.clock.add(time.Minute)
	e.must(200, e.ana, "PUT", "/api/me", map[string]any{"displayName": "Ana Two"})
	e.clock.add(time.Minute)
	e.must(200, e.ana, "PUT", "/api/me", map[string]any{"bio": "spins", "displayName": "Ana Three"})
	e.produce()
	if len(updates()) != 1 {
		t.Fatalf("an Update inside the five minutes: %d", len(updates()))
	}
	e.clock.add(3*time.Minute - time.Second)
	e.produce()
	if len(updates()) != 1 {
		t.Fatal("an Update a second before the five minutes")
	}
	e.clock.add(time.Second)
	e.produce()
	if u := updates(); len(u) != 2 || !strings.Contains(u[1].body, `"name":"Ana Three"`) || !strings.Contains(u[1].body, "spins") {
		t.Fatalf("the coalesced Update: %d, %v", len(u), u)
	}
	// A change while one is being made: the row stays due, five minutes after.
	e.clock.add(10 * time.Minute)
	e.must(200, e.ana, "PUT", "/api/me", map[string]any{"displayName": "Ana Four"})
	e.a.fed.personMade = func() {
		e.a.fed.personMade = nil
		e.must(200, e.ana, "PUT", "/api/me", map[string]any{"displayName": "Ana Five"})
	}
	e.produce()
	if len(updates()) != 3 || e.count(`SELECT count(*) FROM ap_person_out WHERE due_at IS NOT NULL`) != 1 {
		t.Fatalf("a change while sending: %d updates, %d due", len(updates()), e.count(`SELECT count(*) FROM ap_person_out WHERE due_at IS NOT NULL`))
	}
	e.clock.add(5 * time.Minute)
	e.produce()
	if u := updates(); len(u) != 4 || !strings.Contains(u[3].body, `"name":"Ana Five"`) {
		t.Fatalf("after a change while sending: %d", len(u))
	}
	// Nothing the actor shows: no Update. A member who stopped sharing: none either.
	e.clock.add(10 * time.Minute)
	e.must(200, e.ana, "PUT", "/api/me", map[string]any{"prefs": map[string]bool{"autoPost": false}})
	e.produce()
	e.must(200, e.ana, "PUT", "/api/me", map[string]any{"federates": false})
	e.must(200, e.ana, "PUT", "/api/me", map[string]any{"displayName": "Ana Gone"})
	e.produce()
	if len(updates()) != 4 {
		t.Errorf("an Update for nothing shown or for a member not sharing: %d", len(updates()))
	}
	// With the switches off, nothing is marked.
	e.must(200, e.ana, "PUT", "/api/me", map[string]any{"federates": true})
	e.produce()
	e.clock.add(10 * time.Minute)
	n := len(updates())
	e.setFlag(fedFlag, false)
	e.must(200, e.ana, "PUT", "/api/me", map[string]any{"displayName": "Ana Paused"})
	e.setFlag(fedFlag, true)
	e.produce()
	if len(updates()) != n {
		t.Error("a change made while the Federation switch was off was sent")
	}
}

// What leaves the server is what 14.5 lists: with every private field of a member set (practice, progress, notes,
// stats, preferences, the password hash and sessions, bookmarks, a This server post, a hidden post, a milestone, a
// combo's role and id), the bytes enqueued for a Public post, its deletion and an Update of the profile hold none of
// them, and a post that is not Public makes no record and no activity at all.
func TestDBNothingPrivateLeaves(t *testing.T) {
	e := newNotesEnv(t)
	f1, _ := e.remote("a", "one.example", true)
	e.follows(f1)
	secrets := []string{"SECRET-LOCAL-POST", "SECRET-HIDDEN-POST", "SECRET-NOTE", "SECRET-PROGRESS", "SECRET-BOOKMARK", "98765", "autoPost", "shareTransitions", "signature", "combo-id-secret", "flowa-test-only"}
	e.mustExec(`UPDATE users SET pw_hash = 'pbkdf2-secret-hash-of-ana' WHERE id = $1`, e.anaID)
	e.must(200, e.ana, "PUT", "/api/me", map[string]any{"stats": map[string]any{"xp": 98765, "level": 9, "streak": 98765}, "prefs": map[string]bool{"autoPost": true, "shareTransitions": true},
		"bio": "public bio", "displayName": "Ana"})
	e.mustExec(`INSERT INTO progress (user_id, prop, state) VALUES ($1, 'nunchaku', '{"notes":"SECRET-NOTE","sessions":[{"date":"2026-11-03","minutes":98765,"note":"SECRET-PROGRESS"}]}')`, e.anaID)
	e.mustExec(`INSERT INTO bookmarks (user_id, kind, ref) VALUES ($1, 'combo', 'SECRET-BOOKMARK')`, e.anaID)
	e.postAs(e.ana, map[string]any{"body": "SECRET-LOCAL-POST", "audience": "local"})
	hidden := e.postAs(e.ana, map[string]any{"body": "SECRET-HIDDEN-POST", "audience": "public"})
	e.mustExec(`UPDATE posts SET hidden_at = now() WHERE id = $1`, hidden)
	e.mustExec(`DELETE FROM ap_post_out WHERE post_id = $1`, hidden) // hidden before anything was made of it
	if e.count(`SELECT count(*) FROM ap_post_out`) != 0 || e.count(`SELECT count(*) FROM ap_outbox`) != 0 {
		t.Fatal("a post that is not Public made a record or an activity")
	}
	pub := e.postAs(e.ana, map[string]any{"body": "hello world", "audience": "public", "image": jpegData(t, 8, 8), "alt": "a picture",
		"combo": map[string]any{"id": "combo-id-secret", "name": "Morning", "role": "signature", "prop": "nunchaku", "seq": []string{"F7"}}})
	e.produce()
	e.clock.add(10 * time.Minute)
	e.must(200, e.ana, "PUT", "/api/me", map[string]any{"bio": "a new public bio"})
	e.must(200, e.ana, "DELETE", fmt.Sprintf("/api/posts/%d", pub), nil)
	e.produce()
	var pw string
	e.a.db.QueryRow(`SELECT pw_hash FROM users WHERE id = $1`, e.anaID).Scan(&pw)
	var tokens []string
	rows, _ := e.a.db.Query(`SELECT token_hash FROM sessions WHERE user_id = $1`, e.anaID)
	for rows.Next() {
		var s string
		rows.Scan(&s)
		tokens = append(tokens, s)
	}
	rows.Close()
	out := e.outRows()
	kinds := []string{}
	for _, r := range out {
		kinds = append(kinds, r.kind)
		for _, s := range append(append(slices.Clone(secrets), pw, e.ana), tokens...) {
			if s != "" && strings.Contains(r.body, s) {
				t.Errorf("a %s carries %q: %s", r.kind, s, r.body)
			}
		}
		for _, field := range []string{`"xp"`, `"level"`, `"streak"`, `"stats"`, `"prefs"`, `"role"`, `"audience"`, `"hidden`, `"email"`} {
			if strings.Contains(r.body, field) {
				t.Errorf("a %s names %s: %s", r.kind, field, r.body)
			}
		}
	}
	// The first Update is the profile saved before the post (with the stats and preferences that stay here), the
	// second the bio changed after it.
	if !slices.Equal(kinds, []string{"Create", "Update", "Delete", "Update"}) {
		t.Errorf("enqueued %v, want a Create, an Update, a Delete and an Update", kinds)
	}
}

// Two Flowa instances over the real fetcher, signer, inbox and delivery queue: a member of b.example follows ana
// here, ana posts, and b.example's inbox takes the Create, verified, and counts and drops it (notes in are FWR 5);
// ana deletes the post, and the Delete arrives the same way.
func TestDBTwoFlowaNotes(t *testing.T) {
	e := newNotesEnv(t)
	be := newEnv(t)
	if err := be.fedStart(map[string]string{"FLOWA_ORIGIN": "https://b.example"}); err != nil {
		t.Fatal(err)
	}
	be.setFlag(fedFlag, true)
	be.setFlag("follows", true)
	be.mustExec(`INSERT INTO instance_settings (key, value) VALUES ('federation_policy', '"open"') ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
	bf := be.a.fed
	if err := bf.loadPolicy(context.Background()); err != nil {
		t.Fatal(err)
	}
	boID, bo := be.user("bob", "user")
	be.must(200, bo, "PUT", "/api/me", map[string]any{"federates": true})
	for _, o := range []keyOwner{keyOwner(boID), instanceActor} {
		if _, err := bf.ensureKey(context.Background(), o); err != nil {
			t.Fatal(err)
		}
	}
	settleKeys(t, bf)
	e.c.fetch = actorStub(t, be.h)
	e.c.fetch.origin = e.a.fed.origin
	e.out.fetch = actorStub(t, be.h)
	e.out.fetch.origin = e.a.fed.origin
	bf.actors.fetch = actorStub(t, e.h)
	bf.actors.fetch.origin = bf.origin
	bf.actors.now, bf.actors.built = e.clock.now, time.Time{}
	bf.verifier.now, bf.verifier.built = e.clock.now, time.Time{}
	bf.inbox.now, bf.inbox.manual = e.clock.now, true
	var heard []inboxReason
	bf.inbox.heard = func(r inboxReason) { heard = append(heard, r) }
	bq := bf.out
	bq.manual, bq.now, bq.built, bq.jitter = true, e.clock.now, time.Time{}, func() float64 { return 0 }
	bq.mu.Lock()
	bq.floor = time.Time{}
	bq.mu.Unlock()
	bq.fetch = actorStub(t, e.h)
	bq.fetch.origin = bf.origin
	ctx := context.Background()
	pump := func() {
		t.Helper()
		for i := 0; i < 50; i++ {
			worked := false
			for _, step := range []func(context.Context) (bool, error){e.out.step, e.q.step, bq.step, bf.inbox.step} {
				w, err := step(ctx)
				if err != nil {
					t.Fatal(err)
				}
				worked = worked || w
			}
			if !worked {
				return
			}
		}
		t.Fatal("the workers did not settle")
	}
	// bob at b.example follows ana here, as a lookup by handle leaves her in b.example's cache.
	if _, err := bf.remoteActor(ctx, "https://here.example/users/ana"); err != nil {
		t.Fatal(err)
	}
	var anaThere int64
	if err := be.a.db.QueryRow(`SELECT user_id FROM ap_actors WHERE ap_id = 'https://here.example/users/ana'`).Scan(&anaThere); err != nil {
		t.Fatal(err)
	}
	be.must(200, bo, "PUT", fmt.Sprintf("/api/fed/accounts/%d/follow", anaThere), nil)
	pump()
	if e.count(`SELECT count(*) FROM follows WHERE followee_id = $1 AND state = 'accepted'`, e.anaID) != 1 {
		t.Fatal("bob does not follow ana")
	}
	dropped := func(kind string) int64 { return bf.inbox.stats.numbers(time.Now()).Dropped[kind] }
	heard = nil
	p := e.postAs(e.ana, map[string]any{"body": "hello b.example", "audience": "public"})
	pump()
	if !slices.Equal(heard, []inboxReason{inDropped}) || dropped("Create") != 1 {
		t.Fatalf("b.example heard %v, dropped Creates %d", heard, dropped("Create"))
	}
	e.must(200, e.ana, "DELETE", fmt.Sprintf("/api/posts/%d", p), nil)
	pump()
	if !slices.Equal(heard, []inboxReason{inDropped, inDropped}) || dropped("Delete") != 1 {
		t.Fatalf("b.example heard %v, dropped Deletes %d", heard, dropped("Delete"))
	}
	if n := e.count(`SELECT count(*) FROM ap_deliveries`) + be.count(`SELECT count(*) FROM ap_deliveries`); n != 0 {
		t.Errorf("%d deliveries left on the two queues", n)
	}
}

// The post handler, a deletion, a hide, a profile change and the producer in a mode that federates on a pool of one
// connection (TestDBOneConnection's rule): none holds a connection while it waits for another.
func TestDBNotesOneConnection(t *testing.T) {
	e := newNotesEnv(t)
	f1, _ := e.remote("a", "one.example", true)
	e.follows(f1)
	e.remote("men", "mentioned.example", true)
	_, mod := e.user("mo", "moderator")
	e.a.db.SetMaxOpenConns(1)
	defer e.a.db.SetMaxOpenConns(8)
	within := func(what string, f func()) {
		t.Helper()
		done := make(chan struct{})
		go func() { f(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatalf("%s did not finish in 10 s on one connection", what)
		}
	}
	var p1, p2 int64
	within("a Public post with a mention", func() {
		p1 = e.postAs(e.ana, map[string]any{"body": "hi @men@mentioned.example", "audience": "public", "image": jpegData(t, 8, 8), "alt": "x"})
		p2 = e.postAs(e.ana, map[string]any{"body": "two", "audience": "public"})
	})
	within("the producer", e.produce)
	within("a deletion", func() { e.must(200, e.ana, "DELETE", fmt.Sprintf("/api/posts/%d", p1), nil) })
	within("a hide", func() {
		e.must(200, mod, "POST", "/api/mod/actions", map[string]any{"action": "hide", "kind": "post", "id": p2, "reason": "x"})
	})
	within("a profile change", func() { e.must(200, e.ana, "PUT", "/api/me", map[string]any{"bio": "one connection"}) })
	within("the producer again", e.produce)
	if e.count(`SELECT count(*) FROM ap_outbox`) != 5 {
		t.Errorf("%d activities, want two Creates, two Deletes and an Update", e.count(`SELECT count(*) FROM ap_outbox`))
	}
}

/* ---------- statements of the post handler in local mode ---------- */

// stmtCounter is a driver that counts the statements sent to Postgres (Exec, Query and Prepare) and the
// transactions begun, around lib/pq.
type stmtCounter struct {
	dsn       string
	stmts, tx atomic.Int64
}

func (c *stmtCounter) Connect(context.Context) (driver.Conn, error) {
	conn, err := pq.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	return &countedConn{conn.(interface {
		driver.Conn
		driver.ExecerContext
		driver.QueryerContext
		driver.ConnBeginTx
	}), c}, nil
}

func (c *stmtCounter) Driver() driver.Driver { return pq.Driver{} }

type countedConn struct {
	inner interface {
		driver.Conn
		driver.ExecerContext
		driver.QueryerContext
		driver.ConnBeginTx
	}
	c *stmtCounter
}

func (k *countedConn) Prepare(q string) (driver.Stmt, error) {
	k.c.stmts.Add(1)
	return k.inner.Prepare(q)
}
func (k *countedConn) Close() error { return k.inner.Close() }
func (k *countedConn) Begin() (driver.Tx, error) {
	k.c.tx.Add(1)
	return k.inner.BeginTx(context.Background(), driver.TxOptions{})
}
func (k *countedConn) BeginTx(ctx context.Context, o driver.TxOptions) (driver.Tx, error) {
	k.c.tx.Add(1)
	return k.inner.BeginTx(ctx, o)
}
func (k *countedConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	k.c.stmts.Add(1)
	return k.inner.ExecContext(ctx, q, args)
}
func (k *countedConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	k.c.stmts.Add(1)
	return k.inner.QueryContext(ctx, q, args)
}

// postHandlerStatements is what POST /api/posts sends to Postgres in local mode for a post with a picture and a
// mention of a member: 8, measured with this test on the commit before FWR 4 (9882009) and pinned here
// (TestDBLocalModeUnchanged's rule: a deployment that sets nothing new does what it did). Read with a driver that
// counts every statement sent, around lib/pq.
const postHandlerStatements = 8

// Posting in local mode sends Postgres the statements it did before FWR 4, writes no record and starts no worker:
// the same count with a picture, its alt text and a mention, and with a handle of another server in the text.
func TestDBPostLocalStatements(t *testing.T) {
	e := newEnv(t)
	e.setFlag("community", true)
	_, ana := e.user("ana", "user")
	e.user("bob", "user")
	var dsn string
	if err := e.a.db.QueryRow(`SELECT current_setting('search_path')`).Scan(&dsn); err != nil {
		t.Fatal(err)
	}
	u := os.Getenv("TEST_DATABASE_URL")
	sep := "?"
	if strings.Contains(u, "?") {
		sep = "&"
	}
	c := &stmtCounter{dsn: u + sep + "search_path=" + dsn}
	db := sql.OpenDB(c)
	defer db.Close()
	was := e.a.db
	e.a.db = db
	defer func() { e.a.db = was }()
	for _, body := range []map[string]any{
		{"body": "a post that tells @bob", "image": jpegData(t, 8, 8), "alt": "a small picture"},
		{"body": "a post that names @bob@social.example and @bob"},
	} {
		c.stmts.Store(0)
		c.tx.Store(0)
		e.must(200, ana, "POST", "/api/posts", body)
		if n := c.stmts.Load(); n != postHandlerStatements {
			t.Errorf("POST /api/posts in local mode sent %d statements, want %d as before FWR 4", n, postHandlerStatements)
		}
		t.Logf("POST /api/posts in local mode: %d statements, %d transactions", c.stmts.Load(), c.tx.Load())
	}
	if e.count(`SELECT count(*) FROM ap_post_out`) != 0 || e.a.fed != nil {
		t.Error("local mode wrote a record of a way out, or has a federation")
	}
}
