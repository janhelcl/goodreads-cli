# goodreads-cli

`goodreads-cli` is a Go command-line client for managing a personal Goodreads library through Goodreads' web UI.

The project deliberately keeps Goodreads as the only source of truth. It does not maintain a local book database or synchronization ledger. A dedicated Chromium profile stores the signed-in browser session; every read and write is performed live through browser automation.

## Direction

- Go + Cobra CLI, distributed as a single cross-platform executable
- [Rod](https://github.com/go-rod/rod) as the initial Go browser-automation library
- visible, user-controlled login on Goodreads' own pages
- headless-by-default automation after login, with a headed troubleshooting mode
- semantic commands such as `library`, `add`, `start`, `finish`, `rate`, and `review`
- deterministic `--json` output for scripts and agents
- MCP as a later, thin adapter over the same application core
- no private Goodreads API, password collection, local library database, or background sync

CSV export remains an explicit user-facing escape hatch. CSV import/export is not the implementation path for ordinary library reads or mutations.

The implementation contract lives in [`specs/`](specs/README.md). Start there before changing product behavior.

## Implementation status

The dedicated browser profile, OS lock, and Rod launcher are implemented. On a
Linux machine with Chrome, manual sign-in persisted across a headless restart.
`gr login`, `status`, `logout`, `library`, `get <isbn>`, and the verified
mutations `add <isbn>`, `start <isbn>`, `finish <isbn>`, and
`rate <isbn> <rating>`, and `review <isbn>` are implemented with `--json`
output. `gr export` generates a fresh Goodreads CSV, validates the browser
download, and either writes it to stdout or atomically installs `--out`.
Authentication, populated/filtered shelf reads, and reversible live canaries
for add, status, rating, finish date, and review passed. Exact lookup remains
fail-closed when any scanned row omits its ISBN. The
[compatibility matrix](specs/compatibility-matrix.md) records the tested scope
and remaining gates.

For development with Go installed, run `go run . status --json` to check the
saved session, then `go run . library --json` to read the live shelf. `get` and
`rate` accept an exact ISBN-10 or ISBN-13; ratings must be 1 through 5.
`start` sets an exact library edition to `currently-reading`. `logout` deletes
the CLI-owned browser profile, including its saved sign-in session. `finish`
sets `read`, records `--date YYYY-MM-DD` (defaulting to today), and optionally
sets `--rating 1..5`; every requested field is read back before success.
`add` defaults to `to-read` and accepts `--shelf currently-reading|read`.
`review` requires exactly one of `--text`, `--file`, or `--clear` and verifies
the complete saved text without including review contents in errors.
`export --out library.csv` refuses to replace an existing file unless
`--force` is explicit; JSON mode requires `--out`.

For now, use an installed Chrome, Chromium, or Edge. `GOODREADS_CLI_BROWSER`
may point to a supported executable for development; Rod-managed browser
downloads remain disabled pending the first-run compatibility experiment.
