# goodreads-cli

[![CI](https://github.com/janhelcl/goodreads-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/janhelcl/goodreads-cli/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/janhelcl/goodreads-cli)](https://github.com/janhelcl/goodreads-cli/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Manage your Goodreads library from the command line—or give the same capabilities to an AI agent through MCP.

`goodreads-cli` works with Goodreads as the only source of truth. It keeps no local book database, cache, or synchronization ledger. Reads and writes happen live through Goodreads' web interface using a dedicated browser profile, and every successful mutation is read back and verified.

> [!NOTE]
> This is an unofficial project. It is not affiliated with or endorsed by Goodreads or Amazon.

## What it can do

- sign in through a user-controlled browser window and reuse the saved session;
- list and filter your Goodreads shelves;
- find an exact edition by ISBN-10 or ISBN-13;
- add a book, start or finish reading it, rate it, and write or clear a review;
- generate a fresh Goodreads CSV export;
- return deterministic JSON for scripts and agents;
- expose the same live operations through a local stdio MCP server.

The CLI deliberately does not provide title search, recommendations, or a local library mirror. Resolve a book's ISBN elsewhere, then use that exact ISBN here.

## Requirements

- Linux, macOS, or Windows on AMD64 or ARM64;
- Google Chrome, Chromium, or Microsoft Edge installed locally;
- a Goodreads account;
- a visible desktop session for the initial login.

If browser discovery fails, set `GOODREADS_CLI_BROWSER` to the browser executable's absolute path.

## Install

Download the archive for your operating system and architecture from [GitHub Releases](https://github.com/janhelcl/goodreads-cli/releases). Verify it against `checksums.txt`, extract it, and place `gr` (`gr.exe` on Windows) somewhere on your `PATH`.

Confirm the installation:

```console
gr --version
```

## Quick start

Sign in once. Credentials are entered only on Goodreads/Amazon pages in the dedicated browser window; the CLI does not collect them.

```console
gr login
gr status
```

Read your library:

```console
gr library
gr library --shelf currently-reading
gr library --shelf read --rating 5 --limit 50
gr get 9781603580557
```

Update Goodreads:

```console
gr add 9781603580557
gr add 9781603580557 --shelf currently-reading
gr start 9781603580557
gr finish 9781603580557 --date 2026-09-24 --rating 5
gr rate 9781603580557 4
gr review 9781603580557 --text "A concise review."
gr review 9781603580557 --clear
```

Generate a Goodreads CSV export:

```console
gr export --out goodreads-library.csv
```

Use `--force` to replace an existing regular file. Without `--out`, `gr export` writes the CSV to stdout; JSON mode requires `--out`.

## Commands

| Command | Purpose |
|---|---|
| `gr login` | Open a dedicated browser window and save the authenticated session |
| `gr status` | Check whether the saved session is valid |
| `gr logout` | Stop the dedicated browser and delete its profile |
| `gr library` | List live shelf entries, optionally filtered by shelf or rating |
| `gr get <isbn>` | Find one exact ISBN in your library |
| `gr add <isbn>` | Add an exact edition; defaults to `to-read` |
| `gr start <isbn>` | Set and verify `currently-reading` |
| `gr finish <isbn>` | Set and verify `read`, finish date, and optional rating |
| `gr rate <isbn> <1-5>` | Set and verify a rating |
| `gr review <isbn>` | Set a review with `--text` or `--file`, or clear it with `--clear` |
| `gr export` | Generate and download a fresh Goodreads CSV export |
| `gr mcp` | Run the local MCP server over stdio |

Run `gr <command> --help` for all command-specific options.

### Global options

| Option | Purpose |
|---|---|
| `--json` | Emit one machine-readable JSON result |
| `--timeout <duration>` | Override the command's total timeout, for example `3m` |
| `--headed` | Show the browser for troubleshooting |
| `--debug` | Emit redacted operational diagnostics to stderr |
| `--no-color` | Disable terminal colors |

Global options may appear before or after the command:

```console
gr --json library --shelf read
gr get 9781603580557 --json
gr --headed --debug get 9781603580557
```

Debug output contains stable flow stages, timings, browser version, error category, and retry count. It excludes ISBNs, book and account identity, review text, authenticated URLs and HTML, cookies, browser storage, export contents, and profile paths.

## JSON and exit codes

Successful mutations return `"verified": true`. The CLI does not report success until Goodreads has been read back and the requested state has been confirmed.

Errors are written to stderr. Stable exit codes make the CLI suitable for scripts and agents:

| Code | Meaning |
|---:|---|
| `0` | Success |
| `1` | Unexpected internal error |
| `2` | Invalid command, argument, or option |
| `3` | Authentication, session, or browser-launch failure |
| `4` | Book not found or ISBN not resolved |
| `5` | Mutation ambiguous, partially completed, or verification failed |
| `6` | Timeout, network failure, or incomplete bounded scan |
| `7` | Goodreads UI compatibility changed |
| `8` | Another operation holds the browser profile lock |
| `9` | Supported browser unavailable |

If a multi-step mutation fails after a verified write, the error reports the completed steps and safe observed state. The CLI never silently rolls back, replays, or claims that Goodreads was unchanged.

## MCP

Run `gr login` first, then configure an MCP client to launch the same binary over stdio:

```json
{
  "mcpServers": {
    "goodreads": {
      "command": "/absolute/path/to/gr",
      "args": ["mcp"]
    }
  }
}
```

The server exposes:

- `get_library`
- `get_book`
- `add_book`
- `start_reading`
- `finish_reading`
- `rate_book`
- `review_book`

MCP uses the same browser profile, exact-ISBN resolution, profile lock, mutation verification, and error taxonomy as the CLI. Tool calls are serialized. Login remains an interactive CLI operation, and remote HTTP MCP is not supported.

## Safety model

- **User-controlled authentication:** login happens in the browser; the CLI never receives your password.
- **Dedicated profile:** automation is isolated from your normal browser profile.
- **Exact identity:** mutations require a valid ISBN-10 or ISBN-13 and never fall back to title matching.
- **Verified writes:** every successful mutation is freshly read back from Goodreads.
- **No hidden sync state:** Goodreads remains authoritative; there is no local library database or mutation queue.
- **No private API:** the project uses visible Goodreads pages and controls.
- **Conservative failures:** incomplete identity scans and uncertain writes fail explicitly instead of guessing or retrying automatically.

## Current limitations

- Goodreads has no supported public API for these operations, so UI changes can temporarily break compatibility.
- An installed Chrome, Chromium, or Edge is required; automatic browser downloads are disabled.
- Book discovery and recommendations are outside the project. Commands operate on exact ISBNs.
- MCP is local stdio only and uses the current user's browser profile.
- Full live Goodreads UAT currently covers Linux x86-64 with Chrome. macOS and Windows builds and profile-process cleanup run in native CI, but complete browser-backed Goodreads flows have not yet been validated there.

See the [compatibility matrix](specs/compatibility-matrix.md) and latest [sanitized UAT record](specs/uat-2026-09-22.md) for the precise tested scope.

## Development

Go 1.25 or newer is required.

```console
go test ./...
go test -race ./...
go vet ./...
```

Local browser integration tests use synthetic pages and do not contact Goodreads:

```console
GOODREADS_BROWSER_TESTS=1 \
GOODREADS_CLI_BROWSER=google-chrome \
go test -count=1 ./internal/browser ./internal/goodreads
```

The implementation contract, architecture, security rules, testing strategy, and recorded decisions live in [`specs/`](specs/README.md). Read those documents before changing product behavior. Live Goodreads probes are opt-in development tools and must use a dedicated, non-critical account.

## License

[MIT](LICENSE)
