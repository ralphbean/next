package repo

import (
	"fmt"
	"testing"
)

func TestParseRemoteURL(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		envHost  string
		want     Info
	}{
		{
			name: "github https",
			url:  "https://github.com/owner/repo.git",
			want: Info{Owner: "owner", Name: "repo", Host: "github.com", Platform: GitHub},
		},
		{
			name: "github https no .git",
			url:  "https://github.com/owner/repo",
			want: Info{Owner: "owner", Name: "repo", Host: "github.com", Platform: GitHub},
		},
		{
			name: "github ssh",
			url:  "git@github.com:owner/repo.git",
			want: Info{Owner: "owner", Name: "repo", Host: "github.com", Platform: GitHub},
		},
		{
			name: "gitlab https",
			url:  "https://gitlab.com/owner/repo.git",
			want: Info{Owner: "owner", Name: "repo", Host: "gitlab.com", Platform: GitLab},
		},
		{
			name: "gitlab ssh",
			url:  "git@gitlab.com:owner/repo.git",
			want: Info{Owner: "owner", Name: "repo", Host: "gitlab.com", Platform: GitLab},
		},
		{
			name:    "custom gitlab host via env",
			url:     "https://git.example.com/team/project.git",
			envHost: "git.example.com",
			want:    Info{Owner: "team", Name: "project", Host: "git.example.com", Platform: GitLab},
		},
		{
			name: "unknown host defaults to github",
			url:  "https://git.example.com/team/project.git",
			want: Info{Owner: "team", Name: "project", Host: "git.example.com", Platform: GitHub},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseRemoteURL(tt.url, tt.envHost)
			if err != nil {
				t.Fatalf("ParseRemoteURL(%q) error: %v", tt.url, err)
			}
			if got != tt.want {
				t.Errorf("ParseRemoteURL(%q) = %+v, want %+v", tt.url, got, tt.want)
			}
		})
	}
}

func TestDetect(t *testing.T) {
	tests := []struct {
		name       string
		remoteName string
		remoteURL  string
		gitlabHost string
		want       Info
	}{
		{
			name:       "origin remote",
			remoteName: "origin",
			remoteURL:  "git@github.com:owner/repo.git",
			want:       Info{Owner: "owner", Name: "repo", Host: "github.com", Platform: GitHub},
		},
		{
			name:       "upstream remote",
			remoteName: "upstream",
			remoteURL:  "https://github.com/org/project.git",
			want:       Info{Owner: "org", Name: "project", Host: "github.com", Platform: GitHub},
		},
		{
			name:       "custom remote name",
			remoteName: "myfork",
			remoteURL:  "git@gitlab.com:user/proj.git",
			want:       Info{Owner: "user", Name: "proj", Host: "gitlab.com", Platform: GitLab},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := func(name string, args ...string) ([]byte, error) {
				if name == "git" && len(args) >= 3 && args[0] == "remote" && args[1] == "get-url" && args[2] == tt.remoteName {
					return []byte(tt.remoteURL + "\n"), nil
				}
				return nil, fmt.Errorf("unexpected command: %s %v", name, args)
			}
			got, err := Detect(runner, tt.remoteName, tt.gitlabHost)
			if err != nil {
				t.Fatalf("Detect() error: %v", err)
			}
			if got != tt.want {
				t.Errorf("Detect() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestDetectError(t *testing.T) {
	runner := func(name string, args ...string) ([]byte, error) {
		return nil, fmt.Errorf("exit status 2")
	}
	_, err := Detect(runner, "nonexistent", "")
	if err == nil {
		t.Fatal("Detect() expected error for missing remote")
	}
}

func TestParseRemoteURLInvalid(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"empty", ""},
		{"no path", "https://github.com"},
		{"single segment", "https://github.com/owner"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseRemoteURL(tt.url, "")
			if err == nil {
				t.Errorf("ParseRemoteURL(%q) expected error", tt.url)
			}
		})
	}
}
