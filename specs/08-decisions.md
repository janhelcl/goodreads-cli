# Decisions, assumptions, and open questions

This file distinguishes deliberate product choices from facts that still need to be measured against Goodreads.

## Confirmed decisions

These come from the agreed design and should not be reopened during routine implementation.

| Area | Decision |
|---|---|
| Language | Go |
| CLI framework | Cobra |
| Product core | CLI/application library first |
| Source of truth | Goodreads only |
| Local library DB | None |
| Sync engine | None |
| Goodreads library integration | Authenticated import/export over ordinary HTTP |
| Authentication | Browser-assisted login using a temporary isolated Chromium-family profile; capture reusable Goodreads session cookies via CDP |
| Password handling | CLI never collects or stores the user's Goodreads password |
| Browser automation | Allowed only for authentication/session acquisition; forbidden for Goodreads library operations |
| Supported login browsers V1 | Chrome, Chromium, Edge where discoverable |
| Public book metadata/search | Out of scope; caller/AI resolves ISBN via web |
| Mutation identity | ISBN-10/ISBN-13 |
| Read freshness | Fresh Goodreads export per query invocation |
| MCP | Thin adapter over same application core |
| Local MCP | stdio via same binary |
| Remote MCP | Later HTTP transport via same binary |
| Hosted model | Single Goodreads account per process/deployment; no multi-user SaaS |
| Distribution goal | Single cross-platform binary |
| Telemetry | None by default |

## Provisional defaults that Codex may implement

These are low-cost choices and do not need to block the compatibility spike.

### D1 — binary name

Use `gr` in command examples and as the initial built binary **unless a clear packaging/path collision is found**. Repository/module remains `goodreads-cli`.

If `gr` proves too collision-prone before first public release, rename before compatibility guarantees are published.

### D2 — mutation verification

Default mutation success means Goodreads explicitly reports the import accepted/completed, with `verified=false` unless a subsequent export was performed.

Provide a future/global `--verify` option or verification mode that re-exports and confirms requested fields. During compatibility/live tests, always verify.

Rationale: exporting can be asynchronous and expensive enough that doing it both before and after every mutation may make the UX unnecessarily slow. The application architecture must nevertheless support verification.

### D3 — custom shelves

Core status only (`to-read`, `currently-reading`, `read`) in the first slice. Preserve custom shelves when mutating an existing row, but do not expose custom-shelf editing until import semantics are proven.

### D4 — secure session persistence

Use OS credential/keychain storage for local sessions. Headless/remote deployments use an explicit secret environment variable/file. Do not silently persist cookies in plaintext.

Library choice is an implementation detail; prefer a maintained cross-platform package with minimal complexity.

### D5 — browser-assisted login implementation

Authentication coverage is now resolved: V1 should use a supported local Chromium-family browser rather than implementing Goodreads email/password HTTP login.

Implementation defaults:

- auto-detect Chrome, Chromium, or Edge;
- launch a visible browser with a dedicated temporary profile;
- remote debugging/CDP bound to loopback only;
- user performs the login manually on Goodreads/provider pages;
- no credential typing/click automation;
- capture only reusable Goodreads session cookies/state;
- validate the captured session through the normal HTTP adapter;
- store via the session abstraction/keychain;
- close the launched browser and remove the temporary profile after capture;
- no fallback that asks the CLI for a password;
- no use of browser/CDP for add/start/finish/rate/review/import/export.

Supporting Firefox/Safari is explicitly deferred. If Chromium-family login cannot reliably yield a reusable Goodreads HTTP session, stop and revisit the product decision rather than expanding browser automation.

## Blocking empirical questions — not product decisions

Codex must answer these through the compatibility spike in `07-testing-and-delivery.md`:

1. **Browser login/session reuse:** can a temporary Chrome/Chromium/Edge profile complete the current Goodreads login flow and yield cookies that can be serialized/restored into the Go HTTP client and accepted by the import/export pages?
2. **Fresh export:** what exact current workflow triggers a new export, how is completion detected, and can stale exports be distinguished?
3. **Minimal import:** what headers/fields does Goodreads currently accept for a one-book import?
4. **Existing-book updates:** does re-importing a one-row CSV reliably update rating, status, date read, and review for a book already in the library?
5. **Status encoding:** does current import honor `Exclusive Shelf`, `Bookshelves`, or another field for `to-read` / `currently-reading` / `read`?
6. **Preservation:** can a narrow mutation preserve unrelated rating/review/custom-shelf state?
7. **Import completion:** how does Goodreads signal queued, completed, rejected, or partially failed imports?
8. **Session lifetime:** which Goodreads cookies must be persisted, how long do they remain valid, and how is expiry represented?

If #1 fails, revisit authentication before proceeding. If #4 or #6 fails, stop: the central import/export mutation design is invalid and should be discussed before implementation continues.

## Known CSV limitations accepted by design

Current/historical Goodreads exports expose a useful but incomplete model. In particular, started-reading date and rich reread history are not part of the stable export contract we are targeting.

Therefore:

- `start` promises status change, not recording a start timestamp;
- `finish` can use `Date Read` if verified by compatibility tests;
- reread event history is out of scope;
- do not introduce a local DB to compensate for missing Goodreads CSV fields.

## What Codex should do when it encounters ambiguity

1. Check these specs first.
2. If it is an empirical Goodreads behavior, add/extend a compatibility test or dev probe; do not guess.
3. If it changes the product invariants above, stop and surface the decision.
4. Prefer a smaller explicit limitation over a hidden workaround.
