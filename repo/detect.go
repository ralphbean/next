package repo

import (
	"fmt"
	"strings"
)

type Platform string

const (
	GitHub Platform = "github"
	GitLab Platform = "gitlab"
)

type Info struct {
	Owner    string
	Name     string
	Host     string
	Platform Platform
}

// ParseRemoteURL parses a git remote URL into repo info.
// gitlabHost is the value of GITLAB_HOST env var (may be empty).
func ParseRemoteURL(rawURL, gitlabHost string) (Info, error) {
	if rawURL == "" {
		return Info{}, fmt.Errorf("empty remote URL")
	}

	var host, path string

	if strings.Contains(rawURL, "://") {
		// HTTPS: https://github.com/owner/repo.git
		parts := strings.SplitN(rawURL, "://", 2)
		rest := parts[1]
		idx := strings.Index(rest, "/")
		if idx < 0 {
			return Info{}, fmt.Errorf("cannot parse remote URL: %q", rawURL)
		}
		host = rest[:idx]
		path = rest[idx+1:]
	} else if strings.Contains(rawURL, ":") {
		// SSH: git@github.com:owner/repo.git
		parts := strings.SplitN(rawURL, ":", 2)
		hostPart := parts[0]
		if at := strings.Index(hostPart, "@"); at >= 0 {
			host = hostPart[at+1:]
		} else {
			host = hostPart
		}
		path = parts[1]
	} else {
		return Info{}, fmt.Errorf("cannot parse remote URL: %q", rawURL)
	}

	path = strings.TrimSuffix(path, ".git")
	segments := strings.Split(path, "/")
	if len(segments) < 2 {
		return Info{}, fmt.Errorf("cannot determine owner/repo from URL: %q", rawURL)
	}

	owner := segments[0]
	name := segments[1]

	platform := detectPlatform(host, gitlabHost)

	return Info{
		Owner:    owner,
		Name:     name,
		Host:     host,
		Platform: platform,
	}, nil
}

func detectPlatform(host, gitlabHost string) Platform {
	if strings.Contains(host, "gitlab") {
		return GitLab
	}
	if gitlabHost != "" && host == gitlabHost {
		return GitLab
	}
	return GitHub
}

// Detect reads the URL for the named git remote and returns parsed repo info.
func Detect(run func(string, ...string) ([]byte, error), remoteName, gitlabHost string) (Info, error) {
	out, err := run("git", "remote", "get-url", remoteName)
	if err != nil {
		return Info{}, fmt.Errorf("no remote %q configured: %w", remoteName, err)
	}
	url := strings.TrimSpace(string(out))
	return ParseRemoteURL(url, gitlabHost)
}
