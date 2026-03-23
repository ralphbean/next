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

- **VCS backends**: Supports both GitHub and GitLab. Each backend implements a common interface for fetching issues/PRs and determining user activity.
  - GitHub: authenticates via `gh auth` (the GitHub CLI)
  - GitLab: authenticates via `glab auth` (the GitLab CLI), respects `GITLAB_TOKEN` and `GITLAB_HOST` environment variables
- **Repo detection**: Derives the remote owner/repo from the git remote of the current working directory. Exits with an error if not inside a git repo.
- **Filtering logic**: Finds the most recently updated issue or PR/MR that the current user has *not* interacted with within the `--since` duration.
- **Output**: Default text mode prints a clickable URL, summary, and a list of timestamped updates since the user's last interaction, each truncated to fit one line. Format example: `(3 days ago) @user123 commented on the issue: > I think that this is a good idea, but we...`

## CLI Flags

- `--since <duration>` — cooldown period before an item the user touched reappears (default `30m`). Accepts Go-style durations plus `d` for days (e.g., `1h`, `3d`).

## Key Design Decisions

- Uses `gh` and `glab` CLI tools for authentication to avoid managing tokens directly and to avoid rate-limiting.
- The tool must fail clearly if not run inside a git repository.
- The tool auto-detects whether the remote is GitHub or GitLab from the git remote URL.
