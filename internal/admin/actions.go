package admin

import (
	"context"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"

	"travellog/internal/auth"
	"travellog/internal/media"
)

// Writer is every change the panel makes. It is separate from Store so a read
// page cannot reach a write by accident.
type Writer interface {
	Rename(ctx context.Context, travellerID, name string) (int64, error)
	MintInvite(ctx context.Context, hash []byte, note string) error
	DeleteInvite(ctx context.Context, hash []byte) error
	RevokeSessionByID(ctx context.Context, sessionID string) error
	DeleteTraveller(ctx context.Context, travellerID string) ([]string, error)
}

// record is the audit line every mutating action writes.
func (d Deps) record(action, target string, err error) {
	if err != nil {
		d.Log.Error("admin: action failed", slog.String("action", action),
			slog.String("target", target), slog.String("err", err.Error()))
		return
	}
	d.Log.Info("admin: action", slog.String("action", action), slog.String("target", target))
}

// refuse re-draws the traveller's page with a line on it. http.Error answers
// plain text that htmx discards, so the control looks broken instead.
func (d Deps) refuse(w http.ResponseWriter, r *http.Request, id string, status int, why string) {
	data := page(r, "Traveller")
	data.Error = why
	if detail, err := d.Store.Traveller(r.Context(), id); err == nil {
		data.Traveller = &detail
		data.Storage = humanBytes(detail.BucketBytes)
		data.Title = detail.Email
		if sessions, err := d.Store.Sessions(r.Context(), id); err == nil {
			data.Sessions = sessionRows(sessions)
		}
	}
	d.Render.Page(w, status, "traveller", data)
}

func renameTraveller(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		name := strings.TrimSpace(r.PostFormValue("name"))

		if _, err := d.Writer.Rename(r.Context(), id, name); err != nil {
			d.record("rename", id, err)
			d.refuse(w, r, id, http.StatusBadRequest, "That name was not accepted.")
			return
		}
		d.record("rename", id, nil)
		hxRedirect(w, r, "/admin/travellers/"+id)
	}
}

func mintInvite(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		note := strings.TrimSpace(r.PostFormValue("note"))

		code, hash, err := auth.NewInvite()
		if err != nil {
			d.record("mint invite", note, err)
			http.Error(w, "the invite could not be minted", http.StatusInternalServerError)
			return
		}
		if err := d.Writer.MintInvite(r.Context(), hash, note); err != nil {
			d.record("mint invite", note, err)
			http.Error(w, "the invite could not be saved", http.StatusInternalServerError)
			return
		}

		d.record("mint invite", note, nil)
		rows, err := d.Store.Invites(r.Context())
		if err != nil {
			d.fail(w, r, "invites", "Invites", err)
			return
		}
		data := page(r, "Invites")
		data.Invites = inviteRows(rows)
		data.Minted = code
		d.Render.Page(w, http.StatusOK, "invites", data)
	}
}

func revokeInvite(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hash, err := hex.DecodeString(r.PathValue("hash"))
		if err != nil {
			http.Error(w, "that is not an invite", http.StatusBadRequest)
			return
		}
		if err := d.Writer.DeleteInvite(r.Context(), hash); err != nil {
			d.record("revoke invite", r.PathValue("hash"), err)
			http.Error(w, "the invite could not be revoked", http.StatusInternalServerError)
			return
		}
		d.record("revoke invite", r.PathValue("hash"), nil)
		hxRedirect(w, r, "/admin/invites")
	}
}

func revokeSession(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if err := d.Writer.RevokeSessionByID(r.Context(), id); err != nil {
			d.record("revoke session", id, err)
			http.Error(w, "the session could not be revoked", http.StatusInternalServerError)
			return
		}
		d.record("revoke session", id, nil)
		hxRedirect(w, r, "/admin/sessions")
	}
}

// hxRedirect moves an htmx caller with the header it understands, and anyone
// else with an ordinary 303.
func hxRedirect(w http.ResponseWriter, r *http.Request, to string) {
	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("HX-Redirect", to)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// deleteTraveller needs the traveller's email typed exactly. Rows go before
// bytes: a storage failure then leaves an orphan, not a photograph with none.
func deleteTraveller(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		detail, err := d.Store.Traveller(r.Context(), id)
		if err != nil {
			http.Error(w, "no traveller with that id", http.StatusNotFound)
			return
		}

		typed := strings.TrimSpace(r.PostFormValue("email"))
		if typed != detail.Email {
			d.record("delete refused", detail.Email, nil)
			d.refuse(w, r, id, http.StatusBadRequest,
				"That is not their address, so nothing was deleted.")
			return
		}

		objects, err := d.Writer.DeleteTraveller(r.Context(), id)
		if err != nil {
			d.record("delete", detail.Email, err)
			d.refuse(w, r, id, http.StatusInternalServerError,
				"The traveller could not be deleted, and nothing was removed.")
			return
		}
		d.record("delete", detail.Email, nil)
		d.forget(r.Context(), id, objects)

		hxRedirect(w, r, "/admin/travellers")
	}
}

// forget removes the bytes, best effort, logging each failure by object id.
// None of them undoes the delete.
func (d Deps) forget(ctx context.Context, travellerID string, objects []string) {
	if d.Objects == nil {
		return
	}
	for _, object := range objects {
		err := d.Objects.Delete(ctx, media.Key{Traveller: travellerID, Object: object})
		if err != nil {
			d.Log.Error("admin: an object outlived its traveller",
				slog.String("traveller", travellerID),
				slog.String("object", object),
				slog.String("err", err.Error()))
		}
	}
}
