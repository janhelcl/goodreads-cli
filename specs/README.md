# goodreads-cli specifications

These specifications are the implementation contract for `goodreads-cli`.

The product is intentionally small: a command-line client that lets humans and AI agents manipulate **their Goodreads library**, while Goodreads remains the only source of truth. The integration boundary is Goodreads' own library import/export workflow, accessed with ordinary authenticated HTTP requests. There is no Goodreads API dependency, no browser automation, no local library database, and no book-metadata service.

## Normative language

`MUST`, `MUST NOT`, `SHOULD`, and `MAY` are normative. When code and specs disagree, update one intentionally; do not silently reinterpret the specs.

## Read this first

1. [01-product.md](01-product.md) — product scope and invariants
2. [02-architecture.md](02-architecture.md) — layers, interfaces, state, and dependency rules
3. [03-cli.md](03-cli.md) — public CLI contract
4. [04-goodreads-adapter.md](04-goodreads-adapter.md) — HTTP/session/CSV integration contract
5. [05-mcp.md](05-mcp.md) — MCP adapter over the same application core
6. [06-security-and-config.md](06-security-and-config.md) — credentials, sessions, secrets, logs
7. [07-testing-and-delivery.md](07-testing-and-delivery.md) — compatibility spike, tests, implementation slices, release criteria
8. [08-decisions.md](08-decisions.md) — confirmed decisions, assumptions, and open questions

## Non-negotiable principles

- **Goodreads is the database.** Never introduce SQLite/Postgres/a local canonical library or sync ledger.
- **Fresh reads come from Goodreads.** User-facing read/query operations fetch a current Goodreads export before answering.
- **Writes go through Goodreads import.** Mutations are expressed as Goodreads-compatible CSV imports. No hidden alternative write path.
- **No browser automation.** No Playwright, Selenium, Puppeteer, WebDriver, browser extension, DOM clicking, or headless browser.
- **No private Goodreads API dependency.** Ordinary HTTP needed for sign-in/import/export is allowed; the product must not grow into a reverse-engineered general Goodreads API.
- **CLI first.** The command line and application core are primary. MCP is a thin adapter over exactly the same core operations.
- **Agent friendly.** Every meaningful command has deterministic machine-readable output and useful exit codes.
- **No book discovery responsibility.** Title search, public Goodreads ratings, recommendations, metadata enrichment, and ISBN discovery belong to the calling assistant/web search, not this tool.
- **Fail safe on Goodreads drift.** Unknown or incompatible import/export behavior must produce a clear compatibility error rather than guessing or mutating partially.

## Implementation order

Do not build the full CLI before completing the live compatibility spike in `07-testing-and-delivery.md`. The core architecture depends on several empirical Goodreads behaviors that are not documented as stable contracts.

Once the spike passes, implement in vertical slices:

1. HTTP session + export + CSV parsing
2. `status`, `login`, `logout`, `export`, `library`
3. minimal import + one mutation (`rate` is a good first canary)
4. `add`, `start`, `finish`, `review`
5. stable JSON contracts and error taxonomy
6. MCP stdio adapter
7. optional remote HTTP MCP transport
8. packaging/release automation

## External integration reference

The authenticated Goodreads import/export UI currently lives at `https://www.goodreads.com/review/import`. It redirects unauthenticated users to sign-in/sign-up. Treat exact endpoints, form fields, CSRF mechanics, polling behavior, and accepted CSV columns as compatibility details discovered by tests, not permanent constants inferred from old examples.
