# Architecture specification

## Shape

```text
CLI (Cobra) ────────────┐
                       │
MCP transport ─────────┤
                       ▼
                Application service
                       │
                       ▼
             Goodreads browser adapter
                 │             │
          Rod + Chromium   DOM contracts
                 │             │
                 └──────┬──────┘
                        ▼
                 Goodreads web UI
```

All Goodreads behavior lives below the application boundary. CLI and MCP MUST NOT contain selectors, page-navigation logic, or browser-library details.

The browser adapter launches Chromium with one dedicated CLI-owned user-data directory. Login runs headed and leaves the profile in place. Ordinary operations reuse the profile headlessly by default.

## Suggested Go package boundaries

Exact names MAY change, but dependency direction must remain equivalent.

```text
cmd/                    Cobra wiring only
internal/app/           use cases / application service
internal/domain/        book, status, filter, update, and result types
internal/goodreads/     Goodreads page flows and DOM contracts
internal/browser/       Rod launcher, profile, page, download, and lifecycle
internal/profile/       profile paths, locking, and deletion
internal/mcp/           MCP tool/transport adapter
internal/output/        human and JSON CLI rendering
```

`main.go` should do little more than invoke the root command.

Rod is the initial implementation choice. Goodreads code MUST depend on a narrow local browser interface so DOM parsing and application behavior can be tested without launching Rod in every test.

## Domain model

Keep it smaller than Goodreads' complete UI.

```go
type ReadingStatus string

const (
    StatusToRead           ReadingStatus = "to-read"
    StatusCurrentlyReading ReadingStatus = "currently-reading"
    StatusRead             ReadingStatus = "read"
)

type Book struct {
    BookID        string
    Title         string
    Author        string
    ISBN10        string
    ISBN13        string
    Rating        int
    AverageRating *float64
    DateRead      *time.Time
    DateAdded     *time.Time
    Status        ReadingStatus
    Bookshelves   []string
    Review        string
}

type BookUpdate struct {
    Status   *ReadingStatus
    Rating   *int
    DateRead *time.Time
    Review   *string
}

type MutationResult struct {
    Operation string
    Before    Book
    After     Book
    Changes   BookUpdate
    Verified  bool
}
```

The public model must not leak CSS selectors, Rod element handles, page URLs, or raw HTML.

## Application service

The application layer exposes semantic methods and has no awareness of Cobra flags or MCP schemas.

```go
type LibraryService interface {
    Status(ctx context.Context) (ConnectionStatus, error)
    Export(ctx context.Context, destination string) (ExportResult, error)
    Library(ctx context.Context, filter LibraryFilter) ([]Book, error)
    Get(ctx context.Context, isbn string) (Book, error)
    Add(ctx context.Context, isbn string, status ReadingStatus) (MutationResult, error)
    Start(ctx context.Context, isbn string) (MutationResult, error)
    Finish(ctx context.Context, isbn string, date time.Time, rating *int) (MutationResult, error)
    Rate(ctx context.Context, isbn string, rating int) (MutationResult, error)
    Review(ctx context.Context, isbn string, review *string) (MutationResult, error)
}
```

Authentication/profile lifecycle is a separate interactive boundary:

```go
type AuthService interface {
    Login(ctx context.Context) (ConnectionStatus, error)
    Logout(ctx context.Context) error
    Status(ctx context.Context) (ConnectionStatus, error)
}
```

Exact method names MAY differ. Product semantics in `01-product.md` and CLI semantics in `03-cli.md` are authoritative.

## Goodreads adapter boundary

Define a semantic interface narrow enough to fake:

```go
type Goodreads interface {
    ValidateSession(ctx context.Context) error
    ListLibrary(ctx context.Context, filter LibraryFilter) ([]Book, error)
    FindByISBN(ctx context.Context, isbn string) (Book, error)
    Add(ctx context.Context, isbn string, status ReadingStatus) (MutationResult, error)
    Update(ctx context.Context, book Book, change BookUpdate) (MutationResult, error)
    DownloadExport(ctx context.Context, destination string) (ExportResult, error)
}
```

The production adapter owns navigation, page interpretation, selectors, waits, and readback verification. The application service owns argument validation and maps semantic use cases onto this interface.

## Browser boundary

A small wrapper keeps Rod details out of the Goodreads flow tests. The design may resemble:

```go
type BrowserFactory interface {
    Launch(ctx context.Context, opts LaunchOptions) (Browser, error)
}

type LaunchOptions struct {
    ProfileDir  string
    Headless    bool
    DownloadDir string
}

type Browser interface {
    NewPage(ctx context.Context, url string) (Page, error)
    Close() error
}
```

Do not create a generic framework for arbitrary websites. Add only the operations needed by tested Goodreads flows.

The launcher is responsible for:

- locating a supported installed Chromium-family browser or a Rod-managed Chromium;
- launching it with the dedicated user-data directory;
- using headed mode for login and headless mode by default otherwise;
- keeping debugging endpoints local to the process;
- propagating cancellation and closing browser processes;
- configuring a private temporary download directory unless the user requested a destination;
- producing typed launch and browser-unavailable errors.

## Dedicated browser profile

The profile directory is persistent authentication state.

Requirements:

- one profile per local Goodreads account in v0.1;
- stored under the platform-appropriate application data/config root;
- never point at the user's ordinary browser profile;
- restrictive permissions where supported;
- never inspect Chromium databases directly to derive library state;
- never copy credentials out into a second cookie/session format;
- delete only through explicit `gr logout` or a future reset command;
- exclude it from backups/logging support bundles by default.

The browser profile can contain cookies, local storage, cache, and other sensitive browser state. Treat the whole directory as a credential.

## Login flow

`gr login` is interactive:

1. acquire the profile lock;
2. create or open the dedicated profile;
3. launch a visible browser;
4. navigate to Goodreads sign-in;
5. let the user complete Goodreads/provider authentication manually;
6. wait until a known authenticated Goodreads page/state is observed;
7. validate by opening a private library page;
8. close the browser while retaining the profile;
9. report success.

The automation MUST NOT inspect password fields, type credentials, submit the login form for the user, or attach to an existing personal browser session.

If the profile already has a valid session, `login` MAY report success without requiring another sign-in, but the browser remains headed for this command.

## Live read flow

A library read should:

1. acquire the profile lock;
2. launch the dedicated profile;
3. navigate to the user's Goodreads shelf/library page;
4. confirm authenticated state;
5. apply server-side shelf navigation when requested;
6. paginate only until the requested result/limit is satisfied;
7. parse each rendered row/card into the domain model;
8. return the live result and close the browser.

Local filtering is allowed for fields already loaded in this invocation. No result is persisted as application state.

`Get` MAY locate the book by navigating shelf/search UI or an exact Goodreads book page resolved from ISBN. It must reject ambiguous or mismatched results.

Exact-edition resolution is a semantic adapter capability, not a CLI-specific loop. It SHOULD prefer a tested Goodreads owner-library lookup that can prove the exact ISBN or stable edition identity. If resolution requires pagination, the scanner must separate:

- normal termination at the actual end of the shelf;
- a repeated-page or pagination-loop compatibility failure; and
- exhaustion of an explicit safety budget, reported as `ErrScanIncomplete`.

A fixed small page count is not a supported-library definition. No mutation may start after an incomplete identity scan.

## Mutation flow

A mutation should:

1. acquire the profile lock;
2. launch the dedicated profile;
3. resolve the exact ISBN and current library state;
4. capture fields needed to detect unintended changes;
5. perform the narrowest applicable UI interaction;
6. wait for a recognized completion state;
7. reload or revisit the affected Goodreads view;
8. parse the resulting state;
9. compare each requested field and required preservation invariant; and
10. return `Verified=true` only when comparison succeeds.

If completion is ambiguous, return an ambiguous/compatibility error and include safe troubleshooting guidance. Do not automatically replay the click or form submission.

### Compound mutation reconciliation

`add` with a non-default target status and `finish` are compound operations. Model their ordered semantic steps explicitly rather than assuming all-or-nothing behavior from the remote UI.

After each mutating step:

1. read back and verify that step;
2. record only the verified semantic fields in memory;
3. continue to the next requested step.

If a later step fails after at least one verified write, perform one final readback when it is safe and return a typed `PartialMutationError`. The error carries the operation, completed steps, failed step, and safe observed state. It MUST NOT contain review text, raw HTML, selectors, account identifiers, or profile paths. Automatic rollback and automatic replay are prohibited.

## Selector and page contracts

Centralize compatibility knowledge by page/flow, for example:

```text
internal/goodreads/
  contracts.go
  auth.go
  shelves.go
  book.go
  mutate.go
  export.go
  parse.go
```

Selector policy, in preference order:

1. stable accessible role/name or associated label;
2. semantic form attributes and stable URLs;
3. human-visible text scoped to a known page region;
4. narrowly scoped CSS selectors as a last resort.

Avoid positional selectors, generated class names, fixed sleeps, and selectors duplicated across commands. Every flow must assert the expected page identity before acting.

## No repository abstraction

Do **not** introduce a generic repository backed by SQLite or files. Goodreads is the remote source. The Goodreads adapter is sufficient.

## Temporary data

Screenshots, HTML snapshots, traces, and downloads are disabled by default.

When explicitly enabled for debugging:

- write to a user-selected or clearly reported path;
- redact or warn about private library/session content;
- never include cookies, browser storage, or credential fields;
- avoid automatic long-term retention.

`gr export --out file.csv` is the only normal workflow that persists library data.

## Concurrency

Chromium user-data directories cannot be safely shared by concurrent processes, and simultaneous Goodreads mutations risk races.

Every command that opens the profile MUST acquire one exclusive per-profile process lock.

Requirements:

- finite lock-acquisition timeout;
- owner metadata sufficient for an actionable busy message;
- safe stale-lock recovery using an OS lock primitive rather than timestamp guessing;
- structured busy errors in JSON/MCP;
- release on success, failure, cancellation, and panic where possible;
- no read/write concurrency optimization in v0.1.

The lock is coordination metadata, not library state.

## Context, cancellation, and timeouts

All operations MUST accept `context.Context`.

Use a sensible operation timeout with a global override. Login has a separate, longer interactive timeout. Navigation, element waits, and downloads must derive bounded contexts from the operation context.

No infinite waits or fixed sleeps as synchronization.

## Error taxonomy

Use typed application errors so CLI and MCP map failures consistently:

- `ErrNotAuthenticated`
- `ErrSessionExpired`
- `ErrBrowserUnavailable`
- `ErrBrowserLaunch`
- `ErrLoginCancelled`
- `ErrBookNotFound`
- `ErrInvalidISBN`
- `ErrInvalidRating`
- `ErrCompatibility`
- `ErrMutationAmbiguous`
- `ErrPartialMutation`
- `ErrVerificationFailed`
- `ErrScanIncomplete`
- `ErrExportFailed`
- `ErrBusy`
- `ErrNetwork`
- `ErrUnsupported`

User-facing errors must be actionable without exposing cookies, authenticated HTML, review text, or profile contents.

`ErrPartialMutation` is distinct from `ErrMutationAmbiguous`: the former proves that at least one semantic step completed and reports the reconciled safe state; the latter cannot prove whether the attempted write completed. `ErrScanIncomplete` means no mutation was attempted because exact identity could not be established within the scan budget.

## Observability

- optional redacted debug logging to stderr;
- no telemetry in the initial product;
- no library-content logging by default;
- operation name, duration, page stage, retry count, browser product/version, and selector contract version may be logged;
- secrets, browser storage, cookies, form values, reviews, and raw authenticated pages must never be logged;
- a screenshot/HTML diagnostic capture requires an explicit user flag and privacy warning.

## Dependency rule

```text
cmd -> app/domain <- mcp
          |
          +-> goodreads -> browser
                          |
                          +-> profile
```

The core MUST be testable without Cobra, MCP, or a live Goodreads account.
