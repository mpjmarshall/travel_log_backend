// test-first (agent-graph-spec-V4 §6.7).
package logging_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"travellog/internal/logging"
)

// theSecret is a value distinctive enough that finding it anywhere in the raw
// output is proof of a leak.
const theSecret = "s3kr1t-QZmVsaXg-do-not-log-me"

func logged(t *testing.T, level slog.Level, emit func(*slog.Logger)) (raw string, fields map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	emit(logging.New(&buf, level))

	raw = buf.String()
	if raw == "" {
		return "", nil
	}
	line := strings.TrimSpace(strings.Split(raw, "\n")[0])
	if err := json.Unmarshal([]byte(line), &fields); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, raw)
	}
	return raw, fields
}

func TestNewWritesOneJSONObjectPerRecord(t *testing.T) {
	raw, fields := logged(t, slog.LevelInfo, func(l *slog.Logger) {
		l.Info("serving", slog.String("addr", ":8080"))
	})

	for _, key := range []string{"time", "level", "msg"} {
		if _, ok := fields[key]; !ok {
			t.Errorf("record has no %q key: %s", key, raw)
		}
	}
	if fields["msg"] != "serving" {
		t.Errorf("msg = %v, want %q", fields["msg"], "serving")
	}
	if fields["addr"] != ":8080" {
		t.Errorf("addr = %v, want %q", fields["addr"], ":8080")
	}
	if fields["level"] != "INFO" {
		t.Errorf("level = %v, want INFO", fields["level"])
	}
}

func TestNewHonoursTheLevel(t *testing.T) {
	if raw, _ := logged(t, slog.LevelInfo, func(l *slog.Logger) {
		l.Debug("chatter")
	}); raw != "" {
		t.Errorf("a Debug record was written at LevelInfo: %s", raw)
	}

	if raw, _ := logged(t, slog.LevelDebug, func(l *slog.Logger) {
		l.Debug("chatter")
	}); raw == "" {
		t.Error("no Debug record was written at LevelDebug")
	}
}

func TestRedactsTheThreeNamedKeys(t *testing.T) {
	for _, key := range []string{"token", "passphrase", "authorization"} {
		t.Run(key, func(t *testing.T) {
			raw, fields := logged(t, slog.LevelInfo, func(l *slog.Logger) {
				l.Info("auth", slog.String(key, theSecret))
			})

			if strings.Contains(raw, theSecret) {
				t.Errorf("the secret reached the output: %s", raw)
			}
			if fields[key] != logging.Redacted {
				t.Errorf("%s = %v, want %q", key, fields[key], logging.Redacted)
			}
		})
	}
}

// A header is "Authorization" and a struct field marshals as "Token".
func TestRedactionIsCaseInsensitive(t *testing.T) {
	for _, key := range []string{"Authorization", "Token", "PASSPHRASE", "AuthoriZation"} {
		raw, fields := logged(t, slog.LevelInfo, func(l *slog.Logger) {
			l.Info("auth", slog.String(key, theSecret))
		})
		if strings.Contains(raw, theSecret) {
			t.Errorf("%s: the secret reached the output: %s", key, raw)
		}
		if fields[key] != logging.Redacted {
			t.Errorf("%s = %v, want %q", key, fields[key], logging.Redacted)
		}
	}
}

// The match is on a substring of the key, not on equality, and this is the
// leg that says so.
func TestRedactsKeysThatCONTAINAName(t *testing.T) {
	for _, key := range []string{"access_token", "sessionToken", "authorization_header", "user_passphrase"} {
		raw, fields := logged(t, slog.LevelInfo, func(l *slog.Logger) {
			l.Info("auth", slog.String(key, theSecret))
		})
		if strings.Contains(raw, theSecret) {
			t.Errorf("%s: the secret reached the output: %s", key, raw)
		}
		if fields[key] != logging.Redacted {
			t.Errorf("%s = %v, want %q", key, fields[key], logging.Redacted)
		}
	}
}

// The boundary, asserted rather than assumed.
func TestLeavesOrdinaryKeysAlone(t *testing.T) {
	_, fields := logged(t, slog.LevelInfo, func(l *slog.Logger) {
		l.Info("request",
			slog.String("email", "traveller@example.com"),
			slog.String("path", "/v1/logbook"),
			slog.Int("status", 200),
		)
	})

	if fields["email"] != "traveller@example.com" {
		t.Errorf("email = %v, want it untouched", fields["email"])
	}
	if fields["path"] != "/v1/logbook" {
		t.Errorf("path = %v, want it untouched", fields["path"])
	}
	if fields["status"] != float64(200) {
		t.Errorf("status = %v, want 200", fields["status"])
	}
}

// slog's own keys must survive.
func TestLeavesSlogsOwnKeysAlone(t *testing.T) {
	_, fields := logged(t, slog.LevelWarn, func(l *slog.Logger) {
		l.Warn("the database ping failed")
	})

	if fields["msg"] != "the database ping failed" {
		t.Errorf("msg = %v, want the message", fields["msg"])
	}
	if fields["level"] != "WARN" {
		t.Errorf("level = %v, want WARN", fields["level"])
	}
	if fields["time"] == logging.Redacted {
		t.Error("time was redacted")
	}
}

func TestRedactsInsideAGroup(t *testing.T) {
	raw, fields := logged(t, slog.LevelInfo, func(l *slog.Logger) {
		l.Info("request", slog.Group("headers", slog.String("authorization", "Bearer "+theSecret)))
	})

	if strings.Contains(raw, theSecret) {
		t.Errorf("the secret reached the output from inside a group: %s", raw)
	}
	headers, ok := fields["headers"].(map[string]any)
	if !ok {
		t.Fatalf("headers is not an object: %v", fields["headers"])
	}
	if headers["authorization"] != logging.Redacted {
		t.Errorf("headers.authorization = %v, want %q", headers["authorization"], logging.Redacted)
	}
}

func TestRedactsAnAttributeAddedByWith(t *testing.T) {
	raw, fields := logged(t, slog.LevelInfo, func(l *slog.Logger) {
		l.With(slog.String("token", theSecret)).Info("authenticated")
	})

	if strings.Contains(raw, theSecret) {
		t.Errorf("the secret reached the output through With: %s", raw)
	}
	if fields["token"] != logging.Redacted {
		t.Errorf("token = %v, want %q", fields["token"], logging.Redacted)
	}
}

// The value need not be a string.
func TestRedactsRegardlessOfTheValueType(t *testing.T) {
	type credential struct {
		Raw   string `json:"raw"`
		Bytes []byte `json:"bytes"`
	}

	raw, fields := logged(t, slog.LevelInfo, func(l *slog.Logger) {
		l.Info("auth", slog.Any("token", credential{Raw: theSecret, Bytes: []byte(theSecret)}))
	})

	if strings.Contains(raw, theSecret) {
		t.Errorf("the secret reached the output inside a struct value: %s", raw)
	}
	if fields["token"] != logging.Redacted {
		t.Errorf("token = %v, want %q", fields["token"], logging.Redacted)
	}
}
