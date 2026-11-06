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
var flagDefaults = map[string]bool{"details": false, "demos": false, "references": false, "signups": true, "tour": false, "model3d": true}

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
	Created     time.Time       `json:"created"`
}

const userCols = "u.id, u.username, u.display_name, u.role, u.props, u.seen, u.onboarded, u.avatar, u.created_at"

type scanner interface{ Scan(...any) error }

func scanUser(row scanner) (*user, error) {
	var u user
	var props, seen []byte
	if err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Role, &props, &seen, &u.Onboarded, &u.Avatar, &u.Created); err != nil {
		return nil, err
	}
	u.Props, u.Seen = props, seen
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
	writeJSON(w, http.StatusOK, map[string]any{"user": a.currentUser(r), "flags": f, "props": knownProps})
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
	u, err := func() (*user, error) {
		row := a.db.QueryRow(`SELECT `+userCols+`, u.pw_hash FROM users u WHERE u.username = $1`, name)
		var x user
		var props, seen []byte
		err := row.Scan(&x.ID, &x.Username, &x.DisplayName, &x.Role, &props, &seen, &x.Onboarded, &x.Avatar, &x.Created, &hash)
		x.Props, x.Seen = props, seen
		return &x, err
	}()
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
	_, err := a.db.Exec(`UPDATE users SET display_name = $2, props = $3, seen = $4, onboarded = $5, avatar = $6 WHERE id = $1`,
		u.ID, u.DisplayName, string(u.Props), string(u.Seen), u.Onboarded, u.Avatar)
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
	_, err = db.Exec(schema)
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
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		if err := db.Ping(); err != nil {
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
	mux.HandleFunc("GET /", handleStatic(assets))

	srv := &http.Server{Addr: *listen, Handler: guard(mux), ReadHeaderTimeout: 5 * time.Second,
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
