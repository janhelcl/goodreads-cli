# Product specification

## Problem

Goodreads no longer offers a practical public API for personal-library management, while AI assistants are increasingly good at discovering books and reasoning over public book information. The missing piece is a tiny, portable tool that gives an assistant controlled access to the user's **private Goodreads library state**.

`goodreads-cli` fills only that gap.

A user or agent should be able to express actions such as:

- "show what I am currently reading"
- "add ISBN X to want-to-read"
- "I started ISBN X"
- "I finished ISBN X today and gave it four stars"
- "change my rating of ISBN X to five"
- "set my review of ISBN X"

The tool performs the Goodreads-specific state transition. It does not try to understand which book the user meant from a natural-language title; the calling assistant can resolve that using web search and then pass an ISBN.

## Primary users

### Human CLI user

Wants a tiny executable with no runtime or database. Installs it, authenticates once through the normal Goodreads sign-in page opened in a supported local browser, and can inspect or mutate Goodreads from a shell.

### AI agent with shell access

Uses deterministic CLI commands and `--json`. The agent is expected to do book discovery and public-web research itself, then call this tool with a concrete ISBN and explicit desired state.

### MCP client

Uses semantic MCP tools backed by the exact same application service. MCP is transport/integration, not a second implementation.

## Source-of-truth invariant

Goodreads is the **only** canonical library state.

The application MUST NOT persist a copy of the user's library for later synchronization. Exported CSV data may exist transiently in memory or temporary files during one operation. An explicitly requested `gr export` output is a user artifact, not application state.

Permitted persistent state:

- authenticated Goodreads session material
- non-secret configuration
- lock files / ephemeral process coordination
- logs that contain no library contents or secrets by default

Forbidden persistent state:

- local book database
- cached canonical library
- mutation queue intended to sync later
- shadow copy of ratings/reviews/shelves
- background synchronization state

## Goals

1. Minimal installation and setup.
2. A single cross-platform binary.
3. Goodreads import/export as the sole library-state integration mechanism.
4. Browser-assisted authentication that never asks the CLI to collect the user's Goodreads password.
5. Useful direct CLI UX.
6. Deterministic agent UX (`--json`, stable errors, no prompts outside explicit auth flows).
7. A reusable application core that can be exposed over MCP.
8. Safe handling of Goodreads session cookies.
9. Conservative behavior when Goodreads changes its pages or CSV formats.

## Non-goals

- Goodreads public book search
- scraping ratings/reviews/book pages
- recommendations
- natural-language title matching
- metadata database
- reading analytics beyond simple filtering of a freshly exported library
- progress/page tracking unless Goodreads import/export later exposes it reliably
- started-reading date if the import/export format cannot express it
- reread event history if the CSV cannot represent it faithfully
- Goodreads social features
- friends/followers/groups
- browser automation for Goodreads library operations
- multi-user hosted SaaS
- automatic periodic sync

A narrowly scoped browser-assisted login ceremony is explicitly allowed. It exists only to let the user authenticate on Goodreads' own page and to capture the resulting session cookies. After login, all Goodreads library operations MUST use ordinary HTTP plus import/export; no DOM automation is allowed for those operations.

## Book identity

The public mutation contract is ISBN-first.

- Commands MUST accept ISBN-10 or ISBN-13.
- Input ISBNs MUST be normalized by removing common presentation separators and validating checksum where practical.
- Internally prefer ISBN-13 when both are available.
- The tool MUST NOT guess a book from a title.
- If Goodreads import cannot identify the requested edition/book from the provided ISBN, return a clear not-found/import failure.
- Books without a usable ISBN are outside the initial mutation scope.

This is intentional: AI assistants with web access are better placed to resolve "book X" into an ISBN than this tool is.

## Reading status model

The core status enum is:

- `to-read`
- `currently-reading`
- `read`

Do not invent statuses that Goodreads import/export does not round-trip reliably. Custom/exclusive shelves are compatibility-dependent and should only be added after the base statuses are proven.

## Product-level mutation semantics

### Add

`add(isbn, status=to-read)` ensures the book is present with the requested status. It MUST preserve existing user state when Goodreads already contains the book except for fields explicitly requested by the operation.

### Start

`start(isbn)` changes the status to `currently-reading` while preserving rating, review, custom shelves and other user-owned fields represented in the export.

No started date is promised unless the Goodreads CSV contract demonstrably supports one.

### Finish

`finish(isbn, date, rating?)` changes the status to `read`, sets `Date Read`, and optionally sets the rating. Default date is the caller's local calendar date if omitted.

### Rate

`rate(isbn, 1..5)` changes only the user's rating. A rating of zero/unset is not exposed until its semantics are validated.

### Review

`review(isbn, text)` changes only the user's review. Clearing a review is a separate explicit operation/flag; an omitted review never means "erase".

## Freshness

`library` and any query built on it MUST obtain a fresh Goodreads export for that invocation. There is no hidden TTL cache.

Mutations MUST first obtain the current row when preservation of existing fields is required. For adding a genuinely new ISBN, an export may still be used to detect existing state and maintain the "Goodreads first" invariant.

## Mobile and portability

The CLI itself runs where the executable runs. Mobile assistant use is enabled later by the same binary exposing remote MCP (`gr mcp --http ...`) from a reachable host. The application core must not depend on whether the caller is CLI, stdio MCP, or HTTP MCP.

A hosted multi-user account system is explicitly out of scope. One running process/session represents one Goodreads account.

## Success criteria for the first useful release

A clean machine can install one binary and then:

1. run `gr login`, authenticate on the real Goodreads sign-in page in a supported Chromium-family browser, and have the resulting session stored securely without giving the CLI a password;
2. close the login browser and perform all subsequent Goodreads operations without browser automation;
3. `gr library --shelf currently-reading --json` and receive the current state;
4. `gr add <isbn>`;
5. `gr start <isbn>`;
6. `gr finish <isbn> --rating 4`;
7. `gr rate <isbn> 5`;
8. `gr review <isbn> --text ...`;
9. run the same application operations through local MCP stdio.

If Goodreads CSV re-import cannot reliably update existing books, this product shape must be reconsidered rather than bypassing the constraint with browser automation.
