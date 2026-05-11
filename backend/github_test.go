package backend

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ralphbean/next/format"
)

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

func TestGitHubCurrentUser(t *testing.T) {
	runner := func(name string, args ...string) ([]byte, error) {
		return []byte(`{"login":"testuser"}` + "\n"), nil
	}
	gh := NewGitHub(runner)
	user, err := gh.CurrentUser()
	if err != nil {
		t.Fatalf("CurrentUser() error: %v", err)
	}
	if user != "testuser" {
		t.Errorf("CurrentUser() = %q, want %q", user, "testuser")
	}
}

func TestGitHubNextItems(t *testing.T) {
	now := time.Now()

	// Build fake issue timeline
	issues := []ghIssue{
		{
			Number:    10,
			Title:     "Old issue I touched",
			HTMLURL:   "https://github.com/o/r/issues/10",
			UpdatedAt: now.Add(-1 * time.Hour),
		},
		{
			Number:    20,
			Title:     "Recent issue someone else updated",
			HTMLURL:   "https://github.com/o/r/issues/20",
			UpdatedAt: now.Add(-30 * time.Minute),
		},
	}

	// Timeline events for issue 10: I commented recently
	events10 := []ghTimelineEvent{
		{
			Event:     "commented",
			CreatedAt: now.Add(-20 * time.Minute),
			Actor:     ghActor{Login: "me"},
			Body:      "I'll fix this",
		},
	}

	// Timeline events for issue 20: someone else commented
	events20 := []ghTimelineEvent{
		{
			Event:     "commented",
			CreatedAt: now.Add(-30 * time.Minute),
			Actor:     ghActor{Login: "other"},
			Body:      "Please review this change",
		},
	}

	runner := func(name string, args ...string) ([]byte, error) {
		// Match the API call pattern
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
			if i > 0 && args[i-1] == "repos/o/r/issues/10/timeline" {
				return json.Marshal(events10)
			}
			if i > 0 && args[i-1] == "repos/o/r/issues/20/timeline" {
				return json.Marshal(events20)
			}
		}
		return nil, fmt.Errorf("unexpected call: %v", args)
	}

	gh := NewGitHub(runner)
	items := ghCollect(t, gh, "o", "r", "me", 30*time.Minute, nil, nil, 1)
	if len(items) != 1 {
		t.Fatalf("NextItems() returned %d items, want 1", len(items))
	}
	if items[0].Title != "Recent issue someone else updated" {
		t.Errorf("expected issue 20, got %q", items[0].Title)
	}
	if len(items[0].Events) == 0 {
		t.Error("expected at least one event")
	}
}

func TestGitHubNextItemsIgnoreEvents(t *testing.T) {
	now := time.Now()

	issues := []ghIssue{
		{
			Number:    1,
			Title:     "Issue with only mentioned events",
			HTMLURL:   "https://github.com/o/r/issues/1",
			UpdatedAt: now.Add(-10 * time.Minute),
			User:      ghActor{Login: "me"},
		},
		{
			Number:    2,
			Title:     "Issue with a real comment",
			HTMLURL:   "https://github.com/o/r/issues/2",
			UpdatedAt: now.Add(-20 * time.Minute),
			User:      ghActor{Login: "other"},
		},
	}

	events1 := []ghTimelineEvent{
		{
			Event:     "mentioned",
			CreatedAt: now.Add(-10 * time.Minute),
			Actor:     ghActor{Login: "other"},
		},
		{
			Event:     "subscribed",
			CreatedAt: now.Add(-10 * time.Minute),
			Actor:     ghActor{Login: "other"},
		},
	}

	events2 := []ghTimelineEvent{
		{
			Event:     "commented",
			CreatedAt: now.Add(-20 * time.Minute),
			Actor:     ghActor{Login: "other"},
			Body:      "a real comment",
		},
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

	ignore := MatchSet{"mentioned", "subscribed"}
	gh := NewGitHub(runner)
	items := ghCollect(t, gh, "o", "r", "me", 30*time.Minute, ignore, nil, 1)
	if len(items) != 1 {
		t.Fatalf("NextItems() returned %d items, want 1", len(items))
	}
	// Issue 1 should be skipped (only has ignored events), should get issue 2
	if items[0].Title != "Issue with a real comment" {
		t.Errorf("expected issue 2, got %q", items[0].Title)
	}
}

func TestGitHubNextItemsReviewCountsAsTouch(t *testing.T) {
	now := time.Now()
	prMarker := json.RawMessage(`{"url":"https://api.github.com/repos/o/r/pulls/5"}`)

	issues := []ghIssue{
		{
			Number:      5,
			Title:       "PR I reviewed recently",
			HTMLURL:     "https://github.com/o/r/pull/5",
			UpdatedAt:   now.Add(-10 * time.Minute),
			PullRequest: &prMarker,
		},
		{
			Number:    6,
			Title:     "Issue someone else updated",
			HTMLURL:   "https://github.com/o/r/issues/6",
			UpdatedAt: now.Add(-20 * time.Minute),
		},
	}

	// Timeline for PR 5: someone else's event (my review won't appear here)
	events5 := []ghTimelineEvent{
		{
			Event:     "review_requested",
			CreatedAt: now.Add(-1 * time.Hour),
			Actor:     ghActor{Login: "other"},
		},
	}

	// Reviews for PR 5: I reviewed it recently
	reviews5 := []ghReview{
		{
			User:        ghActor{Login: "me"},
			State:       "COMMENTED",
			SubmittedAt: now.Add(-10 * time.Minute),
			Body:        "needs changes here",
		},
	}

	// Timeline for issue 6
	events6 := []ghTimelineEvent{
		{
			Event:     "commented",
			CreatedAt: now.Add(-20 * time.Minute),
			Actor:     ghActor{Login: "other"},
			Body:      "please look at this",
		},
	}

	runner := func(name string, args ...string) ([]byte, error) {
		for i, a := range args {
			if a == "repos/o/r/issues" {
				return json.Marshal(issues)
			}
			if a == "graphql" {
				return json.Marshal(map[string]interface{}{
					"data": map[string]interface{}{
						"repository": map[string]interface{}{
							"pullRequest": map[string]interface{}{
								"reviews": map[string]interface{}{
									"nodes": []interface{}{},
								},
							},
						},
					},
				})
			}
			if strings.HasSuffix(a, "/reactions") {
				return json.Marshal([]ghReaction{})
			}
			if strings.HasSuffix(a, "/comments") {
				return json.Marshal([]ghComment{})
			}
			if i > 0 && args[i-1] == "repos/o/r/issues/5/timeline" {
				return json.Marshal(events5)
			}
			if i > 0 && args[i-1] == "repos/o/r/pulls/5/reviews" {
				return json.Marshal(reviews5)
			}
			if i > 0 && args[i-1] == "repos/o/r/issues/6/timeline" {
				return json.Marshal(events6)
			}
		}
		return nil, fmt.Errorf("unexpected call: %v", args)
	}

	gh := NewGitHub(runner)
	items := ghCollect(t, gh, "o", "r", "me", 30*time.Minute, nil, nil, 1)
	if len(items) != 1 {
		t.Fatalf("NextItems() returned %d items, want 1", len(items))
	}
	// PR 5 should be skipped because I reviewed it within 30m, should get issue 6
	if items[0].Title != "Issue someone else updated" {
		t.Errorf("expected issue 6, got %q", items[0].Title)
	}
}

func TestGitHubNextItemsAllTouchedByMe(t *testing.T) {
	now := time.Now()

	issues := []ghIssue{
		{
			Number:    1,
			Title:     "Only issue",
			HTMLURL:   "https://github.com/o/r/issues/1",
			UpdatedAt: now.Add(-10 * time.Minute),
		},
	}

	events := []ghTimelineEvent{
		{
			Event:     "commented",
			CreatedAt: now.Add(-10 * time.Minute),
			Actor:     ghActor{Login: "me"},
			Body:      "working on it",
		},
	}

	runner := func(name string, args ...string) ([]byte, error) {
		for _, a := range args {
			if a == "repos/o/r/issues" {
				return json.Marshal(issues)
			}
		}
		return json.Marshal(events)
	}

	gh := NewGitHub(runner)
	items := ghCollect(t, gh, "o", "r", "me", 30*time.Minute, nil, nil, 1)
	if len(items) != 0 {
		t.Errorf("expected empty slice (nothing to do), got %+v", items)
	}
}

func TestGitHubNextItemsIgnoreUsers(t *testing.T) {
	now := time.Now()

	issues := []ghIssue{
		{
			Number:    36,
			Title:     "PR with bot activity",
			HTMLURL:   "https://github.com/o/r/pull/36",
			UpdatedAt: now.Add(-5 * time.Minute),
		},
	}

	// Bot commented after user, making it look like new activity
	events36 := []ghTimelineEvent{
		{
			Event:     "commented",
			CreatedAt: now.Add(-1 * time.Hour),
			Actor:     ghActor{Login: "other"},
			Body:      "please review",
		},
		{
			Event:     "commented",
			CreatedAt: now.Add(-50 * time.Minute),
			Actor:     ghActor{Login: "me"},
			Body:      "on it",
		},
		{
			Event:     "commented",
			CreatedAt: now.Add(-5 * time.Minute),
			Actor:     ghActor{Login: "qodo-code-review[bot]"},
			Body:      "automated review comment",
		},
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
			if i > 0 && args[i-1] == "repos/o/r/issues/36/timeline" {
				return json.Marshal(events36)
			}
		}
		return nil, fmt.Errorf("unexpected call: %v", args)
	}

	gh := NewGitHub(runner)

	// Without ignoring the bot, the bot's comment is the only event after "me",
	// but since we ignore the bot user, there are no new events → empty result
	ignoreUsers := MatchSet{"*[bot]"}
	items := ghCollect(t, gh, "o", "r", "me", 30*time.Minute, nil, ignoreUsers, 1)
	if len(items) != 0 {
		t.Errorf("expected empty slice (bot activity should be ignored), got %+v", items)
	}

	// Without ignoring the bot, we should get the item since the bot's comment
	// appears as new activity after the user's last touch (which is outside the cooldown)
	items = ghCollect(t, gh, "o", "r", "me", 30*time.Minute, nil, nil, 1)
	if len(items) != 1 {
		t.Fatalf("expected 1 item when bot is not ignored, got %d", len(items))
	}
	if len(items[0].Events) != 1 || items[0].Events[0].Author != "qodo-code-review[bot]" {
		t.Errorf("expected bot event, got %+v", items[0].Events)
	}
}

func TestGitHubNextItemsLimit(t *testing.T) {
	now := time.Now()

	issues := []ghIssue{
		{
			Number:    1,
			Title:     "First untouched issue",
			HTMLURL:   "https://github.com/o/r/issues/1",
			UpdatedAt: now.Add(-10 * time.Minute),
		},
		{
			Number:    2,
			Title:     "Second untouched issue",
			HTMLURL:   "https://github.com/o/r/issues/2",
			UpdatedAt: now.Add(-20 * time.Minute),
		},
		{
			Number:    3,
			Title:     "Third untouched issue",
			HTMLURL:   "https://github.com/o/r/issues/3",
			UpdatedAt: now.Add(-30 * time.Minute),
		},
	}

	events1 := []ghTimelineEvent{
		{Event: "commented", CreatedAt: now.Add(-10 * time.Minute), Actor: ghActor{Login: "other"}, Body: "first"},
	}
	events2 := []ghTimelineEvent{
		{Event: "commented", CreatedAt: now.Add(-20 * time.Minute), Actor: ghActor{Login: "other"}, Body: "second"},
	}
	events3 := []ghTimelineEvent{
		{Event: "commented", CreatedAt: now.Add(-30 * time.Minute), Actor: ghActor{Login: "other"}, Body: "third"},
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

	// limit=2 should return the first 2 matching items
	items := ghCollect(t, gh, "o", "r", "me", 30*time.Minute, nil, nil, 2)
	if len(items) != 2 {
		t.Fatalf("NextItems(limit=2) returned %d items, want 2", len(items))
	}
	if items[0].Title != "First untouched issue" {
		t.Errorf("first item: expected 'First untouched issue', got %q", items[0].Title)
	}
	if items[1].Title != "Second untouched issue" {
		t.Errorf("second item: expected 'Second untouched issue', got %q", items[1].Title)
	}

	// limit=5 with only 3 available should return all 3
	items = ghCollect(t, gh, "o", "r", "me", 30*time.Minute, nil, nil, 5)
	if len(items) != 3 {
		t.Fatalf("NextItems(limit=5) returned %d items, want 3", len(items))
	}
}

func TestGitHubNextItemsUntouchedByAnyone(t *testing.T) {
	now := time.Now()

	issues := []ghIssue{
		{
			Number:    42,
			Title:     "Brand new issue from someone else",
			HTMLURL:   "https://github.com/o/r/issues/42",
			CreatedAt: now.Add(-2 * time.Hour),
			UpdatedAt: now.Add(-2 * time.Hour),
			User:      ghActor{Login: "other"},
		},
		{
			Number:    43,
			Title:     "My own issue with no activity",
			HTMLURL:   "https://github.com/o/r/issues/43",
			CreatedAt: now.Add(-3 * time.Hour),
			UpdatedAt: now.Add(-3 * time.Hour),
			User:      ghActor{Login: "me"},
		},
	}

	// Both issues have empty timelines — no one has interacted
	emptyEvents := []ghTimelineEvent{}

	runner := func(name string, args ...string) ([]byte, error) {
		for _, a := range args {
			if a == "repos/o/r/issues" {
				return json.Marshal(issues)
			}
		}
		return json.Marshal(emptyEvents)
	}

	gh := NewGitHub(runner)
	items := ghCollect(t, gh, "o", "r", "me", 30*time.Minute, nil, nil, 5)
	// Should include the issue filed by "other" but not the one filed by "me"
	if len(items) != 1 {
		t.Fatalf("NextItems() returned %d items, want 1", len(items))
	}
	if items[0].Title != "Brand new issue from someone else" {
		t.Errorf("expected issue 42, got %q", items[0].Title)
	}
	if len(items[0].Events) != 1 {
		t.Fatalf("expected 1 synthetic event, got %d", len(items[0].Events))
	}
	if items[0].Events[0].Summary != "opened" {
		t.Errorf("expected 'opened' event, got %q", items[0].Events[0].Summary)
	}
	if items[0].Events[0].Author != "other" {
		t.Errorf("expected author 'other', got %q", items[0].Events[0].Author)
	}
}

func TestGitHubNextItemsApprovalSummary(t *testing.T) {
	now := time.Now()
	prMarker := json.RawMessage(`{"url":"https://api.github.com/repos/o/r/pulls/7"}`)

	issues := []ghIssue{
		{
			Number:      7,
			Title:       "PR that was approved",
			HTMLURL:     "https://github.com/o/r/pull/7",
			UpdatedAt:   now.Add(-10 * time.Minute),
			User:        ghActor{Login: "me"},
			PullRequest: &prMarker,
		},
	}

	events7 := []ghTimelineEvent{}
	reviews7 := []ghReview{
		{
			User:        ghActor{Login: "reviewer"},
			State:       "APPROVED",
			SubmittedAt: now.Add(-10 * time.Minute),
		},
	}

	emptyGraphQL := map[string]interface{}{
		"data": map[string]interface{}{
			"repository": map[string]interface{}{
				"pullRequest": map[string]interface{}{
					"reviews": map[string]interface{}{
						"nodes": []interface{}{},
					},
				},
			},
		},
	}
	runner := func(name string, args ...string) ([]byte, error) {
		for i, a := range args {
			if a == "repos/o/r/issues" {
				return json.Marshal(issues)
			}
			if a == "graphql" {
				return json.Marshal(emptyGraphQL)
			}
			if strings.HasSuffix(a, "/reactions") {
				return json.Marshal([]ghReaction{})
			}
			if strings.HasSuffix(a, "/comments") {
				return json.Marshal([]ghComment{})
			}
			if i > 0 && args[i-1] == "repos/o/r/issues/7/timeline" {
				return json.Marshal(events7)
			}
			if i > 0 && args[i-1] == "repos/o/r/pulls/7/reviews" {
				return json.Marshal(reviews7)
			}
		}
		return nil, fmt.Errorf("unexpected call: %v", args)
	}

	gh := NewGitHub(runner)
	items := ghCollect(t, gh, "o", "r", "me", 30*time.Minute, nil, nil, 5)
	if len(items) != 1 {
		t.Fatalf("NextItems() returned %d items, want 1", len(items))
	}
	if len(items[0].Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(items[0].Events))
	}
	if items[0].Events[0].Summary != "approved" {
		t.Errorf("expected 'approved' summary, got %q", items[0].Events[0].Summary)
	}
}

func TestGitHubNextItemsReactionCountsAsTouch(t *testing.T) {
	now := time.Now()

	issues := []ghIssue{
		{
			Number:    50,
			Title:     "Issue I reacted to recently",
			HTMLURL:   "https://github.com/o/r/issues/50",
			UpdatedAt: now.Add(-10 * time.Minute),
		},
		{
			Number:    51,
			Title:     "Issue I have not touched",
			HTMLURL:   "https://github.com/o/r/issues/51",
			UpdatedAt: now.Add(-20 * time.Minute),
		},
	}

	events50 := []ghTimelineEvent{
		{
			Event:     "commented",
			CreatedAt: now.Add(-1 * time.Hour),
			Actor:     ghActor{Login: "other"},
			Body:      "needs attention",
		},
	}
	reactions50 := []ghReaction{
		{
			User:      ghActor{Login: "me"},
			Content:   "+1",
			CreatedAt: now.Add(-10 * time.Minute),
		},
	}

	events51 := []ghTimelineEvent{
		{
			Event:     "commented",
			CreatedAt: now.Add(-20 * time.Minute),
			Actor:     ghActor{Login: "other"},
			Body:      "also needs attention",
		},
	}

	runner := func(name string, args ...string) ([]byte, error) {
		for i, a := range args {
			if a == "repos/o/r/issues" {
				return json.Marshal(issues)
			}
			if i > 0 && args[i-1] == "repos/o/r/issues/50/timeline" {
				return json.Marshal(events50)
			}
			if i > 0 && args[i-1] == "repos/o/r/issues/50/reactions" {
				return json.Marshal(reactions50)
			}
			if strings.HasSuffix(a, "/comments") {
				return json.Marshal([]ghComment{})
			}
			if i > 0 && args[i-1] == "repos/o/r/issues/51/timeline" {
				return json.Marshal(events51)
			}
			if i > 0 && args[i-1] == "repos/o/r/issues/51/reactions" {
				return json.Marshal([]ghReaction{})
			}
		}
		return nil, fmt.Errorf("unexpected call: %v", args)
	}

	gh := NewGitHub(runner)
	items := ghCollect(t, gh, "o", "r", "me", 30*time.Minute, nil, nil, 5)
	// Issue 50 should be skipped (I reacted within 30m), should get issue 51
	if len(items) != 1 {
		t.Fatalf("NextItems() returned %d items, want 1", len(items))
	}
	if items[0].Title != "Issue I have not touched" {
		t.Errorf("expected issue 51, got %q", items[0].Title)
	}
}

func TestGitHubNextItemsCommentReactionCountsAsTouch(t *testing.T) {
	now := time.Now()

	issues := []ghIssue{
		{
			Number:    60,
			Title:     "Issue where I reacted to a comment",
			HTMLURL:   "https://github.com/o/r/issues/60",
			UpdatedAt: now.Add(-10 * time.Minute),
		},
		{
			Number:    61,
			Title:     "Issue I have not touched",
			HTMLURL:   "https://github.com/o/r/issues/61",
			UpdatedAt: now.Add(-20 * time.Minute),
		},
	}

	events60 := []ghTimelineEvent{
		{
			Event:     "commented",
			CreatedAt: now.Add(-1 * time.Hour),
			Actor:     ghActor{Login: "other"},
			Body:      "some comment",
		},
	}
	events61 := []ghTimelineEvent{
		{
			Event:     "commented",
			CreatedAt: now.Add(-20 * time.Minute),
			Actor:     ghActor{Login: "other"},
			Body:      "needs attention",
		},
	}

	// Issue 60 has a comment with a reaction from "me"
	comments60 := []ghComment{
		{
			ID:        100,
			User:      ghActor{Login: "other"},
			Body:      "some comment",
			CreatedAt: now.Add(-1 * time.Hour),
			Reactions: struct {
				TotalCount int `json:"total_count"`
			}{TotalCount: 1},
		},
	}
	commentReactions100 := []ghReaction{
		{
			User:      ghActor{Login: "me"},
			Content:   "+1",
			CreatedAt: now.Add(-10 * time.Minute),
		},
	}

	runner := func(name string, args ...string) ([]byte, error) {
		for i, a := range args {
			if a == "repos/o/r/issues" {
				return json.Marshal(issues)
			}
			if i > 0 && args[i-1] == "repos/o/r/issues/60/timeline" {
				return json.Marshal(events60)
			}
			if i > 0 && args[i-1] == "repos/o/r/issues/61/timeline" {
				return json.Marshal(events61)
			}
			if strings.HasSuffix(a, "/issues/60/reactions") {
				return json.Marshal([]ghReaction{})
			}
			if strings.HasSuffix(a, "/issues/61/reactions") {
				return json.Marshal([]ghReaction{})
			}
			if i > 0 && args[i-1] == "repos/o/r/issues/60/comments" {
				return json.Marshal(comments60)
			}
			if i > 0 && args[i-1] == "repos/o/r/issues/61/comments" {
				return json.Marshal([]ghComment{})
			}
			if i > 0 && args[i-1] == "repos/o/r/issues/comments/100/reactions" {
				return json.Marshal(commentReactions100)
			}
		}
		return nil, fmt.Errorf("unexpected call: %v", args)
	}

	gh := NewGitHub(runner)
	items := ghCollect(t, gh, "o", "r", "me", 30*time.Minute, nil, nil, 5)
	// Issue 60 should be skipped (I reacted to a comment within 30m), should get issue 61
	if len(items) != 1 {
		t.Fatalf("NextItems() returned %d items, want 1", len(items))
	}
	if items[0].Title != "Issue I have not touched" {
		t.Errorf("expected issue 61, got %q", items[0].Title)
	}
}

func TestGitHubReviewCommentReactionMarksTouched(t *testing.T) {
	now := time.Now()
	pr := json.RawMessage(`{}`)

	issues := []ghIssue{
		{
			Number:      70,
			Title:       "PR with review comment reaction",
			HTMLURL:     "https://github.com/o/r/pull/70",
			CreatedAt:   now.Add(-2 * time.Hour),
			UpdatedAt:   now.Add(-5 * time.Minute),
			User:        ghActor{Login: "other"},
			PullRequest: &pr,
		},
		{
			Number:    71,
			Title:     "Issue I have not touched",
			HTMLURL:   "https://github.com/o/r/issues/71",
			CreatedAt: now.Add(-3 * time.Hour),
			UpdatedAt: now.Add(-10 * time.Minute),
			User:      ghActor{Login: "other"},
		},
	}

	events70 := []ghTimelineEvent{
		{
			Event:     "commented",
			CreatedAt: now.Add(-1 * time.Hour),
			Actor:     ghActor{Login: "other"},
			Body:      "review comment",
		},
	}
	events71 := []ghTimelineEvent{
		{
			Event:     "commented",
			CreatedAt: now.Add(-20 * time.Minute),
			Actor:     ghActor{Login: "other"},
			Body:      "needs attention",
		},
	}

	// PR 70 has a review comment with a reaction from "me"
	reviewComments70 := []ghComment{
		{
			ID:        200,
			User:      ghActor{Login: "other"},
			Body:      "inline code comment",
			CreatedAt: now.Add(-1 * time.Hour),
			Reactions: struct {
				TotalCount int `json:"total_count"`
			}{TotalCount: 1},
		},
	}
	reviewCommentReactions200 := []ghReaction{
		{
			User:      ghActor{Login: "me"},
			Content:   "+1",
			CreatedAt: now.Add(-10 * time.Minute),
		},
	}

	runner := func(name string, args ...string) ([]byte, error) {
		for i, a := range args {
			if a == "repos/o/r/issues" {
				return json.Marshal(issues)
			}
			if a == "graphql" {
				return json.Marshal(map[string]interface{}{
					"data": map[string]interface{}{
						"repository": map[string]interface{}{
							"pullRequest": map[string]interface{}{
								"reviews": map[string]interface{}{
									"nodes": []interface{}{},
								},
							},
						},
					},
				})
			}
			if i > 0 && args[i-1] == "repos/o/r/issues/70/timeline" {
				return json.Marshal(events70)
			}
			if i > 0 && args[i-1] == "repos/o/r/issues/71/timeline" {
				return json.Marshal(events71)
			}
			if strings.HasSuffix(a, "/issues/70/reactions") {
				return json.Marshal([]ghReaction{})
			}
			if strings.HasSuffix(a, "/issues/71/reactions") {
				return json.Marshal([]ghReaction{})
			}
			if i > 0 && args[i-1] == "repos/o/r/issues/70/comments" {
				return json.Marshal([]ghComment{})
			}
			if i > 0 && args[i-1] == "repos/o/r/issues/71/comments" {
				return json.Marshal([]ghComment{})
			}
			if i > 0 && args[i-1] == "repos/o/r/pulls/70/comments" {
				return json.Marshal(reviewComments70)
			}
			if i > 0 && args[i-1] == "repos/o/r/pulls/comments/200/reactions" {
				return json.Marshal(reviewCommentReactions200)
			}
			if i > 0 && args[i-1] == "repos/o/r/pulls/70/reviews" {
				return json.Marshal([]ghReview{})
			}
		}
		return nil, fmt.Errorf("unexpected call: %v", args)
	}

	gh := NewGitHub(runner)
	items := ghCollect(t, gh, "o", "r", "me", 30*time.Minute, nil, nil, 5)
	// PR 70 should be skipped (I reacted to a review comment within 30m), should get issue 71
	if len(items) != 1 {
		t.Fatalf("NextItems() returned %d items, want 1", len(items))
	}
	if items[0].Title != "Issue I have not touched" {
		t.Errorf("expected issue 71, got %q", items[0].Title)
	}
}

func TestGitHubRetryOnRateLimit(t *testing.T) {
	now := time.Now()

	issues := []ghIssue{
		{
			Number:    80,
			Title:     "Some issue",
			HTMLURL:   "https://github.com/o/r/issues/80",
			CreatedAt: now.Add(-2 * time.Hour),
			UpdatedAt: now.Add(-5 * time.Minute),
			User:      ghActor{Login: "other"},
		},
	}
	events80 := []ghTimelineEvent{
		{
			Event:     "commented",
			CreatedAt: now.Add(-10 * time.Minute),
			Actor:     ghActor{Login: "other"},
			Body:      "hello",
		},
	}

	// Simulate rate limit on first timeline call, then succeed on retry
	var timelineCalls atomic.Int32
	resetTime := time.Now().Add(1 * time.Second)

	runner := func(name string, args ...string) ([]byte, error) {
		for i, a := range args {
			if a == "repos/o/r/issues" {
				return json.Marshal(issues)
			}
			if a == "rate_limit" {
				rl := struct {
					Rate struct {
						Remaining int   `json:"remaining"`
						Reset     int64 `json:"reset"`
					} `json:"rate"`
				}{}
				rl.Rate.Reset = resetTime.Unix()
				return json.Marshal(rl)
			}
			if i > 0 && args[i-1] == "repos/o/r/issues/80/timeline" {
				if timelineCalls.Add(1) == 1 {
					return nil, fmt.Errorf("API rate limit exceeded (HTTP 403)")
				}
				return json.Marshal(events80)
			}
			if strings.HasSuffix(a, "/issues/80/reactions") {
				return json.Marshal([]ghReaction{})
			}
			if i > 0 && args[i-1] == "repos/o/r/issues/80/comments" {
				return json.Marshal([]ghComment{})
			}
		}
		return nil, fmt.Errorf("unexpected call: %v", args)
	}

	gh := NewGitHub(runner)
	items := ghCollect(t, gh, "o", "r", "me", 30*time.Minute, nil, nil, 5)
	if len(items) != 1 {
		t.Fatalf("NextItems() returned %d items, want 1", len(items))
	}
	if items[0].Title != "Some issue" {
		t.Errorf("expected 'Some issue', got %q", items[0].Title)
	}
	if got := timelineCalls.Load(); got != 2 {
		t.Errorf("expected 2 timeline calls (1 failed + 1 retry), got %d", got)
	}
}

func TestGitHubReviewReactionCountsAsTouch(t *testing.T) {
	now := time.Now()
	pr := json.RawMessage(`{}`)

	issues := []ghIssue{
		{
			Number:      90,
			Title:       "PR where I reacted to a review",
			HTMLURL:     "https://github.com/o/r/pull/90",
			CreatedAt:   now.Add(-2 * time.Hour),
			UpdatedAt:   now.Add(-5 * time.Minute),
			User:        ghActor{Login: "other"},
			PullRequest: &pr,
		},
		{
			Number:    91,
			Title:     "Issue I have not touched",
			HTMLURL:   "https://github.com/o/r/issues/91",
			CreatedAt: now.Add(-3 * time.Hour),
			UpdatedAt: now.Add(-10 * time.Minute),
			User:      ghActor{Login: "other"},
		},
	}

	events90 := []ghTimelineEvent{
		{
			Event:     "review_requested",
			CreatedAt: now.Add(-1 * time.Hour),
			Actor:     ghActor{Login: "other"},
		},
	}
	reviews90 := []ghReview{
		{
			User:        ghActor{Login: "reviewer"},
			State:       "COMMENTED",
			SubmittedAt: now.Add(-30 * time.Minute),
			Body:        "looks good but needs a tweak",
		},
	}
	events91 := []ghTimelineEvent{
		{
			Event:     "commented",
			CreatedAt: now.Add(-20 * time.Minute),
			Actor:     ghActor{Login: "other"},
			Body:      "needs attention",
		},
	}

	// GraphQL response: I reacted to the review with thumbs up 10 min ago
	graphQLResp := map[string]interface{}{
		"data": map[string]interface{}{
			"repository": map[string]interface{}{
				"pullRequest": map[string]interface{}{
					"reviews": map[string]interface{}{
						"nodes": []interface{}{
							map[string]interface{}{
								"reactions": map[string]interface{}{
									"nodes": []interface{}{
										map[string]interface{}{
											"user":      map[string]interface{}{"login": "me"},
											"content":   "THUMBS_UP",
											"createdAt": now.Add(-10 * time.Minute).Format(time.RFC3339),
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	runner := func(name string, args ...string) ([]byte, error) {
		for i, a := range args {
			if a == "repos/o/r/issues" {
				return json.Marshal(issues)
			}
			if a == "graphql" {
				return json.Marshal(graphQLResp)
			}
			if strings.HasSuffix(a, "/reactions") {
				return json.Marshal([]ghReaction{})
			}
			if strings.HasSuffix(a, "/comments") {
				return json.Marshal([]ghComment{})
			}
			if i > 0 && args[i-1] == "repos/o/r/issues/90/timeline" {
				return json.Marshal(events90)
			}
			if i > 0 && args[i-1] == "repos/o/r/pulls/90/reviews" {
				return json.Marshal(reviews90)
			}
			if i > 0 && args[i-1] == "repos/o/r/issues/91/timeline" {
				return json.Marshal(events91)
			}
		}
		return nil, fmt.Errorf("unexpected call: %v", args)
	}

	gh := NewGitHub(runner)
	items := ghCollect(t, gh, "o", "r", "me", 30*time.Minute, nil, nil, 5)
	// PR 90 should be skipped (I reacted to a review within 30m), should get issue 91
	if len(items) != 1 {
		t.Fatalf("NextItems() returned %d items, want 1", len(items))
	}
	if items[0].Title != "Issue I have not touched" {
		t.Errorf("expected issue 91, got %q", items[0].Title)
	}
}

func TestGitHubNextItemsOrgScope(t *testing.T) {
	now := time.Now()

	// Search API returns issues from different repos in the org
	searchResult := map[string]interface{}{
		"total_count": 2,
		"items": []ghIssue{
			{
				Number:    10,
				Title:     "Issue in repo-a",
				HTMLURL:   "https://github.com/myorg/repo-a/issues/10",
				UpdatedAt: now.Add(-10 * time.Minute),
				User:      ghActor{Login: "other"},
			},
			{
				Number:    5,
				Title:     "Issue in repo-b",
				HTMLURL:   "https://github.com/myorg/repo-b/issues/5",
				UpdatedAt: now.Add(-20 * time.Minute),
				User:      ghActor{Login: "other"},
			},
		},
	}

	events10 := []ghTimelineEvent{
		{Event: "commented", CreatedAt: now.Add(-10 * time.Minute), Actor: ghActor{Login: "other"}, Body: "please look"},
	}
	events5 := []ghTimelineEvent{
		{Event: "commented", CreatedAt: now.Add(-20 * time.Minute), Actor: ghActor{Login: "other"}, Body: "needs review"},
	}

	runner := func(name string, args ...string) ([]byte, error) {
		for i, a := range args {
			if a == "search/issues" {
				return json.Marshal(searchResult)
			}
			if strings.HasSuffix(a, "/reactions") {
				return json.Marshal([]ghReaction{})
			}
			if strings.HasSuffix(a, "/comments") {
				return json.Marshal([]ghComment{})
			}
			if i > 0 && args[i-1] == "repos/myorg/repo-a/issues/10/timeline" {
				return json.Marshal(events10)
			}
			if i > 0 && args[i-1] == "repos/myorg/repo-b/issues/5/timeline" {
				return json.Marshal(events5)
			}
		}
		return nil, fmt.Errorf("unexpected call: %v", args)
	}

	gh := NewGitHub(runner)
	var items []format.Item
	err := gh.NextItems("myorg", "", "me", 30*time.Minute, time.Time{}, nil, nil, 2, 0, ScopeOrg, func(item format.Item) {
		items = append(items, item)
	})
	if err != nil {
		t.Fatalf("NextItems() error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("NextItems() returned %d items, want 2", len(items))
	}
	if items[0].Title != "Issue in repo-a" {
		t.Errorf("first item: expected 'Issue in repo-a', got %q", items[0].Title)
	}
	if items[1].Title != "Issue in repo-b" {
		t.Errorf("second item: expected 'Issue in repo-b', got %q", items[1].Title)
	}
}

// TestGitHubCommentUserField verifies that "commented" timeline events
// are correctly detected even when the actor is in the "user" JSON field
// (which is how GitHub's Timeline API actually returns them) rather than
// the "actor" field.
func TestGitHubCommentUserField(t *testing.T) {
	now := time.Now()

	issues := []ghIssue{
		{
			Number:    324,
			Title:     "Issue I commented on recently",
			HTMLURL:   "https://github.com/o/r/issues/324",
			UpdatedAt: now.Add(-10 * time.Minute),
			User:      ghActor{Login: "someone"},
		},
	}

	// "commented" events from the timeline API use "user", not "actor"
	events324 := []ghTimelineEvent{
		{
			Event:     "commented",
			CreatedAt: now.Add(-10 * time.Minute),
			User:      ghActor{Login: "me"},
			Body:      "I just commented on this",
		},
	}

	runner := func(name string, args ...string) ([]byte, error) {
		for _, a := range args {
			if a == "repos/o/r/issues" {
				return json.Marshal(issues)
			}
			if a == "repos/o/r/issues/324/timeline" {
				return json.Marshal(events324)
			}
			if strings.HasSuffix(a, "/reactions") || strings.HasSuffix(a, "/comments") {
				return []byte("[]"), nil
			}
		}
		return nil, fmt.Errorf("unexpected call: %v", args)
	}

	gh := NewGitHub(runner)
	items := ghCollect(t, gh, "o", "r", "me", 1*time.Hour, nil, nil, 5)
	if len(items) != 0 {
		t.Fatalf("NextItems() returned %d items, want 0 (issue should be filtered because user commented recently)", len(items))
	}
}

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

func TestGitHubTierRecencyWithinOthers(t *testing.T) {
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
	// With the performance optimization, non-authored items are processed in
	// recency order (most recently updated first) rather than sorting tier-2
	// before tier-3. The more recently updated general issue wins.
	if items[0].Title != "General issue (more recent)" {
		t.Errorf("expected most recent non-authored issue to win, got %q", items[0].Title)
	}
	if items[0].Tier != 3 {
		t.Errorf("expected Tier 3, got %d", items[0].Tier)
	}
}

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
	if items[0].Title != "Authored issue (oldest)" {
		t.Errorf("first item: expected authored, got %q", items[0].Title)
	}
	if items[0].Tier != 1 {
		t.Errorf("first item: expected Tier 1, got %d", items[0].Tier)
	}
	// After authored, remaining items appear in recency order (not tier order)
	if items[1].Title != "General issue (most recent)" {
		t.Errorf("second item: expected most recent non-authored, got %q", items[1].Title)
	}
	if items[1].Tier != 3 {
		t.Errorf("second item: expected Tier 3, got %d", items[1].Tier)
	}
	if items[2].Title != "Participated issue" {
		t.Errorf("third item: expected participated, got %q", items[2].Title)
	}
	if items[2].Tier != 2 {
		t.Errorf("third item: expected Tier 2, got %d", items[2].Tier)
	}
}

func TestGitHubSincePassedToAPI(t *testing.T) {
	now := time.Now()
	sinceTime := now.Add(-7 * 24 * time.Hour) // 7 days ago

	issues := []ghIssue{
		{
			Number:    1,
			Title:     "Some issue",
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
		for _, a := range args {
			if a == "repos/o/r/issues" {
				// Capture the full args for the issues listing call
				capturedArgs = append([]string{name}, args...)
				return json.Marshal(issues)
			}
			if strings.HasSuffix(a, "/reactions") {
				return json.Marshal([]ghReaction{})
			}
			if strings.HasSuffix(a, "/comments") {
				return json.Marshal([]ghComment{})
			}
			if strings.HasSuffix(a, "/timeline") {
				return json.Marshal(events1)
			}
		}
		return nil, fmt.Errorf("unexpected call: %v", args)
	}

	gh := NewGitHub(runner)
	var items []format.Item
	err := gh.NextItems("o", "r", "me", 30*time.Minute, sinceTime, nil, nil, 5, 0, ScopeRepo, func(item format.Item) {
		items = append(items, item)
	})
	if err != nil {
		t.Fatalf("NextItems() error: %v", err)
	}

	// Verify that the captured args contain the since parameter
	expectedSince := "since=" + sinceTime.Format(time.RFC3339)
	found := false
	for _, a := range capturedArgs {
		if a == expectedSince {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected args to contain %q, got %v", expectedSince, capturedArgs)
	}
}

func TestGitHubSincePassedToOrgSearch(t *testing.T) {
	now := time.Now()
	sinceTime := now.Add(-14 * 24 * time.Hour) // 14 days ago

	searchResult := map[string]interface{}{
		"total_count": 1,
		"items": []ghIssue{
			{
				Number:    10,
				Title:     "Org issue",
				HTMLURL:   "https://github.com/myorg/repo-a/issues/10",
				UpdatedAt: now.Add(-10 * time.Minute),
				User:      ghActor{Login: "other"},
			},
		},
	}

	events10 := []ghTimelineEvent{
		{Event: "commented", CreatedAt: now.Add(-10 * time.Minute), Actor: ghActor{Login: "other"}, Body: "please look"},
	}

	var capturedQuery string
	runner := func(name string, args ...string) ([]byte, error) {
		for i, a := range args {
			if a == "search/issues" {
				// Find the query parameter
				for j, arg := range args {
					if arg == "-f" && j+1 < len(args) && strings.HasPrefix(args[j+1], "q=") {
						capturedQuery = args[j+1]
					}
				}
				_ = i
				return json.Marshal(searchResult)
			}
			if strings.HasSuffix(a, "/reactions") {
				return json.Marshal([]ghReaction{})
			}
			if strings.HasSuffix(a, "/comments") {
				return json.Marshal([]ghComment{})
			}
			if strings.HasSuffix(a, "/timeline") {
				return json.Marshal(events10)
			}
		}
		return nil, fmt.Errorf("unexpected call: %v", args)
	}

	gh := NewGitHub(runner)
	var items []format.Item
	err := gh.NextItems("myorg", "", "me", 30*time.Minute, sinceTime, nil, nil, 5, 0, ScopeOrg, func(item format.Item) {
		items = append(items, item)
	})
	if err != nil {
		t.Fatalf("NextItems() error: %v", err)
	}

	// Verify that the search query includes the updated:>= filter
	expectedDateStr := sinceTime.Format("2006-01-02")
	expectedFragment := "updated:>=" + expectedDateStr
	if !strings.Contains(capturedQuery, expectedFragment) {
		t.Errorf("expected query to contain %q, got %q", expectedFragment, capturedQuery)
	}
}

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

	var perItemPaginate atomic.Bool
	runner := func(name string, args ...string) ([]byte, error) {
		isPerItem := false
		for _, a := range args {
			if strings.Contains(a, "/timeline") || strings.Contains(a, "/reactions") ||
				strings.Contains(a, "/comments") || strings.Contains(a, "/reviews") {
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
			if strings.Contains(a, "/timeline") {
				return json.Marshal(events)
			}
			if strings.Contains(a, "/reactions") {
				return json.Marshal([]ghReaction{})
			}
			if strings.Contains(a, "/comments") {
				return json.Marshal([]ghComment{})
			}
		}
		return nil, fmt.Errorf("unexpected call: %v", args)
	}

	gh := NewGitHub(runner)
	err := gh.NextItems("o", "r", "me", 30*time.Minute, time.Time{}, nil, nil, 5, 50, ScopeRepo, func(item format.Item) {})
	if err != nil {
		t.Fatalf("NextItems() error: %v", err)
	}
	if perItemPaginate.Load() {
		t.Error("per-item API calls should not use --paginate when maxEvents > 0")
	}
}
