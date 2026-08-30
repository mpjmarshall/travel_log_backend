// The load: ten tables, one transaction, and one refusal.
package seed

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// LoadOptions is deliberately EMPTY, and it carries no `Force` and no
// `Reset`.
type LoadOptions struct{}

// Report is what a completed load answers with.
type Report struct {
	Rows     map[string]int
	Duration time.Duration
}

// ErrTravellerExists is the refusal.
var ErrTravellerExists = errors.New("seed: this database already has a traveller")

// TravellerExistsError names the traveller that was found.
type TravellerExistsError struct {
	TravellerID string
	Email       string
}

func (e *TravellerExistsError) Error() string {
	return fmt.Sprintf("seed: this database already has a traveller — id %s, %s. "+
		"It is not an empty development database, and loading a captured log into "+
		"it would put a stranger's geography in somebody's record permanently: "+
		"nothing in this application can delete a city (DEC-57)",
		e.TravellerID, e.Email)
}

func (e *TravellerExistsError) Is(target error) bool { return target == ErrTravellerExists }

// Load inserts a Dataset into a migrated schema, or refuses.
func Load(ctx context.Context, db *sql.DB, d *Dataset, _ LoadOptions) (Report, error) {
	started := time.Now()
	report := Report{Rows: map[string]int{}}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return report, fmt.Errorf("seed: beginning the load: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	existing, err := firstTraveller(ctx, tx)
	if err != nil {
		return report, err
	}
	if existing != nil {
		return report, existing
	}

	for _, step := range insertSteps(d) {
		if len(step.rows) == 0 {
			report.Rows[step.table] = 0
			continue
		}
		statement, args := step.statement()
		if _, err := tx.ExecContext(ctx, statement, args...); err != nil {
			return report, fmt.Errorf("seed: inserting %d rows into %s: %w", len(step.rows), step.table, err)
		}
		report.Rows[step.table] = len(step.rows)
	}

	if err := tx.Commit(); err != nil {
		return report, fmt.Errorf("seed: committing the load: %w", err)
	}
	report.Duration = time.Since(started)
	return report, nil
}

// insertStep is one table's whole INSERT: the column list, the per-column
// cast each placeholder carries, and one []any per row.
type insertStep struct {
	table   string
	columns []string
	casts   []string
	rows    [][]any
}

// statement renders `insert into t (a, b) VALUES ($1::uuid, $2), ($3::uuid,
// $4)` and the flat argument list beside it.
func (s insertStep) statement() (string, []any) {
	var b strings.Builder
	b.WriteString("INSERT INTO ")
	b.WriteString(s.table)
	b.WriteString(" (")
	b.WriteString(strings.Join(s.columns, ", "))
	b.WriteString(") VALUES ")

	args := make([]any, 0, len(s.rows)*len(s.columns))
	for i, row := range s.rows {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteByte('(')
		for j, v := range row {
			if j > 0 {
				b.WriteString(", ")
			}
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(len(args) + 1))
			b.WriteString(s.casts[j])
			args = append(args, v)
		}
		b.WriteByte(')')
	}
	return b.String(), args
}

// insertSteps is dependency order, spelled once.
func insertSteps(d *Dataset) []insertStep {
	return []insertStep{
		travellerStep(d), mediaStep(d), cityStep(d), tripStep(d), tripCityStep(d),
		placeStep(d), visitStep(d), photoStep(d), walkStep(d), shareLinkStep(d),
	}
}

func travellerStep(d *Dataset) insertStep {
	step := insertStep{
		table:   "travellers",
		columns: []string{"id", "email", "passphrase_hash", "name", "logbook_version", "created_at"},
		casts:   []string{"::uuid", "", "", "", "", ""},
	}
	for _, t := range d.Travellers {
		step.rows = append(step.rows, []any{t.ID, t.Email, t.PassphraseHash, t.Name, t.LogbookVersion, t.CreatedAt})
	}
	return step
}

func mediaStep(d *Dataset) insertStep {
	step := insertStep{
		table:   "media_objects",
		columns: []string{"traveller_id", "id", "byte_size", "content_type", "created_at", "uploaded_at"},
		casts:   []string{"::uuid", "", "", "", "", ""},
	}
	for _, m := range d.MediaObjects {
		step.rows = append(step.rows, []any{m.TravellerID, m.ID, m.ByteSize, m.ContentType, m.CreatedAt, m.UploadedAt})
	}
	return step
}

func cityStep(d *Dataset) insertStep {
	step := insertStep{
		table: "cities",
		columns: []string{"traveller_id", "id", "name", "country_code", "country_name",
			"centre_lat", "centre_lng", "cover_asset", "geocoder_ref", "created_at"},
		casts: []string{"::uuid", "", "", "", "", "", "", "", "", ""},
	}
	for _, c := range d.Cities {
		step.rows = append(step.rows, []any{c.TravellerID, c.ID, c.Name, c.CountryCode, c.CountryName,
			c.CentreLat, c.CentreLng, c.CoverAsset, c.GeocoderRef, c.CreatedAt})
	}
	return step
}

func tripStep(d *Dataset) insertStep {
	step := insertStep{
		table: "trips",
		columns: []string{"traveller_id", "id", "name", "started_on", "ended_on", "summary",
			"cover_asset", "share_photos", "share_notes", "share_coordinates", "created_at"},
		casts: []string{"::uuid", "", "", "::date", "::date", "", "", "", "", "", ""},
	}
	for _, t := range d.Trips {
		step.rows = append(step.rows, []any{t.TravellerID, t.ID, t.Name,
			dayLiteral(t.StartedOn), dayLiteral(t.EndedOn), t.Summary, t.CoverAsset,
			t.SharePhotos, t.ShareNotes, t.ShareCoordinates, t.CreatedAt})
	}
	return step
}

func tripCityStep(d *Dataset) insertStep {
	step := insertStep{
		table:   "trip_cities",
		columns: []string{"traveller_id", "trip_id", "city_id", "ordinal"},
		casts:   []string{"::uuid", "", "", ""},
	}
	for _, tc := range d.TripCities {
		step.rows = append(step.rows, []any{tc.TravellerID, tc.TripID, tc.CityID, tc.Ordinal})
	}
	return step
}

func placeStep(d *Dataset) insertStep {
	step := insertStep{
		table:   "places",
		columns: []string{"traveller_id", "id", "city_id", "name", "lat", "lng", "plan", "cover_asset", "created_at"},
		casts:   []string{"::uuid", "", "", "", "", "", "", "", ""},
	}
	for _, p := range d.Places {
		step.rows = append(step.rows, []any{p.TravellerID, p.ID, p.CityID, p.Name,
			p.Lat, p.Lng, p.Plan, p.CoverAsset, p.CreatedAt})
	}
	return step
}

func visitStep(d *Dataset) insertStep {
	step := insertStep{
		table:   "visits",
		columns: []string{"traveller_id", "id", "place_id", "trip_id", "ordinal", "at", "note", "created_at"},
		casts:   []string{"::uuid", "", "", "", "", "", "", ""},
	}
	for _, v := range d.Visits {
		step.rows = append(step.rows, []any{v.TravellerID, v.ID, v.PlaceID, v.TripID,
			v.Ordinal, v.At, v.Note, v.CreatedAt})
	}
	return step
}

func photoStep(d *Dataset) insertStep {
	step := insertStep{
		table: "photos",
		columns: []string{"traveller_id", "id", "trip_id", "city_id", "place_id", "visit_id",
			"taken_at", "asset", "caption", "lat", "lng", "accuracy_metres", "filed_later", "created_at"},
		casts: []string{"::uuid", "", "", "", "", "", "", "", "", "", "", "", "", ""},
	}
	for _, p := range d.Photos {
		step.rows = append(step.rows, []any{p.TravellerID, p.ID, p.TripID, p.CityID,
			p.PlaceID, p.VisitID, p.TakenAt, p.Asset, p.Caption, p.Lat, p.Lng,
			p.AccuracyMetres, p.FiledLater, p.CreatedAt})
	}
	return step
}

func walkStep(d *Dataset) insertStep {
	step := insertStep{
		table: "walks",
		columns: []string{"traveller_id", "id", "trip_id", "city_id", "recorded_on",
			"distance_km", "points", "name", "dismissed", "created_at"},
		casts: []string{"::uuid", "", "", "", "::date", "", "::jsonb", "", "", ""},
	}
	for _, w := range d.Walks {
		step.rows = append(step.rows, []any{w.TravellerID, w.ID, w.TripID, w.CityID,
			w.RecordedOn.UTC().Format(dateLayout), w.DistanceKm, w.Points,
			w.Name, w.Dismissed, w.CreatedAt})
	}
	return step
}

func shareLinkStep(d *Dataset) insertStep {
	step := insertStep{
		table:   "share_links",
		columns: []string{"traveller_id", "trip_id", "token_hash", "created_at", "revoked_at"},
		casts:   []string{"::uuid", "", "", "", ""},
	}
	for _, s := range d.ShareLinks {
		step.rows = append(step.rows, []any{s.TravellerID, s.TripID, s.TokenHash, s.CreatedAt, s.RevokedAt})
	}
	return step
}

// ExistingTraveller answers the traveller this database already holds, or
// nil.
func ExistingTraveller(ctx context.Context, db *sql.DB) (*TravellerExistsError, error) {
	return firstTraveller(ctx, db)
}

// rowQuerier is *sql.DB and *sql.Tx.
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

const firstTravellerSQL = `SELECT id::text, email FROM travellers ORDER BY created_at, id LIMIT 1`

func firstTraveller(ctx context.Context, q rowQuerier) (*TravellerExistsError, error) {
	var id, email string
	switch err := q.QueryRowContext(ctx, firstTravellerSQL).Scan(&id, &email); {
	case err == nil:
		return &TravellerExistsError{TravellerID: id, Email: email}, nil
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	default:
		return nil, fmt.Errorf("seed: asking whether this database has a traveller: %w", err)
	}
}
