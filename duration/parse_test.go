package duration

import (
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		{"30m", 30 * time.Minute},
		{"1h", 1 * time.Hour},
		{"2h30m", 2*time.Hour + 30*time.Minute},
		{"1d", 24 * time.Hour},
		{"3d", 72 * time.Hour},
		{"7d", 168 * time.Hour},
		{"500ms", 500 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := Parse(tt.input)
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("Parse(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseInvalid(t *testing.T) {
	tests := []string{
		"",
		"abc",
		"3x",
		"-1d",
	}
	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			_, err := Parse(input)
			if err == nil {
				t.Errorf("Parse(%q) expected error, got nil", input)
			}
		})
	}
}
