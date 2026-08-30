// Removes bucket objects the database no longer references. Reports by
// default: deleting takes -delete, because the report is the safe thing to run.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"travellog/internal/media"
	"travellog/internal/postgres"
	"travellog/internal/sweep"
)

// minAge leaves recent objects alone. The row is written before the presigned
// PUT, so nothing in flight can look orphaned; this does not rely on that.
const minAge = 24 * time.Hour

const budget = 5 * time.Minute

func main() {
	dsn := flag.String("dsn", "", "the database to read, required")
	endpoint := flag.String("endpoint", "", "the S3 endpoint, required")
	bucket := flag.String("bucket", "", "the bucket to sweep, required")
	access := flag.String("access-key", "", "required")
	secret := flag.String("secret-key", "", "required")
	region := flag.String("region", "us-east-1", "")
	remove := flag.Bool("delete", false, "actually remove what it finds")
	flag.Parse()

	err := run(*dsn, media.Config{
		InternalEndpoint: *endpoint,
		PublicBaseURL:    *endpoint,
		Region:           *region,
		Bucket:           *bucket,
		AccessKey:        *access,
		SecretKey:        *secret,
		TTLPrivate:       time.Minute,
		TTLPublic:        time.Minute,
	}, *remove)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sweep:", err)
		os.Exit(1)
	}
}

func run(dsn string, cfg media.Config, remove bool) error {
	if dsn == "" || cfg.InternalEndpoint == "" || cfg.Bucket == "" {
		return fmt.Errorf("-dsn, -endpoint and -bucket are all required")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("opening the database: %w", err)
	}
	defer func() { _ = db.Close() }()

	objects, err := media.New(cfg)
	if err != nil {
		return fmt.Errorf("the media store: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()

	known, err := postgres.AdminStore{DB: db}.KnownObjects(ctx)
	if err != nil {
		return err
	}
	found, err := sweep.Plan(ctx, objects, known, time.Now(), minAge)
	if err != nil {
		return err
	}

	report(found, known, remove)
	if !remove || len(found.Orphans) == 0 {
		return nil
	}

	done, failed := sweep.Apply(ctx, objects, found.Orphans)
	fmt.Printf("removed %d of %d\n", done, len(found.Orphans))
	for _, err := range failed {
		fmt.Fprintln(os.Stderr, " ", err)
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d objects could not be removed", len(failed))
	}
	return nil
}

func report(found sweep.Found, known map[string]struct{}, remove bool) {
	fmt.Printf("%d objects in the bucket, %d referenced by the database\n",
		found.Seen, len(known))
	if found.Skipped > 0 {
		fmt.Printf("%d left alone for being newer than %s\n", found.Skipped, minAge)
	}
	if len(found.Orphans) == 0 {
		fmt.Println("no orphans")
		return
	}
	fmt.Printf("%d orphans, %d bytes:\n", len(found.Orphans), found.Bytes)
	for _, key := range found.Orphans {
		fmt.Printf("  %s/%s\n", key.Traveller, key.Object)
	}
	if !remove {
		fmt.Println("nothing was removed. Pass -delete to remove them.")
	}
}
