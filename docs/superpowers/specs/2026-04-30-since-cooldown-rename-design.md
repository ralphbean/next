# Rename --since to --cooldown, Add New --since for API-Level Time Filtering

## Summary

Rename the existing `--since` flag to `--cooldown` (same behavior — how long before a touched item resurfaces). Add a new `--since` flag that filters at the API level, only fetching issues/PRs updated within the given window (default `24h`). This drastically reduces API calls for repos with many open items.

## Flag Definitions

| Flag | Default | Purpose |
|------|---------|---------|
| `--cooldown` | `30m` | How long before a touched item resurfaces. Previously named `--since`. |
| `--since` | `24h` | Only fetch items updated within this window. Passed to the API query. |

Both flags accept the same duration format: Go-style durations plus `d` for days (e.g. `30m`, `1h`, `3d`).

## API-Level Filtering

The `--since` value is converted to an absolute timestamp (`time.Now().Add(-since)`) and passed to the platform API:

### GitHub

- `listRepoIssues`: add `-f "since=<ISO8601>"` to the `gh api` call. The GitHub Issues API `since` parameter returns issues updated at or after this time.
- `searchOrgIssues`: add `updated:>=<YYYY-MM-DD>` to the search query string.

### GitLab

- `listIssues`, `listMRs`, `listGroupIssues`, `listGroupMRs`: append `&updated_after=<ISO8601>` to the endpoint URL.

## Backend Interface Change

Update the `NextItems` signature to accept the API-level time filter:

```go
NextItems(owner, repo, user string, cooldown time.Duration, since time.Time, ignoreEvents MatchSet, ignoreUsers MatchSet, limit int, scope Scope, emit func(format.Item)) error
```

- `cooldown` (renamed from `since`): duration for the cooldown filter (client-side logic, unchanged)
- `since`: absolute timestamp passed to the API to limit which items are fetched

## Changes Required

### `main.go`

- Rename the `--since` flag to `--cooldown` (keep default `30m`, same help text adjusted).
- Add new `--since` flag (default `24h`).
- Parse both durations.
- Compute `sinceTime := time.Now().Add(-sinceDuration)` and pass to `NextItems`.

### `backend/backend.go`

- Update `NextItems` signature: rename `since` parameter to `cooldown`, add `since time.Time` parameter.

### `backend/github.go`

- Rename `since` parameter to `cooldown` in `NextItems`.
- Accept `since time.Time` and pass it to `listRepoIssues` and `searchOrgIssues`.
- `listRepoIssues`: add `-f "since=<since.Format(time.RFC3339)>"`.
- `searchOrgIssues`: add `updated:>=<since.Format("2006-01-02")>` to the query string.
- Update all internal references from `since` to `cooldown`.

### `backend/gitlab.go`

- Rename `since` parameter to `cooldown` in `NextItems`.
- Accept `since time.Time` and pass it to all four list functions.
- Append `&updated_after=<since.Format(time.RFC3339)>` to each endpoint URL.
- Update all internal references from `since` to `cooldown`.

### Tests

- Update all test call sites to use the new `NextItems` signature.
- Add tests verifying that the `since` timestamp is forwarded to the API calls.

## What Does NOT Change

- Cooldown filtering logic (identical, just the parameter name changes).
- Tier-based prioritization.
- Event building, activity checks, "no new activity" skip logic.
- `--ignore-events`, `--ignore-users`, `--limit`, `--scope` flags.

## Edge Cases

- **`--since 30m --cooldown 1h`**: cooldown longer than fetch window. Fine — fewer items come back, cooldown filters more of them. No special handling needed.
- **Very large `--since`** (e.g. `365d`): equivalent to current behavior, fetches everything. Works correctly, just slow.
- **CLAUDE.md docs**: update the CLI Flags section to reflect the rename and new flag.