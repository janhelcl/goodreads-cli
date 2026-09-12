# Security and configuration specification

## Threat model

The sensitive asset is the authenticated Goodreads session. Possession of it may allow reading and mutating the user's Goodreads library without their password.

The tool also transiently handles:

- Goodreads password during login;
- CSRF tokens;
- full Goodreads library exports, including reviews/private notes if present;
- remote MCP bearer token when HTTP MCP is enabled.

Treat all of these as secrets/private data.

## Password handling

- password MUST be read from a no-echo terminal prompt for interactive login;
- password MUST NOT be accepted via a normal positional argument or `--password` flag;
- password MUST NOT be logged;
- password MUST NOT be written to disk;
- password MUST be discarded after authentication completes;
- tests use synthetic credentials only.

For headless automation, if a password-based login mode is ever necessary, use a documented secret stdin/file-descriptor/environment mechanism with an explicit warning. Prefer provisioning session material instead.

## Session persistence

Desktop/local default:

- store session material in the operating system's credential/keychain service behind a `SessionStore` interface;
- store only the minimum cookies/session fields needed;
- never silently fall back to an unencrypted plaintext session file.

Headless/container mode:

- support loading serialized session material from an explicit environment variable or secret file;
- the exact format must be versioned, e.g. `gr-session-v1`;
- documentation must treat it like a password;
- secret-file permissions should be checked where the platform supports it.

The application config directory may contain non-secret metadata and lock files, not Goodreads cookies unless the user explicitly opts into a documented fallback in a future release.

## Session serialization

Do not expose raw cookie jar internals as unstable Go gob data.

Use an explicit versioned structure, for example:

```json
{
  "version": 1,
  "cookies": [
    {
      "name": "...",
      "value": "...",
      "domain": ".goodreads.com",
      "path": "/",
      "expires": "...",
      "secure": true,
      "http_only": true
    }
  ]
}
```

The serialized value itself is secret and should be encrypted by the OS keychain at rest when stored locally.

## Configuration precedence

Keep configuration minimal. Suggested precedence:

1. explicit CLI flags
2. environment variables
3. small config file for non-secret defaults
4. built-in defaults

Do not create a config file until there is a real setting to persist.

Likely environment variables for headless use:

```text
GOODREADS_CLI_SESSION       serialized secret session
GOODREADS_CLI_MCP_TOKEN     bearer token for remote MCP
GOODREADS_CLI_TIMEOUT       optional operation timeout
```

Names are provisional until first release; once documented, keep them stable.

## Logs

Default logging is minimal and never contains library data.

Always redact:

- `Cookie`
- `Set-Cookie`
- `Authorization`
- CSRF tokens
- password/form credential fields
- serialized session payloads
- raw request/response bodies from authenticated Goodreads pages
- review/private-note text

Debug logging may include:

- URL origin + path (without secret query parameters)
- HTTP method
- status code
- duration
- retry/poll attempt
- parsed compatibility stage/state
- CSV row/header counts

It should not dump rows.

## CSV privacy

A full export is personal data. Normal commands should keep it in memory.

`gr export --out` is an explicit user request and may write a file. Use normal user permissions and do not copy it elsewhere.

Temporary files, if unavoidable:

- restrictive permissions;
- OS temp directory;
- randomized names;
- cleanup on success and ordinary failure;
- no predictable shared path.

## Remote MCP security

`gr mcp --http` MUST require client authentication unless bound only to a loopback interface.

Initial acceptable model:

- loopback binding: may run without bearer auth by explicit local-only default;
- non-loopback binding: refuse startup unless an MCP bearer token/approved auth mechanism is configured.

Remote MCP token and Goodreads session are separate secrets. Never return either through an MCP tool.

TLS termination may be provided by the hosting platform/reverse proxy. Document that internet-facing deployments require HTTPS.

## No telemetry by default

Initial releases send no analytics, crash reports, library metrics, or usage telemetry to the project maintainer.

If telemetry is ever proposed, it requires a separate product decision and explicit opt-in design.

## Supply-chain expectations

- keep dependencies few;
- prefer standard library for HTTP/CSV;
- pin Go module versions;
- CI runs vulnerability/dependency scanning where practical;
- release binaries should be checksummed; signing can be added once distribution is established.
