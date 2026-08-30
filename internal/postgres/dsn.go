// The session settings the application's own connections carry, and the one
// place a DSN is rewritten.
package postgres

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// IdleInTransactionTimeout bounds a transaction nobody is advancing.
const IdleInTransactionTimeout = 60 * time.Second

// WithSessionOptions returns dsn with the application's session settings on
// its `options=` parameter, preserving anything already there.
func WithSessionOptions(dsn string, statementTimeout time.Duration) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("postgres: DATABASE_URL is not a URL: %w", err)
	}

	q := u.Query()
	existing := strings.TrimSpace(q.Get("options"))

	wanted := [][2]string{
		{"search_path", "public"},
		{"statement_timeout", durationOption(statementTimeout)},
		{"idle_in_transaction_session_timeout", durationOption(IdleInTransactionTimeout)},
	}

	parts := []string{}
	if existing != "" {
		parts = append(parts, existing)
	}
	for _, setting := range wanted {
		if strings.Contains(existing, setting[0]+"=") {
			continue
		}
		parts = append(parts, "-c "+setting[0]+"="+setting[1])
	}

	q.Set("options", strings.Join(parts, " "))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// durationOption renders a duration the way libpq reads one.
func durationOption(d time.Duration) string {
	return fmt.Sprintf("%dms", d.Milliseconds())
}
