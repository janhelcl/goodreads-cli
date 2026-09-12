# MCP specification

MCP is an adapter over the application core, not a separate Goodreads implementation.

## Goals

- make the same semantic operations available to Claude and other MCP clients;
- keep tool schemas small enough that agents use them correctly;
- preserve Goodreads as the source of truth;
- support local stdio first;
- leave a straightforward path to a private remote HTTP MCP instance for mobile clients.

## Commands

### Local stdio

```text
gr mcp
```

Runs an MCP server over stdio. No logs or progress may be written to stdout; diagnostics go to stderr.

### Remote HTTP (later slice)

```text
gr mcp --http :8080
```

Runs the same server over the MCP-recommended HTTP transport supported by the chosen Go SDK.

Remote HTTP is single-account-per-process. This project does not implement a multi-user Goodreads SaaS.

## MCP tools

Keep the tool surface semantic and close to the CLI/application API.

### `get_library`

Input:

```json
{
  "shelf": "currently-reading",
  "rating": 5,
  "limit": 20
}
```

All fields optional. Always based on a fresh Goodreads export.

### `get_book`

Input:

```json
{"isbn": "9781603580557"}
```

### `add_book`

Input:

```json
{
  "isbn": "9781603580557",
  "status": "to-read"
}
```

`status` defaults to `to-read` only if MCP schema/default semantics make that obvious to clients.

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

`date` and `rating` optional according to the same semantics as CLI. Prefer requiring explicit `date` from remote agents if server timezone would make "today" ambiguous; see implementation note below.

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

To clear, use an explicit boolean/nullable field rather than conflating missing input with an empty review.

## Book discovery is not an MCP tool

Do not add `search_goodreads`, `get_average_rating`, recommendation tools, or Goodreads public-page scraping. MCP clients with web/search capability should resolve titles and public information externally, then call these tools with an ISBN.

## Results

Return compact structured data. The application result type should be reused/mapped directly; do not generate conversational prose in the server.

Example mutation result:

```json
{
  "ok": true,
  "operation": "finish",
  "isbn13": "9781603580557",
  "title": "Thinking in Systems",
  "changes": {
    "status": "read",
    "rating": 4,
    "date_read": "2026-09-12"
  },
  "verified": false
}
```

MCP errors map from typed application errors with safe, actionable messages.

## Authentication to Goodreads

Local stdio uses the same local session store as CLI.

Remote/headless deployments may load serialized Goodreads session material from an explicit secret environment/file configuration described in `06-security-and-config.md`. The process represents one Goodreads account.

MCP-level client authentication for remote HTTP is separate from Goodreads authentication. A remote server MUST NOT be exposed unauthenticated. Initial remote mode may use a static bearer token supplied as a deployment secret; full OAuth/multi-user account management is out of scope.

## Date/time semantics

CLI `finish` can default date to the local machine's current date.

For remote MCP, server timezone may differ from the user's phone. Therefore the MCP tool SHOULD require/strongly encourage an explicit ISO date for `finish_reading`. If omitted, document that server local date is used. A future client-timezone setting is preferable to inferring locale.

## Write safety

MCP descriptions should clearly state which tools mutate Goodreads. The server does not implement an extra confirmation protocol; confirmation/approval is a client concern.

Mutation tool descriptions MUST say exactly what fields change and that other library fields are preserved.

## Deployment philosophy

`gr mcp --http` exists so the same binary can be deployed to a small host such as Render and then used from a mobile MCP client. Do not add deployment-provider-specific logic to the core application.

Optional future assets (`render.yaml`, container image, install docs) belong at the packaging/deployment edge.
