# goodreads-cli specifications

These specifications are the implementation contract for `goodreads-cli`.

The product is intentionally small: a Go command-line client that lets humans and AI agents inspect and mutate **their Goodreads library** through Goodreads' live web UI. Goodreads remains the only source of truth. A dedicated CLI-owned Chromium profile preserves authentication between invocations; the CLI never reads the user's normal browser profile and never collects a Goodreads password.

## Normative language

`MUST`, `MUST NOT`, `SHOULD`, and `MAY` are normative. When code and specs disagree, update one intentionally; do not silently reinterpret the specs.

Versioned implementation plans live in [`../plans/`](../plans/). Plans describe sequencing, experiments, and delivery gates; they do not override this normative contract. The active plan is [`v0.1.1-hardening.md`](../plans/v0.1.1-hardening.md).

## Read this first

1. [01-product.md](01-product.md) — product scope and invariants
2. [02-architecture.md](02-architecture.md) — layers, browser boundary, state, and dependencies
3. [03-cli.md](03-cli.md) — public CLI contract
4. [04-goodreads-adapter.md](04-goodreads-adapter.md) — Goodreads browser-automation contract
5. [05-mcp.md](05-mcp.md) — MCP adapter over the same application core
6. [06-security-and-config.md](06-security-and-config.md) — profile security, secrets, configuration, and logs
7. [07-testing-and-delivery.md](07-testing-and-delivery.md) — compatibility spike, tests, delivery slices, and release criteria
8. [08-decisions.md](08-decisions.md) — confirmed decisions and empirical questions

## Non-negotiable principles

- **Goodreads is the database.** Never introduce SQLite, Postgres, a local canonical library, a mutation queue, or a sync ledger.
- **Reads are live.** User-facing reads navigate Goodreads for that invocation; no hidden TTL cache or stale CSV snapshot may answer them.
- **Writes use the visible product contract.** Mutations drive Goodreads' web UI and MUST read the resulting state back before reporting success.
- **The browser profile belongs to this CLI.** Never attach to, copy, or inspect the user's ordinary browser profile.
- **Login stays with Goodreads.** `gr login` opens a headed browser and the user completes authentication. The CLI never types, receives, or stores credentials.
- **No private Goodreads API dependency.** Do not reverse-engineer or call undocumented JSON endpoints as an alternative implementation.
- **Go first.** The application, CLI, and browser adapter remain Go-native. Rod is the initial browser library, behind a narrow interface.
- **CLI first.** MCP is a thin adapter over the same application service, not a separate Goodreads implementation.
- **Agent friendly.** Commands have deterministic JSON, stable errors, explicit mutations, and no prompts outside login.
- **No book discovery responsibility.** Initial mutation commands are ISBN-first. Public metadata, recommendations, and fuzzy title resolution stay outside the tool.
- **Fail closed on Goodreads drift.** Missing selectors, unexpected pages, or unverifiable mutations produce compatibility errors rather than guessed clicks.
- **Incomplete scans are explicit.** A safety budget may stop an unusually large scan, but the result MUST be `scan_incomplete`, never a false not-found or generic compatibility result.
- **Partial writes are observable.** A compound command that verifies an early write and then fails MUST reconcile the final Goodreads state and report a typed partial-mutation error. It MUST NOT roll back or replay automatically.

## Implementation order

Do not build the full CLI before completing the browser compatibility spike in `07-testing-and-delivery.md`.

After the spike, implement vertical slices:

1. Rod launcher, dedicated profile, operation lock, login/status/logout
2. live shelf reads, ISBN normalization, `library`, and `get`
3. one verified UI mutation as a canary
4. `add`, `start`, `finish`, `rate`, and `review`
5. export download and stable JSON/error contracts
6. MCP stdio adapter
7. optional remote browser/MCP deployment after an explicit design decision
8. packaging and release automation

## External integration reference

Goodreads page URLs, DOM structure, accessibility labels, selectors, and success markers are compatibility facts discovered by tests. Centralize them in the adapter and assume they can change without notice.

The current browser spike results and remaining live-account gates are recorded in the [compatibility matrix](compatibility-matrix.md). The 2026-09-20 public-CLI user-acceptance run is [uat-2026-09-20.md](uat-2026-09-20.md).
