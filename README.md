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
