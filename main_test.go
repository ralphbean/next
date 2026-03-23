package main

import (
	"testing"
	"time"
)

func TestParseSinceFlag(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		{"30m", 30 * time.Minute},
		{"1h", 1 * time.Hour},
		{"3d", 72 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseSince(tt.input)
			if err != nil {
				t.Fatalf("parseSince(%q) error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("parseSince(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseSinceFlagInvalid(t *testing.T) {
	_, err := parseSince("banana")
	if err == nil {
		t.Error("expected error for invalid duration")
	}
}

func TestParseIgnoreEvents(t *testing.T) {
	tests := []struct {
		input string
		want  map[string]bool
	}{
		{"mentioned,subscribed", map[string]bool{"mentioned": true, "subscribed": true}},
		{"", map[string]bool{}},
		{"mentioned", map[string]bool{"mentioned": true}},
		{" mentioned , subscribed ", map[string]bool{"mentioned": true, "subscribed": true}},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseIgnoreEvents(tt.input)
			if len(got) != len(tt.want) {
				t.Errorf("parseIgnoreEvents(%q) = %v, want %v", tt.input, got, tt.want)
				return
			}
			for k := range tt.want {
				if !got[k] {
					t.Errorf("parseIgnoreEvents(%q) missing key %q", tt.input, k)
				}
			}
		})
	}
}

func TestTerminalWidth(t *testing.T) {
	w := getTerminalWidth()
	if w < 40 {
		t.Errorf("terminal width too small: %d", w)
	}
}
