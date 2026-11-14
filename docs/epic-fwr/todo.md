# FWR: Federation, write side

Why, scope and risks: [epics.md](../epics.md#fwr-federation-write-side). Design:
[federation.md](../federation.md). Order: [roadmap.md](../roadmap.md#third-cycle).
Category: feature. Size: L. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** in progress (issue [#90](https://github.com/TuanSSM/flowtopia/issues/90)); the inbox is built (item 1, in review), the delivery queue is built (item 2, its fan-out on the Pi left to the owner), follows across servers are built (item 3, the recorded Mastodon fixtures left to the owner's trial), and Public posts go out as notes (item 4, in review). 0 of 9 items done. Federated mode is marked experimental in
the Admin page and the README from the first item until the last one passes. Every setting, table,
route and test named below is new unless the design note says it exists.

- [ ] **The inbox and its queue.** `POST /users/{name}/inbox`, `/inbox` and `/actor/inbox`; `guard`
  lets only these through with `application/activity+json` or `application/ld+json`; verification
  against the origin's host; the limits of federation.md 7.5; `ap_inbox`; 202 and a worker; the
  `federation` flag (off by default) as the pause switch; the README's proxy section says the app
  must sit at the root of the origin with the path unchanged. A refused signature is answered 401,
  which is what lets Mastodon 4.7's own fallback to RFC 9421 work (federation.md 7.1). DoD:
  `TestInboxRefusals` (unsigned, the request line not signed, no `headers` parameter, bad digest,
  old date, future date, an RFC 9421 signature without `created`, without `content-digest` or with
  two signatures, an `alg` that is not `rsa-v1_5-sha256`, replayed, an activity id of another origin,
  too large, too deep, too many array elements, key owner not the actor, a key on another port of
  the actor's host, blocked host, queue full); `BenchmarkInboxVerify` under budget.
  Two things the signatures item left for this one (federation.md 7.2 and 7.4): the verifier calls the
  key lookup for whatever https key id a request names before any cryptography, so the lookup gets a
  fetch cache, a negative cache and a limit of key fetches per host before it is wired to fetch; and a
  restart empties the replay memory, so a replay within 12 hours of a restart rests on de-duplication
  by activity id alone. A `sigFull` refusal from the verifier is answered 503 with `Retry-After`, not
  401, the housekeeping pass calls the replay memory's `sweep`, and when the memory's `stepped` says
  the clock was corrected the inbox logs one line. **Required:** the inbox applies the 7.3 check (the
  activity's actor and the key id that signed it on one origin, `sameOrigin`) to every delivery before
  acting on it. Replay safety of the draft form depends on it: a cavage signature does not cover its
  `keyId`, so a signature evicted from the replay memory can come back under another domain's key id,
  and only this check refuses it (federation.md 7.2, `TestReplayAcrossDomains`; `TestInboxRefusals`
  gets the case "a delivery signed under a key id not on the actor's origin").
  From FRD's decoder (federation.md 6.3, 7.5): **required:** the inbox decodes at most 4 bodies at
  once, the same bound as its handling at once, never one per request that waits. One decode of a
  256 KB body allocates at most 4 MB (`TestDecodeMemory`; 3.4 MB measured at worst), so 4 at once is
  16 MB, half of section 12's 32 MB for federation at peak, leaving the other half for the queue and the
  workers; 8 would take all of it. The readers' counts of what they dropped (`apAudience.Dropped`,
  `TagsDropped`, `KeysSkipped`, `AlsoKnownAsDropped`, `ObjectsDropped`) and `BadDate` are logged as
  counts, and a post with `ReplyToUnknown` is handled as a reply to an unknown post.
  From FRD's actor cache (federation.md 7.4, "As built (FRD 7)"): **required:** the verifier's key lookup is
  `remoteKey(ctx, keyID, keyToVerify)`, then `remoteKeyFailed(ctx, keyID)` once when the signature does not
  verify; a stale row (its refetch failed, for any reason, a busy turn included), a gone actor and a suspended
  one give no key to verify with, and only the handling of an actor's own Delete asks with `keyForOwnDelete`
  for the cached key (6.3). `TestInboxRefusals` gets the cases "signed with the key of a stale row", "of a gone
  actor", "of a suspended actor" and "an actor's own Delete signed with its cached key after a 410", beside
  `TestDBRemoteActorTurns`. `actorBusy` (no turn within 5 s) is answered 503 with `Retry-After`, as `sigFull` is.
  The first inbox request that fetches a key freezes the origin through `ensureOrigin`, by design: a signed
  fetch names the instance actor's key id under it (federation.md 4.3).
  Built (federation.md 7.10 says what and why; with FMD 1's domain policies, which the inbox needs, in their own
  commit): the three inboxes past `guard`, every refusal in the order and with the status of 7.10 (pinned by
  `TestInboxStageOrder`), the `federation` flag (only in a mode that federates) as the pause, `ap_inbox`
  (migration 18, unique by the signer's actor and the activity id), two workers with claim, lease, retries, dead
  rows, retention and a shutdown that gives a claimed row back, `fedConnsMax` turns at the database. Who pays:
  before the signature verifies only the request's address (3,600 deliveries and 60 key fetches an hour, at most 2
  of the 8 bodies being read, each turn ending when its body has arrived within 10 s, else 408); once verified, the
  key id's host (300 in 5 minutes) and domain (3,000) and, for a fetched key that verified, the domain's 120 key
  fetches an hour. `keyMemo` tied to its actor's row, the origin rule, the stale, gone and suspended rules with
  `keyForOwnDelete`, the unknown actor's Delete dropped with no fetch and an unverified own Delete answered alike
  (202; this server's own failures keep their 503), the replay memory's `sweep` in housekeeping and its `stepped`
  logged once, the readers' drop counts logged, the README's sentences on the root of the origin and on the
  sender's address behind a proxy (logged once when a trusted proxy passes none), the inbox section, `fedBuilt`
  and NodeInfo's `inbox`, `endpoints.sharedInbox` in the actor.
  What each type does in this version: an actor's own Delete marks it gone, an Update of the actor's own document
  fetches it again; everything else (Follow included) is counted per type and dropped with 202, and nothing is
  answered back. Tests: `TestInboxRefusals` (every case above, each with its status and reason),
  `TestInboxKeys` (a rotated key after one refetch, a stale row, a gone and a suspended actor, an own Delete with
  the cached key after the actor's server stopped answering, a Delete from an actor never seen with no fetch),
  `TestInboxKeyFetchBound`, `TestDBInboxSlowKey` (no connection held while the key is fetched), `TestDBInboxQueue`
  (the same id 9 times, 8 at once: one row; two workers never handle a row twice; the retry schedule on the
  test's clock and the give-up; the lease; a stop mid-handling gives the row back; retention; the pause),
  `TestDBInboxHandlers`, `TestDBInboxMethods` (replacing `TestDBInboxNotYet`), `TestDBTwoFlowaInbox` (a signed
  delivery in both forms through the real fetcher and `httpSigner`, verified, queued and handled by the other
  instance), `TestDBInboxOneConnection`, `TestDBFedConnsBound`, `TestDBInboxClockStep`, `TestDBInboxTurns` (4
  decodes at once, 8 waiting), `TestDBInboxFlood` (unsigned 256 KB bodies refused unread with no fetch or row; keys
  nobody keeps limited to their 2 turns while a kept key's delivery is accepted), `TestDBInboxForwardPays` (a
  forward spends the signer's allowances, never the named actor's domain's), `TestDBPolicyBothWays` (the allowlist
  outbound too), `TestInboxStageOrder`, `TestInboxSlowBodies`, `TestInboxReadTurnFreed`, `TestInboxForgedSpendsGroup`,
  `TestInboxIDSquat`, `TestInboxPeekOracle`, `TestInboxOwnDeleteByAnother`, `TestInboxUnforwardedSaidOnce`,
  `TestKeyMemoRaces`, `TestDBInboxKeyFollowsRow`, `TestDBInboxWorkerLimits`, `TestDBPurgeRemote` and
  `TestDBQueryPlans` updated, all with `-race -count=3` on two cores;
  `BenchmarkInboxRequest` (with the key kept: 66 to 75 µs and 173 allocations dropped, 1.4 to 2.1 ms wall and 219
  allocations stored with its commit; an unsigned 256 KB request 6.2 to 6.6 µs and 41 allocations; a 256 KB Delete
  of an actor never seen 1.7 to 1.8 ms and 640 KB, on a 2.1 GHz Xeon) and `BenchmarkInboxVerify` under budget. The `federated` browser suite covers the inbox's answers
  over HTTP that need no other server and the Federation tab (`federated-admin-federation`).
  **Open:** `TestFederationMemory` (it needs FWR 2's deliveries; the inbox's share is `TestDecodeMemory`'s 4 MB
  times its 4 turns), the `/healthz` fields (FWR 2), the Pi's figures for the budget of section 12, and the box
  stays unchecked until the owner's review merges it.
- [ ] **A delivery queue in Postgres.** `ap_outbox`, `ap_deliveries`, `ap_hosts`; workers
  (`FLOWA_FEDERATION_WORKERS`), one request at a time per host, 10 a second, at most 3 database
  connections; the retry schedule and the 48-hour horizon (F9); the circuit breaker and unavailable
  hosts; back-pressure; a shutdown that waits (`main` waits for `Shutdown`, the workers have their
  own context and wait group: federation.md 8.6); nothing signed while the clock cannot be trusted;
  the `/healthz` fields for a local probe. DoD:
  `TestDBDeliveryQueue` (the schedule with a fake clock, two workers never send one row twice,
  give-up, a 400 or a 401 sent again at once in the other signature form and the form that worked
  recorded for the host, a 400 refused in both forms given up, 410 removes the follower),
  `TestClockGuard`, `TestDBDeliveryBackPressure`, `TestDeliveryShutdown`,
  `BenchmarkClaimDelivery`, `TestFederationMemory`; the fan-out time for 300 hosts measured on the
  Pi written here.
  Built (federation.md 8.8 says what and why; migration 19 with `flowa-schema18.dump`): `ap_outbox`, `ap_deliveries`
  and `ap_hosts`; `enqueue`, one transaction per activity, shared inboxes and one row per inbox, the policy and the
  unavailable hosts left out, 2,000 rows per host and 50,000 in all held exactly (an activity past the cap waits
  whole and is fanned out under 40,000; 1,000 waiting, then `errDeliveryFull`); `FLOWA_FEDERATION_WORKERS` (4, 1 to 8,
  checked at the start in a mode that federates; the `compose.yml` line and `test/release.sh`'s list); workers that
  claim with `FOR UPDATE SKIP LOCKED` and a 60 s lease renewed past half, one request per host, 10 a second in all
  (the pacer), the policy asked again at the send, `httpSigner` through the guarded fetcher, no turn, connection or
  lock held across a request; 8.3's schedule with 20% jitter and the 48-hour horizon; 400 and 401 sent again in the
  other form, the form recorded for the host, a 400 in both given up and a 401 retried; 410 gives up every row for
  that inbox and calls `goneInbox`; 429's `Retry-After`; a redirect retried without counting against the host; the
  circuit (5 in 10 minutes, 10 minutes open, the host's rows moved to its end, half-open: the one row tried after
  reopens it on a failure and closes it on any other answer) and unavailable hosts (failing for 7 days with no sign of
  life between and no gap over 48 hours, a daily NodeInfo probe, a valid delivery from it written at most hourly as a
  sign of life, the RFC 9421 mark from the inbox unless a send learned the form); every inbox on its actor's host,
  checked by `enqueue`; the clock guard (the build and the newest time the database
  held, believed for the first hour after a start); the `federation` flag as the pause; a stop that gives back what was claimed and lets a request in flight
  finish within `serve`'s 5 s; retention of failed rows (7 days, at most 20,000) and finished activities after 7
  days, 1,000 rows a statement; `-purge-remote`; the
  `/healthz` fields for a local probe (`mode`, `queued`, `oldestQueuedSeconds`, `failingHosts`, and `paused` and
  `clockWaiting`), asked only for a local probe and kept 5 s, unchanged in local and plain single mode; "Sending to other servers" on the Federation tab and a
  part of the dashboard's mode line. Nothing sends yet: FWR 3 is the first caller, and fills `goneInbox` with the
  removal of the followers whose inbox answered 410 (until then a 410 gives up the rows and nothing more). **For FWR 3:**
  each recipient passed to `enqueue` names its actor's id, the verified `ap_actors.ap_id` and never an id a delivery
  merely named, beside its inbox and shared inbox (from `ap_actors`), and
  an inbox not on the actor's host is refused, a shared inbox on another host passed over for the actor's own inbox
  (federation.md 8.8 says why and what it costs). After the second review of #112: a host proved alive between a
  counted failure and its mark stays available, batch deletes go through the primary key, a clock that jumps forward
  holds rows for an hour instead of giving them up and dates written ahead are pulled back (federation.md 8.3), a 429
  holds the host for its `Retry-After` (at most an hour) and neither it nor a 401 resets the circuit's count,
  addresses are compared in one spelling, and the page says "20,000 or more" at the cap (`TestDBDeliveryClockJump`,
  `TestDBDeliveryUnavailableRace`, `TestDBDelivery429`, `TestDBDeliveryAddressSpelling`). Tests:
  `TestDBDeliveryQueue` (the schedule and the jitter on the test's clock, the horizon, four workers on 40 rows each
  sent once and one at a time per host, a lease renewed and a lease lost, both forms with the form recorded, 400 and
  401 refused in both, 410 with the hook, 404, 403, 429, the circuit, unavailable hosts and the probe),
  `TestDeliveryClassify`, `TestDBDeliveryPacing` (one request per host with requests the stub holds; the pacer's
  places a tenth of a second apart on the test's clock), `TestDBDeliveryPolicyAndPause`, `TestDBDeliveryNoConnectionHeld`,
  `TestClockGuard`, `TestDBDeliveryBackPressure`, `TestDeliveryShutdown` (claimed and given back, in flight through
  `serve`, outlasting the stop), `TestDBTwoFlowaDelivery` (one Flowa sends, the other's real inbox verifies and
  queues, in both forms), `TestDBHealthzFederation`, `TestDBDeliveryRetention`, `TestFederationMemory`,
  `TestDBQueryPlans` (the claim, the counts, the fan-out, a host's failure, the numbers), `TestDBPurgeRemote`,
  `TestDBLocalModeUnchanged` and `TestDBDefaultsUnchanged` extended, all with `-race -count=3` on two cores;
  `BenchmarkClaimDelivery` 0.75 to 0.85 ms with 50,000 rows queued and 25 hosts open (budget 2 ms) and
  `TestFederationMemory` 16.5 MB of the 32 MB budget: the rows are in Postgres, so it measures the Go heap of 4
  workers each holding a claimed 256 KB activity in flight (1.6 MB), with the inbox's 14.9 MB added as a bound, on a
  2.1 GHz Xeon. 23 deliberate breakages each failed a test; the review of #112 added the week of failures for
  "unavailable", signs of life from the inbox, inboxes held to their actor's host, the local-only health numbers,
  batched and capped retention, and the half-open circuit, each with its test.
  **Open:** the fan-out time on the Pi, which is the owner's to measure, so the box stays unchecked: on the Pi, with a
  throwaway database (`docker run -d --name fanout-db -e POSTGRES_PASSWORD=flowa-test-only -p 127.0.0.1:55433:5432
  "$(test/db-image.sh)"`, the image `compose.yml` pins), run `TEST_DATABASE_URL='postgres://postgres:flowa-test-only@127.0.0.1:55433/postgres?sslmode=disable'
  go test -run '^$' -bench BenchmarkDeliveryFanOut -benchtime 3x .` in the checkout, write the ns/op shown (one post
  to 1,000 followers on 300 hosts; 29.9 s on the 2.1 GHz Xeon, bound by the 10 a second) here, and remove the container (`docker rm -f fanout-db`). Also open:
  the Pi's figures for section 12's memory and the container's (`docker stats`, FWR 8), the page per host with a
  retry button (14.2), and the owner's review.
- [ ] **Follows across servers.** Follow, Accept, Reject and Undo(Follow) in and out, and Undo of an
  Accept of our Follow read as a Reject (Mastodon lists "Accept Follow" among what it undoes);
  approval by hand (`users.locked`, `manuallyApprovesFollowers`); follow request notifications with
  Approve and Decline. DoD: `TestDBFollowsAcross` with the recorded Mastodon Follow and Accept fixtures;
  `TestTwoInstances` covers follow, accept and unfollow.
  Built (federation.md 6.4, "Follows across servers, as built (FWR 3)", says what and why; migration 20 with
  `flowa-schema19.dump`): `follows.state` and `follows.ap_id`, `users.locked`, `follows_pending` and
  `users_remote_domain`; Follow, Accept, Reject, Undo (of a Follow, and of an Accept read as a Reject) and Block in, each
  stored only after the origin rule and when it can be about a follow here (`followShaped`), matched by actor and object
  (an Accept's embedded Follow by any id or none, as Mastodon may send it; by id alone only the id we sent), idempotent on
  repeats; a Follow of a member who may be shown accepted at once with Accept, or held as a request with a
  `follow_request` notification when the member approves by hand; Reject for a member who does not federate and has a
  key, for a Follow across a block and for the instance actor, nothing for a name nobody has; Follow, Undo, Accept and
  Reject out through `enqueue` alone (`sendFollow`), to the actor's own inbox, signed with the member's key, and only to an
  inbox on the actor's own origin; "Requested" until the Accept; the routes `GET /api/fed/accounts/{id}`, `PUT` and `DELETE`
  of its `follow` and `block`, `GET /api/fed/requests`, `PUT` and `DELETE /api/fed/requests/{id}` (`limits.fedFollows`, 100
  an hour); the Settings switch "Approve followers from other servers by hand" only in a mode that federates (local
  follows unchanged: the design is silent, so the lock holds back remote followers alone), `manuallyApprovesFollowers` in
  the actor; the remote profile page `#/fed/{id}`, the Follow button in People elsewhere and on that page with its three
  states, remote accounts in the followers, following and blocks lists and the bell with their handle shown only when
  `handleNameOK` passes it, the requests list and the request notification with Approve and Decline, the sentence in Home
  and on the remote profile that remote posts do not show yet; a block of a remote account ending both follows with Reject
  and Undo (no Block sent), a Block in ending both; `goneInbox` (`goneFollower`) removing the follows of the account whose
  own inbox answered 410; caps of 200 requests per member, 100 per sending host and 2,000 follows per sending host; one rule
  for counts (accepted only; the public count's other side a member who may be shown or a remote account, `publicSide`);
  `follows` in `userRefs`, so a followed or following actor is never evicted; `fedBuilt` and NodeInfo's `follows`.
  Tests: `TestDBFollowsAcross` (every path above with the documentation-derived fixtures of
  `testdata/federation/follows`: repeats, the wrong actor, an object not ours, request, approve, decline, Undo of Accept,
  block both ways, members not federating, the caps, an inbox on another host, 410, eviction, the lists and a hostile
  name), `TestDBTwoFlowaFollow` (two Flowa instances over the real fetcher, signer, inbox and delivery queue: follow,
  accept and unfollow both ways, and approval by hand), `TestDBLockedOnlyFederating`, `TestDBFollowsOneConnection`,
  `TestDBQueryPlans`, `TestDBPurgeRemote`, `TestDBInboxHandlers` updated, with `-race -count=3` on two cores; the
  `federated` browser suite (the switch, the remote profile, the button's three states, a request with Approve and
  Decline; pictures `federated-remote-profile` and `federated-follow-request`).
  After the review (federation.md 6.4, "Behind the follows switch" and the parts after it): with the `follows` flag off
  nothing adds or accepts a follow (Follow and Accept counted and dropped, no follow out, the lock switch hidden, the actor
  saying `manuallyApprovesFollowers`), what only removes one still acted on, the rows there were kept and nothing replayed;
  an Undo, a Block and a 410 take the follow's notifications with it, a member keeps the newest 50 notifications from one
  other server and 500 from all, and a notification no longer keeps a remote account in the cache (`userRefsGoing`); a
  repeat answered at most once an hour per pair; suspending a domain ends its follows both ways; the bell names a remote
  account by its handle and host. Tests `TestDBFollowsFlagOff`, `TestDBFollowUndoLeavesNothing`, `TestDBFollowsHeld`
  (the review's four mutations, a real 410 through the queue, a suspension), `TestDBQueryPlans` (the trim, 81 rows).
  After the second review: a switch that cannot be read is not "off" (503 at the inbox, retried by the worker, no
  actor document kept); the trim reads only follow notifications through `notifications_follows` and spares a request
  that waits; a 410 compares the queue's own address; only a true repeat is silenced; the suspension's audit counts
  the follows it ended; a long handle wraps in the bell and the lists. Tests `TestDBTrimKeepsLocalAndRequests`,
  `TestDBFollowsSwitchUnread`, `TestDBFollowsAnswerAndPort`, the subdomain case of `TestDBFollowsHeld`, and the golden
  `actor-ana-follows-off.json`.
  **Open:** the DoD's recorded Mastodon Follow and Accept fixtures (the fixtures are written from documentation, as their
  README says, since no session can reach a Mastodon server: the owner's trial, item 9, records them); `TestTwoInstances`
  is item 8's container suite (here `TestDBTwoFlowaFollow` covers the same steps in Go); the box stays unchecked until
  both hold and the owner's review merges it. For the owner (defaults built): whether approval by hand should hold back
  local followers too (the design is silent; built: remote only), whether a public follower count should count every local follower
  (F10, unchanged), and the caps' numbers.
- [ ] **Public posts go out as notes.** Create(Note) for Public posts of members who opted in, the
  HTML of federation.md 6.5, mentions, one hashtag per prop, `flowa:tricks` and `flowa:combo` with
  the text fallback, the picture with alt text (a new field in the composer; written as both `name`
  and `summary`, federation.md 6.5), sent as `application/ld+json; profile=...`, Delete(Tombstone) on
  delete or hide, Update(Person) at most every five minutes; milestone posts stay local (F13).
  `mentionRE` and `richText` learn `@name@host` here, before the first remote mention is sent, so
  `@anna@social.example` is never read as the local `anna`. DoD: `TestNoteFromPost` (golden),
  `TestNoteContentEscaping`, `TestMentions` extended, `TestDBPostFanOut` (one delivery per shared
  inbox).
  Built (federation.md 6.5, "Public posts out, as built (FWR 4)", says what and why; migration 21 with
  `flowa-schema20.dump`): `posts.alt`, `posts.ap_mentions`, `ap_post_out` and `ap_person_out`; Create(Note) for a post of
  kind post, audience Public, by a member who shares, while the mode federates, federation is not refused and the
  `federation` and `follows` flags are on (a post made while either is off is never sent later; milestones and This
  server posts never), its record written by the post's own statement and turned into the activity by a worker of the
  delivery queue in the transaction that enqueues it (`enqueueThen`), so a crash loses or doubles nothing; the
  recipients in one bounded statement (`postRecipientsSQL`: accepted followers one per shared inbox, a follower whose
  server names none by its own inbox, the remote accounts mentioned; at most 10,000 addresses, the rest left out and
  logged), sent as `application/ld+json; profile=...` and signed as the member; the HTML of 6.5 (`noteHTMLWith`: `p`, `br`,
  `a`, `span`, the classes Mastodon's sanitiser keeps, every character escaped, links only http and https, control and
  direction characters dropped), mentions as h-card links with `Mention` tags and in `cc`, one `Hashtag` per prop of the
  chips and the combo, `flowa:tricks` and `flowa:combo` beside the text fallback, the picture with its alt text as both
  `name` and `summary`; one function for the Note served and the Note sent (`noteOf`); the alt text field in the composer
  in every mode, shown once a picture is attached, at most 1,500 characters, the picture's alt in Flowa's pages, `alt` in
  an answer only when a post has one; Delete(Tombstone) on a deletion and a moderator's hide to the inboxes the Create went
  to (recorded in `ap_post_out`), nothing on a restore; Update(Person) on a change the actor shows, coalesced in
  `ap_person_out` to one every five minutes with the newest state; `parseMentions` and the page's `mentionsIn` reading
  `@name@host` as another server's account and never the local name, held to `test/mentions-fixtures.json`; mentions
  resolved from the cache alone (an unknown handle stays text and is not sent to); `-purge-remote` removes the records;
  `fedBuilt`, NodeInfo's `posts`, the Settings and composer sentences, the Federation tab's subtitle. Tests:
  `TestNoteFromPost` (golden `note-full.json` and `create-ana.json`, the Note sent compared byte for byte with the Note
  served), `TestNoteContentEscaping`, `FuzzNoteContent` (30 s, no failure), `TestMentions` and `node test/mentions.js` (in
  the lint job), `TestDBPostFanOut` (600 followers on 300 servers give 300 deliveries; a server with no shared inbox, a
  suspended domain, a gone actor, a pending follow, a suspended account, a mentioned account, every audience, a
  milestone, each flag off, a member who stops sharing, federation refused, a Create made twice),
  `TestDBPostDeleteAndHide`, `TestDBPersonUpdateCoalesced`, `TestDBNothingPrivateLeaves`, `TestDBTwoFlowaNotes` (the other
  Flowa's inbox verifies the Create and the Delete and counts and drops them), `TestDBNotesOneConnection`,
  `TestDBPostLocalStatements` (8 statements for a post in local mode, as before), `TestDBQueryPlans` (the recipients of
  1,000 followers on 300 servers through indexes, about 12 rows counted per follower), `TestDBPurgeRemote`,
  `TestDBEvictionCoversReferences`, `TestDBLocalModeUnchanged` extended, with `-race -count=3` on two cores; the `social`,
  `keys` and `a11y` browser suites (the field's name, its keyboard path, the post's alt) and `federated` (the new
  sentences).
  After the review (federation.md 8.8 "Taking back", and 6.5's "as built" notes marked after the review): a Delete never
  reaches an inbox before its Create's outcome there (`ap_outbox.undoes`; a Create never tried is given up and no Delete
  goes, one that met an error (it may have arrived) is given up and the Delete goes, one in flight holds the Delete until its lease ends, and is given up at the Delete's send if it failed
  meanwhile), and the same for FWR 3's Undo of a Follow; mentions read only where the content shows them (one reading,
  `bodyMentions`, for the content, the mentions resolved and local notifications: a name inside a link is no mention, in
  local mode too), anything shaped `@name@...` never the local name (a bad host is plain text, a host in another script
  too), `@name@own-host` the member on both sides; a Delete the full queue refuses kept and tried again on the delivery
  schedule; the switches asked again at the send, and turning one off drops the Creates and Updates that wait; a post
  deleted or hidden in local mode sends its Delete at the next start that federates; the alt text counted as the server
  counts, with a counter, and shown in the moderation queue; the Federation tab counts activities cut at 10,000
  addresses. Tests `TestDBDeleteNeverOvertakesCreate`, `TestDBDeleteOfWaitingCreate`, `TestDBUndoNeverOvertakesFollow`,
  `TestDBMentionInsideLinkNotAddressed`, `TestDBOutWhenQueueFull`, `TestDBOutSwitchesRechecked`,
  `TestDBDeleteAfterLocalMode`, `TestDBNotesForStaff`, `TestMentions` and its fixtures extended.
  **Open:** an Accept and a later Reject of the same Follow (after a block) can overtake each other at one inbox, since the
  Accept's id is not kept; a recorded Mastodon post confirming the h-card nesting and which of `name` and `summary` it writes for alt
  text (the owner's trial, item 9); Update(Person) after `-rotate-keys`; nothing is sent when a member is suspended; the
  box stays unchecked until the owner's review merges it. For the owner (defaults built): whether a post made while a
  switch was off should go out when it comes back on (built: never), whether the alt text field belongs in local mode
  (built: every mode, shown once a picture is attached), whether a Delete should go after a member stops sharing
  (built: yes), whether the combo's prop gets a hashtag (built: yes), and the numbers: 10 mentions, 1,500 characters of
  alt text, 5 minutes between profile updates, 10,000 addresses per activity.
- [ ] **Posts from followed accounts in Home.** Create, Update and Delete of Note in; `htmlToText`;
  the visibility rules of federation.md 6.7 (direct messages dropped); remote pictures through the
  bounded cache under `-data/media` (F6); Announce of a local post as a `share` notification; polls,
  long posts and unknown media as text and links; a remote mention written short (`@bo`) is rewritten
  to `@bo@social.example` from the object's `Mention` tags, and local members are notified from
  those tags' `href`s only (federation.md 6.6); the Community timeline (`feedSQL`) keeps local posts
  only. This item is the first to store remote posts, so its pull request says that an older build
  after `./deploy.sh --rollback` shows them as local (`/nunchaku -purge-remote` exists since FRD's
  remote actors item). The decoder cuts `content` at 64 KB without regard to markup (`apNote.Cut`), so
  `htmlToText` must cope with a text that ends inside a tag, an attribute, a comment or an entity: it
  drops the unterminated tail and still adds the link to the original. DoD: `TestRemoteHTMLToText` (a
  hostile corpus and the fixtures, and a cut text ending in each of those),
  `TestDBRemoteNotes` (a short remote mention never notifies or links the local member of that
  name; an Update without `updated` or older than the stored one is ignored), `TestMediaCacheBudget`, `TestDBQueryPlans` with 20,000 remote posts.
- [ ] **Replies and likes across servers.** Remote replies become comments under local posts (deeper
  replies flattened as RPL does for a reply to a reply); local comments on Public posts go out; Like
  and Undo(Like) both ways; Update(Note) for comment edits inside the 15-minute window, carrying
  `updated` (Mastodon applies an edit only with it); a remote reply addressed to a local member's
  followers collection forwarded once to those followers (ActivityPub 7.1.2, federation.md 6.4);
  notifications. DoD: `TestDBRemoteReplies` (including the forward: sent once, only for the first
  sight, only to the collection named), `TestDBRemoteLikes`; `TestTwoInstances` covers reply, edit,
  like and unlike.
- [ ] **Account deletion across servers.** Delete(Person) out when an account is deleted and from
  "Remove me from other servers"; Delete(Person) in purges the remote account's content and follows;
  a Delete from an actor never seen is answered 202 with no fetch. Retired names exist since FRD's
  actor item. DoD: `TestDBDeletePerson` (both directions; an unknown actor's Delete opens no
  connection).
- [ ] **Two instances federating in CI.** `test/federation.sh`: two containers from the built image
  and two databases on a Docker network, `FLOWA_FEDERATION_ALLOW_PRIVATE` set, driven through their
  APIs: follow, accept, post with a chip and a picture, like, reply, edit, delete, block, unfollow,
  delete the account; `docker stats` under 300 MB per app container. DoD: the `federation` suite in
  `ALL` in `test/ci-plan.sh` and its self-test, green in `process_pr`; `test/release.sh` fails when
  `compose.yml` names `FLOWA_FEDERATION_ALLOW_PRIVATE`.
- [ ] **The owner's trial against a real Mastodon server.** Not a pull request: the owner's item,
  because no session that builds Flowa can reach a fediverse server. On the stage instance with
  `FLOWA_MODE=federated` and the Mastodon server's domain on the allowlist: (1) find the stage
  account from Mastodon's search; (2) follow it from Mastodon and see the follower on Flowa; (3) post
  on Flowa with a chip and a picture with alt text, and see it on Mastodon with the text fallback and
  the picture; (4) reply and like from Mastodon, and see both on Flowa; (5) follow the Mastodon
  account from Flowa, post on Mastodon, and see it in Home; (6) reply from Flowa and see it threaded
  on Mastodon; (7) delete the Flowa post and see it gone on Mastodon; (8) report a Flowa post from
  Mastodon and see it in the queue; (9) unfollow both ways. DoD: each step written here with the
  Mastodon version and pass or fail; each failure becomes a pull request; federated mode loses the
  "experimental" mark only when all nine pass. Also written here, without gating the mark: whether
  Mastodon finds and mentions a stage account whose name ends in a dot (federation.md, finding 11),
  and the details the note's Sources section leaves to this trial.
