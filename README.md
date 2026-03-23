# next-up

Shows the single most important issue or PR you should look at next in the current repo.

It finds the most recently updated item that you haven't touched within a cooldown window.

## Install

```
go install github.com/rbean/next-up@latest
```

## Usage

Run inside a git repository:

```
next-up
```

### Flags

```
--since <duration>       Cooldown before items you touched reappear (default: 30m)
--ignore-events <list>   Timeline event types to ignore (default: mentioned,subscribed)
--ignore-users <list>    Users to ignore when determining activity
```

Durations accept Go syntax plus `d` for days (e.g., `1h`, `3d`).

## Requirements

- `gh` CLI (GitHub) or `glab` CLI (GitLab), authenticated
- A git repo with an `origin` remote pointing to GitHub or GitLab

The platform is auto-detected from the remote URL. For self-hosted GitLab, set `GITLAB_HOST`.
