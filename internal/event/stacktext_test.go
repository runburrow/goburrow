package event

import "testing"

func TestParseStackTextLuau(t *testing.T) {
	// What ScriptContext.Error hands a Roblox server: innermost call first.
	st := ParseStackText("ServerScriptService.Shop.Checkout:42 function buy\nServerScriptService.Shop:10\nCorePackages.Packages.Promise:88 function run\n")
	if st == nil || len(st.Frames) != 3 {
		t.Fatalf("frames = %+v", st)
	}
	crash := st.Frames[len(st.Frames)-1]
	if crash.Filename != "ServerScriptService.Shop.Checkout" || crash.Lineno != 42 || crash.Function != "buy" || !crash.IsInApp() {
		t.Errorf("crash frame = %+v", crash)
	}
	if st.Frames[0].IsInApp() {
		t.Errorf("CorePackages frame should not be in-app: %+v", st.Frames[0])
	}
}

func TestParseStackTextFormats(t *testing.T) {
	cases := []struct {
		name, text, file, fn string
		line                 int
	}{
		{"luau old", "Script 'Workspace.Door Model.Script', Line 7 - function open\nStack End", "Workspace.Door Model.Script", "open", 7},
		{"lua", "stack traceback:\n\tshop.lua:42: in function 'buy'\n\tmain.lua:3: in main chunk", "shop.lua", "buy", 42},
		{"python", "Traceback (most recent call last):\n  File \"main.py\", line 3, in <module>\n  File \"shop.py\", line 42, in buy\nKeyError: 'x'", "shop.py", "buy", 42},
		{"js", "TypeError: x is undefined\n    at buy (app/shop.js:42:7)\n    at main (app/main.js:3:1)", "app/shop.js", "buy", 42},
	}
	for _, c := range cases {
		st := ParseStackText(c.text)
		if st == nil {
			t.Errorf("%s: no frames", c.name)
			continue
		}
		f := st.Frames[len(st.Frames)-1]
		if f.Filename != c.file || f.Function != c.fn || f.Lineno != c.line {
			t.Errorf("%s: crash frame = %+v", c.name, f)
		}
	}
	if ParseStackText("just some words\nno frames here") != nil {
		t.Error("prose should not parse as a stack")
	}
}

func TestStripLocationPrefix(t *testing.T) {
	if got := StripLocationPrefix("ServerScriptService.Shop:42: attempt to index nil with 'Price'"); got != "attempt to index nil with 'Price'" {
		t.Errorf("got %q", got)
	}
	if got := StripLocationPrefix("plain message"); got != "plain message" {
		t.Errorf("got %q", got)
	}
}

func TestParseStackTextGroupsPlayers(t *testing.T) {
	a := ParseStackText("Players.Ana.PlayerScripts.Hud:17 function refresh")
	b := ParseStackText("Players.Bo.PlayerScripts.Hud:17 function refresh")
	if a.Frames[0].Filename != "Players.<player>.PlayerScripts.Hud" || a.Frames[0].Filename != b.Frames[0].Filename {
		t.Errorf("player names should be hidden: %q vs %q", a.Frames[0].Filename, b.Frames[0].Filename)
	}
}
