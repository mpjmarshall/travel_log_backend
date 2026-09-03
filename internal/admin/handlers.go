package admin

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
)

// PageSize is how many travellers one page holds.
const PageSize = 25

// Store is every read the panel makes. cmd/api supplies the postgres one.
type Store interface {
	Overview(ctx context.Context) (Overview, error)
	Travellers(ctx context.Context, query string, limit, offset int) ([]Traveller, int, error)
	Traveller(ctx context.Context, id string) (TravellerDetail, error)
	Sessions(ctx context.Context, travellerID string) ([]Session, error)
	Invites(ctx context.Context) ([]Invite, error)
}

// page starts the data every template needs, so no handler forgets the token.
func page(r *http.Request, title string) PageData {
	return PageData{Title: title, CSRF: csrfFrom(r.Context()), SignedIn: true}
}

// fail draws the page it was asked for with a line saying the read did not
// work, rather than a blank screen or a stack trace.
func (d Deps) fail(w http.ResponseWriter, r *http.Request, name, title string, err error) {
	d.Log.Error("admin: reading", slog.String("page", name), slog.String("err", err.Error()))
	data := page(r, title)
	data.Failed = true
	d.Render.Page(w, http.StatusInternalServerError, name, data)
}

func overview(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		o, err := d.Store.Overview(r.Context())
		if err != nil {
			d.fail(w, r, "dashboard", "Overview", err)
			return
		}
		sessions, err := d.Store.Sessions(r.Context(), "")
		if err != nil {
			d.fail(w, r, "dashboard", "Overview", err)
			return
		}

		data := page(r, "Overview")
		data.Cards = []Card{
			{"Travellers", strconv.Itoa(o.Travellers)},
			{"Trips", strconv.Itoa(o.Trips)},
			{"Photos", strconv.Itoa(o.Photos)},
			{"Places", strconv.Itoa(o.Places)},
			{"Live sessions", strconv.Itoa(o.LiveSessions)},
			{"Unused invites", strconv.Itoa(o.UnusedInvites)},
			{"Storage", humanBytes(o.BucketBytes)},
		}
		data.Sessions = sessionRows(sessions)
		d.Render.Page(w, http.StatusOK, "dashboard", data)
	}
}

func travellers(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("q")
		offset := max(0, atoi(r.URL.Query().Get("offset")))

		rows, total, err := d.Store.Travellers(r.Context(), query, PageSize, offset)
		if err != nil {
			d.fail(w, r, "travellers", "Travellers", err)
			return
		}

		data := page(r, "Travellers")
		data.Query = query
		data.Travellers = rows
		data.Total = total
		data.Offset = offset
		data.PageSize = PageSize

		if r.Header.Get("HX-Request") != "" {
			d.Render.Fragment(w, http.StatusOK, "rows", data)
			return
		}
		d.Render.Page(w, http.StatusOK, "travellers", data)
	}
}

func traveller(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		detail, err := d.Store.Traveller(r.Context(), id)
		if err != nil {
			d.Log.Warn("admin: no such traveller", slog.String("id", id))
			data := page(r, "Traveller")
			data.Failed = true
			d.Render.Page(w, http.StatusNotFound, "traveller", data)
			return
		}
		sessions, err := d.Store.Sessions(r.Context(), id)
		if err != nil {
			d.fail(w, r, "traveller", "Traveller", err)
			return
		}

		data := page(r, detail.Email)
		data.Traveller = &detail
		data.Storage = humanBytes(detail.BucketBytes)
		data.Sessions = sessionRows(sessions)
		d.Render.Page(w, http.StatusOK, "traveller", data)
	}
}

func sessionsPage(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := d.Store.Sessions(r.Context(), "")
		if err != nil {
			d.fail(w, r, "sessions", "Sessions", err)
			return
		}
		data := page(r, "Sessions")
		data.Sessions = sessionRows(rows)
		d.Render.Page(w, http.StatusOK, "sessions", data)
	}
}

func invitesPage(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := d.Store.Invites(r.Context())
		if err != nil {
			d.fail(w, r, "invites", "Invites", err)
			return
		}
		data := page(r, "Invites")
		data.Invites = inviteRows(rows)
		d.Render.Page(w, http.StatusOK, "invites", data)
	}
}
