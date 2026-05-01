package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/ralphbean/next/backend"
	"github.com/ralphbean/next/config"
	"github.com/ralphbean/next/duration"
	"github.com/ralphbean/next/format"
	"github.com/ralphbean/next/repo"
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
	cmd := exec.Command(name, args...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return out, fmt.Errorf("%s: %s", err, ee.Stderr)
		}
	}
	return out, err
}

func openBrowser(url string) error {
	var cmd string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "windows":
		cmd = "start"
	default:
		cmd = "xdg-open"
	}
	return exec.Command(cmd, url).Start()
}

func run() error {
	sinceStr := flag.String("since", "30m", "cooldown before showing items you recently touched (e.g., 30m, 1h, 3d)")
	ignoreStr := flag.String("ignore-events", "labeled,unlabeled,mentioned,subscribed,assigned,unassigned,referenced,cross-referenced,head_ref_force_pushed,convert_to_draft,renamed,project_v2_item_status_changed,added_to_project_v2", "comma-separated list of event patterns to ignore (supports * wildcards)")
	ignoreUsersStr := flag.String("ignore-users", "*[bot]", "comma-separated list of user patterns to ignore (supports * wildcards)")
	limit := flag.Int("limit", 1, "maximum number of items to show")
	scopeStr := flag.String("scope", "repo", "scope to search: repo (current repo) or org (all repos in the org)")
	autoOpen := flag.Bool("auto-open", false, "automatically open each result in the browser")
	showConfig := flag.Bool("show-config", false, "show configured remotes for all repos")
	configFlag := flag.Bool("config", false, "show config and available remotes, then choose which remote to track")
	flag.Parse()

	cfgPath, err := config.ConfigPath()
	if err != nil {
		return err
	}

	getURL := func(remote string) string {
		out, err := defaultRunner("git", "remote", "get-url", remote)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}

	if *showConfig {
		return config.ShowConfig(defaultRunner, cfgPath, getURL)
	}

	prompter := config.TTYPrompter{GetURL: getURL}

	if *configFlag {
		return config.InteractiveConfig(defaultRunner, prompter, cfgPath)
	}

	since, err := parseSince(*sinceStr)
	if err != nil {
		return fmt.Errorf("invalid --since value: %w", err)
	}

	if *limit < 1 {
		return fmt.Errorf("invalid --limit value: must be at least 1")
	}

	scope := backend.Scope(*scopeStr)
	if scope != backend.ScopeRepo && scope != backend.ScopeOrg {
		return fmt.Errorf("invalid --scope value: must be 'repo' or 'org'")
	}

	gitlabHost := os.Getenv("GITLAB_HOST")

	interactive := term.IsTerminal(int(os.Stdin.Fd()))
	remoteName, err := config.ResolveRemote(defaultRunner, prompter, interactive, cfgPath)
	if err != nil {
		return fmt.Errorf("error resolving remote: %w", err)
	}

	info, err := repo.Detect(defaultRunner, remoteName, gitlabHost)
	if err != nil {
		return fmt.Errorf("error: %w\nAre you in a git repository with a remote %q?", err, remoteName)
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

	width := getTerminalWidth()
	var urls []string
	emitted := 0
	sepWidth := width
	if sepWidth > 40 {
		sepWidth = 40
	}
	separator := strings.Repeat("─", sepWidth)

	emit := func(item format.Item) {
		emitted++
		if *autoOpen {
			urls = append(urls, item.URL)
		}
		if *limit == 1 {
			fmt.Printf("\033[1m%s\033[0m", format.FormatItem(item, width))
			return
		}
		if emitted > 1 {
			fmt.Printf("  %s\n", separator)
		}
		fmt.Printf("\033[1m▶ %s\n", item.URL)
		fmt.Printf("  %s\n", item.Title)
		for _, e := range item.Events {
			fmt.Printf("    %s\n", format.FormatEvent(e, width-4))
		}
		fmt.Print("\033[0m")
	}

	err = b.NextItems(info.Owner, info.Name, user, since, ignore, ignoreUsers, *limit, scope, emit)
	if err != nil {
		return err
	}

	if emitted == 0 {
		fmt.Println("Nothing to do! All items were recently touched by you.")
	}

	for _, u := range urls {
		if err := openBrowser(u); err != nil {
			fmt.Fprintf(os.Stderr, "failed to open %s: %v\n", u, err)
		}
	}

	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
