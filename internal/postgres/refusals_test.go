// The refusals nothing asserted. Each was proven unasserted first: giving all
// eleven a sentinel field name left the whole suite green.
package postgres

import (
	"context"
	"strings"
	"testing"

	"travellog/internal/logbook"
)

// TestAWriteWithNoIdIsRefusedByEveryStore covers the refusal five files spelt
// identically. It is unreachable over HTTP: a handler fills the id first.
func TestAWriteWithNoIdIsRefusedByEveryStore(t *testing.T) {
	ctx := context.Background()

	for _, leg := range []struct {
		entity string
		put    func(t *testing.T) error
	}{
		{"walk", func(t *testing.T) error {
			store, _ := walkStore(t)
			_, _, err := store.PutWalk(ctx, tid, logbook.WalkWrite{})
			return err
		}},
		{"place", func(t *testing.T) error {
			store, _ := placeStore(t)
			_, _, err := store.PutPlace(ctx, tid, logbook.PlaceWrite{})
			return err
		}},
		{"city", func(t *testing.T) error {
			store, _ := cityStore(t)
			_, err := store.PutCity(ctx, tid, logbook.CityWrite{})
			return err
		}},
		{"trip", func(t *testing.T) error {
			store, _, _ := logbookStore(t)
			_, _, err := store.PutTrip(ctx, tid, logbook.TripWrite{})
			return err
		}},
		{"photo", func(t *testing.T) error {
			store, _ := photoStore(t)
			_, _, err := store.PutPhoto(ctx, tid, logbook.PhotoWrite{})
			return err
		}},
	} {
		t.Run(leg.entity, func(t *testing.T) {
			if got := fieldNamed(leg.put(t)); got != "id" {
				t.Errorf("a %s write carrying no id named %q, want \"id\"", leg.entity, got)
			}
		})
	}
}

func TestSnoozingWithoutBothHalvesIsRefusedNamingPhotoIds(t *testing.T) {
	store, _ := photoStore(t)
	ctx := context.Background()

	if got := fieldNamed(snoozeErr(store, ctx, logbook.SnoozeWrite{Until: ptr(at("2027-11-01T00:00:00Z"))})); got != "photoIds" {
		t.Errorf("a snooze with no ids named %q, want \"photoIds\"", got)
	}
	if got := fieldNamed(snoozeErr(store, ctx, logbook.SnoozeWrite{PhotoIDs: &[]string{"p-autumn"}})); got != "photoIds" {
		t.Errorf("a snooze with no until named %q, want \"photoIds\" — a group with no "+
			"moment is a filing that never ends", got)
	}
}

func snoozeErr(store PhotoStore, ctx context.Context, w logbook.SnoozeWrite) error {
	_, _, err := store.SnoozePhotos(ctx, tid, w)
	return err
}

func TestARefileNamingOnlyHalfThePairIsRefused(t *testing.T) {
	store, _ := photoStore(t)
	ctx := context.Background()

	_, err := store.RefilePhoto(ctx, tid, "p-autumn", logbook.RefileWrite{PlaceID: ptr("gamcheon")})
	if got := fieldNamed(err); got != "visitId" {
		t.Errorf("a re-file naming a place and no occasion named %q, want \"visitId\" — "+
			"the pair is coherent by a Go rule and not by the schema", got)
	}

	_, err = store.RefilePhoto(ctx, tid, "p-autumn", logbook.RefileWrite{VisitID: ptr("v-anything")})
	if got := fieldNamed(err); got != "placeId" {
		t.Errorf("a re-file naming an occasion and no place named %q, want \"placeId\" — "+
			"the store used to answer visitId for both halves while the API answered "+
			"placeId, and there is one answer now", got)
	}
}

func TestARefileToSomethingThatIsNotAPinIsRefused(t *testing.T) {
	store, _ := photoStore(t)

	_, err := store.RefilePhoto(context.Background(), tid, "p-autumn",
		logbook.RefileWrite{PlaceID: ptr("not-a-pin-in-this-log"), VisitID: ptr("v-anything")})
	if got := fieldNamed(err); got != "placeId" {
		t.Errorf("re-filing to a place that is not in the log named %q, want \"placeId\"", got)
	}
}

func TestAPhotoCreateNamingAnUnknownTripOrCityNamesTheField(t *testing.T) {
	store, _ := photoStore(t)
	ctx := context.Background()

	good := logbook.PhotoWrite{
		ID: ptr("p-new"), TripID: ptr("autumn-crossing"), CityID: ptr("seoul"),
		TakenAt: ptr(at("2027-09-30T12:00:00Z")), Asset: ptr(assetA),
	}

	unknownTrip := good
	unknownTrip.TripID = ptr("no-such-trip")
	if _, _, err := store.PutPhoto(ctx, tid, unknownTrip); fieldNamed(err) != "tripId" {
		t.Errorf("a photograph on a trip that is not in the log named %q, want \"tripId\" "+
			"(err %v)", fieldNamed(err), err)
	}

	unknownCity := good
	unknownCity.CityID = ptr("no-such-city")
	if _, _, err := store.PutPhoto(ctx, tid, unknownCity); fieldNamed(err) != "cityId" {
		t.Errorf("a photograph in a city that is not in the log named %q, want \"cityId\" "+
			"(err %v)", fieldNamed(err), err)
	}
}

// TestClearingAPlaceThatHasOccasionsIsRefused is the longest message in
// place_store.go, and it was the one refusal with no leg on it.
func TestClearingAPlaceThatHasOccasionsIsRefused(t *testing.T) {
	store, db := placeStore(t)
	ctx := context.Background()

	held := []logbook.Visit{
		{ID: "v-fushimi-may", TripID: "kyoto-in-may", At: at("2027-05-03T07:05:00Z")},
		{ID: "v-fushimi-sep", TripID: "autumn-crossing", At: at("2027-09-20T07:05:00Z")},
	}
	if _, _, err := store.PutPlace(ctx, tid, logbook.PlaceWrite{ID: ptr("fushimi-inari"), Visits: &held}); err != nil {
		t.Fatalf("seeding two occasions: %v", err)
	}

	_, _, err := store.PutPlace(ctx, tid,
		logbook.PlaceWrite{ID: ptr("fushimi-inari"), Visits: &[]logbook.Visit{}})

	var invalid logbook.InvalidFieldError
	if !asInvalidField(err, &invalid) || invalid.Field != "visits" {
		t.Fatalf("clearing a place that holds two occasions = %v, want an invalid_field "+
			"on visits — it destroys a history no client control asks to destroy", err)
	}
	if n := count(t, db, `SELECT count(*) FROM visits WHERE place_id='fushimi-inari'`); n != 2 {
		t.Errorf("visits = %d, want 2 — the refusal rolls back", n)
	}
	if !strings.Contains(invalid.Why, "all 2 occasions") {
		t.Errorf("the reason is %q and does not carry the count it read — the count is "+
			"the only thing that read produces which the decision does not use, so it "+
			"is the whole of what the message adds", invalid.Why)
	}
}
