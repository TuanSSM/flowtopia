# Follows: documents written from documentation

Nothing in this folder was recorded from a running server. This sandbox cannot reach other fediverse
servers (docs/federation.md, section 13), and Mastodon's documentation prints no example of a Follow, an
Accept, a Reject or an Undo, so these were written by hand for FWR 3 from what the texts say:

- Mastodon's ActivityPub page (last updated 30 March 2026), "Supported activities for profiles": Follow;
  Accept and Reject "used to approve or deny Follow activities"; Undo "a previous Follow, Accept Follow,
  or Block". Its one printed activity for profiles, the Block of "Remote blocking (Block)"
  (`../shapes/mastodon-block.json`), gives the form used here: an id at the root of the server
  (`https://mastodon.example/<uuid>`), the actor and the object as plain ids.
- The Activity Vocabulary (W3C Recommendation), Accept, Reject and Undo: each embeds the activity it
  answers or takes back as its object, with that activity's own actor and object.
- ActivityPub 6.10 (an Undo and what it undoes have the same actor) and 7.2 (an id on the actor's own
  origin).

The hosts are the stub world's of the tests (`social.example` for the remote server, `here.example` for
this instance), not the documentation's. The Accept fixtures cover the three ways the design note (6.3) says
an Accept may name our Follow: embedded with the id we sent, embedded with an id of the other server's own
(`accept-unknown-id.json`) or with none (`accept-no-id.json`), and by id only (`accept-by-id.json`).

`follows_test.go` reads every file here (`TestDBFollowsAcross`). FWR 3's definition of done asks for the
recorded Mastodon Follow and Accept: those remain open until the owner's trial against a real server (FWR 9),
and go in a folder of their own that says they were recorded.

| File | Type | What it is |
| --- | --- | --- |
| `follow.json` | Follow | bo on social.example follows ana here |
| `accept-embedded.json` | Accept | bo accepts ana's Follow, embedded with the id ana sent |
| `accept-unknown-id.json` | Accept | the same, the Follow embedded under an id of bo's server's own |
| `accept-no-id.json` | Accept | the same, the embedded Follow with no id |
| `accept-by-id.json` | Accept | the same, the Follow named by the id ana sent alone |
| `reject-embedded.json` | Reject | bo rejects ana's Follow, embedded |
| `undo-follow.json` | Undo | bo takes back the Follow of `follow.json`, embedded |
| `undo-accept.json` | Undo | bo takes back the Accept of `accept-embedded.json`, embedded |
