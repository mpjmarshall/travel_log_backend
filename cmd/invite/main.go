// Mints a single-use invite and prints it once. By hand, so you always know
// who let each person in.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"travellog/internal/auth"
	"travellog/internal/postgres"
)

func main() {
	dsn := flag.String("dsn", "", "the database to mint into, required")
	note := flag.String("note", "", "who this is for, for your own records")
	flag.Parse()

	if err := run(*dsn, *note); err != nil {
		fmt.Fprintln(os.Stderr, "invite:", err)
		os.Exit(1)
	}
}

func run(dsn, note string) error {
	if dsn == "" {
		return fmt.Errorf("-dsn is required and has no environment fallback")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("opening the database: %w", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("reaching the database: %w", err)
	}

	code, hash, err := auth.NewInvite()
	if err != nil {
		return err
	}
	if err := (postgres.AuthStore{DB: db}).MintInvite(ctx, hash, note); err != nil {
		return err
	}

	fmt.Printf("\n  %s\n\n  Single use. Nothing stores the plaintext, so this is the only time it is shown.\n", code)
	return nil
}
