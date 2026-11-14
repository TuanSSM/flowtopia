package main

// The cache's answers however an actor is asked for, counted in requests: the review's reproductions of
// the first version (a redirecting id, a fragment, a key under a redirecting path, a key the actor does
// not name, a host that answers every path, a key kept at a path of its own, a key document that owns
// itself), the turns, what a stale key may be used for, callers waiting for another's fetch, the bound,
// and a stop.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// asks runs ask n times and fails unless every answer has the reason want (0 for none) and the stub saw
// exactly req requests in all.
func (r *remoteEnv) asks(t *testing.T, what string, n, req int, want actorReason, ask func() error) {
	t.Helper()
	before := r.w.total()
	for i := range n {
		if err := ask(); reason(err) != want || (want == 0) != (err == nil) {
			t.Errorf("%s, ask %d: %v, want %v", what, i+1, err, want)
		}
	}
	if got := r.w.total() - before; got != req {
		t.Errorf("%s: %d asks cost %d requests, want %d", what, n, got, req)
	}
}

// One answer per actor however it is asked for (H1): N asks of any one string cost at most one fetch in
// the time its answer is kept. The first version paid for every ask: 5 lookups of a redirecting id cost 10
// requests, 5 of an id with a fragment 5, and 5 lookups of a key under a redirecting path 10, each answered
// "no usable key" and none remembered.
func TestDBRemoteOneAnswer(t *testing.T) {
	r := newRemoteEnv(t)
	a, _ := sigTestKeys()
	bo, al := "https://remote.example/users/bo", "https://remote.example/users/al"
	r.w.put("https://remote.example/@bo", worldDoc{status: 302, location: "/users/bo"})
	r.w.put(bo, worldDoc{body: actorDoc(bo, "bo", "Bo", &a.PublicKey, nil)})
	r.w.put("https://remote.example/@al", worldDoc{status: 302, location: "/users/al"})
	r.w.put(al, worldDoc{body: actorDoc(al, "al", "Al", &a.PublicKey, nil)})
	is := func(asked, id string) func() error {
		return func() error {
			row, err := r.actor(asked)
			if err == nil && row.ID != id {
				return fmt.Errorf("answered with %s", row.ID)
			}
			return err
		}
	}
	key := func(id string) func() error {
		return func() error { _, err := r.a.fed.remoteKey(context.Background(), id, keyToVerify); return err }
	}
	r.asks(t, "a redirecting id", 5, 2, 0, is("https://remote.example/@bo", bo)) // the redirect and the document, once
	r.asks(t, "an id with a fragment", 5, 0, 0, is(bo+"#x", bo))                 // the actor is fresh
	r.asks(t, "a key id under a redirecting path", 5, 2, actorNoKey, key("https://remote.example/@al#main-key"))
	r.asks(t, "a key id the actor does not name", 5, 0, actorNoKey, key(bo+"#other"))
	if k, err := r.a.fed.remoteKey(context.Background(), bo+"#main-key", keyToVerify); err != nil || k.ID != bo {
		t.Fatalf("the actor's own key, after all that: %+v %v", k, err)
	}
	// After 10 minutes each string is asked once more, then remembered again; after 24 hours the actor is.
	r.clock.add(actorFailFor)
	r.asks(t, "a key id the actor does not name, 10 minutes on", 5, 1, actorNoKey, key(bo+"#other"))
	r.asks(t, "a key id under a redirecting path, 10 minutes on", 5, 1, actorNoKey, key("https://remote.example/@al#main-key"))
	r.asks(t, "a redirecting id, 10 minutes on", 5, 0, 0, is("https://remote.example/@bo", bo))
	r.clock.add(actorTTL)
	r.asks(t, "a redirecting id, a day on", 5, 2, 0, is("https://remote.example/@bo", bo))

	// A host that answers every path with a redirect to one actor: each address costs one fetch, the first 8
	// are remembered as the actor's names and the rest as refused, so asking any of them again costs nothing.
	one := "https://other.example/users/one"
	r.w.put(one, worldDoc{body: actorDoc(one, "one", "", nil, nil)})
	for i := range 12 {
		r.w.put(fmt.Sprintf("https://other.example/p%d", i), worldDoc{status: 302, location: "/users/one"})
	}
	for i := range 12 {
		want := actorReason(0)
		if i >= actorNamesMax {
			want = actorTooManyNames
		}
		p := fmt.Sprintf("https://other.example/p%d", i)
		r.asks(t, "one of 12 paths that redirect to one actor", 3, 2, want, is(p, one))
	}
	// A host that answers every path with one document under one id: refused as not where it came from, and
	// remembered per address.
	for i := range 5 {
		p := fmt.Sprintf("https://other.example/q%d", i)
		r.w.put(p, worldDoc{body: actorDoc("https://other.example/users/fixed", "f", "", nil, nil)})
		r.asks(t, "one of 5 paths that answer one document", 3, 1, actorWrongID, is(p, ""))
	}
	if n := r.count(`SELECT count(*) FROM ap_actors`); n != 3 {
		t.Errorf("%d actors stored, want bo, al and one", n)
	}
}

// keyDoc is a key document at id owned by owner.
func keyDoc(id, owner, pem string) []byte {
	return []byte(`{"@context":["https://www.w3.org/ns/activitystreams","https://w3id.org/security/v1"],"id":"` + id + `","type":"Key","owner":"` + owner +
		`","publicKeyPem":` + string(must(json.Marshal(pem))) + `}`)
}

// A key kept at a path of its own stays kept (H2): the owner's document names it by id only, so every
// fetch of the owner follows it again, and a rotation through remoteKeyFailed is taken. The first version
// stored no key at the first refetch: after 24 hours remoteKey failed once (3 requests) and succeeded on
// the next call (5), and remoteKeyFailed left no key at all.
func TestDBRemoteKeyOfItsOwn(t *testing.T) {
	r := newRemoteEnv(t)
	a, b := sigTestKeys()
	kid, owner := "https://social.example/keys/alyssa", "https://social.example/alyssa/"
	r.w.put(kid, worldDoc{body: keyDoc(kid, owner, pemOf(&a.PublicKey))})
	r.w.put(owner, worldDoc{body: actorDoc(owner, "alyssa", "", nil, map[string]any{"publicKey": kid})})
	verified := func(k *remoteActor, want string) func() error {
		return func() error {
			got, err := r.a.fed.remoteKey(context.Background(), kid, keyToVerify)
			if err == nil && got.keyPEM != want {
				return errors.New("another key")
			}
			return err
		}
	}
	r.asks(t, "the first lookup of the key", 1, 2, 0, verified(nil, pemOf(&a.PublicKey))) // the key document and its owner
	r.asks(t, "the key again", 3, 0, 0, verified(nil, pemOf(&a.PublicKey)))
	r.clock.add(actorTTL)
	r.asks(t, "the key a day on", 3, 2, 0, verified(nil, pemOf(&a.PublicKey))) // the owner, and its key followed again
	if n := r.count(`SELECT count(*) FROM ap_actors WHERE ap_id = $1 AND key_id = $2`, owner, kid); n != 1 {
		t.Error("the refetched owner lost its key")
	}
	// The owner looked up by itself keeps it too.
	r.clock.add(actorTTL)
	r.asks(t, "the owner by its id a day on", 1, 2, 0, func() error {
		row, err := r.actor(owner)
		if err == nil && row.KeyID != kid {
			return errors.New("no key kept")
		}
		return err
	})
	// A rotation: the key document now holds another key; a failed verification takes it, once in 10 minutes.
	r.w.put(kid, worldDoc{body: keyDoc(kid, owner, pemOf(&b.PublicKey))})
	r.clock.add(actorRecheck)
	r.asks(t, "a failed verification of a rotated key", 1, 2, 0, func() error {
		got, err := r.a.fed.remoteKeyFailed(context.Background(), kid)
		if err == nil && got.keyPEM != pemOf(&b.PublicKey) {
			return errors.New("the rotation was not taken")
		}
		return err
	})
	r.asks(t, "the rotated key", 2, 0, 0, verified(nil, pemOf(&b.PublicKey)))
}

// No lookup can wait on itself (M1): a key document that names itself as its owner is refused at once and
// remembered; two key documents that name each other end at the second, which is never followed further.
// The first version waited on its own fetch for 15 s, every time.
func TestDBRemoteKeyLoops(t *testing.T) {
	r := newRemoteEnv(t)
	a, _ := sigTestKeys()
	self := "https://remote.example/keys/self"
	r.w.put(self, worldDoc{body: keyDoc(self, self, pemOf(&a.PublicKey))})
	x, y := "https://remote.example/keys/x", "https://remote.example/keys/y"
	r.w.put(x, worldDoc{body: keyDoc(x, y, pemOf(&a.PublicKey))})
	r.w.put(y, worldDoc{body: keyDoc(y, x, pemOf(&a.PublicKey))})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	key := func(id string) func() error {
		return func() error { _, err := r.a.fed.remoteKey(ctx, id, keyToVerify); return err }
	}
	r.asks(t, "a key document that owns itself", 3, 1, actorNoKey, key(self))
	r.asks(t, "two key documents that own each other", 3, 2, actorUndecodable, key(x))
	if ctx.Err() != nil {
		t.Fatal("a loop waited for its time")
	}
}

// Turns (M2): one host holds at most one of the two, so a slow host cannot make every other lookup busy;
// and a key is only used to verify when it is fresh and the actor neither gone nor suspended, so a turn
// that was not had cannot keep a rotated key working.
func TestDBRemoteActorTurns(t *testing.T) {
	r := newRemoteEnv(t)
	r.c.wait = 300 * time.Millisecond // a turn a held request keeps: the wait always ends, at whatever speed
	a, _ := sigTestKeys()
	s1, s2, o, g := "https://remote.example/users/s1", "https://remote.example/users/s2", "https://other.example/users/o", "https://other.example/users/g"
	for _, id := range []string{s1, s2, o, g} {
		r.w.put(id, worldDoc{body: actorDoc(id, "x", "", &a.PublicKey, nil)})
	}
	if _, err := r.actor(g); err != nil {
		t.Fatal(err)
	}
	release := r.w.holdAt(s1)
	defer release()
	held := make(chan error, 1)
	go func() { _, err := r.actor(s1); held <- err }()
	<-r.w.arrived
	if _, err := r.actor(s2); reason(err) != actorBusy || r.w.hit(s2) != 0 {
		t.Errorf("a second fetch at a held host: %v, %d requests; want busy without a request", err, r.w.hit(s2))
	}
	if row, err := r.actor(o); err != nil || row.ID != o {
		t.Errorf("another host while one is held: %+v %v", row, err)
	}

	// A stale row: answered as a profile, but its key verifies nothing; the actor's own Delete may use it.
	r.clock.add(actorTTL)
	r.w.put(g, worldDoc{status: 503})
	row, err := r.actor(g)
	if err != nil || !row.Stale || row.StaleReason != actorUnreachable {
		t.Fatalf("a stale row: %+v %v", row, err)
	}
	if _, err := r.a.fed.remoteKey(context.Background(), g+"#main-key", keyToVerify); reason(err) != actorStale {
		t.Errorf("a stale key used to verify: %v", err)
	}
	if k, err := r.a.fed.remoteKey(context.Background(), g+"#main-key", keyForOwnDelete); err != nil || k.ID != g {
		t.Errorf("a stale key for the actor's own Delete: %v", err)
	}
	if _, err := row.publicKey(keyToVerify); reason(err) != actorStale {
		t.Errorf("a stale row's key parsed to verify: %v", err)
	}
	// Stale because no turn was had (its host held): the same.
	r.clock.add(actorTTL)
	s1row := "https://remote.example/users/s3"
	r.w.put(s1row, worldDoc{body: actorDoc(s1row, "x", "", &a.PublicKey, nil)})
	release()
	if err := <-held; err != nil {
		t.Fatal(err)
	}
	if _, err := r.actor(s1row); err != nil {
		t.Fatal(err)
	}
	r.clock.add(actorTTL)
	release2 := r.w.holdAt(s1)
	defer release2()
	go func() { _, err := r.actor(s1); held <- err }()
	<-r.w.arrived
	if row, err := r.actor(s1row); err != nil || !row.Stale || row.StaleReason != actorBusy {
		t.Errorf("a refetch that got no turn: %+v %v", row, err)
	}
	if _, err := r.a.fed.remoteKey(context.Background(), s1row+"#main-key", keyToVerify); reason(err) != actorStale {
		t.Errorf("a key kept past its day because the turns were busy: %v", err)
	}
	release2()
	<-held
	// Gone and suspended: never to verify, still for the actor's own Delete.
	r.w.put(g, worldDoc{status: 410})
	r.clock.add(actorTTL)
	if _, err := r.actor(g); err != nil {
		t.Fatal(err)
	}
	sus := "https://other.example/users/sus"
	r.w.put(sus, worldDoc{body: actorDoc(sus, "sus", "", &a.PublicKey, map[string]any{"suspended": true})})
	if _, err := r.actor(sus); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]actorReason{g: actorGone, sus: actorSuspended} {
		if _, err := r.a.fed.remoteKey(context.Background(), id+"#main-key", keyToVerify); reason(err) != want {
			t.Errorf("%s to verify: %v, want %v", id, err, want)
		}
		if _, err := r.a.fed.remoteKey(context.Background(), id+"#main-key", keyForOwnDelete); err != nil {
			t.Errorf("%s for its own Delete: %v", id, err)
		}
	}
}

// Callers that wait for a fetch started with another question get an answer to their own (M3): two keys of
// one actor asked at once both verify, the second with one fetch of its own.
func TestDBRemoteActorWaiters(t *testing.T) {
	r := newRemoteEnv(t)
	a, b := sigTestKeys()
	id := "https://remote.example/users/two"
	k1, k2 := id+"#k1", id+"#k2"
	r.w.put(id, worldDoc{body: actorDoc(id, "two", "", nil, map[string]any{"publicKey": []any{
		map[string]any{"id": k1, "owner": id, "publicKeyPem": pemOf(&a.PublicKey)},
		map[string]any{"id": k2, "owner": id, "publicKeyPem": pemOf(&b.PublicKey)},
	}})})
	release := r.w.holdAt(id)
	defer release()
	joined := make(chan bool, 8)
	r.c.joined = func(_ string, lead bool) { joined <- lead }
	type answer struct {
		row *remoteActor
		err error
	}
	got := map[string]chan answer{k1: make(chan answer, 1), k2: make(chan answer, 1)}
	ask := func(k string) {
		row, err := r.a.fed.remoteKey(context.Background(), k, keyToVerify)
		got[k] <- answer{row, err}
	}
	// next is the next thing on a channel, or a failure after 10 s: a test that hangs says what it waited for.
	next := func(what string, ch <-chan bool) bool {
		t.Helper()
		select {
		case v := <-ch:
			return v
		case <-time.After(10 * time.Second):
			t.Fatalf("waited 10 s for %s", what)
			return false
		}
	}
	go ask(k1)
	<-r.w.arrived
	go ask(k2)
	if !next("the first caller", joined) || next("the second caller", joined) {
		t.Fatal("the second caller did not wait for the first fetch")
	}
	release()
	for _, k := range []string{k1, k2} {
		select {
		case a := <-got[k]:
			if a.err != nil || a.row.KeyID != k {
				t.Errorf("%s asked at once with another key: %+v %v", k, a.row, a.err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("waited 10 s for the answer to %s", k)
		}
	}
	if n := r.w.hit(id); n != 2 {
		t.Errorf("%d requests, want 2: the shared fetch and the second caller's own", n)
	}
	// A failed verification that joins an ordinary lookup of the actor: the fetch it waited for is the fetch
	// it asked for.
	// Every caller of the first part has its answer, so nothing calls the hook while it is replaced.
	joined2 := make(chan bool, 8)
	r.c.joined = func(_ string, lead bool) { joined2 <- lead }
	r.clock.add(actorTTL)
	release = r.w.holdAt(id)
	plain := make(chan error, 1)
	go func() { _, err := r.actor(id); plain <- err }()
	<-r.w.arrived
	forced := make(chan answer, 1)
	go func() { row, err := r.a.fed.remoteKeyFailed(context.Background(), k2); forced <- answer{row, err} }()
	next("the lookup", joined2)
	next("the failed verification", joined2)
	release()
	if err := <-plain; err != nil {
		t.Fatal(err)
	}
	if a := <-forced; a.err != nil || a.row.KeyID != k2 {
		t.Errorf("a failed verification that joined a lookup: %+v %v", a.row, a.err)
	}
}

// The bound never removes an account anything points at (M4), whichever table it is: the catalog's foreign
// keys to users are each in userRefs with an index that starts with the column, and a blocked, a
// suspended, a commenting and a moderated remote account survive while the unreferenced ones go.
func TestDBEvictionCoversReferences(t *testing.T) {
	r := newRemoteEnv(t)
	rows, err := r.a.db.Query(`SELECT c.conrelid::regclass::text, a.attname,
		EXISTS (SELECT 1 FROM pg_index i WHERE i.indrelid = c.conrelid AND i.indkey[0] = a.attnum AND i.indpred IS NULL)
		FROM pg_constraint c JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = c.conkey[1]
		WHERE c.contype = 'f' AND c.confrelid = 'users'::regclass`)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, ref := range userRefs {
		listed[ref.table+"."+ref.column] = true
	}
	seen := map[string]bool{}
	for rows.Next() {
		var table, column string
		var indexed bool
		if err := rows.Scan(&table, &column, &indexed); err != nil {
			t.Fatal(err)
		}
		name := table + "." + column
		seen[name] = true
		if name == "ap_actors.user_id" {
			continue
		}
		if !listed[name] {
			t.Errorf("%s references users and is not in userRefs: the bound would remove an account it points at", name)
		}
		if !indexed {
			t.Errorf("%s has no index that starts with it: the bound would read the table for each account", name)
		}
	}
	rows.Close()
	for name := range listed {
		if !seen[name] {
			t.Errorf("%s is in userRefs and references nothing", name)
		}
	}

	_, ana := r.user("ana", "user")
	_ = ana
	ids := map[string]string{}
	for _, n := range []string{"u1", "blocked", "suspended", "commenting", "moderated", "u2", "new"} {
		ids[n] = "https://remote.example/users/" + n
		r.w.put(ids[n], worldDoc{body: actorDoc(ids[n], n, "", nil, nil)})
	}
	uid := map[string]int64{}
	for _, n := range []string{"u1", "blocked", "suspended", "commenting", "moderated", "u2"} {
		row, err := r.actor(ids[n])
		if err != nil {
			t.Fatal(err)
		}
		uid[n] = row.UserID
		r.clock.add(time.Minute)
	}
	post := r.post(ana, "a local post")
	for _, q := range []string{
		fmt.Sprintf(`INSERT INTO blocks (user_id, target_id) SELECT id, %d FROM users WHERE username = 'ana'`, uid["blocked"]),
		fmt.Sprintf(`UPDATE users SET suspended_until = now() + interval '1 day', suspend_reason = 'spam' WHERE id = %d`, uid["suspended"]),
		fmt.Sprintf(`INSERT INTO comments (post_id, user_id, body) VALUES (%d, %d, 'a remote comment')`, post, uid["commenting"]),
		fmt.Sprintf(`INSERT INTO mod_actions (action, target_kind, target_id, target_user_id) VALUES ('mute', 'user', %[1]d, %[1]d)`, uid["moderated"]),
	} {
		if _, err := r.a.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	r.c.max = 3
	if _, err := r.actor(ids["new"]); err != nil {
		t.Fatal(err)
	}
	var left []string
	lr, err := r.a.db.Query(`SELECT u.username FROM ap_actors a JOIN users u ON u.id = a.user_id ORDER BY a.fetched_at, a.user_id`)
	if err != nil {
		t.Fatal(err)
	}
	for lr.Next() {
		var s string
		lr.Scan(&s)
		left = append(left, strings.TrimSuffix(s, "@remote.example"))
	}
	lr.Close()
	if got := strings.Join(left, " "); got != "blocked suspended commenting moderated new" {
		t.Errorf("left %q, want every account something points at and the one just stored", got)
	}
}

// Names (LOW 1): a remote actor whose name a local account holds (ADMIN_USER is not held to usernameRE)
// gets the hashed name; ADMIN_USER naming a remote actor already kept is refused in words.
func TestDBRemoteNameTaken(t *testing.T) {
	r := newRemoteEnv(t)
	if _, err := r.a.db.Exec(`INSERT INTO users (username, pw_hash, role) VALUES ('bo@remote.example', 'x', 'admin')`); err != nil {
		t.Fatal(err)
	}
	id := "https://remote.example/users/bo"
	r.w.put(id, worldDoc{body: actorDoc(id, "bo", "", nil, nil)})
	row, err := r.actor(id)
	if err != nil || row.Username != remoteUsername("bo", "remote.example", id, true) {
		t.Fatalf("a remote name a local account holds: %+v %v", row, err)
	}
	cid := "https://remote.example/users/carol"
	r.w.put(cid, worldDoc{body: actorDoc(cid, "carol", "", nil, nil)})
	if _, err := r.actor(cid); err != nil {
		t.Fatal(err)
	}
	out := logged(t)
	if n, err := r.a.seedAdmin("carol@remote.example", "flowa-test-only"); n != 0 || err != nil || !strings.Contains(out.String(), "-purge-remote") {
		t.Errorf("ADMIN_USER naming a remote actor beside other accounts: %d %v, log %q", n, err, out.String())
	}
	if _, err := r.a.db.Exec(`DELETE FROM users WHERE domain = ''`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.a.seedAdmin("carol@remote.example", "flowa-test-only"); err == nil || !strings.Contains(err.Error(), "another server") {
		t.Errorf("ADMIN_USER naming a remote actor and no other account: %v", err)
	}
}

// A stop waits for a fetch in flight, which it ends, and no fetch starts after it (LOW 2, 8.6).
func TestDBRemoteActorStop(t *testing.T) {
	r := newRemoteEnv(t)
	id, later := "https://remote.example/users/slow", "https://other.example/users/later"
	r.w.put(id, worldDoc{body: actorDoc(id, "slow", "", nil, nil)})
	r.w.put(later, worldDoc{body: actorDoc(later, "later", "", nil, nil)})
	release := r.w.holdAt(id)
	defer release()
	// The fetch's goroutine, once its fetch is cut, waits here until the test lets it end: a stop that
	// returned before then would not have waited for it.
	end := make(chan struct{})
	r.c.ended = func() { <-end }
	waiting := make(chan error, 1)
	go func() { _, err := r.actor(id); waiting <- err }()
	<-r.w.arrived
	short, cancelShort := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancelShort()
	if err := r.a.fed.stop(short); err == nil {
		t.Fatal("stop returned while a fetch's goroutine had not ended")
	}
	// Its caller has its answer already: the stop cut the fetch.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	select {
	case err := <-waiting:
		if err == nil {
			t.Error("a fetch cut by a stop answered with a row")
		}
	case <-ctx.Done():
		t.Fatal("the caller of a fetch cut by a stop got no answer")
	}
	close(end)
	if err := r.a.fed.stop(ctx); err != nil {
		t.Fatalf("stop, once the fetch ended: %v", err)
	}
	if _, err := r.actor(later); reason(err) != actorFedRefused || r.w.hit(later) != 0 {
		t.Errorf("a fetch after stop: %v, %d requests", err, r.w.hit(later))
	}
}

// A stale row is answered for at most 30 days after its last fetch (LOW 3, 9.3's retention); then the
// failure is the answer.
func TestDBRemoteActorStaleBound(t *testing.T) {
	r := newRemoteEnv(t)
	id := "https://remote.example/users/old"
	r.w.put(id, worldDoc{body: actorDoc(id, "old", "", nil, nil)})
	if _, err := r.actor(id); err != nil {
		t.Fatal(err)
	}
	r.w.put(id, worldDoc{status: 503})
	r.clock.add(actorStaleMax - actorFailFor)
	if row, err := r.actor(id); err != nil || !row.Stale {
		t.Fatalf("a stale row under 30 days: %+v %v", row, err)
	}
	r.clock.add(actorFailFor - time.Second)
	if row, err := r.actor(id); err != nil || !row.Stale {
		t.Fatalf("a stale row a second under 30 days, the failure remembered: %+v %v", row, err)
	}
	r.clock.add(time.Second)
	if _, err := r.actor(id); reason(err) != actorUnreachable {
		t.Errorf("a stale row of 30 days, the failure remembered: %v", err)
	}
	r.clock.add(actorFailFor)
	if _, err := r.actor(id); reason(err) != actorUnreachable {
		t.Errorf("a stale row of 30 days and 10 minutes, fetched again: %v", err)
	}
}
