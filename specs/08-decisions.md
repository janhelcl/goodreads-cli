# Decisions, assumptions, and open questions

This file separates deliberate product choices from Goodreads/browser behavior that must be measured.

## Confirmed decisions

These choices should not be reopened during routine implementation.

| Area | Decision |
|---|---|
| Language | Go |
| CLI framework | Cobra |
| Browser automation | Rod behind a narrow local interface |
| Product core | CLI/application library first |
| Source of truth | Goodreads only |
| Local library DB/cache | None |
| Sync engine/queue | None |
| Goodreads integration | Browser automation of the ordinary web UI for reads and writes |
| Authentication | Manual login in a visible, dedicated persistent Chromium profile |
| Password handling | CLI never collects, types, stores, or logs credentials |
| Session model | Browser-owned profile; no copied cookie jar or serialized-session format |
| Normal operation mode | Headless by default; `--headed` runs the same flow visibly |
| Mutation success | Mandatory Goodreads readback; successful results have `verified=true` |
| Private Goodreads API | Prohibited |
| CSV | Explicit export only; not normal read/write state transport |
| Public book metadata/search | Out of scope; caller resolves an ISBN |
| Mutation identity | ISBN-10/ISBN-13 |
| Read freshness | Goodreads pages loaded during each invocation |
| Concurrency | Exclusive OS-backed lock per browser profile |
| MCP | Thin adapter over the same application core |
| Local MCP | stdio via the same binary |
| Remote MCP | Deferred pending a browser/profile security design |
| Hosted model | No multi-user SaaS |
| Telemetry | None by default |
| Large-library lookup | Exact operations must not depend on a small fixed full-shelf page limit; budget exhaustion is a typed incomplete scan |
| Compound mutation failure | Final readback plus typed partial-mutation error; no automatic rollback or replay |

## Superseded decisions

The following earlier design is no longer authoritative:

- temporary browser profile used only for login;
- extracting Goodreads cookies into a Go HTTP client/keychain;
- ordinary HTTP import/export as the library boundary;
- CSV re-import for mutations;
- browser automation prohibited after login;
- optional post-import verification.

Current specs replace that design comprehensively. Historical git commits may still describe it but implementation must follow this file and the other current specs.

## Provisional defaults

These defaults may be implemented without blocking on product discussion, but the compatibility spike can refine them.

### D1 — binary name

Use `gr` in examples and initial builds unless a clear packaging/path collision appears. The repository/module remains `goodreads-cli`.

If `gr` is too collision-prone, rename before public compatibility guarantees are published.

### D2 — browser selection

Resolution order:

1. explicit configured executable;
2. installed Chrome, Chromium, or Edge;
3. Rod-managed Chromium if first-run download and compatibility prove acceptable.

Support Firefox/Safari is deferred. Pin the Rod version and document the Chromium strategy.

### D3 — persistent profile

Use one CLI-owned profile under the platform application-data root.

The profile persists until `gr logout`, contains browser-managed authentication state, and is protected by restrictive permissions plus an exclusive OS lock. Do not extract cookies into a keychain or support a plaintext session fallback.

### D4 — browser visibility

`gr login` is always headed. All other commands are headless by default and accept `--headed` for observation/troubleshooting.

Headed and headless modes must execute the same Goodreads flow and verification logic.

### D5 — mutation verification

Readback verification is mandatory, not optional.

A mutation may return verified success when the desired state was already present without clicking. Once a mutating action has been attempted, an ambiguous response is followed by one fresh state read, never a blind replay.

### D6 — custom shelves

Core editing supports only `to-read`, `currently-reading`, and `read` initially.

Existing custom shelves should be displayed/preserved when the UI exposes them. Editing custom shelves is deferred until explicitly specified and tested.

### D7 — MCP deployment

Local stdio is in scope. Remote HTTP is not a v0.1 deliverable.

Do not support cookie environment variables or copied profile archives as an undocumented remote-provisioning shortcut.

## Blocking empirical questions

The compatibility spike in `07-testing-and-delivery.md` must answer:

1. **Browser matrix:** which Chrome/Chromium/Edge versions and platforms work with the selected Rod release and profile strategy?
2. **Managed browser:** is Rod-managed Chromium a reliable, safe fallback, and what is its first-run installation cost?
3. **Login persistence:** can manual login in the dedicated headed profile be reused headlessly after restart?
4. **Authentication detection:** which combined page markers distinguish valid, expired, challenged, and incompatible states?
5. **Shelf reads:** which Goodreads pages/selectors provide stable live library data and pagination?
6. **Field visibility:** where are ISBN, rating, status, review, custom shelves, and finish date visible for parsing and verification?
7. **Exact identity:** can the adapter prove an exact ISBN match for both existing and new books?
8. **Status mutation:** which UI control reliably sets each core exclusive shelf?
9. **Rating mutation:** which control and readback source reliably set/verify a numeric rating?
10. **Review mutation:** which editor and readback source preserve Unicode and support explicit clearing?
11. **Finish date:** can the ordinary UI set and expose a requested finish date reliably?
12. **Preservation:** can each narrow UI operation prove unrelated rating/review/date/custom-shelf fields remain unchanged?
13. **Ambiguity:** what page markers and readback behavior distinguish failure, delayed success, and selector drift after a click?
14. **Export:** how does the UI request a fresh CSV and how can the resulting browser download be tied to the invocation?
15. **Headless challenges:** does Goodreads present challenges in headless mode that require rerunning headed?
16. **Targeted owner lookup:** can the visible owner-library UI locate and prove one exact ISBN or stable edition ID without scanning every shelf page?
17. **Large-library bounds:** what page size and safety budget cover realistic libraries while retaining loop and runaway protection?

If #3 fails, stop and revisit the profile/browser approach. If #7 or #12 fails for an operation, do not ship that operation. If headless execution is unreliable but headed works, surface that result for a product decision rather than using evasion techniques.

## Accepted initial limitations

- ISBN-first mutations exclude books whose edition cannot be verified.
- Start date and reread history are not promised.
- Custom-shelf editing is absent.
- Goodreads social features are absent.
- Headless operation depends on Goodreads accepting the supported browser.
- Login and challenges require a local display and user interaction.
- UI changes may temporarily disable commands until contracts and fixtures are updated.
- Remote/mobile MCP is deferred.
- v0.1.0 exact lookup is bounded to ten shelf pages; v0.1.1 replaces that product limitation with the resolution contract above.

Do not introduce a local database or undocumented API to compensate for these limitations.

## Handling ambiguity

1. Check the current specs.
2. If the question is empirical Goodreads/browser behavior, extend the compatibility probe and sanitized fixture.
3. If it changes a confirmed decision, stop and surface the product tradeoff.
4. Prefer an explicit unsupported error over a hidden workaround.
5. Never bypass challenges, weaken browser security silently, or replay an ambiguous mutation.
