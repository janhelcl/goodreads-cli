# goodreads-cli

A small, stateless CLI for working with Goodreads while keeping Goodreads as the only source of truth.

The project deliberately stays narrow:

- use Goodreads import/export as the library integration boundary
- no local library database or sync state
- use a temporary local browser only for interactive Goodreads login/session capture
- no browser automation for Goodreads library operations
- run locally and be easy to share
- expose a clean CLI that agents can use directly
- keep room for an MCP adapter later without making MCP the core

## Stack

- Go
- Cobra

## Principle

Every library operation starts from Goodreads and writes back to Goodreads through import/export. The only persistent local secret is reusable Goodreads session material stored securely; any library CSVs or temporary browser profile data are transport artifacts, not a second copy of the library.

## Status

Early development. See `specs/` for the implementation contract.
