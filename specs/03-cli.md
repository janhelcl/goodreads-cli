# CLI contract

Examples use `gr` as the provisional binary name. See `08-decisions.md`.

## UX rules

- Human mode is concise and readable.
- `--json` is deterministic and intended for agents and scripts.
- Mutating commands MUST never infer missing semantic choices from prose.
- Commands other than `login` MUST be non-interactive.
- stdout is for results; stderr is for diagnostics and progress.
- `--json` emits exactly one valid JSON value to stdout on success and no decorative prose.
- The CLI MUST NOT ask for or accept a Goodreads password.
- Browser windows are hidden by default except during `login` or when `--headed` is explicit.
- A successful mutation means Goodreads state was read back and verified.

## Global flags

Initial set:

```text
--json              machine-readable output
--debug             redacted diagnostic logging to stderr
--timeout DURATION  total operation timeout
--headed            show the automated browser window
--no-color          disable terminal colors in human mode
```

`--headed` is a diagnostic/visibility switch, not a different implementation path. It has no effect on `login`, which is always headed.

A browser executable override MAY be introduced through configuration or a global `--browser PATH` flag once the compatibility spike shows it is needed. Do not expose the profile directory as a routine flag in v0.1.

## Authentication commands

### `gr login`

Interactive command that establishes a Goodreads session in the CLI-owned Chromium profile.

```text
gr login
```

Target human UX:

```text
Opening Goodreads in a dedicated browser...
Complete sign-in on Goodreads, then return here.

✓ Goodreads connected
```

Requirements:

- launch a supported installed Chromium-family browser or a Rod-managed Chromium;
- always use the dedicated profile, never the user's normal browser profile;
- use a visible browser;
- navigate to Goodreads sign-in;
- let the user complete email/password, Amazon/social-provider, MFA, and challenge flows themselves;
- do not inspect, collect, type, or submit credentials;
- detect authenticated Goodreads state and validate access to a private library page;
- retain the dedicated profile for later commands;
- close the launched browser on success, failure, cancellation, or timeout;
- fail actionably if no usable browser is available.

`--json` is allowed. Interaction stays in the browser, progress goes to stderr, and stdout contains only the final result.

```json
{
  "connected": true,
  "session_valid": true
}
```

### `gr logout`

```text
gr logout
```

Closes any browser process started by the command and deletes the dedicated profile after confirmation of the exact path owned by this application. It does not promise to invalidate other Goodreads sessions.

The command MUST be safe to repeat. In JSON mode:

```json
{
  "connected": false,
  "profile_removed": true
}
```

### `gr status`

Launches the dedicated profile, visits a lightweight authenticated Goodreads page, and reports whether the session is accepted.

Human example:

```text
Goodreads: connected
```

JSON shape:

```json
{
  "connected": true,
  "session_valid": true
}
```

Do not expose profile paths, cookies, or session details in ordinary output.

## Library/read commands

### `gr library`

Reads the user's live Goodreads shelf pages.

```text
gr library
gr library --shelf currently-reading
gr library --rating 5
gr library --limit 20 --json
```

Initial filters:

- `--shelf to-read|currently-reading|read`
- `--rating 1..5`
- `--limit N`

Shelf filtering SHOULD be applied through Goodreads navigation where practical. Other filtering may happen over rows loaded during that invocation. `--limit` bounds returned results and browser pagination; it must not imply a persistent cache.

Human output should be compact table/text. JSON is an array of stable book objects:

```json
[
  {
    "book_id": "12345",
    "title": "Thinking in Systems",
    "author": "Donella H. Meadows",
    "isbn10": "1603580557",
    "isbn13": "9781603580557",
    "rating": 4,
    "status": "read",
    "date_read": "2026-09-12",
    "review": ""
  }
]
```

Dates are ISO `YYYY-MM-DD`; missing dates are `null`. Fields Goodreads does not render in the traversed view may be absent or `null` only if the JSON schema documents that behavior.

### `gr get <isbn>`

Returns one live library entry or not-found.

```text
gr get 9781603580557
```

It MUST verify an exact normalized ISBN before returning a result. Title-only matches are insufficient.

### `gr export`

Explicit escape hatch that requests/downloads the current Goodreads CSV export through the web UI.

```text
gr export --out goodreads_library_export.csv
```

Requirements:

- initiate a new export or prove the downloaded file belongs to this invocation;
- wait with bounded polling for Goodreads to prepare it;
- refuse to overwrite an existing destination unless `--force` is present;
- without `--out`, write CSV to stdout only in human mode;
- with `--json`, require `--out` and return safe metadata, not CSV content;
- never use the export as hidden application state or as the normal read/mutation path.

## Mutation commands

All mutation commands:

1. validate local arguments before launching the browser;
2. acquire the profile lock;
3. load current Goodreads state needed for preservation checks;
4. resolve and verify the exact ISBN;
5. perform the narrowest Goodreads UI action;
6. wait for a recognized completion state;
7. reload/read back the affected state; and
8. report success only when requested fields match.

A command MUST NOT automatically retry an ambiguous form submission or click. It may safely re-read state and return either verified success or an ambiguity error.

For a compound command, failure after an earlier verified step is a partial mutation. The command performs one final readback when safe, exits non-zero, names the completed and failed steps without private content, and explicitly says not to retry automatically.

### `gr add <isbn>`

```text
gr add 9781603580557
gr add 9781603580557 --shelf currently-reading
```

- default status is `to-read`;
- `--shelf` accepts only the three core statuses;
- if the book already exists, the operation is an idempotent ensure-status action;
- rating, review, custom shelves, and dates MUST remain unchanged unless Goodreads itself necessarily normalizes them and the adapter reports that incompatibility.

### `gr start <isbn>`

```text
gr start 9781603580557
```

Sets status to `currently-reading` and preserves other user state.

It does not promise a started-reading date in v0.1.

### `gr finish <isbn>`

```text
gr finish 9781603580557
gr finish 9781603580557 --rating 4
gr finish 9781603580557 --date 2026-09-11 --rating 4
```

- status becomes `read`;
- `--date` is `YYYY-MM-DD`;
- omitted `--date` means the machine's current local calendar date;
- optional `--rating` is 1..5;
- existing review and custom shelves are preserved;
- the compatibility spike must establish the Goodreads UI needed to set the finish date reliably.

### `gr rate <isbn> <rating>`

```text
gr rate 9781603580557 5
```

Changes only the user's rating.

### `gr review <isbn>`

```text
gr review 9781603580557 --text "Excellent systems primer."
gr review 9781603580557 --file review.md
gr review 9781603580557 --clear
```

Exactly one of `--text`, `--file`, or `--clear` is required. The command changes only the review field. `--file` treats a single trailing newline (`\n` or `\r\n`) as a file terminator rather than review content, so ordinary editor-saved files verify. `--text` is used as supplied.

## Mutation result

Human output states the verified result:

```text
Updated Thinking in Systems — read, 4/5, finished 2026-09-12
```

JSON uses a stable object:

```json
{
  "ok": true,
  "operation": "finish",
  "isbn13": "9781603580557",
  "book_id": "12345",
  "title": "Thinking in Systems",
  "changes": {
    "status": "read",
    "rating": 4,
    "date_read": "2026-09-12"
  },
  "verified": true
}
```

`verified` MUST be `true` for a successful mutation. An unverified or mismatched outcome is an error, not a successful result with `verified=false`.

## Partial mutation error

A partial mutation is an error, not a success result. The application error exposes safe structured details to transport adapters:

```json
{
  "code": "partial_mutation",
  "operation": "finish",
  "completed": ["status", "date"],
  "failed": "rating",
  "observed": {
    "status": "read",
    "date_read": "2026-09-18",
    "rating": 3
  },
  "retry_automatically": false
}
```

Human CLI output states the same facts concisely. It MUST NOT include review text, raw Goodreads content, account identifiers, or selectors. The CLI uses exit code 5. Existing `--json` success behavior remains unchanged; a future general JSON error-envelope decision is outside v0.1.1.

An incomplete exact-identity scan is reported as `scan_incomplete`. It guarantees that no mutation was attempted and uses exit code 6.

## Exit codes

Keep these stable once released:

```text
0 success
1 unexpected internal error
2 CLI usage / validation error
3 authentication, session, or browser-launch error
4 book not found / ISBN not resolved
5 mutation ambiguous, partially completed, or verification failed
6 network / remote service failure or incomplete bounded scan
7 Goodreads UI compatibility drift
8 operation busy / profile locked
9 browser unavailable or unsupported
```

MCP maps the same application error taxonomy rather than shell codes.

## Browser diagnostics

When a compatibility failure occurs, the error SHOULD suggest rerunning the same command with `--headed --debug`.

`--debug` emits redacted operational events to stderr. At minimum it SHOULD report the operation, stable flow stage, elapsed duration, browser product/version, and typed error category. It MUST NOT emit selectors, raw URLs containing account identifiers, ISBN/title/review values, authenticated HTML, cookies, browser storage, or profile paths.

A future explicit diagnostic flag may capture a screenshot or sanitized DOM snapshot. It must be opt-in, warn that Goodreads pages contain personal data, and never write cookies or credential fields.

## Raw browser or import commands

Do not expose general-purpose commands that execute arbitrary JavaScript, selectors, URLs, or CSV imports. They bypass the semantic safety boundary.

Dev-only compatibility probes MAY exist behind build tags or an explicitly unstable command namespace and MUST NOT be enabled in release builds by default.

## Agent guidance

The CLI documentation should tell agents:

- resolve the intended book and ISBN using public sources before invoking this tool;
- use `gr` only for the user's private Goodreads library state;
- prefer `--json`;
- never parse human tables when JSON is available;
- treat auth, compatibility, ambiguity, and verification errors as terminal for that action;
- never compensate by scripting Goodreads separately or replaying a failed mutation.
