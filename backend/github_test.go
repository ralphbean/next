package backend

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

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
	items, err := gh.NextItems("o", "r", "me", 30*time.Minute, nil, nil, 1)
	if err != nil {
		t.Fatalf("NextItems() error: %v", err)
	}
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
		},
		{
			Number:    2,
			Title:     "Issue with a real comment",
			HTMLURL:   "https://github.com/o/r/issues/2",
			UpdatedAt: now.Add(-20 * time.Minute),
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
			if i > 0 && args[i-1] == "repos/o/r/issues/1/timeline" {
				return json.Marshal(events1)
			}
			if i > 0 && args[i-1] == "repos/o/r/issues/2/timeline" {
				return json.Marshal(events2)
			}
		}
		return nil, fmt.Errorf("unexpected call: %v", args)
	}

	ignore := map[string]bool{"mentioned": true, "subscribed": true}
	gh := NewGitHub(runner)
	items, err := gh.NextItems("o", "r", "me", 30*time.Minute, ignore, nil, 1)
	if err != nil {
		t.Fatalf("NextItems() error: %v", err)
	}
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
	items, err := gh.NextItems("o", "r", "me", 30*time.Minute, nil, nil, 1)
	if err != nil {
		t.Fatalf("NextItems() error: %v", err)
	}
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
	items, err := gh.NextItems("o", "r", "me", 30*time.Minute, nil, nil, 1)
	if err != nil {
		t.Fatalf("NextItems() error: %v", err)
	}
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
			if i > 0 && args[i-1] == "repos/o/r/issues/36/timeline" {
				return json.Marshal(events36)
			}
		}
		return nil, fmt.Errorf("unexpected call: %v", args)
	}

	gh := NewGitHub(runner)

	// Without ignoring the bot, the bot's comment is the only event after "me",
	// but since we ignore the bot user, there are no new events → empty result
	ignoreUsers := map[string]bool{"qodo-code-review[bot]": true}
	items, err := gh.NextItems("o", "r", "me", 30*time.Minute, nil, ignoreUsers, 1)
	if err != nil {
		t.Fatalf("NextItems() error: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected empty slice (bot activity should be ignored), got %+v", items)
	}

	// Without ignoring the bot, we should get the item since the bot's comment
	// appears as new activity after the user's last touch (which is outside the cooldown)
	items, err = gh.NextItems("o", "r", "me", 30*time.Minute, nil, nil, 1)
	if err != nil {
		t.Fatalf("NextItems() error: %v", err)
	}
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
	items, err := gh.NextItems("o", "r", "me", 30*time.Minute, nil, nil, 2)
	if err != nil {
		t.Fatalf("NextItems() error: %v", err)
	}
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
	items, err = gh.NextItems("o", "r", "me", 30*time.Minute, nil, nil, 5)
	if err != nil {
		t.Fatalf("NextItems() error: %v", err)
	}
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
	items, err := gh.NextItems("o", "r", "me", 30*time.Minute, nil, nil, 5)
	if err != nil {
		t.Fatalf("NextItems() error: %v", err)
	}
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
