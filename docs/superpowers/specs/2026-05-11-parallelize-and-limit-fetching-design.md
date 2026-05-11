 Parallelize API Calls and Limit Per-Item Fetching

Addresses: https://github.com/ralphbean/next/issues/5
Builds on: https://github.com/ralphbean/next/pull/7 (early-exit via tier sorting)

## Problem

For each issue/PR, the GitHub backend makes 5-7 sequential API calls (timeline, reactions, comments, comment reactions, reviews, review comment reactions, review reactions via GraphQL). All items are processed sequentially. A repo with 20 recently-updated items means 100-140 sequential API calls.

PR #7 mitigates this with early-exit (stop processing once `--limit` candidates are found), but when the first few items are skipped (cooldown, no activity), the tool still processes many items sequentially before finding candidates.

## Design

### 1. Parallel per-item detail fetching

**Approach:** Semaphore-bounded goroutines with batch processing.

- A constant `maxConcurrency = 5` controls how many items are fetched concurrently.
- Items are processed in batches of `maxConcurrency`. For each batch:
  1. Launch goroutines for all items in the batch, each fetching all details (timeline, reactions, reviews, etc.).
  2. Each goroutine writes its result to a pre-allocated slot in a results slice, indexed by position.
  3. Wait for all goroutines in the batch to complete.
  4. Process results in order, emitting items and incrementing the found counter.
  5. If `found >= limit`, stop. Otherwise, process the next batch.
- This preserves deterministic recency/tier ordering while achieving ~5x speedup.

**Error handling:** If any goroutine encounters an error, it stores the error in its result slot. After the batch completes, the first error (in order) is returned.

**Applies to:** Both GitHub and GitLab backends.

### 2. `--max-events` flag

**Approach:** Limit API responses at the request level using `per_page=N` without `--paginate`.

- New CLI flag: `--max-events N` (default `100`).
- When set, all per-item API calls use `per_page=N` and drop `--paginate`, fetching only the first page.
- Applies to: timeline events, issue reactions, comments, review comments, reviews.
- The GraphQL review reactions query uses `first: N` instead of `first: 100`.
- Comment reactions and review comment reactions that drill into individual items also respect the cap.
- A value of `0` means no cap (fetch all pages, preserving current behavior).

**Why `per_page` without `--paginate`:** The APIs return items sorted by creation date. Dropping `--paginate` means we get only the first page. For the tool's purposes (checking recent activity, cooldown, building event summaries), the most recent N events are sufficient.

**Not applied to:** Issue/MR list endpoints — those already have their own pagination and are filtered server-side by `--since`.

### 3. Backend interface change

The `Backend` interface gains a `maxEvents int` parameter on `NextItems`:

```go
NextItems(owner, repo, user string, cooldown time.Duration, since time.Time,
    ignoreEvents MatchSet, ignoreUsers MatchSet, limit int, maxEvents int,
    scope Scope, emit func(format.Item)) error
```

### 4. No new dependencies

Both features use only the Go standard library (`sync`). No external packages needed.

## Files affected

- `main.go` — Add `--max-events` flag, pass to backend.
- `backend/backend.go` — Add `maxEvents` parameter to `Backend` interface.
- `backend/github.go` — Parallelize `NextItems` loop, pass `maxEvents` to all per-item fetch methods.
- `backend/gitlab.go` — Parallelize `NextItems` loop, pass `maxEvents` to `getNotes`.
- `backend/github_test.go` — Update `ghCollect` helper, add concurrency test.
- `backend/gitlab_test.go` — Update helper, add concurrency test.
- `main_test.go` — Update call sites if needed.

## Testing strategy

- Existing tests continue to pass (they use `maxEvents=0` meaning "no cap" / fetch all).
- New test: verify that with `maxEvents=N`, only one page of N results is fetched (mock runner asserts no `--paginate` flag).
- New test: verify parallel fetching produces same results as sequential (use atomic counter in mock runner to confirm concurrent calls).
- `go vet ./...` clean.