# Remote posts, written from documentation

None of these was recorded from a real server: no session that builds Flowa can reach one (federation.md,
section 13; the owner's trial, FWR item 9, records real ones). Each is a Create of a post by
`https://social.example/users/bo`, written from the shapes Mastodon's ActivityPub page shows
(docs.joinmastodon.org/spec/activitypub/: "Polls", "Custom emojis", "Focal points", "Blurhash", "Sensitive
content", "Quote posts", and the h-card mentions of its "Mentions" section) and from Activity Streams 2.0
Vocabulary (Article, Question, Document, Image), read on 14 November 2026. `here.example` is the instance the
tests run as.

| File | Shape |
| --- | --- |
| `cw.json` | a Note with a content warning (`summary`) and `sensitive`, and one JPEG picture with a description and a `blurhash` |
| `poll.json` | a Question with `anyOf` choices, each a Note with a name and a `replies` count, `endTime` and `votersCount` |
| `attachments.json` | a PNG with `focalPoint` and `blurhash`, a WebP, an MP4 video, an MP3 sound, and an Image with no media type |
| `quote.json` | a quote post: `quote`, `quoteUri`, `_misskey_quote`, the `quote-inline` paragraph, and unknown fields (`interactionPolicy`, `memorial`) |
| `hashtags-emoji.json` | a Hashtag linked in the text, and a custom Emoji tag whose `:shortcode:` stays text |
| `article.json` | a long Article with a `name`, a heading, a quote and a list, and a `url` as a Link of `text/html` |
| `followers-only.json` | addressed to the author's followers collection and one member here, with its Mention |
| `direct.json` | addressed to one member here alone: a direct message, which Flowa drops |
| `mention.json` | a short `@anna` of the author's server, written as Mastodon links it, a mention of a member here, and a plain `@anna` |
