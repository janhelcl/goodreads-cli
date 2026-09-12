# Security and configuration specification

## Threat model

The sensitive asset is the authenticated Goodreads session. Possession of it may allow reading and mutating the user's Goodreads library without their password.

The tool also transiently handles:

- CDP access to the temporary login browser;
- CSRF tokens;
- full Goodreads library exports, including reviews/private notes if present;
- remote MCP bearer token when HTTP MCP is enabled.

The CLI MUST NOT collect or store the user's Goodreads password. Passwords and social-provider credentials are entered only into Goodreads/provider pages inside the launched browser.

Treat all session/authentication data and library exports as secrets/private data.

## Browser-assisted login security

`gr login` launches a supported Chromium-family browser with an isolated temporary profile.

Security requirements:

- never attach to the user's normal/default browser profile;
- never read cookies from an existing profile;
- never automate typing/clicking credentials;
- never inspect, record, or log password/form field contents;
- use a visible browser, not headless mode;
- bind remote debugging to loopback only;
- use an ephemeral/random debugging port where practical;
- keep CDP access alive only for the duration of the login ceremony;
- capture only cookies/session state needed for Goodreads authentication reuse;
- validate the captured session through the normal HTTP adapter before persisting it;
- persist only validated session material;
- terminate the launched browser and remove the temporary profile on success, failure, timeout, or cancellation where feasible;
- temporary profile directory permissions must be restrictive where supported by the OS;
- debug logs must never contain CDP cookie payloads or profile contents.

The browser bootstrap is an authentication mechanism only. It must not become an implementation path for Goodreads library operations.

## Session persistence

Desktop/local default:

- store session material in the operating system's credential/keychain service behind a `SessionStore` interface;
- store only the minimum cookies/session fields needed;
- never silently fall back to an unencrypted plaintext session file.

Headless/container mode:

- support loading serialized session material from an explicit environment variable or secret file;
- the exact format must be versioned, e.g. `gr-session-v1`;
- documentation must treat it like a password;
- secret-file permissions should be checked where the platform supports it;
- headless environments do not perform interactive browser login; session material is provisioned out of band.

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

Do not assume every browser cookie attribute maps one-to-one to Go's cookie jar. Preserve enough explicit metadata to reconstruct a functionally equivalent authenticated session and version the format for future changes.

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

Do not introduce an environment variable for Goodreads passwords.

## Logs

Default logging is minimal and never contains library data.

Always redact:

- `Cookie`
- `Set-Cookie`
- `Authorization`
- CSRF tokens
- serialized session payloads
- CDP cookie results
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
- browser discovery result (product/path/version), but not profile contents or cookies

It should not dump rows or browser storage.

## CSV privacy

A full export is personal data. Normal commands should keep it in memory.

`gr export --out` is an explicit user request and may write a file. Use normal user permissions and do not copy it elsewhere.

Temporary files, if unavoidable:

- restrictive permissions;
- OS temp directory;
- randomized names;
- cleanup on success and ordinary failure;
- no predictable shared path.

The temporary browser profile follows the same rules and should be treated as especially sensitive because it may contain authenticated browser state before cleanup.

## Remote MCP security

`gr mcp --http` MUST require client authentication unless bound only to a loopback interface.

Initial acceptable model:

- loopback binding: may run without bearer auth by explicit local-only default;
- non-loopback binding: refuse startup unless an MCP bearer token/approved auth mechanism is configured.

Remote MCP token and Goodreads session are separate secrets. Never return either through an MCP tool.

TLS termination may be provided by the hosting platform/reverse proxy. Document that internet-facing deployments require HTTPS.

Interactive `gr login` is a local-machine workflow. A remotely hosted/headless MCP process should be provisioned with a previously captured serialized session rather than exposing CDP/browser login remotely.

## No telemetry by default

Initial releases send no analytics, crash reports, library metrics, or usage telemetry to the project maintainer.

If telemetry is ever proposed, it requires a separate product decision and explicit opt-in design.

## Supply-chain expectations

- keep dependencies few;
- prefer standard library for HTTP/CSV;
- use the smallest maintained CDP/browser-launch dependency that cleanly supports browser discovery, temporary-profile launch, navigation, and cookie retrieval;
- do not introduce a full end-to-end browser automation framework solely for login;
- pin Go module versions;
- CI runs vulnerability/dependency scanning where practical;
- release binaries should be checksummed; signing can be added once distribution is established.
