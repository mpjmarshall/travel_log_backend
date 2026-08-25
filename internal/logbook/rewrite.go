// PD-03's asset rewrite, and the decoder that reads a captured document back
// in.
//
// WHY A REWRITE EXISTS AT ALL. The client's own log addresses a photograph by
// a BUNDLE PATH — `assets/imagery/card-ireland.png` — because until DEC-46
// every image in the app was an asset compiled into the binary. On this server
// the same four columns hold a 64-character lowercase hex sha256 (DEC-38), and
// the schema says so: `photos_asset_sha256_ck`, and one CHECK of the same
// shape on each of the three cover columns. So loading the captured fixture is
// a decode plus EXACTLY ONE transformation, and this is it.
//
// FOUR COLUMNS, NOT ONE (DEC-46). `photos.asset` and the three `coverAsset`s
// on Trip, City and Place. DEC-40's format bump to `"version": 2` describes
// those four fields and nothing else, so a rewrite reaching three of them is
// the format bump only three quarters applied.
//
// IT IS A NAMED FUNCTION RATHER THAN A LOOP INSIDE THE SEED, and the reason is
// a measurement rather than tidiness. The client's 284 photographs carry two
// distinct locators, 189 of one and 95 of the other, so pointing one locator
// at the other's digest changes 189 rows while every row count, every foreign
// key and every dangling-reference check stays green. Only a diff of the
// emitted document against the client's own can see it. A named function is
// what gives that mutation one place to be applied and one leg to redden.
package logbook

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// RewriteAssets answers a copy of doc with every asset locator replaced by the
// object id `mapping` gives for it.
//
// IT REFUSES AN UNMAPPED LOCATOR AND NAMES BOTH THE VALUE AND THE COLUMN. The
// alternative is to pass it through, at which point the failure arrives from
// PostgreSQL as SQLSTATE 23514 naming `photos_asset_sha256_ck` — a constraint
// name where what the operator needs is the name of the file nobody uploaded.
//
// AN ABSENT COVER IS NOT AN UNMAPPED ONE. The client's own log leaves one trip,
// three cities and eight places with no cover at all, so treating nil as a miss
// would refuse the one document this function exists for.
//
// THE INPUT IS COPIED, AND THAT IS LOAD-BEARING RATHER THAN POLITE. A Document
// is six slice headers over shared backing arrays: rewriting in place would
// make the caller's "before" and the callee's "after" the same memory, and the
// round trip that compares the client's decoded document against what came
// back out of PostgreSQL would then pass against anything at all.
func RewriteAssets(doc Document, mapping map[string]string) (Document, error) {
	out := doc

	out.Photos = make([]Photo, len(doc.Photos))
	copy(out.Photos, doc.Photos)
	for i := range out.Photos {
		id, err := lookup(mapping, out.Photos[i].Asset, "photos[%d].asset", i)
		if err != nil {
			return Document{}, err
		}
		out.Photos[i].Asset = id
	}

	out.Trips = make([]Trip, len(doc.Trips))
	copy(out.Trips, doc.Trips)
	for i := range out.Trips {
		cover, err := lookupCover(mapping, out.Trips[i].CoverAsset, "trips[%d].coverAsset", i)
		if err != nil {
			return Document{}, err
		}
		out.Trips[i].CoverAsset = cover
	}

	out.Cities = make([]City, len(doc.Cities))
	copy(out.Cities, doc.Cities)
	for i := range out.Cities {
		cover, err := lookupCover(mapping, out.Cities[i].CoverAsset, "cities[%d].coverAsset", i)
		if err != nil {
			return Document{}, err
		}
		out.Cities[i].CoverAsset = cover
	}

	out.Places = make([]Place, len(doc.Places))
	copy(out.Places, doc.Places)
	for i := range out.Places {
		cover, err := lookupCover(mapping, out.Places[i].CoverAsset, "places[%d].coverAsset", i)
		if err != nil {
			return Document{}, err
		}
		out.Places[i].CoverAsset = cover
	}

	return out, nil
}

// lookupCover is the nil-tolerant half. It answers a NEW pointer rather than
// writing through the one it was given, because the caller's pointer is shared
// with the document it handed in.
func lookupCover(mapping map[string]string, cover *string, where string, i int) (*string, error) {
	if cover == nil {
		return nil, nil
	}
	id, err := lookup(mapping, *cover, where, i)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

func lookup(mapping map[string]string, locator, where string, i int) (string, error) {
	id, ok := mapping[locator]
	if !ok {
		return "", fmt.Errorf("logbook: %s addresses %q, which is not one of the "+
			"%d locators this load has object ids for; nothing has uploaded it, "+
			"so the row cannot be written",
			fmt.Sprintf(where, i), locator, len(mapping))
	}
	return id, nil
}

// ErrNotAnEnvelope is a file that is not one of these documents at all.
var ErrNotAnEnvelope = errors.New("logbook: that is not a logbook envelope")

// DecodeEnvelope reads a captured logbook document — the client's own encoder's
// output — back into these types. It is Emit's counterpart and is the only way
// `make seed` gets a Document.
//
// IT DOES NOT JUDGE THE VERSION, and that is deliberate. DEC-40's refusal
// belongs on the wire, where a client is asking for a shape this build cannot
// write; here the version is a FACT ABOUT A FILE somebody captured, and the
// captured file is format 1 while this build emits 2. Refusing it would make
// the one document this project has to load the one document it cannot.
//
// IT REFUSES TRAILING CONTENT for the reason DecodeJSON does: two documents in
// one file would silently load the first and report success.
func DecodeEnvelope(raw []byte) (Envelope, error) {
	var envelope Envelope
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&envelope); err != nil {
		return Envelope{}, fmt.Errorf("%w: %v", ErrNotAnEnvelope, err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return Envelope{}, fmt.Errorf("%w: it carries more than one JSON value", ErrNotAnEnvelope)
		}
		return Envelope{}, fmt.Errorf("%w: %v", ErrNotAnEnvelope, err)
	}
	return envelope, nil
}
