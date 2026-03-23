package backend

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"time"

	"github.com/rbean/next-up/format"
)

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
	IID       int       `json:"iid"`
	Title     string    `json:"title"`
	WebURL    string    `json:"web_url"`
	UpdatedAt time.Time `json:"updated_at"`
}

type glMR struct {
	IID       int       `json:"iid"`
	Title     string    `json:"title"`
	WebURL    string    `json:"web_url"`
	UpdatedAt time.Time `json:"updated_at"`
}

type glUser struct {
	Username string `json:"username"`
}

// glItem is a unified type for sorting issues and MRs together.
type glItem struct {
	IID       int
	Title     string
	WebURL    string
	UpdatedAt time.Time
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

func (g *gitLab) NextItem(owner, repo, user string, since time.Duration) (*format.Item, error) {
	projectPath := url.PathEscape(owner + "/" + repo)

	// Fetch issues and MRs
	issues, err := g.listIssues(projectPath)
	if err != nil {
		return nil, err
	}
	mrs, err := g.listMRs(projectPath)
	if err != nil {
		return nil, err
	}

	// Merge into unified list sorted by updated_at descending
	var items []glItem
	for _, iss := range issues {
		items = append(items, glItem{
			IID: iss.IID, Title: iss.Title, WebURL: iss.WebURL,
			UpdatedAt: iss.UpdatedAt, Kind: "issues",
		})
	}
	for _, mr := range mrs {
		items = append(items, glItem{
			IID: mr.IID, Title: mr.Title, WebURL: mr.WebURL,
			UpdatedAt: mr.UpdatedAt, Kind: "merge_requests",
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})

	cutoff := time.Now().Add(-since)

	for _, item := range items {
		notes, err := g.getNotes(projectPath, item.Kind, item.IID)
		if err != nil {
			return nil, err
		}

		userTouched := false
		for _, n := range notes {
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
			if n.Author.Username == user && n.CreatedAt.After(lastUserTime) {
				lastUserTime = n.CreatedAt
			}
		}

		var fmtEvents []format.Event
		for _, n := range notes {
			if n.Author.Username == user || n.System {
				continue
			}
			if !lastUserTime.IsZero() && n.CreatedAt.Before(lastUserTime) {
				continue
			}
			body := n.Body
			if len(body) > 80 {
				body = body[:80]
			}
			fmtEvents = append(fmtEvents, format.Event{
				Timestamp: n.CreatedAt,
				Author:    n.Author.Username,
				Summary:   fmt.Sprintf("commented: > %s", body),
			})
		}

		return &format.Item{
			URL:    item.WebURL,
			Title:  item.Title,
			Events: fmtEvents,
		}, nil
	}

	return nil, nil
}

func (g *gitLab) listIssues(projectPath string) ([]glIssue, error) {
	endpoint := fmt.Sprintf("projects/%s/issues", projectPath)
	out, err := g.run(g.cmd(), "api", endpoint, "--paginate",
		"-f", "state=opened",
		"-f", "order_by=updated_at",
		"-f", "sort=desc",
		"-f", "per_page=30",
	)
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
	endpoint := fmt.Sprintf("projects/%s/merge_requests", projectPath)
	out, err := g.run(g.cmd(), "api", endpoint, "--paginate",
		"-f", "state=opened",
		"-f", "order_by=updated_at",
		"-f", "sort=desc",
		"-f", "per_page=30",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list GitLab MRs: %w", err)
	}
	var mrs []glMR
	if err := json.Unmarshal(out, &mrs); err != nil {
		return nil, fmt.Errorf("failed to parse GitLab MRs: %w", err)
	}
	return mrs, nil
}

func (g *gitLab) getNotes(projectPath, kind string, iid int) ([]glNote, error) {
	endpoint := fmt.Sprintf("projects/%s/%s/%d/notes", projectPath, kind, iid)
	out, err := g.run(g.cmd(), "api", endpoint, "--paginate")
	if err != nil {
		return nil, fmt.Errorf("failed to get notes for %s #%d: %w", kind, iid, err)
	}
	var notes []glNote
	if err := json.Unmarshal(out, &notes); err != nil {
		return nil, fmt.Errorf("failed to parse notes: %w", err)
	}
	return notes, nil
}
