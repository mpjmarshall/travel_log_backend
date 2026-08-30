// The Service layer's shape, which is the thing is about.
package logbook_test

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"travellog/internal/logbook"
)

// The service is three operations, built in two stages.
func TestTheServiceHasOnlyTheOperationsDEC62Named(t *testing.T) {
	planned := map[string]string{
		"CommitMedia": "R3",
		"RemovePlace": "R6",
		"RefilePhoto": "R7",
	}

	var got []string
	svc := reflect.TypeOf(logbook.Service{})
	for i := range svc.NumMethod() {
		got = append(got, svc.Method(i).Name)
	}
	sort.Strings(got)

	for _, name := range got {
		if _, ok := planned[name]; !ok {
			t.Errorf("logbook.Service has %s, which is not one of the three "+
				"operations DEC-62 named — a fourth is a DECISION and not drift, "+
				"and it needs the conversation the first three had", name)
		}
	}

	for _, shipped := range []string{"CommitMedia", "RefilePhoto", "RemovePlace"} {
		if !slicesContains(got, shipped) {
			t.Fatalf("logbook.Service has %v and not %s, which is a shipped step's "+
				"whole reason for touching this file", got, shipped)
		}
	}

	t.Logf("logbook.Service: %s (planned: CommitMedia R3, RemovePlace R6, RefilePhoto R7)",
		strings.Join(got, ", "))
}

func slicesContains(in []string, want string) bool {
	for _, got := range in {
		if got == want {
			return true
		}
	}
	return false
}
