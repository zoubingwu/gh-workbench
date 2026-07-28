# GitHub Workbench

GitHub Workbench is a local browser workbench for GitHub issues and pull
requests. It runs as a GitHub CLI extension, finds open work authored by,
assigned to, mentioning, reviewed by, or awaiting review from the active `gh`
account across repositories, stores an account-scoped cache in SQLite, and
keeps the UI current through adaptive polling and WebSocket updates.

## Development

Requirements:

- Go 1.25 or newer
- Node.js 24 or newer
- pnpm 11 or newer
- GitHub CLI authenticated with `gh auth login`

```sh
make install
make check
make dev
```

`make dev` builds the frontend and starts the account workbench from this
checkout. After installation, `gh workbench` starts from any directory. Set
`GH_HOST` when selecting an authenticated GitHub Enterprise host.

## Install as a local extension

From this repository:

```sh
make build
gh extension remove workbench 2>/dev/null || true
gh extension install .
gh workbench
```

## Polling model

Account-wide GraphQL searches reconcile those five direct relationships. Each
relevant pull request's reactions remain an independent polling resource so
body reactions stay observable. A changed resource returns to a
ten-second interval. Unchanged resources cool down exponentially, with caps
based on recent GitHub activity: five minutes for the last day, thirty minutes
for the last week, and one day for older open work. HTTP ETags avoid downloading
unchanged reaction payloads. A bounded worker pool and an account-wide
one-request-per-second gate keep the global polling queue controlled. GitHub
rate-limit responses pause the queue until its reset time. Search omissions
leave the visible workbench immediately while the cache keeps them for three
searches, protecting reactions from transient index or pagination gaps. `Sync
now` refreshes both account search and every relevant pull request reaction.
