package backend

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/rbean/next-up/format"
)

type ghActor struct {
	Login string `json:"login"`
}

type ghIssue struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	HTMLURL   string    `json:"html_url"`
	UpdatedAt time.Time `json:"updated_at"`
	IsPR      bool      `json:"pull_request,omitempty"`
}

type ghTimelineEvent struct {
	Event     string    `json:"event"`
	CreatedAt time.Time `json:"created_at"`
	Actor     ghActor   `json:"actor"`
	Body      string    `json:"body"`
}

type ghUser struct {
	Login string `json:"login"`
}

type gitHub struct {
	run CmdRunner
}

func NewGitHub(run CmdRunner) Backend {
	return &gitHub{run: run}
}

func (g *gitHub) CurrentUser() (string, error) {
	out, err := g.run("gh", "api", "user")
	if err != nil {
		return "", fmt.Errorf("failed to get current GitHub user: %w", err)
	}
	var u ghUser
	if err := json.Unmarshal(out, &u); err != nil {
		return "", fmt.Errorf("failed to parse user response: %w", err)
	}
	return u.Login, nil
}

func (g *gitHub) NextItem(owner, repo, user string, since time.Duration) (*format.Item, error) {
	// Fetch issues (includes PRs) sorted by updated
	endpoint := fmt.Sprintf("repos/%s/%s/issues", owner, repo)
	out, err := g.run("gh", "api", endpoint,
		"--paginate",
		"-q", ".",
		"--method", "GET",
		"-f", "state=open",
		"-f", "sort=updated",
		"-f", "direction=desc",
		"-f", "per_page=30",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list issues: %w", err)
	}

	var issues []ghIssue
	if err := json.Unmarshal(out, &issues); err != nil {
		return nil, fmt.Errorf("failed to parse issues: %w", err)
	}

	// Sort by updated descending (most recent first)
	sort.Slice(issues, func(i, j int) bool {
		return issues[i].UpdatedAt.After(issues[j].UpdatedAt)
	})

	cutoff := time.Now().Add(-since)

	for _, issue := range issues {
		events, err := g.getTimeline(owner, repo, issue.Number)
		if err != nil {
			return nil, err
		}

		// Check if user interacted within the since window
		userTouched := false
		for _, ev := range events {
			if ev.Actor.Login == user && ev.CreatedAt.After(cutoff) {
				userTouched = true
				break
			}
		}
		if userTouched {
			continue
		}

		// Build the item with events since user's last interaction
		var lastUserTime time.Time
		for _, ev := range events {
			if ev.Actor.Login == user && ev.CreatedAt.After(lastUserTime) {
				lastUserTime = ev.CreatedAt
			}
		}

		var fmtEvents []format.Event
		for _, ev := range events {
			if ev.Actor.Login == user {
				continue
			}
			if !lastUserTime.IsZero() && ev.CreatedAt.Before(lastUserTime) {
				continue
			}
			summary := eventSummary(ev.Event, ev.Body)
			fmtEvents = append(fmtEvents, format.Event{
				Timestamp: ev.CreatedAt,
				Author:    ev.Actor.Login,
				Summary:   summary,
			})
		}

		return &format.Item{
			URL:    issue.HTMLURL,
			Title:  issue.Title,
			Events: fmtEvents,
		}, nil
	}

	return nil, nil
}

func (g *gitHub) getTimeline(owner, repo string, number int) ([]ghTimelineEvent, error) {
	endpoint := fmt.Sprintf("repos/%s/%s/issues/%d/timeline", owner, repo, number)
	out, err := g.run("gh", "api", endpoint, "--paginate")
	if err != nil {
		return nil, fmt.Errorf("failed to get timeline for #%d: %w", number, err)
	}
	var events []ghTimelineEvent
	if err := json.Unmarshal(out, &events); err != nil {
		return nil, fmt.Errorf("failed to parse timeline: %w", err)
	}
	return events, nil
}

func eventSummary(event, body string) string {
	switch event {
	case "commented":
		if len(body) > 80 {
			body = body[:80]
		}
		return fmt.Sprintf("commented: > %s", body)
	case "closed":
		return "closed the issue"
	case "reopened":
		return "reopened the issue"
	case "labeled":
		return "added a label"
	case "assigned":
		return "was assigned"
	case "review_requested":
		return "requested a review"
	case "reviewed":
		return "reviewed the PR"
	case "merged":
		return "merged the PR"
	default:
		return event
	}
}
