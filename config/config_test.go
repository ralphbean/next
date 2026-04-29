package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type mockPrompter struct {
	selection string
	err       error
}

func (m mockPrompter) SelectRemote(remotes []string, suggested string) (string, error) {
	return m.selection, m.err
}

func mockRunner(responses map[string]string) CmdRunner {
	return func(name string, args ...string) ([]byte, error) {
		key := name
		for _, a := range args {
			key += " " + a
		}
		if out, ok := responses[key]; ok {
			return []byte(out), nil
		}
		return nil, fmt.Errorf("command not found: %s", key)
	}
}

func TestConfigPath(t *testing.T) {
	t.Run("respects XDG_CONFIG_HOME", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "/custom/config")
		got, err := ConfigPath()
		if err != nil {
			t.Fatalf("ConfigPath() error: %v", err)
		}
		want := "/custom/config/next-up.json"
		if got != want {
			t.Errorf("ConfigPath() = %q, want %q", got, want)
		}
	})

	t.Run("defaults to home/.config", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "")
		got, err := ConfigPath()
		if err != nil {
			t.Fatalf("ConfigPath() error: %v", err)
		}
		home, _ := os.UserHomeDir()
		want := filepath.Join(home, ".config", "next-up.json")
		if got != want {
			t.Errorf("ConfigPath() = %q, want %q", got, want)
		}
	})
}

func TestLoadSave(t *testing.T) {
	t.Run("file not found returns empty config", func(t *testing.T) {
		cfg, err := Load(filepath.Join(t.TempDir(), "nonexistent.json"))
		if err != nil {
			t.Fatalf("Load() error: %v", err)
		}
		if len(cfg) != 0 {
			t.Errorf("Load() = %v, want empty", cfg)
		}
	})

	t.Run("round trip", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "subdir", "config.json")
		want := Config{"/home/user/repo": "upstream"}
		if err := Save(want, path); err != nil {
			t.Fatalf("Save() error: %v", err)
		}
		got, err := Load(path)
		if err != nil {
			t.Fatalf("Load() error: %v", err)
		}
		if got["/home/user/repo"] != "upstream" {
			t.Errorf("Load() = %v, want %v", got, want)
		}
	})

	t.Run("malformed JSON returns error", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "bad.json")
		os.WriteFile(path, []byte("{invalid"), 0644)
		_, err := Load(path)
		if err == nil {
			t.Fatal("Load() expected error for malformed JSON")
		}
	})
}

func TestGitRoot(t *testing.T) {
	run := mockRunner(map[string]string{
		"git rev-parse --show-toplevel": "/home/user/myrepo\n",
	})
	got, err := GitRoot(run)
	if err != nil {
		t.Fatalf("GitRoot() error: %v", err)
	}
	if got != "/home/user/myrepo" {
		t.Errorf("GitRoot() = %q, want %q", got, "/home/user/myrepo")
	}
}

func TestListRemotes(t *testing.T) {
	t.Run("multiple remotes", func(t *testing.T) {
		run := mockRunner(map[string]string{
			"git remote": "origin\nupstream\n",
		})
		got, err := ListRemotes(run)
		if err != nil {
			t.Fatalf("ListRemotes() error: %v", err)
		}
		if len(got) != 2 || got[0] != "origin" || got[1] != "upstream" {
			t.Errorf("ListRemotes() = %v, want [origin upstream]", got)
		}
	})

	t.Run("single remote", func(t *testing.T) {
		run := mockRunner(map[string]string{
			"git remote": "origin\n",
		})
		got, err := ListRemotes(run)
		if err != nil {
			t.Fatalf("ListRemotes() error: %v", err)
		}
		if len(got) != 1 || got[0] != "origin" {
			t.Errorf("ListRemotes() = %v, want [origin]", got)
		}
	})
}

func TestSuggestRemote(t *testing.T) {
	tests := []struct {
		name    string
		remotes []string
		want    string
	}{
		{"prefers upstream", []string{"origin", "upstream"}, "upstream"},
		{"falls back to origin", []string{"origin", "myfork"}, "origin"},
		{"falls back to first", []string{"myfork", "other"}, "myfork"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := suggestRemote(tt.remotes)
			if got != tt.want {
				t.Errorf("suggestRemote(%v) = %q, want %q", tt.remotes, got, tt.want)
			}
		})
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error: %v", err)
	}
	origStdout := os.Stdout
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = origStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	return buf.String()
}

func TestShowConfig(t *testing.T) {
	t.Run("empty config", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.json")
		run := mockRunner(map[string]string{})
		out := captureStdout(t, func() {
			ShowConfig(run, cfgPath, nil)
		})
		if out != "No remotes configured yet.\n" {
			t.Errorf("ShowConfig() output = %q, want empty config message", out)
		}
	})

	t.Run("marks current repo", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.json")
		Save(Config{
			"/home/user/repo":  "upstream",
			"/home/user/other": "origin",
		}, cfgPath)
		run := mockRunner(map[string]string{
			"git rev-parse --show-toplevel": "/home/user/repo\n",
		})
		out := captureStdout(t, func() {
			ShowConfig(run, cfgPath, nil)
		})
		if !bytes.Contains([]byte(out), []byte("* /home/user/repo")) {
			t.Errorf("ShowConfig() should mark current repo with *, got: %s", out)
		}
		if !bytes.Contains([]byte(out), []byte("  /home/user/other")) {
			t.Errorf("ShowConfig() should not mark other repos with *, got: %s", out)
		}
	})
}

func TestInteractiveConfig(t *testing.T) {
	t.Run("saves selection", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.json")
		run := mockRunner(map[string]string{
			"git rev-parse --show-toplevel": "/home/user/repo\n",
			"git remote":                   "origin\nupstream\n",
		})
		captureStdout(t, func() {
			err := InteractiveConfig(run, mockPrompter{selection: "upstream"}, cfgPath)
			if err != nil {
				t.Fatalf("InteractiveConfig() error: %v", err)
			}
		})
		cfg, _ := Load(cfgPath)
		if cfg["/home/user/repo"] != "upstream" {
			t.Errorf("InteractiveConfig() did not save selection: %v", cfg)
		}
	})

	t.Run("shows existing config", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.json")
		Save(Config{"/home/user/repo": "origin"}, cfgPath)
		run := mockRunner(map[string]string{
			"git rev-parse --show-toplevel": "/home/user/repo\n",
			"git remote":                   "origin\nupstream\n",
		})
		out := captureStdout(t, func() {
			InteractiveConfig(run, mockPrompter{selection: "upstream"}, cfgPath)
		})
		if !bytes.Contains([]byte(out), []byte("Current remote")) {
			t.Errorf("InteractiveConfig() should show current remote, got: %s", out)
		}
	})
}

func TestResolveRemote(t *testing.T) {
	t.Run("returns cached config", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.json")
		Save(Config{"/home/user/repo": "upstream"}, cfgPath)
		run := mockRunner(map[string]string{
			"git rev-parse --show-toplevel": "/home/user/repo\n",
		})
		got, err := ResolveRemote(run, mockPrompter{}, true, cfgPath)
		if err != nil {
			t.Fatalf("ResolveRemote() error: %v", err)
		}
		if got != "upstream" {
			t.Errorf("ResolveRemote() = %q, want %q", got, "upstream")
		}
	})

	t.Run("single remote auto-selects", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.json")
		run := mockRunner(map[string]string{
			"git rev-parse --show-toplevel": "/home/user/repo\n",
			"git remote":                   "origin\n",
		})
		got, err := ResolveRemote(run, mockPrompter{}, true, cfgPath)
		if err != nil {
			t.Fatalf("ResolveRemote() error: %v", err)
		}
		if got != "origin" {
			t.Errorf("ResolveRemote() = %q, want %q", got, "origin")
		}
		cfg, _ := Load(cfgPath)
		if cfg["/home/user/repo"] != "origin" {
			t.Errorf("config not saved: %v", cfg)
		}
	})

	t.Run("interactive prompts and saves", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.json")
		run := mockRunner(map[string]string{
			"git rev-parse --show-toplevel": "/home/user/repo\n",
			"git remote":                   "origin\nupstream\n",
		})
		got, err := ResolveRemote(run, mockPrompter{selection: "upstream"}, true, cfgPath)
		if err != nil {
			t.Fatalf("ResolveRemote() error: %v", err)
		}
		if got != "upstream" {
			t.Errorf("ResolveRemote() = %q, want %q", got, "upstream")
		}
		cfg, _ := Load(cfgPath)
		if cfg["/home/user/repo"] != "upstream" {
			t.Errorf("config not saved: %v", cfg)
		}
	})

	t.Run("non-interactive falls back without saving", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.json")
		run := mockRunner(map[string]string{
			"git rev-parse --show-toplevel": "/home/user/repo\n",
			"git remote":                   "origin\nupstream\n",
		})
		got, err := ResolveRemote(run, mockPrompter{}, false, cfgPath)
		if err != nil {
			t.Fatalf("ResolveRemote() error: %v", err)
		}
		if got != "upstream" {
			t.Errorf("ResolveRemote() = %q, want %q", got, "upstream")
		}
		cfg, _ := Load(cfgPath)
		if _, ok := cfg["/home/user/repo"]; ok {
			t.Error("config should not be saved in non-interactive mode")
		}
	})

	t.Run("non-interactive falls back to origin when no upstream", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.json")
		run := mockRunner(map[string]string{
			"git rev-parse --show-toplevel": "/home/user/repo\n",
			"git remote":                   "origin\nmyfork\n",
		})
		got, err := ResolveRemote(run, mockPrompter{}, false, cfgPath)
		if err != nil {
			t.Fatalf("ResolveRemote() error: %v", err)
		}
		if got != "origin" {
			t.Errorf("ResolveRemote() = %q, want %q", got, "origin")
		}
	})
}
