package seed

import "errors"

// Generate builds a whole database's worth of rows from Options alone.
//
// IT IS STILL A STUB, AND `next_slice` OWNS IT. R4 loads the CAPTURED CLIENT
// DOCUMENT (DEC-75) — 284 photographs with the edge cases the schema was
// designed around — and the 50,000-row generator is what makes DEC-63/DEC-70's
// index question answerable, which is a different job in a different step.
//
// ONE THING R4 LEARNED THAT THIS FUNCTION HAS TO KNOW, AND IT IS ARITHMETIC
// RATHER THAN TASTE. Load writes one multi-row INSERT per table, and the wire
// protocol counts a statement's parameters in an int16 — so the ceiling is
// 65,535. The captured fixture's largest statement is photos at 284 rows x 14
// written columns = 3,976 parameters, comfortably under it. THE SAME SHAPE AT
// DefaultPhotos IS 50,000 x 14 = 700,000, more than ten times the ceiling. So
// the generator CHUNKS — 4,000 photograph rows per statement is 56,000
// parameters and leaves room — and the 284-row seed correctly does not.
//
// The other way out is `unnest` of fourteen arrays, which is fourteen
// parameters at any row count. It is not available: database/sql has no array
// type, so it needs pgx's pgtype, and spec L20 says pgx is used solely as a
// blank import driver. cmd/api/imports_test.go went red against exactly that
// draft of load.go.
func Generate(opts Options) (*Dataset, error) {
	return nil, errors.New("seed: Generate is not implemented")
}
