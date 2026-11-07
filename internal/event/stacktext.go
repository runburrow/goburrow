package event

import (
	"regexp"
	"strconv"
	"strings"
)

// Stack trace text, for reporters that have no structured frames: Roblox
// (debug.traceback and ScriptContext.Error), plain Lua, Python tracebacks
// and V8-style JavaScript stacks. Each line that matches a known shape
// becomes a frame; anything else is ignored.

var (
	// Luau, current format: "ServerScriptService.Shop:42 function buy"
	luauLine = regexp.MustCompile(`^([^\s:][^:]*):(\d+)(?: function (\S.*))?$`)
	// Luau, older format: "Script 'ServerScriptService.Shop', Line 42 - function buy"
	luauOldLine = regexp.MustCompile(`^Script '([^']+)', Line (\d+)(?: - function (\S.*))?$`)
	// Lua 5.x: "shop.lua:42: in function 'buy'" / "in local 'buy'" / "in main chunk"
	luaLine = regexp.MustCompile(`^(.+?):(\d+): in (?:(?:local |method |upvalue |field |function )'?<?([^'>]+)>?'?|main chunk|\?)`)
	// Python: `File "app/shop.py", line 42, in buy`
	pyLine = regexp.MustCompile(`^File "([^"]+)", line (\d+)(?:, in (.+))?$`)
	// V8: "at buy (app/shop.js:42:7)" / "at app/shop.js:42:7"
	jsLine     = regexp.MustCompile(`^at (?:(.+?) \()?(.+?):(\d+):(\d+)\)?$`)
	luauPrefix = regexp.MustCompile(`^[^\s:][^:]*:\d+: `)
)

// ParseStackText turns a stack trace string into frames, oldest call first
// (the Sentry convention). It returns nil when no line is recognised.
func ParseStackText(text string) *Stacktrace {
	var frames []Frame
	newestFirst := true
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "stack traceback:" || line == "Stack Begin" || line == "Stack End" ||
			strings.HasPrefix(line, "Traceback (most recent call last)") {
			if strings.HasPrefix(line, "Traceback") {
				newestFirst = false
			}
			continue
		}
		if f, ok := parseStackLine(line); ok {
			frames = append(frames, f)
		}
	}
	if len(frames) == 0 {
		return nil
	}
	if newestFirst {
		for i, j := 0, len(frames)-1; i < j; i, j = i+1, j-1 {
			frames[i], frames[j] = frames[j], frames[i]
		}
	}
	if len(frames) > 100 {
		frames = frames[len(frames)-100:]
	}
	return &Stacktrace{Frames: frames}
}

func parseStackLine(line string) (Frame, bool) {
	var file, fn string
	var lineno, colno int
	switch {
	case pyLine.MatchString(line):
		m := pyLine.FindStringSubmatch(line)
		file, lineno, fn = m[1], atoi(m[2]), m[3]
	case strings.HasPrefix(line, "at ") && jsLine.MatchString(line):
		m := jsLine.FindStringSubmatch(line)
		fn, file, lineno, colno = m[1], m[2], atoi(m[3]), atoi(m[4])
	case luaLine.MatchString(line):
		m := luaLine.FindStringSubmatch(line)
		file, lineno, fn = m[1], atoi(m[2]), m[3]
	case luauOldLine.MatchString(line):
		m := luauOldLine.FindStringSubmatch(line)
		file, lineno, fn = robloxPath(m[1]), atoi(m[2]), m[3]
	case luauLine.MatchString(line):
		m := luauLine.FindStringSubmatch(line)
		file, lineno, fn = robloxPath(m[1]), atoi(m[2]), m[3]
	default:
		return Frame{}, false
	}
	// Roblox's own scripts are not the game's code.
	inApp := !strings.HasPrefix(file, "CorePackages.") && !strings.HasPrefix(file, "CoreGui.") &&
		!strings.Contains(file, "node_modules") && !strings.Contains(file, "site-packages")
	return Frame{Filename: file, Function: strings.TrimSpace(fn), Lineno: lineno, Colno: colno, InApp: &inApp}, true
}

// robloxPath hides the player's name in client script paths
// ("Players.Ana.PlayerScripts.Hud" becomes "Players.<player>.PlayerScripts.Hud"),
// so one bug hitting many players groups into one issue.
func robloxPath(p string) string {
	if rest, ok := strings.CutPrefix(p, "Players."); ok {
		if _, after, ok := strings.Cut(rest, "."); ok {
			return "Players.<player>." + after
		}
	}
	return p
}

// StripLocationPrefix removes the "Script:42: " prefix Luau and Lua put on
// error messages, since the stack already says where the error happened.
func StripLocationPrefix(msg string) string {
	if loc := luauPrefix.FindString(msg); loc != "" && len(loc) < len(msg) {
		return msg[len(loc):]
	}
	return msg
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
