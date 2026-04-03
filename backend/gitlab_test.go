package backend

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestGitLabCurrentUser(t *testing.T) {
	runner := func(name string, args ...string) ([]byte, error) {
		return []byte(`{"username":"gluser"}` + "\n"), nil
	}
	gl := NewGitLab(runner, "")
	user, err := gl.CurrentUser()
	if err != nil {
		t.Fatalf("CurrentUser() error: %v", err)
	}
	if user != "gluser" {
		t.Errorf("CurrentUser() = %q, want %q", user, "gluser")
	}
}

func TestGitLabNextItems(t *testing.T) {
	now := time.Now()

	issues := []glIssue{
		{
			IID:       5,
			Title:     "My issue I touched",
			WebURL:    "https://gitlab.com/o/r/-/issues/5",
			UpdatedAt: now.Add(-2 * time.Hour),
		},
		{
			IID:       8,
			Title:     "Issue someone else updated",
			WebURL:    "https://gitlab.com/o/r/-/issues/8",
			UpdatedAt: now.Add(-1 * time.Hour),
		},
	}

	mrs := []glMR{
		{
			IID:       3,
			Title:     "MR from someone",
			WebURL:    "https://gitlab.com/o/r/-/merge_requests/3",
			UpdatedAt: now.Add(-45 * time.Minute),
		},
	}

	notes5 := []glNote{
		{
			Body:      "I'll handle this",
			CreatedAt: now.Add(-10 * time.Minute),
			Author:    glNoteAuthor{Username: "me"},
		},
	}

	notes8 := []glNote{
		{
			Body:      "Could someone take a look?",
			CreatedAt: now.Add(-1 * time.Hour),
			Author:    glNoteAuthor{Username: "other"},
		},
	}

	notesMR3 := []glNote{
		{
			Body:      "LGTM",
			CreatedAt: now.Add(-45 * time.Minute),
			Author:    glNoteAuthor{Username: "reviewer"},
		},
	}

	runner := func(name string, args ...string) ([]byte, error) {
		for _, a := range args {
			if strings.HasPrefix(a, "projects/o%2Fr/issues?") {
				return json.Marshal(issues)
			}
			if strings.HasPrefix(a, "projects/o%2Fr/merge_requests?") {
				return json.Marshal(mrs)
			}
			if a == "projects/o%2Fr/issues/5/notes" {
				return json.Marshal(notes5)
			}
			if a == "projects/o%2Fr/issues/8/notes" {
				return json.Marshal(notes8)
			}
			if a == "projects/o%2Fr/merge_requests/3/notes" {
				return json.Marshal(notesMR3)
			}
		}
		return nil, fmt.Errorf("unexpected call: %v", args)
	}

	gl := NewGitLab(runner, "")
	items, err := gl.NextItems("o", "r", "me", 30*time.Minute, nil, nil, 1)
	if err != nil {
		t.Fatalf("NextItems() error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("NextItems() returned %d items, want 1", len(items))
	}
	// Should pick MR 3 (45m ago, not touched by me) — most recently updated untouched item
	// Issue 8 was 1h ago, MR 3 was 45m ago, issue 5 was touched by me within 30m
	if items[0].Title != "MR from someone" {
		t.Errorf("expected MR 3, got %q", items[0].Title)
	}
}

func TestGitLabNextItemsNoneAvailable(t *testing.T) {
	now := time.Now()

	issues := []glIssue{
		{
			IID:       1,
			Title:     "Only issue",
			WebURL:    "https://gitlab.com/o/r/-/issues/1",
			UpdatedAt: now.Add(-5 * time.Minute),
		},
	}

	notes := []glNote{
		{
			Body:      "on it",
			CreatedAt: now.Add(-5 * time.Minute),
			Author:    glNoteAuthor{Username: "me"},
		},
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
				return json.Marshal(notes)
			}
		}
		return nil, fmt.Errorf("unexpected call: %v", args)
	}

	gl := NewGitLab(runner, "")
	items, err := gl.NextItems("o", "r", "me", 30*time.Minute, nil, nil, 1)
	if err != nil {
		t.Fatalf("NextItems() error: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected empty slice, got %+v", items)
	}
}

func TestGitLabNextItemsLimit(t *testing.T) {
	now := time.Now()

	issues := []glIssue{
		{
			IID:       1,
			Title:     "First issue",
			WebURL:    "https://gitlab.com/o/r/-/issues/1",
			UpdatedAt: now.Add(-10 * time.Minute),
		},
		{
			IID:       2,
			Title:     "Second issue",
			WebURL:    "https://gitlab.com/o/r/-/issues/2",
			UpdatedAt: now.Add(-20 * time.Minute),
		},
	}

	notes1 := []glNote{
		{Body: "please look", CreatedAt: now.Add(-10 * time.Minute), Author: glNoteAuthor{Username: "other"}},
	}
	notes2 := []glNote{
		{Body: "needs review", CreatedAt: now.Add(-20 * time.Minute), Author: glNoteAuthor{Username: "other"}},
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

	// limit=2 should return both items
	items, err := gl.NextItems("o", "r", "me", 30*time.Minute, nil, nil, 2)
	if err != nil {
		t.Fatalf("NextItems() error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("NextItems(limit=2) returned %d items, want 2", len(items))
	}
	if items[0].Title != "First issue" {
		t.Errorf("first item: expected 'First issue', got %q", items[0].Title)
	}
	if items[1].Title != "Second issue" {
		t.Errorf("second item: expected 'Second issue', got %q", items[1].Title)
	}
}

func TestGitLabNextItemsUntouchedByAnyone(t *testing.T) {
	now := time.Now()

	issues := []glIssue{
		{
			IID:       10,
			Title:     "New issue from someone else",
			WebURL:    "https://gitlab.com/o/r/-/issues/10",
			CreatedAt: now.Add(-2 * time.Hour),
			UpdatedAt: now.Add(-2 * time.Hour),
			Author:    glNoteAuthor{Username: "other"},
		},
		{
			IID:       11,
			Title:     "My own issue with no notes",
			WebURL:    "https://gitlab.com/o/r/-/issues/11",
			CreatedAt: now.Add(-3 * time.Hour),
			UpdatedAt: now.Add(-3 * time.Hour),
			Author:    glNoteAuthor{Username: "me"},
		},
	}

	emptyNotes := []glNote{}

	runner := func(name string, args ...string) ([]byte, error) {
		for _, a := range args {
			if strings.HasPrefix(a, "projects/o%2Fr/issues?") {
				return json.Marshal(issues)
			}
			if strings.HasPrefix(a, "projects/o%2Fr/merge_requests?") {
				return json.Marshal([]glMR{})
			}
		}
		return json.Marshal(emptyNotes)
	}

	gl := NewGitLab(runner, "")
	items, err := gl.NextItems("o", "r", "me", 30*time.Minute, nil, nil, 5)
	if err != nil {
		t.Fatalf("NextItems() error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("NextItems() returned %d items, want 1", len(items))
	}
	if items[0].Title != "New issue from someone else" {
		t.Errorf("expected issue 10, got %q", items[0].Title)
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

func TestGitLabNextItemsApprovalNote(t *testing.T) {
	now := time.Now()

	issues := []glIssue{}
	mrs := []glMR{
		{
			IID:       20,
			Title:     "MR that was approved",
			WebURL:    "https://gitlab.com/o/r/-/merge_requests/20",
			CreatedAt: now.Add(-2 * time.Hour),
			UpdatedAt: now.Add(-10 * time.Minute),
			Author:    glNoteAuthor{Username: "me"},
		},
	}

	// The approval shows up as a system note
	notesMR20 := []glNote{
		{
			Body:      "approved this merge request",
			CreatedAt: now.Add(-10 * time.Minute),
			Author:    glNoteAuthor{Username: "reviewer"},
			System:    true,
		},
	}

	runner := func(name string, args ...string) ([]byte, error) {
		for _, a := range args {
			if strings.HasPrefix(a, "projects/o%2Fr/issues?") {
				return json.Marshal(issues)
			}
			if strings.HasPrefix(a, "projects/o%2Fr/merge_requests?") {
				return json.Marshal(mrs)
			}
			if a == "projects/o%2Fr/merge_requests/20/notes" {
				return json.Marshal(notesMR20)
			}
		}
		return nil, fmt.Errorf("unexpected call: %v", args)
	}

	gl := NewGitLab(runner, "")
	items, err := gl.NextItems("o", "r", "me", 30*time.Minute, nil, nil, 5)
	if err != nil {
		t.Fatalf("NextItems() error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("NextItems() returned %d items, want 1", len(items))
	}
	if len(items[0].Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(items[0].Events))
	}
	if items[0].Events[0].Summary != "approved" {
		t.Errorf("expected 'approved' summary, got %q", items[0].Events[0].Summary)
	}
	if items[0].Events[0].Author != "reviewer" {
		t.Errorf("expected author 'reviewer', got %q", items[0].Events[0].Author)
	}
}
