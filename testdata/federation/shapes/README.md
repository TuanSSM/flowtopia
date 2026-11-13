# Shapes: documents written from documentation

Nothing in this folder was recorded from a running server. This sandbox cannot reach other
fediverse servers (docs/federation.md, section 13), so every file is an example printed in a
specification or in Mastodon's documentation, copied exactly as printed (the text of the example
block, with its own indentation and without the page's line numbers), or one of the design note's
own examples. They show the shapes the texts say a server sends; they are not evidence of what any
server sends today. The recorded fixtures of section 13 (`test/federation-record.sh`) go in their own
folders beside this one when someone who can reach those servers records them, and each says it was
recorded.

`apdecode_test.go` reads every file here (`TestDecodeFixtures` holds the table below) and the shape
table (`TestDecodeShapes`) takes several of its cases from them.

| File | Copied from | Read as | Outcome |
| --- | --- | --- | --- |
| `ap-ex1-actor.json` | W3C ActivityPub (Recommendation, 23 January 2018), 1 Overview, Example 1 | actor | read |
| `ap-ex3-create.json` | ActivityPub 1, Example 3 | activity, embedded Note | read |
| `ap-ex4-create-reply.json` | ActivityPub 1, Example 4 | activity | refused: the example's content string holds raw line breaks, which JSON does not allow (RFC 8259 7) |
| `ap-ex5-like.json` | ActivityPub 1, Example 5 | activity | read |
| `ap-ex6-create-public.json` | ActivityPub 1, Example 6 | activity, embedded Note | read, Public in `to` |
| `ap-ex7-like-embedded.json` | ActivityPub 3 Objects, Example 7 | activity | read; the embedded Note carries its own `@context` object and has another origin than the actor (the forged Note of that section) |
| `ap-ex9-actor.json` | ActivityPub 4.1, Example 9 | actor | read; `icon` is a list of one URL |
| `ap-ex15-note-no-id.json` | ActivityPub 6.2.1, Example 15 | Note | refused: no id (a client's object before its server assigns one) |
| `ap-ex16-create.json` | ActivityPub 6.2.1, Example 16 | activity, embedded Note | read; the Note's id is on example.com, the actor on example.net |
| `ap-ex17-tombstone.json` | ActivityPub 6.4, Example 17 | Tombstone | read |
| `mastodon-block.json` | Mastodon, ActivityPub page (last updated 30 March 2026), "Remote blocking (Block)" | activity | read; `to` is one string |
| `mastodon-flag.json` | same page, "Reporting profiles and posts (Flag)" | activity | read; `object` is a list of three ids |
| `mastodon-move.json` | same page, "Account migration (Move)" | activity | read, with `target` |
| `mastodon-question.json` | same page, "Polls", the first example | Note reader | read as kind Question; it has no `attributedTo` |
| `mastodon-vote.json` | same page, "Polls", the second example | activity, embedded Note | read; the ids carry fragments |
| `mastodon-public-key.json` | same page, "Public key" | actor | refused: no inbox (the page shows only the key); its `publicKey` read alone gives the PEM unchanged to `parsePublicKeyPEM` |
| `mastodon-suspended.json` | same page, "Suspended flag" | actor | read: suspended, no inbox |
| `mastodon-memorial.json` | same page, "Memorial flag" | actor | refused: no inbox and not suspended |
| `mastodon-hashtag.json` | same page, "Hashtags" | Note | read; one Hashtag |
| `mastodon-emoji.json` | same page, "Custom emojis" | Note | refused: the example's `@context` has a comma before its closing brace, which JSON does not allow |
| `mastodon-focal-point.json` | same page, "Focal points" | Note | read; `focalPoint`, a list Flowa does not read, is skipped |
| `as-core-ex1-context-string.json` | W3C Activity Streams 2.0 Core (Recommendation, 23 May 2017), 2.1.1, Example 1 | root object | `@context` as a string; refused as a Note: no id |
| `as-core-ex2-context-object.json` | AS2 Core 2.1.2, Example 2 | root object | `@context` as an object, extension members; refused as a Note: no id |
| `as-core-ex3-context-array.json` | AS2 Core 2.1.3, Example 3 | root object | `@context` as a list of a string and an object; refused as a Note: no id |
| `as-core-ex16-like-http.json` | AS2 Core 4.4, Example 16 | activity | refused: an `http` id |
| `as-core-ex20-collection-http.json` | AS2 Core 4.6.1, Example 20 | collection | refused: an `http` id |
| `flowa-5.5-actor.json` | docs/federation.md 5.5, the actor Flowa will serve | actor | read; its PEM is shortened in the note, so it is not a key |
| `flowa-6.5-create.json` | docs/federation.md 6.5, a Flowa post as a Note | activity, embedded Note | read; one Mention, one Hashtag, one picture |
