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

## Install

Release archives are published for 64-bit Intel/AMD and ARM Linux, macOS, and
Windows on the [GitHub Releases](https://github.com/janhelcl/goodreads-cli/releases)
page. Linux and macOS use `.tar.gz`; Windows uses `.zip`. Download the matching
archive and `checksums.txt`, verify its SHA-256 checksum, then place `gr`
(`gr.exe` on Windows) on your `PATH`.

The CLI requires an installed Google Chrome, Chromium, or Microsoft Edge.
Automatic browser downloads are intentionally disabled until their first-run
and compatibility behavior has been validated. If discovery fails, set
`GOODREADS_CLI_BROWSER` to the browser executable's absolute path.

Run `gr --version` to verify the installation, then `gr login` to sign in in
the dedicated browser window. Packaged binaries are cross-compiled for all six
OS/architecture combinations; the current live runtime evidence is limited to
Linux x86-64 with Chrome, as recorded in the
[compatibility matrix](specs/compatibility-matrix.md).

## Implementation status

The dedicated browser profile, OS lock, and Rod launcher are implemented. On a
Linux machine with Chrome, manual sign-in persisted across a headless restart.
`gr login`, `status`, `logout`, `library`, `get <isbn>`, and the verified
mutations `add <isbn>`, `start <isbn>`, `finish <isbn>`, and
`rate <isbn> <rating>`, and `review <isbn>` are implemented with `--json`
output. `gr export` generates a fresh Goodreads CSV, validates the browser
download, and either writes it to stdout or atomically installs `--out`.
`gr mcp` runs a local stdio MCP server exposing the same live reads and
verified mutations through semantic tools; login remains a separate
interactive CLI command.
Version tags produce cross-platform release archives and a SHA-256 checksum
manifest. Pull requests and `main` run formatting, vet, Staticcheck, tests,
live-probe compilation, race detection, vulnerability scanning, six target
builds, and local-only Chrome integration flows before release packaging
repeats those gates.
Authentication, populated/filtered shelf reads, and reversible live canaries
for add, status, rating, finish date, and review passed. Exact lookup treats a
unique owner ISBN match as conclusive when no other row shares that book ID,
and otherwise locates the edition by a visible public ISBN book ID.
Exact-edition operations use a separate 100-page safety budget; exhausting it
returns `scan_incomplete` and guarantees that no mutation was attempted.
Compound `add` and `finish` operations report verified completed steps and one
reconciled safe final state when a later step fails, and never roll back or
replay automatically. The
[compatibility matrix](specs/compatibility-matrix.md) records the tested scope
and remaining gates.

For development with Go 1.25 or newer installed, run
`go run . status --json` to check the saved session, then
`go run . library --json` to read the live shelf. `get` and `rate` accept an
exact ISBN-10 or ISBN-13; ratings must be 1 through 5.
`start` sets an exact library edition to `currently-reading`. `logout` deletes
the CLI-owned browser profile, including its saved sign-in session. `finish`
sets `read`, records `--date YYYY-MM-DD` (defaulting to today), and optionally
sets `--rating 1..5`; every requested field is read back before success.
`add` defaults to `to-read` and accepts `--shelf currently-reading|read`.
`review` requires exactly one of `--text`, `--file`, or `--clear` and verifies
the complete saved text without including review contents in errors.
`export --out library.csv` refuses to replace an existing file unless
`--force` is explicit; JSON mode requires `--out`.

Run `go run . mcp` from an MCP client configuration after `gr login`. The
server exposes `get_library`, `get_book`, `add_book`, `start_reading`,
`finish_reading`, `rate_book`, and `review_book`; it serializes tool calls and
returns safe typed errors when authentication or compatibility fails.

For now, use an installed Chrome, Chromium, or Edge. `GOODREADS_CLI_BROWSER`
may point to a supported executable; Rod-managed browser downloads remain
disabled pending the first-run compatibility experiment.

Use `--debug` for redacted operational events containing only stable operation
and flow-stage names, elapsed time, browser product/version, error kind, and
safe retry count. Debug output omits ISBNs, book/account identity, reviews,
selectors, authenticated URLs/HTML, browser state, export rows, and profile
paths.
