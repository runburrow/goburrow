package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/runburrow/goburrow/internal/store"
)

// apiTokenCreate makes a read-only API token and shows it once on the
// Account page. Rendered rather than redirected, so the secret never ends
// up in a URL, browser history, or access logs.
func (s *Server) apiTokenCreate(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	_, secret, err := s.Store.CreateAPIToken(r.Context(), sess.User.ID, r.PostFormValue("name"))
	if errors.Is(err, store.ErrTooManyTokens) {
		data := s.accountData(r, "")
		data["TokenError"] = "You already have " + strconv.Itoa(store.MaxAPITokens) + " API tokens. Revoke one you no longer use first."
		s.render(w, r, http.StatusBadRequest, "account", "Your account", data)
		return
	} else if err != nil {
		s.serverError(w, r, err)
		return
	}
	data := s.accountData(r, "")
	data["NewToken"] = secret
	s.render(w, r, http.StatusOK, "account", "Your account", data)
}

func (s *Server) apiTokenRevoke(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	err = s.Store.DeleteAPIToken(r.Context(), sessionFrom(r.Context()).User.ID, id)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return
	} else if err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/account?flash=token-revoked#api", http.StatusSeeOther)
}
