# Stage audit: the plan

An end-to-end audit of a stage copy of Flowa (own database) on 8 November 2026 found 46 items and
four structural questions. The full reports (map.md, happy.md, unhappy.md, security.md) hold the
repro steps and evidence; this plan says, for every item, what is wrong in the code, how it is fixed,
how the fix is tested, and in which pull request it ships. The controls the audit found holding stay
as they are: authorisation on every admin and moderation route, no IDOR, no stored XSS on any text
surface, no SSRF in link previews, the Origin check, sessions, the Host check and uploads.

Numbers and file:line references are the audit's, against `main` at the time of the audit. Each PR
below is one branch, one purpose, squash-merged with a Conventional Commits title, and lands with the
tests named. Order: PR 1 goes first and alone; PRs 2 to 4 are independent of each other; PRs 5 to 7
follow. The structural questions are not scheduled; they are written up for the product discussion.

## PR 1: root certificates in the image (High)

`fix(build): give the container its root certificates`

| # | Finding | Fix | Test |
| --- | --- | --- | --- |
| 1 HP-01 | The image is `FROM scratch` with the binary alone, so there are no root CAs and every link preview fetch fails with `x509: certificate signed by unknown authority`, on production too. | Import `golang.org/x/crypto/x509roots/fallback` in `main.go`: Go then uses the Mozilla bundle compiled into the binary when the system has none. No change to the image or the deploy, and the bundle is updated with the module (pinned in `go.mod`). At start, log whether there are roots, so a deploy shows `TLS roots: present` and a broken image shows `TLS roots: none`. (The plan first asked for a count; Go has no supported way to count a pool, and the deprecated one fails staticcheck.) | `TestRootsPresent` in `main_test.go` runs the test binary a second time with `SSL_CERT_FILE` and `SSL_CERT_DIR` pointed at an empty directory, which is the container's situation, and checks there that the pool is not empty and that the Let's Encrypt and Google roots in `testdata/` are trusted. It fails when the import is removed. `TestDBLinkCards` keeps using the stub; a manual check on stage: post a YouTube link and see the card. |

Why not copy `/etc/ssl/certs` from an alpine stage: the binary is built on the host and the image
is assembled by `deploy.sh`, so a multi-stage copy would add a pull of a second image to every
deploy on the Pi; the fallback package needs nothing at build or deploy time.

## PR 2: limits that count the right thing (Medium, server)

`fix(server): limits count attempts fairly and cannot lock people out`

| # | Finding | Fix | Test |
| --- | --- | --- | --- |
| 6 UN-05 | A failed sign-up (bad JSON, invalid name, name taken) uses up the 10 per hour per address. The form is `novalidate`. | Count after parsing and validation, and count a taken name at a tenth of a slot: the limiter gets `addWeight(key, w)` with the window's allowance in tenths. A validation failure costs nothing; a taken name costs 0.1; a created account costs 1. The name is looked up before the password is hashed, so a taken name costs no hashing either; two sign-ups racing for one name are settled by the unique index, and the loser pays the tenth. | `TestDBSignupLimits`: 10 invalid bodies then a valid sign-up succeeds; 10 taken names cost one slot; with those, the ninth account is the last and the next is refused, and another address is not affected. |
| 7 SEC-04 | 20 wrong passwords for a username, from any addresses, lock that account for 10 minutes, including an admin's, and a correct password from a clean address is refused too. | The per-account counter no longer refuses before checking the password. When it is over, the password is still verified (hashing bounded by the slots and by the per-address limits, which do refuse); a correct password signs in and resets the counter; a wrong one is refused as 429 and does not add to the account's count, though it still counts against its address. The per-address and per-address-and-username limits stay as they are: they protect the CPU and are per attacker. | `TestDBSigninLimits` gains: 20 wrong passwords from 13 addresses, then a wrong one from a new address is 429 and the right password from it succeeds; after that a wrong one is 401 again; an address that kept guessing is refused even with the right password. |
| 8 SEC-05 | Per-address keys use the whole IPv6 address, so one /64 has 2^64 keys. Production has AAAA records. | `clientIP` returns the address as before; a new `limitKey(ip)` masks IPv6 to its /64 (`netip.Prefix`) and is what every limiter and the sign-up counter key on. IPv4 stays whole. | `TestLimitKey`: two addresses in one /64 share a key, two /64s do not, IPv4 unchanged, IPv4-mapped IPv6 treated as IPv4. `TestDBLimitsBehindProxy` adds an IPv6 case. |
| 15 SEC-10 | At 20 000 keys a limiter refuses every new key, so a flood of addresses stops all sign-ups. | When full, sweep entries whose window has passed; if still full, evict the oldest entry rather than refuse. The cap becomes a memory bound, not a denial. The limiter keeps its keys in the order they were first seen, so finding the oldest is not a scan of 20 000 entries. | `TestLimiter` fills a limiter with a small cap, adds one more: accepted, the oldest gone, expired ones first. |
| 20 UN-06 | A post the server rejects (too long, bad tags) still counts against 30 per hour. | A request the server turns down costs a tenth of one that goes through, for every per-member allowance (posts, comments, likes, badges, reports, suggestions, bookmarks, reactions). `allowed` takes the whole slot before the handler runs and gives nine tenths back when the answer is an error. This differs from the first plan (count after the insert, rejected requests free) in two ways, both on purpose: taking the slot first means two requests at once cannot both slip under the limit, and a tenth instead of nothing means a script sending bad requests still runs out. A write is taken while the count is under the allowance, so a few tenths from typos never cost the last whole post. | `TestDBHardening` gains: 30 rejected posts, then a valid one succeeds; ten likes on a missing post and ten empty comments leave room for one of each, and then the limit holds. |
| 10 MAP-03 | `community` and `game` are UI-only flags: with them off, posting and badges still work through the API. | Wrap `socialRoutes` in `a.flagged("community", ...)` (posts, comments, likes, users, notifications; bookmarks follow their own flag) and achievements in `flagged("game", ...)`. Two things stay reachable with `community` off, because other pages need them: a member's picture (`GET /api/users/{id}/avatar`, used by the profile and admin pages) and badges, which then post no milestone to the timeline; the share window hides its "Post to the Flowa timeline" button. | `TestDBFlagsGateAPI`: flag off gives 404 on each of the 16 routes and writes nothing, a picture still loads, a badge is saved without a post; on gives the usual answer. `mod.py` already toggles flags; it checks the timeline endpoint with `community` off and on again. |
| 19 SEC-16 | The Origin check compares the host only. | Compare the scheme where it matters: a request that came over TLS (`viaHTTPS`: a direct TLS connection, or `X-Forwarded-Proto: https` from a trusted proxy) is refused when its Origin is an `http://` page. The other mismatch, an `https://` page writing to a request that looks like plain HTTP, is allowed: that is what a proxy or tunnel that ends TLS without sending `X-Forwarded-Proto` looks like, and refusing it would lock every member out of such a setup; a page served over HTTPS on the same host is also not the attacker this check is for. | `TestSameOrigin`: twelve cases with matching and mismatching hosts and schemes, direct and behind a trusted or untrusted proxy. |
| 18 SEC-15 | Nothing is logged for failed sign-ins or 429s; logs are capped at 2 x 1 MB. | One line per refused sign-in and per 429 (`refused: <what> from <address or /64>` or `by member <id>`), never a username or a password, at most one line per kind and key per minute, and at most 120 lines a minute in all, with one line counting what was left out, so a flood from many addresses cannot fill the log either. Raise the app's log cap to 5 x 5 MB in `compose.yml`. | `TestDBSigninLimits` captures the log and sees one line for a run of failures and one for the refusals after it, with no username or password in it; `TestDBSignupLimits` does the same for sign-ups. |

## PR 3: nothing lost on the client (Medium, UI and API)

`fix(ui): keep drafts and edits safe across sign-out and devices`

| # | Finding | Fix | Test |
| --- | --- | --- | --- |
| 2 UN-04 | Composer text, picture, tags, combo and links stay after sign-out; the next account in the tab can post them. | Everything the community pages hold belongs to one account (`social.owner`): `load` calls `sameOwner`, which puts every `social` field back to empty when the signed-in member changed, including the drafts, the link offer, the picker, the send keys and the tour replay. Answers that arrive after the change are dropped (`social.gen`). | `social.py`: type a draft and add a picture, sign out, sign in as the second user: the composer is empty. |
| 3 UN-01 | A re-render while Post is in flight re-enables the button; two posts land. Same for comments. | A `social.sending` set keyed by form; the button stays disabled and the form ignores submits while its key is in it. Each POST carries an `Idempotency-Key`: a random id per form and content, kept until the send succeeds, so sending the same draft again after a lost answer repeats the key and an edited draft gets a new one (the plan said "made when the draft starts", which would have replayed the old answer for edited text). The server keeps `(user, path, key)` for 10 minutes in memory and answers the first result again for a repeat, with `Idempotency-Replayed: true`. A post sent while the cursor rests in a comment box clears the composer directly, because the page is not redrawn there. | `TestDBIdempotentPost`: two posts with one key make one row. `social.py`: submit twice quickly (the second before the answer): one post; lose the answer and send again: one post. |
| 4 UN-02 | Profile save sends every field; the server writes every column, so a save on one device wipes a bio saved on another. | `PUT /api/me` only writes the fields, links and preferences present in the body, and takes `rev`: a short hash of the bio, links and preferences as the client last saw them (no `updated_at` column, so no migration). A stale `rev` is 409 with the current user. The About form then keeps what was typed, shows the other device's values in the fields that were not touched, and saves on top at the next press (in place of a separate "apply on top" step). The form sends only what changed. | `TestDBProfileRev`: two saves with the same `rev`, the second is 409; a partial body leaves other columns. `social.py`: a link changed elsewhere and a bio typed here both survive. |
| 5 UN-03 | A progress edit from a second device is dropped on 409 even when it touched a different skill. | On 409 the client merges three ways (`web/merge.js`): the copy both devices started from (kept per prop as the server last had it), this device's copy and the fresh one. Skills, sessions, combos, gates and settings merge per item; the same field changed on both sides keeps this device's value; events are the union, newest first, capped at 300. It then saves again. After three clashes in a row it loads the latest copy and says the last edit was not kept, as before. This replaces "re-apply the last local change", which lost every edit but the last when several were waiting. An answer that arrives after switching props now stays with the prop it was sent for. | `node test/merge.js` (29 cases). `combo.py`: edit two different skills from two sessions; both survive, and the next edit saves without a clash. |
| 28 UN-14 | No warning when leaving a page with a non-empty composer. | `beforeunload` while the composer holds text, a picture, tags, a combo or links, while a comment, reply or comment edit differs from what it started as, or while the About form has unsaved changes; in-app navigation keeps the draft (it already does). | `social.py` sends a cancelable `beforeunload` and reads whether it was held: with and without a draft, a half-written comment, an unsaved About form. |
| 21 UN-07 | Liking a deleted post: error toast, heart stays filled. | On a failed like, put the button back to its previous state and count; a second tap while the first is on its way is ignored. | `social.py`: delete a post in the other session, like it here: heart off, toast shown. |
| 27 UN-13 | Whitespace-only comment, reply or edit does nothing silently. | Trim; if empty, show "Write something first." and keep focus. | `social.py`: a comment, a reply and an edit of only spaces. |
| 23 UN-09 | Removing a link card also removes the URL from the text. | Attaching still moves the link out of the text into a chip (the card stands for it); removing the chip puts the link back at the end of the text when it fits in 2,000 characters, and it is offered again. The plan's "the text is untouched" would have shown each link twice in a post. | `links.py`: attach, remove, the link is back and offered. |
| 24 UN-10 | "Attach as a card" is offered for links the server will reject, and the error does not name the link. | Offer a card only for links `parseLink`'s client twin accepts (`web/links.js`, its own file so Node can check it; a link at the end of a sentence is tried without the punctuation; the same video written another way is not offered twice); the server's error names the link. `parseLink` itself now refuses a port in the address. | `TestParseLink` and `node test/links.js` read the same `test/links-fixtures.json`; `links.py` with three rejected links and the server's answer. |
| 25 UN-11 | A transparent PNG turns black; a GIF becomes a still, with no notice. | Draw pictures onto a white background before encoding, for posts and for profile pictures; when a GIF is chosen, say "Animated pictures are kept as a still." under the preview (in the toast for a profile picture). | `social.py` puts a transparent PNG on both file inputs and reads a pixel of the result; the GIF notice is asserted in both places. |

## PR 4: admin and moderation correctness (Medium)

`fix(admin): list every flag and make account changes deliberate`

| # | Finding | Fix | Test |
| --- | --- | --- | --- |
| 9 MAP-02 | Admin > Features lists 12 of 14 flags; `moderation` and `catalogEditor` are missing while the off-page text points admins there. | Add both to `FLAGS` in `web/app.js` with descriptions; `test/flags.js` checks the UI list equals `flagDefaults` (as `test/badges.js` does for badges) and runs in lint. | `node test/flags.js`; `mod.py` sees 15 switches (the server had 15 by the time this was built), the same keys `/api/me` returns. |
| 12 HP-02 | The Accounts list is cached for the session, so a suspended or muted user shows as Active until a full reload. | Fetch the list when the tab opens (any change of page drops the copy) and after every action on a user. | `mod.py`: suspend a user, the row reads Suspended at once; a mute made from another window shows when the tab is opened again. |
| 14 HP-05 | The role select applies on change with no confirmation. | A confirm step: choosing a role shows "Make @name a moderator?" with Apply and Cancel; Apply sends the change, Cancel or choosing the present role takes the question away. The list is offered only where the server would take the change. | `mod.py`: change a role, cancel, unchanged; apply, changed. |
| 13 HP-03 | "Add a skill" selects act on every change, so arrow keys and typeahead add the wrong skill. | The select no longer adds on `change`; an Add button next to it (or Enter in the select) adds the chosen skill and leaves the keyboard on the list. Two selects acted this way, the combo's and the practice log's; both changed. The role select is HP-05; the remaining `data-change` selects only filter or set a value that is saved with its form. | `combo.py`: arrow down three times adds nothing; Enter adds the third. |
| 35 HP-09 | A suspension end shows in UTC, a mute end in local time. | The server sends both as RFC 3339, in data and inside its messages (sign-in refusal, mute notice, notifications); the page formats every one with the same function (`fmtWhen`, `localTimes` in `web/account.js`). | `mod.py`: a mute's end reads the same in the accounts row, the muted notice and the bell; a suspension's end reads the same in the row and in the sign-in refusal. `TestDBAccounts` checks the message carries an RFC 3339 time. |
| 36 HP-10 | "Last active" becomes "Never" after a suspension, because suspending deletes sessions. | Keep `users.last_seen` (a column written at sign-in and with the hourly session touch) and show that, on the accounts page and in the dashboard's active counts. Migration 5 fills it from the sessions that exist. | `TestDBAccounts`: last active survives a suspension and the person still counts as active in 7 days. |
| 31 UN-17 | The report dialog pre-selects Spam. | No reason pre-selected; Send report disabled until one is chosen; the reasons are a named radio group. | `mod.py`. |
| 26 UN-12 | Display names of only zero-width or bidi control characters are accepted. | Strip format characters (Unicode category Cf) and the blank ones outside it (braille blank, Hangul fillers), and refuse a name without a letter or digit. Direction overrides, embeddings and isolates are refused outright; the plain direction marks are dropped. Two uses of format characters are kept because removing them changes what is drawn: a joiner between two letters or symbols (Persian and Indic spelling, family emoji) and the tags of the England, Scotland and Wales flags. The bio is cleaned the same way but may be all symbols, since it is not what tells one person from another. | `TestCleanName` with zero-width, RLO and ordinary names; `TestDBAccounts` through `PUT /api/me` and sign-up. |
| 30 UN-16 | "a instagram handle". | "an Instagram handle", with each service under its own name (`linkNames`). | `TestDBAccounts` message check. |
| 34 HP-08 | "View more comments" shows on a focused thread when every comment is visible. | On a post opened at one thread, show it only when `commentCount` exceeds the comments on the page that count toward it (not deleted, not hidden). | `social.py`: one thread, no button; a second thread, the button. |
| 17 SEC-12 | Any admin can demote and then delete any other admin. | An owner: `users.owner` (migration 5; the first admin is marked at start when nobody is, and the database allows one owner, who must be an admin). The owner cannot be demoted or deleted; only the owner changes an admin's role, and until an owner exists nobody does. Any admin may still promote. The owner hands ownership to another admin who is not suspended from Admin > Accounts, through `PUT /api/admin/users/{id}/owner`, which is audited and tells the new owner. | `TestDBRoles` gains the owner cases, the hand-over and its refusals; `mod.py` sees the Owner chip. |
| 16 SEC-11 | `password` is accepted; PBKDF2 uses 200 000 rounds where OWASP now says 600 000. | Refuse the 1 000 most used passwords of 8 characters or more (the UK NCSC list, Open Government Licence v3.0, in `main.go`, checked case-folded and trimmed) and the username itself, at sign-up, password change and admin reset; 600 000 rounds for new hashes, with the round count already stored per hash, and a rehash on the next successful sign-in for older ones. One hash on the Pi went from 0.57 s to 1.69 s; the two hash slots bound it. The Go suite takes about 3 minutes where it took 1.5. | `TestPasswordPolicy`; `TestDBAccounts` sees a rehash after a right sign-in, none after a wrong one. |

## PR 5: phones and reach (Low, UI)

`fix(ui): tap targets, phone layout and the landing page`

| # | Finding | Fix | Test |
| --- | --- | --- | --- |
| 29 UN-15, HP-12 | Comment like 13 x 17 px, Reply, Delete, Report about 35 x 17 px, demo controls 24 to 28 px. | Every tappable control at least 44 x 44 px on pointers that are coarse (`@media (pointer: coarse)`), by padding, not by changing the layout on desktop. | `pages.py` at phone width measures each control's box; a check fails under 44 px. |
| 37 HP-11 | The avatar editor overflows a phone by 4 px. | Swatch rows wrap and the stage has `max-width: 100%`. | `pages.py` phone screenshot of `#/avatar` and a `scrollWidth` check. |
| 38 HP-13, MAP-16 | The phone landing says "Search skills with Ctrl K" and shows no map or demo. | The hint reads "Search" with the search button on touch devices; the landing shows one demo (the signature move) and a map thumbnail. | `pages.py` phone screenshot and text check. |
| 32 HP-06 | Ctrl K searches the current prop only, without saying so. | The palette searches every prop the member has, with the prop as the tag; a hint line names the scope. | `combo.py` palette check. |
| 42 MAP-11 | Avatar onboarding shows two sets of confirm and exit buttons. | One set: Keep this avatar, Skip for now, in the card; the editor's own Save hidden during onboarding. | `avatar.py`. |
| 43 MAP-12 | The full sidebar shows but does nothing during the prop chooser. | The sidebar shows only the profile entry and Settings until the chooser is done. | `pages.py` first-run screenshot. |
| 22 UN-08 | The section tour survives Back and blocks the next page. | The tour closes on `hashchange` and remembers the section as seen. | `avatar.py` or a new tour check in `combo.py`. |

## PR 6: copy and the surface (Low)

`fix(ui): the server has no Pi, and the words match the pages`

| # | Finding | Fix | Test |
| --- | --- | --- | --- |
| 11 HP-04 | "Raspi home" links everyone to `<host>:8080`; copy says "Could not reach the Pi" and "everyone on this Pi". | The link becomes `FLOWA_HOME_URL` (an environment variable, empty by default: no link), shown to admins only. Copy: "Could not reach Flowa", "everyone on this Flowa". README documents the variable. | `TestDBStaticCSP` or a `/api/me` field check; `pages.py` sees no link by default. |
| 45 MAP-15 | Settings links "Open nunchaku roadmap" (an outside artifact) for every prop. | Replace with a link to the repository's `docs/roadmap.md` (and `docs/epics.md`), the same for every prop. | `pages.py` settings screenshot. |
| 44 MAP-14 | `docs/roadmap.md` still says "Now: RPL, then MOD" though all five epics shipped. | Done in this PR: the plan section says where things stand (all five shipped) and what is next (this audit plan, then the structure questions). | `test/docs.sh`. |
| 40 MAP-09 | Dashboard and Skill maps name both member pages and admin tabs; member versus User; quests versus phases. | Admin tabs become Overview and Catalogs; the admin list says Members throughout; Plan says phases everywhere, with "quests" only in the badge group name. | `pages.py` text checks. |
| 41 MAP-10 | Moderation and Admin are two staff areas and neither is in the command palette. | Both in the palette for those who may open them; one "Staff" group in the sidebar footer. | `mod.py` palette check. |
| 39 MAP-08 | For a visitor, three of five sidebar items are sign-in walls with the wrong words. | Visitors see Welcome, Skill maps and Sign in; the walls say "Sign in to use the timeline" and so on, one sentence each. | `pages.py` signed-out screenshot. |
| 46 MAP-17 | The combo card mixes logging (Clean run, Dropped) with Publish; view and speed controls look alike. | Logging buttons in a "Log a run" group under the player; Publish in the header; speed as a segmented control with a label. | `combo.py` and the screenshot. |
| 33 HP-07 | Two "Practice streak" values on the dashboard without saying which is which. | Label them "Nunchaku streak" and "All props"; the badge rule names the one it uses. | `pages.py` text check. |

## PR 7: the figure and the drawn body

Already in flight as `fix(figure): fit the body to the drawn model and keep props out of it`:
props drawn through the figure, glasses and eyes inside the face, and frames. It is listed here so
the audit's demo items (HP-12 demo controls) are not done twice: the control sizes are PR 5's.

## Structure: for the product discussion

These are not bugs and are not scheduled. Each has the options the audit's findings suggest.

- **MAP-01 The current prop is a hidden global mode**, changed from five places. Options: (a) the
  prop lives in the URL for every prop-scoped page (`#/nunchaku/practice`), so browsing another map
  never changes where Practice, Combos and Plan act; (b) keep the mode but show it in the header as a
  switch and make map browsing not change it. (a) is cleaner and is a day's work in `parseRoute`.
- **MAP-05 Level, streak and challenges repeat on up to five pages**; Plan's phases are below the fold
  on a phone. Option: one Progress card on the dashboard, a one-line summary elsewhere, and Plan
  starting with the current phase.
- **MAP-06 Account and settings are split over Settings, Profile, Avatar and the public profile.**
  Option: Profile holds who you are (name, bio, links, avatar, picture); Settings holds how Flowa
  behaves (theme, dominant hand, sharing, data); the public profile is a view, not an editor.
- **MAP-07 Five entry points to the maps; the PROPS tree dominates the sidebar** and the phone drawer
  is taller than the screen. Option: one Skill maps entry with the prop chosen on the page; the tree
  only for the current prop, collapsed by default on phones.

## Checking the whole

When PRs 1 to 6 have merged, the audit's four journeys run again on stage: every item above is
marked fixed in the audit report with the PR number, or reopened with what still fails.
