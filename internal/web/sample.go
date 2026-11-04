package web

import (
	"time"

	"github.com/mozthewah/opensourcedsentryalternative/internal/event"
)

// sampleEvent is what "Send test event" ingests: realistic enough to show
// off every panel of the issue page.
func sampleEvent() *event.Event {
	yes, no := true, false
	now := time.Now().UTC()
	return &event.Event{
		EventID:     event.NewEventID(),
		Timestamp:   now,
		Platform:    "python",
		Level:       "error",
		Environment: "production",
		Release:     "burrow-demo@1.0.0",
		ServerName:  "demo-host-1",
		Transaction: "POST /api/checkout",
		Exceptions: []event.Exception{{
			Type:      "ZeroDivisionError",
			Value:     "division by zero",
			Module:    "builtins",
			Mechanism: &event.Mechanism{Type: "generic", Handled: &no},
			Stacktrace: &event.Stacktrace{Frames: []event.Frame{
				{Filename: "flask/app.py", Function: "dispatch_request", Lineno: 1469, Module: "flask.app", InApp: &no,
					ContextLine: "        return self.ensure_sync(self.view_functions[rule.endpoint])(**view_args)"},
				{Filename: "shop/views.py", Function: "checkout", Lineno: 42, Module: "shop.views", InApp: &yes,
					PreContext:  []string{"@app.post(\"/api/checkout\")", "def checkout():", "    cart = load_cart(request.user)"},
					ContextLine: "    per_item = cart.total / cart.item_count",
					PostContext: []string{"    return charge(cart, per_item)", ""},
					Vars:        map[string]any{"cart": "<Cart id=1337 items=0>"}},
			}},
		}},
		Breadcrumbs: []event.Breadcrumb{
			{Timestamp: now.Add(-3 * time.Second), Category: "navigation", Message: "/cart -> /checkout", Level: "info"},
			{Timestamp: now.Add(-2 * time.Second), Category: "http", Type: "http", Level: "info", Data: map[string]any{"method": "POST", "url": "/api/checkout", "status_code": 500}},
			{Timestamp: now.Add(-1 * time.Second), Category: "query", Message: "SELECT * FROM cart_items WHERE cart_id = 1337", Level: "info"},
		},
		Tags: []event.Tag{{Key: "browser", Value: "Firefox 132"}, {Key: "os", Value: "Linux"}, {Key: "burrow.test", Value: "true"}},
		User: map[string]any{"id": "42", "email": "fox@example.com", "ip_address": "203.0.113.7"},
		Request: map[string]any{
			"method": "POST", "url": "https://shop.example.com/api/checkout",
			"headers": map[string]any{"User-Agent": "Mozilla/5.0 (X11; Linux x86_64; rv:132.0) Firefox/132.0"},
		},
		Contexts: map[string]any{
			"runtime": map[string]any{"name": "CPython", "version": "3.13.0"},
			"os":      map[string]any{"name": "Linux", "version": "6.8"},
		},
		SDK: &event.SDK{Name: "burrow.test-event", Version: "1"},
	}
}
