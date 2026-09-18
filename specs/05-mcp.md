# MCP specification

MCP is an adapter over the application core, not a separate Goodreads implementation.

## Goals

- expose the same semantic operations to MCP clients;
- keep tool schemas small and explicit;
- preserve Goodreads as the only source of truth;
- use the same dedicated browser profile and verification rules as the CLI;
- support local stdio first.

## Local stdio command

```text
gr mcp
```

Runs an MCP server over stdio. Protocol messages are the only stdout content; diagnostics go to stderr.

The server may keep one browser process open for its lifetime to reduce launch overhead, but it MUST:

- use the same dedicated profile;
- serialize all tool calls;
- load Goodreads state live for every tool invocation;
- close the browser cleanly;
- avoid retaining a library cache between calls.

If another CLI/MCP process holds the profile lock, return the same structured busy error as the CLI.

## Authentication

The local MCP server uses the profile created by `gr login`.

It MUST NOT expose login as an MCP tool in v0.1 because login requires a local visible user-controlled browser. If the profile is missing or expired, tools return an actionable authentication error directing the user to run `gr login`.

MCP tools never return profile paths, cookies, browser storage, raw HTML, or credentials.

## MCP tools

Keep the surface semantic and close to the application API.

### `get_library`

Input:

```json
{
  "shelf": "currently-reading",
  "rating": 5,
  "limit": 20
}
```

All fields are optional. Results come from Goodreads pages loaded for that tool call.

### `get_book`

Input:

```json
{"isbn": "9781603580557"}
```

The result requires an exact normalized ISBN match.

### `add_book`

Input:

```json
{
  "isbn": "9781603580557",
  "status": "to-read"
}
```

`status` defaults to `to-read` only if the tool schema makes that default unambiguous to clients.

### `start_reading`

```json
{"isbn": "9781603580557"}
```

### `finish_reading`

```json
{
  "isbn": "9781603580557",
  "date": "2026-09-12",
  "rating": 4
}
```

`date` and `rating` follow the same semantics as the CLI. MCP descriptions should encourage an explicit date because the host timezone may differ from the user.

### `rate_book`

```json
{
  "isbn": "9781603580557",
  "rating": 5
}
```

### `review_book`

```json
{
  "isbn": "9781603580557",
  "review": "Excellent systems primer."
}
```

Clearing a review uses an explicit `clear: true` field or a distinct schema branch. Missing input never means clear.

## Book discovery is not an MCP tool

Do not add `search_goodreads`, average-rating, recommendation, or public-page scraping tools. MCP clients with web/search capability should resolve an ISBN externally, then call these private-library tools.

The adapter may use Goodreads' visible search UI internally only to resolve and verify the supplied exact ISBN.

## Results

Return compact structured data and map the application result directly. Do not generate conversational prose inside the server.

Example mutation result:

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

A successful mutation result always has `verified: true`. Ambiguous or mismatched state is a tool error.

Errors map from typed application errors and include safe, actionable messages. Compatibility errors may include the stable flow stage but not selectors, raw HTML, or private field contents.

`partial_mutation` tool errors include structured safe details: operation, completed semantic steps, failed step, observed status/rating/date when available, and `retry_automatically: false`. They never include review text, title, account identifiers, raw URLs, selectors, or HTML.

`scan_incomplete` means the adapter could not prove exact identity within its explicit safety budget and no mutation was attempted. Clients may ask the user to retry with a longer command timeout or a future supported scan-budget setting, but MUST NOT substitute fuzzy identity or a separate Goodreads script.

## Date/time semantics

CLI `finish` may default to the local machine's date.

MCP clients SHOULD send an explicit ISO date. If omitted, the server's local calendar date is used and that behavior must be stated in the tool description and result.

## Write safety

Tool descriptions MUST:

- identify mutating tools;
- state exactly which fields change;
- state that unrelated fields are preserved;
- explain that Goodreads is read back before success;
- avoid encouraging blind retries after ambiguity.

The server does not add a separate confirmation protocol; approval is the MCP client's responsibility.

## Remote HTTP transport

Remote HTTP MCP is deferred beyond v0.1.

A remote design must answer, before implementation:

- how a dedicated Chromium profile is provisioned and encrypted;
- whether the host can run the supported browser reliably;
- how interactive reauthentication occurs without exposing remote debugging;
- how one account/process is isolated;
- how MCP client authentication and HTTPS are enforced;
- how browser/profile persistence survives deployments safely.

Do not support serialized-cookie environment variables as a shortcut around the browser-owned profile model. Do not add provider-specific deployment logic to the application core.

If remote mode is approved later, it remains single-account-per-process and uses the same application service, live reads, exclusive browser access, and mandatory mutation verification.
