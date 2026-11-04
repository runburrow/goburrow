package event

import (
	"fmt"
	"testing"
)

func exc(typ, value string, frames ...Frame) *Event {
	return &Event{Exceptions: []Exception{{Type: typ, Value: value, Stacktrace: &Stacktrace{Frames: frames}}}}
}

func inApp(module, fn string, line int) Frame {
	t := true
	return Frame{Module: module, Function: fn, Lineno: line, InApp: &t}
}

func TestGroupingIgnoresLineNumbersAndValues(t *testing.T) {
	a := exc("KeyError", "'A-100'", inApp("shop", "load", 10))
	b := exc("KeyError", "'B-200'", inApp("shop", "load", 14)) // code moved
	if Fingerprint(a) != Fingerprint(b) {
		t.Error("same type + same in-app frames should group together")
	}
	c := exc("KeyError", "'A-100'", inApp("shop", "save", 10))
	if Fingerprint(a) == Fingerprint(c) {
		t.Error("different call path should split")
	}
	d := exc("ValueError", "'A-100'", inApp("shop", "load", 10))
	if Fingerprint(a) == Fingerprint(d) {
		t.Error("different type should split")
	}
}

func TestGroupingPrefersInAppFrames(t *testing.T) {
	no := false
	lib := func(fn string) Frame { return Frame{Module: "framework", Function: fn, InApp: &no} }
	a := exc("E", "", lib("dispatch_v1"), inApp("app", "handler", 1))
	b := exc("E", "", lib("dispatch_v2"), inApp("app", "handler", 1))
	if Fingerprint(a) != Fingerprint(b) {
		t.Error("framework upgrades must not split issues when in-app frames match")
	}
}

func TestGroupingJSBundleHashes(t *testing.T) {
	f := func(file string) *Event {
		return exc("TypeError", "x", Frame{Filename: file, Function: "render"})
	}
	if Fingerprint(f("https://cdn.example.com/app.3f9a2c1b.js?v=1")) != Fingerprint(f("https://cdn.example.com/app.77de01aa.js")) {
		t.Error("cache-busting bundle hashes should not split issues")
	}
}

func TestGroupingMessages(t *testing.T) {
	cases := [][2]string{
		{"Payment provider timed out after 3000ms", "Payment provider timed out after 4521ms"},
		{"User 42 not found", "User 97 not found"},
		{"Order 0xdeadbeef failed", "Order 0xcafe failed"},
		{"job 3fa85f64-5717-4562-b3fc-2c963f66afa6 crashed", "job 7c9e6679-7425-40de-944b-e07fc1f90ae7 crashed"},
		{"conn to 10.0.0.1:5432 refused", "conn to 10.0.0.2:5432 refused"},
		{"cache miss for 'user:1'", "cache miss for 'user:2'"},
	}
	for _, c := range cases {
		a, b := &Event{Message: c[0]}, &Event{Message: c[1]}
		if Fingerprint(a) != Fingerprint(b) {
			t.Errorf("%q and %q should group (normalized %q vs %q)", c[0], c[1], NormalizeMessage(c[0]), NormalizeMessage(c[1]))
		}
	}
	if Fingerprint(&Event{Message: "disk full"}) == Fingerprint(&Event{Message: "disk empty"}) {
		t.Error("different messages should split")
	}
}

func TestGroupingUsesTemplate(t *testing.T) {
	a := &Event{Message: "User bob not found", MessageTemplate: "User %s not found"}
	b := &Event{Message: "User alice not found", MessageTemplate: "User %s not found"}
	if Fingerprint(a) != Fingerprint(b) {
		t.Error("message template should drive grouping")
	}
}

func TestCustomFingerprint(t *testing.T) {
	base := exc("E", "v", inApp("m", "f", 1))
	custom := *base
	custom.Fingerprint = []string{"payments"}
	ext := *base
	ext.Fingerprint = []string{"{{ default }}", "tenant-a"}
	ext2 := *base
	ext2.Fingerprint = []string{"{{ default }}", "tenant-b"}

	seen := map[string]string{}
	for name, e := range map[string]*Event{"base": base, "custom": &custom, "ext": &ext, "ext2": &ext2} {
		fp := Fingerprint(e)
		if other, dup := seen[fp]; dup {
			t.Errorf("%s and %s collided", name, other)
		}
		seen[fp] = name
	}
	again := *base
	again.Fingerprint = []string{"{{default}}"}
	if Fingerprint(&again) != Fingerprint(base) {
		t.Error(`["{{default}}"] should equal the default grouping`)
	}
}

func ExampleNormalizeMessage() {
	fmt.Println(NormalizeMessage("Request 8812 to 10.1.2.3:80 took 3000ms at 2026-11-04T10:00:00Z"))
	// Output: Request <n> to <ip> took <n>ms at <time>
}
