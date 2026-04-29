# next-up

Shows the single most important issue or PR you should look at next in the current repo.

It finds the most recently updated item that you haven't touched within a cooldown window.

## Install

```
go install github.com/ralphbean/next@latest
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
--show-config            Show configured remotes for all repos
--config                 Show config and available remotes, then choose which to track
```

Durations accept Go syntax plus `d` for days (e.g., `1h`, `3d`).

### Remote selection

By default, `next-up` queries the `origin` remote. If you work on a fork where `origin` is your fork and `upstream` is the main repo, `next-up` will prompt you to choose a remote the first time you run it in that repo.

The selection is saved to `~/.config/next-up.json` (or `$XDG_CONFIG_HOME/next-up.json`) so you're only asked once per repo.

To see all configured repos and their remotes:

```
next-up --show-config
```

To change the remote for the current repo:

```
next-up --config
```

In non-interactive environments (CI, pipes), the prompt is skipped and `next-up` falls back to `upstream` if available, otherwise `origin`.

## Requirements

- `gh` CLI (GitHub) or `glab` CLI (GitLab), authenticated
- A git repo with a remote pointing to GitHub or GitLab

The platform is auto-detected from the remote URL. For self-hosted GitLab, set `GITLAB_HOST`.
