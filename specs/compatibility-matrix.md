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
| Populated shelf structure | Same | Three owner rows established `div.stars[data-rating]`, five semantic star links, review-edit links, editable date wrappers, core/custom shelf links, and filtered headings such as `My Books: Read (2)`; unfiltered, `read`, and empty `currently-reading` reads passed live | Pass for owner rows and filtered/empty shelf reads; populated pagination remains synthetic |
| Exact ISBN lookup | Same plus synthetic fixtures | Checksum normalization, exact ISBN-10/13 match, duplicate rejection, and bounded full scan are tested locally; live rows parse exact ISBNs, but one other edition does not render either ISBN | Positive `get` remains fail-closed because the unidentified row prevents proof of a unique full-library match |
| Verified rating mutation | Same | Owner `div.stars[data-rating]` and the full review textarea were observed without a write; live `rate` verified an idempotent 5, changed 5→4, freshly read rating/status/date/custom shelves/full review, restored 4→5, and a separate shelf read confirmed 5 | Pass for one exact-ISBN book with an empty review; live non-empty-review and post-click ambiguity cases remain pending |
| Status mutation | Same | The owner shelf chooser exposes one floating box with exact `li.visible.exclusive[alt]` controls for all three core statuses and marks the current one with `exclusive_chosen`. Waiting for the browser-initiated Goodreads XHR/fetch to finish before one fresh readback resolved the earlier optimistic-row race. Reversible live canaries verified `read`→`currently-reading`, `read`→`to-read`, `to-read`→`currently-reading`, and restoration; rating, review, finish date, and custom shelves matched on each successful `start` leg. The modern book-page shelf dialog exposes no alternative core-status controls. Returning to `read` creates a finish date and an additional reading session, which the canary removed and freshly verified during restoration. | Pass for `start` via the legacy owner shelf chooser with request completion plus mandatory readback. A transition to `read` must be implemented as the separate `finish` flow with intentional date semantics; `add` remains pending. |
| Finish-date clearing | Same | The ordinary review editor exposes per-session controls. Removing the single dated reading session, submitting the visible form, and freshly reading the exact shelf row restored an unset finish date while preserving status, rating, custom shelves, and full review. Multiple all-unset reading sessions parse as a semantic `null`; conflicting completed reread dates still fail closed. | Pass for clearing one canary-created finish date; setting a requested finish date remains pending. |
| Other mutations and export | Dedicated test account and independent flow evidence required | No add/review write, requested finish-date write, or export has been attempted | Pending |
| Rod-managed Chromium fallback | First-run download and compatibility experiment required | Installed-browser resolution is implemented; automatic download is disabled | Pending |
| macOS and Windows runtime checks | Browser-equipped host or CI runner required | Cross-platform compilation only | Pending |

## Manual account probe

`go run -tags liveprobe ./tools/liveprobe` remains a development-only manual
sign-in and profile-restart probe. It never reads or enters credentials. The
authenticated marker is now part of the adapter and has synthetic tests. The
current private shelf has a small set of test books. `tools/libraryprobe`
records owner-row and filtered-heading structure without printing book data;
`tools/ratingprobe <isbn>` checks the exact target's owner rating control and
review form without printing the book, review, account, or raw HTML.

Shelf reads return `review` only if a complete review is available; the current
table parser omits it. Missing dates are JSON `null`. `get` scans at most ten
shelf pages and returns an error if a page bound or a row without a rendered
ISBN prevents a unique exact match. It never reports an incomplete scan as
not-found.
