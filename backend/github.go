package backend

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/ralphbean/next/format"
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
	User      ghActor   `json:"user"`
	Body      string    `json:"body"`
}

// login returns the effective actor login for a timeline event.
// "commented" events use the "user" field instead of "actor".
func (e ghTimelineEvent) login() string {
	if e.Actor.Login != "" {
		return e.Actor.Login
	}
	return e.User.Login
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

const maxRetries = 3

// runAPI wraps g.run with rate-limit retry. When gh api returns a rate
// limit error, it queries the rate_limit endpoint for the reset time
// and waits, falling back to exponential backoff if that fails.
func (g *gitHub) runAPI(name string, args ...string) ([]byte, error) {
	backoff := 5 * time.Second
	for attempt := 0; ; attempt++ {
		out, err := g.run(name, args...)
		if err == nil {
			return out, nil
		}
		if !isRateLimitError(err) || attempt >= maxRetries {
			return out, err
		}
		wait := g.rateLimitWait()
		if wait <= 0 {
			wait = backoff
			backoff *= 2
		}
		fmt.Fprintf(os.Stderr, "\033[2mRate limited by GitHub API, waiting %s before retrying...\033[0m\n", wait.Truncate(time.Second))
		time.Sleep(wait)
	}
}

func isRateLimitError(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "rate limit")
}

// rateLimitWait queries the GitHub rate_limit endpoint and returns how
// long to wait until the core rate limit resets. Returns 0 on any error.
func (g *gitHub) rateLimitWait() time.Duration {
	out, err := g.run("gh", "api", "rate_limit")
	if err != nil {
		return 0
	}
	var rl struct {
		Rate struct {
			Remaining int   `json:"remaining"`
			Reset     int64 `json:"reset"`
		} `json:"rate"`
	}
	if err := json.Unmarshal(out, &rl); err != nil {
		return 0
	}
	resetTime := time.Unix(rl.Rate.Reset, 0)
	wait := time.Until(resetTime) + 2*time.Second // small buffer
	if wait < 0 {
		return 0
	}
	return wait
}

func (g *gitHub) CurrentUser() (string, error) {
	out, err := g.runAPI("gh", "api", "user")
	if err != nil {
		return "", fmt.Errorf("failed to get current GitHub user: %w", err)
	}
	var u ghUser
	if err := json.Unmarshal(out, &u); err != nil {
		return "", fmt.Errorf("failed to parse user response: %w", err)
	}
	return u.Login, nil
}

func (g *gitHub) NextItems(owner, repo, user string, cooldown time.Duration, since time.Time, ignoreEvents MatchSet, ignoreUsers MatchSet, limit int, scope Scope, emit func(format.Item)) error {
	var issues []ghIssue
	var err error
	if scope == ScopeOrg {
		issues, err = g.searchOrgIssues(owner, since)
	} else {
		issues, err = g.listRepoIssues(owner, repo, since)
	}
	if err != nil {
		return err
	}

	// Sort by updated descending (most recent first)
	sort.Slice(issues, func(i, j int) bool {
		return issues[i].UpdatedAt.After(issues[j].UpdatedAt)
	})

	cutoff := time.Now().Add(-cooldown)

	type candidate struct {
		item      format.Item
		tier      int
		updatedAt time.Time
	}
	var candidates []candidate

	for _, issue := range issues {
		// For org scope, extract owner/repo per issue from its URL
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

		// Fetch details lazily per issue to avoid rate limiting
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

		// Check if user interacted within the since window
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

		// Build the item with events since user's last interaction
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

		// Check if any non-user, non-ignored actor has activity after the user's last interaction
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
			// If the user has already interacted and there's no new activity
			// from others, skip — there's nothing new for the user to act on.
			// If no one else has touched it, the user hasn't interacted, and
			// it was filed by someone else, include a synthetic "opened" event.
			if othersHaveActivity || !lastUserTime.IsZero() || issue.User.Login == user {
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

func (g *gitHub) listRepoIssues(owner, repo string, since time.Time) ([]ghIssue, error) {
	endpoint := fmt.Sprintf("repos/%s/%s/issues", owner, repo)
	args := []string{"api", endpoint,
		"--paginate",
		"--method", "GET",
		"-f", "state=open",
		"-f", "sort=updated",
		"-f", "direction=desc",
		"-f", "per_page=100",
	}
	if !since.IsZero() {
		args = append(args, "-f", "since="+since.Format(time.RFC3339))
	}
	out, err := g.runAPI("gh", args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list issues: %w", err)
	}
	var issues []ghIssue
	if err := json.Unmarshal(out, &issues); err != nil {
		return nil, fmt.Errorf("failed to parse issues: %w", err)
	}
	return issues, nil
}

func (g *gitHub) searchOrgIssues(org string, since time.Time) ([]ghIssue, error) {
	query := fmt.Sprintf("org:%s is:open", org)
	if !since.IsZero() {
		query += " updated:>=" + since.Format("2006-01-02")
	}
	out, err := g.runAPI("gh", "api", "search/issues",
		"--method", "GET",
		"-f", "q="+query,
		"-f", "sort=updated",
		"-f", "order=desc",
		"-f", "per_page=100",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to search org issues: %w", err)
	}
	var result struct {
		Items []ghIssue `json:"items"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("failed to parse search results: %w", err)
	}
	return result.Items, nil
}

// parseRepoFromURL extracts owner and repo from a GitHub issue/PR URL
// like "https://github.com/OWNER/REPO/issues/123".
func parseRepoFromURL(htmlURL string) (string, string) {
	parts := strings.Split(htmlURL, "/")
	if len(parts) >= 5 {
		return parts[3], parts[4]
	}
	return "", ""
}

func (g *gitHub) getTimeline(owner, repo string, number int) ([]ghTimelineEvent, error) {
	endpoint := fmt.Sprintf("repos/%s/%s/issues/%d/timeline", owner, repo, number)
	out, err := g.runAPI("gh", "api", endpoint, "--paginate")
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
	out, err := g.runAPI("gh", "api", endpoint, "--paginate")
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
		out, err := g.runAPI("gh", "api", endpoint, "--paginate")
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
	out, err := g.runAPI("gh", "api", endpoint, "--paginate")
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
		out, err := g.runAPI("gh", "api", ep, "--paginate")
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
	out, err := g.runAPI("gh", "api", endpoint, "--paginate")
	if err != nil {
		return nil, fmt.Errorf("failed to get comments for #%d: %w", number, err)
	}
	var comments []ghComment
	if err := json.Unmarshal(out, &comments); err != nil {
		return nil, fmt.Errorf("failed to parse comments: %w", err)
	}
	return comments, nil
}

func (g *gitHub) getReviewReactions(owner, repo string, number int) ([]ghReaction, error) {
	query := fmt.Sprintf(`{
		repository(owner: %q, name: %q) {
			pullRequest(number: %d) {
				reviews(first: 100) {
					nodes {
						reactions(first: 100) {
							nodes {
								user { login }
								content
								createdAt
							}
						}
					}
				}
			}
		}
	}`, owner, repo, number)
	out, err := g.runAPI("gh", "api", "graphql", "-f", "query="+query)
	if err != nil {
		return nil, fmt.Errorf("failed to get review reactions for #%d: %w", number, err)
	}
	var resp struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					Reviews struct {
						Nodes []struct {
							Reactions struct {
								Nodes []struct {
									User      ghActor   `json:"user"`
									Content   string    `json:"content"`
									CreatedAt time.Time `json:"createdAt"`
								} `json:"nodes"`
							} `json:"reactions"`
						} `json:"nodes"`
					} `json:"reviews"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse review reactions: %w", err)
	}
	var all []ghReaction
	for _, review := range resp.Data.Repository.PullRequest.Reviews.Nodes {
		for _, r := range review.Reactions.Nodes {
			all = append(all, ghReaction{
				User:      r.User,
				Content:   r.Content,
				CreatedAt: r.CreatedAt,
			})
		}
	}
	return all, nil
}

func (g *gitHub) getReviews(owner, repo string, number int) ([]ghReview, error) {
	endpoint := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", owner, repo, number)
	out, err := g.runAPI("gh", "api", endpoint, "--paginate")
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
