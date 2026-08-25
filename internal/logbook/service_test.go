// The Service layer's SHAPE, which is the thing PD-05 is about.
//
// What CommitMedia DOES is exercised at internal/httpapi (over the real mux,
// against media.Memory) and at internal/postgres (against a real database).
// This file is about the rule that a fourth method arrives as a decision.
package logbook_test

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"travellog/internal/logbook"
)

// THE SERVICE IS THREE OPERATIONS AND R3 BUILDS THE FIRST (DEC-62, PD-05).
//
// DEC-62 named them — RefilePhoto, RemovePlace and the media commit flow — and
// ruled IN THE SAME BREATH that most routes are plain CRUD where a service
// method forwards to the repository, and that such a file is noise.
//
// THE THREE LAND IN THREE DIFFERENT STEPS, WHICH IS WHY THIS LEG EXISTS.
// CommitMedia is R3's, RemovePlace is R6's and RefilePhoto is R7's, so nobody
// ever sees the pattern in one sitting and a worker looking for symmetry
// applies "there is a Service" uniformly — at which point every route goes
// through a method that forwards to a store, and the layer stops meaning
// anything.
//
// IT ASSERTS THE WHOLE SET AND NOT A CEILING. A leg saying "at most three"
// passes against a Service with one method wrongly named; this names the ones
// that may be there, so a method arriving reddens it and a method LEAVING
// reddens it too.
func TestTheServiceHasOnlyTheOperationsDEC62Named(t *testing.T) {
	// The three DEC-62 named, and the step each belongs to. A method not on
	// this list is a fourth operation and needs the conversation the first
	// three had.
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

	// AND THE ONE THIS STEP OWNS IS ACTUALLY THERE. Without this the loop
	// above passes against a Service with no methods at all.
	if !slicesContains(got, "CommitMedia") {
		t.Fatalf("logbook.Service has %v and not CommitMedia, which is R3's "+
			"whole reason for the file existing", got)
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
