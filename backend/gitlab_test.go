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

func TestGitLabNextItem(t *testing.T) {
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
	item, err := gl.NextItem("o", "r", "me", 30*time.Minute, nil)
	if err != nil {
		t.Fatalf("NextItem() error: %v", err)
	}
	if item == nil {
		t.Fatal("NextItem() returned nil")
	}
	// Should pick MR 3 (45m ago, not touched by me) — most recently updated untouched item
	// Issue 8 was 1h ago, MR 3 was 45m ago, issue 5 was touched by me within 30m
	if item.Title != "MR from someone" {
		t.Errorf("expected MR 3, got %q", item.Title)
	}
}

func TestGitLabNextItemNoneAvailable(t *testing.T) {
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
	item, err := gl.NextItem("o", "r", "me", 30*time.Minute, nil)
	if err != nil {
		t.Fatalf("NextItem() error: %v", err)
	}
	if item != nil {
		t.Errorf("expected nil, got %+v", item)
	}
}
