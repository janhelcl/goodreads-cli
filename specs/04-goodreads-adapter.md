# Goodreads browser-adapter specification

This is the highest-risk boundary. Implement it narrowly, visibly, and defensively.

## Allowed integration surface

The product uses browser automation to drive Goodreads' ordinary web UI for:

- authentication-state validation;
- shelf/library reads;
- exact-ISBN book resolution;
- add/start/finish/rate/review mutations;
- explicit CSV export requests and downloads.

The initial Go implementation uses Rod with Chromium DevTools Protocol. Rod MUST remain behind the local browser boundary described in `02-architecture.md`.

The adapter MUST NOT:

- call a reverse-engineered private Goodreads JSON/API endpoint;
- emulate internal AJAX calls outside the page as an alternative client;
- attach to the user's ordinary browser profile;
- enter passwords or provider credentials;
- bypass CAPTCHAs, challenges, rate limits, or anti-automation controls;
- become a general Goodreads crawler, recommendation engine, or metadata API;
- execute arbitrary caller-supplied selectors or JavaScript.

Normal page resource requests initiated by Chromium are expected. Reading the rendered DOM, accessible tree, navigation result, and browser-managed downloads is allowed.

## Browser and profile model

Use one dedicated persistent Chromium user-data directory per local account.

- `login` launches it headed.
- Ordinary operations launch it headlessly by default.
- `--headed` uses the same flows and profile with a visible window.
- All commands serialize access with the per-profile lock.
- The adapter closes every browser it starts.
- The profile persists until explicit logout/reset.
- Cookies stay browser-managed; do not copy them into a separate Go HTTP cookie jar.

Initial browser resolution order:

1. explicit configured executable, if supported;
2. a compatible installed Chrome, Chromium, or Edge;
3. a Rod-managed Chromium, if the packaging/first-run experiment confirms an acceptable user experience.

Record the selected product/version in redacted debug logs. Do not silently switch between materially different profile formats if doing so risks corruption. A timeout or cancellation while locating or launching the browser stays a timeout or cancellation; it is not remapped to browser-unavailable or launch-failure.

## Browser-assisted authentication

Authentication is interactive and user-controlled.

Conceptual algorithm:

1. acquire the profile lock;
2. launch the dedicated profile in headed mode;
3. navigate to the current Goodreads sign-in page;
4. tell the user to complete sign-in in the browser;
5. wait for a known authenticated Goodreads state;
6. open a private library page and confirm account access;
7. close the browser, retaining the profile;
8. report success.

Requirements:

- MUST NOT inspect or capture password-field contents;
- MUST NOT type or submit credentials;
- MUST NOT automate provider/MFA challenge choices;
- MUST NOT read an existing personal browser profile;
- MUST allow the user to interact with any login flow Goodreads presents;
- MUST use a bounded, cancellable login timeout;
- MUST distinguish cancellation, timeout, browser exit, and compatibility drift;
- MUST leave no half-created success marker outside the browser profile.

A CAPTCHA or provider challenge is completed manually in the headed window. The application never attempts to defeat it.

## Authenticated-state detection

Do not infer authentication from one cookie name.

Validation SHOULD combine:

- final origin/URL is an expected Goodreads page rather than sign-in;
- a stable authenticated navigation/account marker is present;
- a private library page loads without redirecting to login.

If markers disagree, return `ErrCompatibility` or `ErrSessionExpired`; do not guess.

## Page contracts

Each flow has an explicit contract containing:

- allowed starting URLs/origins;
- page-identity assertions;
- selector alternatives in preference order;
- navigation or completion conditions;
- parser expectations;
- safe diagnostic stage names.

Prefer selectors in this order:

1. accessible role and name;
2. associated label and semantic form attribute;
3. human-visible text scoped to a stable region;
4. narrow CSS selector based on stable IDs/data attributes;
5. DOM structure only when covered by fixtures and no semantic option exists.

Never use generated CSS class names, global text matches, positional `:nth-child` selectors, or fixed sleeps when a condition can be awaited.

Selector alternatives are intentional compatibility branches and must be tested. They are not an excuse to click the first loosely matching element.

## Waiting and navigation

Every operation has a total deadline.

Use event/condition waits for:

- a parseable document and expected page identity;
- element actionable state;
- network/navigation completion when relevant;
- visible confirmation;
- changed Goodreads state.

`NewPage` waits until the location is no longer `about:blank` and `document.body`
exists. It does not wait for `window.onload` or `document.readyState === "complete"`.
A hanging image, third-party request, or unfinished HTML stream must not consume
the operation deadline. Callers then assert page identity from the rendered DOM.
`Status` and shelf reads wait a bounded time for the private-library table markers
after that body exists; a brief first paint without `#books` is not compatibility
drift.

Do not use unbounded waits. Short bounded settling delays MAY be used only when documented and paired with a real state condition.

Unexpected cross-origin navigation must stop the flow unless it is an allowed authentication provider during interactive login.

## Live library reads

The adapter reads shelf/library pages loaded in this invocation.

List reads navigate to the requested shelf (or the unfiltered library) and
assert authenticated table identity there, including an empty `#booksBody`.
They do not first load the unfiltered library solely to check the session.

For each page:

1. assert authenticated page identity;
2. parse only recognized book rows/cards;
3. normalize book ID, title, author, ISBNs, rating, exclusive shelf, dates, custom shelves, and review when available;
4. follow pagination only as needed for the caller's filter/limit;
5. detect repeated pages or pagination loops;
6. distinguish actual shelf termination from safety-budget exhaustion.

Missing optional data is represented explicitly. Missing data required for the operation is a compatibility error.

For list operations, the caller's result limit bounds pagination. Exact-identity operations use a separate resolution strategy and MUST NOT inherit a small list-oriented page limit. The current exact scanner requests the validated `per_page=100` rendered shelf size and still scans to actual termination to detect duplicates. Exhausting a documented page/request budget returns `ErrScanIncomplete`; it is not not-found and is not selector drift.

The adapter must not retain parsed books after returning. Browser HTTP cache is acceptable runtime behavior; application-level result caching is not.

## Exact ISBN resolution

Mutations are ISBN-first.

Resolution may use Goodreads' visible search/navigation UI or ISBN information on a resolved book page. The adapter must:

1. normalize and validate the input;
2. obtain one candidate through the UI;
3. inspect the candidate's edition identifiers when Goodreads renders them;
4. require an exact ISBN-10 or ISBN-13 match;
5. reject ambiguous, missing, or mismatched candidates.

A title/author match alone is never sufficient. If Goodreads hides ISBNs needed for proof, record the alternative stable identity contract in the compatibility matrix before implementation.

Public ISBN identity for `get` proves one book ID from the visible search result and book-page metadata. It MUST NOT wait for the Want-to-Read add control: already-owned book pages replace that control with a shelf-status action. Add still waits for Want to Read before clicking it. After the public book ID is proved, match it against the owner rows already scanned for that call; do not load the library again. A completed search page with no book route is `book_not_found`; do not wait for a `/book/show/` link that will never appear. A public-lookup timeout or cancellation stays a timeout or cancellation; it is not remapped to `library.row`.

Before changing the existing full-shelf scan, run a focused compatibility experiment in this order:

1. test whether the visible owner-library UI can filter/search by exact ISBN and prove the returned owner row;
2. test whether a visible exact-ISBN book route can prove one stable Goodreads edition ID and whether the owner library can then be located by that ID;
3. otherwise determine the largest proven shelf page size and paginate to actual termination under the operation context.

The chosen path must share one implementation across `get` and all mutation commands. It must test absent, present, duplicate, unidentified-row, pagination-loop, and safety-budget cases. No undocumented endpoint or hidden CSV snapshot may substitute for the visible UI.

## Current-state capture

Before a mutation, capture the target's relevant state from Goodreads.

At minimum preserve/compare:

- exclusive shelf/status;
- rating;
- review when the operation can affect it;
- finish date when the operation can affect it;
- custom shelves when the UI flow may affect them.

If the UI cannot expose a field needed to prove preservation, the compatibility spike must determine whether the operation is safe. Do not assume.

## Mutation contract

A mutation uses the narrowest tested UI path.

Common requirements:

- assert the expected book and current state before clicking;
- perform one semantic action;
- scope elements to the target book/page;
- wait for an explicit completion marker or changed state;
- reload or revisit the authoritative Goodreads view;
- parse the resulting fields;
- compare requested changes and preservation invariants;
- return `Verified=true` only after a match.

The terminating resolution scan may supply an in-memory row candidate to
later steps in the same command. Fresh verification first revisits that known
owner page and requires the same row ID, book ID, and exact ISBN. If the row
moved or disappeared, verification falls back to the full exact resolver.
Candidate state is never persisted or reused across commands.

A click or form submission is not success. An HTTP 2xx observed inside the browser is not success. A toast may be one completion signal but is not readback verification.

### Add / status change

The spike must identify the most stable Goodreads control for selecting `to-read`, `currently-reading`, or `read`.

For an existing book, the adapter treats add as ensure-status and preserves unrelated fields. For a new book, it verifies the resulting library entry and exact ISBN.

Session proof is the owner-library scan, not a prior unfiltered `Status` page load.
Open the owner shelf chooser through the row control and retry that open click
until the floating exclusive-option contract holds, with a bounded wait. Those
interaction waits MUST NOT inherit the remaining command deadline as an
unbounded element wait.

Returning an edition to `read` may create a dated reading session. `add --shelf
read` MUST restore an unset finish date by removing that extra session after
ensuring another session row remains, then verify the shelf date is again unset.

### Rating

Select the exact numeric rating through the owner-shelf star control. Session
proof is the owner-library scan, not a prior unfiltered `Status` page load.
Wait until the scoped star control is present, click it, and wait for its
browser-initiated request before one fresh readback. Those interaction waits
are bounded; they MUST NOT inherit the remaining command deadline as a Rod
element wait. Verify the numeric value after reload. Do not infer success from
star hover/visual classes alone unless the parser contract proves them stable.

### Review

Use the ordinary review editor. Preserve the full supplied Unicode text and verify the saved value after reload. Clearing must be explicit.

Never include review text in logs, screenshots by default, or error messages.

### Finish date

Use the supported Goodreads review/edit-reading-activity UI established by the spike. Verify the resulting calendar date after reload. If Goodreads cannot reliably represent or expose it, `finish --date` remains unsupported rather than partially succeeding.

## Idempotency and retries

Reads and navigation MAY retry conservative transient failures when no mutation has been attempted.

After a mutating click/submission:

- do not automatically replay it after timeout or ambiguous navigation;
- first perform a fresh readback;
- if state matches, return verified success;
- if state does not match and completion is unknown, return `ErrMutationAmbiguous`;
- allow a new user invocation to act idempotently from observed state.

Already-satisfied desired state returns verified success without an unnecessary click when preservation checks pass.

For compound mutations, a later failure after an earlier verified write triggers one final readback. If safe state is obtained, return `ErrPartialMutation` with completed steps, the failed step, and the non-sensitive observed fields. If even the final readback is inconclusive, return `ErrMutationAmbiguous`. Never compensate with automatic rollback or replay.

## Verification

Verification is mandatory in v0.1.

Compare only the requested fields plus explicitly identified preservation invariants. Goodreads-owned metadata changes do not fail verification.

A structured verification failure should include safe field names and expected/observed non-sensitive values. Review mismatches report lengths/hashes or a generic mismatch, not the private text.

## Export workflow

`gr export` drives Goodreads' official import/export page through the browser.

Conceptual flow:

1. open the import/export page and assert authentication;
2. trigger a new export;
3. wait with bounded polling for generation;
4. identify the download produced for this invocation;
5. download through the browser;
6. validate that the file is CSV with recognizable Goodreads headers;
7. atomically place it at the requested destination or stream it as specified by the CLI.

Do not use an old export link unless freshness can be proven. Export CSV is a user artifact, not the read path for `library` or the write path for mutations.

CSV import is not part of the initial production adapter.

## Downloads and files

Use a private temporary download directory per operation.

- randomized path;
- restrictive permissions where possible;
- reject unexpected filenames/types;
- apply a conservative size limit;
- clean up on success and ordinary failure;
- move to the requested destination only after validation;
- refuse overwrite unless the application authorized `--force`.

## Compatibility failures

Keep stage names stable enough for issue reports, for example:

- `auth.page`
- `auth.private-library`
- `library.page`
- `library.row`
- `book.resolve`
- `mutation.status`
- `mutation.rating`
- `mutation.review`
- `mutation.finish-date`
- `mutation.verify`
- `export.generate`
- `export.download`

When Goodreads changes:

- return `ErrCompatibility` with stage and safe context;
- suggest `--headed --debug`;
- never dump authenticated HTML automatically;
- update fixtures/contracts before changing production selectors;
- never fall back to a private endpoint or broad scraper.

## Politeness and bounds

This tool is for low-volume personal actions.

- no background polling except bounded waits for an explicit command;
- no scheduled sync;
- no bulk crawling;
- cap pagination and retries;
- use conservative backoff for export generation;
- honor visible service errors and retry guidance;
- avoid loading pages unrelated to the requested action;
- keep live-test activity serial and small.
