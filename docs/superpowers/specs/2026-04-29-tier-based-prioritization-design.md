# Tier-Based Item Prioritization

## Summary

Replace the current pure-recency sorting with a three-tier priority system. Items the user authored rank highest, items the user participated in rank second, and general activity ranks third. Within each tier, items are sorted most-recent-first. The existing cooldown and discard logic is unchanged.

## Motivation

The current approach treats all activity equally — a comment on a random issue ranks the same as a direct reply on the user's own PR. Developers generally want to unblock people responding to them before diving into unrelated threads. Tier-based prioritization surfaces those "waiting on you" items first.

## Tier Definitions

| Tier | Label | Terminal Color | Condition |
|------|-------|----------------|-----------|
| 1 | `[authored]` | Bold yellow | The current user created the issue/PR |
| 2 | `[participated]` | Cyan | The current user previously commented, reviewed, or reacted on the item (but did not author it) |
| 3 | `[general]` | Dim/default | All other open items with activity that pass existing filters |

## Ordering Rules

1. All qualifying Tier 1 items come first, sorted by most-recent update.
2. Then all qualifying Tier 2 items, sorted by most-recent update.
3. Then all qualifying Tier 3 items, sorted by most-recent update.
4. The `--limit` flag (default 1) caps total items emitted across all tiers.

## Display

Each item is prefixed with a color-coded tier label:

```
[authored] #42 Fix login redirect  
  5m ago  alice  commented: > looks good, one nit on line 12
```

When `--limit=1` (default), only the single highest-priority item is shown with its tier label.

## Tier Classification Logic

### Tier 1 — Authored

An item qualifies for Tier 1 if the item's author matches the current user. This information is already available:
- GitHub: `ghIssue.User.Login == user`
- GitLab: `glItem.Author == user`

### Tier 2 — Participated

An item qualifies for Tier 2 if the user is not the author but has prior activity on the item. Prior activity is determined from the same data already fetched:
- GitHub: any timeline event, review, or reaction by the user on the item
- GitLab: any note by the user on the item

This is already computed as part of the "last user interaction time" logic that exists today. If that timestamp is non-zero, the user participated.

### Tier 3 — General

Any item that passes the existing filters and is neither authored nor participated in by the user.

## Changes Required

### `format/format.go`

- Add a `Tier int` field to the `Item` struct (values 1, 2, 3).
- Add a function to render the tier label with ANSI color codes.

### `backend/github.go`

- After an item passes all existing filters (cooldown, activity check, event filtering), classify it into a tier based on author and user interaction history.
- Set the `Tier` field on the emitted `format.Item`.
- Collect all passing items, sort by tier then recency, and emit up to `--limit`.

### `backend/gitlab.go`

- Same changes as GitHub backend.

### `main.go`

- Update the emit/display callback to render the tier label prefix before the item title.

## What Does NOT Change

- CLI flags — no new flags needed.
- `Backend` interface signature — unchanged.
- API calls and data fetching — no additional API calls.
- Cooldown and discard logic — identical behavior.
- `--ignore-events` and `--ignore-users` filtering — unchanged.
- The `--since` duration semantics — unchanged.

## Edge Cases

- **User authored AND participated**: Tier 1 wins (authored takes precedence).
- **No items in higher tiers**: Fall through to next tier. If all tiers empty, show existing "Nothing to do!" message.
- **Org scope (`--scope=org`)**: Tiers apply the same way across all repos in the org.