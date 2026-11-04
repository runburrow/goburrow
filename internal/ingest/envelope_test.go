package ingest

import "testing"

func TestParseEnvelope(t *testing.T) {
	body := []byte("{\"event_id\":\"abc\",\"dsn\":\"https://k@h/1\"}\n" +
		"{\"type\":\"event\",\"length\":13}\n{\"a\":\"\\n\\nb\"}\n" + // length-prefixed, payload contains escaped newlines
		"{\"type\":\"session\"}\n{\"sid\":\"x\"}\n" + // newline-delimited
		"{\"type\":\"attachment\",\"length\":3}\nabc") // no trailing newline
	env, err := ParseEnvelope(body)
	if err != nil {
		t.Fatal(err)
	}
	if env.Header.EventID != "abc" || env.Header.DSN != "https://k@h/1" {
		t.Errorf("header = %+v", env.Header)
	}
	if len(env.Items) != 3 {
		t.Fatalf("items = %d", len(env.Items))
	}
	want := []struct{ typ, payload string }{{"event", `{"a":"\n\nb"}`}, {"session", `{"sid":"x"}`}, {"attachment", "abc"}}
	for i, w := range want {
		if env.Items[i].Type != w.typ || string(env.Items[i].Payload) != w.payload {
			t.Errorf("item %d = %q %q", i, env.Items[i].Type, env.Items[i].Payload)
		}
	}
}

func TestParseEnvelopeErrors(t *testing.T) {
	for _, b := range []string{"", "nope\n", "{}\n{\"type\":\"event\",\"length\":999}\n{}"} {
		if _, err := ParseEnvelope([]byte(b)); err == nil {
			t.Errorf("expected error for %q", b)
		}
	}
}

func TestKeyFromAuthHeader(t *testing.T) {
	for h, want := range map[string]string{
		"Sentry sentry_key=abc, sentry_version=7":                   "abc",
		"Sentry sentry_version=7,sentry_client=x/1,sentry_key=def ": "def",
		"sentry sentry_key=lower":                                   "lower",
		"Bearer abc":                                                "",
		"":                                                          "",
	} {
		if got := keyFromAuthHeader(h); got != want {
			t.Errorf("%q: got %q want %q", h, got, want)
		}
	}
}
