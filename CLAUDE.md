# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

`next-up` is a Go CLI tool that shows a developer the single most important issue or pull request they should focus on next. It identifies the most recently updated item in the current repo that the user hasn't touched within a configurable time window (default 30 minutes).

## Build and Test Commands

```bash
go build -o next-up .
go test ./...
go test ./... -run TestSpecificName    # run a single test
go vet ./...
```

## Architecture

- **`duration/`** — Parses duration strings with `d` (days) support on top of Go's `time.ParseDuration`.
- **`repo/`** — Detects owner/repo/platform from git remote URL (SSH and HTTPS). Respects `GITLAB_HOST` env var.
- **`format/`** — Formats items and events for terminal output with relative timestamps and line truncation.
- **`backend/`** — `Backend` interface with `CurrentUser()` and `NextItem()`. Two implementations:
  - `gitHub` — uses `gh api` for issues + timeline events
  - `gitLab` — uses `glab api` for issues + MRs + notes
- **`main.go`** — CLI entry point using `flag` package. Wires repo detection → backend selection → fetch → format.

All command execution goes through `backend.CmdRunner` (`func(name string, args ...string) ([]byte, error)`) for testability. Only external dependency is `golang.org/x/term` for terminal width.

## CLI Flags

- `--since <duration>` — cooldown period before an item the user touched reappears (default `30m`). Accepts Go-style durations plus `d` for days (e.g., `1h`, `3d`).

## Key Design Decisions

- Uses `gh` and `glab` CLI tools for authentication to avoid managing tokens directly and to avoid rate-limiting.
- The tool must fail clearly if not run inside a git repository.
- The tool auto-detects whether the remote is GitHub or GitLab from the git remote URL.
