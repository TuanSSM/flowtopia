// Flowa: a progress tracker for flow arts props. It serves the embedded UI and keeps
// accounts, sessions and per-prop progress in Postgres.
package main

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/lib/pq"
)

//go:embed web
var webFS embed.FS

const (
	maxBody     = 5 << 20
	keepBackups = 14
	sessionTTL  = 30 * 24 * time.Hour
	cookieName  = "flowa_session"
	// PBKDF2-SHA256 rounds. 200,000 takes about 0.7 s on this Pi 4; raise it on faster hardware.
	pwRounds   = 200000
	maxFails   = 8
	failWindow = 10 * time.Minute
)

// Feature flags and their defaults. They are changed at runtime by an admin.
var flagDefaults = map[string]bool{"details": false, "demos": false, "references": false, "signups": true, "tour": false, "model3d": true, "community": true, "game": true}

// Props a user can track. The catalog for each prop lives in the web UI.
var knownProps = []string{"nunchaku", "poi", "staff", "doublestaff", "buugeng"}

var usernameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,23}$`)

const schema = `
CREATE TABLE IF NOT EXISTS users (
	id BIGSERIAL PRIMARY KEY,
	username TEXT NOT NULL UNIQUE,
	display_name TEXT NOT NULL DEFAULT '',
	pw_hash TEXT NOT NULL,
	role TEXT NOT NULL DEFAULT 'user',
	props JSONB NOT NULL DEFAULT '[]',
	seen JSONB NOT NULL DEFAULT '[]',
	onboarded BOOLEAN NOT NULL DEFAULT false,
	avatar TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS sessions (
	token_hash TEXT PRIMARY KEY,
	user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	expires_at TIMESTAMPTZ NOT NULL
);
CREATE TABLE IF NOT EXISTS progress (
	user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	prop TEXT NOT NULL,
	rev BIGINT NOT NULL DEFAULT 0,
	state JSONB NOT NULL,
	updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	PRIMARY KEY (user_id, prop)
);
CREATE TABLE IF NOT EXISTS flags (
	name TEXT PRIMARY KEY,
	enabled BOOLEAN NOT NULL
);`

type app struct {
	db      *sql.DB
	dataDir string
	mu      sync.Mutex
	fails   map[string]*failCount
}

type failCount struct {
	n     int
	reset time.Time
}

type user struct {
	ID          int64           `json:"id"`
	Username    string          `json:"username"`
	DisplayName string          `json:"displayName"`
	Role        string          `json:"role"`
	Props       json.RawMessage `json:"props"`
	Seen        json.RawMessage `json:"seen"`
	Onboarded   bool            `json:"onboarded"`
	Avatar      string          `json:"avatar"`
	Bio         string          `json:"bio"`
	Links       json.RawMessage `json:"links"`
	Prefs       json.RawMessage `json:"prefs"`
	Stats       json.RawMessage `json:"stats"`
	Created     time.Time       `json:"created"`
}

const userCols = "u.id, u.username, u.display_name, u.role, u.props, u.seen, u.onboarded, u.avatar, u.bio, u.links, u.prefs, u.stats, u.created_at"

type scanner interface{ Scan(...any) error }

// scanUser reads userCols, followed by any extra columns the query selected.
func scanUser(row scanner, extra ...any) (*user, error) {
	var u user
	var props, seen, links, prefs, stats []byte
	dst := append([]any{&u.ID, &u.Username, &u.DisplayName, &u.Role, &props, &seen, &u.Onboarded, &u.Avatar, &u.Bio, &links, &prefs, &stats, &u.Created}, extra...)
	if err := row.Scan(dst...); err != nil {
		return nil, err
	}
	u.Props, u.Seen, u.Links, u.Prefs, u.Stats = props, seen, links, prefs, stats
	return &u, nil
}

/* ---------- passwords and sessions ---------- */

func hashPassword(pw string) string {
	salt := make([]byte, 16)
	rand.Read(salt)
	key, _ := pbkdf2.Key(sha256.New, pw, salt, pwRounds, 32)
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", pwRounds, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}

func checkPassword(hash, pw string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	rounds, err := strconv.Atoi(parts[1])
	salt, err2 := base64.RawStdEncoding.DecodeString(parts[2])
	want, err3 := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || err2 != nil || err3 != nil || rounds < 1 {
		return false
	}
	got, _ := pbkdf2.Key(sha256.New, pw, salt, rounds, len(want))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// A hash to burn the same time on when the username does not exist.
var dummyHash = hashPassword("not a real password")

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (a *app) startSession(w http.ResponseWriter, r *http.Request, id int64) error {
	raw := make([]byte, 32)
	rand.Read(raw)
	token := base64.RawURLEncoding.EncodeToString(raw)
	exp := time.Now().Add(sessionTTL)
	if _, err := a.db.Exec(`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`, tokenHash(token), id, exp); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", Expires: exp, HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"})
	return nil
}

func (a *app) currentUser(r *http.Request) *user {
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return nil
	}
	u, err := scanUser(a.db.QueryRow(`SELECT `+userCols+` FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now()`, tokenHash(c.Value)))
	if err != nil {
		return nil
	}
	return u
}

/* ---------- HTTP helpers ---------- */

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (a *app) serverError(w http.ResponseWriter, err error) {
	log.Printf("error: %v", err)
	fail(w, http.StatusInternalServerError, "Something went wrong on the server.")
}

func readJSON(w http.ResponseWriter, r *http.Request, limit int64, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit)).Decode(v); err != nil {
		fail(w, http.StatusBadRequest, "Invalid JSON, or the request is too large.")
		return false
	}
	return true
}

func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	return err == nil && u.Host == r.Host
}

// guard refuses writes that a foreign page or a plain HTML form could send.
func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if !sameOrigin(r) {
				fail(w, http.StatusForbidden, "Cross-origin write refused.")
				return
			}
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				fail(w, http.StatusUnsupportedMediaType, "Expected JSON.")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// authed wraps a handler that needs a signed-in user, or an admin.
func (a *app) authed(admin bool, h func(http.ResponseWriter, *http.Request, *user)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := a.currentUser(r)
		if u == nil {
			fail(w, http.StatusUnauthorized, "Sign in first.")
			return
		}
		if admin && u.Role != "admin" {
			fail(w, http.StatusForbidden, "Admins only.")
			return
		}
		h(w, r, u)
	}
}

/* ---------- flags ---------- */

func (a *app) flags() (map[string]bool, error) {
	out := map[string]bool{}
	for k, v := range flagDefaults {
		out[k] = v
	}
	rows, err := a.db.Query(`SELECT name, enabled FROM flags`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var on bool
		if err := rows.Scan(&name, &on); err != nil {
			return nil, err
		}
		if _, ok := flagDefaults[name]; ok {
			out[name] = on
		}
	}
	return out, rows.Err()
}

func (a *app) setFlags(in map[string]bool) error {
	for k, v := range in {
		if _, err := a.db.Exec(`INSERT INTO flags (name, enabled) VALUES ($1, $2)
			ON CONFLICT (name) DO UPDATE SET enabled = EXCLUDED.enabled`, k, v); err != nil {
			return err
		}
	}
	return nil
}

func (a *app) handlePutFlags(w http.ResponseWriter, r *http.Request, _ *user) {
	var in struct {
		Flags map[string]bool `json:"flags"`
	}
	if !readJSON(w, r, 4096, &in) {
		return
	}
	for k := range in.Flags {
		if _, ok := flagDefaults[k]; !ok {
			fail(w, http.StatusBadRequest, "Unknown flag: "+k)
			return
		}
	}
	if err := a.setFlags(in.Flags); err != nil {
		a.serverError(w, err)
		return
	}
	f, err := a.flags()
	if err != nil {
		a.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"flags": f})
}

/* ---------- accounts ---------- */

func (a *app) handleMe(w http.ResponseWriter, r *http.Request) {
	f, err := a.flags()
	if err != nil {
		a.serverError(w, err)
		return
	}
	u := a.currentUser(r)
	var ach []achievement
	if u != nil {
		if ach, err = a.achievementsOf(u.ID); err != nil {
			a.serverError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": u, "flags": f, "props": knownProps, "achievements": ach})
}

type credentials struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	DisplayName string `json:"displayName"`
}

func validPassword(w http.ResponseWriter, pw string) bool {
	if len(pw) < 8 || len(pw) > 200 {
		fail(w, http.StatusBadRequest, "Use a password of at least 8 characters.")
		return false
	}
	return true
}

func (a *app) handleSignup(w http.ResponseWriter, r *http.Request) {
	f, err := a.flags()
	if err != nil {
		a.serverError(w, err)
		return
	}
	if !f["signups"] {
		fail(w, http.StatusForbidden, "New sign-ups are turned off. Ask the admin for an account.")
		return
	}
	var in credentials
	if !readJSON(w, r, 4096, &in) {
		return
	}
	name := strings.ToLower(strings.TrimSpace(in.Username))
	if !usernameRE.MatchString(name) {
		fail(w, http.StatusBadRequest, "Usernames are 3 to 24 characters: letters, digits, dot, dash or underscore.")
		return
	}
	if !validPassword(w, in.Password) {
		return
	}
	display := strings.TrimSpace(in.DisplayName)
	if display == "" {
		display = name
	}
	if len([]rune(display)) > 40 {
		fail(w, http.StatusBadRequest, "Keep the display name under 40 characters.")
		return
	}
	u, err := scanUser(a.db.QueryRow(`INSERT INTO users AS u (username, display_name, pw_hash) VALUES ($1, $2, $3) RETURNING `+userCols,
		name, display, hashPassword(in.Password)))
	var pe *pq.Error
	if errors.As(err, &pe) && pe.Code == "23505" {
		fail(w, http.StatusConflict, "That username is taken.")
		return
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	if err := a.startSession(w, r, u.ID); err != nil {
		a.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": u})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// blocked reports whether this address has failed too often for this username.
func (a *app) blocked(key string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	f := a.fails[key]
	return f != nil && time.Now().Before(f.reset) && f.n >= maxFails
}

func (a *app) noteFail(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	for k, f := range a.fails {
		if now.After(f.reset) {
			delete(a.fails, k)
		}
	}
	f := a.fails[key]
	if f == nil {
		f = &failCount{reset: now.Add(failWindow)}
		a.fails[key] = f
	}
	f.n++
}

func (a *app) handleSignin(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if !readJSON(w, r, 4096, &in) {
		return
	}
	name := strings.ToLower(strings.TrimSpace(in.Username))
	key := clientIP(r) + "|" + name
	if a.blocked(key) {
		fail(w, http.StatusTooManyRequests, "Too many failed attempts. Wait ten minutes and try again.")
		return
	}
	var hash string
	u, err := scanUser(a.db.QueryRow(`SELECT `+userCols+`, u.pw_hash FROM users u WHERE u.username = $1`, name), &hash)
	if errors.Is(err, sql.ErrNoRows) {
		checkPassword(dummyHash, in.Password)
	} else if err != nil {
		a.serverError(w, err)
		return
	}
	if err != nil || !checkPassword(hash, in.Password) {
		a.noteFail(key)
		fail(w, http.StatusUnauthorized, "Wrong username or password.")
		return
	}
	if err := a.startSession(w, r, u.ID); err != nil {
		a.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": u})
}

func (a *app) handleSignout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		a.db.Exec(`DELETE FROM sessions WHERE token_hash = $1`, tokenHash(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *app) handlePutMe(w http.ResponseWriter, r *http.Request, u *user) {
	var in struct {
		DisplayName *string   `json:"displayName"`
		Props       *[]string `json:"props"`
		Seen        *[]string `json:"seen"`
		Avatar      *string   `json:"avatar"`
		Bio         *string   `json:"bio"`
		// Social links, as handles or pasted URLs; see cleanLink.
		Links *map[string]string `json:"links"`
		// Preferences: autoPost (share milestones on the timeline).
		Prefs *map[string]bool `json:"prefs"`
		// Level, streak and totals the client worked out, shown on profiles.
		Stats json.RawMessage `json:"stats"`
	}
	if !readJSON(w, r, 200<<10, &in) {
		return
	}
	if in.DisplayName != nil {
		d := strings.TrimSpace(*in.DisplayName)
		if d == "" || len([]rune(d)) > 40 {
			fail(w, http.StatusBadRequest, "The display name needs 1 to 40 characters.")
			return
		}
		u.DisplayName = d
	}
	if in.Props != nil {
		if len(*in.Props) == 0 {
			fail(w, http.StatusBadRequest, "Pick at least one prop.")
			return
		}
		for _, p := range *in.Props {
			if !slices.Contains(knownProps, p) {
				fail(w, http.StatusBadRequest, "Unknown prop: "+p)
				return
			}
		}
		u.Props, _ = json.Marshal(*in.Props)
		u.Onboarded = true
	}
	if in.Seen != nil {
		if len(*in.Seen) > 40 {
			fail(w, http.StatusBadRequest, "Too many entries.")
			return
		}
		for _, s := range *in.Seen {
			if len(s) > 40 {
				fail(w, http.StatusBadRequest, "Entry too long.")
				return
			}
		}
		u.Seen, _ = json.Marshal(*in.Seen)
	}
	if in.Avatar != nil {
		v := *in.Avatar
		ok := v == "" || strings.HasPrefix(v, "data:image/jpeg;base64,") || strings.HasPrefix(v, "data:image/png;base64,")
		if !ok || len(v) > 120000 {
			fail(w, http.StatusBadRequest, "The picture must be a JPEG or PNG under 90 KB.")
			return
		}
		u.Avatar = v
	}
	if in.Bio != nil {
		b := strings.TrimSpace(*in.Bio)
		if len([]rune(b)) > maxBio {
			fail(w, http.StatusBadRequest, "Keep the bio under 300 characters.")
			return
		}
		u.Bio = b
	}
	if in.Links != nil {
		links := map[string]string{}
		json.Unmarshal(u.Links, &links)
		for _, k := range linkKinds {
			v, ok := (*in.Links)[k]
			if !ok {
				continue
			}
			h, good := cleanLink(k, v)
			if !good {
				fail(w, http.StatusBadRequest, "That does not look like a "+k+" handle or link.")
				return
			}
			if h == "" {
				delete(links, k)
			} else {
				links[k] = h
			}
		}
		u.Links, _ = json.Marshal(links)
	}
	if in.Prefs != nil {
		prefs := map[string]bool{}
		json.Unmarshal(u.Prefs, &prefs)
		for k, v := range *in.Prefs {
			if k != "autoPost" {
				fail(w, http.StatusBadRequest, "Unknown preference: "+k)
				return
			}
			prefs[k] = v
		}
		u.Prefs, _ = json.Marshal(prefs)
	}
	if len(in.Stats) > 0 {
		if !validStats(in.Stats) {
			fail(w, http.StatusBadRequest, "Stats must be a small object of numbers.")
			return
		}
		u.Stats = in.Stats
	}
	_, err := a.db.Exec(`UPDATE users SET display_name = $2, props = $3, seen = $4, onboarded = $5, avatar = $6, bio = $7, links = $8, prefs = $9, stats = $10 WHERE id = $1`,
		u.ID, u.DisplayName, string(u.Props), string(u.Seen), u.Onboarded, u.Avatar, u.Bio, string(u.Links), string(u.Prefs), string(u.Stats))
	if err != nil {
		a.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": u})
}

func (a *app) handlePassword(w http.ResponseWriter, r *http.Request, u *user) {
	var in struct {
		Current string `json:"current"`
		Next    string `json:"next"`
	}
	if !readJSON(w, r, 4096, &in) {
		return
	}
	var hash string
	if err := a.db.QueryRow(`SELECT pw_hash FROM users WHERE id = $1`, u.ID).Scan(&hash); err != nil {
		a.serverError(w, err)
		return
	}
	if !checkPassword(hash, in.Current) {
		fail(w, http.StatusForbidden, "The current password is wrong.")
		return
	}
	if !validPassword(w, in.Next) {
		return
	}
	// Changing the password signs out every other device.
	keep := ""
	if c, err := r.Cookie(cookieName); err == nil {
		keep = tokenHash(c.Value)
	}
	if _, err := a.db.Exec(`UPDATE users SET pw_hash = $2 WHERE id = $1`, u.ID, hashPassword(in.Next)); err != nil {
		a.serverError(w, err)
		return
	}
	a.db.Exec(`DELETE FROM sessions WHERE user_id = $1 AND token_hash <> $2`, u.ID, keep)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

/* ---------- progress ---------- */

type document struct {
	Rev   int64           `json:"rev"`
	State json.RawMessage `json:"state"`
}

func (a *app) getProgress(id int64, prop string) (document, error) {
	doc := document{State: json.RawMessage("{}")}
	var state []byte
	err := a.db.QueryRow(`SELECT rev, state FROM progress WHERE user_id = $1 AND prop = $2`, id, prop).Scan(&doc.Rev, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return doc, nil
	}
	doc.State = state
	return doc, err
}

func propOf(w http.ResponseWriter, r *http.Request) (string, bool) {
	p := r.PathValue("prop")
	if !slices.Contains(knownProps, p) {
		fail(w, http.StatusNotFound, "Unknown prop.")
		return "", false
	}
	return p, true
}

func (a *app) handleGetProgress(w http.ResponseWriter, r *http.Request, u *user) {
	prop, ok := propOf(w, r)
	if !ok {
		return
	}
	doc, err := a.getProgress(u.ID, prop)
	if err != nil {
		a.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

func (a *app) handlePutProgress(w http.ResponseWriter, r *http.Request, u *user) {
	prop, ok := propOf(w, r)
	if !ok {
		return
	}
	var in document
	if !readJSON(w, r, maxBody, &in) {
		return
	}
	if t := strings.TrimSpace(string(in.State)); t == "" || t[0] != '{' {
		fail(w, http.StatusBadRequest, "State must be an object.")
		return
	}
	a.backup()
	var rev int64
	var err error
	if in.Rev == 0 {
		err = a.db.QueryRow(`INSERT INTO progress (user_id, prop, rev, state) VALUES ($1, $2, 1, $3)
			ON CONFLICT DO NOTHING RETURNING rev`, u.ID, prop, string(in.State)).Scan(&rev)
	} else {
		err = a.db.QueryRow(`UPDATE progress SET rev = rev + 1, state = $3, updated_at = now()
			WHERE user_id = $1 AND prop = $2 AND rev = $4 RETURNING rev`, u.ID, prop, string(in.State), in.Rev).Scan(&rev)
	}
	if errors.Is(err, sql.ErrNoRows) {
		// Someone else saved first: hand back the current copy.
		doc, err := a.getProgress(u.ID, prop)
		if err != nil {
			a.serverError(w, err)
			return
		}
		writeJSON(w, http.StatusConflict, doc)
		return
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"rev": rev})
}

// write replaces the file atomically, so a power cut leaves the old or the new copy.
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// backup writes everyone's progress to a file once per day, before the day's first change.
func (a *app) backup() {
	a.mu.Lock()
	defer a.mu.Unlock()
	dir := filepath.Join(a.dataDir, "backups")
	day := filepath.Join(dir, "progress-"+time.Now().Format("2006-01-02")+".json")
	if _, err := os.Stat(day); err == nil {
		return
	}
	rows, err := a.db.Query(`SELECT u.username, p.prop, p.rev, p.state FROM progress p JOIN users u ON u.id = p.user_id ORDER BY 1, 2`)
	if err != nil {
		log.Printf("backup: %v", err)
		return
	}
	defer rows.Close()
	type entry struct {
		Username string          `json:"username"`
		Prop     string          `json:"prop"`
		Rev      int64           `json:"rev"`
		State    json.RawMessage `json:"state"`
	}
	all := []entry{}
	for rows.Next() {
		var e entry
		var state []byte
		if err := rows.Scan(&e.Username, &e.Prop, &e.Rev, &state); err != nil {
			log.Printf("backup: %v", err)
			return
		}
		e.State = state
		all = append(all, e)
	}
	b, _ := json.Marshal(all)
	if err := writeAtomic(day, b); err != nil {
		log.Printf("backup: %v", err)
		return
	}
	old, _ := filepath.Glob(filepath.Join(dir, "progress-*.json"))
	sort.Strings(old)
	for len(old) > keepBackups {
		os.Remove(old[0])
		old = old[1:]
	}
}

/* ---------- admin ---------- */

func (a *app) handleUsers(w http.ResponseWriter, _ *http.Request, _ *user) {
	rows, err := a.db.Query(`SELECT ` + userCols + ` FROM users u ORDER BY u.id`)
	if err != nil {
		a.serverError(w, err)
		return
	}
	defer rows.Close()
	out := []*user{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			a.serverError(w, err)
			return
		}
		out = append(out, u)
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

func (a *app) handleDeleteUser(w http.ResponseWriter, r *http.Request, u *user) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if id == u.ID {
		fail(w, http.StatusBadRequest, "You cannot delete your own account here.")
		return
	}
	a.backup()
	res, err := a.db.Exec(`DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		a.serverError(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		fail(w, http.StatusNotFound, "No such user.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *app) handleResetPassword(w http.ResponseWriter, r *http.Request, _ *user) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var in struct {
		Next string `json:"next"`
	}
	if !readJSON(w, r, 4096, &in) || !validPassword(w, in.Next) {
		return
	}
	res, err := a.db.Exec(`UPDATE users SET pw_hash = $2 WHERE id = $1`, id, hashPassword(in.Next))
	if err != nil {
		a.serverError(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		fail(w, http.StatusNotFound, "No such user.")
		return
	}
	a.db.Exec(`DELETE FROM sessions WHERE user_id = $1`, id)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

/* ---------- start-up ---------- */

func openDB(dsn string) (*sql.DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	// The database container may still be starting.
	for i := 0; ; i++ {
		if err = db.Ping(); err == nil {
			break
		}
		if i >= 60 {
			return nil, fmt.Errorf("database not reachable: %w", err)
		}
		time.Sleep(time.Second)
	}
	if _, err = db.Exec(schema); err != nil {
		return nil, err
	}
	_, err = db.Exec(schemaSocial)
	return db, err
}

// seedAdmin creates the admin account once. It never resets an existing password.
func (a *app) seedAdmin(name, pw string) (int64, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || pw == "" {
		return 0, nil
	}
	var id int64
	err := a.db.QueryRow(`SELECT id FROM users WHERE username = $1`, name).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		err = a.db.QueryRow(`INSERT INTO users (username, display_name, pw_hash, role) VALUES ($1, $2, $3, 'admin') RETURNING id`,
			name, "Admin", hashPassword(pw)).Scan(&id)
		if err == nil {
			log.Printf("created admin account %q", name)
		}
	}
	return id, err
}

// importLegacy moves the single-user files from before accounts existed into the
// admin's account, once. The files are renamed, not deleted.
func (a *app) importLegacy(adminID int64) {
	if adminID == 0 {
		return
	}
	state := filepath.Join(a.dataDir, "state.json")
	if b, err := os.ReadFile(state); err == nil {
		var doc document
		if json.Unmarshal(b, &doc) == nil && len(doc.State) > 2 {
			res, err := a.db.Exec(`INSERT INTO progress (user_id, prop, rev, state) VALUES ($1, 'nunchaku', $2, $3) ON CONFLICT DO NOTHING`,
				adminID, max(doc.Rev, 1), string(doc.State))
			if err != nil {
				log.Printf("import: %v", err)
				return
			}
			if n, _ := res.RowsAffected(); n == 1 {
				log.Printf("imported earlier progress into the admin account")
			}
		}
		os.Rename(state, state+".imported")
	}
	flagsFile := filepath.Join(a.dataDir, "flags.json")
	if b, err := os.ReadFile(flagsFile); err == nil {
		old := map[string]bool{}
		if json.Unmarshal(b, &old) == nil {
			keep := map[string]bool{}
			for k, v := range old {
				if _, ok := flagDefaults[k]; ok {
					keep[k] = v
				}
			}
			if err := a.setFlags(keep); err != nil {
				log.Printf("import: %v", err)
				return
			}
		}
		os.Rename(flagsFile, flagsFile+".imported")
	}
}

type asset struct {
	body  []byte
	ctype string
	etag  string
}

func loadAssets() (map[string]asset, error) {
	out := map[string]asset{}
	err := fs.WalkDir(webFS, "web", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := webFS.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		ct := mime.TypeByExtension(filepath.Ext(p))
		if ct == "" {
			ct = http.DetectContentType(b)
		}
		out["/"+strings.TrimPrefix(p, "web/")] = asset{b, ct, `"` + hex.EncodeToString(sum[:8]) + `"`}
		return nil
	})
	return out, err
}

func handleStatic(assets map[string]asset) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == "/" {
			p = "/index.html"
		}
		a, ok := assets[p]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", a.ctype)
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("ETag", a.etag)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; object-src 'none'; base-uri 'none'; form-action 'self'")
		if r.Header.Get("If-None-Match") == a.etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Write(a.body)
	}
}

// routes builds the HTTP handler: every API route, the static files, and the write guard.
func (a *app) routes(assets map[string]asset) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		if err := a.db.Ping(); err != nil {
			http.Error(w, "database down", http.StatusServiceUnavailable)
			return
		}
		io.WriteString(w, "ok\n")
	})
	mux.HandleFunc("GET /api/me", a.handleMe)
	mux.HandleFunc("PUT /api/me", a.authed(false, a.handlePutMe))
	mux.HandleFunc("PUT /api/me/password", a.authed(false, a.handlePassword))
	mux.HandleFunc("POST /api/signup", a.handleSignup)
	mux.HandleFunc("POST /api/signin", a.handleSignin)
	mux.HandleFunc("POST /api/signout", a.handleSignout)
	mux.HandleFunc("GET /api/progress/{prop}", a.authed(false, a.handleGetProgress))
	mux.HandleFunc("PUT /api/progress/{prop}", a.authed(false, a.handlePutProgress))
	mux.HandleFunc("PUT /api/flags", a.authed(true, a.handlePutFlags))
	mux.HandleFunc("GET /api/admin/users", a.authed(true, a.handleUsers))
	mux.HandleFunc("DELETE /api/admin/users/{id}", a.authed(true, a.handleDeleteUser))
	mux.HandleFunc("PUT /api/admin/users/{id}/password", a.authed(true, a.handleResetPassword))
	a.socialRoutes(mux)
	mux.HandleFunc("GET /", handleStatic(assets))
	return guard(mux)
}

func main() {
	listen := flag.String("listen", ":3169", "listen address")
	dataDir := flag.String("data", "/data", "directory for backups")
	health := flag.Bool("healthcheck", false, "probe the local server and exit")
	flag.Parse()

	if *health {
		c := http.Client{Timeout: 3 * time.Second}
		resp, err := c.Get("http://127.0.0.1" + *listen + "/healthz")
		if err != nil || resp.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		io.Copy(io.Discard, resp.Body)
		return
	}

	mime.AddExtensionType(".webmanifest", "application/manifest+json")
	mime.AddExtensionType(".js", "text/javascript; charset=utf-8")
	mime.AddExtensionType(".css", "text/css; charset=utf-8")
	mime.AddExtensionType(".svg", "image/svg+xml")

	if err := os.MkdirAll(filepath.Join(*dataDir, "backups"), 0o755); err != nil {
		log.Fatal(err)
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is not set")
	}
	db, err := openDB(dsn)
	if err != nil {
		log.Fatal(err)
	}
	a := &app{db: db, dataDir: *dataDir, fails: map[string]*failCount{}}
	adminID, err := a.seedAdmin(os.Getenv("ADMIN_USER"), os.Getenv("ADMIN_PASSWORD"))
	if err != nil {
		log.Fatal(err)
	}
	a.importLegacy(adminID)
	db.Exec(`DELETE FROM sessions WHERE expires_at < now()`)

	assets, err := loadAssets()
	if err != nil {
		log.Fatal(err)
	}

	srv := &http.Server{Addr: *listen, Handler: a.routes(assets), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 120 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(c)
	}()
	log.Printf("listening on %s", *listen)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

/* ---------- community ----------

Community features: profiles with a bio and social links, a timeline of posts with
likes and comments, notifications, and achievements. Progress stays a client-owned
document, so achievements and the stats shown on profiles are reported by the client
and only recorded here. */

const schemaSocial = `
ALTER TABLE users ADD COLUMN IF NOT EXISTS bio TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS links JSONB NOT NULL DEFAULT '{}';
ALTER TABLE users ADD COLUMN IF NOT EXISTS prefs JSONB NOT NULL DEFAULT '{}';
ALTER TABLE users ADD COLUMN IF NOT EXISTS stats JSONB NOT NULL DEFAULT '{}';
CREATE TABLE IF NOT EXISTS posts (
	id BIGSERIAL PRIMARY KEY,
	user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	kind TEXT NOT NULL DEFAULT 'post',
	body TEXT NOT NULL DEFAULT '',
	prop TEXT NOT NULL DEFAULT '',
	skill TEXT NOT NULL DEFAULT '',
	image TEXT NOT NULL DEFAULT '',
	data JSONB NOT NULL DEFAULT '{}',
	created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS posts_user ON posts (user_id, id DESC);
CREATE TABLE IF NOT EXISTS likes (
	post_id BIGINT NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
	user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	PRIMARY KEY (post_id, user_id)
);
CREATE TABLE IF NOT EXISTS comments (
	id BIGSERIAL PRIMARY KEY,
	post_id BIGINT NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
	user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	body TEXT NOT NULL,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS comments_post ON comments (post_id, id);
CREATE TABLE IF NOT EXISTS achievements (
	user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	key TEXT NOT NULL,
	unlocked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	PRIMARY KEY (user_id, key)
);
CREATE TABLE IF NOT EXISTS notifications (
	id BIGSERIAL PRIMARY KEY,
	user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	actor_id BIGINT REFERENCES users(id) ON DELETE CASCADE,
	kind TEXT NOT NULL,
	post_id BIGINT REFERENCES posts(id) ON DELETE CASCADE,
	text TEXT NOT NULL DEFAULT '',
	read BOOLEAN NOT NULL DEFAULT false,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS notifications_user ON notifications (user_id, id DESC);
ALTER TABLE comments ADD COLUMN IF NOT EXISTS parent_id BIGINT REFERENCES comments(id) ON DELETE CASCADE;
ALTER TABLE comments ADD COLUMN IF NOT EXISTS deleted BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE comments ADD COLUMN IF NOT EXISTS edited_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS comments_parent ON comments (parent_id, id);
CREATE TABLE IF NOT EXISTS comment_likes (
	comment_id BIGINT NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
	user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	PRIMARY KEY (comment_id, user_id)
);
ALTER TABLE notifications ADD COLUMN IF NOT EXISTS comment_id BIGINT REFERENCES comments(id) ON DELETE CASCADE;`

const (
	maxBio       = 300
	maxPost      = 2000
	maxComment   = 1000
	maxImage     = 400000 // bytes of data URL; the client sends a resized JPEG
	pageSize     = 20
	maxAchKeys   = 60
	maxNotifList = 40
	maxTags      = 10
	// Comments: how many the feed carries per post and per thread, page sizes, and the edit window.
	previewComments = 2
	previewReplies  = 2
	commentPage     = 20
	replyPage       = 50
	maxThread       = 500
	editWindow      = 15 * time.Minute
)

// Social networks a profile can link to. The value stored is the handle, not a URL.
var linkKinds = []string{"instagram", "tiktok", "youtube"}

var (
	handleRE   = regexp.MustCompile(`^[A-Za-z0-9._-]{1,60}$`)
	ytHandleRE = regexp.MustCompile(`^[A-Za-z0-9._@/-]{1,80}$`)
	achKeyRE   = regexp.MustCompile(`^[a-z0-9][a-z0-9:_-]{0,60}$`)
	mentionRE  = regexp.MustCompile(`(?:^|[^a-z0-9._-])@([a-z0-9][a-z0-9._-]{2,23})`)
)

// cleanLink turns a pasted URL or handle into a bare handle, or reports it as invalid.
// An empty value is valid and removes the link.
func cleanLink(kind, v string) (string, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", true
	}
	low := strings.ToLower(v)
	for _, p := range []string{"https://", "http://"} {
		if strings.HasPrefix(low, p) {
			v, low = v[len(p):], low[len(p):]
			break
		}
	}
	for _, p := range []string{"www.", "m.", "vm."} {
		if strings.HasPrefix(low, p) {
			v, low = v[len(p):], low[len(p):]
			break
		}
	}
	for _, d := range []string{"instagram.com/", "tiktok.com/", "youtube.com/"} {
		if strings.HasPrefix(low, d) {
			v = v[len(d):]
			break
		}
	}
	if i := strings.IndexAny(v, "?#"); i >= 0 {
		v = v[:i]
	}
	v = strings.Trim(v, "/")
	v = strings.TrimPrefix(v, "@")
	if kind == "youtube" {
		// A channel or handle, not a video, short or playlist link.
		low := strings.ToLower(v)
		for _, p := range []string{"watch", "shorts/", "playlist", "embed/", "youtu.be"} {
			if strings.HasPrefix(low, p) {
				return "", false
			}
		}
		return v, ytHandleRE.MatchString(v)
	}
	return v, handleRE.MatchString(v)
}

// mentions lists the usernames written as @name in a text, without repeats.
func mentions(text string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, m := range mentionRE.FindAllStringSubmatch(strings.ToLower(text), -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

// validStats accepts a small flat object of numbers and booleans, as the client reports
// its level, streak and totals for the profile and the people list.
func validStats(raw json.RawMessage) bool {
	if len(raw) > 2000 {
		return false
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil || len(m) > 40 {
		return false
	}
	for k, v := range m {
		if len(k) > 30 {
			return false
		}
		switch v.(type) {
		case float64, bool:
		default:
			return false
		}
	}
	return true
}

func validImage(v string) bool {
	return v == "" || ((strings.HasPrefix(v, "data:image/jpeg;base64,") || strings.HasPrefix(v, "data:image/png;base64,")) && len(v) <= maxImage)
}

/* ---------- public view of a user ---------- */

type pubUser struct {
	ID          int64           `json:"id"`
	Username    string          `json:"username"`
	DisplayName string          `json:"displayName"`
	Role        string          `json:"role"`
	Avatar      string          `json:"avatar"`
	Bio         string          `json:"bio"`
	Links       json.RawMessage `json:"links"`
	Props       json.RawMessage `json:"props"`
	Stats       json.RawMessage `json:"stats"`
	Created     time.Time       `json:"created"`
}

func (u *user) public() pubUser {
	return pubUser{u.ID, u.Username, u.DisplayName, u.Role, u.Avatar, u.Bio, u.Links, u.Props, u.Stats, u.Created}
}

func (a *app) usersByID(ids []int64) (map[int64]pubUser, error) {
	out := map[int64]pubUser{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := a.db.Query(`SELECT `+userCols+` FROM users u WHERE u.id = ANY($1)`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out[u.ID] = u.public()
	}
	return out, rows.Err()
}

func statNum(raw json.RawMessage, key string) float64 {
	var m map[string]any
	json.Unmarshal(raw, &m)
	f, _ := m[key].(float64)
	return f
}

// handleUserList is the people list: everyone, with the stats they reported, most XP first.
func (a *app) handleUserList(w http.ResponseWriter, _ *http.Request, _ *user) {
	rows, err := a.db.Query(`SELECT ` + userCols + ` FROM users u ORDER BY u.id`)
	if err != nil {
		a.serverError(w, err)
		return
	}
	defer rows.Close()
	out := []pubUser{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			a.serverError(w, err)
			return
		}
		out = append(out, u.public())
	}
	sort.SliceStable(out, func(i, j int) bool { return statNum(out[i].Stats, "xp") > statNum(out[j].Stats, "xp") })
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

func (a *app) handleUserProfile(w http.ResponseWriter, r *http.Request, _ *user) {
	name := strings.ToLower(r.PathValue("username"))
	u, err := scanUser(a.db.QueryRow(`SELECT `+userCols+` FROM users u WHERE u.username = $1`, name))
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "No such user.")
		return
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	ach, err := a.achievementsOf(u.ID)
	if err != nil {
		a.serverError(w, err)
		return
	}
	var posts, likes int
	if err := a.db.QueryRow(`SELECT count(*) FROM posts WHERE user_id = $1`, u.ID).Scan(&posts); err != nil {
		a.serverError(w, err)
		return
	}
	if err := a.db.QueryRow(`SELECT count(*) FROM likes l JOIN posts p ON p.id = l.post_id WHERE p.user_id = $1`, u.ID).Scan(&likes); err != nil {
		a.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": u.public(), "achievements": ach, "posts": posts, "likes": likes})
}

/* ---------- achievements ---------- */

type achievement struct {
	Key string    `json:"key"`
	At  time.Time `json:"at"`
}

func (a *app) achievementsOf(id int64) ([]achievement, error) {
	rows, err := a.db.Query(`SELECT key, unlocked_at FROM achievements WHERE user_id = $1 ORDER BY unlocked_at, key`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []achievement{}
	for rows.Next() {
		var x achievement
		if err := rows.Scan(&x.Key, &x.At); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// handleAchievements records the achievements the client found unlocked. New ones get a
// notification, and one milestone post for the batch unless the user turned that off.
func (a *app) handleAchievements(w http.ResponseWriter, r *http.Request, u *user) {
	var in struct {
		Keys []string `json:"keys"`
		Prop string   `json:"prop"`
		Post *bool    `json:"post"` // false for badges earned by posting or sharing, which need no milestone
	}
	if !readJSON(w, r, 8192, &in) {
		return
	}
	if len(in.Keys) > maxAchKeys {
		fail(w, http.StatusBadRequest, "Too many keys at once.")
		return
	}
	fresh := []string{}
	for _, k := range in.Keys {
		if !achKeyRE.MatchString(k) {
			fail(w, http.StatusBadRequest, "Bad achievement key: "+k)
			return
		}
		var got string
		err := a.db.QueryRow(`INSERT INTO achievements (user_id, key) VALUES ($1, $2) ON CONFLICT DO NOTHING RETURNING key`, u.ID, k).Scan(&got)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			a.serverError(w, err)
			return
		}
		fresh = append(fresh, got)
		a.notify(u.ID, 0, "achievement", 0, got)
	}
	var postID int64
	if len(fresh) > 0 && (in.Post == nil || *in.Post) && a.pref(u, "autoPost", true) {
		data, _ := json.Marshal(map[string]any{"achievements": fresh})
		prop := ""
		for _, p := range knownProps {
			if p == in.Prop {
				prop = p
			}
		}
		if err := a.db.QueryRow(`INSERT INTO posts (user_id, kind, prop, data) VALUES ($1, 'milestone', $2, $3) RETURNING id`, u.ID, prop, string(data)).Scan(&postID); err != nil {
			a.serverError(w, err)
			return
		}
	}
	all, err := a.achievementsOf(u.ID)
	if err != nil {
		a.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"new": fresh, "all": all, "postId": postID})
}

func (a *app) pref(u *user, key string, def bool) bool {
	var m map[string]any
	json.Unmarshal(u.Prefs, &m)
	if v, ok := m[key].(bool); ok {
		return v
	}
	return def
}

/* ---------- notifications ---------- */

type notification struct {
	ID      int64  `json:"id"`
	Kind    string `json:"kind"`
	ActorID int64  `json:"actorId"`
	PostID  int64  `json:"postId"`
	// CommentID is the comment the event is about; RootID is the top of its thread.
	CommentID int64     `json:"commentId,omitempty"`
	RootID    int64     `json:"rootId,omitempty"`
	Text      string    `json:"text"`
	Read      bool      `json:"read"`
	Created   time.Time `json:"created"`
}

// notify records an event for a user. Nothing is recorded for one's own actions.
func (a *app) notify(userID, actorID int64, kind string, postID int64, text string) {
	a.notifyAt(userID, actorID, kind, postID, 0, text)
}

// notifyAt records an event about a comment, so the notification can open that comment.
func (a *app) notifyAt(userID, actorID int64, kind string, postID, commentID int64, text string) {
	if userID == 0 || userID == actorID {
		return
	}
	var actor, post, cmt any
	if actorID != 0 {
		actor = actorID
	}
	if postID != 0 {
		post = postID
	}
	if commentID != 0 {
		cmt = commentID
	}
	if _, err := a.db.Exec(`INSERT INTO notifications (user_id, actor_id, kind, post_id, comment_id, text) VALUES ($1, $2, $3, $4, $5, $6)`, userID, actor, kind, post, cmt, text); err != nil {
		log.Printf("notify: %v", err)
	}
}

func (a *app) handleNotifications(w http.ResponseWriter, _ *http.Request, u *user) {
	rows, err := a.db.Query(`SELECT n.id, n.kind, COALESCE(n.actor_id, 0), COALESCE(n.post_id, 0), COALESCE(n.comment_id, 0),
		COALESCE(c.parent_id, c.id, 0), n.text, n.read, n.created_at
		FROM notifications n LEFT JOIN comments c ON c.id = n.comment_id
		WHERE n.user_id = $1 ORDER BY n.id DESC LIMIT $2`, u.ID, maxNotifList)
	if err != nil {
		a.serverError(w, err)
		return
	}
	defer rows.Close()
	items := []notification{}
	ids := []int64{}
	for rows.Next() {
		var n notification
		if err := rows.Scan(&n.ID, &n.Kind, &n.ActorID, &n.PostID, &n.CommentID, &n.RootID, &n.Text, &n.Read, &n.Created); err != nil {
			a.serverError(w, err)
			return
		}
		items = append(items, n)
		if n.ActorID != 0 {
			ids = append(ids, n.ActorID)
		}
	}
	if err := rows.Err(); err != nil {
		a.serverError(w, err)
		return
	}
	var unread int
	if err := a.db.QueryRow(`SELECT count(*) FROM notifications WHERE user_id = $1 AND NOT read`, u.ID).Scan(&unread); err != nil {
		a.serverError(w, err)
		return
	}
	users, err := a.usersByID(ids)
	if err != nil {
		a.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "unread": unread, "users": users})
}

func (a *app) handleReadNotifications(w http.ResponseWriter, r *http.Request, u *user) {
	var in struct {
		IDs []int64 `json:"ids"`
	}
	if !readJSON(w, r, 8192, &in) {
		return
	}
	var err error
	if len(in.IDs) == 0 {
		_, err = a.db.Exec(`UPDATE notifications SET read = true WHERE user_id = $1 AND NOT read`, u.ID)
	} else {
		_, err = a.db.Exec(`UPDATE notifications SET read = true WHERE user_id = $1 AND id = ANY($2)`, u.ID, pq.Array(in.IDs))
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

/* ---------- posts, likes and comments ---------- */

// A comment, or a reply when ParentID is set. Threads are one level deep: a reply's parent is
// always a top-level comment. A deleted comment that still has replies is kept, with no text.
type comment struct {
	ID         int64     `json:"id"`
	PostID     int64     `json:"postId"`
	UserID     int64     `json:"userId"`
	ParentID   int64     `json:"parentId,omitempty"`
	Body       string    `json:"body"`
	Created    time.Time `json:"created"`
	Edited     bool      `json:"edited,omitempty"`
	Deleted    bool      `json:"deleted,omitempty"`
	Likes      int       `json:"likes"`
	Liked      bool      `json:"liked"`
	ReplyCount int       `json:"replyCount"`
	Replies    []comment `json:"replies,omitempty"`
}

type post struct {
	ID      int64           `json:"id"`
	UserID  int64           `json:"userId"`
	Kind    string          `json:"kind"`
	Body    string          `json:"body"`
	Prop    string          `json:"prop"`
	Skill   string          `json:"skill"`
	Image   string          `json:"image"`
	Data    json.RawMessage `json:"data"`
	Created time.Time       `json:"created"`
	Likes   int             `json:"likes"`
	Liked   bool            `json:"liked"`
	// Comments holds the newest top-level comments, each with its newest replies; the rest
	// load on demand. CommentCount counts every comment and reply that is not deleted.
	Comments     []comment `json:"comments"`
	CommentCount int       `json:"commentCount"`
}

// loadPosts reads a page of posts newest first, with like counts, whether the viewer
// liked each one, every comment, and the users involved.
func (a *app) loadPosts(viewer, before, byUser, only int64) (map[string]any, error) {
	rows, err := a.db.Query(`SELECT p.id, p.user_id, p.kind, p.body, p.prop, p.skill, p.image, p.data, p.created_at,
		(SELECT count(*) FROM likes l WHERE l.post_id = p.id), EXISTS (SELECT 1 FROM likes l WHERE l.post_id = p.id AND l.user_id = $1),
		(SELECT count(*) FROM comments c WHERE c.post_id = p.id AND NOT c.deleted)
		FROM posts p WHERE ($2 = 0 OR p.id < $2) AND ($3 = 0 OR p.user_id = $3) AND ($4 = 0 OR p.id = $4) ORDER BY p.id DESC LIMIT $5`,
		viewer, before, byUser, only, pageSize+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	posts := []*post{}
	byID := map[int64]*post{}
	ids, uids := []int64{}, []int64{}
	for rows.Next() {
		p := &post{Comments: []comment{}}
		var data []byte
		if err := rows.Scan(&p.ID, &p.UserID, &p.Kind, &p.Body, &p.Prop, &p.Skill, &p.Image, &data, &p.Created, &p.Likes, &p.Liked, &p.CommentCount); err != nil {
			return nil, err
		}
		p.Data = data
		posts = append(posts, p)
		byID[p.ID] = p
		ids = append(ids, p.ID)
		uids = append(uids, p.UserID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	more := len(posts) > pageSize
	if more {
		delete(byID, posts[pageSize].ID)
		posts = posts[:pageSize]
		ids = ids[:pageSize]
	}
	if len(ids) > 0 {
		// The newest top-level comments of each post, shown oldest first.
		roots, err := a.queryComments(viewer, `SELECT * FROM (SELECT c.*, row_number() OVER (PARTITION BY c.post_id ORDER BY c.id DESC) AS rn
			FROM comments c WHERE c.post_id = ANY($2) AND c.parent_id IS NULL) c WHERE c.rn <= $3 ORDER BY c.id`, pq.Array(ids), previewComments)
		if err != nil {
			return nil, err
		}
		if err := a.attachReplies(viewer, roots); err != nil {
			return nil, err
		}
		for _, c := range roots {
			if p := byID[c.PostID]; p != nil {
				p.Comments = append(p.Comments, *c)
			}
			uids = append(uids, commentUsers(*c)...)
		}
	}
	users, err := a.usersByID(uids)
	if err != nil {
		return nil, err
	}
	return map[string]any{"posts": posts, "users": users, "more": more}, nil
}

func queryID(r *http.Request, key string) int64 {
	n, _ := strconv.ParseInt(r.URL.Query().Get(key), 10, 64)
	return n
}

func (a *app) handleGetPosts(w http.ResponseWriter, r *http.Request, u *user) {
	var byUser int64
	if name := strings.ToLower(r.URL.Query().Get("user")); name != "" {
		err := a.db.QueryRow(`SELECT id FROM users WHERE username = $1`, name).Scan(&byUser)
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, http.StatusNotFound, "No such user.")
			return
		}
		if err != nil {
			a.serverError(w, err)
			return
		}
	}
	out, err := a.loadPosts(u.ID, queryID(r, "before"), byUser, 0)
	if err != nil {
		a.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *app) writePost(w http.ResponseWriter, viewer, id int64) {
	out, err := a.loadPosts(viewer, 0, 0, id)
	if err != nil {
		a.serverError(w, err)
		return
	}
	if len(out["posts"].([]*post)) == 0 {
		fail(w, http.StatusNotFound, "No such post.")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *app) handleGetPost(w http.ResponseWriter, r *http.Request, u *user) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	a.writePost(w, u.ID, id)
}

// notifyMentions tells each @named user, except the actor and anyone already told.
func (a *app) notifyMentions(text string, actor, postID, commentID int64, skip ...int64) {
	a.notifyNames(mentions(text), actor, postID, commentID, skip...)
}

// notifyNames tells each named user, except the actor and anyone in skip.
func (a *app) notifyNames(names []string, actor, postID, commentID int64, skip ...int64) {
	if len(names) == 0 {
		return
	}
	rows, err := a.db.Query(`SELECT id FROM users WHERE username = ANY($1)`, pq.Array(names))
	if err != nil {
		log.Printf("mentions: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if rows.Scan(&id) != nil {
			continue
		}
		told := id == actor
		for _, s := range skip {
			told = told || id == s
		}
		if !told {
			a.notifyAt(id, actor, "mention", postID, commentID, "")
		}
	}
}

// A trick tagged on a post, and a combo attached to one. Skill ids are catalog ids such as
// F7; the browser resolves them to names, so a renamed skill still reads right.
type postTag struct {
	Prop  string `json:"prop"`
	Skill string `json:"skill"`
}
type postCombo struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	Role string   `json:"role"`
	Prop string   `json:"prop"`
	Seq  []string `json:"seq"`
	Reps []int    `json:"reps"`
}

var skillIDRE = regexp.MustCompile(`^[A-Za-z0-9]{1,12}$`)

func knownProp(p string) bool { return slices.Contains(knownProps, p) }

// cleanTags drops repeats and anything that is not a known prop and a plausible skill id.
func cleanTags(in []postTag) ([]postTag, bool) {
	out := []postTag{}
	seen := map[string]bool{}
	for _, t := range in {
		t.Skill = strings.TrimSpace(t.Skill)
		if !knownProp(t.Prop) || !skillIDRE.MatchString(t.Skill) {
			return nil, false
		}
		if k := t.Prop + ":" + t.Skill; !seen[k] {
			seen[k] = true
			out = append(out, t)
		}
	}
	return out, len(out) <= maxTags
}

func cleanCombo(c *postCombo) bool {
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" || len([]rune(c.Name)) > 80 || len(c.ID) > 40 || !knownProp(c.Prop) || len(c.Seq) == 0 || len(c.Seq) > 30 {
		return false
	}
	if !slices.Contains([]string{"", "reliable", "signature", "stretch"}, c.Role) {
		return false
	}
	for _, s := range c.Seq {
		if !skillIDRE.MatchString(s) {
			return false
		}
	}
	if len(c.Reps) != 0 && len(c.Reps) != len(c.Seq) {
		return false
	}
	for _, n := range c.Reps {
		if n < 1 || n > 9 {
			return false
		}
	}
	return true
}

func (a *app) handleCreatePost(w http.ResponseWriter, r *http.Request, u *user) {
	var in struct {
		Body  string     `json:"body"`
		Image string     `json:"image"`
		Tags  []postTag  `json:"tags"`
		Combo *postCombo `json:"combo"`
	}
	if !readJSON(w, r, maxImage+4*maxPost+8192, &in) {
		return
	}
	body := strings.TrimSpace(in.Body)
	if len([]rune(body)) > maxPost {
		fail(w, http.StatusBadRequest, "Keep a post under 2000 characters.")
		return
	}
	if !validImage(in.Image) {
		fail(w, http.StatusBadRequest, "The picture must be a JPEG or PNG under 300 KB.")
		return
	}
	tags, ok := cleanTags(in.Tags)
	if !ok {
		fail(w, http.StatusBadRequest, "Tag up to 10 tricks from your props.")
		return
	}
	if in.Combo != nil && !cleanCombo(in.Combo) {
		fail(w, http.StatusBadRequest, "That combo cannot be attached.")
		return
	}
	if body == "" && in.Image == "" && in.Combo == nil {
		fail(w, http.StatusBadRequest, "Write something, add a picture or attach a combo.")
		return
	}
	data := map[string]any{}
	if len(tags) > 0 {
		data["tags"] = tags
	}
	if in.Combo != nil {
		data["combo"] = in.Combo
	}
	dataJSON, _ := json.Marshal(data)
	// The first tag also fills the older prop and skill columns, so earlier clients still read it.
	prop, skill := "", ""
	if len(tags) > 0 {
		prop, skill = tags[0].Prop, tags[0].Skill
	}
	var id int64
	if err := a.db.QueryRow(`INSERT INTO posts (user_id, body, prop, skill, image, data) VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		u.ID, body, prop, skill, in.Image, string(dataJSON)).Scan(&id); err != nil {
		a.serverError(w, err)
		return
	}
	a.notifyMentions(body, u.ID, id, 0)
	a.writePost(w, u.ID, id)
}

func (a *app) handleDeletePost(w http.ResponseWriter, r *http.Request, u *user) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	res, err := a.db.Exec(`DELETE FROM posts WHERE id = $1 AND (user_id = $2 OR $3)`, id, u.ID, u.Role == "admin")
	if err != nil {
		a.serverError(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		fail(w, http.StatusNotFound, "No such post, or it is not yours.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// postOwner finds who wrote a post, answering the request itself when it cannot.
func (a *app) postOwner(w http.ResponseWriter, id int64) (int64, bool) {
	var owner int64
	err := a.db.QueryRow(`SELECT user_id FROM posts WHERE id = $1`, id).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "No such post.")
		return 0, false
	}
	if err != nil {
		a.serverError(w, err)
		return 0, false
	}
	return owner, true
}

func (a *app) handleLike(w http.ResponseWriter, r *http.Request, u *user) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	owner, ok := a.postOwner(w, id)
	if !ok {
		return
	}
	// One statement either way, so two quick clicks cannot race into a duplicate key.
	res, err := a.db.Exec(`INSERT INTO likes (post_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, id, u.ID)
	if err != nil {
		a.serverError(w, err)
		return
	}
	n, _ := res.RowsAffected()
	liked := n == 1
	if liked {
		// Liking, unliking and liking again tells the author once.
		var told bool
		if err := a.db.QueryRow(`SELECT EXISTS (SELECT 1 FROM notifications WHERE user_id = $1 AND actor_id = $2 AND post_id = $3 AND kind = 'like')`, owner, u.ID, id).Scan(&told); err != nil {
			a.serverError(w, err)
			return
		}
		if !told {
			a.notify(owner, u.ID, "like", id, "")
		}
	} else if _, err := a.db.Exec(`DELETE FROM likes WHERE post_id = $1 AND user_id = $2`, id, u.ID); err != nil {
		a.serverError(w, err)
		return
	}
	var likes int
	if err := a.db.QueryRow(`SELECT count(*) FROM likes WHERE post_id = $1`, id).Scan(&likes); err != nil {
		a.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"liked": liked, "likes": likes})
}

/* ---------- comments and replies ---------- */

const commentCols = `c.id, c.post_id, c.user_id, COALESCE(c.parent_id, 0), c.body, c.created_at, c.edited_at IS NOT NULL, c.deleted,
	(SELECT count(*) FROM comment_likes l WHERE l.comment_id = c.id), EXISTS (SELECT 1 FROM comment_likes l WHERE l.comment_id = c.id AND l.user_id = $1),
	(SELECT count(*) FROM comments r WHERE r.parent_id = c.id)`

// queryComments runs inner, a query over the comments table whose first parameter is $2, and
// returns its rows oldest first, with likes and reply counts as seen by viewer ($1).
func (a *app) queryComments(viewer int64, inner string, args ...any) ([]*comment, error) {
	rows, err := a.db.Query(`SELECT `+commentCols+` FROM (`+inner+`) c ORDER BY c.id`, append([]any{viewer}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*comment{}
	for rows.Next() {
		c := &comment{}
		if err := rows.Scan(&c.ID, &c.PostID, &c.UserID, &c.ParentID, &c.Body, &c.Created, &c.Edited, &c.Deleted, &c.Likes, &c.Liked, &c.ReplyCount); err != nil {
			return nil, err
		}
		if c.Deleted {
			c.Body, c.Likes, c.Liked, c.Edited = "", 0, false, false
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// attachReplies gives each top-level comment its newest replies, oldest first.
func (a *app) attachReplies(viewer int64, roots []*comment) error {
	if len(roots) == 0 {
		return nil
	}
	ids, byID := make([]int64, len(roots)), map[int64]*comment{}
	for i, c := range roots {
		ids[i], byID[c.ID] = c.ID, c
	}
	reps, err := a.queryComments(viewer, `SELECT * FROM (SELECT c.*, row_number() OVER (PARTITION BY c.parent_id ORDER BY c.id DESC) AS rn
		FROM comments c WHERE c.parent_id = ANY($2)) c WHERE c.rn <= $3`, pq.Array(ids), previewReplies)
	if err != nil {
		return err
	}
	for _, r := range reps {
		if p := byID[r.ParentID]; p != nil {
			p.Replies = append(p.Replies, *r)
		}
	}
	return nil
}

func commentUsers(c comment) []int64 {
	out := []int64{c.UserID}
	for _, r := range c.Replies {
		out = append(out, r.UserID)
	}
	return out
}

func (a *app) writeComments(w http.ResponseWriter, list []*comment, page int, extra map[string]any) {
	more := len(list) > page
	if more {
		list = list[:page]
	}
	uids := []int64{}
	for _, c := range list {
		uids = append(uids, commentUsers(*c)...)
	}
	users, err := a.usersByID(uids)
	if err != nil {
		a.serverError(w, err)
		return
	}
	out := map[string]any{"comments": list, "users": users, "more": more}
	for k, v := range extra {
		out[k] = v
	}
	writeJSON(w, http.StatusOK, out)
}

// handleGetComments pages through a post's top-level comments, oldest first, each with its
// newest replies.
func (a *app) handleGetComments(w http.ResponseWriter, r *http.Request, u *user) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if _, ok := a.postOwner(w, id); !ok {
		return
	}
	list, err := a.queryComments(u.ID, `SELECT * FROM comments WHERE post_id = $2 AND parent_id IS NULL AND id > $3 ORDER BY id LIMIT $4`, id, queryID(r, "after"), commentPage+1)
	if err == nil {
		err = a.attachReplies(u.ID, list)
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	a.writeComments(w, list, commentPage, nil)
}

// commentRow is what the handlers need to know about one comment and its post.
type commentRow struct {
	postID, userID, parentID, postOwner int64
	deleted                             bool
	age                                 time.Duration
	body                                string
}

func (a *app) commentRow(w http.ResponseWriter, id int64) (commentRow, bool) {
	var c commentRow
	var secs float64
	err := a.db.QueryRow(`SELECT c.post_id, c.user_id, COALESCE(c.parent_id, 0), p.user_id, c.deleted, extract(epoch FROM now() - c.created_at), c.body
		FROM comments c JOIN posts p ON p.id = c.post_id WHERE c.id = $1`, id).Scan(&c.postID, &c.userID, &c.parentID, &c.postOwner, &c.deleted, &secs, &c.body)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "That comment is gone.")
		return c, false
	}
	if err != nil {
		a.serverError(w, err)
		return c, false
	}
	c.age = time.Duration(secs * float64(time.Second))
	return c, true
}

// handleGetReplies pages through the replies to one top-level comment, oldest first.
func (a *app) handleGetReplies(w http.ResponseWriter, r *http.Request, u *user) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	c, ok := a.commentRow(w, id)
	if !ok {
		return
	}
	if c.parentID != 0 {
		id = c.parentID
	}
	list, err := a.queryComments(u.ID, `SELECT * FROM comments WHERE parent_id = $2 AND id > $3 ORDER BY id LIMIT $4`, id, queryID(r, "after"), replyPage+1)
	if err != nil {
		a.serverError(w, err)
		return
	}
	a.writeComments(w, list, replyPage, nil)
}

// handleThread returns the whole thread a comment belongs to, for a notification to open.
func (a *app) handleThread(w http.ResponseWriter, r *http.Request, u *user) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	c, ok := a.commentRow(w, id)
	if !ok {
		return
	}
	root := id
	if c.parentID != 0 {
		root = c.parentID
	}
	roots, err := a.queryComments(u.ID, `SELECT * FROM comments WHERE id = $2`, root)
	if err != nil || len(roots) == 0 {
		a.serverError(w, fmt.Errorf("thread %d: %v", root, err))
		return
	}
	reps, err := a.queryComments(u.ID, `SELECT * FROM comments WHERE parent_id = $2 ORDER BY id LIMIT $3`, root, maxThread)
	if err != nil {
		a.serverError(w, err)
		return
	}
	for _, x := range reps {
		roots[0].Replies = append(roots[0].Replies, *x)
	}
	a.writeComments(w, roots, 1, map[string]any{"postId": c.postID, "focus": id})
}

var leadMentionRE = regexp.MustCompile(`^@([a-z0-9][a-z0-9._-]{2,23})\b`)

// startsWithMention reports whether text opens with @name.
func startsWithMention(text, name string) bool {
	m := leadMentionRE.FindStringSubmatch(strings.ToLower(text))
	return m != nil && m[1] == name
}

func (a *app) handleComment(w http.ResponseWriter, r *http.Request, u *user) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var in struct {
		Body     string `json:"body"`
		ParentID int64  `json:"parentId"`
	}
	if !readJSON(w, r, 8192, &in) {
		return
	}
	body := strings.TrimSpace(in.Body)
	if body == "" || len([]rune(body)) > maxComment {
		fail(w, http.StatusBadRequest, "A comment needs 1 to 1000 characters.")
		return
	}
	owner, ok := a.postOwner(w, id)
	if !ok {
		return
	}
	tx, err := a.db.Begin()
	if err != nil {
		a.serverError(w, err)
		return
	}
	defer tx.Rollback()
	var root, answered int64
	if in.ParentID != 0 {
		// Lock the comment being answered, so it cannot be deleted while the reply is written.
		var postID, parent int64
		var deleted bool
		err := tx.QueryRow(`SELECT post_id, user_id, COALESCE(parent_id, 0), deleted FROM comments WHERE id = $1 FOR SHARE`, in.ParentID).Scan(&postID, &answered, &parent, &deleted)
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, http.StatusNotFound, "That comment is gone.")
			return
		}
		if err != nil {
			a.serverError(w, err)
			return
		}
		if postID != id {
			fail(w, http.StatusBadRequest, "That comment is on another post.")
			return
		}
		if deleted {
			fail(w, http.StatusBadRequest, "That comment was deleted, so it cannot be answered.")
			return
		}
		root = in.ParentID
		if parent != 0 {
			root = parent // one level of threading: a reply to a reply joins the same thread
		}
		if answered != u.ID {
			var name string
			if err := tx.QueryRow(`SELECT username FROM users WHERE id = $1`, answered).Scan(&name); err != nil {
				a.serverError(w, err)
				return
			}
			if !startsWithMention(body, name) {
				body = "@" + name + " " + body
			}
		}
	}
	var parentArg any
	if root != 0 {
		parentArg = root
	}
	var cid int64
	if err := tx.QueryRow(`INSERT INTO comments (post_id, user_id, body, parent_id) VALUES ($1, $2, $3, $4) RETURNING id`, id, u.ID, body, parentArg).Scan(&cid); err != nil {
		a.serverError(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		a.serverError(w, err)
		return
	}
	// The person answered hears about a reply; the post owner hears about a comment unless they
	// were the one answered; anyone else @mentioned hears once.
	if answered != 0 {
		a.notifyAt(answered, u.ID, "reply", id, cid, "")
	}
	if owner != answered {
		a.notifyAt(owner, u.ID, "comment", id, cid, "")
	}
	a.notifyMentions(body, u.ID, id, cid, owner, answered)
	out, err := a.loadPosts(u.ID, 0, 0, id)
	if err != nil {
		a.serverError(w, err)
		return
	}
	list, err := a.queryComments(u.ID, `SELECT * FROM comments WHERE id = $2`, cid)
	if err != nil || len(list) == 0 {
		a.serverError(w, fmt.Errorf("new comment %d: %v", cid, err))
		return
	}
	out["comment"] = list[0]
	writeJSON(w, http.StatusOK, out)
}

// handleEditComment lets the author change a comment within the edit window. People newly
// @mentioned are told; people mentioned before are not told again.
func (a *app) handleEditComment(w http.ResponseWriter, r *http.Request, u *user) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var in struct {
		Body string `json:"body"`
	}
	if !readJSON(w, r, 8192, &in) {
		return
	}
	c, ok := a.commentRow(w, id)
	if !ok {
		return
	}
	if c.userID != u.ID {
		fail(w, http.StatusForbidden, "Only the author can edit a comment.")
		return
	}
	if c.deleted {
		fail(w, http.StatusBadRequest, "That comment was deleted.")
		return
	}
	if c.age > editWindow {
		fail(w, http.StatusForbidden, "Comments can be edited for 15 minutes after posting.")
		return
	}
	body := strings.TrimSpace(in.Body)
	if body == "" || len([]rune(body)) > maxComment {
		fail(w, http.StatusBadRequest, "A comment needs 1 to 1000 characters.")
		return
	}
	if _, err := a.db.Exec(`UPDATE comments SET body = $2, edited_at = now() WHERE id = $1`, id, body); err != nil {
		a.serverError(w, err)
		return
	}
	before := map[string]bool{}
	for _, n := range mentions(c.body) {
		before[n] = true
	}
	added := []string{}
	for _, n := range mentions(body) {
		if !before[n] {
			added = append(added, n)
		}
	}
	a.notifyNames(added, u.ID, c.postID, id, c.postOwner)
	list, err := a.queryComments(u.ID, `SELECT * FROM comments WHERE id = $2`, id)
	if err != nil || len(list) == 0 {
		a.serverError(w, fmt.Errorf("edited comment %d: %v", id, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"comment": list[0]})
}

// handleDeleteComment removes a comment. One that has replies is kept as a placeholder with no
// text, so the thread still reads; the placeholder goes when its last reply does.
func (a *app) handleDeleteComment(w http.ResponseWriter, r *http.Request, u *user) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	tx, err := a.db.Begin()
	if err != nil {
		a.serverError(w, err)
		return
	}
	defer tx.Rollback()
	var author, owner, parent int64
	err = tx.QueryRow(`SELECT c.user_id, p.user_id, COALESCE(c.parent_id, 0) FROM comments c JOIN posts p ON p.id = c.post_id WHERE c.id = $1 FOR UPDATE OF c`, id).Scan(&author, &owner, &parent)
	if err == nil && author != u.ID && owner != u.ID && u.Role != "admin" {
		err = sql.ErrNoRows
	}
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "No such comment, or it is not yours.")
		return
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	var hasReplies bool
	if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM comments WHERE parent_id = $1)`, id).Scan(&hasReplies); err != nil {
		a.serverError(w, err)
		return
	}
	how := "removed"
	if hasReplies {
		how = "kept"
		_, err = tx.Exec(`UPDATE comments SET deleted = true, body = '', edited_at = NULL WHERE id = $1`, id)
		if err == nil {
			_, err = tx.Exec(`DELETE FROM comment_likes WHERE comment_id = $1`, id)
		}
	} else {
		_, err = tx.Exec(`DELETE FROM comments WHERE id = $1`, id)
		if err == nil && parent != 0 {
			_, err = tx.Exec(`DELETE FROM comments WHERE id = $1 AND deleted AND NOT EXISTS (SELECT 1 FROM comments WHERE parent_id = $1)`, parent)
		}
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "placeholder": how == "kept"})
}

// handleLikeComment toggles a like on a comment in one statement either way.
func (a *app) handleLikeComment(w http.ResponseWriter, r *http.Request, u *user) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	c, ok := a.commentRow(w, id)
	if !ok {
		return
	}
	if c.deleted {
		fail(w, http.StatusBadRequest, "That comment was deleted.")
		return
	}
	res, err := a.db.Exec(`INSERT INTO comment_likes (comment_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, id, u.ID)
	if err != nil {
		a.serverError(w, err)
		return
	}
	n, _ := res.RowsAffected()
	liked := n == 1
	if liked {
		var told bool
		if err := a.db.QueryRow(`SELECT EXISTS (SELECT 1 FROM notifications WHERE user_id = $1 AND actor_id = $2 AND comment_id = $3 AND kind = 'comment_like')`, c.userID, u.ID, id).Scan(&told); err != nil {
			a.serverError(w, err)
			return
		}
		if !told {
			a.notifyAt(c.userID, u.ID, "comment_like", c.postID, id, "")
		}
	} else if _, err := a.db.Exec(`DELETE FROM comment_likes WHERE comment_id = $1 AND user_id = $2`, id, u.ID); err != nil {
		a.serverError(w, err)
		return
	}
	var likes int
	if err := a.db.QueryRow(`SELECT count(*) FROM comment_likes WHERE comment_id = $1`, id).Scan(&likes); err != nil {
		a.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"liked": liked, "likes": likes})
}

func (a *app) socialRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/users", a.authed(false, a.handleUserList))
	mux.HandleFunc("GET /api/users/{username}", a.authed(false, a.handleUserProfile))
	mux.HandleFunc("GET /api/posts", a.authed(false, a.handleGetPosts))
	mux.HandleFunc("POST /api/posts", a.authed(false, a.handleCreatePost))
	mux.HandleFunc("GET /api/posts/{id}", a.authed(false, a.handleGetPost))
	mux.HandleFunc("DELETE /api/posts/{id}", a.authed(false, a.handleDeletePost))
	mux.HandleFunc("POST /api/posts/{id}/like", a.authed(false, a.handleLike))
	mux.HandleFunc("POST /api/posts/{id}/comments", a.authed(false, a.handleComment))
	mux.HandleFunc("GET /api/posts/{id}/comments", a.authed(false, a.handleGetComments))
	mux.HandleFunc("DELETE /api/comments/{id}", a.authed(false, a.handleDeleteComment))
	mux.HandleFunc("PUT /api/comments/{id}", a.authed(false, a.handleEditComment))
	mux.HandleFunc("GET /api/comments/{id}/replies", a.authed(false, a.handleGetReplies))
	mux.HandleFunc("GET /api/comments/{id}/thread", a.authed(false, a.handleThread))
	mux.HandleFunc("POST /api/comments/{id}/like", a.authed(false, a.handleLikeComment))
	mux.HandleFunc("POST /api/achievements", a.authed(false, a.handleAchievements))
	mux.HandleFunc("GET /api/notifications", a.authed(false, a.handleNotifications))
	mux.HandleFunc("PUT /api/notifications/read", a.authed(false, a.handleReadNotifications))
}
