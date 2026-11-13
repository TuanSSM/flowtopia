# FRD: Federation, read side

Why, scope and risks: [epics.md](../epics.md#frd-federation-read-side). Design:
[federation.md](../federation.md). Order: [roadmap.md](../roadmap.md#third-cycle).
Category: feature. Size: L. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** in progress (issue [#89](https://github.com/TuanSSM/flowtopia/issues/89)): the first item is
built behind the mode, which is not startable yet, and waits for its Pi measurement; the owner's review of the
design is still asked for. 0 of 8 items done. The design note was checked against the specifications and
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
  recorded and a change refused, `-rotate-keys`, and `main` waiting for `Shutdown` and the worker (8.6);
  `TestOrigin`, `TestKeySecret`, `TestSealKey`, `FuzzOpenKey`, `TestDBKeysGenerated`,
  `TestDBOriginFrozen`, `TestDBFederatedStart` and `TestServeWaits` pass. Not reachable by an instance yet:
  `federated` is not in `modesBuilt`. On the CI-class runner (Xeon 2.3 GHz) `BenchmarkGenerateKey` is
  about 44 ms a key, `BenchmarkSealKey` 3.6 µs and `BenchmarkOpenKey` (open and parse) 0.12 ms.
  Open: the key generation time on the Pi (`go test -run xxx -bench GenerateKey -benchmem .` on the Pi, or
  the line "keys: made the key of the instance actor in N ms" in its log).
- [ ] **One guarded fetcher.** `fetcher.do` generalises `fetcher.get`: GET and POST, any host the
  policy allows for federation, the preview allowlist for link cards as before, redirects re-checked
  (to another origin only for media and WebFinger), caps on time, size and content type, the
  `Accept` header ActivityPub requires on a GET; only port 443, and the 6to4 and Teredo ranges
  refused (federation.md 7.5 and 7.8). Until FMD's domain policies exist the fetcher takes its list of
  refused hosts as an argument, which the tests fill. DoD: `TestFetcherRefusals` and
  `TestFetcherLookups` pass unchanged; `TestPublicIPReserved` gains the two ranges;
  `TestFederationFetcherRefusals` covers a redirect to a private address, to http, to a refused
  host, another port, a large body, a slow body, a wrong type, a mismatched id.
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
- [ ] **WebFinger and NodeInfo.** `/.well-known/webfinger`, `/.well-known/nodeinfo` and
  `/nodeinfo/2.1`, only in a federating mode and only for members who opted in (the `federates`
  column and its switch in Settings land here). Opting in with a name that ends with a dot or a dash
  shows a warning that Mastodon cannot mention it (federation.md, finding 11). DoD:
  `TestWebFinger` (every edge case in federation.md 5.3, including `rel` and the CORS header on
  errors), `TestNodeInfo` (the discovery JRD, every field the 2.1 schema requires, the content
  type, counts as the schema defines them); `TestDBLocalModeUnchanged` still green.
- [ ] **Actor documents and public pages.** `/users/{name}` and `/actor`, content negotiation with
  `Vary: Accept`, the small HTML pages for profiles and Public posts, the public picture URLs, the
  post audience (Public or This server) in the composer. A name is public from here on, so
  `retired_usernames` lands here: the name of a deleted account that had opted in is never given to
  anyone else. The actor's properties and context are those federation.md 5.5 checked against
  Mastodon's ActivityPub page (`indexable` false among them). DoD: `TestActorDocument` against a
  golden file, `TestContentNegotiation`,
  `TestDBPublicSurfaces` (every public route refuses This server posts and members who did not opt
  in), `TestDBRetiredNames` (refused at sign-up and at a rename); a screenshot of the public post
  page.
- [ ] **Outbox and collections.** A paged outbox of Create activities for Public posts; followers and
  following with public counts and pages only for the account itself; an empty featured collection.
  DoD: `TestOutboxPaging` (empty, one page, many pages, a deleted post, a This server post absent).
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
- [ ] **People elsewhere: search by handle.** A box that takes `@name@host` or a profile URL,
  resolves it by WebFinger (HTTPS only, the `self` link, the reverse lookup from the actor's
  `preferredUsername` and host: federation.md section 11), and shows the remote profile and the
  first page of its outbox; no full-text search; an allowance per member. DoD: `TestDBResolveHandle`
  against a stub serving the recorded fixtures (including a WebFinger redirect to another host and
  an actor whose reverse lookup does not match); a browser check with the stub.
