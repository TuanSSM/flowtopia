# LNK: Instagram, TikTok and YouTube links in posts

Why, scope and risks: [epics.md](../epics.md#lnk-instagram-tiktok-and-youtube-links-in-posts).
Order: [roadmap.md](../roadmap.md); starts after MOD's reports and hiding (M2, M3) are live. Each
milestone is one commit in the epic's pull request ([implementation plan](../implementation-plan.md)). An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** done. 15 of 15 items done, in the LNK pull request; the MOD prerequisite shipped first.

## Already in place

- [x] Links in post text are made clickable and open in a new tab with `noopener noreferrer`.
- [x] Profile social links stored as handles, with URL normalisation (`cleanLink`).
- [x] Posts carry structured `data` (trick chips, combo), so links can join it.
- [x] Reports and hiding from MOD M2 and M3 (prerequisite, tracked there).

## M1. Recognise and store links

- [x] **Parser.** A Go function turns a pasted URL into `{site, kind, id, canonical}` for Instagram
  posts and reels, TikTok videos (including `vm.tiktok.com` short links, resolved later) and YouTube
  videos, shorts and `youtu.be` links. Tracking parameters are dropped.
  DoD: `TestParseLink`: 31 real-shaped URLs, including mobile hosts, trailing slashes, `?si=` and
  `?igsh=` parameters, and 14 that must be refused.
- [x] **Stored on the post.** `POST /api/posts` accepts `links` (up to 4); the server parses each and
  stores the canonical form in `data.links`. A post may be links alone.
  DoD: Go test for limits and refusals; the stored URL is canonical even when a tracking URL was sent.
- [x] **Composer.** Pasting a supported URL into the text box offers "Attach as a card"; attached
  links show as removable chips beside the trick chips, with the site's icon.
  DoD: `links.py` pastes a YouTube URL, is offered a card, attaches it, posts, and sees the card.

## M2. Previews fetched by the server

- [x] **Fetcher.** A preview fetcher with an allowlist of hosts and endpoints (YouTube oEmbed,
  TikTok oEmbed, Instagram page metadata), 3 s timeout, 256 KB response cap, redirects only within
  the allowlist, no cookies, one fetch at a time per host.
  DoD: `TestFetcherRefusals` with an `httptest` stub: timeout, oversize, redirect off the allowlist,
  hosts off the allowlist, plain http, and private, loopback and link-local addresses refused at connect.
- [x] **Stored previews.** `link_previews` table keyed by canonical URL: title, author, thumbnail
  (shrunk to at most 640 px and 60 KB, re-encoded as JPEG by the server), fetched time, status.
  Previews are fetched once when a post is created and refreshed at most weekly.
  DoD: Go test that a second post with the same link makes no new fetch.
- [x] **Short links.** `vm.tiktok.com` and similar are resolved by following redirects within the
  allowlist before parsing.
  DoD: Go test with a stub redirect chain.
- [x] **Graceful failure.** A failed or empty preview becomes a plain card (site name, icon, link);
  it never blocks posting.
  DoD: Go test that a post with a link whose preview times out is created at once.

## M3. Cards on the timeline

- [x] **Card.** Thumbnail served by Flowa (`/api/previews/{id}.jpg`), title, author and site, the
  whole card linking out in a new tab.
  DoD: `links.py` checks the card's link (canonical, new tab, `noopener noreferrer`) and that the image
  URL is on Flowa's own origin.
- [x] **No leaks.** A visitor's browser makes no request to Instagram, TikTok or YouTube until they
  press play or open a link.
  DoD: browser test records every request while scrolling a timeline with cards from all three
  sites and finds none to their hosts.
- [x] **Phone layout.** Cards are full-width at 420 px, thumbnails keep their aspect ratio (9:16 for
  shorts and reels, 16:9 for videos).
  DoD: `pages.py` adds "Timeline with link cards, phone"; reviewed in the PR.

## M4. Play YouTube in place (optional, own flag)

- [x] **Click to play.** Pressing play on a YouTube card swaps it for an iframe from
  `https://www.youtube-nocookie.com/embed/<id>?autoplay=1`, with `referrerpolicy="no-referrer"`.
  DoD: browser test presses play and finds the iframe; before pressing, no iframe exists.
- [x] **CSP.** `frame-src https://www.youtube-nocookie.com` is added only when the `videoEmbeds` flag
  is on; nothing else in the policy changes.
  DoD: Go test that the header differs only by that directive between flag states; the PR states
  the change and why.

## M5. Moderation and finish

- [x] **Reportable.** Link cards can be reported and hidden like any post content; a hidden link's
  preview is no longer served.
  DoD: `links.py` and `TestDBLinkCards` hide a post with a card and the preview URL returns 404 to others.
- [x] **Admin can turn it off.** `linkCards` flag (off: links stay plain text, no fetching) and
  `videoEmbeds` flag (off by default).
  DoD: with `linkCards` off, posting a link makes no outbound request (fetcher not called, checked
  in a Go test).
- [x] **Docs.** README community section, Community API, and the CSP note in the security
  paragraph; this file shows every item checked.
  DoD: `test/docs.sh` passes.
