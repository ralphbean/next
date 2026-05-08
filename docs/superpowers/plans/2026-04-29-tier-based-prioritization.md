# Tier-Based Item Prioritization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace pure-recency sorting with three-tier priority: authored items first, participated items second, general items third. Each tier is internally sorted by most-recent update. Display a color-coded tier label on each item.

**Architecture:** Add a `Tier` field to `format.Item` and a tier label renderer in the `format` package. Modify both backends to collect all qualifying items with tier classification, sort by tier then recency, and emit up to `--limit`. Update `main.go` to render the tier prefix.

**Tech Stack:** Go, ANSI escape codes for terminal colors.

---

### Task 1: Add Tier field and label rendering to format package

**Files:**
- Modify: `format/format.go:9-19` (Item struct)
- Modify: `format/format.go:52-87` (FormatItem, FormatItems)
- Test: `format/format_test.go`

- [ ] **Step 1: Write failing test for TierLabel function**

Add to `format/format_test.go`:

```go
func TestTierLabel(t *testing.T) {
	tests := []struct {
		tier int
		want string
	}{
		{1, "[authored]"},
		{2, "[participated]"},
		{3, "[general]"},
		{0, ""},
	}
	for _, tt := range tests {
		got := TierLabel(tt.tier)
		// Strip ANSI codes for content check
		stripped := stripANSI(got)
		if stripped != tt.want {
			t.Errorf("TierLabel(%d) = %q, want %q", tt.tier, stripped, tt.want)
		}
	}
}

// stripANSI removes ANSI escape sequences for testing.
func stripANSI(s string) string {
	result := s
	for {
		start := strings.Index(result, "\033[")
		if start == -1 {
			break
		}
		end := strings.IndexByte(result[start:], 'm')
		if end == -1 {
			break
		}
		result = result[:start] + result[start+end+1:]
	}
	return result
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./format/ -run TestTierLabel -v`
Expected: compilation error — `TierLabel` not defined.

- [ ] **Step 3: Add Tier field to Item and implement TierLabel**

In `format/format.go`, add `Tier` field to `Item`:

```go
type Item struct {
	URL    string
	Title  string
	Events []Event
	Tier   int
}
```

Add the `TierLabel` function after the `Item` struct:

```go
func TierLabel(tier int) string {
	switch tier {
	case 1:
		return "\033[1;33m[authored]\033[0m"
	case 2:
		return "\033[36m[participated]\033[0m"
	case 3:
		return "\033[2m[general]\033[0m"
	default:
		return ""
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./format/ -run TestTierLabel -v`
Expected: PASS

- [ ] **Step 5: Write failing test for tier label in FormatItem output**

Add to `format/format_test.go`:

```go
func TestFormatItemWithTier(t *testing.T) {
	item := Item{
		URL:   "https://github.com/owner/repo/issues/42",
		Title: "Fix the widget",
		Tier:  1,
		Events: []Event{
			{
				Timestamp: time.Now().Add(-1 * time.Hour),
				Author:    "alice",
				Summary:   "commented: looks good",
			},
		},
	}
	got := FormatItem(item, 120)
	if !strings.Contains(got, "[authored]") {
		t.Errorf("expected [authored] label in output, got:\n%s", got)
	}
	if !strings.Contains(got, item.URL) {
		t.Errorf("expected URL in output")
	}
}

func TestFormatItemWithoutTier(t *testing.T) {
	item := Item{
		URL:   "https://github.com/owner/repo/issues/42",
		Title: "Fix the widget",
		Events: []Event{
			{
				Timestamp: time.Now().Add(-1 * time.Hour),
				Author:    "alice",
				Summary:   "commented: looks good",
			},
		},
	}
	got := FormatItem(item, 120)
	if strings.Contains(got, "[authored]") || strings.Contains(got, "[participated]") || strings.Contains(got, "[general]") {
		t.Errorf("expected no tier label when Tier is 0, got:\n%s", got)
	}
}
```

- [ ] **Step 6: Run test to verify it fails**

Run: `go test ./format/ -run TestFormatItemWith -v`
Expected: FAIL — `[authored]` not in output.

- [ ] **Step 7: Update FormatItem to render tier label**

In `format/format.go`, update `FormatItem`:

```go
func FormatItem(item Item, maxWidth int) string {
	var b strings.Builder
	if label := TierLabel(item.Tier); label != "" {
		fmt.Fprintf(&b, "%s %s\n", label, item.URL)
	} else {
		fmt.Fprintf(&b, "%s\n", item.URL)
	}
	fmt.Fprintf(&b, "%s\n", item.Title)
	for _, e := range item.Events {
		fmt.Fprintf(&b, "  %s\n", FormatEvent(e, maxWidth-2))
	}
	return b.String()
}
```

Update `FormatItems` similarly for the multi-item bullet format:

```go
func FormatItems(items []Item, maxWidth int) string {
	var b strings.Builder
	if len(items) == 1 {
		b.WriteString(FormatItem(items[0], maxWidth))
		return b.String()
	}
	sepWidth := maxWidth
	if sepWidth > 40 {
		sepWidth = 40
	}
	separator := strings.Repeat("─", sepWidth)
	for i, item := range items {
		if i > 0 {
			fmt.Fprintf(&b, "  %s\n", separator)
		}
		if label := TierLabel(item.Tier); label != "" {
			fmt.Fprintf(&b, "▶ %s %s\n", label, item.URL)
		} else {
			fmt.Fprintf(&b, "▶ %s\n", item.URL)
		}
		fmt.Fprintf(&b, "  %s\n", item.Title)
		for _, e := range item.Events {
			fmt.Fprintf(&b, "    %s\n", FormatEvent(e, maxWidth-4))
		}
	}
	return b.String()
}
```

- [ ] **Step 8: Run all format tests**

Run: `go test ./format/ -v`
Expected: ALL PASS

- [ ] **Step 9: Commit**

```bash
git add format/format.go format/format_test.go
git commit -m "feat: add Tier field to Item and color-coded tier labels"
```

---

### Task 2: Refactor GitHub backend to collect, classify, and sort by tier

**Files:**
- Modify: `backend/github.go:145-369` (NextItems method)
- Test: `backend/github_test.go`

The current GitHub `NextItems` emits items one at a time during iteration and stops at `limit`. To support tier-based ordering, it needs to collect all qualifying items first, sort by tier then recency, then emit up to `limit`.

- [ ] **Step 1: Write failing test for tier classification — authored item wins over more-recent general item**

Add to `backend/github_test.go`:

```go
func TestGitHubTierAuthoredBeforeGeneral(t *testing.T) {
	now := time.Now()

	issues := []ghIssue{
		{
			Number:    1,
			Title:     "General issue (more recent)",
			HTMLURL:   "https://github.com/o/r/issues/1",
			UpdatedAt: now.Add(-5 * time.Minute),
			User:      ghActor{Login: "other"},
		},
		{
			Number:    2,
			Title:     "My authored issue (older)",
			HTMLURL:   "https://github.com/o/r/issues/2",
			UpdatedAt: now.Add(-20 * time.Minute),
			User:      ghActor{Login: "me"},
		},
	}

	events1 := []ghTimelineEvent{
		{Event: "commented", CreatedAt: now.Add(-5 * time.Minute), Actor: ghActor{Login: "other"}, Body: "general comment"},
	}
	events2 := []ghTimelineEvent{
		{Event: "commented", CreatedAt: now.Add(-20 * time.Minute), Actor: ghActor{Login: "other"}, Body: "comment on my issue"},
	}

	runner := func(name string, args ...string) ([]byte, error) {
		for i, a := range args {
			if a == "repos/o/r/issues" {
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
			if i > 0 && args[i-1] == "repos/o/r/issues/2/timeline" {
				return json.Marshal(events2)
			}
		}
		return nil, fmt.Errorf("unexpected call: %v", args)
	}

	gh := NewGitHub(runner)
	items := ghCollect(t, gh, "o", "r", "me", 30*time.Minute, nil, nil, 1)
	if len(items) != 1 {
		t.Fatalf("NextItems() returned %d items, want 1", len(items))
	}
	if items[0].Title != "My authored issue (older)" {
		t.Errorf("expected authored issue to win, got %q", items[0].Title)
	}
	if items[0].Tier != 1 {
		t.Errorf("expected Tier 1, got %d", items[0].Tier)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./backend/ -run TestGitHubTierAuthoredBeforeGeneral -v`
Expected: FAIL — general item (issue 1) is returned instead of authored item (issue 2), and Tier is 0.

- [ ] **Step 3: Write failing test for participated tier**

Add to `backend/github_test.go`:

```go
func TestGitHubTierParticipatedBeforeGeneral(t *testing.T) {
	now := time.Now()

	issues := []ghIssue{
		{
			Number:    1,
			Title:     "General issue (more recent)",
			HTMLURL:   "https://github.com/o/r/issues/1",
			UpdatedAt: now.Add(-5 * time.Minute),
			User:      ghActor{Login: "stranger"},
		},
		{
			Number:    2,
			Title:     "Issue I commented on before (older)",
			HTMLURL:   "https://github.com/o/r/issues/2",
			UpdatedAt: now.Add(-20 * time.Minute),
			User:      ghActor{Login: "other"},
		},
	}

	events1 := []ghTimelineEvent{
		{Event: "commented", CreatedAt: now.Add(-5 * time.Minute), Actor: ghActor{Login: "stranger"}, Body: "general comment"},
	}
	events2 := []ghTimelineEvent{
		{Event: "commented", CreatedAt: now.Add(-2 * time.Hour), Actor: ghActor{Login: "me"}, Body: "my old comment"},
		{Event: "commented", CreatedAt: now.Add(-20 * time.Minute), Actor: ghActor{Login: "other"}, Body: "reply to me"},
	}

	runner := func(name string, args ...string) ([]byte, error) {
		for i, a := range args {
			if a == "repos/o/r/issues" {
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
			if i > 0 && args[i-1] == "repos/o/r/issues/2/timeline" {
				return json.Marshal(events2)
			}
		}
		return nil, fmt.Errorf("unexpected call: %v", args)
	}

	gh := NewGitHub(runner)
	items := ghCollect(t, gh, "o", "r", "me", 30*time.Minute, nil, nil, 1)
	if len(items) != 1 {
		t.Fatalf("NextItems() returned %d items, want 1", len(items))
	}
	if items[0].Title != "Issue I commented on before (older)" {
		t.Errorf("expected participated issue to win, got %q", items[0].Title)
	}
	if items[0].Tier != 2 {
		t.Errorf("expected Tier 2, got %d", items[0].Tier)
	}
}
```

- [ ] **Step 4: Run test to verify it fails**

Run: `go test ./backend/ -run TestGitHubTierParticipatedBeforeGeneral -v`
Expected: FAIL

- [ ] **Step 5: Write failing test for all three tiers with limit > 1**

Add to `backend/github_test.go`:

```go
func TestGitHubTierOrdering(t *testing.T) {
	now := time.Now()

	issues := []ghIssue{
		{
			Number:    1,
			Title:     "General issue (most recent)",
			HTMLURL:   "https://github.com/o/r/issues/1",
			UpdatedAt: now.Add(-5 * time.Minute),
			User:      ghActor{Login: "stranger"},
		},
		{
			Number:    2,
			Title:     "Participated issue",
			HTMLURL:   "https://github.com/o/r/issues/2",
			UpdatedAt: now.Add(-15 * time.Minute),
			User:      ghActor{Login: "other"},
		},
		{
			Number:    3,
			Title:     "Authored issue (oldest)",
			HTMLURL:   "https://github.com/o/r/issues/3",
			UpdatedAt: now.Add(-25 * time.Minute),
			User:      ghActor{Login: "me"},
		},
	}

	events1 := []ghTimelineEvent{
		{Event: "commented", CreatedAt: now.Add(-5 * time.Minute), Actor: ghActor{Login: "stranger"}, Body: "hello"},
	}
	events2 := []ghTimelineEvent{
		{Event: "commented", CreatedAt: now.Add(-2 * time.Hour), Actor: ghActor{Login: "me"}, Body: "my old comment"},
		{Event: "commented", CreatedAt: now.Add(-15 * time.Minute), Actor: ghActor{Login: "other"}, Body: "reply"},
	}
	events3 := []ghTimelineEvent{
		{Event: "commented", CreatedAt: now.Add(-25 * time.Minute), Actor: ghActor{Login: "other"}, Body: "comment on my issue"},
	}

	runner := func(name string, args ...string) ([]byte, error) {
		for i, a := range args {
			if a == "repos/o/r/issues" {
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
			if i > 0 && args[i-1] == "repos/o/r/issues/2/timeline" {
				return json.Marshal(events2)
			}
			if i > 0 && args[i-1] == "repos/o/r/issues/3/timeline" {
				return json.Marshal(events3)
			}
		}
		return nil, fmt.Errorf("unexpected call: %v", args)
	}

	gh := NewGitHub(runner)
	items := ghCollect(t, gh, "o", "r", "me", 30*time.Minute, nil, nil, 5)
	if len(items) != 3 {
		t.Fatalf("NextItems() returned %d items, want 3", len(items))
	}
	// Tier order: authored (3), participated (2), general (1)
	if items[0].Title != "Authored issue (oldest)" {
		t.Errorf("first item: expected authored, got %q", items[0].Title)
	}
	if items[0].Tier != 1 {
		t.Errorf("first item: expected Tier 1, got %d", items[0].Tier)
	}
	if items[1].Title != "Participated issue" {
		t.Errorf("second item: expected participated, got %q", items[1].Title)
	}
	if items[1].Tier != 2 {
		t.Errorf("second item: expected Tier 2, got %d", items[1].Tier)
	}
	if items[2].Title != "General issue (most recent)" {
		t.Errorf("third item: expected general, got %q", items[2].Title)
	}
	if items[2].Tier != 3 {
		t.Errorf("third item: expected Tier 3, got %d", items[2].Tier)
	}
}
```

- [ ] **Step 6: Run test to verify it fails**

Run: `go test ./backend/ -run TestGitHubTierOrdering -v`
Expected: FAIL

- [ ] **Step 7: Implement tier-based collection and sorting in GitHub backend**

In `backend/github.go`, replace the `NextItems` method body. The key changes:
1. Instead of emitting items inline and breaking at `limit`, collect all qualifying items into a slice.
2. After filtering, classify each item's tier:
   - Tier 1 if `issue.User.Login == user`
   - Tier 2 if `!lastUserTime.IsZero()` (user has prior interaction)
   - Tier 3 otherwise
3. Sort by tier ascending, then by `UpdatedAt` descending within each tier.
4. Emit up to `limit` items.

Replace the `NextItems` method (lines 145-369) with:

```go
func (g *gitHub) NextItems(owner, repo, user string, since time.Duration, ignoreEvents MatchSet, ignoreUsers MatchSet, limit int, scope Scope, emit func(format.Item)) error {
	var issues []ghIssue
	var err error
	if scope == ScopeOrg {
		issues, err = g.searchOrgIssues(owner)
	} else {
		issues, err = g.listRepoIssues(owner, repo)
	}
	if err != nil {
		return err
	}

	sort.Slice(issues, func(i, j int) bool {
		return issues[i].UpdatedAt.After(issues[j].UpdatedAt)
	})

	cutoff := time.Now().Add(-since)

	type candidate struct {
		item      format.Item
		tier      int
		updatedAt time.Time
	}
	var candidates []candidate

	for _, issue := range issues {
		issueOwner, issueRepo := owner, repo
		if scope == ScopeOrg {
			issueOwner, issueRepo = parseRepoFromURL(issue.HTMLURL)
			if issueOwner == "" || issueRepo == "" {
				continue
			}
		}

		kind := "issue"
		if issue.PullRequest != nil {
			kind = "PR"
		}
		label := fmt.Sprintf("#%d", issue.Number)
		title := issue.Title
		if r := []rune(title); len(r) > 50 {
			title = string(r[:50]) + "..."
		}

		events, err := g.getTimeline(issueOwner, issueRepo, issue.Number)
		if err != nil {
			return err
		}
		issueReactions, err := g.getReactions(issueOwner, issueRepo, issue.Number)
		if err != nil {
			return err
		}
		commentReactions, err := g.getCommentReactions(issueOwner, issueRepo, issue.Number)
		if err != nil {
			return err
		}
		var reviews []ghReview
		var reviewCommentReactions []ghReaction
		if issue.PullRequest != nil {
			reviews, err = g.getReviews(issueOwner, issueRepo, issue.Number)
			if err != nil {
				return err
			}
			reviewCommentReactions, err = g.getReviewCommentReactions(issueOwner, issueRepo, issue.Number)
			if err != nil {
				return err
			}
			var reviewReactions []ghReaction
			reviewReactions, err = g.getReviewReactions(issueOwner, issueRepo, issue.Number)
			if err != nil {
				return err
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
			continue
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
			if othersHaveActivity || !lastUserTime.IsZero() || issue.User.Login == user || ignoreUsers.Match(issue.User.Login) {
				fmt.Fprintf(os.Stderr, "\033[2m  %s %s %s — skipped (no new activity)\033[0m\n", kind, label, title)
				continue
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

		candidates = append(candidates, candidate{
			item: format.Item{
				URL:    issue.HTMLURL,
				Title:  issue.Title,
				Events: fmtEvents,
				Tier:   tier,
			},
			tier:      tier,
			updatedAt: issue.UpdatedAt,
		})
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].tier != candidates[j].tier {
			return candidates[i].tier < candidates[j].tier
		}
		return candidates[i].updatedAt.After(candidates[j].updatedAt)
	})

	for i, c := range candidates {
		if i >= limit {
			break
		}
		emit(c.item)
	}

	return nil
}
```

- [ ] **Step 8: Run new tier tests**

Run: `go test ./backend/ -run "TestGitHubTier" -v`
Expected: ALL PASS

- [ ] **Step 9: Run all existing GitHub tests to check for regressions**

Run: `go test ./backend/ -run "TestGitHub" -v`
Expected: ALL PASS. Existing tests should still pass because the filtering logic is identical — only the ordering changed (and existing tests don't mix tiers).

- [ ] **Step 10: Commit**

```bash
git add backend/github.go backend/github_test.go
git commit -m "feat: add tier-based prioritization to GitHub backend"
```

---

### Task 3: Refactor GitLab backend to collect, classify, and sort by tier

**Files:**
- Modify: `backend/gitlab.go:101-284` (NextItems method)
- Test: `backend/gitlab_test.go`

Same structural change as GitHub: collect all qualifying items with tier classification, sort by tier then recency, emit up to `limit`.

- [ ] **Step 1: Write failing test for tier classification in GitLab**

Add to `backend/gitlab_test.go`:

```go
func TestGitLabTierAuthoredBeforeGeneral(t *testing.T) {
	now := time.Now()

	issues := []glIssue{
		{
			IID:       1,
			Title:     "General issue (more recent)",
			WebURL:    "https://gitlab.com/o/r/-/issues/1",
			UpdatedAt: now.Add(-5 * time.Minute),
			Author:    glNoteAuthor{Username: "stranger"},
		},
		{
			IID:       2,
			Title:     "My authored issue (older)",
			WebURL:    "https://gitlab.com/o/r/-/issues/2",
			UpdatedAt: now.Add(-20 * time.Minute),
			Author:    glNoteAuthor{Username: "me"},
		},
	}

	notes1 := []glNote{
		{Body: "hello", CreatedAt: now.Add(-5 * time.Minute), Author: glNoteAuthor{Username: "stranger"}},
	}
	notes2 := []glNote{
		{Body: "comment on my issue", CreatedAt: now.Add(-20 * time.Minute), Author: glNoteAuthor{Username: "other"}},
	}

	runner := func(name string, args ...string) ([]byte, error) {
		for _, a := range args {
			if strings.HasPrefix(a, "projects/o%2Fr/issues?") {
				return json.Marshal(issues)
			}
			if strings.HasPrefix(a, "projects/o%2Fr/merge_requests?") {
				return json.Marshal([]glMR{})
			}
			if a == "projects/o%2Fr/issues/1/notes" {
				return json.Marshal(notes1)
			}
			if a == "projects/o%2Fr/issues/2/notes" {
				return json.Marshal(notes2)
			}
		}
		return nil, fmt.Errorf("unexpected call: %v", args)
	}

	gl := NewGitLab(runner, "")
	items := glCollect(t, gl, "o", "r", "me", 30*time.Minute, nil, nil, 1)
	if len(items) != 1 {
		t.Fatalf("NextItems() returned %d items, want 1", len(items))
	}
	if items[0].Title != "My authored issue (older)" {
		t.Errorf("expected authored issue to win, got %q", items[0].Title)
	}
	if items[0].Tier != 1 {
		t.Errorf("expected Tier 1, got %d", items[0].Tier)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./backend/ -run TestGitLabTierAuthoredBeforeGeneral -v`
Expected: FAIL

- [ ] **Step 3: Write failing test for all three tiers in GitLab**

Add to `backend/gitlab_test.go`:

```go
func TestGitLabTierOrdering(t *testing.T) {
	now := time.Now()

	issues := []glIssue{
		{
			IID:       1,
			Title:     "General issue (most recent)",
			WebURL:    "https://gitlab.com/o/r/-/issues/1",
			UpdatedAt: now.Add(-5 * time.Minute),
			Author:    glNoteAuthor{Username: "stranger"},
		},
		{
			IID:       2,
			Title:     "Participated issue",
			WebURL:    "https://gitlab.com/o/r/-/issues/2",
			UpdatedAt: now.Add(-15 * time.Minute),
			Author:    glNoteAuthor{Username: "other"},
		},
		{
			IID:       3,
			Title:     "Authored issue (oldest)",
			WebURL:    "https://gitlab.com/o/r/-/issues/3",
			UpdatedAt: now.Add(-25 * time.Minute),
			Author:    glNoteAuthor{Username: "me"},
		},
	}

	notes1 := []glNote{
		{Body: "hello", CreatedAt: now.Add(-5 * time.Minute), Author: glNoteAuthor{Username: "stranger"}},
	}
	notes2 := []glNote{
		{Body: "my old comment", CreatedAt: now.Add(-2 * time.Hour), Author: glNoteAuthor{Username: "me"}},
		{Body: "reply", CreatedAt: now.Add(-15 * time.Minute), Author: glNoteAuthor{Username: "other"}},
	}
	notes3 := []glNote{
		{Body: "comment on my issue", CreatedAt: now.Add(-25 * time.Minute), Author: glNoteAuthor{Username: "other"}},
	}

	runner := func(name string, args ...string) ([]byte, error) {
		for _, a := range args {
			if strings.HasPrefix(a, "projects/o%2Fr/issues?") {
				return json.Marshal(issues)
			}
			if strings.HasPrefix(a, "projects/o%2Fr/merge_requests?") {
				return json.Marshal([]glMR{})
			}
			if a == "projects/o%2Fr/issues/1/notes" {
				return json.Marshal(notes1)
			}
			if a == "projects/o%2Fr/issues/2/notes" {
				return json.Marshal(notes2)
			}
			if a == "projects/o%2Fr/issues/3/notes" {
				return json.Marshal(notes3)
			}
		}
		return nil, fmt.Errorf("unexpected call: %v", args)
	}

	gl := NewGitLab(runner, "")
	items := glCollect(t, gl, "o", "r", "me", 30*time.Minute, nil, nil, 5)
	if len(items) != 3 {
		t.Fatalf("NextItems() returned %d items, want 3", len(items))
	}
	if items[0].Title != "Authored issue (oldest)" {
		t.Errorf("first item: expected authored, got %q", items[0].Title)
	}
	if items[0].Tier != 1 {
		t.Errorf("first item: expected Tier 1, got %d", items[0].Tier)
	}
	if items[1].Title != "Participated issue" {
		t.Errorf("second item: expected participated, got %q", items[1].Title)
	}
	if items[1].Tier != 2 {
		t.Errorf("second item: expected Tier 2, got %d", items[1].Tier)
	}
	if items[2].Title != "General issue (most recent)" {
		t.Errorf("third item: expected general, got %q", items[2].Title)
	}
	if items[2].Tier != 3 {
		t.Errorf("third item: expected Tier 3, got %d", items[2].Tier)
	}
}
```

- [ ] **Step 4: Run test to verify it fails**

Run: `go test ./backend/ -run TestGitLabTierOrdering -v`
Expected: FAIL

- [ ] **Step 5: Implement tier-based collection and sorting in GitLab backend**

In `backend/gitlab.go`, replace the `NextItems` method body (lines 101-284). Same pattern as GitHub:
1. Collect all qualifying items into a `candidates` slice with tier classification.
2. Tier 1 if `item.Author == user`, Tier 2 if `!lastUserTime.IsZero()`, Tier 3 otherwise.
3. Sort by tier ascending, then `UpdatedAt` descending.
4. Emit up to `limit`.

```go
func (g *gitLab) NextItems(owner, repo, user string, since time.Duration, ignoreEvents MatchSet, ignoreUsers MatchSet, limit int, scope Scope, emit func(format.Item)) error {
	var issues []glIssue
	var mrs []glMR
	var issErr, mrErr error
	var listWg sync.WaitGroup
	listWg.Add(2)
	if scope == ScopeOrg {
		groupPath := url.PathEscape(owner)
		go func() {
			defer listWg.Done()
			issues, issErr = g.listGroupIssues(groupPath)
		}()
		go func() {
			defer listWg.Done()
			mrs, mrErr = g.listGroupMRs(groupPath)
		}()
	} else {
		projectPath := url.PathEscape(owner + "/" + repo)
		go func() {
			defer listWg.Done()
			issues, issErr = g.listIssues(projectPath)
		}()
		go func() {
			defer listWg.Done()
			mrs, mrErr = g.listMRs(projectPath)
		}()
	}
	listWg.Wait()
	if issErr != nil {
		return issErr
	}
	if mrErr != nil {
		return mrErr
	}

	var items []glItem
	projectRef := url.PathEscape(owner + "/" + repo)
	for _, iss := range issues {
		ref := projectRef
		if scope == ScopeOrg {
			ref = fmt.Sprintf("%d", iss.ProjectID)
		}
		items = append(items, glItem{
			IID: iss.IID, Title: iss.Title, WebURL: iss.WebURL,
			CreatedAt: iss.CreatedAt, UpdatedAt: iss.UpdatedAt,
			Author: iss.Author.Username, Kind: "issues",
			ProjectRef: ref,
		})
	}
	for _, mr := range mrs {
		ref := projectRef
		if scope == ScopeOrg {
			ref = fmt.Sprintf("%d", mr.ProjectID)
		}
		items = append(items, glItem{
			IID: mr.IID, Title: mr.Title, WebURL: mr.WebURL,
			CreatedAt: mr.CreatedAt, UpdatedAt: mr.UpdatedAt,
			Author: mr.Author.Username, Kind: "merge_requests",
			ProjectRef: ref,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})

	cutoff := time.Now().Add(-since)

	type candidate struct {
		item      format.Item
		tier      int
		updatedAt time.Time
	}
	var candidates []candidate

	for _, item := range items {
		notes, err := g.getNotes(item.ProjectRef, item.Kind, item.IID)
		if err != nil {
			return err
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
			continue
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
			if othersHaveActivity || !lastUserTime.IsZero() || item.Author == user || ignoreUsers.Match(item.Author) {
				fmt.Fprintf(os.Stderr, "\033[2m  %s %s %s — skipped (no new activity)\033[0m\n", kind, label, title)
				continue
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

		candidates = append(candidates, candidate{
			item: format.Item{
				URL:    item.WebURL,
				Title:  item.Title,
				Events: fmtEvents,
				Tier:   tier,
			},
			tier:      tier,
			updatedAt: item.UpdatedAt,
		})
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].tier != candidates[j].tier {
			return candidates[i].tier < candidates[j].tier
		}
		return candidates[i].updatedAt.After(candidates[j].updatedAt)
	})

	for i, c := range candidates {
		if i >= limit {
			break
		}
		emit(c.item)
	}

	return nil
}
```

- [ ] **Step 6: Run new GitLab tier tests**

Run: `go test ./backend/ -run "TestGitLabTier" -v`
Expected: ALL PASS

- [ ] **Step 7: Run all existing GitLab tests to check for regressions**

Run: `go test ./backend/ -run "TestGitLab" -v`
Expected: ALL PASS

- [ ] **Step 8: Commit**

```bash
git add backend/gitlab.go backend/gitlab_test.go
git commit -m "feat: add tier-based prioritization to GitLab backend"
```

---

### Task 4: Update main.go display to render tier label

**Files:**
- Modify: `main.go:108-123` (emit callback)
- Test: `main_test.go`

- [ ] **Step 1: Read main_test.go to understand existing test patterns**

Run: `cat main_test.go`

Check if there are existing tests for the emit callback output. If not, we'll rely on the format package tests (Task 1) which already verify the tier label rendering.

- [ ] **Step 2: Update the emit callback in main.go**

The current `emit` callback in `main.go` (lines 108-123) manually formats output. Update it to use `format.FormatItem` and `format.FormatItems` which now handle tier labels, or update the inline formatting to include the tier label.

Replace the emit callback (lines 108-123) with:

```go
	emit := func(item format.Item) {
		emitted++
		if *limit == 1 {
			fmt.Printf("\033[1m%s\033[0m", format.FormatItem(item, width))
			return
		}
		if emitted > 1 {
			fmt.Printf("  %s\n", separator)
		}
		if label := format.TierLabel(item.Tier); label != "" {
			fmt.Printf("\033[1m▶ %s %s\n", label, item.URL)
		} else {
			fmt.Printf("\033[1m▶ %s\n", item.URL)
		}
		fmt.Printf("  %s\n", item.Title)
		for _, e := range item.Events {
			fmt.Printf("    %s\n", format.FormatEvent(e, width-4))
		}
		fmt.Print("\033[0m")
	}
```

- [ ] **Step 3: Run all tests**

Run: `go test ./... -v`
Expected: ALL PASS

- [ ] **Step 4: Build and verify**

Run: `go build -o next-up . && go vet ./...`
Expected: builds cleanly, no vet warnings.

- [ ] **Step 5: Commit**

```bash
git add main.go
git commit -m "feat: render tier label in CLI output"
```