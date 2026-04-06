package backend

import (
	"strings"
	"time"

	"github.com/rbean/next-up/format"
)

// CmdRunner executes a command and returns its output.
type CmdRunner func(name string, args ...string) ([]byte, error)

// MatchSet holds a list of patterns that support simple wildcards.
// Only * is treated as special (matches any sequence of characters).
// All other characters including [ ] are literal.
type MatchSet []string

// Match reports whether s matches any pattern in the set.
func (m MatchSet) Match(s string) bool {
	for _, p := range m {
		if matchGlob(p, s) {
			return true
		}
	}
	return false
}

func matchGlob(pattern, s string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == s
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	for i := 1; i < len(parts)-1; i++ {
		idx := strings.Index(s, parts[i])
		if idx < 0 {
			return false
		}
		s = s[idx+len(parts[i]):]
	}
	return strings.HasSuffix(s, parts[len(parts)-1])
}

// Backend fetches issues/PRs from a hosting platform.
type Backend interface {
	CurrentUser() (string, error)
	NextItems(owner, repo, user string, since time.Duration, ignoreEvents MatchSet, ignoreUsers MatchSet, limit int) ([]format.Item, error)
}
