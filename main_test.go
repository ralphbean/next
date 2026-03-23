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

func TestTerminalWidth(t *testing.T) {
	w := getTerminalWidth()
	if w < 40 {
		t.Errorf("terminal width too small: %d", w)
	}
}
