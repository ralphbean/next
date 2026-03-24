package format

import (
	"strings"
	"testing"
	"time"
)

func TestRelativeTime(t *testing.T) {
	tests := []struct {
		name string
		ago  time.Duration
		want string
	}{
		{"seconds", 30 * time.Second, "just now"},
		{"minutes", 5 * time.Minute, "5 minutes ago"},
		{"one minute", 1 * time.Minute, "1 minute ago"},
		{"hours", 3 * time.Hour, "3 hours ago"},
		{"one hour", 1 * time.Hour, "1 hour ago"},
		{"days", 2 * 24 * time.Hour, "2 days ago"},
		{"one day", 24 * time.Hour, "1 day ago"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := time.Now().Add(-tt.ago)
			got := RelativeTime(ts)
			if got != tt.want {
				t.Errorf("RelativeTime() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatEvent(t *testing.T) {
	now := time.Now()
	e := Event{
		Timestamp: now.Add(-3 * time.Hour),
		Author:    "user123",
		Summary:   "commented on the issue: > I think that this is a good idea, but we should consider the performance implications of this approach before merging",
	}
	got := FormatEvent(e, 120)

	if !strings.HasPrefix(got, "(3 hours ago)") {
		t.Errorf("expected prefix '(3 hours ago)', got %q", got)
	}
	if !strings.Contains(got, "@user123") {
		t.Errorf("expected @user123 in output, got %q", got)
	}
	if len(got) > 120 {
		t.Errorf("expected len <= 120, got %d: %q", len(got), got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("expected truncation suffix '...', got %q", got)
	}
}

func TestFormatEventShort(t *testing.T) {
	e := Event{
		Timestamp: time.Now().Add(-1 * time.Minute),
		Author:    "bob",
		Summary:   "approved",
	}
	got := FormatEvent(e, 120)
	if strings.HasSuffix(got, "...") {
		t.Errorf("short event should not be truncated: %q", got)
	}
}

func TestFormatItem(t *testing.T) {
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
	if !strings.Contains(got, item.URL) {
		t.Errorf("expected URL in output")
	}
	if !strings.Contains(got, item.Title) {
		t.Errorf("expected title in output")
	}
	if !strings.Contains(got, "@alice") {
		t.Errorf("expected author in output")
	}
}

func TestFormatItems(t *testing.T) {
	items := []Item{
		{
			URL:   "https://github.com/owner/repo/issues/1",
			Title: "First issue",
			Events: []Event{
				{Timestamp: time.Now().Add(-1 * time.Hour), Author: "alice", Summary: "commented: hello"},
			},
		},
		{
			URL:   "https://github.com/owner/repo/issues/2",
			Title: "Second issue",
			Events: []Event{
				{Timestamp: time.Now().Add(-2 * time.Hour), Author: "bob", Summary: "commented: world"},
			},
		},
	}
	got := FormatItems(items, 120)
	if !strings.Contains(got, "First issue") {
		t.Error("expected first item title in output")
	}
	if !strings.Contains(got, "Second issue") {
		t.Error("expected second item title in output")
	}
	if !strings.Contains(got, "@alice") {
		t.Error("expected first item author in output")
	}
	if !strings.Contains(got, "@bob") {
		t.Error("expected second item author in output")
	}
	// Items should be separated by a blank line
	if !strings.Contains(got, "\n\n") {
		t.Error("expected blank line separator between items")
	}
}

func TestFormatItemsSingle(t *testing.T) {
	items := []Item{
		{
			URL:   "https://github.com/owner/repo/issues/1",
			Title: "Only issue",
			Events: []Event{
				{Timestamp: time.Now().Add(-1 * time.Hour), Author: "alice", Summary: "commented: hi"},
			},
		},
	}
	got := FormatItems(items, 120)
	// Single item should not have trailing blank line separator
	singleGot := FormatItem(items[0], 120)
	if got != singleGot {
		t.Errorf("FormatItems with single item should match FormatItem output.\nFormatItems: %q\nFormatItem:  %q", got, singleGot)
	}
}
