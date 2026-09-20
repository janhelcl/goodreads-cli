# Testing, compatibility spike, and delivery plan

## Rule zero: prove Goodreads behavior first

Goodreads UI behavior is not a stable public contract. Before implementing the polished command surface, run a narrow, documented compatibility spike with the Go browser stack.

Do not infer selectors or mutation success from third-party examples alone. Do not fall back to a private API or CSV import when a UI flow fails.

## Compatibility spike

Use a dedicated/non-critical Goodreads test account and a small set of known books when possible.

Never commit real profiles, cookies, authenticated HTML, screenshots containing private data, or personal CSV exports. Convert observations into sanitized synthetic fixtures and a compatibility matrix.

### A. Browser/runtime viability

Validate on each initially supported OS/browser combination where practical:

- Rod can launch installed Chrome, Chromium, or Edge with a dedicated profile;
- Rod-managed Chromium fallback behavior and first-run download, if enabled;
- headed login and headless subsequent launch use the same profile safely;
- the profile persists authentication after browser/process restart;
- CDP/debugging is loopback-only or process-local;
- cancellation and timeout close the browser;
- the per-profile OS lock prevents concurrent launches;
- the user's ordinary browser profile is never touched;
- `--headed` runs the same operational flow as headless mode.

Record browser product/version and platform in the compatibility matrix.

### B. Interactive login and session detection

Validate:

- the user can complete the current Goodreads login manually;
- provider/MFA/CAPTCHA flows remain entirely user-controlled;
- authenticated state can be detected without reading credential fields;
- a private shelf/library page confirms the session;
- expired sessions are distinguished from selector drift;
- `status` works after a new process launches the saved profile;
- `logout` removes only the dedicated profile;
- no password, cookie, or profile contents enter logs.

If the dedicated profile cannot retain a usable Goodreads session, stop and report the blocker. Do not collect passwords or copy cookies from another profile.

### C. Live shelf reads

Determine and record:

- current library/shelf URLs and page identity markers;
- row/card structure and stable semantic selectors;
- pagination behavior and termination;
- where book ID, title, author, ISBN, rating, status, custom shelves, dates, and review are visible;
- which fields require visiting a detail/review page;
- empty shelf and signed-out behavior;
- any lazy loading or localization constraints.

Create sanitized HTML fixtures for the minimum supported variants.

### D. Exact ISBN resolution

Validate:

- ISBN-10 and ISBN-13 inputs;
- visible Goodreads search/navigation path for an exact ISBN;
- proof that the selected edition matches the normalized ISBN;
- a book already in the library;
- a book not yet in the library;
- invalid/unresolvable ISBN;
- multiple editions or misleading title matches;
- a book whose Goodreads page does not expose an ISBN.

If exact identity cannot be proven reliably, do not ship that mutation path.

### E. First verified mutation — blocking gate

Choose the smallest reversible/idempotent action, initially `rate` on a test book.

Validate:

1. capture the current value;
2. set a different rating through the UI;
3. observe a completion marker;
4. reload/revisit the authoritative view;
5. parse and match the new rating;
6. confirm status, review, date, and custom shelves are unchanged;
7. repeat the same desired rating and confirm idempotency;
8. restore the original value when safe.

If the adapter cannot prove both the requested change and the required preservation invariants, stop before implementing more mutations.

### F. Core mutation flows

Validate independently:

- add a new ISBN to `to-read`;
- ensure/change status to `currently-reading`;
- change `currently-reading` to `read`;
- set and verify finish date;
- set rating;
- set, replace, and explicitly clear review;
- preserve unrelated rating/review/date/custom shelves;
- already-satisfied desired state;
- book not found and edition mismatch.

After every action, perform a fresh readback. Document exact page identity, control contract, completion markers, and verification source.

### G. Ambiguous and failure behavior

Exercise safe failure cases:

- browser unavailable;
- browser launch failure;
- profile already locked;
- login cancelled/timed out;
- browser exits unexpectedly;
- expired session;
- unexpected sign-in redirect;
- Goodreads service-error page;
- missing/changed selector;
- element present but disabled/covered;
- navigation timeout before mutation;
- timeout immediately after a mutating click;
- completion toast without changed state;
- changed state without expected toast;
- verification mismatch;
- CAPTCHA/challenge during ordinary headless operation;
- pagination loop;
- invalid or unresolved ISBN.

For a post-click timeout, prove that the adapter reads state once and never blindly replays the action.

### H. Export download

Validate:

- current UI control that starts a new export;
- synchronous/asynchronous behavior;
- how freshness is distinguished from an older export;
- browser download handling;
- expected filename/content type/headers;
- timeout and failure markers;
- CSV validation;
- temporary download cleanup.

Create a synthetic CSV fixture; never commit a personal export.

## Test pyramid

### Unit tests

Use table-driven tests for deterministic code:

- ISBN normalization/checksum;
- status/rating/date validation;
- library filters;
- JSON models;
- typed error mapping;
- selector-contract selection;
- URL/origin allow-list decisions;
- parsed field normalization;
- verification comparisons;
- review mismatch redaction;
- profile path validation;
- lock behavior;
- browser executable selection.

### DOM/parser fixture tests

Parse sanitized HTML fragments/pages without a live browser:

- library rows/cards;
- pagination;
- signed-out pages;
- book identifiers and ISBNs;
- ratings;
- shelves/status;
- review/date edit forms;
- success/error markers;
- export page states;
- compatibility drift when required markers are absent.

Fixtures should be minimal enough to review and must contain invented account/book data.

### Browser component tests

Launch Chromium against a local `httptest.Server` or deterministic test site to exercise:

- dedicated profile persistence;
- headed/headless launch options;
- page lifecycle;
- navigation and condition waits;
- form interactions;
- download interception;
- cancellation/timeout;
- browser cleanup;
- operation locking;
- unexpected-origin rejection.

These tests exercise Rod/CDP without Goodreads credentials and MAY run in a dedicated CI browser job.

### Goodreads flow tests

Serve synthetic Goodreads-like pages from a local test server and exercise the production adapter through the browser boundary.

Cover:

- authentication redirect and private-page validation;
- paginated shelf reads;
- exact ISBN resolution;
- each mutation and readback verification;
- idempotent already-satisfied state;
- post-submit ambiguity;
- selector alternatives;
- export generation/download;
- bounded request/page counts.

Avoid replacing all browser interactions with mocks; at least one local end-to-end flow should execute the real Rod wrapper and DOM contracts.

### Application tests

Use a fake semantic Goodreads adapter. Assert:

- argument validation occurs before browser work;
- no library cache survives an invocation;
- every mutation requires `Verified=true`;
- verification/ambiguity failures never report success;
- only requested semantic fields are passed;
- operation lock and error mapping are consistent.

### CLI tests

Inject fake services and test:

- arguments and flags;
- human output smoke tests;
- exact JSON schemas;
- stdout/stderr separation;
- exit codes;
- `login` is the only interactive command;
- `--headed` propagation;
- no secret/private-data leakage;
- no raw import or arbitrary-browser commands.

### MCP contract tests

Run tools against the same fake application service and verify schema, result, serialization, and error mapping. No separate Goodreads logic is allowed.

### Live probes

Live Goodreads probes are opt-in, serial, and use a dedicated account. They are
development commands rather than `go test` suites, and every probe requires the
`liveprobe` build tag.

Start with the headed login/profile-restart probe:

```text
go run -tags liveprobe ./tools/liveprobe
```

After login, run the operation-specific probes documented in the compatibility
matrix. Canary commands may mutate the dedicated test account and must be given
only the explicit arguments they document.

Requirements:

- never run in ordinary PR CI;
- require explicit confirmation/configuration;
- cap actions and pagination;
- prefer reversible/idempotent mutations;
- restore changed state when safe;
- never store credentials in test code;
- never print authenticated content;
- maintainers perform headed login separately.

## CI

Continuous CI runs for pull requests and pushes to `main` and includes:

- `gofmt` check;
- `go vet ./...`;
- `staticcheck ./...`;
- `go test ./...`;
- `go test -tags liveprobe ./...` to compile the development probes without running them;
- race detector on Linux;
- builds on Linux, macOS, and Windows;
- dependency/vulnerability scan where practical;
- a Linux Chromium job with `GOODREADS_BROWSER_TESTS=1` for local-server browser tests.

The tag-triggered release workflow must run or depend on the same required checks before publishing artifacts. Browser-component tests use only local synthetic pages and require no Goodreads credentials.

No live Goodreads calls or interactive sign-in in PR CI.

## Implementation slices

### Slice 0 — browser compatibility harness

- narrow Rod wrapper;
- browser resolution and launch;
- dedicated profile path and OS lock;
- headed/headless lifecycle;
- local deterministic browser tests;
- dev-only live probes;
- compatibility matrix for supported platforms.

Deliverable: documented evidence that a profile can log in manually, persist, reopen, and reach Goodreads safely.

### Slice 1 — authentication and live reads

- polished `login`, `status`, and `logout`;
- authenticated-state contracts;
- shelf parser and pagination;
- ISBN normalization/resolution;
- `library` and `get`;
- JSON and typed errors.

### Slice 2 — first verified write

- current-state capture;
- one mutation flow, with `rate` as the initial canary;
- mandatory readback verification;
- ambiguity handling;
- local-server and opt-in live tests.

Proceed only if the Slice 2 gate proves safe mutation and preservation.

### Slice 3 — core mutations

- `add`;
- `start`;
- `finish` and finish date;
- `review`;
- idempotency and preservation tests;
- headed troubleshooting UX.

### Slice 4 — export and packaging

- browser-driven fresh export/download;
- safe destination handling;
- installation/browser acquisition UX;
- cross-platform release automation and checksums.

### Slice 5 — MCP stdio

- `gr mcp`;
- semantic tool schemas;
- shared profile lifecycle and locking;
- MCP contract tests.

### Slice 6 — remote/mobile decision

Before implementing remote HTTP MCP, write a separate decision covering profile provisioning, encrypted persistence, reauthentication, browser sandboxing, and client auth. No remote mode is implied by v0.1.

## Release criteria for v0.1

- compatibility matrix records tested Goodreads/browser behavior;
- the project remains Go and uses a narrow Rod-backed browser adapter;
- `gr login` uses a dedicated headed profile and never handles credentials;
- later invocations reuse that profile without touching the user's normal browser;
- core reads load current Goodreads pages;
- core mutations drive the web UI and return success only after readback verification;
- no private Goodreads API or CSV-import mutation path;
- no local library database/cache/sync queue;
- profile locking and safe logout deletion are tested;
- JSON contracts and exit codes are tested;
- unit, fixture, local-browser, application, and CLI tests pass;
- live test-account smoke checks pass for the release browser matrix;
- README accurately states browser requirements and limitations.

## v0.1.1 hardening

The execution sequence and per-slice acceptance criteria live in [`../plans/v0.1.1-hardening.md`](../plans/v0.1.1-hardening.md). The hardening release adds no new Goodreads product capability. It closes four reliability gaps:

1. scalable exact-edition resolution with explicit incomplete-scan semantics;
2. final-state reconciliation for partially completed compound mutations;
3. required continuous/browser integration tests;
4. typed network errors and useful redacted diagnostics.

Release criteria for v0.1.1:

- exact lookup succeeds against a synthetic library larger than ten pages;
- safety-budget exhaustion returns `ErrScanIncomplete` and performs no mutation;
- failure after every step of `add` and `finish` is injected and reconciled;
- partial completion maps consistently through application, CLI, and MCP layers;
- pull-request/main CI, race tests, target builds, and the Linux Chromium component job pass;
- at least one synthetic flow executes the real Rod wrapper and production Goodreads contracts together;
- `ErrNetwork`, `ErrScanIncomplete`, and `ErrPartialMutation` have stable redacted public mappings;
- a fresh interactive `gr login` and the selected exact-resolution path are manually smoke-tested and recorded in the compatibility matrix.

## Compatibility policy

Goodreads may change without notice. Treat adapter drift as an expected operational failure.

A release should prefer “Goodreads UI changed; this version cannot safely continue at `mutation.rating`” over a guessed selector or best-effort write.

Restore compatibility by updating the documented contract, sanitized fixture, local flow test, and live observation together before shipping changed selectors.
