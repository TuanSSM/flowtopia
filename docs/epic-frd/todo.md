# FRD: Federation, read side

Why, scope and risks: [epics.md](../epics.md#frd-federation-read-side). Design:
[federation.md](../federation.md). Order: [roadmap.md](../roadmap.md#third-cycle).
Category: feature. Size: L. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** in progress (issue [#89](https://github.com/TuanSSM/flowtopia/issues/89)): federated mode, and
single mode with `FLOWA_FEDERATE=on`, start since the WebFinger and NodeInfo item, and can be read since the
actor documents item (discovery, the actors, the profiles and Public posts of members who opted in, and since the
outbox item their outboxes and collections; no inbox, so nobody can follow yet, and nothing is delivered); the
first item waits for its Pi measurement; the guarded fetcher is done, and nothing calls its federation part yet;
HTTP signatures are built, with no caller yet, and wait for their Pi measurements; WebFinger and NodeInfo are
done; actor documents and public pages are done, with the post audience and retired names; the outbox and
collections are done; remote accounts and the cache of remote actors are built and wait for the recorded
fixtures; the search by handle is built, the cache's one caller, and waits for the same fixtures and a browser
check against a remote server; the owner's review of the design is still asked for. 4 of 8 items done, and
the code of all eight is written (what is left is listed under "What of FRD is still open" at the end). The design note was checked against the specifications and
Mastodon's documentation on 12 November 2026; its Sources section lists what no text settles, and
an item that relies on one of those details settles it (from a recorded fixture or the owner's
trial) and corrects the note. Every setting, table, route and test named below is new unless the
design note says it exists.

- [ ] **Public origin, key secret and keys.** `FLOWA_ORIGIN` (https only; its host accepted by
  `allowedHost`), `FLOWA_KEY_SECRET` (64 hex characters), federation refused at start without them,
  with a message; `ap_keys` with RSA 2048 keys sealed by AES-256-GCM, generated in the background one
  at a time; the origin recorded at first federation and a later change refused (federation.md 4.3);
  `/nunchaku -rotate-keys`. DoD: `TestOrigin`, `TestSealKey` (wrong secret, tampered bytes),
  `TestDBKeysGenerated`, `TestDBOriginFrozen`; the key generation time on the Pi written here.
  Built: `parseOrigin`, `parseKeySecret` (HKDF-SHA256 to the AES-256-GCM key; the value never printed),
  sealing bound to the row (`v1.`), `ap_keys` (migration 12), `ensureKey` and one key worker, the origin
  recorded and a change refused (recorded when a member's actor id is first announced since FRD 4, which moved
  it from the instance actor's first key: a key holds no origin, and a mistyped origin started once must
  not freeze; federation.md 4.3), `-rotate-keys`, and `main` waiting for `Shutdown` and the worker (8.6);
  `TestOrigin`, `TestKeySecret`, `TestSealKey`, `FuzzOpenKey`, `TestDBKeysGenerated`,
  `TestDBOriginFrozen`, `TestDBFederatedStart`, `TestServeWaits` and `TestDBServeStopsWorker` pass. No
  key is made for anyone while federation is refused. Reachable by an instance since the WebFinger and
  NodeInfo item, which made the mode startable. On the CI-class runner (Xeon 2.3 GHz) `BenchmarkGenerateKey` is
  about 44 ms a key, `BenchmarkSealKey` 3.6 µs and `BenchmarkOpenKey` (open and parse) 0.12 ms.
  Open: the key generation time on the Pi (`go test -run xxx -bench GenerateKey -benchmem .` on the Pi, or
  the line "keys: made the key of the instance actor in N ms" in its log).
- [x] **One guarded fetcher.** `fetcher.do` generalises `fetcher.get`: GET and POST, any host the
  policy allows for federation, the preview allowlist for link cards as before, redirects re-checked
  (to another origin only for media and WebFinger), caps on time, size and content type, the
  `Accept` header ActivityPub requires on a GET; only port 443, and the 6to4 and Teredo ranges
  refused (federation.md 7.5 and 7.8). Until FMD's domain policies exist the fetcher takes its list of
  refused hosts as an argument, which the tests fill. DoD: `TestFetcherRefusals` and
  `TestFetcherLookups` pass unchanged; `TestPublicIPReserved` gains the two ranges;
  `TestFederationFetcherRefusals` covers a redirect to a private address, to http, to a refused
  host, another port, a large body, a slow body, a wrong type, a mismatched id.
  Built: `fetchRules`, one row per kind (link card, document, WebFinger, NodeInfo, picture, delivery) with
  its method, headers, deadline, size, answer types, statuses and redirect rule; `fetcher.get` a thin caller
  for link cards; a typed `signer` hook run on every request sent; `fetchError` with the failure kinds later
  items decide by; `sameID` for the id check of federation.md 7.3; one turn per host, taken where each request
  lands after redirects, the wait capped at the kind's time (`failBusy`), dropped when idle;
  `refusedHosts`, which takes only ASCII names; `fetcher.deny`, the hook for the instance's own addresses; `reservedNets` with 6to4, Teredo and the ranges federation.md 7.8 lists.
  `TestFederationFetcherRefusals` and its neighbours, `TestDialGuard`, `TestSameID` and
  `TestPublicIPReserved` pass; `TestFetcherLookups` and `TestFetcherRefusals` are unedited (the latter
  needs IPv6 on the machine for its `[::1]` line, which CI has). `BenchmarkFetchDocument` on the CI-class
  runner: about 70 to 85 µs, 9.1 KB and 107 allocations a fetch over a kept-alive TLS connection, the
  local stub's own work included; section 12 names no budget for it.
- [ ] **HTTP signatures.** Signing and verifying draft-cavage-12 (`rsa-sha256` sent; `hs2019` and no
  `algorithm` accepted, all verified as RSASSA-PKCS1-v1_5 with SHA-256; `Digest: SHA-256=` on POST)
  and RFC 9421 as Mastodon accepts it (one signature, `created` present, `@method`, `@target-uri`
  and `content-digest` signed, `Content-Digest: sha-256=:...:`, `rsa-v1_5-sha256`, POST only), the
  request line among the signed parts, the digest checked, the date window (12 hours back, 1 hour
  ahead), the replay map, whole origins compared for the key and its owner, PKCS#1 and SPKI public
  key PEMs (federation.md 7.1 to 7.3); standard library only. DoD: `TestSignatureVectors` against
  the vectors federation.md 7.1 names, copied into `testdata/federation/vectors/` with their
  section numbers: cavage-12 Appendix C.1 and C.2 (signed and verified, PKCS#1 v1.5), RFC 9421 4.3
  `proxy_sig` with the B.1.1 key (signed and verified, PKCS#1 v1.5), and the signature bases of RFC
  9421 2.5 and B.2.1 to B.2.3 (base only, their signatures are RSA-PSS); `BenchmarkInboxVerify` and
  `BenchmarkSignDelivery` with `-benchmem` under the budgets in federation.md section 12;
  `TestReplayWindow`.
  Built, with no caller yet (federation.md 7.1 to 7.3 say what and why, section by section): the pure
  parsers and builders (`parseCavage`, `cavageString`, a strict Structured Fields parser for
  `Signature-Input`, `Signature` and `Content-Digest`, `rfc9421Base`, `parseDigest`,
  `parseContentDigest`, `parsePublicKeyPEM`), the policy (`sigVerifier.verify`, a typed `sigReason` for
  every refusal), the replay memory (`replaySet`: entries kept until the window would refuse them,
  grouped by the key id's domain (its last two labels), each group with its own floor and a cap of a
  tenth, the heaviest group evicted from when full, 2,000 groups at most, so a flood costs only its
  sender and its domain),
  `clockTrusted` and `builtAt`, `verifyRequest` for a
  request that arrived and `httpSigner` for the fetcher's hook. `TestSignatureVectors` (cavage C.1 and
  C.2 verified and reproduced, C.3 kept as a documented negative, RFC 9421 4.3 `proxy_sig` verified and
  reproduced, the bases of 2.5 and B.2.1 to B.2.3 byte for byte, the digests of RFC 9530, RFC 9421 and
  the draft), `TestReplayWindow`, `TestReplayFlood` and `TestReplayFloodThroughVerifier` (the review's
  reproduction: one key's flood dated 59 minutes ahead no longer refuses anyone else),
  `TestReplayManyOrigins`, `TestReplaySenderGroup`, `TestReplaySubdomainFlood`, `TestReplaySharedFloor`
  (the cost of grouping by domain, written down), `TestReplayModel` (random operations against a reference that remembers
  everything), `TestReplayExpiry`, `TestReplayConcurrent`, `TestSignaturePolicy`, `TestSignatureNever`, `TestSignatureWindow`,
  `TestSignatureThroughFetcher` (both forms, GET with a redirect and POST, over TLS) and their
  neighbours pass, and the three fuzz targets ran 30 s each, and 20 s each again after the replay
  memory was rebuilt, with no failure. On the CI-class runner (Xeon 2.3 GHz) `BenchmarkInboxVerify` is
  about 35 µs, 4.4 to 5.6 KB and 51 to 56 allocations (budget 2 ms and 400), measured again on a 2.1 GHz
  Xeon at about 46 µs, 4.7 to 5.9 KB and 55 to 60 allocations; `BenchmarkSignDelivery` about 0.8 ms
  (1.0 ms on the 2.1 GHz one); `BenchmarkReplayAdd` at the cap 0.4 to 0.6 µs and no allocation; the
  replay memory 52 to 56 bytes a signature, 5.2 to 5.6 MB full, at most about 7 MB. Open: the two Pi budgets of section 12, 10 ms to verify and 15 ms to sign
  (`go test -run xxx -bench 'InboxVerify|SignDelivery' -benchmem .` on the Pi).
- [x] **WebFinger and NodeInfo.** `/.well-known/webfinger`, `/.well-known/nodeinfo` and
  `/nodeinfo/2.1`, only in a federating mode and only for members who opted in (the `federates`
  column and its switch in Settings land here). Opting in with a name that ends with a dot or a dash
  shows a warning that Mastodon cannot mention it (federation.md, finding 11). DoD:
  `TestWebFinger` (every edge case in federation.md 5.3, including `rel` and the CORS header on
  errors), `TestNodeInfo` (the discovery JRD, every field the 2.1 schema requires, the content
  type, counts as the schema defines them); `TestDBLocalModeUnchanged` still green.
  Built (federation.md 4.1, 4.2, 4.3, 5.1, 5.3, 5.4, 5.7, 11 and 14.2 say what and why, each in an "As built
  (FRD 4)" paragraph): `federated` in `modesBuilt` and `FLOWA_FEDERATE` read (`modeFederates`: single mode
  only, `on` or `off`, default `off`, anything else stops the start), so a mode that federates is federated
  mode or single mode with it on; the start logs once that this version answers discovery only, and the
  README, the note and the dashboard's mode line (from the server's `built`) say the same; `GET
  /.well-known/webfinger`, `GET /.well-known/nodeinfo` and `GET /nodeinfo/2.1` registered only in a mode that
  federates (`testdata/routes-federated.txt`), with the CORS header on every answer there and nowhere else,
  405 and `Allow` for other methods before the write guard (only in a mode that federates), short cache
  times, and an allowance of 600 an hour per address apart from every member's; `parseResource` (every form
  of 5.3, decided against RFC 7033, RFC 7565 sections 4 and 7, RFC 3986 and Mastodon's WebFinger page); one
  statement on the unique username index for a member, the same 404 for an unknown, a not opted in, a
  suspended and a deleted member, and in single mode the owner only; the origin frozen by the first answer
  that names a member (`ensureOrigin`), not by a key, not by the instance actor's answer and not by NodeInfo,
  and the dashboard saying "not announced yet" or since when; NodeInfo from typed structs, its counts kept
  five minutes, `software.version` the empty string (the build is private by default, as in `/healthz`),
  checked against the schema file itself (`testdata/federation/nodeinfo-2.1.schema.json`); `users.federates`
  (migration 13, with `flowa-schema12.dump` for the restore drill), the switch in Settings ("Beyond this
  server", labelled and explained in the server's words for what is built, only in a mode that federates;
  implied and without a switch in single mode; "unavailable", with the reason for staff, while federation is
  refused), the field ignored as any unknown field outside a mode that federates, the member's key asked for
  on opting in, the owner's at start in single mode, and every start asking again for opted-in members
  without one; the warning of finding 11. `TestWebFinger`, `TestDBWebFinger`, `TestNodeInfo`,
  `TestNodeInfoSchemaCheck`, `TestDBNodeInfo`, `TestFederateSetting`, `TestDBFederatedRoutes`,
  `TestDBFederateOptIn`, `TestDBDefaultsUnchanged` and the reworked `TestDBOriginFrozen` pass, and
  `TestDBLocalModeUnchanged`, `routes-local.txt` and `routes-single.txt` are unedited and green; the
  `federated` browser suite (in `test/ci-plan.sh` and the workflow: federated mode with `https://flowa.test`
  and a test-only key secret) asks the three addresses over HTTP, works the switch with the keyboard, holds the
  card to the rules of `keys`, `a11y` and `contrast` (their walks, read from their source) and to 420 px, and
  writes the pictures of the card at desktop and 420 px and of the dashboard's mode line, which the screenshots
  job publishes with the others. `BenchmarkWebFinger` with `-benchmem` through the whole server chain on a
  2.1 GHz Xeon: the instance actor (no query) about 6.5 µs, 3.3 KB and 37 allocations a request; a member (one
  query to a local Postgres) about 0.29 ms, 4.1 KB and 58 allocations.
  Left for later items, outside this one's DoD: the instance setting `nodeinfo_version` (5.4), which would let
  an admin publish the version (the default stays the empty string); `/users/{name}`, which the `self` link
  names, served by FRD 5; and the `federation` pause flag of F1 and 14.1, which no item has built yet (the
  mode, off by default, is the gate meanwhile), for the owner to place.
- [x] **Actor documents and public pages.** `/users/{name}` and `/actor`, content negotiation with
  `Vary: Accept`, the small HTML pages for profiles and Public posts, the public picture URLs, the
  post audience (Public or This server) in the composer. A name is public from here on, so
  `retired_usernames` lands here: the name of a deleted account that had opted in is never given to
  anyone else. The actor's properties and context are those federation.md 5.5 checked against
  Mastodon's ActivityPub page (`indexable` false among them). DoD: `TestActorDocument` against a
  golden file, `TestContentNegotiation`,
  `TestDBPublicSurfaces` (every public route refuses This server posts and members who did not opt
  in), `TestDBRetiredNames` (refused at sign-up and at a rename); a screenshot of the public post
  page.
  Built (federation.md 4.1, 4.2, 4.3, 5.2, 5.3, 5.5, 5.6, 6.2, 6.5, 9.4 and 11 say what and why, each in an "As
  built (FRD 5)" paragraph), in two commits. The audience: `posts.audience` (migration 14, `local` for every
  post there was and every post made outside a mode that federates, the check `NOT VALID`; `posts_public` for
  the public pages), "Who can read it" in the composer (Public first, only for a member who opted in while
  federation is not refused; the server never gives Public by a default), the mark beside the author's own
  post, milestones This server (F13); posts are not edited, so taking a Public post back is deleting it, and
  sending that Delete to the servers that read it is FWR's (account deletion and Delete of Notes). Retired
  names: `retired_usernames` (migration 14, with `flowa-schema13.dump` for the restore drill), filled in the
  delete's transaction for an account that opted in or ever had a key, in every mode; refused at sign-up, at a
  request to join, at approving one and for `ADMIN_USER`, as a taken name; no endpoint renames; the instance's
  host reserved in a mode that federates. The read side: `GET /users/{name}`, `GET /users/{name}/posts/{id}`
  and `GET /actor` with content negotiation on the same URL (`parseAccept`, `wantsActivity`; `Vary: Accept`
  on every answer), the actor of 5.5 and the Note of 6.5 from typed structs (inbox and outbox named, as
  ActivityPub 4.1 requires; followers, following, featured and the shared inbox left out until they answer),
  the small pages from `pubTmpl` (no script, the policy of 5.6, `noindex`, links `nofollow noopener ugc`,
  pictures from this server, Sign in, a profile's Public posts 20 at a time by a cursor), the two public
  picture addresses (`.jpg` or `.png`), one 404 per representation for everything not shown, 410 with a
  Tombstone for a retired name, 503 while an opted-in member's key is being made, `ensureOrigin` before a
  member's document or Note (not the instance actor's), `limits.public` (2,400 an hour per address, before
  any database work), 405 with `Allow` for other methods, no CORS header; the actor documents kept per member
  and dropped on every change of what they say; single mode with `FLOWA_FEDERATE=on` lists the owner's Public
  posts at `/`; the one sentence of what is built (`fedBuilt`) read by the start log, the dashboard and
  NodeInfo's `metadata.federationBuilt`. `TestActorDocument` (golden files `actor-ana.json`,
  `actor-instance.json`, `note-ana.json`, both `Accept` forms, the 304, a strict reader's shape: one `type`, https
  ids, no name twice), `TestContentNegotiation` (and `FuzzAccept`), `TestNoteHTML`, `TestDBPublicSurfaces`
  (every public address and every case byte for byte in both representations, every post id under every name
  for the pictures, HEAD, 405, the allowance, refused federation), `TestDBActorCache`, `TestDBPublicPages`,
  `TestDBRetiredNames`, `TestDBPostAudience`, `TestDBQueryPlans` (each public statement on an index, with
  70,000 posts, 2,000 retired names and 500 keys) and `TestDBDefaultsUnchanged` (every public address, seven
  methods, four `Accept` values, every header compared with an unknown address in local and plain single mode)
  pass; `routes-local.txt`, `routes-single.txt` and `fetch_test.go` are unedited. The `federated` browser suite
  works the composer with the keyboard, reads the actor, the Note and a This server post's 404 over HTTP, opens
  the public post page and the profile page held to the a11y, contrast and keys walks, and writes their pictures
  at desktop and 420 px (`federated-post`, `federated-profile`, and `-phone`) with the composer's. On a 2.1 GHz
  Xeon through the whole server chain, `BenchmarkActorDocument` (kept in memory, the gate's one query to a local
  Postgres) is about 0.54 ms, 6.5 KB and 61 allocations a request, and `BenchmarkPublicPost` (one query, the Note
  made) about 0.88 ms, 7.8 KB and 108 allocations (`go test -run xxx -bench 'ActorDocument|PublicPost'
  -benchmem .`). `TestDBOwnDocumentsDecode` runs every golden file and documents served for awkward members
  and posts through the strict decoder of 6.3 (`apScan`, `decodeActor` with `key()`, `decodeNote`,
  `decodeTombstone`); `TestDBRetireRace` holds that no door gives away a name a delete is retiring (a lock per
  name, 5.2). Open: the answers local mode gives after federating (410 and a
  Tombstone, 4.3), which the note does not place in this item; the instance setting to let a member be indexed
  by search engines (the pages say `noindex` until then).
- [x] **Outbox and collections.** A paged outbox of Create activities for Public posts; followers and
  following with public counts and pages only for the account itself; an empty featured collection.
  DoD: `TestOutboxPaging` (empty, one page, many pages, a deleted post, a This server post absent).
  Built (federation.md 5.2, 5.5, 6.2, 6.4, 6.5, 7.6 and 11 say what and why, each in an "As built (FRD 6)"
  paragraph): `GET /users/{name}/outbox` (an `OrderedCollection` with `totalItems` and `first`; pages of 20
  Creates newest first, `OrderedCollectionPage` with `partOf` and `next`, no `prev`, by the profile page's cursor
  `?page=true&before=<id>` through the same statement; every other query the 404, read whole from the raw query
  by `docQuery`, which also holds every other new address to no query at all), `GET
  /users/{name}/posts/{id}/activity` (the Create at its own id, the post's full address with `/activity` after it,
  as in `https://flowa.example.org/users/ana/posts/812/activity`: `createOf`, the one function the outbox and FWR 4
  use, to the public and cc the followers as the Note now is), `GET /users/{name}/followers` and `/following`
  (a count only while the app shows counts, the `community` and `follows` flags both on, through the
  predicate the app's profile asks, `followCountsShown`; counting only follows whose other side also federates,
  opted in and not suspended, and none across a block, `publicFollowCountsSQL`; never `first`, a page or an item:
  nobody who asks there can be the account itself, which reads its lists in the app; whether a public count
  should include every local follower is left to the owner under F10), `GET /users/{name}/featured` and `GET
  /actor/outbox` (empty); the
  actor names `followers`, `following` and `featured`; `fedBuilt` and NodeInfo's parts (`outbox`, `collections`)
  say so; a browser at any of these is sent with 303 to the page that shows the same thing; a retired name is the
  404 there, as at its posts. Every rule of the public addresses holds through item 5's code (the gate, one 404,
  the allowance first, HEAD, 405, `Vary: Accept`, three minutes in caches, `ensureOrigin`, no CORS). This item
  found that a profile page cursor past 2^31 answered 500; the fix (a bigint) merged with FRD 5 (#107). When the
  published skill names cannot be read, the shipped ones stand in for 10 s (`namesRetry`), so a failing read
  costs one statement per prop, not one per chip and combo step of every Create on a page (80 measured without
  it, `TestDBNamesFailingRead`). The test is `TestDBOutboxPaging` (the DB prefix
  CI's database job selects with `-run DB`): empty, one page, many pages (20, 20, 6), a deleted and a hidden
  post, This server posts and a milestone absent and uncounted, cursors naming other members' posts, This server
  posts, missing posts and ids past 32 bits, every query that names no page, the browser's redirect; with
  `TestDBCreateActivity`, `TestDBCollections` (the counts with the community flag off, followers who never
  opted in, are suspended or opt out later, a blocked follow), `TestDBCollectionSurfaces` (each new address,
  every case and every query that names nothing, byte for byte in both representations, HEAD, 405, the
  allowance, refused federation), `TestDBOwnDocumentsDecode` (the outbox, its pages, the Creates and the
  collections through `decodeCollection` and `decodeActivity`), `TestDBOneConnection` and `TestDBPublicPool`
  (every new address on a pool of one and 40 at once on the real pool, at a cold names cache, with 25 posts with
  chips and a combo), `TestDBQueryPlans` (`outboxSQL`: the count reads the member's Public posts through
  `posts_public` and nothing else; `publicFollowCountsSQL`: the member's follows and each other side by its
  key), `TestDBDefaultsUnchanged` (the new addresses in local and plain single mode) and `TestDBFederatedRoutes`
  pass; `routes-local.txt`, `routes-single.txt`, `headers-local.txt` and `fetch_test.go` are unedited. The
  `federated` browser suite reads the outbox, its first page, the Create, the collections and the redirect over
  HTTP. On a 2.1 GHz Xeon through the whole server chain, `BenchmarkOutboxPage` (a full page of 20 Creates, one
  query to a local Postgres) is about 1.7 ms, 137 KB and 1,326 allocations a request (`go test -run xxx -bench
  OutboxPage -benchmem .`). Left for the owner: the count walks the member's Public posts (about 6 ms for 22,000
  on that machine) because `posts_public` does not hold `kind`; adding `AND kind = 'post'` to its condition
  would make it an index-only count.
- [ ] **Remote actors by signed fetch.** Remote accounts as `users` rows with `domain`, and
  `ap_actors` (F5); fetches signed by the instance actor; expiry after 24 hours; a key refetched on a
  failed verification at most once in 10 minutes; negative caching; every surface that lists members
  limited to local ones; the decoder for the shapes JSON-LD allows (federation.md 6.3). This item
  is the first to store remote rows, so its pull request says that an older build after
  `./deploy.sh --rollback` lists them as members, and adds `/nunchaku -purge-remote`. The recorded
  fixtures land here (federation.md section 13), each marked as recorded or as written from
  documentation; with them, the details the note's Sources section lists as settled by a recording
  (alt text property, mention nesting, the actor next to `TestActorDocument`'s golden file) are
  settled and the note corrected. DoD: `TestDBRemoteActorCache`, `TestFixturesParse`, `TestDecodeShapes`,
  `TestDBPurgeRemote`,
  `TestDBRemoteRowsStayRemote`; `TestDBQueryPlans` holds with 2,000 remote accounts.
  Built so far, with no caller yet: the decoder (federation.md 6.3 says what it reads and every rule
  chosen where the texts leave a choice; 7.5 the limits it holds, its memory among them). `apScan` checks
  a whole document in one pass before anything is decoded (well-formed JSON, UTF-8 without lone
  surrogates, 24 levels, 1,000 entries in any list or object, no member name twice in any spelling);
  `oneOrMany` and `idOrObject` read every property that may be a list or a link; `decodeActor` (with
  `key`, chosen by id, fragment included), `decodeActivity`, `decodeNote`, `decodeTombstone` and
  `decodeCollection` read exactly what the note uses, and the same for an object embedded in another.
  After its review it refuses a document only for what Flowa would act on wrongly (an id, the actor,
  an author, the object acted on, an address it delivers to, more than one value where one is acted on,
  by a table of activity types for `object`) and drops and counts what it merely cannot use (recipients,
  `alsoKnownAs`, Mentions, unreadable keys, a Flag's foreign objects); display fields that cannot be read
  are absent on the careful side, an unreadable `inReplyTo` is a reply to an unknown post
  (`ReplyToUnknown`), and `summaryMap` and `nameMap` are read. Every refusal is a typed reason with what
  was read and the property's path. The 28 documents in `testdata/federation/shapes` are examples copied
  from ActivityPub, Activity Streams 2.0 Core, Mastodon's ActivityPub page and this note, none recorded,
  so the recorded fixtures, `TestFixturesParse` and the questions only a recording settles (alt text
  property, mention nesting) are still open. `TestDecodeShapes`, `TestDecodeFixtures`, `TestDecodeActor`,
  `TestDecodeNote`, `TestDecodeActivity`, `TestDecodeCollection`, `TestDecodeRefusals`, `TestDecodeDrops`,
  `TestDecodeLanguageMaps`, `TestDecodeErrorText`, `TestDecodeContextTerms`, `TestDecodeAdversarial` and
  `TestDecodeMemory` pass with `-race -count=3`; `FuzzDecodeDocument`, `FuzzDecodeScan` and the review's
  two invariants, `FuzzDecodeNames` (the walker sees the names and values `encoding/json` does) and
  `FuzzDecodeIDs` (every id and link returned passes `apIDOK`), ran 30 s each (the first round's run
  found that whitespace before the document was refused, fixed and kept as a corpus entry). 28
  deliberate breakages, three of them for the review's new rules, each fail a test. On a 2.1 GHz Xeon
  `BenchmarkDecodeNote` (the Create of 6.5 with its Note) is about 41 µs, 14 KB and 173 allocations,
  `BenchmarkDecodeActor` (the actor of 5.5) about 26 µs, 12 KB and 135 allocations; the costly inputs of
  `BenchmarkDecodeAdversarial` take 11 to 17 times as long at 1 MB as at 64 KB, the slowest (a thousand
  attachments of empty objects) about 22 ms for 1 MB, and a list, a nesting or a repeated name over the
  limit is refused in under 0.05 ms at either size. One decode of a 256 KB body allocates at most 3.4 MB
  measured (budget 4 MB, `TestDecodeMemory`); FWR's inbox item records that it decodes at most 4 bodies
  at once.
  Built next, with one way in and no caller yet (federation.md F5, 4.3, 6.3, 7.3, 7.4, 9.1, 9.2, 9.3, 12, 13
  and 14.4 say what and why, each in an "As built (FRD 7)" paragraph, with the corrections the code settled):
  remote accounts as `users` rows with `domain` and `ap_actors` (migration 15, with `flowa-schema14.dump` for
  the restore drill; every existing account local; the check `users_remote_inert` keeps a remote row from a
  password, a role, the owner, the opt-in and a name that is not "name@host"; `users_local`); the username
  of a remote account (the `preferredUsername` as written, case kept, else or when taken "+" and twelve hex
  digits of the actor id's hash, always "@host"; hosts in their `xn--` form), its display name as plain text,
  its summary kept as HTML for FWR's `htmlToText`; every statement that reads `users` gone through, each
  surface that lists or looks up members local only (9.2 lists them with the decision for each), sessions
  and sign-in included; the cache (`remoteActor`, the one way in, for FRD 8; `remoteKey(ctx, keyID, use)` and
  `remoteKeyFailed` for FWR's verifier): fetches signed by the instance actor (cavage) through `fetcher.do`
  after `ensureOrigin` (the first signed request freezes the origin), `decodeActor`, `sameID` after
  redirects, the key owned by the actor and on its origin, a key at a path of its own followed to its owner
  and back, expiry after 24 hours on the cache's own clock, a key fetched again on a failed verification at
  most once in 10 minutes per actor and a changed key taken, failures remembered by kind (10 minutes; 410 a day
  for an id never stored, for good for a stored actor, which turns `gone`; this server's own state never), a
  stored actor answered stale when its fetch fails, `suspended` as a state of its own, one fetch per actor
  (callers wait, each on its own context), at most 2 at once, no connection or lock held across the network,
  20,000 actors at most with the least recently fetched removed first and never one something points at,
  the instance's own origin and the refused hosts (a hook, empty until FMD) never asked; `/nunchaku
  -purge-remote` (one transaction, any mode, prints what it removed, idempotent; it does not check for a running
  server, as `-rotate-keys` does not). `TestDBRemoteActorCache`, `TestDBRemoteActorFlight`,
  `TestDBRemoteActorBound`, `TestDBRemoteActorNotReady`, `TestDBRemoteFixtures`, `TestDBTwoFlowaActors` (one
  Flowa fetches a member of another through the real fetcher and the other's routes, which verify the signature
  with `verifyRequest`), `TestFixturesParse` (every document under `testdata/federation`, the shapes, three
  actors written from Mastodon's page and the SWICG report in `testdata/federation/actors`, and the golden
  files, through the decoder and the cache's choices), `TestDecodeShapes`, `TestDBPurgeRemote`,
  `TestDBRemoteRowsStayRemote` (every surface byte for byte with and without remote rows, in local, federated
  and single mode), `TestDialOverrideTestOnly`, `TestRemoteUsername`, `TestRemoteDisplay`, `TestActorReasons`,
  `TestDBOneConnection` (a fetch held at the stub on a pool of one) and `TestDBQueryPlans` with 2,000 remote
  accounts (people and the dashboard now through `users_local`; each new statement on its index) pass, the new
  ones with `-race -count=3` on two cores; `routes-local.txt`, `routes-single.txt`, `headers-local.txt` and
  `fetch_test.go` are unedited, and `TestDBLocalModeUnchanged` and `TestDBDefaultsUnchanged` pass. On a 2.1 GHz
  Xeon `BenchmarkRemoteActorCached` is about 0.8 ms, 6.2 KB and 79 allocations a lookup and
  `BenchmarkRemoteActorFetch` about 7 ms, 50 KB and 586 allocations a refetch (signing, the local stub and five
  queries); a cached actor with a 2048-bit key takes 858 bytes in its two rows (budget about 2 KB). After the
  review, every way in goes through one lookup: a string that is not the actor's id (a fragment, a
  redirecting address) is remembered as its name (at most 8 per actor) and a key id the actor does not name is
  refused and remembered, so N asks of one string cost at most one fetch in the time its answer is kept
  (`TestDBRemoteOneAnswer`: 5 lookups of a redirecting id 2 requests, were 10); a key kept at a path of its
  own is followed at every fetch (`TestDBRemoteKeyOfItsOwn`); no lookup can wait on itself
  (`TestDBRemoteKeyLoops`); one turn per host (`TestDBRemoteActorTurns`); a key verifies only from a fresh
  row of an actor neither gone nor suspended, except for its own Delete (`keyForOwnDelete`); a caller that
  waited for another's fetch gets an answer to its own question (`TestDBRemoteActorWaiters`); the bound never
  removes an account any table points at, checked against the catalog's foreign keys
  (`TestDBEvictionCoversReferences`, with the nine indexes it needs in migration 15); a stop waits for a fetch
  (`TestDBRemoteActorStop`); a stale row is answered for at most 30 days (`TestDBRemoteActorStaleBound`).
  Open: the
  recorded fixtures (none can be recorded here; `test/federation-record.sh` is not written), and with them the
  details federation.md's Sources section leaves to a recording (the alt text property, the nesting of a
  mention, an actor beside `TestActorDocument`'s golden file); the in-memory cache of parsed keys and the limit
  of key fetches per host, which come with FWR's inbox, the first to verify.
- [ ] **People elsewhere: search by handle.** A box that takes `@name@host` or a profile URL,
  resolves it by WebFinger (HTTPS only, the `self` link, the reverse lookup from the actor's
  `preferredUsername` and host: federation.md section 11), and shows the remote profile and the
  first page of its outbox; no full-text search; an allowance per member. DoD: `TestDBResolveHandle`
  against a stub serving the recorded fixtures (including a WebFinger redirect to another host and
  an actor whose reverse lookup does not match); a browser check with the stub.
  Built (federation.md 5.3, 6.3, 6.6, 9.1, 9.4, 11, 12, 13 and 14.4 say what and why, each in an "As built (FRD 8)"
  paragraph, with the corrections the code settled), in three commits besides the restore drill's
  `flowa-schema15.dump`. The outbox address of a remote actor: the decoder reads `outbox` and leaves out one it
  cannot use, and `ap_actors.outbox` (migration 16) keeps it; a commit of its own because it touches FRD 7's
  cache. Remote HTML as text: `htmlToText` as 6.6 specifies (planned with FWR 5, needed here for the bio and
  the posts), including the `Cut` rule (whatever is left open at the end is dropped, and for a cut text an
  entity without its semicolon), with the correction that a mention link is matched to its Mention by href or
  by its short `@name` on the Mention's host (Mastodon links the profile page and names the actor id);
  `TestRemoteHTMLToText`, `TestRemoteHTMLFixtures`, `FuzzHTMLToText` (30 s, no failure) and
  `BenchmarkHTMLToText` (64 KB of each kind of markup in 0.04 to 1.8 ms). The search: `POST /api/fed/lookup`
  (only in a mode that federates, behind `guard` and a session, `testdata/routes-federated.txt`), `parseLookup`
  (strict, a table test), `localTarget` (this instance's own host answered from the database, never fetched),
  `lookupHostOK` (the fetcher's host rules before any connection), forward WebFinger through the fetcher's
  WebFinger kind, `parseJRD`, the actor through `remoteActor`, the reverse check with the canonical account
  of Mastodon's page and the rule written in 5.3 for a handle's host apart from the actor's (the query's own
  subject must be the proven account), a profile address asked as an actor and else of its host's WebFinger by
  URL, the first page of the outbox (only Public and unlisted Notes by the actor on its origin, at most 20,
  nothing stored), eight fixed reasons (`lookupReason`), `limits.lookups` (30 an hour per member, counted first),
  one lookup at a time per member and 4 at once, at most 8 requests through the fetcher and 15 s (when the
  member's request ends the lookup stops; an actor fetch already started runs on in the cache, as 7.4 says);
  the card "People elsewhere" in the timeline (`lookupCard` in `web/social.js`, offered by `/api/me`'s
  `federation.lookup`), with no remote picture and no Follow, and the server's notice saying why; `fedBuilt`
  and NodeInfo's parts (`lookup`). `TestParseLookup`, `TestAcct`, `TestParseJRD`, `TestLookupReasons`,
  `TestDBResolveHandle` (against stubs serving documents written from Mastodon's WebFinger page and the SWICG
  report, through the test-only `dialOverride`: a plain handle in 4 requests, another case, a WebFinger redirect
  to another host for a handle domain apart from its server, a self link to someone else's actor, an actor
  whose reverse lookup names another actor, finds nothing, or names a canonical account that leads elsewhere or
  is on this instance, a profile page, a profile address that redirects to its actor (5 requests, 3 when asked again), an actor whose server stops answering (shown with the date it was kept) and one now gone, every failure, the outbox's cases, a lookup that takes every step in 13
  requests, and a member who leaves), `TestDBLookupRoute` (the allowance before any connection, a refused
  address costing its count, the turns, a visitor, a suspended member, another page, a GET, staff answered byte
  for byte as members, another server's words never shown, nothing remote as HTML), `TestDBLookupOffered`,
  `TestDBOneConnection` (a lookup held at its WebFinger and at its outbox on a pool of one),
  `TestDBDefaultsUnchanged` (the new address in local and plain single mode, every method, signed in or not, as
  an API address nobody has), `TestDBFederatedRoutes`, `TestNodeInfo`, `TestDBRemoteActorCache` and
  `TestDecodeActorOutbox` pass, the new ones with `-race -count=3` on two cores; `routes-local.txt`,
  `routes-single.txt`, `headers-local.txt` and `fetch_test.go` are unedited, and `TestDBLocalModeUnchanged`
  passes. The `federated` browser suite asks the address over HTTP for a handle on its own host and for every
  failure that needs no other server (not a handle, http, a private address, localhost, another port, a name
  that does not resolve), a visitor, another page's write and a GET, then works the card with the keyboard (a
  member found, an address refused, each said to a screen reader) held to the a11y, contrast and keys walks and
  to 420 px, and writes `federated-lookup`, `federated-lookup-failure` and their `-phone` pictures.
  After its review (#110): a handle's name part only as `handleNameOK` takes it, shown isolated and apart from its host; the name fallback through the proven handle; links only to the account's own origin, with the host shown; one reason, "not found", for every failure after the resolver, answered no sooner than 1 s (`lookupFloor`); the request bound corrected to 8 (the key document the cache fetches inside `remoteActor`); outboxes read without the actor cache's turns, and one lookup at a time per member. `TestHandleNameOK`, `FuzzHandleName` (30 s), `TestDBLookupReviewProbes`, `TestDBLookupTimingFloor` and `TestDBLookupKeepsKeyTurns` hold them.
  Open: the DoD's "recorded fixtures" (none is recorded: the stub serves documents written from documentation,
  as FRD 7's do) and "a browser check with the stub" (the browser suite's server refuses private addresses and
  must not be given a way round that, so a remote account, the reverse check and the outbox are checked in Go
  only; a browser sees a remote profile first with FWR 8's two instances); remote pictures, with FWR 5's cache
  (F6, 9.4); Follow, with FWR 3.

## What of FRD is still open

With this item every item's code is written; what keeps four boxes open is not code:

- **Measurements on the Pi** (FRD 1, FRD 3): the key generation time, and the two budgets of section 12 (10 ms
  to verify a signature, 15 ms to sign a delivery), by the commands written in those items.
- **Recorded fixtures** (FRD 7, FRD 8, and the details the note's Sources section leaves to a recording: the
  alt text property, the nesting of a mention, an actor beside `TestActorDocument`'s golden file, and whether
  Mastodon answers a profile page's address with its actor): `test/federation-record.sh` is not written, and
  no session here can reach a fediverse server; the owner, or a session that can, records them.
- **A browser check of the search against a remote server** (FRD 8): with FWR 8's two instances, the first
  place where a running Flowa may reach another one.
- **The owner's decisions**: the review of the design note (F1 to F16 proceed on their recommendations), the
  instance setting `nodeinfo_version` and the `federation` pause flag left from FRD 4, the answers of local
  mode after federating (410 and a Tombstone, 4.3) and the indexing setting left from FRD 5, the public count
  of followers under F10 and the `posts_public` condition left from FRD 6, and whether the search by handle
  should show unlisted posts as Mastodon does (it does) or only those addressed to the public in `to`, and
  whether it should be offered to every member (it is: looking someone up shares nothing of the member) or
  only to members who opted in.
