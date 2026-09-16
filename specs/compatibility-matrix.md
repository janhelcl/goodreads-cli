# Browser compatibility spike: current evidence

This matrix records observations, not a promise that Goodreads flows work. Never
commit an authenticated page, browser profile, screenshot, cookie, or personal
library/export data. The Goodreads test-account stages in `07-testing-and-delivery.md`
remain gates for login, reads, and mutations.

| Stage | Platform/browser | Evidence | Result |
|---|---|---|---|
| Dedicated profile launch and restart | Linux x86-64, Google Chrome 143.0.7499.40, Rod v0.116.2 | `GOODREADS_BROWSER_TESTS=1 go test ./internal/browser -v` against `httptest.Server` | Pass: persistent synthetic cookie survived a clean headless restart |
| Local CDP and origin policy | Same | launcher accepts only a loopback CDP URL; synthetic redirect to a different local origin is rejected | Pass |
| Cancellation and shutdown | Same | local Rod component test cancels the operation and waits for Chrome to exit | Pass |
| Browser product pinning | Same | test refuses a different Chromium product for an existing profile and clears the marker with profile deletion | Pass |
| Profile isolation and lock | Linux, `gofrs/flock` v0.13.0 | unit test uses a dedicated temporary profile, verifies permissions, cross-process lock refusal/release, repeat deletion, and symlink refusal | Pass |
| Headed launch, manual sign-in, and saved profile restart | Linux x86-64, Chrome 143.0.7499.40, visible display | User completed Goodreads sign-in in the dedicated profile; a headless restart reached the private library | Pass for this machine/account |
| Authentication detection | Same | Saved profile reached `/review/list/{account}` with exact `My Books` heading, `#books`, `#booksBody`, and sign-out link; a fresh profile was redirected to `/user/sign_in` without these markers; live `gr status --json` and existing-session `gr login --json` succeeded | Pass for saved-session detection; fresh interactive `gr login` remains untested |
| Empty private shelf | Same | Authenticated `#books` and `#booksBody` table had zero rows; live `gr library --json --limit 2` and `gr library --shelf currently-reading --json --limit 2` returned `[]` | Pass for empty shelves and shelf navigation |
| Populated shelf structure | Same, read-only public shelf observation plus invented local fixture | Public page showed `tr[id^='review_']`, book/author links, ISBN columns, rating stars, date columns, and `a.next_page`; public viewer shelf controls differ from owner controls | Partial: populated owner-row parser and pagination are unverified live |
| Exact ISBN lookup | Synthetic shelf fixtures and empty private shelf | Checksum normalization, exact ISBN-10/13 match, duplicate rejection, and bounded full scan are tested locally; live `gr get` returned not-found with exit code 4 on the empty shelf | Positive live `gr get` on a populated owner shelf pending |
| Verified mutations and export | Dedicated test account with a test book required | No Goodreads write has been attempted or inferred from the public shelf | Pending |
| Rod-managed Chromium fallback | First-run download and compatibility experiment required | Installed-browser resolution is implemented; automatic download is disabled | Pending |
| macOS and Windows runtime checks | Browser-equipped host or CI runner required | Cross-platform compilation only | Pending |

## Manual account probe

`go run -tags liveprobe ./tools/liveprobe` remains a development-only manual
sign-in and profile-restart probe. It never reads or enters credentials. The
authenticated marker is now part of the adapter and has synthetic tests. The
current private shelf contains no books, so populated owner-row parsing, exact
ISBN lookup, and Goodreads writes must wait for a suitable test book. Public
shelf markup is useful structural evidence but cannot validate owner controls.

Shelf reads return `review` only if a complete review is available; the current
table parser omits it. Missing dates are JSON `null`. `get` scans at most ten
shelf pages and returns an error if a page bound or a row without a rendered
ISBN prevents a unique exact match. It never reports an incomplete scan as
not-found.
