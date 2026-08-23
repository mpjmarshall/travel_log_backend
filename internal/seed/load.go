package seed

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// LoadOptions carries the two overrides on the refusal.
type LoadOptions struct {
	Force bool
	Reset bool
}

// Report is what a completed load answers with.
type Report struct {
	Rows     map[string]int
	Duration time.Duration
}

// Load inserts a Dataset into a migrated schema.
func Load(ctx context.Context, db *sql.DB, d *Dataset, o LoadOptions) (Report, error) {
	return Report{}, errors.New("seed: Load is not implemented")
}
