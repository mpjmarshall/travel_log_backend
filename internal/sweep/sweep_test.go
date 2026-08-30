// What counts as an orphan, and what a sweep refuses to touch.
package sweep_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"travellog/internal/media"
	"travellog/internal/sweep"
)

var now = time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)

const traveller = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"

func object(n byte) string {
	out := make([]byte, 64)
	for i := range out {
		out[i] = "0123456789abcdef"[int(n)%16]
	}
	return string(out)
}

type fakeLister struct {
	items []media.Listing
	err   error
}

func (f fakeLister) List(context.Context) ([]media.Listing, error) { return f.items, f.err }

type fakeDeleter struct {
	deleted []media.Key
	failOn  string
}

func (f *fakeDeleter) Delete(_ context.Context, key media.Key) error {
	if key.Object == f.failOn {
		return errors.New("the bucket refused")
	}
	f.deleted = append(f.deleted, key)
	return nil
}

func listing(obj string, age time.Duration) media.Listing {
	return media.Listing{
		Traveller:    traveller,
		Object:       obj,
		Size:         100,
		LastModified: now.Add(-age),
	}
}

func TestAnObjectWithARowIsNotAnOrphan(t *testing.T) {
	known := map[string]struct{}{traveller + "/" + object(1): {}}

	found, err := sweep.Plan(context.Background(),
		fakeLister{items: []media.Listing{listing(object(1), 48*time.Hour)}},
		known, now, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(found.Orphans) != 0 {
		t.Errorf("an object the database still references was called an orphan: %+v", found.Orphans)
	}
}

func TestAnObjectWithNoRowIsAnOrphan(t *testing.T) {
	found, err := sweep.Plan(context.Background(),
		fakeLister{items: []media.Listing{listing(object(2), 48*time.Hour)}},
		map[string]struct{}{}, now, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(found.Orphans) != 1 || found.Orphans[0].Object != object(2) {
		t.Fatalf("Orphans = %+v, want the one unreferenced object", found.Orphans)
	}
	if found.Bytes != 100 {
		t.Errorf("Bytes = %d, want 100", found.Bytes)
	}
}

func TestAYoungObjectIsLeftAloneEvenWithNoRow(t *testing.T) {
	found, err := sweep.Plan(context.Background(),
		fakeLister{items: []media.Listing{listing(object(3), time.Hour)}},
		map[string]struct{}{}, now, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(found.Orphans) != 0 {
		t.Error("an object written an hour ago was swept. The row is written before " +
			"the bytes, so this should be impossible, and a bucket-wide delete does " +
			"not rest its safety on one reading of one code path")
	}
	if found.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1, so the report says what it left", found.Skipped)
	}
}

func TestARowWithNoObjectIsNotTheSweepsBusiness(t *testing.T) {
	known := map[string]struct{}{traveller + "/" + object(4): {}}

	found, err := sweep.Plan(context.Background(),
		fakeLister{items: nil}, known, now, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(found.Orphans) != 0 {
		t.Error("a row whose bytes have not landed yet is a begun upload, not an orphan")
	}
}

func TestPlanReportsWhatItCouldNotList(t *testing.T) {
	_, err := sweep.Plan(context.Background(),
		fakeLister{err: errors.New("the bucket is unreachable")},
		map[string]struct{}{}, now, 24*time.Hour)
	if err == nil {
		t.Error("a bucket that could not be listed reported no orphans and no error, " +
			"which reads as a clean sweep")
	}
}

func TestApplyDeletesEveryOrphanAndCountsTheBytes(t *testing.T) {
	deleter := &fakeDeleter{}
	orphans := []media.Key{
		{Traveller: traveller, Object: object(5)},
		{Traveller: traveller, Object: object(6)},
	}

	done, failed := sweep.Apply(context.Background(), deleter, orphans)
	if done != 2 || len(failed) != 0 {
		t.Fatalf("Apply() = %d done, %d failed, want 2 and 0", done, len(failed))
	}
	if len(deleter.deleted) != 2 {
		t.Errorf("the bucket was asked for %d deletes, want 2", len(deleter.deleted))
	}
}

func TestOneFailureDoesNotStopTheRest(t *testing.T) {
	deleter := &fakeDeleter{failOn: object(7)}
	orphans := []media.Key{
		{Traveller: traveller, Object: object(7)},
		{Traveller: traveller, Object: object(8)},
	}

	done, failed := sweep.Apply(context.Background(), deleter, orphans)
	if done != 1 {
		t.Errorf("done = %d, want 1", done)
	}
	if len(failed) != 1 {
		t.Errorf("failed = %d, want 1: a sweep that stops at the first refusal leaves "+
			"the rest of the bucket uncleaned and reports nothing about why", len(failed))
	}
}
