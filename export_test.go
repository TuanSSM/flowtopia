package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"maps"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// exportOf downloads the archive of the session's member and opens it; it fails the test unless the answer is a
// whole zip.
func (e *tenv) exportOf(tok, query string) (*zip.Reader, []byte, http.Header) {
	e.t.Helper()
	r := httptest.NewRequest("GET", "/api/export"+query, nil)
	r.AddCookie(&http.Cookie{Name: cookieName, Value: tok})
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	if w.Code != 200 {
		e.t.Fatalf("GET /api/export%s: %d %s", query, w.Code, w.Body.String())
	}
	raw := w.Body.Bytes()
	z, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		e.t.Fatalf("the archive is not a zip Go can read: %v", err)
	}
	return z, raw, w.Result().Header
}

// exportCode asks for the archive and returns only the status and the body.
func (e *tenv) exportCode(tok, query string, hdr map[string]string) (int, string) {
	e.t.Helper()
	r := httptest.NewRequest("GET", "/api/export"+query, nil)
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	if tok != "" {
		r.AddCookie(&http.Cookie{Name: cookieName, Value: tok})
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w.Code, w.Body.String()
}

// zipFiles reads every file of an archive into memory, by name, in order.
func zipFiles(t *testing.T, z *zip.Reader) ([]string, map[string][]byte) {
	t.Helper()
	var names []string
	out := map[string][]byte{}
	for _, f := range z.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("%s: %v", f.Name, err)
		}
		if _, twice := out[f.Name]; twice {
			t.Errorf("%s is in the archive twice", f.Name)
		}
		names = append(names, f.Name)
		out[f.Name] = b
	}
	return names, out
}

// exportNames is every name a file of the archive may have: fixed words and row numbers only.
var exportNames = regexp.MustCompile(`^(README\.txt|profile\.json|profile-picture\.(jpg|png)|posts\.json|comments\.json|following\.json|followers\.json|blocks\.json|manifest\.json|pictures/post-[0-9]+\.(jpg|png))$`)

// jpegWithMetadata is a JPEG as a data URL that carries text outside its pixels: an Exif segment and a comment, as
// a phone camera's location would be carried.
func jpegWithMetadata(t *testing.T, mark string, shade uint8) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for i := range img.Pix {
		img.Pix[i] = shade
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	seg := func(marker byte, body string) []byte {
		n := len(body) + 2
		return append([]byte{0xFF, marker, byte(n >> 8), byte(n)}, body...)
	}
	out := append([]byte{}, b[:2]...) // SOI
	out = append(out, seg(0xE1, "Exif\x00\x00"+mark+" GPS 51.5N 0.1W")...)
	out = append(out, seg(0xFE, mark)...)
	out = append(out, b[2:]...)
	if !bytes.Contains(out, []byte(mark)) {
		t.Fatal("the test picture does not carry its mark")
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(out)
}

// pngShade is a small PNG of one colour, so each picture of the test is told apart by its bytes.
func pngShade(t *testing.T, shade uint8) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for i := range img.Pix {
		img.Pix[i] = shade
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func dataBytes(t *testing.T, v string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(v[strings.IndexByte(v, ',')+1:])
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestDBExport is PRT 1's DoD: the archive holds exactly the member's rows and nothing of anyone else's. Two members
// and accounts of other servers write rows of every kind the export reads (and some it must not); every row of
// the member carries ANA-OWN, every row of anyone else OTHER-SECRET, and the secrets of the member's own account
// are known strings. The archive must hold each of the member's rows, no marker of anyone else in any file's
// name or bytes (pictures included), and none of the secrets.
func TestDBExport(t *testing.T) {
	e := newEnv(t)
	e.setFlag("export", true)
	e.setFlag("follows", true)
	anaID, ana := e.user("ana", "user")
	boID, bo := e.user("bo", "user")
	cydID, _ := e.user("cydsecret", "user") // followed by bo only
	deeID, _ := e.user("dee", "user")       // blocked by ana
	eveID, _ := e.user("evesecret", "user") // blocked by bo only

	// The member's profile, and its secrets: the password hash, the session, a sealed key.
	e.mustExec(`UPDATE users SET display_name = 'ANA-OWN-display', pw_hash = 'SECRET-HASH-ana', links = '{"instagram": "ANA-OWN-link"}', prefs = '{"autoPost": false}' WHERE id = $1`, anaID)
	e.must(200, ana, "PUT", "/api/me", map[string]any{"bio": "ANA-OWN-bio", "avatar": jpegWithMetadata(t, "ANA-EXIF-LOCATION-avatar", 90)})
	e.mustExec(`INSERT INTO ap_keys (key_id, user_id, public_key, private_sealed) VALUES ('https://example.com/users/ana#main-key', $1, 'SECRET-PUBLIC-KEY', 'SECRET-SEALED-KEY')`, anaID)
	// Moderation and notices about her written by others are not hers.
	e.mustExec(`UPDATE users SET pw_hash = 'SECRET-HASH-bo', bio = 'OTHER-SECRET-bio', display_name = 'OTHER-SECRET-display', avatar = $2 WHERE id = $1`, boID, pngShade(t, 7))

	// Her posts: one of text, one with a picture whose file carried a location, one a moderator hid.
	p1 := e.post(ana, "ANA-OWN-post-text @bo")
	out := e.must(200, ana, "POST", "/api/posts", map[string]any{"body": "ANA-OWN-post-picture", "image": jpegWithMetadata(t, "ANA-EXIF-LOCATION-post", 160), "alt": "ANA-OWN-alt"})
	p2 := num(out["posts"].([]any)[0].(map[string]any)["id"])
	p3 := e.post(ana, "ANA-OWN-post-hidden")
	e.mustExec(`UPDATE posts SET hidden_at = now(), hidden_reason = 'ANA-OWN-hidden-reason' WHERE id = $1`, p3)

	// Bo's posts, one with a picture, one hidden, and comments on hers.
	q1 := e.post(bo, "OTHER-SECRET-post")
	out = e.must(200, bo, "POST", "/api/posts", map[string]any{"body": "OTHER-SECRET-post-picture", "image": pngShade(t, 33), "alt": "OTHER-SECRET-alt"})
	q2 := num(out["posts"].([]any)[0].(map[string]any)["id"])
	var boPic string
	if err := e.a.db.QueryRow(`SELECT image FROM posts WHERE id = $1`, q2).Scan(&boPic); err != nil {
		t.Fatal(err)
	}
	q3 := e.post(bo, "OTHER-SECRET-post-hidden")
	e.mustExec(`UPDATE posts SET hidden_at = now(), hidden_reason = 'OTHER-SECRET-reason' WHERE id = $1`, q3)
	boC := num(e.comment(bo, q1, 0, "OTHER-SECRET-comment")["id"])
	e.comment(bo, p1, 0, "OTHER-SECRET-comment-on-ana")

	// Accounts of other servers: one followed by her and following her, one she asked to follow, one only bo knows.
	var zedID, pendID, farID int64
	for _, r := range []struct {
		id         *int64
		name, pref string
	}{{&zedID, "zed@far.example", "zed"}, {&pendID, "pend@far.example", "pend"}, {&farID, "othersecret@far.example", "othersecret"}} {
		if err := e.a.db.QueryRow(`INSERT INTO users (username, display_name, pw_hash, domain, bio) VALUES ($1, 'OTHER-SECRET-remote-name', '!', 'far.example', 'OTHER-SECRET-remote-bio') RETURNING id`, r.name).Scan(r.id); err != nil {
			t.Fatal(err)
		}
		e.mustExec(`INSERT INTO ap_actors (user_id, ap_id, kind, preferred_username, summary, inbox, fetched_at, expires_at) VALUES ($1, $2, 'Person', $3, 'OTHER-SECRET-summary', 'https://far.example/inbox', now(), now() + interval '1 day')`,
			*r.id, "https://far.example/users/"+r.pref, r.pref)
	}
	// A remote post she answered, and a remote reply under her post.
	var rp int64
	if err := e.a.db.QueryRow(`INSERT INTO posts (user_id, body, audience, ap_id) VALUES ($1, 'OTHER-SECRET-remote-post', 'public', 'https://far.example/notes/1') RETURNING id`, zedID).Scan(&rp); err != nil {
		t.Fatal(err)
	}
	var rc int64
	if err := e.a.db.QueryRow(`INSERT INTO comments (post_id, user_id, body, ap_id) VALUES ($1, $2, 'OTHER-SECRET-remote-reply-root', 'https://far.example/notes/2') RETURNING id`, rp, zedID).Scan(&rc); err != nil {
		t.Fatal(err)
	}
	e.mustExec(`INSERT INTO comments (post_id, user_id, body, ap_id) VALUES ($1, $2, 'OTHER-SECRET-remote-reply', 'https://far.example/notes/3')`, p1, zedID)

	// Her comments: under bo's post, a reply to bo, under the remote post and as a reply to the remote reply, one a
	// moderator hid, and one she deleted (whose text is gone).
	c1 := num(e.comment(ana, q1, 0, "ANA-OWN-comment-under-bo")["id"])
	c2 := num(e.comment(ana, q1, boC, "ANA-OWN-reply-to-bo")["id"])
	var c3, c4 int64
	if err := e.a.db.QueryRow(`INSERT INTO comments (post_id, user_id, body) VALUES ($1, $2, 'ANA-OWN-comment-remote') RETURNING id`, rp, anaID).Scan(&c3); err != nil {
		t.Fatal(err)
	}
	if err := e.a.db.QueryRow(`INSERT INTO comments (post_id, user_id, parent_id, body) VALUES ($1, $2, $3, 'ANA-OWN-reply-remote') RETURNING id`, rp, anaID, rc).Scan(&c4); err != nil {
		t.Fatal(err)
	}
	c5 := num(e.comment(ana, p1, 0, "ANA-OWN-comment-hidden")["id"])
	e.mustExec(`UPDATE comments SET hidden_at = now(), hidden_reason = 'ANA-OWN-comment-hidden-reason' WHERE id = $1`, c5)
	gone := num(e.comment(ana, q1, 0, "will be deleted")["id"])
	e.must(200, ana, "DELETE", fmt.Sprintf("/api/comments/%d", gone), nil)
	// A deleted comment with a reply stays as a placeholder; one without goes. Either way it is not in the archive.
	kept := num(e.comment(ana, q1, 0, "will be deleted under a reply")["id"])
	e.comment(bo, q1, kept, "OTHER-SECRET-reply-to-a-deleted-one")
	e.must(200, ana, "DELETE", fmt.Sprintf("/api/comments/%d", kept), nil)
	if e.count(`SELECT count(*) FROM comments WHERE id = $1 AND deleted`, kept) != 1 {
		t.Fatal("the deleted comment with a reply is not kept as a placeholder")
	}

	// Follows and blocks, hers and others'.
	e.must(200, ana, "PUT", "/api/users/bo/follow", nil)
	e.must(200, bo, "PUT", "/api/users/ana/follow", nil)
	e.must(200, bo, "PUT", "/api/users/cydsecret/follow", nil)
	e.mustExec(`INSERT INTO follows (follower_id, followee_id, state) VALUES ($1, $2, 'accepted'), ($2, $1, 'accepted'), ($1, $3, 'pending'), ($4, $5, 'accepted')`, anaID, zedID, pendID, farID, boID)
	e.must(200, ana, "PUT", "/api/users/dee/block", nil)
	// Follows kept from before the block, both ways, which the pages no longer show (followShown).
	e.mustExec(`INSERT INTO follows (follower_id, followee_id) VALUES ($1, $2), ($2, $1)`, anaID, deeID)
	e.must(200, bo, "PUT", "/api/users/evesecret/block", nil)
	_, _, _ = cydID, deeID, eveID

	// Rows the export must not read: her likes, notifications to her, reports and moderation about her.
	e.must(200, ana, "POST", fmt.Sprintf("/api/posts/%d/like", q1), nil)
	e.mustExec(`INSERT INTO notifications (user_id, actor_id, kind, text) VALUES ($1, $2, 'mention', 'OTHER-SECRET-notification')`, anaID, boID)
	e.mustExec(`INSERT INTO reports (reporter_id, target_kind, target_id, reason, note) VALUES ($1, 'user', $2, 'spam', 'OTHER-SECRET-report')`, boID, anaID)
	e.mustExec(`INSERT INTO mod_actions (action, target_kind, target_id, target_user_id, reason) VALUES ('mute', 'user', $1, $1, 'OTHER-SECRET-modnote')`, anaID)

	// Another member's id in the address changes nothing: the archive is always the signed-in member's.
	for _, q := range []string{"", fmt.Sprintf("?user=%d", boID), fmt.Sprintf("?id=%d&username=bo", boID)} {
		e.a.lim.export.forget(fmt.Sprint(anaID)) // one download an hour; each address is checked on a download of its own
		z, raw, hdr := e.exportOf(ana, q)
		names, files := zipFiles(t, z)
		if q == "" {
			checkExportHeaders(t, hdr)
			unzipTest(t, raw)
		}

		// Names: generated, from the list, README first and manifest last.
		for _, n := range names {
			if !exportNames.MatchString(n) {
				t.Errorf("%q is not a name the archive gives", n)
			}
		}
		if names[0] != "README.txt" || names[len(names)-1] != "manifest.json" {
			t.Errorf("files in the order %v, want README.txt first and manifest.json last", names)
		}

		// (b) Nothing of anyone else's, anywhere, and none of the secrets.
		sess := tokenHash(ana)
		for _, n := range names {
			for _, bad := range []string{"OTHER-SECRET", "othersecret", "cydsecret", "evesecret", "SECRET-HASH", "SECRET-SEALED-KEY", "SECRET-PUBLIC-KEY",
				"ANA-EXIF-LOCATION", "GPS", ana, sess, "far.example/notes/3"} {
				if strings.Contains(n, bad) || bytes.Contains(files[n], []byte(bad)) {
					t.Errorf("%s (query %q) holds %q", n, q, bad)
				}
			}
			if b := dataBytes(t, boPic); bytes.Contains(files[n], b) {
				t.Errorf("%s holds bo's picture", n)
			}
		}

		// (a) Every row of hers.
		var prof map[string]any
		mustJSON(t, files["profile.json"], &prof)
		if prof["username"] != "ana" || prof["handle"] != "ana" || prof["displayName"] != "ANA-OWN-display" || prof["bio"] != "ANA-OWN-bio" || prof["picture"] != "profile-picture.jpg" ||
			prof["links"].(map[string]any)["instagram"] != "ANA-OWN-link" || prof["settings"].(map[string]any)["preferences"].(map[string]any)["autoPost"] != false || prof["version"] != float64(exportFormat) {
			t.Errorf("profile.json: %v", prof)
		}
		for _, k := range []string{"id", "pwHash", "pw_hash", "salt", "sessions", "muteReason", "mutedUntil", "email", "lastSeen"} {
			if _, ok := prof[k]; ok {
				t.Errorf("profile.json has %q", k)
			}
		}
		var avatar string
		if err := e.a.db.QueryRow(`SELECT avatar FROM users WHERE id = $1`, anaID).Scan(&avatar); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(files["profile-picture.jpg"], dataBytes(t, avatar)) {
			t.Error("profile-picture.jpg is not her picture as kept")
		}
		var posts []map[string]any
		mustJSON(t, files["posts.json"], &posts)
		got := map[int64]map[string]any{}
		for _, p := range posts {
			got[num(p["id"])] = p
		}
		if len(posts) != 3 || got[p1]["text"] != "ANA-OWN-post-text @bo" || got[p2]["text"] != "ANA-OWN-post-picture" || got[p3]["text"] != "ANA-OWN-post-hidden" {
			t.Fatalf("posts.json: %v", posts)
		}
		pic := fmt.Sprintf("pictures/post-%d.jpg", p2)
		if got[p2]["picture"] != pic || got[p2]["alt"] != "ANA-OWN-alt" || got[p1]["picture"] != nil || got[p3]["hidden"] != true || got[p3]["hiddenReason"] != "ANA-OWN-hidden-reason" || got[p1]["hidden"] != nil {
			t.Errorf("posts.json: %v", posts)
		}
		if _, err := time.Parse(time.RFC3339, got[p1]["created"].(string)); err != nil || !strings.HasSuffix(got[p1]["created"].(string), "Z") {
			t.Errorf("a time is not RFC 3339 in UTC: %v", got[p1]["created"])
		}
		var img string
		if err := e.a.db.QueryRow(`SELECT image FROM posts WHERE id = $1`, p2).Scan(&img); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(files[pic], dataBytes(t, img)) {
			t.Errorf("%s is not her picture as kept", pic)
		}
		if _, err := jpeg.Decode(bytes.NewReader(files[pic])); err != nil {
			t.Errorf("%s does not open as a JPEG: %v", pic, err)
		}
		var comments []map[string]any
		mustJSON(t, files["comments.json"], &comments)
		cs := map[int64]map[string]any{}
		for _, c := range comments {
			cs[num(c["id"])] = c
		}
		want := map[int64]string{c1: "ANA-OWN-comment-under-bo", c2: "@bo ANA-OWN-reply-to-bo", c3: "ANA-OWN-comment-remote", c4: "ANA-OWN-reply-remote", c5: "ANA-OWN-comment-hidden"}
		if len(comments) != len(want) {
			t.Errorf("comments.json has %d comments, want %d: %v", len(comments), len(want), comments)
		}
		for id, text := range want {
			if cs[id]["text"] != text {
				t.Errorf("comment %d: %v, want %q", id, cs[id], text)
			}
		}
		if num(cs[c1]["postId"]) != q1 || cs[c1]["parentId"] != nil || num(cs[c2]["parentId"]) != boC || cs[c3]["postAddress"] != "https://far.example/notes/1" ||
			num(cs[c4]["parentId"]) != rc || cs[c4]["parentAddress"] != "https://far.example/notes/2" || cs[c5]["hidden"] != true || cs[c5]["hiddenReason"] != "ANA-OWN-comment-hidden-reason" {
			t.Errorf("comments.json: %v", comments)
		}
		for _, id := range []int64{gone, kept} {
			if _, ok := cs[id]; ok {
				t.Errorf("deleted comment %d is in the archive", id)
			}
		}
		accounts := func(file string) []string {
			var l []map[string]any
			mustJSON(t, files[file], &l)
			var out []string
			for _, a := range l {
				s := a["handle"].(string)
				if v, ok := a["address"]; ok {
					s += " " + v.(string)
				}
				if a["pending"] == true {
					s += " pending"
				}
				out = append(out, s)
			}
			slices.Sort(out)
			return out
		}
		if got := accounts("following.json"); !slices.Equal(got, []string{"bo", "pend@far.example https://far.example/users/pend pending", "zed@far.example https://far.example/users/zed"}) {
			t.Errorf("following.json: %v", got)
		}
		if got := accounts("followers.json"); !slices.Equal(got, []string{"bo", "zed@far.example https://far.example/users/zed"}) {
			t.Errorf("followers.json: %v", got)
		}
		if got := accounts("blocks.json"); !slices.Equal(got, []string{"dee"}) {
			t.Errorf("blocks.json: %v", got)
		}

		// The manifest counts what is there and lists every file before it.
		var man struct {
			Format   string         `json:"format"`
			Version  int            `json:"version"`
			Complete bool           `json:"complete"`
			Exported string         `json:"exported"`
			Counts   map[string]int `json:"counts"`
			Files    []string       `json:"files"`
		}
		mustJSON(t, files["manifest.json"], &man)
		wantCounts := map[string]int{"posts": 3, "pictures": 1, "profilePicture": 1, "comments": 5, "following": 3, "followers": 2, "blocks": 1}
		if man.Format != "flowa-export" || man.Version != exportFormat || !man.Complete || !maps.Equal(man.Counts, wantCounts) || !slices.Equal(man.Files, names[:len(names)-1]) {
			t.Errorf("manifest.json: %+v, files %v", man, names)
		}
		for _, words := range []string{"manifest.json", "another server that waits for your answer is listed too, marked pending", "no file"} {
			if !bytes.Contains(files["README.txt"], []byte(words)) {
				t.Errorf("README.txt does not say %q", words)
			}
		}
	}
	// The downloads above were three in a row from one session: the limit is the next test's.
}

func mustJSON(t *testing.T, b []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("not JSON: %v: %s", err, b)
	}
}

func checkExportHeaders(t *testing.T, h http.Header) {
	t.Helper()
	for k, want := range map[string]string{"Content-Type": "application/zip", "Cache-Control": "no-store", "X-Content-Type-Options": "nosniff",
		"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'", "X-Frame-Options": "DENY", "Referrer-Policy": "no-referrer"} {
		if got := h.Get(k); got != want {
			t.Errorf("%s: %q, want %q", k, got, want)
		}
	}
	if cd := h.Get("Content-Disposition"); !regexp.MustCompile(`^attachment; filename="flowa-[a-z0-9._-]+-\d{4}-\d\d-\d\d\.zip"$`).MatchString(cd) {
		t.Errorf("Content-Disposition: %q", cd)
	}
}

// unzipTest runs unzip -t on the archive where unzip is installed; where it is not, the check is skipped and says so.
func unzipTest(t *testing.T, raw []byte) {
	t.Helper()
	t.Run("unzip -t", func(t *testing.T) {
		bin, err := exec.LookPath("unzip")
		if err != nil {
			t.Skip("unzip is not installed here")
		}
		p := filepath.Join(t.TempDir(), "a.zip")
		if err := os.WriteFile(p, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(bin, "-t", p).CombinedOutput(); err != nil {
			t.Fatalf("unzip -t: %v\n%s", err, out)
		}
	})
}

// The archive once an hour, and who may have it: a session first, the flag, a request from another site refused,
// a download that fails not counted, a muted member yes, a suspended or deleted one no, and a member with
// nothing gets an archive that says so.
func TestDBExportLimits(t *testing.T) {
	e := newEnv(t)
	var fail atomic.Bool
	var sized atomic.Int64
	e.a.exportHook = func(step string) error {
		if step == "size" {
			sized.Add(1)
		}
		if step == "write" && fail.Load() {
			return errors.New("the test cut the download")
		}
		return nil
	}
	var logged bytes.Buffer
	e.a.reqLog.mu.Lock()
	e.a.reqLog.out = &logged
	e.a.reqLog.mu.Unlock()
	logText := func() string {
		e.a.reqLog.mu.Lock()
		defer e.a.reqLog.mu.Unlock()
		return logged.String()
	}
	adminID, admin := e.user("boss", "admin")
	e.mustExec(`UPDATE users SET owner = true WHERE id = $1`, adminID)
	anaID, ana := e.user("ana", "user")
	e.post(ana, "a post")

	// Off by default: the address answers as one nobody has.
	if code, _ := e.exportCode(ana, "", nil); code != 404 {
		t.Fatalf("with the flag off: %d, want 404", code)
	}
	e.setFlag("export", true)
	if code, body := e.exportCode("", "", nil); code != 401 || !strings.Contains(body, "Sign in") {
		t.Fatalf("without a session: %d %s", code, body)
	}
	if code, _ := e.exportCode("not-a-session", "", nil); code != 401 {
		t.Fatalf("with a made-up session: %d", code)
	}
	if code, body := e.exportCode(ana, "", map[string]string{"Sec-Fetch-Site": "cross-site"}); code != 403 || !strings.Contains(body, "Settings") {
		t.Fatalf("from another site: %d %s", code, body)
	}
	if code, _ := e.exportCode(ana, "", map[string]string{"Sec-Fetch-Site": "same-site"}); code != 403 {
		t.Fatalf("from a sibling site: %d", code)
	}

	// A download that is cut off is not counted. The server drops the connection, so it runs on a real one.
	srv := httptest.NewServer(e.h)
	defer srv.Close()
	get := func(tok string) (*http.Response, error) {
		req, _ := http.NewRequest("GET", srv.URL+"/api/export", nil)
		req.Host = "example.com"
		req.AddCookie(&http.Cookie{Name: cookieName, Value: tok})
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		return srv.Client().Do(req)
	}
	fail.Store(true)
	for range 2 {
		res, err := get(ana)
		if err == nil {
			_, err = io.ReadAll(res.Body)
			res.Body.Close()
		}
		if err == nil {
			t.Fatal("a download cut off by the server read as complete")
		}
	}
	fail.Store(false)
	if code, body := e.exportCode(ana, "?check=1", nil); code != 200 || !strings.Contains(body, `"bytes"`) {
		t.Fatalf("after two failed downloads the check says %d %s", code, body)
	}
	// A download given back leaves no window behind: the next one opens its own, so two whole archives are always an
	// hour apart, not two minutes either side of the end of a window a failed one began.
	if a := e.a.lim.export; func() bool { a.mu.Lock(); defer a.mu.Unlock(); return a.m[fmt.Sprint(anaID)] != nil }() {
		t.Fatal("a download given back left its window in the limiter")
	}
	// The request log says why a download that was sent as 200 stopped.
	if !strings.Contains(logText(), `"refused":"download of data stopped"`) {
		t.Errorf("the request log does not say the download stopped:\n%s", logText())
	}
	between := time.Now()
	e.exportOf(ana, "")
	if a := e.a.lim.export; func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		f := a.m[fmt.Sprint(anaID)]
		return f == nil || !f.reset.After(between.Add(time.Hour))
	}() {
		t.Fatal("the whole download did not open a window of its own")
	}
	// Used: the check and the download both say so, and say when, before the size is read.
	before := sized.Load()
	for _, q := range []string{"?check=1", ""} {
		if code, body := e.exportCode(ana, q, nil); code != 429 || !strings.Contains(body, "less than an hour ago") {
			t.Fatalf("a second download in the hour (%q): %d %s", q, code, body)
		}
	}
	if n := sized.Load() - before; n != 0 {
		t.Fatalf("a refused request read the size %d times", n)
	}
	// HEAD is refused before anything is read or counted.
	cID, c := e.user("cai", "user")
	r := httptest.NewRequest("HEAD", "/api/export", nil)
	r.AddCookie(&http.Cookie{Name: cookieName, Value: c})
	w := httptest.NewRecorder()
	before = sized.Load()
	e.h.ServeHTTP(w, r)
	if w.Code != 405 || w.Header().Get("Allow") != "GET" || sized.Load() != before || w.Header().Get("Content-Type") == "application/zip" {
		t.Fatalf("HEAD: %d, Allow %q, %d size reads", w.Code, w.Header().Get("Allow"), sized.Load()-before)
	}
	if code, body := e.exportCode(c, "?check=1", nil); code != 200 {
		t.Fatalf("after a HEAD the check says %d %s", code, body)
	}
	_ = cID
	// Downloads begun are bounded too, failed or not: six in an hour.
	bID, b := e.user("bea", "user")
	fail.Store(true)
	for range 6 {
		if res, err := get(b); err == nil {
			io.Copy(io.Discard, res.Body)
			res.Body.Close()
		}
	}
	fail.Store(false)
	for _, q := range []string{"?check=1", ""} {
		if code, body := e.exportCode(b, q, nil); code != 429 || !strings.Contains(body, "did not finish") {
			t.Fatalf("a seventh download begun in the hour (%q): %d %s, want 429 in its own words", q, code, body)
		}
	}
	_ = bID

	// A muted member has their data; so has a member with nothing in it.
	mutedID, muted := e.user("mutey", "user")
	e.mustExec(`UPDATE users SET muted_until = now() + interval '1 day', mute_reason = 'OTHER-SECRET-mute' WHERE id = $1`, mutedID)
	z, _, _ := e.exportOf(muted, "")
	names, files := zipFiles(t, z)
	if slices.Contains(names, "profile-picture.jpg") || slices.Contains(names, "profile-picture.png") {
		t.Error("a member with no picture has one in the archive")
	}
	for _, n := range names {
		if bytes.Contains(files[n], []byte("OTHER-SECRET")) {
			t.Errorf("%s holds the mute's reason", n)
		}
	}
	for _, f := range []string{"posts.json", "comments.json", "following.json", "followers.json", "blocks.json"} {
		if strings.TrimSpace(string(files[f])) != "[]" {
			t.Errorf("%s of a member with nothing: %q", f, files[f])
		}
	}
	var man struct {
		Complete bool           `json:"complete"`
		Counts   map[string]int `json:"counts"`
	}
	mustJSON(t, files["manifest.json"], &man)
	if !man.Complete || !maps.Equal(man.Counts, map[string]int{"posts": 0, "pictures": 0, "profilePicture": 0, "comments": 0, "following": 0, "followers": 0, "blocks": 0}) {
		t.Errorf("manifest of a member with nothing: %+v", man)
	}

	// A suspended member is signed out and cannot sign in, so has no session to ask with (an open item of PRT).
	susID, sus := e.user("susy", "user")
	e.mustExec(`UPDATE users SET suspended_until = now() + interval '1 day', suspend_reason = 'spam' WHERE id = $1`, susID)
	if code, _ := e.exportCode(sus, "", nil); code != 401 {
		t.Fatalf("a suspended member: %d, want 401", code)
	}
	// A deleted account has nothing to export, and staff cannot export someone else's.
	if code, _ := e.exportCode(admin, fmt.Sprintf("?user=%d", anaID), nil); code != 200 {
		t.Fatalf("an admin's own export: %d", code)
	}
	e.must(200, admin, "DELETE", fmt.Sprintf("/api/admin/users/%d", anaID), nil)
	if code, _ := e.exportCode(ana, "", nil); code != 401 {
		t.Fatalf("a deleted account: %d, want 401", code)
	}
}

// The admin's archive above, asked with another member's id, is the admin's own: no staff export of another member
// is built. This checks it on the archive itself.
func TestDBExportOnlyOwn(t *testing.T) {
	e := newEnv(t)
	e.setFlag("export", true)
	adminID, admin := e.user("boss", "admin")
	e.mustExec(`UPDATE users SET owner = true WHERE id = $1`, adminID)
	anaID, ana := e.user("ana", "user")
	e.post(ana, "OTHER-SECRET-ana-post")
	for _, q := range []string{fmt.Sprintf("?user=%d", anaID), "?username=ana", fmt.Sprintf("?id=%d", anaID)} {
		z, _, _ := e.exportOf(admin, q)
		names, files := zipFiles(t, z)
		for _, n := range names {
			if bytes.Contains(files[n], []byte("OTHER-SECRET")) || bytes.Contains(files[n], []byte(`"ana"`)) {
				t.Errorf("an admin's archive asked with %s holds ana's data in %s", q, n)
			}
		}
		// Each is a download of its own; the limit is per member, so let the admin's count go for the next.
		e.a.lim.export.forget(fmt.Sprint(adminID))
	}
}

// A size cap and a time cap, each with an honest failure: too large is refused before a byte (and not counted), and
// running out of time drops the connection without the zip's directory.
func TestDBExportCaps(t *testing.T) {
	e := newEnv(t)
	e.setFlag("export", true)
	e.a.exportMax = 4096
	_, ana := e.user("ana", "user")
	e.must(200, ana, "POST", "/api/posts", map[string]any{"body": "big", "image": jpegWithMetadata(t, "x", 10)})
	e.mustExec(`UPDATE users SET bio = repeat('b', 300) WHERE username = 'ana'`)
	for _, q := range []string{"?check=1", "", ""} {
		if code, body := e.exportCode(ana, q, nil); code != 413 || !strings.Contains(body, "larger than") {
			t.Fatalf("over the size cap (%q): %d %s", q, code, body)
		}
	}

	slow := newEnv(t)
	slow.setFlag("export", true)
	slow.a.exportTime = time.Nanosecond // spent before the first file is begun
	_, bo := slow.user("bo", "user")
	srv := httptest.NewServer(slow.h)
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL+"/api/export", nil)
	req.Host = "example.com"
	req.AddCookie(&http.Cookie{Name: cookieName, Value: bo})
	var raw []byte
	res, err := srv.Client().Do(req)
	if err == nil {
		raw, err = io.ReadAll(res.Body)
		res.Body.Close()
	}
	if err == nil {
		t.Fatalf("a download out of time ended as if complete (%d bytes)", len(raw))
	}
	if _, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw))); err == nil {
		t.Fatal("what a download out of time sent opens as a zip")
	}
	if code, _ := slow.exportCode(bo, "?check=1", nil); code != 200 {
		t.Fatalf("a download out of time was counted: the check says %d", code)
	}

	// The writer's own cap, for an archive that grows while it is written.
	var sink bytes.Buffer
	cw := &exportWriter{w: &sink, max: 10}
	if _, err := cw.Write([]byte("12345")); err != nil {
		t.Fatal(err)
	}
	if _, err := cw.Write([]byte("123456")); !errors.Is(err, errExportTooBig) || sink.Len() != 5 {
		t.Fatalf("past the cap: %v, %d bytes written", err, sink.Len())
	}
}

// noisePNG is a PNG of random pixels, which no compression shrinks, as a data URL under maxImage.
func noisePNG(t *testing.T, side int) string {
	t.Helper()
	r := rand.New(rand.NewPCG(1, 2))
	img := image.NewRGBA(image.Rect(0, 0, side, side))
	for y := range side {
		for x := range side {
			img.Set(x, y, color.RGBA{uint8(r.UintN(256)), uint8(r.UintN(256)), uint8(r.UintN(256)), 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	v := "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
	if len(v) > maxImage {
		t.Fatalf("the test picture is %d bytes as a data URL, over maxImage", len(v))
	}
	return v
}

// manyPictures gives the member n posts with a picture of about 290 KB each.
func manyPictures(t *testing.T, e *tenv, uid int64, n int) {
	t.Helper()
	e.mustExec(`INSERT INTO posts (user_id, body, image) SELECT $1, 'post ' || g, $2 FROM generate_series(1, $3::int) g`, uid, noisePNG(t, 300), n)
}

// A member with many pictures: the archive (about 51 MB) is sent while the heap stays within a budget far under it,
// because the server holds one picture at a time.
func TestDBExportManyPictures(t *testing.T) {
	e := newEnv(t)
	e.setFlag("export", true)
	uid, tok := e.user("ana", "user")
	const n = 200
	manyPictures(t, e, uid, n)
	var peak atomic.Uint64
	var writes atomic.Int64
	var ms runtime.MemStats
	var mu sync.Mutex
	e.a.exportHook = func(step string) error {
		if writes.Add(1)%25 == 0 {
			mu.Lock()
			runtime.ReadMemStats(&ms)
			if ms.HeapAlloc > peak.Load() {
				peak.Store(ms.HeapAlloc)
			}
			mu.Unlock()
		}
		return nil
	}
	srv := httptest.NewServer(e.h)
	defer srv.Close()
	defer debug.SetGCPercent(debug.SetGCPercent(10)) // collect often, so the heap shows what is held, not what waits
	runtime.GC()
	runtime.ReadMemStats(&ms)
	base := ms.HeapAlloc
	req, _ := http.NewRequest("GET", srv.URL+"/api/export", nil)
	req.Host = "example.com"
	req.AddCookie(&http.Cookie{Name: cookieName, Value: tok})
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "a.zip")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	size, err := io.Copy(f, res.Body)
	res.Body.Close()
	f.Close()
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("%d after %d bytes: %v", res.StatusCode, size, err)
	}
	const budget = 24 << 20
	used := int64(peak.Load()) - int64(base)
	t.Logf("archive %d MB; heap at most %d MB over the %d MB before it", size>>20, used>>20, base>>20)
	if size < 3*budget/2 {
		t.Fatalf("the archive is %d bytes, too small to show that it is not held", size)
	}
	if used > budget {
		t.Fatalf("the heap grew %d MB while sending, over the %d MB budget", used>>20, budget>>20)
	}
	z, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	pics := 0
	for _, f := range z.File {
		if strings.HasPrefix(f.Name, "pictures/") {
			pics++
		}
	}
	if pics != n {
		t.Fatalf("%d pictures in the archive, want %d", pics, n)
	}
	raw, _ := os.ReadFile(p)
	unzipTest(t, raw)
}

// A client that stops reading: the server's write fails, the handler ends and gives its connection back, and the
// download is not counted.
func TestDBExportClientGone(t *testing.T) {
	e := newEnv(t)
	e.setFlag("export", true)
	uid, tok := e.user("ana", "user")
	manyPictures(t, e, uid, 80)
	done := make(chan struct{})
	h := e.h
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		h.ServeHTTP(w, r)
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/export", nil)
	req.Host = "example.com"
	req.AddCookie(&http.Cookie{Name: cookieName, Value: tok})
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(res.Body, make([]byte, 1<<20)); err != nil {
		t.Fatal(err)
	}
	cancel() // the person closes the tab
	res.Body.Close()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("the handler did not end within 60 s of the client going")
	}
	if n := e.a.db.Stats().InUse; n != 0 {
		t.Fatalf("%d connections still in use after the handler ended", n)
	}
	if code, _ := e.exportCode(tok, "?check=1", nil); code != 200 {
		t.Fatalf("a download the client left was counted: the check says %d", code)
	}
}

// On a pool of one connection (TestDBOneConnection's rule): the download answers, and at every write to the client
// and after every page the one connection is free, so the handler never holds it while a slow client reads.
func TestDBExportOneConnection(t *testing.T) {
	e := newEnv(t)
	e.setFlag("export", true)
	e.setFlag("follows", true)
	uid, tok := e.user("ana", "user")
	_, bo := e.user("bo", "user")
	manyPictures(t, e, uid, 3)
	p := e.post(bo, "a post")
	for i := range 3 {
		e.comment(tok, p, 0, fmt.Sprint("comment ", i))
	}
	e.must(200, tok, "PUT", "/api/users/bo/follow", nil)
	e.must(200, bo, "PUT", "/api/users/ana/follow", nil)
	e.must(200, tok, "PUT", "/api/me", map[string]any{"avatar": pngShade(t, 3)})
	e.a.db.SetMaxOpenConns(1)
	// A handler that holds the one connection would wait on itself; its own time cap ends it, so the test fails in
	// seconds rather than hanging until its timeout.
	e.a.exportTime = 20 * time.Second
	var held atomic.Value
	var checks atomic.Int64
	e.a.exportHook = func(step string) error {
		checks.Add(1)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var one int
		if err := e.a.db.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil {
			held.CompareAndSwap(nil, step+": "+err.Error())
			return err // the download stops, and gives back what it held, so the test ends at once
		}
		return nil
	}
	srv := httptest.NewServer(e.h)
	defer srv.Close()
	type answer struct {
		code int
		raw  []byte
		err  error
	}
	got := make(chan answer, 1)
	go func() {
		req, _ := http.NewRequest("GET", srv.URL+"/api/export", nil)
		req.Host = "example.com"
		req.AddCookie(&http.Cookie{Name: cookieName, Value: tok})
		res, err := srv.Client().Do(req)
		if err != nil {
			got <- answer{err: err}
			return
		}
		raw, err := io.ReadAll(res.Body)
		res.Body.Close()
		got <- answer{res.StatusCode, raw, err}
	}()
	select {
	case a := <-got:
		if v := held.Load(); v != nil {
			t.Fatalf("a connection was held while the archive was written (%v)", v)
		}
		if a.err != nil || a.code != 200 {
			t.Fatalf("on one connection: %d %v", a.code, a.err)
		}
		if _, err := zip.NewReader(bytes.NewReader(a.raw), int64(len(a.raw))); err != nil {
			t.Fatalf("on one connection the archive is not whole: %v", err)
		}
	case <-time.After(120 * time.Second):
		t.Fatal("the download did not answer in 120 s on one connection")
	}
	if v := held.Load(); v != nil {
		t.Fatalf("a connection was held while the archive was written (%v)", v)
	}
	if checks.Load() < 10 {
		t.Fatalf("only %d writes and pages were checked", checks.Load())
	}
}

// Every mode has the archive (the item says local included; the tests above run in local mode): a single-profile
// instance's owner, and a member of a federated instance, whose handles carry the instance's host.
func TestDBExportModes(t *testing.T) {
	s := newEnv(t)
	if err := s.fedSingleStart(map[string]string{"FLOWA_FEDERATE": "off"}); err != nil {
		t.Fatal(err)
	}
	s.setFlag("export", true)
	_, own := s.user("solo", "admin")
	s.post(own, "a post of the owner")
	z, _, _ := s.exportOf(own, "")
	_, files := zipFiles(t, z)
	var prof map[string]any
	mustJSON(t, files["profile.json"], &prof)
	if prof["handle"] != "solo" || !bytes.Contains(files["posts.json"], []byte("a post of the owner")) {
		t.Errorf("single mode: %v %s", prof, files["posts.json"])
	}

	f := newEnv(t)
	if err := f.fedStart(nil); err != nil {
		t.Fatal(err)
	}
	f.setFlag("export", true)
	f.setFlag("follows", true)
	_, ana := f.user("ana", "user")
	_, bo := f.user("bo", "user")
	f.must(200, ana, "PUT", "/api/users/bo/follow", nil)
	f.must(200, bo, "PUT", "/api/users/ana/follow", nil)
	z, _, _ = f.exportOf(ana, "")
	_, files = zipFiles(t, z)
	mustJSON(t, files["profile.json"], &prof)
	host := f.a.fedHost
	if host == "" || prof["handle"] != "ana@"+host || !bytes.Contains(files["following.json"], []byte(`"bo@`+host+`"`)) || !bytes.Contains(files["followers.json"], []byte(`"bo@`+host+`"`)) {
		t.Errorf("federated mode: %v %s %s", prof, files["following.json"], files["followers.json"])
	}
}

// The size is read without unpacking pictures: octet_length is the size a value is kept at, where length would
// unpack every picture to count its characters (TestDBQueryPlans holds the plan to the indexes).
func TestExportSizeReadsNoPicture(t *testing.T) {
	if regexp.MustCompile(`(^|[^_])length\(`).MatchString(exportSizeSQL) {
		t.Fatalf("exportSizeSQL counts characters with length(), which unpacks every picture:\n%s", exportSizeSQL)
	}
}

// At most exportsAtOnce downloads run at once across the server: the next is refused in words with Retry-After and
// not counted against the member, the check says so too, and a place is given back on every way out.
func TestDBExportAtOnce(t *testing.T) {
	e := newEnv(t)
	e.setFlag("export", true)
	hold, held := make(chan struct{}), make(chan struct{}, exportsAtOnce)
	var release sync.Once
	letGo := func() { release.Do(func() { close(hold) }) }
	var fail atomic.Bool
	e.a.exportHook = func(step string) error {
		if step == "page" {
			select {
			case held <- struct{}{}:
				<-hold
			default:
			}
		}
		if step == "write" && fail.Load() {
			return errors.New("the test cut the download")
		}
		return nil
	}
	srv := httptest.NewServer(e.h)
	defer srv.Close()
	// Deferred after the server's Close, so it runs first: a failure below still lets the held downloads end, and
	// the server's Close then finds none running, so the test fails instead of hanging.
	defer letGo()
	get := func(tok string) error {
		req, _ := http.NewRequest("GET", srv.URL+"/api/export", nil)
		req.Host = "example.com"
		req.AddCookie(&http.Cookie{Name: cookieName, Value: tok})
		res, err := srv.Client().Do(req)
		if err != nil {
			return err
		}
		defer res.Body.Close()
		_, err = io.Copy(io.Discard, res.Body)
		return err
	}
	var toks []string
	for i := range exportsAtOnce + 1 {
		_, tok := e.user(fmt.Sprint("member", i), "user")
		toks = append(toks, tok)
	}
	done := make(chan error, exportsAtOnce)
	for _, tok := range toks[:exportsAtOnce] {
		go func() { done <- get(tok) }()
	}
	for range exportsAtOnce {
		select {
		case <-held:
		case <-time.After(30 * time.Second):
			t.Fatal("the downloads did not start")
		}
	}
	last := toks[exportsAtOnce]
	for _, q := range []string{"?check=1", ""} {
		code, body, hdr := func() (int, string, http.Header) {
			r := httptest.NewRequest("GET", "/api/export"+q, nil)
			r.AddCookie(&http.Cookie{Name: cookieName, Value: last})
			w := httptest.NewRecorder()
			e.h.ServeHTTP(w, r)
			return w.Code, w.Body.String(), w.Result().Header
		}()
		if code != 503 || hdr.Get("Retry-After") != "60" || !strings.Contains(body, "other people's downloads") {
			t.Fatalf("a download past %d at once (%q): %d %s, Retry-After %q", exportsAtOnce, q, code, body, hdr.Get("Retry-After"))
		}
	}
	letGo()
	for range exportsAtOnce {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	// The member who waited was not counted: a whole download now; then a cut-off one gives its place back too.
	e.exportOf(last, "")
	fail.Store(true)
	for i := range exportsAtOnce + 1 {
		_, tok := e.user(fmt.Sprint("cut", i), "user")
		if err := get(tok); err == nil {
			t.Fatal("a cut download read as complete")
		}
	}
	fail.Store(false)
	if n := len(e.a.exportSlots); n != 0 {
		t.Fatalf("%d places still taken after every download ended", n)
	}
}

// A post deleted between posts.json and the reading of its picture: the name stays in posts.json with no file, as
// README.txt says, and manifest.json counts the pictures that are there.
func TestDBExportPictureGone(t *testing.T) {
	e := newEnv(t)
	e.setFlag("export", true)
	_, tok := e.user("ana", "user")
	out := e.must(200, tok, "POST", "/api/posts", map[string]any{"body": "soon gone", "image": pngShade(t, 40)})
	id := num(out["posts"].([]any)[0].(map[string]any)["id"])
	var once sync.Once
	e.a.exportHook = func(step string) error {
		if step == "page" {
			once.Do(func() { e.mustExec(`DELETE FROM posts WHERE id = $1`, id) })
		}
		return nil
	}
	z, _, _ := e.exportOf(tok, "")
	names, files := zipFiles(t, z)
	if !bytes.Contains(files["posts.json"], []byte(fmt.Sprintf(`"pictures/post-%d.png"`, id))) || slices.Contains(names, fmt.Sprintf("pictures/post-%d.png", id)) {
		t.Fatalf("posts.json %s, files %v", files["posts.json"], names)
	}
	var man struct {
		Counts map[string]int `json:"counts"`
	}
	mustJSON(t, files["manifest.json"], &man)
	if man.Counts["pictures"] != 0 || man.Counts["posts"] != 1 || !bytes.Contains(files["README.txt"], []byte("no file")) {
		t.Fatalf("manifest %v; README %s", man.Counts, files["README.txt"])
	}
}

// The size estimate is an upper bound of the bytes the writer counts: with the cap set to the estimate exactly, a
// member whose text is as large on the wire as text can be (four-byte characters at random, which compression
// cannot shrink, and characters JSON writes as six bytes) gets a whole archive, and one byte less is refused
// before it starts.
func TestDBExportEstimateBound(t *testing.T) {
	e := newEnv(t)
	e.setFlag("export", true)
	uid, tok := e.user("ana", "user")
	_, bo := e.user("bo", "user")
	r := rand.New(rand.NewPCG(3, 4))
	wide := func(n int) string {
		var b strings.Builder
		for range n {
			if r.IntN(4) == 0 {
				b.WriteRune([]rune{'<', '>', '&', '\x01', '\x1f', '"', '\\'}[r.IntN(7)])
			} else {
				b.WriteRune(rune(0x1F300 + r.IntN(0x900)))
			}
		}
		return b.String()
	}
	e.mustExec(`UPDATE users SET bio = $2, display_name = $3 WHERE id = $1`, uid, wide(300), wide(40))
	p := e.post(bo, "a post")
	for range 15 {
		e.mustExec(`INSERT INTO posts (user_id, body, alt, image) VALUES ($1, $2, $3, $4)`, uid, wide(2000), wide(200), noisePNG(t, 40))
		e.mustExec(`INSERT INTO comments (post_id, user_id, body) VALUES ($1, $2, $3)`, p, uid, wide(1000))
	}
	atEstimate(t, e, uid, tok)
}

// atEstimate sets the cap to the member's estimate exactly and downloads a whole archive over a real connection (a
// download past the cap is broken off), then sets it one byte lower and is refused before anything is sent.
func atEstimate(t *testing.T, e *tenv, uid int64, tok string) {
	t.Helper()
	code, body := e.exportCode(tok, "?check=1", nil)
	if code != 200 {
		t.Fatalf("check: %d %s", code, body)
	}
	var est struct {
		Bytes int64 `json:"bytes"`
	}
	mustJSON(t, []byte(body), &est)
	// On a real connection, since a download past the cap is broken off.
	e.a.exportMax = est.Bytes
	srv := httptest.NewServer(e.h)
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL+"/api/export", nil)
	req.Host = "example.com"
	req.AddCookie(&http.Cookie{Name: cookieName, Value: tok})
	var raw []byte
	res, err := srv.Client().Do(req)
	if err == nil {
		raw, err = io.ReadAll(res.Body)
		res.Body.Close()
	}
	if err != nil {
		t.Fatalf("with the cap at the estimate the download broke off after %d bytes: %v", len(raw), err)
	}
	z, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if _, files := zipFiles(t, z); !bytes.Contains(files["manifest.json"], []byte(`"complete": true`)) {
		t.Fatal("the archive at the cap is not complete")
	}
	t.Logf("estimate %d bytes, archive %d bytes", est.Bytes, len(raw))
	e.a.lim.export.forget(fmt.Sprint(uid))
	e.a.exportMax = est.Bytes - 1
	if code, _ := e.exportCode(tok, "", nil); code != 413 {
		t.Fatalf("one byte under the estimate: %d, want 413", code)
	}
	e.a.exportMax = 0
}

// remoteAddr is an address on another server of n random characters, a sixteenth of them &, which JSON writes as six
// bytes, and the rest letters and digits, so that compression cannot shrink it much.
func remoteAddr(r *rand.Rand, n int) string {
	const set = "&&&&<>abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := []byte("https://far.example/")
	for range n {
		b = append(b, set[r.IntN(len(set))])
	}
	return string(b)
}

// remoteAccount makes an account of another server with an actor address of n characters, and returns its id.
func remoteAccount(t *testing.T, e *tenv, r *rand.Rand, name string, n int) int64 {
	t.Helper()
	var id int64
	if err := e.a.db.QueryRow(`INSERT INTO users (username, display_name, pw_hash, domain) VALUES ($1, '', '!', 'far.example') RETURNING id`, name+"@far.example").Scan(&id); err != nil {
		t.Fatal(err)
	}
	e.mustExec(`INSERT INTO ap_actors (user_id, ap_id, kind, preferred_username, inbox, fetched_at, expires_at) VALUES ($1, $2, 'Person', $3, 'https://far.example/inbox', now(), now() + interval '1 day')`,
		id, remoteAddr(r, n), name)
	return id
}

// The addresses of other servers' accounts count in the estimate: a member whose lists are long addresses (and
// little else, so nothing else in the estimate covers them) still gets a whole archive at the cap set to the
// estimate. Without the account terms it is broken off.
func TestDBExportEstimateAccounts(t *testing.T) {
	e := newEnv(t)
	e.setFlag("export", true)
	uid, tok := e.user("ria", "user")
	r := rand.New(rand.NewPCG(5, 6))
	for i := range 100 {
		rid := remoteAccount(t, e, r, fmt.Sprint("far", i), 2500)
		e.mustExec(`INSERT INTO follows (follower_id, followee_id) VALUES ($1, $2), ($2, $1)`, uid, rid)
	}
	atEstimate(t, e, uid, tok)
}

// The addresses of other servers' posts and comments that a member answered count in the estimate too, on their
// own: comments of a few letters under remote posts, as replies to remote comments, both with long addresses.
// Without the comment terms it is broken off.
func TestDBExportEstimateComments(t *testing.T) {
	e := newEnv(t)
	e.setFlag("export", true)
	uid, tok := e.user("ria", "user")
	r := rand.New(rand.NewPCG(7, 8))
	rid := remoteAccount(t, e, r, "far", 20)
	for range 60 {
		var post, root int64
		if err := e.a.db.QueryRow(`INSERT INTO posts (user_id, body, audience, ap_id) VALUES ($1, 'x', 'public', $2) RETURNING id`, rid, remoteAddr(r, 2500)).Scan(&post); err != nil {
			t.Fatal(err)
		}
		if err := e.a.db.QueryRow(`INSERT INTO comments (post_id, user_id, body, ap_id) VALUES ($1, $2, 'x', $3) RETURNING id`, post, rid, remoteAddr(r, 2500)).Scan(&root); err != nil {
			t.Fatal(err)
		}
		e.mustExec(`INSERT INTO comments (post_id, user_id, parent_id, body) VALUES ($1, $2, $3, 'y')`, post, uid, root)
	}
	atEstimate(t, e, uid, tok)
}

// flushSpy stands between the server's writer and Flowa's, and counts the pushes that reach it through Flowa's own
// wrappers (the request log's and the allowance's), then passes them on.
type flushSpy struct {
	http.ResponseWriter
	n *atomic.Int32
}

func (f flushSpy) FlushError() error {
	f.n.Add(1)
	return http.NewResponseController(f.ResponseWriter).Flush()
}

func (f flushSpy) Unwrap() http.ResponseWriter { return f.ResponseWriter }

// The last bytes of a whole archive (the end of the zip's directory, held in the server's buffer) are pushed out
// before the download counts: a failure there is a failed download, broken off and given back. On a real connection
// the push reaches the server's writer through every wrapper, so it is not one that always answers "not supported".
func TestDBExportFlush(t *testing.T) {
	e := newEnv(t)
	e.setFlag("export", true)
	_, tok := e.user("ana", "user")
	e.post(tok, "a post")
	var fail, flushed atomic.Bool
	e.a.exportHook = func(step string) error {
		if step == "flush" {
			flushed.Store(true)
			if fail.Load() {
				return errors.New("the test failed the last push")
			}
		}
		return nil
	}
	var pushes atomic.Int32
	h := e.h
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(flushSpy{w, &pushes}, r)
	}))
	defer srv.Close()
	get := func() ([]byte, error) {
		req, _ := http.NewRequest("GET", srv.URL+"/api/export", nil)
		req.Host = "example.com"
		req.AddCookie(&http.Cookie{Name: cookieName, Value: tok})
		res, err := srv.Client().Do(req)
		if err != nil {
			return nil, err
		}
		defer res.Body.Close()
		return io.ReadAll(res.Body)
	}
	fail.Store(true)
	if _, err := get(); err == nil || !flushed.Load() {
		t.Fatalf("a download whose last push failed ended as complete (flushed %v)", flushed.Load())
	}
	fail.Store(false)
	if code, body := e.exportCode(tok, "?check=1", nil); code != 200 {
		t.Fatalf("a download whose last push failed was counted: %d %s", code, body)
	}
	pushes.Store(0)
	raw, err := get()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw))); err != nil {
		t.Fatal(err)
	}
	if pushes.Load() == 0 {
		t.Fatal("the last push of a whole download never reached the server's writer")
	}
}

// The page's ask (?check) has its own allowance: the 61st in an hour is refused in words before the size is read, and
// neither the hour nor the tries are touched, so a download still starts.
func TestDBExportCheckLimit(t *testing.T) {
	e := newEnv(t)
	e.setFlag("export", true)
	_, tok := e.user("ana", "user")
	var sized atomic.Int64
	e.a.exportHook = func(step string) error {
		if step == "size" {
			sized.Add(1)
		}
		return nil
	}
	for i := range 60 {
		if code, body := e.exportCode(tok, "?check=1", nil); code != 200 {
			t.Fatalf("ask %d: %d %s", i+1, code, body)
		}
	}
	before := sized.Load()
	code, body := e.exportCode(tok, "?check=1", nil)
	if code != 429 || !strings.Contains(body, "asked about a download many times") || sized.Load() != before {
		t.Fatalf("the 61st ask: %d %s, %d size reads", code, body, sized.Load()-before)
	}
	e.exportOf(tok, "")
}

// A reader slower than exportMinRate after exportGrace lets its place go: the download is broken off, the hour given
// back and the try counted; a reader just above the rate gets the whole archive. The clock is the test's: for the
// slow reader it moves 20 s at every write, far under the rate; for the other it runs from the bytes the client has
// read at 9 KB a second. The server counts at least what the client read, so its rate is never under 9 KB/s, and
// the download runs past the grace, so the rule is asked at every write after it. Nothing waits.
func TestDBExportSlowReader(t *testing.T) {
	e := newEnv(t)
	e.setFlag("export", true)
	uid, tok := e.user("ana", "user")
	manyPictures(t, e, uid, 4) // about 1 MB: two minutes at 9 KB/s
	var mu sync.Mutex
	start := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	clock := start
	var slow atomic.Bool
	var read atomic.Int64
	const fast = 9 << 10
	e.a.exportNow = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		if slow.Load() {
			return clock
		}
		return start.Add(time.Duration(read.Load()) * time.Second / fast)
	}
	e.a.exportHook = func(step string) error {
		if step == "write" && slow.Load() {
			mu.Lock()
			clock = clock.Add(20 * time.Second) // each write of at most a few KB takes 20 s: far under 8 KB/s
			mu.Unlock()
		}
		return nil
	}
	srv := httptest.NewServer(e.h)
	defer srv.Close()
	get := func() ([]byte, error) {
		req, _ := http.NewRequest("GET", srv.URL+"/api/export", nil)
		req.Host = "example.com"
		req.AddCookie(&http.Cookie{Name: cookieName, Value: tok})
		res, err := srv.Client().Do(req)
		if err != nil {
			return nil, err
		}
		defer res.Body.Close()
		var out bytes.Buffer
		buf := make([]byte, 2048)
		for {
			n, err := res.Body.Read(buf)
			out.Write(buf[:n])
			read.Add(int64(n))
			if err == io.EOF {
				return out.Bytes(), nil
			}
			if err != nil {
				return out.Bytes(), err
			}
		}
	}
	slow.Store(true)
	if raw, err := get(); err == nil {
		t.Fatalf("a download far under the slowest rate ran to the end (%d bytes)", len(raw))
	}
	slow.Store(false)
	key := fmt.Sprint(uid)
	tries := func() int {
		l := e.a.lim.exportTries
		l.mu.Lock()
		defer l.mu.Unlock()
		if f := l.m[key]; f != nil {
			return f.n
		}
		return 0
	}
	if n := tries(); n != tenths {
		t.Fatalf("the slow download's try counts %d tenths, want %d", n, tenths)
	}
	if code, body := e.exportCode(tok, "?check=1", nil); code != 200 {
		t.Fatalf("the slow download spent the hour: %d %s", code, body)
	}
	read.Store(0)
	raw, err := get()
	if err != nil {
		t.Fatalf("a reader at 9 KB/s was broken off after %d bytes: %v", len(raw), err)
	}
	if _, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw))); err != nil {
		t.Fatal(err)
	}
	if took := time.Duration(read.Load()) * time.Second / fast; took <= exportGrace {
		t.Fatalf("the reader at 9 KB/s took %v by the test's clock, within the grace, so the rule was never asked", took)
	}
}
