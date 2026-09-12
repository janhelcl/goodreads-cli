# Architecture specification

## Shape

```text
CLI (Cobra) ─────────────┐
                        │
MCP transport ──────────┤
                        ▼
                 Application service
                        │
                        ▼
                 Goodreads adapter
                ┌───────┴────────┐
                │                │
          auth/session      import/export
                │                │
                └───────┬────────┘
                        ▼
                    Goodreads
```

All Goodreads behavior lives below the application boundary. CLI and MCP MUST NOT contain CSV manipulation or Goodreads HTTP details.

## Suggested Go package boundaries

The exact names MAY change, but dependency direction must remain equivalent.

```text
cmd/                    Cobra wiring only
internal/app/           use cases / application service
internal/domain/        Book, status, update/filter/result types
internal/goodreads/     authenticated HTTP + import/export implementation
internal/csvgr/         Goodreads CSV parsing/encoding
internal/session/       credential/session persistence abstraction
internal/mcp/           MCP tool/transport adapter
internal/output/        human + JSON CLI rendering
```

`main.go` should do little more than invoke the root command.

## Domain model

Keep it smaller than Goodreads' complete export schema.

```go
type ReadingStatus string

const (
    StatusToRead           ReadingStatus = "to-read"
    StatusCurrentlyReading ReadingStatus = "currently-reading"
    StatusRead             ReadingStatus = "read"
)

type Book struct {
    BookID       string        // Goodreads export identity, read-only/internal
    Title        string
    Author       string
    ISBN10       string
    ISBN13       string
    Rating       int
    AverageRating *float64     // informational; from export only
    DateRead     *time.Time    // date semantics, no time-of-day contract
    DateAdded    *time.Time
    Status       ReadingStatus
    Bookshelves  []string
    Review       string
}

type BookUpdate struct {
    Status   *ReadingStatus
    Rating   *int
    DateRead *time.Time
    Review   *string
}
```

The Goodreads CSV layer may use a richer lossless row type so existing fields can be preserved across a mutation. Do not force every export column into the public domain model.

## Application service

The application layer exposes semantic methods and has no awareness of CLI flags or MCP schemas.

Representative interface:

```go
type LibraryService interface {
    Status(ctx context.Context) (ConnectionStatus, error)
    Export(ctx context.Context) ([]Book, error)
    Get(ctx context.Context, isbn string) (Book, error)
    Add(ctx context.Context, isbn string, status ReadingStatus) (MutationResult, error)
    Start(ctx context.Context, isbn string) (MutationResult, error)
    Finish(ctx context.Context, isbn string, date time.Time, rating *int) (MutationResult, error)
    Rate(ctx context.Context, isbn string, rating int) (MutationResult, error)
    Review(ctx context.Context, isbn string, review *string) (MutationResult, error)
}
```

Exact API names MAY differ. Semantics from `01-product.md` and `03-cli.md` are authoritative.

## Goodreads adapter boundary

Define an interface narrow enough to fake in tests:

```go
type Goodreads interface {
    ValidateSession(ctx context.Context) error
    ExportLibrary(ctx context.Context) (Export, error)
    ImportLibrary(ctx context.Context, csv []byte) (ImportResult, error)
}
```

Authentication/session acquisition may be a separate interface because it has interactive concerns.

The application service should orchestrate:

1. fresh export;
2. locate/derive the target row;
3. apply exactly the requested semantic change while preserving unrelated fields;
4. encode a minimal safe import document;
5. submit import;
6. map the Goodreads outcome to a stable result/error.

## No repository abstraction

Do **not** introduce a generic `Repository` backed by SQLite/files. Goodreads is the remote source. The Goodreads adapter is sufficient.

## Temporary data

Prefer in-memory CSV bytes. If a temporary file is required by a library/API, use the OS temporary directory, restrictive permissions, and delete it promptly. Do not place library exports in the config directory automatically.

`gr export --out file.csv` is the only normal path that intentionally persists a library CSV.

## Concurrency

Goodreads import/export are account-level operations and may be asynchronous. Concurrent mutations from the same local account risk races.

The process MUST serialize operations that perform Goodreads import/export. For separate `gr` processes, use a lightweight per-account lock in the application config/runtime area. The lock is coordination state, not library state.

Requirements:

- lock acquisition has a finite timeout;
- stale locks can be recovered safely;
- JSON mode returns a structured busy error;
- `library` reads MAY share the same exclusive lock initially for simplicity;
- optimize only if real usage justifies it.

## Context, cancellation, and timeouts

All network operations MUST accept `context.Context`.

Have one sensible default request/operation timeout and allow global override. Export generation may require polling; total operation timeout is distinct from per-request timeout if needed.

No infinite polling.

## Error taxonomy

Use typed/sentinel application errors so CLI and MCP map failures consistently:

- `ErrNotAuthenticated`
- `ErrSessionExpired`
- `ErrBookNotFound`
- `ErrInvalidISBN`
- `ErrInvalidRating`
- `ErrCompatibility` — page/schema/expected Goodreads behavior changed
- `ErrImportRejected`
- `ErrExportFailed`
- `ErrBusy`
- `ErrNetwork`
- `ErrUnsupported`

Errors returned to users should include an actionable message without leaking cookies, passwords, CSRF tokens, HTML bodies containing personal data, or full CSV rows.

## Observability

Keep it boring:

- optional debug logging to stderr;
- no telemetry in the initial product;
- no library-content logging by default;
- redact session/cookie/header values unconditionally;
- include operation names, durations, HTTP status classes, retry counts, and compatibility-stage information when debug mode is enabled.

## Dependency rule

Transport adapters depend inward:

```text
cmd -> app/domain <- mcp
          |
          v
      goodreads
          |
          v
        csvgr
```

The core MUST be usable in tests without Cobra and without an MCP server.
