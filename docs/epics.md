# Epics

Each epic is a user outcome large enough to need several pull requests. This page says why each
one matters, what is in and out of scope, how success is measured, and what can go wrong. The
order and the reasons for it are in [roadmap.md](roadmap.md). The working checklist for each epic,
with a definition of done on every item, is in `docs/epic-<code>/todo.md`.

| Code | Epic | Size | Flag | Checklist |
| --- | --- | --- | --- | --- |
| RPL | Replies and threaded comments | S | `community` | [epic-rpl/todo.md](epic-rpl/todo.md) |
| MOD | Moderators, admin panels and the skill map editor | L | `moderation`, `catalogEditor` | [epic-mod/todo.md](epic-mod/todo.md) |
| LNK | Instagram, TikTok and YouTube links in posts | M | `linkCards`, `videoEmbeds` | [epic-lnk/todo.md](epic-lnk/todo.md) |
| GEO | Next-trick suggestions from the geometry of moves | L | `suggest` | [epic-geo/todo.md](epic-geo/todo.md) |
| AVT | Customisable avatars in demos, combos and stickers | L | `avatars` | [epic-avt/todo.md](epic-avt/todo.md) |

---

## RPL: Replies and threaded comments

**Problem.** Comments are a flat list. When two people answer each other under a post, the
exchange is lost among other comments, and the person being answered is told nothing unless they
were @mentioned by hand.

**Who it is for.** Anyone who comments: a learner asking how a move is done, the person who posted
answering, a friend cheering.

**Outcome.** A comment can be answered in place. Replies sit under the comment they answer, the
author of that comment is told, and long threads fold away so a post stays readable.

**In scope.**
- One level of threading, as Instagram and TikTok do it: replies to a comment sit under it; a
  reply to a reply joins the same thread and starts with an @mention of the person answered.
- Likes on comments and replies, and sorting a thread by time.
- Folding: the two newest replies are shown, the rest behind "View N more replies".
- A reply notification, and a deleted comment with replies kept as "Comment deleted" so the
  thread still reads.
- Comments loaded per post on demand, instead of every comment with every page of posts.

**Out of scope.** Nested trees deeper than one level, editing after 15 minutes, rich text,
reactions other than a like.

**Success.** Comments and replies per post rise; the share of comments that get an answer rises;
no page slows down: a post with 200 comments opens as fast as one with 2.

**Risks.** Loading comments lazily changes the feed's JSON; older clients still expect
`comments` on each post, so the field stays (with the first few) until every client is updated.

---

## MOD: Moderators, admin panels and the skill map editor

**Problem.** There is one admin and no way for anyone else to help keep the timeline healthy.
Nobody can report a post. Fixing a wrong prerequisite or a typo in a skill map needs a code
change and a redeploy. Achievements and stats are self-reported and nobody can correct them.

**Who it is for.** The admin who runs the Pi; trusted members who will moderate; everyone who
wants a timeline without spam or abuse; anyone who finds a mistake in a skill map.

**Outcome.** Three roles: user, moderator and admin, each with a clear set of powers. Users can
report posts, comments and people. Moderators work a queue, hide content with a reason, mute or
suspend accounts for a time, and every action is logged. Admins get a dashboard, account and role
management, and an editor for the skill maps that validates every change, shows its effect on
people's progress, and can be rolled back.

**In scope.**
- Roles and a permission table enforced on the server; the UI only hides what the server refuses.
- Reports with a reason, a moderation queue, hide and restore, mute and suspend with an end time,
  an audit log of every moderation and admin action, and notifications to the people affected.
- Admin dashboard: active users, sessions logged, posts and reports per week, from data already
  in Postgres.
- Skill map editor: catalogs moved into the database with revisions, drafts, a diff, publish,
  roll back; the same validation rules as `test/catalog.js`, implemented once in Go and run by
  both the server and CI; skill ids that are never reused, with renames kept as aliases.
- Moderator tools for self-reported data: revoke a badge, reset reported stats.

**Out of scope.** Automatic content filtering, appeals by email, per-prop moderators, editing the
demo engine's clips from the browser.

**Success.** Reports resolved within 48 hours; zero skill-map fixes that need a redeploy after the
editor ships; zero catalog changes that orphan progress.

**Risks.**
- **Two validators drifting.** The Go validator becomes the only one; `test/catalog.js` either
  calls it or is retired, and both are run against the same fixtures until then.
- **Orphaned progress.** Retiring or renaming a skill must never lose levels. The editor shows how
  many users have progress on a skill before it can be retired, and renames keep the old id.
- **Moderator overreach.** Every action is logged with who, what, why and when, and is visible
  to admins.

---

## LNK: Instagram, TikTok and YouTube links in posts

**Problem.** People film their practice on Instagram, TikTok and YouTube. Pasting a link into a
Flowa post gives a bare URL with no picture, so nobody clicks it.

**Who it is for.** Anyone who posts their videos elsewhere and wants their Flowa friends to see
them; anyone scrolling the timeline.

**Outcome.** Pasting a link to an Instagram post or reel, a TikTok video or a YouTube video or
short turns into a card with the thumbnail, title and author, without the visitor's browser
talking to those sites until they choose to. YouTube videos can play in place when the admin
allows it.

**In scope.**
- Recognising links from the three sites, normalising them to a canonical URL, and storing them on
  the post (up to 4 per post), alongside the trick and combo chips.
- Server-side previews: the Go server fetches the site's public oEmbed or page metadata from an
  allowlist of hosts, with short timeouts and size limits, and stores the title, author and a
  shrunk thumbnail itself. Visitors load the thumbnail from Flowa, so no request leaks to the site.
- A plain card when no preview is available (Instagram's oEmbed needs a Meta app token, so its
  cards may be title-less at first), with the site's name and the link.
- Click-to-play YouTube through `youtube-nocookie.com`, behind its own flag, which needs one
  `frame-src` entry in the CSP. Instagram and TikTok open in a new tab: their embeds need their
  own scripts, which the CSP does not allow and this epic will not add.
- Linking a post on another site from a profile's social link, for "my latest video".

**Out of scope.** Uploading video to Flowa, importing a whole feed, any login to the three sites.

**Success.** Share of posts with a link card; click-through on cards; zero requests to the three
sites from a visitor's browser unless they press play or open the link.

**Risks.**
- **Server-side request forgery.** Only fixed hosts and fixed endpoints are fetched, redirects are
  followed only within the allowlist, and responses are capped in size and time.
- **Sites change their markup or APIs.** Previews degrade to the plain card, never to an error.
- **CSP.** The one `frame-src` entry for YouTube is named in the PR and sits behind its own flag.

---

## GEO: Next-trick suggestions from the geometry of moves

**Problem.** Building a combo means knowing which trick can follow the last one: same plane or a
plane the hand can reach, same direction of spin or a clean reversal, the prop ending where the
next trick starts. Today the combo builder offers every skill in a long list, and people find out
what flows only by trying.

**Who it is for.** Anyone building a combo, from a beginner linking two figure-8s to someone
shaping a 90-second routine.

**Outcome.** The combo builder suggests the next trick, ranked by how well it flows out of the last
one and whether the user can do it, with a reason in plain words ("same wall plane, same direction,
the striker is already on the outside"). When nothing links directly, it suggests a bridge: the
shortest chain of known tricks that gets there.

**In scope.**
- **Measuring every clip at build time** (`sim/`): the path of each prop's tip, its length,
  speed, curvature and torsion computed with discrete differential geometry; a rotation-minimising
  (parallel transport) frame instead of the Frenet frame, which flips at inflections; the plane of
  motion as a best-fit normal on the sphere; the sense of rotation from angular momentum about that
  normal; the winding number per cycle; and the state at entry and exit: hand position, prop
  orientation in SO(3), angular velocity and grip.
- **Comparing moves**: geodesic angle between plane normals, the angle between orientations on
  SO(3), the change in angular velocity, the distance the hand must travel, and the shape of whole
  cycles with elastic shape analysis (square-root velocity functions), which compares curves
  independently of speed and starting point.
- **A transition score** combining these, with weights fitted at build time against the
  transitions people have marked Works, Needs bridge or Blocked, using only anonymous counts that
  users opt into sharing.
- **Skills without clips** (most of the 411) get structured fields in their catalog: plane,
  direction, hand, grip, entry and exit, maintained in MOD's editor, and marked unknown where no
  source says.
- **In the combo builder**: a ranked list of next tricks, the reason for each, a bridge when there
  is no direct link, and a way to say a suggestion was wrong.

**Out of scope.** Analysing the user's own videos, pose estimation from a camera, machine-learned
models that run in the browser.

**Success.** Offline, the score separates Works from Blocked transitions with an AUC of at least
0.75 before any suggestion is shown. In use, over a quarter of suggestions are added to a combo
and over 60% of those are later marked Works.

**Risks.**
- **Coverage.** Only 104 of 411 skills have clips. Suggestions rely on catalog fields for the rest,
  and say so when they do.
- **Too few labels** to fit weights. Until there are enough, fixed weights from the physics are
  used, and the offline check decides whether suggestions are shown at all.
- **Wrong confidence.** A suggestion with weak evidence says so, and "Blocked" feedback lowers it
  for everyone who opted in.

---

## AVT: Customisable avatars in demos, combos and stickers

**Problem.** Every demo is played by the same grey mannequin. People would rather see themselves:
their build, their style, their prop colours. Profiles and the streak overlay use a photo or
initials, which many people do not want to share.

**Who it is for.** Everyone; most of all people who do not want to post photos of themselves.

**Outcome.** Each user builds an avatar, as with Snapchat's Bitmoji: skin tone, hair, face, outfit,
accessories and prop colours, plus a body preset. Their avatar plays their demos and their combos,
appears on their profile and comments, and poses on stickers: the streak overlay, badge unlocks,
and milestone posts.

**In scope.**
- **An avatar editor** with live 3D preview: skin tones, hair styles and colours, eyes, brows and
  mouth from a set, tops, bottoms and shoes with colours, hats, glasses and wristbands, and prop
  colours per prop.
- **Modular parts** skinned to the same Mixamo skeleton the figure already uses, so a part is one
  more mesh and no animation changes. Flat materials and vertex colours, so no textures are needed.
- **Body presets** (for example short, average and tall, and lean or broad): each preset's bone
  lengths are baked through the demo engine and checked by `node test/physics.js` like any clip.
  Free sliders are not offered, because a body nobody has checked can put a prop through a leg.
- **Avatar in demos and the combo player**, falling back to the standard figure where WebGL is
  missing or the `avatars` flag is off.
- **Stickers**: the avatar rendered in a set of poses taken from the rig (celebrate, salute, the
  last frame of a trick) as a transparent PNG, for the streak overlay templates, badge unlock
  cards and the profile picture.
- **Storage**: a small validated JSON on the user (about 1 KB), public on the profile.

**Out of scope.** Photo-to-avatar generation, face scanning, paid items, user-made parts.

**Success.** Over half of active users customise an avatar within a month; demo pages are no
slower: a frame under 33 ms in Firefox on the Pi and under 16 ms on a mid-range phone.

**Risks.**
- **Performance.** The figure is skinned on the CPU (shader skinning drew nothing in software
  WebGL), so each extra mesh costs time on every frame. Parts are merged per avatar into one mesh
  when it is built, and a frame budget is checked in CI.
- **Licensing.** Parts must be our own or CC0. The X Bot model's licence is checked before parts
  are fitted to it; if it does not allow derivatives, a CC0 base is used instead.
- **Physics honesty.** A cosmetic part never changes the clip. Only checked body presets do.
