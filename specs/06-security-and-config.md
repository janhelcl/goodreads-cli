# Security and configuration specification

## Threat model

The most sensitive local asset is the dedicated Chromium profile. Possession of it may allow reading and mutating the user's Goodreads library without their password and may expose browser-managed session data.

The tool also transiently handles:

- authenticated Goodreads pages;
- CSRF tokens and form state inside Chromium;
- reviews and private notes rendered by Goodreads;
- browser screenshots/HTML when explicitly enabled;
- full CSV exports;
- an MCP bearer token if remote HTTP is added later.

The CLI MUST NOT collect, receive, store, or log the user's Goodreads password. Credentials are entered only into Goodreads or identity-provider pages inside the headed login browser.

Treat the entire profile and all private Goodreads content as secret/private data.

## Dedicated profile security

`gr login` creates or opens a CLI-owned Chromium profile under the platform-appropriate application data directory.

Requirements:

- use a path unique to this application;
- never use, attach to, copy, or inspect the user's ordinary browser profile;
- create directories with restrictive permissions where supported;
- do not copy cookies into a second application session store;
- do not query Chromium's cookies/history/databases directly;
- keep remote debugging bound to loopback or a process-local transport;
- never expose a debugging endpoint on a network interface;
- hold the per-profile lock whenever Chromium uses the profile;
- do not include the profile in diagnostic bundles or backups by default;
- delete it only after resolving the exact owned path and only for explicit logout/reset;
- never follow attacker-controlled symlinks or accept an unsafe profile path for deletion.

The browser may encrypt some stored values using operating-system facilities, but the application MUST NOT claim that the full profile is encrypted at rest. Documentation must tell users that local account access can imply Goodreads access.

## Login security

The login browser is always visible and user-controlled.

The automation MUST NOT:

- inspect or read password fields;
- type or submit credentials;
- select an identity/provider account;
- intercept credential requests;
- bypass MFA, CAPTCHA, provider warnings, or anti-automation checks;
- extract session cookies for use outside the dedicated profile.

The application observes only enough page state to determine whether Goodreads authentication succeeded and a private library page is accessible.

On cancellation, timeout, or browser exit, close processes cleanly. Retaining a partially initialized dedicated profile is acceptable only as browser-owned state; the command must not report success until authenticated access is validated.

## Logout and profile deletion

`gr logout` removes local access by deleting the dedicated profile. It does not promise global Goodreads logout.

Deletion requirements:

1. acquire the profile lock;
2. resolve and validate the exact application-owned profile path;
3. ensure no browser process started by the application is using it;
4. delete only that directory;
5. return an actionable partial-failure error if cleanup is incomplete.

The command is idempotent. Never broaden deletion to a parent config/data directory.

A future `logout --remote` may drive Goodreads' sign-out UI before local deletion, but it requires a separate tested contract.

## Configuration precedence

Keep configuration minimal:

1. explicit CLI flags
2. environment variables
3. a small config file for non-secret defaults
4. built-in defaults

Do not create a config file until there is a real persistent setting.

Provisional environment variables:

```text
GOODREADS_CLI_BROWSER       explicit Chromium-family executable path
GOODREADS_CLI_TIMEOUT       ordinary operation timeout
GOODREADS_CLI_LOGIN_TIMEOUT interactive login timeout
GOODREADS_CLI_MCP_TOKEN     future remote MCP bearer token
```

Names become stable only when documented for a release.

There is no password variable, cookie variable, or serialized-session variable. The profile location follows platform defaults and is not routinely overridden.

## Browser selection and acquisition

An explicit browser path must point to a supported executable and is validated before launch.

If Rod-managed Chromium download is enabled:

- download only through Rod's pinned mechanism/version;
- verify whatever integrity metadata the dependency provides;
- use a documented cache path separate from the Goodreads profile;
- provide clear first-run progress and failure messages;
- do not require elevated privileges;
- never execute a browser binary from the Goodreads profile or download directory.

If managed download is not reliable on a platform, require a supported installed browser and fail actionably.

## Process isolation

Launch Chromium with only the flags required for profile selection, headless/headed behavior, downloads, and automation.

- avoid `--no-sandbox` by default;
- never disable TLS validation;
- never expose CDP remotely;
- do not install extensions;
- do not load arbitrary user scripts;
- restrict navigation to expected Goodreads origins except user-controlled identity-provider navigation during login;
- treat unexpected post-login cross-origin navigation as an error;
- leftover cleanup may terminate only processes whose command line contains
  the exact dedicated `--user-data-dir`; never a user's ordinary browser
  profile. Errors omit that path.

Containers that require weakened browser sandboxing need a separate deployment threat-model decision; v0.1 does not silently opt into it.

## Logs

Default logging is minimal and contains no library data.

Always redact or omit:

- cookies and browser storage;
- `Cookie`, `Set-Cookie`, and `Authorization`;
- CSRF tokens and authenticated query parameters;
- password/form values;
- raw authenticated HTML;
- review and private-note text;
- CSV row contents;
- screenshots;
- exact profile path in ordinary output.
- ISBNs, book IDs, titles, and authors.

Debug logging may include:

- URL origin and safe path;
- operation and compatibility stage;
- browser product/version;
- page-load duration;
- selector alternative identifier, but not private element text;
- pagination/retry counts;
- high-level verification field names.
- partial-mutation step names and observed status, numeric rating, or ISO date when needed to explain the failed command.

`--debug` is useful only if it adds stable flow stage, elapsed duration, browser product/version, and typed error information beyond the ordinary public message. It must remain safe to paste into an issue by default.

Review mismatches log neither expected nor observed text. Use lengths or a non-reversible diagnostic digest only when genuinely useful.

## Diagnostic artifacts

Screenshots, DOM snapshots, and traces are opt-in because they may contain private library data.

If implemented:

- require an explicit flag/path;
- print a privacy warning to stderr;
- never capture login credential fields;
- prefer screenshots after authentication pages;
- sanitize cookies and storage from traces;
- use restrictive permissions;
- clearly report every created file;
- never upload artifacts automatically.

## Export privacy

A Goodreads CSV export is personal data.

`gr export --out` is an explicit request and may persist it. Use normal user-only permissions where possible and do not copy it elsewhere.

Temporary download files use:

- a randomized private directory;
- restrictive permissions;
- conservative size/type validation;
- cleanup on success and ordinary failure;
- no predictable shared filename.

Do not log CSV rows or review/private-note fields.

## Browser cache versus application cache

Chromium may maintain ordinary cache inside the dedicated profile. This is permitted browser runtime state.

The application MUST NOT:

- read cached responses instead of navigating Goodreads;
- persist parsed books outside an invocation;
- use history/cache databases as a library store;
- promise cache invalidation semantics.

Where freshness is critical, navigation/reload settings should cause Goodreads to provide current state and the verification flow should revisit the authoritative page.

## MCP security

Local stdio MCP inherits the current user's filesystem access and uses the same profile lock.

Remote HTTP is deferred. If implemented later:

- non-loopback binding requires authenticated MCP clients;
- internet exposure requires HTTPS;
- browser/profile storage must be persistent and access-controlled;
- remote debugging must remain inaccessible;
- one process represents one Goodreads account;
- login/reauthentication needs an explicit safe design;
- neither profile data nor MCP tokens may be returned by tools.

The MCP client token and Goodreads browser profile are separate secrets.

## No telemetry by default

Initial releases send no analytics, crash reports, library metrics, screenshots, or usage telemetry to the maintainer.

Any future telemetry requires a separate product decision and explicit opt-in design.

## Supply-chain expectations

- keep dependencies few;
- pin Go module versions;
- isolate direct Rod/CDP use behind the browser package;
- document the Chromium version strategy;
- run dependency and vulnerability scanning in CI where practical;
- publish release checksums;
- add signing when the release process is ready;
- never dynamically download arbitrary scripts or selector updates.
