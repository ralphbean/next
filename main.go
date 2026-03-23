package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"

	"github.com/rbean/next-up/backend"
	"github.com/rbean/next-up/duration"
	"github.com/rbean/next-up/format"
	"github.com/rbean/next-up/repo"
	"golang.org/x/term"
	"time"
)

func parseSince(s string) (time.Duration, error) {
	return duration.Parse(s)
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
	flag.Parse()

	since, err := parseSince(*sinceStr)
	if err != nil {
		return fmt.Errorf("invalid --since value: %w", err)
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

	item, err := b.NextItem(info.Owner, info.Name, user, since)
	if err != nil {
		return err
	}

	if item == nil {
		fmt.Println("Nothing to do! All items were recently touched by you.")
		return nil
	}

	width := getTerminalWidth()
	fmt.Print(format.FormatItem(*item, width))

	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
