# FRD: Federation, read side

Why, scope and risks: [epics.md](../epics.md#frd-federation-read-side). Design:
[federation.md](../federation.md). Order: [roadmap.md](../roadmap.md#third-cycle).
Category: feature. Size: L. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** in progress (issue [#89](https://github.com/TuanSSM/flowtopia/issues/89)): the first item is
built behind the mode, which is not startable yet, and waits for its Pi measurement; the guarded fetcher is
done, and nothing calls its federation part yet; HTTP signatures are built, with no caller yet, and wait
for their Pi measurements; the owner's review of the design is still asked for. 1 of 8 items done. The design note was checked against the specifications and
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
  `TestDBOriginFrozen`, `TestDBFederatedStart`, `TestServeWaits` and `TestDBServeStopsWorker` pass. No
  key is made for anyone while federation is refused. Not reachable by an instance yet: `federated` is
  not in `modesBuilt`. On the CI-class runner (Xeon 2.3 GHz) `BenchmarkGenerateKey` is
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
  Built so far, with no caller yet: the decoder (federation.md 6.3 says what it reads and every rule
  chosen where the texts leave a choice; 7.5 the limits it holds). `apScan` checks a whole document in
  one pass before anything is decoded (well-formed JSON, UTF-8 without lone surrogates, 24 levels, 1,000
  entries in any list or object, no member name twice in any spelling); `oneOrMany` and `idOrObject`
  read every property that may be a list or a link; `decodeActor` (with `key`, chosen by id, fragment
  included), `decodeActivity`, `decodeNote`, `decodeTombstone` and `decodeCollection` read exactly what
  the note uses, and the same for an object embedded in another. Every refusal is a typed reason with the
  property's path. The 28 documents in `testdata/federation/shapes` are examples copied from ActivityPub,
  Activity Streams 2.0 Core, Mastodon's ActivityPub page and this note, none recorded, so the recorded
  fixtures, `TestFixturesParse` and the questions only a recording settles (alt text property, mention
  nesting) are still open. `TestDecodeShapes`, `TestDecodeFixtures`, `TestDecodeActor`, `TestDecodeNote`,
  `TestDecodeActivity`, `TestDecodeCollection`, `TestDecodeRefusals` and `TestDecodeAdversarial` pass with
  `-race -count=3`; `FuzzDecodeDocument` and `FuzzDecodeScan` ran 30 s each (the second found that the
  readers did not allow whitespace before the document, now fixed and kept as a corpus entry). On a 2.1 GHz
  Xeon `BenchmarkDecodeNote` (the Create of 6.5 with its Note) is about 45 µs, 15 KB and 194 allocations,
  `BenchmarkDecodeActor` (the actor of 5.5) about 30 µs, 11 KB and 143 allocations; the costly inputs of
  `BenchmarkDecodeAdversarial` take 13 to 18 times as long at 1 MB as at 64 KB, the slowest (1,000
  distinct escaped names in every object) about 40 ms and 23 MB for 1 MB, and a list, a nesting or a
  repeated name over the limit is refused in under 0.05 ms at either size.
- [ ] **People elsewhere: search by handle.** A box that takes `@name@host` or a profile URL,
  resolves it by WebFinger (HTTPS only, the `self` link, the reverse lookup from the actor's
  `preferredUsername` and host: federation.md section 11), and shows the remote profile and the
  first page of its outbox; no full-text search; an allowance per member. DoD: `TestDBResolveHandle`
  against a stub serving the recorded fixtures (including a WebFinger redirect to another host and
  an actor whose reverse lookup does not match); a browser check with the stub.
