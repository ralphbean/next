package backend

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/rbean/next-up/format"
)

type ghActor struct {
	Login string `json:"login"`
}

type ghIssue struct {
	Number      int              `json:"number"`
	Title       string           `json:"title"`
	HTMLURL     string           `json:"html_url"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
	User        ghActor          `json:"user"`
	PullRequest *json.RawMessage `json:"pull_request,omitempty"`
}

type ghTimelineEvent struct {
	Event     string    `json:"event"`
	CreatedAt time.Time `json:"created_at"`
	Actor     ghActor   `json:"actor"`
	Body      string    `json:"body"`
}

type ghReview struct {
	User        ghActor   `json:"user"`
	State       string    `json:"state"`
	SubmittedAt time.Time `json:"submitted_at"`
	Body        string    `json:"body"`
}

type ghReaction struct {
	User      ghActor   `json:"user"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

type ghComment struct {
	ID        int       `json:"id"`
	User      ghActor   `json:"user"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	Reactions struct {
		TotalCount int `json:"total_count"`
	} `json:"reactions"`
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

func (g *gitHub) NextItems(owner, repo, user string, since time.Duration, ignoreEvents MatchSet, ignoreUsers MatchSet, limit int) ([]format.Item, error) {
	// Fetch first page of issues (includes PRs) sorted by updated
	endpoint := fmt.Sprintf("repos/%s/%s/issues", owner, repo)
	out, err := g.run("gh", "api", endpoint,
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

	// Prefetch timeline and reviews in parallel for all issues
	type prefetch struct {
		events                 []ghTimelineEvent
		reviews                []ghReview
		reactions              []ghReaction
		commentReactions       []ghReaction
		reviewCommentReactions []ghReaction
		err                    error
	}
	fetched := make([]prefetch, len(issues))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5)
	for i, issue := range issues {
		wg.Add(1)
		go func(i int, issue ghIssue) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			events, err := g.getTimeline(owner, repo, issue.Number)
			if err != nil {
				fetched[i].err = err
				return
			}
			fetched[i].events = events
			reactions, err := g.getReactions(owner, repo, issue.Number)
			if err != nil {
				fetched[i].err = err
				return
			}
			fetched[i].reactions = reactions
			commentReactions, err := g.getCommentReactions(owner, repo, issue.Number)
			if err != nil {
				fetched[i].err = err
				return
			}
			fetched[i].commentReactions = commentReactions
			if issue.PullRequest != nil {
				reviews, err := g.getReviews(owner, repo, issue.Number)
				if err != nil {
					fetched[i].err = err
					return
				}
				fetched[i].reviews = reviews
				reviewCommentReactions, err := g.getReviewCommentReactions(owner, repo, issue.Number)
				if err != nil {
					fetched[i].err = err
					return
				}
				fetched[i].reviewCommentReactions = reviewCommentReactions
			}
		}(i, issue)
	}
	wg.Wait()

	var result []format.Item
	for i, issue := range issues {
		if fetched[i].err != nil {
			return nil, fetched[i].err
		}
		events := fetched[i].events
		reviews := fetched[i].reviews
		reactions := append(fetched[i].reactions, fetched[i].commentReactions...)
		reactions = append(reactions, fetched[i].reviewCommentReactions...)

		// Check if user interacted within the since window
		userTouched := false
		for _, ev := range events {
			if ignoreUsers.Match(ev.Actor.Login) {
				continue
			}
			if ignoreEvents.Match(ev.Event) {
				continue
			}
			if ev.Actor.Login != "" && ev.Actor.Login == user && ev.CreatedAt.After(cutoff) {
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
			continue
		}

		// Build the item with events since user's last interaction
		var lastUserTime time.Time
		for _, ev := range events {
			if ignoreUsers.Match(ev.Actor.Login) {
				continue
			}
			if ignoreEvents.Match(ev.Event) {
				continue
			}
			if ev.Actor.Login == user && ev.CreatedAt.After(lastUserTime) {
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

		// Check if any non-user, non-ignored actor has any activity at all
		othersHaveActivity := false
		for _, ev := range events {
			if ev.Actor.Login != "" && ev.Actor.Login != user && !ignoreUsers.Match(ev.Actor.Login) {
				othersHaveActivity = true
				break
			}
		}
		if !othersHaveActivity {
			for _, r := range reviews {
				if r.User.Login != user && !ignoreUsers.Match(r.User.Login) {
					othersHaveActivity = true
					break
				}
			}
		}

		var fmtEvents []format.Event
		for _, ev := range events {
			if ev.Actor.Login == "" || ev.CreatedAt.IsZero() {
				continue
			}
			if ignoreEvents.Match(ev.Event) {
				continue
			}
			if ev.Actor.Login == user || ignoreUsers.Match(ev.Actor.Login) {
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
			// If others have activity that got filtered out, skip this item.
			// If no one else has touched it and it was filed by someone else,
			// include a synthetic "opened" event so it still surfaces.
			if othersHaveActivity || issue.User.Login == user || ignoreUsers.Match(issue.User.Login) {
				continue
			}
			fmtEvents = append(fmtEvents, format.Event{
				Timestamp: issue.CreatedAt,
				Author:    issue.User.Login,
				Summary:   "opened",
			})
		}

		result = append(result, format.Item{
			URL:    issue.HTMLURL,
			Title:  issue.Title,
			Events: fmtEvents,
		})
		if len(result) >= limit {
			break
		}
	}

	return result, nil
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

func (g *gitHub) getReactions(owner, repo string, number int) ([]ghReaction, error) {
	endpoint := fmt.Sprintf("repos/%s/%s/issues/%d/reactions", owner, repo, number)
	out, err := g.run("gh", "api", endpoint, "--paginate")
	if err != nil {
		return nil, fmt.Errorf("failed to get reactions for #%d: %w", number, err)
	}
	var reactions []ghReaction
	if err := json.Unmarshal(out, &reactions); err != nil {
		return nil, fmt.Errorf("failed to parse reactions: %w", err)
	}
	return reactions, nil
}

func (g *gitHub) getCommentReactions(owner, repo string, number int) ([]ghReaction, error) {
	comments, err := g.getComments(owner, repo, number)
	if err != nil {
		return nil, err
	}
	var all []ghReaction
	for _, c := range comments {
		if c.Reactions.TotalCount == 0 {
			continue
		}
		endpoint := fmt.Sprintf("repos/%s/%s/issues/comments/%d/reactions", owner, repo, c.ID)
		out, err := g.run("gh", "api", endpoint, "--paginate")
		if err != nil {
			return nil, fmt.Errorf("failed to get reactions for comment %d: %w", c.ID, err)
		}
		var reactions []ghReaction
		if err := json.Unmarshal(out, &reactions); err != nil {
			return nil, fmt.Errorf("failed to parse comment reactions: %w", err)
		}
		all = append(all, reactions...)
	}
	return all, nil
}

func (g *gitHub) getReviewCommentReactions(owner, repo string, number int) ([]ghReaction, error) {
	endpoint := fmt.Sprintf("repos/%s/%s/pulls/%d/comments", owner, repo, number)
	out, err := g.run("gh", "api", endpoint, "--paginate")
	if err != nil {
		return nil, fmt.Errorf("failed to get review comments for #%d: %w", number, err)
	}
	var comments []ghComment
	if err := json.Unmarshal(out, &comments); err != nil {
		return nil, fmt.Errorf("failed to parse review comments: %w", err)
	}
	var all []ghReaction
	for _, c := range comments {
		if c.Reactions.TotalCount == 0 {
			continue
		}
		ep := fmt.Sprintf("repos/%s/%s/pulls/comments/%d/reactions", owner, repo, c.ID)
		out, err := g.run("gh", "api", ep, "--paginate")
		if err != nil {
			return nil, fmt.Errorf("failed to get reactions for review comment %d: %w", c.ID, err)
		}
		var reactions []ghReaction
		if err := json.Unmarshal(out, &reactions); err != nil {
			return nil, fmt.Errorf("failed to parse review comment reactions: %w", err)
		}
		all = append(all, reactions...)
	}
	return all, nil
}

func (g *gitHub) getComments(owner, repo string, number int) ([]ghComment, error) {
	endpoint := fmt.Sprintf("repos/%s/%s/issues/%d/comments", owner, repo, number)
	out, err := g.run("gh", "api", endpoint, "--paginate")
	if err != nil {
		return nil, fmt.Errorf("failed to get comments for #%d: %w", number, err)
	}
	var comments []ghComment
	if err := json.Unmarshal(out, &comments); err != nil {
		return nil, fmt.Errorf("failed to parse comments: %w", err)
	}
	return comments, nil
}

func (g *gitHub) getReviews(owner, repo string, number int) ([]ghReview, error) {
	endpoint := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", owner, repo, number)
	out, err := g.run("gh", "api", endpoint, "--paginate")
	if err != nil {
		return nil, fmt.Errorf("failed to get reviews for #%d: %w", number, err)
	}
	var reviews []ghReview
	if err := json.Unmarshal(out, &reviews); err != nil {
		return nil, fmt.Errorf("failed to parse reviews: %w", err)
	}
	return reviews, nil
}

func reviewSummary(state, body string) string {
	switch state {
	case "APPROVED":
		if body != "" {
			if r := []rune(body); len(r) > 60 {
				body = string(r[:60])
			}
			return fmt.Sprintf("approved: > %s", body)
		}
		return "approved"
	default:
		summary := fmt.Sprintf("reviewed (%s)", state)
		if body != "" {
			if r := []rune(body); len(r) > 60 {
				body = string(r[:60])
			}
			summary = fmt.Sprintf("reviewed (%s): > %s", state, body)
		}
		return summary
	}
}

func eventSummary(event, body string) string {
	switch event {
	case "commented":
		if r := []rune(body); len(r) > 80 {
			body = string(r[:80])
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
