// Flowa: a progress tracker for flow arts props. It serves the embedded UI and keeps
// accounts, sessions and per-prop progress in Postgres.
package main

import (
	"bytes"
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
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"
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
	maxProgress = 512 << 10 // one progress document; a full map of notes is far smaller
	keepBackups = 14
	sessionTTL  = 30 * 24 * time.Hour
	sessionIdle = "14 days" // a session unused this long ends (a Postgres interval)
	cookieName  = "flowa_session"
	// PBKDF2-SHA256 rounds. 200,000 takes about 0.7 s on this Pi 4; raise it on faster hardware.
	pwRounds   = 200000
	maxFails   = 8
	failWindow = 10 * time.Minute
)

// Feature flags and their defaults. They are changed at runtime by an admin.
var flagDefaults = map[string]bool{"details": true, "demos": true, "references": true, "signups": true, "tour": true, "model3d": true, "community": true, "game": true, "moderation": true, "catalogEditor": true, "linkCards": true, "videoEmbeds": true, "suggest": true, "avatars": true}

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
	db        *sql.DB
	dataDir   string
	backupMu  sync.Mutex // one backup at a time
	flagMu    sync.Mutex // guards flagCache and flagsAt
	lim       limits
	hosts     []string     // names the server answers to besides addresses and private names (FLOWA_HOSTS)
	proxies   []*net.IPNet // reverse proxies whose forwarding headers are believed (FLOWA_TRUSTED_PROXIES)
	fetch     *fetcher     // link previews; nil means no fetching
	flagCache map[string]bool
	flagsAt   time.Time
}

func newApp(db *sql.DB, dataDir string) *app {
	p, _ := parseProxies(defaultProxies)
	return &app{db: db, dataDir: dataDir, lim: newLimits(), proxies: p}
}

// defaultProxies are where a reverse proxy connects from when it runs on the same host (loopback,
// or Docker's bridge gateway) or in a Docker network beside the app. Docker networks use
// 172.16.0.0/12; a home LAN (192.168.x.x) is not in the list, so a device there cannot pick its own
// address by sending forwarding headers.
const defaultProxies = "127.0.0.0/8,::1/128,172.16.0.0/12"

// parseProxies reads FLOWA_TRUSTED_PROXIES: addresses or CIDR ranges separated by commas, or
// "none" to trust no proxy (every visitor connects directly).
func parseProxies(s string) ([]*net.IPNet, error) {
	out := []*net.IPNet{}
	if strings.TrimSpace(s) == "none" {
		return out, nil
	}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		if !strings.Contains(p, "/") {
			if ip := net.ParseIP(p); ip != nil && ip.To4() != nil {
				p += "/32"
			} else {
				p += "/128"
			}
		}
		_, n, err := net.ParseCIDR(p)
		if err != nil {
			return nil, fmt.Errorf("FLOWA_TRUSTED_PROXIES: %q is not an address or range", p)
		}
		out = append(out, n)
	}
	return out, nil
}

func (a *app) trustedProxy(ip net.IP) bool {
	for _, n := range a.proxies {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

type failCount struct {
	n     int
	reset time.Time
}

// limiter counts events per key in a fixed window, under its own lock. It holds at most maxKeys
// keys: when full it drops expired ones, and if it is still full a new key counts as over the
// limit, so a flood of new keys cannot make it grow without bound.
type limiter struct {
	mu      sync.Mutex
	n       int
	window  time.Duration
	maxKeys int
	m       map[string]*failCount
}

func newLimiter(n int, window time.Duration) *limiter {
	return &limiter{n: n, window: window, maxKeys: 20000, m: map[string]*failCount{}}
}

// over reports whether key has used its n events in the current window, without counting one.
func (l *limiter) over(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	f := l.m[key]
	return f != nil && time.Now().Before(f.reset) && f.n >= l.n
}

// add counts one event for key and reports whether it is still within the limit.
func (l *limiter) add(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	f := l.m[key]
	if f == nil || now.After(f.reset) {
		if f == nil && len(l.m) >= l.maxKeys {
			for k, g := range l.m {
				if now.After(g.reset) {
					delete(l.m, k)
				}
			}
			if len(l.m) >= l.maxKeys {
				return false
			}
		}
		f = &failCount{reset: now.Add(l.window)}
		l.m[key] = f
	}
	f.n++
	return f.n <= l.n
}

// limits are the allowances: failed sign-ins (per address and username, per address, per account),
// sign-ups per address, wrong current passwords per member, and writes per member per hour.
type limits struct {
	signin, signinIP, account, signup, password *limiter
	posts, comments, likes, badges, marks       *limiter
}

func newLimits() limits {
	return limits{
		signin: newLimiter(maxFails, failWindow), signinIP: newLimiter(30, failWindow), account: newLimiter(20, failWindow),
		signup: newLimiter(10, time.Hour), password: newLimiter(5, failWindow),
		posts: newLimiter(30, time.Hour), comments: newLimiter(120, time.Hour), likes: newLimiter(600, time.Hour),
		badges: newLimiter(60, time.Hour), marks: newLimiter(120, time.Hour),
	}
}

// allowed counts a write by u against l and answers 429 when the allowance is used up.
func allowed(w http.ResponseWriter, l *limiter, u *user, what string) bool {
	if l.add(strconv.FormatInt(u.ID, 10)) {
		return true
	}
	fail(w, http.StatusTooManyRequests, "That is more "+what+" than Flowa takes in an hour. Wait a while and try again.")
	return false
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
	// Moderation state: set only while a mute or suspension runs.
	MutedUntil     *time.Time `json:"mutedUntil,omitempty"`
	MuteReason     string     `json:"muteReason,omitempty"`
	SuspendedUntil *time.Time `json:"suspendedUntil,omitempty"`
	SuspendReason  string     `json:"suspendReason,omitempty"`
	StatsHidden    bool       `json:"statsHidden,omitempty"`
	LastActive     *time.Time `json:"lastActive,omitempty"` // filled only in the admin accounts list
	// AvatarCfg is the avatar the user built (web/avatar-def.json lists every option); public.
	AvatarCfg json.RawMessage `json:"avatarCfg"`
}

const userCols = "u.id, u.username, u.display_name, u.role, u.props, u.seen, u.onboarded, u.avatar, u.bio, u.links, u.prefs, u.stats, u.created_at, u.muted_until, u.mute_reason, u.suspended_until, u.suspend_reason, u.stats_hidden, u.avatar_cfg"

type scanner interface{ Scan(...any) error }

// scanUser reads userCols, followed by any extra columns the query selected.
func scanUser(row scanner, extra ...any) (*user, error) {
	var u user
	var props, seen, links, prefs, stats []byte
	var muted, suspended sql.NullTime
	var avcfg []byte
	dst := append([]any{&u.ID, &u.Username, &u.DisplayName, &u.Role, &props, &seen, &u.Onboarded, &u.Avatar, &u.Bio, &links, &prefs, &stats, &u.Created,
		&muted, &u.MuteReason, &suspended, &u.SuspendReason, &u.StatsHidden, &avcfg}, extra...)
	if err := row.Scan(dst...); err != nil {
		return nil, err
	}
	u.AvatarCfg = avcfg
	// Only a mute or suspension still running is reported.
	if muted.Valid && muted.Time.After(time.Now()) {
		u.MutedUntil = &muted.Time
	} else {
		u.MuteReason = ""
	}
	if suspended.Valid && suspended.Time.After(time.Now()) {
		u.SuspendedUntil = &suspended.Time
	} else {
		u.SuspendReason = ""
	}
	u.Props, u.Seen, u.Links, u.Prefs, u.Stats = props, seen, links, prefs, stats
	return &u, nil
}

/* ---------- passwords and sessions ---------- */

// hashSlots lets at most two password hashes run at once: each costs most of a second of CPU on
// the Pi, which also runs the printer.
var hashSlots = make(chan struct{}, 2)

func pbkdf2Key(pw string, salt []byte, rounds, n int) []byte {
	hashSlots <- struct{}{}
	defer func() { <-hashSlots }()
	key, _ := pbkdf2.Key(sha256.New, pw, salt, rounds, n)
	return key
}

func hashPassword(pw string) string {
	salt := make([]byte, 16)
	rand.Read(salt)
	key := pbkdf2Key(pw, salt, pwRounds, 32)
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
	got := pbkdf2Key(pw, salt, rounds, len(want))
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
		SameSite: http.SameSiteLaxMode, Secure: a.viaHTTPS(r)})
	return nil
}

func (a *app) currentUser(r *http.Request) *user {
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return nil
	}
	// A session ends at expires_at, or after sessionIdle without use; use is noted once an hour.
	th := tokenHash(c.Value)
	var stale bool
	u, err := scanUser(a.db.QueryRow(`SELECT `+userCols+`, s.last_seen < now() - interval '1 hour' FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now() AND s.last_seen > now() - $2::interval`, th, sessionIdle), &stale)
	if err != nil || u.SuspendedUntil != nil {
		return nil
	}
	if stale {
		a.db.Exec(`UPDATE sessions SET last_seen = now() WHERE token_hash = $1`, th)
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

// privateSuffixes are name endings that only resolve inside a home network or a private VPN.
var privateSuffixes = []string{".local", ".lan", ".home", ".home.arpa", ".internal", ".localdomain", ".ts.net"}

// allowedHost reports whether the Host of a request names this server the way someone on the
// home network would: an IP address, localhost, a single-label name ("raspi"), a private name, or
// one listed in FLOWA_HOSTS. Any other name is refused, which stops a website from pointing its
// own name at the Pi (DNS rebinding) and talking to Flowa from a visitor's browser.
func allowedHost(host string, extra []string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSuffix(strings.Trim(host, "[]"), "."))
	if host == "" {
		return false
	}
	if net.ParseIP(host) != nil || host == "localhost" || !strings.Contains(host, ".") || slices.Contains(extra, host) {
		return true
	}
	for _, s := range privateSuffixes {
		if strings.HasSuffix(host, s) {
			return true
		}
	}
	return false
}

// secure checks the Host and sets the headers every response carries: no framing by other sites,
// no MIME sniffing, no referrer, a separate browsing context, and no powerful browser features.
// API responses get a CSP that allows nothing; pages set their own (handleStatic).
func (a *app) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		if !allowedHost(r.Host, a.hosts) {
			http.Error(w, "Flowa does not answer to this address. Add it to FLOWA_HOSTS to use it.", http.StatusMisdirectedRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
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

// authed wraps a handler that needs a signed-in user and, unless perm is empty, that permission.
func (a *app) authed(perm string, h func(http.ResponseWriter, *http.Request, *user)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := a.currentUser(r)
		if u == nil {
			fail(w, http.StatusUnauthorized, "Sign in first.")
			return
		}
		if perm != "" && !can(u, perm) {
			fail(w, http.StatusForbidden, "You do not have permission to do that.")
			return
		}
		h(w, r, u)
	}
}

/* ---------- roles and permissions ---------- */

// Permissions. Handlers ask for these, never for a role, so the table below is the one place
// that says who may do what.
const (
	permContentDelete  = "content.delete"  // delete anyone's post or comment
	permPostHide       = "post.hide"       // hide and restore posts and comments
	permUserMute       = "user.mute"       // stop someone posting and commenting for a time
	permUserSuspend    = "user.suspend"    // sign someone out and keep them out for a time
	permReportResolve  = "report.resolve"  // work the moderation queue
	permBadgeRevoke    = "badge.revoke"    // revoke a badge, hide reported stats
	permDashboard      = "dashboard.read"  // the admin dashboard and the accounts panel
	permAudit          = "audit.read"      // the audit log
	permCatalogEdit    = "catalog.edit"    // draft changes to skill maps
	permCatalogPublish = "catalog.publish" // publish and roll back skill maps
	permFlagsEdit      = "flags.edit"      // feature flags
	permUserRole       = "user.role"       // change roles
	permUserManage     = "user.manage"     // reset passwords, delete accounts
)

var roleRank = map[string]int{"user": 0, "moderator": 1, "admin": 2}

var rolePerms = map[string][]string{
	"user":      {},
	"moderator": {permContentDelete, permPostHide, permUserMute, permUserSuspend, permReportResolve, permBadgeRevoke, permDashboard, permCatalogEdit},
	"admin": {permContentDelete, permPostHide, permUserMute, permUserSuspend, permReportResolve, permBadgeRevoke, permDashboard, permAudit,
		permCatalogEdit, permCatalogPublish, permFlagsEdit, permUserRole, permUserManage},
}

func can(u *user, perm string) bool { return u != nil && slices.Contains(rolePerms[u.Role], perm) }

// permsOf lists what a user may do, for the interface to show only what the server allows.
func permsOf(u *user) []string {
	if u == nil {
		return []string{}
	}
	return append([]string{}, rolePerms[u.Role]...)
}

// outranks reports whether someone with role a may act on someone with role b.
func outranks(a, b string) bool { return roleRank[a] > roleRank[b] }

// below lists the roles a member of role r outranks, for queries.
func below(r string) []string {
	out := []string{}
	for k := range roleRank {
		if outranks(r, k) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
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
	// The next request reads the new flags, not the cached ones.
	a.flagMu.Lock()
	a.flagCache = nil
	a.flagMu.Unlock()
	return nil
}

func (a *app) handlePutFlags(w http.ResponseWriter, r *http.Request, u *user) {
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
	changed := map[string]any{}
	for k, v := range in.Flags {
		changed[k] = v
	}
	if err := a.audit(a.db, u.ID, "flags", "", 0, 0, "", changed); err != nil {
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
	writeJSON(w, http.StatusOK, map[string]any{"user": u, "flags": f, "props": knownProps, "achievements": ach, "perms": permsOf(u)})
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
	if !a.lim.signup.add(a.clientIP(r)) {
		fail(w, http.StatusTooManyRequests, "Too many new accounts from this address. Try again in an hour.")
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

// clientIP is the visitor's address, which the sign-in and sign-up limits count by. Behind a reverse
// proxy the connection comes from the proxy, so when it does (a trusted proxy), the visitor is the
// nearest address in X-Forwarded-For that is not itself a trusted proxy, read from the right: each
// proxy appends the address it saw, so only the right-hand end is trustworthy and a visitor cannot
// choose their address by sending the header. Without X-Forwarded-For, X-Real-IP is used.
func (a *app) clientIP(r *http.Request) string {
	remote, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		remote = r.RemoteAddr
	}
	ip := net.ParseIP(remote)
	if ip == nil || !a.trustedProxy(ip) {
		return remote
	}
	var hops []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(v, ",")...)
	}
	for i := len(hops) - 1; i >= 0; i-- {
		hop := net.ParseIP(strings.TrimSpace(hops[i]))
		if hop == nil {
			return remote // a malformed entry: nothing to its left can be believed
		}
		if !a.trustedProxy(hop) {
			return hop.String()
		}
	}
	if xr := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); xr != nil {
		return xr.String()
	}
	if len(hops) > 0 {
		return net.ParseIP(strings.TrimSpace(hops[0])).String() // every hop is a trusted proxy
	}
	return remote
}

// viaHTTPS reports whether the visitor's connection is encrypted: directly, or as a trusted proxy says.
func (a *app) viaHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	remote, _, _ := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(remote)
	return ip != nil && a.trustedProxy(ip) && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (a *app) handleSignin(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if !readJSON(w, r, 4096, &in) {
		return
	}
	name := strings.ToLower(strings.TrimSpace(in.Username))
	ip := a.clientIP(r)
	key := ip + "|" + name
	// Failures are counted per address and username, per address (guessing across many accounts)
	// and per account (guessing from many addresses).
	if a.lim.signin.over(key) || a.lim.signinIP.over(ip) || a.lim.account.over(name) {
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
		a.lim.signin.add(key)
		a.lim.signinIP.add(ip)
		a.lim.account.add(name)
		fail(w, http.StatusUnauthorized, "Wrong username or password.")
		return
	}
	if u.SuspendedUntil != nil {
		msg := "Your account is suspended until " + u.SuspendedUntil.UTC().Format("2 January 2006, 15:04 UTC") + "."
		if u.SuspendReason != "" {
			msg += " Reason: " + u.SuspendReason
		}
		fail(w, http.StatusForbidden, msg)
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
		// The avatar, checked against web/avatar-def.json.
		AvatarCfg json.RawMessage `json:"avatarCfg"`
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
		v, err := cleanImage(*in.Avatar, 1024, maxAvatar)
		if err != nil {
			fail(w, http.StatusBadRequest, "That picture cannot be used: "+err.Error()+".")
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
			// autoPost: share milestones; shareTransitions: send anonymous transition marks for suggestions.
			if k != "autoPost" && k != "shareTransitions" {
				fail(w, http.StatusBadRequest, "Unknown preference: "+k)
				return
			}
			prefs[k] = v
		}
		u.Prefs, _ = json.Marshal(prefs)
	}
	if len(in.AvatarCfg) > 0 {
		if err := validAvatar(in.AvatarCfg); err != nil {
			fail(w, http.StatusBadRequest, "That avatar cannot be saved: "+err.Error())
			return
		}
		u.AvatarCfg = in.AvatarCfg
	}
	if len(in.Stats) > 0 {
		if !validStats(in.Stats) {
			fail(w, http.StatusBadRequest, "Stats must be a small object of numbers.")
			return
		}
		u.Stats = in.Stats
	}
	_, err := a.db.Exec(`UPDATE users SET display_name = $2, props = $3, seen = $4, onboarded = $5, avatar = $6, bio = $7, links = $8, prefs = $9, stats = $10, avatar_cfg = $11 WHERE id = $1`,
		u.ID, u.DisplayName, string(u.Props), string(u.Seen), u.Onboarded, u.Avatar, u.Bio, string(u.Links), string(u.Prefs), string(u.Stats), string(u.AvatarCfg))
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
	uid := strconv.FormatInt(u.ID, 10)
	if a.lim.password.over(uid) {
		fail(w, http.StatusTooManyRequests, "Too many wrong passwords. Wait ten minutes and try again.")
		return
	}
	if !checkPassword(hash, in.Current) {
		a.lim.password.add(uid)
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

// handleSignoutOthers ends every session of the member except the one making the request.
func (a *app) handleSignoutOthers(w http.ResponseWriter, r *http.Request, u *user) {
	keep := ""
	if c, err := r.Cookie(cookieName); err == nil {
		keep = tokenHash(c.Value)
	}
	res, err := a.db.Exec(`DELETE FROM sessions WHERE user_id = $1 AND token_hash <> $2`, u.ID, keep)
	if err != nil {
		a.serverError(w, err)
		return
	}
	n, _ := res.RowsAffected()
	writeJSON(w, http.StatusOK, map[string]int64{"ended": n})
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
	if !readJSON(w, r, maxProgress, &in) {
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
	a.backupMu.Lock()
	defer a.backupMu.Unlock()
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
	// Last active: the latest sign-in or progress save.
	rows, err := a.db.Query(`SELECT ` + userCols + `, GREATEST((SELECT max(created_at) FROM sessions s WHERE s.user_id = u.id), (SELECT max(updated_at) FROM progress p WHERE p.user_id = u.id))
		FROM users u ORDER BY u.id`)
	if err != nil {
		a.serverError(w, err)
		return
	}
	defer rows.Close()
	out := []*user{}
	for rows.Next() {
		var last sql.NullTime
		u, err := scanUser(rows, &last)
		if err != nil {
			a.serverError(w, err)
			return
		}
		if last.Valid {
			u.LastActive = &last.Time
		}
		u.Avatar = u.avatarURL()
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
	// Only user.manage reaches here (admins); the last admin is protected below.
	var name, role string
	err := a.db.QueryRow(`SELECT username, role FROM users WHERE id = $1`, id).Scan(&name, &role)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "No such user.")
		return
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	// Admins are peers: one cannot delete another (change their role first, which is audited).
	if !outranks(u.Role, role) {
		fail(w, http.StatusForbidden, "You cannot delete the account of someone with your role or higher. Change their role first.")
		return
	}
	a.backup()
	tx, err := a.db.Begin()
	if err != nil {
		a.serverError(w, err)
		return
	}
	defer tx.Rollback()
	res, err := tx.Exec(`DELETE FROM users WHERE id = $1 AND (role <> 'admin' OR (SELECT count(*) FROM users WHERE role = 'admin') > 1)`, id)
	if err == nil {
		if n, _ := res.RowsAffected(); n == 0 {
			fail(w, http.StatusBadRequest, "Flowa needs at least one admin.")
			return
		}
		err = a.audit(tx, u.ID, "delete_user", "user", id, 0, "", map[string]any{"username": name})
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *app) handleResetPassword(w http.ResponseWriter, r *http.Request, u *user) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var in struct {
		Next string `json:"next"`
	}
	if !readJSON(w, r, 4096, &in) || !validPassword(w, in.Next) {
		return
	}
	// Your own password changes on the profile, with the current one; another admin's never here,
	// or one admin could sign in as another.
	if id == u.ID {
		fail(w, http.StatusBadRequest, "Change your own password on your profile.")
		return
	}
	res, err := a.db.Exec(`UPDATE users SET pw_hash = $2 WHERE id = $1 AND role = ANY($3)`, id, hashPassword(in.Next), pq.Array(below(u.Role)))
	if err != nil {
		a.serverError(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		fail(w, http.StatusNotFound, "No such user, or their role is the same as yours or higher.")
		return
	}
	a.db.Exec(`DELETE FROM sessions WHERE user_id = $1`, id)
	if err := a.audit(a.db, u.ID, "reset_password", "user", id, id, "", nil); err != nil {
		a.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleSetRole changes someone's role. Nobody changes their own, and Flowa always keeps an admin.
func (a *app) handleSetRole(w http.ResponseWriter, r *http.Request, u *user) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var in struct {
		Role   string `json:"role"`
		Reason string `json:"reason"`
	}
	if !readJSON(w, r, 4096, &in) {
		return
	}
	if _, ok := roleRank[in.Role]; !ok {
		fail(w, http.StatusBadRequest, "The role must be user, moderator or admin.")
		return
	}
	if id == u.ID {
		fail(w, http.StatusBadRequest, "You cannot change your own role.")
		return
	}
	tx, err := a.db.Begin()
	if err != nil {
		a.serverError(w, err)
		return
	}
	defer tx.Rollback()
	var old string
	err = tx.QueryRow(`SELECT role FROM users WHERE id = $1 FOR UPDATE`, id).Scan(&old)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "No such user.")
		return
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	next := in.Role
	if old == "admin" && next != "admin" {
		var admins int
		if err := tx.QueryRow(`SELECT count(*) FROM (SELECT 1 FROM users WHERE role = 'admin' FOR UPDATE) x`).Scan(&admins); err != nil {
			a.serverError(w, err)
			return
		}
		if admins <= 1 {
			fail(w, http.StatusBadRequest, "Flowa needs at least one admin.")
			return
		}
	}
	if old == in.Role {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "role": old})
		return
	}
	if _, err := tx.Exec(`UPDATE users SET role = $2 WHERE id = $1`, id, in.Role); err != nil {
		a.serverError(w, err)
		return
	}
	if err := a.audit(tx, u.ID, "role", "user", id, id, strings.TrimSpace(in.Reason), map[string]any{"from": old, "to": in.Role}); err != nil {
		a.serverError(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		a.serverError(w, err)
		return
	}
	a.notify(id, u.ID, "moderation", 0, "Your role is now "+in.Role+".")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "role": in.Role})
}

/* ---------- moderation: reports, the queue, actions ---------- */

var reportReasons = []string{"spam", "harassment", "unsafe", "offtopic", "other"}

const (
	maxReportsPerDay = 10
	maxModReason     = 300
	maxModUntil      = 366 * 24 * time.Hour
)

// flagged answers 404 while a feature flag is off, so its endpoints do not exist until it is on.
func (a *app) flagged(name string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f, err := a.flags()
		if err != nil {
			a.serverError(w, err)
			return
		}
		if !f[name] {
			http.NotFound(w, r)
			return
		}
		h(w, r)
	}
}

// targetOwner finds who a report or action is about: the author of a post or comment, or the
// user themself. Hidden content counts as missing to someone who may not see it.
func (a *app) targetOwner(kind string, id int64, viewer *user) (int64, error) {
	var owner int64
	var err error
	switch kind {
	case "post":
		err = a.db.QueryRow(`SELECT user_id FROM posts WHERE id = $1 AND (hidden_at IS NULL OR user_id = $2 OR $3)`, id, viewer.ID, can(viewer, permPostHide)).Scan(&owner)
	case "comment":
		err = a.db.QueryRow(`SELECT c.user_id FROM comments c JOIN posts p ON p.id = c.post_id WHERE c.id = $1 AND NOT c.deleted
			AND (c.hidden_at IS NULL OR c.user_id = $2 OR $3) AND (p.hidden_at IS NULL OR p.user_id = $2 OR $3)`, id, viewer.ID, can(viewer, permPostHide)).Scan(&owner)
	case "user":
		err = a.db.QueryRow(`SELECT id FROM users WHERE id = $1`, id).Scan(&owner)
	default:
		return 0, errBadKind
	}
	return owner, err
}

var errBadKind = errors.New("unknown kind")

func (a *app) handleReport(w http.ResponseWriter, r *http.Request, u *user) {
	var in struct {
		Kind   string `json:"kind"`
		ID     int64  `json:"id"`
		Reason string `json:"reason"`
		Note   string `json:"note"`
	}
	if !readJSON(w, r, 4096, &in) {
		return
	}
	if !slices.Contains(reportReasons, in.Reason) {
		fail(w, http.StatusBadRequest, "Choose a reason for the report.")
		return
	}
	note := strings.TrimSpace(in.Note)
	if len([]rune(note)) > 500 {
		fail(w, http.StatusBadRequest, "Keep the note under 500 characters.")
		return
	}
	owner, err := a.targetOwner(in.Kind, in.ID, u)
	if errors.Is(err, errBadKind) {
		fail(w, http.StatusBadRequest, "You can report a post, a comment or a person.")
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "That is gone already.")
		return
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	if owner == u.ID {
		fail(w, http.StatusBadRequest, "You cannot report yourself or your own posts.")
		return
	}
	var today int
	if err := a.db.QueryRow(`SELECT count(*) FROM reports WHERE reporter_id = $1 AND created_at > now() - interval '1 day'`, u.ID).Scan(&today); err != nil {
		a.serverError(w, err)
		return
	}
	if today >= maxReportsPerDay {
		fail(w, http.StatusTooManyRequests, "You have sent 10 reports today. Thank you; the moderators will get to them.")
		return
	}
	_, err = a.db.Exec(`INSERT INTO reports (reporter_id, target_kind, target_id, reason, note) VALUES ($1, $2, $3, $4, $5)`, u.ID, in.Kind, in.ID, in.Reason, note)
	var pe *pq.Error
	if errors.As(err, &pe) && pe.Code == "23505" {
		fail(w, http.StatusConflict, "You already reported this. A moderator will look at it.")
		return
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// A group of open reports about one thing, as the queue shows it.
type queueItem struct {
	Kind    string           `json:"kind"`
	ID      int64            `json:"id"`
	Target  map[string]any   `json:"target"`
	Author  map[string]any   `json:"author"`
	Reports []map[string]any `json:"reports"`
	Oldest  time.Time        `json:"oldest"`
	History []map[string]any `json:"history"`
}

func (a *app) handleQueue(w http.ResponseWriter, r *http.Request, u *user) {
	status := "open"
	if r.URL.Query().Get("status") == "resolved" {
		status = "resolved"
	}
	q := `SELECT r.id, r.target_kind, r.target_id, r.reason, r.note, r.created_at, r.status, r.resolution, ru.username, COALESCE(rv.username, '')
		FROM reports r JOIN users ru ON ru.id = r.reporter_id LEFT JOIN users rv ON rv.id = r.resolver_id
		WHERE ` + map[string]string{"open": `r.status = 'open' ORDER BY r.id LIMIT 500`, "resolved": `r.status <> 'open' ORDER BY r.resolved_at DESC LIMIT 200`}[status]
	rows, err := a.db.Query(q)
	if err != nil {
		a.serverError(w, err)
		return
	}
	defer rows.Close()
	items := []*queueItem{}
	byKey := map[string]*queueItem{}
	for rows.Next() {
		var id, tid int64
		var kind, reason, note, st, res, reporter, resolver string
		var at time.Time
		if err := rows.Scan(&id, &kind, &tid, &reason, &note, &at, &st, &res, &reporter, &resolver); err != nil {
			a.serverError(w, err)
			return
		}
		k := fmt.Sprintf("%s:%d", kind, tid)
		it := byKey[k]
		if it == nil {
			it = &queueItem{Kind: kind, ID: tid, Oldest: at}
			byKey[k] = it
			items = append(items, it)
		}
		it.Reports = append(it.Reports, map[string]any{"id": id, "reason": reason, "note": note, "at": at, "by": reporter, "status": st, "resolution": res, "resolver": resolver})
	}
	if err := rows.Err(); err != nil {
		a.serverError(w, err)
		return
	}
	for _, it := range items {
		if err := a.fillQueueItem(it); err != nil {
			a.serverError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// fillQueueItem adds the reported content, its author and the author's moderation history.
func (a *app) fillQueueItem(it *queueItem) error {
	var author int64
	switch it.Kind {
	case "post":
		var body, image, reason string
		var data []byte
		var hidden bool
		err := a.db.QueryRow(`SELECT user_id, body, image, data, hidden_at IS NOT NULL, hidden_reason FROM posts WHERE id = $1`, it.ID).Scan(&author, &body, &image, &data, &hidden, &reason)
		if errors.Is(err, sql.ErrNoRows) {
			it.Target = map[string]any{"gone": true}
			return nil
		}
		if err != nil {
			return err
		}
		it.Target = map[string]any{"body": body, "image": image, "data": json.RawMessage(data), "hidden": hidden, "hiddenReason": reason}
	case "comment":
		var body, reason string
		var post int64
		var hidden, deleted bool
		err := a.db.QueryRow(`SELECT user_id, post_id, body, hidden_at IS NOT NULL, hidden_reason, deleted FROM comments WHERE id = $1`, it.ID).Scan(&author, &post, &body, &hidden, &reason, &deleted)
		if errors.Is(err, sql.ErrNoRows) || deleted {
			it.Target = map[string]any{"gone": true}
			return nil
		}
		if err != nil {
			return err
		}
		it.Target = map[string]any{"body": body, "postId": post, "hidden": hidden, "hiddenReason": reason}
	case "user":
		author = it.ID
		it.Target = map[string]any{}
	}
	tu, err := scanUser(a.db.QueryRow(`SELECT `+userCols+` FROM users u WHERE u.id = $1`, author))
	if errors.Is(err, sql.ErrNoRows) {
		it.Target = map[string]any{"gone": true}
		return nil
	}
	if err != nil {
		return err
	}
	var posts, actions int
	if err := a.db.QueryRow(`SELECT (SELECT count(*) FROM posts WHERE user_id = $1), (SELECT count(*) FROM mod_actions WHERE target_user_id = $1)`, author).Scan(&posts, &actions); err != nil {
		return err
	}
	it.Author = map[string]any{"id": tu.ID, "username": tu.Username, "displayName": tu.DisplayName, "role": tu.Role, "avatar": tu.Avatar, "bio": tu.Bio,
		"posts": posts, "actions": actions, "mutedUntil": tu.MutedUntil, "suspendedUntil": tu.SuspendedUntil, "statsHidden": tu.StatsHidden}
	hist, err := a.auditRows(`WHERE m.target_user_id = $1 ORDER BY m.id DESC LIMIT 5`, author)
	it.History = hist
	return err
}

// handleModAction hides or restores content, mutes or suspends a person, revokes a badge, or
// hides reported stats. Each action is logged and the person affected is told.
func (a *app) handleModAction(w http.ResponseWriter, r *http.Request, u *user) {
	var in struct {
		Action string `json:"action"`
		Kind   string `json:"kind"`
		ID     int64  `json:"id"`
		Reason string `json:"reason"`
		Until  string `json:"until"`
		Key    string `json:"key"`
	}
	if !readJSON(w, r, 4096, &in) {
		return
	}
	reason := strings.TrimSpace(in.Reason)
	if len([]rune(reason)) > maxModReason {
		fail(w, http.StatusBadRequest, "Keep the reason under 300 characters.")
		return
	}
	perm := map[string]string{"hide": permPostHide, "restore": permPostHide, "mute": permUserMute, "unmute": permUserMute,
		"suspend": permUserSuspend, "unsuspend": permUserSuspend, "revoke_badge": permBadgeRevoke, "hide_stats": permBadgeRevoke, "show_stats": permBadgeRevoke}[in.Action]
	if perm == "" {
		fail(w, http.StatusBadRequest, "Unknown action.")
		return
	}
	if !can(u, perm) {
		fail(w, http.StatusForbidden, "You do not have permission to do that.")
		return
	}
	kind := in.Kind
	if in.Action != "hide" && in.Action != "restore" {
		kind = "user"
	}
	if (in.Action == "hide" || in.Action == "mute" || in.Action == "suspend" || in.Action == "revoke_badge") && reason == "" {
		fail(w, http.StatusBadRequest, "Give a reason. The person affected will see it.")
		return
	}
	owner, err := a.targetOwner(kind, in.ID, u)
	if errors.Is(err, errBadKind) {
		fail(w, http.StatusBadRequest, "You can hide a post or a comment.")
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "That is gone already.")
		return
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	if owner == u.ID {
		fail(w, http.StatusBadRequest, "You cannot moderate yourself.")
		return
	}
	var role string
	if err := a.db.QueryRow(`SELECT role FROM users WHERE id = $1`, owner).Scan(&role); err != nil {
		a.serverError(w, err)
		return
	}
	// Moderators act on users; admins on users and moderators. Nobody acts on an admin.
	if !outranks(u.Role, role) {
		fail(w, http.StatusForbidden, "You cannot moderate someone with the same role as yours or higher.")
		return
	}
	var until time.Time
	if in.Action == "mute" || in.Action == "suspend" {
		until, err = time.Parse(time.RFC3339, in.Until)
		if err != nil || !until.After(time.Now().Add(time.Minute)) || until.After(time.Now().Add(maxModUntil)) {
			fail(w, http.StatusBadRequest, "Choose an end time between now and a year from now.")
			return
		}
	}
	tx, err := a.db.Begin()
	if err != nil {
		a.serverError(w, err)
		return
	}
	defer tx.Rollback()
	var res sql.Result
	detail := map[string]any{}
	msg := ""
	table := map[string]string{"post": "posts", "comment": "comments"}[kind]
	switch in.Action {
	case "hide":
		res, err = tx.Exec(`UPDATE `+table+` SET hidden_at = now(), hidden_reason = $2 WHERE id = $1 AND hidden_at IS NULL`, in.ID, reason)
		msg = "A moderator hid your " + kind + ". Reason: " + reason
	case "restore":
		res, err = tx.Exec(`UPDATE `+table+` SET hidden_at = NULL, hidden_reason = '' WHERE id = $1 AND hidden_at IS NOT NULL`, in.ID)
		msg = "A moderator restored your " + kind + "."
	case "mute":
		res, err = tx.Exec(`UPDATE users SET muted_until = $2, mute_reason = $3 WHERE id = $1`, owner, until, reason)
		detail["until"] = until
		msg = "You are muted until " + until.UTC().Format("2 January 2006, 15:04 UTC") + ", so you cannot post or comment. Reason: " + reason
	case "unmute":
		res, err = tx.Exec(`UPDATE users SET muted_until = NULL, mute_reason = '' WHERE id = $1 AND muted_until > now()`, owner)
		msg = "You can post and comment again."
	case "suspend":
		res, err = tx.Exec(`UPDATE users SET suspended_until = $2, suspend_reason = $3 WHERE id = $1`, owner, until, reason)
		if err == nil {
			_, err = tx.Exec(`DELETE FROM sessions WHERE user_id = $1`, owner)
		}
		detail["until"] = until
		msg = "Your account was suspended until " + until.UTC().Format("2 January 2006, 15:04 UTC") + ". Reason: " + reason
	case "unsuspend":
		res, err = tx.Exec(`UPDATE users SET suspended_until = NULL, suspend_reason = '' WHERE id = $1 AND suspended_until > now()`, owner)
		msg = "Your suspension was lifted."
	case "revoke_badge":
		if !achKeyRE.MatchString(in.Key) {
			fail(w, http.StatusBadRequest, "Unknown badge.")
			return
		}
		res, err = tx.Exec(`DELETE FROM achievements WHERE user_id = $1 AND key = $2`, owner, in.Key)
		if err == nil {
			_, err = tx.Exec(`INSERT INTO badge_blocks (user_id, key) VALUES ($1, $2) ON CONFLICT DO NOTHING`, owner, in.Key)
		}
		detail["key"] = in.Key
		msg = "A moderator removed one of your badges (" + in.Key + "). Reason: " + reason
	case "hide_stats":
		res, err = tx.Exec(`UPDATE users SET stats_hidden = true WHERE id = $1 AND NOT stats_hidden`, owner)
		msg = "A moderator hid your level and XP from other people."
		if reason != "" {
			msg += " Reason: " + reason
		}
	case "show_stats":
		res, err = tx.Exec(`UPDATE users SET stats_hidden = false WHERE id = $1 AND stats_hidden`, owner)
		msg = "Your level and XP are shown to other people again."
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		fail(w, http.StatusConflict, "Nothing to change: it is already that way.")
		return
	}
	if err := a.audit(tx, u.ID, in.Action, kind, in.ID, owner, reason, detail); err != nil {
		a.serverError(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		a.serverError(w, err)
		return
	}
	a.notify(owner, u.ID, "moderation", 0, msg)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleResolve closes every open report about one thing, and tells each reporter.
func (a *app) handleResolve(w http.ResponseWriter, r *http.Request, u *user) {
	var in struct {
		Kind    string `json:"kind"`
		ID      int64  `json:"id"`
		Outcome string `json:"outcome"`
		Note    string `json:"note"`
	}
	if !readJSON(w, r, 4096, &in) {
		return
	}
	if in.Outcome != "actioned" && in.Outcome != "dismissed" {
		fail(w, http.StatusBadRequest, "The outcome is actioned or dismissed.")
		return
	}
	note := strings.TrimSpace(in.Note)
	if len([]rune(note)) > maxModReason {
		fail(w, http.StatusBadRequest, "Keep the note under 300 characters.")
		return
	}
	tx, err := a.db.Begin()
	if err != nil {
		a.serverError(w, err)
		return
	}
	defer tx.Rollback()
	rows, err := tx.Query(`UPDATE reports SET status = $3, resolver_id = $4, resolution = $5, resolved_at = now()
		WHERE target_kind = $1 AND target_id = $2 AND status = 'open' RETURNING reporter_id`, in.Kind, in.ID, in.Outcome, u.ID, note)
	if err != nil {
		a.serverError(w, err)
		return
	}
	reporters := []int64{}
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			reporters = append(reporters, id)
		}
	}
	rows.Close()
	if len(reporters) == 0 {
		fail(w, http.StatusNotFound, "There are no open reports about that.")
		return
	}
	if err := a.audit(tx, u.ID, "resolve", in.Kind, in.ID, 0, note, map[string]any{"outcome": in.Outcome, "reports": len(reporters)}); err != nil {
		a.serverError(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		a.serverError(w, err)
		return
	}
	msg := "Thank you for your report. A moderator looked at it and left it up."
	if in.Outcome == "actioned" {
		msg = "Thank you for your report. A moderator took action."
	}
	for _, id := range reporters {
		a.notify(id, u.ID, "moderation", 0, msg)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "resolved": len(reporters)})
}

// auditRows reads the audit log with usernames; where adds conditions over m (mod_actions).
func (a *app) auditRows(where string, args ...any) ([]map[string]any, error) {
	rows, err := a.db.Query(`SELECT m.id, m.action, m.target_kind, m.target_id, m.reason, m.detail, m.created_at, COALESCE(ua.username, ''), COALESCE(ut.username, '')
		FROM mod_actions m LEFT JOIN users ua ON ua.id = m.actor_id LEFT JOIN users ut ON ut.id = m.target_user_id `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, tid int64
		var action, kind, reason, actor, target string
		var detail []byte
		var at time.Time
		if err := rows.Scan(&id, &action, &kind, &tid, &reason, &detail, &at, &actor, &target); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "action": action, "kind": kind, "targetId": tid, "reason": reason, "detail": json.RawMessage(detail), "at": at, "actor": actor, "target": target})
	}
	return out, rows.Err()
}

// handleAudit pages through the audit log, newest first, filtered by who acted and what they did.
func (a *app) handleAudit(w http.ResponseWriter, r *http.Request, _ *user) {
	q := r.URL.Query()
	before := queryID(r, "before")
	list, err := a.auditRows(`WHERE ($1 = 0 OR m.id < $1) AND ($2 = '' OR ua.username = $2) AND ($3 = '' OR m.action = $3) ORDER BY m.id DESC LIMIT 51`,
		before, strings.ToLower(q.Get("actor")), q.Get("action"))
	if err != nil {
		a.serverError(w, err)
		return
	}
	more := len(list) > 50
	if more {
		list = list[:50]
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list, "more": more})
}

/* ---------- skill map catalogs ---------- */

// catalogIssue is one problem in a catalog, with the path of the field it is about.
type catalogIssue struct {
	Path string `json:"path"`
	Msg  string `json:"msg"`
}

var motionValues = map[string][]string{"plane": {"wall", "face", "table", "mixed", "unknown"}, "direction": {"forward", "reverse", "either", "unknown"},
	"hands": {"one", "both", "unknown"}, "grip": {"handle", "end", "middle", "unknown"}, "source": {"clip", "description", "unknown"}}

var (
	branchKeyRE = regexp.MustCompile(`^[A-Z]$`)
	catSkillRE  = regexp.MustCompile(`^[A-Z][1-9][0-9]?$`)
	weekIDRE    = regexp.MustCompile(`^w[0-9]{1,2}$`)
	httpsRE     = regexp.MustCompile(`^https://[^\s]+$`)
)

// validateCatalog checks a catalog against docs/catalog-format.md with the same rules as
// web/catalog-check.js; test/catalog-fixtures holds cases both must judge the same way.
func validateCatalog(raw []byte) (errs, warns []catalogIssue) {
	errs, warns = []catalogIssue{}, []catalogIssue{}
	E := func(path, f string, a ...any) { errs = append(errs, catalogIssue{path, fmt.Sprintf(f, a...)}) }
	W := func(path, f string, a ...any) { warns = append(warns, catalogIssue{path, fmt.Sprintf(f, a...)}) }
	var any_ any
	if err := json.Unmarshal(raw, &any_); err != nil {
		E("", "not valid JSON: %v", err)
		return
	}
	c, ok := any_.(map[string]any)
	if !ok {
		E("", "not a catalog object")
		return
	}
	// u16 counts length the way JavaScript does, so both validators measure text alike.
	u16 := func(s string) int {
		n := 0
		for _, r := range s {
			n++
			if r > 0xFFFF {
				n++
			}
		}
		return n
	}
	str := func(v any, lo, hi int) bool {
		t, ok := v.(string)
		return ok && u16(strings.TrimSpace(t)) >= lo && u16(t) <= hi
	}
	list := func(v any) []any { l, _ := v.([]any); return l }
	obj := func(v any) map[string]any { m, _ := v.(map[string]any); return m }
	intIn := func(v any, lo, hi int) (int, bool) {
		f, ok := v.(float64)
		if !ok || f != float64(int(f)) || int(f) < lo || int(f) > hi {
			return 0, false
		}
		return int(f), true
	}
	isList := func(v any) bool { _, ok := v.([]any); return ok }
	for _, k := range []string{"id", "name", "about", "careNote"} {
		if !str(c[k], 1, 600) {
			E(k, `missing or bad "%s"`, k)
		}
	}
	branches, skills := list(c["branches"]), list(c["skills"])
	refs := obj(c["refs"])
	if refs == nil {
		refs = map[string]any{}
	}
	if len(branches) < 5 || len(branches) > 11 {
		E("branches", "needs 5 to 11 branches, has %d", len(branches))
	}
	bk := map[string]bool{}
	for i, x := range branches {
		b := obj(x)
		at := fmt.Sprintf("branches.%d", i)
		k, _ := b["k"].(string)
		if !branchKeyRE.MatchString(k) {
			E(at+".k", `branch key "%v" is not one capital letter`, b["k"])
		} else if bk[k] {
			E(at+".k", "duplicate branch %s", k)
		}
		if branchKeyRE.MatchString(k) {
			bk[k] = true
		}
		if b == nil || !str(b["name"], 1, 40) {
			E(at+".name", "branch %s: bad name", k)
		}
		if b == nil || !str(b["blurb"], 1, 120) {
			E(at+".blurb", "branch %s: bad blurb", k)
		}
	}
	ids := map[string]map[string]any{}
	order := []string{}
	strs := func(v any) ([]string, bool) {
		l, ok := v.([]any)
		if !ok {
			return nil, false
		}
		out := []string{}
		for _, x := range l {
			out = append(out, fmt.Sprint(x))
		}
		return out, true
	}
	for i, x := range skills {
		sk := obj(x)
		at := fmt.Sprintf("skills.%d", i)
		id, _ := sk["id"].(string)
		if sk == nil || !catSkillRE.MatchString(id) {
			E(at+".id", "skill %v: bad id", sk["id"])
			continue
		}
		who := "skill " + id
		if ids[id] != nil {
			E(at+".id", "%s: duplicate", who)
		} else {
			order = append(order, id)
		}
		ids[id] = sk
		if !bk[id[:1]] {
			E(at+".id", "%s: no branch %s", who, id[:1])
		}
		if _, ok := intIn(sk["tier"], 1, 5); !ok {
			E(at+".tier", "%s: tier must be 1 to 5", who)
		}
		if !str(sk["name"], 2, 60) {
			E(at+".name", "%s: bad name", who)
		}
		if !str(sk["desc"], 10, 260) {
			E(at+".desc", "%s: bad desc", who)
		}
		if t, _ := sk["track"].(string); t != "one" && t != "LR" && t != "FR" {
			E(at+".track", "%s: track must be one, LR or FR", who)
		}
		if !isList(sk["needs"]) {
			E(at+".needs", "%s: needs must be a list", who)
		}
		allStr := func(l []any, lo, hi int) bool {
			for _, v := range l {
				if !str(v, lo, hi) {
					return false
				}
			}
			return true
		}
		if st := list(sk["steps"]); !isList(sk["steps"]) || len(st) < 1 || len(st) > 5 || !allStr(st, 5, 320) {
			E(at+".steps", "%s: needs 1 to 5 steps", who)
		}
		if wl := list(sk["watch"]); !isList(sk["watch"]) || len(wl) < 1 || len(wl) > 3 || !allStr(wl, 5, 240) {
			E(at+".watch", "%s: needs 1 to 3 watch items", who)
		}
		if !str(sk["drill"], 10, 320) {
			E(at+".drill", "%s: needs a drill", who)
		}
		if rl, ok := strs(sk["refs"]); !ok {
			E(at+".refs", "%s: refs must be a list", who)
		} else {
			for _, r := range rl {
				if _, ok := refs[r]; !ok {
					E(at+".refs", `%s: unknown ref "%s"`, who, r)
				}
			}
			if len(rl) == 0 && sk["retired"] != true {
				W(at+".refs", "%s: no refs", who)
			}
		}
		for _, k := range []string{"star", "care", "retired"} {
			if v, ok := sk[k]; ok {
				if _, isBool := v.(bool); !isBool {
					E(at+"."+k, "%s: %s must be true or false", who, k)
				}
			}
		}
		if mv, ok := sk["motion"]; ok {
			// How the move travels, for next-trick suggestions: each value from a fixed list, or unknown.
			if m := obj(mv); m == nil {
				E(at+".motion", "%s: motion must be an object", who)
			} else {
				for k, v := range m {
					allowed, known := motionValues[k]
					s, _ := v.(string)
					if !known {
						E(at+".motion."+k, `%s: motion has no field "%s"`, who, k)
					} else if !slices.Contains(allowed, s) {
						E(at+".motion."+k, "%s: motion %s must be one of %s", who, k, strings.Join(allowed, ", "))
					}
				}
			}
		}
		text := []string{}
		for _, k := range []string{"name", "desc", "drill"} {
			text = append(text, fmt.Sprint(sk[k]))
		}
		for _, k := range []string{"steps", "watch"} {
			for _, v := range list(sk[k]) {
				text = append(text, fmt.Sprint(v))
			}
		}
		if strings.ContainsAny(strings.Join(text, " "), "—–") {
			E(at, "%s: contains a long dash; use plain punctuation", who)
		}
	}
	live := func(id string) bool { sk := ids[id]; return sk != nil && sk["retired"] != true }
	tier := func(sk map[string]any) float64 { f, _ := sk["tier"].(float64); return f }
	for i, x := range skills {
		sk := obj(x)
		id, _ := sk["id"].(string)
		needs, ok := strs(sk["needs"])
		if ids[id] == nil || !ok {
			continue
		}
		at := fmt.Sprintf("skills.%d.needs", i)
		for _, n := range needs {
			nid := strings.TrimSuffix(n, "!")
			switch {
			case ids[nid] == nil:
				E(at, "skill %s: needs unknown %s", id, n)
			case nid == id:
				E(at, "skill %s: needs itself", id)
			case sk["retired"] != true && !live(nid):
				E(at, "skill %s: needs %s, which is retired", id, nid)
			case tier(ids[nid]) > tier(sk):
				W(at, "skill %s (T%v) needs %s (T%v), a higher tier", id, sk["tier"], nid, ids[nid]["tier"])
			}
		}
	}
	state := map[string]int{}
	var visit func(id string, path []string)
	visit = func(id string, path []string) {
		if state[id] == 2 {
			return
		}
		if state[id] == 1 {
			E("skills", "cycle: %s", strings.Join(append(path, id), " > "))
			return
		}
		state[id] = 1
		needs, _ := strs(ids[id]["needs"])
		for _, n := range needs {
			k := strings.TrimSuffix(n, "!")
			if ids[k] != nil && k != id {
				visit(k, append(append([]string{}, path...), id))
			}
		}
		state[id] = 2
	}
	for _, id := range order {
		visit(id, nil)
	}
	for b := range bk {
		has := false
		for _, x := range skills {
			sk := obj(x)
			if id, _ := sk["id"].(string); id != "" && id[:1] == b && sk["retired"] != true {
				has = true
			}
		}
		if !has {
			E("branches", "branch %s has no skills", b)
		}
	}
	used := map[string]bool{}
	for _, x := range skills {
		rl, _ := strs(obj(x)["refs"])
		for _, r := range rl {
			used[r] = true
		}
	}
	for k, v := range refs {
		l := list(v)
		u := ""
		if len(l) > 1 {
			u, _ = l[1].(string)
		}
		if len(l) == 0 || !str(l[0], 3, 140) || !httpsRE.MatchString(u) {
			E("refs."+k, "ref %s: needs [label, https url]", k)
		}
		if !used[k] {
			W("refs."+k, "ref %s is never used", k)
		}
	}
	phases := list(c["phases"])
	if len(phases) < 3 || len(phases) > 6 {
		E("phases", "needs 3 to 6 phases, has %d", len(phases))
	}
	for i, x := range phases {
		p := obj(x)
		at := fmt.Sprintf("phases.%d", i)
		pid, _ := p["id"].(string)
		if pid != fmt.Sprintf("p%d", i+1) {
			E(at+".id", "phase %d: id must be p%d", i+1, i+1)
		}
		if !str(p["name"], 2, 40) || !str(p["time"], 2, 40) || !str(p["gate"], 10, 260) {
			E(at, "phase %s: bad name, time or gate", pid)
		}
		sks, _ := p["skills"].(string)
		for _, id := range strings.Fields(sks) {
			if !live(id) {
				E(at+".skills", "phase %s: unknown or retired skill %s", pid, id)
			}
		}
		req, ok := strs(p["req"])
		if !ok || len(req) == 0 {
			E(at+".req", "phase %s: needs req", pid)
		}
		for _, r := range req {
			if !live(strings.TrimSuffix(r, "!")) {
				E(at+".req", "phase %s: unknown or retired req %s", pid, r)
			}
		}
	}
	if v, ok := c["weeks"]; ok {
		if !isList(v) {
			E("weeks", "weeks must be a list")
		} else {
			seen := map[string]bool{}
			for i, x := range list(v) {
				wk := obj(x)
				at := fmt.Sprintf("weeks.%d", i)
				wid, _ := wk["id"].(string)
				if !weekIDRE.MatchString(wid) || seen[wid] {
					E(at+".id", "week %d: id must be w and a number, unique", i+1)
				}
				seen[wid] = true
				from, ok1 := intIn(wk["from"], 1, 52)
				to, ok2 := intIn(wk["to"], 1, 52)
				if !ok1 || !ok2 || to < from {
					E(at, "week %s: from and to must be weeks 1 to 52, from first", wid)
				}
				for _, k := range []string{"label", "focus", "result", "gate"} {
					if !str(wk[k], 2, 160) {
						E(at+"."+k, "week %s: bad %s", wid, k)
					}
				}
			}
		}
	}
	if v, ok := c["aliases"]; ok {
		al := obj(v)
		if al == nil {
			E("aliases", "aliases must be an object of old id to new id")
		} else {
			for from, to := range al {
				if !catSkillRE.MatchString(from) {
					E("aliases."+from, "alias %s: bad id", from)
				} else if ids[from] != nil {
					E("aliases."+from, "alias %s: that id is a skill, so it cannot also be an alias", from)
				}
				if t, _ := to.(string); !live(t) {
					E("aliases."+from, "alias %s: points at %v, which is not a current skill", from, to)
				}
			}
		}
	}
	return errs, warns
}

// Catalogs live in catalog_revisions: numbered published revisions and at most one draft per prop.
// The files shipped in web/catalog seed revision 1, and a later deploy that ships a changed file
// publishes it automatically, unless an admin has published edits since; then the editor offers it.

type catalogRev struct {
	Rev     int             `json:"rev"`
	Status  string          `json:"status"`
	Body    json.RawMessage `json:"body,omitempty"`
	Source  string          `json:"source"`
	Author  string          `json:"author"`
	Note    string          `json:"note"`
	Created time.Time       `json:"created"`
}

func shippedCatalog(prop string) ([]byte, bool) {
	b, err := webFS.ReadFile("web/catalog/" + prop + ".json")
	return b, err == nil
}

func hashOf(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:8]) }

// canonHash hashes JSON after re-encoding it, so formatting and key order (which Postgres changes
// when it stores JSONB) do not count as a change.
func canonHash(b []byte) string {
	var v any
	if json.Unmarshal(b, &v) != nil {
		return hashOf(b)
	}
	c, _ := json.Marshal(v)
	return hashOf(c)
}

// seedCatalogs imports each shipped catalog that is new or changed since it was last imported.
func (a *app) seedCatalogs() error {
	for _, prop := range knownProps {
		raw, ok := shippedCatalog(prop)
		if !ok {
			continue
		}
		h := canonHash(raw)
		var rev int
		var source, last string
		err := a.db.QueryRow(`SELECT rev, source, shipped_hash FROM catalog_revisions WHERE prop = $1 AND status = 'published' ORDER BY rev DESC LIMIT 1`, prop).Scan(&rev, &source, &last)
		if errors.Is(err, sql.ErrNoRows) {
			rev, source = 0, "shipped"
		} else if err != nil {
			return err
		}
		if source != "shipped" || last == h {
			continue // edited since, or already current
		}
		if _, err := a.db.Exec(`INSERT INTO catalog_revisions (prop, rev, status, body, source, shipped_hash, note) VALUES ($1, $2, 'published', $3, 'shipped', $4, 'The version shipped with Flowa')`,
			prop, rev+1, string(raw), h); err != nil {
			return err
		}
		log.Printf("catalog %s: imported the shipped version as revision %d", prop, rev+1)
	}
	return nil
}

func (a *app) latestCatalog(prop string) (json.RawMessage, int, error) {
	var body []byte
	var rev int
	err := a.db.QueryRow(`SELECT body, rev FROM catalog_revisions WHERE prop = $1 AND status = 'published' ORDER BY rev DESC LIMIT 1`, prop).Scan(&body, &rev)
	return body, rev, err
}

// handleCatalogFile serves /catalog/<prop>.json from the latest published revision, or the shipped
// file when the database has none.
func (a *app) handleCatalogFile(w http.ResponseWriter, r *http.Request) {
	prop := strings.TrimSuffix(r.PathValue("file"), ".json")
	if !slices.Contains(knownProps, prop) || !strings.HasSuffix(r.PathValue("file"), ".json") {
		http.NotFound(w, r)
		return
	}
	body, _, err := a.latestCatalog(prop)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("catalog %s: %v", prop, err)
		}
		raw, ok := shippedCatalog(prop)
		if !ok {
			http.NotFound(w, r)
			return
		}
		body = raw
	}
	tag := `"` + hashOf(body) + `"`
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", tag)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Header.Get("If-None-Match") == tag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Write(body)
}

func catalogProp(w http.ResponseWriter, r *http.Request) (string, bool) {
	p := r.PathValue("prop")
	if !slices.Contains(knownProps, p) {
		fail(w, http.StatusNotFound, "Unknown prop.")
		return "", false
	}
	return p, true
}

func (a *app) catalogRevs(prop string) ([]catalogRev, error) {
	rows, err := a.db.Query(`SELECT c.rev, c.status, c.source, COALESCE(u.username, ''), c.note, c.created_at FROM catalog_revisions c LEFT JOIN users u ON u.id = c.author_id
		WHERE c.prop = $1 ORDER BY c.status = 'draft' DESC, c.rev DESC`, prop)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []catalogRev{}
	for rows.Next() {
		var x catalogRev
		if err := rows.Scan(&x.Rev, &x.Status, &x.Source, &x.Author, &x.Note, &x.Created); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// handleCatalogAdmin returns what the editor needs: the published catalog, the draft if there is
// one, the list of revisions, and whether a newer shipped version waits.
func (a *app) handleCatalogAdmin(w http.ResponseWriter, r *http.Request, _ *user) {
	prop, ok := catalogProp(w, r)
	if !ok {
		return
	}
	pub, rev, err := a.latestCatalog(prop)
	if errors.Is(err, sql.ErrNoRows) {
		raw, _ := shippedCatalog(prop)
		pub, rev, err = raw, 0, nil
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	var draft []byte
	if err := a.db.QueryRow(`SELECT body FROM catalog_revisions WHERE prop = $1 AND status = 'draft'`, prop).Scan(&draft); err != nil && !errors.Is(err, sql.ErrNoRows) {
		a.serverError(w, err)
		return
	}
	revs, err := a.catalogRevs(prop)
	if err != nil {
		a.serverError(w, err)
		return
	}
	shipped := false
	if raw, ok := shippedCatalog(prop); ok {
		var n int
		a.db.QueryRow(`SELECT count(*) FROM catalog_revisions WHERE prop = $1 AND shipped_hash = $2`, prop, canonHash(raw)).Scan(&n)
		shipped = n == 0
	}
	out := map[string]any{"published": json.RawMessage(pub), "rev": rev, "revisions": revs, "shippedUpdate": shipped}
	if draft != nil {
		out["draft"] = json.RawMessage(draft)
	}
	writeJSON(w, http.StatusOK, out)
}

// progressOn counts, for each skill id, the people with progress or notes recorded under it.
func (a *app) progressOn(prop string) (map[string]int, error) {
	rows, err := a.db.Query(`SELECT k, count(DISTINCT p.user_id) FROM progress p, jsonb_object_keys(CASE WHEN jsonb_typeof(p.state->'skills') = 'object' THEN p.state->'skills' ELSE '{}' END) k
		WHERE p.prop = $1 GROUP BY k`, prop)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}

// catShape is the part of a catalog the editor's checks need.
type catShape struct {
	ID      string            `json:"id"`
	Aliases map[string]string `json:"aliases"`
	Skills  []map[string]any  `json:"skills"`
}

func parseShape(raw []byte) catShape {
	var c catShape
	json.Unmarshal(raw, &c)
	return c
}

func (c catShape) skillMap() map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, s := range c.Skills {
		if id, ok := s["id"].(string); ok {
			out[id] = s
		}
	}
	return out
}

// safeToPublish refuses a catalog that would orphan progress (an id with progress that is neither a
// skill nor an alias in it) or, unless restoring an earlier revision (which brings back the same
// skills), reuse an id that the published catalog keeps as an alias.
func (a *app) safeToPublish(prop string, next []byte, restoring bool) []catalogIssue {
	issues := []catalogIssue{}
	n := parseShape(next)
	if n.ID != prop {
		return append(issues, catalogIssue{"id", fmt.Sprintf("the catalog id must be %q", prop)})
	}
	sk := n.skillMap()
	if cur, _, err := a.latestCatalog(prop); err == nil && !restoring {
		for from := range parseShape(cur).Aliases {
			if sk[from] != nil {
				issues = append(issues, catalogIssue{"skills", fmt.Sprintf("%s is an old id kept as an alias, so it cannot be used for a skill again", from)})
			}
		}
	}
	prog, err := a.progressOn(prop)
	if err != nil {
		return append(issues, catalogIssue{"", "could not check progress: " + err.Error()})
	}
	ids := make([]string, 0, len(prog))
	for id := range prog {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if sk[id] == nil && n.Aliases[id] == "" {
			issues = append(issues, catalogIssue{"skills", fmt.Sprintf("%s has progress for %d %s; retire it or alias it instead of removing it", id, prog[id], map[bool]string{true: "person", false: "people"}[prog[id] == 1])})
		}
	}
	return issues
}

func (a *app) handleCatalogDraft(w http.ResponseWriter, r *http.Request, u *user) {
	prop, ok := catalogProp(w, r)
	if !ok {
		return
	}
	var in struct {
		Body json.RawMessage `json:"body"`
	}
	if !readJSON(w, r, 4<<20, &in) {
		return
	}
	var probe map[string]any
	if json.Unmarshal(in.Body, &probe) != nil {
		fail(w, http.StatusBadRequest, "The draft must be a catalog object.")
		return
	}
	if probe["id"] != prop {
		fail(w, http.StatusBadRequest, fmt.Sprintf("The draft's id must be %q.", prop))
		return
	}
	// A draft may be incomplete while it is being edited; its problems are returned, not refused.
	if _, err := a.db.Exec(`INSERT INTO catalog_revisions (prop, rev, status, body, author_id) VALUES ($1, 0, 'draft', $2, $3)
		ON CONFLICT (prop) WHERE status = 'draft' DO UPDATE SET body = EXCLUDED.body, author_id = EXCLUDED.author_id, created_at = now()`, prop, string(in.Body), u.ID); err != nil {
		a.serverError(w, err)
		return
	}
	errs, warns := validateCatalog(in.Body)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "errors": errs, "warnings": warns})
}

func (a *app) handleCatalogDiscard(w http.ResponseWriter, r *http.Request, _ *user) {
	prop, ok := catalogProp(w, r)
	if !ok {
		return
	}
	if _, err := a.db.Exec(`DELETE FROM catalog_revisions WHERE prop = $1 AND status = 'draft'`, prop); err != nil {
		a.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleCatalogDiff compares the draft with the published catalog, skill by skill, with the number
// of people whose progress each change touches.
func (a *app) handleCatalogDiff(w http.ResponseWriter, r *http.Request, _ *user) {
	prop, ok := catalogProp(w, r)
	if !ok {
		return
	}
	var draft []byte
	err := a.db.QueryRow(`SELECT body FROM catalog_revisions WHERE prop = $1 AND status = 'draft'`, prop).Scan(&draft)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "There is no draft.")
		return
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	pub, _, err := a.latestCatalog(prop)
	if errors.Is(err, sql.ErrNoRows) {
		pub, _ = shippedCatalog(prop)
	} else if err != nil {
		a.serverError(w, err)
		return
	}
	prog, err := a.progressOn(prop)
	if err != nil {
		a.serverError(w, err)
		return
	}
	old, next := parseShape(pub), parseShape(draft)
	om, nm := old.skillMap(), next.skillMap()
	// A renamed skill shows once, as renamed, not also as a new skill under its new id.
	renamedTo := map[string]bool{}
	for from, to := range next.Aliases {
		if om[from] != nil && nm[from] == nil {
			renamedTo[to] = true
		}
	}
	type change struct {
		ID     string   `json:"id"`
		Kind   string   `json:"kind"`
		Fields []string `json:"fields,omitempty"`
		To     string   `json:"to,omitempty"`
		People int      `json:"people"`
	}
	changes := []change{}
	for _, s := range next.Skills {
		id, _ := s["id"].(string)
		o := om[id]
		switch {
		case o == nil && renamedTo[id]:
		case o == nil:
			changes = append(changes, change{ID: id, Kind: "added"})
		case s["retired"] == true && o["retired"] != true:
			changes = append(changes, change{ID: id, Kind: "retired", People: prog[id]})
		default:
			fields := []string{}
			keys := map[string]bool{}
			for k := range s {
				keys[k] = true
			}
			for k := range o {
				keys[k] = true
			}
			for k := range keys {
				a, _ := json.Marshal(s[k])
				b, _ := json.Marshal(o[k])
				if string(a) != string(b) {
					fields = append(fields, k)
				}
			}
			sort.Strings(fields)
			if len(fields) > 0 {
				changes = append(changes, change{ID: id, Kind: "changed", Fields: fields, People: prog[id]})
			}
		}
	}
	for _, s := range old.Skills {
		id, _ := s["id"].(string)
		if nm[id] == nil {
			if to := next.Aliases[id]; to != "" {
				changes = append(changes, change{ID: id, Kind: "renamed", To: to, People: prog[id]})
			} else {
				changes = append(changes, change{ID: id, Kind: "removed", People: prog[id]})
			}
		}
	}
	errs, warns := validateCatalog(draft)
	writeJSON(w, http.StatusOK, map[string]any{"changes": changes, "errors": errs, "warnings": warns, "blocked": a.safeToPublish(prop, draft, false)})
}

// publishCatalog makes body the next published revision, after the same checks the editor shows.
func (a *app) publishCatalog(w http.ResponseWriter, u *user, prop string, body []byte, note, action string, detail map[string]any) {
	if errs, _ := validateCatalog(body); len(errs) > 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Fix the problems in the catalog first.", "errors": errs})
		return
	}
	if issues := a.safeToPublish(prop, body, action == "catalog_restore"); len(issues) > 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": issues[0].Msg, "errors": issues})
		return
	}
	tx, err := a.db.Begin()
	if err != nil {
		a.serverError(w, err)
		return
	}
	defer tx.Rollback()
	var rev int
	if err := tx.QueryRow(`SELECT COALESCE(max(rev), 0) + 1 FROM catalog_revisions WHERE prop = $1 AND status = 'published'`, prop).Scan(&rev); err != nil {
		a.serverError(w, err)
		return
	}
	// Publishing the shipped version records its hash, so it is not offered again.
	shipped := ""
	if raw, ok := shippedCatalog(prop); ok && canonHash(raw) == canonHash(body) {
		shipped = canonHash(raw)
	}
	_, err = tx.Exec(`INSERT INTO catalog_revisions (prop, rev, status, body, source, shipped_hash, author_id, note) VALUES ($1, $2, 'published', $3, 'editor', $4, $5, $6)`, prop, rev, string(body), shipped, u.ID, note)
	if err == nil {
		_, err = tx.Exec(`DELETE FROM catalog_revisions WHERE prop = $1 AND status = 'draft'`, prop)
	}
	if err == nil {
		detail["prop"], detail["rev"] = prop, rev
		err = a.audit(tx, u.ID, action, "catalog", int64(rev), 0, note, detail)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "rev": rev})
}

func (a *app) handleCatalogPublish(w http.ResponseWriter, r *http.Request, u *user) {
	prop, ok := catalogProp(w, r)
	if !ok {
		return
	}
	var in struct {
		Note string `json:"note"`
	}
	if !readJSON(w, r, 4096, &in) {
		return
	}
	var draft []byte
	err := a.db.QueryRow(`SELECT body FROM catalog_revisions WHERE prop = $1 AND status = 'draft'`, prop).Scan(&draft)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "There is no draft to publish.")
		return
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	a.publishCatalog(w, u, prop, draft, strings.TrimSpace(in.Note), "catalog_publish", map[string]any{})
}

func (a *app) catalogRevision(w http.ResponseWriter, r *http.Request) (string, int, []byte, bool) {
	prop, ok := catalogProp(w, r)
	if !ok {
		return "", 0, nil, false
	}
	rev, _ := strconv.Atoi(r.PathValue("rev"))
	var body []byte
	err := a.db.QueryRow(`SELECT body FROM catalog_revisions WHERE prop = $1 AND rev = $2 AND status = 'published'`, prop, rev).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "No such revision.")
		return "", 0, nil, false
	}
	if err != nil {
		a.serverError(w, err)
		return "", 0, nil, false
	}
	return prop, rev, body, true
}

// handleCatalogExport downloads a revision in the catalog format, ready to commit to web/catalog.
func (a *app) handleCatalogExport(w http.ResponseWriter, r *http.Request, _ *user) {
	prop, rev, body, ok := a.catalogRevision(w, r)
	if !ok {
		return
	}
	var v any
	json.Unmarshal(body, &v)
	pretty, _ := json.MarshalIndent(v, "", " ")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-revision-%d.json"`, prop, rev))
	w.Write(append(pretty, '\n'))
}

func (a *app) handleCatalogRestore(w http.ResponseWriter, r *http.Request, u *user) {
	prop, rev, body, ok := a.catalogRevision(w, r)
	if !ok {
		return
	}
	a.publishCatalog(w, u, prop, body, fmt.Sprintf("Restored revision %d", rev), "catalog_restore", map[string]any{"from": rev})
}

// handleCatalogShipped loads the shipped version of a catalog as the draft, for an admin to review.
func (a *app) handleCatalogShipped(w http.ResponseWriter, r *http.Request, u *user) {
	prop, ok := catalogProp(w, r)
	if !ok {
		return
	}
	raw, ok := shippedCatalog(prop)
	if !ok {
		fail(w, http.StatusNotFound, "No shipped version.")
		return
	}
	if _, err := a.db.Exec(`INSERT INTO catalog_revisions (prop, rev, status, body, author_id, note) VALUES ($1, 0, 'draft', $2, $3, 'The version shipped with Flowa')
		ON CONFLICT (prop) WHERE status = 'draft' DO UPDATE SET body = EXCLUDED.body, author_id = EXCLUDED.author_id, created_at = now()`, prop, string(raw), u.ID); err != nil {
		a.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

/* ---------- link cards: Instagram, TikTok and YouTube ---------- */

// A link to a post on another site, as stored on a Flowa post.
type postLink struct {
	URL  string `json:"url"`  // canonical, without tracking parameters
	Site string `json:"site"` // instagram, tiktok or youtube
	Kind string `json:"kind"` // post, reel, video, short, or shortlink (not yet resolved)
	ID   string `json:"id"`
}

// linkCard is a postLink with what the server learned about it, as the feed shows it.
type linkCard struct {
	postLink
	Title  string `json:"title,omitempty"`
	Author string `json:"author,omitempty"`
	Thumb  string `json:"thumb,omitempty"` // a Flowa URL, never the site's
	Ratio  string `json:"ratio,omitempty"` // "9:16" for shorts and reels, else "16:9"
	Status string `json:"status"`          // pending, ok or failed
}

const maxLinks = 4

var (
	ytIDRE   = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
	igCodeRE = regexp.MustCompile(`^[A-Za-z0-9_-]{5,40}$`)
	ttIDRE   = regexp.MustCompile(`^[0-9]{8,25}$`)
	ttUserRE = regexp.MustCompile(`^@[A-Za-z0-9._]{1,40}$`)
	ttShort  = regexp.MustCompile(`^[A-Za-z0-9]{5,20}$`)
)

// parseLink recognises a link to an Instagram post or reel, a TikTok video, or a YouTube video or
// short, and returns it in canonical form. Anything else is refused.
func parseLink(raw string) (postLink, bool) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Port() != "" {
		return postLink{}, false
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	host = strings.TrimPrefix(host, "m.")
	parts := strings.FieldsFunc(u.Path, func(r rune) bool { return r == '/' })
	switch host {
	case "youtube.com", "music.youtube.com":
		id, kind := "", "video"
		switch {
		case len(parts) == 1 && parts[0] == "watch":
			id = u.Query().Get("v")
		case len(parts) == 2 && (parts[0] == "shorts" || parts[0] == "live" || parts[0] == "embed"):
			id = parts[1]
			if parts[0] == "shorts" {
				kind = "short"
			}
		}
		if !ytIDRE.MatchString(id) {
			return postLink{}, false
		}
		if kind == "short" {
			return postLink{"https://www.youtube.com/shorts/" + id, "youtube", kind, id}, true
		}
		return postLink{"https://www.youtube.com/watch?v=" + id, "youtube", kind, id}, true
	case "youtu.be":
		if len(parts) == 1 && ytIDRE.MatchString(parts[0]) {
			return postLink{"https://www.youtube.com/watch?v=" + parts[0], "youtube", "video", parts[0]}, true
		}
	case "instagram.com":
		// /p/CODE, /reel/CODE, /reels/CODE, /tv/CODE, optionally after the account name.
		if len(parts) >= 3 && parts[1] != "" && (parts[1] == "p" || parts[1] == "reel" || parts[1] == "reels" || parts[1] == "tv") {
			parts = parts[1:]
		}
		if len(parts) >= 2 && igCodeRE.MatchString(parts[1]) {
			switch parts[0] {
			case "p", "tv":
				return postLink{"https://www.instagram.com/p/" + parts[1] + "/", "instagram", "post", parts[1]}, true
			case "reel", "reels":
				return postLink{"https://www.instagram.com/reel/" + parts[1] + "/", "instagram", "reel", parts[1]}, true
			}
		}
	case "tiktok.com":
		if len(parts) == 3 && ttUserRE.MatchString(parts[0]) && parts[1] == "video" && ttIDRE.MatchString(parts[2]) {
			return postLink{"https://www.tiktok.com/" + parts[0] + "/video/" + parts[2], "tiktok", "video", parts[2]}, true
		}
		if len(parts) == 2 && parts[0] == "v" && ttIDRE.MatchString(strings.TrimSuffix(parts[1], ".html")) {
			id := strings.TrimSuffix(parts[1], ".html")
			return postLink{"https://m.tiktok.com/v/" + id + ".html", "tiktok", "video", id}, true
		}
		if len(parts) == 2 && parts[0] == "t" && ttShort.MatchString(parts[1]) {
			return postLink{"https://www.tiktok.com/t/" + parts[1] + "/", "tiktok", "shortlink", parts[1]}, true
		}
	case "vm.tiktok.com", "vt.tiktok.com":
		if len(parts) == 1 && ttShort.MatchString(parts[0]) {
			return postLink{"https://" + host + "/" + parts[0] + "/", "tiktok", "shortlink", parts[0]}, true
		}
	}
	return postLink{}, false
}

// The fetcher asks the three sites about a link from the server, never from a visitor's browser.
// It only talks to the hosts below, follows redirects only between them, refuses private addresses,
// gives up after 3 s and reads at most 256 KB of an answer (2 MB of a thumbnail).
var (
	previewHosts = []string{"youtube.com", "www.youtube.com", "tiktok.com", "www.tiktok.com", "m.tiktok.com", "vm.tiktok.com", "vt.tiktok.com", "instagram.com", "www.instagram.com"}
	thumbHosts   = []string{"ytimg.com", "tiktokcdn.com", "tiktokcdn-us.com", "cdninstagram.com", "fbcdn.net"}
)

const (
	fetchTimeout = 3 * time.Second
	maxMeta      = 256 << 10
	maxThumbIn   = 2 << 20
	maxThumbOut  = 60 << 10
	thumbSide    = 640
	refreshAfter = 7 * 24 * time.Hour
)

type fetcher struct {
	client *http.Client
	// base, set only by tests (FLOWA_PREVIEW_BASE), sends every request to one stub server instead.
	base string
	mu   sync.Mutex
	busy map[string]*sync.Mutex // one fetch at a time per host
	off  bool                   // FLOWA_PREVIEW_FETCH=off: never fetch
}

func hostAllowed(host string, list []string) bool {
	host = strings.ToLower(host)
	for _, h := range list {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}

// reservedNets are ranges that are not the public internet, beyond what net.IP's methods cover:
// shared address space (carrier-grade NAT and VPNs), IETF protocol assignments, benchmarking,
// "this network", the reserved class E block, and NAT64, which can lead back to a private IPv4.
var reservedNets = func() []*net.IPNet {
	out := []*net.IPNet{}
	for _, c := range []string{"100.64.0.0/10", "192.0.0.0/24", "198.18.0.0/15", "0.0.0.0/8", "240.0.0.0/4", "64:ff9b::/96", "64:ff9b:1::/48"} {
		_, n, _ := net.ParseCIDR(c)
		out = append(out, n)
	}
	return out
}()

func publicIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	for _, n := range reservedNets {
		if n.Contains(ip) {
			return false
		}
	}
	return true
}

func newFetcher(base string, allowPrivate bool) *fetcher {
	dialer := &net.Dialer{Timeout: fetchTimeout, Control: func(network, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		if ip := net.ParseIP(host); ip == nil || (!allowPrivate && !publicIP(ip)) {
			return fmt.Errorf("refusing to connect to %s", host)
		}
		return nil
	}}
	tr := &http.Transport{DialContext: dialer.DialContext, TLSHandshakeTimeout: fetchTimeout, ResponseHeaderTimeout: fetchTimeout, MaxIdleConns: 4, DisableCompression: false}
	f := &fetcher{base: strings.TrimRight(base, "/"), busy: map[string]*sync.Mutex{}}
	f.client = &http.Client{Transport: tr, Timeout: fetchTimeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("too many redirects")
		}
		host := req.URL.Hostname()
		if f.base != "" {
			// Tests: a redirect must stay on the stub, and name an allowed host as its first segment.
			if !strings.HasPrefix(req.URL.String(), f.base+"/") {
				return fmt.Errorf("redirect off the stub refused")
			}
			host, _, _ = strings.Cut(strings.TrimPrefix(req.URL.Path, "/"), "/")
		}
		if !hostAllowed(host, append(append([]string{}, previewHosts...), thumbHosts...)) {
			return fmt.Errorf("redirect to %s refused", host)
		}
		return nil
	}}
	return f
}

// get fetches an allowed URL and returns at most limit bytes of the body.
func (f *fetcher) get(ctx context.Context, target string, hosts []string, limit int64) ([]byte, *url.URL, error) {
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "https" || !hostAllowed(u.Hostname(), hosts) {
		return nil, nil, fmt.Errorf("not an allowed URL: %s", target)
	}
	reqURL := target
	if f.base != "" {
		// Tests: the stub serves /<host><path>?<query>.
		reqURL = f.base + "/" + u.Hostname() + u.EscapedPath()
		if u.RawQuery != "" {
			reqURL += "?" + u.RawQuery
		}
	}
	f.mu.Lock()
	lock := f.busy[u.Hostname()]
	if lock == nil {
		lock = &sync.Mutex{}
		f.busy[u.Hostname()] = lock
	}
	f.mu.Unlock()
	lock.Lock()
	defer lock.Unlock()
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", "Flowa link preview (+self-hosted)")
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, nil, fmt.Errorf("%s answered %d", u.Hostname(), resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, nil, err
	}
	if int64(len(body)) > limit {
		return nil, nil, fmt.Errorf("answer from %s is too large", u.Hostname())
	}
	return body, resp.Request.URL, nil
}

var (
	ogTitleRE = regexp.MustCompile(`<meta[^>]+property="og:title"[^>]+content="([^"]*)"`)
	ogImageRE = regexp.MustCompile(`<meta[^>]+property="og:image"[^>]+content="([^"]*)"`)
)

type previewMeta struct {
	Title, Author, Thumb string
}

// lookup asks the site about a link: its public oEmbed answer where there is one (YouTube, TikTok),
// otherwise the page's Open Graph tags (Instagram, which may answer with nothing without a login).
func (f *fetcher) lookup(ctx context.Context, l postLink) (previewMeta, postLink, error) {
	var m previewMeta
	if l.Kind == "shortlink" {
		// Follow the short link to the video it stands for, within the allowed hosts.
		_, final, err := f.get(ctx, l.URL, previewHosts, maxMeta)
		if err != nil {
			return m, l, err
		}
		target := final.String()
		if f.base != "" {
			target = "https://" + strings.TrimPrefix(final.Path, "/")
		}
		nl, ok := parseLink(target)
		if !ok || nl.Kind == "shortlink" {
			return m, l, fmt.Errorf("short link did not lead to a video")
		}
		l = nl
	}
	var endpoint string
	switch l.Site {
	case "youtube":
		endpoint = "https://www.youtube.com/oembed?format=json&url=" + url.QueryEscape(l.URL)
	case "tiktok":
		endpoint = "https://www.tiktok.com/oembed?url=" + url.QueryEscape(l.URL)
	case "instagram":
		body, _, err := f.get(ctx, l.URL, previewHosts, maxMeta)
		if err != nil {
			return m, l, err
		}
		if t := ogTitleRE.FindSubmatch(body); t != nil {
			m.Title = htmlUnescape(string(t[1]))
		}
		if t := ogImageRE.FindSubmatch(body); t != nil {
			m.Thumb = htmlUnescape(string(t[1]))
		}
		return m, l, nil
	}
	body, _, err := f.get(ctx, endpoint, previewHosts, maxMeta)
	if err != nil {
		return m, l, err
	}
	var o struct {
		Title        string `json:"title"`
		AuthorName   string `json:"author_name"`
		ThumbnailURL string `json:"thumbnail_url"`
	}
	if err := json.Unmarshal(body, &o); err != nil {
		return m, l, err
	}
	return previewMeta{o.Title, o.AuthorName, o.ThumbnailURL}, l, nil
}

func htmlUnescape(s string) string {
	return strings.NewReplacer("&amp;", "&", "&quot;", `"`, "&#39;", "'", "&#x27;", "'", "&lt;", "<", "&gt;", ">").Replace(s)
}

// shrinkThumb decodes a JPEG or PNG, scales it to at most 640 px on its longer side and re-encodes
// it as a JPEG of at most 60 KB, so what Flowa serves is its own small copy.
func shrinkThumb(raw []byte) ([]byte, int, int, error) {
	// The size first: a small file can declare a huge image, which would take all memory to decode.
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, 0, 0, err
	}
	if cfg.Width > 4096 || cfg.Height > 4096 {
		return nil, 0, 0, errors.New("image too large")
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, 0, 0, err
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return nil, 0, 0, errors.New("empty image")
	}
	scale := min(1, float64(thumbSide)/float64(max(w, h)))
	nw, nh := max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	// Box filter: each output pixel averages the source pixels it covers.
	for y := 0; y < nh; y++ {
		y0, y1 := b.Min.Y+y*h/nh, b.Min.Y+max((y+1)*h/nh, y*h/nh+1)
		for x := 0; x < nw; x++ {
			x0, x1 := b.Min.X+x*w/nw, b.Min.X+max((x+1)*w/nw, x*w/nw+1)
			var r, g, bl, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					cr, cg, cb, _ := src.At(sx, sy).RGBA()
					r, g, bl, n = r+uint64(cr), g+uint64(cg), bl+uint64(cb), n+1
				}
			}
			i := dst.PixOffset(x, y)
			dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2], dst.Pix[i+3] = uint8(r/n>>8), uint8(g/n>>8), uint8(bl/n>>8), 255
		}
	}
	for q := 82; q >= 35; q -= 12 {
		var out bytes.Buffer
		if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: q}); err != nil {
			return nil, 0, 0, err
		}
		if out.Len() <= maxThumbOut {
			return out.Bytes(), nw, nh, nil
		}
	}
	return nil, 0, 0, errors.New("thumbnail too large even at low quality")
}

// preview fetches and stores what the site says about a link, once; failures are stored too, so a
// broken link does not cause a fetch on every view. Runs in the background.
func (a *app) preview(l postLink) {
	if a.fetch == nil || a.fetch.off {
		return
	}
	// Claim the link in one statement: a new link, or one neither pending nor fetched recently.
	// Two feed loads at once cannot both claim it, so a link is fetched once at a time.
	var id int64
	err := a.db.QueryRow(`INSERT INTO link_previews (url, site, kind, status) VALUES ($1, $2, $3, 'pending')
		ON CONFLICT (url) DO UPDATE SET status = 'pending'
		WHERE link_previews.status <> 'pending' AND (link_previews.fetched_at IS NULL OR link_previews.fetched_at < now() - $4::interval)
		RETURNING id`, l.URL, l.Site, l.Kind, fmt.Sprintf("%d seconds", int(refreshAfter.Seconds()))).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return
	}
	if err != nil {
		log.Printf("preview: %v", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*fetchTimeout)
	defer cancel()
	m, resolved, err := a.fetch.lookup(ctx, l)
	var thumb []byte
	var tw, th int
	if err == nil && m.Thumb != "" {
		if raw, _, terr := a.fetch.get(ctx, m.Thumb, thumbHosts, maxThumbIn); terr == nil {
			thumb, tw, th, _ = shrinkThumb(raw)
		} else {
			log.Printf("preview thumbnail %s: %v", l.URL, terr)
		}
	}
	if err != nil {
		log.Printf("preview %s: %v", l.URL, err)
		a.db.Exec(`UPDATE link_previews SET status = 'failed', fetched_at = now() WHERE url = $1`, l.URL)
		return
	}
	trunc := func(s string, n int) string {
		if r := []rune(s); len(r) > n {
			return string(r[:n])
		}
		return s
	}
	if _, err := a.db.Exec(`UPDATE link_previews SET status = 'ok', title = $2, author = $3, thumb = $4, thumb_w = $5, thumb_h = $6, resolved = $7, fetched_at = now() WHERE url = $1`,
		l.URL, trunc(m.Title, 200), trunc(m.Author, 80), thumb, tw, th, resolved.URL); err != nil {
		log.Printf("preview: %v", err)
	}
}

// attachLinks gives each post's links the previews stored for them.
func (a *app) attachLinks(posts []*post) error {
	urls := []string{}
	for _, p := range posts {
		var d struct {
			Links []postLink `json:"links"`
		}
		json.Unmarshal(p.Data, &d)
		for _, l := range d.Links {
			p.Links = append(p.Links, linkCard{postLink: l, Status: "pending"})
			urls = append(urls, l.URL)
		}
	}
	if len(urls) == 0 {
		return nil
	}
	rows, err := a.db.Query(`SELECT id, url, status, title, author, thumb IS NOT NULL, thumb_w, thumb_h, fetched_at FROM link_previews WHERE url = ANY($1)`, pq.Array(urls))
	if err != nil {
		return err
	}
	defer rows.Close()
	type pv struct {
		id            int64
		status, t, au string
		thumb         bool
		w, h          int
		at            sql.NullTime
	}
	got := map[string]pv{}
	for rows.Next() {
		var x pv
		var u string
		if err := rows.Scan(&x.id, &u, &x.status, &x.t, &x.au, &x.thumb, &x.w, &x.h, &x.at); err != nil {
			return err
		}
		got[u] = x
	}
	for _, p := range posts {
		for i := range p.Links {
			c := &p.Links[i]
			c.Ratio = "16:9"
			if c.Kind == "short" || c.Kind == "reel" || c.Site == "tiktok" {
				c.Ratio = "9:16"
			}
			x, ok := got[c.URL]
			if !ok {
				continue
			}
			c.Status, c.Title, c.Author = x.status, x.t, x.au
			if x.thumb {
				c.Thumb = fmt.Sprintf("api/previews/%d.jpg", x.id)
			}
			if x.status == "ok" && x.at.Valid && time.Since(x.at.Time) > refreshAfter {
				go a.preview(c.postLink) // refreshed at most weekly, in the background
			}
		}
	}
	return rows.Err()
}

// handlePreviewImage serves a stored thumbnail, only while a post the viewer may see links to it.
func (a *app) handlePreviewImage(w http.ResponseWriter, r *http.Request, u *user) {
	id, _ := strconv.ParseInt(strings.TrimSuffix(r.PathValue("file"), ".jpg"), 10, 64)
	var img []byte
	err := a.db.QueryRow(`SELECT lp.thumb FROM link_previews lp WHERE lp.id = $1 AND lp.thumb IS NOT NULL AND EXISTS (
		SELECT 1 FROM posts p WHERE p.data->'links' @> jsonb_build_array(jsonb_build_object('url', lp.url)) AND (p.hidden_at IS NULL OR p.user_id = $2 OR $3))`,
		id, u.ID, can(u, permPostHide)).Scan(&img)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write(img)
}

// flagsCached reads the flags at most every two seconds, for the security header on every file.
func (a *app) flagsCached() map[string]bool {
	a.flagMu.Lock()
	defer a.flagMu.Unlock()
	if time.Since(a.flagsAt) < 2*time.Second && a.flagCache != nil {
		return a.flagCache
	}
	f, err := a.flags()
	if err != nil {
		return map[string]bool{}
	}
	a.flagCache, a.flagsAt = f, time.Now()
	return f
}

// csp is the Content-Security-Policy. The one exception to "self", a YouTube frame, exists only
// while the videoEmbeds flag is on.
func csp(embeds bool) string {
	p := "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
	if embeds {
		p += "; frame-src https://www.youtube-nocookie.com"
	}
	return p
}

/* ---------- next-trick suggestions: shared transition marks and usage ---------- */

// Transition marks are counted per pair of skills with no user attached. A browser that has the user's
// consent sends changes as they happen (from one mark to another), so a later change or a withdrawal
// of consent subtracts what it added, and no row ever says who marked what.

var transitionMarks = []string{"untested", "works", "bridge", "blocked"}

func (a *app) handleTransitionLabels(w http.ResponseWriter, r *http.Request, u *user) {
	// Without consent only withdrawals are taken: a browser takes its marks back out after the
	// profile has turned sharing off, so the change back to untested must still be accepted.
	sharing := a.pref(u, "shareTransitions", false)
	if !allowed(w, a.lim.marks, u, "suggestion updates") {
		return
	}
	var in struct {
		Prop   string `json:"prop"`
		Deltas []struct {
			A    string `json:"a"`
			B    string `json:"b"`
			From string `json:"from"`
			To   string `json:"to"`
		} `json:"deltas"`
	}
	if !readJSON(w, r, 64<<10, &in) {
		return
	}
	if !knownProp(in.Prop) || len(in.Deltas) > 500 {
		fail(w, http.StatusBadRequest, "Send up to 500 changes for one prop.")
		return
	}
	tx, err := a.db.Begin()
	if err != nil {
		a.serverError(w, err)
		return
	}
	defer tx.Rollback()
	for _, d := range in.Deltas {
		if !skillIDRE.MatchString(d.A) || !skillIDRE.MatchString(d.B) || !slices.Contains(transitionMarks, d.From) || !slices.Contains(transitionMarks, d.To) {
			fail(w, http.StatusBadRequest, "Each change names two skill ids and two marks.")
			return
		}
		if !sharing && d.To != "untested" {
			fail(w, http.StatusForbidden, "Sharing transition marks is off in your profile.")
			return
		}
		if d.From == d.To {
			continue
		}
		inc := func(col string, by int) error {
			if col == "untested" {
				return nil
			}
			_, err := tx.Exec(`INSERT INTO transition_counts (prop, a, b, `+col+`) VALUES ($1, $2, $3, GREATEST($4, 0))
				ON CONFLICT (prop, a, b) DO UPDATE SET `+col+` = GREATEST(transition_counts.`+col+` + $4, 0)`, in.Prop, d.A, d.B, by)
			return err
		}
		if err := inc(d.From, -1); err != nil {
			a.serverError(w, err)
			return
		}
		if err := inc(d.To, 1); err != nil {
			a.serverError(w, err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		a.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleTransitionCounts returns the shared counts for one prop, or for every prop to an admin
// exporting them for sim/fit-suggest.js.
func (a *app) handleTransitionCounts(w http.ResponseWriter, r *http.Request, u *user) {
	prop := r.URL.Query().Get("prop")
	if prop == "" && !can(u, permDashboard) {
		fail(w, http.StatusBadRequest, "Name a prop.")
		return
	}
	if prop != "" && !knownProp(prop) {
		fail(w, http.StatusNotFound, "Unknown prop.")
		return
	}
	rows, err := a.db.Query(`SELECT prop, a, b, works, bridge, blocked FROM transition_counts WHERE ($1 = '' OR prop = $1) AND works + bridge + blocked > 0 ORDER BY prop, a, b`, prop)
	if err != nil {
		a.serverError(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var p, x, y string
		var wk, br, bl int
		if err := rows.Scan(&p, &x, &y, &wk, &br, &bl); err != nil {
			a.serverError(w, err)
			return
		}
		out = append(out, map[string]any{"prop": p, "a": x, "b": y, "works": wk, "bridge": br, "blocked": bl})
	}
	writeJSON(w, http.StatusOK, map[string]any{"counts": out})
}

// handleSuggestUsage adds to this week's counts of suggestions shown, added to a combo, and later
// marked Works. No user is recorded.
func (a *app) handleSuggestUsage(w http.ResponseWriter, r *http.Request, u *user) {
	if !allowed(w, a.lim.marks, u, "suggestion updates") {
		return
	}
	var in struct {
		Shown, Added, Worked int
	}
	if !readJSON(w, r, 1024, &in) {
		return
	}
	if in.Shown < 0 || in.Added < 0 || in.Worked < 0 || in.Shown > 1000 || in.Added > 100 || in.Worked > 100 {
		fail(w, http.StatusBadRequest, "Counts out of range.")
		return
	}
	if _, err := a.db.Exec(`INSERT INTO suggest_usage (week, shown, added, worked) VALUES (date_trunc('week', now())::date, $1, $2, $3)
		ON CONFLICT (week) DO UPDATE SET shown = suggest_usage.shown + $1, added = suggest_usage.added + $2, worked = suggest_usage.worked + $3`, in.Shown, in.Added, in.Worked); err != nil {
		a.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

/* ---------- avatars ---------- */

// avatarDef is web/avatar-def.json: every option of the avatar editor, and the palettes colours come
// from. The editor reads the same file, so the server and the page cannot disagree about what exists.
type avatarDefT struct {
	Version   int                 `json:"version"`
	Options   map[string][]string `json:"options"`
	ColorKeys map[string]string   `json:"colorKeys"`
	Palettes  map[string][]string `json:"-"`
	Props     []string            `json:"propColors"`
}

var avatarDef = func() avatarDefT {
	raw, err := webFS.ReadFile("web/avatar-def.json")
	if err != nil {
		panic(err)
	}
	var d avatarDefT
	var all map[string]json.RawMessage
	if json.Unmarshal(raw, &d) != nil || json.Unmarshal(raw, &all) != nil {
		panic("web/avatar-def.json is not valid")
	}
	d.Palettes = map[string][]string{}
	for _, name := range d.ColorKeys {
		var list []string
		json.Unmarshal(all[name], &list)
		d.Palettes[name] = list
	}
	return d
}()

const maxAvatarCfg = 2048

// validAvatar accepts an avatar whose every field is a known option or a colour from its palette,
// or an empty object, which removes the avatar.
func validAvatar(raw json.RawMessage) error {
	if len(raw) > maxAvatarCfg {
		return errors.New("it is larger than 2 KB")
	}
	var c map[string]any
	if json.Unmarshal(raw, &c) != nil || c == nil {
		return errors.New("it is not an object")
	}
	if len(c) == 0 {
		return nil
	}
	if v, _ := c["v"].(float64); int(v) != avatarDef.Version {
		return fmt.Errorf("its version must be %d", avatarDef.Version)
	}
	for k, v := range c {
		switch {
		case k == "v":
		case k == "props":
			m, ok := v.(map[string]any)
			if !ok {
				return errors.New("props must map a prop to a colour")
			}
			for p, col := range m {
				s, _ := col.(string)
				if !knownProp(p) || !slices.Contains(avatarDef.Props, s) {
					return fmt.Errorf("prop colour %v for %s is not one of the prop colours", col, p)
				}
			}
		case avatarDef.Options[k] != nil:
			if s, _ := v.(string); !slices.Contains(avatarDef.Options[k], s) {
				return fmt.Errorf("%s must be one of %s", k, strings.Join(avatarDef.Options[k], ", "))
			}
		case avatarDef.ColorKeys[k] != "":
			if s, _ := v.(string); !slices.Contains(avatarDef.Palettes[avatarDef.ColorKeys[k]], s) {
				return fmt.Errorf("%s must be a colour from its palette", k)
			}
		default:
			return fmt.Errorf("there is no field %q", k)
		}
	}
	return nil
}

/* ---------- admin dashboard ---------- */

const dashWeeks = 12

// handleDashboard returns the numbers on the admin dashboard: totals, active users, and per week
// for the last 12 weeks (Monday to Sunday) the practice sessions logged, posts, comments and reports.
func (a *app) handleDashboard(w http.ResponseWriter, _ *http.Request, _ *user) {
	out := map[string]any{}
	var users, mods, admins, active7, active30, open int
	err := a.db.QueryRow(`SELECT count(*), count(*) FILTER (WHERE role = 'moderator'), count(*) FILTER (WHERE role = 'admin') FROM users`).Scan(&users, &mods, &admins)
	if err == nil {
		// Active: signed in or saved progress within the window.
		err = a.db.QueryRow(`WITH act AS (SELECT user_id, created_at AS t FROM sessions UNION ALL SELECT user_id, updated_at FROM progress)
			SELECT count(DISTINCT user_id) FILTER (WHERE t > now() - interval '7 days'), count(DISTINCT user_id) FILTER (WHERE t > now() - interval '30 days') FROM act`).Scan(&active7, &active30)
	}
	if err == nil {
		err = a.db.QueryRow(`SELECT count(DISTINCT (target_kind, target_id)) FROM reports WHERE status = 'open'`).Scan(&open)
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	out["totals"] = map[string]int{"users": users, "moderators": mods, "admins": admins, "active7": active7, "active30": active30, "openReports": open}
	rows, err := a.db.Query(`WITH weeks AS (SELECT generate_series(date_trunc('week', now()) - ($1 - 1) * interval '1 week', date_trunc('week', now()), interval '1 week') AS w),
		sess AS (SELECT CASE WHEN s->>'date' ~ '^\d{4}-\d{2}-\d{2}$' THEN date_trunc('week', (s->>'date')::date) END AS w
			FROM progress, jsonb_array_elements(CASE WHEN jsonb_typeof(state->'sessions') = 'array' THEN state->'sessions' ELSE '[]' END) s)
		SELECT weeks.w,
			(SELECT count(*) FROM sess WHERE sess.w = weeks.w),
			(SELECT count(*) FROM posts p WHERE date_trunc('week', p.created_at) = weeks.w),
			(SELECT count(*) FROM comments c WHERE date_trunc('week', c.created_at) = weeks.w),
			(SELECT count(*) FROM reports r WHERE date_trunc('week', r.created_at) = weeks.w)
		FROM weeks ORDER BY weeks.w`, dashWeeks)
	if err != nil {
		a.serverError(w, err)
		return
	}
	defer rows.Close()
	weeks := []map[string]any{}
	for rows.Next() {
		var wk time.Time
		var sessions, posts, comments, reports int
		if err := rows.Scan(&wk, &sessions, &posts, &comments, &reports); err != nil {
			a.serverError(w, err)
			return
		}
		weeks = append(weeks, map[string]any{"week": wk.Format("2006-01-02"), "sessions": sessions, "posts": posts, "comments": comments, "reports": reports})
	}
	if err := rows.Err(); err != nil {
		a.serverError(w, err)
		return
	}
	out["weeks"] = weeks
	// Suggestions over the same 12 weeks: how many were added to a combo, and how many of those later worked.
	var shown, added, worked int
	if err := a.db.QueryRow(`SELECT COALESCE(sum(shown), 0), COALESCE(sum(added), 0), COALESCE(sum(worked), 0) FROM suggest_usage WHERE week >= (date_trunc('week', now()) - ($1 - 1) * interval '1 week')::date`, dashWeeks).Scan(&shown, &added, &worked); err != nil {
		a.serverError(w, err)
		return
	}
	out["suggest"] = map[string]int{"shown": shown, "added": added, "worked": worked}
	writeJSON(w, http.StatusOK, out)
}

/* ---------- audit log ---------- */

type execer interface {
	Exec(string, ...any) (sql.Result, error)
}

// audit records one moderation or admin action. Every such handler calls it exactly once, inside
// the same transaction as the action where there is one, so an action cannot happen unlogged.
func (a *app) audit(x execer, actor int64, action, kind string, targetID, targetUser int64, reason string, detail map[string]any) error {
	if detail == nil {
		detail = map[string]any{}
	}
	d, _ := json.Marshal(detail)
	var tu any
	if targetUser != 0 {
		tu = targetUser
	}
	_, err := x.Exec(`INSERT INTO mod_actions (actor_id, action, target_kind, target_id, target_user_id, reason, detail) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		actor, action, kind, targetID, tu, reason, string(d))
	return err
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
	return db, migrate(db)
}

// migrations change the schema in order, each exactly once, and are recorded in schema_migrations.
// Append new ones; never edit or reorder one that has shipped. The first two are the schema as it
// stood before versioning, written with IF NOT EXISTS, so a database from before versioning runs
// them again harmlessly and is then recorded at version 2.
var migrations = []string{schema, schemaSocial,
	// 3: sessions also end when unused for sessionIdle.
	`ALTER TABLE sessions ADD COLUMN IF NOT EXISTS last_seen TIMESTAMPTZ NOT NULL DEFAULT now()`,
}

// migrate brings the database up to the latest migration. An advisory lock keeps two starting
// servers from migrating at once; each migration and its record commit together.
func migrate(db *sql.DB) error {
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock(7130651)`); err != nil {
		return err
	}
	defer conn.ExecContext(ctx, `SELECT pg_advisory_unlock(7130651)`)
	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	var have int
	if err := conn.QueryRowContext(ctx, `SELECT COALESCE(max(version), 0) FROM schema_migrations`).Scan(&have); err != nil {
		return err
	}
	for v := have + 1; v <= len(migrations); v++ {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[v-1]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", v, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version) VALUES ($1)`, v); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		if have > 0 {
			log.Printf("schema: applied migration %d", v)
		}
	}
	return nil
}

// housekeep removes rows nothing needs any more: expired sessions, notifications read and older
// than 180 days, and link previews no post refers to (fetched over a day ago, so a preview for a
// post being written now is kept). It returns how many rows each step removed.
func (a *app) housekeep() (map[string]int64, error) {
	steps := []struct{ name, sql string }{
		{"sessions", `DELETE FROM sessions WHERE expires_at < now() OR last_seen < now() - interval '` + sessionIdle + `'`},
		{"notifications", `DELETE FROM notifications WHERE read AND created_at < now() - interval '180 days'`},
		{"previews", `DELETE FROM link_previews lp WHERE COALESCE(lp.fetched_at, now() - interval '2 days') < now() - interval '1 day'
			AND NOT EXISTS (SELECT 1 FROM posts p WHERE p.data->'links' @> jsonb_build_array(jsonb_build_object('url', lp.url)))`},
	}
	out := map[string]int64{}
	for _, st := range steps {
		res, err := a.db.Exec(st.sql)
		if err != nil {
			return out, fmt.Errorf("housekeeping %s: %w", st.name, err)
		}
		out[st.name], _ = res.RowsAffected()
	}
	return out, nil
}

// housekeeping runs housekeep at start and then every interval until ctx ends.
func (a *app) housekeeping(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if n, err := a.housekeep(); err != nil {
			log.Print(err)
		} else if n["sessions"]+n["notifications"]+n["previews"] > 0 {
			log.Printf("housekeeping: removed %d expired sessions, %d old notifications, %d unused link previews", n["sessions"], n["notifications"], n["previews"])
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
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

func (a *app) handleStatic(assets map[string]asset) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == "/" {
			p = "/index.html"
		}
		f, ok := assets[p]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", f.ctype)
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("ETag", f.etag)
		w.Header().Set("Content-Security-Policy", csp(a.flagsCached()["videoEmbeds"]))
		if r.Header.Get("If-None-Match") == f.etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Write(f.body)
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
	mux.HandleFunc("PUT /api/me", a.authed("", a.handlePutMe))
	mux.HandleFunc("PUT /api/me/password", a.authed("", a.handlePassword))
	mux.HandleFunc("POST /api/me/signout-others", a.authed("", a.handleSignoutOthers))
	mux.HandleFunc("POST /api/signup", a.handleSignup)
	mux.HandleFunc("POST /api/signin", a.handleSignin)
	mux.HandleFunc("POST /api/signout", a.handleSignout)
	mux.HandleFunc("GET /api/progress/{prop}", a.authed("", a.handleGetProgress))
	mux.HandleFunc("PUT /api/progress/{prop}", a.authed("", a.handlePutProgress))
	mux.HandleFunc("PUT /api/flags", a.authed(permFlagsEdit, a.handlePutFlags))
	mux.HandleFunc("GET /api/admin/users", a.authed(permDashboard, a.handleUsers))
	mux.HandleFunc("DELETE /api/admin/users/{id}", a.authed(permUserManage, a.handleDeleteUser))
	mux.HandleFunc("PUT /api/admin/users/{id}/password", a.authed(permUserManage, a.handleResetPassword))
	mux.HandleFunc("PUT /api/admin/users/{id}/role", a.authed(permUserRole, a.handleSetRole))
	mux.HandleFunc("GET /api/admin/audit", a.authed(permAudit, a.handleAudit))
	mux.HandleFunc("GET /api/admin/dashboard", a.authed(permDashboard, a.handleDashboard))
	mux.HandleFunc("GET /catalog/{file}", a.handleCatalogFile)
	mux.HandleFunc("POST /api/suggest/labels", a.flagged("suggest", a.authed("", a.handleTransitionLabels)))
	mux.HandleFunc("GET /api/suggest/counts", a.flagged("suggest", a.authed("", a.handleTransitionCounts)))
	mux.HandleFunc("POST /api/suggest/usage", a.flagged("suggest", a.authed("", a.handleSuggestUsage)))
	ed := func(perm string, h func(http.ResponseWriter, *http.Request, *user)) http.HandlerFunc {
		return a.flagged("catalogEditor", a.authed(perm, h))
	}
	mux.HandleFunc("GET /api/admin/catalog/{prop}", ed(permCatalogEdit, a.handleCatalogAdmin))
	mux.HandleFunc("PUT /api/admin/catalog/{prop}/draft", ed(permCatalogEdit, a.handleCatalogDraft))
	mux.HandleFunc("DELETE /api/admin/catalog/{prop}/draft", ed(permCatalogEdit, a.handleCatalogDiscard))
	mux.HandleFunc("GET /api/admin/catalog/{prop}/diff", ed(permCatalogEdit, a.handleCatalogDiff))
	mux.HandleFunc("POST /api/admin/catalog/{prop}/publish", ed(permCatalogPublish, a.handleCatalogPublish))
	mux.HandleFunc("POST /api/admin/catalog/{prop}/shipped", ed(permCatalogEdit, a.handleCatalogShipped))
	mux.HandleFunc("GET /api/admin/catalog/{prop}/revisions/{rev}", ed(permCatalogEdit, a.handleCatalogExport))
	mux.HandleFunc("POST /api/admin/catalog/{prop}/revisions/{rev}/restore", ed(permCatalogPublish, a.handleCatalogRestore))
	mux.HandleFunc("POST /api/reports", a.flagged("moderation", a.authed("", a.handleReport)))
	mux.HandleFunc("GET /api/mod/queue", a.flagged("moderation", a.authed(permReportResolve, a.handleQueue)))
	mux.HandleFunc("POST /api/mod/resolve", a.flagged("moderation", a.authed(permReportResolve, a.handleResolve)))
	mux.HandleFunc("POST /api/mod/actions", a.flagged("moderation", a.authed("", a.handleModAction)))
	a.socialRoutes(mux)
	mux.HandleFunc("GET /api/previews/{file}", a.authed("", a.handlePreviewImage))
	mux.HandleFunc("GET /", a.handleStatic(assets))
	return a.secure(guard(mux))
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
	a := newApp(db, *dataDir)
	if v, ok := os.LookupEnv("FLOWA_TRUSTED_PROXIES"); ok && strings.TrimSpace(v) != "" {
		p, err := parseProxies(v)
		if err != nil {
			log.Fatal(err)
		}
		a.proxies = p
	}
	log.Printf("visitor addresses: forwarding headers believed from %d trusted proxy ranges", len(a.proxies))
	for _, h := range strings.Split(os.Getenv("FLOWA_HOSTS"), ",") {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			a.hosts = append(a.hosts, h)
		}
	}
	// FLOWA_PREVIEW_BASE points the fetcher at a stub server in tests; FLOWA_PREVIEW_FETCH=off disables it.
	a.fetch = newFetcher(os.Getenv("FLOWA_PREVIEW_BASE"), os.Getenv("FLOWA_PREVIEW_BASE") != "")
	a.fetch.off = os.Getenv("FLOWA_PREVIEW_FETCH") == "off"
	if _, err := a.seedAdmin(os.Getenv("ADMIN_USER"), os.Getenv("ADMIN_PASSWORD")); err != nil {
		log.Fatal(err)
	}
	if err := a.seedCatalogs(); err != nil {
		log.Fatal(err)
	}

	assets, err := loadAssets()
	if err != nil {
		log.Fatal(err)
	}

	srv := &http.Server{Addr: *listen, Handler: a.routes(assets), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 120 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go a.housekeeping(ctx, time.Hour)
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
ALTER TABLE notifications ADD COLUMN IF NOT EXISTS comment_id BIGINT REFERENCES comments(id) ON DELETE CASCADE;
CREATE TABLE IF NOT EXISTS mod_actions (
	id BIGSERIAL PRIMARY KEY,
	actor_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
	action TEXT NOT NULL,
	target_kind TEXT NOT NULL DEFAULT '',
	target_id BIGINT NOT NULL DEFAULT 0,
	target_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
	reason TEXT NOT NULL DEFAULT '',
	detail JSONB NOT NULL DEFAULT '{}',
	created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS mod_actions_time ON mod_actions (id DESC);
ALTER TABLE users ADD COLUMN IF NOT EXISTS muted_until TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN IF NOT EXISTS mute_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS suspended_until TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN IF NOT EXISTS suspend_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS stats_hidden BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE users ADD COLUMN IF NOT EXISTS avatar_cfg JSONB NOT NULL DEFAULT '{}';
ALTER TABLE posts ADD COLUMN IF NOT EXISTS hidden_at TIMESTAMPTZ;
ALTER TABLE posts ADD COLUMN IF NOT EXISTS hidden_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE comments ADD COLUMN IF NOT EXISTS hidden_at TIMESTAMPTZ;
ALTER TABLE comments ADD COLUMN IF NOT EXISTS hidden_reason TEXT NOT NULL DEFAULT '';
CREATE TABLE IF NOT EXISTS reports (
	id BIGSERIAL PRIMARY KEY,
	reporter_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	target_kind TEXT NOT NULL,
	target_id BIGINT NOT NULL,
	reason TEXT NOT NULL,
	note TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'open',
	resolver_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
	resolution TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	resolved_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS reports_open_once ON reports (reporter_id, target_kind, target_id) WHERE status = 'open';
CREATE INDEX IF NOT EXISTS reports_status ON reports (status, id);
CREATE TABLE IF NOT EXISTS catalog_revisions (
	id BIGSERIAL PRIMARY KEY,
	prop TEXT NOT NULL,
	rev INT NOT NULL,
	status TEXT NOT NULL,
	body JSONB NOT NULL,
	source TEXT NOT NULL DEFAULT 'editor',
	shipped_hash TEXT NOT NULL DEFAULT '',
	author_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
	note TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS catalog_published ON catalog_revisions (prop, rev) WHERE status = 'published';
CREATE UNIQUE INDEX IF NOT EXISTS catalog_draft ON catalog_revisions (prop) WHERE status = 'draft';
CREATE TABLE IF NOT EXISTS link_previews (
	id BIGSERIAL PRIMARY KEY,
	url TEXT NOT NULL UNIQUE,
	site TEXT NOT NULL,
	kind TEXT NOT NULL,
	status TEXT NOT NULL DEFAULT 'pending',
	title TEXT NOT NULL DEFAULT '',
	author TEXT NOT NULL DEFAULT '',
	thumb BYTEA,
	thumb_w INT NOT NULL DEFAULT 0,
	thumb_h INT NOT NULL DEFAULT 0,
	resolved TEXT NOT NULL DEFAULT '',
	fetched_at TIMESTAMPTZ
);
CREATE TABLE IF NOT EXISTS transition_counts (
	prop TEXT NOT NULL,
	a TEXT NOT NULL,
	b TEXT NOT NULL,
	works INT NOT NULL DEFAULT 0,
	bridge INT NOT NULL DEFAULT 0,
	blocked INT NOT NULL DEFAULT 0,
	PRIMARY KEY (prop, a, b)
);
CREATE TABLE IF NOT EXISTS suggest_usage (
	week DATE PRIMARY KEY,
	shown INT NOT NULL DEFAULT 0,
	added INT NOT NULL DEFAULT 0,
	worked INT NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS badge_blocks (
	user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	key TEXT NOT NULL,
	PRIMARY KEY (user_id, key)
);`

const (
	maxBio       = 300
	maxPost      = 2000
	maxComment   = 1000
	maxImage     = 400000 // bytes of data URL; the client sends a resized JPEG
	maxAvatar    = 120000 // the same for a profile picture
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
// maxMentions is how many people one post or comment can notify; further names stay plain text.
const maxMentions = 10

func mentions(text string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, m := range mentionRE.FindAllStringSubmatch(strings.ToLower(text), -1) {
		if !seen[m[1]] && len(out) < maxMentions {
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

// cleanImage checks a picture sent as a data URL and encodes it again: JPEG or PNG only, at most
// maxSide pixels a side, and at most maxLen characters as a data URL. Encoding it again keeps only
// the pixels, so whatever else the file carried (the location a phone camera records, anything
// hidden after the image data) is gone. An empty string stays empty.
func cleanImage(v string, maxSide, maxLen int) (string, error) {
	if v == "" {
		return "", nil
	}
	kind := ""
	for _, k := range []string{"jpeg", "png"} {
		if strings.HasPrefix(v, "data:image/"+k+";base64,") {
			kind = k
		}
	}
	if kind == "" {
		return "", errors.New("it must be a JPEG or PNG")
	}
	if len(v) > maxLen*2 {
		return "", errors.New("it is too large")
	}
	raw, err := base64.StdEncoding.DecodeString(v[strings.IndexByte(v, ',')+1:])
	if err != nil {
		return "", errors.New("it is not valid base64")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || format != kind {
		return "", errors.New("it is not a readable " + strings.ToUpper(kind))
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > maxSide || cfg.Height > maxSide {
		return "", fmt.Errorf("it must be at most %d pixels on each side", maxSide)
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return "", errors.New("it is not a readable " + strings.ToUpper(kind))
	}
	for _, q := range []int{88, 78, 68} {
		var buf bytes.Buffer
		if kind == "png" {
			err = png.Encode(&buf, img)
		} else {
			err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: q})
		}
		if err != nil {
			return "", err
		}
		out := "data:image/" + kind + ";base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
		if len(out) <= maxLen {
			return out, nil
		}
		if kind == "png" {
			break
		}
	}
	return "", errors.New("it is too large")
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
	AvatarCfg   json.RawMessage `json:"avatarCfg"`
}

func (u *user) public() pubUser {
	if u.StatsHidden {
		v := *u
		v.Stats = json.RawMessage("{}")
		u = &v
	}
	return pubUser{u.ID, u.Username, u.DisplayName, u.Role, u.avatarURL(), u.Bio, u.Links, u.Props, u.Stats, u.Created, u.AvatarCfg}
}

// avatarURL is where someone else's profile picture is fetched from, with a version that changes
// with the picture. Lists and feeds carry this short URL instead of the whole picture.
func (u *user) avatarURL() string {
	if u.Avatar == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(u.Avatar))
	return fmt.Sprintf("api/users/%d/avatar?v=%x", u.ID, sum[:6])
}

// handleAvatar serves a profile picture. The URL carries a version, so it can be cached.
func (a *app) handleAvatar(w http.ResponseWriter, r *http.Request, _ *user) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var v string
	err := a.db.QueryRow(`SELECT avatar FROM users WHERE id = $1`, id).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && v == "") {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	comma := strings.IndexByte(v, ',')
	raw, err := base64.StdEncoding.DecodeString(v[comma+1:])
	if comma < 0 || err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", strings.TrimSuffix(strings.TrimPrefix(v[:comma], "data:"), ";base64"))
	w.Header().Set("Cache-Control", "private, max-age=604800")
	w.Write(raw)
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
// handleUserList is the people list: the 100 members with the most XP (hidden stats count as none).
func (a *app) handleUserList(w http.ResponseWriter, _ *http.Request, _ *user) {
	rows, err := a.db.Query(`SELECT ` + userCols + ` FROM users u
		ORDER BY CASE WHEN NOT u.stats_hidden AND jsonb_typeof(u.stats->'xp') = 'number' THEN (u.stats->>'xp')::numeric ELSE 0 END DESC, u.id LIMIT 100`)
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

func (a *app) handleUserProfile(w http.ResponseWriter, r *http.Request, viewer *user) {
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
	if err := a.db.QueryRow(`SELECT count(*) FROM posts WHERE user_id = $1 AND (hidden_at IS NULL OR $1 = $2 OR $3)`, u.ID, viewer.ID, can(viewer, permPostHide)).Scan(&posts); err != nil {
		a.serverError(w, err)
		return
	}
	if err := a.db.QueryRow(`SELECT count(*) FROM likes l JOIN posts p ON p.id = l.post_id WHERE p.user_id = $1`, u.ID).Scan(&likes); err != nil {
		a.serverError(w, err)
		return
	}
	out := map[string]any{"user": u.public(), "achievements": ach, "posts": posts, "likes": likes}
	// With avatars on, the skills this person has clean, so their avatar can play one on their profile.
	if a.flagsCached()["avatars"] && !u.StatsHidden {
		clean, err := a.cleanSkills(u.ID)
		if err != nil {
			a.serverError(w, err)
			return
		}
		out["clean"] = clean
	}
	writeJSON(w, http.StatusOK, out)
}

// cleanSkills lists, per prop, the skills a user has at level 2 (clean) or above in any variant,
// read from their progress documents (state.skills: skill id -> variant -> level).
func (a *app) cleanSkills(id int64) (map[string][]string, error) {
	rows, err := a.db.Query(`SELECT prop, state->'skills' FROM progress WHERE user_id = $1 ORDER BY prop`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var prop string
		var raw []byte
		if err := rows.Scan(&prop, &raw); err != nil {
			return nil, err
		}
		var skills map[string]map[string]any
		if json.Unmarshal(raw, &skills) != nil {
			continue // a document without skills, or with an unexpected shape, lists none
		}
		ids := []string{}
		for sid, rec := range skills {
			if !skillIDRE.MatchString(sid) {
				continue
			}
			for _, v := range rec {
				if n, ok := v.(float64); ok && n >= 2 {
					ids = append(ids, sid)
					break
				}
			}
		}
		slices.Sort(ids)
		if len(ids) > 200 {
			ids = ids[:200]
		}
		out[prop] = ids
	}
	return out, rows.Err()
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
// badgeKeys are the badges the page awards (the registry in web/game.js); test/badges.js keeps the
// two lists equal. Anything else is refused, so a client cannot invent badges.
var badgeKeys = map[string]bool{
	"sessions-1":   true,
	"sessions-10":  true,
	"sessions-50":  true,
	"sessions-200": true,
	"hours-10":     true,
	"hours-50":     true,
	"hours-100":    true,
	"streak-3":     true,
	"streak-7":     true,
	"streak-30":    true,
	"streak-100":   true,
	"challenges-1": true,
	"challenges-4": true,
	"clean-1":      true,
	"clean-10":     true,
	"clean-50":     true,
	"clean-100":    true,
	"both-5":       true,
	"both-20":      true,
	"gates-1":      true,
	"gates-5":      true,
	"runs-10":      true,
	"runs-50":      true,
	"props-2":      true,
	"week-5":       true,
	"perf-1":       true,
	"notes-10":     true,
	"combo-1":      true,
	"combo-ready":  true,
	"first-post":   true,
	"share-streak": true}

func (a *app) handleAchievements(w http.ResponseWriter, r *http.Request, u *user) {
	if muted(w, u) || !allowed(w, a.lim.badges, u, "badge updates") {
		return
	}
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
		if !badgeKeys[k] {
			fail(w, http.StatusBadRequest, "There is no badge called "+strconv.Quote(k)+".")
			return
		}
		var got string
		err := a.db.QueryRow(`INSERT INTO achievements (user_id, key) SELECT $1, $2 WHERE NOT EXISTS (SELECT 1 FROM badge_blocks WHERE user_id = $1 AND key = $2)
			ON CONFLICT DO NOTHING RETURNING key`, u.ID, k).Scan(&got)
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
	out := map[string]any{"items": items, "unread": unread, "users": users}
	if can(u, permReportResolve) {
		// Moderators see how many reports wait, beside the menu entry.
		var queue int
		if err := a.db.QueryRow(`SELECT count(DISTINCT (target_kind, target_id)) FROM reports WHERE status = 'open'`).Scan(&queue); err != nil {
			a.serverError(w, err)
			return
		}
		out["queue"] = queue
	}
	writeJSON(w, http.StatusOK, out)
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
	Removed    bool      `json:"removed,omitempty"` // a placeholder for a comment a moderator hid
	Hidden     string    `json:"hidden,omitempty"`  // the reason, for its author and moderators
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
	// Hidden is the moderator's reason when the post is hidden; only its author and moderators see it.
	Hidden string `json:"hidden,omitempty"`
	// Links are the Instagram, TikTok and YouTube links on the post, with their previews.
	Links []linkCard `json:"links,omitempty"`
}

// loadPosts reads a page of posts newest first, with like counts, whether the viewer
// liked each one, every comment, and the users involved.
func (a *app) loadPosts(viewer *user, before, byUser, only int64) (map[string]any, error) {
	// Hidden posts are seen only by their author and by those who may hide posts.
	rows, err := a.db.Query(`SELECT p.id, p.user_id, p.kind, p.body, p.prop, p.skill, p.image, p.data, p.created_at,
		(SELECT count(*) FROM likes l WHERE l.post_id = p.id), EXISTS (SELECT 1 FROM likes l WHERE l.post_id = p.id AND l.user_id = $1),
		(SELECT count(*) FROM comments c WHERE c.post_id = p.id AND NOT c.deleted AND c.hidden_at IS NULL), p.hidden_reason
		FROM posts p WHERE ($2 = 0 OR p.id < $2) AND ($3 = 0 OR p.user_id = $3) AND ($4 = 0 OR p.id = $4)
		AND (p.hidden_at IS NULL OR p.user_id = $1 OR $6) ORDER BY p.id DESC LIMIT $5`,
		viewer.ID, before, byUser, only, pageSize+1, can(viewer, permPostHide))
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
		if err := rows.Scan(&p.ID, &p.UserID, &p.Kind, &p.Body, &p.Prop, &p.Skill, &p.Image, &data, &p.Created, &p.Likes, &p.Liked, &p.CommentCount, &p.Hidden); err != nil {
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
	if err := a.attachLinks(posts); err != nil {
		return nil, err
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
	out, err := a.loadPosts(u, queryID(r, "before"), byUser, 0)
	if err != nil {
		a.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *app) writePost(w http.ResponseWriter, viewer *user, id int64) {
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
	a.writePost(w, u, id)
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

// muted answers the request when the user is muted, and reports whether they were.
func muted(w http.ResponseWriter, u *user) bool {
	if u.MutedUntil == nil {
		return false
	}
	msg := "You are muted until " + u.MutedUntil.UTC().Format("2 January 2006, 15:04 UTC") + ", so you cannot post or comment."
	if u.MuteReason != "" {
		msg += " Reason: " + u.MuteReason
	}
	fail(w, http.StatusForbidden, msg)
	return true
}

func (a *app) handleCreatePost(w http.ResponseWriter, r *http.Request, u *user) {
	if muted(w, u) || !allowed(w, a.lim.posts, u, "posts") {
		return
	}
	var in struct {
		Body  string     `json:"body"`
		Image string     `json:"image"`
		Tags  []postTag  `json:"tags"`
		Combo *postCombo `json:"combo"`
		Links []string   `json:"links"`
	}
	if !readJSON(w, r, maxImage+4*maxPost+8192, &in) {
		return
	}
	body := strings.TrimSpace(in.Body)
	if len([]rune(body)) > maxPost {
		fail(w, http.StatusBadRequest, "Keep a post under 2000 characters.")
		return
	}
	img, err := cleanImage(in.Image, 2048, maxImage)
	if err != nil {
		fail(w, http.StatusBadRequest, "That picture cannot be used: "+err.Error()+".")
		return
	}
	in.Image = img
	tags, ok := cleanTags(in.Tags)
	if !ok {
		fail(w, http.StatusBadRequest, "Tag up to 10 tricks from your props.")
		return
	}
	if in.Combo != nil && !cleanCombo(in.Combo) {
		fail(w, http.StatusBadRequest, "That combo cannot be attached.")
		return
	}
	// Links become cards only while the linkCards flag is on; otherwise they stay in the text.
	links := []postLink{}
	if len(in.Links) > 0 {
		if !a.flagsCached()["linkCards"] {
			in.Links = nil
		}
		if len(in.Links) > maxLinks {
			fail(w, http.StatusBadRequest, "Attach up to 4 links.")
			return
		}
		seen := map[string]bool{}
		for _, raw := range in.Links {
			l, ok := parseLink(raw)
			if !ok {
				fail(w, http.StatusBadRequest, "Links can be Instagram posts and reels, TikTok videos, and YouTube videos and shorts.")
				return
			}
			if !seen[l.URL] {
				seen[l.URL] = true
				links = append(links, l)
			}
		}
	}
	if body == "" && in.Image == "" && in.Combo == nil && len(links) == 0 {
		fail(w, http.StatusBadRequest, "Write something, add a picture or attach a combo.")
		return
	}
	data := map[string]any{}
	if len(links) > 0 {
		data["links"] = links
	}
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
	for _, l := range links {
		go a.preview(l) // in the background: posting never waits for another site
	}
	a.writePost(w, u, id)
}

func (a *app) handleDeletePost(w http.ResponseWriter, r *http.Request, u *user) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var author int64
	// Authors delete their own posts; deleting someone else's needs the permission and a higher role.
	err := a.db.QueryRow(`DELETE FROM posts p USING users au WHERE p.id = $1 AND au.id = p.user_id
		AND (p.user_id = $2 OR ($3 AND au.role = ANY($4))) RETURNING p.user_id`, id, u.ID, can(u, permContentDelete), pq.Array(below(u.Role))).Scan(&author)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "No such post, or it is not yours.")
		return
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	if author != u.ID {
		if err := a.audit(a.db, u.ID, "delete_post", "post", id, author, "", nil); err != nil {
			a.serverError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// postOwner finds who wrote a post the viewer may see, answering the request itself when it
// cannot. A hidden post is "gone" to everyone but its author and moderators.
func (a *app) postOwner(w http.ResponseWriter, id int64, viewer *user) (int64, bool) {
	var owner int64
	err := a.db.QueryRow(`SELECT user_id FROM posts WHERE id = $1 AND (hidden_at IS NULL OR user_id = $2 OR $3)`, id, viewer.ID, can(viewer, permPostHide)).Scan(&owner)
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
	if !allowed(w, a.lim.likes, u, "likes") {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	owner, ok := a.postOwner(w, id, u)
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
	(SELECT count(*) FROM comments r WHERE r.parent_id = c.id AND r.hidden_at IS NULL), COALESCE(c.hidden_reason, ''), c.hidden_at IS NOT NULL`

// queryComments runs inner, a query over the comments table whose first parameter is $2, and
// returns its rows oldest first, with likes and reply counts as seen by viewer ($1).
// A hidden comment is shown in full to its author and moderators; to everyone else it is left
// out, or kept as an empty placeholder when it has replies that are still shown.
func (a *app) queryComments(viewer *user, inner string, args ...any) ([]*comment, error) {
	rows, err := a.db.Query(`SELECT `+commentCols+` FROM (`+inner+`) c ORDER BY c.id`, append([]any{viewer.ID}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*comment{}
	for rows.Next() {
		c := &comment{}
		var reason string
		var hidden bool
		if err := rows.Scan(&c.ID, &c.PostID, &c.UserID, &c.ParentID, &c.Body, &c.Created, &c.Edited, &c.Deleted, &c.Likes, &c.Liked, &c.ReplyCount, &reason, &hidden); err != nil {
			return nil, err
		}
		if hidden {
			if c.UserID == viewer.ID || can(viewer, permPostHide) {
				c.Hidden = reason
			} else if c.ReplyCount > 0 {
				c.Deleted, c.Removed = true, true
			} else {
				continue
			}
		}
		if c.Deleted {
			c.Body, c.Likes, c.Liked, c.Edited = "", 0, false, false
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// attachReplies gives each top-level comment its newest replies, oldest first.
func (a *app) attachReplies(viewer *user, roots []*comment) error {
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
	if _, ok := a.postOwner(w, id, u); !ok {
		return
	}
	list, err := a.queryComments(u, `SELECT * FROM comments WHERE post_id = $2 AND parent_id IS NULL AND id > $3 ORDER BY id LIMIT $4`, id, queryID(r, "after"), commentPage+1)
	if err == nil {
		err = a.attachReplies(u, list)
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

func (a *app) commentRow(w http.ResponseWriter, id int64, viewer *user) (commentRow, bool) {
	var c commentRow
	var secs float64
	err := a.db.QueryRow(`SELECT c.post_id, c.user_id, COALESCE(c.parent_id, 0), p.user_id, c.deleted, extract(epoch FROM now() - c.created_at), c.body
		FROM comments c JOIN posts p ON p.id = c.post_id WHERE c.id = $1
		AND (c.hidden_at IS NULL OR c.user_id = $2 OR $3) AND (p.hidden_at IS NULL OR p.user_id = $2 OR $3)`, id, viewer.ID, can(viewer, permPostHide)).Scan(&c.postID, &c.userID, &c.parentID, &c.postOwner, &c.deleted, &secs, &c.body)
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
	c, ok := a.commentRow(w, id, u)
	if !ok {
		return
	}
	if c.parentID != 0 {
		id = c.parentID
	}
	list, err := a.queryComments(u, `SELECT * FROM comments WHERE parent_id = $2 AND id > $3 ORDER BY id LIMIT $4`, id, queryID(r, "after"), replyPage+1)
	if err != nil {
		a.serverError(w, err)
		return
	}
	a.writeComments(w, list, replyPage, nil)
}

// handleThread returns the whole thread a comment belongs to, for a notification to open.
func (a *app) handleThread(w http.ResponseWriter, r *http.Request, u *user) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	c, ok := a.commentRow(w, id, u)
	if !ok {
		return
	}
	root := id
	if c.parentID != 0 {
		root = c.parentID
	}
	roots, err := a.queryComments(u, `SELECT * FROM comments WHERE id = $2`, root)
	if err != nil || len(roots) == 0 {
		a.serverError(w, fmt.Errorf("thread %d: %v", root, err))
		return
	}
	reps, err := a.queryComments(u, `SELECT * FROM comments WHERE parent_id = $2 ORDER BY id LIMIT $3`, root, maxThread)
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
	if muted(w, u) || !allowed(w, a.lim.comments, u, "comments") {
		return
	}
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
	owner, ok := a.postOwner(w, id, u)
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
		var deleted, hidden bool
		err := tx.QueryRow(`SELECT post_id, user_id, COALESCE(parent_id, 0), deleted, hidden_at IS NOT NULL FROM comments WHERE id = $1 FOR SHARE`, in.ParentID).Scan(&postID, &answered, &parent, &deleted, &hidden)
		if err == nil && hidden && answered != u.ID && !can(u, permPostHide) {
			err = sql.ErrNoRows
		}
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
	out, err := a.loadPosts(u, 0, 0, id)
	if err != nil {
		a.serverError(w, err)
		return
	}
	list, err := a.queryComments(u, `SELECT * FROM comments WHERE id = $2`, cid)
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
	if muted(w, u) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var in struct {
		Body string `json:"body"`
	}
	if !readJSON(w, r, 8192, &in) {
		return
	}
	c, ok := a.commentRow(w, id, u)
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
	list, err := a.queryComments(u, `SELECT * FROM comments WHERE id = $2`, id)
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
	var authorRole string
	err = tx.QueryRow(`SELECT c.user_id, p.user_id, COALESCE(c.parent_id, 0), au.role FROM comments c JOIN posts p ON p.id = c.post_id JOIN users au ON au.id = c.user_id
		WHERE c.id = $1 FOR UPDATE OF c`, id).Scan(&author, &owner, &parent, &authorRole)
	// The author and the post's owner may delete; anyone else needs the permission and a higher role.
	if err == nil && author != u.ID && owner != u.ID && !(can(u, permContentDelete) && outranks(u.Role, authorRole)) {
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
	if err == nil && author != u.ID && owner != u.ID {
		err = a.audit(tx, u.ID, "delete_comment", "comment", id, author, "", nil)
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
	if !allowed(w, a.lim.likes, u, "likes") {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	c, ok := a.commentRow(w, id, u)
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
	mux.HandleFunc("GET /api/users", a.authed("", a.handleUserList))
	mux.HandleFunc("GET /api/users/{id}/avatar", a.authed("", a.handleAvatar))
	mux.HandleFunc("GET /api/users/{username}", a.authed("", a.handleUserProfile))
	mux.HandleFunc("GET /api/posts", a.authed("", a.handleGetPosts))
	mux.HandleFunc("POST /api/posts", a.authed("", a.handleCreatePost))
	mux.HandleFunc("GET /api/posts/{id}", a.authed("", a.handleGetPost))
	mux.HandleFunc("DELETE /api/posts/{id}", a.authed("", a.handleDeletePost))
	mux.HandleFunc("POST /api/posts/{id}/like", a.authed("", a.handleLike))
	mux.HandleFunc("POST /api/posts/{id}/comments", a.authed("", a.handleComment))
	mux.HandleFunc("GET /api/posts/{id}/comments", a.authed("", a.handleGetComments))
	mux.HandleFunc("DELETE /api/comments/{id}", a.authed("", a.handleDeleteComment))
	mux.HandleFunc("PUT /api/comments/{id}", a.authed("", a.handleEditComment))
	mux.HandleFunc("GET /api/comments/{id}/replies", a.authed("", a.handleGetReplies))
	mux.HandleFunc("GET /api/comments/{id}/thread", a.authed("", a.handleThread))
	mux.HandleFunc("POST /api/comments/{id}/like", a.authed("", a.handleLikeComment))
	mux.HandleFunc("POST /api/achievements", a.authed("", a.handleAchievements))
	mux.HandleFunc("GET /api/notifications", a.authed("", a.handleNotifications))
	mux.HandleFunc("PUT /api/notifications/read", a.authed("", a.handleReadNotifications))
}
