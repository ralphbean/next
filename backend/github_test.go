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

func TestGitHubNextItem(t *testing.T) {
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
	item, err := gh.NextItem("o", "r", "me", 30*time.Minute, nil)
	if err != nil {
		t.Fatalf("NextItem() error: %v", err)
	}
	if item == nil {
		t.Fatal("NextItem() returned nil")
	}
	if item.Title != "Recent issue someone else updated" {
		t.Errorf("expected issue 20, got %q", item.Title)
	}
	if len(item.Events) == 0 {
		t.Error("expected at least one event")
	}
}

func TestGitHubNextItemIgnoreEvents(t *testing.T) {
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
	item, err := gh.NextItem("o", "r", "me", 30*time.Minute, ignore)
	if err != nil {
		t.Fatalf("NextItem() error: %v", err)
	}
	if item == nil {
		t.Fatal("NextItem() returned nil")
	}
	// Issue 1 should be skipped (only has ignored events), should get issue 2
	if item.Title != "Issue with a real comment" {
		t.Errorf("expected issue 2, got %q", item.Title)
	}
}

func TestGitHubNextItemReviewCountsAsTouch(t *testing.T) {
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
	item, err := gh.NextItem("o", "r", "me", 30*time.Minute, nil)
	if err != nil {
		t.Fatalf("NextItem() error: %v", err)
	}
	if item == nil {
		t.Fatal("NextItem() returned nil")
	}
	// PR 5 should be skipped because I reviewed it within 30m, should get issue 6
	if item.Title != "Issue someone else updated" {
		t.Errorf("expected issue 6, got %q", item.Title)
	}
}

func TestGitHubNextItemAllTouchedByMe(t *testing.T) {
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
	item, err := gh.NextItem("o", "r", "me", 30*time.Minute, nil)
	if err != nil {
		t.Fatalf("NextItem() error: %v", err)
	}
	if item != nil {
		t.Errorf("expected nil (nothing to do), got %+v", item)
	}
}
