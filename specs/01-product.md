# Product specification

## Problem

Goodreads no longer offers a practical public API for personal-library management. AI assistants can discover books and reason over public information, but they need a small, controlled way to work with the user's private Goodreads state.

`goodreads-cli` fills that gap by automating the same Goodreads web interface a person uses.

Typical requests are:

- "show what I am currently reading"
- "add ISBN X to want-to-read"
- "I started ISBN X"
- "I finished ISBN X today and gave it four stars"
- "change my rating of ISBN X to five"
- "set my review of ISBN X"

The tool performs the Goodreads-specific transition. It does not infer a book from a natural-language title; the caller resolves the book to an ISBN first.

## Primary users

### Human CLI user

Installs one Go binary, signs in once through a visible dedicated browser window, and inspects or mutates Goodreads from a shell. No separate runtime or local database is required.

### AI agent with shell access

Uses deterministic commands and `--json`. The agent performs public-web discovery separately, then calls this tool with a concrete ISBN and explicit desired state.

### MCP client

Uses semantic tools backed by the same application service. MCP is a transport adapter, not a second implementation.

## Source-of-truth invariant

Goodreads is the **only** canonical library state.

The application MUST NOT persist book records for later synchronization. Each command obtains the Goodreads state it needs during that invocation.

Permitted persistent state:

- the dedicated browser profile, including Goodreads cookies and web storage
- non-secret configuration
- browser/download cache owned by the automation runtime
- lock files and process-coordination metadata
- redacted diagnostic logs
- a CSV file explicitly requested by `gr export`

Forbidden persistent state:

- local book database or canonical cache
- mutation queue intended to sync later
- shadow copy of ratings, reviews, shelves, or reading history
- background synchronization state

The browser profile is authentication/runtime state, not a second library database. Code MUST NOT query Chromium's profile database as a substitute for visiting Goodreads.

## Goals

1. Minimal installation and setup.
2. A Go-native, cross-platform executable.
3. Browser automation for live Goodreads reads and writes.
4. Authentication that never asks the CLI to collect a password.
5. Useful direct CLI UX.
6. Deterministic agent UX: JSON, stable errors, and non-interactive operations after login.
7. A reusable application core that can later be exposed over MCP.
8. Conservative, verifiable mutations.
9. Clear failure when Goodreads changes its UI.

## Non-goals

- Goodreads public book search or fuzzy title matching
- recommendations or metadata enrichment
- a private/undocumented Goodreads API client
- reading analytics beyond filtering live Goodreads results
- progress/page tracking in the first release
- rich reread event history in the first release
- Goodreads social features, friends, followers, or groups
- manipulating the user's normal Chrome/Chromium/Edge profile
- collecting or autofilling account credentials
- multi-user hosted SaaS
- automatic periodic sync
- using CSV import as the normal mutation mechanism

CSV export remains supported as an explicit user artifact. CSV import is out of the initial public interface.

## Book identity

The initial public mutation contract is ISBN-first.

- Commands MUST accept ISBN-10 or ISBN-13.
- Inputs MUST be normalized by removing common separators and validating checksums where practical.
- Internally prefer ISBN-13 when Goodreads exposes both.
- The tool MUST NOT guess a book from a title.
- If Goodreads cannot resolve the requested edition/book from the ISBN, return a clear not-found error.
- Books without a usable ISBN are outside the initial mutation scope.

The adapter MAY search or navigate Goodreads as necessary to resolve an exact ISBN. It MUST NOT expose a general public Goodreads search API.

Exact-ISBN operations MUST remain usable for libraries larger than an ordinary shelf-page window. A hard-coded pagination limit MUST NOT cause a known match to be reported as not-found or UI incompatibility. If a tested targeted lookup is unavailable and a bounded scan cannot reach a conclusive result, return an explicit incomplete-scan error without attempting a mutation.

## Reading status model

The core status enum is:

- `to-read`
- `currently-reading`
- `read`

Custom shelves may be displayed when already present, but editing them is deferred until explicitly specified and tested.

## Mutation semantics

### Add

`add(isbn, status=to-read)` ensures the book is present with the requested exclusive shelf. If it is already present, only the explicitly requested status may change.

### Start

`start(isbn)` sets the exclusive shelf to `currently-reading` while preserving rating, review, custom shelves, and other user state.

Recording a start timestamp is not promised in the first release.

### Finish

`finish(isbn, date, rating?)` sets the exclusive shelf to `read`, records the finish date through the Goodreads UI when supported, and optionally sets the rating. If omitted, date defaults to the caller's local calendar date.

### Rate

`rate(isbn, 1..5)` changes only the user's rating. Clearing a rating is deferred until separately specified.

### Review

`review(isbn, text)` changes only the user's review. Clearing requires an explicit flag; omitted text never means erase.

## Freshness and verification

Every read MUST be based on pages loaded from Goodreads during that invocation. There is no hidden TTL cache.

Every mutation MUST:

1. observe enough current state to avoid unintended changes;
2. perform the narrow UI action;
3. wait for an explicit completion marker or navigation;
4. read the affected state back from Goodreads; and
5. return success only when the requested fields match.

The application MUST NOT equate a click, HTTP status, toast alone, or page navigation alone with verified success.

Some semantic commands require multiple Goodreads writes. Each completed step MUST be verified independently. If a later step fails, the command MUST perform one final safe readback when possible and report a typed partial-mutation error containing only verified, non-sensitive state. It MUST NOT automatically roll back, replay, or claim that no change occurred.

## Browser behavior

`gr login` launches the CLI-owned profile in headed mode. The user completes any Goodreads/Amazon/social-provider/MFA flow directly in the browser. The CLI only detects that authenticated Goodreads state has been reached.

Ordinary commands reuse that profile and run headless by default. A global headed flag is available for troubleshooting and compatibility work. All browser operations are serialized per profile.

The browser adapter MUST NOT attach to the user's normal browser profile, enter credentials, bypass challenges, or defeat anti-automation controls. If Goodreads blocks the chosen mode, surface an actionable error.

## Mobile and portability

The CLI runs where the browser and executable run. Local stdio MCP can use the same profile.

Remote/headless deployment is deferred because transferring a browser profile is security-sensitive and authentication providers may require a real interactive browser. The architecture must not preclude a future single-account deployment, but v0.1 makes no remote-browser promise.

## Success criteria for the first useful release

A clean machine can install the binary and then:

1. run `gr login`, authenticate in a visible dedicated Chromium window, and retain the session without giving the CLI a password;
2. close that window and reuse the dedicated profile on later commands;
3. run `gr library --shelf currently-reading --json` against live Goodreads state;
4. run `gr add <isbn>`;
5. run `gr start <isbn>`;
6. run `gr finish <isbn> --rating 4`;
7. run `gr rate <isbn> 5`;
8. run `gr review <isbn> --text ...`;
9. receive verified results for every successful mutation;
10. run the same application operations through local MCP stdio.

If a Goodreads UI path cannot be driven and verified reliably, that operation remains unsupported until the compatibility contract is updated. Do not silently fall back to an undocumented API or bulk CSV import.
