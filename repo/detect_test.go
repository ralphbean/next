package repo

import (
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
