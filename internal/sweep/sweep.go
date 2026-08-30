// Finding bucket objects the database no longer references, and removing them.
package sweep

import (
	"context"
	"fmt"
	"time"

	"travellog/internal/media"
)

// Lister answers everything the bucket holds.
type Lister interface {
	List(ctx context.Context) ([]media.Listing, error)
}

// Deleter removes one object.
type Deleter interface {
	Delete(ctx context.Context, key media.Key) error
}

// Found is what a sweep would do, before it does anything.
type Found struct {
	Orphans []media.Key
	Bytes   int64
	Seen    int
	Skipped int
}

// Plan answers the objects the database does not reference, leaving anything
// younger than minAge alone whatever the rows say.
func Plan(ctx context.Context, from Lister, known map[string]struct{},
	now time.Time, minAge time.Duration) (Found, error) {

	items, err := from.List(ctx)
	if err != nil {
		return Found{}, fmt.Errorf("sweep: listing the bucket: %w", err)
	}

	found := Found{Seen: len(items)}

	for _, item := range items {
		if _, ok := known[item.Traveller+"/"+item.Object]; ok {
			continue
		}
		if now.Sub(item.LastModified) < minAge {
			found.Skipped++
			continue
		}
		found.Orphans = append(found.Orphans,
			media.Key{Traveller: item.Traveller, Object: item.Object})
		found.Bytes += item.Size
	}
	return found, nil
}

// Apply removes each orphan, carrying on past a refusal so one bad key does
// not leave the rest of the bucket uncleaned.
func Apply(ctx context.Context, with Deleter, orphans []media.Key) (done int, failed []error) {
	for _, key := range orphans {
		if err := with.Delete(ctx, key); err != nil {
			failed = append(failed, fmt.Errorf("sweep: %s/%s: %w", key.Traveller, key.Object, err))
			continue
		}
		done++
	}
	return done, failed
}
