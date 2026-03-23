package duration

import (
	"fmt"
	"strings"
	"time"
)

// Parse parses a duration string like "30m", "1h", or "3d".
// It extends Go's time.ParseDuration with support for "d" (days = 24h).
func Parse(s string) (time.Duration, error) {
	if s == "" {
		return 0, fmt.Errorf("empty duration string")
	}
	if strings.HasPrefix(s, "-") {
		return 0, fmt.Errorf("negative durations not allowed: %q", s)
	}

	// Handle "d" suffix by converting to hours
	if strings.HasSuffix(s, "d") {
		prefix := s[:len(s)-1]
		var days int
		if _, err := fmt.Sscanf(prefix, "%d", &days); err != nil || fmt.Sprintf("%d", days) != prefix {
			return 0, fmt.Errorf("invalid duration: %q", s)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}

	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration: %q", s)
	}
	return d, nil
}
