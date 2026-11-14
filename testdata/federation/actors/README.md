# Actors: documents the actor cache is tested against, written from documentation

Nothing in this folder was recorded from a running server. This sandbox cannot reach other fediverse
servers (docs/federation.md, section 13), and the shapes folder beside this one holds only examples
copied exactly as printed. The documents here are composed from those examples and from the texts the
note cites, because the printed examples do not show what the actor cache needs: a whole actor with a
key, and a key kept at a path of its own. Each row says what it was written from. They show the shapes
the texts describe, not what any server sends today; a recorded fixture of section 13
(`test/federation-record.sh`) settles that, in its own folder.

`remote_test.go` reads every file here: `TestFixturesParse` through the decoder and the cache's choice
of a key and a name, `TestDBRemoteActorCache` and `TestDBRemoteFixtures` through the cache itself, each
served at its own id by a local stub.

| File | Written from | Read as | Outcome |
| --- | --- | --- | --- |
| `mastodon-actor.json` | Mastodon, ActivityPub page (last updated 30 March 2026), "Public key": the example's id and `publicKey` exactly, with `preferredUsername` taken from the id and an `inbox`, which ActivityPub 4.1 says an actor MUST have (the page's example shows only the key) | actor | read; the key is kept (RSA 2048, owner the actor, on its origin); the name is kept as written, `Gargron@mastodon.social` |
| `swicg-key-object.json` | SWICG report "ActivityPub and HTTP Signatures" (read 12 November 2026), "How to obtain a signature's public key", step 5 ("If it's a raw Key object, use its controller or owner property"), and docs/federation.md 7.3; the report prints no example, so the id is ActivityPub Example 1's host and the PEM is Mastodon's page's | key document | read as a key: its owner is fetched next |
| `swicg-key-owner.json` | ActivityPub 1, Example 1 (Alyssa), copied as the shapes folder has it, with `publicKey` naming the key above by id only (the report: "It may be a compacted id") | actor | read; it names the key back, so the key of the document above is kept for it |
