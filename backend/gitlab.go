package backend

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ralphbean/next/format"
)

// fixPaginatedJSON handles glab's --paginate output which concatenates
// multiple JSON arrays like [...][...] into a single valid array.
func fixPaginatedJSON(data []byte) []byte {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return []byte("[]")
	}
	// Replace "][" with "," to merge concatenated arrays
	data = bytes.ReplaceAll(data, []byte("]["), []byte(","))
	return data
}

type glNoteAuthor struct {
	Username string `json:"username"`
}

type glNote struct {
	Body      string       `json:"body"`
	CreatedAt time.Time    `json:"created_at"`
	Author    glNoteAuthor `json:"author"`
	System    bool         `json:"system"`
}

type glIssue struct {
	IID       int          `json:"iid"`
	Title     string       `json:"title"`
	WebURL    string       `json:"web_url"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
	Author    glNoteAuthor `json:"author"`
	ProjectID int          `json:"project_id"`
}

type glMR struct {
	IID       int          `json:"iid"`
	Title     string       `json:"title"`
	WebURL    string       `json:"web_url"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
	Author    glNoteAuthor `json:"author"`
	ProjectID int          `json:"project_id"`
}

type glUser struct {
	Username string `json:"username"`
}

// glItem is a unified type for sorting issues and MRs together.
type glItem struct {
	IID        int
	Title      string
	WebURL     string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	Author     string
	Kind       string // "issues" or "merge_requests"
	ProjectRef string // URL-encoded project path or numeric project ID for API calls
}

type gitLab struct {
	run  CmdRunner
	host string
}

func NewGitLab(run CmdRunner, host string) Backend {
	return &gitLab{run: run, host: host}
}

func (g *gitLab) cmd() string {
	return "glab"
}

func (g *gitLab) CurrentUser() (string, error) {
	out, err := g.run(g.cmd(), "api", "user")
	if err != nil {
		return "", fmt.Errorf("failed to get current GitLab user: %w", err)
	}
	var u glUser
	if err := json.Unmarshal(out, &u); err != nil {
		return "", fmt.Errorf("failed to parse user response: %w", err)
	}
	return u.Username, nil
}

func (g *gitLab) NextItems(owner, repo, user string, cooldown time.Duration, since time.Time, ignoreEvents MatchSet, ignoreUsers MatchSet, limit int, scope Scope, emit func(format.Item)) error {
	// Fetch issues and MRs in parallel
	var issues []glIssue
	var mrs []glMR
	var issErr, mrErr error
	var listWg sync.WaitGroup
	listWg.Add(2)
	if scope == ScopeOrg {
		groupPath := url.PathEscape(owner)
		go func() {
			defer listWg.Done()
			issues, issErr = g.listGroupIssues(groupPath, since)
		}()
		go func() {
			defer listWg.Done()
			mrs, mrErr = g.listGroupMRs(groupPath, since)
		}()
	} else {
		projectPath := url.PathEscape(owner + "/" + repo)
		go func() {
			defer listWg.Done()
			issues, issErr = g.listIssues(projectPath, since)
		}()
		go func() {
			defer listWg.Done()
			mrs, mrErr = g.listMRs(projectPath, since)
		}()
	}
	listWg.Wait()
	if issErr != nil {
		return issErr
	}
	if mrErr != nil {
		return mrErr
	}

	// Merge into unified list sorted by updated_at descending
	var items []glItem
	projectRef := url.PathEscape(owner + "/" + repo)
	for _, iss := range issues {
		ref := projectRef
		if scope == ScopeOrg {
			ref = fmt.Sprintf("%d", iss.ProjectID)
		}
		items = append(items, glItem{
			IID: iss.IID, Title: iss.Title, WebURL: iss.WebURL,
			CreatedAt: iss.CreatedAt, UpdatedAt: iss.UpdatedAt,
			Author: iss.Author.Username, Kind: "issues",
			ProjectRef: ref,
		})
	}
	for _, mr := range mrs {
		ref := projectRef
		if scope == ScopeOrg {
			ref = fmt.Sprintf("%d", mr.ProjectID)
		}
		items = append(items, glItem{
			IID: mr.IID, Title: mr.Title, WebURL: mr.WebURL,
			CreatedAt: mr.CreatedAt, UpdatedAt: mr.UpdatedAt,
			Author: mr.Author.Username, Kind: "merge_requests",
			ProjectRef: ref,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})

	cutoff := time.Now().Add(-cooldown)

	type candidate struct {
		item      format.Item
		tier      int
		updatedAt time.Time
	}
	var candidates []candidate

	for _, item := range items {
		notes, err := g.getNotes(item.ProjectRef, item.Kind, item.IID)
		if err != nil {
			return err
		}

		kind := "issue"
		label := fmt.Sprintf("#%d", item.IID)
		if item.Kind == "merge_requests" {
			kind = "MR"
			label = fmt.Sprintf("!%d", item.IID)
		}
		title := item.Title
		if r := []rune(title); len(r) > 50 {
			title = string(r[:50]) + "..."
		}

		userTouched := false
		for _, n := range notes {
			if ignoreUsers.Match(n.Author.Username) {
				continue
			}
			if n.System && !isApprovalNote(n.Body) {
				continue
			}
			if n.Author.Username == user && n.CreatedAt.After(cutoff) {
				userTouched = true
				break
			}
		}
		if userTouched {
			fmt.Fprintf(os.Stderr, "\033[2m  %s %s %s — skipped (cooldown)\033[0m\n", kind, label, title)
			continue
		}

		var lastUserTime time.Time
		for _, n := range notes {
			if ignoreUsers.Match(n.Author.Username) {
				continue
			}
			if n.System && !isApprovalNote(n.Body) {
				continue
			}
			if n.Author.Username == user && n.CreatedAt.After(lastUserTime) {
				lastUserTime = n.CreatedAt
			}
		}

		othersHaveActivity := false
		for _, n := range notes {
			if n.Author.Username == user || ignoreUsers.Match(n.Author.Username) {
				continue
			}
			if !n.System || isApprovalNote(n.Body) {
				if lastUserTime.IsZero() || n.CreatedAt.After(lastUserTime) {
					othersHaveActivity = true
					break
				}
			}
		}

		var fmtEvents []format.Event
		for _, n := range notes {
			if n.Author.Username == user || ignoreUsers.Match(n.Author.Username) {
				continue
			}
			if n.System && !isApprovalNote(n.Body) {
				continue
			}
			if !lastUserTime.IsZero() && n.CreatedAt.Before(lastUserTime) {
				continue
			}
			var summary string
			if isApprovalNote(n.Body) {
				summary = "approved"
			} else {
				body := n.Body
				if r := []rune(body); len(r) > 80 {
					body = string(r[:80])
				}
				summary = fmt.Sprintf("commented: > %s", body)
			}
			fmtEvents = append(fmtEvents, format.Event{
				Timestamp: n.CreatedAt,
				Author:    n.Author.Username,
				Summary:   summary,
			})
		}

		if len(fmtEvents) == 0 {
			if othersHaveActivity || !lastUserTime.IsZero() || item.Author == user {
				fmt.Fprintf(os.Stderr, "\033[2m  %s %s %s — skipped (no new activity)\033[0m\n", kind, label, title)
				continue
			}
			fmtEvents = append(fmtEvents, format.Event{
				Timestamp: item.CreatedAt,
				Author:    item.Author,
				Summary:   "opened",
			})
		}

		tier := 3
		if item.Author == user {
			tier = 1
		} else if !lastUserTime.IsZero() {
			tier = 2
		}

		candidates = append(candidates, candidate{
			item: format.Item{
				URL:    item.WebURL,
				Title:  item.Title,
				Events: fmtEvents,
				Tier:   tier,
			},
			tier:      tier,
			updatedAt: item.UpdatedAt,
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

func (g *gitLab) listIssues(projectPath string, since time.Time) ([]glIssue, error) {
	endpoint := fmt.Sprintf("projects/%s/issues?state=opened&order_by=updated_at&sort=desc&per_page=100", projectPath)
	if !since.IsZero() {
		endpoint += "&updated_after=" + since.Format(time.RFC3339)
	}
	out, err := g.run(g.cmd(), "api", endpoint, "--paginate")
	if err != nil {
		return nil, fmt.Errorf("failed to list GitLab issues: %w", err)
	}
	var issues []glIssue
	if err := json.Unmarshal(fixPaginatedJSON(out), &issues); err != nil {
		return nil, fmt.Errorf("failed to parse GitLab issues: %w", err)
	}
	return issues, nil
}

func (g *gitLab) listMRs(projectPath string, since time.Time) ([]glMR, error) {
	endpoint := fmt.Sprintf("projects/%s/merge_requests?state=opened&order_by=updated_at&sort=desc&per_page=100", projectPath)
	if !since.IsZero() {
		endpoint += "&updated_after=" + since.Format(time.RFC3339)
	}
	out, err := g.run(g.cmd(), "api", endpoint, "--paginate")
	if err != nil {
		return nil, fmt.Errorf("failed to list GitLab MRs: %w", err)
	}
	var mrs []glMR
	if err := json.Unmarshal(fixPaginatedJSON(out), &mrs); err != nil {
		return nil, fmt.Errorf("failed to parse GitLab MRs: %w", err)
	}
	return mrs, nil
}

func (g *gitLab) listGroupIssues(groupPath string, since time.Time) ([]glIssue, error) {
	endpoint := fmt.Sprintf("groups/%s/issues?state=opened&order_by=updated_at&sort=desc&per_page=100", groupPath)
	if !since.IsZero() {
		endpoint += "&updated_after=" + since.Format(time.RFC3339)
	}
	out, err := g.run(g.cmd(), "api", endpoint, "--paginate")
	if err != nil {
		return nil, fmt.Errorf("failed to list group issues: %w", err)
	}
	var issues []glIssue
	if err := json.Unmarshal(fixPaginatedJSON(out), &issues); err != nil {
		return nil, fmt.Errorf("failed to parse group issues: %w", err)
	}
	return issues, nil
}

func (g *gitLab) listGroupMRs(groupPath string, since time.Time) ([]glMR, error) {
	endpoint := fmt.Sprintf("groups/%s/merge_requests?state=opened&order_by=updated_at&sort=desc&per_page=100", groupPath)
	if !since.IsZero() {
		endpoint += "&updated_after=" + since.Format(time.RFC3339)
	}
	out, err := g.run(g.cmd(), "api", endpoint, "--paginate")
	if err != nil {
		return nil, fmt.Errorf("failed to list group MRs: %w", err)
	}
	var mrs []glMR
	if err := json.Unmarshal(fixPaginatedJSON(out), &mrs); err != nil {
		return nil, fmt.Errorf("failed to parse group MRs: %w", err)
	}
	return mrs, nil
}

func isApprovalNote(body string) bool {
	return strings.Contains(body, "approved this merge request")
}

func (g *gitLab) getNotes(projectPath, kind string, iid int) ([]glNote, error) {
	endpoint := fmt.Sprintf("projects/%s/%s/%d/notes", projectPath, kind, iid)
	out, err := g.run(g.cmd(), "api", endpoint, "--paginate")
	if err != nil {
		return nil, fmt.Errorf("failed to get notes for %s #%d: %w", kind, iid, err)
	}
	var notes []glNote
	if err := json.Unmarshal(fixPaginatedJSON(out), &notes); err != nil {
		return nil, fmt.Errorf("failed to parse notes: %w", err)
	}
	return notes, nil
}
