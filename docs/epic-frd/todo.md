# FRD: Federation, read side

Why, scope and risks: [epics.md](../epics.md#frd-federation-read-side). Design:
[federation.md](../federation.md). Order: [roadmap.md](../roadmap.md#third-cycle).
Category: feature. Size: L. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** not started (issue [#89](https://github.com/TuanSSM/flowtopia/issues/89)); waits for INS's mode setting and the owner's review of
the design. 0 of 8 items done. An item that relies on a field name, header set or time window from the specifications
first opens the text and corrects the design note where it was wrong (the note's Sources section
lists what could not be checked when it was written). Every setting, table, route and test named
below is new unless the design note says it exists.

- [ ] **Public origin, key secret and keys.** `FLOWA_ORIGIN` (https only; its host accepted by
  `allowedHost`), `FLOWA_KEY_SECRET` (64 hex characters), federation refused at start without them,
  with a message; `ap_keys` with RSA 2048 keys sealed by AES-256-GCM, generated in the background one
  at a time; the origin recorded at first federation and a later change refused (federation.md 4.3);
  `/nunchaku -rotate-keys`. DoD: `TestOrigin`, `TestSealKey` (wrong secret, tampered bytes),
  `TestDBKeysGenerated`, `TestDBOriginFrozen`; the key generation time on the Pi written here.
- [ ] **One guarded fetcher.** `fetcher.do` generalises `fetcher.get`: GET and POST, any host the
  policy allows for federation, the preview allowlist for link cards as before, redirects re-checked,
  caps on time, size and content type (federation.md 7.5 and 7.8). DoD: `TestFetcherRefusals` and
  `TestFetcherLookups` pass unchanged; `TestFederationFetcherRefusals` covers a redirect to a private
  address, to http, to a blocked domain, a large body, a slow body, a wrong type, a mismatched id.
- [ ] **HTTP signatures.** Signing and verifying draft-cavage-12 (`rsa-sha256`, `hs2019` accepted)
  and RFC 9421 (`rsa-v1_5-sha256`, `Content-Digest`), the digest checked, the date window, the replay
  map; standard library only. DoD: `TestSignatureVectors` against the vectors copied from both texts
  into `testdata/federation/vectors/` with their section numbers; `BenchmarkInboxVerify` and
  `BenchmarkSignDelivery` with `-benchmem` under the budgets in federation.md section 12;
  `TestReplayWindow`.
- [ ] **WebFinger and NodeInfo.** `/.well-known/webfinger`, `/.well-known/nodeinfo` and
  `/nodeinfo/2.1`, only in a federating mode and only for members who opted in (the `federates`
  column and its switch in Settings land here). DoD: `TestWebFinger` (every edge case in
  federation.md 5.3), `TestNodeInfo`; `TestDBLocalModeUnchanged` still green.
- [ ] **Actor documents and public pages.** `/users/{name}` and `/actor`, content negotiation with
  `Vary: Accept`, the small HTML pages for profiles and Public posts, the public picture URLs, the
  post audience (Public or This server) in the composer. DoD: `TestActorDocument` against a golden
  file, `TestContentNegotiation`, `TestDBPublicSurfaces` (every public route refuses This server
  posts and members who did not opt in); a screenshot of the public post page.
- [ ] **Outbox and collections.** A paged outbox of Create activities for Public posts; followers and
  following with public counts and pages only for the account itself; an empty featured collection.
  DoD: `TestOutboxPaging` (empty, one page, many pages, a deleted post, a This server post absent).
- [ ] **Remote actors by signed fetch.** Remote accounts as `users` rows with `domain`, and
  `ap_actors` (F5); fetches signed by the instance actor; expiry after 24 hours; a key refetched on a
  failed verification at most once in 10 minutes; negative caching; every surface that lists members
  limited to local ones. The recorded fixtures land here (federation.md section 13), each marked as
  recorded or as written from documentation. DoD: `TestDBRemoteActorCache`, `TestFixturesParse`,
  `TestDBRemoteRowsStayRemote`; `TestDBQueryPlans` holds with 2,000 remote accounts.
- [ ] **People elsewhere: search by handle.** A box that takes `@name@host` or a profile URL,
  resolves it by WebFinger, and shows the remote profile and the first page of its outbox; no
  full-text search; an allowance per member. DoD: `TestDBResolveHandle` against a stub serving the
  recorded fixtures; a browser check with the stub.
