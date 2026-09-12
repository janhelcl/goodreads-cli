# Testing, compatibility spike, and delivery plan

## Rule zero: prove Goodreads behavior first

Several crucial behaviors are not stable/publicly documented enough to assume. Codex MUST implement and run (or prepare for the maintainer to run) a narrow live compatibility spike before building the full command surface.

Do not work around a failed spike with browser automation.

## Compatibility spike

Use a dedicated/non-critical Goodreads test account if possible and a handful of known books.

Capture fixtures/results without committing real cookies, passwords, private library exports, or personally identifying data.

### A. Authentication

Validate:

- ordinary email/password sign-in can be completed using standard HTTP requests only;
- CSRF/form handling can be discovered by parsing the current login page;
- authenticated session can be serialized/restored;
- expired session is detectable from the import/export page;
- logout/local session deletion behavior is understood.

If ordinary login cannot be implemented without browser automation, stop and report this as a product decision. Do not add Playwright.

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
- network timeout after import submission if it can be simulated safely.

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

Table-driven tests are preferred.

### HTTP adapter tests

Use `httptest.Server` with sanitized HTML/CSV fixtures to model:

- login + CSRF + redirects
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

Exercise Cobra commands with injected fake service:

- args/flags
- human output smoke tests
- exact JSON schemas
- stdout/stderr separation
- exit code mapping
- no password leakage

### MCP contract tests

Exercise tools against the same fake application service and verify schema/result/error mapping. No separate mutation logic is allowed.

### Live tests

Opt-in only, never in normal CI. Require explicit environment marker and test-account session.

Example:

```text
GOODREADS_LIVE_TESTS=1 go test ./internal/goodreads -tags=live
```

Live tests should be few, serial, reversible/idempotent where possible, and polite.

## CI

Initial CI should include:

- `gofmt` check
- `go vet ./...`
- `go test ./...`
- race detector on Linux where runtime is reasonable
- build on Linux/macOS/Windows
- dependency/vulnerability scan if practical

No live Goodreads calls in PR CI.

## Implementation slices

### Slice 0 — compatibility harness

- HTTP client/cookie jar scaffolding
- fixture parser helpers
- dev-only commands/tests needed to probe auth/export/import
- `specs` findings updated with concrete current behavior

Deliverable: written compatibility matrix. No polished CLI required.

### Slice 1 — read path

- session store abstraction
- login/status/logout
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
- no provider-specific logic in core

## Release criteria for v0.1

- compatibility matrix documents current Goodreads behavior;
- core read/write commands work against a real test account;
- Goodreads remains the only library source of truth;
- no browser runtime/dependency;
- no local library DB;
- secrets are not stored in plaintext by default;
- JSON contracts and exit codes tested;
- unit/adapter/application tests green on supported platforms;
- installation is a single binary;
- README accurately states limitations.

## Compatibility policy

Goodreads can change without notice. Treat adapter compatibility failures as expected operational errors.

A release should prefer "Goodreads import format changed; this version cannot safely continue" over a best-effort write.

When compatibility is restored, add/adjust fixtures and tests before changing production parsing.
