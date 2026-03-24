package backend

import (
	"time"

	"github.com/rbean/next-up/format"
)

// CmdRunner executes a command and returns its output.
type CmdRunner func(name string, args ...string) ([]byte, error)

// Backend fetches issues/PRs from a hosting platform.
type Backend interface {
	CurrentUser() (string, error)
	NextItems(owner, repo, user string, since time.Duration, ignoreEvents map[string]bool, ignoreUsers map[string]bool, limit int) ([]format.Item, error)
}
