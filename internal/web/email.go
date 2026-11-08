package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/runburrow/goburrow/internal/mail"
	"github.com/runburrow/goburrow/internal/store"
)

// ------------------------------------------------------------ email alerts

func (s *Server) mailOn() bool { return s.Notify != nil && s.Notify.Mail != nil }

func (s *Server) emailAlertCreate(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.loadProject(w, r, accessOwner)
	if !ok {
		return
	}
	back := "/projects/" + p.Slug + "/settings?flash="
	addr := strings.TrimSpace(r.PostFormValue("address"))
	onNew, onRegression := r.PostFormValue("on_new") == "1", r.PostFormValue("on_regression") == "1"
	switch {
	case !s.mailOn():
		http.Redirect(w, r, back+"email-off#email-alerts", http.StatusSeeOther)
		return
	case !mail.ValidAddress(addr):
		http.Redirect(w, r, back+"email-invalid#email-alerts", http.StatusSeeOther)
		return
	case !onNew && !onRegression:
		http.Redirect(w, r, back+"email-no-trigger#email-alerts", http.StatusSeeOther)
		return
	}
	_, err := s.Store.CreateEmailAlert(r.Context(), p.ID, addr, onNew, onRegression)
	switch {
	case errors.Is(err, store.ErrTooManyEmailAlerts):
		http.Redirect(w, r, back+"email-limit#email-alerts", http.StatusSeeOther)
	case errors.Is(err, store.ErrEmailAlertExists):
		http.Redirect(w, r, back+"email-exists#email-alerts", http.StatusSeeOther)
	case err != nil:
		s.serverError(w, r, err)
	default:
		http.Redirect(w, r, back+"email-added#email-alerts", http.StatusSeeOther)
	}
}

func (s *Server) loadEmailAlert(w http.ResponseWriter, r *http.Request) (*store.Project, *store.EmailAlert, bool) {
	p, _, ok := s.loadProject(w, r, accessOwner)
	if !ok {
		return nil, nil, false
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return nil, nil, false
	}
	a, err := s.Store.EmailAlertByID(r.Context(), p.ID, id)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return nil, nil, false
	} else if err != nil {
		s.serverError(w, r, err)
		return nil, nil, false
	}
	return p, a, true
}

func (s *Server) emailAlertDelete(w http.ResponseWriter, r *http.Request) {
	p, a, ok := s.loadEmailAlert(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeleteEmailAlert(r.Context(), p.ID, a.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/projects/"+p.Slug+"/settings?flash=email-removed#email-alerts", http.StatusSeeOther)
}

func (s *Server) emailAlertTest(w http.ResponseWriter, r *http.Request) {
	p, a, ok := s.loadEmailAlert(w, r)
	if !ok {
		return
	}
	code := "off"
	if s.mailOn() {
		err := s.Notify.TestEmail(r.Context(), p, a.Address)
		code, _ = mail.ErrorReason(err)
		if err != nil {
			s.Log.Info("test alert email failed", "project", p.Slug, "to", a.Address, "err", err)
		}
	}
	http.Redirect(w, r, fmt.Sprintf("/projects/%s/settings?flash=email-tested&result=%s#email-alert-%d", p.Slug, code, a.ID), http.StatusSeeOther)
}

// emailResultMessage turns a send result code into the sentence the page
// shows. Only known codes render, so a link can't put text on the page.
func emailResultMessage(code string) map[string]any {
	switch code {
	case "ok":
		return map[string]any{"OK": true, "Text": "Sent. It can take a minute to arrive; check the spam folder if it doesn't."}
	case "off":
		return map[string]any{"Text": "Email isn't set up on this server. An instance admin can set BURROW_SMTP_HOST and the other SMTP settings, then restart Burrow."}
	case "auth":
		return map[string]any{"Text": "The SMTP server refused the username or password. Check BURROW_SMTP_USERNAME and BURROW_SMTP_PASSWORD."}
	case "rejected":
		return map[string]any{"Text": "The SMTP server refused that address. Check it's spelled right and can receive mail."}
	case "timeout":
		return map[string]any{"Text": "The SMTP server didn't answer in time. Check the host and port, and that this server can reach it."}
	case "tls":
		return map[string]any{"Text": "The secure connection to the SMTP server failed. Use BURROW_SMTP_TLS=tls for port 465, or starttls for 587."}
	case "smtp":
		return map[string]any{"Text": "The SMTP server refused the message. Its logs, or Burrow's, have the reason."}
	case "unreachable":
		return map[string]any{"Text": "Couldn't reach the SMTP server. Check BURROW_SMTP_HOST and BURROW_SMTP_PORT."}
	}
	return nil
}
