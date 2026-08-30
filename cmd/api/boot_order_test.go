// The order boot checks things in, where getting it wrong migrates a schema
// the process is then not going to serve.
package main

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"travellog/internal/config"
)

// unreachableDB is a well-formed DSN nothing is listening on, so whichever
// check run reaches first is the one that reports.
const unreachableDB = "postgres://travellog:travellog@127.0.0.1:1/travellog?sslmode=disable"

func bootConfig() config.Config {
	return config.Config{
		DatabaseURL:              unreachableDB,
		Development:              false,
		DBMaxOpenConns:           1,
		DBMaxIdleConns:           1,
		RequestTimeout:           10 * time.Second,
		AuthRateLimitPerMin:      60,
		TravellerRateLimitPerMin: 60,
		PublicRateLimitPerMin:    60,
	}
}

func TestBootRefusesUndeliverableCodesBeforeItTouchesTheSchema(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	err := run(bootConfig(), ":0", log)
	if err == nil {
		t.Fatal("run() = nil error with no mail provider and DEVELOPMENT unset")
	}
	if !strings.Contains(err.Error(), "mail") {
		t.Errorf("run() failed for the wrong reason, so the mail check runs after\n"+
			"    the database work. Migrations drop passphrase_hash, which leaves a\n"+
			"    schema that only code sign-in can open and a process that refuses to\n"+
			"    start. The undeliverable-code check costs nothing and must come first.\n"+
			"    got: %v", err)
	}
}
