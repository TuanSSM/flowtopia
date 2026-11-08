# Security and tech debt: review and plan

A review of the server, the page scripts, the container and the deploy, on 8 November 2026, after
the five epics merged. Each finding was checked in the code; each item below says what changes and
how it is tested. Everything listed under "Done in this change" is in the same pull request.

## Security findings

| # | Finding | Severity | Plan |
| --- | --- | --- | --- |
| S1 | Sign-in and sign-up have no rate limit and no cap on concurrent password hashing (0.7 s of CPU each on the Pi), so random usernames or mass sign-ups can saturate the CPU of the host that also runs the printer. | Medium | At most 2 password hashes at once; sign-up limited per address; failed sign-ins counted per address, per account and per address and username; CPU, memory and process limits on the container. |
| S2 | Moderators can delete posts and comments by admins and other moderators, against the rule that nobody acts on their own role or higher. | Medium | Deleting someone else's post or comment needs the permission and a higher role; authors and post owners keep their rights. |
| S3 | An admin can reset another admin's password and sign in as them, or delete another admin; the reset endpoint also changes one's own password without the current one. | Medium | Refuse resetting or deleting another admin, and resetting one's own password there. Admins stay peers for roles, which is audited, as the moderation epic designed. |
| S4 | No clickjacking protection, and security headers only on static files. | Low | `frame-ancestors 'none'`, `X-Frame-Options: DENY`, `nosniff`, `Referrer-Policy`, `Cross-Origin-Opener-Policy` and a `Permissions-Policy` on every response, API included. |
| S5 | No per-account or per-address limit on failed sign-ins (password spraying); unlimited guesses at the current password when changing it; the failure map has no size cap and walks every entry under the global lock. | Low | Limiters with their own locks and a size cap; wrong current passwords counted per user. |
| S6 | Progress documents up to 5 MB each; backups hold the global lock while querying and writing, and the flag cache shares that lock, so every page load can wait on a backup; the people list returns every user with inline pictures; the open report queue has no limit. | Low | Progress up to 512 KB; separate locks for flags and backups; pictures served from their own URL; the people list and the queue limited. |
| S7 | `/api/achievements` ignores mutes and accepts invented badge keys, each creating a notification and a milestone post. | Low | Refuse muted members; accept only known badge keys (a test keeps the server's list equal to the page's). |
| S8 | No limits on posts, comments, likes or mentions; shared suggestion counts can be inflated by one member. | Low | Per-member allowances per hour for posts, comments, likes, badges and suggestion data; at most 10 people notified per mention. |
| S9 | Pictures are checked only by their data URL prefix; their bytes are stored as sent, metadata (GPS location) included. | Low | Decode, check dimensions, and re-encode every picture, which also drops metadata. |
| S10 | A commenter can keep reading and writing in a thread on a post a moderator hid. | Low | A hidden post's comments are visible only to the post's author and moderators. |
| S11 | The Host header is not checked, so DNS rebinding can reach the endpoints that need no sign-in from a website the user visits. | Hardening | Accept IP addresses, `localhost`, single-label and private names (`.local`, `.lan`, `.home.arpa`, `.internal`, `.ts.net`) and any listed in `FLOWA_HOSTS`; refuse other names with 421. |
| S12 | Sessions last 30 days whatever happens and can be ended everywhere only by changing the password. | Hardening | Sessions also end after 14 days unused; "Sign out other devices" on the profile. |
| S13 | Container: no CPU, memory or process limits; the admin password stays in the container environment. | Hardening | Limits in `compose.yml`; the README says to remove `ADMIN_PASSWORD` from `.env` once the admin exists. Binding to one address and pinning Postgres by digest are left to the owner (see "Left for later"). |
| S14 | Link previews: thumbnails decoded without a size check first; a few reserved address ranges not refused; concurrent feed loads can start the same preview fetch twice. | Hardening | Check dimensions before decoding; refuse the missing ranges; one fetch per link at a time. |

Checked and fine: parameterised SQL everywhere, PBKDF2 with per-user salts and constant-time
comparison, hashed session tokens in HttpOnly SameSite=Lax cookies, writes guarded by Origin and a
JSON content type, scoped progress and notifications, generic 500 responses, a strict CSP with
consistent escaping in the page, and a scratch container running as UID 1000 with a read-only
filesystem and no capabilities.

## Tech debt

| # | Debt | Plan |
| --- | --- | --- |
| D1 | The schema is two strings run in full at every start; there is no record of what ran, so a change that is not "add if missing" (a data fix, a rename) cannot be shipped safely. | Versioned migrations in `schema_migrations`, under an advisory lock, each in a transaction; the old schema is the baseline. |
| D2 | Expired sessions are deleted only at start; read notifications and unused link previews are never removed. | An hourly housekeeping job, tested. |
| D3 | The in-app backup covers progress documents only; accounts, posts, comments and catalogs have no backup. | `backup.sh`: a daily compressed `pg_dump`, checked with `pg_restore --list`, kept 14 days, with restore steps. |
| D4 | A deploy that comes up unhealthy leaves the app down; there is no way back. | `deploy.sh` keeps the running image as `nunchaku:previous`, goes back by itself when the new one is unhealthy, and has `--rollback`; it also runs the Go tests before building. |
| D5 | Code for importing the single-user files from before accounts existed still runs at every start, though both files were imported on 5 November. | Removed. |
| D6 | One global mutex covers unrelated state (sign-in failures, the flag cache, backups). | Each has its own lock. |
| D7 | Shell scripts are not linted. | `shellcheck` in the lint job. |
| D8 | Every browser test run left its Firefox profile (about 100 MB) in `/tmp`. Found when the Pi's disk filled up during this work: 140 profiles, 8.5 GB. | `test/browser/h.py` removes the profile when the suite ends, however it ends. |

## Left for later, with the reason

- **Publishing the port on one address, TLS, and pinning Postgres by digest.** These depend on the
  owner's network (LAN address, a reverse proxy, how updates are taken) and change how the app is
  reached; the README describes the options.
- **Moving progress from client-owned documents to server-checked state.** Stats and badges are
  reported by the browser by design; the limits above bound the damage, and changing the model is
  an epic of its own.
- **Splitting `main.go`.** The repository keeps one file on purpose (`.claude/rules/go-server.md`).
- **Browser tests that wait with fixed sleeps.** They pass, and the flaky step found during the
  epics now waits for its condition; replacing every sleep is a separate cleanup.
