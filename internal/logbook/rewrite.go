// The asset rewrite, and the decoder that reads a captured document back in.
package logbook

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// RewriteAssets answers a copy of doc with every asset locator replaced by
// the object id `mapping` gives for it.
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

// lookupCover is the nil-tolerant half.
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

// DecodeEnvelope reads a captured logbook document — the client's own
// encoder's output — back into these types.
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
