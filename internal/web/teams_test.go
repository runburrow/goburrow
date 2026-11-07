package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/runburrow/goburrow/internal/event"
	"github.com/runburrow/goburrow/internal/store"
)

// as returns a second browser (own cookie jar) against the same server.
func (h *harness) as() *harness {
	jar, _ := cookiejar.New(nil)
	c := *h
	c.client = &http.Client{Jar: jar}
	c.csrf = ""
	return &c
}

var inviteRe = regexp.MustCompile(`<code id="invite-url">[^<]*/invite/([0-9a-f]+)</code>`)

// invite creates an invite on team as h and returns the token.
func (h *harness) invite(team, role, email string) string {
	h.t.Helper()
	h.get("/teams/" + team)
	code, body := h.post("/teams/"+team+"/invites", url.Values{"role": {role}, "email": {email}})
	m := inviteRe.FindStringSubmatch(body)
	if code != 200 || m == nil {
		h.t.Fatalf("create invite: %d", code)
	}
	return m[1]
}

// join signs a new user up through an invite.
func (h *harness) join(token, email string) {
	h.t.Helper()
	h.get("/invite/" + token)
	if code, body := h.post("/invite/"+token, url.Values{"email": {email}, "password": {"long-enough-pw"}, "password2": {"long-enough-pw"}}); code != 200 {
		h.t.Fatalf("join as %s: %d\n%s", email, code, body)
	}
}

func (h *harness) uid(email string) int64 {
	h.t.Helper()
	users, _ := h.st.ListUsers(context.Background())
	for _, u := range users {
		if u.Email == email {
			return u.ID
		}
	}
	h.t.Fatalf("no user %s", email)
	return 0
}

func TestTeamsAndAccessControl(t *testing.T) {
	admin := newHarness(t)
	admin.post("/setup", url.Values{"email": {"root@x.co"}, "password": {"long-enough-pw"}, "password2": {"long-enough-pw"}, "team": {"Core"}})
	admin.get("/projects/new")
	admin.post("/projects/new", url.Values{"name": {"API"}, "team": {"core"}, "platform": {"go"}})
	p, _ := admin.st.ProjectBySlug(context.Background(), "api")
	ev := &event.Event{EventID: event.NewEventID(), Level: "error", Message: "boom"}
	if err := admin.Ingest(p, ev); err != nil {
		t.Fatal(err)
	}
	issues, _ := admin.st.ListIssues(context.Background(), p.ID, store.IssueFilter{})
	issuePath := fmt.Sprintf("/issues/%d", issues[0].ID)

	// Member joins Core via an email-locked invite.
	memberTok := admin.invite("core", "member", "bob@x.co")
	intruder := admin.as()
	intruder.get("/invite/" + memberTok)
	if code, _ := intruder.post("/invite/"+memberTok, url.Values{"email": {"eve@x.co"}, "password": {"long-enough-pw"}, "password2": {"long-enough-pw"}}); code != 403 {
		t.Errorf("email-locked invite accepted a different address: %d", code)
	}
	member := admin.as()
	member.join(memberTok, "bob@x.co")
	bob := admin.uid("bob@x.co")
	if code, _ := admin.as().get("/invite/" + memberTok); code != 404 {
		t.Errorf("used invite should be dead, got %d", code)
	}

	// Outsider: in a different team entirely.
	admin.post("/teams/new", url.Values{"name": {"Other"}})
	outsider := admin.as()
	outsider.join(admin.invite("other", "owner", ""), "carol@x.co")
	carol := admin.uid("carol@x.co")

	// Members can view and triage.
	for _, path := range []string{"/", "/projects/api", "/projects/api/settings", issuePath, "/teams/core"} {
		if code, _ := member.get(path); code != 200 {
			t.Errorf("member GET %s = %d", path, code)
		}
	}
	member.get(issuePath)
	if code, _ := member.post(issuePath+"/status", url.Values{"status": {"resolved"}}); code != 200 {
		t.Errorf("member resolve = %d", code)
	}
	// ...but not manage. These are 403: they already know the project exists.
	member.get("/projects/api/settings")
	for path, form := range map[string]url.Values{
		"/projects/api/rotate-key":                      {},
		"/projects/api/delete":                          {"confirm": {"api"}},
		issuePath + "/delete":                           {},
		"/teams/core/invites":                           {"role": {"owner"}},
		"/teams/core/delete":                            {"confirm": {"core"}},
		"/teams/core/members/1/remove":                  {},
		fmt.Sprintf("/teams/core/members/%d/role", bob): {"role": {"owner"}}, // self-promotion
	} {
		if code, _ := member.post(path, form); code != 403 {
			t.Errorf("member POST %s = %d, want 403", path, code)
		}
	}
	if role, _ := admin.st.TeamRole(context.Background(), 1, bob); role != store.RoleMember {
		t.Errorf("member managed to change their role to %q", role)
	}
	if code, body := member.get("/projects/new"); code != 200 || !strings.Contains(body, "Only team owners") {
		t.Errorf("member should see the owner-only notice on /projects/new")
	}
	if _, body := member.get(issuePath); strings.Contains(body, `/delete"`) {
		t.Errorf("member should not be shown the delete button")
	}

	// Outsiders get 404 for everything in Core, so names can't be probed.
	outsider.get("/")
	for _, path := range []string{"/projects/api", "/projects/api/settings", issuePath, "/teams/core", "/admin/users"} {
		if code, _ := outsider.get(path); code != 404 {
			t.Errorf("outsider GET %s = %d, want 404", path, code)
		}
	}
	if code, _ := outsider.post(issuePath+"/status", url.Values{"status": {"ignored"}}); code != 404 {
		t.Errorf("outsider triage = %d", code)
	}
	if _, body := outsider.get("/"); strings.Contains(body, "API") {
		t.Errorf("outsider's project list leaks Core's project")
	}
	outsider.get("/projects/new")
	if code, _ := outsider.post("/projects/new", url.Values{"name": {"sneaky"}, "team": {"core"}}); code != 404 {
		t.Errorf("outsider created a project in someone else's team: %d", code)
	}

	// The last owner can't be demoted.
	admin.get("/teams/other")
	// Team "Other" has two owners (root created it, carol was invited as
	// owner). Carol may demote root, but then can't demote herself.
	outsider.get("/teams/other")
	if code, _ := outsider.post("/teams/other/members/1/role", url.Values{"role": {"member"}}); code != 200 {
		t.Errorf("demoting a co-owner = %d, want 200", code)
	}
	if code, _ := outsider.post(fmt.Sprintf("/teams/other/members/%d/role", carol), url.Values{"role": {"member"}}); code != 409 {
		t.Errorf("demoting the last owner = %d, want 409", code)
	}

	// Members can leave.
	member.get("/teams/core")
	member.post(fmt.Sprintf("/teams/core/members/%d/remove", bob), url.Values{})
	if code, _ := member.get("/projects/api"); code != 404 {
		t.Errorf("after leaving, member still sees project: %d", code)
	}

	// Admin page: admins only; can't remove the last admin.
	if code, _ := admin.get("/admin/users"); code != 200 {
		t.Errorf("admin users page = %d", code)
	}
	if code, _ := admin.post("/admin/users/1/admin", url.Values{"admin": {"0"}}); code != 409 {
		t.Errorf("removing last admin = %d, want 409", code)
	}
	admin.get("/admin/users")
	admin.post(fmt.Sprintf("/admin/users/%d/delete", carol), url.Values{}) // no confirmation: refused
	if _, err := admin.st.UserByID(context.Background(), carol); err != nil {
		t.Errorf("deleting a user without confirming should keep them: %v", err)
	}
	admin.post(fmt.Sprintf("/admin/users/%d/delete", carol), url.Values{"confirm": {"delete"}})
	if code, _ := outsider.get("/"); code != 200 || !strings.Contains(outsider.lastBody, "Log in") {
		t.Errorf("deleted user should be logged out")
	}
}

func TestInviteForExistingUser(t *testing.T) {
	h := newHarness(t)
	h.post("/setup", url.Values{"email": {"a@x.co"}, "password": {"long-enough-pw"}, "password2": {"long-enough-pw"}, "team": {"One"}})
	h.get("/teams/new")
	h.post("/teams/new", url.Values{"name": {"Two"}})
	tok := h.invite("two", "member", "")

	b := h.as()
	b.join(h.invite("one", "member", ""), "b@x.co")
	// Signing up again with an existing email points them to log in.
	b2 := h.as()
	b2.get("/invite/" + tok)
	if code, body := b2.post("/invite/"+tok, url.Values{"email": {"b@x.co"}, "password": {"long-enough-pw"}, "password2": {"long-enough-pw"}}); code != 409 || !strings.Contains(body, "Log in first") {
		t.Errorf("existing email signup = %d", code)
	}
	// Signed in, they accept with one click.
	b.get("/invite/" + tok)
	if code, _ := b.post("/invite/"+tok, url.Values{}); code != 200 {
		t.Errorf("accept while signed in = %d", code)
	}
	if code, _ := b.get("/teams/two"); code != 200 {
		t.Errorf("should now be in team two: %d", code)
	}
}

// The routes added alongside teams (webhooks, setup download, bulk triage)
// follow the same access rules as the rest of the project.
func TestTeamAccessOnNewerRoutes(t *testing.T) {
	ctx := context.Background()
	admin := newHarness(t)
	admin.post("/setup", url.Values{"email": {"root@x.co"}, "password": {"long-enough-pw"}, "password2": {"long-enough-pw"}, "team": {"Core"}})
	admin.get("/projects/new")
	admin.post("/projects/new", url.Values{"name": {"Obby"}, "team": {"core"}, "platform": {"roblox"}})
	p, _ := admin.st.ProjectBySlug(ctx, "obby")
	admin.get("/projects/obby/settings")
	admin.post("/projects/obby/webhooks", url.Values{"url": {"https://hooks.example.com/secret-token"}, "on_new": {"1"}})
	hooks, _ := admin.st.ListWebhooks(ctx, p.ID)
	if len(hooks) != 1 {
		t.Fatalf("owner couldn't add a webhook")
	}
	admin.Ingest(p, &event.Event{EventID: event.NewEventID(), Level: "error", Message: "boom"})
	issues, _ := admin.st.ListIssues(ctx, p.ID, store.IssueFilter{})

	member := admin.as()
	member.join(admin.invite("core", "member", "bob@x.co"), "bob@x.co")
	outsider := admin.as()
	admin.post("/teams/new", url.Values{"name": {"Other"}})
	outsider.join(admin.invite("other", "member", "eve@x.co"), "eve@x.co")

	// Members see the setup and the key, but not webhook URLs or owner controls.
	code, body := member.get("/projects/obby/settings")
	if code != 200 || !strings.Contains(body, "BurrowClientError") || !strings.Contains(body, p.PublicKey) {
		t.Fatalf("member settings: %d", code)
	}
	for _, hidden := range []string{"secret-token", "Rotate key", "Delete project", "Add webhook"} {
		if strings.Contains(body, hidden) {
			t.Errorf("member settings shows %q", hidden)
		}
	}
	if !strings.Contains(body, "1 webhook is set up") {
		t.Error("member should see that a webhook exists")
	}
	if code, _ := member.get("/projects/obby/setup/Burrow.lua"); code != 200 {
		t.Errorf("member download: %d", code)
	}
	member.get("/projects/obby/settings")
	if code, _ := member.post("/projects/obby/webhooks", url.Values{"url": {"https://hooks.example.com/x"}, "on_new": {"1"}}); code != 403 {
		t.Errorf("member adding a webhook: %d, want 403", code)
	}
	if code, _ := member.post(fmt.Sprintf("/projects/obby/webhooks/%d/delete", hooks[0].ID), url.Values{}); code != 403 {
		t.Errorf("member removing a webhook: %d, want 403", code)
	}
	// Members triage, including bulk changes and their undo.
	member.get("/projects/obby")
	member.post("/projects/obby/issues/status", url.Values{"status": {"resolved"}, "id": {fmt.Sprint(issues[0].ID)}})
	if is, _ := admin.st.IssueByID(ctx, issues[0].ID); is.Status != store.StatusResolved {
		t.Errorf("member bulk resolve: %s", is.Status)
	}

	// Outsiders get 404 everywhere, so they can't even learn the project exists.
	outsider.get("/")
	for _, path := range []string{"/projects/obby/settings", "/projects/obby/setup/Burrow.lua", "/projects/obby"} {
		if code, _ := outsider.get(path); code != 404 {
			t.Errorf("outsider GET %s: %d, want 404", path, code)
		}
	}
	for _, path := range []string{"/projects/obby/webhooks", "/projects/obby/issues/status", "/projects/obby/issues/restore"} {
		if code, _ := outsider.post(path, url.Values{"status": {"ignored"}, "to": {"resolved"}, "id": {fmt.Sprint(issues[0].ID)}, "was": {fmt.Sprintf("%d.unresolved", issues[0].ID)}}); code != 404 {
			t.Errorf("outsider POST %s: %d, want 404", path, code)
		}
	}
	if is, _ := admin.st.IssueByID(ctx, issues[0].ID); is.Status != store.StatusResolved {
		t.Errorf("outsider changed an issue: %s", is.Status)
	}
}
