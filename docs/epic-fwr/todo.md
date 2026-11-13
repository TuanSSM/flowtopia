# FWR: Federation, write side

Why, scope and risks: [epics.md](../epics.md#fwr-federation-write-side). Design:
[federation.md](../federation.md). Order: [roadmap.md](../roadmap.md#third-cycle).
Category: feature. Size: L. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** not started (issue [#90](https://github.com/TuanSSM/flowtopia/issues/90)); waits for FRD. 0 of 9 items done. Federated mode is marked experimental in
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
  401, and the housekeeping pass calls the replay memory's `sweep`.
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
- [ ] **Follows across servers.** Follow, Accept, Reject and Undo(Follow) in and out, and Undo of an
  Accept of our Follow read as a Reject (Mastodon lists "Accept Follow" among what it undoes);
  approval by hand (`users.locked`, `manuallyApprovesFollowers`); follow request notifications with
  Approve and Decline. DoD: `TestDBFollowsAcross` with the recorded Mastodon Follow and Accept fixtures;
  `TestTwoInstances` covers follow, accept and unfollow.
- [ ] **Public posts go out as notes.** Create(Note) for Public posts of members who opted in, the
  HTML of federation.md 6.5, mentions, one hashtag per prop, `flowa:tricks` and `flowa:combo` with
  the text fallback, the picture with alt text (a new field in the composer; written as both `name`
  and `summary`, federation.md 6.5), sent as `application/ld+json; profile=...`, Delete(Tombstone) on
  delete or hide, Update(Person) at most every five minutes; milestone posts stay local (F13).
  `mentionRE` and `richText` learn `@name@host` here, before the first remote mention is sent, so
  `@anna@social.example` is never read as the local `anna`. DoD: `TestNoteFromPost` (golden),
  `TestNoteContentEscaping`, `TestMentions` extended, `TestDBPostFanOut` (one delivery per shared
  inbox).
- [ ] **Posts from followed accounts in Home.** Create, Update and Delete of Note in; `htmlToText`;
  the visibility rules of federation.md 6.7 (direct messages dropped); remote pictures through the
  bounded cache under `-data/media` (F6); Announce of a local post as a `share` notification; polls,
  long posts and unknown media as text and links; a remote mention written short (`@bo`) is rewritten
  to `@bo@social.example` from the object's `Mention` tags, and local members are notified from
  those tags' `href`s only (federation.md 6.6); the Community timeline (`feedSQL`) keeps local posts
  only. This item is the first to store remote posts, so its pull request says that an older build
  after `./deploy.sh --rollback` shows them as local (`/nunchaku -purge-remote` exists since FRD's
  remote actors item). DoD: `TestRemoteHTMLToText` (a hostile corpus and the fixtures),
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
