# CLI contract

Examples use `gr` as the provisional binary name. See `08-decisions.md`.

## UX rules

- Human mode is concise and readable.
- `--json` is deterministic and intended for agents/scripts.
- Commands that can mutate Goodreads MUST never infer missing semantic choices from prose.
- Passwords/tokens MUST never be accepted as normal command-line arguments because shell history/process listings expose them.
- Non-auth commands MUST be non-interactive. Missing required input is an error.
- stdout is for command results; stderr is for diagnostics/progress.
- `--json` MUST emit exactly one valid JSON value to stdout on success and no decorative prose.

## Global flags

Initial set:

```text
--json             machine-readable output
--debug            redacted diagnostic logging to stderr
--timeout DURATION total operation timeout
--no-color         disable terminal colors in human mode
```

Do not add flags speculatively.

## Authentication commands

### `gr login`

Interactive command that establishes a Goodreads session.

```text
gr login
```

Requirements:

- prompt for email/username as needed;
- prompt for password without echo;
- password exists only for the login request and is never persisted;
- on success, persist authenticated session material via the session store;
- validate the session against an authenticated Goodreads page before reporting success;
- if the account uses an unsupported auth mechanism, fail clearly and point to the limitation;
- `--json` may be supported only if credentials come from safe stdin/environment mechanisms documented for headless use; do not prompt and then mix prompt text with JSON stdout.

### `gr logout`

Deletes locally stored session material. It need not invalidate all Goodreads sessions server-side unless a reliable logout endpoint is deliberately supported.

### `gr status`

Checks whether stored session material exists and whether Goodreads accepts it.

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

Do not expose cookie/session details.

## Library/read commands

### `gr export`

Explicit escape hatch to download the current raw Goodreads export.

```text
gr export --out goodreads_library_export.csv
```

- always fetch a new export;
- refuse to overwrite an existing path unless `--force` is provided;
- without `--out`, write CSV to stdout only when not using `--json`;
- `--json` requires `--out` and returns metadata about the saved export, not CSV embedded in JSON.

### `gr library`

Fetches a new export and prints parsed books.

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

Filtering happens locally over that invocation's fresh export.

Human output should be compact table/text. JSON output is an array of stable book objects:

```json
[
  {
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

Dates are ISO `YYYY-MM-DD`; missing dates are `null`.

### `gr get <isbn>`

Convenience query over a fresh export. Returns one library entry or not-found.

## Mutation commands

All mutation commands:

1. validate local arguments before network writes;
2. acquire the account operation lock;
3. fetch a fresh export;
4. preserve fields not explicitly being changed;
5. build the smallest compatible import document;
6. submit it;
7. report the Goodreads import outcome.

Do not silently retry a mutation after an ambiguous response; avoid duplicate or unintended state transitions.

### `gr add <isbn>`

```text
gr add 9781603580557
gr add 9781603580557 --shelf currently-reading
```

- default status: `to-read`;
- initial `--shelf` accepts only the three core statuses;
- if the book already exists, this behaves as an idempotent ensure-status operation **only if** the compatibility spike proves Goodreads import updates existing books reliably;
- never erase rating/review/date/custom shelves as a side effect.

### `gr start <isbn>`

```text
gr start 9781603580557
```

Sets status to `currently-reading` and preserves other user state.

It does **not** promise a started-reading date because current Goodreads export data does not expose one reliably.

### `gr finish <isbn>`

```text
gr finish 9781603580557
gr finish 9781603580557 --rating 4
gr finish 9781603580557 --date 2026-09-11 --rating 4
```

- status becomes `read`;
- `--date` format is `YYYY-MM-DD`;
- omitted `--date` means the machine's current local calendar date;
- optional `--rating` is 1..5;
- existing review/custom shelves are preserved.

### `gr rate <isbn> <rating>`

```text
gr rate 9781603580557 5
```

Changes only `My Rating`.

### `gr review <isbn>`

```text
gr review 9781603580557 --text "Excellent systems primer."
gr review 9781603580557 --file review.md
gr review 9781603580557 --clear
```

Exactly one of `--text`, `--file`, or `--clear` is required.

Changes only the review field.

## Mutation result

Human output should state what Goodreads accepted, e.g.:

```text
Updated Thinking in Systems — read, 4/5, finished 2026-09-12
```

JSON uses a stable result object:

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
  "verified": false
}
```

`verified` means a post-import re-export confirmed the state, not merely that the import request was accepted. Default verification behavior is tracked in `08-decisions.md`.

## Exit codes

Keep these stable once released:

```text
0 success
2 CLI usage / validation error
3 authentication/session error
4 book not found / ISBN cannot be resolved in current library context
5 Goodreads import rejected the requested mutation
6 network/remote service failure
7 Goodreads compatibility/schema drift
8 operation busy/locked
1 unexpected internal error
```

MCP maps the same application error taxonomy rather than shell exit codes.

## Raw import command

Do **not** expose a general `gr import arbitrary.csv` in the initial public interface. It bypasses semantic safety and is unnecessary for the target use case. A hidden/dev-only command MAY exist for compatibility testing.

## Agent guidance

The CLI should eventually document this contract explicitly:

- use web/public sources to identify the intended book and ISBN;
- use `gr` only for the user's private Goodreads library state;
- prefer `--json`;
- do not parse human-formatted tables;
- do not call Goodreads public pages through this tool;
- on compatibility/auth errors, surface the error rather than switching to browser automation.
