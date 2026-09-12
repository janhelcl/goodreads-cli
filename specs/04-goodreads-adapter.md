# Goodreads adapter specification

This is the highest-risk boundary. Implement it narrowly and defensively.

## Allowed integration surface

The adapter may use ordinary HTTPS requests needed to perform:

1. Goodreads authentication/session validation;
2. the authenticated library export workflow;
3. the authenticated library import workflow.

It MUST NOT grow into a general Goodreads scraper or unofficial book API.

Explicitly prohibited:

- Playwright/Selenium/Puppeteer/WebDriver
- headless Chromium/WebKit/Firefox
- DOM clicking/browser automation
- crawling Goodreads book/search/review pages for metadata
- reverse-engineered endpoints unrelated to login/import/export

Parsing the HTML forms/pages directly involved in login/import/export to obtain action URLs, CSRF tokens, generated-export links, or status messages is allowed and expected.

## Session model

Use a standard Go HTTP client with a cookie jar.

The adapter MUST:

- send a stable, honest User-Agent identifying the tool/version;
- preserve relevant cookies across requests;
- follow normal redirects with conservative limits;
- use HTTPS only;
- detect redirect-to-login or login-page responses as session expiry;
- never log cookie values or authentication form contents.

## Authentication

V1 target is ordinary Goodreads email/password authentication performed as form HTTP requests, subject to the compatibility spike.

Requirements:

- discover/parse current form action and CSRF fields rather than hardcoding volatile token values;
- password is caller-supplied transient data and is never stored;
- after form submission, validate authenticated state using the import/export page or another minimal page already needed for this integration;
- store only session material through the session abstraction;
- social-login-only accounts are not automatically supported unless a non-browser flow can be implemented simply and safely.

Do not add a browser solely to support social login.

## Export workflow

The adapter must treat export as potentially asynchronous.

Conceptual algorithm:

1. GET the import/export page and confirm authenticated state.
2. Trigger a new export using the current form/link/request mechanism.
3. Poll the minimum required status resource/page with bounded backoff until a generated export becomes available.
4. Download the generated CSV.
5. Validate that the body is actually CSV with recognizable Goodreads headers rather than an HTML login/error page.
6. Return bytes + export metadata.

Do not assume an old export link is fresh. An operation requesting fresh state MUST trigger or otherwise prove it obtained a newly generated export for that invocation.

Set a conservative poll interval and total timeout. Do not hammer Goodreads.

## Export CSV compatibility

Observed Goodreads exports have historically included fields such as:

```text
Book Id
Title
Author
Author l-f
Additional Authors
ISBN
ISBN13
My Rating
Average Rating
Publisher
Binding
Number of Pages
Year Published
Original Publication Year
Date Read
Date Added
Bookshelves
Bookshelves with positions
Exclusive Shelf
My Review
Spoiler
Private Notes
Read Count
Owned Copies
```

Treat this list as a compatibility baseline, not a permanent guarantee.

The parser MUST:

- parse by header name, not column index;
- tolerate column reordering;
- preserve unknown columns in a raw row representation when practical;
- reject files missing fields essential to the requested operation;
- handle UTF-8 correctly;
- handle quoted commas/newlines in reviews;
- normalize Goodreads/Excel-style ISBN cells such as `="0553379887"` and empty `=""` values;
- normalize CRLF/LF;
- parse ratings conservatively as integers 0..5;
- parse Goodreads date formats observed by fixtures and emit domain dates;
- recognize at least `to-read`, `currently-reading`, and `read` statuses.

Do not silently coerce unknown exclusive shelves to a core status.

## Book lookup in an export

Lookup order for a mutation:

1. normalized ISBN-13 exact match;
2. normalized ISBN-10 exact match;
3. if both identify different rows, return compatibility/conflict error.

Do not fall back to fuzzy title/author matching.

## Import workflow

The compatibility spike must determine the current form action, multipart fields, accepted CSV columns, completion/status response, and whether import is synchronous or asynchronous.

Conceptual algorithm:

1. GET import page and parse current form/CSRF state.
2. Construct a minimal CSV representing the intended mutation.
3. POST as the official import UI does.
4. Follow/poll only the import workflow resources needed to determine accepted/rejected/completed state.
5. Parse explicit Goodreads success/failure information.
6. Return a structured `ImportResult`.

The adapter MUST distinguish:

- transport success (`HTTP 2xx`)
- import accepted/queued
- import completed
- row rejected/unrecognized
- session expired
- compatibility drift (expected form/result cannot be recognized)

HTTP 200 by itself is never sufficient evidence of mutation success.

## Mutation document construction

The safest strategy is export-first preservation:

- for an existing book, start from the freshly exported row;
- change only the requested semantic fields;
- encode only the subset of columns proven safe/accepted by the compatibility suite;
- preserve user-owned fields required to avoid destructive resets (rating, review, shelves, date read, etc.).

For a new ISBN not present in the export, construct the minimum accepted import row. Do not fabricate bibliographic metadata if Goodreads can identify the book from ISBN alone.

The compatibility spike must answer whether `Exclusive Shelf` is accepted by Goodreads import or whether status must be expressed through another accepted column (historical sample formats have differed). Code should encode the empirically verified current contract.

## Idempotency and retries

Reads/export trigger requests MAY be retried conservatively when safe.

Mutation POSTs MUST NOT be automatically repeated after ambiguous network failure unless the protocol provides a reliable idempotency/completion check. Prefer returning an ambiguous-result error and letting the caller re-export/check state.

## Verification

A post-import fresh export is the strongest verification. The application may make this optional for latency/load reasons, but the adapter must make it possible.

Verification compares only fields requested by the mutation; unrelated Goodreads-normalized metadata changes do not fail verification.

## Compatibility versioning

Keep compatibility knowledge explicit and testable. Avoid selectors/regexes scattered across commands.

Suggested internal structure:

```text
goodreads/
  auth.go
  export.go
  import.go
  forms.go
  compatibility.go
```

When Goodreads changes:

- return `ErrCompatibility` with stage (`auth`, `export`, `import`, `csv`);
- include safe diagnostic context in debug logs;
- never guess form field names or silently fall back to scraping unrelated pages.

## Rate limiting / politeness

This tool is for low-volume personal actions.

- no background polling except bounded completion polling for an explicit user request;
- no scheduled sync;
- no bulk crawling;
- use backoff for export/import status checks;
- respect server errors/retry hints;
- keep request counts small and observable in integration tests.
