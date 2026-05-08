# --since/--cooldown Rename and API-Level Time Filtering Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rename the existing `--since` flag to `--cooldown`, add a new `--since` flag that filters at the API level (default `24h`), drastically reducing API calls for repos with many open items.

**Architecture:** The Backend interface gains a `since time.Time` parameter while the existing `since time.Duration` parameter is renamed to `cooldown`. Each platform backend passes the timestamp to its list/search API calls. The CLI parses both flags and computes the absolute timestamp before calling `NextItems`.

**Tech Stack:** Go standard library (`time`, `flag`), `gh api` (GitHub), `glab api` (GitLab)

---

### File Structure

| File | Action | Responsibility |
|------|--------|----------------|
| `backend/backend.go` | Modify | Update `Backend` interface signature |
| `backend/github.go` | Modify | Rename `since` to `cooldown`, accept `since time.Time`, pass to list functions |
| `backend/github_test.go` | Modify | Update all call sites + add `since` timestamp forwarding tests |
| `backend/gitlab.go` | Modify | Rename `since` to `cooldown`, accept `since time.Time`, pass to list functions |
| `backend/gitlab_test.go` | Modify | Update all call sites + add `since` timestamp forwarding tests |
| `main.go` | Modify | Rename `--since` to `--cooldown`, add new `--since` flag, compute timestamp |
| `CLAUDE.md` | Modify | Update CLI Flags section |

---

### Task 1: Update Backend Interface and GitHub Implementation

**Files:**
- Modify: `backend/backend.go:56-59`
- Modify: `backend/github.go:145` (NextItems signature)
- Modify: `backend/github.go:162` (cutoff variable)
- Modify: `backend/github.go:399-417` (listRepoIssues)
- Modify: `backend/github.go:419-438` (searchOrgIssues)
- Test: `backend/github_test.go`

- [ ] **Step 1: Update the Backend interface signature**

In `backend/backend.go`, change the `NextItems` signature to rename `since` to `cooldown` and add `since time.Time`:

```go
type Backend interface {
	CurrentUser() (string, error)
	NextItems(owner, repo, user string, cooldown time.Duration, since time.Time, ignoreEvents MatchSet, ignoreUsers MatchSet, limit int, scope Scope, emit func(format.Item)) error
}
```

- [ ] **Step 2: Update the GitHub NextItems signature and internal references**

In `backend/github.go`, update the `NextItems` method signature:

```go
func (g *gitHub) NextItems(owner, repo, user string, cooldown time.Duration, since time.Time, ignoreEvents MatchSet, ignoreUsers MatchSet, limit int, scope Scope, emit func(format.Item)) error {
```

Rename the `since` parameter usage on line 162 from:

```go
cutoff := time.Now().Add(-since)
```

to:

```go
cutoff := time.Now().Add(-cooldown)
```

- [ ] **Step 3: Add `since` parameter to `listRepoIssues`**

Change the signature and add the `since` filter:

```go
func (g *gitHub) listRepoIssues(owner, repo string, since time.Time) ([]ghIssue, error) {
	endpoint := fmt.Sprintf("repos/%s/%s/issues", owner, repo)
	args := []string{"api", endpoint,
		"--paginate",
		"--method", "GET",
		"-f", "state=open",
		"-f", "sort=updated",
		"-f", "direction=desc",
		"-f", "per_page=100",
	}
	if !since.IsZero() {
		args = append(args, "-f", "since="+since.Format(time.RFC3339))
	}
	out, err := g.runAPI("gh", args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list issues: %w", err)
	}
	var issues []ghIssue
	if err := json.Unmarshal(out, &issues); err != nil {
		return nil, fmt.Errorf("failed to parse issues: %w", err)
	}
	return issues, nil
}
```

- [ ] **Step 4: Add `since` parameter to `searchOrgIssues`**

Change the signature and add the `updated:>=` filter:

```go
func (g *gitHub) searchOrgIssues(org string, since time.Time) ([]ghIssue, error) {
	query := fmt.Sprintf("org:%s is:open", org)
	if !since.IsZero() {
		query += fmt.Sprintf(" updated:>=%s", since.Format("2006-01-02"))
	}
	out, err := g.runAPI("gh", "api", "search/issues",
		"--method", "GET",
		"-f", "q="+query,
		"-f", "sort=updated",
		"-f", "order=desc",
		"-f", "per_page=100",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to search org issues: %w", err)
	}
	var result struct {
		Items []ghIssue `json:"items"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("failed to parse search results: %w", err)
	}
	return result.Items, nil
}
```

- [ ] **Step 5: Update the call sites in NextItems**

In `NextItems`, update the calls to pass the `since` parameter:

```go
if scope == ScopeOrg {
	issues, err = g.searchOrgIssues(owner, since)
} else {
	issues, err = g.listRepoIssues(owner, repo, since)
}
```

- [ ] **Step 6: Update `ghCollect` helper and all existing test call sites**

In `backend/github_test.go`, update the `ghCollect` helper to match the new signature:

```go
func ghCollect(t *testing.T, gh Backend, owner, repo, user string, cooldown time.Duration, ignoreEvents, ignoreUsers MatchSet, limit int) []format.Item {
	t.Helper()
	var items []format.Item
	err := gh.NextItems(owner, repo, user, cooldown, time.Time{}, ignoreEvents, ignoreUsers, limit, ScopeRepo, func(item format.Item) {
		items = append(items, item)
	})
	if err != nil {
		t.Fatalf("NextItems() error: %v", err)
	}
	return items
}
```

Update the org-scope test (`TestGitHubNextItemsOrgScope`) call on line 1131:

```go
err := gh.NextItems("myorg", "", "me", 30*time.Minute, time.Time{}, nil, nil, 2, ScopeOrg, func(item format.Item) {
```

- [ ] **Step 7: Add test for `since` timestamp forwarding to `listRepoIssues`**

```go
func TestGitHubSincePassedToAPI(t *testing.T) {
	now := time.Now()
	sinceTime := now.Add(-24 * time.Hour)

	issues := []ghIssue{
		{
			Number:    1,
			Title:     "Recent issue",
			HTMLURL:   "https://github.com/o/r/issues/1",
			UpdatedAt: now.Add(-10 * time.Minute),
			User:      ghActor{Login: "other"},
		},
	}
	events1 := []ghTimelineEvent{
		{Event: "commented", CreatedAt: now.Add(-10 * time.Minute), Actor: ghActor{Login: "other"}, Body: "hello"},
	}

	var capturedArgs []string
	runner := func(name string, args ...string) ([]byte, error) {
		for i, a := range args {
			if a == "repos/o/r/issues" {
				capturedArgs = args
				return json.Marshal(issues)
			}
			if strings.HasSuffix(a, "/reactions") {
				return json.Marshal([]ghReaction{})
			}
			if strings.HasSuffix(a, "/comments") {
				return json.Marshal([]ghComment{})
			}
			if i > 0 && args[i-1] == "repos/o/r/issues/1/timeline" {
				return json.Marshal(events1)
			}
		}
		return nil, fmt.Errorf("unexpected call: %v", args)
	}

	gh := NewGitHub(runner)
	var items []format.Item
	err := gh.NextItems("o", "r", "me", 30*time.Minute, sinceTime, nil, nil, 5, ScopeRepo, func(item format.Item) {
		items = append(items, item)
	})
	if err != nil {
		t.Fatalf("NextItems() error: %v", err)
	}

	// Verify since parameter was passed to the API call
	found := false
	for i, a := range capturedArgs {
		if a == "-f" && i+1 < len(capturedArgs) && strings.HasPrefix(capturedArgs[i+1], "since=") {
			found = true
			val := strings.TrimPrefix(capturedArgs[i+1], "since=")
			parsed, err := time.Parse(time.RFC3339, val)
			if err != nil {
				t.Fatalf("failed to parse since value %q: %v", val, err)
			}
			if parsed.Sub(sinceTime).Abs() > time.Second {
				t.Errorf("since time mismatch: got %v, want %v", parsed, sinceTime)
			}
			break
		}
	}
	if !found {
		t.Errorf("expected since= parameter in API call args: %v", capturedArgs)
	}
}
```

- [ ] **Step 8: Add test for `since` timestamp forwarding to `searchOrgIssues`**

```go
func TestGitHubSincePassedToOrgSearch(t *testing.T) {
	now := time.Now()
	sinceTime := now.Add(-24 * time.Hour)

	searchResult := map[string]interface{}{
		"total_count": 1,
		"items": []ghIssue{
			{
				Number:    1,
				Title:     "Org issue",
				HTMLURL:   "https://github.com/myorg/repo/issues/1",
				UpdatedAt: now.Add(-10 * time.Minute),
				User:      ghActor{Login: "other"},
			},
		},
	}
	events1 := []ghTimelineEvent{
		{Event: "commented", CreatedAt: now.Add(-10 * time.Minute), Actor: ghActor{Login: "other"}, Body: "hello"},
	}

	var capturedQuery string
	runner := func(name string, args ...string) ([]byte, error) {
		for i, a := range args {
			if a == "search/issues" {
				for j, arg := range args {
					if arg == "-f" && j+1 < len(args) && strings.HasPrefix(args[j+1], "q=") {
						capturedQuery = strings.TrimPrefix(args[j+1], "q=")
					}
				}
				return json.Marshal(searchResult)
			}
			if strings.HasSuffix(a, "/reactions") {
				return json.Marshal([]ghReaction{})
			}
			if strings.HasSuffix(a, "/comments") {
				return json.Marshal([]ghComment{})
			}
			if i > 0 && args[i-1] == "repos/myorg/repo/issues/1/timeline" {
				return json.Marshal(events1)
			}
		}
		return nil, fmt.Errorf("unexpected call: %v", args)
	}

	gh := NewGitHub(runner)
	var items []format.Item
	err := gh.NextItems("myorg", "", "me", 30*time.Minute, sinceTime, nil, nil, 5, ScopeOrg, func(item format.Item) {
		items = append(items, item)
	})
	if err != nil {
		t.Fatalf("NextItems() error: %v", err)
	}

	expectedDate := sinceTime.Format("2006-01-02")
	expectedFragment := "updated:>=" + expectedDate
	if !strings.Contains(capturedQuery, expectedFragment) {
		t.Errorf("expected query to contain %q, got %q", expectedFragment, capturedQuery)
	}
}
```

- [ ] **Step 9: Run tests to verify everything passes**

Run: `go test ./backend/ -v -count=1`
Expected: All existing tests pass with updated signatures, new tests pass.

- [ ] **Step 10: Commit**

```bash
git add backend/backend.go backend/github.go backend/github_test.go
git commit -m "Rename since to cooldown in Backend interface, add since time.Time for API-level filtering (GitHub)"
```

---

### Task 2: Update GitLab Backend

**Files:**
- Modify: `backend/gitlab.go:101` (NextItems signature)
- Modify: `backend/gitlab.go:168` (cutoff variable)
- Modify: `backend/gitlab.go:314-325` (listIssues)
- Modify: `backend/gitlab.go:327-338` (listMRs)
- Modify: `backend/gitlab.go:340-351` (listGroupIssues)
- Modify: `backend/gitlab.go:353-364` (listGroupMRs)
- Test: `backend/gitlab_test.go`

- [ ] **Step 1: Update the GitLab NextItems signature and internal references**

In `backend/gitlab.go`, update the `NextItems` method signature:

```go
func (g *gitLab) NextItems(owner, repo, user string, cooldown time.Duration, since time.Time, ignoreEvents MatchSet, ignoreUsers MatchSet, limit int, scope Scope, emit func(format.Item)) error {
```

Rename the `since` usage on line 168 from:

```go
cutoff := time.Now().Add(-since)
```

to:

```go
cutoff := time.Now().Add(-cooldown)
```

- [ ] **Step 2: Add `since` parameter to all four list functions**

Update `listIssues`:

```go
func (g *gitLab) listIssues(projectPath string, since time.Time) ([]glIssue, error) {
	endpoint := fmt.Sprintf("projects/%s/issues?state=opened&order_by=updated_at&sort=desc&per_page=100", projectPath)
	if !since.IsZero() {
		endpoint += "&updated_after=" + since.Format(time.RFC3339)
	}
	out, err := g.run(g.cmd(), "api", endpoint, "--paginate")
	if err != nil {
		return nil, fmt.Errorf("failed to list GitLab issues: %w", err)
	}
	var issues []glIssue
	if err := json.Unmarshal(fixPaginatedJSON(out), &issues); err != nil {
		return nil, fmt.Errorf("failed to parse GitLab issues: %w", err)
	}
	return issues, nil
}
```

Update `listMRs`:

```go
func (g *gitLab) listMRs(projectPath string, since time.Time) ([]glMR, error) {
	endpoint := fmt.Sprintf("projects/%s/merge_requests?state=opened&order_by=updated_at&sort=desc&per_page=100", projectPath)
	if !since.IsZero() {
		endpoint += "&updated_after=" + since.Format(time.RFC3339)
	}
	out, err := g.run(g.cmd(), "api", endpoint, "--paginate")
	if err != nil {
		return nil, fmt.Errorf("failed to list GitLab MRs: %w", err)
	}
	var mrs []glMR
	if err := json.Unmarshal(fixPaginatedJSON(out), &mrs); err != nil {
		return nil, fmt.Errorf("failed to parse GitLab MRs: %w", err)
	}
	return mrs, nil
}
```

Update `listGroupIssues`:

```go
func (g *gitLab) listGroupIssues(groupPath string, since time.Time) ([]glIssue, error) {
	endpoint := fmt.Sprintf("groups/%s/issues?state=opened&order_by=updated_at&sort=desc&per_page=100", groupPath)
	if !since.IsZero() {
		endpoint += "&updated_after=" + since.Format(time.RFC3339)
	}
	out, err := g.run(g.cmd(), "api", endpoint, "--paginate")
	if err != nil {
		return nil, fmt.Errorf("failed to list group issues: %w", err)
	}
	var issues []glIssue
	if err := json.Unmarshal(fixPaginatedJSON(out), &issues); err != nil {
		return nil, fmt.Errorf("failed to parse group issues: %w", err)
	}
	return issues, nil
}
```

Update `listGroupMRs`:

```go
func (g *gitLab) listGroupMRs(groupPath string, since time.Time) ([]glMR, error) {
	endpoint := fmt.Sprintf("groups/%s/merge_requests?state=opened&order_by=updated_at&sort=desc&per_page=100", groupPath)
	if !since.IsZero() {
		endpoint += "&updated_after=" + since.Format(time.RFC3339)
	}
	out, err := g.run(g.cmd(), "api", endpoint, "--paginate")
	if err != nil {
		return nil, fmt.Errorf("failed to list group MRs: %w", err)
	}
	var mrs []glMR
	if err := json.Unmarshal(fixPaginatedJSON(out), &mrs); err != nil {
		return nil, fmt.Errorf("failed to parse group MRs: %w", err)
	}
	return mrs, nil
}
```

- [ ] **Step 3: Update the call sites in NextItems**

Update all four goroutine calls inside `NextItems` to pass `since`:

```go
if scope == ScopeOrg {
	groupPath := url.PathEscape(owner)
	go func() {
		defer listWg.Done()
		issues, issErr = g.listGroupIssues(groupPath, since)
	}()
	go func() {
		defer listWg.Done()
		mrs, mrErr = g.listGroupMRs(groupPath, since)
	}()
} else {
	projectPath := url.PathEscape(owner + "/" + repo)
	go func() {
		defer listWg.Done()
		issues, issErr = g.listIssues(projectPath, since)
	}()
	go func() {
		defer listWg.Done()
		mrs, mrErr = g.listMRs(projectPath, since)
	}()
}
```

- [ ] **Step 4: Update `glCollect` helper and all existing test call sites**

In `backend/gitlab_test.go`, update the `glCollect` helper:

```go
func glCollect(t *testing.T, gl Backend, owner, repo, user string, cooldown time.Duration, ignoreEvents, ignoreUsers MatchSet, limit int) []format.Item {
	t.Helper()
	var items []format.Item
	err := gl.NextItems(owner, repo, user, cooldown, time.Time{}, ignoreEvents, ignoreUsers, limit, ScopeRepo, func(item format.Item) {
		items = append(items, item)
	})
	if err != nil {
		t.Fatalf("NextItems() error: %v", err)
	}
	return items
}
```

Update the org-scope test (`TestGitLabNextItemsOrgScope`) call on line 329:

```go
err := gl.NextItems("mygroup", "", "me", 30*time.Minute, time.Time{}, nil, nil, 5, ScopeOrg, func(item format.Item) {
```

- [ ] **Step 5: Add test for `since` timestamp forwarding to GitLab API**

```go
func TestGitLabSincePassedToAPI(t *testing.T) {
	now := time.Now()
	sinceTime := now.Add(-24 * time.Hour)

	issues := []glIssue{
		{
			IID:       1,
			Title:     "Recent issue",
			WebURL:    "https://gitlab.com/o/r/-/issues/1",
			UpdatedAt: now.Add(-10 * time.Minute),
			Author:    glNoteAuthor{Username: "other"},
		},
	}
	notes1 := []glNote{
		{Body: "hello", CreatedAt: now.Add(-10 * time.Minute), Author: glNoteAuthor{Username: "other"}},
	}

	var capturedIssueEndpoint, capturedMREndpoint string
	runner := func(name string, args ...string) ([]byte, error) {
		for _, a := range args {
			if strings.HasPrefix(a, "projects/o%2Fr/issues?") {
				capturedIssueEndpoint = a
				return json.Marshal(issues)
			}
			if strings.HasPrefix(a, "projects/o%2Fr/merge_requests?") {
				capturedMREndpoint = a
				return json.Marshal([]glMR{})
			}
			if a == "projects/o%2Fr/issues/1/notes" {
				return json.Marshal(notes1)
			}
		}
		return nil, fmt.Errorf("unexpected call: %v", args)
	}

	gl := NewGitLab(runner, "")
	var items []format.Item
	err := gl.NextItems("o", "r", "me", 30*time.Minute, sinceTime, nil, nil, 5, ScopeRepo, func(item format.Item) {
		items = append(items, item)
	})
	if err != nil {
		t.Fatalf("NextItems() error: %v", err)
	}

	expectedParam := "updated_after=" + sinceTime.Format(time.RFC3339)
	if !strings.Contains(capturedIssueEndpoint, expectedParam) {
		t.Errorf("expected issue endpoint to contain %q, got %q", expectedParam, capturedIssueEndpoint)
	}
	if !strings.Contains(capturedMREndpoint, expectedParam) {
		t.Errorf("expected MR endpoint to contain %q, got %q", expectedParam, capturedMREndpoint)
	}
}
```

- [ ] **Step 6: Run tests to verify everything passes**

Run: `go test ./backend/ -v -count=1`
Expected: All tests pass.

- [ ] **Step 7: Commit**

```bash
git add backend/gitlab.go backend/gitlab_test.go
git commit -m "Add since time.Time API-level filtering to GitLab backend"
```

---

### Task 3: Update CLI Flags in main.go

**Files:**
- Modify: `main.go:20-22` (parseSince function)
- Modify: `main.go:70-79` (flag definitions)
- Modify: `main.go:104-107` (since parsing)
- Modify: `main.go:180` (NextItems call)

- [ ] **Step 1: Rename `parseSince` to `parseDuration` (it's used for both flags now)**

In `main.go`, rename the function:

```go
func parseDuration(s string) (time.Duration, error) {
	return duration.Parse(s)
}
```

- [ ] **Step 2: Update flag definitions**

Replace the `sinceStr` flag definition on line 71 with both flags:

```go
cooldownStr := flag.String("cooldown", "30m", "cooldown before showing items you recently touched (e.g., 30m, 1h, 3d)")
sinceStr := flag.String("since", "24h", "only fetch items updated within this window (e.g., 1h, 3d, 7d)")
```

- [ ] **Step 3: Update the duration parsing block**

Replace lines 104-107 with parsing for both flags:

```go
cooldown, err := parseDuration(*cooldownStr)
if err != nil {
	return fmt.Errorf("invalid --cooldown value: %w", err)
}

sinceDuration, err := parseDuration(*sinceStr)
if err != nil {
	return fmt.Errorf("invalid --since value: %w", err)
}
sinceTime := time.Now().Add(-sinceDuration)
```

- [ ] **Step 4: Update the NextItems call**

Replace line 180:

```go
err = b.NextItems(info.Owner, info.Name, user, cooldown, sinceTime, ignore, ignoreUsers, *limit, scope, emit)
```

- [ ] **Step 5: Run the full test suite**

Run: `go test ./... -count=1`
Expected: All tests pass.

- [ ] **Step 6: Build and verify help output**

Run: `go build -o next-up . && ./next-up --help`
Expected: Help shows both `--cooldown` (default 30m) and `--since` (default 24h).

- [ ] **Step 7: Commit**

```bash
git add main.go
git commit -m "Rename --since to --cooldown, add new --since for API-level time filtering (default 24h)"
```

---

### Task 4: Update CLAUDE.md

**Files:**
- Modify: `CLAUDE.md:30-32` (CLI Flags section)

- [ ] **Step 1: Update the CLI Flags section**

Replace the current CLI Flags section with:

```markdown
## CLI Flags

- `--cooldown <duration>` — cooldown period before an item the user touched reappears (default `30m`). Accepts Go-style durations plus `d` for days (e.g., `1h`, `3d`).
- `--since <duration>` — only fetch items updated within this window, passed to the API to reduce calls (default `24h`). Accepts the same duration format.
- `--ignore-events <patterns>` — comma-separated list of event patterns to ignore (supports `*` wildcards).
- `--ignore-users <patterns>` — comma-separated list of user patterns to ignore (supports `*` wildcards).
- `--limit <n>` — maximum number of items to show (default `1`).
- `--scope <repo|org>` — search within the current repo or across all repos in the org.
- `--auto-open` — automatically open each result in the browser.
- `--show-config` — show configured remotes for all repos.
- `--config` — interactive remote configuration.
```

- [ ] **Step 2: Commit**

```bash
git add CLAUDE.md
git commit -m "Update CLAUDE.md CLI flags docs for --cooldown/--since rename"
```
