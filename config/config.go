package config

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config map[string]string

type CmdRunner func(name string, args ...string) ([]byte, error)

type Prompter interface {
	SelectRemote(remotes []string, suggested string) (string, error)
}

type TTYPrompter struct {
	GetURL func(remote string) string
}

func (p TTYPrompter) SelectRemote(remotes []string, suggested string) (string, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return "", fmt.Errorf("cannot open terminal for input: %w", err)
	}
	defer tty.Close()

	fmt.Fprintf(os.Stderr, "Multiple git remotes found. Which remote points to the main repository?\n")
	for i, r := range remotes {
		url := ""
		if p.GetURL != nil {
			url = p.GetURL(r)
		}
		marker := "  "
		if r == suggested {
			marker = "* "
		}
		if url != "" {
			fmt.Fprintf(os.Stderr, "  %s%d) %s\t(%s)\n", marker, i+1, r, url)
		} else {
			fmt.Fprintf(os.Stderr, "  %s%d) %s\n", marker, i+1, r)
		}
	}
	fmt.Fprintf(os.Stderr, "Select remote [%s]: ", suggested)

	scanner := bufio.NewScanner(tty)
	if !scanner.Scan() {
		return "", fmt.Errorf("no input received")
	}
	input := strings.TrimSpace(scanner.Text())

	if input == "" {
		return suggested, nil
	}

	var idx int
	if _, err := fmt.Sscanf(input, "%d", &idx); err == nil {
		if idx >= 1 && idx <= len(remotes) {
			return remotes[idx-1], nil
		}
		return "", fmt.Errorf("invalid selection: %d (must be 1-%d)", idx, len(remotes))
	}

	for _, r := range remotes {
		if r == input {
			return input, nil
		}
	}
	return "", fmt.Errorf("unknown remote: %q", input)
}

func ConfigPath() (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot determine home directory: %w", err)
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "next-up.json"), nil
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, nil
		}
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid config file %s: %w", path, err)
	}
	return cfg, nil
}

func Save(cfg Config, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}

func GitRoot(run CmdRunner) (string, error) {
	out, err := run("git", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("not in a git repository: %w", err)
	}
	return filepath.Clean(strings.TrimSpace(string(out))), nil
}

func ListRemotes(run CmdRunner) ([]string, error) {
	out, err := run("git", "remote")
	if err != nil {
		return nil, fmt.Errorf("failed to list git remotes: %w", err)
	}
	var remotes []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			remotes = append(remotes, line)
		}
	}
	if len(remotes) == 0 {
		return nil, fmt.Errorf("no git remotes configured")
	}
	return remotes, nil
}

func suggestRemote(remotes []string) string {
	for _, r := range remotes {
		if r == "upstream" {
			return "upstream"
		}
	}
	for _, r := range remotes {
		if r == "origin" {
			return "origin"
		}
	}
	return remotes[0]
}

func ShowConfig(run CmdRunner, cfgPath string, getURL func(string) string) error {
	cfg, err := Load(cfgPath)
	if err != nil {
		return err
	}
	if len(cfg) == 0 {
		fmt.Println("No remotes configured yet.")
		return nil
	}

	var currentRoot string
	root, err := GitRoot(run)
	if err == nil {
		currentRoot = root
	}

	for path, remote := range cfg {
		marker := "  "
		if path == currentRoot {
			marker = "* "
		}
		url := ""
		if path == currentRoot && getURL != nil {
			url = getURL(remote)
		}
		if url != "" {
			fmt.Printf("%s%s → %s (%s)\n", marker, path, remote, url)
		} else {
			fmt.Printf("%s%s → %s\n", marker, path, remote)
		}
	}
	return nil
}

func InteractiveConfig(run CmdRunner, prompter Prompter, cfgPath string) error {
	root, err := GitRoot(run)
	if err != nil {
		return err
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		return err
	}

	if current, ok := cfg[root]; ok {
		fmt.Printf("Current remote for this repo: %s\n", current)
	} else {
		fmt.Println("No remote configured for this repo yet.")
	}

	remotes, err := ListRemotes(run)
	if err != nil {
		return err
	}

	suggested := suggestRemote(remotes)
	if current, ok := cfg[root]; ok {
		suggested = current
	}

	selected, err := prompter.SelectRemote(remotes, suggested)
	if err != nil {
		return err
	}

	cfg[root] = selected
	if err := Save(cfg, cfgPath); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	fmt.Printf("Remote set to %q for %s\n", selected, root)
	return nil
}

func ResolveRemote(run CmdRunner, prompter Prompter, interactive bool, cfgPath string) (string, error) {
	root, err := GitRoot(run)
	if err != nil {
		return "", err
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		return "", err
	}
	if remote, ok := cfg[root]; ok {
		return remote, nil
	}

	remotes, err := ListRemotes(run)
	if err != nil {
		return "", err
	}

	suggested := suggestRemote(remotes)

	if len(remotes) == 1 {
		cfg[root] = remotes[0]
		if err := Save(cfg, cfgPath); err != nil {
			return "", fmt.Errorf("failed to save config: %w", err)
		}
		return remotes[0], nil
	}

	if !interactive {
		return suggested, nil
	}

	selected, err := prompter.SelectRemote(remotes, suggested)
	if err != nil {
		return "", err
	}

	cfg, err = Load(cfgPath)
	if err != nil {
		return "", err
	}
	cfg[root] = selected
	if err := Save(cfg, cfgPath); err != nil {
		return "", fmt.Errorf("failed to save config: %w", err)
	}
	return selected, nil
}