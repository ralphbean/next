# Parallelize API Calls and Limit Per-Item Fetching — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Speed up per-item detail fetching by parallelizing API calls (batch goroutines) and adding a `--max-events` flag to cap the number of events fetched per item.

**Architecture:** Add `maxEvents int` parameter to the `Backend` interface. Each per-item fetch method (`getTimeline`, `getReactions`, etc.) conditionally uses `per_page=N` without `--paginate` when `maxEvents > 0`. The `NextItems` loop processes items in batches of 5 concurrent goroutines, preserving result ordering.

**Tech Stack:** Go standard library only (`sync`, `fmt`, `strconv`).

---

### Task 1: Add `maxEvents` to Backend interface and wire up `--max-events` flag

**Files:**
- Modify: `backend/backend.go:56-59`
- Modify: `main.go:70-80,187`
- Modify: `backend/github.go:145`
- Modify: `backend/gitlab.go:101`
- Modify: `backend/github_test.go:14-24`
- Modify: `backend/gitlab_test.go:13-23`

- [ ] **Step 1: Update Backend interface**

In `backend/backend.go`, add `maxEvents int` after `limit int`:

```go
type Backend interface {
	CurrentUser() (string, error)
	NextItems(owner, repo, user string, cooldown time.Duration, since time.Time, ignoreEvents MatchSet, ignoreUsers MatchSet, limit int, maxEvents int, scope Scope, emit func(format.Item)) error
}
```

- [ ] **Step 2: Update `main.go` — add flag and pass through**

Add the flag after the existing `limit` flag:

```go
maxEvents := flag.Int("max-events", 100, "maximum events/comments/reactions to fetch per item (0 = no limit)")
```

Update the `NextItems` call:

```go
err = b.NextItems(info.Owner, info.Name, user, cooldown, sinceTime, ignore, ignoreUsers, *limit, *maxEvents, scope, emit)
```

- [ ] **Step 3: Update GitHub `NextItems` signature**

In `backend/github.go`, update the method signature:

```go
func (g *gitHub) NextItems(owner, repo, user string, cooldown time.Duration, since time.Time, ignoreEvents MatchSet, ignoreUsers MatchSet, limit int, maxEvents int, scope Scope, emit func(format.Item)) error {
```

The body stays the same for now — `maxEvents` is accepted but not yet used.

- [ ] **Step 4: Update GitLab `NextItems` signature**

In `backend/gitlab.go`, update the method signature:

```go
func (g *gitLab) NextItems(owner, repo, user string, cooldown time.Duration, since time.Time, ignoreEvents MatchSet, ignoreUsers MatchSet, limit int, maxEvents int, scope Scope, emit func(format.Item)) error {
```

The body stays the same for now.

- [ ] **Step 5: Update `ghCollect` test helper**

In `backend/github_test.go`:

```go
func ghCollect(t *testing.T, gh Backend, owner, repo, user string, cooldown time.Duration, ignoreEvents, ignoreUsers MatchSet, limit int) []format.Item {
	t.Helper()
	var items []format.Item
	err := gh.NextItems(owner, repo, user, cooldown, time.Time{}, ignoreEvents, ignoreUsers, limit, 0, ScopeRepo, func(item format.Item) {
		items = append(items, item)
	})
	if err != nil {
		t.Fatalf("NextItems() error: %v", err)
	}
	return items
}
```

Also update the two direct `NextItems` calls in `TestGitHubNextItemsOrgScope` (line 1131), `TestGitHubSincePassedToAPI` (line 1452), and `TestGitHubSincePassedToOrgSearch` (line 1522) to pass `0` for `maxEvents` (add `0,` after the `limit` argument).

- [ ] **Step 6: Update `glCollect` test helper**

In `backend/gitlab_test.go`:

```go
func glCollect(t *testing.T, gl Backend, owner, repo, user string, cooldown time.Duration, ignoreEvents, ignoreUsers MatchSet, limit int) []format.Item {
	t.Helper()
	var items []format.Item
	err := gl.NextItems(owner, repo, user, cooldown, time.Time{}, ignoreEvents, ignoreUsers, limit, 0, ScopeRepo, func(item format.Item) {
		items = append(items, item)
	})
	if err != nil {
		t.Fatalf("NextItems() error: %v", err)
	}
	return items
}
```

Also update the direct `NextItems` calls in `TestGitLabNextItemsOrgScope` (line 329) and `TestGitLabSincePassedToAPI` (line 483) to pass `0` for `maxEvents`.

- [ ] **Step 7: Run tests to verify no regressions**

Run: `go build ./... && go test ./... -count=1`
Expected: All tests pass, build succeeds.

- [ ] **Step 8: Commit**

```bash
git add backend/backend.go main.go backend/github.go backend/gitlab.go backend/github_test.go backend/gitlab_test.go
git commit -m "Add --max-events flag and maxEvents parameter to Backend interface

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

### Task 2: Implement `maxEvents` in GitHub per-item fetch methods

**Files:**
- Modify: `backend/github.go:452-598` (all `get*` methods)
- Test: `backend/github_test.go` (new test)

- [ ] **Step 1: Write test for maxEvents limiting**

Add to `backend/github_test.go`:

```go
func TestGitHubMaxEventsLimitsPagination(t *testing.T) {
	now := time.Now()

	issues := []ghIssue{
		{
			Number:    1,
			Title:     "Issue with events",
			HTMLURL:   "https://github.com/o/r/issues/1",
			UpdatedAt: now.Add(-10 * time.Minute),
			User:      ghActor{Login: "other"},
		},
	}

	events := []ghTimelineEvent{
		{Event: "commented", CreatedAt: now.Add(-10 * time.Minute), Actor: ghActor{Login: "other"}, Body: "hello"},
	}

	var paginateUsed atomic.Bool
	runner := func(name string, args ...string) ([]byte, error) {
		for _, a := range args {
			if a == "--paginate" {
				paginateUsed.Store(true)
			}
			if a == "repos/o/r/issues" {
				return json.Marshal(issues)
			}
			if strings.HasSuffix(a, "/timeline") {
				return json.Marshal(events)
			}
			if strings.HasSuffix(a, "/reactions") {
				return json.Marshal([]ghReaction{})
			}
			if strings.HasSuffix(a, "/comments") {
				return json.Marshal([]ghComment{})
			}
		}
		return nil, fmt.Errorf("unexpected call: %v", args)
	}

	gh := NewGitHub(runner)
	var items []format.Item
	err := gh.NextItems("o", "r", "me", 30*time.Minute, time.Time{}, nil, nil, 5, 50, ScopeRepo, func(item format.Item) {
		items = append(items, item)
	})
	if err != nil {
		t.Fatalf("NextItems() error: %v", err)
	}

	// The issue list endpoint still uses --paginate (maxEvents only affects per-item calls).
	// But per-item calls (timeline, reactions, comments) should NOT use --paginate.
	// We verify by checking that --paginate was used (for the issue list) but
	// the per-item args contain per_page=50 instead.
	// Reset and test with a more targeted check:
	var perItemPaginate atomic.Bool
	runner2 := func(name string, args ...string) ([]byte, error) {
		isPerItem := false
		for _, a := range args {
			if strings.HasSuffix(a, "/timeline") || strings.HasSuffix(a, "/reactions") ||
				strings.HasSuffix(a, "/comments") || strings.HasSuffix(a, "/reviews") {
				isPerItem = true
			}
		}
		if isPerItem {
			for _, a := range args {
				if a == "--paginate" {
					perItemPaginate.Store(true)
				}
			}
		}
		for _, a := range args {
			if a == "repos/o/r/issues" {
				return json.Marshal(issues)
			}
			if strings.HasSuffix(a, "/timeline") {
				return json.Marshal(events)
			}
			if strings.HasSuffix(a, "/reactions") {
				return json.Marshal([]ghReaction{})
			}
			if strings.HasSuffix(a, "/comments") {
				return json.Marshal([]ghComment{})
			}
		}
		return nil, fmt.Errorf("unexpected call: %v", args)
	}

	gh2 := NewGitHub(runner2)
	err = gh2.NextItems("o", "r", "me", 30*time.Minute, time.Time{}, nil, nil, 5, 50, ScopeRepo, func(item format.Item) {})
	if err != nil {
		t.Fatalf("NextItems() error: %v", err)
	}
	if perItemPaginate.Load() {
		t.Error("per-item API calls should not use --paginate when maxEvents > 0")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./backend -run TestGitHubMaxEventsLimitsPagination -v`
Expected: FAIL — per-item calls still use `--paginate`.

- [ ] **Step 3: Update all GitHub `get*` methods to accept `maxEvents`**

Add a helper function at the top of `github.go` (after the `maxRetries` const):

```go
const maxConcurrency = 5

func perItemArgs(endpoint string, maxEvents int) []string {
	if maxEvents > 0 {
		return []string{"api", endpoint, "-f", fmt.Sprintf("per_page=%d", maxEvents)}
	}
	return []string{"api", endpoint, "--paginate"}
}
```

Update `getTimeline`:

```go
func (g *gitHub) getTimeline(owner, repo string, number, maxEvents int) ([]ghTimelineEvent, error) {
	endpoint := fmt.Sprintf("repos/%s/%s/issues/%d/timeline", owner, repo, number)
	out, err := g.runAPI("gh", perItemArgs(endpoint, maxEvents)...)
	if err != nil {
		return nil, fmt.Errorf("failed to get timeline for #%d: %w", number, err)
	}
	var events []ghTimelineEvent
	if err := json.Unmarshal(out, &events); err != nil {
		return nil, fmt.Errorf("failed to parse timeline: %w", err)
	}
	return events, nil
}
```

Update `getReactions`:

```go
func (g *gitHub) getReactions(owner, repo string, number, maxEvents int) ([]ghReaction, error) {
	endpoint := fmt.Sprintf("repos/%s/%s/issues/%d/reactions", owner, repo, number)
	out, err := g.runAPI("gh", perItemArgs(endpoint, maxEvents)...)
	if err != nil {
		return nil, fmt.Errorf("failed to get reactions for #%d: %w", number, err)
	}
	var reactions []ghReaction
	if err := json.Unmarshal(out, &reactions); err != nil {
		return nil, fmt.Errorf("failed to parse reactions: %w", err)
	}
	return reactions, nil
}
```

Update `getComments`:

```go
func (g *gitHub) getComments(owner, repo string, number, maxEvents int) ([]ghComment, error) {
	endpoint := fmt.Sprintf("repos/%s/%s/issues/%d/comments", owner, repo, number)
	out, err := g.runAPI("gh", perItemArgs(endpoint, maxEvents)...)
	if err != nil {
		return nil, fmt.Errorf("failed to get comments for #%d: %w", number, err)
	}
	var comments []ghComment
	if err := json.Unmarshal(out, &comments); err != nil {
		return nil, fmt.Errorf("failed to parse comments: %w", err)
	}
	return comments, nil
}
```

Update `getCommentReactions` — calls `getComments` with `maxEvents`, and per-comment reaction fetches also use `perItemArgs`:

```go
func (g *gitHub) getCommentReactions(owner, repo string, number, maxEvents int) ([]ghReaction, error) {
	comments, err := g.getComments(owner, repo, number, maxEvents)
	if err != nil {
		return nil, err
	}
	var all []ghReaction
	for _, c := range comments {
		if c.Reactions.TotalCount == 0 {
			continue
		}
		endpoint := fmt.Sprintf("repos/%s/%s/issues/comments/%d/reactions", owner, repo, c.ID)
		out, err := g.runAPI("gh", perItemArgs(endpoint, maxEvents)...)
		if err != nil {
			return nil, fmt.Errorf("failed to get reactions for comment %d: %w", c.ID, err)
		}
		var reactions []ghReaction
		if err := json.Unmarshal(out, &reactions); err != nil {
			return nil, fmt.Errorf("failed to parse comment reactions: %w", err)
		}
		all = append(all, reactions...)
	}
	return all, nil
}
```

Update `getReviews`:

```go
func (g *gitHub) getReviews(owner, repo string, number, maxEvents int) ([]ghReview, error) {
	endpoint := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", owner, repo, number)
	out, err := g.runAPI("gh", perItemArgs(endpoint, maxEvents)...)
	if err != nil {
		return nil, fmt.Errorf("failed to get reviews for #%d: %w", number, err)
	}
	var reviews []ghReview
	if err := json.Unmarshal(out, &reviews); err != nil {
		return nil, fmt.Errorf("failed to parse reviews: %w", err)
	}
	return reviews, nil
}
```

Update `getReviewCommentReactions` — the initial pull comments fetch and per-comment reaction fetches use `perItemArgs`:

```go
func (g *gitHub) getReviewCommentReactions(owner, repo string, number, maxEvents int) ([]ghReaction, error) {
	endpoint := fmt.Sprintf("repos/%s/%s/pulls/%d/comments", owner, repo, number)
	out, err := g.runAPI("gh", perItemArgs(endpoint, maxEvents)...)
	if err != nil {
		return nil, fmt.Errorf("failed to get review comments for #%d: %w", number, err)
	}
	var comments []ghComment
	if err := json.Unmarshal(out, &comments); err != nil {
		return nil, fmt.Errorf("failed to parse review comments: %w", err)
	}
	var all []ghReaction
	for _, c := range comments {
		if c.Reactions.TotalCount == 0 {
			continue
		}
		ep := fmt.Sprintf("repos/%s/%s/pulls/comments/%d/reactions", owner, repo, c.ID)
		out, err := g.runAPI("gh", perItemArgs(ep, maxEvents)...)
		if err != nil {
			return nil, fmt.Errorf("failed to get reactions for review comment %d: %w", c.ID, err)
		}
		var reactions []ghReaction
		if err := json.Unmarshal(out, &reactions); err != nil {
			return nil, fmt.Errorf("failed to parse review comment reactions: %w", err)
		}
		all = append(all, reactions...)
	}
	return all, nil
}
```

Update `getReviewReactions` — GraphQL query uses `maxEvents` for `first:` parameter:

```go
func (g *gitHub) getReviewReactions(owner, repo string, number, maxEvents int) ([]ghReaction, error) {
	reviewsFirst := 100
	reactionsFirst := 100
	if maxEvents > 0 {
		reviewsFirst = maxEvents
		reactionsFirst = maxEvents
	}
	query := fmt.Sprintf(`{
		repository(owner: %q, name: %q) {
			pullRequest(number: %d) {
				reviews(first: %d) {
					nodes {
						reactions(first: %d) {
							nodes {
								user { login }
								content
								createdAt
							}
						}
					}
				}
			}
		}
	}`, owner, repo, number, reviewsFirst, reactionsFirst)
	// ... rest stays the same
```

- [ ] **Step 4: Update callers in `NextItems`**

Update all calls inside the `NextItems` for-loop body to pass `maxEvents`:

```go
events, err := g.getTimeline(issueOwner, issueRepo, issue.Number, maxEvents)
```
```go
issueReactions, err := g.getReactions(issueOwner, issueRepo, issue.Number, maxEvents)
```
```go
commentReactions, err := g.getCommentReactions(issueOwner, issueRepo, issue.Number, maxEvents)
```
```go
reviews, err = g.getReviews(issueOwner, issueRepo, issue.Number, maxEvents)
```
```go
reviewCommentReactions, err = g.getReviewCommentReactions(issueOwner, issueRepo, issue.Number, maxEvents)
```
```go
reviewReactions, err = g.getReviewReactions(issueOwner, issueRepo, issue.Number, maxEvents)
```

- [ ] **Step 5: Run tests**

Run: `go test ./backend -run TestGitHubMaxEventsLimitsPagination -v && go test ./... -count=1`
Expected: All tests pass.

- [ ] **Step 6: Commit**

```bash
git add backend/github.go backend/github_test.go
git commit -m "Implement --max-events for GitHub per-item fetch methods

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

### Task 3: Implement `maxEvents` in GitLab `getNotes`

**Files:**
- Modify: `backend/gitlab.go:373-384`
- Test: `backend/gitlab_test.go` (new test)

- [ ] **Step 1: Write test for maxEvents limiting**

Add to `backend/gitlab_test.go`:

```go
func TestGitLabMaxEventsLimitsPagination(t *testing.T) {
	now := time.Now()

	issues := []glIssue{
		{
			IID:       1,
			Title:     "Issue with notes",
			WebURL:    "https://gitlab.com/o/r/-/issues/1",
			UpdatedAt: now.Add(-10 * time.Minute),
			Author:    glNoteAuthor{Username: "other"},
		},
	}
	notes := []glNote{
		{Body: "hello", CreatedAt: now.Add(-10 * time.Minute), Author: glNoteAuthor{Username: "other"}},
	}

	var perItemPaginate atomic.Bool
	runner := func(name string, args ...string) ([]byte, error) {
		isPerItem := false
		for _, a := range args {
			if strings.Contains(a, "/notes") {
				isPerItem = true
			}
		}
		if isPerItem {
			for _, a := range args {
				if a == "--paginate" {
					perItemPaginate.Store(true)
				}
			}
		}
		for _, a := range args {
			if strings.HasPrefix(a, "projects/o%2Fr/issues?") {
				return json.Marshal(issues)
			}
			if strings.HasPrefix(a, "projects/o%2Fr/merge_requests?") {
				return json.Marshal([]glMR{})
			}
			if strings.Contains(a, "/notes") {
				return json.Marshal(notes)
			}
		}
		return nil, fmt.Errorf("unexpected call: %v", args)
	}

	gl := NewGitLab(runner, "")
	err := gl.NextItems("o", "r", "me", 30*time.Minute, time.Time{}, nil, nil, 5, 50, ScopeRepo, func(item format.Item) {})
	if err != nil {
		t.Fatalf("NextItems() error: %v", err)
	}
	if perItemPaginate.Load() {
		t.Error("getNotes should not use --paginate when maxEvents > 0")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./backend -run TestGitLabMaxEventsLimitsPagination -v`
Expected: FAIL — `getNotes` still uses `--paginate`.

- [ ] **Step 3: Update `getNotes` to accept `maxEvents`**

```go
func (g *gitLab) getNotes(projectPath, kind string, iid, maxEvents int) ([]glNote, error) {
	endpoint := fmt.Sprintf("projects/%s/%s/%d/notes", projectPath, kind, iid)
	var args []string
	if maxEvents > 0 {
		args = []string{"api", fmt.Sprintf("%s?per_page=%d", endpoint, maxEvents)}
	} else {
		args = []string{"api", endpoint, "--paginate"}
	}
	out, err := g.run(g.cmd(), args...)
	if err != nil {
		return nil, fmt.Errorf("failed to get notes for %s #%d: %w", kind, iid, err)
	}
	var notes []glNote
	if err := json.Unmarshal(fixPaginatedJSON(out), &notes); err != nil {
		return nil, fmt.Errorf("failed to parse notes: %w", err)
	}
	return notes, nil
}
```

- [ ] **Step 4: Update caller in `NextItems`**

```go
notes, err := g.getNotes(item.ProjectRef, item.Kind, item.IID, maxEvents)
```

- [ ] **Step 5: Run tests**

Run: `go test ./backend -run TestGitLabMaxEventsLimitsPagination -v && go test ./... -count=1`
Expected: All tests pass.

- [ ] **Step 6: Commit**

```bash
git add backend/gitlab.go backend/gitlab_test.go
git commit -m "Implement --max-events for GitLab getNotes

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

### Task 4: Parallelize GitHub `NextItems` with batch goroutines

**Files:**
- Modify: `backend/github.go:145-392` (`NextItems` method)
- Test: `backend/github_test.go` (new test)

- [ ] **Step 1: Write test for concurrent fetching**

Add to `backend/github_test.go`:

```go
func TestGitHubParallelFetching(t *testing.T) {
	now := time.Now()

	var issues []ghIssue
	for i := 1; i <= 10; i++ {
		issues = append(issues, ghIssue{
			Number:    i,
			Title:     fmt.Sprintf("Issue %d", i),
			HTMLURL:   fmt.Sprintf("https://github.com/o/r/issues/%d", i),
			UpdatedAt: now.Add(-time.Duration(i) * time.Minute),
			User:      ghActor{Login: "other"},
		})
	}

	var concurrentPeak atomic.Int32
	var current atomic.Int32

	runner := func(name string, args ...string) ([]byte, error) {
		for _, a := range args {
			if a == "repos/o/r/issues" {
				return json.Marshal(issues)
			}
			if strings.HasSuffix(a, "/timeline") {
				n := current.Add(1)
				for {
					peak := concurrentPeak.Load()
					if n <= peak {
						break
					}
					if concurrentPeak.CompareAndSwap(peak, n) {
						break
					}
				}
				time.Sleep(10 * time.Millisecond)
				current.Add(-1)
				issueNum := 0
				for _, arg := range args {
					if strings.Contains(arg, "/timeline") {
						fmt.Sscanf(arg, "repos/o/r/issues/%d/timeline", &issueNum)
					}
				}
				return json.Marshal([]ghTimelineEvent{
					{Event: "commented", CreatedAt: now.Add(-time.Duration(issueNum) * time.Minute), Actor: ghActor{Login: "other"}, Body: "comment"},
				})
			}
			if strings.HasSuffix(a, "/reactions") {
				return json.Marshal([]ghReaction{})
			}
			if strings.HasSuffix(a, "/comments") {
				return json.Marshal([]ghComment{})
			}
		}
		return nil, fmt.Errorf("unexpected call: %v", args)
	}

	gh := NewGitHub(runner)
	var items []format.Item
	err := gh.NextItems("o", "r", "me", 30*time.Minute, time.Time{}, nil, nil, 10, 0, ScopeRepo, func(item format.Item) {
		items = append(items, item)
	})
	if err != nil {
		t.Fatalf("NextItems() error: %v", err)
	}
	if len(items) != 10 {
		t.Fatalf("expected 10 items, got %d", len(items))
	}
	// Verify ordering is preserved (most recently updated first)
	if items[0].Title != "Issue 1" {
		t.Errorf("first item should be Issue 1, got %q", items[0].Title)
	}
	if items[9].Title != "Issue 10" {
		t.Errorf("last item should be Issue 10, got %q", items[9].Title)
	}
	// Verify concurrent execution happened (peak > 1)
	if peak := concurrentPeak.Load(); peak <= 1 {
		t.Errorf("expected concurrent execution (peak > 1), got peak=%d", peak)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./backend -run TestGitHubParallelFetching -v`
Expected: FAIL — peak concurrency = 1 (sequential).

- [ ] **Step 3: Add result type and extract `processIssue` helper**

Add to `backend/github.go` after the `perItemArgs` function:

```go
type ghItemResult struct {
	item *format.Item
	err  error
}

func (g *gitHub) processIssue(issue ghIssue, owner, repo, user string, cutoff time.Time, ignoreEvents, ignoreUsers MatchSet, maxEvents int) ghItemResult {
	kind := "issue"
	if issue.PullRequest != nil {
		kind = "PR"
	}
	label := fmt.Sprintf("#%d", issue.Number)
	title := issue.Title
	if r := []rune(title); len(r) > 50 {
		title = string(r[:50]) + "..."
	}

	events, err := g.getTimeline(owner, repo, issue.Number, maxEvents)
	if err != nil {
		return ghItemResult{err: err}
	}
	issueReactions, err := g.getReactions(owner, repo, issue.Number, maxEvents)
	if err != nil {
		return ghItemResult{err: err}
	}
	commentReactions, err := g.getCommentReactions(owner, repo, issue.Number, maxEvents)
	if err != nil {
		return ghItemResult{err: err}
	}
	var reviews []ghReview
	var reviewCommentReactions []ghReaction
	if issue.PullRequest != nil {
		reviews, err = g.getReviews(owner, repo, issue.Number, maxEvents)
		if err != nil {
			return ghItemResult{err: err}
		}
		reviewCommentReactions, err = g.getReviewCommentReactions(owner, repo, issue.Number, maxEvents)
		if err != nil {
			return ghItemResult{err: err}
		}
		var reviewReactions []ghReaction
		reviewReactions, err = g.getReviewReactions(owner, repo, issue.Number, maxEvents)
		if err != nil {
			return ghItemResult{err: err}
		}
		reviewCommentReactions = append(reviewCommentReactions, reviewReactions...)
	}
	reactions := append(issueReactions, commentReactions...)
	reactions = append(reactions, reviewCommentReactions...)

	userTouched := false
	for _, ev := range events {
		if ignoreUsers.Match(ev.login()) {
			continue
		}
		if ignoreEvents.Match(ev.Event) {
			continue
		}
		if ev.login() != "" && ev.login() == user && ev.CreatedAt.After(cutoff) {
			userTouched = true
			break
		}
	}
	if !userTouched {
		for _, r := range reviews {
			if ignoreUsers.Match(r.User.Login) {
				continue
			}
			if r.User.Login == user && r.SubmittedAt.After(cutoff) {
				userTouched = true
				break
			}
		}
	}
	if !userTouched {
		for _, r := range reactions {
			if r.User.Login == user && r.CreatedAt.After(cutoff) {
				userTouched = true
				break
			}
		}
	}
	if userTouched {
		fmt.Fprintf(os.Stderr, "\033[2m  %s %s %s — skipped (cooldown)\033[0m\n", kind, label, title)
		return ghItemResult{}
	}

	var lastUserTime time.Time
	for _, ev := range events {
		if ignoreUsers.Match(ev.login()) {
			continue
		}
		if ignoreEvents.Match(ev.Event) {
			continue
		}
		if ev.login() == user && ev.CreatedAt.After(lastUserTime) {
			lastUserTime = ev.CreatedAt
		}
	}
	for _, r := range reviews {
		if ignoreUsers.Match(r.User.Login) {
			continue
		}
		if r.User.Login == user && r.SubmittedAt.After(lastUserTime) {
			lastUserTime = r.SubmittedAt
		}
	}
	for _, r := range reactions {
		if r.User.Login == user && r.CreatedAt.After(lastUserTime) {
			lastUserTime = r.CreatedAt
		}
	}

	othersHaveActivity := false
	for _, ev := range events {
		if ev.login() != "" && ev.login() != user && !ignoreUsers.Match(ev.login()) && !ignoreEvents.Match(ev.Event) {
			if lastUserTime.IsZero() || ev.CreatedAt.After(lastUserTime) {
				othersHaveActivity = true
				break
			}
		}
	}
	if !othersHaveActivity {
		for _, r := range reviews {
			if r.User.Login != user && !ignoreUsers.Match(r.User.Login) {
				if lastUserTime.IsZero() || r.SubmittedAt.After(lastUserTime) {
					othersHaveActivity = true
					break
				}
			}
		}
	}

	var fmtEvents []format.Event
	for _, ev := range events {
		if ev.login() == "" || ev.CreatedAt.IsZero() {
			continue
		}
		if ignoreEvents.Match(ev.Event) {
			continue
		}
		if ev.login() == user || ignoreUsers.Match(ev.login()) {
			continue
		}
		if !lastUserTime.IsZero() && ev.CreatedAt.Before(lastUserTime) {
			continue
		}
		summary := eventSummary(ev.Event, ev.Body)
		fmtEvents = append(fmtEvents, format.Event{
			Timestamp: ev.CreatedAt,
			Author:    ev.login(),
			Summary:   summary,
		})
	}
	for _, r := range reviews {
		if r.User.Login == user || ignoreUsers.Match(r.User.Login) {
			continue
		}
		if !lastUserTime.IsZero() && r.SubmittedAt.Before(lastUserTime) {
			continue
		}
		summary := reviewSummary(r.State, r.Body)
		fmtEvents = append(fmtEvents, format.Event{
			Timestamp: r.SubmittedAt,
			Author:    r.User.Login,
			Summary:   summary,
		})
	}

	if len(fmtEvents) == 0 {
		if othersHaveActivity || !lastUserTime.IsZero() || issue.User.Login == user {
			fmt.Fprintf(os.Stderr, "\033[2m  %s %s %s — skipped (no new activity)\033[0m\n", kind, label, title)
			return ghItemResult{}
		}
		fmtEvents = append(fmtEvents, format.Event{
			Timestamp: issue.CreatedAt,
			Author:    issue.User.Login,
			Summary:   "opened",
		})
	}

	tier := 3
	if issue.User.Login == user {
		tier = 1
	} else if !lastUserTime.IsZero() {
		tier = 2
	}

	item := format.Item{
		URL:    issue.HTMLURL,
		Title:  issue.Title,
		Events: fmtEvents,
		Tier:   tier,
	}
	return ghItemResult{item: &item}
}
```

- [ ] **Step 4: Rewrite `NextItems` loop with batch processing**

Replace the `found := 0` loop in `NextItems` with:

```go
	found := 0
	for batchStart := 0; batchStart < len(orderedIssues) && found < limit; batchStart += maxConcurrency {
		batchEnd := batchStart + maxConcurrency
		if batchEnd > len(orderedIssues) {
			batchEnd = len(orderedIssues)
		}
		batch := orderedIssues[batchStart:batchEnd]

		type indexedIssue struct {
			idx                int
			issue              ghIssue
			issueOwner, issueRepo string
		}

		var valid []indexedIssue
		for i, issue := range batch {
			issueOwner, issueRepo := owner, repo
			if scope == ScopeOrg {
				issueOwner, issueRepo = parseRepoFromURL(issue.HTMLURL)
				if issueOwner == "" || issueRepo == "" {
					continue
				}
			}
			valid = append(valid, indexedIssue{idx: i, issue: issue, issueOwner: issueOwner, issueRepo: issueRepo})
		}

		results := make([]ghItemResult, len(batch))
		var wg sync.WaitGroup
		for _, vi := range valid {
			wg.Add(1)
			go func(idx int, iss ghIssue, o, r string) {
				defer wg.Done()
				results[idx] = g.processIssue(iss, o, r, user, cutoff, ignoreEvents, ignoreUsers, maxEvents)
			}(vi.idx, vi.issue, vi.issueOwner, vi.issueRepo)
		}
		wg.Wait()

		for _, res := range results {
			if res.err != nil {
				return res.err
			}
			if res.item != nil {
				emit(*res.item)
				found++
				if found >= limit {
					break
				}
			}
		}
	}
```

Add `"sync"` to the imports at the top of `github.go`.

- [ ] **Step 5: Run tests**

Run: `go test ./backend -run TestGitHubParallelFetching -v && go test ./... -count=1`
Expected: All tests pass, peak concurrency > 1.

- [ ] **Step 6: Commit**

```bash
git add backend/github.go backend/github_test.go
git commit -m "Parallelize GitHub per-item detail fetching with batch goroutines

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

### Task 5: Parallelize GitLab `NextItems` with batch goroutines

**Files:**
- Modify: `backend/gitlab.go:101-303` (`NextItems` method)
- Test: `backend/gitlab_test.go` (new test)

- [ ] **Step 1: Write test for concurrent fetching**

Add to `backend/gitlab_test.go`:

```go
func TestGitLabParallelFetching(t *testing.T) {
	now := time.Now()

	var issues []glIssue
	for i := 1; i <= 10; i++ {
		issues = append(issues, glIssue{
			IID:       i,
			Title:     fmt.Sprintf("Issue %d", i),
			WebURL:    fmt.Sprintf("https://gitlab.com/o/r/-/issues/%d", i),
			UpdatedAt: now.Add(-time.Duration(i) * time.Minute),
			Author:    glNoteAuthor{Username: "other"},
		})
	}

	var concurrentPeak atomic.Int32
	var current atomic.Int32

	runner := func(name string, args ...string) ([]byte, error) {
		for _, a := range args {
			if strings.HasPrefix(a, "projects/o%2Fr/issues?") {
				return json.Marshal(issues)
			}
			if strings.HasPrefix(a, "projects/o%2Fr/merge_requests?") {
				return json.Marshal([]glMR{})
			}
			if strings.Contains(a, "/notes") {
				n := current.Add(1)
				for {
					peak := concurrentPeak.Load()
					if n <= peak {
						break
					}
					if concurrentPeak.CompareAndSwap(peak, n) {
						break
					}
				}
				time.Sleep(10 * time.Millisecond)
				current.Add(-1)
				iid := 0
				fmt.Sscanf(a, "projects/o%%2Fr/issues/%d/notes", &iid)
				return json.Marshal([]glNote{
					{Body: "comment", CreatedAt: now.Add(-time.Duration(iid) * time.Minute), Author: glNoteAuthor{Username: "other"}},
				})
			}
		}
		return nil, fmt.Errorf("unexpected call: %v", args)
	}

	gl := NewGitLab(runner, "")
	var items []format.Item
	err := gl.NextItems("o", "r", "me", 30*time.Minute, time.Time{}, nil, nil, 10, 0, ScopeRepo, func(item format.Item) {
		items = append(items, item)
	})
	if err != nil {
		t.Fatalf("NextItems() error: %v", err)
	}
	if len(items) != 10 {
		t.Fatalf("expected 10 items, got %d", len(items))
	}
	if items[0].Title != "Issue 1" {
		t.Errorf("first item should be Issue 1, got %q", items[0].Title)
	}
	if items[9].Title != "Issue 10" {
		t.Errorf("last item should be Issue 10, got %q", items[9].Title)
	}
	if peak := concurrentPeak.Load(); peak <= 1 {
		t.Errorf("expected concurrent execution (peak > 1), got peak=%d", peak)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./backend -run TestGitLabParallelFetching -v`
Expected: FAIL — peak concurrency = 1 (sequential).

- [ ] **Step 3: Add result type and extract `processItem` helper**

Add to `backend/gitlab.go`:

```go
type glItemResult struct {
	item *format.Item
	err  error
}

func (g *gitLab) processItem(item glItem, user string, cutoff time.Time, ignoreEvents, ignoreUsers MatchSet, maxEvents int) glItemResult {
	notes, err := g.getNotes(item.ProjectRef, item.Kind, item.IID, maxEvents)
	if err != nil {
		return glItemResult{err: err}
	}

	kind := "issue"
	label := fmt.Sprintf("#%d", item.IID)
	if item.Kind == "merge_requests" {
		kind = "MR"
		label = fmt.Sprintf("!%d", item.IID)
	}
	title := item.Title
	if r := []rune(title); len(r) > 50 {
		title = string(r[:50]) + "..."
	}

	userTouched := false
	for _, n := range notes {
		if ignoreUsers.Match(n.Author.Username) {
			continue
		}
		if n.System && !isApprovalNote(n.Body) {
			continue
		}
		if n.Author.Username == user && n.CreatedAt.After(cutoff) {
			userTouched = true
			break
		}
	}
	if userTouched {
		fmt.Fprintf(os.Stderr, "\033[2m  %s %s %s — skipped (cooldown)\033[0m\n", kind, label, title)
		return glItemResult{}
	}

	var lastUserTime time.Time
	for _, n := range notes {
		if ignoreUsers.Match(n.Author.Username) {
			continue
		}
		if n.System && !isApprovalNote(n.Body) {
			continue
		}
		if n.Author.Username == user && n.CreatedAt.After(lastUserTime) {
			lastUserTime = n.CreatedAt
		}
	}

	othersHaveActivity := false
	for _, n := range notes {
		if n.Author.Username == user || ignoreUsers.Match(n.Author.Username) {
			continue
		}
		if !n.System || isApprovalNote(n.Body) {
			if lastUserTime.IsZero() || n.CreatedAt.After(lastUserTime) {
				othersHaveActivity = true
				break
			}
		}
	}

	var fmtEvents []format.Event
	for _, n := range notes {
		if n.Author.Username == user || ignoreUsers.Match(n.Author.Username) {
			continue
		}
		if n.System && !isApprovalNote(n.Body) {
			continue
		}
		if !lastUserTime.IsZero() && n.CreatedAt.Before(lastUserTime) {
			continue
		}
		var summary string
		if isApprovalNote(n.Body) {
			summary = "approved"
		} else {
			body := n.Body
			if r := []rune(body); len(r) > 80 {
				body = string(r[:80])
			}
			summary = fmt.Sprintf("commented: > %s", body)
		}
		fmtEvents = append(fmtEvents, format.Event{
			Timestamp: n.CreatedAt,
			Author:    n.Author.Username,
			Summary:   summary,
		})
	}

	if len(fmtEvents) == 0 {
		if othersHaveActivity || !lastUserTime.IsZero() || item.Author == user {
			fmt.Fprintf(os.Stderr, "\033[2m  %s %s %s — skipped (no new activity)\033[0m\n", kind, label, title)
			return glItemResult{}
		}
		fmtEvents = append(fmtEvents, format.Event{
			Timestamp: item.CreatedAt,
			Author:    item.Author,
			Summary:   "opened",
		})
	}

	tier := 3
	if item.Author == user {
		tier = 1
	} else if !lastUserTime.IsZero() {
		tier = 2
	}

	result := format.Item{
		URL:    item.WebURL,
		Title:  item.Title,
		Events: fmtEvents,
		Tier:   tier,
	}
	return glItemResult{item: &result}
}
```

- [ ] **Step 4: Rewrite `NextItems` loop with batch processing**

Replace the `found := 0` loop in `NextItems` with:

```go
	found := 0
	for batchStart := 0; batchStart < len(orderedItems) && found < limit; batchStart += maxConcurrency {
		batchEnd := batchStart + maxConcurrency
		if batchEnd > len(orderedItems) {
			batchEnd = len(orderedItems)
		}
		batch := orderedItems[batchStart:batchEnd]

		results := make([]glItemResult, len(batch))
		var wg sync.WaitGroup
		for i, item := range batch {
			wg.Add(1)
			go func(idx int, it glItem) {
				defer wg.Done()
				results[idx] = g.processItem(it, user, cutoff, ignoreEvents, ignoreUsers, maxEvents)
			}(i, item)
		}
		wg.Wait()

		for _, res := range results {
			if res.err != nil {
				return res.err
			}
			if res.item != nil {
				emit(*res.item)
				found++
				if found >= limit {
					break
				}
			}
		}
	}
```

- [ ] **Step 5: Run tests**

Run: `go test ./backend -run TestGitLabParallelFetching -v && go test ./... -count=1`
Expected: All tests pass, peak concurrency > 1.

- [ ] **Step 6: Run vet**

Run: `go vet ./...`
Expected: Clean.

- [ ] **Step 7: Commit**

```bash
git add backend/gitlab.go backend/gitlab_test.go
git commit -m "Parallelize GitLab per-item detail fetching with batch goroutines

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```
