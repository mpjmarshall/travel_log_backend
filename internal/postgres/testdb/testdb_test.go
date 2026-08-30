// The two things testdb has to get right when there is no database, plus the
// version floor.
package testdb

import (
	"fmt"
	"strings"
	"testing"
)

// recorder is a TB that records instead of stopping.
type recorder struct {
	skips  []string
	fatals []string
}

func (r *recorder) Helper() {}
func (r *recorder) Skipf(format string, args ...any) {
	r.skips = append(r.skips, fmt.Sprintf(format, args...))
}
func (r *recorder) Fatalf(format string, args ...any) {
	r.fatals = append(r.fatals, fmt.Sprintf(format, args...))
}
func (r *recorder) Cleanup(func()) {}

func TestOpenFailsWhenThereIsNoDatabaseAndNobodySaidSo(t *testing.T) {
	t.Setenv(urlVar, "")
	t.Setenv(skipVar, "")
	r := &recorder{}
	if db, _ := Open(r); db != nil {
		t.Errorf("Open returned a *sql.DB with %s unset", urlVar)
	}
	if len(r.fatals) != 1 {
		t.Fatalf("Open recorded %d fatals with %s unset, want exactly 1 (skips: %v)",
			len(r.fatals), urlVar, r.skips)
	}
	if len(r.skips) != 0 {
		t.Errorf("Open skipped rather than failing: %v", r.skips)
	}
	for _, want := range []string{urlVar, "make test-db", skipVar} {
		if !strings.Contains(r.fatals[0], want) {
			t.Errorf("the failure %q does not name %q", r.fatals[0], want)
		}
	}
}

func TestTheOptOutReadsTheUsualSpellings(t *testing.T) {
	for _, yes := range []string{"1", "true", "TRUE", "yes", "y", "on", " true "} {
		if !optedOut(yes) {
			t.Errorf("optedOut(%q) = false, want true", yes)
		}
	}
	for _, no := range []string{"", "  ", "0", "false", "no", "maybe"} {
		if optedOut(no) {
			t.Errorf("optedOut(%q) = true, want false", no)
		}
	}
}

func TestOpenSkipsWhenSomebodyOptedOutInWriting(t *testing.T) {
	t.Setenv(urlVar, "")
	t.Setenv(skipVar, "1")
	r := &recorder{}
	if db, _ := Open(r); db != nil {
		t.Errorf("Open returned a *sql.DB with %s unset", urlVar)
	}
	if len(r.skips) != 1 {
		t.Fatalf("Open recorded %d skips under %s=1, want exactly 1 (fatals: %v)",
			len(r.skips), skipVar, r.fatals)
	}
	if len(r.fatals) != 0 {
		t.Errorf("Open failed despite the opt-out: %v", r.fatals)
	}
	if !strings.Contains(r.skips[0], "ON PURPOSE") {
		t.Errorf("the skip %q does not say it was deliberate", r.skips[0])
	}
}

func TestTheServerVersionFloorIsFifteen(t *testing.T) {
	cases := []struct {
		num  int
		want bool // true == refused
	}{
		{110000, true},
		{140012, true},
		{149999, true},
		{150000, false},
		{170011, false},
		{180000, false},
	}
	for _, c := range cases {
		err := checkServerVersion(c.num)
		if (err != nil) != c.want {
			t.Errorf("checkServerVersion(%d) error = %v, want refused=%v", c.num, err, c.want)
		}
	}
}

// Without these words the failure is "syntax error at or near (" in a file
// nobody is reading.
func TestTheVersionRefusalSaysWhatBreaksOnAnOlderServer(t *testing.T) {
	err := checkServerVersion(140012)
	if err == nil {
		t.Fatal("checkServerVersion(140012) = nil, want a refusal")
	}
	for _, want := range []string{"15", "ON DELETE SET NULL", "14.12"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not mention %q", err.Error(), want)
		}
	}
}
