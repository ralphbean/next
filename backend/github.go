package backend

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
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
	CreatedAt time.Time `json:"created_at"`
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

type ghItemResult struct {
	item *format.Item
	err  error
}

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

func (g *gitHub) NextItems(owner, repo, user string, cooldown time.Duration, since time.Time, ignoreEvents MatchSet, ignoreUsers MatchSet, limit int, maxEvents int, scope Scope, emit func(format.Item)) error {
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

	// Split by authorship for tier-aware ordering without processing all items.
	// Authored issues (tier 1) are always highest priority and can be identified
	// without any API calls since authorship is in the issue list response.
	var authoredIssues, otherIssues []ghIssue
	for _, issue := range issues {
		if issue.User.Login == user {
			authoredIssues = append(authoredIssues, issue)
		} else {
			otherIssues = append(otherIssues, issue)
		}
	}
	// Process authored first so tier-1 items are emitted before tier-2/3.
	// Both slices preserve the recency ordering from the sort above.
	orderedIssues := append(authoredIssues, otherIssues...)

	found := 0
	for batchStart := 0; batchStart < len(orderedIssues) && found < limit; batchStart += maxConcurrency {
		batchEnd := batchStart + maxConcurrency
		if batchEnd > len(orderedIssues) {
			batchEnd = len(orderedIssues)
		}
		batch := orderedIssues[batchStart:batchEnd]

		type indexedIssue struct {
			idx                       int
			issue                     ghIssue
			issueOwner, issueRepo string
		}

		var valid []indexedIssue
		for i, issue := range batch {
			issueOwner, issueRepo := owner, repo
			if scope == ScopeOrg {
				issueOwner, issueRepo = parseRepoFromURL(issue.HTMLURL)
				if issueOwner == "" || issueRepo == "" {
					continue
				}
			}
			valid = append(valid, indexedIssue{idx: i, issue: issue, issueOwner: issueOwner, issueRepo: issueRepo})
		}

		results := make([]ghItemResult, len(batch))
		var wg sync.WaitGroup
		for _, vi := range valid {
			wg.Add(1)
			go func(idx int, iss ghIssue, o, r string) {
				defer wg.Done()
				results[idx] = g.processIssue(iss, o, r, user, cutoff, ignoreEvents, ignoreUsers, maxEvents)
			}(vi.idx, vi.issue, vi.issueOwner, vi.issueRepo)
		}
		wg.Wait()

		for _, res := range results {
			if res.err != nil {
				return res.err
			}
			if res.item != nil {
				emit(*res.item)
				found++
				if found >= limit {
					break
				}
			}
		}
	}

	return nil
}

func (g *gitHub) processIssue(issue ghIssue, owner, repo, user string, cutoff time.Time, ignoreEvents, ignoreUsers MatchSet, maxEvents int) ghItemResult {
	kind := "issue"
	if issue.PullRequest != nil {
		kind = "PR"
	}
	label := fmt.Sprintf("#%d", issue.Number)
	title := issue.Title
	if r := []rune(title); len(r) > 50 {
		title = string(r[:50]) + "..."
	}

	// Phase A: fetch timeline and check cooldown early
	events, err := g.getTimeline(owner, repo, issue.Number, maxEvents)
	if err != nil {
		return ghItemResult{err: err}
	}
	for _, ev := range events {
		if ignoreUsers.Match(ev.login()) || ignoreEvents.Match(ev.Event) {
			continue
		}
		if ev.login() != "" && ev.login() == user && ev.CreatedAt.After(cutoff) {
			fmt.Fprintf(os.Stderr, "\033[2m  %s %s %s — skipped (cooldown)\033[0m\n", kind, label, title)
			return ghItemResult{}
		}
	}

	// Phase B: fetch reviews for PRs and check cooldown
	var reviews []ghReview
	if issue.PullRequest != nil {
		reviews, err = g.getReviews(owner, repo, issue.Number, maxEvents)
		if err != nil {
			return ghItemResult{err: err}
		}
		for _, r := range reviews {
			if ignoreUsers.Match(r.User.Login) {
				continue
			}
			if r.User.Login == user && r.SubmittedAt.After(cutoff) {
				fmt.Fprintf(os.Stderr, "\033[2m  %s %s %s — skipped (cooldown)\033[0m\n", kind, label, title)
				return ghItemResult{}
			}
		}
	}

	// Phase C: fetch all reactions via single GraphQL query and check cooldown
	reactions, err := g.getAllReactions(owner, repo, issue.Number, issue.PullRequest != nil, maxEvents)
	if err != nil {
		return ghItemResult{err: err}
	}
	for _, r := range reactions {
		if r.User.Login == user && r.CreatedAt.After(cutoff) {
			fmt.Fprintf(os.Stderr, "\033[2m  %s %s %s — skipped (cooldown)\033[0m\n", kind, label, title)
			return ghItemResult{}
		}
	}

	var lastUserTime time.Time
	for _, ev := range events {
		if ignoreUsers.Match(ev.login()) || ignoreEvents.Match(ev.Event) {
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
		if othersHaveActivity || !lastUserTime.IsZero() || issue.User.Login == user {
			fmt.Fprintf(os.Stderr, "\033[2m  %s %s %s — skipped (no new activity)\033[0m\n", kind, label, title)
			return ghItemResult{}
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

	item := format.Item{
		URL:    issue.HTMLURL,
		Title:  issue.Title,
		Events: fmtEvents,
		Tier:   tier,
	}
	return ghItemResult{item: &item}
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

func (g *gitHub) getTimeline(owner, repo string, number, maxEvents int) ([]ghTimelineEvent, error) {
	endpoint := fmt.Sprintf("repos/%s/%s/issues/%d/timeline", owner, repo, number)
	out, err := g.runAPI("gh", "api", endpoint, "--paginate")
	if err != nil {
		return nil, fmt.Errorf("failed to get timeline for #%d: %w", number, err)
	}
	var events []ghTimelineEvent
	if err := json.Unmarshal(out, &events); err != nil {
		return nil, fmt.Errorf("failed to parse timeline: %w", err)
	}
	if maxEvents > 0 && len(events) > maxEvents {
		events = events[len(events)-maxEvents:]
	}
	return events, nil
}

func (g *gitHub) getReviews(owner, repo string, number, maxEvents int) ([]ghReview, error) {
	endpoint := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", owner, repo, number)
	out, err := g.runAPI("gh", "api", endpoint, "--paginate")
	if err != nil {
		return nil, fmt.Errorf("failed to get reviews for #%d: %w", number, err)
	}
	var reviews []ghReview
	if err := json.Unmarshal(out, &reviews); err != nil {
		return nil, fmt.Errorf("failed to parse reviews: %w", err)
	}
	if maxEvents > 0 && len(reviews) > maxEvents {
		reviews = reviews[len(reviews)-maxEvents:]
	}
	return reviews, nil
}

func (g *gitHub) getAllReactions(owner, repo string, number int, isPR bool, maxEvents int) ([]ghReaction, error) {
	limit := maxEvents
	if limit <= 0 {
		limit = 100
	}
	var query string
	if isPR {
		query = fmt.Sprintf(`{
			repository(owner: %q, name: %q) {
				pullRequest(number: %d) {
					reactions(last: 20) { nodes { user { login } createdAt } }
					comments(last: %d) { nodes { reactions(last: 20) { nodes { user { login } createdAt } } } }
					reviews(last: %d) { nodes {
						reactions(last: 20) { nodes { user { login } createdAt } }
						comments(last: 20) { nodes { reactions(last: 20) { nodes { user { login } createdAt } } } }
					} }
				}
			}
		}`, owner, repo, number, limit, limit)
	} else {
		query = fmt.Sprintf(`{
			repository(owner: %q, name: %q) {
				issue(number: %d) {
					reactions(last: 20) { nodes { user { login } createdAt } }
					comments(last: %d) { nodes { reactions(last: 20) { nodes { user { login } createdAt } } } }
				}
			}
		}`, owner, repo, number, limit)
	}

	out, err := g.runAPI("gh", "api", "graphql", "-f", "query="+query)
	if err != nil {
		return nil, fmt.Errorf("failed to get reactions for #%d: %w", number, err)
	}

	type gqlReactionNode struct {
		User      ghActor   `json:"user"`
		CreatedAt time.Time `json:"createdAt"`
	}
	type gqlReactions struct {
		Nodes []gqlReactionNode `json:"nodes"`
	}
	type gqlComment struct {
		Reactions gqlReactions `json:"reactions"`
	}
	type gqlReview struct {
		Reactions gqlReactions `json:"reactions"`
		Comments  struct {
			Nodes []gqlComment `json:"nodes"`
		} `json:"comments"`
	}
	type gqlItem struct {
		Reactions gqlReactions `json:"reactions"`
		Comments  struct {
			Nodes []gqlComment `json:"nodes"`
		} `json:"comments"`
		Reviews struct {
			Nodes []gqlReview `json:"nodes"`
		} `json:"reviews"`
	}

	var resp struct {
		Data struct {
			Repository struct {
				Issue       *gqlItem `json:"issue"`
				PullRequest *gqlItem `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse reactions: %w", err)
	}

	item := resp.Data.Repository.Issue
	if isPR {
		item = resp.Data.Repository.PullRequest
	}
	if item == nil {
		return nil, nil
	}

	var all []ghReaction
	for _, n := range item.Reactions.Nodes {
		all = append(all, ghReaction{User: n.User, CreatedAt: n.CreatedAt})
	}
	for _, c := range item.Comments.Nodes {
		for _, n := range c.Reactions.Nodes {
			all = append(all, ghReaction{User: n.User, CreatedAt: n.CreatedAt})
		}
	}
	for _, r := range item.Reviews.Nodes {
		for _, n := range r.Reactions.Nodes {
			all = append(all, ghReaction{User: n.User, CreatedAt: n.CreatedAt})
		}
		for _, c := range r.Comments.Nodes {
			for _, n := range c.Reactions.Nodes {
				all = append(all, ghReaction{User: n.User, CreatedAt: n.CreatedAt})
			}
		}
	}
	return all, nil
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
