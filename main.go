package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/rbean/next-up/backend"
	"github.com/rbean/next-up/duration"
	"github.com/rbean/next-up/format"
	"github.com/rbean/next-up/repo"
	"golang.org/x/term"
)

func parseSince(s string) (time.Duration, error) {
	return duration.Parse(s)
}

func parsePatterns(s string) backend.MatchSet {
	if s == "" {
		return nil
	}
	var patterns backend.MatchSet
	for _, e := range strings.Split(s, ",") {
		e = strings.TrimSpace(e)
		if e != "" {
			patterns = append(patterns, e)
		}
	}
	return patterns
}

func getTerminalWidth() int {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w < 40 {
		return 120
	}
	return w
}

func defaultRunner(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

func run() error {
	sinceStr := flag.String("since", "30m", "cooldown before showing items you recently touched (e.g., 30m, 1h, 3d)")
	ignoreStr := flag.String("ignore-events", "labeled,unlabeled,mentioned,subscribed,assigned,cross-referenced,project_v2_item_status_changed", "comma-separated list of event patterns to ignore (supports * wildcards)")
	ignoreUsersStr := flag.String("ignore-users", "*[bot]", "comma-separated list of user patterns to ignore (supports * wildcards)")
	limit := flag.Int("limit", 1, "maximum number of items to show")
	flag.Parse()

	since, err := parseSince(*sinceStr)
	if err != nil {
		return fmt.Errorf("invalid --since value: %w", err)
	}

	if *limit < 1 {
		return fmt.Errorf("invalid --limit value: must be at least 1")
	}

	gitlabHost := os.Getenv("GITLAB_HOST")

	info, err := repo.Detect(gitlabHost)
	if err != nil {
		return fmt.Errorf("error: %w\nAre you in a git repository with a remote 'origin'?", err)
	}

	var b backend.Backend
	switch info.Platform {
	case repo.GitHub:
		b = backend.NewGitHub(defaultRunner)
	case repo.GitLab:
		b = backend.NewGitLab(defaultRunner, gitlabHost)
	}

	user, err := b.CurrentUser()
	if err != nil {
		return fmt.Errorf("failed to determine current user: %w", err)
	}

	ignore := parsePatterns(*ignoreStr)
	ignoreUsers := parsePatterns(*ignoreUsersStr)
	items, err := b.NextItems(info.Owner, info.Name, user, since, ignore, ignoreUsers, *limit)
	if err != nil {
		return err
	}

	if len(items) == 0 {
		fmt.Println("Nothing to do! All items were recently touched by you.")
		return nil
	}

	width := getTerminalWidth()
	fmt.Print(format.FormatItems(items, width))

	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
