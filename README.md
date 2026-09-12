# goodreads-cli

A small, stateless CLI for working with Goodreads while keeping Goodreads as the only source of truth.

The project deliberately stays narrow:

- use Goodreads import/export as the integration boundary
- no local library database or sync state
- no browser automation
- run locally and be easy to share
- expose a clean CLI that agents can use directly
- keep room for an MCP adapter later without making MCP the core

## Stack

- Go
- Cobra

## Principle

Every operation starts from Goodreads and writes back to Goodreads. Any local files used during the process are temporary transport artifacts, not a second copy of the library.

## Status

Early development.
