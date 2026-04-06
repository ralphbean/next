package backend

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rbean/next-up/format"
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
}

type glMR struct {
	IID       int          `json:"iid"`
	Title     string       `json:"title"`
	WebURL    string       `json:"web_url"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
	Author    glNoteAuthor `json:"author"`
}

type glUser struct {
	Username string `json:"username"`
}

// glItem is a unified type for sorting issues and MRs together.
type glItem struct {
	IID       int
	Title     string
	WebURL    string
	CreatedAt time.Time
	UpdatedAt time.Time
	Author    string
	Kind      string // "issues" or "merge_requests"
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

func (g *gitLab) NextItems(owner, repo, user string, since time.Duration, ignoreEvents MatchSet, ignoreUsers MatchSet, limit int) ([]format.Item, error) {
	projectPath := url.PathEscape(owner + "/" + repo)

	// Fetch issues and MRs in parallel
	var issues []glIssue
	var mrs []glMR
	var issErr, mrErr error
	var listWg sync.WaitGroup
	listWg.Add(2)
	go func() {
		defer listWg.Done()
		issues, issErr = g.listIssues(projectPath)
	}()
	go func() {
		defer listWg.Done()
		mrs, mrErr = g.listMRs(projectPath)
	}()
	listWg.Wait()
	if issErr != nil {
		return nil, issErr
	}
	if mrErr != nil {
		return nil, mrErr
	}

	// Merge into unified list sorted by updated_at descending
	var items []glItem
	for _, iss := range issues {
		items = append(items, glItem{
			IID: iss.IID, Title: iss.Title, WebURL: iss.WebURL,
			CreatedAt: iss.CreatedAt, UpdatedAt: iss.UpdatedAt,
			Author: iss.Author.Username, Kind: "issues",
		})
	}
	for _, mr := range mrs {
		items = append(items, glItem{
			IID: mr.IID, Title: mr.Title, WebURL: mr.WebURL,
			CreatedAt: mr.CreatedAt, UpdatedAt: mr.UpdatedAt,
			Author: mr.Author.Username, Kind: "merge_requests",
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})

	cutoff := time.Now().Add(-since)

	// Prefetch notes in parallel for all items
	type prefetch struct {
		notes []glNote
		err   error
	}
	fetched := make([]prefetch, len(items))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5)
	for i, item := range items {
		wg.Add(1)
		go func(i int, item glItem) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			notes, err := g.getNotes(projectPath, item.Kind, item.IID)
			fetched[i] = prefetch{notes: notes, err: err}
		}(i, item)
	}
	wg.Wait()

	var result []format.Item
	for i, item := range items {
		if fetched[i].err != nil {
			return nil, fetched[i].err
		}
		notes := fetched[i].notes

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
				othersHaveActivity = true
				break
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
			if othersHaveActivity || item.Author == user || ignoreUsers.Match(item.Author) {
				continue
			}
			fmtEvents = append(fmtEvents, format.Event{
				Timestamp: item.CreatedAt,
				Author:    item.Author,
				Summary:   "opened",
			})
		}

		result = append(result, format.Item{
			URL:    item.WebURL,
			Title:  item.Title,
			Events: fmtEvents,
		})
		if len(result) >= limit {
			break
		}
	}

	return result, nil
}

func (g *gitLab) listIssues(projectPath string) ([]glIssue, error) {
	endpoint := fmt.Sprintf("projects/%s/issues?state=opened&order_by=updated_at&sort=desc&per_page=30", projectPath)
	out, err := g.run(g.cmd(), "api", endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to list GitLab issues: %w", err)
	}
	var issues []glIssue
	if err := json.Unmarshal(out, &issues); err != nil {
		return nil, fmt.Errorf("failed to parse GitLab issues: %w", err)
	}
	return issues, nil
}

func (g *gitLab) listMRs(projectPath string) ([]glMR, error) {
	endpoint := fmt.Sprintf("projects/%s/merge_requests?state=opened&order_by=updated_at&sort=desc&per_page=30", projectPath)
	out, err := g.run(g.cmd(), "api", endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to list GitLab MRs: %w", err)
	}
	var mrs []glMR
	if err := json.Unmarshal(out, &mrs); err != nil {
		return nil, fmt.Errorf("failed to parse GitLab MRs: %w", err)
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
