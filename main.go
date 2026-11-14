// Flowa: a progress tracker for flow arts props. It serves the embedded UI and keeps
// accounts, sessions and per-prop progress in Postgres.
package main

import (
	"bytes"
	"compress/gzip"
	"container/heap"
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"html"
	"html/template"
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"io/fs"
	"log"
	"maps"
	"math"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/lib/pq"
	// The image is built FROM scratch and holds only this binary, so it has no root certificates of
	// its own. This package compiles Mozilla's bundle into the binary; Go uses it only when the
	// system has none, so a host with its own certificates keeps using them.
	_ "golang.org/x/crypto/x509roots/fallback"
)

//go:embed web
var webFS embed.FS

// brotliFS is what brotli.js wrote at build time: each file of web/ compressed, under the file's path
// with the hash of its bytes added. The folder's .gitignore is its one file in git, so there is always
// something to embed; a build that did not run the script has no brotli and serves gzip.
//
//go:embed all:brotli
var brotliFS embed.FS

const (
	maxProgress = 512 << 10 // one progress document; a full map of notes is far smaller
	keepBackups = 14
	sessionTTL  = 30 * 24 * time.Hour
	sessionIdle = "14 days" // a session unused this long ends (a Postgres interval)
	cookieName  = "flowa_session"
	// PBKDF2-SHA256 rounds for a new hash: the 600,000 OWASP asks for, about 1.7 s on this Pi 4. A
	// hash made with fewer is made again at that member's next sign-in (handleSignin).
	pwRounds   = 600000
	maxFails   = 8
	failWindow = 10 * time.Minute
)

// Feature flags and their defaults. They are changed at runtime by an admin. A flag holds its default
// until an admin saves it (flags keeps a row only for a saved one), so a new flag that is off by
// default is off on an instance that existed before it as on a new one. follows is the first: what
// it adds must change nothing on an instance that sets nothing (docs/federation.md, 14.1).
var flagDefaults = map[string]bool{"details": true, "demos": true, "references": true, "signups": true, "tour": true, "model3d": true, "community": true, "game": true, "moderation": true, "catalogEditor": true, "linkCards": true, "videoEmbeds": true, "suggest": true, "avatars": true, "bookmarks": true, "follows": false}

// Props a user can track. The catalog for each prop lives in the web UI.
var knownProps = []string{"nunchaku", "poi", "staff", "doublestaff", "buugeng"}

var usernameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,23}$`)

/* ---------- names that are never given out again ----------

A username is an identity on other servers once its actor id has gone out (docs/federation.md 4.3 and
5.2): a remote server that stored https://flowa.example.org/users/ana would take whoever holds the name
next for the same person. So the name of a deleted account that had opted in is retired, in every mode,
and refused at every door an account is made by: sign-up, a request to join, approving one, and the
ADMIN_USER account at start. The refusal says what a taken name says, and nothing about an account that
once had it. No endpoint renames an account, so a name that has federated cannot move either (4.3).

The instance's own host is taken too, in a mode that federates: usernameRE allows dots, and a member
named like the host would be shadowed by the instance actor in WebFinger (acct:host@host is the instance
actor, 5.3), so other servers could never find them by their handle. A member who already has that name
keeps the account; opting in is refused to them with the reason (handlePutMe). */

// retiredSQL is the condition that the name $1 is retired: one look in the primary key.
const retiredSQL = `EXISTS (SELECT 1 FROM retired_usernames r WHERE r.username = $1)`

// retireSQL retires the name of the account $1 when it had opted in or ever had a key: a key is made only
// when a member opts in (and for the owner of a single-profile instance with FLOWA_FEDERATE=on, whose
// opt-in is that setting and leaves no column behind) and is kept when they opt out, so it marks an
// account whose actor id may have been seen elsewhere. It runs before the row is deleted, with it.
const retireSQL = `INSERT INTO retired_usernames (username) SELECT u.username FROM users u
	WHERE u.id = $1 AND (u.federates OR EXISTS (SELECT 1 FROM ap_keys k WHERE k.user_id = u.id)) ON CONFLICT DO NOTHING`

// reservedName says whether name is the instance's own host in a mode that federates (fedHost), which no
// account may take.
func (a *app) reservedName(name string) bool { return a.fedHost != "" && name == a.fedHost }

// nameLockSQL takes a lock on one username until the transaction ends. The transaction that retires a
// name and each one that makes an account (sign-up, approving a request, ADMIN_USER) take it first, before
// they read or write the name. Without it a sign-up could take a name that a delete was retiring: its
// INSERT waits on the unique index for the deleting transaction, and once that commits it goes ahead with
// the snapshot it took before, in which the name was not retired yet. Under READ COMMITTED every
// statement takes a new snapshot when it starts, so an INSERT sent after the lock is granted sees the
// retirement, which committed before the lock was let go. The two-number form of the key is a space of
// its own, apart from the one-number locks elsewhere in this file; 7130654 says "a username", hashtext
// the name (two names that hash alike only wait for each other).
const nameLockSQL = `SELECT pg_advisory_xact_lock(7130654, hashtext($1))`

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
	countsTok string       // lets the fit workflow read the shared transition counts without an account (FLOWA_COUNTS_TOKEN); "" means no token opens them
	proxies   []*net.IPNet // reverse proxies whose forwarding headers are believed (FLOWA_TRUSTED_PROXIES)
	fetch     *fetcher     // link previews; nil means no fetching
	homeURL   string       // a link for admins to the machine's own start page (FLOWA_HOME_URL); "" means none
	mode      instanceMode // how the instance runs (FLOWA_MODE); local unless the operator says otherwise
	fed       *federation  // the origin, the keys and whether federation is refused; nil unless the mode federates
	fedHost   string       // the origin's host in a mode that federates, a name no account may take (takenName); "" otherwise
	version   string       // what this build is: buildVersion (a release tag, main.<commit>, or "dev"), or VERSION when set
	flagCache map[string]bool
	flagsAt   time.Time
	instMu    sync.Mutex    // guards instCache and instAt
	instCache *instanceInfo // what instanceCached last read; nil after a save, so the next read asks the database
	instAt    time.Time
	pageMu    sync.Mutex                // guards page; held while the visitor's page is made, so a flood makes it once
	page      *visitorPage              // the visitor's page of a single-profile instance as last made; nil until asked, and after a save
	sent      *onceStore                // posts and comments that named themselves, so a repeat is not a second one
	reqLog    *requestLog               // one line per request; see logged
	healthMu  sync.Mutex                // guards healthAt and healthWas
	healthAt  time.Time                 // when the database was last asked for GET /healthz
	healthWas health                    // what it said then
	nodeMu    sync.Mutex                // guards nodeUse and nodeAt
	nodeUse   nodeUsage                 // NodeInfo's counts as last read (nodeCounts)
	nodeAt    time.Time                 // when; zero until first asked
	namesMu   sync.Mutex                // guards names
	names     map[string]skillNameCache // skill names per prop for the public pages (skillNames)
}

// buildVersion names the build. A release image has its tag compiled in (build-image.sh, with -X), and
// deploy.sh --build the checkout's commit; anything else is "dev". VERSION in the environment, when
// set, is shown instead.
var buildVersion = "dev"

func newApp(db *sql.DB, dataDir string) *app {
	p, _ := parseProxies(defaultProxies)
	return &app{db: db, dataDir: dataDir, lim: newLimits(), proxies: p, version: buildVersion, mode: modeLocal, sent: &onceStore{m: map[string]*onceEntry{}}, reqLog: &requestLog{out: io.Discard}}
}

// instanceMode is how an instance runs (FLOWA_MODE; docs/federation.md, section 4). It is read once
// at start and never changes while the server runs, because in the modes that federate it decides
// which addresses other servers are told about.
type instanceMode string

const (
	modeLocal     instanceMode = "local"     // a community that lives on this server only, as Flowa has always run
	modeFederated instanceMode = "federated" // a community whose members follow and are followed across servers
	modeSingle    instanceMode = "single"    // one person's own instance
)

// modesBuilt are the modes this version can run. A mode that is planned and not built stops the
// start, as an unknown word does. All three are built. Federation itself is built only as far as
// reading: a federated instance (and a single-profile one with FLOWA_FEDERATE=on) answers WebFinger and
// NodeInfo and serves the actors, profiles and Public posts of the members who opted in, and has no
// inbox and delivers nothing yet. The start says so in the log (fedBuiltLog), and so do the admin dashboard and the
// README, so that an operator is not told more than this version does.
var modesBuilt = map[instanceMode]bool{modeLocal: true, modeFederated: true, modeSingle: true}

// parseMode reads FLOWA_MODE. Unset or blank is local, so a deployment that sets nothing runs as it
// always has. Capitals and spaces around the word are forgiven; any other word is an error that names
// the three, and so is a mode this version cannot run yet.
func parseMode(v string) (instanceMode, error) {
	m := instanceMode(strings.ToLower(strings.TrimSpace(v)))
	switch m {
	case "":
		return modeLocal, nil
	case modeLocal, modeFederated, modeSingle:
		if !modesBuilt[m] {
			return "", fmt.Errorf("FLOWA_MODE=%s is planned (docs/federation.md) and not built into this version; leave it unset or set it to local", m)
		}
		return m, nil
	}
	if len(v) > 40 {
		v = v[:40] // enough to recognise a typing mistake; the log is not the place for a pasted file
	}
	return "", fmt.Errorf("FLOWA_MODE must be local, federated or single, not %q", v)
}

// A post or a comment can name itself with an Idempotency-Key header: a random key the page makes
// once for each thing a person writes and sends again with every retry of it. The first request
// with a key does the work; a repeat of it, from a second tap or after a lost answer on a slow
// network, is answered with the post or comment the first one made instead of making another.
// Only the id of what was made is remembered, in memory, for ten minutes: the answer is built
// afresh, so a repeat does not hold a picture in memory. A restart forgets the keys, and a repeat
// after one is then an ordinary write.
const (
	onceWindow = 10 * time.Minute
	maxOnce    = 5000 // keys remembered at once; beyond that a write goes ahead unremembered
)

var onceKeyRE = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

type onceEntry struct {
	done chan struct{} // closed when the first request has finished
	id   int64         // what it made; 0 while it runs
	at   time.Time     // when it finished
}

type onceStore struct {
	mu    sync.Mutex
	m     map[string]*onceEntry
	swept time.Time
}

// sweep forgets keys older than the window. The caller holds the lock.
func (s *onceStore) sweep(now time.Time) {
	if len(s.m) < maxOnce && now.Sub(s.swept) < time.Minute {
		return
	}
	s.swept = now
	for k, e := range s.m {
		if e.id != 0 && now.Sub(e.at) > onceWindow {
			delete(s.m, k)
		}
	}
}

// once looks at the request's Idempotency-Key. It returns prior, the id an earlier request with
// the same key made (0 if this is the first), and finish, which the first request must call when
// it ends with the id it made, or 0 if it made nothing. A request that failed is forgotten, so
// the person can correct it and send it again under the same key. While the first request is
// still running, a repeat waits for it. Keys belong to one member and one path. ok is false when
// the request has been answered here (a key that is not a key).
func (a *app) once(w http.ResponseWriter, r *http.Request, u *user) (prior int64, finish func(int64), ok bool) {
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		return 0, func(int64) {}, true
	}
	if !onceKeyRE.MatchString(key) {
		fail(w, http.StatusBadRequest, "The Idempotency-Key header needs 8 to 64 letters, digits, dashes or underscores.")
		return 0, nil, false
	}
	s, k := a.sent, strconv.FormatInt(u.ID, 10)+" "+r.URL.Path+" "+key
	for {
		s.mu.Lock()
		e := s.m[k]
		if e == nil {
			s.sweep(time.Now())
			if len(s.m) >= maxOnce {
				s.mu.Unlock()
				return 0, func(int64) {}, true
			}
			e = &onceEntry{done: make(chan struct{})}
			s.m[k] = e
			s.mu.Unlock()
			return 0, func(id int64) {
				s.mu.Lock()
				if id == 0 {
					delete(s.m, k)
				} else {
					e.id, e.at = id, time.Now()
				}
				s.mu.Unlock()
				close(e.done)
			}, true
		}
		s.mu.Unlock()
		select {
		case <-e.done:
		case <-r.Context().Done():
			fail(w, http.StatusServiceUnavailable, "The first try is still being saved. Try again in a moment.")
			return 0, nil, false
		}
		s.mu.Lock()
		id, fresh := e.id, time.Since(e.at) < onceWindow
		if (id == 0 || !fresh) && s.m[k] == e {
			delete(s.m, k)
		}
		s.mu.Unlock()
		if id != 0 && fresh {
			w.Header().Set("Idempotency-Replayed", "true")
			return id, nil, true
		}
		// The first try made nothing, or was long ago: this one starts over as a first.
	}
}

// defaultProxies are where a reverse proxy connects from when it runs on the same host (loopback,
// or Docker's bridge gateway) or in a Docker network beside the app. Docker networks use
// 172.16.0.0/12; a home LAN (192.168.x.x) is not in the list, so a device there cannot pick its own
// address by sending forwarding headers.
const defaultProxies = "127.0.0.0/8,::1/128,172.16.0.0/12"

// homeLink reads FLOWA_HOME_URL: the address of the start page of the machine Flowa runs on, which
// admins get as a link in the menu. Empty means no link. It has to be a whole http or https address,
// because the page puts it into a link as it is.
func homeLink(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return "", fmt.Errorf("FLOWA_HOME_URL must be an http or https address without a password, like http://pi.local:8080/, not %q", s)
	}
	return u.String(), nil
}

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

// tenths is how the limiters count: a whole event is ten, so a request that was only partly the
// visitor's doing (a username that turned out to be taken, a post the server turned down) can cost
// a tenth of one.
const tenths = 10

type failCount struct {
	n     int // in tenths of an event
	reset time.Time
}

// aged is a counter with its key, in the queue that keeps counters in the order they were made.
type aged struct {
	key string
	f   *failCount
}

// limiter counts events per key in a fixed window, under its own lock. It holds at most maxKeys
// keys: when full it drops the ones whose window has passed, and if it is still full it forgets
// the oldest counter to make room. The cap bounds memory; it never refuses a visitor, so a flood
// of new keys cannot stop everyone else from signing up or signing in.
type limiter struct {
	mu      sync.Mutex
	n       int
	window  time.Duration
	maxKeys int
	m       map[string]*failCount
	// Every counter in the order it was made. The window is the same for all, so this is also the
	// order they run out in, and the oldest is always at the front.
	order []aged
}

func newLimiter(n int, window time.Duration) *limiter {
	return &limiter{n: n, window: window, maxKeys: 20000, m: map[string]*failCount{}}
}

// over reports whether key has used its n events in the current window, without counting one.
func (l *limiter) over(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	f := l.m[key]
	return f != nil && time.Now().Before(f.reset) && f.n >= l.n*tenths
}

// add counts one event for key and reports whether there was room for it.
func (l *limiter) add(key string) bool { return l.addWeight(key, tenths) }

// addWeight counts w tenths of an event for key and reports whether there was room for it: whether
// the count before it was under the limit. Whole events therefore stop at the limit exactly, and a
// member whose count holds a few tenths from requests that were turned down still gets the last
// whole one. A negative w gives part of an earlier count back.
func (l *limiter) addWeight(key string, w int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	f := l.m[key]
	if f == nil || now.After(f.reset) {
		if w <= 0 {
			return true // nothing to give back in a window that has passed
		}
		l.makeRoom(now)
		f = &failCount{reset: now.Add(l.window)}
		l.m[key] = f
		l.order = append(l.order, aged{key, f})
	}
	if w > 0 && f.n >= l.n*tenths {
		return false // full: nothing more to count until the window passes
	}
	f.n = max(f.n+w, 0)
	return true
}

// forget drops the count for key, as after a correct password.
func (l *limiter) forget(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.m, key)
}

// makeRoom drops the counters whose window has passed and then, while the limiter is still full,
// the oldest one. The caller holds the lock.
func (l *limiter) makeRoom(now time.Time) {
	for len(l.order) > 0 {
		o := l.order[0]
		current := l.m[o.key] == o.f // not replaced by a later window, and not forgotten
		if current && !now.After(o.f.reset) && len(l.m) < l.maxKeys {
			return
		}
		if current {
			delete(l.m, o.key)
		}
		l.order[0] = aged{}
		l.order = l.order[1:]
	}
}

// limits are the allowances: failed sign-ins (per address and username, per address, per account),
// sign-ups per address, wrong current passwords per member, and writes per member per hour.
type limits struct {
	signin, signinIP, account, signup, password *limiter
	posts, comments, likes, badges, marks       *limiter
	bookmarks, blocks, follows                  *limiter
	instance, about                             *limiter // saves of the instance information per admin; reads of the about page per address
	visitor                                     *limiter // reads of a single-profile instance's visitor page and its picture, per address
	discovery                                   *limiter // WebFinger and NodeInfo requests, per address, in a mode that federates
	public                                      *limiter // reads of actor documents, public pages and their pictures, per address, in a mode that federates
	joins                                       *limiter // approvals and declines of requests to join, per admin
}

func newLimits() limits {
	return limits{
		signin: newLimiter(maxFails, failWindow), signinIP: newLimiter(30, failWindow), account: newLimiter(20, failWindow),
		signup: newLimiter(10, time.Hour), password: newLimiter(5, failWindow),
		posts: newLimiter(30, time.Hour), comments: newLimiter(120, time.Hour), likes: newLimiter(600, time.Hour),
		badges: newLimiter(60, time.Hour), marks: newLimiter(120, time.Hour),
		bookmarks: newLimiter(300, time.Hour), blocks: newLimiter(100, time.Hour), follows: newLimiter(200, time.Hour),
		instance: newLimiter(60, time.Hour), about: newLimiter(120, time.Hour),
		visitor: newLimiter(300, time.Hour), // a page and its picture count two: 150 views an hour from one address is more than a person makes
		joins:   newLimiter(400, time.Hour), // twice the most requests that can wait at once (maxWaiting)
		// Another server asks WebFinger once per handle it meets and keeps the answer; 600 an hour from one
		// address is a mention every six seconds, and stops a harvest of names by guessing (RFC 7033 9.3).
		discovery: newLimiter(600, time.Hour),
		// A person reading a public profile asks for the page, the profile picture and up to 20 pictures of
		// posts; 2,400 an hour from one address is about a hundred such pages, and far more than a server
		// that fetches each actor and post once and keeps it.
		public: newLimiter(2400, time.Hour),
	}
}

// allowed refuses a member who has used up the allowance l, with a 429. Otherwise it counts the
// write and returns the writer the handler must answer through: when the handler turns the request
// down (too long, not found, muted), nine tenths of the count are given back. A typo therefore
// does not cost a post, and a flood of bad requests still runs out, ten times later.
func allowed(w http.ResponseWriter, l *limiter, u *user, what string) (http.ResponseWriter, bool) {
	key := strconv.FormatInt(u.ID, 10)
	if !l.add(key) {
		logRefusal(w, "too many "+what, "by member "+key)
		fail(w, http.StatusTooManyRequests, "That is more "+what+" than Flowa takes in an hour. Wait a while and try again.")
		return w, false
	}
	return &refunding{ResponseWriter: w, l: l, key: key}, true
}

// refunding gives most of a counted write back when the answer is an error.
type refunding struct {
	http.ResponseWriter
	l    *limiter
	key  string
	done bool
}

func (r *refunding) WriteHeader(code int) {
	if !r.done {
		r.done = true
		if code >= 400 {
			r.l.addWeight(r.key, 1-tenths)
		}
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *refunding) Write(b []byte) (int, error) {
	r.done = true // a body without a status is a 200
	return r.ResponseWriter.Write(b)
}

// Unwrap gives the writer underneath, for http.ResponseController and for noteRefusal.
func (r *refunding) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// refusals remembers what logRefusal wrote in the minute that began at since.
var refusals = struct {
	mu      sync.Mutex
	since   time.Time
	seen    map[string]bool
	dropped int
}{seen: map[string]bool{}}

// maxRefusalLines is how many refusals are written to the log in one minute, whoever they are for.
const maxRefusalLines = 120

// logRefusal writes one line for a refused sign-in or a used-up allowance: what was refused and
// for whom (an address, a /64 or a member's number; never a username or a password, because
// people type passwords into the username box). A key gets one line a minute and all keys together
// get maxRefusalLines, with the rest counted in one line, so a flood cannot fill the log. The request's
// own line in the request log names the refusal too, whether or not it got a line here.
func logRefusal(w http.ResponseWriter, what, who string) {
	noteRefusal(w, what+" "+who)
	now := time.Now()
	refusals.mu.Lock()
	defer refusals.mu.Unlock()
	if now.Sub(refusals.since) >= time.Minute {
		if refusals.dropped > 0 {
			log.Printf("refused: %d more in the minute from %s, not listed", refusals.dropped, refusals.since.UTC().Format("15:04:05 UTC"))
		}
		refusals.since, refusals.seen, refusals.dropped = now, map[string]bool{}, 0
	}
	line := what + " " + who
	if refusals.seen[line] {
		return
	}
	if len(refusals.seen) >= maxRefusalLines {
		refusals.dropped++
		return
	}
	refusals.seen[line] = true
	log.Printf("refused: %s", line)
}

// limitKey is what the limits count an address by. An IPv4 address counts as itself. An IPv6
// address counts as its /64, because one home or one phone is given a whole /64 and could
// otherwise come from a new address for every request.
func limitKey(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	addr = addr.Unmap().WithZone("")
	if addr.Is4() {
		return addr.String()
	}
	p, _ := addr.Prefix(64)
	return p.String()
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
	// Owner marks the one admin whose role nobody can change and who alone changes another admin's.
	Owner bool `json:"owner,omitempty"`
	// AvatarCfg is the avatar the user built (web/avatar-def.json lists every option); public.
	AvatarCfg json.RawMessage `json:"avatarCfg"`
	// Rev names the state of the profile text (see profileRev); only the owner's own copy carries it.
	Rev string `json:"rev,omitempty"`
}

// profileRev names the state of the part of a profile a person edits by hand: the bio, the links
// and the preferences. A save can say which state it was made against, and a save made against an
// older one is turned down instead of undoing what another device saved. The stats and the list of
// notes already read are left out on purpose: the page writes those by itself, and they must not
// make a profile edit look stale.
func profileRev(u *user) string {
	links, prefs := map[string]string{}, map[string]bool{}
	json.Unmarshal(u.Links, &links)
	json.Unmarshal(u.Prefs, &prefs)
	b, _ := json.Marshal([]any{u.Bio, links, prefs})
	return hashOf(b)
}

const userCols = "u.id, u.username, u.display_name, u.role, u.props, u.seen, u.onboarded, u.avatar, u.bio, u.links, u.prefs, u.stats, u.created_at, u.muted_until, u.mute_reason, u.suspended_until, u.suspend_reason, u.stats_hidden, u.avatar_cfg, u.owner"

type scanner interface{ Scan(...any) error }

// scanUser reads userCols, followed by any extra columns the query selected.
func scanUser(row scanner, extra ...any) (*user, error) {
	var u user
	var props, seen, links, prefs, stats []byte
	var muted, suspended sql.NullTime
	var avcfg []byte
	dst := append([]any{&u.ID, &u.Username, &u.DisplayName, &u.Role, &props, &seen, &u.Onboarded, &u.Avatar, &u.Bio, &links, &prefs, &stats, &u.Created,
		&muted, &u.MuteReason, &suspended, &u.SuspendReason, &u.StatsHidden, &avcfg, &u.Owner}, extra...)
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

// pbkdf2Runs counts the hashes made and checked since start. Nothing in the server reads it: the tests
// do, to prove that a refused sign-up costs no hash and a sign-in costs exactly one. A counter here
// changes nothing the server does, where a hash function a test could swap would add a way in.
var pbkdf2Runs atomic.Int64

func pbkdf2Key(pw string, salt []byte, rounds, n int) []byte {
	pbkdf2Runs.Add(1)
	hashSlots <- struct{}{}
	defer func() { <-hashSlots }()
	key, _ := pbkdf2.Key(sha256.New, pw, salt, rounds, n)
	return key
}

func hashPassword(pw string) string { return hashWith(pw, pwRounds) }

// hashWith makes a hash with a given round count. The count is stored in the hash, so hashes
// made before the count was raised still check.
func hashWith(pw string, rounds int) string {
	salt := make([]byte, 16)
	rand.Read(salt)
	key := pbkdf2Key(pw, salt, rounds, 32)
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", rounds, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
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

// hashRounds is the round count a stored hash was made with, or 0 when it cannot be read.
func hashRounds(hash string) int {
	parts := strings.Split(hash, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return 0
	}
	n, _ := strconv.Atoi(parts[1])
	return n
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
	// The account remembers when it was last used, so "Last active" outlives its sessions. Only a local
	// account gets a session (a remote one cannot sign in, and currentUser would not take it anyway).
	if _, err := a.db.Exec(`WITH s AS (INSERT INTO sessions (token_hash, user_id, expires_at) SELECT $1, $2, $3 WHERE EXISTS (SELECT 1 FROM users WHERE id = $2 AND domain = ''))
		UPDATE users SET last_seen = now() WHERE id = $2 AND domain = ''`, tokenHash(token), id, exp); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", Expires: exp, HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: a.viaHTTPS(r)})
	return nil
}

// sessionTokenRE is what a session cookie can hold. startSession makes 43 characters of base64url (32
// random bytes); the range around that keeps any token of the same alphabet a test or an older build
// made, and refuses what no session can be: too short, too long, or with other characters.
var sessionTokenRE = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)

// sessionLookups counts the sessions looked up in the database since start. Nothing in the server reads
// it: the tests do, to prove that a cookie that cannot be a session, or a visitor over the allowance,
// costs no query (as pbkdf2Runs counts the hashes).
var sessionLookups atomic.Int64

// sessionToken is the request's session cookie when it can be a session token, or "". A cookie that
// cannot is answered as no cookie at all, with no query: whoever sends one learns nothing either way.
func sessionToken(r *http.Request) string {
	c, err := r.Cookie(cookieName)
	if err != nil || !sessionTokenRE.MatchString(c.Value) {
		return ""
	}
	return c.Value
}

func (a *app) currentUser(r *http.Request) *user {
	tok := sessionToken(r)
	if tok == "" {
		return nil
	}
	sessionLookups.Add(1)
	// A session ends at expires_at, or after sessionIdle without use; use is noted once an hour.
	th := tokenHash(tok)
	var stale bool
	// A session of a remote account, which nothing here writes (startSession), is no session.
	u, err := scanUser(a.db.QueryRow(`SELECT `+userCols+`, s.last_seen < now() - interval '1 hour' FROM sessions s JOIN users u ON u.id = s.user_id AND u.domain = ''
		WHERE s.token_hash = $1 AND s.expires_at > now() AND s.last_seen > now() - $2::interval`, th, sessionIdle), &stale)
	if err != nil || u.SuspendedUntil != nil {
		return nil
	}
	if stale {
		a.db.Exec(`WITH s AS (UPDATE sessions SET last_seen = now() WHERE token_hash = $1 RETURNING user_id)
			UPDATE users u SET last_seen = now() FROM s WHERE u.id = s.user_id`, th)
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

// sameOrigin reports whether a write comes from Flowa's own pages: the Origin names this host, and
// a request that arrived over HTTPS does not come from a plain HTTP page, which anyone on the
// visitor's network could have written. The other way round is let through: an HTTPS page writing
// to what looks like plain HTTP is what a proxy that ends TLS without saying so looks like, and
// refusing it would lock everyone out.
func (a *app) sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	return err == nil && u.Host == r.Host && (u.Scheme == "https" || !a.viaHTTPS(r))
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
func (a *app) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if !a.sameOrigin(r) {
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
	permUserManage     = "user.manage"     // reset passwords, delete accounts, approve and decline requests to join
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

/* ---------- instance information ----------

What an admin tells people about this Flowa: a description, a way to reach the admins, and the rules,
as Mastodon has them (docs/federation.md, section 10). They are kept in instance_settings, one row a
part, and an instance where nobody has saved any of them answers exactly as it did before they
existed: sign-up shows nothing new, no page gains a link, and the visitor's GET /api/about answers as
an address nobody has. Everything is plain text: stored as typed, trimmed, and shown escaped by the
page, never read as HTML or Markdown. */

// The limits of each part, in characters. The page shows the same numbers, from instanceLimits.
const (
	maxInstanceDescription = 500  // a paragraph or two
	maxInstanceContact     = 200  // an address or a sentence
	maxInstanceRules       = 20   // as many as a person reads before agreeing
	maxRuleText            = 200  // the rule itself, one sentence
	maxRuleHint            = 1000 // an optional explanation under it
)

var instanceLimits = map[string]int{"description": maxInstanceDescription, "contact": maxInstanceContact, "rules": maxInstanceRules, "ruleText": maxRuleText, "ruleHint": maxRuleHint}

// instanceParts are the keys of instance_settings this version reads and writes. Later settings
// (policy, origin, retention) get their own keys when their items are built.
var instanceParts = []string{"description", "contact", "rules", "registration"}

// How a person joins, while the signups flag is on (docs/federation.md, section 10). There are two
// values and one switch, so that each question has one place to be answered: whether anyone may
// join at all is the signups flag, as it always was, and how they join is this setting.
//
//	signups flag   registration           sign-up
//	off            anything               closed: 403, as before
//	on             open (or nothing saved) open: an account and a session at once, as before
//	on             approval               a request to join that waits for an admin
//
// A third value "closed" would have made two switches that each close sign-up, and a flag that
// says on while the setting says closed. Nothing saved means open, so an instance that sets nothing
// signs people up exactly as it did before the setting existed.
const (
	regOpen     = "open"
	regApproval = "approval"
)

// instanceRule is one rule: a short sentence, and an optional longer explanation.
type instanceRule struct {
	Text string `json:"text"`
	Hint string `json:"hint"`
}

// instanceInfo is what an admin has saved. An empty description means none; a contact says what
// kind it is, so the page knows whether it may be a link.
type instanceInfo struct {
	Description string         `json:"description"`
	Contact     string         `json:"contact"`
	ContactKind string         `json:"contactKind"` // "email", "link", "text", or "" with no contact
	Rules       []instanceRule `json:"rules"`
	// Registration is regOpen or regApproval. It is not part of what the about page shows, so it is
	// left out of that JSON; the editor's answer (instanceAnswer) and /api/me name it themselves.
	Registration string `json:"-"`
}

func (i instanceInfo) empty() bool {
	return i.Description == "" && i.Contact == "" && len(i.Rules) == 0
}

type querier interface {
	Query(string, ...any) (*sql.Rows, error)
}

// readInstance reads the saved parts. version names the state they are in: the latest time any part
// was saved, or "" when none ever was; a write must name the version it was made against. by is the
// account that saved last, "" when nothing was saved or that account is gone.
func readInstance(q querier) (info instanceInfo, version, by string, err error) {
	info.Rules = []instanceRule{}
	rows, err := q.Query(`SELECT s.key, s.value, s.updated_at, COALESCE(u.username, '') FROM instance_settings s LEFT JOIN users u ON u.id = s.updated_by
		WHERE s.key IN ('description', 'contact', 'rules', 'registration')`)
	if err != nil {
		return info, "", "", err
	}
	defer rows.Close()
	var latest time.Time
	for rows.Next() {
		var key, name string
		var raw []byte
		var at time.Time
		if err := rows.Scan(&key, &raw, &at, &name); err != nil {
			return info, "", "", err
		}
		switch key {
		case "description":
			err = json.Unmarshal(raw, &info.Description)
		case "contact":
			err = json.Unmarshal(raw, &info.Contact)
		case "rules":
			err = json.Unmarshal(raw, &info.Rules)
		case "registration":
			err = json.Unmarshal(raw, &info.Registration)
		}
		if err != nil {
			return info, "", "", fmt.Errorf("instance setting %s: %w", key, err)
		}
		if at.After(latest) {
			latest, by = at, name
		}
	}
	if err := rows.Err(); err != nil {
		return info, "", "", err
	}
	if info.Rules == nil {
		info.Rules = []instanceRule{}
	}
	if info.Registration != regApproval {
		info.Registration = regOpen // no row, or a word this version does not know, is open, as before
	}
	info.ContactKind, _ = contactKind(info.Contact)
	if !latest.IsZero() {
		version = latest.UTC().Format(time.RFC3339Nano)
	}
	return info, version, by, nil
}

// instanceCached is what readInstance says, read at most every ten seconds and at once after a save
// on this server. A signed-out visitor can open the about page and every page load asks /api/me,
// so neither may cost a database read each time.
func (a *app) instanceCached() (instanceInfo, error) {
	a.instMu.Lock()
	defer a.instMu.Unlock()
	if a.instCache != nil && time.Since(a.instAt) < 10*time.Second {
		return *a.instCache, nil
	}
	info, _, _, err := readInstance(a.db)
	if err != nil {
		return instanceInfo{}, err
	}
	a.instCache, a.instAt = &info, time.Now()
	return info, nil
}

// plainText cleans one part as typed: a line break from Windows becomes a plain one, a tab a space,
// what draws nothing is dropped (visibleText), and the ends are trimmed. Any other control character
// (NEL, the vertical tab and the form feed among them) is refused rather than dropped, because it was
// not typed by a person. The line and paragraph separators (U+2028, U+2029) are no control characters,
// but a browser breaks the line at them: where lines make sense (multi) they are line breaks like any
// other, and in a one-line part they are refused as a line break is. Line breaks are kept only where
// lines make sense, and then at most one empty line in a row, so a paste cannot push the rest of the
// page away. It returns the text, or why it cannot be used.
func plainText(s, what string, multi bool, max int) (string, string) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if multi {
		s = strings.NewReplacer("\u2028", "\n", "\u2029", "\n").Replace(s)
	}
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return "", what + " cannot hold control characters."
		}
		if (r == '\n' || r == '\u2028' || r == '\u2029') && !multi {
			return "", what + " is one line: it cannot hold a line break."
		}
	}
	v, ok := visibleText(s)
	if !ok {
		return "", what + " cannot hold characters that change the direction of the text."
	}
	if multi {
		lines := strings.Split(v, "\n")
		for i := range lines {
			lines[i] = strings.TrimRightFunc(lines[i], unicode.IsSpace)
		}
		v = blankLines.ReplaceAllString(strings.Join(lines, "\n"), "\n\n")
	}
	v = strings.TrimSpace(v)
	if n := len([]rune(v)); n > max {
		return "", fmt.Sprintf("%s takes at most %d characters; this has %d.", what, max, n)
	}
	return v, ""
}

var (
	blankLines = regexp.MustCompile(`\n{3,}`)
	// emailRE is an address a person can write to: letters, digits and . _ + - before the @, and a
	// domain with a dot. Nothing that means something in a mailto: link is let in (? & = # / for
	// headers such as cc, % for characters written as codes), so the page's link goes to that address
	// and nowhere else; an address with other characters is kept as text.
	emailRE = regexp.MustCompile(`^[A-Za-z0-9._+-]+@[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)+$`)
	// schemeRE is the start of a link of any kind ("javascript:", "data:", "https:"): a word and a
	// colon with no space after it. "Phone: 0123" is text, because of the space.
	schemeRE = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9+.-]*):\S`)
	// hostRE is the host of a contact link, with its port: ASCII letters, digits, dots and dashes.
	hostRE = regexp.MustCompile(`^[A-Za-z0-9.-]+(:[0-9]+)?$`)
)

// contactKind says what a contact is: an email address (the page makes it a mailto link), an http or
// https link (a link that opens in a new tab), or a short text (shown as it is). A link of any other
// kind is refused, so a contact can never be javascript: or data:, and neither is a link with a name
// and password in it, or with a host in other than plain letters, digits, dots and dashes: both can
// make one site look like another (https://аpple.com with a Cyrillic а). Such a host is written in its
// xn-- form, which shows what it is; turning it into that form here would need a library for little.
// Text that only looks like a link, such as //evil.example, is text: the page never makes it a link.
// ok is false when it is refused.
func contactKind(c string) (kind string, ok bool) {
	switch {
	case c == "":
		return "", true
	case emailRE.MatchString(c):
		return "email", true
	}
	m := schemeRE.FindStringSubmatch(c)
	if m == nil {
		return "text", true
	}
	if s := strings.ToLower(m[1]); s != "http" && s != "https" {
		return "", false
	}
	u, err := url.Parse(c)
	if err != nil || u.Host == "" || u.User != nil || strings.ContainsFunc(c, unicode.IsSpace) || !hostRE.MatchString(u.Host) {
		return "", false
	}
	return "link", true
}

// instanceInput is a save: every part at once, so two admins cannot each save half, and the version
// the edit was made against. Registration may be left out, which keeps it as it is: an editor made
// before the setting existed still saves the rest, and the version check still covers it.
type instanceInput struct {
	Description  *string        `json:"description"`
	Contact      *string        `json:"contact"`
	Rules        []instanceRule `json:"rules"`
	Registration *string        `json:"registration"`
	UpdatedAt    *string        `json:"updatedAt"`
}

// cleanInstance checks a save against the limits. It returns what is to be stored, or the field the
// problem is in ("description", "contact", "rules", "rules.2.text") and why.
func cleanInstance(in instanceInput) (instanceInfo, string, string) {
	var out instanceInfo
	if in.Description == nil || in.Contact == nil || in.Rules == nil || in.UpdatedAt == nil {
		return out, "", "Send the description, the contact, the rules and updatedAt together: a save replaces all of them."
	}
	var msg string
	if out.Description, msg = plainText(*in.Description, "The description", true, maxInstanceDescription); msg != "" {
		return out, "description", msg
	}
	if out.Contact, msg = plainText(*in.Contact, "The contact", false, maxInstanceContact); msg != "" {
		return out, "contact", msg
	}
	var ok bool
	if out.ContactKind, ok = contactKind(out.Contact); !ok {
		return out, "contact", "The contact is an email address, a link that starts with https:// or http:// (a host in letters other than a to z written in its xn-- form), or a short text that is not a link of another kind."
	}
	if len(in.Rules) > maxInstanceRules {
		return out, "rules", fmt.Sprintf("Keep to %d rules at most; this has %d.", maxInstanceRules, len(in.Rules))
	}
	out.Rules = []instanceRule{}
	for i, r := range in.Rules {
		n := strconv.Itoa(i + 1)
		var c instanceRule
		if c.Text, msg = plainText(r.Text, "Rule "+n, false, maxRuleText); msg != "" {
			return out, "rules." + strconv.Itoa(i) + ".text", msg
		}
		if c.Text == "" {
			return out, "rules." + strconv.Itoa(i) + ".text", "Rule " + n + " is empty. Write it, or remove it."
		}
		if c.Hint, msg = plainText(r.Hint, "The explanation of rule "+n, true, maxRuleHint); msg != "" {
			return out, "rules." + strconv.Itoa(i) + ".hint", msg
		}
		out.Rules = append(out.Rules, c)
	}
	if in.Registration != nil {
		if *in.Registration != regOpen && *in.Registration != regApproval {
			return out, "registration", `Registration is "open" or "approval". To close sign-up, turn off Open sign-ups under Features.`
		}
		out.Registration = *in.Registration
	}
	return out, "", ""
}

// instanceAnswer is what the admin's editor reads: the parts, the version to send back with a save,
// who saved last, and the limits.
func instanceAnswer(info instanceInfo, version, by string) map[string]any {
	var who any
	if by != "" {
		who = by
	}
	return map[string]any{"description": info.Description, "contact": info.Contact, "contactKind": info.ContactKind, "rules": info.Rules,
		"registration": info.Registration, "updatedAt": version, "updatedBy": who, "limits": instanceLimits}
}

func (a *app) handleGetInstanceAdmin(w http.ResponseWriter, _ *http.Request, _ *user) {
	info, version, by, err := readInstance(a.db)
	if err != nil {
		a.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, instanceAnswer(info, version, by))
}

// handlePutInstance saves the whole document. The parts that changed are written, with the account
// and the time, in one transaction with their audit row. A lock for the length of the transaction
// keeps two saves in order, and the version check refuses a save made against a state that has since
// changed (409, with the state as it is), so one admin cannot quietly undo another's rules.
func (a *app) handlePutInstance(w http.ResponseWriter, r *http.Request, u *user) {
	w, ok := allowed(w, a.lim.instance, u, "saves of the instance information")
	if !ok {
		return
	}
	// The limits are in characters, and one character takes up to 4 bytes in JSON as sent: a whole
	// document at its limits (24,700 characters) is under 100 KB in any script, so 256 KB is room enough
	// and still a bound.
	var in instanceInput
	if !readJSON(w, r, 256<<10, &in) {
		return
	}
	next, field, msg := cleanInstance(in)
	if msg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg, "field": field})
		return
	}
	tx, err := a.db.Begin()
	if err != nil {
		a.serverError(w, err)
		return
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(7130652)`); err != nil {
		a.serverError(w, err)
		return
	}
	cur, version, by, err := readInstance(tx)
	if err != nil {
		a.serverError(w, err)
		return
	}
	if version != *in.UpdatedAt {
		out := instanceAnswer(cur, version, by)
		out["error"] = "Another admin saved the instance information while you were editing it. Look at what they saved, then save yours again."
		writeJSON(w, http.StatusConflict, out)
		return
	}
	if next.Registration == "" {
		next.Registration = cur.Registration // left out of the save: kept
	}
	values := map[string]any{"description": next.Description, "contact": next.Contact, "rules": next.Rules, "registration": next.Registration}
	before := map[string]any{"description": cur.Description, "contact": cur.Contact, "rules": cur.Rules, "registration": cur.Registration}
	var changed []string
	for _, k := range instanceParts {
		nb, _ := json.Marshal(values[k])
		ob, _ := json.Marshal(before[k])
		if bytes.Equal(nb, ob) {
			continue
		}
		changed = append(changed, k)
		// clock_timestamp, not now(): the time of the write, after the lock, so a save that waited for
		// another is always the later version.
		if _, err := tx.Exec(`INSERT INTO instance_settings (key, value, updated_at, updated_by) VALUES ($1, $2, clock_timestamp(), $3)
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at, updated_by = EXCLUDED.updated_by`, k, string(nb), u.ID); err != nil {
			a.serverError(w, err)
			return
		}
	}
	if len(changed) > 0 {
		// The log keeps rows short, as it does for flags: which parts changed and how many rules
		// there are now, not the text, which the settings themselves hold.
		// How people join is short and matters, so a change of it is named.
		detail := map[string]any{"parts": changed, "rules": len(next.Rules)}
		if slices.Contains(changed, "registration") {
			detail["registration"] = next.Registration
		}
		if err := a.audit(tx, u.ID, "instance", "", 0, 0, "", detail); err != nil {
			a.serverError(w, err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		a.serverError(w, err)
		return
	}
	a.instMu.Lock()
	a.instCache = nil
	a.instMu.Unlock()
	a.dropVisitorPage()
	info, version, by, err := readInstance(a.db)
	if err != nil {
		a.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, instanceAnswer(info, version, by))
}

// handleInstance is the about page for a signed-in member, in every mode, whatever has been saved.
func (a *app) handleInstance(w http.ResponseWriter, _ *http.Request, _ *user) {
	info, err := a.instanceCached()
	if err != nil {
		a.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// handleAbout is the about page for anyone, signed in or not. Until an admin has saved a description
// it answers as an address nobody has (the same 404 as handleStatic), so an instance that sets
// nothing tells a visitor nothing new. After that it is counted by address, as sign-in is, though
// the answer comes from memory: the limit is for the bandwidth, the cache for the database.
func (a *app) handleAbout(w http.ResponseWriter, r *http.Request) {
	info, err := a.instanceCached()
	if err != nil {
		a.serverError(w, err)
		return
	}
	if info.Description == "" {
		http.NotFound(w, r)
		return
	}
	from := limitKey(a.clientIP(r))
	if !a.lim.about.add(from) {
		logRefusal(w, "too many reads of the about page", "from "+from)
		fail(w, http.StatusTooManyRequests, "Too many requests from this address. Try again in an hour.")
		return
	}
	writeJSON(w, http.StatusOK, info)
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
		u.Rev = profileRev(u)
		if ach, err = a.achievementsOf(u.ID); err != nil {
			a.serverError(w, err)
			return
		}
	}
	out := map[string]any{"user": u, "flags": f, "props": knownProps, "achievements": ach, "perms": permsOf(u)}
	// What an admin has said about this Flowa, only once something is saved, so an instance that has
	// saved nothing answers as it always has. about says whether this reader may open the about page (a
	// visitor only once there is a description); a visitor is also given the rules, for the sign-up form.
	info, err := a.instanceCached()
	if err != nil {
		a.serverError(w, err)
		return
	}
	// approval tells a visitor, before they type, that an admin approves new accounts here; it is
	// there only while that is so, and an instance that never chose it answers as before. In single
	// mode nobody joins, so it is never said there.
	approval := u == nil && info.Registration == regApproval && a.mode != modeSingle
	if !info.empty() || approval {
		in := map[string]any{"about": u != nil || info.Description != ""}
		if u == nil && len(info.Rules) > 0 {
			in["rules"] = info.Rules
		}
		if approval {
			in["approval"] = true
		}
		out["instance"] = in
	}
	// In single mode a signed-in user is told so (single), so the page offers no way to make an account
	// and goes to the visitor's page at / on signing out. A visitor is not told the mode: the signups
	// flag they are given is what sign-up does here, off, which hides every way to make an account by
	// itself. Local mode answers as before.
	if a.mode == modeSingle {
		if u != nil {
			out["single"] = true
		} else {
			shown := maps.Clone(f)
			shown["signups"] = false
			out["flags"] = shown
		}
	}
	// Where the machine's own start page is concerns the people who run it, nobody else.
	if u != nil && u.Role == "admin" && a.homeURL != "" {
		out["homeUrl"] = a.homeURL
	}
	// In a mode that federates, the member's handle and whether they share it (fedView); in any other
	// mode the answer is as before, with no word of it.
	if a.fed != nil && u != nil {
		var on bool
		if err := a.db.QueryRow(`SELECT federates FROM users WHERE id = $1`, u.ID).Scan(&on); err != nil {
			a.serverError(w, err)
			return
		}
		out["federation"] = a.fedView(u, on)
	}
	writeJSON(w, http.StatusOK, out)
}

type credentials struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	DisplayName string `json:"displayName"`
	Reason      string `json:"reason"` // why the person wants to join; read only while sign-up is by approval
}

// passwordProblem says why a password cannot be used for the account with this username, or ""
// when it can. Besides the length it refuses the username itself and the passwords most people
// pick (commonPasswords), whatever the case: those are the first ones anyone guessing would try.
func passwordProblem(pw, username string) string {
	if len(pw) < 8 || len(pw) > 200 {
		return "Use a password of 8 to 200 characters."
	}
	low := strings.ToLower(strings.TrimSpace(pw))
	if username != "" && low == strings.ToLower(username) {
		return "The password cannot be the username."
	}
	if commonPasswords[low] {
		return "That password is one of the most used, so it is among the first anyone would try. Choose another."
	}
	return ""
}

func validPassword(w http.ResponseWriter, pw, username string) bool {
	if msg := passwordProblem(pw, username); msg != "" {
		fail(w, http.StatusBadRequest, msg)
		return false
	}
	return true
}

func (a *app) handleSignup(w http.ResponseWriter, r *http.Request) {
	// A single-profile instance takes no new account whatever the flag and the setting say. It is the
	// first thing asked, before the flags are read, the body is read or anything is counted or hashed.
	if a.mode == modeSingle {
		fail(w, http.StatusForbidden, singleOnly)
		return
	}
	f, err := a.flags()
	if err != nil {
		a.serverError(w, err)
		return
	}
	if !f["signups"] {
		fail(w, http.StatusForbidden, "New sign-ups are turned off. Ask the admin for an account.")
		return
	}
	// Only what costs something is counted: a new account is one, a name that is taken is a tenth
	// (it tells the visitor whether an account exists), and a form the server turns down is free.
	from := limitKey(a.clientIP(r))
	tooMany := func() {
		logRefusal(w, "too many new accounts", "from "+from)
		fail(w, http.StatusTooManyRequests, "Too many new accounts from this address. Try again in an hour.")
	}
	if a.lim.signup.over(from) {
		tooMany()
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
	if !validPassword(w, in.Password, name) {
		return
	}
	display := name
	if strings.TrimSpace(in.DisplayName) != "" {
		var msg string
		if display, msg = cleanName(in.DisplayName); msg != "" {
			fail(w, http.StatusBadRequest, msg)
			return
		}
	}
	// While sign-up is by approval, the person may say why they want to join, as on Mastodon.
	info, err := a.instanceCached()
	if err != nil {
		a.serverError(w, err)
		return
	}
	approval := info.Registration == regApproval
	var reason string
	if approval {
		var msg string
		if reason, msg = plainText(in.Reason, "The reason", true, maxJoinReason); msg != "" {
			fail(w, http.StatusBadRequest, msg)
			return
		}
	}
	// Looked up before the password is hashed, so asking for a taken name costs no CPU. A name that
	// waits in a request to join is taken too, so nobody can claim it while an admin decides, and so is a
	// retired name or the instance's own host (takenName), in the same words.
	var taken bool
	if err := a.db.QueryRow(`SELECT EXISTS (SELECT 1 FROM users WHERE username = $1) OR EXISTS (SELECT 1 FROM join_requests WHERE username = $1) OR `+retiredSQL, name).Scan(&taken); err != nil {
		a.serverError(w, err)
		return
	}
	taken = taken || a.reservedName(name)
	if taken {
		a.lim.signup.addWeight(from, 1)
		fail(w, http.StatusConflict, "That username is taken.")
		return
	}
	// The allowance is counted before the password is hashed, on both paths, so what an address can make
	// the server hash is bounded by it, even when many requests arrive at once: add is one step under the
	// limiter's lock, and only the requests it lets through go on to hash.
	if !a.lim.signup.add(from) {
		tooMany()
		return
	}
	if approval {
		a.askToJoin(w, from, name, display, in.Password, reason)
		return
	}
	// The insert checks in the same statement that no request to join waits under the name, which leaves
	// only the moment an admin switches the setting for an account and a request to share one (see
	// askToJoin). No row back means a request took the name: answered as a taken name.
	// The password is hashed before the transaction, which holds the name's lock (nameLockSQL) only for
	// the insert.
	hash := hashPassword(in.Password)
	tx, err := a.db.Begin()
	if err != nil {
		a.serverError(w, err)
		return
	}
	defer tx.Rollback()
	var u *user
	if _, err = tx.Exec(nameLockSQL, name); err == nil {
		u, err = scanUser(tx.QueryRow(`INSERT INTO users AS u (username, display_name, pw_hash) SELECT $1, $2, $3
			WHERE NOT EXISTS (SELECT 1 FROM join_requests WHERE username = $1) AND NOT `+retiredSQL+` RETURNING `+userCols,
			name, display, hash))
	}
	if err == nil {
		err = tx.Commit()
	}
	var pe *pq.Error
	if errors.As(err, &pe) && pe.Code == "23505" || errors.Is(err, sql.ErrNoRows) {
		a.lim.signup.addWeight(from, 1-tenths) // someone took the name in between: a tenth, as above
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

/* ---------- requests to join ----------

While registration is by approval, signing up makes a request to join, not an account. A request is a
row of its own table (join_requests), never a row of users, so nothing that lists, counts, mentions,
follows, blocks or signs in an account can meet one: the people list, profiles, mentions, the member
totals and every session read users and only users. An admin with user.manage approves it, which
makes the account, or declines it, which deletes the request and frees the name. Nobody is told
either way (Flowa keeps no email address): the person learns it by signing in. */

const (
	maxJoinReason = 500 // characters in the answer to "Why do you want to join?"
	maxWaiting    = 200 // requests that may wait at once; past it, sign-up says to try again later
	joinWaitDays  = 30  // days a request waits before housekeeping removes it
)

// Said to a person whose request waits, after the right password only.
const waitingSentence = "Your request to join is waiting for an admin to approve it. Nobody can tell you when that happens, so try signing in again later."

// askToJoin stores a request to join, once handleSignup has counted it against the sign-up allowance
// exactly as it counts an account, before anything is hashed. At most maxWaiting requests wait at once.
// A full queue is seen first by a plain count, so it costs no hash; a request it turns away keeps its
// count against the address like any other attempt, so one address cannot ask without end. The count
// that decides is made again under a lock with the insert, so requests sent together cannot pass the
// cap. The answer is 202 with no session: the person is not signed in until an admin approves.
func (a *app) askToJoin(w http.ResponseWriter, from, name, display, pw, reason string) {
	full := func() {
		logRefusal(w, "too many requests to join waiting", "from "+from)
		fail(w, http.StatusServiceUnavailable, "This Flowa is not taking requests to join right now. Try again in a few days.")
	}
	var waiting int
	if err := a.db.QueryRow(`SELECT count(*) FROM join_requests`).Scan(&waiting); err != nil {
		a.serverError(w, err)
		return
	}
	if waiting >= maxWaiting {
		full()
		return
	}
	hash := hashPassword(pw) // before the transaction, which holds the lock as briefly as it can
	tx, err := a.db.Begin()
	if err != nil {
		a.serverError(w, err)
		return
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(7130653)`); err != nil {
		a.serverError(w, err)
		return
	}
	if err := tx.QueryRow(`SELECT count(*) FROM join_requests`).Scan(&waiting); err != nil {
		a.serverError(w, err)
		return
	}
	if waiting >= maxWaiting {
		full()
		return
	}
	// The insert checks in the same statement that no account has the name, as the open path checks for
	// a request. What is left is the moment an admin switches the setting: a sign-up that read "open" just
	// before and one that reads "approval" just after can each pass the other's check. Then an account and
	// a request share a name; Accounts says so beside the request, Approve answers 409, and the admin
	// declines it. Closing that moment for good would need a lock on the open path too.
	res, err := tx.Exec(`INSERT INTO join_requests (username, display_name, pw_hash, reason) SELECT $1, $2, $3, $4
		WHERE NOT EXISTS (SELECT 1 FROM users WHERE username = $1) AND NOT `+retiredSQL, name, display, hash, reason)
	var pe *pq.Error
	taken := errors.As(err, &pe) && pe.Code == "23505"
	if err == nil {
		n, _ := res.RowsAffected()
		taken = n == 0
	}
	if taken {
		a.lim.signup.addWeight(from, 1-tenths) // the name was taken in between: a tenth, as a taken name, as the open path does
		fail(w, http.StatusConflict, "That username is taken.")
		return
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"waiting": true})
}

// joinRequest is a request as the admins' list shows it. The reason is for the staff who decide.
type joinRequest struct {
	ID          int64     `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"displayName"`
	Reason      string    `json:"reason"`
	Created     time.Time `json:"created"`
	// Taken says an account has the name now (the setting was switched while two sign-ups were on their
	// way, see askToJoin): the request cannot become an account, and the page says to decline it.
	Taken bool `json:"taken"`
}

// handleJoinRequests lists the requests that wait, oldest first, with the limits the page explains.
func (a *app) handleJoinRequests(w http.ResponseWriter, _ *http.Request, _ *user) {
	rows, err := a.db.Query(`SELECT j.id, j.username, j.display_name, j.reason, j.created_at, EXISTS (SELECT 1 FROM users u WHERE u.username = j.username)
		FROM join_requests j ORDER BY j.created_at, j.id`)
	if err != nil {
		a.serverError(w, err)
		return
	}
	defer rows.Close()
	out := []joinRequest{}
	for rows.Next() {
		var j joinRequest
		if err := rows.Scan(&j.ID, &j.Username, &j.DisplayName, &j.Reason, &j.Created, &j.Taken); err != nil {
			a.serverError(w, err)
			return
		}
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		a.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": out, "max": maxWaiting, "days": joinWaitDays})
}

const noRequest = "No such request: it was approved or declined already, or it waited too long."

// handleApproveRequest makes the account a request asked for. The request is deleted and the account
// made in one transaction with its audit row; the delete locks the row, so of two admins acting at
// once the second finds nothing and is told so (404), and only one account and one audit row are
// made. If an account took the name meanwhile (the setting was changed while a sign-up was on its
// way, or ADMIN_USER names it), the request stays for the admin to decline.
func (a *app) handleApproveRequest(w http.ResponseWriter, r *http.Request, u *user) {
	w, ok := allowed(w, a.lim.joins, u, "approvals and declines of requests to join")
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	tx, err := a.db.Begin()
	if err != nil {
		a.serverError(w, err)
		return
	}
	defer tx.Rollback()
	// The name's lock is taken before the request's row, in the order makeAdmin takes them (the name, then
	// the requests under it), so the two cannot wait for each other; the name of a request never changes.
	var name, display, hash string
	err = tx.QueryRow(`SELECT username FROM join_requests WHERE id = $1`, id).Scan(&name)
	if err == nil {
		_, err = tx.Exec(nameLockSQL, name)
	}
	if err == nil {
		err = tx.QueryRow(`DELETE FROM join_requests WHERE id = $1 RETURNING username, display_name, pw_hash`, id).Scan(&name, &display, &hash)
	}
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, noRequest)
		return
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	var uid int64
	// A retired name, or the instance's own host, cannot become an account either; the admin is told it
	// is taken, in the words of a name an account holds, and nothing of an account that once had it.
	err = tx.QueryRow(`INSERT INTO users (username, display_name, pw_hash) SELECT $1, $2, $3 WHERE NOT `+retiredSQL+` AND NOT $4 RETURNING id`, name, display, hash, a.reservedName(name)).Scan(&uid)
	var pe *pq.Error
	if errors.As(err, &pe) && pe.Code == "23505" || errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusConflict, "The username @"+name+" is taken, so this request cannot become an account. Decline it.")
		return
	}
	if err == nil {
		// The row names the account and the name it was asked under, as a decline's does, and no more:
		// the log keeps who decided what, and when the request was made or why matters only until it is
		// answered (the reason was for the admin who decided, not for the log).
		err = a.audit(tx, u.ID, "approve_user", "user", uid, uid, "", map[string]any{"username": name})
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": uid, "username": name})
}

// handleDeclineRequest deletes a request. Nothing of it is kept but the audit row with the name: a
// declined person's password hash and reason are not Flowa's to keep, and the name is free again, as
// it was before they asked. They are not told; signing in answers as for any name nobody has.
func (a *app) handleDeclineRequest(w http.ResponseWriter, r *http.Request, u *user) {
	w, ok := allowed(w, a.lim.joins, u, "approvals and declines of requests to join")
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	tx, err := a.db.Begin()
	if err != nil {
		a.serverError(w, err)
		return
	}
	defer tx.Rollback()
	var name string
	err = tx.QueryRow(`DELETE FROM join_requests WHERE id = $1 RETURNING username`, id).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, noRequest)
		return
	}
	if err == nil {
		err = a.audit(tx, u.ID, "decline_user", "", id, 0, "", map[string]any{"username": name})
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

// clientIP is the visitor's address, which the sign-in and sign-up limits count by. Behind a reverse
// proxy the connection comes from the proxy, so when it does (a trusted proxy), the visitor is the
// nearest address in X-Forwarded-For that is not itself a trusted proxy, read from the right: each
// proxy appends the address it saw, so only the right-hand end is trustworthy and a visitor cannot
// choose their address by sending the header. X-Real-IP is read only when there is no X-Forwarded-For
// at all: a proxy that writes X-Forwarded-For may still pass on the X-Real-IP a visitor sent (Caddy
// does), so beside the proxy's own header it says nothing.
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
	if len(hops) > 0 {
		// Every hop is a trusted proxy: the visitor is on the proxy's own network (a request from
		// the machine itself, or a Docker that does not keep visitors' addresses), and the
		// leftmost hop is the nearest thing to their address that a proxy wrote.
		return net.ParseIP(strings.TrimSpace(hops[0])).String()
	}
	if xr := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); xr != nil {
		return xr.String()
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

// rowQuerier is what signinLookup reads through: the database, or in a test something that counts.
type rowQuerier interface {
	QueryRow(string, ...any) *sql.Row
}

// signinSQL reads a name in one statement: the account, or, when no account has the name, the request
// to join that waits under it, shaped as an account (id 0) with request true. A member's name, a waiting
// name and an unknown one each cost this one query.
const signinSQL = `SELECT ` + userCols + `, u.pw_hash, false FROM users u WHERE u.username = $1 AND u.domain = ''
	UNION ALL
	SELECT 0, j.username, j.display_name, 'user', '[]', '[]', false, '', '', '{}', '{}', '{}', j.created_at, NULL, '', NULL, '', false, '{}', false, j.pw_hash, true
	FROM join_requests j WHERE j.username = $1 AND NOT EXISTS (SELECT 1 FROM users WHERE username = $1 AND domain = '')`

// signinLookup is the one read a sign-in makes. err is sql.ErrNoRows for a name nobody has.
func signinLookup(q rowQuerier, name string) (u *user, hash string, request bool, err error) {
	u, err = scanUser(q.QueryRow(signinSQL, name), &hash, &request)
	return u, hash, request, err
}

func (a *app) handleSignin(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if !readJSON(w, r, 4096, &in) {
		return
	}
	name := strings.ToLower(strings.TrimSpace(in.Username))
	ip := limitKey(a.clientIP(r))
	key := ip + "|" + name
	// Failures are counted per address and username, and per address (guessing across many
	// accounts). Those two refuse before the password is looked at: they are per visitor, and
	// they are what keeps guessing from using the CPU.
	const wait = "Too many failed attempts. Wait ten minutes and try again."
	if a.lim.signin.over(key) || a.lim.signinIP.over(ip) {
		logRefusal(w, "too many sign-in attempts", "from "+ip)
		fail(w, http.StatusTooManyRequests, wait)
		return
	}
	// Failures are also counted per account (guessing from many addresses). That count must not
	// refuse the right password, or anyone could lock a member or the admin out by guessing at
	// their name. So the password is still checked: the right one signs in, a wrong one is told to
	// wait and learns nothing more.
	locked := a.lim.account.over(name)
	u, hash, request, err := signinLookup(a.db, name)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		a.serverError(w, err)
		return
	}
	// Exactly one password check per attempt, whatever the name is: an account's hash, a waiting
	// request's, or for a name nobody has a hash that matches nothing. Each costs the same time, so how
	// long the answer takes does not tell which of the three the name is.
	ok := false
	if err != nil {
		checkPassword(dummyHash, in.Password)
	} else {
		ok = checkPassword(hash, in.Password)
	}
	// A request to join that waits: only the right password is told so. Anyone else learns no more than
	// for a name nobody has, so a stranger cannot find out who asked to join.
	// In single mode a request left from local mode will never be approved, so it is a name nobody has.
	if request && a.mode == modeSingle {
		ok = false
	}
	if ok && request {
		a.lim.account.forget(name)
		fail(w, http.StatusForbidden, waitingSentence)
		return
	}
	if !ok {
		a.lim.signin.add(key)
		a.lim.signinIP.add(ip)
		if locked {
			logRefusal(w, "too many sign-in attempts on one account", "from "+ip)
			fail(w, http.StatusTooManyRequests, wait)
			return
		}
		a.lim.account.add(name)
		logRefusal(w, "wrong username or password", "from "+ip)
		fail(w, http.StatusUnauthorized, "Wrong username or password.")
		return
	}
	a.lim.account.forget(name)
	if u.SuspendedUntil != nil {
		msg := "Your account is suspended until " + stamp(*u.SuspendedUntil) + "."
		if u.SuspendReason != "" {
			msg += " Reason: " + u.SuspendReason
		}
		fail(w, http.StatusForbidden, msg)
		return
	}
	// A hash made with fewer rounds than new ones get is made again now, the one moment the
	// password is known. It replaces the stored hash only if that is still the one just checked,
	// and a failure here does not stop the sign-in.
	if hashRounds(hash) < pwRounds {
		if _, err := a.db.Exec(`UPDATE users SET pw_hash = $3 WHERE id = $1 AND pw_hash = $2`, u.ID, hash, hashPassword(in.Password)); err != nil {
			log.Printf("error: making the password hash again: %v", err)
		}
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

// handlePutMe changes the signed-in member's account. Only what the body names is written, and
// it is written onto the account as it is now, not as it was when the request arrived: a save from
// one device leaves alone what another device saved a moment ago. A body with rev says which state
// of the bio, links and preferences the edit was made against; if that is no longer the state, the
// answer is 409 with the account as it is, and nothing is written.
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
		// The profile state the edit was made against (profileRev); optional.
		Rev *string `json:"rev"`
		// Share the profile beyond this server (docs/federation.md, F2): only in a mode that federates,
		// and not in single mode, where FLOWA_FEDERATE=on is the owner's choice. Kept raw and read below
		// only in such a mode, so that elsewhere no value of it, of any type, can change the answer.
		Federates json.RawMessage `json:"federates"`
	}
	if !readJSON(w, r, 200<<10, &in) {
		return
	}
	// Outside a mode that federates the field is ignored, as every field this handler does not know is, so
	// an instance that sets nothing new answers as it always has, whatever the field holds. In a mode that
	// federates it is true or false; null is the same as leaving it out, as it is for the fields above.
	var federates *bool
	if a.fed != nil && len(in.Federates) > 0 && string(in.Federates) != "null" {
		var on bool
		if json.Unmarshal(in.Federates, &on) != nil {
			fail(w, http.StatusBadRequest, "The field federates must be true or false.")
			return
		}
		federates = &on
	}
	// Everything is checked before the account is read, so the row is locked only briefly.
	var cols []string
	args := []any{u.ID}
	set := func(col string, v any) {
		args = append(args, v)
		cols = append(cols, col+" = $"+strconv.Itoa(len(args)))
	}
	if in.DisplayName != nil {
		d, msg := cleanName(*in.DisplayName)
		if msg != "" {
			fail(w, http.StatusBadRequest, msg)
			return
		}
		set("display_name", d)
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
		b, _ := json.Marshal(*in.Props)
		set("props", string(b))
		set("onboarded", true)
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
		b, _ := json.Marshal(*in.Seen)
		set("seen", string(b))
	}
	if in.Avatar != nil {
		v, err := cleanImage(*in.Avatar, 1024, maxAvatar)
		if err != nil {
			fail(w, http.StatusBadRequest, "That picture cannot be used: "+err.Error()+".")
			return
		}
		set("avatar", v)
	}
	if in.Bio != nil {
		b, ok := visibleText(*in.Bio)
		if !ok {
			fail(w, http.StatusBadRequest, "The bio cannot hold characters that change the direction of the text.")
			return
		}
		// Trimmed after the unseen characters are gone, so the first line always shows something.
		if b = strings.TrimSpace(b); len([]rune(b)) > maxBio {
			fail(w, http.StatusBadRequest, "Keep the bio under 300 characters.")
			return
		}
		set("bio", b)
	}
	// Links and preferences are kept per key: the ones the body names are cleaned here and laid
	// over the stored ones once the row is locked. An empty link takes that link away.
	links := map[string]string{}
	if in.Links != nil {
		for _, k := range linkKinds {
			v, ok := (*in.Links)[k]
			if !ok {
				continue
			}
			h, good := cleanLink(k, v)
			if !good {
				fail(w, http.StatusBadRequest, "That does not look like "+linkNames[k]+" handle or link.")
				return
			}
			links[k] = h
		}
	}
	if in.Prefs != nil {
		for k := range *in.Prefs {
			// autoPost: share milestones; shareTransitions: send anonymous transition marks for suggestions;
			// askTransitions: after a logged combo run, be asked how each transition not yet marked went.
			if k != "autoPost" && k != "shareTransitions" && k != "askTransitions" {
				fail(w, http.StatusBadRequest, "Unknown preference: "+k)
				return
			}
		}
	}
	if len(in.AvatarCfg) > 0 {
		if err := validAvatar(in.AvatarCfg); err != nil {
			fail(w, http.StatusBadRequest, "That avatar cannot be saved: "+err.Error())
			return
		}
		set("avatar_cfg", string(in.AvatarCfg))
	}
	if len(in.Stats) > 0 {
		if !validStats(in.Stats) {
			fail(w, http.StatusBadRequest, "Stats must be a small object of numbers.")
			return
		}
		set("stats", string(in.Stats))
	}
	// In single mode with FLOWA_FEDERATE=on the field is refused with the reason: that mode is new, so
	// nothing relied on the field there, and the owner who sends it should learn that the setting, not the
	// field, decides.
	if federates != nil && a.mode == modeSingle {
		fail(w, http.StatusBadRequest, "On a Flowa that is one person's own, the setting FLOWA_FEDERATE decides whether other servers can find the profile.")
		return
	}
	if federates != nil && *federates && a.reservedName(u.Username) {
		fail(w, http.StatusBadRequest, hostNameRefusal)
		return
	}
	tx, err := a.db.Begin()
	if err != nil {
		a.serverError(w, err)
		return
	}
	defer tx.Rollback()
	cur, err := scanUser(tx.QueryRow(`SELECT `+userCols+` FROM users u WHERE u.id = $1 FOR UPDATE`, u.ID))
	if err != nil {
		a.serverError(w, err)
		return
	}
	cur.Rev = profileRev(cur)
	if in.Rev != nil && *in.Rev != cur.Rev {
		writeJSON(w, http.StatusConflict, map[string]any{"user": cur,
			"error": "Your profile was changed on another device. Your edit is still here: save again to put it on top."})
		return
	}
	if len(links) > 0 {
		all := map[string]string{}
		json.Unmarshal(cur.Links, &all)
		for k, h := range links {
			if h == "" {
				delete(all, k)
			} else {
				all[k] = h
			}
		}
		b, _ := json.Marshal(all)
		set("links", string(b))
	}
	if in.Prefs != nil && len(*in.Prefs) > 0 {
		all := map[string]bool{}
		json.Unmarshal(cur.Prefs, &all)
		for k, v := range *in.Prefs {
			all[k] = v
		}
		b, _ := json.Marshal(all)
		set("prefs", string(b))
	}
	if len(cols) > 0 {
		// The column names come from the fixed list above, never from the request.
		cur, err = scanUser(tx.QueryRow(`UPDATE users u SET `+strings.Join(cols, ", ")+` WHERE u.id = $1 RETURNING `+userCols, args...))
		if err != nil {
			a.serverError(w, err)
			return
		}
		cur.Rev = profileRev(cur)
	}
	// The opt-in is its own column, outside userCols: it is read only in a mode that federates, so no
	// other answer about a member carries it. It is not part of profileRev either: it is a switch saved
	// on its own, not text edited against an older copy. was is what it was before, for the key below.
	var fedOn, fedWas bool
	if a.fed != nil {
		if err := tx.QueryRow(`SELECT federates FROM users WHERE id = $1`, u.ID).Scan(&fedWas); err != nil {
			a.serverError(w, err)
			return
		}
		fedOn = fedWas
		if federates != nil && *federates != fedWas {
			if _, err := tx.Exec(`UPDATE users SET federates = $2 WHERE id = $1`, u.ID, *federates); err != nil {
				a.serverError(w, err)
				return
			}
			fedOn = *federates
		}
	}
	if err := tx.Commit(); err != nil {
		a.serverError(w, err)
		return
	}
	// A member's key is made when they opt in (docs/federation.md 5.7), in the background; a key made
	// once stays when they opt out, and is used again if they come back.
	if fedOn && !fedWas {
		a.fed.requestKey(keyOwner(u.ID))
	}
	// The actor document says the name, the bio, the picture, the props and the links: any save made it
	// old. Whether the member is shown at all is asked on every request, so the opt-in needs no drop; it
	// gets one anyway, so a document never outlives the choice that let it out.
	a.fed.dropActor(keyOwner(u.ID))
	// What the visitor's page of a single-profile instance shows changed: it is made again when next
	// asked. The stats and the other parts the page writes by itself are not on it.
	if in.DisplayName != nil || in.Avatar != nil || in.Bio != nil || in.Links != nil || federates != nil {
		a.dropVisitorPage()
	}
	out := map[string]any{"user": cur}
	if a.fed != nil {
		out["federation"] = a.fedView(cur, fedOn)
	}
	writeJSON(w, http.StatusOK, out)
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
		logRefusal(w, "too many wrong current passwords", "by member "+uid)
		fail(w, http.StatusTooManyRequests, "Too many wrong passwords. Wait ten minutes and try again.")
		return
	}
	if !checkPassword(hash, in.Current) {
		a.lim.password.add(uid)
		fail(w, http.StatusForbidden, "The current password is wrong.")
		return
	}
	if !validPassword(w, in.Next, u.Username) {
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
	// Last active: the latest sign-in, hour of use or progress save. The account keeps its own
	// record (last_seen), because a suspension or a password reset removes the sessions.
	rows, err := a.db.Query(`SELECT ` + userCols + `, GREATEST(u.last_seen, (SELECT max(created_at) FROM sessions s WHERE s.user_id = u.id), (SELECT max(updated_at) FROM progress p WHERE p.user_id = u.id))
		FROM users u WHERE u.domain = '' ORDER BY u.id`)
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
	var owner bool
	err := a.db.QueryRow(`SELECT username, role, owner FROM users WHERE id = $1 AND domain = ''`, id).Scan(&name, &role, &owner)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "No such user.")
		return
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	if owner {
		fail(w, http.StatusForbidden, "The owner's account cannot be deleted.")
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
	// The name of an account that had opted in, whose actor id other servers may hold, is retired in the
	// same transaction (retireSQL), before the row and its key go, in every mode: 4.3 says retired names
	// stay retired whatever the mode, and an instance back in local mode may still hold such accounts. A
	// delete refused below rolls it back with the rest. The name's lock is held until the commit, so no
	// account can be made under the name in between (nameLockSQL).
	if _, err := tx.Exec(nameLockSQL, name); err != nil {
		a.serverError(w, err)
		return
	}
	if _, err := tx.Exec(retireSQL, id); err != nil {
		a.serverError(w, err)
		return
	}
	res, err := tx.Exec(`DELETE FROM users WHERE id = $1 AND domain = '' AND (role <> 'admin' OR (SELECT count(*) FROM users WHERE role = 'admin') > 1)`, id)
	if err == nil {
		if n, _ := res.RowsAffected(); n == 0 {
			fail(w, http.StatusBadRequest, "Flowa needs at least one admin.")
			return
		}
	}
	if err == nil {
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
	if !readJSON(w, r, 4096, &in) {
		return
	}
	// Your own password changes on the profile, with the current one; another admin's never here,
	// or one admin could sign in as another.
	if id == u.ID {
		fail(w, http.StatusBadRequest, "Change your own password on your profile.")
		return
	}
	// The name is read only to refuse a password that repeats it; whether this admin may reset
	// this account at all is decided by the update below.
	var name string
	if err := a.db.QueryRow(`SELECT username FROM users WHERE id = $1 AND domain = ''`, id).Scan(&name); err != nil && !errors.Is(err, sql.ErrNoRows) {
		a.serverError(w, err)
		return
	}
	if !validPassword(w, in.Next, name) {
		return
	}
	res, err := a.db.Exec(`UPDATE users SET pw_hash = $2 WHERE id = $1 AND domain = '' AND role = ANY($3)`, id, hashPassword(in.Next), pq.Array(below(u.Role)))
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

// handleSetRole changes someone's role. Nobody changes their own, the owner's never changes, only
// the owner changes another admin's, and Flowa always keeps an admin.
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
	var owner bool
	err = tx.QueryRow(`SELECT role, owner FROM users WHERE id = $1 AND domain = '' FOR UPDATE`, id).Scan(&old, &owner)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "No such user.")
		return
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	next := in.Role
	if owner && next != old {
		fail(w, http.StatusForbidden, "The owner's role cannot be changed. The owner can hand ownership to another admin first.")
		return
	}
	// Admins are peers: one cannot take another's role away. That is the owner's to decide.
	if old == "admin" && next != "admin" && !u.Owner {
		fail(w, http.StatusForbidden, "Only the owner can change an admin's role.")
		return
	}
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

// handleSetOwner hands ownership to another admin. Only the owner can, and there is always
// exactly one: the two rows change together, and the database refuses a second owner.
func (a *app) handleSetOwner(w http.ResponseWriter, r *http.Request, u *user) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	tx, err := a.db.Begin()
	if err != nil {
		a.serverError(w, err)
		return
	}
	defer tx.Rollback()
	// Both rows are locked in id order, so two requests cannot wait on each other.
	rows, err := tx.Query(`SELECT id, username, role, owner, suspended_until IS NOT NULL AND suspended_until > now() FROM users WHERE id IN ($1, $2) AND domain = '' ORDER BY id FOR UPDATE`, u.ID, id)
	if err != nil {
		a.serverError(w, err)
		return
	}
	var mine, found, suspended bool
	var name, role string
	for rows.Next() {
		var rid int64
		var rname, rrole string
		var rowner, rsusp bool
		if err := rows.Scan(&rid, &rname, &rrole, &rowner, &rsusp); err != nil {
			rows.Close()
			a.serverError(w, err)
			return
		}
		if rid == u.ID {
			mine = rowner
		} else {
			found, name, role, suspended = true, rname, rrole, rsusp
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		a.serverError(w, err)
		return
	}
	if !mine {
		fail(w, http.StatusForbidden, "Only the owner can hand ownership over.")
		return
	}
	if id == u.ID {
		fail(w, http.StatusBadRequest, "You are the owner already.")
		return
	}
	if !found {
		fail(w, http.StatusNotFound, "No such user.")
		return
	}
	if role != "admin" {
		fail(w, http.StatusBadRequest, "Only an admin can become the owner. Make them an admin first.")
		return
	}
	if suspended {
		fail(w, http.StatusBadRequest, "A suspended account cannot become the owner.")
		return
	}
	if _, err := tx.Exec(`UPDATE users SET owner = false WHERE id = $1`, u.ID); err != nil {
		a.serverError(w, err)
		return
	}
	if _, err := tx.Exec(`UPDATE users SET owner = true WHERE id = $1`, id); err != nil {
		a.serverError(w, err)
		return
	}
	if err := a.audit(tx, u.ID, "owner", "user", id, id, "", map[string]any{"from": u.Username, "to": name}); err != nil {
		a.serverError(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		a.serverError(w, err)
		return
	}
	a.notify(id, u.ID, "moderation", 0, "You are now the owner of this Flowa: the one admin whose role cannot be changed, and the only one who can change another admin's.")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ensureOwner makes the longest-standing admin the owner when there is none: on a fresh database
// once the admin account exists, and on one where the admin was made by hand.
func (a *app) ensureOwner() error {
	_, err := a.db.Exec(`UPDATE users SET owner = true WHERE id = (SELECT id FROM users WHERE role = 'admin' ORDER BY id LIMIT 1)
		AND NOT EXISTS (SELECT 1 FROM users WHERE owner)`)
	return err
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
		msg = "You are muted until " + stamp(until) + ", so you cannot post or comment. Reason: " + reason
	case "unmute":
		res, err = tx.Exec(`UPDATE users SET muted_until = NULL, mute_reason = '' WHERE id = $1 AND muted_until > now()`, owner)
		msg = "You can post and comment again."
	case "suspend":
		res, err = tx.Exec(`UPDATE users SET suspended_until = $2, suspend_reason = $3 WHERE id = $1`, owner, until, reason)
		if err == nil {
			_, err = tx.Exec(`DELETE FROM sessions WHERE user_id = $1`, owner)
		}
		detail["until"] = until
		msg = "Your account was suspended until " + stamp(until) + ". Reason: " + reason
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
	// A hide, a restore or a suspension changes what a single-profile instance's page at / lists
	// (federation.md 4.2): it is made again when next asked.
	a.dropVisitorPage()
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

// catalogConcepts are the ideas that carry over from one prop to another (CONCEPTS in
// web/catalog-check.js). A catalog names the ones it teaches and a skill lists the ones it is about;
// a person chooses them in the editor, and nothing works them out from a skill's text.
var catalogConcepts = []string{"plane", "timing", "weave", "isolation", "extension"}

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
	concepts, taught := map[string]bool{}, map[string]bool{}
	if cv, ok := c["concepts"]; ok {
		if m := obj(cv); m == nil {
			E("concepts", "concepts must be an object: a concept and a sentence on what it means for this prop")
		} else {
			for k, v := range m {
				text, _ := v.(string)
				if !slices.Contains(catalogConcepts, k) {
					E("concepts."+k, `concepts has no "%s"; the concepts are %s`, k, strings.Join(catalogConcepts, ", "))
				} else if !str(v, 10, 240) {
					E("concepts."+k, "concept %s: needs a sentence of 10 to 240 characters", k)
				} else if strings.ContainsAny(text, "—–") {
					E("concepts."+k, "concept %s: contains a long dash; use plain punctuation", k)
				} else {
					concepts[k] = true
				}
			}
		}
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
		if cv, ok := sk["concepts"]; ok {
			// The shared ideas this skill is about: up to three, each one the catalog itself names.
			l, isList := cv.([]any)
			seen, words := map[string]bool{}, true
			for _, x := range l {
				k, isWord := x.(string)
				seen[k], words = true, words && isWord
			}
			if !isList || len(l) > 3 || !words || len(seen) != len(l) {
				E(at+".concepts", "%s: concepts must be a list of up to 3 different concepts", who)
			} else {
				for _, x := range l {
					if k, _ := x.(string); !concepts[k] {
						E(at+".concepts", `%s: "%v" is not one of this catalog's concepts`, who, x)
					} else {
						taught[k] = true
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
	for _, k := range catalogConcepts {
		if concepts[k] && !taught[k] {
			W("concepts."+k, "concept %s: no skill names it", k)
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
	// What changed outside the skills: the map's own parts, such as its branches, phases or concepts.
	// Without this a draft that only renames a branch would look the same as the published map.
	var op, np map[string]any
	_ = json.Unmarshal(pub, &op)
	_ = json.Unmarshal(draft, &np)
	whole, parts := []string{}, map[string]bool{}
	for k := range op {
		parts[k] = true
	}
	for k := range np {
		parts[k] = true
	}
	for k := range parts {
		was, _ := json.Marshal(op[k])
		is, _ := json.Marshal(np[k])
		if k != "skills" && string(was) != string(is) {
			whole = append(whole, k)
		}
	}
	sort.Strings(whole)
	errs, warns := validateCatalog(draft)
	writeJSON(w, http.StatusOK, map[string]any{"changes": changes, "map": whole, "errors": errs, "warnings": warns, "blocked": a.safeToPublish(prop, draft, false)})
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
	// No account, and no colon after the name: not a port, and not the empty port of "youtu.be:".
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || strings.Contains(u.Host, ":") {
		return postLink{}, false
	}
	// Only A to Z are folded, so a look-alike letter that Unicode lower-cases to a plain one (the
	// Kelvin sign for k) does not make a name match. web/links.js follows the same rules, and
	// test/links-fixtures.json holds the cases both must agree on.
	host := strings.Map(func(c rune) rune {
		if c >= 'A' && c <= 'Z' {
			return c + 'a' - 'A'
		}
		return c
	}, u.Host)
	host = strings.TrimPrefix(host, "www.")
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

// The fetcher is the server's one way out to other machines (docs/federation.md, 7.8). Link cards
// ask the three video sites about a link from the server, never from a visitor's browser: only the
// hosts below, redirects only between them, 3 s, at most 256 KB of an answer (2 MB of a thumbnail).
// Federation, once it exists, asks any host its domain policy allows, under the stricter rules of
// fetchRules. Every request, of either kind, goes through fetcher.do and its one dialer.
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

	// The transport's own limits are backstops: the deadline of each kind (fetchRules) is shorter
	// and covers the whole request. 10 s is the longest kind, a picture.
	dialTimeout   = 10 * time.Second
	maxRedirects  = 3                // redirects followed, for every kind that follows any
	maxURL        = 2048             // bytes in a federation URL; longer ones are refused unasked
	errBodyCap    = 4 << 10          // bytes read of an answer that is not used (an error, a redirect, a delivery's reply)
	maxRetryAfter = 12 * time.Hour   // the longest Retry-After believed, as the delivery queue does (8.3)
	maxHeaderIn   = 64 << 10         // bytes of response headers; Go's default is 1 MB
	perHost       = 1                // requests at once to one host, as link cards have always done and 8.4 budgets
	idleTimeout   = 30 * time.Second // an idle pooled connection is closed after this
)

// fetchKind names what a request is for. Each kind has one row in fetchRules, and fetcher.do takes
// nothing else, so a later caller cannot pick a method, a header or a cap of its own.
type fetchKind uint8

const (
	fetchPreview   fetchKind = iota // a link card: a page or oEmbed answer, or its thumbnail
	fetchDocument                   // an ActivityPub actor, object or collection
	fetchWebFinger                  // a WebFinger lookup (RFC 7033)
	fetchNodeInfo                   // a NodeInfo discovery document or the document it names
	fetchPicture                    // a remote picture: an avatar, a header, an attachment
	fetchDelivery                   // an activity POSTed to an inbox
	fetchKinds
)

// redirectRule says which redirects a kind follows. Every redirect followed is checked again like the
// first URL (scheme, host, port), its address by the dialer, and at most maxRedirects are followed.
type redirectRule uint8

const (
	redirectNone       redirectRule = iota // a 3xx is a failure: a delivery is never resent elsewhere
	redirectSameOrigin                     // only to the same scheme, host and port: a document's id must be where it came from
	redirectAnyOrigin                      // to another origin too: pictures, and WebFinger as RFC 7033 4.2 allows
	redirectPreview                        // link cards, as always: to any preview or thumbnail host
)

const (
	asType   = `application/ld+json; profile="https://www.w3.org/ns/activitystreams"`
	asAccept = asType + `, application/activity+json`
)

// fetchRule is everything a kind may do.
type fetchRule struct {
	method   string
	accept   string        // the Accept header; "" sends none
	ctype    string        // the Content-Type of a POST's body
	timeout  time.Duration // the whole request: DNS, connect, TLS, headers, every redirect and the body
	limit    int64         // bytes of the answer after inflation; a larger answer is refused, never cut
	types    []string      // media types a successful answer may have, checked before its body is read; nil takes any
	ok       []int         // statuses that are a success
	redirect redirectRule
	fed      bool // the federation host rules, port 443 only, the refused list; false: the caller's allowlist
	signed   bool // a Date header is set and the signer runs on every request sent, redirects included
	keep     bool // the body is read and returned; false: it is drained up to errBodyCap and dropped
}

// fetchRules is the one table of what leaves the server. Documents accept only the two types
// ActivityPub 3.2 names (not plain application/json, which some servers have sent: no recording
// shows Flowa needs it, and a JSON API on a hostile host is not an actor); WebFinger also takes
// application/json, NodeInfo only that (federation.md 7.5). Link cards keep their caller's
// allowlist and cap (fetcher.get), their 3 s and their redirect rule, and check no type, as before.
// Sizes: 1 MB a document, 64 KB a WebFinger or NodeInfo answer (a few KB in practice), 8 MB a
// picture, and 4 KB read and dropped of a delivery's reply. The fetcher allows one request at a time
// to one host and sets no limit across different hosts: N pictures at once can hold 8 MB times N.
// Callers bound their own concurrency, as the delivery queue and the picture cache do with their
// budgets in federation.md section 12.
var fetchRules = [fetchKinds]fetchRule{
	// method, accept, ctype, timeout, limit, types, ok, redirect, fed, signed, keep
	fetchPreview:   {"GET", "", "", fetchTimeout, 0, nil, []int{200}, redirectPreview, false, false, true},
	fetchDocument:  {"GET", asAccept, "", 5 * time.Second, 1 << 20, []string{"application/activity+json", "application/ld+json"}, []int{200}, redirectSameOrigin, true, true, true},
	fetchWebFinger: {"GET", "application/jrd+json, application/json", "", 5 * time.Second, 64 << 10, []string{"application/jrd+json", "application/json"}, []int{200}, redirectAnyOrigin, true, false, true},
	fetchNodeInfo:  {"GET", "application/json", "", 5 * time.Second, 64 << 10, []string{"application/json"}, []int{200}, redirectSameOrigin, true, false, true},
	fetchPicture:   {"GET", "image/jpeg, image/png, image/gif", "", 10 * time.Second, 8 << 20, []string{"image/jpeg", "image/png", "image/gif"}, []int{200}, redirectAnyOrigin, true, false, true},
	fetchDelivery:  {"POST", "", asType, 5 * time.Second, errBodyCap, nil, []int{200, 201, 202, 204}, redirectNone, true, true, false},
}

// signer adds the headers that sign a request (httpSigner: Signature, Digest, and Date again from
// its own reading of the clock). fetcher.do calls it with the request exactly as it will be sent, Date
// already set, once for every request, a redirect included, because a signature covers the host and
// the path. It must not change the method or URL.
type signer func(req *http.Request, body []byte) error

// fetchReq describes one outbound request.
type fetchReq struct {
	kind fetchKind
	url  string
	body []byte // a delivery's activity; nil for a GET
	// refused are the hosts the domain policy refuses, each with its subdomains. Until FMD keeps
	// domain policies the caller passes them; the tests fill them.
	refused []string
	self    bool   // the instance's own origin may be asked (nothing does yet); refused otherwise
	sign    signer // required for a delivery, optional for a document (signed fetch)
	// Link cards only, as fetcher.get has always been given them: the hosts allowed and the cap.
	allow []string
	limit int64
}

// fetchResult is a successful answer.
type fetchResult struct {
	Status     int
	URL        *url.URL // where the answer came from after redirects: the id check (sameID) compares with it
	Type       string   // the answer's media type, lower case, without parameters
	Body       []byte
	RetryAfter time.Duration // from Retry-After, bounded to 12 hours; 0 when absent
}

// fetchFail is what went wrong, for callers that decide by it: the key refetch rule, the delivery
// queue's retries and the host circuit breaker.
type fetchFail uint8

const (
	failRefused  fetchFail = iota + 1 // refused by a rule: the URL, the host, the port, or the address at connect time
	failConnect                       // could not connect: the name did not resolve, the connection or TLS failed
	failTimeout                       // the kind's deadline passed, or the caller's
	failCanceled                      // the caller gave up
	failTooLarge                      // the answer is over the kind's cap
	failType                          // the answer's media type is not one the kind takes
	failGone                          // 410: the object or actor was deleted
	failNotFound                      // 404
	failStatus                        // another status; Status and RetryAfter say which and when to ask again
	failRedirect                      // a redirect the kind does not follow, or too many
	failBusy                          // no turn at the host within the kind's time: this server's own queue, not the host's fault; ask again later
)

// fetchError is every error fetcher.do returns. Reason is short and safe to log: it never holds a
// body, a header value or a URL's query.
type fetchError struct {
	Fail       fetchFail
	Host       string
	Reason     string
	Status     int
	RetryAfter time.Duration
	err        error // the cause, for errors.Is (a context error); not printed
}

func (e *fetchError) Error() string { return e.Host + ": " + e.Reason }
func (e *fetchError) Unwrap() error { return e.err }

// addrRefused is the dialer's refusal, told apart from a failed connection.
type addrRefused struct{ what string }

func (e *addrRefused) Error() string { return "refusing to connect to " + e.what }

// dialOverride lets a test reach its local stub without weakening the guard for anything else. It is
// a field set by the test, never a setting: resolve stands in for DNS, every address it gives is still
// checked by dialGuard, and only the exact stub address passes whatever its port and address.
type dialOverride struct {
	resolve func(host string) []string // host to "ip:port" addresses, in the order a resolver would give them
	stub    string                     // the stub's listening address
}

type fetcher struct {
	client *http.Client
	tr     *http.Transport
	// base, set only by tests (FLOWA_PREVIEW_BASE), sends every link card request to one stub server
	// instead. Federation kinds are refused while it is set: the stub weakens the address guard.
	base         string
	allowPrivate bool
	mu           sync.Mutex
	busy         map[string]*hostSlot // requests in flight per host; an entry goes when nobody uses it
	off          bool                 // FLOWA_PREVIEW_FETCH=off: never fetch a link card
	origin       publicOrigin         // this instance, in a mode that federates: never asked unless a request says so
	version      string               // in the User-Agent
	over         *dialOverride        // tests only
	// deny are more address ranges never dialled, beside reservedNets: the instance's own public
	// address and the router's, which a stranger's name can point at and which look public to the
	// guard. Empty until the federation settings item lets an operator list them.
	deny []netip.Prefix
	// dials counts the connections this fetcher tried to open: allowed, refused, or to a name that
	// did not resolve. Nothing reads it while the server runs; TestDBLocalModeUnchanged holds it at
	// zero through a start.
	dials atomic.Int64
}

// hostSlot holds the requests in flight to one host: at most perHost at once, the others waiting for
// as long as their caller allows. A fixed mutex per host, as link cards had, would keep one entry for
// every host ever asked, and federation asks any host.
type hostSlot struct {
	ch    chan struct{}
	users int
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
// documentation, "this network", the reserved class E block, the deprecated 6to4 relay and site-local
// ranges, discard-only, and the IPv6 ranges that carry an IPv4 address inside (IPv4-compatible,
// IPv4-translated, NAT64, 6to4 and Teredo), each of which can lead back to a private IPv4, and the
// IPv6 blocks that are never routed on the internet (ORCHID, SRv6 segment ids).
// IPv4-mapped addresses (::ffff:a.b.c.d) need no row: net.IP checks them as the IPv4 they carry.
var reservedNets = func() []*net.IPNet {
	out := []*net.IPNet{}
	for _, c := range []string{
		"100.64.0.0/10", "192.0.0.0/24", "198.18.0.0/15", "0.0.0.0/8", "240.0.0.0/4", "64:ff9b::/96", "64:ff9b:1::/48",
		"192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24", "192.88.99.0/24", // documentation, 6to4 relay
		"::/96", "::ffff:0:0:0/96", "2002::/16", "2001::/32", // IPv4-compatible, IPv4-translated, 6to4, Teredo
		"2001:db8::/32", "3fff::/20", "2001:2::/48", "100::/64", "fec0::/10", // documentation, benchmarking, discard, site-local
		"2001:10::/28", "2001:20::/28", "5f00::/16", // ORCHID and ORCHIDv2 (never routed), SRv6 segment ids
	} {
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

// dialGuard is the dialer's Control function: it runs on the address actually being connected, after
// DNS, once for every address tried (each address of an answer with several, each redirect, each new
// connection), and before the connection is made. A name cannot answer a public address to a check and
// a private one to the dial, because there is no earlier check: this is the only one. A pooled
// connection was dialled through it too, and is reused only for the same scheme, host and port.
// Only port 443 is dialled; the link card stub (allowPrivate) and a test's stub are the exceptions.
// deny is the fetcher's own list of ranges never dialled (fetcher.deny), whatever allowPrivate says.
func dialGuard(address string, allowPrivate bool, over *dialOverride, deny []netip.Prefix) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if over != nil && address == over.stub {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || (!allowPrivate && !publicIP(ip)) {
		return &addrRefused{host}
	}
	if a, ok := netip.AddrFromSlice(ip); ok {
		a = a.Unmap()
		for _, p := range deny {
			if p.Contains(a) {
				return &addrRefused{host}
			}
		}
	}
	if !allowPrivate && port != "443" {
		return &addrRefused{"port " + port}
	}
	return nil
}

func newFetcher(base string, allowPrivate bool) *fetcher {
	f := &fetcher{base: strings.TrimRight(base, "/"), allowPrivate: allowPrivate, busy: map[string]*hostSlot{}, version: buildVersion}
	dialer := &net.Dialer{Timeout: dialTimeout, Control: func(network, address string, _ syscall.RawConn) error {
		return dialGuard(address, allowPrivate, f.over, f.deny)
	}}
	// Counted before the name is looked up: a fetch to a name that does not resolve asked a name
	// server, and that is a connection to another machine too.
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		f.dials.Add(1)
		if f.over != nil {
			return f.over.dial(ctx, dialer, network, address)
		}
		return dialer.DialContext(ctx, network, address)
	}
	// Proxy stays nil: a proxy from HTTPS_PROXY would make the connection for us, to an address the
	// guard never sees. No cookie jar, and TLS verified as Go does by default, at least TLS 1.2.
	f.tr = &http.Transport{
		Proxy:                  nil,
		DialContext:            dial,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:    dialTimeout,
		ResponseHeaderTimeout:  dialTimeout,
		MaxResponseHeaderBytes: maxHeaderIn,
		MaxIdleConns:           8,
		MaxIdleConnsPerHost:    1,
		IdleConnTimeout:        idleTimeout,
	}
	// Redirects are followed by do, which checks each one by the kind's rule.
	f.client = &http.Client{Transport: f.tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return f
}

// dial connects to the addresses resolve gives for the host, in turn, each through the guard, as
// net.Dialer does with the addresses of a DNS answer.
func (o *dialOverride) dial(ctx context.Context, d *net.Dialer, network, address string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addrs := o.resolve(host)
	if len(addrs) == 0 {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	var errs []error
	for _, a := range addrs {
		c, err := d.DialContext(ctx, network, a)
		if err == nil {
			return c, nil
		}
		errs = append(errs, err)
	}
	return nil, errors.Join(errs...)
}

// fedHost says why a host may not be asked for a federation kind, or "" when it may, and gives the
// host in the form compared with the refused list: lower case, without a trailing dot. Refused before
// any connection: an IP address in any form url.Parse or a resolver accepts (dotted, bracketed, and
// the decimal, octal and hex forms of inet_aton such as 2130706433 or 0x7f.1, which all end in a
// numeric label), a name with no dot, localhost, the private names allowedHost lets through for the
// home network, and anything but ASCII letters, digits, dots and dashes (Go turns a name in another
// script into its xn-- form on the way out, which the refused list would not have seen).
func fedHost(host string) (string, string) {
	h := strings.TrimSuffix(strings.ToLower(host), ".")
	switch {
	case h == "":
		return h, "no host"
	case len(h) > 253:
		return h, "a host name over 253 characters"
	case net.ParseIP(h) != nil || strings.Contains(h, ":"):
		return h, "an IP address"
	case strings.Trim(h, "abcdefghijklmnopqrstuvwxyz0123456789.-") != "":
		return h, "not a plain DNS name"
	case h == "localhost" || strings.HasSuffix(h, ".localhost"):
		return h, "localhost"
	case !strings.Contains(h, "."):
		return h, "a name with no dot"
	case strings.Contains(h, ".."), strings.HasPrefix(h, "."):
		return h, "an empty label"
	}
	for _, s := range privateSuffixes {
		if strings.HasSuffix(h, s) {
			return h, "a private name"
		}
	}
	last := h[strings.LastIndexByte(h, '.')+1:]
	if strings.HasPrefix(last, "0x") || strings.Trim(last, "0123456789") == "" {
		return h, "an IP address in numeric form"
	}
	return h, ""
}

// check says why a URL may not be asked for a kind, or "" when it may. It runs on the first URL and
// on every redirect.
func (f *fetcher) check(rule *fetchRule, r *fetchReq, refused []string, u *url.URL) string {
	if !rule.fed {
		if u.Scheme != "https" || !hostAllowed(u.Hostname(), r.allow) {
			return "not an allowed URL"
		}
		return ""
	}
	if u.Scheme != "https" {
		return "not https"
	}
	if u.User != nil {
		return "a user name in the URL"
	}
	if p := u.Port(); p != "" && p != "443" {
		return "a port other than 443"
	}
	host, why := fedHost(u.Hostname())
	if why != "" {
		return why
	}
	if hostAllowed(host, refused) {
		return "a refused host"
	}
	if f.origin.host != "" && host == f.origin.host && !r.self {
		return "this instance's own origin"
	}
	return ""
}

// refusedHosts builds the refused set from a domain policy's list: each entry lower case without a
// trailing dot, matched with its subdomains. An entry that is not a plain ASCII DNS name is an error,
// and the request is refused: the standard library cannot turn bücher.example into the
// xn--bcher-kva.example a URL carries (golang.org/x/net/idna is not a dependency), so a list in
// Unicode would let that very host through. Whatever stores the list (FMD's admin page) must store
// the xn-- form.
func refusedHosts(list []string) ([]string, error) {
	out := make([]string, 0, len(list))
	for _, h := range list {
		n := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
		if n == "" || len(n) > 253 || strings.Trim(n, "abcdefghijklmnopqrstuvwxyz0123456789.-") != "" || strings.Contains(n, "..") || strings.HasPrefix(n, ".") {
			return nil, errors.New("a refused host that is not a plain ASCII DNS name (write a name in another script in its xn-- form)")
		}
		out = append(out, n)
	}
	return out, nil
}

// slotKey is the name a host's turn is kept under: lower case, without a trailing dot, so
// victim.example and VICTIM.example. wait for the same turn.
func slotKey(host string) string { return strings.TrimSuffix(strings.ToLower(host), ".") }

// originOf is a URL's origin, compared whole: scheme, host and port, the default port written out.
func originOf(u *url.URL) string {
	scheme := strings.ToLower(u.Scheme)
	port := u.Port()
	if port == "" {
		port = map[string]string{"https": "443", "http": "80"}[scheme]
	}
	return scheme + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}

// sameID says whether a fetched document's id is the URL it was fetched from (federation.md 7.3):
// the fragment removed, the origins compared whole (host case and a written default port do not
// matter), the path and query compared as written (a trailing slash does).
func sameID(id string, fetched *url.URL) bool {
	u, err := url.Parse(id)
	if err != nil || fetched == nil || !u.IsAbs() || u.Opaque != "" || u.User != nil || u.Host == "" {
		return false
	}
	return originOf(u) == originOf(fetched) && u.EscapedPath() == fetched.EscapedPath() && u.RawQuery == fetched.RawQuery
}

// retryAfter reads Retry-After, in seconds or as a date, bounded to 0 and 12 hours.
func retryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	var d time.Duration
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		d = time.Duration(min(max(n, 0), int64(maxRetryAfter/time.Second))) * time.Second
	} else if t, err := http.ParseTime(v); err == nil {
		d = t.Sub(now)
	}
	return min(max(d, 0), maxRetryAfter)
}

// acquire waits for a place among the requests to host, for as long as ctx allows.
func (f *fetcher) acquire(ctx context.Context, host string) (func(), error) {
	f.mu.Lock()
	s := f.busy[host]
	if s == nil {
		s = &hostSlot{ch: make(chan struct{}, perHost)}
		f.busy[host] = s
	}
	s.users++
	f.mu.Unlock()
	leave := func() {
		f.mu.Lock()
		if s.users--; s.users == 0 {
			delete(f.busy, host)
		}
		f.mu.Unlock()
	}
	select {
	case s.ch <- struct{}{}:
		return func() { <-s.ch; leave() }, nil
	case <-ctx.Done():
		leave()
		return nil, ctx.Err()
	}
}

// turn waits for a turn at host while wait allows, and tells why it got none: the caller gave up
// (parent cancelled or past its own deadline), or the host stayed busy with this server's other
// requests (failBusy, which the delivery queue retries like any temporary failure).
func (f *fetcher) turn(wait, parent context.Context, host string, rule *fetchRule) (func(), error) {
	release, err := f.acquire(wait, host)
	switch {
	case err == nil:
		return release, nil
	case errors.Is(parent.Err(), context.Canceled):
		return nil, &fetchError{Fail: failCanceled, Host: host, Reason: "canceled", err: parent.Err()}
	case parent.Err() != nil:
		return nil, &fetchError{Fail: failTimeout, Host: host, Reason: "timed out", err: context.DeadlineExceeded}
	}
	return nil, &fetchError{Fail: failBusy, Host: host, Reason: fmt.Sprintf("no turn at %s within %v", host, rule.timeout), err: err}
}

// do sends one request as its kind allows and returns the answer, or a *fetchError. What it
// guarantees, for every kind: the URL and every redirect checked before anything is dialled, every
// address checked by the dialer, one deadline over the whole request, the answer's status and type
// checked before its body is read, and the body read up to the cap and refused above it (after
// inflation: Go's transport inflates gzip itself). It logs nothing.
func (f *fetcher) do(ctx context.Context, r fetchReq) (*fetchResult, error) {
	if r.kind >= fetchKinds {
		return nil, &fetchError{Fail: failRefused, Reason: "an unknown kind of request"}
	}
	rule := &fetchRules[r.kind]
	refuse := func(host, why string) (*fetchResult, error) {
		return nil, &fetchError{Fail: failRefused, Host: host, Reason: why}
	}
	if rule.fed && len(r.url) > maxURL {
		return refuse("", "a URL over 2048 bytes")
	}
	u, err := url.Parse(r.url)
	if err != nil {
		return refuse("", "not a URL")
	}
	host := slotKey(u.Hostname())
	refused, err := refusedHosts(r.refused)
	if err != nil {
		return refuse(host, err.Error())
	}
	if why := f.check(rule, &r, refused, u); why != "" {
		return refuse(host, why)
	}
	switch {
	case rule.fed && (f.base != "" || f.allowPrivate):
		return refuse(host, "the link card stub is set, which lets private addresses through")
	case rule.fed && r.allow != nil:
		return refuse(host, "an allowlist is for link cards")
	case rule.method == "GET" && r.body != nil:
		return refuse(host, "a body on a GET")
	case r.kind == fetchDelivery && r.sign == nil:
		return refuse(host, "a delivery is never sent unsigned")
	}
	limit := rule.limit
	if !rule.fed {
		limit = r.limit
	}
	// The address on the wire: the URL itself, or for link cards in a test, the stub's /<host><path>.
	wire := u
	if !rule.fed && f.base != "" {
		s := f.base + "/" + u.Hostname() + u.EscapedPath()
		if u.RawQuery != "" {
			s += "?" + u.RawQuery
		}
		if wire, err = url.Parse(s); err != nil {
			return refuse(host, "not a URL")
		}
	}

	// One request at a time per host, counted at the host each request is actually sent to: the turn
	// moves with a redirect to another host, and only one turn is held at once, so two requests
	// redirected across each other cannot wait on each other. The first wait is capped at the kind's
	// time, and the kind's deadline starts once the turn is had, as link cards always did.
	parent := ctx
	wait, stop := context.WithTimeout(parent, rule.timeout)
	release, err := f.turn(wait, parent, host, rule)
	stop()
	if err != nil {
		return nil, err
	}
	defer func() { release() }()
	ctx, cancel := context.WithTimeout(parent, rule.timeout)
	defer cancel()
	// moveTo hands the turn over to the host a redirect leads to, within what is left of the deadline.
	moveTo := func(to string) error {
		if to = slotKey(to); to == host {
			return nil
		}
		release()
		release = func() {}
		next, err := f.turn(ctx, parent, to, rule)
		if err != nil {
			return err
		}
		release, host = next, to
		return nil
	}

	for hops := 0; ; hops++ {
		resp, err := f.send(ctx, rule, &r, wire)
		if err != nil {
			return nil, f.failure(ctx, parent, host, err)
		}
		if !isRedirect(resp.StatusCode) || resp.Header.Get("Location") == "" {
			return f.answer(ctx, parent, rule, limit, host, wire, resp)
		}
		loc := resp.Header.Get("Location")
		io.CopyN(io.Discard, resp.Body, errBodyCap)
		resp.Body.Close()
		if rule.redirect == redirectNone {
			// Following would send the signed body again to an address its signature did not name;
			// the delivery queue tries again later instead.
			return nil, &fetchError{Fail: failRedirect, Host: host, Status: resp.StatusCode, Reason: fmt.Sprintf("answered %d; a delivery is not redirected", resp.StatusCode)}
		}
		if hops >= maxRedirects {
			return nil, &fetchError{Fail: failRedirect, Host: host, Reason: "too many redirects"}
		}
		next, err := wire.Parse(loc)
		if err != nil {
			return refuse(host, "a redirect to something that is not a URL")
		}
		if rule.redirect == redirectPreview {
			// Link cards: to any preview or thumbnail host, over https. In a test the redirect stays on
			// the stub (whose base is http) and names an allowed host as its first segment.
			to := next.Hostname()
			if f.base != "" {
				if !strings.HasPrefix(next.String(), f.base+"/") {
					return refuse(host, "redirect off the stub refused")
				}
				to, _, _ = strings.Cut(strings.TrimPrefix(next.Path, "/"), "/")
			} else if next.Scheme != "https" {
				return refuse(host, "redirect to not https refused")
			}
			if !hostAllowed(to, append(append([]string{}, previewHosts...), thumbHosts...)) {
				return refuse(host, fmt.Sprintf("redirect to %s refused", to))
			}
			if err := moveTo(to); err != nil {
				return nil, err
			}
			wire = next
			continue
		}
		if why := f.check(rule, &r, refused, next); why != "" {
			return refuse(host, "redirect to "+why)
		}
		if rule.redirect == redirectSameOrigin && originOf(next) != originOf(wire) {
			return nil, &fetchError{Fail: failRedirect, Host: host, Reason: "a redirect to another origin"}
		}
		if err := moveTo(next.Hostname()); err != nil {
			return nil, err
		}
		wire = next
	}
}

func isRedirect(code int) bool {
	switch code {
	case 301, 302, 303, 307, 308:
		return true
	}
	return false
}

// send makes one request, with only the headers the kind sends: built afresh for every redirect, so
// no Authorization, Cookie or signature header is carried from one request to the next, and no
// Referer is ever sent. Accept-Encoding is left to the transport, which then inflates gzip itself.
func (f *fetcher) send(ctx context.Context, rule *fetchRule, r *fetchReq, wire *url.URL) (*http.Response, error) {
	var body io.Reader
	if r.body != nil {
		body = bytes.NewReader(r.body)
	}
	req, err := http.NewRequestWithContext(ctx, rule.method, wire.String(), body)
	if err != nil {
		return nil, err
	}
	if rule.fed {
		ua := "Flowa/" + f.version
		if f.origin.url != "" {
			ua += " (+" + f.origin.url + "/)"
		}
		req.Header.Set("User-Agent", ua)
	} else {
		req.Header.Set("User-Agent", "Flowa link preview (+self-hosted)")
	}
	if rule.accept != "" {
		req.Header.Set("Accept", rule.accept)
	}
	if rule.ctype != "" {
		req.Header.Set("Content-Type", rule.ctype)
	}
	if rule.signed {
		req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
		if r.sign != nil {
			before := req.Method + " " + req.URL.String()
			if err := r.sign(req, r.body); err != nil {
				return nil, &fetchError{Fail: failRefused, Host: wire.Hostname(), Reason: "could not sign the request", err: err}
			}
			if req.Method+" "+req.URL.String() != before {
				return nil, &fetchError{Fail: failRefused, Host: wire.Hostname(), Reason: "the signer changed the request line"}
			}
		}
	}
	return f.client.Do(req)
}

// answer checks a final answer and reads its body within the cap.
func (f *fetcher) answer(ctx, parent context.Context, rule *fetchRule, limit int64, host string, wire *url.URL, resp *http.Response) (*fetchResult, error) {
	defer resp.Body.Close()
	ra := retryAfter(resp.Header.Get("Retry-After"), time.Now())
	if !slices.Contains(rule.ok, resp.StatusCode) {
		io.CopyN(io.Discard, resp.Body, errBodyCap)
		e := &fetchError{Fail: failStatus, Host: host, Status: resp.StatusCode, RetryAfter: ra, Reason: fmt.Sprintf("%s answered %d", host, resp.StatusCode)}
		switch resp.StatusCode {
		case http.StatusNotFound:
			e.Fail = failNotFound
		case http.StatusGone:
			e.Fail = failGone
		}
		return nil, e
	}
	mt, params, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	// ld+json is an Activity Streams document only without a profile or with that one among its profiles.
	otherProfile := mt == "application/ld+json" && params["profile"] != "" && !slices.Contains(strings.Fields(params["profile"]), "https://www.w3.org/ns/activitystreams")
	if rule.types != nil && (!slices.Contains(rule.types, mt) || otherProfile) {
		return nil, &fetchError{Fail: failType, Host: host, Status: resp.StatusCode, Reason: "an answer of a type this request does not take"}
	}
	res := &fetchResult{Status: resp.StatusCode, URL: wire, Type: mt, RetryAfter: ra}
	if !rule.keep {
		io.CopyN(io.Discard, resp.Body, errBodyCap)
		return res, nil
	}
	tooLarge := &fetchError{Fail: failTooLarge, Host: host, Status: resp.StatusCode, Reason: fmt.Sprintf("answer from %s is too large", host)}
	if resp.ContentLength > limit {
		return nil, tooLarge
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, f.failure(ctx, parent, host, err)
	}
	if int64(len(body)) > limit {
		return nil, tooLarge
	}
	res.Body = body
	return res, nil
}

// failure turns an error from the transport into a *fetchError. The reason is one of a few fixed
// phrases: Go's errors carry the URL, which may have a query, and sometimes text the remote sent.
func (f *fetcher) failure(ctx, parent context.Context, host string, err error) error {
	var fe *fetchError
	if errors.As(err, &fe) {
		return fe
	}
	var ar *addrRefused
	var ne net.Error
	var dns *net.DNSError
	var cert *tls.CertificateVerificationError
	var rec tls.RecordHeaderError
	switch {
	case errors.As(err, &ar):
		return &fetchError{Fail: failRefused, Host: host, Reason: ar.Error(), err: err}
	case errors.Is(parent.Err(), context.Canceled):
		return &fetchError{Fail: failCanceled, Host: host, Reason: "canceled", err: parent.Err()}
	case ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()):
		return &fetchError{Fail: failTimeout, Host: host, Reason: "timed out", err: context.DeadlineExceeded}
	case errors.As(err, &dns):
		return &fetchError{Fail: failConnect, Host: host, Reason: "could not connect: the name did not resolve", err: err}
	case errors.As(err, &cert), errors.As(err, &rec):
		return &fetchError{Fail: failConnect, Host: host, Reason: "could not connect: TLS failed", err: err}
	}
	return &fetchError{Fail: failConnect, Host: host, Reason: "could not connect", err: err}
}

// get fetches a link card URL from an allowed host and returns at most limit bytes of the body.
func (f *fetcher) get(ctx context.Context, target string, hosts []string, limit int64) ([]byte, *url.URL, error) {
	res, err := f.do(ctx, fetchReq{kind: fetchPreview, url: target, allow: hosts, limit: limit})
	if err != nil {
		return nil, nil, err
	}
	return res.Body, res.URL, nil
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
	w, ok := allowed(w, a.lim.marks, u, "suggestion updates")
	if !ok {
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
		// The week this pair first counted toward the fit (Works or Blocked from someone), for the
		// dashboard's pace. A week and not a moment, so that it cannot be laid next to who was
		// signed in; gone again when the last such mark is withdrawn.
		if _, err := tx.Exec(`UPDATE transition_counts SET first_week = CASE WHEN works > 0 OR blocked > 0 THEN COALESCE(first_week, date_trunc('week', now())::date) END
			WHERE prop = $1 AND a = $2 AND b = $3`, in.Prop, d.A, d.B); err != nil {
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

// countsToken reads FLOWA_COUNTS_TOKEN: the secret the fit workflow sends to read the shared
// transition counts of every prop without an account. Empty means there is none. A short one is
// refused at start, because this is the only thing between the internet and that answer.
func countsToken(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if len(s) < 32 || strings.ContainsAny(s, " \t\r\n") {
		return "", fmt.Errorf("FLOWA_COUNTS_TOKEN must be at least 32 characters with no spaces (for example: openssl rand -hex 32); the one given has %d", len(s))
	}
	return s, nil
}

// countsByToken answers GET /api/suggest/counts for the fit workflow (.github/workflows/fit_suggest.yml):
// a request that carries "Authorization: Bearer <FLOWA_COUNTS_TOKEN>" gets the counts of every prop
// without signing in. The counts name no member, so the token opens nothing personal. Any other
// request goes on to next, which wants a session as before.
func (a *app) countsByToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got, bearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !bearer {
			next(w, r)
			return
		}
		if a.countsTok == "" || subtle.ConstantTimeCompare([]byte(got), []byte(a.countsTok)) != 1 {
			logRefusal(w, "a token that does not open the counts", "from "+limitKey(a.clientIP(r)))
			fail(w, http.StatusUnauthorized, "That token does not open the counts.")
			return
		}
		a.writeCounts(w, "")
	}
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
	a.writeCounts(w, prop)
}

// writeCounts writes the pairs of one prop, or of every prop when prop is "", that anyone has marked.
func (a *app) writeCounts(w http.ResponseWriter, prop string) {
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
	w, ok := allowed(w, a.lim.marks, u, "suggestion updates")
	if !ok {
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

// The admin dashboard's four questions. The first three read small tables whole, on purpose: users,
// sessions and progress hold a row or a few per member, and open reports are few by their nature.
const dashTotalsSQL = `SELECT count(*), count(*) FILTER (WHERE role = 'moderator'), count(*) FILTER (WHERE role = 'admin') FROM users WHERE domain = ''`

// Active: signed in or saved progress within the window.
const dashActiveSQL = `WITH act AS (SELECT user_id, created_at AS t FROM sessions UNION ALL SELECT user_id, updated_at FROM progress
		UNION ALL SELECT id, last_seen FROM users WHERE last_seen IS NOT NULL AND domain = '')
	SELECT count(DISTINCT user_id) FILTER (WHERE t > now() - interval '7 days'), count(DISTINCT user_id) FILTER (WHERE t > now() - interval '30 days') FROM act`

const dashOpenSQL = `SELECT count(DISTINCT (target_kind, target_id)) FROM reports WHERE status = 'open'`

// dashWeeksSQL counts sessions, posts, comments and reports for each of the last $1 weeks. A week is
// the rows from its Monday up to the next, written as a range so that posts_created and
// comments_created (migration 6) answer it: asked as date_trunc('week', created_at) = week, every
// week read every post and every comment, a third of a second for twelve weeks of 10,000 posts.
// The practice sessions live inside the progress documents; they are unpacked once (MATERIALIZED),
// not once per week.
const dashWeeksSQL = `WITH weeks AS (SELECT generate_series(date_trunc('week', now()) - ($1 - 1) * interval '1 week', date_trunc('week', now()), interval '1 week') AS w),
	sess AS MATERIALIZED (SELECT w, count(*) AS n FROM (SELECT CASE WHEN s->>'date' ~ '^\d{4}-\d{2}-\d{2}$' THEN date_trunc('week', (s->>'date')::date) END AS w
		FROM progress, jsonb_array_elements(CASE WHEN jsonb_typeof(state->'sessions') = 'array' THEN state->'sessions' ELSE '[]' END) s) x GROUP BY w)
	SELECT weeks.w,
		COALESCE((SELECT n FROM sess WHERE sess.w = weeks.w), 0),
		(SELECT count(*) FROM posts p WHERE p.created_at >= weeks.w AND p.created_at < weeks.w + interval '1 week'),
		(SELECT count(*) FROM comments c WHERE c.created_at >= weeks.w AND c.created_at < weeks.w + interval '1 week'),
		(SELECT count(*) FROM reports r WHERE r.created_at >= weeks.w AND r.created_at < weeks.w + interval '1 week')
	FROM weeks ORDER BY weeks.w`

// gatePairs is how many pairs of skills need a shared Works or Blocked mark before the suggestion
// weights can be fitted and measured. It is MIN_PAIRS in sim/fit-suggest.js; TestGate holds the two equal.
const gatePairs = 30

// gateSQL counts the pairs that count toward the fit, and those among them that first counted in one
// of the four whole weeks before this one.
const gateSQL = `SELECT count(*), count(*) FILTER (WHERE first_week >= (date_trunc('week', now()) - interval '4 weeks')::date AND first_week < date_trunc('week', now())::date)
	FROM transition_counts WHERE works > 0 OR blocked > 0`

// gateOpens says when the fit can run: "now" with enough pairs, the Monday of the week it would be
// reached at the pace of the last four weeks (recent pairs in four weeks), or "" when that pace is
// nothing, because then there is no honest date to give.
func gateOpens(pairs, recent int, now time.Time) string {
	if pairs >= gatePairs {
		return "now"
	}
	if recent <= 0 {
		return ""
	}
	// Whole weeks still needed, rounded up: (gatePairs - pairs) pairs at recent/4 a week.
	weeks := ((gatePairs-pairs)*4 + recent - 1) / recent
	y, m, d := now.UTC().Date()
	monday := time.Date(y, m, d, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -((int(now.UTC().Weekday()) + 6) % 7))
	return monday.AddDate(0, 0, 7*weeks).Format("2006-01-02")
}

// handleDashboard returns the numbers on the admin dashboard: totals, active users, and per week
// for the last 12 weeks (Monday to Sunday) the practice sessions logged, posts, comments and reports.
func (a *app) handleDashboard(w http.ResponseWriter, _ *http.Request, _ *user) {
	out := map[string]any{"version": a.version, "mode": string(a.mode)}
	// In a mode that federates, the origin and what this version of federation does, or why it is
	// refused: the dashboard says it in its mode line, as the start log does.
	if a.fed != nil {
		// announced is when the origin was first announced and frozen (ensureOrigin), or "" before that.
		var announced string
		if err := a.db.QueryRow(`SELECT COALESCE((SELECT value #>> '{}' FROM instance_settings WHERE key = 'first_federated_at'), '')`).Scan(&announced); err != nil {
			a.serverError(w, err)
			return
		}
		out["federation"] = map[string]string{"origin": a.fed.origin.url, "refused": a.fed.refusal(), "built": fedBuilt, "announced": announced}
	}
	var users, mods, admins, active7, active30, open int
	err := a.db.QueryRow(dashTotalsSQL).Scan(&users, &mods, &admins)
	if err == nil {
		err = a.db.QueryRow(dashActiveSQL).Scan(&active7, &active30)
	}
	if err == nil {
		err = a.db.QueryRow(dashOpenSQL).Scan(&open)
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	totals := map[string]int{"users": users, "moderators": mods, "admins": admins, "active7": active7, "active30": active30, "openReports": open}
	// Requests to join are not members, so they are counted on their own, beside the open reports.
	// The count is in the answer only while sign-up is by approval or a request still waits, so an
	// instance that never chose approval answers as before.
	info, err := a.instanceCached()
	if err == nil {
		var waiting int
		if err = a.db.QueryRow(`SELECT count(*) FROM join_requests`).Scan(&waiting); err == nil && (waiting > 0 || info.Registration == regApproval) {
			totals["waiting"] = waiting
		}
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	out["totals"] = totals
	rows, err := a.db.Query(dashWeeksSQL, dashWeeks)
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
	// How far the shared marks are from the fit: pairs marked Works or Blocked by someone, and how
	// many of them first counted in the four whole weeks before this one, which is the pace.
	var pairs, recent int
	if err := a.db.QueryRow(gateSQL).Scan(&pairs, &recent); err != nil {
		a.serverError(w, err)
		return
	}
	out["suggest"] = map[string]any{"shown": shown, "added": added, "worked": worked,
		"pairs": pairs, "gate": gatePairs, "recent": recent, "opens": gateOpens(pairs, recent, time.Now())}
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

/* ---------- federation: the public origin, the key secret and the keys ----------

The foundation of federation (docs/federation.md, F7, 4.3, 5.1 and 5.7). All of it runs only in a mode
that federates: federated mode, or single mode with FLOWA_FEDERATE=on (modeFederates). What talks to
another server is built as far as reading: discovery (WebFinger and NodeInfo) and the actor documents
and public pages, further down; there is no inbox and nothing is delivered yet. */

// fedMemberBuilt is what a member is told in Settings about what sharing does in this version, as the
// server sends it (fedView), and fedSwitch the switch's label, the opt-in of F2 in the note's words: other
// servers can find the handle and read the profile and the Public posts, and nobody can follow yet.
const (
	fedSwitch      = "Share my profile and Public posts beyond this server"
	fedMemberBuilt = "Anyone, on other servers or in a browser, can read your profile and the posts you mark Public, without signing in. Nobody there can follow you yet, and nothing is sent to them. Practice, progress, notes, stats and badges never leave this server."
	fedUnavailable = "Sharing beyond this server is not available at the moment. Other servers cannot find anyone here."
)

// modeFederates says whether a mode talks to other servers (docs/federation.md, 4.1). Federated mode
// does, always: FLOWA_FEDERATE is implied there and ignored. Single mode does when FLOWA_FEDERATE is on;
// unset, blank or off is off, with capitals and spaces around the word forgiven as for FLOWA_MODE, and
// any other value is an error, so a typing mistake cannot leave an operator thinking the profile is
// announced when it is not (or the other way round). Local mode never does, and ignores the setting.
func modeFederates(m instanceMode, federate string) (bool, error) {
	switch m {
	case modeFederated:
		return true, nil
	case modeSingle:
		switch strings.ToLower(strings.TrimSpace(federate)) {
		case "", "off":
			return false, nil
		case "on":
			return true, nil
		}
		if len(federate) > 40 {
			federate = federate[:40] // as parseMode cuts it
		}
		return false, fmt.Errorf("FLOWA_FEDERATE must be on or off, not %q (FLOWA_MODE=single reads it; unset is off)", federate)
	}
	return false, nil
}

// publicOrigin is the address other servers know this instance by (FLOWA_ORIGIN): "https://" and a
// host, with a port only when it is not 443. Every actor id is made from it, and other servers compare
// ids as text, so it is kept in one written form: lower case, no trailing slash, no ":443".
type publicOrigin struct {
	url  string // https://flowa.example.org, or https://flowa.example.org:8443
	host string // flowa.example.org: the name allowedHost accepts
}

func (o publicOrigin) String() string { return o.url }

// originMax is the longest FLOWA_ORIGIN read: "https://", a host of 253 characters, a colon and five
// digits, and a trailing slash.
const originMax = len("https://") + 253 + 6 + 1

// parseOrigin reads FLOWA_ORIGIN. What it forgives, each because the result is the same origin:
// spaces around the value (a line copied from a file), capitals (scheme and host are not case
// sensitive), one trailing slash (what a browser's address bar shows), and ":443" (the port https
// means anyway). What it refuses, each with a message: anything but https (other servers reach an
// actor only over https), a path, a query, a fragment, a user name, a space or a control character
// inside, an IP address (no certificate names one for other servers to check, and Mastodon expects a
// domain), localhost and the single-label and private names allowedHost lets through for the home
// network (no other server can reach them), a host in other than ASCII letters, digits, dots and
// dashes (a name in another script is written in its xn-- form, as the instance contact is, since
// turning it into that form would need a library), a host over 253 characters or a label over 63, an
// empty label, a trailing dot (the same name written two ways would be two origins), and a port that
// is not 1 to 65535. A value too long to be an origin is cut in the message, as parseMode does.
func parseOrigin(v string) (publicOrigin, error) {
	shown := v
	if len(shown) > 60 {
		shown = shown[:60] + "..."
	}
	bad := func(why string) (publicOrigin, error) {
		return publicOrigin{}, fmt.Errorf("FLOWA_ORIGIN=%q %s; set it to https:// and the public name of this instance, such as https://flowa.example.org", shown, why)
	}
	s := strings.TrimSpace(v)
	if s == "" {
		return publicOrigin{}, errors.New("FLOWA_ORIGIN is not set; a mode that federates needs the public address of this instance, such as https://flowa.example.org")
	}
	if len(s) > originMax {
		return bad("is too long to be an origin")
	}
	if strings.ContainsFunc(s, func(r rune) bool { return r <= ' ' || unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return bad("has a space or a control character in it")
	}
	s = strings.ToLower(s)
	rest, ok := strings.CutPrefix(s, "https://")
	switch {
	case ok:
	case strings.HasPrefix(s, "http://"):
		return bad("is http: other servers reach an actor only over https")
	case strings.Contains(s, "://"):
		return bad("has a scheme other than https")
	default:
		return bad("does not start with https://")
	}
	rest = strings.TrimSuffix(rest, "/")
	if i := strings.IndexAny(rest, "/\\?#@"); i >= 0 {
		return bad(map[byte]string{'/': "has a path", '\\': "has a path", '?': "has a query", '#': "has a fragment", '@': "has a user name"}[rest[i]])
	}
	if strings.HasPrefix(rest, "[") {
		return bad("is an IP address; other servers need a name")
	}
	host, port, hasPort := strings.Cut(rest, ":")
	if hasPort {
		n, err := strconv.Atoi(port)
		if port == "" || len(port) > 5 || strings.Trim(port, "0123456789") != "" || err != nil || n < 1 || n > 65535 {
			return bad("has a port that is not a number from 1 to 65535")
		}
		port = strconv.Itoa(n)
		if n == 443 {
			port = ""
		}
	}
	if why := originHost(host); why != "" {
		return bad(why)
	}
	o := publicOrigin{url: "https://" + host, host: host}
	if port != "" {
		o.url += ":" + port
	}
	return o, nil
}

// originHost says what is wrong with the host of an origin, or "" when nothing is. host is lower case.
func originHost(host string) string {
	switch {
	case host == "":
		return "has no host"
	case len(host) > 253:
		return "has a host longer than 253 characters"
	case strings.ContainsFunc(host, func(r rune) bool { return r >= utf8.RuneSelf }):
		return "has a host in other than plain letters, digits, dots and dashes; write a name in another script in its xn-- form"
	case net.ParseIP(host) != nil:
		return "is an IP address; other servers need a name"
	case strings.HasSuffix(host, "."):
		return "ends with a dot; write the name without it, since the same name written two ways would be two origins"
	}
	labels := strings.Split(host, ".")
	for _, l := range labels {
		switch {
		case l == "":
			return "has an empty label in its host (two dots in a row)"
		case len(l) > 63:
			return "has a label longer than 63 characters in its host"
		case strings.Trim(l, "abcdefghijklmnopqrstuvwxyz0123456789-") != "":
			return "has a character other than letters, digits, dots and dashes in its host"
		case l[0] == '-' || l[len(l)-1] == '-':
			return "has a label that starts or ends with a dash in its host"
		}
	}
	if strings.Trim(labels[len(labels)-1], "0123456789") == "" {
		return "is an IP address; other servers need a name" // 127.1, 10.1.2: shapes ParseIP does not take
	}
	if len(labels) == 1 || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return "is a name only this machine or a home network can reach"
	}
	for _, p := range privateSuffixes {
		if strings.HasSuffix(host, p) {
			return "is a private name (" + p + ") that other servers cannot reach"
		}
	}
	return ""
}

// keySecret seals the private keys in ap_keys (FLOWA_KEY_SECRET, F7). It holds only the cipher made
// from the secret, never the secret's text, and it prints as a fixed word in every format and in
// JSON, so that it cannot reach a log line, an error or the audit log by accident. As a field of
// another struct it prints as an address (fmt does not call methods on unexported fields). Nothing
// writes it to the database, a backup or the dashboard.
type keySecret struct{ aead cipher.AEAD }

// secretShown is what a keySecret prints as.
const secretShown = "[key secret]"

func (keySecret) String() string               { return secretShown }
func (keySecret) GoString() string             { return secretShown }
func (keySecret) Format(f fmt.State, _ rune)   { io.WriteString(f, secretShown) }
func (keySecret) MarshalJSON() ([]byte, error) { return json.Marshal(secretShown) }
func (keySecret) MarshalText() ([]byte, error) { return []byte(secretShown), nil }
func (s keySecret) set() bool                  { return s.aead != nil }

// sealInfo names what the AES key made from FLOWA_KEY_SECRET is for. The secret is never used as a key
// itself: HKDF-SHA256 makes one from it for this purpose, and a second purpose later names its own
// info and gets an unrelated key from the same secret.
const sealInfo = "flowa ap_keys private key sealing v1"

// keySecretHow is the end of every message about FLOWA_KEY_SECRET. No message says anything of the
// value itself, not even its length.
const keySecretHow = "make one with openssl rand -hex 32, keep it beside the database password (never in the repository), and use it for nothing else"

// parseKeySecret reads FLOWA_KEY_SECRET: exactly 64 hexadecimal characters (32 bytes), in either case,
// with spaces around it forgiven. A value made by hand in a common way is refused as not random
// (weakSecret, which also gives the chance of refusing a random one: under 2^-45). Nothing subtler
// can be checked honestly.
func parseKeySecret(v string) (keySecret, error) {
	s := strings.TrimSpace(v)
	if s == "" {
		return keySecret{}, errors.New("FLOWA_KEY_SECRET is not set; a mode that federates seals the signing keys with it: " + keySecretHow)
	}
	if len(s) != 64 {
		return keySecret{}, errors.New("FLOWA_KEY_SECRET has the wrong length: it must be 64 hexadecimal characters (32 bytes); " + keySecretHow)
	}
	raw, err := hex.DecodeString(s)
	if err != nil { // hex's own error names the character, so it is not passed on
		return keySecret{}, errors.New("FLOWA_KEY_SECRET must be hexadecimal: the digits 0 to 9 and the letters a to f; " + keySecretHow)
	}
	defer clear(raw)
	if weakSecret(raw) {
		return keySecret{}, errors.New("FLOWA_KEY_SECRET is not random: it repeats itself, counts up, has too few different bytes, or is the hex of typed text; " + keySecretHow)
	}
	key, err := hkdf.Key(sha256.New, raw, nil, sealInfo, 32)
	if err != nil {
		return keySecret{}, errors.New("FLOWA_KEY_SECRET: cannot make the sealing key")
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return keySecret{}, errors.New("FLOWA_KEY_SECRET: cannot make the sealing key")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return keySecret{}, errors.New("FLOWA_KEY_SECRET: cannot make the sealing key")
	}
	return keySecret{aead: aead}, nil
}

// weakSecret reports whether 32 secret bytes are plainly not random: fewer than 8 different bytes
// ("00...", "abab..."); a block of 16 bytes or fewer repeated ("0123456789abcdef" four times); an
// arithmetic run, each byte the one before plus a constant ("000102...1f"); or every byte printable
// ASCII, 0x20 to 0x7e, which is what the hex of a typed password or passphrase always is. For 32
// random bytes the last is by far the likeliest, (95/256)^32, about 2^-46 or one in 6 * 10^13; the
// others are below 2^-120 together. So a random secret is refused with a chance under 2^-45, and a
// secret made by hand in these common ways never gets through.
func weakSecret(b []byte) bool {
	seen := map[byte]bool{}
	printable, run := true, true
	for i, c := range b {
		seen[c] = true
		printable = printable && c >= 0x20 && c <= 0x7e
		run = run && (i < 2 || c-b[i-1] == b[1]-b[0])
	}
	if len(seen) < 8 || printable || run {
		return true
	}
	for p := 1; p <= len(b)/2; p++ {
		if bytes.Equal(b[p:], b[:len(b)-p]) {
			return true
		}
	}
	return false
}

// keyOwner is whose key a row of ap_keys holds: a users id, or instanceActor, which is NULL there.
type keyOwner int64

// instanceActor owns the instance's own key (docs/federation.md, 5.2: /actor). Ids of users start at 1.
const instanceActor keyOwner = 0

func (o keyOwner) String() string {
	if o == instanceActor {
		return "the instance actor"
	}
	return "user " + strconv.FormatInt(int64(o), 10)
}

// arg is the owner as a query argument: NULL for the instance actor.
func (o keyOwner) arg() any {
	if o == instanceActor {
		return nil
	}
	return int64(o)
}

// sealPrefix names the sealing scheme, so that a later one can be told apart and the old rows still
// opened: v1 is AES-256-GCM with a random 12-byte nonce, then base64url of the nonce, the ciphertext
// and the tag.
const sealPrefix = "v1."

// errOpenKey is every failure to open a sealed key. It never says more, and never carries the bytes.
var errOpenKey = errors.New("cannot open the key: it was sealed with another FLOWA_KEY_SECRET, for another row, or it was changed")

// keyAAD binds a sealed key to its row: its key id and its owner are authenticated with it, so a blob
// copied onto another row does not open there.
func keyAAD(keyID string, o keyOwner) []byte {
	return []byte("flowa ap_keys\x00" + keyID + "\x00" + o.String())
}

// seal encrypts a private key (PKCS#8 DER) for the row with keyID and owner o.
func (s keySecret) seal(plain []byte, keyID string, o keyOwner) (string, error) {
	if !s.set() {
		return "", errors.New("cannot seal the key: FLOWA_KEY_SECRET is not set")
	}
	n := s.aead.NonceSize()
	buf := make([]byte, n, n+len(plain)+s.aead.Overhead())
	rand.Read(buf) // a fresh nonce for every seal; crypto/rand does not fail
	return sealPrefix + base64.RawURLEncoding.EncodeToString(s.aead.Seal(buf, buf, plain, keyAAD(keyID, o))), nil
}

// open decrypts what seal made for the same row. Any other input is errOpenKey, never a panic.
func (s keySecret) open(sealed, keyID string, o keyOwner) ([]byte, error) {
	body, ok := strings.CutPrefix(sealed, sealPrefix)
	if !s.set() || !ok {
		return nil, errOpenKey
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(body)
	n := s.aead.NonceSize()
	if err != nil || len(raw) < n+s.aead.Overhead() {
		return nil, errOpenKey
	}
	plain, err := s.aead.Open(nil, raw[:n], raw[n:], keyAAD(keyID, o))
	if err != nil {
		return nil, errOpenKey
	}
	return plain, nil
}

// fedSettings are the two settings a mode that federates needs before it starts. on says whether the
// mode federates at all; the zero value is a mode that does not.
type fedSettings struct {
	on     bool
	origin publicOrigin
	secret keySecret
}

// parseFederation reads FLOWA_FEDERATE (single mode only), and FLOWA_ORIGIN and FLOWA_KEY_SECRET only in
// a mode that federates: a stray value cannot stop a local instance or a single-profile one that does
// not federate, which never use them (TestDBLocalModeUnchanged starts one with both set to values that
// would be refused here). main calls it beside parseMode, before the database is opened, for the same
// reason; start calls it again before anything is written.
func parseFederation(m instanceMode, getenv func(string) string) (fedSettings, error) {
	on, err := modeFederates(m, getenv("FLOWA_FEDERATE"))
	if err != nil || !on {
		return fedSettings{}, err
	}
	which := "FLOWA_MODE=" + string(m)
	if m == modeSingle {
		which += " with FLOWA_FEDERATE=on"
	}
	o, err := parseOrigin(getenv("FLOWA_ORIGIN"))
	if err != nil {
		return fedSettings{}, fmt.Errorf("%s: %w", which, err)
	}
	s, err := parseKeySecret(getenv("FLOWA_KEY_SECRET"))
	if err != nil {
		return fedSettings{}, fmt.Errorf("%s: %w", which, err)
	}
	return fedSettings{on: true, origin: o, secret: s}, nil
}

const (
	keyBits       = 2048              // RSA, what Mastodon verifies and the SWICG report asks for (5.7)
	keyQueue      = 64                // owners waiting for a key; a request beyond that is dropped and asked again later
	keyCacheSize  = 256               // parsed private keys kept in memory; any one goes when it is full
	instanceKeyID = "/actor#main-key" // the instance actor's key id, after the origin
)

// federation is what a mode that federates holds at run time: the settings, whether federation is
// refused, the parsed keys, and the worker that makes keys. A nil *federation on the app means the
// mode does not federate; no goroutine, key or row exists then.
type federation struct {
	db     *sql.DB
	origin publicOrigin
	secret keySecret
	gen    func() (*rsa.PrivateKey, error) // makes a key pair; a test counts or holds it
	// handled, when set (by a test), hears of each request the worker has finished with.
	handled func(o keyOwner, made bool, err error)

	mu      sync.Mutex
	refused string // why this instance federates nothing although its mode federates; "" when it may
	cache   map[keyOwner]cachedKey
	docs    map[keyOwner]keptDoc // actor documents as made (actorDoc), by owner
	docsGen uint64               // moves at every dropActor, so a document made meanwhile is not kept

	// actors is the cache of remote actors (remoteActor), set by startFederation with the app's fetcher;
	// nil in the federation -rotate-keys makes, which fetches nothing.
	actors *actorCache

	recordMu sync.Mutex  // one recordOrigin at a time from ensureOrigin
	recorded atomic.Bool // the origin is recorded in instance_settings and is this one

	ask    chan keyOwner // owners the worker is asked to make a key for
	once   sync.Once     // starts the worker on the first request
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type cachedKey struct {
	key *rsa.PrivateKey
	id  string
}

// generateKey makes a key pair for newFederation. The tests replace it to count or hold the keys made.
var generateKey = func() (*rsa.PrivateKey, error) { return rsa.GenerateKey(rand.Reader, keyBits) }

func newFederation(db *sql.DB, s fedSettings) *federation {
	ctx, cancel := context.WithCancel(context.Background())
	return &federation{db: db, origin: s.origin, secret: s.secret, cache: map[keyOwner]cachedKey{}, gen: generateKey,
		ask: make(chan keyOwner, keyQueue), ctx: ctx, cancel: cancel}
}

// refuse stops federation for as long as the server runs, says why once in the log, and keeps the
// first reason. Later items read refusal before they answer or send anything.
func (f *federation) refuse(why string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refused == "" {
		f.refused = why
		log.Print(why)
	}
}

func (f *federation) refusal() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.refused
}

// federating says whether this instance may federate now: its mode federates, and nothing refused it.
func (a *app) federating() bool { return a.fed != nil && a.fed.refusal() == "" }

// keyIDOf is the key id of an owner, the part after the origin (docs/federation.md, 5.2). ap_keys keeps
// this part only: the origin is recorded once and frozen (recordOrigin), so writing it into every row
// would repeat it, and the full id is the origin and this part joined.
func (f *federation) keyIDOf(ctx context.Context, o keyOwner) (string, error) {
	if o == instanceActor {
		return instanceKeyID, nil
	}
	var name string
	if err := f.db.QueryRowContext(ctx, `SELECT username FROM users WHERE id = $1 AND domain = ''`, int64(o)).Scan(&name); err != nil {
		return "", err
	}
	return "/users/" + name + "#main-key", nil
}

// makeKey makes a key pair and returns the public key as an SPKI PEM and the private key sealed for
// its row. It takes 0.1 to 2 seconds, and holds no database connection while it does.
func (f *federation) makeKey(keyID string, o keyOwner) (pubPEM, sealed string, err error) {
	k, err := f.gen()
	if err != nil {
		return "", "", err
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return "", "", err
	}
	defer clear(der)
	if sealed, err = f.secret.seal(der, keyID, o); err != nil {
		return "", "", err
	}
	spki, err := x509.MarshalPKIXPublicKey(&k.PublicKey)
	if err != nil {
		return "", "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: spki})), sealed, nil
}

// ensureKey makes sure an owner has a key pair and reports whether this call made it. It looks first,
// makes a key only when there is none, and writes it with ON CONFLICT DO NOTHING: when two goroutines
// or two servers (the old and the new during a deploy) make one at once, the unique index keeps the
// first and the other is thrown away, and both then read the same key. Within one server the worker
// asks one owner at a time, so a key is made twice only across servers, rarely, and harmlessly; not
// making it then would need a lock held for the whole second it takes. It never replaces a key: a
// key that cannot be opened is the operator's to replace (-rotate-keys). It records nothing about the
// origin: a key holds no origin (the key id is the part after it, and the sealing is bound to the key id
// and the owner), so making one announces nothing, and the origin is frozen only when an actor id is
// first given out (ensureOrigin).
func (f *federation) ensureKey(ctx context.Context, o keyOwner) (bool, error) {
	// While federation is refused no key is made for anyone: under a secret that does not open the
	// stored keys a new one would be sealed under a second secret, and under a moved origin it would
	// belong to ids that cannot be announced.
	if f.refusal() != "" {
		return false, errRefused
	}
	var have bool
	if err := f.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM ap_keys WHERE user_id IS NOT DISTINCT FROM $1::bigint)`, o.arg()).Scan(&have); err != nil {
		return false, err
	}
	if have {
		return false, nil
	}
	keyID, err := f.keyIDOf(ctx, o)
	if err != nil {
		return false, err
	}
	pub, sealed, err := f.makeKey(keyID, o)
	if err != nil {
		return false, err
	}
	if f.refusal() != "" { // refused while the key was being made
		return false, errRefused
	}
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	// The key id follows from the owner, so a second maker meets both unique constraints at once; DO
	// NOTHING with no target covers both, and the check after it tells a lost race from a key id that
	// another owner holds.
	res, err := tx.ExecContext(ctx, `INSERT INTO ap_keys (user_id, key_id, public_key, private_sealed) VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`, o.arg(), keyID, pub, sealed)
	if err != nil {
		return false, err
	}
	made, _ := res.RowsAffected()
	if made == 0 {
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM ap_keys WHERE user_id IS NOT DISTINCT FROM $1::bigint)`, o.arg()).Scan(&have); err != nil {
			return false, err
		}
		if !have {
			return false, fmt.Errorf("the key id %s belongs to another owner", keyID)
		}
	}
	return made == 1, tx.Commit()
}

// ensureOrigin freezes the origin: it records it and the time (recordOrigin), in one transaction, before
// the first answer that names a member's actor id to another server, which is when the origin is
// announced (docs/federation.md 4.3). In this version that answer is a WebFinger 200 for a member; the
// instance actor's answer does not freeze it (handleWebFinger). The actor document, the first request the
// instance actor signs (FRD 7) and the first delivery will call it too when they come. Until then nothing is frozen and a new FLOWA_ORIGIN is taken
// at the next start. Once it is recorded this is one atomic load. Two first answers at once in this
// process take turns on recordMu, and the second finds it recorded. A first answer while the row is being
// written by another transaction waits for that one to commit (INSERT ... ON CONFLICT DO NOTHING), then
// reads its row. One process runs per database, so the origin cannot be recorded by another one after
// this one started; if it were (the old and the new container of a deploy overlapping with two different
// origins), recordOrigin finds the other origin and federation is refused here, as at start, before any id
// goes out under the wrong origin.
func (f *federation) ensureOrigin(ctx context.Context) error {
	if f.recorded.Load() {
		return nil
	}
	f.recordMu.Lock()
	defer f.recordMu.Unlock()
	if f.recorded.Load() {
		return nil
	}
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := recordOrigin(ctx, tx, f.origin); err != nil {
		var moved originMoved
		if errors.As(err, &moved) {
			f.refuse(moved.Error())
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	f.recorded.Store(true)
	return nil
}

// errNoKey says an owner has no key yet.
var errNoKey = errors.New("no key yet")

// privateKey returns an owner's private key and its key id (the part after the origin), from memory
// when it was read before. A key that does not open is errOpenKey, with nothing of the key in it.
func (f *federation) privateKey(ctx context.Context, o keyOwner) (*rsa.PrivateKey, string, error) {
	f.mu.Lock()
	c, ok := f.cache[o]
	f.mu.Unlock()
	if ok {
		return c.key, c.id, nil
	}
	var keyID, sealed string
	err := f.db.QueryRowContext(ctx, `SELECT key_id, private_sealed FROM ap_keys WHERE user_id IS NOT DISTINCT FROM $1::bigint`, o.arg()).Scan(&keyID, &sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", errNoKey
	}
	if err != nil {
		return nil, "", err
	}
	der, err := f.secret.open(sealed, keyID, o)
	if err != nil {
		return nil, "", err
	}
	defer clear(der)
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	k, isRSA := parsed.(*rsa.PrivateKey)
	if err != nil || !isRSA {
		return nil, "", errOpenKey
	}
	f.mu.Lock()
	if len(f.cache) >= keyCacheSize {
		for x := range f.cache {
			delete(f.cache, x)
			break
		}
	}
	f.cache[o] = cachedKey{k, keyID}
	f.mu.Unlock()
	return k, keyID, nil
}

// errRefused is what ensureKey answers while federation is refused.
var errRefused = errors.New("federation is refused, so no key is made")

// requestKey asks the worker to make an owner's key, and returns at once: no request waits for a key.
// It reports false while federation is refused (no key is made then, for any owner), and when the
// queue is full; such an owner is asked for again at the next start (startFederation asks for every
// member who opted in and has no key). A refusal lasts until the server stops, so a start is also where
// the keys a refusal held back are asked for.
func (f *federation) requestKey(o keyOwner) bool {
	if f.refusal() != "" {
		return false
	}
	f.once.Do(f.startWorker)
	select {
	case f.ask <- o:
		return true
	default:
		log.Printf("keys: %d owners wait for a key already; the key of %s is asked for again at the next start", keyQueue, o)
		return false
	}
}

// startWorker starts the one goroutine that makes keys, one at a time, until stop. A key takes up to
// about two seconds of one core on the Pi, so one at a time keeps the other cores for the requests.
func (f *federation) startWorker() {
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		for {
			select {
			case <-f.ctx.Done():
				return
			case o := <-f.ask:
				began := time.Now()
				made, err := f.ensureKey(f.ctx, o)
				switch {
				case errors.Is(err, errRefused): // said once already, by refuse
				case err != nil && f.ctx.Err() == nil:
					log.Printf("keys: the key of %s was not made: %v", o, err)
				case made:
					log.Printf("keys: made the key of %s in %d ms", o, time.Since(began).Milliseconds())
					f.dropActor(o) // a document is not kept without a key, but one may be on its way
				}
				if f.handled != nil {
					f.handled(o, made, err)
				}
			}
		}
	}()
}

// stop tells the worker to stop and waits for it until ctx ends. A key being made is finished first
// (key generation cannot be interrupted), and is not written once ctx has ended; the next start asks
// for it again.
func (f *federation) stop(ctx context.Context) error {
	f.cancel()
	done := make(chan struct{})
	go func() { f.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// originMoved is a start whose FLOWA_ORIGIN is not the origin this instance first federated under.
type originMoved struct{ was, now string }

func (e originMoved) Error() string {
	return fmt.Sprintf("federation refused: the origin was %s and is now %s; actor ids cannot move", e.was, e.now)
}

// recordedOrigin is the origin recorded at the first federation, or "" when there was none.
func recordedOrigin(q queryRower) (string, error) {
	var s string
	err := q.QueryRow(`SELECT value #>> '{}' FROM instance_settings WHERE key = 'origin'`).Scan(&s)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return s, err
}

// originMatches says whether a recorded origin is o, however either was written.
func originMatches(recorded string, o publicOrigin) bool {
	p, err := parseOrigin(recorded)
	return err == nil && p.url == o.url
}

// recordOrigin records the origin and the time (instance_settings: origin, first_federated_at) the
// first time this instance announces an actor id, and refuses with originMoved when another origin was
// recorded before: actor ids announced to other servers cannot move (docs/federation.md, 4.3). It runs in
// ensureOrigin only. Two transactions recording at once both insert with DO NOTHING; the second waits for
// the first to commit, then reads its row, so one wins and both agree.
func recordOrigin(ctx context.Context, tx *sql.Tx, o publicOrigin) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO instance_settings (key, value) VALUES ('origin', to_jsonb($1::text)), ('first_federated_at', to_jsonb(now()))
		ON CONFLICT (key) DO NOTHING`, o.url); err != nil {
		return err
	}
	was, err := recordedOrigin(tx)
	if err != nil {
		return err
	}
	if !originMatches(was, o) {
		return originMoved{was: was, now: o.url}
	}
	return nil
}

// startFederation is the end of start in a mode that federates. A recorded origin that is not
// FLOWA_ORIGIN, or a key secret that does not open the instance actor's key, does not stop the server:
// it starts and serves the app as in local mode, says why once in the log, and federates nothing
// (federation.refused). Otherwise the instance actor's key is asked for when there is none yet.
func (a *app) startFederation(s fedSettings) error {
	f := newFederation(a.db, s)
	f.actors = newActorCache(f, a.fetch)
	a.fed = f
	was, err := recordedOrigin(a.db)
	if err != nil {
		return err
	}
	if was != "" && !originMatches(was, s.origin) {
		f.refuse(originMoved{was: was, now: s.origin.url}.Error())
		return nil
	}
	f.recorded.Store(was != "")
	if was == "" {
		log.Printf("federation: the origin %s is not announced yet; it is frozen the first time a member's actor id is given out (a WebFinger answer that finds a member), and until then a different FLOWA_ORIGIN is taken at a start", s.origin)
	}
	_, _, err = f.privateKey(context.Background(), instanceActor)
	switch {
	case errors.Is(err, errNoKey):
		log.Printf("federation: origin %s; the instance actor's key is made in the background", s.origin)
		f.requestKey(instanceActor)
	case errors.Is(err, errOpenKey):
		f.refuse("federation refused: FLOWA_KEY_SECRET does not open the keys stored here, so it is not the secret they were sealed with. Set that secret again, or make new keys for every account with /nunchaku -rotate-keys")
	case err != nil:
		return err
	default:
		log.Printf("federation: origin %s; the instance actor's key opens", s.origin)
	}
	// The owner of a single-profile instance has opted in by FLOWA_FEDERATE=on, and a member's key is made
	// when they opt in (5.7): the owner's is asked for here, after the instance actor's.
	if a.mode == modeSingle && f.refusal() == "" {
		var id int64
		if err := a.db.QueryRow(`SELECT id FROM users WHERE owner`).Scan(&id); err != nil {
			return err
		}
		f.requestKey(keyOwner(id))
	}
	// Members who opted in and have no key yet (their request was dropped from a full queue, or federation
	// was refused when they opted in): asked for again, as many as the queue holds; the rest at the next
	// start. The worker still makes one at a time.
	if f.refusal() == "" {
		rows, err := a.db.Query(missingKeysSQL, keyQueue-2)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return err
			}
			f.requestKey(keyOwner(id))
		}
		if err := rows.Err(); err != nil {
			return err
		}
	}
	return nil
}

// missingKeysSQL lists members who opted in and have no key, oldest first, at most $1. A suspended member
// is left out, with the condition webFingerSQL uses, since nobody can find them while it lasts; they get
// their key at the first start after it ends.
const missingKeysSQL = `SELECT u.id FROM users u WHERE u.domain = '' AND u.federates AND (u.suspended_until IS NULL OR u.suspended_until <= now()) AND NOT EXISTS (SELECT 1 FROM ap_keys k WHERE k.user_id = u.id) ORDER BY u.id LIMIT $1`

// rotateKeys makes a new key pair for every owner that has one, one at a time, and replaces each old
// key in one statement, only if it is still the one read: a key another rotation changed meanwhile,
// or whose account went, is skipped. It opens no old key, so it is also what an operator runs after
// losing the key secret. It returns how many keys it replaced and how many it skipped.
func (f *federation) rotateKeys(ctx context.Context) (replaced, skipped int, err error) {
	type old struct {
		owner      keyOwner
		keyID, pub string
	}
	rows, err := f.db.QueryContext(ctx, `SELECT COALESCE(user_id, 0), key_id, public_key FROM ap_keys ORDER BY user_id NULLS FIRST`)
	if err != nil {
		return 0, 0, err
	}
	var all []old
	for rows.Next() {
		var r old
		if err := rows.Scan(&r.owner, &r.keyID, &r.pub); err != nil {
			rows.Close()
			return 0, 0, err
		}
		all = append(all, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	for _, r := range all {
		pub, sealed, err := f.makeKey(r.keyID, r.owner)
		if err != nil {
			return replaced, skipped, err
		}
		res, err := f.db.ExecContext(ctx, `UPDATE ap_keys SET public_key = $1, private_sealed = $2, created_at = now() WHERE key_id = $3 AND public_key = $4`, pub, sealed, r.keyID, r.pub)
		if err != nil {
			return replaced, skipped, err
		}
		if c, _ := res.RowsAffected(); c == 1 {
			replaced++
		} else {
			skipped++
		}
		f.mu.Lock()
		delete(f.cache, r.owner)
		f.mu.Unlock()
		f.dropActor(r.owner) // its document names the key
	}
	return replaced, skipped, nil
}

// rotateCommand is /nunchaku -rotate-keys. It reads the mode and the settings before it opens the
// database, as the server does, and refuses a mode that does not federate.
func rotateCommand(getenv func(string) string, out io.Writer) error {
	mode, err := parseMode(getenv("FLOWA_MODE"))
	if err != nil {
		return fmt.Errorf("-rotate-keys: %w", err)
	}
	if s, err := parseFederation(mode, getenv); err != nil {
		return fmt.Errorf("-rotate-keys: %w", err)
	} else if !s.on {
		return fmt.Errorf("-rotate-keys: FLOWA_MODE=%s does not federate, so there are no keys to rotate", mode)
	}
	dsn := getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("-rotate-keys: DATABASE_URL is not set")
	}
	db, err := openDB(dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	return rotateWith(db, mode, getenv, out)
}

// rotateWith rotates the keys in db for a mode that federates, and says what it did.
func rotateWith(db *sql.DB, mode instanceMode, getenv func(string) string, out io.Writer) error {
	s, err := parseFederation(mode, getenv)
	if err != nil {
		return fmt.Errorf("-rotate-keys: %w", err)
	}
	if !s.on {
		return fmt.Errorf("-rotate-keys: FLOWA_MODE=%s does not federate, so there are no keys to rotate", mode)
	}
	f := newFederation(db, s)
	defer f.cancel()
	n, skipped, err := f.rotateKeys(context.Background())
	if err != nil {
		return fmt.Errorf("-rotate-keys: %d keys replaced, %d skipped, then: %w", n, skipped, err)
	}
	if skipped > 0 {
		// Exits non-zero (main's log.Fatal): not every key is new, and the operator should know.
		return fmt.Errorf("-rotate-keys: rotated %d keys and skipped %d that changed or went while this ran (another rotation at the same time, or a deleted account); run it again to replace them", n, skipped)
	}
	fmt.Fprintf(out, "rotated %d keys. Restart the server so that it reads them. This version does not send Update to followers yet: other servers pick the new keys up when a signature next fails to verify.\n", n)
	return nil
}

/* ---------- federation: discovery (WebFinger and NodeInfo) ----------

How another server finds a member of this instance and learns what the instance is (docs/federation.md,
5.3 and 5.4). Three addresses, registered only in a mode that federates (table): WebFinger
(RFC 7033), which turns acct:ana@flowa.example.org into the id of ana's actor, and the two NodeInfo
documents, which say what software runs here and how many people use it. WebFinger answers only for a
member who opted in (users.federates, F2), or for the owner of a single-profile instance with
FLOWA_FEDERATE=on, whose opt-in that setting is.

The ids WebFinger gives out (/users/ana, /actor) are the actor documents further down (FRD 5), and the
same address is the member's public page, which the profile-page link names.

All three answer every request with Access-Control-Allow-Origin: * (RFC 7033 section 5 asks it of
WebFinger, and the NodeInfo documents are as public), errors included, and sit behind one allowance
per address (limits.discovery), apart from every allowance a member uses, so a remote server's lookups
are bounded and cannot lock anyone out of the app. While federation is refused (a moved origin, a key
secret that does not open the keys: 4.3) every answer is the same 404 as an unknown name. */

const (
	jrdType        = "application/jrd+json" // RFC 7033 section 10.2
	activityType   = "application/activity+json"
	nodeInfoRel    = "http://nodeinfo.diaspora.software/ns/schema/2.1"
	profilePageRel = "http://webfinger.net/rel/profile-page"
	nodeInfoType   = `application/json; profile="http://nodeinfo.diaspora.software/ns/schema/2.1#"`
	flowaRepo      = "https://github.com/TuanSSM/flowtopia"
	resourceMax    = 300  // bytes of a WebFinger resource once decoded; an acct handle is far shorter
	wfQueryMax     = 2048 // bytes of the whole query, which may also name several rel values
	discoveryKeep  = "public, max-age=300"
	notFoundKeep   = "public, max-age=60"
	nodeCountsAge  = 5 * time.Minute
	notHere        = "Nothing here goes by that name."
)

// discoveryPaths are the three addresses, for discoveryMethods.
var discoveryPaths = map[string]bool{"/.well-known/webfinger": true, "/.well-known/nodeinfo": true, "/nodeinfo/2.1": true}

// jrdLink is one link of a JSON Resource Descriptor (RFC 7033 4.4.4).
type jrdLink struct {
	Rel  string `json:"rel"`
	Type string `json:"type,omitempty"`
	Href string `json:"href"`
}

// jrd is a JSON Resource Descriptor (RFC 7033 4.4). links is always written, as [] when no link is
// left, which RFC 7033 4.3 allows for a rel that matched nothing.
type jrd struct {
	Subject string    `json:"subject,omitempty"`
	Aliases []string  `json:"aliases,omitempty"`
	Links   []jrdLink `json:"links"`
}

// wfTarget is what a WebFinger resource names on this instance: a member by username, or the
// instance actor.
type wfTarget struct {
	name  string // the username as stored (lower case); "" for the instance actor
	actor bool
}

// errResource is a resource WebFinger cannot read, answered with 400 (RFC 7033 4.2).
var errResource = errors.New("the resource is not an acct: handle or an https URL")

// uriSchemeRE is the scheme of a URI (RFC 3986 3.1), with its colon.
var uriSchemeRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)

// parseResource reads a WebFinger resource against the origin o. It returns errResource when the value
// is malformed (answered 400), ok false when it is well formed but names nothing here (404, the same
// answer as for a member who does not exist or has not opted in), and the target otherwise. It runs no
// query and holds no lock.
//
// What is accepted, and why:
//   - acct:name@host (RFC 7565, the form Mastodon asks: its WebFinger page), with the scheme in any case
//     (RFC 3986 3.1: schemes are not case sensitive), and without the scheme, as name@host or @name@host,
//     which is how Mastodon writes handles for people; one leading @ is forgiven in every form.
//   - the name in any case: Flowa's usernames are lower case (usernameRE), so Ana can only mean ana, and
//     the answer's subject is the name as stored (RFC 7033 4.4.1 lets the subject differ from the
//     resource when it is the canonical form).
//   - the host compared with the origin's host in any case and with one trailing dot taken off (a DNS
//     name means the same with it). An acct host has no port: RFC 7565 section 7 writes the scheme as
//     "acct" ":" userpart "@" host with RFC 3986's host rule (a name or an address, never the port that
//     authority adds), so a host with one names no account here. The name and the host are compared
//     after percent-encoding normalization (RFC 7565 section 4), so acct:%61na@host is ana.
//   - the actor's own URL, https://host/users/name or https://host/actor, as an actor id is written:
//     the path exactly, no query, fragment or user, the name as stored. Its port may be written when it
//     is the origin's (":443" for an origin without one, RFC 3986 6.2.3), and an unreserved character may
//     be percent-encoded (6.2.2.2), as in an acct handle (RFC 7565 section 4).
//   - acct:host@host, the instance actor (the form other servers use for an instance's own actor). It is
//     decided before any member is looked up, so it always wins: usernameRE allows dots, and a member
//     whose username is the origin's host (possible when the host has 3 to 24 characters) cannot be
//     found by handle, only by the URL form. No new account may take that name (reservedName).
//
// What is refused as malformed: an empty value, one over resourceMax bytes, a space, a control or a
// non-ASCII byte (a URI is ASCII: RFC 3986 2), a handle with no @ or more than one after the leading one,
// an empty name or host, and an https URL that does not parse. Any other scheme (mailto:, http:) is a
// well formed URI this server has no information about: 404. The two forms differ on one edge: a
// percent-encoded host is normalized in an acct handle (acct:ana@flowa%2Eexample.org is ana) but makes
// the URL form a 400, because Go's URL parser refuses an escape in a host name; RFC 3986 allows it in
// both, and no server writes it, so the URL form is not parsed by hand for it.
func parseResource(v string, o publicOrigin) (wfTarget, bool, error) {
	if v == "" || len(v) > resourceMax {
		return wfTarget{}, false, errResource
	}
	for i := 0; i < len(v); i++ {
		if c := v[i]; c <= ' ' || c >= 0x7f {
			return wfTarget{}, false, errResource
		}
	}
	rest := v
	if m := uriSchemeRE.FindString(v); m != "" {
		switch strings.ToLower(m) {
		case "acct:":
			rest = v[len(m):]
		case "https:":
			return parseActorURL(v, o)
		default:
			return wfTarget{}, false, nil
		}
	}
	rest = strings.TrimPrefix(rest, "@")
	name, host, found := strings.Cut(rest, "@")
	if !found || name == "" || host == "" || strings.Contains(host, "@") {
		return wfTarget{}, false, errResource
	}
	// RFC 7565 section 4: acct URIs are compared after percent-encoding normalization (RFC 3986 6.2.2.2),
	// so %61na is ana. A % that does not start an escape is malformed. A name that decodes to anything a
	// Flowa name cannot hold (an encoded @ among them) is nobody's.
	var err error
	if name, err = url.PathUnescape(name); err != nil {
		return wfTarget{}, false, errResource
	}
	if host, err = url.PathUnescape(host); err != nil {
		return wfTarget{}, false, errResource
	}
	if !sameHost(host, o.host) {
		return wfTarget{}, false, nil
	}
	name = strings.ToLower(name)
	if sameHost(name, o.host) {
		return wfTarget{actor: true}, true, nil
	}
	if !usernameRE.MatchString(name) {
		return wfTarget{}, false, nil // no member can have this name, so there is nothing to look up
	}
	return wfTarget{name: name}, true, nil
}

// sameHost compares a host as written in a request with the origin's host (lower case, no trailing
// dot), in any case and with one trailing dot taken off.
func sameHost(h, origin string) bool {
	return strings.EqualFold(strings.TrimSuffix(h, "."), origin)
}

// parseActorURL reads the URL form of a WebFinger resource (see parseResource).
func parseActorURL(v string, o publicOrigin) (wfTarget, bool, error) {
	u, err := url.Parse(v)
	if err != nil || u.Host == "" {
		return wfTarget{}, false, errResource
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.RawPath != "" && !unreservedEscapes(u.RawPath)) || !sameHost(u.Hostname(), o.host) {
		return wfTarget{}, false, nil
	}
	want := "443"
	if _, p, err := net.SplitHostPort(strings.TrimPrefix(o.url, "https://")); err == nil {
		want = p
	}
	if got := u.Port(); got != want && (got != "" || want != "443") {
		return wfTarget{}, false, nil
	}
	if u.Path == "/actor" {
		return wfTarget{actor: true}, true, nil
	}
	if name, ok := strings.CutPrefix(u.Path, "/users/"); ok && usernameRE.MatchString(name) {
		return wfTarget{name: name}, true, nil
	}
	return wfTarget{}, false, nil
}

// unreservedEscapes says whether every percent-escape in a path stands for an unreserved character
// (RFC 3986 2.3), the only ones percent-encoding normalization decodes (6.2.2.2): /users/%61na is
// /users/ana, but /users%2Fana is not /users/ana, since an encoded slash is not a slash.
func unreservedEscapes(p string) bool {
	for i := 0; i < len(p); i++ {
		if p[i] != '%' {
			continue
		}
		if i+2 >= len(p) {
			return false
		}
		v, err := strconv.ParseUint(p[i+1:i+3], 16, 8)
		if c := byte(v); err != nil || !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~') {
			return false
		}
		i += 2
	}
	return true
}

// webFingerSQL finds a member WebFinger may answer for: one lookup on the unique username index. $2 is
// true on a single-profile instance, whose owner's opt-in is FLOWA_FEDERATE=on itself: the owner only,
// not whoever else a database might hold. A suspended
// member is not found, as one who does not exist, has not opted in or (later) has a retired name: every
// one of them costs this same query and gets the same answer. A remote account is never found: its name
// holds an "@" no resource here can ask, and users_remote_inert keeps its federates and owner false.
const webFingerSQL = `SELECT username FROM users WHERE username = $1 AND (federates OR ($2 AND owner)) AND (suspended_until IS NULL OR suspended_until <= now())`

// discoveryAllowed counts a request against the address's discovery allowance, and answers 429 when it
// is used up. It is asked first, before the query string is read or the database asked.
func (a *app) discoveryAllowed(w http.ResponseWriter, r *http.Request) bool {
	from := limitKey(a.clientIP(r))
	if a.lim.discovery.add(from) {
		return true
	}
	logRefusal(w, "too many discovery requests", "from "+from)
	w.Header().Set("Retry-After", "3600")
	discoveryFail(w, http.StatusTooManyRequests, "Too many requests from this address. Try again in an hour.")
	return false
}

// discoveryFail answers a discovery request with an error. A 404 may be kept for a minute, as the
// answer to a name that does not federate yet; the other errors are not kept.
func discoveryFail(w http.ResponseWriter, code int, msg string) {
	h := w.Header()
	h.Set("Content-Type", "application/json")
	if code == http.StatusNotFound {
		h.Set("Cache-Control", notFoundKeep)
	} else {
		h.Set("Cache-Control", "no-store")
	}
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// discoveryWrite sends a discovery document with its type, its length and a short time to keep it: a
// member who stops sharing should drop out of other servers' caches within minutes.
func discoveryWrite(w http.ResponseWriter, ctype string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		log.Printf("error: a discovery document: %v", err)
		discoveryFail(w, http.StatusInternalServerError, "Something went wrong on the server.")
		return
	}
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("Cache-Control", discoveryKeep)
	h.Set("Content-Length", strconv.Itoa(len(b)))
	w.Write(b)
}

// fedMethods answers a request to one of the discovery addresses, or of the public addresses (the actors,
// the public pages and their pictures), with a method other than GET or HEAD with 405 and Allow, before
// guard would answer it as a write with 403 or 415: discovery with the CORS header, the public addresses
// counted against their allowance first, as a GET is. It passes everything else on, and is not in the
// chain at all in a mode that does not federate (routes).
//
// The inboxes the actor documents name (/users/{name}/inbox and /actor/inbox) are not built yet (FWR 1),
// and answer every method with 405 and an empty Allow, counted against the public allowance: RFC 9110
// 15.5.6 makes 405 the answer for a method the target does not support, and 10.2.1 an empty Allow the way
// a resource temporarily disabled by configuration says it takes none. A 4xx tells a sender not to keep
// trying, so a Follow pressed on Mastodon now is not retried for days and stays requested there. 404
// would say the address does not exist, which the actor contradicts, and 501 is for a method the server
// supports nowhere (15.6.2), while it takes POST elsewhere.
func (a *app) fedMethods(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if inboxPath(r.URL.Path) {
			if !a.publicAllowed(w, r) {
				return
			}
			h := w.Header()
			h.Set("Allow", "")
			h.Set("Cache-Control", "no-store")
			http.Error(w, "This server does not take deliveries yet.", http.StatusMethodNotAllowed)
			return
		}
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		switch {
		case discoveryPaths[r.URL.Path]:
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", "*")
			h.Set("Allow", "GET, HEAD")
			discoveryFail(w, http.StatusMethodNotAllowed, "Only GET and HEAD are answered here.")
		case publicPaths(r.URL.Path):
			if !a.publicAllowed(w, r) {
				return
			}
			h := w.Header()
			h.Set("Allow", "GET, HEAD")
			h.Set("Cache-Control", "no-store")
			http.Error(w, "Only GET and HEAD are answered here.", http.StatusMethodNotAllowed)
		default:
			next.ServeHTTP(w, r)
		}
	})
}

// handleWebFinger answers GET /.well-known/webfinger (RFC 7033; docs/federation.md 5.3). The resource
// must be there exactly once (400 otherwise, RFC 7033 4.2); rel, any number of times, keeps only the
// links of those relations (4.3), compared as plain strings (4.4.4.1). A member is one indexed lookup;
// the instance actor none. The answer's links are the self link Mastodon needs, to the actor id, and the
// profile page at the same address (handleActor answers a browser with it); the alias is the id.
func (a *app) handleWebFinger(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if !a.discoveryAllowed(w, r) {
		return
	}
	f := a.fed
	if f.refusal() != "" {
		discoveryFail(w, http.StatusNotFound, notHere)
		return
	}
	if len(r.URL.RawQuery) > wfQueryMax {
		discoveryFail(w, http.StatusBadRequest, "The query is too long.")
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(q["resource"]) != 1 {
		discoveryFail(w, http.StatusBadRequest, "Give the resource exactly once, as acct:name@host.")
		return
	}
	t, ok, err := parseResource(q["resource"][0], f.origin)
	if err != nil {
		discoveryFail(w, http.StatusBadRequest, "The resource is not an acct: handle or an https URL of this server.")
		return
	}
	if !ok {
		discoveryFail(w, http.StatusNotFound, notHere)
		return
	}
	var subject, id string
	if t.actor {
		subject, id = "acct:"+f.origin.host+"@"+f.origin.host, f.origin.url+"/actor"
	} else {
		var name string
		err := a.db.QueryRowContext(r.Context(), webFingerSQL, t.name, a.mode == modeSingle).Scan(&name)
		if errors.Is(err, sql.ErrNoRows) {
			discoveryFail(w, http.StatusNotFound, notHere)
			return
		}
		if err != nil {
			log.Printf("error: webfinger: %v", err)
			discoveryFail(w, http.StatusInternalServerError, "Something went wrong on the server.")
			return
		}
		subject, id = "acct:"+name+"@"+f.origin.host, f.origin.url+"/users/"+name
	}
	// A member's id going out is what announces the origin (4.3): it is recorded first, once. The instance
	// actor's answer records nothing, so a scanner, or the operator's own check of acct:host@host, cannot
	// freeze a mistyped origin; the first request the instance actor signs (FRD 7) will call ensureOrigin.
	// Nor does a HEAD request: it gets the same headers as a GET, but no id leaves in a body.
	if !t.actor && r.Method != http.MethodHead {
		if err := f.ensureOrigin(r.Context()); err != nil {
			var moved originMoved
			if errors.As(err, &moved) {
				discoveryFail(w, http.StatusNotFound, notHere)
				return
			}
			log.Printf("error: webfinger: recording the origin: %v", err)
			discoveryFail(w, http.StatusInternalServerError, "Something went wrong on the server.")
			return
		}
	}
	// The self link Mastodon needs, and the profile page (the same address, answered as HTML to a browser,
	// 5.6), which the SWICG report lists among the common links.
	links := []jrdLink{{Rel: "self", Type: activityType, Href: id}, {Rel: profilePageRel, Type: "text/html", Href: id}}
	if rels, asked := q["rel"]; asked {
		links = slices.DeleteFunc(links, func(l jrdLink) bool { return !slices.Contains(rels, l.Rel) })
	}
	discoveryWrite(w, jrdType, jrd{Subject: subject, Aliases: []string{id}, Links: links})
}

// handleNodeInfoLinks answers GET /.well-known/nodeinfo: a JRD with the one schema this server serves,
// as application/json (NodeInfo protocol, "Discovery").
func (a *app) handleNodeInfoLinks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if !a.discoveryAllowed(w, r) {
		return
	}
	if a.fed.refusal() != "" {
		discoveryFail(w, http.StatusNotFound, notHere)
		return
	}
	discoveryWrite(w, "application/json", jrd{Links: []jrdLink{{Rel: nodeInfoRel, Href: a.fed.origin.url + "/nodeinfo/2.1"}}})
}

// nodeInfo is the NodeInfo 2.1 document. Every member the schema requires is a field without
// omitempty, and the arrays are never nil, so none is written as null.
type nodeInfo struct {
	Version           string       `json:"version"`
	Software          nodeSoftware `json:"software"`
	Protocols         []string     `json:"protocols"`
	Services          nodeServices `json:"services"`
	OpenRegistrations bool         `json:"openRegistrations"`
	Usage             nodeUsage    `json:"usage"`
	Metadata          nodeMetadata `json:"metadata"`
}

type nodeSoftware struct {
	Name       string `json:"name"`    // ^[a-z0-9-]+$
	Version    string `json:"version"` // required, any string; "" says nothing about the build
	Repository string `json:"repository"`
}

type nodeServices struct {
	Inbound  []string `json:"inbound"`
	Outbound []string `json:"outbound"`
}

type nodeUsage struct {
	Users      nodeUsers `json:"users"`
	LocalPosts int       `json:"localPosts"`
}

type nodeUsers struct {
	Total          int `json:"total"`
	ActiveMonth    int `json:"activeMonth"`
	ActiveHalfyear int `json:"activeHalfyear"`
}

// nodeMetadata is free form in the schema. It says the mode, and how much of federation this version
// has, so that nobody reading it takes discovery for the whole of ActivityPub.
type nodeMetadata struct {
	Mode            string   `json:"mode"`
	FederationBuilt []string `json:"federationBuilt"`
}

// nodeCountsSQL counts as the schema defines its numbers: every registered account, those that signed
// in during the last 30 and 180 days (last_seen, which a session's use moves once an hour), and every
// post. Four counts of two small tables, so they are kept for nodeCountsAge (nodeCounts).
const nodeCountsSQL = `SELECT (SELECT count(*) FROM users WHERE domain = ''), (SELECT count(*) FROM users WHERE domain = '' AND last_seen > now() - interval '30 days'),
	(SELECT count(*) FROM users WHERE domain = '' AND last_seen > now() - interval '180 days'), (SELECT count(*) FROM posts)`

// nodeCounts are the usage counts, read at most once every nodeCountsAge: the document is public and
// each count is a query, so a flood of requests costs one read every few minutes.
func (a *app) nodeCounts(ctx context.Context) (nodeUsage, error) {
	a.nodeMu.Lock()
	defer a.nodeMu.Unlock()
	if !a.nodeAt.IsZero() && time.Since(a.nodeAt) < nodeCountsAge {
		return a.nodeUse, nil
	}
	var u nodeUsage
	if err := a.db.QueryRowContext(ctx, nodeCountsSQL).Scan(&u.Users.Total, &u.Users.ActiveMonth, &u.Users.ActiveHalfyear, &u.LocalPosts); err != nil {
		return nodeUsage{}, err
	}
	a.nodeUse, a.nodeAt = u, time.Now()
	return u, nil
}

// openRegistrations is NodeInfo's "whether this server allows open self-registration": the signups
// flag on and sign-up not by approval, and never in single mode, which takes no account. Both are read
// from the caches the pages use (two and ten seconds).
func (a *app) openRegistrations() (bool, error) {
	if a.mode == modeSingle {
		return false, nil
	}
	info, err := a.instanceCached()
	if err != nil {
		return false, err
	}
	return a.flagsCached()["signups"] && info.Registration != regApproval, nil
}

// handleNodeInfo answers GET /nodeinfo/2.1 with the document and the content type the NodeInfo protocol
// names ("Retrieval"). protocols holds activitypub because the schema needs one entry from its list and
// that is the protocol this server is being built for; metadata says how much of it is built.
// software.version is the empty string: the schema requires the member and asks only that it be a string,
// and which build runs helps an attacker, which is why /healthz keeps it to local probes. Publishing it
// becomes an admin's choice with the instance setting nodeinfo_version (docs/federation.md 5.4), later.
func (a *app) handleNodeInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if !a.discoveryAllowed(w, r) {
		return
	}
	if a.fed.refusal() != "" {
		discoveryFail(w, http.StatusNotFound, notHere)
		return
	}
	use, err := a.nodeCounts(r.Context())
	if err == nil {
		var open bool
		if open, err = a.openRegistrations(); err == nil {
			discoveryWrite(w, nodeInfoType, nodeInfo{
				Version:           "2.1",
				Software:          nodeSoftware{Name: "flowa", Version: "", Repository: flowaRepo},
				Protocols:         []string{"activitypub"},
				Services:          nodeServices{Inbound: []string{}, Outbound: []string{}},
				OpenRegistrations: open,
				Usage:             use,
				Metadata:          nodeMetadata{Mode: string(a.mode), FederationBuilt: fedBuiltParts},
			})
			return
		}
	}
	log.Printf("error: nodeinfo: %v", err)
	discoveryFail(w, http.StatusInternalServerError, "Something went wrong on the server.")
}

// handleWarning is said to a member who opts in with a name that ends with a dot or a dash
// (docs/federation.md, finding 11): Flowa allows such a name, Mastodon's own usernames may have a dot
// or a dash only inside, and a handle Mastodon cannot read cannot be mentioned or found from there.
const handleWarning = "Your name ends with a dot or a dash. Mastodon allows those only inside a name, so people on Mastodon may not be able to mention you or find you by your handle. Names cannot be changed here."

// hostNameRefusal is said to a member whose username is the instance's own host when they opt in: other
// servers read acct:host@host as the instance itself (5.3), so they could never find the member by it.
const hostNameRefusal = "Your username is this server's own name, which other servers read as the server itself, so they could not find you by it. Names cannot be changed here."

// fedView is what /api/me and PUT /api/me tell a signed-in member about sharing beyond this server, in
// a mode that federates; the callers leave it out otherwise, so the page shows nothing of it. In single
// mode the owner's opt-in is FLOWA_FEDERATE=on (implied), and there is no switch. The words the card
// shows about what is built come from here (label, built), so they change with the server. While
// federation is refused the member is told it is unavailable (unavailable), and staff why (refused).
func (a *app) fedView(u *user, optedIn bool) map[string]any {
	if a.fed == nil || u == nil {
		return nil
	}
	v := map[string]any{"handle": "@" + u.Username + "@" + a.fed.origin.host, "optedIn": optedIn || a.mode == modeSingle, "label": fedSwitch, "built": fedMemberBuilt}
	if a.mode == modeSingle {
		v["implied"] = true
	}
	if why := a.fed.refusal(); why != "" {
		v["unavailable"] = fedUnavailable
		if can(u, permDashboard) {
			v["refused"] = why
		}
	}
	if strings.HasSuffix(u.Username, ".") || strings.HasSuffix(u.Username, "-") {
		v["warning"] = handleWarning
	}
	// The composer offers the audience only to a member who may post publicly: opted in (or the owner of a
	// single-profile instance), while federation is not refused. Its words are the server's, as the card's;
	// the card then links the member's public page.
	if v["optedIn"] == true && v["unavailable"] == nil {
		v["audiences"] = audienceChoices
		v["profile"] = "/users/" + u.Username // the public page people read (handleActor)
	}
	return v
}

// audienceChoice is one audience as the composer offers it, Public first (chosen at first: federation.md 11).
type audienceChoice struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Hint  string `json:"hint"`
}

var audienceChoices = []audienceChoice{
	{audPublic, "Public", audPublicHint},
	{audLocal, "This server", "Only members of this Flowa, signed in."},
}

// audPublicHint says what Public means in this version.
const audPublicHint = "Anyone can read it, on other servers or in a browser, without signing in. It is not sent to anyone."

/* ---------- federation: actor documents and public pages ----------

What another server, or a person in a browser, may read of this instance (docs/federation.md, F16, 5.2,
5.5, 5.6, 6.5, 6.7, 9.4 and 11): the profile and the Public posts of a member who opted in, the instance
actor, and the pictures they show. Registered only in a mode that federates (table), so local mode and
single mode without FLOWA_FEDERATE have none of these addresses.

  - GET /users/{name} and GET /users/{name}/posts/{id} answer the ActivityStreams document (the actor of
    5.5, the Note of 6.5) to a request whose Accept prefers it, and a small HTML page the server writes to
    anyone else (5.6), with Vary: Accept on every answer, errors included.
  - GET /actor is the instance actor, the same way.
  - GET /users/{name}/avatar.{jpg,png} and GET /users/{name}/posts/{id}/picture.{jpg,png} are the pictures
    those name: the member's profile picture and a Public post's picture, and nothing else (9.4).

Who is shown: a member who opted in (users.federates), or the owner of a single-profile instance with
FLOWA_FEDERATE=on, who is not suspended. What is shown of them: a post whose audience is Public, that is
not hidden and is not a milestone (F13). Everything else (a This server post, a hidden one, one of a member
who has not opted in or opted out since, who is suspended or deleted, and an id or a name nobody has) gets
one 404, the same bytes and headers for all of them, in each representation; while federation is refused
every answer is that 404 too. The one other answer is 410 with a Tombstone, for the actor of a member who
deleted the account after opting in (retired_usernames; ActivityPub 6.4), so other servers stop asking.

Every answer costs one statement on an index (TestDBQueryPlans) and is counted against the address's
allowance (limits.public) before any database work, apart from every allowance members use. The actor
documents are kept in memory per member (federation.docs) and dropped when the profile, the picture, the
key or the opt-in changes. Nothing here reads a cookie or a session. */

const (
	asContext        = "https://www.w3.org/ns/activitystreams"
	secContext       = "https://w3id.org/security/v1"
	publicCollection = "https://www.w3.org/ns/activitystreams#Public" // the public collection, sent only in this form (6.7)
	apKeep           = "public, max-age=180"                          // 5.6; opting out reaches other servers' caches within three minutes
	acceptMax        = 4096                                           // bytes of Accept read; a longer header is read as none
	acceptRange      = 64                                             // media ranges read; more is read as no header
	pubPage          = 20                                             // Public posts on a page of a profile
	docsMax          = 1000                                           // actor documents kept in memory; all are dropped when it is full
	docsAge          = 10 * time.Minute                               // a kept document is made again after this, as a net for a change made by hand
)

// fedBuilt says what this version of federation does. The start logs it once (fedBuiltLog), the admin
// dashboard shows it as the server sends it, and the README and the design note say the same; NodeInfo's
// metadata.federationBuilt lists the same parts (fedBuiltParts). Change all three here, together.
const fedBuilt = "this version answers discovery (WebFinger and NodeInfo) and serves the profiles and Public posts of the members who opted in, to other servers and to people in a browser, with each member's outbox of Public posts and the counts of their followers and follows; it has no inbox, so nobody can follow anyone yet, and nothing is delivered"

const fedBuiltLog = "federation: " + fedBuilt

var fedBuiltParts = []string{"webfinger", "nodeinfo", "actors", "notes", "outbox", "collections"}

/* The Accept header (RFC 9110, 12.5.1), read only to choose between the ActivityStreams document and the
HTML page. */

// mediaRange is one element of Accept: a type, a subtype, the profile parameter when there is one, and
// its weight in thousandths.
type mediaRange struct {
	typ, sub string
	profile  []string // the URIs of a profile parameter, split on spaces; nil when there is none
	q        int      // 0 to 1000
}

// parseAccept reads the Accept headers of a request (several lines are one list, RFC 9110 5.3) into
// media ranges. An element that does not parse is skipped, as RFC 9110 lets a recipient ignore what it
// cannot read; a header longer than acceptMax bytes or with more than acceptRange elements is read as no
// header at all, so a hostile one costs a bounded amount of work.
func parseAccept(vals []string) []mediaRange {
	n := 0
	for _, v := range vals {
		n += len(v) + 1
	}
	if n > acceptMax {
		return nil
	}
	var out []mediaRange
	for _, el := range splitQuoted(strings.Join(vals, ","), ',') {
		if len(out) == acceptRange {
			return nil
		}
		if m, ok := parseMediaRange(el); ok {
			out = append(out, m)
		}
	}
	return out
}

// splitQuoted splits s at sep outside quoted strings (a quoted profile may hold the separator), honouring
// the backslash escapes of a quoted string (RFC 9110 5.6.4).
func splitQuoted(s string, sep byte) []string {
	var parts []string
	start, quoted := 0, false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case quoted && c == '\\':
			i++
		case c == '"':
			quoted = !quoted
		case !quoted && c == sep:
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return append(parts, s[start:])
}

// isToken says whether s is an RFC 9110 token (5.6.2).
func isToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c <= ' ' || c >= 0x7f || strings.IndexByte(`"(),/:;<=>?@[\]{}`, c) >= 0 {
			return false
		}
	}
	return true
}

// parseMediaRange reads one element: type/subtype, then parameters in any order and case. A parameter
// value is a token or a quoted string. The weight q is 0 to 1 with at most three decimals (RFC 9110
// 12.4.2); what follows it is ignored. Only the profile parameter is kept: it is the one that says
// whether application/ld+json means ActivityStreams.
func parseMediaRange(el string) (mediaRange, bool) {
	parts := splitQuoted(el, ';')
	full := strings.ToLower(strings.TrimSpace(parts[0]))
	typ, sub, ok := strings.Cut(full, "/")
	if !ok || !isToken(typ) || !isToken(sub) || (typ == "*" && sub != "*") {
		return mediaRange{}, false
	}
	m := mediaRange{typ: typ, sub: sub, q: 1000}
	for _, p := range parts[1:] {
		name, val, ok := strings.Cut(strings.TrimSpace(p), "=")
		name = strings.ToLower(strings.TrimSpace(name))
		val = strings.TrimSpace(val)
		if !ok || !isToken(name) {
			return mediaRange{}, false
		}
		if strings.HasPrefix(val, `"`) {
			if len(val) < 2 || !strings.HasSuffix(val, `"`) || strings.HasSuffix(val, `\"`) && !strings.HasSuffix(val, `\\"`) {
				return mediaRange{}, false
			}
			var b strings.Builder
			for i := 1; i < len(val)-1; i++ {
				if val[i] == '\\' {
					i++
				}
				b.WriteByte(val[i])
			}
			val = b.String()
		} else if !isToken(val) {
			return mediaRange{}, false
		}
		if name == "q" {
			q, ok := parseQ(val)
			if !ok {
				return mediaRange{}, false
			}
			m.q = q
			break // the parameters after the weight are accept extensions, not the media type's
		}
		if name == "profile" {
			m.profile = strings.Fields(val)
		}
	}
	return m, true
}

// parseQ reads a weight: "0" to "1", with at most three decimals, "1" only as 1.000.
func parseQ(v string) (int, bool) {
	whole, frac, _ := strings.Cut(v, ".")
	if (whole != "0" && whole != "1") || len(frac) > 3 || strings.Trim(frac, "0123456789") != "" {
		return 0, false
	}
	n, _ := strconv.Atoi((frac + "000")[:3])
	if whole == "1" {
		if n != 0 {
			return 0, false
		}
		return 1000, true
	}
	return n, true
}

// weigh is the weight a list gives one offered type, and how specific the range that gave it was (3: the
// type with its profile, 2: the type, 1: type/*, 0: */*): the most specific range that matches decides
// (RFC 9110 12.5.1). For application/ld+json (asProfile) a range with a profile matches only when the
// profile names ActivityStreams, since ld+json alone says nothing of the vocabulary; the parameter is a
// list of URIs, and any of them may be it. For any other type a profile changes nothing:
// application/activity+json is ActivityStreams whatever parameter follows it, and some servers send it
// with the ActivityStreams profile too.
func weigh(ranges []mediaRange, typ, sub string, asProfile bool) (q, spec int) {
	spec = -1
	for _, m := range ranges {
		s := -1
		switch {
		case m.typ == typ && m.sub == sub && asProfile && m.profile != nil:
			if slices.Contains(m.profile, asContext) {
				s = 3
			}
		case m.typ == typ && m.sub == sub:
			s = 2
		case m.typ == typ && m.sub == "*":
			s = 1
		case m.typ == "*":
			s = 0
		}
		if s > spec || s == spec && s >= 0 && m.q > q {
			q, spec = m.q, s
		}
	}
	if spec < 0 {
		return 0, -1
	}
	return q, spec
}

// wantsActivity decides the representation of a negotiated address: the ActivityStreams document when the
// request's Accept weighs application/activity+json or application/ld+json (with no profile, or one that
// names ActivityStreams) above zero and above text/html, or the same with a more specific range; the HTML
// page otherwise: with no Accept, with */* alone, for a browser's header, and when the header accepts
// neither (RFC 9110 12.5.1 lets a server disregard Accept rather than answer 406, and a person who gets a
// page reads why). ActivityPub 3.2: a server MUST answer the ld+json profile and SHOULD answer
// activity+json with the object.
func wantsActivity(h http.Header) bool {
	ranges := parseAccept(h.Values("Accept"))
	if len(ranges) == 0 {
		return false
	}
	qa, sa := weigh(ranges, "application", "activity+json", false)
	if q, s := weigh(ranges, "application", "ld+json", true); q > qa || q == qa && s > sa {
		qa, sa = q, s
	}
	qh, sh := weigh(ranges, "text", "html", false)
	return qa > 0 && (qa > qh || qa == qh && sa > sh)
}

/* The documents. Each is a typed struct written by encoding/json without HTML escaping, so the HTML
inside summary and content stays as written; every name appears once and every id is https. Their names
start with out: they are what this server writes, kept apart from the types of the decoder that reads
what other servers send (6.3), which take the ap names. */

// actorTerms is the object in the @context of an actor (5.5), its terms in the note's order.
type actorTerms struct {
	ManuallyApprovesFollowers string `json:"manuallyApprovesFollowers"`
	Toot                      string `json:"toot"`
	Discoverable              string `json:"discoverable"`
	Indexable                 string `json:"indexable"`
	Featured                  idTerm `json:"featured"`
	PropertyValue             string `json:"PropertyValue"`
	Value                     string `json:"value"`
	Schema                    string `json:"schema"`
	Flowa                     string `json:"flowa"`
}

type idTerm struct {
	ID   string `json:"@id"`
	Type string `json:"@type"`
}

// actorContext is 5.5's context. schema is http://schema.org# on purpose: it is what Mastodon expects,
// though its own page calls it incorrect (5.5).
var actorContext = []any{asContext, secContext, actorTerms{
	ManuallyApprovesFollowers: "as:manuallyApprovesFollowers", Toot: "http://joinmastodon.org/ns#",
	Discoverable: "toot:discoverable", Indexable: "toot:indexable", Featured: idTerm{"toot:featured", "@id"},
	PropertyValue: "schema:PropertyValue", Value: "schema:value", Schema: "http://schema.org#", Flowa: flowaNS,
}}

// flowaNS is the one namespace of Flowa's own terms, the same on every Flowa (F14).
const flowaNS = "https://github.com/TuanSSM/flowtopia/ns#"

// outActor is an actor document (5.5). The addresses an actor MUST name (ActivityPub 4.1: inbox and
// outbox) are named, the inbox though it takes nothing yet (405, inboxPath). Of the ones it only SHOULD or
// MAY name, a member's followers, following and featured answer since FRD 6 and are named;
// endpoints.sharedInbox is left out until it answers (FWR 1). The instance actor names none of them.
type outActor struct {
	Context                   []any         `json:"@context"`
	ID                        string        `json:"id"`
	Type                      string        `json:"type"`
	PreferredUsername         string        `json:"preferredUsername"`
	Name                      string        `json:"name,omitempty"`
	Summary                   string        `json:"summary,omitempty"`
	URL                       string        `json:"url"`
	Inbox                     string        `json:"inbox"`
	Outbox                    string        `json:"outbox"`
	Followers                 string        `json:"followers,omitempty"`
	Following                 string        `json:"following,omitempty"`
	Featured                  string        `json:"featured,omitempty"`
	ManuallyApprovesFollowers *bool         `json:"manuallyApprovesFollowers,omitempty"`
	Discoverable              *bool         `json:"discoverable,omitempty"`
	Indexable                 *bool         `json:"indexable,omitempty"`
	Published                 string        `json:"published,omitempty"`
	Icon                      *outImage     `json:"icon,omitempty"`
	Attachment                []outProperty `json:"attachment,omitempty"`
	PublicKey                 outKey        `json:"publicKey"`
}

type outImage struct {
	Type      string `json:"type"`
	MediaType string `json:"mediaType"`
	URL       string `json:"url"`
}

type outProperty struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

type outKey struct {
	ID           string `json:"id"`
	Owner        string `json:"owner"`
	PublicKeyPem string `json:"publicKeyPem"`
}

// noteTerms is the context object of a Note (6.5).
type noteTerms struct {
	Flowa     string `json:"flowa"`
	Sensitive string `json:"sensitive"`
	Hashtag   string `json:"Hashtag"`
}

var noteContext = []any{asContext, noteTerms{Flowa: flowaNS, Sensitive: "as:sensitive", Hashtag: "as:Hashtag"}}

// outNote is a Public post as a Note (6.5), with what this version can serve: the text (and the chips and
// the combo as text, the fallback 6.5 asks for), the picture and the addressing of 6.4 and 6.7 (to the
// public collection, cc the author's followers, which answer since FRD 6). Mentions and hashtags in tag,
// and flowa:tricks, come with the item that sends them (FWR 4); a reader that knows only ActivityStreams
// sees the same post either way. Inside a Create or a page the Note has no @context of its own.
type outNote struct {
	Context      []any           `json:"@context,omitempty"`
	ID           string          `json:"id"`
	Type         string          `json:"type"`
	AttributedTo string          `json:"attributedTo"`
	URL          string          `json:"url"`
	Published    string          `json:"published"`
	To           []string        `json:"to"`
	Cc           []string        `json:"cc"`
	Sensitive    bool            `json:"sensitive"`
	Content      string          `json:"content"`
	Attachment   []outAttachment `json:"attachment,omitempty"`
}

// outAttachment is a post's picture. name and summary are its alt text, empty until the composer has a
// field for it (FWR 4); both are written, as 6.5 says.
type outAttachment struct {
	Type      string `json:"type"`
	MediaType string `json:"mediaType"`
	URL       string `json:"url"`
	Name      string `json:"name"`
	Summary   string `json:"summary"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
}

// outTombstone is what the actor of a deleted member who had opted in answers, with 410 (ActivityPub 6.4).
type outTombstone struct {
	Context    string `json:"@context"`
	ID         string `json:"id"`
	Type       string `json:"type"`
	FormerType string `json:"formerType"`
}

// outCreate is the Create that wraps a Public post's Note (6.4, 6.5): in a page of the outbox, at its own
// id, and, once FWR delivers, in the body sent to followers; createOf makes it for all three, so what is
// read and what is sent agree. Its id is the post's id with /activity, so it stays the same however
// often it is made, with nothing stored. Its actor, published, to and cc are the Note's (6.7). In a page
// it has no @context of its own.
type outCreate struct {
	Context   []any    `json:"@context,omitempty"`
	ID        string   `json:"id"`
	Type      string   `json:"type"`
	Actor     string   `json:"actor"`
	Published string   `json:"published"`
	To        []string `json:"to"`
	Cc        []string `json:"cc"`
	Object    outNote  `json:"object"`
}

// outCollection is an OrderedCollection (ActivityPub 5, AS2 Core 2.3 and Vocabulary): an outbox with its
// count and its first page, a followers or following collection with its count alone (or not even that
// while the follows flag is off), or an empty one (featured, the instance actor's outbox), whose
// orderedItems is written as [] so a reader sees the list it would hold.
type outCollection struct {
	Context      any       `json:"@context"`
	ID           string    `json:"id"`
	Type         string    `json:"type"`
	TotalItems   *int      `json:"totalItems,omitempty"`
	First        string    `json:"first,omitempty"`
	OrderedItems *[]string `json:"orderedItems,omitempty"`
}

// outPage is one page of an outbox (AS2 Core 2.3, an OrderedCollectionPage): pubPage Creates newest first,
// the collection it is part of, and the next (older) page when there is one. Pages are a singly linked
// list (AS2 Core 2.3 allows it): there is no prev, since a newer page would need a second cursor, and a
// reader walks from first by next (AS2 Core 2.3, "navigate to the first ... page then recursively follow
// the next").
type outPage struct {
	Context      []any       `json:"@context"`
	ID           string      `json:"id"`
	Type         string      `json:"type"`
	PartOf       string      `json:"partOf"`
	Next         string      `json:"next,omitempty"`
	OrderedItems []outCreate `json:"orderedItems"`
}

// apJSON writes a document as JSON without escaping <, > and &, which encoding/json does by default and
// which would turn the HTML of summary and content into <.
func apJSON(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

/* What the documents and the pages say, from the stored text. */

// propNames are the props' names as the page shows them (web/props.js).
var propNames = map[string]string{"nunchaku": "Nunchaku", "poi": "Poi", "staff": "Staff", "doublestaff": "Double staff", "buugeng": "Buugeng"}

// textURL finds the http and https links in a post's text, as the page links them.
var textURL = regexp.MustCompile(`https?://[^\s<>"]+`)

// textPart is a run of a post's text, or a link in it (URL set).
type textPart struct{ Text, URL string }

// textParts cuts text into runs and links. A link loses the punctuation that ends a sentence after it.
func textParts(s string) []textPart {
	var out []textPart
	at := 0
	for _, m := range textURL.FindAllStringIndex(s, -1) {
		end := m[1]
		for end > m[0] && strings.IndexByte(".,;:!?)'", s[end-1]) >= 0 {
			end--
		}
		if at < m[0] {
			out = append(out, textPart{Text: s[at:m[0]]})
		}
		out = append(out, textPart{Text: s[m[0]:end], URL: s[m[0]:end]})
		at = end
	}
	if at < len(s) {
		out = append(out, textPart{Text: s[at:]})
	}
	return out
}

// paraBreak splits paragraphs: a blank line, or more.
var paraBreak = regexp.MustCompile(`\n[ \t]*\n\s*`)

// noteHTML is the content of a Note (6.5): the text escaped by html.EscapeString, paragraphs on blank
// lines as <p>, line breaks as <br>, links as <a href> with rel="nofollow noopener", and each of more (the
// chips and the combo as text) as a paragraph of its own. Only p, br and a are written, which Mastodon keeps
// in every version (6.5). Mentions are plain text until FWR 4 writes them as Mastodon reads them.
func noteHTML(body string, more []string) string {
	var b strings.Builder
	para := func(s string) {
		b.WriteString("<p>")
		for i, line := range strings.Split(s, "\n") {
			if i > 0 {
				b.WriteString("<br>")
			}
			for _, p := range textParts(line) {
				if p.URL == "" {
					b.WriteString(html.EscapeString(p.Text))
					continue
				}
				b.WriteString(`<a href="` + html.EscapeString(p.URL) + `" rel="nofollow noopener">` + html.EscapeString(p.Text) + `</a>`)
			}
		}
		b.WriteString("</p>")
	}
	body = strings.ReplaceAll(strings.TrimSpace(body), "\r\n", "\n")
	if body != "" {
		for _, p := range paraBreak.Split(body, -1) {
			para(p)
		}
	}
	for _, m := range more {
		para(m)
	}
	return b.String()
}

// summaryHTML is a bio as the actor's summary: the same rules as a post's text.
func summaryHTML(bio string) string { return noteHTML(bio, nil) }

// skillNames are the names of a prop's skills from its latest published map, kept for a minute: the
// chips and combos of a public post read as names, and a renamed skill shows under its new name (6.5).
// The lock guards the map only and is never held while the database is asked: a caller that waits for
// it must never be holding a connection another caller needs. Two requests that find the names old at
// once both read them, and the later one is kept. The read takes the request's context; when it fails
// the shipped map's names are used and kept for namesRetry only, so a failing read costs one statement
// per prop in that time, not one for every chip and combo step of every post on a page (an outbox page
// has 20), and the published names come back soon after the database does.
func (a *app) skillNames(ctx context.Context, prop string) map[string]string {
	a.namesMu.Lock()
	c, ok := a.names[prop]
	a.namesMu.Unlock()
	if ok && time.Now().Before(c.until) {
		return c.names
	}
	var raw []byte
	err := a.db.QueryRowContext(ctx, `SELECT body FROM catalog_revisions WHERE prop = $1 AND status = 'published' ORDER BY rev DESC LIMIT 1`, prop).Scan(&raw)
	keep := err == nil || errors.Is(err, sql.ErrNoRows)
	if err != nil {
		raw, _ = shippedCatalog(prop)
	}
	var cat struct {
		Skills []struct{ ID, Name string } `json:"skills"`
	}
	json.Unmarshal(raw, &cat)
	names := map[string]string{}
	for _, s := range cat.Skills {
		names[s.ID] = s.Name
	}
	until := time.Now().Add(time.Minute)
	if !keep {
		until = time.Now().Add(namesRetry)
	}
	a.namesMu.Lock()
	defer a.namesMu.Unlock()
	if a.names == nil {
		a.names = map[string]skillNameCache{}
	}
	a.names[prop] = skillNameCache{names, until}
	return names
}

// namesRetry is how long the shipped names stand in after a failed read of the published ones.
const namesRetry = 10 * time.Second

type skillNameCache struct {
	names map[string]string
	until time.Time // used until then
}

// postExtras are a post's chips and combo as lines of text (6.5's fallback): "Tricks: Six basic figure-8s
// (Nunchaku), ..." and "Combo: Name: A, B, C". A skill the map no longer has is shown by its id.
func (a *app) postExtras(ctx context.Context, data []byte) []string {
	var d struct {
		Tags  []postTag  `json:"tags"`
		Combo *postCombo `json:"combo"`
	}
	json.Unmarshal(data, &d)
	name := func(prop, id string) string {
		if n := a.skillNames(ctx, prop)[id]; n != "" {
			return n
		}
		return id
	}
	var out []string
	if len(d.Tags) > 0 {
		var t []string
		for _, x := range d.Tags {
			t = append(t, name(x.Prop, x.Skill)+" ("+propNames[x.Prop]+")")
		}
		out = append(out, "Tricks: "+strings.Join(t, ", "))
	}
	if c := d.Combo; c != nil && knownProp(c.Prop) {
		var s []string
		for _, id := range c.Seq {
			s = append(s, name(c.Prop, id))
		}
		out = append(out, "Combo: "+c.Name+": "+strings.Join(s, ", "))
	}
	return out
}

// pictureOf reads the type and size of a stored picture (a data URL cleanImage wrote) from its first
// bytes: the extension of its public address, its media type, and its width and height in pixels when
// they can be read. ok is false when it is not a JPEG or a PNG.
func pictureOf(head string) (ext, mediaType string, w, h int, ok bool) {
	switch {
	case strings.HasPrefix(head, "data:image/jpeg;base64,"):
		ext, mediaType = "jpg", "image/jpeg"
	case strings.HasPrefix(head, "data:image/png;base64,"):
		ext, mediaType = "png", "image/png"
	default:
		return "", "", 0, 0, false
	}
	data := head[strings.IndexByte(head, ',')+1:]
	data = data[:len(data)/4*4]
	if raw, err := base64.StdEncoding.DecodeString(data); err == nil {
		if c, _, err := image.DecodeConfig(bytes.NewReader(raw)); err == nil {
			w, h = c.Width, c.Height
		}
	}
	return ext, mediaType, w, h, true
}

// pictureHead is how much of a stored picture the queries read to learn its type and size: the header of
// a JPEG that cleanImage wrote, or of a PNG, is in its first few hundred bytes.
const pictureHead = "1024" // bytes, written into the statements below

/* The member's public face: one statement each. $2 is true on a single-profile instance, whose owner's
opt-in is FLOWA_FEDERATE=on (as in webFingerSQL). */

// publicMember is the condition that the member u may be shown beyond this server. A remote account never
// meets it: the check users_remote_inert (migration 15) keeps its federates and owner false.
const publicMember = `(u.federates OR ($2 AND u.owner)) AND (u.suspended_until IS NULL OR u.suspended_until <= now())`

// publicPost is the condition that the post p may be shown: Public, not hidden, not a milestone (F13).
const publicPost = `p.audience = 'public' AND p.hidden_at IS NULL AND p.kind = 'post'`

// actorGateSQL finds a member who may be shown, by name, and says whether the name is retired: one look
// in each primary key.
const actorGateSQL = `SELECT (SELECT u.id FROM users u WHERE u.username = $1 AND ` + publicMember + `), ` + retiredSQL

// actorDocSQL reads what an actor document says of the member $1 (the gate has let them through): the
// profile, the first bytes of the picture and a hash of it for its address, and the public key.
const actorDocSQL = `SELECT u.username, u.display_name, u.bio, u.links, u.props, u.created_at, left(u.avatar, 64),
	CASE WHEN u.avatar = '' THEN '' ELSE left(md5(u.avatar), 12) END, COALESCE(k.public_key, '')
	FROM users u LEFT JOIN ap_keys k ON k.user_id = u.id WHERE u.id = $1`

// profilePageSQL is a member's public page in one statement: the member, if they may be shown, with a page
// of their Public posts newest first ($3: older than this post, 0 for the first page; $4 how many), and
// whether the name is retired. posts_public serves the posts. $3 is a bigint like the ids it is compared
// with: written as "$3 = 0" alone, Postgres would take it for an integer and fail on a cursor past 2^31.
const profilePageSQL = `SELECT r.retired, m.id, m.username, m.display_name, m.bio, m.links, m.head, m.tag, m.pid, m.body, m.image, m.data, m.created_at
	FROM (SELECT ` + retiredSQL + ` AS retired) r LEFT JOIN (
		SELECT u.id, u.username, u.display_name, u.bio, u.links, left(u.avatar, 64) AS head, CASE WHEN u.avatar = '' THEN '' ELSE left(md5(u.avatar), 12) END AS tag,
			p.id AS pid, p.body, left(p.image, ` + pictureHead + `) AS image, p.data, p.created_at
		FROM users u LEFT JOIN LATERAL (SELECT p.id, p.body, p.image, p.data, p.created_at FROM posts p WHERE p.user_id = u.id AND ` + publicPost + `
			AND ($3::bigint = 0 OR p.id < $3) ORDER BY p.id DESC LIMIT $4) p ON true
		WHERE u.username = $1 AND ` + publicMember + `) m ON true ORDER BY m.pid DESC`

// publicPostSQL reads a Public post $3 by the member $1, with what its page and its Note say of the author.
const publicPostSQL = `SELECT u.username, u.display_name, left(u.avatar, 64), CASE WHEN u.avatar = '' THEN '' ELSE left(md5(u.avatar), 12) END,
	p.body, left(p.image, ` + pictureHead + `), p.data, p.created_at
	FROM posts p JOIN users u ON u.id = p.user_id WHERE p.id = $3 AND u.username = $1 AND ` + publicPost + ` AND ` + publicMember

// postPictureSQL reads the picture of a Public post $3 by the member $1, and avatarSQL a member's picture.
const (
	postPictureSQL = `SELECT p.image FROM posts p JOIN users u ON u.id = p.user_id WHERE p.id = $3 AND u.username = $1 AND ` + publicPost + ` AND ` + publicMember
	avatarSQL      = `SELECT u.avatar FROM users u WHERE u.username = $1 AND ` + publicMember
)

/* The answers. */

// publicAllowed counts a request against the address's allowance for these addresses, and answers 429
// once it is used up. It is asked first, before anything is read.
func (a *app) publicAllowed(w http.ResponseWriter, r *http.Request) bool {
	from := limitKey(a.clientIP(r))
	if a.lim.public.add(from) {
		return true
	}
	logRefusal(w, "too many reads of public pages", "from "+from)
	h := w.Header()
	h.Set("Retry-After", "3600")
	h.Set("Cache-Control", "no-store")
	http.Error(w, "Too many requests from this address. Try again in an hour.", http.StatusTooManyRequests)
	return false
}

// inboxPath says whether a path is an inbox an actor document names: /actor/inbox, or /users/{name}/inbox
// for any name, so the answer says nothing of who exists.
func inboxPath(p string) bool {
	if p == "/actor/inbox" {
		return true
	}
	name, ok := strings.CutSuffix(strings.TrimPrefix(p, "/users/"), "/inbox")
	return ok && strings.HasPrefix(p, "/users/") && name != "" && !strings.Contains(name, "/")
}

// publicPaths says whether a path is one of the addresses above, for publicMethods.
func publicPaths(p string) bool {
	if p == "/actor" || p == "/actor/outbox" {
		return true
	}
	rest, ok := strings.CutPrefix(p, "/users/")
	if !ok {
		return false
	}
	parts := strings.Split(rest, "/")
	switch len(parts) {
	case 1:
		return parts[0] != ""
	case 2:
		return parts[1] == "avatar.jpg" || parts[1] == "avatar.png" || slices.Contains(memberCollections, parts[1])
	case 3:
		return parts[1] == "posts"
	case 4:
		return parts[1] == "posts" && (parts[3] == "picture.jpg" || parts[3] == "picture.png" || parts[3] == "activity")
	}
	return false
}

// pubAnswer is a made answer: its bytes, type and validator.
type pubAnswer struct {
	body  []byte
	ctype string
	etag  string
}

func newAnswer(body []byte, ctype string) pubAnswer {
	return pubAnswer{body, ctype, `"` + hashOf(body) + `"`}
}

// serve sends an answer, a page with the policy of 5.6, and a 200 with its validator: ServeContent answers If-None-Match with 304, HEAD with the
// headers alone, and sets the length.
func (p pubAnswer) serve(w http.ResponseWriter, r *http.Request, code int, keep string) {
	h := w.Header()
	h.Set("Content-Type", p.ctype)
	if strings.HasPrefix(p.ctype, "text/html") {
		h.Set("Content-Security-Policy", visitorCSP) // 5.6: no script, pictures and style from this server only
	}
	h.Set("Cache-Control", keep)
	if code != http.StatusOK {
		h.Set("Content-Length", strconv.Itoa(len(p.body)))
		w.WriteHeader(code)
		if r.Method != http.MethodHead {
			w.Write(p.body)
		}
		return
	}
	h.Set("ETag", p.etag)
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(p.body))
}

// pubNotFoundJSON is the one 404 of the documents.
var pubNotFoundJSON = newAnswer([]byte(`{"error":"Not found."}`), "application/json")

// pubNoPicture is the one 404 of the pictures.
var pubNoPicture = newAnswer([]byte("Not found.\n"), "text/plain; charset=utf-8")

// pubSite is what every public page needs: the style sheet and the icon by their hashed names, and the
// 404 and 410 pages, made once.
type pubSite struct {
	css, icon      string
	missing, wiped pubAnswer
}

func (a *app) pubSiteOf(st *static) *pubSite {
	s := &pubSite{}
	if st != nil {
		if f, ok := st.plain["/app.css"]; ok {
			s.css = "/" + hashedName("app.css", f.hash)
		}
		if f, ok := st.plain["/icon.svg"]; ok {
			s.icon = "/" + hashedName("icon.svg", f.hash)
		}
	}
	s.missing = s.page(pubData{Kind: "missing", Title: "Not found"})
	s.wiped = s.page(pubData{Kind: "gone", Title: "Account deleted"})
	return s
}

// page writes one page with pubTmpl.
func (s *pubSite) page(d pubData) pubAnswer {
	d.CSS, d.Icon = s.css, s.icon
	var b bytes.Buffer
	if err := pubTmpl.Execute(&b, d); err != nil {
		// The template and the data are this file's; an error is a mistake here, and the 404 says less.
		log.Printf("error: a public page: %v", err)
		return s.missing
	}
	return newAnswer(b.Bytes(), "text/html; charset=utf-8")
}

// notFound is the one 404 of a negotiated address, in the representation asked for.
func (s *pubSite) notFound(w http.ResponseWriter, r *http.Request, activity bool) {
	if activity {
		pubNotFoundJSON.serve(w, r, http.StatusNotFound, notFoundKeep)
		return
	}
	s.missing.serve(w, r, http.StatusNotFound, notFoundKeep)
}

// unavailable is said while an opted-in member's key is still being made (5.7): the actor document needs
// it, and it comes within seconds.
func unavailable(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Retry-After", "60")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	w.Write([]byte(`{"error":"Not ready yet. Try again in a minute."}`))
}

// pubData is what a public page is given. Every field is plain text that html/template escapes for where
// it stands; none is marked safe. Links and picture addresses are built here from stored values: links
// only to the sites cleanLink takes (or a post's own http and https links, which html/template checks
// again), pictures only from this server.
type pubData struct {
	Kind, Title, CSS, Icon string
	Who                    *pubWho
	Posts                  []pubPost
	Older                  string
	Host, ActorHandle      string
}

type pubWho struct {
	Name, Username, Handle, Initial, Bio, Picture, Profile, App string
	Links                                                       []visitorLink
}

type pubPost struct {
	URL, Picture, PictureAlt, When, Day string
	Paras                               [][]textPart
	Extras                              []string
}

var pubTmpl = template.Must(template.New("pub").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="dark light">
<meta name="robots" content="noindex">
<meta name="referrer" content="no-referrer">
<title>{{.Title}}</title>
{{with .Icon}}<link rel="icon" href="{{.}}" type="image/svg+xml">
{{end}}{{with .CSS}}<link rel="stylesheet" href="{{.}}">
{{end}}</head>
<body class="pub">
<header class="pub-top"><a class="btn primary" href="/index.html#/signin">Sign in</a></header>
<main class="pub-main">
{{if eq .Kind "missing"}}<section class="card pub-card" aria-labelledby="pub-h"><h1 id="pub-h">Nothing here</h1><p>There is no public profile or post at this address. It may never have been public, or it is no longer shared.</p></section>
{{else if eq .Kind "gone"}}<section class="card pub-card" aria-labelledby="pub-h"><h1 id="pub-h">Account deleted</h1><p>The account that had this address was deleted. Its name is not given to anyone else.</p></section>
{{else if eq .Kind "actor"}}<section class="card pub-card" aria-labelledby="pub-h"><h1 id="pub-h">{{.Host}}</h1><p class="pub-handle">{{.ActorHandle}}</p><p>This is the server's own account. Other servers see it when this Flowa asks them for something on nobody's behalf; it posts nothing and follows nobody.</p></section>
{{else}}{{with .Who}}<article class="card pub-card" aria-labelledby="pub-name">
<div class="pub-who">{{if .Picture}}<img class="pub-pic" src="{{.Picture}}" alt="" width="72" height="72">{{else}}<span class="pub-pic pub-initial" aria-hidden="true">{{.Initial}}</span>{{end}}
<div class="pub-names">{{if eq $.Kind "post"}}<p class="pub-title" id="pub-name"><a href="{{.Profile}}">{{.Name}}</a></p>{{else}}<h1 id="pub-name">{{.Name}}</h1>{{end}}<p class="pub-handle">{{.Handle}}</p></div></div>
{{if ne $.Kind "post"}}{{with .Bio}}<p class="pub-text">{{.}}</p>
{{end}}{{if .Links}}<ul class="pub-links" aria-label="Links">{{range .Links}}<li><a class="btn sm" href="{{.URL}}" rel="nofollow noopener ugc">{{.Name}}</a></li>{{end}}</ul>
{{end}}{{end}}</article>
{{end}}{{if eq .Kind "profile"}}<section class="pub-posts" aria-labelledby="pub-posts-h"><h2 id="pub-posts-h">Public posts</h2>
{{if not .Posts}}<p class="pub-hint">No public posts yet.</p>
{{end}}{{end}}{{range .Posts}}<article class="card pub-card pub-post">
{{if eq $.Kind "post"}}<h1 class="pub-when"><time datetime="{{.When}}">{{.Day}}</time></h1>{{else}}<h3 class="pub-when"><a href="{{.URL}}"><time datetime="{{.When}}">{{.Day}}</time></a></h3>{{end}}
{{range .Paras}}<p class="pub-text">{{range .}}{{if .URL}}<a href="{{.URL}}" rel="nofollow noopener ugc">{{.Text}}</a>{{else}}{{.Text}}{{end}}{{end}}</p>
{{end}}{{range .Extras}}<p class="pub-text pub-hint">{{.}}</p>
{{end}}{{if .Picture}}<img class="pub-img" src="{{.Picture}}" alt="{{.PictureAlt}}" loading="lazy">
{{end}}</article>
{{end}}{{if eq .Kind "profile"}}{{with .Older}}<p><a class="btn sm" href="{{.}}">Older posts</a></p>
{{end}}</section>
{{end}}{{with .Who}}<p class="pub-hint">On Flowa, a tracker for flow arts. <a href="{{.App}}">Open in Flowa</a> (signed in).</p>
{{end}}{{end}}</main>
</body>
</html>
`))

// keptDoc is an actor document as made, and when.
type keptDoc struct {
	ans pubAnswer
	at  time.Time
}

// keptActor is an owner's actor document from memory, while it is younger than docsAge.
func (f *federation) keptActor(o keyOwner) (pubAnswer, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.docs[o]
	if !ok || time.Since(d.at) >= docsAge {
		return pubAnswer{}, false
	}
	return d.ans, true
}

// keepActor keeps an owner's document, unless a drop came while it was being made (gen is docsGen as it
// was read before the database was), so a document made from a profile just replaced is never kept.
func (f *federation) keepActor(o keyOwner, gen uint64, p pubAnswer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.docsGen != gen {
		return
	}
	if f.docs == nil || len(f.docs) >= docsMax {
		f.docs = map[keyOwner]keptDoc{}
	}
	f.docs[o] = keptDoc{p, time.Now()}
}

// dropActor forgets an owner's document, after a change of anything it says: the profile or the picture
// (handlePutMe), the opt-in (handlePutMe; the gate is asked on every request anyway), or the key (the key
// worker, rotateKeys). A nil federation has none.
func (f *federation) dropActor(o keyOwner) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.docsGen++
	delete(f.docs, o)
}

// docGen is the generation of the kept documents, read before one is made.
func (f *federation) docGen() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.docsGen
}

// actorDoc is a member's actor document (5.5), from memory or made: one statement. errNoKey while the key
// is still being made.
func (a *app) actorDoc(ctx context.Context, uid int64) (pubAnswer, error) {
	f := a.fed
	if p, ok := f.keptActor(keyOwner(uid)); ok {
		return p, nil
	}
	gen := f.docGen()
	var name, display, bio, head, tag, pubPEM string
	var links, props []byte
	var created time.Time
	if err := a.db.QueryRowContext(ctx, actorDocSQL, uid).Scan(&name, &display, &bio, &links, &props, &created, &head, &tag, &pubPEM); err != nil {
		return pubAnswer{}, err
	}
	if pubPEM == "" {
		return pubAnswer{}, errNoKey
	}
	id := f.origin.url + "/users/" + name
	no := false
	doc := outActor{Context: actorContext, ID: id, Type: "Person", PreferredUsername: name, Name: display, Summary: summaryHTML(bio),
		URL: id, Inbox: id + "/inbox", Outbox: id + "/outbox", Followers: id + "/followers", Following: id + "/following", Featured: id + "/featured",
		ManuallyApprovesFollowers: &no, Discoverable: &no, Indexable: &no, Published: created.UTC().Format(time.RFC3339),
		PublicKey: outKey{ID: id + "#main-key", Owner: id, PublicKeyPem: pubPEM}}
	if ext, mt, _, _, ok := pictureOf(head); ok && tag != "" {
		doc.Icon = &outImage{Type: "Image", MediaType: mt, URL: id + "/avatar." + ext + "?v=" + tag}
	}
	// The props a member practises and their links, as profile fields (5.5); no number (F12).
	var ids []string
	json.Unmarshal(props, &ids)
	var names []string
	for _, p := range ids {
		if n := propNames[p]; n != "" {
			names = append(names, n)
		}
	}
	if len(names) > 0 {
		doc.Attachment = append(doc.Attachment, outProperty{Type: "PropertyValue", Name: "Props", Value: html.EscapeString(strings.Join(names, ", "))})
	}
	stored := map[string]string{}
	json.Unmarshal(links, &stored)
	for _, k := range linkKinds {
		handle, _ := cleanLink(k, stored[k])
		if href, ok := visitorLinkURL(k, stored[k]); ok {
			doc.Attachment = append(doc.Attachment, outProperty{Type: "PropertyValue", Name: visitorLinkNames[k],
				Value: `<a href="` + html.EscapeString(href) + `" rel="me nofollow noopener" target="_blank">` + html.EscapeString(handle) + `</a>`})
		}
	}
	b, err := apJSON(doc)
	if err != nil {
		return pubAnswer{}, err
	}
	p := newAnswer(b, activityType)
	f.keepActor(keyOwner(uid), gen, p)
	return p, nil
}

// instanceDoc is the instance actor's document (5.5): an Application named by the host, with its key, and
// the inbox and outbox ActivityPub 4.1 requires it to name.
func (a *app) instanceDoc(ctx context.Context) (pubAnswer, error) {
	f := a.fed
	if p, ok := f.keptActor(instanceActor); ok {
		return p, nil
	}
	gen := f.docGen()
	var pubPEM string
	err := a.db.QueryRowContext(ctx, `SELECT public_key FROM ap_keys WHERE user_id IS NULL`).Scan(&pubPEM)
	if errors.Is(err, sql.ErrNoRows) {
		return pubAnswer{}, errNoKey
	}
	if err != nil {
		return pubAnswer{}, err
	}
	id := f.origin.url + "/actor"
	b, err := apJSON(outActor{Context: actorContext, ID: id, Type: "Application", PreferredUsername: f.origin.host, URL: id,
		Inbox: id + "/inbox", Outbox: id + "/outbox", PublicKey: outKey{ID: id + "#main-key", Owner: id, PublicKeyPem: pubPEM}})
	if err != nil {
		return pubAnswer{}, err
	}
	p := newAnswer(b, activityType)
	f.keepActor(instanceActor, gen, p)
	return p, nil
}

// pubFailed answers a public address when the server failed, without a word about why.
func pubFailed(w http.ResponseWriter, what string, err error) {
	log.Printf("error: %s: %v", what, err)
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	http.Error(w, "Something went wrong on the server.", http.StatusInternalServerError)
}

// canonicalName is the {name} of a path when the path is written exactly as an id is (no percent-encoding,
// lower case, a name usernameRE takes): another spelling of an id is not that id, and is answered as an
// address nobody has, without a query.
func canonicalName(r *http.Request, rest string) (string, bool) {
	name := r.PathValue("name")
	return name, usernameRE.MatchString(name) && r.URL.EscapedPath() == "/users/"+name+rest
}

// canonicalID is the {id} of a path when it is a post id written as one: digits, no sign, no leading zero.
func canonicalID(r *http.Request) (int64, bool) {
	s := r.PathValue("id")
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && n > 0 && strconv.FormatInt(n, 10) == s
}

// whoOf is a member as a public page shows them.
func (a *app) whoOf(name, display, bio string, links []byte, head, tag string) *pubWho {
	w := &pubWho{Name: display, Username: name, Handle: "@" + name + "@" + a.fed.origin.host, Bio: strings.TrimSpace(bio),
		Profile: "/users/" + name, App: "/#/u/" + name}
	if w.Name == "" {
		w.Name = name
	}
	if i := strings.IndexFunc(w.Name, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }); i >= 0 {
		r, _ := utf8.DecodeRuneInString(w.Name[i:])
		w.Initial = strings.ToUpper(string(r))
	}
	if ext, _, _, _, ok := pictureOf(head); ok && tag != "" {
		w.Picture = "/users/" + name + "/avatar." + ext + "?v=" + tag
	}
	stored := map[string]string{}
	json.Unmarshal(links, &stored)
	for _, k := range linkKinds {
		if href, ok := visitorLinkURL(k, stored[k]); ok {
			w.Links = append(w.Links, visitorLink{visitorLinkNames[k], href})
		}
	}
	return w
}

// postOf is a Public post as a public page shows it.
func (a *app) postOf(ctx context.Context, name string, id int64, body, head string, data []byte, created time.Time) pubPost {
	url := "/users/" + name + "/posts/" + strconv.FormatInt(id, 10)
	p := pubPost{URL: url, When: created.UTC().Format(time.RFC3339), Day: created.UTC().Format("2 January 2006"), Extras: a.postExtras(ctx, data)}
	if body = strings.TrimSpace(strings.ReplaceAll(body, "\r\n", "\n")); body != "" {
		for _, para := range paraBreak.Split(body, -1) {
			p.Paras = append(p.Paras, textParts(para))
		}
	}
	if ext, _, _, _, ok := pictureOf(head); ok {
		p.Picture, p.PictureAlt = url+"/picture."+ext, "Picture in the post"
	}
	return p
}

// handleActor answers GET /users/{name}: the actor document or the member's public page (5.5, 5.6).
func (a *app) handleActor(st *static) http.HandlerFunc {
	site := a.pubSiteOf(st)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Vary", "Accept")
		if !a.publicAllowed(w, r) {
			return
		}
		activity := wantsActivity(r.Header)
		name, ok := canonicalName(r, "")
		if a.fed.refusal() != "" || !ok {
			site.notFound(w, r, activity)
			return
		}
		if activity {
			a.serveActor(w, r, site, name)
		} else {
			a.serveProfile(w, r, site, name)
		}
	}
}

// serveActor sends a member's actor document. The origin is frozen first (ensureOrigin): this names the
// member's actor id to whoever asked.
func (a *app) serveActor(w http.ResponseWriter, r *http.Request, site *pubSite, name string) {
	var uid sql.NullInt64
	var retired bool
	if err := a.db.QueryRowContext(r.Context(), actorGateSQL, name, a.mode == modeSingle).Scan(&uid, &retired); err != nil {
		pubFailed(w, "an actor document", err)
		return
	}
	if !uid.Valid {
		if retired {
			b, _ := apJSON(outTombstone{Context: asContext, ID: a.fed.origin.url + "/users/" + name, Type: "Tombstone", FormerType: "Person"})
			newAnswer(b, activityType).serve(w, r, http.StatusGone, apKeep)
			return
		}
		site.notFound(w, r, true)
		return
	}
	if !a.originFrozen(w, r, site) {
		return
	}
	doc, err := a.actorDoc(r.Context(), uid.Int64)
	if errors.Is(err, errNoKey) {
		unavailable(w)
		return
	}
	if err != nil {
		pubFailed(w, "an actor document", err)
		return
	}
	doc.serve(w, r, http.StatusOK, apKeep)
}

// originFrozen freezes the origin before an id of a member goes out (4.3), and answers the 404 when the
// origin turns out to have moved, as WebFinger does.
func (a *app) originFrozen(w http.ResponseWriter, r *http.Request, site *pubSite) bool {
	err := a.fed.ensureOrigin(r.Context())
	if err == nil {
		return true
	}
	var moved originMoved
	if errors.As(err, &moved) {
		site.notFound(w, r, true)
		return false
	}
	pubFailed(w, "recording the origin", err)
	return false
}

// serveProfile sends a member's public page: the profile and their Public posts, newest first, pubPage at a
// time, older ones by ?before=<id>. One statement.
func (a *app) serveProfile(w http.ResponseWriter, r *http.Request, site *pubSite, name string) {
	before, ok := pageCursor(r.URL.Query())
	if !ok {
		site.notFound(w, r, false)
		return
	}
	// The rows are read whole and closed before anything else is done: making a post's lines may ask the
	// database again (skillNames), and a request must never hold one connection while it waits for another,
	// or a few at once could take the whole pool and stop every request (TestDBOneConnection).
	type profileRow struct {
		uid, pid                                  sql.NullInt64
		uname, display, bio, head, tag, body, img sql.NullString
		links, data                               []byte
		created                                   sql.NullTime
	}
	var retired bool
	var got []profileRow
	rows, err := a.db.QueryContext(r.Context(), profilePageSQL, name, a.mode == modeSingle, before, pubPage+1)
	if err != nil {
		pubFailed(w, "a public profile", err)
		return
	}
	for rows.Next() {
		var x profileRow
		if err = rows.Scan(&retired, &x.uid, &x.uname, &x.display, &x.bio, &x.links, &x.head, &x.tag, &x.pid, &x.body, &x.img, &x.data, &x.created); err != nil {
			break
		}
		got = append(got, x)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		pubFailed(w, "a public profile", err)
		return
	}
	d := pubData{Kind: "profile"}
	var last int64
	n := 0
	for _, x := range got {
		if !x.uid.Valid {
			break
		}
		if d.Who == nil {
			d.Who = a.whoOf(x.uname.String, x.display.String, x.bio.String, x.links, x.head.String, x.tag.String)
		}
		if !x.pid.Valid {
			continue
		}
		if n++; n > pubPage {
			d.Older = "/users/" + name + "?before=" + strconv.FormatInt(last, 10)
			break
		}
		last = x.pid.Int64
		d.Posts = append(d.Posts, a.postOf(r.Context(), x.uname.String, x.pid.Int64, x.body.String, x.img.String, x.data, x.created.Time))
	}
	switch {
	case d.Who == nil && retired:
		site.wiped.serve(w, r, http.StatusGone, apKeep)
	case d.Who == nil:
		site.notFound(w, r, false)
	default:
		d.Title = d.Who.Name + " (" + d.Who.Handle + ")"
		site.page(d).serve(w, r, http.StatusOK, apKeep)
	}
}

// handleInstanceActor answers GET /actor: the instance actor's document, or a page that says what it is.
// It freezes nothing: like its WebFinger answer (5.3, as built in FRD 4), anyone can ask for it, a scanner
// or the operator checking the setup, and it names no member, so a mistyped origin must not be locked in
// by it. The first request the instance actor signs (FRD 7) is what announces it.
func (a *app) handleInstanceActor(st *static) http.HandlerFunc {
	site := a.pubSiteOf(st)
	host := a.fed.origin.host
	page := site.page(pubData{Kind: "actor", Title: host, Host: host, ActorHandle: "@" + host + "@" + host})
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Vary", "Accept")
		if !a.publicAllowed(w, r) {
			return
		}
		activity := wantsActivity(r.Header)
		if a.fed.refusal() != "" || r.URL.EscapedPath() != "/actor" {
			site.notFound(w, r, activity)
			return
		}
		if !activity {
			page.serve(w, r, http.StatusOK, apKeep)
			return
		}
		doc, err := a.instanceDoc(r.Context())
		if errors.Is(err, errNoKey) {
			unavailable(w)
			return
		}
		if err != nil {
			pubFailed(w, "the instance actor", err)
			return
		}
		doc.serve(w, r, http.StatusOK, apKeep)
	}
}

// handlePublicPost answers GET /users/{name}/posts/{id}: a Public post as a Note (6.5) or as a page; and,
// with create, GET /users/{name}/posts/{id}/activity: the Create that wraps that Note (6.4), the id the
// outbox and FWR's deliveries give it, which a browser is sent on from to the post's own address.
func (a *app) handlePublicPost(st *static, create bool) http.HandlerFunc {
	site := a.pubSiteOf(st)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Vary", "Accept")
		if !a.publicAllowed(w, r) {
			return
		}
		activity := wantsActivity(r.Header)
		id, idOK := canonicalID(r)
		rest := "/posts/" + r.PathValue("id")
		if create {
			rest += "/activity"
		}
		name, ok := canonicalName(r, rest)
		if create {
			_, _, qOK := docQuery(r.URL.RawQuery, false)
			ok = ok && qOK
		}
		if a.fed.refusal() != "" || !ok || !idOK {
			site.notFound(w, r, activity)
			return
		}
		if create && !activity {
			seeOther(w, "/users/"+name+"/posts/"+strconv.FormatInt(id, 10))
			return
		}
		var uname, display, head, tag, body, image string
		var data []byte
		var created time.Time
		err := a.db.QueryRowContext(r.Context(), publicPostSQL, name, a.mode == modeSingle, id).Scan(&uname, &display, &head, &tag, &body, &image, &data, &created)
		if errors.Is(err, sql.ErrNoRows) {
			site.notFound(w, r, activity)
			return
		}
		if err != nil {
			pubFailed(w, "a public post", err)
			return
		}
		if !activity {
			who := a.whoOf(uname, display, "", nil, head, tag)
			who.App = "/#/post/" + strconv.FormatInt(id, 10)
			d := pubData{Kind: "post", Who: who, Posts: []pubPost{a.postOf(r.Context(), uname, id, body, image, data, created)}, Title: "Post by " + who.Name + " (" + who.Handle + ")"}
			site.page(d).serve(w, r, http.StatusOK, apKeep)
			return
		}
		if !a.originFrozen(w, r, site) {
			return
		}
		var doc any
		if create {
			c := a.createOf(r.Context(), uname, id, body, image, data, created)
			c.Context = noteContext
			doc = c
		} else {
			n := a.noteOf(r.Context(), uname, id, body, image, data, created)
			n.Context = noteContext
			doc = n
		}
		b, err := apJSON(doc)
		if err != nil {
			pubFailed(w, "a note", err)
			return
		}
		newAnswer(b, activityType).serve(w, r, http.StatusOK, apKeep)
	}
}

// noteOf is a Public post by the member name as the Note of 6.5, without @context: to the public
// collection, cc the author's followers (6.4, 6.7), the text with the chips and the combo as text, and
// the picture. image is the first pictureHead bytes of the stored picture.
func (a *app) noteOf(ctx context.Context, name string, id int64, body, image string, data []byte, created time.Time) outNote {
	actor := a.fed.origin.url + "/users/" + name
	n := outNote{ID: actor + "/posts/" + strconv.FormatInt(id, 10), Type: "Note", AttributedTo: actor,
		Published: created.UTC().Format(time.RFC3339), To: []string{publicCollection}, Cc: []string{actor + "/followers"},
		Content: noteHTML(body, a.postExtras(ctx, data))}
	n.URL = n.ID
	if ext, mt, pw, ph, ok := pictureOf(image); ok {
		n.Attachment = []outAttachment{{Type: "Image", MediaType: mt, URL: n.ID + "/picture." + ext, Width: pw, Height: ph}}
	}
	return n
}

// createOf is the Create of a Public post (6.4), without @context: the one function that makes it for the
// outbox, for its own id, and for FWR's deliveries. The activity is the post's: its actor is the author,
// its published the post's, its to and cc the Note's (6.7: Mastodon reads the audience from them, and an
// activity whose addressing differs from its object's would be read two ways).
func (a *app) createOf(ctx context.Context, name string, id int64, body, image string, data []byte, created time.Time) outCreate {
	n := a.noteOf(ctx, name, id, body, image, data, created)
	return outCreate{ID: n.ID + "/activity", Type: "Create", Actor: n.AttributedTo, Published: n.Published, To: n.To, Cc: n.Cc, Object: n}
}

/* The outbox and the collections (ActivityPub 5.1, 5.3 and 5.4; docs/federation.md 5.2, 6.2 and F10), at
/users/{name}/outbox, /followers, /following and /featured, and the instance actor's outbox at
/actor/outbox. Each is a document for other servers and has no page of its own: a browser is sent on to
the profile page, or the instance actor's (seeOther). Who is shown and what is counted follow the rules
above, through the same statements: the gate of publicMember, Public posts by publicPost, one 404 for
everything not shown. A retired name is that 404 too, as a retired name's posts are: the Tombstone at the
actor is what tells other servers to stop asking. */

// outboxSQL finds a member who may be shown, by name, with the number of their Public posts: the count
// walks posts_public for that one member, so it costs the member's Public posts, never their other posts
// or anyone else's (TestDBQueryPlans).
const outboxSQL = `SELECT u.id, (SELECT count(*) FROM posts p WHERE p.user_id = u.id AND ` + publicPost + `) FROM users u WHERE u.username = $1 AND ` + publicMember

// pageCursor reads the cursor of a page of the profile, ?before=<post id>: the posts older than that one,
// or the newest (0) when it is absent. ok is false when it is given and is not one positive id written
// once. It only ever bounds a statement that is already held to one member's Public posts
// (profilePageSQL), so no value of it reaches anything else. The outbox reads the same cursor more
// strictly (docQuery), since a document's id must be the address it answers at.
func pageCursor(q url.Values) (int64, bool) {
	if !q.Has("before") {
		return 0, true
	}
	n, err := strconv.ParseInt(q.Get("before"), 10, 64)
	return n, err == nil && n > 0 && len(q["before"]) == 1
}

// docQuery reads the query of a document-only address (the outbox, the collections, a Create, the instance
// actor's outbox) from the raw query, so nothing is dropped unseen (url.ParseQuery skips a pair with a
// semicolon). Every address answers with no query at all; the outbox (pages true) also answers exactly
// "page=true", its first page, and "page=true&before=<id>", an older one, the id written as
// strconv.FormatInt writes it (no sign, no leading zero, no escape). Anything else is no page and is the
// 404, so each page has one address, the one its id names, and a cache one copy of it. There is no size
// parameter: a page is pubPage Creates.
func docQuery(raw string, pages bool) (page bool, before int64, ok bool) {
	switch {
	case raw == "":
		return false, 0, true
	case !pages:
		return false, 0, false
	case raw == "page=true":
		return true, 0, true
	}
	s, found := strings.CutPrefix(raw, "page=true&before=")
	n, err := strconv.ParseInt(s, 10, 64)
	if !found || err != nil || n <= 0 || strconv.FormatInt(n, 10) != s {
		return false, 0, false
	}
	return true, n, true
}

// seeOther sends a browser from a document-only address to the page that shows the same thing, as people
// get pages where servers get documents (5.6). It reads nothing, so it says the same for every name, shown
// or not, and the page it leads to answers for itself.
func seeOther(w http.ResponseWriter, to string) {
	h := w.Header()
	h.Set("Location", to)
	h.Set("Cache-Control", apKeep)
	w.WriteHeader(http.StatusSeeOther)
}

// serveDoc sends a document made here with the headers of 5.6.
func serveDoc(w http.ResponseWriter, r *http.Request, what string, v any) {
	b, err := apJSON(v)
	if err != nil {
		pubFailed(w, what, err)
		return
	}
	newAnswer(b, activityType).serve(w, r, http.StatusOK, apKeep)
}

// memberCollections are the collections of a member, by the last part of their address (5.2).
var memberCollections = []string{"outbox", "followers", "following", "featured"}

// handleCollection answers GET /users/{name}/{kind}, kind one of memberCollections. The followers,
// following and featured documents take no query (docQuery).
func (a *app) handleCollection(st *static, kind string) http.HandlerFunc {
	site := a.pubSiteOf(st)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Vary", "Accept")
		if !a.publicAllowed(w, r) {
			return
		}
		activity := wantsActivity(r.Header)
		name, ok := canonicalName(r, "/"+kind)
		page, before, qOK := docQuery(r.URL.RawQuery, kind == "outbox")
		if a.fed.refusal() != "" || !ok || !qOK {
			site.notFound(w, r, activity)
			return
		}
		if !activity {
			to := "/users/" + name
			if before > 0 {
				to += "?before=" + strconv.FormatInt(before, 10)
			}
			seeOther(w, to)
			return
		}
		id := a.fed.origin.url + "/users/" + name + "/" + kind
		switch {
		case kind == "outbox" && page:
			a.serveOutboxPage(w, r, site, name, id, before)
		case kind == "outbox":
			a.serveOutbox(w, r, site, name, id)
		default:
			a.serveFollows(w, r, site, name, id, kind)
		}
	}
}

// serveOutbox sends a member's outbox (ActivityPub 5.1): an OrderedCollection with the number of their
// Public posts and its first page. One statement.
func (a *app) serveOutbox(w http.ResponseWriter, r *http.Request, site *pubSite, name, id string) {
	var uid int64
	var n int
	err := a.db.QueryRowContext(r.Context(), outboxSQL, name, a.mode == modeSingle).Scan(&uid, &n)
	if errors.Is(err, sql.ErrNoRows) {
		site.notFound(w, r, true)
		return
	}
	if err != nil {
		pubFailed(w, "an outbox", err)
		return
	}
	if !a.originFrozen(w, r, site) {
		return
	}
	serveDoc(w, r, "an outbox", outCollection{Context: asContext, ID: id, Type: "OrderedCollection", TotalItems: &n, First: id + "?page=true"})
}

// outboxRow is a post of a page of the outbox as profilePageSQL reads it.
type outboxRow struct {
	id         int64
	body, head string
	data       []byte
	created    time.Time
}

// serveOutboxPage sends a page of a member's outbox: pubPage Creates of their Public posts, newest first,
// older than before (ActivityPub 5: reverse chronological, by the post id, which only grows). It is the
// profile page's statement, so the page shows exactly the posts the profile page shows, and one more row
// says whether there is a next page. The rows are read before any Create is made, since a Create reads the
// skill names and that may ask the database.
func (a *app) serveOutboxPage(w http.ResponseWriter, r *http.Request, site *pubSite, name, id string, before int64) {
	rows, err := a.db.QueryContext(r.Context(), profilePageSQL, name, a.mode == modeSingle, before, pubPage+1)
	if err != nil {
		pubFailed(w, "an outbox page", err)
		return
	}
	var posts []outboxRow
	var uname string
	found := false
	for rows.Next() {
		var retired bool
		var uid, pid sql.NullInt64
		var un, display, bio, head, tag, body, image sql.NullString
		var links, data []byte
		var created sql.NullTime
		if err := rows.Scan(&retired, &uid, &un, &display, &bio, &links, &head, &tag, &pid, &body, &image, &data, &created); err != nil {
			rows.Close()
			pubFailed(w, "an outbox page", err)
			return
		}
		if !uid.Valid {
			break
		}
		found, uname = true, un.String
		if pid.Valid {
			posts = append(posts, outboxRow{pid.Int64, body.String, image.String, data, created.Time})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		pubFailed(w, "an outbox page", err)
		return
	}
	if !found {
		site.notFound(w, r, true)
		return
	}
	if !a.originFrozen(w, r, site) {
		return
	}
	p := outPage{Context: noteContext, ID: id + "?page=true", Type: "OrderedCollectionPage", PartOf: id, OrderedItems: []outCreate{}}
	if before > 0 {
		p.ID += "&before=" + strconv.FormatInt(before, 10)
	}
	if len(posts) > pubPage {
		posts = posts[:pubPage]
		p.Next = id + "?page=true&before=" + strconv.FormatInt(posts[pubPage-1].id, 10)
	}
	for _, x := range posts {
		p.OrderedItems = append(p.OrderedItems, a.createOf(r.Context(), uname, x.id, x.body, x.head, x.data, x.created))
	}
	serveDoc(w, r, "an outbox page", p)
}

// publicFollowCountsSQL is how many follow member $1 and how many they follow, counting only the other
// sides who may be shown beyond this server themselves (publicMember, $2 as in the gate) and no follow
// across a block (followShown): followCountsSQL's two counts, narrowed to what is already public. The other
// side is looked up by its primary key once per follow (OFFSET 0 keeps the planner from joining users
// whole), so the cost is the member's follows, as for followCountsSQL.
var publicFollowCountsSQL = `SELECT (SELECT count(*) FROM follows f WHERE f.followee_id = $1 AND ` + followShown("f.follower_id") + `
		AND EXISTS (SELECT 1 FROM users u WHERE u.id = f.follower_id AND ` + publicMember + ` OFFSET 0)),
	(SELECT count(*) FROM follows f WHERE f.follower_id = $1 AND ` + followShown("f.followee_id") + `
		AND EXISTS (SELECT 1 FROM users u WHERE u.id = f.followee_id AND ` + publicMember + ` OFFSET 0))`

// serveFollows sends a member's followers or following collection, or their featured one.
//
// Followers and following (ActivityPub 5.3 and 5.4, which let either be filtered by who asks; F10): the
// count, and never who. F10 shows the members of each list only to the account itself, and nobody who asks
// at this address can be that account: a request here carries no session (nothing here reads one), and a
// signed request, once authorized fetch exists (7.6), is signed by another server's actor. A member reads
// their own lists in the app (GET /api/followers and /api/following), a different surface. So there is no
// first and no page, for anyone. The count says no more than the app does, and less: it is given only while
// the app shows counts at all (followCountsShown: the community and follows flags both on), and it counts
// only the follows whose other side is itself a member who may be shown beyond this server
// (publicFollowCountsSQL), since a follow by a member who never chose to federate is theirs to keep inside
// the app. Without it the collection has no totalItems, which says nothing. Remote followers do not exist
// yet (FWR 3). Whether a public count should include every local follower is F10's open question.
//
// Featured (Mastodon's pinned posts, 5.2): empty, since posts cannot be pinned; it is there because
// Mastodon fetches it for a profile, and an empty list is the true answer.
func (a *app) serveFollows(w http.ResponseWriter, r *http.Request, site *pubSite, name, id, kind string) {
	var uid sql.NullInt64
	var retired bool
	single := a.mode == modeSingle
	if err := a.db.QueryRowContext(r.Context(), actorGateSQL, name, single).Scan(&uid, &retired); err != nil {
		pubFailed(w, "a collection", err)
		return
	}
	if !uid.Valid {
		site.notFound(w, r, true)
		return
	}
	if !a.originFrozen(w, r, site) {
		return
	}
	doc := outCollection{Context: asContext, ID: id, Type: "OrderedCollection"}
	switch {
	case kind == "featured":
		none := 0
		doc.TotalItems, doc.OrderedItems = &none, &[]string{}
	case a.followCountsShown():
		var followers, following int
		if err := a.db.QueryRowContext(r.Context(), publicFollowCountsSQL, uid.Int64, single).Scan(&followers, &following); err != nil {
			pubFailed(w, "a collection", err)
			return
		}
		if kind == "following" {
			followers = following
		}
		doc.TotalItems = &followers
	}
	serveDoc(w, r, "a collection", doc)
}

// handleInstanceOutbox answers GET /actor/outbox: the instance actor's outbox, which ActivityPub 4.1 makes
// it name, and which is empty, since it posts nothing (5.5). Like the instance actor's document it freezes
// nothing and names no member.
func (a *app) handleInstanceOutbox(st *static) http.HandlerFunc {
	site := a.pubSiteOf(st)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Vary", "Accept")
		if !a.publicAllowed(w, r) {
			return
		}
		activity := wantsActivity(r.Header)
		if _, _, qOK := docQuery(r.URL.RawQuery, false); a.fed.refusal() != "" || r.URL.EscapedPath() != "/actor/outbox" || !qOK {
			site.notFound(w, r, activity)
			return
		}
		if !activity {
			seeOther(w, "/actor")
			return
		}
		none := 0
		serveDoc(w, r, "the instance actor's outbox", outCollection{Context: asContext, ID: a.fed.origin.url + "/actor/outbox", Type: "OrderedCollection",
			TotalItems: &none, OrderedItems: &[]string{}})
	}
}

// handlePublicPicture answers the two picture addresses (9.4): a member's profile picture
// (/users/{name}/avatar.{ext}) when post is false, or a Public post's picture
// (/users/{name}/posts/{id}/picture.{ext}). The address names the member, and the post, so it reaches
// only what the documents and the pages name: nothing private can be asked for through it, whatever id is
// tried, and a picture under the wrong extension or the wrong member is the same 404. The ?v= of the
// profile picture's address is a hash that changes with it; the picture is still kept only for
// three minutes, because one that is withdrawn (an opt-out, a hidden post) should leave caches soon.
func (a *app) handlePublicPicture(post bool, ext string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.publicAllowed(w, r) {
			return
		}
		rest := "/avatar." + ext
		var id int64
		idOK := true
		if post {
			id, idOK = canonicalID(r)
			rest = "/posts/" + r.PathValue("id") + "/picture." + ext
		}
		name, ok := canonicalName(r, rest)
		if a.fed.refusal() != "" || !ok || !idOK {
			pubNoPicture.serve(w, r, http.StatusNotFound, notFoundKeep)
			return
		}
		var stored string
		var err error
		if post {
			err = a.db.QueryRowContext(r.Context(), postPictureSQL, name, a.mode == modeSingle, id).Scan(&stored)
		} else {
			err = a.db.QueryRowContext(r.Context(), avatarSQL, name, a.mode == modeSingle).Scan(&stored)
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			pubFailed(w, "a public picture", err)
			return
		}
		got, mt, _, _, ok := pictureOf(stored[:min(len(stored), 64)])
		var raw []byte
		if ok && got == ext {
			raw, err = base64.StdEncoding.DecodeString(stored[strings.IndexByte(stored, ',')+1:])
		}
		if raw == nil || err != nil {
			pubNoPicture.serve(w, r, http.StatusNotFound, notFoundKeep)
			return
		}
		newAnswer(raw, mt).serve(w, r, http.StatusOK, apKeep)
	}
}

/* ---------- federation: HTTP signatures ----------
How a request between servers says who sent it (docs/federation.md 7.1 to 7.3). Two forms are read
and written: draft-cavage-http-signatures-12 ("cavage" below), which most of the fediverse sends and
Mastodon has always read, and RFC 9421, HTTP Message Signatures, in the one shape Mastodon accepts
since 4.5.0 (its security page, "HTTP Message Signatures (RFC9421)"). Three layers, kept apart so
each can be tested alone:

  - parsing and building, pure functions of text: the cavage Signature header, the Structured Fields
    (RFC 8941) that RFC 9421 and RFC 9530 use, read by a small strict parser for exactly what they
    need, the cavage signing string, the RFC 9421 signature base, Digest and Content-Digest;
  - the policy (sigVerifier.verify): what must be covered, the time window, the digest, replay;
  - adapters to an *http.Request that arrived (verifyRequest) and to the fetcher's signer hook
    (httpSigner).

No layer reads the database: the verifier asks a function for the public key of a key id, and that
function (a later item) fetches the key and checks its owner; the signer is handed the private key.
Nothing calls any of this yet: the inbox is FWR's, signed fetches are FRD's remote actors item. One
line is logged, once, by builtAt at the first verifier made, and only when the build does not know
its own date: it says the clock check is skipped. Nothing else here logs. */

const (
	sigPast       = 12 * time.Hour // Mastodon's window: "made within the past 12 hours" (security page, Verifying HTTP signatures)
	sigAhead      = time.Hour      // Flowa's own allowance for a sender whose clock runs ahead (federation.md 7.1)
	sigSeenMax    = 100_000        // signatures remembered at most; one sender group holds a tenth, 2,000 groups at most (federation.md 7.2)
	sigFieldMax   = 8 << 10        // bytes of Signature, Signature-Input, Digest or Content-Digest, its lines joined
	sigLinesMax   = 8              // lines of one of those fields
	sigCoveredMax = 32             // covered components (headers) of one signature
	sigParamsMax  = 16             // parameters of one item or inner list; members of one dictionary
	sigValueMax   = 16 << 10       // bytes of one covered header's value
	sigBaseMax    = 64 << 10       // bytes of a signing string or a signature base
	sigKeyMin     = 2048           // bits: the SWICG report asks for "a 2048-bit or larger RSA keypair"
	sigKeyMax     = 8192           // bits: a cost limit, since verifying grows with the key; larger is refused
	sigPEMMax     = 16 << 10       // bytes of a public key PEM
)

// sigForm is which of the two texts a signature follows.
type sigForm uint8

const (
	sigCavage  sigForm = iota + 1 // draft-cavage-http-signatures-12
	sigRFC9421                    // RFC 9421, as Mastodon accepts it
)

// sigReason is why a signature was refused. Each is a fixed phrase, safe to log: never a header
// value, a body, a signing string or a key.
type sigReason uint8

const (
	sigMissing          sigReason = iota + 1 // no Signature header
	sigMalformed                             // a signature field, or a value it covers, could not be read
	sigTooLarge                              // over one of the caps above
	sigBothDisagree                          // a cavage and an RFC 9421 signature name different keys
	sigMultiple                              // more than one signature of one form
	sigAlgorithm                             // an algorithm Flowa does not accept
	sigKeyID                                 // the key id is not an https URL
	sigNoRequestLine                         // the method, the path and the query are not all covered
	sigNoHost                                // cavage: host is not covered
	sigNoTime                                // neither date nor created is covered (RFC 9421: created is absent)
	sigNoDigest                              // a request with a body whose digest is not covered
	sigDigestMissing                         // the digest is covered but the header is absent
	sigDigestUnknown                         // the digest header names no algorithm Flowa computes
	sigDigestMismatch                        // the digest is not that of the body as received
	sigTooOld                                // signed more than 12 hours ago
	sigFuture                                // signed in the future, beyond what is allowed
	sigExpired                               // expires is in the past
	sigHeaderMissing                         // a covered header or component is not in the request
	sigHeaderTwice                           // Date is covered and sent twice
	sigComponentTwice                        // a component is named twice in the covered list
	sigComponentUnknown                      // a derived component or a parameter Flowa does not know
	sigBadSignature                          // the signature is not base64, or not as long as the key
	sigKeyUnavailable                        // the key could not be had (the caller's error is wrapped)
	sigKeyRefused                            // the key is under 2048 or over 8192 bits
	sigInvalid                               // the signature does not verify
	sigReplay                                // this signature was accepted before
	sigStale                                 // older than what the replay memory can still vouch for
	sigClock                                 // this server's clock is earlier than its build: it cannot judge times
	sigFull                                  // the replay memory holds as many sender groups as it can: try later (503)
)

var sigReasons = [...]string{
	sigMissing:          "no signature",
	sigMalformed:        "a malformed signature header",
	sigTooLarge:         "a signature header over its size limit",
	sigBothDisagree:     "a cavage and an RFC 9421 signature that name different keys",
	sigMultiple:         "more than one signature",
	sigAlgorithm:        "an algorithm that is not accepted",
	sigKeyID:            "a key id that is not an https URL",
	sigNoRequestLine:    "the request line is not signed",
	sigNoHost:           "the host is not signed",
	sigNoTime:           "neither date nor created is signed",
	sigNoDigest:         "the digest of the body is not signed",
	sigDigestMissing:    "the signed digest header is absent",
	sigDigestUnknown:    "a digest of no algorithm that is checked",
	sigDigestMismatch:   "the digest does not match the body",
	sigTooOld:           "signed more than 12 hours ago",
	sigFuture:           "signed in the future",
	sigExpired:          "the signature has expired",
	sigHeaderMissing:    "a signed header is missing from the request",
	sigHeaderTwice:      "a signed Date sent twice",
	sigComponentTwice:   "a component signed twice",
	sigComponentUnknown: "a component or parameter that is not known",
	sigBadSignature:     "a signature that is not base64 of the key's length",
	sigKeyUnavailable:   "the key could not be had",
	sigKeyRefused:       "a key of a size that is not accepted",
	sigInvalid:          "the signature does not verify",
	sigReplay:           "a signature used before",
	sigStale:            "a signature older than the replay memory reaches",
	sigClock:            "this server's clock is not set yet",
	sigFull:             "the replay memory is full for now",
}

func (r sigReason) String() string {
	if int(r) < len(sigReasons) && sigReasons[r] != "" {
		return sigReasons[r]
	}
	return "refused"
}

// sigError is every refusal the verifier returns. Its text is the reason's fixed phrase.
type sigError struct {
	Reason sigReason
	err    error // the key function's error, for errors.As by the caller; never printed
}

func (e *sigError) Error() string { return "signature refused: " + e.Reason.String() }
func (e *sigError) Unwrap() error { return e.err }

func sigFail(r sigReason) error { return &sigError{Reason: r} }

// sigMessage is what a signature covers, taken from a request: the method as sent, the scheme and
// authority (on receipt, the configured origin's, never the Host the proxy passed on: federation.md,
// finding 4), the request target in origin form exactly as on the wire (path and query, percent
// encoding untouched), and the header fields.
type sigMessage struct {
	method    string
	scheme    string
	authority string // host, and ":port" unless it is the scheme's default, lower case (RFC 9110, 4.2.3)
	target    string
	header    http.Header
}

// urlAuthority is a URL's authority as RFC 9421 2.2.3 defines @authority: the host in lower case,
// and the port only when it is not the default (RFC 9110, 4.2.3: "the default port is omitted").
func urlAuthority(u *url.URL) string {
	h := strings.ToLower(u.Hostname())
	if strings.Contains(h, ":") {
		h = "[" + h + "]"
	}
	if p := u.Port(); p != "" && p != map[string]string{"https": "443", "http": "80"}[strings.ToLower(u.Scheme)] {
		h += ":" + p
	}
	return h
}

// httpsURL says whether a key id is an https URL: the only kind of key id Flowa can look up, and
// what the SWICG report expects ("It should be a URL"). Only the characters RFC 3986 allows in a URL
// (2.2, 2.3: unreserved, reserved, and "%" followed by two hex digits, 2.1), within maxURL: so never
// a space, a quote or a backslash, and a key id always fits in a quoted string unchanged.
func httpsURL(s string) bool {
	if s == "" || len(s) > maxURL {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '%':
			if i+2 >= len(s) || !isHex(s[i+1]) || !isHex(s[i+2]) {
				return false
			}
		case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || isDigit(c) || strings.IndexByte("-._~:/?#[]@!$&'()*+,;=", c) >= 0:
		default:
			return false
		}
	}
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.Opaque == ""
}

// sameOrigin says whether a key id and an actor id share an origin, compared whole: scheme, host and
// port, the fragment ignored (federation.md 7.3). A key vouches for an actor only on its own origin;
// the caller also checks that the key document names the actor and the actor names the key.
func sameOrigin(keyID, actorID string) bool {
	if !httpsURL(keyID) || !httpsURL(actorID) {
		return false
	}
	a, _ := url.Parse(keyID)
	b, _ := url.Parse(actorID)
	return originOf(a) == originOf(b)
}

func isTchar(c byte) bool {
	// RFC 9110, 5.6.2: token characters.
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func allDigits(s string, max int) bool {
	if s == "" || len(s) > max {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isDigit(s[i]) {
			return false
		}
	}
	return true
}

// b64Decode reads base64 (RFC 4648, 4) as both texts use it: the standard alphabet, padding optional
// (RFC 8941, 4.2.7: parsers "SHOULD NOT fail when "=" padding is not present"), and nothing else,
// not even the line breaks Go's decoder skips (4.2.7: parsers "MUST fail on ... line feeds").
func b64Decode(s string) ([]byte, bool) {
	t := strings.TrimRight(s, "=")
	if len(s)-len(t) > 2 {
		return nil, false
	}
	for i := 0; i < len(t); i++ {
		c := t[i]
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || isDigit(c) || c == '+' || c == '/') {
			return nil, false
		}
	}
	b, err := base64.RawStdEncoding.DecodeString(t)
	return b, err == nil
}

/* Structured Fields (RFC 8941), only what Signature-Input, Signature and Content-Digest need: a
Dictionary whose members are Items or Inner Lists, with Parameters, of every bare item type. Strict
as 8941 1.1 asks: anything that does not parse refuses the whole field. */

type sfKind uint8

const (
	sfInteger sfKind = iota + 1
	sfDecimal
	sfString
	sfToken
	sfBytes
	sfBoolean
)

// sfBare is a bare item (RFC 8941, 3.3). A Decimal is kept in thousandths, its whole precision (3.3.2).
type sfBare struct {
	kind sfKind
	n    int64  // Integer; Decimal in thousandths; Boolean as 0 or 1
	s    string // String, Token
	b    []byte // Byte Sequence
}

type sfParam struct {
	key string
	val sfBare
}

type sfItem struct {
	val    sfBare
	params []sfParam
}

// sfMember is one member of a Dictionary (3.2): an Item, or an Inner List with its own parameters.
type sfMember struct {
	key    string
	inner  bool
	item   sfItem
	list   []sfItem
	params []sfParam // the Inner List's
}

var errSF = errors.New("not a structured field")

type sfParser struct {
	s string
	i int
}

func (p *sfParser) peek() byte {
	if p.i < len(p.s) {
		return p.s[p.i]
	}
	return 0
}

func (p *sfParser) ows() {
	for p.i < len(p.s) && (p.s[p.i] == ' ' || p.s[p.i] == '\t') {
		p.i++
	}
}

// sfDictionary parses a Dictionary (RFC 8941, 4.2 and 4.2.2) from a field's lines already joined with
// commas (4.2: parsers "MUST combine all field lines ... into one comma-separated field-value").
// Stricter than 8941 in what a field definition may add (2): a key given twice is refused rather than
// the last one kept (RFC 9421, 4.1: "The signature labels MUST be unique across all field values"; a
// digest algorithm named twice is as ambiguous), and at most sigParamsMax members, sigCoveredMax
// items in an inner list and sigParamsMax parameters on one (3.1.1, 3.1.2: field specifications "can
// constrain the ... cardinality").
func sfDictionary(v string) ([]sfMember, error) {
	for i := 0; i < len(v); i++ {
		if v[i] >= 0x80 {
			return nil, errSF // 4.2: the input is ASCII
		}
	}
	p := &sfParser{s: v}
	for p.peek() == ' ' {
		p.i++
	}
	var out []sfMember
	for p.i < len(p.s) {
		key, err := p.key()
		if err != nil {
			return nil, err
		}
		m := sfMember{key: key}
		if p.peek() == '=' {
			p.i++
			if p.peek() == '(' {
				m.inner = true
				if m.list, m.params, err = p.innerList(); err != nil {
					return nil, err
				}
			} else if m.item, err = p.item(); err != nil {
				return nil, err
			}
		} else {
			params, err := p.params()
			if err != nil {
				return nil, err
			}
			m.item = sfItem{val: sfBare{kind: sfBoolean, n: 1}, params: params}
		}
		for _, o := range out {
			if o.key == key {
				return nil, errSF
			}
		}
		if len(out) == sigParamsMax {
			return nil, errSF
		}
		out = append(out, m)
		p.ows()
		if p.i == len(p.s) {
			return out, nil
		}
		if p.s[p.i] != ',' {
			return nil, errSF
		}
		p.i++
		p.ows()
		if p.i == len(p.s) {
			return nil, errSF // a trailing comma
		}
	}
	return out, nil
}

// innerList is 4.2.1.2.
func (p *sfParser) innerList() ([]sfItem, []sfParam, error) {
	p.i++ // "("
	var list []sfItem
	for p.i < len(p.s) {
		for p.peek() == ' ' {
			p.i++
		}
		if p.peek() == ')' {
			p.i++
			params, err := p.params()
			return list, params, err
		}
		if len(list) == sigCoveredMax {
			return nil, nil, errSF
		}
		it, err := p.item()
		if err != nil {
			return nil, nil, err
		}
		list = append(list, it)
		if c := p.peek(); c != ' ' && c != ')' {
			return nil, nil, errSF
		}
	}
	return nil, nil, errSF
}

// item is 4.2.3.
func (p *sfParser) item() (sfItem, error) {
	b, err := p.bare()
	if err != nil {
		return sfItem{}, err
	}
	params, err := p.params()
	return sfItem{val: b, params: params}, err
}

// params is 4.2.3.2, refusing a key given twice where 8941 keeps the last: two values of created or
// keyid would leave the serialized line ambiguous.
func (p *sfParser) params() ([]sfParam, error) {
	var out []sfParam
	for p.peek() == ';' {
		p.i++
		for p.peek() == ' ' {
			p.i++
		}
		key, err := p.key()
		if err != nil {
			return nil, err
		}
		val := sfBare{kind: sfBoolean, n: 1}
		if p.peek() == '=' {
			p.i++
			if val, err = p.bare(); err != nil {
				return nil, err
			}
		}
		for _, o := range out {
			if o.key == key {
				return nil, errSF
			}
		}
		if len(out) == sigParamsMax {
			return nil, errSF
		}
		out = append(out, sfParam{key, val})
	}
	return out, nil
}

// key is 4.2.3.3.
func (p *sfParser) key() (string, error) {
	start := p.i
	if c := p.peek(); !(c >= 'a' && c <= 'z' || c == '*') {
		return "", errSF
	}
	for p.i < len(p.s) {
		c := p.s[p.i]
		if !(c >= 'a' && c <= 'z' || isDigit(c) || c == '_' || c == '-' || c == '.' || c == '*') {
			break
		}
		p.i++
	}
	return p.s[start:p.i], nil
}

// bare is 4.2.3.1.
func (p *sfParser) bare() (sfBare, error) {
	switch c := p.peek(); {
	case c == '-' || isDigit(c):
		return p.number()
	case c == '"':
		return p.str()
	case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '*':
		start := p.i // 4.2.6, a Token
		p.i++
		for p.i < len(p.s) && (isTchar(p.s[p.i]) || p.s[p.i] == ':' || p.s[p.i] == '/') {
			p.i++
		}
		return sfBare{kind: sfToken, s: p.s[start:p.i]}, nil
	case c == ':':
		end := strings.IndexByte(p.s[p.i+1:], ':') // 4.2.7, a Byte Sequence
		if end < 0 {
			return sfBare{}, errSF
		}
		b, ok := b64Decode(p.s[p.i+1 : p.i+1+end])
		if !ok {
			return sfBare{}, errSF
		}
		p.i += end + 2
		return sfBare{kind: sfBytes, b: b}, nil
	case c == '?':
		if p.i+1 < len(p.s) && (p.s[p.i+1] == '0' || p.s[p.i+1] == '1') { // 4.2.8, a Boolean
			v := int64(p.s[p.i+1] - '0')
			p.i += 2
			return sfBare{kind: sfBoolean, n: v}, nil
		}
	}
	return sfBare{}, errSF
}

// number is 4.2.4: an Integer of at most 15 digits, or a Decimal of at most 12 digits, a dot and 1 to
// 3 more.
func (p *sfParser) number() (sfBare, error) {
	neg := p.peek() == '-'
	if neg {
		p.i++
	}
	start := p.i
	if !isDigit(p.peek()) {
		return sfBare{}, errSF
	}
	dot := -1
	for p.i < len(p.s) {
		c := p.s[p.i]
		if isDigit(c) {
			p.i++
		} else if c == '.' && dot < 0 {
			if p.i-start > 12 {
				return sfBare{}, errSF
			}
			dot = p.i
			p.i++
		} else {
			break
		}
	}
	num := p.s[start:p.i]
	sign := int64(1)
	if neg {
		sign = -1
	}
	if dot < 0 {
		if len(num) > 15 {
			return sfBare{}, errSF
		}
		n, _ := strconv.ParseInt(num, 10, 64)
		return sfBare{kind: sfInteger, n: sign * n}, nil
	}
	whole, frac := p.s[start:dot], p.s[dot+1:p.i]
	if len(num) > 16 || frac == "" || len(frac) > 3 {
		return sfBare{}, errSF
	}
	w, _ := strconv.ParseInt(whole, 10, 64)
	f, _ := strconv.ParseInt((frac + "00")[:3], 10, 64)
	return sfBare{kind: sfDecimal, n: sign * (w*1000 + f)}, nil
}

// str is 4.2.5.
func (p *sfParser) str() (sfBare, error) {
	p.i++ // the opening quote
	var b strings.Builder
	for p.i < len(p.s) {
		c := p.s[p.i]
		p.i++
		switch {
		case c == '\\':
			if p.i == len(p.s) || (p.s[p.i] != '"' && p.s[p.i] != '\\') {
				return sfBare{}, errSF
			}
			b.WriteByte(p.s[p.i])
			p.i++
		case c == '"':
			return sfBare{kind: sfString, s: b.String()}, nil
		case c < 0x20 || c >= 0x7f:
			return sfBare{}, errSF
		default:
			b.WriteByte(c)
		}
	}
	return sfBare{}, errSF
}

// sfWriteBare serializes a bare item (RFC 8941, 4.1.3 to 4.1.9).
func sfWriteBare(b *strings.Builder, v sfBare) {
	switch v.kind {
	case sfInteger:
		b.WriteString(strconv.FormatInt(v.n, 10))
	case sfDecimal:
		n := v.n
		if n < 0 {
			b.WriteByte('-')
			n = -n
		}
		frac := strings.TrimRight(fmt.Sprintf("%03d", n%1000), "0") // 4.1.5: no trailing zeros, at least one digit
		if frac == "" {
			frac = "0"
		}
		b.WriteString(strconv.FormatInt(n/1000, 10) + "." + frac)
	case sfString:
		b.WriteByte('"')
		for i := 0; i < len(v.s); i++ {
			if v.s[i] == '"' || v.s[i] == '\\' {
				b.WriteByte('\\')
			}
			b.WriteByte(v.s[i])
		}
		b.WriteByte('"')
	case sfToken:
		b.WriteString(v.s)
	case sfBytes:
		b.WriteString(":" + base64.StdEncoding.EncodeToString(v.b) + ":")
	case sfBoolean:
		b.WriteString(map[bool]string{true: "?1", false: "?0"}[v.n == 1])
	}
}

// sfWriteParams is 4.1.1.2: a true Boolean is written as the key alone.
func sfWriteParams(b *strings.Builder, params []sfParam) {
	for _, p := range params {
		b.WriteString(";" + p.key)
		if p.val.kind != sfBoolean || p.val.n != 1 {
			b.WriteByte('=')
			sfWriteBare(b, p.val)
		}
	}
}

// sfInnerList serializes an Inner List with its parameters (4.1.1.1), in the order given.
func sfInnerList(list []sfItem, params []sfParam) string {
	var b strings.Builder
	b.WriteByte('(')
	for i, it := range list {
		if i > 0 {
			b.WriteByte(' ')
		}
		sfWriteBare(&b, it.val)
		sfWriteParams(&b, it.params)
	}
	b.WriteByte(')')
	sfWriteParams(&b, params)
	return b.String()
}

/* draft-cavage-http-signatures-12 */

// cavageSig is a parsed cavage Signature header.
type cavageSig struct {
	keyID, algorithm, signature string
	headers                     []string // lower case, in order; ["(created)"] when the parameter is absent (2.1.6)
	created, expires            string   // as written; "" when absent
}

// parseCavage reads a Signature header (draft-cavage-12, 4.1: "one or more auth-params" of RFC 7235,
// 4.1, each `token BWS "=" BWS ( token / quoted-string )`, separated by commas with optional
// whitespace). Parameter names are matched in any letter case (RFC 7235, 2.1: "matched
// case-insensitively"); a known parameter given twice refuses the header and an unknown one is
// skipped (cavage 2.2); keyId and signature are required (2.1.1, 2.1.2); a missing headers parameter
// means `(created)` (2.1.6), which the policy then refuses for not covering the request line; an
// empty one is refused (2.1.6, note 2). created must be an integer (2.1.4); expires may have a
// fraction (2.1.5).
func parseCavage(v string) (*cavageSig, error) {
	if len(v) > sigFieldMax {
		return nil, sigFail(sigTooLarge)
	}
	bad := sigFail(sigMalformed)
	sig := &cavageSig{}
	var seen [6]bool
	known := []string{"keyid", "signature", "algorithm", "created", "expires", "headers"}
	headers := ""
	i, n := 0, 0
	for {
		// Whitespace and empty list elements: RFC 9110 5.6.1.2, a recipient "MUST parse and ignore a
		// reasonable number of empty list elements".
		for i < len(v) && (v[i] == ' ' || v[i] == '\t' || v[i] == ',') {
			i++
		}
		if i == len(v) {
			break
		}
		if n++; n > sigParamsMax {
			return nil, bad
		}
		j := i
		for j < len(v) && isTchar(v[j]) {
			j++
		}
		if j == i {
			return nil, bad
		}
		name := strings.ToLower(v[i:j])
		for i = j; i < len(v) && (v[i] == ' ' || v[i] == '\t'); i++ {
		}
		if i == len(v) || v[i] != '=' {
			return nil, bad
		}
		for i++; i < len(v) && (v[i] == ' ' || v[i] == '\t'); i++ {
		}
		var val string
		if i < len(v) && v[i] == '"' {
			var ok bool
			if val, i, ok = quotedString(v, i); !ok {
				return nil, bad
			}
		} else {
			for j = i; j < len(v) && isTchar(v[j]); j++ {
			}
			if j == i {
				return nil, bad
			}
			val, i = v[i:j], j
		}
		for i < len(v) && (v[i] == ' ' || v[i] == '\t') {
			i++
		}
		if i < len(v) && v[i] != ',' {
			return nil, bad
		}
		k := slices.Index(known, name)
		if k < 0 {
			continue
		}
		if seen[k] {
			return nil, bad
		}
		seen[k] = true
		switch name {
		case "keyid":
			sig.keyID = val
		case "signature":
			sig.signature = val
		case "algorithm":
			sig.algorithm = val
		case "created":
			sig.created = val
		case "expires":
			sig.expires = val
		case "headers":
			headers = val
		}
	}
	if !seen[0] || !seen[1] {
		return nil, bad
	}
	if seen[5] {
		for _, h := range strings.Fields(headers) {
			if len(sig.headers) == sigCoveredMax {
				return nil, bad
			}
			sig.headers = append(sig.headers, strings.ToLower(h))
		}
		if len(sig.headers) == 0 {
			return nil, bad
		}
	} else {
		sig.headers = []string{"(created)"}
	}
	if seen[3] && !allDigits(sig.created, 15) {
		return nil, bad
	}
	if seen[4] {
		whole, frac, dot := strings.Cut(sig.expires, ".")
		if !allDigits(whole, 15) || dot && !allDigits(frac, 9) {
			return nil, bad
		}
	}
	return sig, nil
}

// quotedString reads a quoted-string (RFC 9110, 5.6.4) starting at v[i], and returns its value and
// the index after it. Only printable ASCII, a space or a tab inside.
func quotedString(v string, i int) (string, int, bool) {
	var b strings.Builder
	for i++; i < len(v); i++ {
		c := v[i]
		switch {
		case c == '"':
			return b.String(), i + 1, true
		case c == '\\':
			if i+1 == len(v) || (v[i+1] < 0x20 && v[i+1] != '\t') || v[i+1] >= 0x7f {
				return "", 0, false
			}
			i++
			b.WriteByte(v[i])
		case c < 0x20 && c != '\t' || c >= 0x7f:
			return "", 0, false
		default:
			b.WriteByte(c)
		}
	}
	return "", 0, false
}

// validFieldName says whether a covered header name is a token in lower case (RFC 9110, 5.1; RFC
// 9421, 2.1: "implementations MUST use lowercased field names").
func validFieldName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isTchar(s[i]) || s[i] >= 'A' && s[i] <= 'Z' {
			return false
		}
	}
	return true
}

// fieldValue is a header's value as both texts cover it: each line's value with its leading and
// trailing whitespace removed, the lines joined by ", " in the order received (draft-cavage-12, 2.3,
// rule 4.1: "concatenated, separated by a ASCII comma and an ASCII space"; RFC 9421, 2.1: "a single
// comma and a single space"). host is the authority of the message, which on receipt is the
// configured origin's (federation.md, finding 4). Go's HTTP/1.1 reader has already replaced
// obsolete line folding with a space, which RFC 9112 5.2 lets a recipient do and which both texts'
// examples show; a value that still holds a CR, LF or NUL cannot be a received field and is refused.
// For RFC 9421 (ascii) a value must also be printable ASCII (2.5: "Produce an error if the output
// string contains any non-ASCII characters").
func fieldValue(m *sigMessage, name string, ascii bool) (string, error) {
	if name == "host" {
		return m.authority, nil
	}
	lines := m.header.Values(name)
	if len(lines) == 0 {
		return "", sigFail(sigHeaderMissing)
	}
	var b strings.Builder
	for k, l := range lines {
		l = strings.Trim(l, " \t")
		for i := 0; i < len(l); i++ {
			c := l[i]
			if c == '\r' || c == '\n' || c == 0 || ascii && (c < 0x20 && c != '\t' || c >= 0x7f) {
				return "", sigFail(sigMalformed)
			}
		}
		if k > 0 {
			b.WriteString(", ")
		}
		b.WriteString(l)
		if b.Len() > sigValueMax {
			return "", sigFail(sigTooLarge)
		}
	}
	return b.String(), nil
}

// rsaFamily says whether a cavage algorithm name starts with rsa, hmac or ecdsa, with which (created)
// and (expires) "MUST produce an error" (2.3, rules 2 and 3).
func rsaFamily(alg string) bool {
	return strings.HasPrefix(alg, "rsa") || strings.HasPrefix(alg, "hmac") || strings.HasPrefix(alg, "ecdsa")
}

// cavageString builds the signing string (draft-cavage-12, 2.3): for each entry of headers, in its
// order, the lower-case name, ": " and the value, joined by "\n" with none at the end.
// (request-target) is the lower-case method, a space and the path with its query as on the wire
// (rule 1; lower case "only applies to the :method"). A name given twice is refused (the draft is
// silent; RFC 9421 2.5 step 2.1 makes it an error, and no signer needs it), as is an unknown
// "(...)" name. A missing header refuses the string (rule 4.4).
func cavageString(sig *cavageSig, m *sigMessage) (string, error) {
	var b strings.Builder
	for k, name := range sig.headers {
		if slices.Contains(sig.headers[:k], name) {
			return "", sigFail(sigComponentTwice)
		}
		var val string
		switch name {
		case "(request-target)":
			val = strings.ToLower(m.method) + " " + m.target
		case "(created)", "(expires)":
			val = sig.created
			if name == "(expires)" {
				val = sig.expires // the draft's rule 3 says "created" here: a slip, since the line is (expires)
			}
			if rsaFamily(sig.algorithm) || !allDigits(val, 15) {
				return "", sigFail(sigMalformed)
			}
		default:
			if strings.HasPrefix(name, "(") {
				return "", sigFail(sigComponentUnknown)
			}
			if !validFieldName(name) {
				return "", sigFail(sigMalformed)
			}
			var err error
			if val, err = fieldValue(m, name, false); err != nil {
				return "", err
			}
		}
		if k > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(name + ": " + val)
		if b.Len() > sigBaseMax {
			return "", sigFail(sigTooLarge)
		}
	}
	return b.String(), nil
}

/* RFC 9421 */

// sigInput is one parsed member of Signature-Input (RFC 9421, 4.1): the covered components and the
// signature parameters, both in the order received.
type sigInput struct {
	label              string
	covered            []sfItem
	params             []sfParam
	keyID, alg         string
	hasAlg, hasCreated bool
	created, expires   int64
	hasExpires         bool
}

// parseSignatureInput reads Signature-Input (RFC 9421, 4.1): a Dictionary whose members are Inner
// Lists of Strings (2.3: "an ordered Inner List of String values") with the parameters of 2.3, each
// of its type: created and expires Integers, keyid, alg, nonce and tag Strings. Other parameters are
// kept as received, so they stay in the signature parameters line, and otherwise ignored.
func parseSignatureInput(v string) ([]sigInput, error) {
	if len(v) > sigFieldMax {
		return nil, sigFail(sigTooLarge)
	}
	d, err := sfDictionary(v)
	if err != nil {
		return nil, sigFail(sigMalformed)
	}
	out := make([]sigInput, 0, len(d))
	for _, m := range d {
		if !m.inner {
			return nil, sigFail(sigMalformed)
		}
		in := sigInput{label: m.key, covered: m.list, params: m.params}
		for _, c := range m.list {
			if c.val.kind != sfString {
				return nil, sigFail(sigMalformed)
			}
		}
		for _, p := range m.params {
			want := sfString
			if p.key == "created" || p.key == "expires" {
				want = sfInteger
			}
			switch p.key {
			case "created", "expires", "keyid", "alg", "nonce", "tag":
				if p.val.kind != want {
					return nil, sigFail(sigMalformed)
				}
			}
			switch p.key {
			case "created":
				in.created, in.hasCreated = p.val.n, true
			case "expires":
				in.expires, in.hasExpires = p.val.n, true
			case "keyid":
				in.keyID = p.val.s
			case "alg":
				in.alg, in.hasAlg = p.val.s, true
			}
		}
		out = append(out, in)
	}
	return out, nil
}

// parseSignatureField reads Signature (RFC 9421, 4.2): a Dictionary whose members are Byte Sequences.
func parseSignatureField(v string) (labels []string, sigs [][]byte, err error) {
	if len(v) > sigFieldMax {
		return nil, nil, sigFail(sigTooLarge)
	}
	d, err := sfDictionary(v)
	if err != nil {
		return nil, nil, sigFail(sigMalformed)
	}
	for _, m := range d {
		if m.inner || m.item.val.kind != sfBytes {
			return nil, nil, sigFail(sigMalformed)
		}
		labels = append(labels, m.key)
		sigs = append(sigs, m.item.val.b)
	}
	return labels, sigs, nil
}

// sameComponent says whether two component identifiers are the same: the same name and the same
// parameters in any order (RFC 9421, 2: "the order of parameters is not significant when comparing
// two component identifiers for equality").
func sameComponent(a, b sfItem) bool {
	if a.val.s != b.val.s || len(a.params) != len(b.params) {
		return false
	}
	for _, p := range a.params {
		if !slices.ContainsFunc(b.params, func(q sfParam) bool { return q.key == p.key && sameBare(p.val, q.val) }) {
			return false
		}
	}
	return true
}

func sameBare(a, b sfBare) bool {
	return a.kind == b.kind && a.n == b.n && a.s == b.s && bytes.Equal(a.b, b.b)
}

// printable says whether a derived value holds only printable characters and spaces and neither
// starts nor ends with a space (RFC 9421, 2.2).
func printable(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] >= 0x7f {
			return false
		}
	}
	return s == strings.Trim(s, " ")
}

// derivedValue is the value of a derived component of a request (RFC 9421, 2.2). @target-uri is the
// scheme, the authority and the request target as on the wire (2.2.2, the target URI of RFC 9110
// 7.1, rebuilt from origin form); @path is the path before any "?", "/" when empty (2.2.6), percent
// encoding untouched; @query the query with its "?", or "?" alone when there is none (2.2.7). Only
// @query-param takes a parameter, name (2.2.8). @status is for responses only (2.2.9: "MUST NOT be
// used in a request message") and @signature-params is never covered (2.3); those, and any name not
// in 2.2, are refused (2.5: "If the derived component name is unknown ... produce an error").
func derivedValue(c sfItem, m *sigMessage) (string, error) {
	name := c.val.s
	if name != "@query-param" && len(c.params) > 0 {
		return "", sigFail(sigComponentUnknown)
	}
	path, query, _ := strings.Cut(m.target, "?")
	var v string
	switch name {
	case "@method":
		v = m.method
	case "@target-uri":
		v = m.scheme + "://" + m.authority + m.target
	case "@authority":
		v = m.authority
	case "@scheme":
		v = m.scheme
	case "@request-target":
		v = m.target
	case "@path":
		v = path
		if v == "" {
			v = "/"
		}
	case "@query":
		v = "?" + query
	case "@query-param":
		if len(c.params) != 1 || c.params[0].key != "name" || c.params[0].val.kind != sfString {
			return "", sigFail(sigComponentUnknown)
		}
		found := 0
		for _, kv := range queryParams(query) {
			if kv[0] == c.params[0].val.s {
				v = kv[1]
				found++
			}
		}
		switch {
		case found == 0:
			return "", sigFail(sigHeaderMissing) // 2.2.8: "MUST cause an error"
		case found > 1:
			return "", sigFail(sigMalformed) // 2.2.8: a name that occurs more than once "MUST NOT be included"
		}
	default:
		return "", sigFail(sigComponentUnknown)
	}
	if !printable(v) {
		return "", sigFail(sigMalformed)
	}
	return v, nil
}

// queryParams parses a query as application/x-www-form-urlencoded (WHATWG URL, 5.1, as RFC 9421
// 2.2.8 asks) and returns each name and value encoded again as the RFC's examples show: every byte
// but ASCII letters, digits and "*-._" percent-encoded, a space as %20 ("with+plus+whitespace"
// becomes "with%20plus%20whitespace" in 2.2.8, where the WHATWG serializer would write "+").
func queryParams(q string) [][2]string {
	var out [][2]string
	for _, part := range strings.Split(q, "&") {
		if part == "" {
			continue
		}
		n, v, _ := strings.Cut(part, "=")
		out = append(out, [2]string{formEncode(formDecode(n)), formEncode(formDecode(v))})
	}
	return out
}

func formDecode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '+' {
			c = ' '
		} else if c == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]) {
			x, _ := strconv.ParseUint(s[i+1:i+3], 16, 8)
			c = byte(x)
			i += 2
		}
		b.WriteByte(c)
	}
	return b.String()
}

func isHex(c byte) bool { return isDigit(c) || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' }

func formEncode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || isDigit(c) || c == '*' || c == '-' || c == '.' || c == '_' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// rfc9421Base builds the signature base (RFC 9421, 2.5): one line per covered component, in the
// order received, `"name"`, its parameters, ": " and the value, each ending in "\n", then the
// `"@signature-params": ` line, with no newline at the end. The parameters line is the Inner List
// serialized from what was received, components and parameters in the order received and of the
// types received (3.2, step 7: "the value of the Signature-Input field for this signature serialized
// according to the rules described in Section 2.3"; 4.1: "list order and parameter order have to be
// preserved"), never rebuilt in an order of Flowa's. A component named twice (2.5, step 2.1), a
// parameter on a header Flowa does not understand (2.5, step 2.5: sf, key, bs, req and tr are not
// used by any server Flowa talks to), and a missing header (2.5: "If the field cannot be found ...
// produce an error") are refused.
func rfc9421Base(in *sigInput, m *sigMessage) (string, error) {
	var b strings.Builder
	for i, c := range in.covered {
		for _, d := range in.covered[:i] {
			if sameComponent(c, d) {
				return "", sigFail(sigComponentTwice)
			}
		}
		name := c.val.s
		var val string
		var err error
		if strings.HasPrefix(name, "@") {
			val, err = derivedValue(c, m)
		} else if len(c.params) > 0 {
			err = sigFail(sigComponentUnknown)
		} else if !validFieldName(name) {
			err = sigFail(sigMalformed)
		} else {
			val, err = fieldValue(m, name, true)
		}
		if err != nil {
			return "", err
		}
		sfWriteBare(&b, c.val)
		sfWriteParams(&b, c.params)
		b.WriteString(": " + val + "\n")
		if b.Len() > sigBaseMax {
			return "", sigFail(sigTooLarge)
		}
	}
	b.WriteString(`"@signature-params": ` + sfInnerList(in.covered, in.params))
	return b.String(), nil
}

/* Digests */

// digestHeader is the Digest header Flowa sends with a cavage POST: "SHA-256=" and the base64 of the
// body's SHA-256, as draft-cavage-12 3.1 and Appendix C write it (RFC 3230, 4.3.2).
func digestHeader(body []byte) string {
	h := sha256.Sum256(body)
	return "SHA-256=" + base64.StdEncoding.EncodeToString(h[:])
}

// contentDigestHeader is the Content-Digest Flowa sends with an RFC 9421 POST: a Dictionary whose key
// is the algorithm and whose value is a Byte Sequence (RFC 9530, 2).
func contentDigestHeader(body []byte) string {
	h := sha256.Sum256(body)
	return "sha-256=:" + base64.StdEncoding.EncodeToString(h[:]) + ":"
}

// parseDigest reads Digest (RFC 3230, 4.3.2: `#(instance-digest)`; 4.1.1: "All digest-algorithm
// values are case-insensitive") and returns the SHA-256 and SHA-512 values it carries. Others are
// ignored (4.3.2: "A recipient MAY ignore any or all of the instance-digests"); either named twice
// is refused.
func parseDigest(v string) (s256, s512 []byte, err error) {
	if len(v) > sigFieldMax {
		return nil, nil, sigFail(sigTooLarge)
	}
	for _, part := range strings.Split(v, ",") {
		part = strings.Trim(part, " \t")
		if part == "" {
			continue
		}
		alg, val, ok := strings.Cut(part, "=")
		if !ok || alg == "" {
			return nil, nil, sigFail(sigMalformed)
		}
		var dst *[]byte
		size := 0
		switch strings.ToLower(alg) {
		case "sha-256":
			dst, size = &s256, sha256.Size
		case "sha-512":
			dst, size = &s512, sha512.Size
		default:
			continue
		}
		b, ok := b64Decode(val)
		if *dst != nil || !ok || len(b) != size {
			return nil, nil, sigFail(sigMalformed)
		}
		*dst = b
	}
	return s256, s512, nil
}

// parseContentDigest reads Content-Digest (RFC 9530, 2) and returns its sha-256 and sha-512 values.
// Other keys are ignored (2: "A recipient MAY ignore any or all digests"; RFC 8941, 3.2: unknown
// members are ignored), including the Deprecated ones of RFC 9530 5, which "MUST NOT be used in a
// potentially adversarial setting".
func parseContentDigest(v string) (s256, s512 []byte, err error) {
	if len(v) > sigFieldMax {
		return nil, nil, sigFail(sigTooLarge)
	}
	d, err := sfDictionary(v)
	if err != nil {
		return nil, nil, sigFail(sigMalformed)
	}
	for _, m := range d {
		size := map[string]int{"sha-256": sha256.Size, "sha-512": sha512.Size}[m.key]
		if size == 0 {
			continue
		}
		if m.inner || m.item.val.kind != sfBytes || len(m.item.val.b) != size {
			return nil, nil, sigFail(sigMalformed)
		}
		if m.key == "sha-256" {
			s256 = m.item.val.b
		} else {
			s512 = m.item.val.b
		}
	}
	return s256, s512, nil
}

// digestMatches checks every digest Flowa computes that the header carries against the body, in
// constant time: at least one must be there, and all that are there must match. Checking only one
// when both are sent would leave the downgrade RFC 9530 6.6 and 6.7 warn of ("skipping validation
// ... of a more-preferred algorithm"); the second costs one hash of a body already in memory.
func digestMatches(body, s256, s512 []byte) sigReason {
	if s256 == nil && s512 == nil {
		return sigDigestUnknown
	}
	if s256 != nil {
		if h := sha256.Sum256(body); subtle.ConstantTimeCompare(h[:], s256) != 1 {
			return sigDigestMismatch
		}
	}
	if s512 != nil {
		if h := sha512.Sum512(body); subtle.ConstantTimeCompare(h[:], s512) != 1 {
			return sigDigestMismatch
		}
	}
	return 0
}

/* Public keys */

// pubKeyError says why a public key PEM was refused. Its text is fixed, safe to log.
type pubKeyError struct{ Reason string }

func (e *pubKeyError) Error() string { return "public key refused: " + e.Reason }

// parsePublicKeyPEM reads the publicKeyPem of an actor: one PEM block, either SPKI ("PUBLIC KEY") or
// PKCS#1 ("RSA PUBLIC KEY"), since the SWICG report says it "may be encoded as PKCS-1, X.509 SPKI,
// or something else". Refused: anything before or after the block, a second block, headers in it, a
// key that is not RSA, an RSA key under minBits or over sigKeyMax, and one with an exponent under 3,
// an even exponent or an even modulus. minBits is sigKeyMin in
// production; the vector tests pass 1024 for the draft's old test key. An Ed25519 key is refused
// with its own reason: Mastodon validates Ed25519 only from 4.7.0, not released when this was
// written; accepting it would be a case here and a second verification algorithm chosen by the key.
// Caching parsed keys is the caller's (7.4).
func parsePublicKeyPEM(s string, minBits int) (*rsa.PublicKey, error) {
	if len(s) > sigPEMMax {
		return nil, &pubKeyError{"longer than a public key"}
	}
	rest := strings.TrimLeft(s, " \t\r\n")
	if !strings.HasPrefix(rest, "-----BEGIN ") {
		return nil, &pubKeyError{"not a PEM block"}
	}
	block, after := pem.Decode([]byte(rest))
	switch {
	case block == nil:
		return nil, &pubKeyError{"not a PEM block"}
	case len(block.Headers) > 0:
		return nil, &pubKeyError{"a PEM block with headers"}
	case bytes.Contains(after, []byte("-----BEGIN")):
		return nil, &pubKeyError{"more than one PEM block"}
	case len(bytes.TrimSpace(after)) > 0:
		return nil, &pubKeyError{"data after the PEM block"}
	}
	var pub any
	var err error
	switch block.Type {
	case "PUBLIC KEY":
		pub, err = x509.ParsePKIXPublicKey(block.Bytes)
	case "RSA PUBLIC KEY":
		pub, err = x509.ParsePKCS1PublicKey(block.Bytes)
	default:
		return nil, &pubKeyError{"a PEM block that is not a public key"}
	}
	if err != nil {
		return nil, &pubKeyError{"not a key"}
	}
	k, ok := pub.(*rsa.PublicKey)
	_, ed := pub.(ed25519.PublicKey)
	switch {
	case ed:
		return nil, &pubKeyError{"an Ed25519 key, which is not accepted yet"}
	case !ok:
		return nil, &pubKeyError{"not an RSA key"}
	case k.N.BitLen() < minBits:
		return nil, &pubKeyError{"an RSA key under the minimum size"}
	case k.N.BitLen() > sigKeyMax:
		return nil, &pubKeyError{"an RSA key over 8192 bits"}
	case k.E < 3 || k.E%2 == 0 || k.N.Bit(0) == 0:
		// No RSA key has these (an exponent must be odd and over 1, a modulus the product of two odd
		// primes); verifying refuses them anyway, and refusing here keeps them out of a key cache.
		return nil, &pubKeyError{"not a key"}
	}
	return k, nil
}

/* The replay memory */

// replayKey is the first 128 bits of the SHA-256 of a signature's decoded bytes: the same signature
// written in another base64 (padding left out, other pad bits) is the same key. Half the hash halves
// the memory, and is still far beyond reach: only signatures that verified are remembered, so a
// stranger cannot fill the memory with chosen values, and making a valid signature collide with
// another remembered one, or with a future one, would take about 2^64 valid signatures or a 2^128
// search.
type replayKey [16]byte

func replayKeyOf(sig []byte) replayKey {
	h := sha256.Sum256(sig)
	return replayKey(h[:16])
}

// senderGroup is the group a key id's signatures share in the replay memory: the last two labels of
// its host, in lower case, scheme and port ignored, so a.b.evil.example:8443 and evil.example are
// both evil.example. It returns "" for a host it cannot group (a single label, an empty label). The
// verifier refuses before this any key id whose host the fetcher would refuse (fedHost: IP addresses,
// numeric names, private names, one label), since its key could never be fetched; an IP literal given
// here anyway is a group of its own.
//
// It is a rough stand-in for the registrable domain, and safe: a floor kept per group refuses at
// least everything a floor per origin would, so no replay is let through. Its cost is fairness under
// pressure only. Servers that share their last two labels (every server under co.uk, or every
// customer of a host that gives out subdomains) share one group's share of the memory and one floor,
// so a flood from one of them can cost its group-mates a delivery dated at or before the group's floor,
// which the sender retries with a fresh date. That is far better than grouping by origin, where one
// wildcard DNS name could fill the 2,000 groups and have every server not yet seen refused for as
// long as it kept the flood up. The list of public suffixes would draw the line better; it is not taken as a dependency.
func senderGroup(keyID string) string {
	u, err := url.Parse(keyID)
	if err != nil {
		return ""
	}
	h := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if _, err := netip.ParseAddr(h); err == nil {
		return h
	}
	labels := strings.Split(h, ".")
	if len(labels) < 2 || slices.Contains(labels, "") {
		return ""
	}
	return labels[len(labels)-2] + "." + labels[len(labels)-1]
}

// replaySig is one signature for the replay memory: its key and the earliest time it vouches for, in
// Unix seconds. The same signature always vouches for the same time.
type replaySig struct {
	key replayKey
	at  int64
}

// replaySet remembers the signatures accepted on requests that change something, so that each is
// accepted once (federation.md 7.2). Each entry belongs to the sender group of its key id
// (senderGroup: the last two labels of its host), and every rule below is per group.
//
// What it promises, exactly: a signature is never accepted twice under the same sender group while
// the time window would take it. Across groups it promises nothing more, and for the draft form that
// matters. A cavage signature does not cover its keyId, so once it has been evicted from its group,
// the same bytes under a key id of another domain that serves a copy of the sender's public key
// (public keys are public) pass the memory and verify. That delivery is refused by the origin rule of
// 7.3, which the inbox applies to every delivery: the body, bound by the digest, still names the
// first sender's actor, and the key id is not on that actor's origin. So the origin rule is part of
// replay safety for the draft form, not only of authorship (TestReplayAcrossDomains). An RFC 9421
// signature covers keyid in its base, so under another key id it does not verify, and the memory
// alone suffices. No grouping closes this in the memory: keyed by the key's fingerprint, whoever
// claims a fingerprint first would own its floor; one floor for all is the blocker this replaced.
//
// An entry lives as long as the time window could still accept its signature: until 12 hours (past)
// after the time it vouches for, through the last second, which is exactly when inWindow starts to
// refuse it as too old. A signature dated an hour ahead is kept 13 hours, one dated 12 hours ago is
// barely kept at all. The memory keeps its own clock, the latest time it was given, and refuses as
// stale anything that clock says is past its 12 hours, so a system clock stepped back by up to an
// hour cannot bring a forgotten signature back into the window. A step back of more than an hour is
// a corrected clock, and the memory forgets everything as a restart does (advance).
//
// The memory is bounded and the number of valid signatures in a window is not, so sometimes a valid
// new signature has to be refused (a replay under the same group never is). The rule decides who that
// is: the group that filled the memory, never a bystander.
//
//   - An entry is only ever dropped by expiry or by eviction. An evicted entry is its group's
//     smallest time, and that group's floor rises to it: from then on a new signature from that
//     group vouching for a time at or before its floor is refused as stale, since it might be the
//     one dropped. One group's floor never touches another group. A signature from the same group
//     with exactly the floor's time is refused too: by time alone it cannot be told from the dropped
//     one. A floor is forgotten when the window would refuse its time anyway.
//   - One group holds at most perGroup entries (a tenth of the memory). At that cap it evicts its
//     own oldest, whatever dates it claims: a flood from one group costs only that group.
//   - When the whole memory is full, the entry evicted is the oldest of the group holding the most
//     entries, or the inserting group's own when it already holds at least an equal share (the
//     memory divided by the groups in it). The heaviest pays because it is the one using more than
//     its share: a group at or below an equal share is never evicted from by another's traffic, so
//     an attacker gains nothing by spreading a flood unless it brings more groups than there are
//     honest ones, and every honest group keeps at least an equal share of its newest entries.
//   - At most maxGroups groups are remembered (one in fifty of the memory). A signature from a new
//     group when that many are kept is refused as sigFull, which the inbox answers with 503 and
//     Retry-After: no group is forgotten while it might still have a floor or an entry to keep.
//
// The worst case, a flood of valid signatures from very many groups (each needs a domain of its own
// that the domain policy lets in, since subdomains and ports share their domain's group, a key that
// verifies and can be fetched, and each host is held to the inbox's allowance): an honest group
// already in the memory keeps at least an equal share of its newest entries, 50 at the full 2,000
// groups, and loses only a delivery dated no later than the ones evicted, that is, one overtaken by
// that many of its own later deliveries. An honest group not yet in the memory gets 503 for as long
// as the flood is kept up: holding all 2,000 groups takes 2,000 registered names with fetchable keys,
// each sending a signed request at least every 13 hours (each entry, dated an hour ahead, lasts 13).
// Either way the sender retries with a fresh date and the delivery goes through once there is room.
//
// Safe for concurrent use, under one mutex held for O(log n) work: a min-heap of entries by time per
// group, and two heaps of groups, by count (for the heaviest) and by their oldest time (for a
// sweep that never scans). Every call forgets up to replaySweepStep expired entries of any group, so
// expired ones are cleared at least as fast as new ones arrive, and at most replayCallBudget in all;
// a new group is turned away as full only after that budget has been spent on what had expired.
// Entries sit in one pool, grown by doubling up to max and reused, so the memory at the cap is
// fixed. In memory only: a restart forgets it, and the inbox's de-duplication by activity id (7.2)
// still holds across one.
type replaySet struct {
	mu        sync.Mutex
	max       int   // signatures remembered at most
	perGroup  int   // signatures one group holds at most
	maxGroups int   // groups remembered at most
	past      int64 // seconds a signature stays acceptable after the time it vouches for
	back      int64 // seconds the wall clock may read behind the memory's own before it counts as corrected
	now       int64 // the latest time given, Unix seconds: the memory's clock goes back only by a reset
	reset     bool  // the memory was emptied by a clock step back, not yet reported by stepped
	n         int
	m         map[replayKey]struct{}
	pool      []replaySig // entries; a free slot holds the next free slot in at
	free      int32       // the first free slot of pool, or -1
	groups    map[string]*replayGroup
	byCount   groupsByCount
	byAge     groupsByAge
}

// replaySweepStep is how many expired entries of any group each call forgets, oldest first, and
// replayCallBudget how many in all one call may forget: those, then the named group's own, then, when
// a new group finds the groups full, any. A call costs at most that many heap removals, whoever sends
// it: the check before the key lookup runs on requests not yet verified.
const (
	replaySweepStep  = 8
	replayCallBudget = 64
)

// advance sets the memory's clock from the wall clock. The memory's clock does not go back: a step
// back of up to sigAhead is taken as the wall clock's error and ignored, so that a forgotten
// signature cannot come back into the window. A step back of more than that is taken as a clock
// that was wrong and has been corrected (a Raspberry Pi takes its time from the network after it
// starts): keeping the memory's clock would refuse as stale every honest signature older than the
// size of the step, and all of them once it passes 12 hours, until real time caught up. So the memory
// forgets everything, as a restart does, and takes the wall clock; stepped reports it once. Every caller
// passes the wall clock as it read it just before the call, so calls in flight together differ by
// moments, never by sigAhead.
func (s *replaySet) advance(now int64) {
	if now < s.now-s.back {
		s.forgetAll()
		s.reset = true
	}
	s.now = max(s.now, now)
}

// forgetAll empties the memory, keeping its slots.
func (s *replaySet) forgetAll() {
	s.n, s.free, s.pool, s.now = 0, -1, s.pool[:0], 0
	clear(s.m)
	clear(s.groups)
	s.byCount, s.byAge = s.byCount[:0], s.byAge[:0]
}

// stepped says, once, that the memory forgot everything because the clock stepped back by more than
// sigAhead, so the caller can log one line; the memory logs nothing itself.
func (s *replaySet) stepped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.reset
	s.reset = false
	return r
}

// replayGroup is what the memory keeps for one group.
type replayGroup struct {
	s        *replaySet
	name     string
	h        []int32 // slots of s.pool, a min-heap by time
	floor    int64   // with hasFloor: the latest time among this group's evicted entries
	hasFloor bool
	cnt, age int // positions in s.byCount and s.byAge
}

func (o *replayGroup) Len() int           { return len(o.h) }
func (o *replayGroup) Less(i, j int) bool { return o.s.pool[o.h[i]].at < o.s.pool[o.h[j]].at }
func (o *replayGroup) Swap(i, j int)      { o.h[i], o.h[j] = o.h[j], o.h[i] }
func (o *replayGroup) Push(x any)         { o.h = append(o.h, x.(int32)) }
func (o *replayGroup) Pop() any           { x := o.h[len(o.h)-1]; o.h = o.h[:len(o.h)-1]; return x }

// oldest is the time by which the sweep orders groups: the oldest entry's, or the floor's when no
// entry is left (a floor is never later than an entry that was kept when it was set).
func (o *replayGroup) oldest() int64 {
	if len(o.h) > 0 {
		return o.s.pool[o.h[0]].at
	}
	return o.floor
}

// groupsByCount is a heap of groups, the one holding the most entries first.
type groupsByCount []*replayGroup

func (h groupsByCount) Len() int           { return len(h) }
func (h groupsByCount) Less(i, j int) bool { return len(h[i].h) > len(h[j].h) }
func (h groupsByCount) Swap(i, j int)      { h[i], h[j] = h[j], h[i]; h[i].cnt, h[j].cnt = i, j }
func (h *groupsByCount) Push(x any)        { o := x.(*replayGroup); o.cnt = len(*h); *h = append(*h, o) }
func (h *groupsByCount) Pop() any          { o := (*h)[len(*h)-1]; *h = (*h)[:len(*h)-1]; return o }

// groupsByAge is a heap of groups, the one whose oldest time is earliest first.
type groupsByAge []*replayGroup

func (h groupsByAge) Len() int           { return len(h) }
func (h groupsByAge) Less(i, j int) bool { return h[i].oldest() < h[j].oldest() }
func (h groupsByAge) Swap(i, j int)      { h[i], h[j] = h[j], h[i]; h[i].age, h[j].age = i, j }
func (h *groupsByAge) Push(x any)        { o := x.(*replayGroup); o.age = len(*h); *h = append(*h, o) }
func (h *groupsByAge) Pop() any          { o := (*h)[len(*h)-1]; *h = (*h)[:len(*h)-1]; return o }

// newReplaySet makes a memory of size signatures, of which one group holds a tenth and which keeps
// at most one group in fifty (at least two), for signatures acceptable for past after their time.
func newReplaySet(size int, past time.Duration) *replaySet {
	return &replaySet{max: size, perGroup: max(size/10, 1), maxGroups: max(size/50, 2), past: int64(past / time.Second), back: int64(sigAhead / time.Second),
		free: -1, m: map[replayKey]struct{}{}, groups: map[string]*replayGroup{}}
}

// expired says whether the window can no longer accept a signature vouching for at.
func (s *replaySet) expired(at int64) bool { return at+s.past < s.now }

// check says whether signatures from an group may be accepted now, without remembering them.
func (s *replaySet) check(group string, now int64, sigs ...replaySig) sigReason {
	return s.admit(group, now, sigs, false)
}

// add remembers the signatures of one request from an group, all or none: if one is already
// remembered or refused, nothing is added and the reason is returned.
func (s *replaySet) add(group string, now int64, sigs ...replaySig) sigReason {
	return s.admit(group, now, sigs, true)
}

func (s *replaySet) admit(group string, now int64, sigs []replaySig, keep bool) sigReason {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.advance(now)
	budget := replayCallBudget - s.sweepLocked(replaySweepStep)
	o := s.groups[group]
	if o != nil {
		for ; budget > 0 && len(o.h) > 0 && s.expired(o.oldest()); budget-- {
			s.popOldest(o)
		}
		if !s.settle(o) {
			o = nil
		}
	}
	for _, g := range sigs {
		if _, ok := s.m[g.key]; ok {
			return sigReplay
		}
		if s.expired(g.at) || o != nil && o.hasFloor && g.at <= o.floor {
			return sigStale
		}
	}
	if o == nil && len(s.groups) >= s.maxGroups {
		if budget > 0 {
			s.sweepLocked(budget) // what has expired goes first
		}
		if len(s.groups) >= s.maxGroups {
			return sigFull
		}
	}
	if !keep {
		return 0
	}
	if o == nil {
		o = s.newGroup(group)
	}
	for _, g := range sigs {
		if _, ok := s.m[g.key]; !ok {
			o = s.insert(o, g)
		}
	}
	return 0
}

func (s *replaySet) newGroup(name string) *replayGroup {
	o := &replayGroup{s: s, name: name}
	s.groups[name] = o
	heap.Push(&s.byCount, o)
	heap.Push(&s.byAge, o)
	return o
}

// insert remembers one signature of o, making room first: o's own oldest when o is at its cap, and
// when the memory is full, the oldest of o if it holds at least an equal share, else of the heaviest.
func (s *replaySet) insert(o *replayGroup, g replaySig) *replayGroup {
	if len(o.h) >= s.perGroup {
		s.evict(o)
	}
	if s.n >= s.max {
		v := o
		if len(o.h)*len(s.groups) < s.n {
			v = s.byCount[0]
		}
		s.evict(v)
	}
	if s.groups[o.name] != o { // evicting o's last entry, already expired, would have let it go
		o = s.newGroup(o.name)
	}
	var i int32
	if s.free >= 0 {
		i, s.free = s.free, int32(s.pool[s.free].at)
	} else {
		if len(s.pool) == cap(s.pool) {
			p := make([]replaySig, len(s.pool), len(s.pool)+min(max(len(s.pool), 64), s.max-len(s.pool)))
			copy(p, s.pool)
			s.pool = p
		}
		i = int32(len(s.pool))
		s.pool = append(s.pool, replaySig{})
	}
	s.pool[i] = g
	o.h = append(o.h, i)
	heap.Fix(o, len(o.h)-1)
	s.m[g.key] = struct{}{}
	s.n++
	s.settle(o)
	return o
}

// evict drops o's oldest entry to make room and raises o's floor to its time, unless the window
// refuses that time already.
func (s *replaySet) evict(o *replayGroup) {
	e := s.popOldest(o)
	if !s.expired(e.at) && (!o.hasFloor || e.at > o.floor) {
		o.floor, o.hasFloor = e.at, true
	}
	s.settle(o)
}

// popOldest forgets o's oldest entry and returns it; the caller settles o.
func (s *replaySet) popOldest(o *replayGroup) replaySig {
	i := o.h[0]
	last := len(o.h) - 1
	o.Swap(0, last)
	o.h = o.h[:last]
	if last > 0 {
		heap.Fix(o, 0)
	}
	if c := cap(o.h); c > 64 && len(o.h) < c/4 {
		o.h = append(make([]int32, 0, c/2), o.h...)
	}
	e := s.pool[i]
	delete(s.m, e.key)
	s.pool[i] = replaySig{at: int64(s.free)}
	s.free = i
	s.n--
	return e
}

// settle puts o back in order after a change, forgetting its floor once the window refuses it, and o
// itself once it has neither entries nor floor; it says whether o is still kept.
func (s *replaySet) settle(o *replayGroup) bool {
	if o.hasFloor && s.expired(o.floor) {
		o.hasFloor = false
	}
	if len(o.h) == 0 && !o.hasFloor {
		delete(s.groups, o.name)
		heap.Remove(&s.byCount, o.cnt)
		heap.Remove(&s.byAge, o.age)
		return false
	}
	heap.Fix(&s.byCount, o.cnt)
	heap.Fix(&s.byAge, o.age)
	return true
}

// sweep forgets up to budget expired entries and floors, oldest first, and says how many it forgot:
// for the housekeeping pass, which the inbox item wires.
func (s *replaySet) sweep(now int64, budget int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.advance(now)
	return s.sweepLocked(budget)
}

func (s *replaySet) sweepLocked(budget int) int {
	done := 0
	for ; done < budget && len(s.byAge) > 0; done++ {
		o := s.byAge[0]
		if !s.expired(o.oldest()) {
			break
		}
		if len(o.h) > 0 {
			s.popOldest(o)
		}
		s.settle(o)
	}
	return done
}

func (s *replaySet) size() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}

/* The clock */

// buildTime is when this build's commit was made, in Unix seconds, for the clock check before signing
// (federation.md 8.3). Nothing sets it yet: build-image.sh would add -X main.buildTime= with its
// SOURCE_DATE_EPOCH, the commit's own time, which keeps a commit building the same bytes. That line,
// with the release suite to show the bytes still match, belongs to the item that first signs.
var buildTime = ""

// builtAt is the earliest time this build can be running at: buildTime, or the commit time Go
// records itself when the build is made in a git checkout (deploy.sh --build, which does not pass
// -buildvcs=false), or zero when neither is known, which is logged once and skips the check.
var builtAt = sync.OnceValue(func() time.Time {
	if n, err := strconv.ParseInt(buildTime, 10, 64); err == nil && n > 0 {
		return time.Unix(n, 0)
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			if t, err := time.Parse(time.RFC3339, s.Value); s.Key == "vcs.time" && err == nil {
				return t
			}
		}
	}
	log.Printf("clock: this build does not know when it was made, so signing does not wait for the clock")
	return time.Time{}
})

// clockTrusted says whether the clock can be trusted to date signatures: not earlier than the build
// itself (federation.md 8.3; a Raspberry Pi has no battery clock and starts on its last shutdown's
// time). A zero build time means unknown, and the check is skipped. The newest updated_at in the
// database is the other bound the note names; the delivery queue item adds it.
func clockTrusted(now, built time.Time) bool { return built.IsZero() || !now.Before(built) }

/* The policy: verifying */

// sigVerifier checks signatures of requests that arrived. keyFor returns the public key of a key id,
// or an error: it does the fetching, the caching and the owner check (7.3, 7.4), and is given only
// key ids that are https URLs.
type sigVerifier struct {
	scheme, authority string // of the configured origin
	keyFor            func(ctx context.Context, keyID string) (*rsa.PublicKey, error)
	now               func() time.Time
	built             time.Time // builtAt(); a test sets its own
	minBits           int       // sigKeyMin; the vector tests set 1024
	seen              *replaySet
}

// sigResult is who signed a request that verified.
type sigResult struct {
	Form  sigForm // sigRFC9421 when both forms were present and verified
	KeyID string
	At    time.Time // the earliest time the signature vouches for
}

// newSigVerifier makes the verifier for the instance's origin, such as https://flowa.example.org.
func newSigVerifier(origin string, keyFor func(context.Context, string) (*rsa.PublicKey, error)) (*sigVerifier, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("the origin of a signature verifier is https:// and a host")
	}
	return &sigVerifier{scheme: "https", authority: urlAuthority(u), keyFor: keyFor, now: time.Now, built: builtAt(), minBits: sigKeyMin, seen: newReplaySet(sigSeenMax, sigPast)}, nil
}

// sigCheck is one signature ready for the key: its form, key id, signing string or base, decoded
// signature and the earliest time it vouches for.
type sigCheck struct {
	form  sigForm
	keyID string
	base  string
	sig   []byte
	at    time.Time
}

// sigHeaders reads the signature fields of a request: a cavage Signature header, or RFC 9421's
// Signature-Input and Signature, or both. The two forms share the name Signature: a line that reads
// as a cavage header is one, and when Signature-Input is present the other lines are RFC 9421's
// Dictionary (the SWICG report, "How to upgrade supported versions": Signature-Input "was only added
// in the later versions"; RFC 9421, 4: "each field MUST contain the same labels"). More than one
// cavage header, or more than one RFC 9421 signature, is refused: Mastodon accepts "Only a single
// signature", and a second label (4.3's proxy signature, say) names something Flowa has no use for,
// so choosing one by a rule of Flowa's own would accept what Mastodon refuses.
func sigHeaders(h http.Header) (*cavageSig, *sigInput, []byte, error) {
	lines, inputs := h.Values("Signature"), h.Values("Signature-Input")
	if len(lines) == 0 {
		if len(inputs) > 0 {
			return nil, nil, nil, sigFail(sigMalformed)
		}
		return nil, nil, nil, sigFail(sigMissing)
	}
	if len(lines) > sigLinesMax || len(inputs) > sigLinesMax {
		return nil, nil, nil, sigFail(sigTooLarge)
	}
	var cav *cavageSig
	var dict []string
	for _, l := range lines {
		c, err := parseCavage(l)
		switch {
		case err == nil && cav != nil:
			return nil, nil, nil, sigFail(sigMultiple)
		case err == nil:
			cav = c
		case len(inputs) == 0:
			return nil, nil, nil, err
		default:
			dict = append(dict, l)
		}
	}
	if len(inputs) == 0 {
		return cav, nil, nil, nil
	}
	if len(dict) == 0 {
		return nil, nil, nil, sigFail(sigMalformed)
	}
	ins, err := parseSignatureInput(strings.Join(inputs, ", "))
	if err != nil {
		return nil, nil, nil, err
	}
	labels, sigs, err := parseSignatureField(strings.Join(dict, ", "))
	if err != nil {
		return nil, nil, nil, err
	}
	if len(ins) > 1 || len(labels) > 1 {
		return nil, nil, nil, sigFail(sigMultiple)
	}
	if len(ins) == 0 || len(labels) == 0 || ins[0].label != labels[0] {
		return nil, nil, nil, sigFail(sigMalformed)
	}
	return cav, &ins[0], sigs[0], nil
}

// inWindow checks a time a signature vouches for: no more than 12 hours ago (Mastodon), no more than
// an hour ahead (Flowa's own: federation.md 7.1).
func inWindow(t, now time.Time) sigReason {
	switch {
	case t.Before(now.Add(-sigPast)):
		return sigTooOld
	case t.After(now.Add(sigAhead)):
		return sigFuture
	}
	return 0
}

// signedDate reads a covered Date: one line (a singleton field, RFC 9110 5.5 and 6.6.1, so two lines joined
// would not be a date), an HTTP-date in any of the three forms a recipient "MUST accept" (RFC 9110,
// 5.6.7).
func signedDate(h http.Header) (time.Time, error) {
	if len(h.Values("Date")) > 1 {
		return time.Time{}, sigFail(sigHeaderTwice)
	}
	t, err := http.ParseTime(h.Get("Date"))
	if err != nil {
		return time.Time{}, sigFail(sigMalformed)
	}
	return t, nil
}

// checkCavage applies the policy to a cavage signature (federation.md 7.1, Accepted). The algorithm
// field never chooses the algorithm: the verifier "MUST derive the digital signature algorithm from
// the metadata associated with the keyId and MUST NOT use the value of algorithm" (2.5), and an RSA
// key is always verified as RSASSA-PKCS1-v1_5 with SHA-256, which is what rsa-sha256 names (E.2) and
// what the SWICG report says to assume for hs2019. The field is only checked: absent, rsa-sha256 or
// hs2019, and anything else is refused, whether unknown or naming another kind of key (2.1.3: when
// it "differs from the key metadata ... an implementation MUST produce an error"). Covered must be
// the request line ((request-target): Mastodon's "Creating HTTP signatures", the SWICG report: "Most
// fediverse software will reject GET requests without signed (request-target)"), host, date or
// (created), and on a request with a body the digest (Mastodon: "The Digest: header must also be
// included within the headers parameter").
func (v *sigVerifier) checkCavage(s *cavageSig, m *sigMessage, body []byte, withBody bool, now time.Time) (*sigCheck, error) {
	switch s.algorithm {
	case "", "rsa-sha256", "hs2019":
	default:
		return nil, sigFail(sigAlgorithm)
	}
	if !httpsURL(s.keyID) {
		return nil, sigFail(sigKeyID)
	}
	has := func(n string) bool { return slices.Contains(s.headers, n) }
	switch {
	case !has("(request-target)"):
		return nil, sigFail(sigNoRequestLine)
	case !has("host"):
		return nil, sigFail(sigNoHost)
	case !has("date") && !has("(created)"):
		return nil, sigFail(sigNoTime)
	case withBody && !has("digest"):
		return nil, sigFail(sigNoDigest)
	case has("digest") && len(m.header.Values("Digest")) == 0:
		return nil, sigFail(sigDigestMissing)
	}
	base, err := cavageString(s, m)
	if err != nil {
		return nil, err
	}
	var times []time.Time
	if has("date") {
		t, err := signedDate(m.header)
		if err != nil {
			return nil, err
		}
		times = append(times, t)
	}
	if s.created != "" {
		n, _ := strconv.ParseInt(s.created, 10, 64)
		if n > now.Unix() {
			return nil, sigFail(sigFuture) // 2.1.4: a created "in the future MUST NOT be processed"
		}
		if has("(created)") {
			times = append(times, time.Unix(n, 0))
		}
	}
	if s.expires != "" {
		whole, _, _ := strings.Cut(s.expires, ".")
		if n, _ := strconv.ParseInt(whole, 10, 64); n < now.Unix() {
			return nil, sigFail(sigExpired) // 2.1.5: an expires "in the past MUST NOT be processed"
		}
	}
	at, err := vouched(times, now)
	if err != nil {
		return nil, err
	}
	if has("digest") {
		s256, s512, err := parseDigest(strings.Join(m.header.Values("Digest"), ", "))
		if err != nil {
			return nil, err
		}
		if r := digestMatches(body, s256, s512); r != 0 {
			return nil, sigFail(r)
		}
	}
	sig, ok := b64Decode(s.signature)
	if !ok {
		return nil, sigFail(sigBadSignature)
	}
	return &sigCheck{form: sigCavage, keyID: s.keyID, base: base, sig: sig, at: at}, nil
}

// vouched checks every time a signature vouches for against the window and returns the earliest.
// When both Date and created are signed and differ, both must be inside it: the RFC leaves it to the
// verifier (B.2.3: "it is up to the verifier to determine whether its value has to match"), and
// Flowa takes the stricter reading rather than choosing one.
func vouched(times []time.Time, now time.Time) (time.Time, error) {
	var at time.Time
	for _, t := range times {
		if r := inWindow(t, now); r != 0 {
			return time.Time{}, sigFail(r)
		}
		if at.IsZero() || t.Before(at) {
			at = t
		}
	}
	return at, nil
}

// check9421 applies the policy to an RFC 9421 signature, Mastodon's list (security page): created
// present, @method and @target-uri covered, Content-Digest present and covered, and RSASSA-PKCS1-v1_5
// with SHA-256 the only algorithm. An alg parameter, when given, must say rsa-v1_5-sha256: the key
// (RSA) decides the algorithm, and one stated in two places must agree (3.2, step 6.5); without one
// the key decides alone (6.3), as in Mastodon's example. The request line may also be covered as
// @method, @authority and @path, with @query when the request has a query (the three components
// that make up @target-uri, 2.2.2 to 2.2.7); @request-target is not counted, being "NOT
// RECOMMENDED" where HTTP/2 might be in use (2.2.5). The digest is required on a request with a
// body, so a GET signed without Content-Digest is accepted (Mastodon's page states no exception for
// a GET, so Flowa never sends one: GETs go out as cavage).
func (v *sigVerifier) check9421(in *sigInput, sig []byte, m *sigMessage, body []byte, withBody bool, now time.Time) (*sigCheck, error) {
	if in.hasAlg && in.alg != "rsa-v1_5-sha256" {
		return nil, sigFail(sigAlgorithm)
	}
	if !httpsURL(in.keyID) {
		return nil, sigFail(sigKeyID)
	}
	has := func(n string) bool {
		return slices.ContainsFunc(in.covered, func(c sfItem) bool { return c.val.s == n && len(c.params) == 0 })
	}
	line := has("@method") && (has("@target-uri") || has("@authority") && has("@path") && (!strings.Contains(m.target, "?") || has("@query")))
	switch {
	case !line:
		return nil, sigFail(sigNoRequestLine)
	case !in.hasCreated:
		return nil, sigFail(sigNoTime)
	case withBody && !has("content-digest"):
		return nil, sigFail(sigNoDigest)
	case has("content-digest") && len(m.header.Values("Content-Digest")) == 0:
		return nil, sigFail(sigDigestMissing)
	}
	base, err := rfc9421Base(in, m)
	if err != nil {
		return nil, err
	}
	times := []time.Time{time.Unix(in.created, 0)}
	if has("date") {
		t, err := signedDate(m.header)
		if err != nil {
			return nil, err
		}
		times = append(times, t)
	}
	if in.hasExpires && in.expires < now.Unix() {
		return nil, sigFail(sigExpired) // 3.2.1: "Rejecting signatures past the expiration time"
	}
	at, err := vouched(times, now)
	if err != nil {
		return nil, err
	}
	if has("content-digest") {
		s256, s512, err := parseContentDigest(strings.Join(m.header.Values("Content-Digest"), ", "))
		if err != nil {
			return nil, err
		}
		if r := digestMatches(body, s256, s512); r != 0 {
			return nil, sigFail(r)
		}
	}
	return &sigCheck{form: sigRFC9421, keyID: in.keyID, base: base, sig: sig, at: at}, nil
}

// verify checks the signature of a request that arrived. body is the request's content exactly as
// received (after any transfer coding is removed and before any content coding is undone: RFC 9530 2
// digests "the actual message content"), already read by the caller within its own cap (7.5: 256 KB
// for the inbox). verify reads no body itself, keeps no copy of it, and reads each byte of it at
// most twice in memory (two hashes when both digests are sent). It returns who signed, or a
// *sigError whose reason is safe to log.
//
// When a request carries both forms, each must verify, with the same key: a request vouched for by
// two keys at once has no single signer, so it is refused, and one valid and one invalid signature
// make it invalid. RFC 9421 is then reported as the form, so the sender's host is marked for it
// (7.1: "A host that sends Flowa a valid RFC 9421 signature is marked the same way").
//
// Replay is checked on every method but GET and HEAD. A replayed signed GET within the window reads
// only what the signer could read with its own signature anyway, and remembering every signed fetch
// would fill the memory with them and leave the deliveries, which change things, to the stale rule.
// The memory is asked before the key is looked up and again after the signature verifies, by the
// sender group of the key id (senderGroup); sigFull from it is for a 503 with Retry-After, not a 401.
//
// The key lookup is called for whatever https key id a request names, before any cryptography: the
// inbox must give it a fetch cache and a limit per host before it wires a lookup that fetches (7.4).
func (v *sigVerifier) verify(ctx context.Context, m *sigMessage, body []byte) (*sigResult, error) {
	now := v.now()
	if !clockTrusted(now, v.built) {
		return nil, sigFail(sigClock) // 8.3: the inbox answers 503 for this, not 401
	}
	cav, in, sig9421, err := sigHeaders(m.header)
	if err != nil {
		return nil, err
	}
	if cav != nil && in != nil && cav.keyID != in.keyID {
		return nil, sigFail(sigBothDisagree)
	}
	withBody := len(body) > 0 || m.method != "GET" && m.method != "HEAD"
	var checks []*sigCheck
	if cav != nil {
		c, err := v.checkCavage(cav, m, body, withBody, now)
		if err != nil {
			return nil, err
		}
		checks = append(checks, c)
	}
	if in != nil {
		c, err := v.check9421(in, sig9421, m, body, withBody, now)
		if err != nil {
			return nil, err
		}
		checks = append(checks, c)
	}
	replay := m.method != "GET" && m.method != "HEAD"
	keyID := checks[0].keyID
	u, _ := url.Parse(keyID) // an https URL, checked above
	group := senderGroup(keyID)
	if _, why := fedHost(u.Hostname()); why != "" || group == "" {
		return nil, sigFail(sigKeyID) // a host the fetcher would refuse: its key could never be had
	}
	var sigs [2]replaySig
	if replay {
		for i, c := range checks {
			sigs[i] = replaySig{key: replayKeyOf(c.sig), at: c.at.Unix()}
		}
		if r := v.seen.check(group, now.Unix(), sigs[:len(checks)]...); r != 0 {
			return nil, sigFail(r)
		}
	}
	key, err := v.keyFor(ctx, keyID)
	if err != nil || key == nil {
		return nil, &sigError{Reason: sigKeyUnavailable, err: err}
	}
	if bits := key.N.BitLen(); bits < v.minBits || bits > sigKeyMax {
		return nil, sigFail(sigKeyRefused)
	}
	for _, c := range checks {
		if len(c.sig) != key.Size() {
			return nil, sigFail(sigBadSignature)
		}
		h := sha256.Sum256([]byte(c.base))
		if rsa.VerifyPKCS1v15(key, crypto.SHA256, h[:], c.sig) != nil {
			return nil, sigFail(sigInvalid)
		}
	}
	res := &sigResult{Form: checks[len(checks)-1].form, KeyID: keyID, At: checks[0].at}
	for _, c := range checks {
		if c.at.Before(res.At) {
			res.At = c.at
		}
	}
	if replay {
		if r := v.seen.add(group, now.Unix(), sigs[:len(checks)]...); r != 0 {
			return nil, sigFail(r)
		}
	}
	return res, nil
}

// verifyRequest verifies a request that arrived, with body its content as received within the
// caller's cap (see verify). The path and query are the request target exactly as received
// (r.RequestURI, never Go's decoded r.URL.Path), and the authority is the configured origin's,
// whatever Host the request carried (federation.md, finding 4). Only the origin form of a request
// target ("/path?query") is taken: the inbox is never asked through a proxy's absolute form.
func (v *sigVerifier) verifyRequest(ctx context.Context, r *http.Request, body []byte) (*sigResult, error) {
	if !strings.HasPrefix(r.RequestURI, "/") {
		return nil, sigFail(sigMalformed)
	}
	return v.verify(ctx, &sigMessage{method: r.Method, scheme: v.scheme, authority: v.authority, target: r.RequestURI, header: r.Header}, body)
}

/* Signing */

// signRefusal says why a request was not signed. Its text is fixed.
type signRefusal struct{ why string }

func (e *signRefusal) Error() string { return "not signed: " + e.why }

var (
	errClockUntrusted = &signRefusal{"the clock is earlier than this build, so it is not set yet"}
	errSignKey        = &signRefusal{"no key, or a key id that is not an https URL"}
	errSignURL        = &signRefusal{"only an https URL is signed"}
)

func rsaSign(key *rsa.PrivateKey, s string) ([]byte, error) {
	h := sha256.Sum256([]byte(s))
	return rsa.SignPKCS1v15(nil, key, crypto.SHA256, h[:]) // deterministic: RFC 8017 8.2
}

// httpSigner is the fetcher's signer for one key: keyID is its full https id. It runs on every
// request the fetcher sends, a redirect included, so each signature names the URL it goes to. form
// is the form a host is sent (the delivery queue decides, federation.md 7.1); a GET is always signed
// with cavage, since Mastodon's RFC 9421 list requires a Content-Digest with no exception for a GET.
// What is sent (7.1):
//
//   - cavage: algorithm="rsa-sha256", headers="(request-target) host date" and on a request with a
//     body also digest, with Digest: SHA-256=<base64>;
//   - RFC 9421: Content-Digest: sha-256=:<base64>:, and
//     sig1=("@method" "@target-uri" "content-digest");created=<seconds>;keyid="<key id>", no alg.
//
// The Host header sent is set to the URL's authority as signed: the host in lower case, no default
// port (RFC 9421, 2.2.3; for cavage the host line is the Host header's value, 2.3). The request line
// signed is the one Go writes, req.URL.RequestURI(): "/" for an empty path, the path's own percent
// encoding and capitals, the query as given. Date is set from the same reading of the clock as
// created, replacing the one fetcher.send set, so the two forms never disagree by a second. Nothing
// is signed while the clock is earlier than the build (8.3): errClockUntrusted.
func httpSigner(key *rsa.PrivateKey, keyID string, form sigForm, now func() time.Time, built time.Time) signer {
	return func(req *http.Request, body []byte) error {
		t := now()
		if !clockTrusted(t, built) {
			return errClockUntrusted
		}
		if key == nil || !httpsURL(keyID) || strings.ContainsAny(keyID, `"\`) {
			return errSignKey // the cavage header puts keyID in a quoted string as it is
		}
		if req.URL == nil || req.URL.Scheme != "https" || req.URL.Host == "" {
			return errSignURL
		}
		method := req.Method
		if method == "" {
			method = "GET"
		}
		req.Host = urlAuthority(req.URL)
		req.Header.Set("Date", t.UTC().Format(http.TimeFormat)) // the instant created is, whatever the fetcher set
		m := &sigMessage{method: method, scheme: "https", authority: req.Host, target: req.URL.RequestURI(), header: req.Header}
		withBody := body != nil || method != "GET" && method != "HEAD"
		if form == sigRFC9421 && withBody {
			req.Header.Set("Content-Digest", contentDigestHeader(body))
			var b strings.Builder
			sfWriteBare(&b, sfBare{kind: sfString, s: keyID})
			input := `sig1=("@method" "@target-uri" "content-digest");created=` + strconv.FormatInt(t.Unix(), 10) + ";keyid=" + b.String()
			ins, err := parseSignatureInput(input)
			if err != nil {
				return err
			}
			base, err := rfc9421Base(&ins[0], m)
			if err != nil {
				return err
			}
			sig, err := rsaSign(key, base)
			if err != nil {
				return err
			}
			req.Header.Set("Signature-Input", input)
			req.Header.Set("Signature", "sig1=:"+base64.StdEncoding.EncodeToString(sig)+":")
			return nil
		}
		s := &cavageSig{keyID: keyID, algorithm: "rsa-sha256", headers: []string{"(request-target)", "host", "date"}}
		if withBody {
			req.Header.Set("Digest", digestHeader(body))
			s.headers = append(s.headers, "digest")
		}
		str, err := cavageString(s, m)
		if err != nil {
			return err
		}
		sig, err := rsaSign(key, str)
		if err != nil {
			return err
		}
		req.Header.Set("Signature", `keyId="`+keyID+`",algorithm="rsa-sha256",headers="`+strings.Join(s.headers, " ")+`",signature="`+base64.StdEncoding.EncodeToString(sig)+`"`)
		return nil
	}
}

/* ---------- federation: the shapes activities arrive in ----------
How Flowa reads what another server sends (docs/federation.md 6.3). Activity Streams 2.0 is written
as JSON-LD, and Flowa runs no JSON-LD processor, so one property can arrive in several shapes: one
value or a list of them (AS2 Core 4.2, Example 13: "a JSON Array with a mix of strings and Links"), an
id as a string or as an embedded object that carries it, null or left out ("semantically
equivalent", AS2 Core 2), and its name short, as a compact "as:" name or as a full IRI (AS2 Core 5.1
allows compact names for "property names and the value(s) of the type property"). This section is
the one place those shapes are read. Everything in it is a pure function of the bytes: nothing
fetches, logs or touches the database, and nothing calls it yet (FRD's remote actors item and FWR's
inbox will). Three layers:

  - apScan, one pass over the whole document before anything is decoded: well-formed JSON (RFC
    8259), UTF-8 (AS2 Core 2: "MUST be serialized using the UTF-8 character encoding") with no
    escaped half of a surrogate pair, at most 24 levels deep and 1,000 entries in any list or object
    (federation.md 7.5), and no member name twice in one object. encoding/json, left to itself, keeps
    the last of two equal names without a word (a name with an escaped letter in it included), replaces invalid
    UTF-8 and lone surrogates with U+FFFD, and matches struct fields without regard to case; a
    server that keeps the first of two ids would read another document than Flowa does. So the scan
    refuses all of it first, and decoding then uses maps keyed by the exact name, never structs;
  - two small types, oneOrMany and idOrObject, through which every property that may hold a list or
    a link is read, and apFields, an object's members under their short names;
  - the readers: decodeActor, decodeActivity, decodeNote, decodeTombstone and decodeCollection for a
    document, and idOrObject's actor, activity, note, tombstone and collection for one embedded in
    another. Each reads exactly what federation.md 6.3, 6.5 to 6.7 and 7.3 say Flowa uses, and
    nothing else.

Every refusal is an *apError: a fixed phrase (apReason) and the path of the property it is about, made
of the names in apTerms, never of anything the document said; both are safe to log. Rules chosen
where the texts leave a choice, each written down at the code it governs:

  - @context is never interpreted, whatever its shape; the vocabularies' prefixes as:, ldp:, sec: and
    toot: are read as the normative context and Mastodon's page define them. AS2 Core 2.1 forbids
    overriding the normative context.
  - "@id" and "@type" in an object Flowa reads are refused. The normative context aliases them to id
    and type, and AS2 Core 2.1 requires a document to be "consistent with" JSON-LD compaction with
    that context, which writes the aliases; no text shows a server sending the keywords, and a JSON-LD
    processor errs on an object with both ("colliding keywords"). Inside a property Flowa does not
    read they are left alone (AS2 Core Appendix C's own example uses them in extensions).
  - A list inside a list is refused in every property Flowa reads: AS2 Core 5, "JSON-LD forbids
    arrays of arrays". In a property Flowa does not read it is skipped, since consumers "MUST NOT
    stop processing" for an extension (AS2 Core 5); Mastodon's focalPoint is a list.
  - A null inside a list is skipped, as JSON-LD expansion drops it; an empty list is absence (AS2
    Core 2).
  - type may be one name or a list. The kind of an object is the one Activity Vocabulary type among
    its names (with Hashtag, the as: extension Mastodon's page lists); other names are extensions and
    are ignored; the base types Object and Link beside a more specific type are ignored too (a Note
    is an Object); two specific vocabulary types in one list are refused as a contradiction. So "is a Note" is
    Kind == "Note", and an actor is one whose kind is one of the five Actor Types of the Activity
    Vocabulary 3.2: Application, Group, Organization, Person, Service.
  - A document is refused only for what Flowa would act on wrongly; what it merely cannot use is
    dropped. An actor refused is an account whose every later activity fails its key lookup, so the
    bar for refusing one is high. Ids and the addresses Flowa acts on or delivers to (id, actor,
    attributedTo, object where it is acted on, inbox, followers, sharedInbox, the key asked for and
    its owner, movedTo, first, next, items) must be absolute https URLs the fetcher would ask (apIDOK),
    or the document is refused. Entries of lists Flowa only narrows or shows (recipients, alsoKnownAs,
    Mentions, other keys, url, icon, an attachment's url, a Hashtag's href) that are not are dropped,
    and the readers count what they dropped so that a caller can log it. Display fields that cannot be
    read (a date, a media type, a count) are absent, on the careful side where there is one: an
    unreadable sensitive is sensitive, an unreadable inReplyTo still makes a reply.
  - Ids are compared as written (AS2 Core 2.2: an IRI serving as an id "MUST NOT be so mapped"); the
    SWICG report on HTTP signatures picks a key whose id matches the key id "including fragments".
  - Where Flowa acts on one value of a property the vocabulary lets hold several (actor, target,
    inReplyTo, movedTo, endpoints, and object for every activity type but Flag), a list of one is
    read and a longer list is refused: choosing one would be a second reading the sender did not make.
  - @context is passed over, so a term a context adds is not seen: with {"author": "as:attributedTo"}
    in its context, "author" is a second author to a JSON-LD processor and an unknown member to Flowa.
    Flowa acts for the signer, whose own identity it checks (7.3), and servers that read the plain
    names, Mastodon among them by its page, read what Flowa reads.

Origin checks (federation.md 7.3) need no new helper: the activity's actor and its key id on one
origin, and an embedded object's id on its actor's origin, are both sameOrigin(a, b), which compares
two https URLs' origins whole; apNote.by says whether a Note is attributed to an actor alone. */

const (
	apDepthMax      = 24       // levels of objects and lists (federation.md 7.5)
	apEntriesMax    = 1000     // entries in one list or members in one object (federation.md 7.5, for every list)
	apRecipientsMax = 1000     // to, cc, bto, bcc and audience together
	apTextMax       = 64 << 10 // bytes of a text property (federation.md 7.5); content is cut there instead
	apShortMax      = 255      // bytes of a media type or a language tag
	apLinkDepth     = 2        // an Image's url's Link: how far a link is followed inside one property
	asNS            = "https://www.w3.org/ns/activitystreams#"
	asPublic        = asNS + "Public"
)

// apReason is why a document was refused. Each is a fixed phrase, safe to log.
type apReason uint8

const (
	apTooLarge          apReason = iota + 1 // over the size the caller allows
	apMalformed                             // not well-formed JSON
	apNotObject                             // the document is not a JSON object
	apBadText                               // not UTF-8, or an escaped half of a surrogate pair
	apTooDeep                               // nested deeper than apDepthMax
	apTooMany                               // a list or an object with more than apEntriesMax entries
	apDuplicate                             // a member named twice in one object, in any spelling
	apKeyword                               // "@id" or "@type" in an object Flowa reads
	apWrongShape                            // a value of a JSON kind the property does not take
	apNested                                // a list inside a list
	apTooLong                               // a text, id or type name over its length limit
	apNoID                                  // no id where one is required
	apBadID                                 // an id or address that is not an https URL Flowa may fetch
	apNoType                                // no type
	apTypeConflict                          // two Activity Vocabulary types in one type list
	apWrongKind                             // not the kind of object the reader reads
	apNoInbox                               // an actor without an inbox that is not suspended
	apMoreThanOne                           // more than one value where Flowa acts on one
	apTooManyRecipients                     // more than apRecipientsMax addresses
	apKeyTwice                              // two public keys with the same id
	apNoKey                                 // no public key with the id asked for
	apByID                                  // an object given by id only, read as if embedded
)

var apReasons = [...]string{
	apTooLarge:          "a document over the size limit",
	apMalformed:         "a document that is not well-formed JSON",
	apNotObject:         "a document that is not a JSON object",
	apBadText:           "text that is not valid UTF-8",
	apTooDeep:           "a document nested deeper than 24 levels",
	apTooMany:           "a list or object with more than 1,000 entries",
	apDuplicate:         "a member named twice in one object",
	apKeyword:           "@id or @type in place of id or type",
	apWrongShape:        "a value of the wrong kind",
	apNested:            "a list inside a list",
	apTooLong:           "a value over its length limit",
	apNoID:              "an object without an id",
	apBadID:             "an id that is not an https URL that may be fetched",
	apNoType:            "an object without a type",
	apTypeConflict:      "a type list naming two kinds",
	apWrongKind:         "an object of another kind than expected",
	apNoInbox:           "an actor without an inbox",
	apMoreThanOne:       "more than one value where one is read",
	apTooManyRecipients: "more than 1,000 recipients",
	apKeyTwice:          "two public keys with the same id",
	apNoKey:             "no public key with that id",
	apByID:              "an object given by id only",
}

func (r apReason) String() string {
	if r == 0 {
		return "accepted" // apScan's answer for a document it takes; no refusal has the zero value
	}
	if int(r) < len(apReasons) && apReasons[r] != "" {
		return apReasons[r]
	}
	return "refused"
}

// apError is every refusal of this section. Field is the path of the property, built from the
// short names in apTerms ("object.tag.href"), never from the document's text; What is what the
// reader was reading ("actor", "activity", "post", "tombstone", "collection"), "document" before
// that is known.
type apError struct {
	Reason apReason
	Field  string
	What   string
}

func (e *apError) Error() string {
	what := e.What
	if what == "" {
		what = "document"
	}
	if e.Field == "" {
		return what + " refused: " + e.Reason.String()
	}
	return what + " refused: " + e.Reason.String() + " (" + e.Field + ")"
}

// apAs names what was being read in a reader's refusal.
func apAs(what string, err error) error {
	var e *apError
	if err == nil || !errors.As(err, &e) {
		return err
	}
	return &apError{Reason: e.Reason, Field: e.Field, What: what}
}

func apFail(r apReason) error { return &apError{Reason: r} }

// apIn puts a property's name in front of an error's path.
func apIn(field string, err error) error {
	var e *apError
	if !errors.As(err, &e) {
		return &apError{Reason: apWrongShape, Field: field}
	}
	if e.Field == "" {
		return &apError{Reason: e.Reason, Field: field}
	}
	return &apError{Reason: e.Reason, Field: field + "." + e.Field}
}

/* The scan */

// apScan checks a whole document in one pass, in time linear in its length and with no recursion,
// before anything decodes it. 0 is accepted. The root must be an object (an Activity Streams
// document "is a JSON document whose root value is an Activity Streams Object", AS2 Core 2).
func apScan(b []byte) apReason {
	if !utf8.Valid(b) {
		return apBadText
	}
	type level struct {
		obj    bool
		n      int
		serial int // which object this is, counted from 1 through the document
	}
	var stack []level
	// names[d] maps each member name seen at level d to its place in last, which holds the serial of
	// the last object at that level that had the name: a name is a duplicate when that is the object
	// open now. Objects at one level come one after another, so nothing is ever cleared, each
	// distinct name is stored once per level, and the cost is linear.
	var names []map[string]int
	var last []int
	var buf []byte // a name with escapes, unescaped
	serial := 0
	i := apSpace(b, 0)
	if i >= len(b) || b[i] != '{' {
		return apNotObject
	}
	const (
		wantValue = iota
		wantName
		afterValue
	)
	state := wantValue
	for {
		switch state {
		case wantValue:
			if i >= len(b) {
				return apMalformed
			}
			switch c := b[i]; {
			case c == '{' || c == '[':
				if len(stack) == apDepthMax {
					return apTooDeep
				}
				d := len(stack)
				serial++
				stack = append(stack, level{obj: c == '{', serial: serial})
				for c == '{' && len(names) <= d {
					names = append(names, map[string]int{})
				}
				i = apSpace(b, i+1)
				if i < len(b) && (b[i] == '}' && c == '{' || b[i] == ']' && c == '[') {
					stack = stack[:d]
					i++
					state = afterValue
					continue
				}
				if c == '{' {
					state = wantName
				} else {
					stack[d].n = 1
				}
				continue
			case c == '"':
				j, r := apString(b, i)
				if r != 0 {
					return r
				}
				i = j
			case c == 't' && bytes.HasPrefix(b[i:], []byte("true")):
				i += 4
			case c == 'f' && bytes.HasPrefix(b[i:], []byte("false")):
				i += 5
			case c == 'n' && bytes.HasPrefix(b[i:], []byte("null")):
				i += 4
			case c == '-' || isDigit(c):
				j := apNumber(b, i)
				if j < 0 {
					return apMalformed
				}
				i = j
			default:
				return apMalformed
			}
			state = afterValue
		case wantName:
			if i >= len(b) || b[i] != '"' {
				return apMalformed
			}
			j, r := apString(b, i)
			if r != 0 {
				return r
			}
			top := &stack[len(stack)-1]
			if top.n++; top.n > apEntriesMax {
				return apTooMany
			}
			name := b[i+1 : j-1]
			if bytes.IndexByte(name, '\\') >= 0 {
				buf = apUnescape(buf[:0], name)
				name = buf
			}
			// The map gives the name's place in last; only a name new at this level adds a key (and
			// a string), so assigning to a key already there never copies the name again.
			m := names[len(stack)-1]
			if k, ok := m[string(name)]; !ok {
				m[string(name)] = len(last)
				last = append(last, top.serial)
			} else if last[k] == top.serial {
				return apDuplicate
			} else {
				last[k] = top.serial
			}
			i = apSpace(b, j)
			if i >= len(b) || b[i] != ':' {
				return apMalformed
			}
			i = apSpace(b, i+1)
			state = wantValue
		case afterValue:
			i = apSpace(b, i)
			if len(stack) == 0 {
				if i != len(b) {
					return apMalformed
				}
				return 0
			}
			if i >= len(b) {
				return apMalformed
			}
			top := &stack[len(stack)-1]
			switch c := b[i]; {
			case c == ',':
				i = apSpace(b, i+1)
				if top.obj {
					state = wantName
				} else {
					if top.n++; top.n > apEntriesMax {
						return apTooMany
					}
					state = wantValue
				}
			case c == '}' && top.obj, c == ']' && !top.obj:
				stack = stack[:len(stack)-1]
				i++
			default:
				return apMalformed
			}
		}
	}
}

// apSpace skips the four whitespace characters of RFC 8259 2.
func apSpace(b []byte, i int) int {
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}
	return i
}

// apString checks the string that starts at b[i] and returns the index after its closing quote. A
// control character, an unknown escape and an escaped half of a surrogate pair without its other
// half are refused: RFC 8259 8.2 leaves such a string's meaning "unpredictable", and I-JSON (RFC
// 7493 2.1) forbids it.
func apString(b []byte, i int) (int, apReason) {
	for j := i + 1; j < len(b); {
		switch c := b[j]; {
		case c == '"':
			return j + 1, 0
		case c < 0x20:
			return 0, apMalformed
		case c == '\\':
			if j+1 >= len(b) {
				return 0, apMalformed
			}
			switch b[j+1] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				j += 2
			case 'u':
				r, ok := apHex4(b, j+2)
				if !ok {
					return 0, apMalformed
				}
				j += 6
				switch {
				case r >= 0xDC00 && r <= 0xDFFF:
					return 0, apBadText
				case r >= 0xD800 && r <= 0xDBFF:
					if j+1 >= len(b) || b[j] != '\\' || b[j+1] != 'u' {
						return 0, apBadText
					}
					lo, ok := apHex4(b, j+2)
					if !ok {
						return 0, apMalformed
					}
					if lo < 0xDC00 || lo > 0xDFFF {
						return 0, apBadText
					}
					j += 6
				}
			default:
				return 0, apMalformed
			}
		default:
			j++
		}
	}
	return 0, apMalformed
}

func apHex4(b []byte, i int) (rune, bool) {
	if i+4 > len(b) {
		return 0, false
	}
	var r rune
	for _, c := range b[i : i+4] {
		switch {
		case isDigit(c):
			r = r<<4 | rune(c-'0')
		case c >= 'a' && c <= 'f':
			r = r<<4 | rune(c-'a'+10)
		case c >= 'A' && c <= 'F':
			r = r<<4 | rune(c-'A'+10)
		default:
			return 0, false
		}
	}
	return r, true
}

// apUnescape appends to dst the text of a string's contents (without its quotes) that apString has
// checked, its escapes decoded (RFC 8259 7). It allocates nothing beyond dst's growth.
func apUnescape(dst, s []byte) []byte {
	for i := 0; i < len(s); {
		if s[i] != '\\' {
			dst = append(dst, s[i])
			i++
			continue
		}
		c := s[i+1]
		i += 2
		switch c {
		case 'b':
			dst = append(dst, '\b')
		case 'f':
			dst = append(dst, '\f')
		case 'n':
			dst = append(dst, '\n')
		case 'r':
			dst = append(dst, '\r')
		case 't':
			dst = append(dst, '\t')
		case 'u':
			r, _ := apHex4(s, i)
			i += 4
			if r >= 0xD800 && r <= 0xDBFF { // apString made sure the low half follows
				if lo, ok := apHex4(s, i+2); ok && i+1 < len(s) && s[i] == '\\' && s[i+1] == 'u' && lo >= 0xDC00 && lo <= 0xDFFF {
					r = 0x10000 + (r-0xD800)<<10 + (lo - 0xDC00)
					i += 6
				}
			}
			dst = utf8.AppendRune(dst, r)
		default: // '"', '\\' and '/' stand for themselves
			dst = append(dst, c)
		}
	}
	return dst
}

// apNumber returns the index after the number at b[i] (RFC 8259 6), or -1.
func apNumber(b []byte, i int) int {
	digits := func() bool {
		s := i
		for i < len(b) && isDigit(b[i]) {
			i++
		}
		return i > s
	}
	if b[i] == '-' {
		i++
	}
	switch {
	case i < len(b) && b[i] == '0':
		i++
	case !digits():
		return -1
	}
	if i < len(b) && b[i] == '.' {
		i++
		if !digits() {
			return -1
		}
	}
	if i < len(b) && (b[i] == 'e' || b[i] == 'E') {
		i++
		if i < len(b) && (b[i] == '+' || b[i] == '-') {
			i++
		}
		if !digits() {
			return -1
		}
	}
	return i
}

/* Names and types */

// apTerms maps every spelling of a property Flowa reads to its short name: the short name, the
// compact name and the full IRI. The IRIs are those of the normative Activity Streams context
// (https://www.w3.org/ns/activitystreams, read on 13 November 2026: inbox is ldp:inbox, the rest
// as:), of the security vocabulary for keys (Mastodon's ActivityPub page, "W3ID Security
// Vocabulary"), and of Mastodon's own for suspended ("Mastodon extensions"). contentMap, summaryMap,
// nameMap and orderedItems have no IRI of their own: the context maps them to as:content,
// as:summary, as:name and as:items, so a document with both items and orderedItems names one
// property twice and is refused.
var apTerms = func() map[string]string {
	m := map[string]string{"id": "id", "type": "type", "contentMap": "contentMap", "summaryMap": "summaryMap", "nameMap": "nameMap", "orderedItems": "orderedItems"}
	add := func(prefix, iri string, names ...string) {
		for _, n := range names {
			m[n], m[prefix+":"+n], m[iri+n] = n, n, n
		}
	}
	add("as", asNS, "actor", "alsoKnownAs", "attachment", "attributedTo", "audience", "bcc", "bto", "cc",
		"content", "deleted", "endpoints", "first", "followers", "formerType", "href", "icon", "inReplyTo",
		"items", "manuallyApprovesFollowers", "mediaType", "movedTo", "name", "next", "object",
		"preferredUsername", "published", "sensitive", "sharedInbox", "summary", "tag", "target", "to",
		"totalItems", "updated", "url")
	add("ldp", "http://www.w3.org/ns/ldp#", "inbox")
	add("sec", "https://w3id.org/security#", "controller", "owner", "publicKey", "publicKeyPem")
	add("toot", "http://joinmastodon.org/ns#", "suspended")
	return m
}()

// apCoreTypes are the types of the Activity Vocabulary (sections 2 and 3), and Hashtag, which
// Mastodon's page lists among its as: extensions.
var apCoreTypes = func() map[string]bool {
	m := map[string]bool{}
	for _, t := range strings.Fields(`Object Link Activity IntransitiveActivity Collection OrderedCollection
		CollectionPage OrderedCollectionPage Accept Add Announce Arrive Block Create Delete Dislike Flag
		Follow Ignore Invite Join Leave Like Listen Move Offer Question Reject Read Remove TentativeReject
		TentativeAccept Travel Undo Update View Application Group Organization Person Service Article
		Audio Document Event Image Note Page Place Profile Relationship Tombstone Video Mention Hashtag`) {
		m[t] = true
	}
	return m
}()

var apActorKinds = map[string]bool{"Application": true, "Group": true, "Organization": true, "Person": true, "Service": true}

// apTypeName gives a vocabulary type written as "as:Note" or in full as its short name; any other
// name, an extension, is kept as written.
func apTypeName(s string) string {
	t := strings.TrimPrefix(s, "as:")
	if t == s {
		t = strings.TrimPrefix(s, asNS)
	}
	if t != s && apCoreTypes[t] {
		return t
	}
	return s
}

// apIDOK says whether an id or address may be used: an https URL as httpsURL defines it (RFC 3986
// characters only, at most 2,048 bytes) that the fetcher would ask for a federation kind (no user
// name, port 443, a public DNS name: fetcher.check). This instance's own origin passes: an Accept
// names our own Follow. The instance's refused hosts and its own origin are not known here: the
// caller applies them when it fetches or acts (the fetcher on every request, FMD's domain policy on
// what comes in).
func apIDOK(s string) bool {
	if !httpsURL(s) {
		return false
	}
	u, err := url.Parse(s)
	return err == nil && apRules.check(&fetchRules[fetchDocument], &fetchReq{}, nil, u) == ""
}

var apRules = &fetcher{} // only its check is used, with no origin and no refused list

/* The two shapes */

// apFields is an object's members under their short names, each value the bytes of the document it
// came from (not a copy: the document must not change while a result holding an apFields is used).
// Members Flowa does not read, @context among them, are passed over unread.
type apFields map[string]json.RawMessage

// apSkip returns the index just after the value that starts at b[i]. b has passed apScan (or
// encoding/json's own check, when a type below is handed to it directly), so every value is well
// formed.
func apSkip(b []byte, i int) int {
	switch b[i] {
	case '"':
		return apStringEnd(b, i)
	case '{', '[':
		depth := 0
		for ; i < len(b); i++ {
			switch b[i] {
			case '"':
				i = apStringEnd(b, i) - 1
			case '{', '[':
				depth++
			case '}', ']':
				if depth--; depth == 0 {
					return i + 1
				}
			}
		}
		return i
	}
	for i < len(b) && strings.IndexByte(",]} \t\r\n", b[i]) < 0 {
		i++
	}
	return i
}

// apStringEnd returns the index after the closing quote of the well-formed string at b[i].
func apStringEnd(b []byte, i int) int {
	for i++; i < len(b); i++ {
		switch b[i] {
		case '\\':
			i++
		case '"':
			return i + 1
		}
	}
	return len(b)
}

// apEach calls fn for each member of the object, or each entry of the list, that b holds (b has
// passed apScan): the name with its quotes (nil in a list) and the value, both slices of b. It walks
// b once.
func apEach(b []byte, fn func(name, v []byte) error) error {
	obj := b[0] == '{'
	i := apSpace(b, 1)
	if b[i] == '}' || b[i] == ']' {
		return nil
	}
	for {
		var name []byte
		if obj {
			j := apSkip(b, i)
			name = b[i:j]
			i = apSpace(b, apSpace(b, j)+1) // past the colon
		}
		j := apSkip(b, i)
		if err := fn(name, b[i:j]); err != nil {
			return err
		}
		if i = apSpace(b, j); b[i] != ',' {
			return nil
		}
		i = apSpace(b, i+1)
	}
}

// apStr decodes a JSON string of a scanned document (so valid UTF-8). One without escapes, the usual
// case, is copied as it is.
func apStr(b []byte) (string, error) {
	if b[0] != '"' {
		return "", apFail(apWrongShape)
	}
	if bytes.IndexByte(b, '\\') < 0 {
		return string(b[1 : len(b)-1]), nil
	}
	return string(apUnescape(make([]byte, 0, len(b)), b[1:len(b)-1])), nil
}

// apFieldsOf reads one object of a scanned document; whitespace may come before it (RFC 8259 2).
func apFieldsOf(b []byte) (apFields, error) {
	if b = b[apSpace(b, 0):]; len(b) == 0 || b[0] != '{' {
		return nil, apFail(apWrongShape)
	}
	var f apFields // made at the first member Flowa reads: most embedded objects have few or none
	dup := ""
	var buf []byte
	err := apEach(b, func(q, v []byte) error {
		raw := q[1 : len(q)-1]
		if bytes.IndexByte(raw, '\\') >= 0 {
			buf = apUnescape(buf[:0], raw)
			raw = buf
		}
		if string(raw) == "@id" || string(raw) == "@type" {
			return &apError{Reason: apKeyword}
		}
		n, ok := apTerms[string(raw)]
		if !ok {
			return nil
		}
		if _, twice := f[n]; twice && (dup == "" || n < dup) {
			dup = n // the first in name order, so that one document always names the same one
		}
		if f == nil {
			f = make(apFields, 4)
		}
		f[n] = v
		return nil
	})
	switch {
	case err != nil:
		return nil, err
	case dup != "":
		return nil, &apError{Reason: apDuplicate, Field: dup}
	case f == nil:
		return apNone, nil
	}
	return f, nil
}

// apNone is every object with no member Flowa reads. Nothing writes to an apFields once read, so
// they share it, and an embedded object is still told from an id by Obj != nil.
var apNone = apFields{}

// apDecode decodes one value of a scanned document into v: text into a string, and a type with its
// own UnmarshalJSON (oneOrMany, idOrObject) through it. A value of a JSON kind the target does not
// take (a number where text is read) is apWrongShape.
func apDecode(b []byte, v any) error {
	switch p := v.(type) {
	case *string:
		s, err := apStr(b)
		*p = s
		return err
	case json.Unmarshaler:
		return p.UnmarshalJSON(b)
	}
	return apFail(apWrongShape)
}

func apNull(b []byte) bool { return len(b) == 4 && string(b) == "null" }

// oneOrMany is a property that may hold one value or a list (AS2 Core 4.2). null, an empty list
// and absence are no value; a null in a list is skipped; a list in a list is refused (AS2 Core 5);
// more than apEntriesMax values cannot pass apScan. Like idOrObject it reads a value of a document
// apParse has scanned: given to encoding/json directly, it would read whatever duplicate names and
// invalid text encoding/json lets through.
type oneOrMany[T any] []T

func (m *oneOrMany[T]) UnmarshalJSON(b []byte) error {
	*m = nil
	if apNull(b) {
		return nil
	}
	if b[0] != '[' {
		var v T
		if err := apDecode(b, &v); err != nil {
			return err
		}
		*m = oneOrMany[T]{v}
		return nil
	}
	var out oneOrMany[T]
	err := apEach(b, func(_, r []byte) error {
		switch {
		case apNull(r):
			return nil
		case r[0] == '[':
			return apFail(apNested)
		}
		var v T
		if err := apDecode(r, &v); err != nil {
			return err
		}
		out = append(out, v)
		return nil
	})
	*m = out
	return err
}

// idOrObject is a value that names another object: its id as a string, or the object embedded,
// which carries its id (and a Link its href). Obj holds an embedded object's members for a reader to
// read further, and is nil for an id. Shapes are checked here; whether an id may be used is the
// reader's (apIDOK), since the public collection is written "as:Public" or "Public" too.
type idOrObject struct {
	ID    string
	Href  string   // a Link's target (AS2 Core 4.2)
	Types []string // the type names, the vocabulary's in their short form
	Kind  string   // the Activity Vocabulary type among Types, or ""
	Obj   apFields
}

func (r *idOrObject) UnmarshalJSON(b []byte) error {
	*r = idOrObject{}
	switch {
	case apNull(b):
		return nil
	case b[0] == '"':
		var err error
		if r.ID, err = apStr(b); err != nil {
			return err
		}
		switch {
		case r.ID == "":
			return apFail(apBadID)
		case len(r.ID) > maxURL:
			return apFail(apTooLong)
		}
		return nil
	case b[0] != '{':
		return apFail(apWrongShape)
	}
	f, err := apFieldsOf(b)
	if err != nil {
		return err
	}
	r.Obj = f
	if r.ID, err = f.text("id", maxURL); err != nil {
		return err
	}
	if r.Href, err = f.text("href", maxURL); err != nil {
		return err
	}
	if _, ok := f["type"]; ok {
		if r.Types, r.Kind, err = f.types(); err != nil {
			return err
		}
	}
	return nil
}

/* Reading one property */

// text reads a text property: a string or null. Longer than max is refused.
func (f apFields) text(n string, max int) (string, error) {
	b, ok := f[n]
	if !ok || apNull(b) {
		return "", nil
	}
	s, err := apStr(b)
	switch {
	case err != nil:
		return "", apIn(n, err)
	case len(s) > max:
		return "", &apError{Reason: apTooLong, Field: n}
	}
	return s, nil
}

// short reads a short display value (a media type): one that is not a string of at most apShortMax
// bytes is absent.
func (f apFields) short(n string) string {
	s, err := f.text(n, apShortMax)
	if err != nil {
		return ""
	}
	return s
}

// flag reads a boolean property. Anything but false or null reads as true, the careful side for
// every flag Flowa reads: an unreadable sensitive hides the media, an unreadable
// manuallyApprovesFollowers shows a Follow as requested, an unreadable suspended stores nothing new.
func (f apFields) flag(n string) bool {
	b, ok := f[n]
	return ok && !apNull(b) && string(b) != "false"
}

// date reads a date property: AS2 Core 2.3, the RFC 3339 date-time with seconds optional and an
// upper case "T" and "Z" (time.Parse takes neither in lower case). The zero time is no date; bad is
// true when the property was there and could not be read.
func (f apFields) date(n string) (t time.Time, bad bool) {
	s, err := f.text(n, 64)
	if err != nil {
		return time.Time{}, true
	}
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04Z07:00"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, false
		}
	}
	return time.Time{}, true
}

// count reads totalItems (the context types it xsd:nonNegativeInteger): any JSON number whose value
// is a whole number from 0 to 2^53, so 5, 5.0 and 5e0 alike. Anything else is no count.
func (f apFields) count(n string) (uint64, bool) {
	b, ok := f[n]
	if !ok || (b[0] != '-' && !isDigit(b[0])) {
		return 0, false
	}
	v, err := strconv.ParseFloat(string(b), 64)
	if err != nil || v < 0 || v > 1<<53 || v != math.Trunc(v) {
		return 0, false
	}
	return uint64(v), true
}

// types reads type: one name or a list of them, the vocabulary's in their short form, and the
// object's kind, the one vocabulary type among them. Object and Link, the vocabulary's base types,
// are the kind only when no more specific type is given: ["Object", "Note"] is a Note.
func (f apFields) types() ([]string, string, error) {
	b, ok := f["type"]
	if !ok {
		return nil, "", &apError{Reason: apNoType, Field: "type"}
	}
	var ts oneOrMany[string]
	if err := ts.UnmarshalJSON(b); err != nil {
		return nil, "", apIn("type", err)
	}
	if len(ts) == 0 {
		return nil, "", &apError{Reason: apNoType, Field: "type"}
	}
	kind, base := "", ""
	for i, t := range ts {
		if len(t) > maxURL {
			return nil, "", &apError{Reason: apTooLong, Field: "type"}
		}
		ts[i] = apTypeName(t)
		switch t := ts[i]; {
		case !apCoreTypes[t]:
		case t == "Object" || t == "Link":
			if base == "" {
				base = t
			}
		case kind != "" && kind != t:
			return nil, "", &apError{Reason: apTypeConflict, Field: "type"}
		default:
			kind = t
		}
	}
	if kind == "" {
		kind = base
	}
	return ts, kind, nil
}

// each calls fn with each value of a property that may hold one or many: none for null or absence,
// a null in a list skipped, a list inside a list refused (AS2 Core 5). The values are visited where
// they lie, never gathered into a slice: a slice of a thousand entries in each of a thousand lists
// would cost more than the body. An error from fn ends the walk and is returned.
func (f apFields) each(n string, fn func(e []byte) error) error {
	b, ok := f[n]
	switch {
	case !ok || apNull(b):
		return nil
	case b[0] != '[':
		return fn(b)
	}
	return apEach(b, func(_, r []byte) error {
		switch {
		case apNull(r):
			return nil
		case r[0] == '[':
			return &apError{Reason: apNested, Field: n}
		}
		return fn(r)
	})
}

// refs reads a property that names objects, one or many, every entry readable.
func (f apFields) refs(n string) ([]idOrObject, error) {
	b, ok := f[n]
	if !ok {
		return nil, nil
	}
	var rs oneOrMany[idOrObject]
	if err := rs.UnmarshalJSON(b); err != nil {
		return nil, apIn(n, err)
	}
	return rs, nil
}

// ref reads a property Flowa acts on one value of: absent, one, or a list of one.
func (f apFields) ref(n string) (idOrObject, error) {
	rs, err := f.refs(n)
	switch {
	case err != nil:
		return idOrObject{}, err
	case len(rs) > 1:
		return idOrObject{}, &apError{Reason: apMoreThanOne, Field: n}
	case len(rs) == 0:
		return idOrObject{}, nil
	}
	return rs[0], nil
}

// ids reads a property of ids Flowa acts on (attributedTo), each a string or an embedded object with
// its id: every one must pass apIDOK, since an author Flowa cannot name must not vanish from apNote.by.
func (f apFields) ids(n string) ([]string, error) {
	rs, err := f.refs(n)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		if err := apCheckID(r.ID); err != nil {
			return nil, apIn(n, err)
		}
		out = append(out, r.ID)
	}
	return out, nil
}

// usable reads a list Flowa only narrows or shows (recipients, alsoKnownAs): an entry that cannot be
// read, has no id or fails apIDOK is dropped and counted. With public, the public collection's three
// forms (ActivityPub 5.6) are kept, as asPublic. total is every entry, dropped or not.
func (f apFields) usable(n string, public bool) (ids []string, dropped, total int, err error) {
	var r idOrObject
	err = f.each(n, func(e []byte) error {
		total++
		switch {
		case r.UnmarshalJSON(e) != nil:
			dropped++
		case public && (r.ID == asPublic || r.ID == "as:Public" || r.ID == "Public"):
			ids = append(ids, asPublic)
		case !apIDOK(r.ID):
			dropped++
		default:
			ids = append(ids, r.ID)
		}
		return nil
	})
	return ids, dropped, total, err
}

// id reads a property of one id Flowa acts on; "" when absent.
func (f apFields) id(n string) (string, error) {
	r, err := f.ref(n)
	if err != nil || (r.ID == "" && r.Obj == nil) {
		return "", err
	}
	if err := apCheckID(r.ID); err != nil {
		return "", apIn(n, err)
	}
	return r.ID, nil
}

func apCheckID(id string) error {
	switch {
	case id == "":
		return apFail(apNoID)
	case !apIDOK(id):
		return apFail(apBadID)
	}
	return nil
}

// langMap reads a language map (AS2 Core 4.7: contentMap, summaryMap, nameMap), a language tag to
// the text in that language, each text cut at apTextMax. An entry that is null is none.
func (f apFields) langMap(n string) (m map[string]string, cut bool, err error) {
	b, ok := f[n]
	if !ok || apNull(b) {
		return nil, false, nil
	}
	if b[0] != '{' {
		return nil, false, &apError{Reason: apWrongShape, Field: n}
	}
	m = map[string]string{}
	err = apEach(b, func(q, v []byte) error {
		lang, _ := apStr(q)
		if len(lang) > apShortMax {
			return &apError{Reason: apTooLong, Field: n}
		}
		if apNull(v) {
			return nil
		}
		s, err := apStr(v)
		if err != nil {
			return apIn(n, err)
		}
		var c bool
		m[lang], c = apCut(s)
		cut = cut || c
		return nil
	})
	return m, cut, err
}

// apPick is the one text Flowa takes from a property and its language map: the plain property when
// it has text, otherwise the map's "und" (undetermined) entry, otherwise the entry whose language tag
// sorts first. Flowa has no reader's language to prefer, and the first by tag is the same on every
// run.
func apPick(plain string, m map[string]string) string {
	if plain != "" || len(m) == 0 {
		return plain
	}
	if s, ok := m["und"]; ok {
		return s
	}
	tags := slices.Sorted(maps.Keys(m))
	return m[tags[0]]
}

// prose reads a natural-language property (name, summary) with its map, by apPick, cut at
// apTextMax. strict refuses a value that cannot be read; otherwise it is no text.
func (f apFields) prose(n, mapName string, strict bool) (s string, cut bool, err error) {
	s, err = f.text(n, math.MaxInt)
	if err == nil {
		var m map[string]string
		if m, cut, err = f.langMap(mapName); err == nil {
			var c bool
			s, c = apCut(apPick(s, m))
			cut = cut || c
		}
	}
	if err != nil {
		if strict {
			return "", false, err
		}
		return "", false, nil
	}
	return s, cut, nil
}

// apLink is a URL to show or fetch, with the media type its Link or Image gave.
type apLink struct {
	Href, MediaType string
}

// links reads a property that points at a resource: a URL, a Link (href), an Image or another object
// with a url, which may itself be any of these (AS2 Core 4.2, Examples 11 to 13; an actor's icon is
// "an Image" in Mastodon's page), one or many. Entries that cannot be read, are not https URLs Flowa
// may fetch, or are objects with neither are left out; so is a media type that cannot be read.
func (f apFields) links(n string) ([]apLink, error) { return f.linksIn(n, 0) }

func (f apFields) linksIn(n string, depth int) ([]apLink, error) {
	var out []apLink
	var r idOrObject
	err := f.each(n, func(e []byte) error {
		switch {
		case r.UnmarshalJSON(e) != nil:
		case r.Obj == nil:
			if apIDOK(r.ID) {
				out = append(out, apLink{Href: r.ID})
			}
		case r.Href != "":
			if apIDOK(r.Href) {
				out = append(out, apLink{Href: r.Href, MediaType: r.Obj.short("mediaType")})
			}
		case depth < apLinkDepth:
			inner, err := r.Obj.linksIn("url", depth+1)
			if err != nil {
				return apIn(n, err)
			}
			mt := r.Obj.short("mediaType")
			for _, l := range inner {
				if l.MediaType == "" {
					l.MediaType = mt
				}
				out = append(out, l)
			}
		}
		return nil
	})
	return out, err
}

// apAudience is an object's addressing (federation.md 6.7): to, cc, bto, bcc and audience, each
// entry an actor or collection id, the public collection's three forms read as asPublic. At most
// apRecipientsMax entries in all, counted before any is dropped.
//
// An entry that cannot be read or fails apIDOK is dropped and counted in Dropped. Dropping can only
// narrow what Flowa takes the audience to be, never widen it: the public collection is recognised
// before apIDOK and never dropped, so public() is the same with or without the dropped entries; a
// post is followers-only when the actor's followers collection is addressed, and that collection is
// an id that passed apIDOK when the actor was read, so a dropped entry cannot be it; and every other
// entry only names someone else who may see the post, whom Flowa does not deliver to anyway.
type apAudience struct {
	To, Cc, Bto, Bcc, Audience []string
	Dropped                    int
}

func (f apFields) audience() (apAudience, error) {
	var a apAudience
	n := 0
	for _, p := range []struct {
		name string
		dst  *[]string
	}{{"to", &a.To}, {"cc", &a.Cc}, {"bto", &a.Bto}, {"bcc", &a.Bcc}, {"audience", &a.Audience}} {
		ids, dropped, total, err := f.usable(p.name, true)
		if err != nil {
			return a, err
		}
		if n += total; n > apRecipientsMax {
			return a, &apError{Reason: apTooManyRecipients, Field: p.name}
		}
		*p.dst = ids
		a.Dropped += dropped
	}
	return a, nil
}

// public says whether the object is addressed to the public collection in to or in cc (Mastodon's
// "public" and "unlisted", federation.md 6.7).
func (a apAudience) public() bool {
	return slices.Contains(a.To, asPublic) || slices.Contains(a.Cc, asPublic)
}

/* The readers */

// apParse checks a document and reads its root object. max is the caller's cap on its size: 256 KB
// for an inbox body, 1 MB for a fetched document (federation.md 7.5); the reader of the body has
// already stopped at it, and this is the same limit said again.
func apParse(body []byte, max int) (apFields, error) {
	if len(body) > max {
		return nil, apFail(apTooLarge)
	}
	if r := apScan(body); r != 0 {
		return nil, apFail(r)
	}
	return apFieldsOf(body)
}

// apKey is one entry of an actor's publicKey. A key given by id only (ByID) has no PEM: the SWICG
// report says to fetch it ("If it's an id, fetch it").
type apKey struct {
	ID, Owner, PEM string
	ByID           bool
}

// apActor is what Flowa reads from a remote actor (federation.md 6.3, "What Flowa reads").
type apActor struct {
	ID                        string
	Types                     []string
	Kind                      string // one of apActorKinds
	PreferredUsername         string
	Name, Summary             string // Summary is HTML, for htmlToText; both cut at apTextMax
	URL                       []apLink
	Icon                      []apLink
	Inbox, SharedInbox        string
	Followers                 string
	Keys                      []apKey
	KeysSkipped               int // publicKey entries that could not be read
	ManuallyApprovesFollowers bool
	MovedTo                   string
	AlsoKnownAs               []string
	AlsoKnownAsDropped        int
	Suspended                 bool
}

func decodeActor(body []byte, max int) (*apActor, error) {
	f, err := apParse(body, max)
	if err != nil {
		return nil, apAs("actor", err)
	}
	a, err := actorFrom(f)
	return a, apAs("actor", err)
}

func actorFrom(f apFields) (*apActor, error) {
	a := &apActor{}
	var err error
	if a.Types, a.Kind, err = f.types(); err != nil {
		return nil, err
	}
	if !apActorKinds[a.Kind] {
		return nil, &apError{Reason: apWrongKind, Field: "type"}
	}
	if a.ID, err = f.id("id"); err == nil && a.ID == "" {
		err = &apError{Reason: apNoID, Field: "id"}
	}
	if err != nil {
		return nil, err
	}
	if a.PreferredUsername, err = f.text("preferredUsername", apTextMax); err != nil {
		return nil, err
	}
	a.Name, _, _ = f.prose("name", "nameMap", false)
	a.Summary, _, _ = f.prose("summary", "summaryMap", false)
	if a.URL, err = f.links("url"); err != nil {
		return nil, err
	}
	if a.Icon, err = f.links("icon"); err != nil {
		return nil, err
	}
	if a.Inbox, err = f.id("inbox"); err != nil {
		return nil, err
	}
	if a.Followers, err = f.id("followers"); err != nil {
		return nil, err
	}
	// endpoints is "a json object" or "a link to a JSON-LD document with these properties"
	// (ActivityPub 4.1). Flowa does not fetch the latter: without a shared inbox it uses the inbox.
	ep, err := f.ref("endpoints")
	if err != nil {
		return nil, err
	}
	if ep.Obj != nil {
		if a.SharedInbox, err = ep.Obj.id("sharedInbox"); err != nil {
			return nil, apIn("endpoints", err)
		}
	}
	if a.Keys, a.KeysSkipped, err = f.keys(); err != nil {
		return nil, err
	}
	a.ManuallyApprovesFollowers = f.flag("manuallyApprovesFollowers")
	if a.MovedTo, err = f.id("movedTo"); err != nil {
		return nil, err
	}
	if a.AlsoKnownAs, a.AlsoKnownAsDropped, _, err = f.usable("alsoKnownAs", false); err != nil {
		return nil, err
	}
	a.Suspended = f.flag("suspended")
	// ActivityPub 4.1: an actor "MUST have" an inbox. A suspended Mastodon account is served with
	// "empty data" (its page, "Suspended flag"), so it may have none and is still read, to be marked
	// gone.
	if a.Inbox == "" && !a.Suspended {
		return nil, &apError{Reason: apNoInbox, Field: "inbox"}
	}
	return a, nil
}

// keys reads publicKey: "a compacted id, or an expanded object, or a list of ids or objects" (the
// SWICG report, "How to obtain a signature's public key"). An entry that cannot be read (no id, an
// id or owner that fails apIDOK, a PEM that is not text: a did:key beside an https key, say) is
// skipped and counted: only the key the signature names is acted on, and key fails for a skipped one.
// Two entries with one id are refused, readable or not, since choosing by id would then be a choice
// between them.
func (f apFields) keys() (keys []apKey, skipped int, err error) {
	seen := map[string]bool{}
	var r idOrObject
	err = f.each("publicKey", func(e []byte) error {
		if r.UnmarshalJSON(e) != nil {
			skipped++
			return nil
		}
		if r.ID != "" {
			if seen[r.ID] {
				return &apError{Reason: apKeyTwice, Field: "publicKey"}
			}
			seen[r.ID] = true
		}
		if k, ok := apKeyOf(r); ok {
			keys = append(keys, k)
		} else {
			skipped++
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return keys, skipped, nil
}

// apKeyOf reads one publicKey entry, or says it cannot be used.
func apKeyOf(r idOrObject) (apKey, bool) {
	if !apIDOK(r.ID) {
		return apKey{}, false
	}
	k := apKey{ID: r.ID, ByID: r.Obj == nil}
	if r.Obj == nil {
		return k, true
	}
	var err error
	if k.Owner, err = r.Obj.id("owner"); err == nil && k.Owner == "" {
		// The SWICG report follows controller where owner is missing (federation.md 7.3).
		k.Owner, err = r.Obj.id("controller")
	}
	if err == nil {
		// Handed to parsePublicKeyPEM as it came, after JSON unescaping only.
		k.PEM, err = r.Obj.text("publicKeyPem", apTextMax)
	}
	return k, err == nil
}

// key is the entry of publicKey whose id is keyID, compared as written, fragment included (the
// SWICG report: "find the one whose id matches the original keyId", then "Check that this key
// object's id matches ... including fragments"). That the key's owner is this actor, and the key id
// on its origin, is the caller's check (federation.md 7.3).
func (a *apActor) key(keyID string) (apKey, error) {
	for _, k := range a.Keys {
		if k.ID == keyID {
			return k, nil
		}
	}
	return apKey{}, &apError{Reason: apNoKey, Field: "publicKey", What: "actor"}
}

// apOneObject are the activity types Flowa acts on one object of (federation.md 6.3): each must
// have exactly one. A Flag names several (Mastodon's page, "Reporting profiles and posts"); any
// other type keeps its list, since the inbox only counts and drops it.
var apOneObject = map[string]bool{
	"Create": true, "Update": true, "Delete": true, "Like": true, "Announce": true, "Follow": true,
	"Accept": true, "Reject": true, "Undo": true, "Block": true, "Move": true,
}

// apActivity is an activity's envelope (federation.md 6.3, 6.7, 7.3). Kind is "" for a type the
// vocabulary does not have (EmojiReact, QuoteRequest), which the inbox counts and drops.
type apActivity struct {
	ID             string
	Types          []string
	Kind           string
	Actor          string
	Object         []idOrObject // one for the types of apOneObject, by id or embedded; a Flag names several
	ObjectsDropped int          // a Flag's or another type's objects that could not be used
	Target         idOrObject
	Audience       apAudience
	Published      time.Time
	Updated        time.Time
	BadDate        bool // published or updated was given and could not be read
}

func decodeActivity(body []byte, max int) (*apActivity, error) {
	f, err := apParse(body, max)
	if err != nil {
		return nil, apAs("activity", err)
	}
	a, err := activityFrom(f)
	return a, apAs("activity", err)
}

func activityFrom(f apFields) (*apActivity, error) {
	a := &apActivity{}
	var err error
	if a.Types, a.Kind, err = f.types(); err != nil {
		return nil, err
	}
	// An activity "SHOULD have an id, unless it is intended to be transient" (ActivityPub 7). Flowa
	// requires one: the inbox removes duplicates by it and checks its origin (federation.md 7.2, 7.3).
	if a.ID, err = f.id("id"); err == nil && a.ID == "" {
		err = &apError{Reason: apNoID, Field: "id"}
	}
	if err != nil {
		return nil, err
	}
	if a.Actor, err = f.id("actor"); err == nil && a.Actor == "" {
		err = &apError{Reason: apNoID, Field: "actor"}
	}
	if err != nil {
		return nil, err
	}
	if apOneObject[a.Kind] {
		// The object is acted on: it must be one, readable, with an id Flowa may use.
		if a.Object, err = f.refs("object"); err != nil {
			return nil, err
		}
		switch len(a.Object) {
		case 0:
			return nil, &apError{Reason: apNoID, Field: "object"}
		case 1:
		default:
			return nil, &apError{Reason: apMoreThanOne, Field: "object"}
		}
		if err := apCheckID(a.Object[0].ID); err != nil {
			return nil, apIn("object", err)
		}
	} else {
		// A Flag's objects that cannot be ours, and those of a type the inbox drops, are dropped.
		err := f.each("object", func(e []byte) error {
			var r idOrObject
			if r.UnmarshalJSON(e) != nil || !apIDOK(r.ID) {
				a.ObjectsDropped++
			} else {
				a.Object = append(a.Object, r)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	if a.Target, err = f.ref("target"); err != nil {
		return nil, err
	}
	if a.Target.ID != "" || a.Target.Obj != nil {
		if err := apCheckID(a.Target.ID); err != nil {
			return nil, apIn("target", err)
		}
	}
	if a.Audience, err = f.audience(); err != nil {
		return nil, err
	}
	var b1, b2 bool
	a.Published, b1 = f.date("published")
	a.Updated, b2 = f.date("updated")
	a.BadDate = b1 || b2
	return a, nil
}

// apMention is a Mention tag: the actor it names (href) and the text shown ("@bo@social.example").
type apMention struct{ Href, Name string }

// apHashtag is a Hashtag tag; Href is "" when it was not an https URL.
type apHashtag struct{ Name, Href string }

// apAttachment is one attachment of a post. Name and Summary are both read: Mastodon's page names
// attachment[].summary "media description", the Activity Vocabulary's name is what many servers
// use, and which one a current Mastodon writes is open until a recorded post settles it
// (federation.md, Sources). MediaType is "" when it was absent or could not be read.
type apAttachment struct {
	Kind, MediaType string
	URL             []apLink
	Name, Summary   string
}

// alt is the description of the picture: summary first, then name (federation.md 6.5).
func (t apAttachment) alt() string {
	if t.Summary != "" {
		return t.Summary
	}
	return t.Name
}

// apNote is a post or a reply as federation.md 6.5 and 6.6 read it. Kind is its vocabulary type:
// Note, or one of the types 6.6 keeps as text with a link (Article, Page, Question, ...); the caller
// decides what it takes.
type apNote struct {
	ID           string
	Types        []string
	Kind         string
	AttributedTo []string
	// InReplyTo is the post this one answers. ReplyToUnknown is true when inReplyTo was given but
	// Flowa cannot address it (an id that fails apIDOK, or a value it cannot read): the post is still
	// a reply, to a post Flowa does not know, which 6.3 drops unless the author is followed and never
	// shows as a new thread.
	InReplyTo      string
	ReplyToUnknown bool
	Name           string // name, or from nameMap by apPick
	Summary        string // the content warning: summary, or from summaryMap by apPick
	Content        string // HTML, for htmlToText; cut at apTextMax
	ContentMap     map[string]string
	Cut            bool // content, summary, name or a map entry was cut at apTextMax
	Sensitive      bool
	Mentions       []apMention
	Hashtags       []apHashtag
	TagsDropped    int // Mentions without a usable href, and tags that could not be read
	Attachments    []apAttachment
	URL            []apLink
	Audience       apAudience
	Published      time.Time
	Updated        time.Time
	BadDate        bool // published or updated was given and could not be read
}

// isNote is the rule for "a Note": the one vocabulary type among its types is Note.
func (n *apNote) isNote() bool { return n.Kind == "Note" }

// text is the post's text: content, or from contentMap by apPick.
func (n *apNote) text() string { return apPick(n.Content, n.ContentMap) }

// by says whether the post is attributed to actor alone (federation.md 6.3, Create: "the Note's
// attributedTo and the activity's actor are the signer"). Several authors cannot all be the signer,
// so a list of more than one is false.
func (n *apNote) by(actor string) bool {
	return len(n.AttributedTo) == 1 && n.AttributedTo[0] == actor
}

func decodeNote(body []byte, max int) (*apNote, error) {
	f, err := apParse(body, max)
	if err != nil {
		return nil, apAs("post", err)
	}
	n, err := noteFrom(f)
	return n, apAs("post", err)
}

func noteFrom(f apFields) (*apNote, error) {
	n := &apNote{}
	var err error
	if n.Types, n.Kind, err = f.types(); err != nil {
		return nil, err
	}
	if n.ID, err = f.id("id"); err == nil && n.ID == "" {
		err = &apError{Reason: apNoID, Field: "id"}
	}
	if err != nil {
		return nil, err
	}
	if n.AttributedTo, err = f.ids("attributedTo"); err != nil {
		return nil, err
	}
	replies := 0
	err = f.each("inReplyTo", func(e []byte) error {
		if replies++; replies > 1 {
			return &apError{Reason: apMoreThanOne, Field: "inReplyTo"}
		}
		var r idOrObject
		if r.UnmarshalJSON(e) == nil && apIDOK(r.ID) {
			n.InReplyTo = r.ID
		} else {
			n.ReplyToUnknown = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var cut [4]bool
	n.Name, cut[0], _ = f.prose("name", "nameMap", false)
	// A content warning that cannot be read is not dropped: the post would be shown unfolded.
	if n.Summary, cut[1], err = f.prose("summary", "summaryMap", true); err != nil {
		return nil, err
	}
	// content is cut, not refused, at 64 KB (federation.md 7.5): the converter then adds the link to
	// the original (6.6).
	if n.Content, err = f.text("content", math.MaxInt); err != nil {
		return nil, err
	}
	n.Content, cut[2] = apCut(n.Content)
	if n.ContentMap, cut[3], err = f.langMap("contentMap"); err != nil {
		return nil, err
	}
	n.Cut = cut[0] || cut[1] || cut[2] || cut[3]
	n.Sensitive = f.flag("sensitive")
	if err = n.readTags(f); err != nil {
		return nil, err
	}
	if err = n.readAttachments(f); err != nil {
		return nil, err
	}
	if n.URL, err = f.links("url"); err != nil {
		return nil, err
	}
	if n.Audience, err = f.audience(); err != nil {
		return nil, err
	}
	var b1, b2 bool
	n.Published, b1 = f.date("published")
	n.Updated, b2 = f.date("updated")
	n.BadDate = b1 || b2
	return n, nil
}

// apCut cuts text at apTextMax bytes, on the start of a character.
func apCut(s string) (string, bool) {
	if len(s) <= apTextMax {
		return s, false
	}
	i := apTextMax
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return s[:i], true
}

// readTags reads Mention and Hashtag entries of tag (Mastodon's page, "tag[].type"); Emoji, other
// types and plain ids are skipped. A Mention whose href Flowa cannot use, and an entry it cannot
// read, is dropped and counted: nobody is notified for it (federation.md 6.6). A Hashtag keeps its
// name without an href Flowa cannot use.
func (n *apNote) readTags(f apFields) error {
	var r idOrObject
	return f.each("tag", func(e []byte) error {
		switch {
		case r.UnmarshalJSON(e) != nil:
			n.TagsDropped++
		case r.Obj == nil:
		case r.Kind == "Mention" && !apIDOK(r.Href):
			n.TagsDropped++
		case r.Kind == "Mention":
			name, _ := apCut(r.Obj.shown("name"))
			n.Mentions = append(n.Mentions, apMention{Href: r.Href, Name: name})
		case r.Kind == "Hashtag":
			h := apHashtag{}
			h.Name, _ = apCut(r.Obj.shown("name"))
			if apIDOK(r.Href) {
				h.Href = r.Href
			}
			n.Hashtags = append(n.Hashtags, h)
		}
		return nil
	})
}

// shown reads a text that is shown, not acted on: one that cannot be read is no text.
func (f apFields) shown(n string) string {
	s, err := f.text(n, math.MaxInt)
	if err != nil {
		return ""
	}
	return s
}

// readAttachments reads the attached media: objects only, each with its type, media type, URLs
// (a Link's own href, or the url of an Image or Document) and both descriptions.
func (n *apNote) readAttachments(f apFields) error {
	var r idOrObject
	return f.each("attachment", func(e []byte) error {
		if r.UnmarshalJSON(e) != nil || r.Obj == nil {
			return nil
		}
		t := apAttachment{Kind: r.Kind, MediaType: r.Obj.short("mediaType")}
		if r.Href != "" {
			if apIDOK(r.Href) {
				t.URL = []apLink{{Href: r.Href, MediaType: t.MediaType}}
			}
		} else {
			var err error
			if t.URL, err = r.Obj.links("url"); err != nil {
				return apIn("attachment", err)
			}
		}
		t.Name, _, _ = r.Obj.prose("name", "nameMap", false)
		t.Summary, _, _ = r.Obj.prose("summary", "summaryMap", false)
		n.Attachments = append(n.Attachments, t)
		return nil
	})
}

// apTombstone is a deleted object (Activity Vocabulary, Tombstone; ActivityPub 6.4).
type apTombstone struct {
	ID         string
	FormerType []string
	Deleted    time.Time
	Published  time.Time
	Updated    time.Time
	BadDate    bool // deleted, published or updated was given and could not be read
}

func decodeTombstone(body []byte, max int) (*apTombstone, error) {
	f, err := apParse(body, max)
	if err != nil {
		return nil, apAs("tombstone", err)
	}
	t, err := tombstoneFrom(f)
	return t, apAs("tombstone", err)
}

func tombstoneFrom(f apFields) (*apTombstone, error) {
	t := &apTombstone{}
	_, kind, err := f.types()
	if err != nil {
		return nil, err
	}
	if kind != "Tombstone" {
		return nil, &apError{Reason: apWrongKind, Field: "type"}
	}
	if t.ID, err = f.id("id"); err == nil && t.ID == "" {
		err = &apError{Reason: apNoID, Field: "id"}
	}
	if err != nil {
		return nil, err
	}
	// formerType is shown only: a value that cannot be read is none.
	var ft oneOrMany[string]
	if b, ok := f["formerType"]; ok && ft.UnmarshalJSON(b) == nil {
		for _, s := range ft {
			if len(s) <= maxURL {
				t.FormerType = append(t.FormerType, apTypeName(s))
			}
		}
	}
	var bad [3]bool
	t.Deleted, bad[0] = f.date("deleted")
	t.Published, bad[1] = f.date("published")
	t.Updated, bad[2] = f.date("updated")
	t.BadDate = bad[0] || bad[1] || bad[2]
	return t, nil
}

// apCollection is a collection or one of its pages (ActivityPub 5, AS2 Core 4.6): an outbox, a
// followers collection, a post's replies. First may be embedded (a reply collection's first page
// often is); a page is read with idOrObject.collection. Items are ids or embedded objects, each with
// an id.
type apCollection struct {
	ID         string
	Kind       string // Collection, OrderedCollection, CollectionPage or OrderedCollectionPage
	TotalItems uint64
	HasTotal   bool
	First      idOrObject
	Next       idOrObject
	Items      []idOrObject
}

func decodeCollection(body []byte, max int) (*apCollection, error) {
	f, err := apParse(body, max)
	if err != nil {
		return nil, apAs("collection", err)
	}
	c, err := collectionFrom(f)
	return c, apAs("collection", err)
}

func collectionFrom(f apFields) (*apCollection, error) {
	c := &apCollection{}
	var err error
	if _, c.Kind, err = f.types(); err != nil {
		return nil, err
	}
	switch c.Kind {
	case "Collection", "OrderedCollection", "CollectionPage", "OrderedCollectionPage":
	default:
		return nil, &apError{Reason: apWrongKind, Field: "type"}
	}
	// An embedded first page may come without an id; a fetched one is checked against its URL by
	// the caller (sameID).
	if _, ok := f["id"]; ok {
		if c.ID, err = f.id("id"); err != nil {
			return nil, err
		}
	}
	c.TotalItems, c.HasTotal = f.count("totalItems")
	for _, p := range []struct {
		name string
		dst  *idOrObject
	}{{"first", &c.First}, {"next", &c.Next}} {
		if *p.dst, err = f.ref(p.name); err != nil {
			return nil, err
		}
		if p.dst.ID != "" {
			if err := apCheckID(p.dst.ID); err != nil {
				return nil, apIn(p.name, err)
			}
		}
	}
	name := "items"
	if _, ok := f["orderedItems"]; ok {
		if _, both := f["items"]; both {
			return nil, &apError{Reason: apDuplicate, Field: "items"}
		}
		name = "orderedItems"
	}
	if c.Items, err = f.refs(name); err != nil {
		return nil, err
	}
	for _, it := range c.Items {
		if err := apCheckID(it.ID); err != nil {
			return nil, apIn(name, err)
		}
	}
	return c, nil
}

// Reading an embedded object as one kind. One given by id only is apByID: the caller fetches it.

func (r idOrObject) embedded() (apFields, error) {
	if r.Obj == nil {
		return nil, apFail(apByID)
	}
	return r.Obj, nil
}

func (r idOrObject) actor() (*apActor, error) {
	f, err := r.embedded()
	if err != nil {
		return nil, apAs("actor", err)
	}
	a, err := actorFrom(f)
	return a, apAs("actor", err)
}

func (r idOrObject) activity() (*apActivity, error) {
	f, err := r.embedded()
	if err != nil {
		return nil, apAs("activity", err)
	}
	a, err := activityFrom(f)
	return a, apAs("activity", err)
}

func (r idOrObject) note() (*apNote, error) {
	f, err := r.embedded()
	if err != nil {
		return nil, apAs("post", err)
	}
	n, err := noteFrom(f)
	return n, apAs("post", err)
}

func (r idOrObject) tombstone() (*apTombstone, error) {
	f, err := r.embedded()
	if err != nil {
		return nil, apAs("tombstone", err)
	}
	t, err := tombstoneFrom(f)
	return t, apAs("tombstone", err)
}

func (r idOrObject) collection() (*apCollection, error) {
	f, err := r.embedded()
	if err != nil {
		return nil, apAs("collection", err)
	}
	c, err := collectionFrom(f)
	return c, apAs("collection", err)
}

/* ---------- federation: remote actors ----------

Accounts on other servers, fetched by their ids, signed by the instance actor, and kept (docs/federation.md,
F5, 6.3, 7.3, 7.4, 9.1, 9.2, 9.3 and section 12). A remote account is a users row with a domain and a row of
ap_actors beside it; nothing it holds can sign in, be given a role, opt in or get a key (the check
users_remote_inert, migration 15), and every statement that lists or looks up members by name says
domain = ''. This version has one way in, remoteActor, which the search by handle (FRD 8) calls with the
id a WebFinger answer gave; remoteKey and remoteKeyFailed are the lookups FWR's inbox will give its
signature verifier (7.4), built here because finding a key by its id and following its owner are this
item's (7.3). No route reaches any of them, and nothing runs until one is called: no goroutine, timer or
row exists before.

What a lookup does, in order: the id is checked (an https id the fetcher may ask, never this instance's own
origin); the row is read and the connection given back; a row younger than 24 hours is the answer; so is a
gone one (410), and an id that failed within its time is answered from memory (with its row, marked stale,
when there is one). Otherwise one fetch per id is made at a time (the first caller's, which every other
caller of that id waits for, each for as long as its own context allows), at most actorFetchers at once,
signed by the instance actor (draft-cavage, as every GET is: 7.1), through fetcher.do (the document kind:
same-origin redirects, 5 s, 1 MB, the two Activity Streams types). Before the first signed request the
origin is frozen (ensureOrigin: the request names /actor#main-key under it, 4.3). The answer is decoded
(decodeActor), its id must be the address it came from after redirects (sameID, 7.3), and its key must be
one the actor names, owned by it and on its origin (7.3). Only then is anything written, in one short
transaction. No database connection and no lock is held while the network is waited on. */

const (
	actorTTL         = 24 * time.Hour   // a cached actor is fetched again after this, on use (7.4, 9.3)
	actorRecheck     = 10 * time.Minute // a failed verification fetches the key again at most this often per actor (7.4)
	actorFailFor     = 10 * time.Minute // a failed fetch is not tried again for this long (7.4)
	actorGoneFor     = 24 * time.Hour   // an id never stored that answered 410: a stored one is marked gone for good
	actorFetchers    = 2                // fetches at once (7.4: at most 2 key fetches at once)
	actorWait        = 5 * time.Second  // the longest wait for one of them; the inbox answers 503 after it (7.4)
	actorFlightMax   = 15 * time.Second // a fetch and its writes, from the wait for a turn to the commit
	actorDocMax      = 1 << 20          // a fetched document (7.5), as fetchRules[fetchDocument] caps it
	remoteActorsMax  = 20000            // cached actors (section 12); the least recently fetched go first
	actorFailsMax    = 10000            // failed ids remembered (about 60 bytes each)
	remoteNameMax    = 255              // bytes of preferredUsername kept, for FRD 8's reverse lookup
	remoteSummaryMax = 2048             // bytes of a summary kept, as HTML, for FWR's htmlToText
	remoteDisplayMax = 64               // characters of a display name
	remotePlainMax   = 64               // bytes of a preferredUsername written into a username as it is
)

// remoteActor is a cached remote actor: its ap_actors row and the users row it belongs to.
type remoteActor struct {
	UserID            int64
	ID                string // the actor id, which never moves: a new id is a new actor
	Kind              string // one of apActorKinds
	Username          string // users.username, "name@host": a key for this row, never a Flowa name and not shown as a handle
	Domain            string // the host of ID, lower case, in its xn-- form when it is in another script
	Name              string // users.display_name: the actor's name as plain text, at most remoteDisplayMax characters
	PreferredUsername string // as the actor wrote it, case kept (empty when over remoteNameMax bytes or holding NUL)
	Summary           string // HTML as received, cut at remoteSummaryMax bytes; FWR's htmlToText turns it into text
	URL, Icon         string // the first usable of each (6.3); the picture is fetched through F6's cache, later
	Inbox             string
	SharedInbox       string
	Followers         string
	MovedTo           string
	Locked            bool   // manuallyApprovesFollowers: a Follow shows as requested until Accept (6.3)
	KeyID             string // the one key kept; "" when the actor names none Flowa can use
	PublicKeyPEM      string
	State             string // ok, suspended (the actor says so: nothing new is stored) or gone (410)
	FetchedAt         time.Time
	ExpiresAt         time.Time
	Stale             bool // the row is past its time and fetching it again failed just now, or within actorFailFor
}

// publicKey parses the kept key, as the verifier will.
func (r *remoteActor) publicKey() (*rsa.PublicKey, error) {
	if r.KeyID == "" {
		return nil, &actorError{Reason: actorNoKey, Host: r.Domain}
	}
	return parsePublicKeyPEM(r.PublicKeyPEM, sigKeyMin)
}

const actorCols = `a.user_id, a.ap_id, a.kind, u.username, u.domain, u.display_name, a.preferred_username, a.summary, a.url, a.icon,
	a.inbox, a.shared_inbox, a.followers_url, a.moved_to, a.locked, a.key_id, a.public_key, a.state, a.fetched_at, a.expires_at`

// actorByIDSQL reads a cached actor by its id, through ap_actors' unique index.
const actorByIDSQL = `SELECT ` + actorCols + ` FROM ap_actors a JOIN users u ON u.id = a.user_id WHERE a.ap_id = $1`

// actorByKeySQL reads the cached actor whose kept key is $1, through ap_actors_key. Two actors of one origin
// naming the same key id is a broken server; the latest fetched wins.
const actorByKeySQL = `SELECT ` + actorCols + ` FROM ap_actors a JOIN users u ON u.id = a.user_id WHERE a.key_id = $1 AND a.key_id <> ''
	ORDER BY a.fetched_at DESC LIMIT 1`

func scanActor(row scanner) (*remoteActor, error) {
	var r remoteActor
	err := row.Scan(&r.UserID, &r.ID, &r.Kind, &r.Username, &r.Domain, &r.Name, &r.PreferredUsername, &r.Summary, &r.URL, &r.Icon,
		&r.Inbox, &r.SharedInbox, &r.Followers, &r.MovedTo, &r.Locked, &r.KeyID, &r.PublicKeyPEM, &r.State, &r.FetchedAt, &r.ExpiresAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// actorReason is why a remote actor could not be had. Each has a fixed phrase, safe to log.
type actorReason uint8

const (
	actorBadID       actorReason = iota + 1 // not an https id the fetcher may ask (apIDOK)
	actorLocal                              // an id on this instance's own origin, which is never fetched
	actorFedRefused                         // federation is refused here (a moved origin, a secret that does not open)
	actorNotReady                           // the instance actor has no key to sign with yet (it is made in the background)
	actorClock                              // this server's clock is earlier than its build, so nothing is signed
	actorBusy                               // no turn among the fetches within actorWait, or at the host: this server's queue
	actorCanceled                           // the caller gave up
	actorRefused                            // the fetcher refused the address: a refused host, a private address, a port
	actorUnreachable                        // the host could not be reached, or answered another status
	actorTimeout                            // the fetch took longer than its time
	actorNotFound                           // 404
	actorGone                               // 410
	actorTooLarge                           // over 1 MB
	actorWrongType                          // not one of the two Activity Streams types
	actorRedirect                           // a redirect to another origin, or too many
	actorUndecodable                        // the document does not decode as an actor (decodeActor refused it)
	actorWrongID                            // the document's id is not the address it came from (7.3)
	actorNoKey                              // the actor names no key Flowa can use with that id (7.3)
)

var actorReasons = [...]string{
	actorBadID:       "not an https id that may be fetched",
	actorLocal:       "an id on this instance's own origin",
	actorFedRefused:  "federation is refused",
	actorNotReady:    "the instance actor has no key yet",
	actorClock:       "the clock is earlier than this build",
	actorBusy:        "too many fetches at once",
	actorCanceled:    "canceled",
	actorRefused:     "the address is refused",
	actorUnreachable: "the server could not be reached",
	actorTimeout:     "timed out",
	actorNotFound:    "not found",
	actorGone:        "gone",
	actorTooLarge:    "the document is too large",
	actorWrongType:   "the document is not an Activity Streams document",
	actorRedirect:    "a redirect to another origin",
	actorUndecodable: "the document is not an actor Flowa can read",
	actorWrongID:     "the document's id is not where it came from",
	actorNoKey:       "no usable key with that id",
}

func (r actorReason) String() string {
	if int(r) < len(actorReasons) && actorReasons[r] != "" {
		return actorReasons[r]
	}
	return "refused"
}

// cached says whether a failure of this kind is remembered for actorFailFor (actorGoneFor for gone): what
// the remote server answered is; this server's own state (no key yet, a busy queue, a canceled caller, an
// unset clock, federation refused) is not, since it says nothing about the actor.
func (r actorReason) cached() bool {
	switch r {
	case actorRefused, actorUnreachable, actorTimeout, actorNotFound, actorGone, actorTooLarge, actorWrongType, actorRedirect, actorUndecodable, actorWrongID, actorNoKey:
		return true
	}
	return false
}

// actorError is every error of this section. Host is the actor's host, cut to a DNS name's length; the
// cause is kept for errors.As (a fetchError, an apError) and never printed.
type actorError struct {
	Reason actorReason
	Host   string
	Cached bool // answered from the memory of failures, with no fetch
	err    error
}

func (e *actorError) Error() string {
	s := "remote actor"
	if e.Host != "" {
		s += " on " + e.Host
	}
	s += ": " + e.Reason.String()
	if e.Cached {
		s += " (remembered)"
	}
	return s
}

func (e *actorError) Unwrap() error { return e.err }

// actorHost is the host of an id for an error: lower case, at most 253 bytes, "" when it has none.
func actorHost(id string) string {
	u, err := url.Parse(id)
	if err != nil {
		return ""
	}
	h := strings.ToLower(u.Hostname())
	if len(h) > 253 {
		h = h[:253]
	}
	return h
}

// fetchReason maps the fetcher's failure kinds onto the cache's.
func fetchReason(err error) actorReason {
	var fe *fetchError
	if !errors.As(err, &fe) {
		return actorUnreachable
	}
	switch fe.Fail {
	case failRefused:
		return actorRefused
	case failTimeout:
		return actorTimeout
	case failCanceled:
		return actorCanceled
	case failTooLarge:
		return actorTooLarge
	case failType:
		return actorWrongType
	case failGone:
		return actorGone
	case failNotFound:
		return actorNotFound
	case failRedirect:
		return actorRedirect
	case failBusy:
		return actorBusy
	}
	return actorUnreachable // failConnect, failStatus
}

// actorFail is a failure remembered: why, and until when.
type actorFail struct {
	reason actorReason
	until  time.Time
}

// actorFlight is the one fetch of an id in progress. Its row and error are written before done is closed.
type actorFlight struct {
	done chan struct{}
	row  *remoteActor
	err  error
}

// lookupOpts are what a lookup asks beyond the id.
type lookupOpts struct {
	force  bool   // a failed verification: fetch again unless fetched within actorRecheck
	want   string // the key id the caller needs; the document may also be that key's own (7.3)
	keyDoc *apKey // the key document already fetched, whose owner is being fetched
	depth  int    // 1 while a key document's owner is fetched: an owner is never followed further
}

// actorCache is the cache of remote actors of one federation. Its rows live in Postgres; in memory it
// keeps only the fetches in flight, the failures remembered and a count of the rows.
type actorCache struct {
	fed   *federation
	fetch *fetcher
	now   func() time.Time // the clock of expiry, the 10 minute rule and the failures; a test sets its own
	built time.Time        // builtAt(): nothing is signed before it (clockTrusted)
	wait  time.Duration    // actorWait
	max   int              // remoteActorsMax
	// refused is the domain policy's list of refused hosts, each with its subdomains (fetchReq.refused).
	// FMD's domain policies fill it; until they exist it is nil, and nothing is refused by domain.
	refused func() []string
	slots   chan struct{} // actorFetchers turns

	mu       sync.Mutex
	flights  map[string]*actorFlight
	fails    map[[16]byte]actorFail // by the first 16 bytes of the SHA-256 of the id, so an id costs 16 bytes here
	count    int                    // remote actors cached, -1 until counted
	evicting bool
	// joined, when set (by a test), hears of every caller of join: whether it started the fetch or waits
	// for one in flight.
	joined func(id string, lead bool)
}

func newActorCache(f *federation, ft *fetcher) *actorCache {
	return &actorCache{fed: f, fetch: ft, now: time.Now, built: builtAt(), wait: actorWait, max: remoteActorsMax,
		slots: make(chan struct{}, actorFetchers), flights: map[string]*actorFlight{}, fails: map[[16]byte]actorFail{}, count: -1}
}

// remoteActor returns the actor with this id, from the cache while it is younger than 24 hours, fetched
// otherwise. It is the one way in of this version (FRD 8's search by handle calls it). An error means
// there is no row: a row whose fetch failed is returned marked Stale, a gone one with State "gone".
func (f *federation) remoteActor(ctx context.Context, id string) (*remoteActor, error) {
	if f.actors == nil {
		return nil, &actorError{Reason: actorFedRefused}
	}
	return f.actors.lookup(ctx, id, lookupOpts{})
}

// remoteKey returns the actor whose key has this id (7.3, 7.4): the cached actor that keeps it, or else
// the document at the key id without its fragment, an actor naming the key or a key document naming its
// owner, whose actor must name the key back. The actor returned keeps exactly this key.
func (f *federation) remoteKey(ctx context.Context, keyID string) (*remoteActor, error) {
	if f.actors == nil {
		return nil, &actorError{Reason: actorFedRefused}
	}
	return f.actors.byKey(ctx, keyID, false)
}

// remoteKeyFailed is remoteKey after a signature did not verify with the key it returned: the actor is
// fetched again, unless it was fetched in the last 10 minutes (7.4), so a key that changed is taken (a
// rotation, which the document itself vouches for: fetched from the actor's origin by its id) and a
// stranger's bad signatures cost at most one fetch per actor in 10 minutes.
func (f *federation) remoteKeyFailed(ctx context.Context, keyID string) (*remoteActor, error) {
	if f.actors == nil {
		return nil, &actorError{Reason: actorFedRefused}
	}
	return f.actors.byKey(ctx, keyID, true)
}

// allowed checks an id before anything else: an https id the fetcher may ask, and not on this instance's
// own origin, which is never fetched (the fetcher refuses it too, by name: 7.8).
func (c *actorCache) allowed(id string) error {
	if !apIDOK(id) {
		return &actorError{Reason: actorBadID, Host: actorHost(id)}
	}
	if u, err := url.Parse(id); err == nil && c.fed.origin.url != "" {
		if o, err := url.Parse(c.fed.origin.url); err == nil && originOf(u) == originOf(o) {
			return &actorError{Reason: actorLocal, Host: actorHost(id)}
		}
	}
	return nil
}

// read reads one cached actor; nil when there is none. The row is scanned and its connection given back
// before it returns.
func (c *actorCache) read(ctx context.Context, q string, arg string) (*remoteActor, error) {
	r, err := scanActor(c.fed.db.QueryRowContext(ctx, q, arg))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return r, err
}

func failKey(id string) [16]byte {
	sum := sha256.Sum256([]byte(id))
	return [16]byte(sum[:16])
}

// failed says whether id failed within its time, and why.
func (c *actorCache) failed(id string, now time.Time) (actorReason, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	f, ok := c.fails[failKey(id)]
	if !ok || !now.Before(f.until) {
		return 0, false
	}
	return f.reason, true
}

// remember keeps a failure for its time. When actorFailsMax are kept, the expired go first, then about a
// tenth of the rest, any of them (map order): a flood of failing ids costs a few refetches, never memory.
func (c *actorCache) remember(id string, r actorReason, now time.Time) {
	if !r.cached() {
		return
	}
	d := actorFailFor
	if r == actorGone {
		d = actorGoneFor
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.fails) >= actorFailsMax {
		for k, f := range c.fails {
			if !now.Before(f.until) {
				delete(c.fails, k)
			}
		}
		for k := range c.fails {
			if len(c.fails) < actorFailsMax*9/10 {
				break
			}
			delete(c.fails, k)
		}
	}
	c.fails[failKey(id)] = actorFail{reason: r, until: now.Add(d)}
}

// cached says whether a lookup is answered without a fetch, and with what.
// A row that keeps another key than the one asked for is fetched again as after a failed verification,
// within the same 10 minute rule: the actor may name several keys, or have changed its key. The owner of
// a key document just fetched is always fetched again (unless gone): its stored row keeps no list of the
// keys it names, and only its document can say whether it names this one; the key document's own fetch
// already went through the flight and the memory of failures.
func (c *actorCache) cached(prev *remoteActor, id string, o lookupOpts) (*remoteActor, bool, error) {
	now := c.now()
	if prev != nil {
		force := o.force || o.want != "" && prev.KeyID != o.want
		switch {
		case prev.State == "gone":
			return prev, true, nil
		case o.keyDoc != nil:
		case force && now.Sub(prev.FetchedAt) < actorRecheck:
			return prev, true, nil
		case !force && now.Before(prev.ExpiresAt):
			return prev, true, nil
		}
	}
	if r, ok := c.failed(id, now); ok {
		if prev != nil {
			prev.Stale = true
			return prev, true, nil
		}
		return nil, true, &actorError{Reason: r, Host: actorHost(id), Cached: true}
	}
	return nil, false, nil
}

// lookup is remoteActor's work, and each step of remoteKey's.
func (c *actorCache) lookup(ctx context.Context, id string, o lookupOpts) (*remoteActor, error) {
	if err := c.allowed(id); err != nil {
		return nil, err
	}
	prev, err := c.read(ctx, actorByIDSQL, id)
	if err != nil {
		return nil, err
	}
	if r, done, err := c.cached(prev, id, o); done {
		return r, err
	}
	return c.join(ctx, id, o)
}

// join waits for the fetch of id in flight, or starts it. The fetch runs on its own context, under the
// federation's (so a stop ends it) with actorFlightMax, not under the first caller's: a caller that gives
// up does not fail the others waiting for the same actor. Each caller waits as long as its own context
// allows.
func (c *actorCache) join(ctx context.Context, id string, o lookupOpts) (*remoteActor, error) {
	c.mu.Lock()
	fl := c.flights[id]
	lead := fl == nil
	if lead {
		fl = &actorFlight{done: make(chan struct{})}
		c.flights[id] = fl
		go func() {
			fctx, cancel := context.WithTimeout(c.fed.ctx, actorFlightMax)
			defer cancel()
			row, err := c.lead(fctx, id, o)
			c.mu.Lock()
			fl.row, fl.err = row, err
			delete(c.flights, id)
			c.mu.Unlock()
			close(fl.done)
		}()
	}
	c.mu.Unlock()
	if c.joined != nil {
		c.joined(id, lead)
	}
	select {
	case <-fl.done:
		if fl.row == nil {
			return nil, fl.err
		}
		r := *fl.row // each caller its own copy
		return &r, fl.err
	case <-ctx.Done():
		return nil, &actorError{Reason: actorCanceled, Host: actorHost(id), err: ctx.Err()}
	}
}

// lead is the one fetch of id: the row read again (a fetch that ended just now may have stored it), the
// document fetched, decoded and checked, and the result stored.
func (c *actorCache) lead(ctx context.Context, id string, o lookupOpts) (*remoteActor, error) {
	prev, err := c.read(ctx, actorByIDSQL, id)
	if err != nil {
		return nil, err
	}
	if r, done, err := c.cached(prev, id, o); done {
		return r, err
	}
	res, err := c.get(ctx, id)
	if err != nil {
		var ae *actorError
		var fe *fetchError
		switch {
		case errors.As(err, &ae):
			return c.failure(ctx, id, prev, ae.Reason, ae.err)
		case errors.As(err, &fe):
			return c.failure(ctx, id, prev, fetchReason(err), err)
		}
		return nil, err // the database
	}
	doc, err := decodeActor(res.Body, actorDocMax)
	if err != nil {
		var ae *apError
		if o.want != "" && o.depth == 0 && errors.As(err, &ae) && ae.Reason == apWrongKind {
			return c.keyDocument(ctx, id, res, o)
		}
		return c.failure(ctx, id, prev, actorUndecodable, err)
	}
	if !sameID(doc.ID, res.URL) {
		return c.failure(ctx, id, prev, actorWrongID, nil)
	}
	return c.store(ctx, doc, o)
}

// keyDocument reads a document that is a key of its own (federation.md 7.3: "some servers keep the key
// at a path of its own"): its id must be where it came from and the key id asked for, its owner on the
// same origin; then the owner is fetched, and must name the key back.
func (c *actorCache) keyDocument(ctx context.Context, id string, res *fetchResult, o lookupOpts) (*remoteActor, error) {
	fields, err := apParse(res.Body, actorDocMax)
	if err != nil {
		return c.failure(ctx, id, nil, actorUndecodable, err)
	}
	keyID, err := fields.id("id")
	if err != nil || keyID == "" || !sameID(keyID, res.URL) || keyID != o.want {
		return c.failure(ctx, id, nil, actorWrongID, err)
	}
	k, ok := apKeyOf(idOrObject{ID: keyID, Obj: fields})
	if !ok || k.Owner == "" || k.PEM == "" || !sameOrigin(k.ID, k.Owner) {
		return c.failure(ctx, id, nil, actorNoKey, nil)
	}
	return c.lookup(ctx, k.Owner, lookupOpts{want: o.want, keyDoc: &k, depth: 1})
}

// failure records a failed fetch. A 410 marks a stored actor gone for good (7.4); every failure the remote
// server is the cause of is remembered for its time. A stored row is still the answer, marked stale (or
// gone): a key that verified yesterday is better than none while its server is down, and FWR's handling
// of an actor's own Delete relies on the cached key (6.3, 7.4).
func (c *actorCache) failure(ctx context.Context, id string, prev *remoteActor, r actorReason, cause error) (*remoteActor, error) {
	now := c.now()
	c.remember(id, r, now)
	if prev == nil {
		return nil, &actorError{Reason: r, Host: actorHost(id), err: cause}
	}
	if r == actorGone {
		if _, err := c.fed.db.ExecContext(ctx, `UPDATE ap_actors SET state = 'gone', fetched_at = $2 WHERE user_id = $1`, prev.UserID, now); err != nil {
			return nil, err
		}
		prev.State, prev.FetchedAt = "gone", now
		return prev, nil
	}
	prev.Stale = true
	return prev, nil
}

// get fetches an actor's document, signed by the instance actor. The origin is frozen first: a signed
// request names the instance actor's key id under it (4.3). The instance actor's key is read before a
// turn is taken, so no connection is held while waiting for one or for the network.
func (c *actorCache) get(ctx context.Context, id string) (*fetchResult, error) {
	host := actorHost(id)
	if c.fed.refusal() != "" {
		return nil, &actorError{Reason: actorFedRefused, Host: host}
	}
	if !clockTrusted(c.now(), c.built) {
		return nil, &actorError{Reason: actorClock, Host: host}
	}
	if err := c.fed.ensureOrigin(ctx); err != nil {
		var moved originMoved
		if errors.As(err, &moved) {
			return nil, &actorError{Reason: actorFedRefused, Host: host, err: err}
		}
		return nil, err
	}
	key, keyID, err := c.fed.privateKey(ctx, instanceActor)
	if errors.Is(err, errNoKey) {
		return nil, &actorError{Reason: actorNotReady, Host: host}
	}
	if err != nil {
		return nil, err
	}
	sign := httpSigner(key, c.fed.origin.url+keyID, sigCavage, c.now, c.built)
	var refused []string
	if c.refused != nil {
		refused = c.refused()
	}
	wait, stop := context.WithTimeout(ctx, c.wait)
	defer stop()
	select {
	case c.slots <- struct{}{}:
	case <-wait.Done():
		if ctx.Err() != nil {
			return nil, &actorError{Reason: actorCanceled, Host: host, err: ctx.Err()}
		}
		return nil, &actorError{Reason: actorBusy, Host: host}
	}
	defer func() { <-c.slots }()
	return c.fetch.do(ctx, fetchReq{kind: fetchDocument, url: id, sign: sign, refused: refused})
}

// actorKey chooses the key a row keeps: the one asked for, else the one kept before, else the first the
// actor names that Flowa can use. A key is usable when the actor names it, its owner is the actor, its id
// is on the actor's origin (7.3: whole origins), and its PEM is an RSA key of 2048 to 8192 bits. A key the
// actor names by id only takes its PEM from the key document already fetched (keyDoc), whose owner must be
// the actor too.
func actorKey(doc *apActor, keys []string, keyDoc *apKey) apKey {
	usable := func(k apKey) (apKey, bool) {
		if keyDoc != nil && k.ID == keyDoc.ID && (k.ByID || k.PEM == "") {
			k.PEM, k.Owner = keyDoc.PEM, keyDoc.Owner
		}
		if k.PEM == "" || k.Owner != doc.ID || !sameOrigin(k.ID, doc.ID) {
			return k, false
		}
		_, err := parsePublicKeyPEM(k.PEM, sigKeyMin)
		return k, err == nil
	}
	for _, want := range keys {
		if want == "" {
			continue
		}
		if k, err := doc.key(want); err == nil {
			if k, ok := usable(k); ok {
				return k
			}
		}
	}
	for _, k := range doc.Keys {
		if k, ok := usable(k); ok {
			return k
		}
	}
	return apKey{}
}

// plainRemoteName says whether a preferredUsername can be written into a username as it is: 1 to 64
// bytes of the characters RFC 3986 leaves unreserved (2.3), which hold every name Mastodon allows (the
// SWICG WebFinger report, "Current implementation rules"). Case is kept: the same report says not to
// assume that remote names ignore it.
func plainRemoteName(s string) bool {
	if s == "" || len(s) > remotePlainMax {
		return false
	}
	return strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~") == ""
}

// remoteUsername is the users.username of a remote actor: its preferredUsername and its host, as
// "name@host". A name that is not plain, or that another actor of the host holds already (a name is not
// an identity, the SWICG WebFinger report says: "SHOULD use the actor id"), gets "+" and twelve hex
// digits of the SHA-256 of the actor id, which no plain name has. Every such name holds an "@", which
// usernameRE never allows, so it can never be a Flowa name, at sign-up or anywhere a name is looked up.
func remoteUsername(pref, host, id string, taken bool) string {
	base := pref
	if !plainRemoteName(pref) {
		base = ""
	}
	if base == "" || taken {
		sum := sha256.Sum256([]byte(id))
		base += "+" + hex.EncodeToString(sum[:6])
	}
	return base + "@" + host
}

// remoteDisplay is a remote name as plain text for users.display_name: AS2's name holds no markup (Core
// 4.7), so only what visibleText takes out of a local name is taken out (control and invisible
// characters; a name that turns the text's direction is dropped whole), runs of space become one, and it
// is cut at remoteDisplayMax characters. With no name, the plain preferredUsername stands in.
func remoteDisplay(name, pref string) string {
	v, ok := visibleText(name)
	if !ok {
		v = ""
	}
	v = strings.Join(strings.FieldsFunc(v, unicode.IsSpace), " ")
	if v == "" && plainRemoteName(pref) {
		v = pref
	}
	if r := []rune(v); len(r) > remoteDisplayMax {
		v = string(r[:remoteDisplayMax])
	}
	return v
}

// remoteText is a remote string as it is kept: NUL removed (Postgres text cannot hold it, and JSON's \u0000
// can), cut at max bytes on the start of a character.
func remoteText(s string, max int) string {
	s = strings.ReplaceAll(s, "\x00", "")
	if len(s) <= max {
		return s
	}
	i := max
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return s[:i]
}

func firstLink(l []apLink) string {
	if len(l) == 0 {
		return ""
	}
	return l[0].Href
}

// store writes a fetched actor in one transaction: its row found by id and locked, updated, or made with
// its users row. A suspended actor keeps what was stored before and gets only its state and times (6.3:
// "stores nothing new from it"); one never seen is stored with its id, kind, name and key, so that an
// Update that lifts the suspension can be verified. A row made by another server at the same moment
// (the unique ap_id) wins, and this one reads it.
func (c *actorCache) store(ctx context.Context, doc *apActor, o lookupOpts) (*remoteActor, error) {
	now := c.now()
	host := actorHost(doc.ID)
	pref := doc.PreferredUsername
	if len(pref) > remoteNameMax || strings.ContainsRune(pref, 0) {
		pref = ""
	}
	state := "ok"
	if doc.Suspended {
		state = "suspended"
	}
	tx, err := c.fed.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var uid int64
	var keptKey string
	err = tx.QueryRowContext(ctx, `SELECT user_id, key_id FROM ap_actors WHERE ap_id = $1 FOR UPDATE`, doc.ID).Scan(&uid, &keptKey)
	known := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	key := actorKey(doc, []string{o.want, keptKey}, o.keyDoc)
	display, summary, link, icon := remoteDisplay(doc.Name, pref), remoteText(doc.Summary, remoteSummaryMax), firstLink(doc.URL), firstLink(doc.Icon)
	if doc.Suspended {
		display, summary, link, icon = "", "", "", ""
	}
	inserted := false
	switch {
	case known && doc.Suspended:
		_, err = tx.ExecContext(ctx, `UPDATE ap_actors SET state = 'suspended', fetched_at = $2, expires_at = $3 WHERE user_id = $1`, uid, now, now.Add(actorTTL))
	case known:
		if _, err = tx.ExecContext(ctx, `UPDATE users SET display_name = $2 WHERE id = $1 AND domain <> ''`, uid, display); err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE ap_actors SET kind = $2, preferred_username = $3, summary = $4, url = $5, icon = $6, inbox = $7, shared_inbox = $8,
				followers_url = $9, moved_to = $10, locked = $11, key_id = $12, public_key = $13, state = 'ok', fetched_at = $14, expires_at = $15 WHERE user_id = $1`,
				uid, doc.Kind, pref, summary, link, icon, doc.Inbox, doc.SharedInbox, doc.Followers, doc.MovedTo, doc.ManuallyApprovesFollowers, key.ID, key.PEM, now, now.Add(actorTTL))
		}
	default:
		for _, taken := range []bool{false, true} {
			err = tx.QueryRowContext(ctx, `INSERT INTO users (username, display_name, pw_hash, domain) VALUES ($1, $2, '!', $3) ON CONFLICT (username) DO NOTHING RETURNING id`,
				remoteUsername(pref, host, doc.ID, taken), display, host).Scan(&uid)
			if !errors.Is(err, sql.ErrNoRows) {
				break
			}
		}
		if err != nil {
			return nil, err
		}
		var n int64
		res, err2 := tx.ExecContext(ctx, `INSERT INTO ap_actors (user_id, ap_id, kind, preferred_username, summary, url, icon, inbox, shared_inbox, followers_url, moved_to, locked,
			key_id, public_key, state, fetched_at, expires_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17) ON CONFLICT (ap_id) DO NOTHING`,
			uid, doc.ID, doc.Kind, pref, summary, link, icon, doc.Inbox, doc.SharedInbox, doc.Followers, doc.MovedTo, doc.ManuallyApprovesFollowers, key.ID, key.PEM, state, now, now.Add(actorTTL))
		if err = err2; err == nil {
			n, _ = res.RowsAffected()
		}
		if err == nil && n == 0 {
			tx.Rollback()
			return c.read(ctx, actorByIDSQL, doc.ID)
		}
		inserted = true
	}
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if inserted {
		c.added(ctx)
	}
	return c.read(ctx, actorByIDSQL, doc.ID)
}

// byKey is remoteKey's work. force is a failed verification (remoteKeyFailed).
func (c *actorCache) byKey(ctx context.Context, keyID string, force bool) (*remoteActor, error) {
	if !httpsURL(keyID) {
		return nil, &actorError{Reason: actorBadID, Host: actorHost(keyID)}
	}
	if err := c.allowed(keyID); err != nil {
		return nil, err
	}
	kept, err := c.read(ctx, actorByKeySQL, keyID)
	if err != nil {
		return nil, err
	}
	id := keyID
	if kept != nil {
		id = kept.ID
	} else if i := strings.IndexByte(id, '#'); i >= 0 {
		id = id[:i]
	}
	r, err := c.lookup(ctx, id, lookupOpts{want: keyID, force: force})
	if err != nil {
		return nil, err
	}
	if r.KeyID != keyID {
		return nil, &actorError{Reason: actorNoKey, Host: actorHost(keyID)}
	}
	return r, nil
}

// evictActorsSQL removes the $1 remote accounts fetched longest ago, through ap_actors_fetched, leaving
// any account something of this server points at (a post, a like, a follow or a block, each asked through
// its index, one row at a time). Nothing can point at a remote account in this version: every statement
// that writes such a row writes the session's own account, or one looked up local only. FWR, which writes
// the first, adds the tables it writes to (comments, notifications, reports) here with their indexes.
const evictActorsSQL = `DELETE FROM users u WHERE u.domain <> '' AND u.id IN (SELECT a.user_id FROM ap_actors a
	WHERE NOT EXISTS (SELECT 1 FROM posts p WHERE p.user_id = a.user_id OFFSET 0)
	AND NOT EXISTS (SELECT 1 FROM likes l WHERE l.user_id = a.user_id OFFSET 0)
	AND NOT EXISTS (SELECT 1 FROM comment_likes l WHERE l.user_id = a.user_id OFFSET 0)
	AND NOT EXISTS (SELECT 1 FROM follows f WHERE f.follower_id = a.user_id OFFSET 0)
	AND NOT EXISTS (SELECT 1 FROM follows f WHERE f.followee_id = a.user_id OFFSET 0)
	AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = a.user_id OFFSET 0)
	AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.target_id = a.user_id OFFSET 0)
	ORDER BY a.fetched_at, a.user_id LIMIT $1)`

// added counts a new remote actor, and when the cache holds more than max, removes the least recently
// fetched, a hundredth of max beyond it at once, so a full cache counts and removes once per that many
// new actors rather than at each. The count is read from the table at the first insert and after each
// removal, so a -purge-remote run beside a running server is caught up with then. Only one removal runs
// at a time, and no lock is held across its statements.
func (c *actorCache) added(ctx context.Context) {
	c.mu.Lock()
	if c.count >= 0 {
		c.count++
	}
	run := (c.count < 0 || c.count > c.max) && !c.evicting
	if run {
		c.evicting = true
	}
	c.mu.Unlock()
	if !run {
		return
	}
	n := -1
	defer func() {
		c.mu.Lock()
		c.count, c.evicting = n, false
		c.mu.Unlock()
	}()
	var have int
	if err := c.fed.db.QueryRowContext(ctx, `SELECT count(*) FROM ap_actors`).Scan(&have); err != nil {
		log.Printf("actors: counting the cache: %v", err)
		return
	}
	if have > c.max {
		res, err := c.fed.db.ExecContext(ctx, evictActorsSQL, have-c.max+max(c.max/100, 1))
		if err != nil {
			log.Printf("actors: removing the least recently fetched: %v", err)
			return
		}
		gone, _ := res.RowsAffected()
		have -= int(gone)
	}
	n = have
}

/* ---------- federation: removing every remote row ---------- */

// purgeCounts are the rows that point at remote accounts, counted before they go with them: each
// table and column that references users with ON DELETE CASCADE and can hold a remote account's id.
var purgeCounts = []struct{ what, sql string }{
	{"cached actor documents", `SELECT count(*) FROM ap_actors`},
	{"posts", `SELECT count(*) FROM posts p JOIN users u ON u.id = p.user_id WHERE u.domain <> ''`},
	{"comments", `SELECT count(*) FROM comments c JOIN users u ON u.id = c.user_id WHERE u.domain <> ''`},
	{"likes", `SELECT count(*) FROM likes l JOIN users u ON u.id = l.user_id WHERE u.domain <> ''`},
	{"comment likes", `SELECT count(*) FROM comment_likes l JOIN users u ON u.id = l.user_id WHERE u.domain <> ''`},
	{"follows", `SELECT count(*) FROM follows f WHERE EXISTS (SELECT 1 FROM users u WHERE u.id IN (f.follower_id, f.followee_id) AND u.domain <> '')`},
	{"blocks", `SELECT count(*) FROM blocks b WHERE EXISTS (SELECT 1 FROM users u WHERE u.id IN (b.user_id, b.target_id) AND u.domain <> '')`},
	{"notifications", `SELECT count(*) FROM notifications n WHERE EXISTS (SELECT 1 FROM users u WHERE u.id IN (n.user_id, n.actor_id) AND u.domain <> '')`},
	{"reports", `SELECT count(*) FROM reports r JOIN users u ON u.id = r.reporter_id WHERE u.domain <> ''`},
}

// purgeRemote removes every remote account and everything that hangs off it (docs/federation.md 14.4), in
// one transaction: the cached documents, and whatever later items let point at a remote account (posts,
// comments, likes, follows, blocks, notifications, reports, all ON DELETE CASCADE). Local rows that only
// named one keep their own meaning: an audit line keeps its action with the account left empty (ON
// DELETE SET NULL). It returns the counts in purgeCounts' order, with the accounts last. Run twice, the
// second removes nothing.
func purgeRemote(ctx context.Context, db *sql.DB) ([]int64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// The remote rows are locked before they are counted, so what is printed is what goes.
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM users WHERE domain <> '' FOR UPDATE`); err != nil {
		return nil, err
	}
	out := make([]int64, 0, len(purgeCounts)+1)
	for _, p := range purgeCounts {
		var n int64
		if err := tx.QueryRowContext(ctx, p.sql).Scan(&n); err != nil {
			return nil, fmt.Errorf("counting %s: %w", p.what, err)
		}
		out = append(out, n)
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM users WHERE domain <> ''`)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	out = append(out, n)
	return out, tx.Commit()
}

// purgeCommand is /nunchaku -purge-remote: the step before ./deploy.sh --rollback to a build older than
// remote accounts, which would list them as members (docs/federation.md 14.4). It needs only
// DATABASE_URL and works in every mode, since a database that federated once may be started in local
// mode later. Like -rotate-keys it does not ask whether the server runs: the image has no shell, so it
// runs beside the server (docker exec), and a server of this version fetches no actor by itself.
func purgeCommand(getenv func(string) string, out io.Writer) error {
	dsn := getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("-purge-remote: DATABASE_URL is not set")
	}
	db, err := openDB(dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	return purgeWith(db, out)
}

// purgeWith removes the remote rows in db and says what it removed.
func purgeWith(db *sql.DB, out io.Writer) error {
	n, err := purgeRemote(context.Background(), db)
	if err != nil {
		return fmt.Errorf("-purge-remote: nothing removed: %w", err)
	}
	parts := make([]string, len(purgeCounts))
	for i, p := range purgeCounts {
		parts[i] = fmt.Sprintf("%d %s", n[i], p.what)
	}
	fmt.Fprintf(out, "removed %d remote accounts, and with them %s. Local accounts and their posts are as they were.\n", n[len(n)-1], strings.Join(parts, ", "))
	return nil
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
	// 4: bookmarks. ref is "prop:skill" for a skill, the combo's id from the progress document for a
	// combo, and the post id for a post; a post bookmark goes when the post does (housekeep).
	`CREATE TABLE IF NOT EXISTS bookmarks (
		user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		kind TEXT NOT NULL CHECK (kind IN ('skill', 'combo', 'post')),
		ref TEXT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		PRIMARY KEY (user_id, kind, ref)
	)`,
	// 5: the account remembers when it was last used (sessions go at a suspension or a password
	// reset), and one admin is the owner. The database itself keeps the owner an admin and refuses
	// a second one. Written so that running it twice changes nothing.
	`ALTER TABLE users ADD COLUMN IF NOT EXISTS last_seen TIMESTAMPTZ;
	ALTER TABLE users ADD COLUMN IF NOT EXISTS owner BOOLEAN NOT NULL DEFAULT false;
	ALTER TABLE users DROP CONSTRAINT IF EXISTS users_owner_is_admin;
	ALTER TABLE users ADD CONSTRAINT users_owner_is_admin CHECK (NOT owner OR role = 'admin');
	CREATE UNIQUE INDEX IF NOT EXISTS users_one_owner ON users (owner) WHERE owner;
	UPDATE users u SET last_seen = (SELECT max(GREATEST(s.created_at, s.last_seen)) FROM sessions s WHERE s.user_id = u.id) WHERE u.last_seen IS NULL;
	UPDATE users SET owner = true WHERE id = (SELECT id FROM users WHERE role = 'admin' ORDER BY id LIMIT 1) AND NOT EXISTS (SELECT 1 FROM users WHERE owner)`,
	// 6: indexes for what the plans showed (TestDBQueryPlans, with 10,000 posts and 500 members). The
	// timeline looked for the viewer's own likes, of posts and of comments, by reading every like
	// there is; the admin dashboard counted posts and comments per week by reading all of them for
	// each week. Indexes only: nothing a row holds changes, so going back to an earlier build is safe.
	`CREATE INDEX IF NOT EXISTS likes_user ON likes (user_id, post_id);
	CREATE INDEX IF NOT EXISTS comment_likes_user ON comment_likes (user_id, comment_id);
	CREATE INDEX IF NOT EXISTS posts_created ON posts (created_at);
	CREATE INDEX IF NOT EXISTS comments_created ON comments (created_at)`,
	// 7: the week a pair of skills first got a shared Works or Blocked mark, for the dashboard's count
	// toward the fit of the suggestion weights. Pairs marked before this have none and are counted
	// without a week. One new column: going back to an earlier build is safe.
	`ALTER TABLE transition_counts ADD COLUMN IF NOT EXISTS first_week DATE`,
	// 8: member blocks. user_id has blocked target_id; nobody blocks themself, and a block goes with
	// either account. The primary key answers "has this reader blocked that author" in the timeline,
	// and blocks_target the other way round, "who has blocked me". A new table only: going back to an
	// earlier build is safe, and that build simply shows everything again.
	`CREATE TABLE IF NOT EXISTS blocks (
		user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		target_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		PRIMARY KEY (user_id, target_id),
		CONSTRAINT blocks_not_self CHECK (user_id <> target_id)
	);
	CREATE INDEX IF NOT EXISTS blocks_target ON blocks (target_id, user_id)`,
	// 9: follows between local accounts. follower_id follows followee_id; nobody follows themself, and
	// a follow goes with either account. The primary key answers "whom do I follow" for Home and the
	// counts, and follows_followee "who follows me". The design's state and ap_id columns are for
	// follows with other servers, which wait for an accepted reply; they come with the migration of
	// that item (FWR), not before anything reads them. A new table only: going back to an earlier
	// build is safe, and that build has no Home.
	`CREATE TABLE IF NOT EXISTS follows (
		follower_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		followee_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		PRIMARY KEY (follower_id, followee_id),
		CONSTRAINT follows_not_self CHECK (follower_id <> followee_id)
	);
	CREATE INDEX IF NOT EXISTS follows_followee ON follows (followee_id, follower_id)`,
	// 10: settings an admin changes at runtime (docs/federation.md, F1 and 9.1), one row a key. This
	// version writes three keys, description, contact and rules (instanceParts); later items add their
	// own. updated_by is the account that saved the row, kept null when that account is deleted. A new
	// table only, and nothing writes a row until an admin saves: going back to an earlier build is safe,
	// and that build simply shows no rules and no about page.
	`CREATE TABLE IF NOT EXISTS instance_settings (
		key TEXT PRIMARY KEY,
		value JSONB NOT NULL,
		updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		updated_by BIGINT REFERENCES users(id) ON DELETE SET NULL
	)`,
	// 11: requests to join, while registration is by approval. A table of their own rather than a
	// column on users, so that no query that reads users can show, count or sign in a person nobody has
	// approved. The username is unique here, and sign-up checks both tables, so a name that waits is
	// taken. A new table only: going back to an earlier build is safe, and that build simply never
	// sees the requests (the people waiting cannot sign in there either).
	`CREATE TABLE IF NOT EXISTS join_requests (
		id BIGSERIAL PRIMARY KEY,
		username TEXT NOT NULL UNIQUE,
		display_name TEXT NOT NULL,
		pw_hash TEXT NOT NULL,
		reason TEXT NOT NULL DEFAULT '',
		created_at TIMESTAMPTZ NOT NULL DEFAULT now()
	);
	CREATE INDEX IF NOT EXISTS join_requests_created ON join_requests (created_at)`,
	// 12: the key pairs of local actors (docs/federation.md, 5.7 and 9.1), written only in a mode that
	// federates. user_id is the owner, NULL for the instance actor; NULLS NOT DISTINCT makes one plain
	// unique constraint hold one key per owner for that NULL too, where a partial index would need a
	// second index for it (Postgres 15 or later; compose.yml pins 16). key_id is the part after the origin, such as
	// /actor#main-key: the origin is recorded once and frozen (instance_settings), so it is not repeated
	// in every row. public_key is an SPKI PEM; private_sealed is the PKCS#8 key sealed with AES-256-GCM
	// under FLOWA_KEY_SECRET, as text that names its scheme (v1.), never the key itself, so the daily
	// backup holds no usable private key. A new table only: going back to an earlier build is safe.
	`CREATE TABLE IF NOT EXISTS ap_keys (
		key_id TEXT PRIMARY KEY,
		user_id BIGINT REFERENCES users(id) ON DELETE CASCADE,
		public_key TEXT NOT NULL,
		private_sealed TEXT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		CONSTRAINT ap_keys_one_per_owner UNIQUE NULLS NOT DISTINCT (user_id)
	)`,
	// 13: a member's opt-in to sharing their profile beyond this server (docs/federation.md, F2 and 11),
	// off for everyone, existing members included. Read only in a mode that federates (WebFinger, and the
	// switch in Settings). One column with a constant default: going back to an earlier build is safe,
	// and that build simply does not read it.
	`ALTER TABLE users ADD COLUMN IF NOT EXISTS federates BOOLEAN NOT NULL DEFAULT false`,
	// 14: who may read a post (docs/federation.md, F2, F3 and 9.1), and the names that are never given out
	// again (5.2). posts.audience is 'local' (This server) for every post there is and for every post made
	// outside a mode that federates; 'public' only when a member who opted in chooses it. The check names the
	// four audiences of the design note, so a later item adds none by a migration; NOT VALID means the rows
	// already there are not read to check them (they all hold the default), so the column and the check are
	// changes to the catalog only. posts_public lists a member's Public posts newest first for their public
	// page; it holds no row until someone posts one, and building it reads posts once. retired_usernames
	// holds the name of a deleted account that had opted in, whose actor id other servers may have stored.
	// Additive: an older build ignores the column and the table, and shows no post outside the server.
	`ALTER TABLE posts ADD COLUMN IF NOT EXISTS audience TEXT NOT NULL DEFAULT 'local';
	ALTER TABLE posts DROP CONSTRAINT IF EXISTS posts_audience;
	ALTER TABLE posts ADD CONSTRAINT posts_audience CHECK (audience IN ('local', 'public', 'unlisted', 'followers')) NOT VALID;
	CREATE INDEX IF NOT EXISTS posts_public ON posts (user_id, id DESC) WHERE audience = 'public' AND hidden_at IS NULL AND kind = 'post';
	CREATE TABLE IF NOT EXISTS retired_usernames (
		username TEXT PRIMARY KEY,
		retired_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	// 15: remote accounts (docs/federation.md, F5, 9.1 and 9.2), the first rows about other servers. A remote
	// account is a users row whose domain is the host of its actor id; '' is local, and every row there is
	// keeps '', so every existing account stays local. Its username is "name@host", which usernameRE can never
	// make, so the one unique index on username keeps holding local and remote names apart and no local name
	// can meet a remote one; the actor id, not the name, is what identifies it (ap_actors.ap_id). The check
	// keeps a remote row inert whatever writes it, this build or an older one after a rollback: no password
	// hash checkPassword can match ('!'), no role above user, never the owner, never opted in, and a name that
	// ends in "@" and its domain. users_local is the index the people list and the counts read local members
	// through. ap_actors holds what Flowa reads of a remote actor (6.3) and its one key, kept for 24 hours
	// (7.4); state is ok, suspended (Mastodon's flag, 6.3) or gone (410). Adding the column and the check reads
	// users once (a row or a few per member); the rest are new. Additive: an older build ignores the column and
	// the table, but would list remote rows as members, which is why -purge-remote exists (14.4).
	`ALTER TABLE users ADD COLUMN IF NOT EXISTS domain TEXT NOT NULL DEFAULT '';
	ALTER TABLE users DROP CONSTRAINT IF EXISTS users_remote_inert;
	ALTER TABLE users ADD CONSTRAINT users_remote_inert CHECK (domain = '' OR (pw_hash = '!' AND role = 'user' AND NOT owner AND NOT federates
		AND length(username) > length(domain) + 1 AND right(username, length(domain) + 1) = '@' || domain));
	CREATE INDEX IF NOT EXISTS users_local ON users (id) WHERE domain = '';
	CREATE TABLE IF NOT EXISTS ap_actors (
		user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
		ap_id TEXT NOT NULL UNIQUE,
		kind TEXT NOT NULL CHECK (kind IN ('Application', 'Group', 'Organization', 'Person', 'Service')),
		preferred_username TEXT NOT NULL DEFAULT '',
		summary TEXT NOT NULL DEFAULT '',
		url TEXT NOT NULL DEFAULT '',
		icon TEXT NOT NULL DEFAULT '',
		inbox TEXT NOT NULL DEFAULT '',
		shared_inbox TEXT NOT NULL DEFAULT '',
		followers_url TEXT NOT NULL DEFAULT '',
		moved_to TEXT NOT NULL DEFAULT '',
		locked BOOLEAN NOT NULL DEFAULT false,
		key_id TEXT NOT NULL DEFAULT '',
		public_key TEXT NOT NULL DEFAULT '',
		state TEXT NOT NULL DEFAULT 'ok' CHECK (state IN ('ok', 'suspended', 'gone')),
		fetched_at TIMESTAMPTZ NOT NULL,
		expires_at TIMESTAMPTZ NOT NULL
	);
	CREATE INDEX IF NOT EXISTS ap_actors_key ON ap_actors (key_id) WHERE key_id <> '';
	CREATE INDEX IF NOT EXISTS ap_actors_fetched ON ap_actors (fetched_at, user_id)`,
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
		{"bookmarks", `DELETE FROM bookmarks b WHERE b.kind = 'post' AND NOT EXISTS (SELECT 1 FROM posts p WHERE p.id::text = b.ref)`},
		// A request to join that nobody answered in joinWaitDays is let go, as a decline would, so a
		// forgotten queue frees its names and its room under the cap by itself.
		{"requests", `DELETE FROM join_requests WHERE created_at < now() - make_interval(days => ` + strconv.Itoa(joinWaitDays) + `)`},
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
		n, err := a.housekeep()
		if err != nil {
			log.Print(err)
		} else if n["sessions"]+n["notifications"]+n["previews"]+n["bookmarks"] > 0 {
			log.Printf("housekeeping: removed %d expired sessions, %d old notifications, %d unused link previews, %d bookmarks of deleted posts", n["sessions"], n["notifications"], n["previews"], n["bookmarks"])
		}
		// A line of its own, so the line above reads as it always has.
		if n["requests"] > 0 {
			log.Printf("housekeeping: removed %d requests to join that waited more than %d days", n["requests"], joinWaitDays)
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
	err := a.db.QueryRow(`SELECT id FROM users WHERE username = $1 AND domain = ''`, name).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return a.makeAdmin(name, hashPassword(pw))
	}
	return id, err
}

// makeAdmin makes the ADMIN_USER account under the name's lock (nameLockSQL), so the check that the name
// is not retired and the insert see a delete that retired it meanwhile.
func (a *app) makeAdmin(name, hash string) (int64, error) {
	tx, err := a.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(nameLockSQL, name); err != nil {
		return 0, err
	}
	// A retired name, or the instance's own host, is taken for the operator too. The start goes on
	// without the account (a deleted admin used to come back at every start, and a server that will not
	// start for it would be worse) unless no account exists at all, when there would be nobody to run it.
	var retired, someone bool
	if err := tx.QueryRow(`SELECT `+retiredSQL+`, EXISTS (SELECT 1 FROM users WHERE domain = '')`, name).Scan(&retired, &someone); err != nil {
		return 0, err
	}
	if retired || a.reservedName(name) {
		if !someone {
			return 0, fmt.Errorf("ADMIN_USER=%s: that username is taken. Choose another one for the first account", name)
		}
		log.Printf("ADMIN_USER=%s: that username is taken, so no account is made for it", name)
		return 0, nil
	}
	// The account the operator names is made whatever the registration setting says, and a
	// request to join that waits under the same name gives way to it: the name is the operator's.
	var id int64
	var dropped bool
	err = tx.QueryRow(`WITH gone AS (DELETE FROM join_requests WHERE username = $1 RETURNING 1)
		INSERT INTO users (username, display_name, pw_hash, role) SELECT $1, $2, $3, 'admin' RETURNING id, EXISTS (SELECT 1 FROM gone)`,
		name, "Admin", hash).Scan(&id, &dropped)
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		return 0, err
	}
	log.Printf("created admin account %q", name)
	if dropped {
		log.Printf("removed the request to join that waited under the name %q", name)
	}
	return id, nil
}

/* ---------- static files ----------

The page's files are compiled into the binary (web/). At start each one is hashed and compressed once;
after that a request is a look in a table and a write. A file answers to two names:

  - its own (/app.js), sent with an ETag and "no-cache": the browser keeps it, asks each time, and is
    told 304 with no body while the file is the same. The page's scripts fetch by these names (the
    clips and their lists, the 3D figure and its model, the tour, the avatar parts), and so do
    bookmarks and the browser tests.
  - its name with the first 16 hex characters of the SHA-256 of its bytes in it
    (/app.0123456789abcdef.js), sent as "immutable" for a year: another file is another name, so the
    browser never asks again. index.html is rewritten at start to name its scripts, its style sheet
    and its icon this way.

index.html is "no-cache" under its one name, and it is the only way to the hashed names. The web
manifest stays under its own name too, because some browsers know an installed app by that address.
A baked clip is already named by the hash of its bytes (sim/bake.js); that is checked here, and a clip
that passes is sent as "immutable" under the name it has. The catalogs are not in the table:
handleCatalogFile serves them from the database.

gzip is made here, at start, from the embedded bytes. Brotli is made at build time by brotli.js into
brotli/, because the standard library has no encoder for it; a build that did not run the script
(go run, go test) has none and sends gzip, and a browser that takes neither gets the file as it is.
A compressed copy is used only when it is at least a tenth smaller than the file.

After a deploy, a page loaded from the build before may still ask for that build's hashed names: a tab
the browser restores from its cache is not asked whether its page is still current. The new binary
does not have those bytes. So each build writes the files its page names into the data folder
(keptName), and the build after it answers them from there for keepOldFor after it first started,
then says 404. What is sent is the old file, so an old page gets scripts that belong together and
never a mix of two builds.

The data folder can be written to, the binary cannot, so the record is not believed like the binary
is. Only scripts and style sheets are taken from it, each only under the name its own bytes hash to:
never a page, and not the icon, which opened by itself is a document that can run a script. An old
name is sent with the CSP of an API answer and for a browser to keep for five minutes, not for good,
so what a record said once is gone with the record. */

const (
	cacheAsk     = "no-cache"                            // keep it, and ask each time whether it changed
	cacheForGood = "public, max-age=31536000, immutable" // one year, and never ask
	cacheOld     = "private, max-age=300"                // an old name: this browser alone, for five minutes
	keepOldFor   = time.Hour                             // how long the names of the build before are answered
	keptName     = "static-kept.json.gz"                 // the record of those builds, in the data folder
	keptBuilds   = 3                                     // earlier builds answered at once, when deploys follow each other within the hour
	keptLimit    = 32 << 20                              // bytes read from the record at most
)

// asset is one file as it is sent.
type asset struct {
	body  []byte // the file
	gz    []byte // gzip of it; nil when that is not a tenth smaller
	br    []byte // brotli of it, from brotli.js; nil when the build has none
	ctype string
	hash  string // the first 16 hex characters of the SHA-256 of body: the ETag, and the part in the hashed name
}

// kept is a file of an earlier build and when its name stops being answered.
type kept struct {
	*asset
	until time.Time
}

// static is the table the page's files are served from. Nothing in it changes after start.
type static struct {
	plain  map[string]*asset // by the file's own name, "/app.js"
	hashed map[string]*asset // by its name with the hash in it, "/app.0123456789abcdef.js"
	fixed  map[string]bool   // own names that are the hash of the bytes already (the baked clips)
	old    map[string]kept   // hashed names of earlier builds, from the data folder
	told   []string          // the hashed names index.html names
	build  string            // the hash of index.html as it is sent: it changes when any file it names does
	now    func() time.Time  // the clock, for the hour an old name lasts; a test sets it
	nBr    int               // files with a brotli copy
	saved  [3]int            // bytes of all files: as they are, with gzip where it is used, with brotli where it is used
}

// staticTypes are the content types of the kinds of file in web/. A host's own list of types is not
// asked for these, so the answer is the same on every machine.
var staticTypes = map[string]string{
	".html": "text/html; charset=utf-8", ".js": "text/javascript; charset=utf-8", ".css": "text/css; charset=utf-8",
	".json": "application/json", ".svg": "image/svg+xml", ".webmanifest": "application/manifest+json",
	".glb": "model/gltf-binary",
}

// packed are the kinds of file that are compressed already; compressing them again gains nothing.
var packed = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".avif": true,
	".woff": true, ".woff2": true, ".mp4": true, ".webm": true}

// worth says whether a compressed copy of z bytes is worth sending instead of the n bytes of the file.
func worth(z, n int) bool { return z > 0 && z*10 <= n*9 }

// newAsset hashes one file and makes its gzip. name is its path, for the content type.
func newAsset(name string, body []byte) *asset {
	sum := sha256.Sum256(body)
	ext := path.Ext(name)
	f := &asset{body: body, hash: hex.EncodeToString(sum[:8]), ctype: staticTypes[ext]}
	if f.ctype == "" {
		f.ctype = mime.TypeByExtension(ext)
	}
	if f.ctype == "" {
		f.ctype = http.DetectContentType(body)
	}
	if !packed[ext] {
		var b bytes.Buffer
		zw := gzip.NewWriter(&b) // the default level: the best one takes twice as long for half a percent fewer bytes
		zw.Write(body)
		zw.Close()
		if worth(b.Len(), len(body)) {
			f.gz = b.Bytes()
		}
	}
	return f
}

// hashedName puts a file's hash into its name, before the extension: app.js becomes app.<hash>.js.
func hashedName(name, hash string) string {
	ext := path.Ext(name)
	return strings.TrimSuffix(name, ext) + "." + hash + ext
}

// keepable says whether a file may go into the record of a build and be taken from it: a script or a
// style sheet under its hashed name, with the bytes that hash stands for.
func keepable(name string, body []byte) bool {
	m := keptForm.FindStringSubmatch(name)
	if m == nil {
		return false
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:8]) == m[1]
}

var (
	keptForm = regexp.MustCompile(`\.([0-9a-f]{16})\.(js|css)$`)
	// A file index.html asks for: src="..." or href="...".
	pageRef = regexp.MustCompile(`\b(src|href)="([^"]*)"`)
	// A baked clip: sim/bake.js names it by the first 12 hex characters of the SHA-1 of its JSON.
	clipName = regexp.MustCompile(`^clips/([0-9a-f]{12})\.json$`)
)

// selfNamed says whether a file is a baked clip that is named by the hash of its own bytes.
func selfNamed(name string, body []byte) bool {
	m := clipName.FindStringSubmatch(name)
	if m == nil {
		return false
	}
	sum := sha1.Sum(body) // not for secrecy: it is the hash sim/bake.js names the file by
	return hex.EncodeToString(sum[:6]) == m[1]
}

// newStatic builds the table from the page's files and, when the build made them, their brotli copies
// (br holds <path>.<hash of the file>.br; nil for none). A brotli copy of other bytes than the file's
// has another hash in its name and is never found, so a stale one cannot be sent.
func newStatic(web, br fs.FS) (*static, error) {
	s := &static{plain: map[string]*asset{}, hashed: map[string]*asset{}, fixed: map[string]bool{}, old: map[string]kept{}, now: time.Now}
	var names []string
	err := fs.WalkDir(web, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && !strings.HasPrefix(p, "catalog/") {
			names = append(names, p)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	// Compressing is most of the work; each file is its own job, one job per processor at a time.
	files, errs := make([]*asset, len(names)), make([]error, len(names))
	var wg sync.WaitGroup
	turn := make(chan struct{}, runtime.NumCPU())
	for i, p := range names {
		turn <- struct{}{}
		wg.Go(func() {
			defer func() { <-turn }()
			body, err := fs.ReadFile(web, p)
			if err != nil {
				errs[i] = err
				return
			}
			f := newAsset(p, body)
			if br != nil && !packed[path.Ext(p)] && p != "index.html" {
				if z, err := fs.ReadFile(br, p+"."+f.hash+".br"); err == nil && worth(len(z), len(body)) {
					f.br = z
				}
			}
			files[i] = f
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	for i, p := range names {
		f := files[i]
		s.plain["/"+p] = f
		if p == "index.html" {
			continue
		}
		s.hashed["/"+hashedName(p, f.hash)] = f
		if selfNamed(p, f.body) {
			s.fixed["/"+p] = true
		}
		s.count(f)
	}
	// The page, naming its files by their hashed names. A reference written another way (a leading
	// slash, a query) is left as it is and keeps working under the plain name.
	if page, ok := s.plain["/index.html"]; ok {
		body := pageRef.ReplaceAllFunc(page.body, func(m []byte) []byte {
			sub := pageRef.FindSubmatch(m)
			ref := string(sub[2])
			f, ok := s.plain["/"+ref]
			if !ok || ref == "index.html" || path.Ext(ref) == ".webmanifest" {
				return m
			}
			name := hashedName(ref, f.hash)
			s.told = append(s.told, name)
			return []byte(string(sub[1]) + `="` + name + `"`)
		})
		page = newAsset("index.html", body)
		s.plain["/index.html"], s.build = page, page.hash
		s.count(page)
	}
	return s, nil
}

// count adds one file to the sizes the start line reports.
func (s *static) count(f *asset) {
	gz, br := len(f.body), len(f.body)
	if f.gz != nil {
		gz, br = len(f.gz), len(f.gz)
	}
	if f.br != nil {
		br = len(f.br)
		s.nBr++
	}
	s.saved[0] += len(f.body)
	s.saved[1] += gz
	s.saved[2] += br
}

// keptBuild is the files one build's page names, as the record in the data folder holds them.
type keptBuild struct {
	Build string            `json:"build"`          // the hash of that build's index.html
	Until time.Time         `json:"until,omitzero"` // when its names stop being answered; not set for the build that runs
	Files map[string][]byte `json:"files"`          // hashed name, as the page writes it, to the file
}

// keepOld reads the record of earlier builds from the data folder, takes their files into the table
// until their time, and writes the record back with this build in it for the one that comes next.
// It returns how many old names it answers. An error costs old names and nothing else, and says
// whose: a record that cannot be read, those of pages loaded before this start; one that cannot be
// written, those of pages loaded from this build, after the next one starts.
func (s *static) keepOld(dir string) (int, error) {
	if s.build == "" {
		return 0, nil
	}
	file := filepath.Join(dir, keptName)
	builds, readErr := readKept(file)
	if readErr != nil {
		readErr = fmt.Errorf("the record of the build before cannot be read, so pages loaded before this start get 404 for their old files: %w", readErr)
	}
	now, changed, listed := s.now(), readErr != nil, false
	var next []keptBuild
	for _, b := range builds {
		if b.Build == s.build {
			listed = listed || b.Until.IsZero()
			continue
		}
		// The hour starts when the build after it first starts, and a restart does not start it again.
		// A time further away than that was written by a clock that was wrong, or not by this program.
		if latest := now.Add(keepOldFor); b.Until.IsZero() || b.Until.After(latest) {
			b.Until, changed = latest, true
		}
		if !b.Until.After(now) {
			changed = true
			continue
		}
		next = append(next, b)
	}
	slices.SortFunc(next, func(a, b keptBuild) int { return b.Until.Compare(a.Until) })
	if len(next) > keptBuilds {
		next, changed = next[:keptBuilds], true
	}
	// The builds are in order, the one answered longest first, so a name two of them share keeps
	// the later time.
	for _, b := range next {
		for name, body := range b.Files {
			_, current := s.hashed["/"+name]
			if _, twice := s.old["/"+name]; current || twice || !keepable(name, body) {
				continue
			}
			s.old["/"+name] = kept{newAsset(name, body), b.Until}
		}
	}
	if listed && !changed {
		return len(s.old), readErr
	}
	mine := keptBuild{Build: s.build, Files: map[string][]byte{}}
	for _, name := range s.told {
		if body := s.hashed["/"+name].body; keepable(name, body) {
			mine.Files[name] = body
		}
	}
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	err := json.NewEncoder(zw).Encode(append(next, mine))
	if err == nil {
		err = zw.Close()
	}
	if err == nil {
		err = writeAtomic(file, out.Bytes())
	}
	if err != nil {
		err = fmt.Errorf("this build's page files cannot be written for the build after it, so pages loaded from this one will get 404 for their old files after the next deploy: %w", err)
	}
	return len(s.old), errors.Join(readErr, err)
}

// readKept reads the record of earlier builds; none yet is not an error.
func readKept(file string) ([]keptBuild, error) {
	f, err := os.Open(file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	var builds []keptBuild
	if err := json.NewDecoder(io.LimitReader(zr, keptLimit)).Decode(&builds); err != nil {
		return nil, err
	}
	return builds, nil
}

// loadStatic builds the table from the files compiled in, takes over the names of the build before,
// and says in the log what it made and how long that took.
func loadStatic(dataDir string) (*static, error) {
	began := time.Now()
	web, err := fs.Sub(webFS, "web")
	if err != nil {
		return nil, err
	}
	br, err := fs.Sub(brotliFS, "brotli")
	if err != nil {
		return nil, err
	}
	s, err := newStatic(web, br)
	if err != nil {
		return nil, err
	}
	old, err := s.keepOld(dataDir)
	if err != nil {
		for _, what := range strings.Split(err.Error(), "\n") { // reading and writing can both fail: a line each
			log.Printf("static files: %s", what)
		}
	}
	log.Printf("static files: %d hashed and compressed in %d ms: %d KB as they are, %d KB with gzip, %d KB with brotli (%d copies from the build); index.html names %d by hash, %d clips are named by their own; old names still answered: %d",
		len(s.plain), time.Since(began).Milliseconds(), s.saved[0]/1000, s.saved[1]/1000, s.saved[2]/1000, s.nBr, len(s.told), len(s.fixed), old)
	return s, nil
}

// find looks a path up: the file, and how long a browser may keep it. nil when there is none. An old
// name is the one answer that did not come out of the binary; cacheOld tells handleStatic so.
func (s *static) find(p string) (*asset, string) {
	if p == "/" {
		p = "/index.html"
	}
	if f, ok := s.plain[p]; ok {
		if s.fixed[p] {
			return f, cacheForGood
		}
		return f, cacheAsk
	}
	if f, ok := s.hashed[p]; ok {
		return f, cacheForGood
	}
	if k, ok := s.old[p]; ok && s.now().Before(k.until) {
		return k.asset, cacheOld
	}
	return nil, ""
}

// acceptsEncoding reads an Accept-Encoding header: whether brotli and gzip may be sent. A coding the
// header gives q=0 may not, and "*" speaks for the codings it does not name.
func acceptsEncoding(header string) (br, gz bool) {
	q := map[string]float64{}
	for _, part := range strings.Split(header, ",") {
		name, params, _ := strings.Cut(part, ";")
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		weight := 1.0
		for _, p := range strings.Split(params, ";") {
			if k, v, ok := strings.Cut(p, "="); ok && strings.EqualFold(strings.TrimSpace(k), "q") {
				f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
				if err != nil {
					f = 0 // a weight that cannot be read is not a yes
				}
				weight = f
			}
		}
		q[name] = weight
	}
	can := func(name string) bool {
		if w, ok := q[name]; ok {
			return w > 0
		}
		return q["*"] > 0
	}
	return can("br"), can("gzip") || can("x-gzip")
}

// etagIn says whether an If-None-Match header names the tag.
func etagIn(header, tag string) bool {
	for _, t := range strings.Split(header, ",") {
		if t = strings.TrimPrefix(strings.TrimSpace(t), "W/"); t == tag || t == "*" {
			return true
		}
	}
	return false
}

// send answers with the file: brotli when the browser takes it and the build has it, else gzip, else
// the file as it is. Each of the three has its own ETag. A request for a part of the file (Range) is
// answered from the file as it is, where the positions mean what the browser thinks they mean.
func (f *asset) send(w http.ResponseWriter, r *http.Request, cache string) {
	h := w.Header()
	h.Set("Content-Type", f.ctype)
	h.Set("Cache-Control", cache)
	h.Add("Vary", "Accept-Encoding") // added to, not set: in single mode / also varies on the cookie
	var body []byte
	enc := ""
	if r.Header.Get("Range") == "" {
		br, gz := acceptsEncoding(strings.Join(r.Header.Values("Accept-Encoding"), ","))
		switch {
		case br && f.br != nil:
			body, enc = f.br, "br"
		case gz && f.gz != nil:
			body, enc = f.gz, "gzip"
		}
	}
	if enc == "" {
		// ServeContent does the rest: If-None-Match, Range, If-Range, HEAD and the length.
		h.Set("ETag", `"`+f.hash+`"`)
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(f.body))
		return
	}
	tag := `"` + f.hash + "-" + enc + `"`
	h.Set("ETag", tag)
	if etagIn(r.Header.Get("If-None-Match"), tag) {
		h.Del("Content-Type")
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Encoding", enc)
	h.Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method != http.MethodHead {
		w.Write(body)
	}
}

// handleStatic answers every path no other route takes: a file from the table with the page's CSP, or 404.
func (a *app) handleStatic(s *static) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f, cache := s.find(r.URL.Path)
		if f == nil {
			http.NotFound(w, r)
			return
		}
		if cache != cacheOld { // an old name keeps the CSP every API answer has, which allows nothing
			w.Header().Set("Content-Security-Policy", csp(a.flagsCached()["videoEmbeds"]))
		}
		f.send(w, r, cache)
	}
}

// routeMux is the mux and the patterns registered on it, in order. The standard mux does not give
// its patterns back, and TestDBLocalModeUnchanged compares them with the list in
// testdata/routes-local.txt, so that no route appears on an instance that asked for none.
type routeMux struct {
	*http.ServeMux
	patterns []string
}

func (m *routeMux) HandleFunc(pattern string, h func(http.ResponseWriter, *http.Request)) {
	m.patterns = append(m.patterns, pattern)
	m.ServeMux.HandleFunc(pattern, h)
}

// notSingle closes a route in single mode with 403 and singleOnly, before the session is read: the
// routes that list, approve or decline requests to join, and those that delete an account, change its
// role or hand over ownership, any of which could break the rule of one account, the owner. In local
// mode it only calls the handler.
func (a *app) notSingle(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.mode == modeSingle {
			fail(w, http.StatusForbidden, singleOnly)
			return
		}
		h(w, r)
	}
}

// routes builds the HTTP handler: every API route, the static files, and the write guard.
func (a *app) routes(st *static) http.Handler {
	mux := a.table(st)
	var h http.Handler = a.guard(mux.ServeMux)
	if a.fed != nil {
		h = a.fedMethods(h)
	}
	return a.logged(mux.ServeMux, a.secure(h))
}

// table registers every route the instance answers in its mode. Routes that only a federating mode
// has are added here under a.fed (set by start in a mode that federates) and nowhere else, so local
// mode cannot gain one by accident.
func (a *app) table(st *static) *routeMux {
	mux := &routeMux{ServeMux: http.NewServeMux()}
	mux.HandleFunc("GET /healthz", a.handleHealthz)
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
	mux.HandleFunc("GET /api/admin/instance", a.authed(permFlagsEdit, a.handleGetInstanceAdmin))
	mux.HandleFunc("PUT /api/admin/instance", a.authed(permFlagsEdit, a.handlePutInstance))
	mux.HandleFunc("GET /api/instance", a.authed("", a.handleInstance))
	mux.HandleFunc("GET /api/about", a.handleAbout)
	mux.HandleFunc("GET /api/admin/users", a.authed(permDashboard, a.handleUsers))
	mux.HandleFunc("GET /api/admin/requests", a.notSingle(a.authed(permUserManage, a.handleJoinRequests)))
	mux.HandleFunc("POST /api/admin/requests/{id}/approve", a.notSingle(a.authed(permUserManage, a.handleApproveRequest)))
	mux.HandleFunc("DELETE /api/admin/requests/{id}", a.notSingle(a.authed(permUserManage, a.handleDeclineRequest)))
	mux.HandleFunc("DELETE /api/admin/users/{id}", a.notSingle(a.authed(permUserManage, a.handleDeleteUser)))
	mux.HandleFunc("PUT /api/admin/users/{id}/password", a.authed(permUserManage, a.handleResetPassword))
	mux.HandleFunc("PUT /api/admin/users/{id}/role", a.notSingle(a.authed(permUserRole, a.handleSetRole)))
	mux.HandleFunc("PUT /api/admin/users/{id}/owner", a.notSingle(a.authed(permUserRole, a.handleSetOwner)))
	mux.HandleFunc("GET /api/admin/audit", a.authed(permAudit, a.handleAudit))
	mux.HandleFunc("GET /api/admin/dashboard", a.authed(permDashboard, a.handleDashboard))
	mux.HandleFunc("GET /catalog/{file}", a.handleCatalogFile)
	mux.HandleFunc("POST /api/suggest/labels", a.flagged("suggest", a.authed("", a.handleTransitionLabels)))
	mux.HandleFunc("GET /api/suggest/counts", a.flagged("suggest", a.countsByToken(a.authed("", a.handleTransitionCounts))))
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
	// Single-profile mode adds the visitor's page at / and the owner's picture it shows, and nothing
	// else: testdata/routes-single.txt lists the two, and TestDBSingleRoutes holds the table to it.
	if a.mode == modeSingle {
		mux.HandleFunc("GET /{$}", a.handleRoot(st))
		mux.HandleFunc("GET /profile-picture", a.handleOwnerPicture(st))
	}
	// A mode that federates adds discovery, the actors and the public pages, and nothing else yet:
	// testdata/routes-federated.txt lists them, and TestDBFederatedRoutes holds the table to it. They are
	// there while federation is refused too, and answer 404 then (handleWebFinger, handleActor).
	if a.fed != nil {
		mux.HandleFunc("GET /.well-known/webfinger", a.handleWebFinger)
		mux.HandleFunc("GET /.well-known/nodeinfo", a.handleNodeInfoLinks)
		mux.HandleFunc("GET /nodeinfo/2.1", a.handleNodeInfo)
		mux.HandleFunc("GET /actor", a.handleInstanceActor(st))
		mux.HandleFunc("GET /users/{name}", a.handleActor(st))
		mux.HandleFunc("GET /users/{name}/posts/{id}", a.handlePublicPost(st, false))
		mux.HandleFunc("GET /users/{name}/posts/{id}/activity", a.handlePublicPost(st, true))
		mux.HandleFunc("GET /actor/outbox", a.handleInstanceOutbox(st))
		for _, kind := range memberCollections {
			mux.HandleFunc("GET /users/{name}/"+kind, a.handleCollection(st, kind))
		}
		for _, ext := range []string{"jpg", "png"} {
			mux.HandleFunc("GET /users/{name}/avatar."+ext, a.handlePublicPicture(false, ext))
			mux.HandleFunc("GET /users/{name}/posts/{id}/picture."+ext, a.handlePublicPicture(true, ext))
		}
	}
	mux.HandleFunc("GET /", a.handleStatic(st))
	return mux
}

/* ---------- single-profile mode ----------

FLOWA_MODE=single is one person's own instance (docs/federation.md, section 4). It holds exactly one
local account, the owner (F15): the start refuses a database with more, sign-up and requests to join
are closed whatever the flag and the setting say, and nothing that makes, removes, demotes or hands
over an account is taken. A signed-out visitor to / is shown the owner's profile as a small page the
server writes (F16); a signed-in owner gets the app there as on any instance, and the app also stays
at /index.html, where the page's "Sign in" link leads. Federation is off unless FLOWA_FEDERATE=on; with it
the owner is discovered and read as a member who opted in is in federated mode.

The page is the first thing Flowa shows a stranger about a person, so it holds only what the owner
put on their profile (name, handle, bio, links, picture) and what an admin saved about the instance.
With FLOWA_FEDERATE=on it also shows the owner's Public posts (federation.md 4.2), the newest of them with
a link to the profile page for the rest; without it no post is on it, as before. No level, streak, XP, badge or other number is on it either: the server
cannot vouch for them yet (section 14.6, TRU), and section 14.5 keeps them home. */

// singleOnly is what single mode answers to anything that would make, remove, demote or hand over an
// account. It says what to do instead.
const singleOnly = "This Flowa holds one person's profile, so it takes no new accounts and keeps its one account as it is. To change that, run it in local mode (leave FLOWA_MODE unset)."

// checkSingle refuses a start in single mode that could not keep to one account, before anything is
// written: more than one account (named by count, never deleted), no account and no ADMIN_USER to make
// one, an ADMIN_USER that would make a second one, and a sole account that is suspended, whose owner
// could not sign in to their own instance while its page went on showing to strangers. A mute only
// stops posting and ends by itself, so it is logged and the start goes on. Requests to join left from
// local mode stay where they are: in single mode nobody can approve one or sign in with it, and
// housekeeping removes them after joinWaitDays as in every mode, so going back to local mode finds
// the data as it was.
func (a *app) checkSingle(adminUser, adminPW string) error {
	var n int
	if err := a.db.QueryRow(`SELECT count(*) FROM users WHERE domain = ''`).Scan(&n); err != nil {
		return err
	}
	name := strings.ToLower(strings.TrimSpace(adminUser))
	seeds := name != "" && adminPW != "" // what seedAdmin needs to make an account
	logWaiting := func() error {
		var waiting int
		if err := a.db.QueryRow(`SELECT count(*) FROM join_requests`).Scan(&waiting); err != nil {
			return err
		}
		if waiting > 0 {
			log.Printf("single mode: %d requests to join wait from local mode; nobody can approve them or sign in with them here, and housekeeping removes them after %d days (or decline them in local mode)", waiting, joinWaitDays)
		}
		return nil
	}
	switch {
	case n > 1:
		return fmt.Errorf("FLOWA_MODE=single: this database has %d local accounts, and a single-profile instance has exactly one. Nothing was changed or deleted. Run it in local mode (leave FLOWA_MODE unset), or remove the other accounts there first (Admin, Accounts), then start in single mode again", n)
	case n == 0 && !seeds:
		return errors.New("FLOWA_MODE=single: there is no account yet. Set ADMIN_USER and ADMIN_PASSWORD for the first start; that account becomes the one profile of this instance")
	case n == 0:
		return logWaiting()
	}
	var who string
	var suspended, muted sql.NullTime
	if err := a.db.QueryRow(`SELECT username, suspended_until, muted_until FROM users WHERE domain = ''`).Scan(&who, &suspended, &muted); err != nil {
		return err
	}
	if seeds && name != who {
		return fmt.Errorf("FLOWA_MODE=single: ADMIN_USER=%s names no account here, and this instance already has its one account, @%s. A second account cannot be made in single mode: set ADMIN_USER to %s, or leave ADMIN_USER and ADMIN_PASSWORD unset", name, who, who)
	}
	now := time.Now()
	if suspended.Valid && suspended.Time.After(now) {
		return fmt.Errorf("FLOWA_MODE=single: the one account, @%s, is suspended until %s, so its owner could not sign in to their own instance. Run in local mode and lift the suspension there first (README, A single-profile instance), or wait until it ends", who, stamp(suspended.Time))
	}
	if muted.Valid && muted.Time.After(now) {
		log.Printf("single mode: the one account, @%s, is muted until %s; it can sign in and read, and post again once the mute ends", who, stamp(muted.Time))
	}
	return logWaiting()
}

// soleAdmin makes the one account of a single-profile instance an admin, so that ensureOwner, the rule
// every instance has for choosing its owner, makes it the owner: the person whose instance it is edits
// its information and its skill maps. It changes a row only when exactly one account exists and it is
// not an admin yet (a database that ran in local mode with one member and no admin).
func (a *app) soleAdmin() error {
	var name string
	err := a.db.QueryRow(`UPDATE users SET role = 'admin' WHERE domain = '' AND role <> 'admin' AND (SELECT count(*) FROM users WHERE domain = '') = 1 RETURNING username`).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err == nil {
		log.Printf("single mode: @%s is the one account, so it is made an admin and the owner", name)
	}
	return err
}

// visitorCSP is the policy of the visitor's page (docs/federation.md, 5.6): no script and no frame at
// all, pictures and the style sheet from this server only, and no form may send anything anywhere.
const visitorCSP = "default-src 'none'; img-src 'self'; style-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"

// visitorMaxAge is how long a made page is used before it is made again, as a net for a change that
// did not come through a save on this server (a hand edit in the database). A save drops it at once.
const visitorMaxAge = 10 * time.Minute

// visitorPage is the visitor's page as made: the HTML and its ETag, and the owner's picture, so that
// neither costs a database read while it is fresh.
type visitorPage struct {
	body    []byte
	etag    string
	pic     []byte // the owner's profile picture, or nil when there is none
	picType string // image/jpeg or image/png
	picTag  string
	at      time.Time
	fed     bool // made while the instance federated, so with the owner's Public posts
}

// visitorLink is one of the owner's links, built from the handle as the app builds it (web/social.js).
type visitorLink struct {
	Name, URL string
}

// visitorData is everything the template is given. Every field is plain text, and html/template
// escapes each one for where it stands; none is marked safe.
type visitorData struct {
	Name, Username, Initial, Bio, Picture string
	Links                                 []visitorLink
	Description, Contact, ContactURL      string
	Rules                                 []instanceRule
	CSS, Icon                             string
	// With FLOWA_FEDERATE=on, the owner's newest Public posts (federation.md 4.2), and where the rest are.
	Posts    []pubPost
	AllPosts string
}

var visitorTmpl = template.Must(template.New("visitor").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="dark light">
<title>{{.Name}} (@{{.Username}})</title>
{{with .Icon}}<link rel="icon" href="{{.}}" type="image/svg+xml">
{{end}}{{with .CSS}}<link rel="stylesheet" href="{{.}}">
{{end}}</head>
<body class="pub">
<header class="pub-top"><a class="btn primary" href="index.html#/signin">Sign in</a></header>
<main class="pub-main">
<article class="card pub-card" aria-labelledby="pub-name">
<div class="pub-who">{{if .Picture}}<img class="pub-pic" src="{{.Picture}}" alt="" width="72" height="72">{{else}}<span class="pub-pic pub-initial" aria-hidden="true">{{.Initial}}</span>{{end}}
<div class="pub-names"><h1 id="pub-name">{{.Name}}</h1><p class="pub-handle">@{{.Username}}</p></div></div>
{{with .Bio}}<p class="pub-text">{{.}}</p>
{{end}}{{if .Links}}<ul class="pub-links" aria-label="Links">{{range .Links}}<li><a class="btn sm" href="{{.URL}}" rel="nofollow noopener noreferrer">{{.Name}}</a></li>{{end}}</ul>
{{end}}</article>
{{if .Posts}}<section class="pub-posts" aria-labelledby="pub-posts-h"><h2 id="pub-posts-h">Public posts</h2>
{{range .Posts}}<article class="card pub-card pub-post">
<h3 class="pub-when"><a href="{{.URL}}"><time datetime="{{.When}}">{{.Day}}</time></a></h3>
{{range .Paras}}<p class="pub-text">{{range .}}{{if .URL}}<a href="{{.URL}}" rel="nofollow noopener ugc">{{.Text}}</a>{{else}}{{.Text}}{{end}}{{end}}</p>
{{end}}{{range .Extras}}<p class="pub-text pub-hint">{{.}}</p>
{{end}}{{if .Picture}}<img class="pub-img" src="{{.Picture}}" alt="{{.PictureAlt}}" loading="lazy">
{{end}}</article>
{{end}}{{with .AllPosts}}<p><a class="btn sm" href="{{.}}">All public posts</a></p>
{{end}}</section>
{{end}}{{if or .Description .Contact .Rules}}<section class="card pub-card" aria-labelledby="pub-about">
<h2 id="pub-about">About this Flowa</h2>
{{with .Description}}<p class="pub-text">{{.}}</p>
{{end}}{{if .Contact}}<p>Contact: {{if .ContactURL}}<a href="{{.ContactURL}}" rel="nofollow noopener noreferrer">{{.Contact}}</a>{{else}}{{.Contact}}{{end}}</p>
{{end}}{{if .Rules}}<h3>Rules</h3>
<ol class="pub-rules">{{range .Rules}}<li><span class="pub-text">{{.Text}}</span>{{with .Hint}}<br><span class="pub-hint pub-text">{{.}}</span>{{end}}</li>{{end}}</ol>
{{end}}</section>
{{end}}</main>
</body>
</html>
`))

// visitorLinkURL is where a stored handle points, as linkURL in web/social.js builds it. The handle
// goes through cleanLink again, so a value stored before a rule was tightened, or by hand, is shown
// only if it would be taken today.
func visitorLinkURL(kind, stored string) (string, bool) {
	h, ok := cleanLink(kind, stored)
	if !ok || h == "" {
		return "", false
	}
	h = strings.ReplaceAll(url.PathEscape(h), "%2F", "/")
	switch kind {
	case "instagram":
		return "https://www.instagram.com/" + h + "/", true
	case "tiktok":
		return "https://www.tiktok.com/@" + h, true
	case "youtube":
		if strings.HasPrefix(h, "channel/") || strings.HasPrefix(h, "c/") || strings.HasPrefix(h, "user/") {
			return "https://www.youtube.com/" + h, true
		}
		return "https://www.youtube.com/@" + strings.TrimPrefix(h, "@"), true
	}
	return "", false
}

var visitorLinkNames = map[string]string{"instagram": "Instagram", "tiktok": "TikTok", "youtube": "YouTube"}

// makeVisitorPage reads the owner's profile and the instance information (two reads) and writes the page.
func (a *app) makeVisitorPage(st *static) (*visitorPage, error) {
	u, err := scanUser(a.db.QueryRow(`SELECT ` + userCols + ` FROM users u WHERE u.domain = '' ORDER BY u.owner DESC, u.id LIMIT 1`))
	if err != nil {
		return nil, err
	}
	info, _, _, err := readInstance(a.db)
	if err != nil {
		return nil, err
	}
	d := visitorData{Name: u.DisplayName, Username: u.Username, Bio: strings.TrimSpace(u.Bio), Description: info.Description, Contact: info.Contact, Rules: info.Rules}
	if d.Name == "" {
		d.Name = u.Username
	}
	// The circle where a picture would be holds the first letter or digit of the name (cleanName makes
	// sure there is one), as the app draws a member without a picture.
	if i := strings.IndexFunc(d.Name, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }); i >= 0 {
		r, _ := utf8.DecodeRuneInString(d.Name[i:])
		d.Initial = strings.ToUpper(string(r))
	}
	links := map[string]string{}
	json.Unmarshal(u.Links, &links)
	for _, k := range linkKinds {
		if href, ok := visitorLinkURL(k, links[k]); ok {
			d.Links = append(d.Links, visitorLink{visitorLinkNames[k], href})
		}
	}
	switch info.ContactKind {
	case "email":
		d.ContactURL = "mailto:" + info.Contact
	case "link":
		d.ContactURL = info.Contact
	}
	if st != nil {
		if f, ok := st.plain["/app.css"]; ok {
			d.CSS = hashedName("app.css", f.hash)
		}
		if f, ok := st.plain["/icon.svg"]; ok {
			d.Icon = hashedName("icon.svg", f.hash)
		}
	}
	p := &visitorPage{at: time.Now(), fed: a.federating()}
	// With FLOWA_FEDERATE=on the owner's Public posts are public (4.2): the newest pubPage of them, and a
	// link to the profile page for the rest. Without it the page is as it was, with no post.
	if p.fed {
		// Read whole and closed before a post's lines are made, which may ask the database again
		// (skillNames): nothing holds one connection while it waits for another. The page is shared by
		// every visitor, so it is made without one visitor's context.
		type visitorRow struct {
			id         int64
			body, head string
			data       []byte
			created    time.Time
		}
		var got []visitorRow
		rows, err := a.db.Query(`SELECT p.id, p.body, left(p.image, `+pictureHead+`), p.data, p.created_at FROM posts p WHERE p.user_id = $1 AND `+publicPost+`
			ORDER BY p.id DESC LIMIT $2`, u.ID, pubPage+1)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var x visitorRow
			if err = rows.Scan(&x.id, &x.body, &x.head, &x.data, &x.created); err != nil {
				break
			}
			got = append(got, x)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return nil, err
		}
		for _, x := range got {
			if len(d.Posts) == pubPage {
				d.AllPosts = "/users/" + u.Username
				break
			}
			d.Posts = append(d.Posts, a.postOf(context.Background(), u.Username, x.id, x.body, x.head, x.data, x.created))
		}
	}
	// The picture went through cleanImage when it was saved: a JPEG or PNG made again by the server. Its
	// type is what its bytes decode as, not what the stored prefix says, and anything that decodes as
	// neither is left out rather than sent. It is decoded once here; the page is kept with it.
	if _, data, ok := strings.Cut(u.Avatar, ","); ok {
		if raw, err := base64.StdEncoding.DecodeString(data); err == nil {
			if _, format, err := image.DecodeConfig(bytes.NewReader(raw)); err == nil && (format == "jpeg" || format == "png") {
				p.pic, p.picType, p.picTag = raw, "image/"+format, hashOf(raw)
				d.Picture = "profile-picture?v=" + p.picTag
			}
		}
	}
	var buf bytes.Buffer
	if err := visitorTmpl.Execute(&buf, d); err != nil {
		return nil, err
	}
	p.body, p.etag = buf.Bytes(), `"`+hashOf(buf.Bytes())+`"`
	return p, nil
}

// visitorPageNow is the page from memory, made again only after a save or visitorMaxAge. The lock is
// held while it is made, so a flood of visitors on an empty cache makes it once.
func (a *app) visitorPageNow(st *static) (*visitorPage, error) {
	a.pageMu.Lock()
	defer a.pageMu.Unlock()
	if a.page != nil && time.Since(a.page.at) < visitorMaxAge && a.page.fed == a.federating() {
		return a.page, nil
	}
	p, err := a.makeVisitorPage(st)
	if err != nil {
		return nil, err
	}
	a.page = p
	return p, nil
}

// dropVisitorPage forgets the made page, after a save that may change it: the owner's profile or the
// instance information. In local mode nothing reads it, and dropping it costs nothing.
func (a *app) dropVisitorPage() {
	a.pageMu.Lock()
	a.page = nil
	a.pageMu.Unlock()
}

// visitorLimited counts a read of / or of the picture by address, as the about page is counted, and
// answers 429 once the address has used its allowance. It is asked first, before a session cookie is
// looked up, so an address over its allowance costs no query whatever cookie it sends. It returns the
// address counted, for handleRoot to give the count back to a proven session.
func (a *app) visitorLimited(w http.ResponseWriter, r *http.Request) (string, bool) {
	from := limitKey(a.clientIP(r))
	if a.lim.visitor.add(from) {
		return from, false
	}
	logRefusal(w, "too many reads of the visitor's page", "from "+from)
	w.Header().Set("Retry-After", "3600")
	http.Error(w, "Too many requests from this address. Try again in an hour.", http.StatusTooManyRequests)
	return from, true
}

// handleRoot answers / in single mode. Every request is counted against the address's allowance first.
// Then a cookie that can be a session token is looked up as every handler looks one up (a cookie that
// cannot be one, or is unknown, expired or of a suspended account, counts as none); a valid session gets
// the app, as on any instance, and its count is given back, so the owner reloading / is never refused
// while their address has room. A stranger's made-up token therefore costs at most one query per count
// of the allowance, and none once the address is over it. Anyone else gets the owner's profile as the server writes it. Both answers say
// Vary: Cookie, so no cache hands one to the other. The page is sent with no-cache and an ETag rather
// than kept for a while: the owner who has just saved their profile and signs out should see it as
// saved, and a visitor's browser that asks again is told 304 with no body.
func (a *app) handleRoot(st *static) http.HandlerFunc {
	app := a.handleStatic(st)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Vary", "Cookie")
		from, over := a.visitorLimited(w, r)
		if over {
			return
		}
		if sessionToken(r) != "" && a.currentUser(r) != nil {
			a.lim.visitor.addWeight(from, -tenths) // the owner is no visitor: the count is given back
			app(w, r)
			return
		}
		p, err := a.visitorPageNow(st)
		if err != nil {
			log.Printf("error: the visitor's page: %v", err)
			http.Error(w, "Something went wrong on the server.", http.StatusInternalServerError)
			return
		}
		h := w.Header()
		h.Set("Content-Security-Policy", visitorCSP)
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Cache-Control", "no-cache")
		h.Set("ETag", p.etag)
		// ServeContent answers If-None-Match with 304, HEAD with the headers alone, and sets the length.
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(p.body))
	}
}

// handleOwnerPicture serves the owner's profile picture for the visitor's page, from the same memory
// as the page and under the same limit. It exists only in single mode and only for the owner's
// picture: there is no id in the address to ask for anyone else's. It reads no cookie. The page names
// it with a hash of its bytes (?v=), so under that address it is kept for a year as immutable; any
// other address of it, which may outlive a change of picture, is sent with no-cache.
func (a *app) handleOwnerPicture(st *static) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, over := a.visitorLimited(w, r); over {
			return
		}
		p, err := a.visitorPageNow(st)
		if err != nil {
			log.Printf("error: the visitor's page: %v", err)
			http.Error(w, "Something went wrong on the server.", http.StatusInternalServerError)
			return
		}
		if p.pic == nil {
			http.NotFound(w, r)
			return
		}
		h := w.Header()
		h.Set("Content-Type", p.picType)
		if r.URL.Query().Get("v") == p.picTag {
			h.Set("Cache-Control", "max-age=31536000, immutable")
		} else {
			h.Set("Cache-Control", "no-cache")
		}
		h.Set("ETag", `"`+p.picTag+`"`)
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(p.pic))
	}
}

/* ---------- request log ----------

One line of JSON per request, on the same stream as the rest of the log:

	{"time":"2026-11-10T19:05:24.123Z","method":"POST","route":"/api/posts/{id}/comments","status":201,"ms":12.4,"bytes":310}

The route is the pattern the request matched, never the path, so no id, name or query string is
written; there is no address and no member either. A request that was refused by a limit or at
sign-in also carries "refused" with what logRefusal says about it, which is the one place an address
or a member's number appears. Two kinds of request are left out because their lines say nothing: a
/healthz probe answered 200 (Docker sends one every half minute) and a page file served without an
error. All lines together get maxRequestLines a minute, with the rest counted in one line
({"time":...,"unlisted":1234,"since":...}), so a flood cannot push the lines of an incident out of
the log: compose.yml sizes the log to hold a full day of the most both logs can write. */

// maxRequestLines is how many request lines are written in one minute.
const maxRequestLines = 240

// requestLog writes the request lines and keeps count of the minute that began at since.
type requestLog struct {
	mu      sync.Mutex
	out     io.Writer
	since   time.Time
	n       int // lines written in this minute
	dropped int // requests in this minute that got no line
}

// requestLine is one request in the log.
type requestLine struct {
	Time    string  `json:"time"`   // when the answer was finished, UTC
	Method  string  `json:"method"` // "other" for a method no route uses
	Route   string  `json:"route"`  // the pattern matched, "" when none was
	Status  int     `json:"status"`
	Ms      float64 `json:"ms"`                // from the request's headers read to the answer finished
	Bytes   int64   `json:"bytes"`             // of the answer's body
	Refused string  `json:"refused,omitempty"` // what was refused and for whom, when a limit or the sign-in did
}

// write puts one line in the log, or counts it when this minute's lines are used up.
func (l *requestLog) write(now time.Time, line requestLine) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.since) >= time.Minute {
		if l.dropped > 0 {
			b, _ := json.Marshal(map[string]any{"time": stampMs(now), "unlisted": l.dropped, "since": stampMs(l.since)})
			l.out.Write(append(b, '\n'))
		}
		l.since, l.n, l.dropped = now, 0, 0
	}
	if l.n >= maxRequestLines {
		l.dropped++
		return
	}
	l.n++
	b, _ := json.Marshal(line)
	l.out.Write(append(b, '\n'))
}

// stampMs is a time as the request log writes it.
func stampMs(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// counted is the writer a request is answered through, so its line can say how it went.
type counted struct {
	http.ResponseWriter
	status  int
	bytes   int64
	refused string
}

func (c *counted) WriteHeader(code int) {
	if c.status == 0 {
		c.status = code
	}
	c.ResponseWriter.WriteHeader(code)
}

func (c *counted) Write(b []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK // a body without a status is a 200
	}
	n, err := c.ResponseWriter.Write(b)
	c.bytes += int64(n)
	return n, err
}

// Unwrap gives the writer underneath, for http.ResponseController.
func (c *counted) Unwrap() http.ResponseWriter { return c.ResponseWriter }

// noteRefusal puts what was refused on the request's line. w is the writer the handler answers through,
// which may be wrapped (refunding).
func noteRefusal(w http.ResponseWriter, what string) {
	for w != nil {
		if c, ok := w.(*counted); ok {
			c.refused = what
			return
		}
		u, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return
		}
		w = u.Unwrap()
	}
}

// logMethods are the methods written as they are. Anything else is a word the visitor chose, and it
// is written as "other".
var logMethods = map[string]bool{"GET": true, "HEAD": true, "POST": true, "PUT": true, "DELETE": true, "PATCH": true, "OPTIONS": true}

// logged writes the request's line once it is answered. The route is looked up here and not read
// from what the mux leaves behind, so a request turned away before the mux (a foreign Host, a
// cross-origin write) still says where it was going.
func (a *app) logged(mux *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		_, pattern := mux.Handler(r)
		route := pattern
		if i := strings.IndexByte(route, ' '); i >= 0 {
			route = route[i+1:] // "GET /api/me": the method has its own field
		}
		c := &counted{ResponseWriter: w}
		returned := false
		defer func() {
			if c.status == 0 {
				c.status = http.StatusOK // nothing written is an empty 200
				if !returned {
					c.status = http.StatusInternalServerError // the handler panicked; the server drops the connection
				}
			}
			quiet := c.status < 400 && (route == "/" || (route == "/healthz" && c.status == http.StatusOK))
			if !quiet {
				method := r.Method
				if !logMethods[method] {
					method = "other"
				}
				now := time.Now()
				a.reqLog.write(now, requestLine{Time: stampMs(now), Method: method, Route: route, Status: c.status,
					Ms: math.Round(float64(now.Sub(start).Microseconds())/100) / 10, Bytes: c.bytes, Refused: c.refused})
			}
		}()
		next.ServeHTTP(c, r)
		returned = true
	})
}

// health is what GET /healthz says about the running instance.
type health struct {
	OK         bool   `json:"ok"`         // the database answers and its schema is not older than this build needs
	Database   bool   `json:"database"`   // the database answers
	Schema     int    `json:"schema"`     // the newest migration the database has had
	Migrations int    `json:"migrations"` // the newest migration this build knows; less than schema after going back an image
	TLSRoots   bool   `json:"tlsRoots"`   // root certificates are present, so link previews can be fetched
	Version    string `json:"version"`    // what VERSION says this build is
}

// healthEvery is how often GET /healthz asks the database. The endpoint needs no session, so without
// this a flood of probes would be a flood of queries; with it they share one answer.
const healthEvery = time.Second

// checkHealth reads the schema version from the database, or gives the answer of the last second.
func (a *app) checkHealth() health {
	a.healthMu.Lock()
	defer a.healthMu.Unlock()
	if !a.healthAt.IsZero() && time.Since(a.healthAt) < healthEvery {
		return a.healthWas
	}
	h := health{Migrations: len(migrations), TLSRoots: rootsPresent(), Version: a.version}
	// Not the request's context: a probe that hangs up early must not leave "database down" behind for
	// the probes that share this answer.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := a.db.QueryRowContext(ctx, `SELECT COALESCE(max(version), 0) FROM schema_migrations`).Scan(&h.Schema); err == nil {
		h.Database = true
	} else {
		// The table can be missing (an empty database put in place of the real one) while the database
		// itself answers: say which.
		h.Database = a.db.PingContext(ctx) == nil
	}
	h.OK = h.Database && h.Schema >= h.Migrations
	a.healthAt, a.healthWas = time.Now(), h
	return h
}

// handleHealthz answers probes: 200 when the instance works, 503 when it does not. Anyone may ask and
// learns only that. The rest (schema version, TLS roots, the build) goes to a probe from the machine
// itself: which commit runs is a help to an attacker.
func (a *app) handleHealthz(w http.ResponseWriter, r *http.Request) {
	h := a.checkHealth()
	code := http.StatusOK
	if !h.OK {
		code = http.StatusServiceUnavailable
	}
	if !localProbe(r) {
		writeJSON(w, code, map[string]bool{"ok": h.OK})
		return
	}
	writeJSON(w, code, h)
}

// localProbe reports whether a request comes from the machine the app runs on (in Docker: from inside
// the container, as the image's health check and "docker exec nunchaku /nunchaku -healthcheck" do) and
// was not passed on by a proxy. A reverse proxy on the same machine connects from a local address too,
// but it names the visitor in a forwarding header.
func localProbe(r *http.Request) bool {
	for _, k := range []string{"X-Forwarded-For", "X-Real-Ip", "Forwarded", "Via"} {
		if len(r.Header.Values(k)) > 0 {
			return false
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// probeURL is where -healthcheck asks, given the listen address: the same port on this machine, or on
// the one address the server listens on when -listen names one.
func probeURL(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "http://127.0.0.1" + listen + "/healthz"
	}
	if ip := net.ParseIP(host); host == "" || host == "localhost" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/healthz"
}

// rootsPresent says whether there is anything to check a server's certificate against: the system's
// root certificates, or the bundle compiled into the binary when the system has none.
func rootsPresent() bool {
	pool, err := x509.SystemCertPool()
	return err == nil && pool != nil && !pool.Equal(x509.NewCertPool())
}

// start does what the server does between opening the database and listening: it reads the
// operator's settings and then seeds what a new instance needs. Every setting is read before the
// admin account and the skill maps are written, so a setting that cannot be understood stops the
// server with no row added. (Opening the database has applied the migrations by then; main reads
// the mode before that, see there.) It opens no connection to another machine, in any mode:
// TestDBLocalModeUnchanged holds that.
func (a *app) start(getenv func(string) string) error {
	// The admin dashboard shows the version compiled in (a release tag, or main.<commit> from
	// deploy.sh --build). VERSION in the environment replaces it, for a build that has none.
	if v := strings.TrimSpace(getenv("VERSION")); v != "" {
		a.version = v
	}
	log.Printf("version %s", a.version)
	var err error
	if a.mode, err = parseMode(getenv("FLOWA_MODE")); err != nil {
		return err
	}
	log.Printf("mode %s", a.mode)
	// FLOWA_FEDERATE in single mode, and the public origin and the key secret in a mode that federates
	// (parseFederation), all read before anything is written.
	fed, err := parseFederation(a.mode, getenv)
	if err != nil {
		return err
	}
	if fed.on {
		log.Print(fedBuiltLog)
	}
	if rootsPresent() {
		log.Print("TLS roots: present")
	} else {
		log.Print("TLS roots: none; link previews cannot be fetched")
	}
	if v := getenv("FLOWA_TRUSTED_PROXIES"); strings.TrimSpace(v) != "" {
		if a.proxies, err = parseProxies(v); err != nil {
			return err
		}
	}
	log.Printf("visitor addresses: forwarding headers believed from %d trusted proxy ranges", len(a.proxies))
	if a.homeURL, err = homeLink(getenv("FLOWA_HOME_URL")); err != nil {
		return err
	}
	if a.countsTok, err = countsToken(getenv("FLOWA_COUNTS_TOKEN")); err != nil {
		return err
	}
	for _, h := range strings.Split(getenv("FLOWA_HOSTS"), ",") {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			a.hosts = append(a.hosts, h)
		}
	}
	// The origin's host is a name this server answers to, in a mode that federates and with an origin
	// that parsed. It is accepted even when federation is refused (an origin that moved): the operator
	// points that name at this server, and the app is served there as in local mode.
	if fed.on {
		a.hosts = append(a.hosts, fed.origin.host)
		a.fedHost = fed.origin.host
	}
	// FLOWA_PREVIEW_BASE points the fetcher at a stub server in tests; FLOWA_PREVIEW_FETCH=off disables it.
	a.fetch = newFetcher(getenv("FLOWA_PREVIEW_BASE"), getenv("FLOWA_PREVIEW_BASE") != "")
	a.fetch.off = getenv("FLOWA_PREVIEW_FETCH") == "off"
	// Federation requests name this build and instance in their User-Agent, and never ask the
	// instance itself. Nothing sends one yet.
	a.fetch.version = a.version
	if fed.on {
		a.fetch.origin = fed.origin
	}
	// A single-profile instance is checked before the admin account is made, so an ADMIN_USER that
	// would be a second account is refused with nothing written (checkSingle).
	if a.mode == modeSingle {
		if err := a.checkSingle(getenv("ADMIN_USER"), getenv("ADMIN_PASSWORD")); err != nil {
			return err
		}
	}
	if _, err := a.seedAdmin(getenv("ADMIN_USER"), getenv("ADMIN_PASSWORD")); err != nil {
		return err
	}
	if a.mode == modeSingle {
		if err := a.soleAdmin(); err != nil {
			return err
		}
	}
	if err := a.ensureOwner(); err != nil {
		return err
	}
	if err := a.seedCatalogs(); err != nil {
		return err
	}
	if fed.on {
		return a.startFederation(fed)
	}
	return nil
}

func main() {
	listen := flag.String("listen", ":3169", "listen address")
	dataDir := flag.String("data", "/data", "directory for backups and for the page files kept for the build after this one")
	probe := flag.Bool("healthcheck", false, "ask the local server's /healthz, print the answer, and exit 0 if it is healthy")
	rotate := flag.Bool("rotate-keys", false, "make a new key pair for every account that has one (a mode that federates only), print how many, and exit")
	purge := flag.Bool("purge-remote", false, "remove every remote account and what hangs off it, in one transaction, print what went, and exit (before ./deploy.sh --rollback to a build without remote accounts)")
	showVersion := flag.Bool("version", false, "print the version this binary was built as, and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(buildVersion)
		return
	}
	if *probe {
		c := http.Client{Timeout: 3 * time.Second}
		resp, err := c.Get(probeURL(*listen))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		// The answer is one short line; Docker keeps it with the health state and deploy.sh prints it.
		io.Copy(os.Stdout, io.LimitReader(resp.Body, 4096))
		if resp.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		return
	}
	if *rotate {
		if err := rotateCommand(os.Getenv, os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}
	if *purge {
		if err := purgeCommand(os.Getenv, os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}

	if err := os.MkdirAll(filepath.Join(*dataDir, "backups"), 0o755); err != nil {
		log.Fatal(err)
	}
	// The mode is read before the database is opened, because opening it applies the migrations: a
	// newer build that is refused its mode must leave the schema for the build before it.
	// So are FLOWA_FEDERATE, and the public origin and the key secret of a mode that federates.
	mode, err := parseMode(os.Getenv("FLOWA_MODE"))
	if err != nil {
		log.Fatal(err)
	}
	if _, err := parseFederation(mode, os.Getenv); err != nil {
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
	if err := a.start(os.Getenv); err != nil {
		log.Fatal(err)
	}

	st, err := loadStatic(*dataDir)
	if err != nil {
		log.Fatal(err)
	}

	a.reqLog.out = log.Writer()
	srv := &http.Server{Addr: *listen, Handler: a.routes(st), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 120 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go a.housekeeping(ctx, time.Hour)
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("listening on %s", *listen)
	if err := a.serve(ctx, srv, ln, stop); err != nil {
		log.Fatal(err)
	}
}

// shutdownWait is how long a stop waits, in all, for the requests in flight and the background work.
// Docker gives a container 10 s after SIGTERM before it kills it. A variable so a test can shorten it.
var shutdownWait = 5 * time.Second

// serve answers on ln until ctx ends, then stops: it lets the requests in flight finish and the key
// worker stop, within shutdownWait in all, and returns once both have or the time is up. Before, main
// returned as soon as srv.Shutdown began (that is when ListenAndServe returns), so the time Shutdown was
// given was never waited for (docs/federation.md, 8.6). Housekeeping is not waited for: each of its
// statements is one transaction, and one cut short changes nothing. release is called as soon as ctx
// ends, before the wait: main passes the stop of signal.NotifyContext, so that a second Ctrl-C or
// SIGTERM during the wait ends the process at once, as it did before the wait existed.
func (a *app) serve(ctx context.Context, srv *http.Server, ln net.Listener, release func()) error {
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		<-ctx.Done()
		release()
		c, cancel := context.WithTimeout(context.Background(), shutdownWait)
		defer cancel()
		if err := srv.Shutdown(c); err != nil {
			log.Printf("stopping: requests still running after %s: %v", shutdownWait, err)
		}
		if a.fed != nil {
			if err := a.fed.stop(c); err != nil {
				log.Printf("stopping: the key worker did not stop in time: %v", err)
			}
		}
	}()
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	<-stopped
	return nil
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

// linkNames is each kind as a sentence names it, with its article.
var linkNames = map[string]string{"instagram": "an Instagram", "tiktok": "a TikTok", "youtube": "a YouTube"}

var (
	handleRE   = regexp.MustCompile(`^[A-Za-z0-9._-]{1,60}$`)
	ytHandleRE = regexp.MustCompile(`^[A-Za-z0-9._@/-]{1,80}$`)
	achKeyRE   = regexp.MustCompile(`^[a-z0-9][a-zA-Z0-9:_-]{0,60}$`) // a branch badge ends in its branch's capital letter
	mentionRE  = regexp.MustCompile(`(?:^|[^a-z0-9._-])@([a-z0-9][a-z0-9._-]{2,23})`)
)

// bidiOverride reports the characters that force the direction of the text after them:
// embeddings, overrides and isolates. Inside a name they can make "nimda" read as "admin", so
// text holding one is refused. The plain direction marks are only dropped (they draw nothing):
// some keyboards add them when two scripts are mixed.
func bidiOverride(r rune) bool { return (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069) }

// unseen reports characters that draw nothing though Unicode does not class them as format
// characters: the Hangul fillers, the blank braille pattern and the combining grapheme joiner.
func unseen(r rune) bool {
	switch r {
	case 0x034F, 0x115F, 0x1160, 0x2800, 0x3164, 0xFFA0:
		return true
	}
	return false
}

// visibleText drops what a reader cannot see: format characters (Unicode category Cf), the
// unseen ones above, and control characters other than a line break (a tab becomes a space). Two
// uses of format characters are kept, because taking them out changes what is drawn: a joiner
// between two letters or symbols (Persian and Indic spelling, emoji such as a family), and the
// tags that turn a black flag into the flag of England, Scotland or Wales. It reports false for
// text that holds a direction override.
func visibleText(s string) (string, bool) {
	in := []rune(strings.ReplaceAll(s, "\r\n", "\n"))
	out := make([]rune, 0, len(in))
	joinable := func(r rune) bool {
		return unicode.IsLetter(r) || unicode.IsMark(r) || unicode.Is(unicode.So, r) || unicode.Is(unicode.Sk, r)
	}
	flag := false // inside the tag run after a black flag
	for i, r := range in {
		tag := r >= 0xE0020 && r <= 0xE007F
		switch {
		case bidiOverride(r):
			return "", false
		case tag && flag:
			out = append(out, r)
			flag = r != 0xE007F
			continue
		case r == 0x200C || r == 0x200D:
			if len(out) > 0 && joinable(out[len(out)-1]) && i+1 < len(in) && joinable(in[i+1]) {
				out = append(out, r)
			}
		case r == '\n':
			out = append(out, r)
		case r == '\t':
			out = append(out, ' ')
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r), unseen(r):
		default:
			out = append(out, r)
		}
		flag = r == 0x1F3F4
	}
	return string(out), true
}

// cleanName makes a display name safe to show next to other names: nothing unseen in it, one
// space between words, and at least one letter or digit, so a name can always be read and told
// from another. It returns the name, or why it cannot be used.
func cleanName(s string) (string, string) {
	v, ok := visibleText(s)
	if !ok {
		return "", "The display name cannot hold characters that change the direction of the text."
	}
	v = strings.Join(strings.FieldsFunc(v, unicode.IsSpace), " ")
	if n := len([]rune(v)); n == 0 || n > 40 {
		return "", "The display name needs 1 to 40 characters."
	}
	if !strings.ContainsFunc(v, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) {
		return "", "The display name needs at least one letter or digit."
	}
	return v, ""
}

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
	err := a.db.QueryRow(`SELECT avatar FROM users WHERE id = $1 AND domain = ''`, id).Scan(&v)
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

// peopleSQL is the people list: the hundred members with the most XP. It reads the users table whole
// and sorts it, on purpose: the table has one row per member, and for the few hundred a household
// server holds that is the cheapest plan there is (TestDBQueryPlans keeps count of the rows). $1 is
// the viewer and $2 whether they may hide posts: a member across a block is not listed (blockFree).
var peopleSQL = `SELECT ` + userCols + ` FROM users u WHERE u.domain = '' AND ` + blockFree("$1", "u.id", "$2") + `
	ORDER BY CASE WHEN NOT u.stats_hidden AND jsonb_typeof(u.stats->'xp') = 'number' THEN (u.stats->>'xp')::numeric ELSE 0 END DESC, u.id LIMIT 100`

// handleUserList is the people list: the 100 members with the most XP (hidden stats count as none).
func (a *app) handleUserList(w http.ResponseWriter, _ *http.Request, viewer *user) {
	rows, err := a.db.Query(peopleSQL, viewer.ID, can(viewer, permPostHide))
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
	u, err := scanUser(a.db.QueryRow(`SELECT `+userCols+` FROM users u WHERE u.username = $1 AND u.domain = ''`, name))
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
	// Across a block the profile still opens, so the blocker can unblock from it and the blocked
	// member can block back, but its posts are not shown (feedSQL) and are not counted either: a count
	// with no posts under it would say there is a block. blocked tells the blocker that they blocked
	// this member; the blocked member is not told.
	var posts, likes int
	var blocked bool
	if err := a.db.QueryRow(`SELECT x.blocked,
		CASE WHEN x.free THEN (SELECT count(*) FROM posts p WHERE p.user_id = $1 AND (p.hidden_at IS NULL OR $1 = $2 OR $3)) ELSE 0 END,
		CASE WHEN x.free THEN (SELECT count(*) FROM likes l JOIN posts p ON p.id = l.post_id WHERE p.user_id = $1) ELSE 0 END
		FROM (SELECT EXISTS (SELECT 1 FROM blocks WHERE user_id = $2 AND target_id = $1) AS blocked, `+blockFree("$2", "$1", "$3")+` AS free) x`,
		u.ID, viewer.ID, can(viewer, permPostHide)).Scan(&blocked, &posts, &likes); err != nil {
		a.serverError(w, err)
		return
	}
	out := map[string]any{"user": u.public(), "achievements": ach, "posts": posts, "likes": likes, "blocked": blocked}
	// With follows on, how many follow this member and how many they follow, for everyone, and whether
	// the viewer follows them. Who they are is shown only to the member (handleFollowers). With the flag
	// off none of this is asked, so the profile costs what it did.
	if a.followCountsShown() {
		var followers, following int
		var followed bool
		if err := a.db.QueryRow(followCountsSQL+`, EXISTS (SELECT 1 FROM follows WHERE follower_id = $2 AND followee_id = $1) AND `+blockFree("$2", "$1", "false"),
			u.ID, viewer.ID).Scan(&followers, &following, &followed); err != nil {
			a.serverError(w, err)
			return
		}
		out["followers"], out["following"], out["followed"] = followers, following, followed
	}
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
	if muted(w, u) {
		return
	}
	w, ok := allowed(w, a.lim.badges, u, "badge updates")
	if !ok {
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
	// Every key is checked before any is recorded, so a batch with one unknown key grants nothing
	// and the page is not left resending the half that went through.
	for _, k := range in.Keys {
		if !badgeKeys[k] && !a.propBadge(k) {
			fail(w, http.StatusBadRequest, "There is no badge called "+strconv.Quote(k)+".")
			return
		}
	}
	fresh := []string{}
	for _, k := range in.Keys {
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
	if len(fresh) > 0 && (in.Post == nil || *in.Post) && a.pref(u, "autoPost", true) && a.flagsCached()["community"] {
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

// propBadge says whether key is one of the badges the page builds from a prop's map (propBadges in
// web/game.js): "gates:<prop>" for a prop whose map has a roadmap, and "branch:<prop>:<K>" for a
// branch of it that has a skill in use. They are not in badgeKeys because the maps can change.
func (a *app) propBadge(key string) bool {
	parts := strings.Split(key, ":")
	if len(parts) < 2 || !slices.Contains(knownProps, parts[1]) {
		return false
	}
	// The map the page was given: the published one, or the shipped file when none is published.
	raw, _, err := a.latestCatalog(parts[1])
	if err != nil {
		shipped, ok := shippedCatalog(parts[1])
		if !ok {
			return false
		}
		raw = shipped
	}
	var c struct {
		Phases []json.RawMessage `json:"phases"`
		Skills []struct {
			ID      string `json:"id"`
			Retired bool   `json:"retired"`
		} `json:"skills"`
	}
	if json.Unmarshal(raw, &c) != nil {
		return false
	}
	switch {
	case parts[0] == "gates" && len(parts) == 2:
		return len(c.Phases) > 0
	case parts[0] == "branch" && len(parts) == 3 && branchKeyRE.MatchString(parts[2]):
		for _, s := range c.Skills {
			if !s.Retired && strings.HasPrefix(s.ID, parts[2]) {
				return true
			}
		}
	}
	return false
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
	q := `INSERT INTO notifications (user_id, actor_id, kind, post_id, comment_id, text) SELECT $1::bigint, $2::bigint, $3::text, $4::bigint, $5::bigint, $6::text`
	// Between two members with a block between them nothing social is told, whichever blocked whom:
	// a like, a comment, a reply or a mention across a block makes no notification. What moderators
	// and the game say is told whatever anyone has blocked.
	if !quietKinds[kind] {
		q += ` WHERE ` + blockFree("$1::bigint", "$2::bigint", "false")
	}
	if _, err := a.db.Exec(q, userID, actor, kind, post, cmt, text); err != nil {
		log.Printf("notify: %v", err)
	}
}

// quietKinds are the notifications a block does not silence: a moderator's notice and a new badge.
// The others between two members go when one blocks the other (handleBlock), and none are made
// while the block holds (notifyAt). Those two can still cross, each under READ COMMITTED, so a
// notification can be written just after the block took the others away; the bell asks about blocks
// too (notifShown), and such a one is never shown or counted.
var quietKinds = map[string]bool{"moderation": true, "achievement": true}

// notifShown is the condition that notification n is shown to its member ($1): one of quietKinds,
// one from nobody, or one from a member with no block between them, whichever blocked whom. The
// kinds are the fixed names above, never text from a request.
var notifShown = `(n.kind IN ('` + strings.Join(slices.Sorted(maps.Keys(quietKinds)), "', '") + `') OR n.actor_id IS NULL OR ` +
	blockFree("$1", "n.actor_id", "false") + `)`

// notificationsSQL is a member's newest notifications ($1 the member, $2 how many); unreadSQL counts
// the ones not read yet, which the bell asks for on every page. Both leave out what notifShown does,
// before the LIMIT, one look in the primary key of blocks each way per notification.
var notificationsSQL = `SELECT n.id, n.kind, COALESCE(n.actor_id, 0), COALESCE(n.post_id, 0), COALESCE(n.comment_id, 0),
		COALESCE(c.parent_id, c.id, 0), n.text, n.read, n.created_at
		FROM notifications n LEFT JOIN comments c ON c.id = n.comment_id
		WHERE n.user_id = $1 AND ` + notifShown + ` ORDER BY n.id DESC LIMIT $2`

var unreadSQL = `SELECT count(*) FROM notifications n WHERE n.user_id = $1 AND NOT n.read AND ` + notifShown

func (a *app) handleNotifications(w http.ResponseWriter, _ *http.Request, u *user) {
	rows, err := a.db.Query(notificationsSQL, u.ID, maxNotifList)
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
	if err := a.db.QueryRow(unreadSQL, u.ID).Scan(&unread); err != nil {
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
	// Audience is "public" or "local" (This server), told to the post's author only, and only in a mode
	// that federates: elsewhere every post is This server and the answer is as it always was.
	Audience string `json:"audience,omitempty"`
	aud      string // as stored, for every reader
}

// blockFree is the SQL condition that what author wrote may be shown to viewer; the three arguments are
// SQL expressions written in this file, never text from a request. A block hides the blocked member's
// posts and comments from the blocker, and the blocker's from the blocked member, as Mastodon does it:
// neither is shown the other. staff is "the viewer may hide posts": a moderator or admin whom a member
// has blocked still sees that member's posts, so a block never stands between a member and moderation.
// A moderator's own block hides the member they blocked from their timeline like anyone's; reports and
// the queue read content without asking about blocks.
//
// Each side is one look in the primary key of blocks per row, which is what keeps a page of the
// timeline as cheap with 5,000 blocks as with none (TestDBQueryPlans). OFFSET 0 is what holds the
// planner to that: without it Postgres may turn NOT EXISTS into a join that reads every block the
// reader has, or into a hashed subplan that reads the whole table, before the LIMIT stops anything.
func blockFree(viewer, author, staff string) string {
	return `NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = ` + viewer + ` AND b.target_id = ` + author + ` OFFSET 0)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = ` + author + ` AND b.target_id = ` + viewer + ` AND NOT ` + staff + ` OFFSET 0)`
}

// feedSQL is a page of posts, newest first: the timeline, one member's posts, one post, or the posts
// with given ids. $1 is the viewer, $2 "older than this post", $3 "by this member", $4 "this post",
// $5 how many, $6 whether the viewer may see hidden posts, $7 the ids; $2, $3, $4 and $7 are left out
// with 0 or NULL. The viewer's own like is found through likes_user (migration 6): without it this
// read every like there is, once per page. Posts across a block are left out here, before the LIMIT,
// so a page is full however many recent posts are by someone blocked; the comment count leaves out
// the comments the viewer is not shown, and the replies under them. Likes are counted from everyone:
// the count names nobody.
var feedSQL = `SELECT ` + postCols("$6") + ` FROM posts p WHERE ($2 = 0 OR p.id < $2) AND ($3 = 0 OR p.user_id = $3) AND ($4 = 0 OR p.id = $4) AND ($7::bigint[] IS NULL OR p.id = ANY($7))
		AND (p.hidden_at IS NULL OR p.user_id = $1 OR $6) AND ` + blockFree("$1", "p.user_id", "$6") + ` ORDER BY p.id DESC LIMIT $5`

// postCols is what a page of posts reads of each post p, for the viewer $1; staff is the parameter
// that says whether the viewer may hide posts. feedSQL and homeSQL both read it, so the page draws a
// post of Home exactly as one of the Community timeline.
func postCols(staff string) string {
	return `p.id, p.user_id, p.kind, p.body, p.prop, p.skill, p.image, p.data, p.created_at,
		(SELECT count(*) FROM likes l WHERE l.post_id = p.id), EXISTS (SELECT 1 FROM likes l WHERE l.post_id = p.id AND l.user_id = $1),
		(SELECT count(*) FROM comments c LEFT JOIN comments pc ON pc.id = c.parent_id WHERE c.post_id = p.id AND NOT c.deleted AND c.hidden_at IS NULL
			AND ` + blockFree("$1", "c.user_id", staff) + ` AND (pc.id IS NULL OR ` + blockFree("$1", "pc.user_id", staff) + `)), p.hidden_reason, p.audience`
}

// homeSQL is a page of Home: the posts of the accounts the viewer follows and the viewer's own, newest
// first. $1 is the viewer, $2 "older than this post" (0 for the first page), $3 how many, $4 whether the
// viewer may see hidden posts. What is shown is what the Community timeline shows of those accounts:
// a hidden post only to its author and to staff, nothing across a block (blockFree, asked once per
// account, with the same staff exception).
//
// Postgres cannot merge the newest posts of a list of accounts it learns only while it runs, so each
// account gives its own newest page through posts_user (user_id, id DESC) and the newest of those are
// kept. A page reads at most one page of posts per account followed, whatever the size of the posts
// table and however long ago an account last posted; maxFollows bounds the accounts (TestDBQueryPlans).
// The counts and the viewer's like are read after the LIMIT, for the posts shown only.
var homeSQL = `SELECT ` + postCols("$4") + ` FROM (SELECT h.id FROM
		(SELECT x.id FROM (SELECT $1::bigint AS id UNION ALL SELECT f.followee_id FROM follows f WHERE f.follower_id = $1) x
			WHERE ` + blockFree("$1", "x.id", "$4") + `) a
		CROSS JOIN LATERAL (SELECT p.id FROM posts p WHERE p.user_id = a.id AND ($2 = 0 OR p.id < $2)
			AND (p.hidden_at IS NULL OR p.user_id = $1 OR $4) ORDER BY p.id DESC LIMIT $3) h
		ORDER BY h.id DESC LIMIT $3) h JOIN posts p ON p.id = h.id ORDER BY p.id DESC`

// feedCommentsSQL is the newest top-level comments of each of those posts ($3 the posts, $4 how many
// of each); repliesSQL the newest replies under each of those comments. Both go through queryComments.
// A thread begun by someone across a block is left out whole, its replies with it, because they answer
// a comment the viewer is not shown.
var feedCommentsSQL = `SELECT * FROM (SELECT c.*, row_number() OVER (PARTITION BY c.post_id ORDER BY c.id DESC) AS rn
	FROM comments c WHERE c.post_id = ANY($3) AND c.parent_id IS NULL AND ` + commentBlockFree + `) c WHERE c.rn <= $4 ORDER BY c.id`

// loadPosts reads a page of posts newest first, with like counts, whether the viewer
// liked each one, every comment, and the users involved. With ids, it reads those posts
// instead of a page (the viewer's bookmarks).
func (a *app) loadPosts(viewer *user, before, byUser, only int64, ids ...int64) (map[string]any, error) {
	limit := pageSize + 1
	if ids != nil {
		limit = len(ids) + 1
	}
	// Hidden posts are seen only by their author and by those who may hide posts.
	return a.readPosts(viewer, feedSQL, viewer.ID, before, byUser, only, limit, can(viewer, permPostHide), pq.Array(ids))
}

// readPosts runs q, feedSQL or homeSQL with its arguments, and gives the page as the page reads it:
// the posts with their comments and links, the users involved, and whether more follow.
func (a *app) readPosts(viewer *user, q string, args ...any) (map[string]any, error) {
	rows, err := a.db.Query(q, args...)
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
		if err := rows.Scan(&p.ID, &p.UserID, &p.Kind, &p.Body, &p.Prop, &p.Skill, &p.Image, &data, &p.Created, &p.Likes, &p.Liked, &p.CommentCount, &p.Hidden, &p.aud); err != nil {
			return nil, err
		}
		if a.fed != nil && p.UserID == viewer.ID {
			p.Audience = p.aud
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
		roots, err := a.queryComments(viewer, feedCommentsSQL, pq.Array(ids), previewComments)
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
		err := a.db.QueryRow(`SELECT id FROM users WHERE username = $1 AND domain = ''`, name).Scan(&byUser)
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
	// The ids are read whole and the rows closed before anyone is told: telling writes a row, and a
	// request that held this query's connection while it waited for another could, with a few others
	// doing the same, take the whole pool and stop every request.
	rows, err := a.db.Query(`SELECT id FROM users WHERE username = ANY($1) AND domain = ''`, pq.Array(names))
	if err != nil {
		log.Printf("mentions: %v", err)
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	if err := rows.Err(); err != nil {
		log.Printf("mentions: %v", err)
	}
	rows.Close()
	for _, id := range ids {
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

// stamp writes a time into a message as RFC 3339 in UTC, to the second. Every time a person is
// told about goes out this way, and the page shows it in the reader's own time zone and format
// (localTimes in web/account.js); the same moment used to read differently from place to place.
func stamp(t time.Time) string { return t.UTC().Truncate(time.Second).Format(time.RFC3339) }

// muted answers the request when the user is muted, and reports whether they were.
func muted(w http.ResponseWriter, u *user) bool {
	if u.MutedUntil == nil {
		return false
	}
	msg := "You are muted until " + stamp(*u.MutedUntil) + ", so you cannot post or comment."
	if u.MuteReason != "" {
		msg += " Reason: " + u.MuteReason
	}
	fail(w, http.StatusForbidden, msg)
	return true
}

func (a *app) handleCreatePost(w http.ResponseWriter, r *http.Request, u *user) {
	// A repeat of a post already made is a read: it is answered before the mute and the allowance.
	prior, finish, ok := a.once(w, r, u)
	if !ok {
		return
	}
	if prior != 0 {
		a.writePost(w, u, prior)
		return
	}
	var made int64
	defer func() { finish(made) }()
	if muted(w, u) {
		return
	}
	w, ok = allowed(w, a.lim.posts, u, "posts")
	if !ok {
		return
	}
	var in struct {
		Body  string     `json:"body"`
		Image string     `json:"image"`
		Tags  []postTag  `json:"tags"`
		Combo *postCombo `json:"combo"`
		Links []string   `json:"links"`
		// Who may read it (docs/federation.md, F2): "public" or "local", read only in a mode that federates.
		Audience *string `json:"audience"`
	}
	if !readJSON(w, r, maxImage+4*maxPost+8192, &in) {
		return
	}
	audience, msg, err := a.postAudience(in.Audience, u)
	if err != nil {
		a.serverError(w, err)
		return
	}
	if msg != "" {
		fail(w, http.StatusBadRequest, msg)
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
				fail(w, http.StatusBadRequest, "This link cannot be a card: "+shown(raw, 80)+". Cards are for Instagram posts and reels, TikTok videos, and YouTube videos and shorts.")
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
	if err := a.db.QueryRow(`INSERT INTO posts (user_id, body, prop, skill, image, data, audience) VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		u.ID, body, prop, skill, in.Image, string(dataJSON), audience).Scan(&id); err != nil {
		a.serverError(w, err)
		return
	}
	made = id
	a.dropVisitorPage() // a single-profile instance's page shows the owner's Public posts
	a.notifyMentions(body, u.ID, id, 0)
	for _, l := range links {
		go a.preview(l) // in the background: posting never waits for another site
	}
	a.writePost(w, u, id)
}

// Who may read a post (docs/federation.md, F2, F3 and 6.7). This server ("local") is the audience of every
// post made before audiences existed and of every post made outside a mode that federates; Public
// ("public") is chosen per post, and only by a member who has opted in, while federation is not refused.
// Milestone posts are This server until TRU (F13): handleAchievements writes none other. Posts are not
// edited (1.2), so the audience is decided once, when the post is made: a Public post that another server
// may have read cannot become private by a change of a column, and taking one back is a deletion, which
// FWR sends to the servers it went to.
const (
	audLocal  = "local"
	audPublic = "public"
)

// Said when a post asks for Public and cannot have it.
const (
	audNotOptedIn = "Only members who share their profile beyond this server can post publicly. Turn that on in Settings, or post to this server."
	audOff        = "Posting publicly is not available at the moment. Post to this server instead."
	audUnknown    = "The audience is public or local (this server)."
)

// postAudience decides a new post's audience from what the page asked for (nil: nothing). Outside a mode
// that federates the field is ignored, as every field the handler does not know is, and the post is This
// server, so an instance that sets nothing new answers as before. In a mode that federates nothing asked
// means This server too: Public is never given by a default on the server, so a page from before audiences
// cannot publish by leaving the field out; the composer sends the choice, with Public chosen at first for a
// member who opted in (federation.md 11). Public is refused, with the reason, to a member who has not opted
// in (the owner of a single-profile instance has, by FLOWA_FEDERATE=on) and while federation is refused.
func (a *app) postAudience(want *string, u *user) (aud, msg string, err error) {
	if a.fed == nil || want == nil {
		return audLocal, "", nil
	}
	switch *want {
	case audLocal:
		return audLocal, "", nil
	case audPublic:
	default:
		return "", audUnknown, nil
	}
	if !a.federating() {
		return "", audOff, nil
	}
	if a.mode == modeSingle && u.Owner {
		return audPublic, "", nil
	}
	var on bool
	if err := a.db.QueryRow(`SELECT federates FROM users WHERE id = $1`, u.ID).Scan(&on); err != nil {
		return "", "", err
	}
	if !on {
		return "", audNotOptedIn, nil
	}
	return audPublic, "", nil
}

// shown makes text a person sent safe to say back in a message: printable characters only, and
// at most max of them.
func shown(s string, max int) string {
	out := make([]rune, 0, max+1)
	for _, c := range s {
		if !unicode.IsPrint(c) {
			continue
		}
		if len(out) == max {
			return string(out) + "..."
		}
		out = append(out, c)
	}
	return string(out)
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
	a.dropVisitorPage()
	if author != u.ID {
		if err := a.audit(a.db, u.ID, "delete_post", "post", id, author, "", nil); err != nil {
			a.serverError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// postOwner finds who wrote a post the viewer may see, answering the request itself when it
// cannot. A hidden post is "gone" to everyone but its author and moderators, and a post across a
// block (blockFree) is gone in the same words, so the answer does not say that there is a block.
func (a *app) postOwner(w http.ResponseWriter, id int64, viewer *user) (int64, bool) {
	var owner int64
	err := a.db.QueryRow(`SELECT p.user_id FROM posts p WHERE p.id = $1 AND `+postShown,
		id, viewer.ID, can(viewer, permPostHide)).Scan(&owner)
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

// queryRower is what *sql.DB and *sql.Tx both answer. A helper that may run while a handler holds a
// transaction takes one, and the handler passes its tx: asking a.db there would need a second
// connection while the first is held, and with every connection of the pool held that way each
// request waits for another for ever (TestDBOneConnection).
type queryRower interface {
	QueryRow(string, ...any) *sql.Row
}

// across answers the request with a 404 and msg, the words used when the thing is not there, and
// reports true when the member and any of others have a block between them, whichever blocked whom.
// It guards what reaches another member (a comment, a reply, a like), and it holds for moderators too:
// seeing a blocker's posts for moderation is not a way to answer them. A post or comment that is
// not shown is answered the same way, as Mastodon does, so a refusal does not tell of a block. q is
// a.db, or the transaction the handler holds.
func (a *app) across(q queryRower, w http.ResponseWriter, msg string, member int64, others ...int64) bool {
	var hit bool
	err := q.QueryRow(`SELECT EXISTS (SELECT 1 FROM blocks WHERE user_id = $1 AND target_id = ANY($2))
		OR EXISTS (SELECT 1 FROM blocks WHERE target_id = $1 AND user_id = ANY($2))`, member, pq.Array(others)).Scan(&hit)
	if err != nil {
		a.serverError(w, err)
		return true
	}
	if hit {
		fail(w, http.StatusNotFound, msg)
	}
	return hit
}

func (a *app) handleLike(w http.ResponseWriter, r *http.Request, u *user) {
	w, ok := allowed(w, a.lim.likes, u, "likes")
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	// Taking one's own like back always works: across a block, either way, and on a post a moderator
	// hid. It is decided by the like alone, so the answer says only what the member knew, that they had
	// liked the post; the count comes with it only when the post is shown to them (unliked).
	if done := a.unliked(w, `DELETE FROM likes WHERE post_id = $1 AND user_id = $2`,
		`SELECT (SELECT count(*) FROM likes WHERE post_id = $1) FROM posts p WHERE p.id = $1 AND `+postShown, id, u); done {
		return
	}
	owner, ok := a.postOwner(w, id, u)
	if !ok || a.across(a.db, w, "No such post.", u.ID, owner) {
		return
	}
	// One statement, so two quick clicks cannot race into a duplicate key; a like that is there
	// already was made by the other click, and stays.
	res, err := a.db.Exec(`INSERT INTO likes (post_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, id, u.ID)
	if err != nil {
		a.serverError(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 1 {
		// Liking, unliking and liking again tells the author once.
		var told bool
		if err := a.db.QueryRow(`SELECT EXISTS (SELECT 1 FROM notifications WHERE user_id = $1 AND actor_id = $2 AND post_id = $3 AND kind = 'like')`, owner, u.ID, id).Scan(&told); err != nil {
			a.serverError(w, err)
			return
		}
		if !told {
			a.notify(owner, u.ID, "like", id, "")
		}
	}
	var likes int
	if err := a.db.QueryRow(`SELECT count(*) FROM likes WHERE post_id = $1`, id).Scan(&likes); err != nil {
		a.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"liked": true, "likes": likes})
}

// postShown is the condition that post p is shown to the viewer ($2, and $3 whether they may hide
// posts), as postOwner asks it.
var postShown = `(p.hidden_at IS NULL OR p.user_id = $2 OR $3) AND ` + blockFree("$2", "p.user_id", "$3")

// unliked is the first half of a like toggle: del removes the member's own like ($1 the post or
// comment, $2 the member), whatever stands between them and the author. When there was one it answers
// the request, {"liked": false}, with "likes" read by count ($1, $2, $3 whether they may hide posts)
// only if that finds the thing shown to them, and reports true. With no like to take back it does
// nothing and the toggle goes on to like.
func (a *app) unliked(w http.ResponseWriter, del, count string, id int64, u *user) bool {
	res, err := a.db.Exec(del, id, u.ID)
	if err != nil {
		a.serverError(w, err)
		return true
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false
	}
	out := map[string]any{"liked": false}
	var likes int
	err = a.db.QueryRow(count, id, u.ID, can(u, permPostHide)).Scan(&likes)
	if err == nil {
		out["likes"] = likes
	} else if !errors.Is(err, sql.ErrNoRows) {
		a.serverError(w, err)
		return true
	}
	writeJSON(w, http.StatusOK, out)
	return true
}

/* ---------- bookmarks: skills, combos and posts to come back to ---------- */

const maxBookmarks = 500

// A combo's id is made by the page (uid in web/app.js: base 36 time and a few random characters).
var comboIDRE = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)

// bookmarkRef checks a bookmark's kind and ref for u, and answers the client when they are no good.
// A post must exist and be visible to u; a skill is "prop:skill" with a known prop; a combo is an id.
func (a *app) bookmarkRef(w http.ResponseWriter, u *user, kind, ref string) bool {
	switch kind {
	case "skill":
		prop, skill, ok := strings.Cut(ref, ":")
		if !ok || !knownProp(prop) || !skillIDRE.MatchString(skill) {
			fail(w, http.StatusBadRequest, "A skill bookmark names a prop and a skill id, like nunchaku:F5.")
			return false
		}
	case "combo":
		if !comboIDRE.MatchString(ref) {
			fail(w, http.StatusBadRequest, "That is not a combo id.")
			return false
		}
	case "post":
		id, err := strconv.ParseInt(ref, 10, 64)
		if err != nil || id <= 0 {
			fail(w, http.StatusBadRequest, "That is not a post id.")
			return false
		}
		if _, ok := a.postOwner(w, id, u); !ok {
			return false
		}
	default:
		fail(w, http.StatusBadRequest, "A bookmark is a skill, a combo or a post.")
		return false
	}
	return true
}

// handleBookmark adds the bookmark, or removes it when it is already there, and says which.
func (a *app) handleBookmark(w http.ResponseWriter, r *http.Request, u *user) {
	w, ok := allowed(w, a.lim.bookmarks, u, "bookmarks")
	if !ok {
		return
	}
	var in struct{ Kind, Ref string }
	if !readJSON(w, r, 1024, &in) {
		return
	}
	in.Ref = strings.TrimSpace(in.Ref)
	if !a.bookmarkRef(w, u, in.Kind, in.Ref) {
		return
	}
	// One statement either way, so two quick clicks cannot race into a duplicate key. The cap is
	// checked in the same statement, so a burst cannot pass it.
	res, err := a.db.Exec(`INSERT INTO bookmarks (user_id, kind, ref) SELECT $1, $2, $3
		WHERE (SELECT count(*) FROM bookmarks WHERE user_id = $1) < $4 ON CONFLICT DO NOTHING`, u.ID, in.Kind, in.Ref, maxBookmarks)
	if err != nil {
		a.serverError(w, err)
		return
	}
	n, _ := res.RowsAffected()
	on := n == 1
	if !on {
		res, err := a.db.Exec(`DELETE FROM bookmarks WHERE user_id = $1 AND kind = $2 AND ref = $3`, u.ID, in.Kind, in.Ref)
		if err != nil {
			a.serverError(w, err)
			return
		}
		if gone, _ := res.RowsAffected(); gone == 0 {
			fail(w, http.StatusBadRequest, fmt.Sprintf("You have %d bookmarks, which is the most Flowa keeps. Remove one first.", maxBookmarks))
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"on": on})
}

// bookmarksSQL is everything a member has bookmarked, newest first; the posts among them are then
// read with feedSQL by id.
const bookmarksSQL = `SELECT kind, ref FROM bookmarks WHERE user_id = $1 ORDER BY created_at DESC, ref`

// handleBookmarks lists the viewer's bookmarks, newest first: skill and combo refs, and the bookmarked
// posts in full, as the timeline shows them. A post the viewer may no longer see is left out.
func (a *app) handleBookmarks(w http.ResponseWriter, _ *http.Request, u *user) {
	rows, err := a.db.Query(bookmarksSQL, u.ID)
	if err != nil {
		a.serverError(w, err)
		return
	}
	defer rows.Close()
	skills, combos, ids := []string{}, []string{}, []int64{}
	for rows.Next() {
		var kind, ref string
		if err := rows.Scan(&kind, &ref); err != nil {
			a.serverError(w, err)
			return
		}
		switch kind {
		case "skill":
			skills = append(skills, ref)
		case "combo":
			combos = append(combos, ref)
		case "post":
			id, _ := strconv.ParseInt(ref, 10, 64)
			ids = append(ids, id)
		}
	}
	if err := rows.Err(); err != nil {
		a.serverError(w, err)
		return
	}
	out, err := a.loadPosts(u, 0, 0, 0, ids...)
	if err != nil {
		a.serverError(w, err)
		return
	}
	// The posts carry their own ids; the list of ids says what is bookmarked, in bookmark order.
	visible := []int64{}
	for _, p := range out["posts"].([]*post) {
		visible = append(visible, p.ID)
	}
	out["postIds"], out["skills"], out["combos"] = visible, skills, combos
	delete(out, "more")
	writeJSON(w, http.StatusOK, out)
}

/* ---------- comments and replies ---------- */

// commentBlockFree is blockFree for a comment c in the queries queryComments runs, where $1 is the
// viewer and $2 whether they may hide posts. Each of those queries says it inside, before its LIMIT.
var commentBlockFree = blockFree("$1", "c.user_id", "$2")

var commentCols = `c.id, c.post_id, c.user_id, COALESCE(c.parent_id, 0), c.body, c.created_at, c.edited_at IS NOT NULL, c.deleted,
	(SELECT count(*) FROM comment_likes l WHERE l.comment_id = c.id), EXISTS (SELECT 1 FROM comment_likes l WHERE l.comment_id = c.id AND l.user_id = $1),
	(SELECT count(*) FROM comments r WHERE r.parent_id = c.id AND r.hidden_at IS NULL AND ` + blockFree("$1", "r.user_id", "$2") + `), COALESCE(c.hidden_reason, ''), c.hidden_at IS NOT NULL`

var repliesSQL = `SELECT * FROM (SELECT c.*, row_number() OVER (PARTITION BY c.parent_id ORDER BY c.id DESC) AS rn
		FROM comments c WHERE c.parent_id = ANY($3) AND ` + commentBlockFree + `) c WHERE c.rn <= $4`

// commentsSQL wraps inner, a query over the comments table, into the one queryComments runs.
func commentsSQL(inner string) string {
	return `SELECT ` + commentCols + ` FROM (` + inner + `) c ORDER BY c.id`
}

// queryComments runs inner, a query over the comments table c whose first parameter is $3, and
// returns its rows oldest first, with likes and reply counts as seen by viewer ($1, and $2 whether
// they may hide posts). A query that lists comments says commentBlockFree in its WHERE.
// A hidden comment is shown in full to its author and moderators; to everyone else it is left
// out, or kept as an empty placeholder when it has replies that are still shown.
func (a *app) queryComments(viewer *user, inner string, args ...any) ([]*comment, error) {
	rows, err := a.db.Query(commentsSQL(inner), append([]any{viewer.ID, can(viewer, permPostHide)}, args...)...)
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
	reps, err := a.queryComments(viewer, repliesSQL, pq.Array(ids), previewReplies)
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
	list, err := a.queryComments(u, `SELECT * FROM comments c WHERE c.post_id = $3 AND c.parent_id IS NULL AND c.id > $4 AND `+commentBlockFree+` ORDER BY c.id LIMIT $5`, id, queryID(r, "after"), commentPage+1)
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

// commentShown is comment c with its post p, when it is shown to the viewer ($1 the comment, $2 the
// viewer, $3 whether they may hide posts), as commentRow asks it.
var commentShown = `comments c JOIN posts p ON p.id = c.post_id LEFT JOIN comments rc ON rc.id = c.parent_id WHERE c.id = $1
		AND (c.hidden_at IS NULL OR c.user_id = $2 OR $3) AND (p.hidden_at IS NULL OR p.user_id = $2 OR $3)
		AND ` + blockFree("$2", "c.user_id", "$3") + ` AND ` + blockFree("$2", "p.user_id", "$3") + ` AND (rc.id IS NULL OR ` + blockFree("$2", "rc.user_id", "$3") + `)`

func (a *app) commentRow(w http.ResponseWriter, id int64, viewer *user) (commentRow, bool) {
	var c commentRow
	var secs float64
	// A comment is gone to the viewer when it, the comment it answers or its post is by someone on
	// the other side of a block (blockFree), as it is when it was hidden.
	err := a.db.QueryRow(`SELECT c.post_id, c.user_id, COALESCE(c.parent_id, 0), p.user_id, c.deleted, extract(epoch FROM now() - c.created_at), c.body
		FROM `+commentShown, id, viewer.ID, can(viewer, permPostHide)).Scan(&c.postID, &c.userID, &c.parentID, &c.postOwner, &c.deleted, &secs, &c.body)
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
	list, err := a.queryComments(u, `SELECT * FROM comments c WHERE c.parent_id = $3 AND c.id > $4 AND `+commentBlockFree+` ORDER BY c.id LIMIT $5`, id, queryID(r, "after"), replyPage+1)
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
	roots, err := a.queryComments(u, `SELECT * FROM comments c WHERE c.id = $3`, root)
	if err != nil || len(roots) == 0 {
		a.serverError(w, fmt.Errorf("thread %d: %v", root, err))
		return
	}
	reps, err := a.queryComments(u, `SELECT * FROM comments c WHERE c.parent_id = $3 AND `+commentBlockFree+` ORDER BY c.id LIMIT $4`, root, maxThread)
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
	// A repeat of a comment already made is a read: it is answered before the mute and the allowance.
	prior, finish, ok := a.once(w, r, u)
	if !ok {
		return
	}
	if prior != 0 {
		a.writeComment(w, u, id, prior)
		return
	}
	var made int64
	defer func() { finish(made) }()
	if muted(w, u) {
		return
	}
	w, ok = allowed(w, a.lim.comments, u, "comments")
	if !ok {
		return
	}
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
	if !ok || a.across(a.db, w, "No such post.", u.ID, owner) {
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
		var postID, parent, rootOwner int64
		var deleted, hidden bool
		err := tx.QueryRow(`SELECT c.post_id, c.user_id, COALESCE(c.parent_id, 0), c.deleted, c.hidden_at IS NOT NULL, COALESCE(rc.user_id, 0)
			FROM comments c LEFT JOIN comments rc ON rc.id = c.parent_id WHERE c.id = $1 FOR SHARE OF c`, in.ParentID).Scan(&postID, &answered, &parent, &deleted, &hidden, &rootOwner)
		if err == nil && hidden && answered != u.ID && !can(u, permPostHide) {
			err = sql.ErrNoRows
		}
		// Nobody answers a comment, or joins a thread, begun by someone on the other side of a block.
		// It asks the transaction: a.db here would wait for a second connection while this one is held.
		if err == nil && a.across(tx, w, "That comment is gone.", u.ID, answered, rootOwner) {
			return
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
	made = cid
	a.notifyMentions(body, u.ID, id, cid, owner, answered)
	a.writeComment(w, u, id, cid)
}

// writeComment answers with the post and the comment cid on it, as the page expects after
// commenting. A repeat of a comment that has since been removed gets a 404.
func (a *app) writeComment(w http.ResponseWriter, u *user, id, cid int64) {
	out, err := a.loadPosts(u, 0, 0, id)
	if err != nil {
		a.serverError(w, err)
		return
	}
	list, err := a.queryComments(u, `SELECT * FROM comments c WHERE c.id = $3 AND c.post_id = $4`, cid, id)
	if err != nil {
		a.serverError(w, err)
		return
	}
	if len(out["posts"].([]*post)) == 0 || len(list) == 0 {
		fail(w, http.StatusNotFound, "That comment is gone.")
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
	list, err := a.queryComments(u, `SELECT * FROM comments c WHERE c.id = $3`, id)
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

// handleLikeComment toggles a like on a comment, one statement either way.
func (a *app) handleLikeComment(w http.ResponseWriter, r *http.Request, u *user) {
	w, ok := allowed(w, a.lim.likes, u, "likes")
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	// Taking one's own like back always works, as it does for posts (handleLike).
	if done := a.unliked(w, `DELETE FROM comment_likes WHERE comment_id = $1 AND user_id = $2`,
		`SELECT (SELECT count(*) FROM comment_likes WHERE comment_id = $1) FROM `+commentShown, id, u); done {
		return
	}
	c, ok := a.commentRow(w, id, u)
	if !ok || a.across(a.db, w, "That comment is gone.", u.ID, c.userID) {
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
	if n, _ := res.RowsAffected(); n == 1 {
		var told bool
		if err := a.db.QueryRow(`SELECT EXISTS (SELECT 1 FROM notifications WHERE user_id = $1 AND actor_id = $2 AND comment_id = $3 AND kind = 'comment_like')`, c.userID, u.ID, id).Scan(&told); err != nil {
			a.serverError(w, err)
			return
		}
		if !told {
			a.notifyAt(c.userID, u.ID, "comment_like", c.postID, id, "")
		}
	}
	var likes int
	if err := a.db.QueryRow(`SELECT count(*) FROM comment_likes WHERE comment_id = $1`, id).Scan(&likes); err != nil {
		a.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"liked": true, "likes": likes})
}

/* ---------- blocks: members a member does not want to see or hear from ---------- */

// blockTarget finds the member a block or an unblock is about, by username as the profile route
// does, and answers the request itself when it is no good: nobody blocks themself (400), and a name
// that is no account is a 404 in the profile route's words. A name and not an id, so that blocking and
// the list of blocks tell nobody anything the profile of that name does not: by ids, trying each in
// turn would put a name to every account, those who never posted and are not in the people list too.
// Follows find their member the same way, for the same reason (memberNamed).
func (a *app) blockTarget(w http.ResponseWriter, r *http.Request, u *user) (int64, bool) {
	return a.memberNamed(w, r, u, "You cannot block yourself.")
}

// memberNamed is blockTarget with the words for the one asking about themself.
func (a *app) memberNamed(w http.ResponseWriter, r *http.Request, u *user, self string) (int64, bool) {
	name := strings.ToLower(r.PathValue("username"))
	if name == u.Username {
		fail(w, http.StatusBadRequest, self)
		return 0, false
	}
	var id int64
	err := a.db.QueryRow(`SELECT id FROM users WHERE username = $1 AND domain = ''`, name).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "No such user.")
		return 0, false
	}
	if err != nil {
		a.serverError(w, err)
		return 0, false
	}
	return id, true
}

// handleBlock blocks a member for the one asking; blocking again changes nothing and is answered
// the same. A name that is no account, or one deleted meanwhile, is a 404. Anyone may be blocked, a
// moderator or an admin too, and anyone signed in may block, a muted member too: a block is for the
// blocker's own peace, not something they say to others. It never weakens moderation: reports,
// the queue and moderators' actions do not look at blocks, and a moderator still sees the posts of a
// member who blocked them (blockFree). The blocked member is not told.
func (a *app) handleBlock(w http.ResponseWriter, r *http.Request, u *user) {
	w, ok := allowed(w, a.lim.blocks, u, "blocks")
	if !ok {
		return
	}
	id, ok := a.blockTarget(w, r, u)
	if !ok {
		return
	}
	// One statement, so a quick second click is one row; the account must exist as it is written. The
	// notifications either has had from the other (likes, comments, replies, mentions, follows) go with
	// it: the blocker hears no more of the blocked member, old news included, and the other way round.
	// So do the follows between the two, both ways, as Mastodon removes them: neither stays in the
	// other's Home, lists or counts, and following again is refused while the block holds (handleFollow).
	var found bool
	err := a.db.QueryRow(`WITH t AS (SELECT id FROM users WHERE id = $2 AND domain = ''),
		ins AS (INSERT INTO blocks (user_id, target_id) SELECT $1, id FROM t ON CONFLICT DO NOTHING),
		gone AS (DELETE FROM notifications WHERE kind <> ALL($3) AND ((user_id = $1 AND actor_id = $2) OR (user_id = $2 AND actor_id = $1))),
		unfollowed AS (DELETE FROM follows WHERE (follower_id = $1 AND followee_id = $2) OR (follower_id = $2 AND followee_id = $1))
		SELECT EXISTS (SELECT 1 FROM t)`, u.ID, id, pq.Array(slices.Sorted(maps.Keys(quietKinds)))).Scan(&found)
	var pe *pq.Error
	if errors.As(err, &pe) && pe.Code == "23503" {
		found, err = false, nil // the account was deleted while the block was written
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	if !found {
		fail(w, http.StatusNotFound, "No such user.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"blocked": true})
}

// handleUnblock takes a block back. There being no such block is not an error: the answer is the
// same whether there was one or not. A name that is no account is a 404, as for a block: the profile
// route answers the same for it, so this tells nothing new, and an account that is gone has no block
// to take back (a deleted account's blocks went with it), so the page can drop it from its list.
func (a *app) handleUnblock(w http.ResponseWriter, r *http.Request, u *user) {
	w, ok := allowed(w, a.lim.blocks, u, "blocks")
	if !ok {
		return
	}
	id, ok := a.blockTarget(w, r, u)
	if !ok {
		return
	}
	if _, err := a.db.Exec(`DELETE FROM blocks WHERE user_id = $1 AND target_id = $2`, u.ID, id); err != nil {
		a.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"blocked": false})
}

// blockListSQL is the accounts a member has blocked, the latest first ($1 the member, $2 how many).
// The blocks are chosen first and the accounts read after, so a long list reads its own rows of
// blocks through the primary key and only the accounts it shows.
const blockListSQL = `SELECT ` + userCols + `, b.created_at FROM (SELECT target_id, created_at FROM blocks WHERE user_id = $1
	ORDER BY created_at DESC, target_id LIMIT $2) b JOIN users u ON u.id = b.target_id ORDER BY b.created_at DESC, b.target_id`

// maxBlockList is how many blocks the list shows, the latest; any older ones still hold.
const maxBlockList = 500

// handleBlocks lists the accounts the one asking has blocked, for the list in their profile where
// they can unblock. Only their own: nobody is shown who blocked them.
func (a *app) handleBlocks(w http.ResponseWriter, _ *http.Request, u *user) {
	rows, err := a.db.Query(blockListSQL, u.ID, maxBlockList)
	if err != nil {
		a.serverError(w, err)
		return
	}
	defer rows.Close()
	type blocked struct {
		User  pubUser   `json:"user"`
		Since time.Time `json:"since"`
	}
	out := []blocked{}
	for rows.Next() {
		var since time.Time
		b, err := scanUser(rows, &since)
		if err != nil {
			a.serverError(w, err)
			return
		}
		out = append(out, blocked{b.public(), since})
	}
	if err := rows.Err(); err != nil {
		a.serverError(w, err)
		return
	}
	var total int
	if err := a.db.QueryRow(`SELECT count(*) FROM blocks WHERE user_id = $1`, u.ID).Scan(&total); err != nil {
		a.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"blocks": out, "total": total})
}

/* ---------- follows: members whose posts a member wants in Home ---------- */

// maxFollows is how many accounts one member may follow. It is what bounds the rows a page of Home
// reads: one page of posts per account followed at most (homeSQL).
const maxFollows = 1000

// followCountsShown says whether follow counts are shown at all: the follows routes and a profile's counts
// sit behind both the community flag (the social routes) and the follows flag. A member's profile in the
// app and the followers and following collections of a mode that federates (serveFollows) both ask it,
// so the public documents never show a count the app does not.
func (a *app) followCountsShown() bool {
	f := a.flagsCached()
	return f["community"] && f["follows"]
}

// handleFollow follows a member for the one asking; following again changes nothing and is answered
// the same. Nobody follows themself (400), and a name that is no account is a 404 in the profile
// route's words. Across a block, whichever blocked whom, the answer is a 403 with the same sentence
// both ways, "You cannot follow this account.": the account is there (its profile opens), so a 404
// would be false, and one sentence for both sides does not say who blocked whom. The blocked member
// can still tell that something stands between them; docs/federation.md says so. A muted member may
// follow: a mute stops what they say, not what they read. The one followed is told once, with a
// follow notification; unfollowing and following again within a day tells them nothing new, and
// across a block nothing is told at all (notifyAt).
//
// The follow is written in a short transaction that first locks the follower's row of users, so the
// follows of one member are written one at a time: a second identical follow sent at the same moment
// waits, then finds the first one's row and is answered as a repeat, and the limit is counted with
// every earlier follow of that member in sight, so it is never passed. Everything between Begin and
// Commit asks the transaction, never a.db (TestDBOneConnection).
func (a *app) handleFollow(w http.ResponseWriter, r *http.Request, u *user) {
	w, ok := allowed(w, a.lim.follows, u, "follows")
	if !ok {
		return
	}
	id, ok := a.memberNamed(w, r, u, "You cannot follow yourself.")
	if !ok {
		return
	}
	tx, err := a.db.Begin()
	if err != nil {
		a.serverError(w, err)
		return
	}
	defer tx.Rollback()
	// FOR NO KEY UPDATE: it holds back another follow by this member, and nothing that only points at
	// the row, such as their new post or like.
	if _, err := tx.Exec(`SELECT 1 FROM users WHERE id = $1 FOR NO KEY UPDATE`, u.ID); err != nil {
		a.serverError(w, err)
		return
	}
	// The account must still be there and no block may stand between the two as the row is written.
	// Statements in a READ COMMITTED transaction each see what was committed before they began, so
	// after the lock this one sees every follow the member made before. had is whether this one was there.
	var found, free, made, had bool
	err = tx.QueryRow(`WITH t AS (SELECT id FROM users WHERE id = $2 AND domain = ''),
		ok AS (SELECT id FROM t WHERE `+blockFree("$1::bigint", "t.id", "false")+`),
		ins AS (INSERT INTO follows (follower_id, followee_id) SELECT $1, id FROM ok
			WHERE (SELECT count(*) FROM follows WHERE follower_id = $1) < $3 ON CONFLICT DO NOTHING RETURNING 1)
		SELECT EXISTS (SELECT 1 FROM t), EXISTS (SELECT 1 FROM ok), EXISTS (SELECT 1 FROM ins),
			EXISTS (SELECT 1 FROM follows WHERE follower_id = $1 AND followee_id = $2)`,
		u.ID, id, maxFollows).Scan(&found, &free, &made, &had)
	var pe *pq.Error
	if errors.As(err, &pe) && pe.Code == "23503" {
		found, err = false, nil // the account was deleted while the follow was written
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	switch {
	case !found:
		fail(w, http.StatusNotFound, "No such user.")
		return
	case !free:
		fail(w, http.StatusForbidden, "You cannot follow this account.")
		return
	case !made && !had:
		fail(w, http.StatusBadRequest, fmt.Sprintf("You follow %d accounts, the most there can be. Unfollow someone first.", maxFollows))
		return
	}
	if err := tx.Commit(); err != nil {
		a.serverError(w, err)
		return
	}
	if made {
		var told bool
		if err := a.db.QueryRow(`SELECT EXISTS (SELECT 1 FROM notifications WHERE user_id = $1 AND actor_id = $2 AND kind = 'follow'
			AND created_at > now() - interval '1 day')`, id, u.ID).Scan(&told); err != nil {
			a.serverError(w, err)
			return
		}
		if !told {
			a.notifyAt(id, u.ID, "follow", 0, 0, "")
		}
	}
	a.followAnswer(w, id, true, made)
}

// followAnswer answers a follow or an unfollow of member id: whether the one asking now follows them,
// whether this request changed that, and how many follow them now, so a page that was out of date
// shows the true count and does not count a change twice.
func (a *app) followAnswer(w http.ResponseWriter, id int64, following, changed bool) {
	var followers, followed int
	if err := a.db.QueryRow(followCountsSQL, id).Scan(&followers, &followed); err != nil {
		a.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"following": following, "changed": changed, "followers": followers})
}

// handleUnfollow takes a follow back. There being no such follow is not an error, and neither is a
// block: the answer is the same either way. A name that is no account is a 404, as for unblocking.
func (a *app) handleUnfollow(w http.ResponseWriter, r *http.Request, u *user) {
	w, ok := allowed(w, a.lim.follows, u, "follows")
	if !ok {
		return
	}
	id, ok := a.memberNamed(w, r, u, "You cannot follow yourself.")
	if !ok {
		return
	}
	res, err := a.db.Exec(`DELETE FROM follows WHERE follower_id = $1 AND followee_id = $2`, u.ID, id)
	if err != nil {
		a.serverError(w, err)
		return
	}
	n, _ := res.RowsAffected()
	a.followAnswer(w, id, false, n == 1)
}

// followShown is the condition that follow f between member $1 and other is shown and counted: no
// block stands between the two, whichever way. A block removes the follows between them (handleBlock),
// but a follow written at the same moment as the block can outlast it, and it is left out here as in
// Home (homeSQL). One look in the primary key of blocks each way per follow.
func followShown(other string) string { return blockFree("$1", other, "false") }

// followingSQL is the accounts a member follows and followersSQL those who follow them, the latest
// first ($1 the member, $2 how many). Like blockListSQL, the rows of follows are chosen first through
// the primary key or follows_followee, and only the accounts shown are read.
var followingSQL = `SELECT ` + userCols + `, f.created_at FROM (SELECT f.followee_id AS id, f.created_at FROM follows f WHERE f.follower_id = $1
	AND ` + followShown("f.followee_id") + ` ORDER BY f.created_at DESC, f.followee_id LIMIT $2) f JOIN users u ON u.id = f.id ORDER BY f.created_at DESC, f.id`

var followersSQL = `SELECT ` + userCols + `, f.created_at FROM (SELECT f.follower_id AS id, f.created_at FROM follows f WHERE f.followee_id = $1
	AND ` + followShown("f.follower_id") + ` ORDER BY f.created_at DESC, f.follower_id LIMIT $2) f JOIN users u ON u.id = f.id ORDER BY f.created_at DESC, f.id`

// followCountsSQL is how many follow member $1 and how many they follow: the rows of one index each,
// with followShown asked of each.
var followCountsSQL = `SELECT (SELECT count(*) FROM follows f WHERE f.followee_id = $1 AND ` + followShown("f.follower_id") + `),
	(SELECT count(*) FROM follows f WHERE f.follower_id = $1 AND ` + followShown("f.followee_id") + `)`

// maxFollowList is how many accounts each list shows, the latest; the counts say how many there are.
const maxFollowList = 500

// handleFollowing and handleFollowers list the accounts the one asking follows, and those who follow
// them. Only their own: whom a member follows and who follows them is shown to nobody else, who see
// the counts on the profile only (F10 in docs/federation.md).
func (a *app) handleFollowing(w http.ResponseWriter, _ *http.Request, u *user) {
	a.followList(w, u, followingSQL, "following")
}

func (a *app) handleFollowers(w http.ResponseWriter, _ *http.Request, u *user) {
	a.followList(w, u, followersSQL, "followers")
}

// followList answers with one of the two lists (q), under key, and its total.
func (a *app) followList(w http.ResponseWriter, u *user, q, key string) {
	rows, err := a.db.Query(q, u.ID, maxFollowList)
	if err != nil {
		a.serverError(w, err)
		return
	}
	defer rows.Close()
	type member struct {
		User  pubUser   `json:"user"`
		Since time.Time `json:"since"`
	}
	out := []member{}
	for rows.Next() {
		var since time.Time
		m, err := scanUser(rows, &since)
		if err != nil {
			a.serverError(w, err)
			return
		}
		out = append(out, member{m.public(), since})
	}
	if err := rows.Err(); err != nil {
		a.serverError(w, err)
		return
	}
	var followers, following int
	if err := a.db.QueryRow(followCountsSQL, u.ID).Scan(&followers, &following); err != nil {
		a.serverError(w, err)
		return
	}
	total := map[string]int{"followers": followers, "following": following}[key]
	writeJSON(w, http.StatusOK, map[string]any{key: out, "total": total})
}

// handleHome is a page of Home (homeSQL), in the same shape as the Community timeline, paged the same
// way with ?before=. It has its own route rather than a parameter on GET /api/posts, so the follows
// flag closes it as it closes the other follow routes, and the Community timeline's query is not touched.
func (a *app) handleHome(w http.ResponseWriter, r *http.Request, u *user) {
	out, err := a.readPosts(u, homeSQL, u.ID, queryID(r, "before"), pageSize+1, can(u, permPostHide))
	if err != nil {
		a.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *app) socialRoutes(mux *routeMux) {
	// The community flag turns the timeline off for the API too, not only in the menu, and the game
	// flag does the same for badges. Pictures stay: the profile and the admin pages show them.
	social := func(h func(http.ResponseWriter, *http.Request, *user)) http.HandlerFunc {
		return a.flagged("community", a.authed("", h))
	}
	mux.HandleFunc("GET /api/users", social(a.handleUserList))
	mux.HandleFunc("GET /api/users/{id}/avatar", a.authed("", a.handleAvatar))
	mux.HandleFunc("GET /api/users/{username}", social(a.handleUserProfile))
	mux.HandleFunc("GET /api/posts", social(a.handleGetPosts))
	mux.HandleFunc("POST /api/posts", social(a.handleCreatePost))
	mux.HandleFunc("GET /api/posts/{id}", social(a.handleGetPost))
	mux.HandleFunc("DELETE /api/posts/{id}", social(a.handleDeletePost))
	mux.HandleFunc("POST /api/posts/{id}/like", social(a.handleLike))
	mux.HandleFunc("GET /api/bookmarks", a.flagged("bookmarks", a.authed("", a.handleBookmarks)))
	mux.HandleFunc("POST /api/bookmarks", a.flagged("bookmarks", a.authed("", a.handleBookmark)))
	mux.HandleFunc("POST /api/posts/{id}/comments", social(a.handleComment))
	mux.HandleFunc("GET /api/posts/{id}/comments", social(a.handleGetComments))
	mux.HandleFunc("DELETE /api/comments/{id}", social(a.handleDeleteComment))
	mux.HandleFunc("PUT /api/comments/{id}", social(a.handleEditComment))
	mux.HandleFunc("GET /api/comments/{id}/replies", social(a.handleGetReplies))
	mux.HandleFunc("GET /api/comments/{id}/thread", social(a.handleThread))
	mux.HandleFunc("POST /api/comments/{id}/like", social(a.handleLikeComment))
	mux.HandleFunc("POST /api/achievements", a.flagged("game", a.authed("", a.handleAchievements)))
	mux.HandleFunc("GET /api/notifications", social(a.handleNotifications))
	mux.HandleFunc("PUT /api/notifications/read", social(a.handleReadNotifications))
	mux.HandleFunc("GET /api/blocks", social(a.handleBlocks))
	mux.HandleFunc("PUT /api/users/{username}/block", social(a.handleBlock))
	mux.HandleFunc("DELETE /api/users/{username}/block", social(a.handleUnblock))
	// Follows and Home are behind their own flag too, off by default: with it off they are not there.
	follows := func(h func(http.ResponseWriter, *http.Request, *user)) http.HandlerFunc {
		return a.flagged("follows", social(h))
	}
	mux.HandleFunc("GET /api/home", follows(a.handleHome))
	mux.HandleFunc("GET /api/following", follows(a.handleFollowing))
	mux.HandleFunc("GET /api/followers", follows(a.handleFollowers))
	mux.HandleFunc("PUT /api/users/{username}/follow", follows(a.handleFollow))
	mux.HandleFunc("DELETE /api/users/{username}/follow", follows(a.handleUnfollow))
}

/* ---------- passwords nobody should pick ---------- */

// commonPasswordList is the 1,000 most used passwords of 8 characters or more, lower case, most
// used first. They are the first 1,000 that long in the list of the 100,000 passwords seen most
// often in breaches, published by the UK National Cyber Security Centre with Have I Been Pwned
// (PwnedPasswordsTop100k). Contains public sector information licensed under the Open Government
// Licence v3.0. Shorter ones are refused by length already. Being common, some are crude.
const commonPasswordList = `
123456789 password 12345678 password1 1234567890 iloveyou 1q2w3e4r5t qwertyuiop 1qaz2wsx myspace1 homelesspa 1q2w3e4r qwerty123 target123 987654321
1g2w3e4r zag12wsx gwerty123 asdfghjkl 123123123 computer princess football sunshine 1234qwer iloveyou1 fuckyou1 789456123 princess1 11111111
passer2009 abcd1234 football1 superman jordan23 12qwaszx baseball 123456789a fqrg7cs493 88888888 michael1 linkedin babygirl1 blink182 qwer1234
liverpool michelle j38ifubn q1w2e3r4 0123456789 iloveyou2 jennifer 147258369 babygirl jessica1 fuckyou2 0987654321 anthony1 q1w2e3r4t5y6 baseball1
qazwsxedc 1111111111 basketball passw0rd trustno1 chocolate qwerty12 charlie1 pakistan samantha asdf1234 asshole1 superman1 fuk19600 butterfly
29rsavoy sunshine1 internet alexander 00000000 12341234 whatever qweasdzxc 1qazxsw2 password12 password2 hello123 3rjs1la7qe 12344321 vqsablpzla
brandon1 starwars jonathan cjmasterinf 1234567891 12345qwert q1w2e3r4t5 a123456789 christian 12345678910 diosesfiel dpbk1234 pe#5gz29ptzmse mercedes
victoria matthew1 benjamin 123654789 elizabeth barcelona computer1 michelle1 william1 11223344 welcome1 zaq12wsx jasmine1 liverpool1 1qaz2wsx3edc
metallica slipknot chicken1 87654321 0000000000 spiderman 147852369 butterfly1 tinkerbell danielle nicholas qwerty12345 1234554321 asdfasdf pokemon1
123qweasd chelsea1 chocolate1 1234567a juventus 111222tianya america1 999999999 jennifer1 freedom1 741852963 qwertyui 1password cocacola a838hfid
1234abcd myspace123 madison1 samantha1 elizabeth1 mustang1 november diamond1 valentina whatever1 n0=acc3ss super123 password123 spongebob1 hellokitty
alexandra 1q2w3e4r5t6y december a1b2c3d4 patricia friends1 123hfjdk147 123456789q beautiful carolina 4815162342 heather1 sojdlg123aljg sebastian
caroline startfinding midnight iw14fi9j yankees1 elephant scorpion bubbles1 fuckoff1 cowboys1 patrick1 veronica melissa1 september alexander1
iloveyou! raiders1 zxcvbnm1 12121212 spongebob stephanie lovelove precious manchester cristina a1234567 qqww1122 jackson1 asdfghjkl1 qazwsx123
newyork1 poohbear richard1 destiny1 slipknot1 monster1 blessed1 spiderman1 emmanuel charlotte maverick creative beautiful1 softball 421uiopy258
brittany aa123456 kristina garfield scooter1 myspace2 steelers isabella 1a2b3c4d 9876543210 12345678a kimberly 1v7upjw3nt babyboy1 brittany1 123456abc
forever1 1122334455 metallica1 1029384756 cameron1 california christian1 password! 963852741 danielle1 samsung1 gangsta1 icecream letmein1
qwerty123456 qwert123 uqa9ebw445 leonardo christine bismillah iloveyou12 snickers arsenal1 fernando swordfish marlboro qwerty1234 twilight yagjecc826
dolphin1 loveyou1 tiffany1 hardcore rockstar playboy1 greenday angelina starwars1 123qwe123 sz9kqcctwy courtney crystal1 abc123456 marseille password3
alejandro justinbieb poohbear1 corvette jackass1 jonathan1 lollipop motorola nicholas1 friendster angel123 portugal iloveme1 brooklyn darkness
rainbow1 snowball 1234512345 shannon1 123qweasdzxc victoria1 catherine pa55word password. logitech remember popcorn1 isabelle p3rat54797 steelers1
lakers24 lizottes blessing rockstar1 aaaaaaaa florida1 peaches1 love4ever goodluck santiago strawberry abcdefg1 bigdaddy thunder1 asdfghjk vanessa1
happy123 fuckyou! iverson3 svetlana satan666 superstar babygurl1 fyfcnfcbz qwert12345 chester1 loverboy azertyuiop 5555555555 angelica yourmom1
bullshit christopher love1234 evildick midnight1 williams 55555555 charles1 sweetpea rush2112 greenday1 sunflower 1123581321 precious1 brooklyn1
stephanie1 undertaker sweetheart ihateyou fktrcfylh 123698745 21212121 lebron23 paradise kawasaki polniypizdec0211 a12345678 money123 22222222
3d8cubaj2e vladimir abc12345 shopping career121 12345qwerty christina qq123456 scarface dolphins giovanni soccer12 facebook changeme zxcvbnm123
password11 123454321 qw123321 australia southside1 soccer10 maryjane rebecca1 getmoney1 brianna1 realmadrid westside linkinpark asdasdasd francesco
inuyasha 111222333 ig4abox4 sterling ronaldo7 savannah bigdaddy1 courtney1 123456aa valentin chicago1 cjkysirj jesus123 warcraft mahalkita monkey123
hollister1 alessandro w5txn36alfw password7 awesome1 bestfriend gabriel1 gateway1 volleyball yuantuo2012 princess12 chouchou bitch123 champion
element1 fishing1 teddybear natalie1 passwort francesca qwerasdf apple123 gabriela mariposa abcdefgh cheyenne babygirl12 zachary1 antonio1 veronika
111111111 nintendo happiness monkey12 engineer bitches1 estrella grandma1 princesa virginia scarface1 chris123 anderson abcdefg123 77777777 blahblah
aleksandr giuseppe softball1 vkontakte simpsons godisgood twilight1 pumpkin1 147896325 loveyou2 ladybug1 benjamin1 kingkong fuckyou123 florence
adgjmptw cookies1 margarita hallo123 pass1234 children zzzzzzzz cristian password5 mamapapa soccer11 hollywood password01 inuyasha1 amoremio serenity
sabrina1 n8zgt5p0shw= rangers1 123456qwerty nirvana1 business panasonic gangster 25802580 colorado microsoft superstar1 groupd2013 31415926 dbrnjhbz
airforce1 3odi15ngxb internet1 westside1 christina1 flowers1 icecream1 bulldog1 redneck1 johncena1 myspace! asdasd123 kathleen cupcake1 skittles
frankie1 p@ssw0rd christmas qti7zxh18u football12 snickers1 lasvegas september1 123456789m marshall minecraft terminator security drowssap 123456789z
19871987 poiuytrewq jackson5 kimberly1 rammstein alexandre scotland asdfjkl: motdepasse harrypotter babydoll 192837465 321654987 iloveyou. truelove
runescape1 casanova spencer1 francisco chocolat christophe phoenix1 jesus777 99999999 1qaz1qaz killer123 rhbcnbyf alejandra stonecold a1s2d3f4
mitchell g9l2d1fzpy babygirl2 19851985 welcome123 franklin carpediem aaaaaaaaaa zaq1xsw2 megaparol12345 admin123 handsome shithead anastasia 12369874
savannah1 sweetie1 diwtgm8492 1234567q margaret muhammad maxwell1 123456654321 india123 platinum gangster1 passport jesuschrist hotmail1 katherine
myspace12 123321123 snowball1 asdfghjkl: patches1 d1lakiss trouble1 69696969 valentine hercules beatrice wolverine jamesbond 19841984 maradona
green123 amsterdam mercedes1 yahoo.com 19861986 goldfish james123 lawrence winston1 princess2 14789632 idontknow hollywood1 diamonds trinity1
ihateyou1 cutiepie soccer13 bulldogs onelove1 success1 einstein forever21 135792468 nathalie hahahaha harrison timothy1 123456123 1234asdf anhyeuem
buttercup argentina cocacola1 soccer123 omsairam georgia1 stephen1 johncena maryjane1 dragonball stargate colombia 19891989 infinity trfnthbyf
redskins digital1 sexybitch1 dinosaur 7894561230 sureno13 penelope 19921992 runescape paintball1 test1234 summer08 ciaociao football2 swimming
telechargement orlando1 nonmember password4 shithead1 thuglife vrbgqns997 nothing1 tequiero comeon11 lacrosse scoobydoo zxcv1234 123789456 bullshit1
monique1 wordpass 1qa2ws3ed cutiepie1 sebastian1 gordon24 sexygirl1 ilovegod kittycat cleopatra 19821982 qwerty321 porsche1 password13 19801980
david123 teddybear1 baseball12 katerina 11112222 qazwsx12 fuckyou69 golfcourse martinez elephant1 ilovejesus 123456123456 houston1 allison1 smoke420
godzilla peterpan buddy123 iloveme2 skittles1 x4ivyga51f 9-11-1961 pineapple panthers napoleon chopper1 drummer1 11235813 federico music123 startrek
vincent1 qazxswedc mohammed qwe12345 cambiami iloveyou3 christine1 friendship pa55w0rd pokemon123 mountain 12301230 raymond1 bradley1 packers1
isabella1 19831983 123456789s catalina wildcats michigan butthead myspace. 13131313 vampires charmed1 strength panther1 cdtnkfyf charlotte1 barcelona1
hardcore1 1loveyou pussycat spitfire tnk0mk16vx babygurl qweasd123 bluebird 123456987 carolina1 asdqwe123 sexygirl 2012comeer medicine something
naruto123 10101010 fordf150 asd123456 ilovehim1 moonlight 10203040 yfdbufnjh10305070 johnson1 password10 birthday baseball2 dearbook 19951995
password0 blackberry lilwayne1 66666666 firebird salvador drpepper melanie1 mypassword thebest1 natasha1 ferrari1 fuku00198 pebbles1 abigail1
spartan117 qwerty11 cassandra tiger123 genesis1 guinness personal alabama1 jaimatadi darkangel evony192 19881988 budlight1 predator ka_djkhjsy6
alejandro1 newport1 montana1 20102010 fuckme69 sporting indonesia fernanda devil666 aquarius dragon123 mexican1 12qw23we 1234567890q 44444444 lovelife
sample123 penguin1 bearshare remember1 lorraine aaliyah1 blondie1 jeremiah capricorn 12131415 wrestling1 cherokee asdfasdf1 kenneth1 blablabla
veronica1 summer09 passer2011 cricket1 cooldude 123qwerty myspace3 skateboard sk84life westlife defender dumbass1 vampire1 darkness1 daniel123
azerty123 assassin testtest youbye123 dragons1 14531453 federica explorer dickhead babylove formula1 password9 wangyut2 cheyenne1 monkeys1 dominique
tokiohotel alexandra1 papillon dolphins1 babyblue pitbull1 miranda1 sebastien teacher1 monalisa ekaterina 19941994 jeffrey1 sapphire england1 19931993
1234567899 1myspace dragon12 killer12 agent007 123456789j getmoney 33333333 idontknow1 wrestling jamaica1 lucky123 salvatore loverboy1 19901990
panthers1 lollipop1 kkkkkkkk 1princess death666 19811981 9999999999 watermelon iloveyou7 special1 bluefish american dodgers1 blahblah1 karolina
justice1 shadow12 qazwsxedcrfv babydoll1 123456qwe bulldogs1 369258147 bollocks dalejr88 abcde12345 password00 6hbf28w791 broncos1 sweetpea1 kitty123
buttercup1 987456321 lilmama1 kissmyass stellina 123456asd dragonfly loser123 dominic1 password8 lindsey1 ronaldinho summer07 hotstuff qweqweqwe
19911991 candy123 0.00000000 nightmare password69 thailand password22 sexymama1 february lovebug1 viktoria newlife1 redwings aspirine charlene
dickhead1 123456789k master123 geronimo atlanta1 mommy123 scorpio1 yousuck1 barbara1 justdoit pakistan1 19781978 josephine qawsedrf ladygaga lover123
yankees2 godislove
`

var commonPasswords = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(commonPasswordList) {
		m[w] = true
	}
	return m
}()
