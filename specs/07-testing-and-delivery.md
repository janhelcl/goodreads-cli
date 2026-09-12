# Testing, compatibility spike, and delivery plan

## Rule zero: prove Goodreads behavior first

Several crucial behaviors are not stable/publicly documented enough to assume. Codex MUST implement and run (or prepare for the maintainer to run) a narrow live compatibility spike before building the full command surface.

Do not work around failed import/export behavior with browser automation. Browser use is permitted only for the login/session-acquisition ceremony described in the specs.

## Compatibility spike

Use a dedicated/non-critical Goodreads test account if possible and a handful of known books.

Capture fixtures/results without committing real cookies, browser profiles, private library exports, or personally identifying data.

### A. Browser-assisted authentication

Validate:

- Chrome, Chromium, and Edge discovery on supported platforms where available;
- launch with an isolated temporary user-data directory;
- remote-debugging endpoint is loopback-only and ephemeral;
- the browser is visible and the user can complete ordinary Goodreads authentication manually;
- social/provider login flows can complete because the tool does not automate or intercept them;
- authenticated Goodreads cookies can be captured through CDP without inspecting credential fields;
- captured session can be serialized, restored into the ordinary Go HTTP client, and accepted by Goodreads;
- expired session is detectable from the import/export page;
- temporary browser/profile cleanup works on success, cancellation, timeout, and ordinary failure;
- `gr logout`/local session deletion behavior is understood;
- no Goodreads password is ever passed to CLI code or logs.

If supported Chromium browsers cannot provide a reliable reusable Goodreads session this way, stop and report this as a product decision. Do not fall back to password collection or library-operation browser automation.

### B. Export

Validate:

- exact request that triggers generation of a fresh export;
- whether generation is asynchronous;
- how completion is detected;
- how the generated CSV URL/file is obtained;
- observed generation time for a small library;
- current headers and date/ISBN encodings;
- whether a newly triggered export can be distinguished from a stale previous export.

Save a **sanitized synthetic fixture** matching the observed schema under tests/fixtures, not a real personal export.

### C. Minimal import

Determine the smallest accepted CSV/header set for adding a book by ISBN.

Validate at least:

- add a new book to `to-read`;
- add/set `currently-reading`;
- set/read status;
- rating;
- review;
- date read;
- custom `Bookshelves` preservation if present.

Record whether status is controlled by `Exclusive Shelf`, `Bookshelves`, or another currently accepted input convention.

### D. Updating an existing book — blocking gate

This is the critical product assumption.

Starting from a known existing book, test independently:

1. rating `3 -> 4`;
2. `to-read -> currently-reading`;
3. `currently-reading -> read` + `Date Read`;
4. review change;
5. preserve unrelated review when rating changes;
6. preserve unrelated rating when status changes;
7. preserve custom non-exclusive shelves/tags.

After each import, trigger a new export and verify the actual stored Goodreads state.

If a one-row/small CSV import does **not** reliably update existing records, stop implementation and report. The architecture should be reconsidered with the maintainer rather than quietly sending a full-library import or adding browser automation.

### E. Idempotency

Repeat a successful mutation with identical desired state. Confirm it is harmless and does not create duplicate library entries or corrupt dates/shelves.

### F. Failure behavior

Test safe cases:

- invalid ISBN;
- ISBN Goodreads cannot resolve;
- malformed CSV;
- expired session;
- invalid CSRF;
- network timeout after import submission if it can be simulated safely;
- browser not installed;
- login cancelled;
- login timeout;
- browser exits before session capture.

Document what response/page markers indicate each state.

## Test pyramid

### Unit tests

High coverage for deterministic code:

- ISBN normalization/checksum
- CSV parser/encoder
- Excel-style ISBN cell normalization
- dates
- status/rating validation
- row preservation during mutations
- library filters
- JSON output models
- error mapping
- session serialization/redaction
- supported-browser discovery/path selection logic
- cookie filtering/conversion from CDP representation to session representation

Table-driven tests are preferred.

### Browser-auth component tests

Keep most browser-auth logic testable without live Goodreads:

- browser executable discovery;
- command-line construction uses an isolated user-data directory;
- debugging is loopback-only;
- temporary directory lifecycle/cleanup;
- cancellation/timeout cleanup;
- CDP cookie filtering and session conversion;
- no attachment to default browser profiles.

A small opt-in integration test may launch a real local Chromium against a local test page/server to prove CDP session capture without involving Goodreads credentials.

### HTTP adapter tests

Use `httptest.Server` with sanitized HTML/CSV fixtures to model:

- authenticated session validation
- export trigger/poll/download
- import form/post/result
- session expiry
- compatibility drift
- retries/timeouts

Tests should assert request counts so accidental polling/crawling regressions are caught.

### Application tests

Use a fake Goodreads adapter. Assert semantic invariants:

- every library query exports fresh state;
- mutation exports before preserving/updating a row;
- only requested fields change;
- failed import never reports success;
- ambiguous mutation errors are not automatically retried.

### CLI tests

Exercise Cobra commands with injected fake service/session acquirer:

- args/flags
- human output smoke tests
- exact JSON schemas
- stdout/stderr separation
- exit code mapping
- login invokes session acquisition rather than collecting credentials
- no secret leakage

### MCP contract tests

Exercise tools against the same fake application service and verify schema/result/error mapping. No separate mutation logic is allowed.

### Live tests

Opt-in only, never in normal CI. Require explicit environment marker and test-account session.

Example:

```text
GOODREADS_LIVE_TESTS=1 go test ./internal/goodreads -tags=live
```

Live import/export tests should be few, serial, reversible/idempotent where possible, and polite.

Interactive browser-login tests are maintainer-run compatibility checks, not CI. They must never rely on stored account passwords.

## CI

Initial CI should include:

- `gofmt` check
- `go vet ./...`
- `go test ./...`
- race detector on Linux where runtime is reasonable
- build on Linux/macOS/Windows
- dependency/vulnerability scan if practical

No live Goodreads calls or interactive browsers in PR CI.

## Implementation slices

### Slice 0 — compatibility harness

- session model/store abstraction
- browser discovery + isolated Chromium login bootstrap
- HTTP client/cookie jar scaffolding
- fixture parser helpers
- dev-only commands/tests needed to probe session validation/export/import
- `specs` findings updated with concrete current behavior

Deliverable: written compatibility matrix proving both browser-assisted session reuse and import/export semantics. No polished CLI required.

### Slice 1 — read path

- production session store
- polished login/status/logout
- export workflow
- CSV parser
- ISBN normalization
- `export`, `library`, `get`
- JSON output + errors

### Slice 2 — first write path

- import workflow
- safe row transformation
- one simple mutation (`rate` recommended)
- post-import verification option
- integration fixtures/tests

Only proceed if Slice 0 D-gate passed.

### Slice 3 — core UX

- `add`
- `start`
- `finish`
- `review`
- operation locking
- packaging polish

### Slice 4 — MCP stdio

- `gr mcp`
- semantic tool schemas
- contract tests

### Slice 5 — remote/mobile enablement

- `gr mcp --http`
- remote bearer auth
- container/deployment example (e.g. Render) if wanted
- session provisioning for headless deployment
- no provider-specific logic in core

## Release criteria for v0.1

- compatibility matrix documents current Goodreads behavior;
- `gr login` uses Goodreads' own browser login UI and stores only the reusable session;
- core read/write commands work against a real test account;
- Goodreads remains the only library source of truth;
- browser/CDP is used only for authentication, never library operations;
- no local library DB;
- secrets are not stored in plaintext by default;
- JSON contracts and exit codes tested;
- unit/adapter/application tests green on supported platforms;
- installation is a single binary plus an already-installed supported Chromium-family browser for interactive login;
- README accurately states limitations.

## Compatibility policy

Goodreads can change without notice. Treat adapter compatibility failures as expected operational errors.

A release should prefer "Goodreads import format changed; this version cannot safely continue" over a best-effort write.

When compatibility is restored, add/adjust fixtures and tests before changing production parsing.
