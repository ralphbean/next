package format

import (
	"fmt"
	"strings"
	"time"
)

type Event struct {
	Timestamp time.Time
	Author    string
	Summary   string
}

type Item struct {
	URL    string
	Title  string
	Events []Event
}

// RelativeTime returns a human-readable relative time string.
func RelativeTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < 2*time.Minute:
		return "1 minute ago"
	case d < time.Hour:
		return fmt.Sprintf("%d minutes ago", int(d.Minutes()))
	case d < 2*time.Hour:
		return "1 hour ago"
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	case d < 48*time.Hour:
		return "1 day ago"
	default:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	}
}

// FormatEvent formats a single event as a one-line string, truncated to maxWidth.
func FormatEvent(e Event, maxWidth int) string {
	line := fmt.Sprintf("(%s) @%s %s", RelativeTime(e.Timestamp), e.Author, e.Summary)
	if r := []rune(line); len(r) > maxWidth {
		line = string(r[:maxWidth-3]) + "..."
	}
	return line
}

// FormatItem formats an item with its URL, title, and events.
func FormatItem(item Item, maxWidth int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", item.URL)
	fmt.Fprintf(&b, "%s\n", item.Title)
	for _, e := range item.Events {
		fmt.Fprintf(&b, "  %s\n", FormatEvent(e, maxWidth-2))
	}
	return b.String()
}

// FormatItems formats multiple items. When there are multiple items, each is
// prefixed with a bullet character and items are separated by a horizontal line.
func FormatItems(items []Item, maxWidth int) string {
	var b strings.Builder
	if len(items) == 1 {
		b.WriteString(FormatItem(items[0], maxWidth))
		return b.String()
	}
	// Build a separator line that spans the terminal width
	sepWidth := maxWidth
	if sepWidth > 40 {
		sepWidth = 40
	}
	separator := strings.Repeat("─", sepWidth)
	for i, item := range items {
		if i > 0 {
			fmt.Fprintf(&b, "  %s\n", separator)
		}
		fmt.Fprintf(&b, "▶ %s\n", item.URL)
		fmt.Fprintf(&b, "  %s\n", item.Title)
		for _, e := range item.Events {
			fmt.Fprintf(&b, "    %s\n", FormatEvent(e, maxWidth-4))
		}
	}
	return b.String()
}
