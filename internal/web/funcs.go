package web

import (
	"encoding/json"
	"fmt"
	"html/template"
	"path"
	"sort"
	"time"

	"github.com/mozthewah/opensourcedsentryalternative/internal/event"
)

var funcs = template.FuncMap{
	"ago":        ago,
	"datetime":   func(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05 UTC") },
	"isodate":    func(t time.Time) string { return t.UTC().Format(time.RFC3339) },
	"clock":      func(t time.Time) string { return t.UTC().Format("15:04:05") },
	"json":       prettyJSON,
	"reverseExc": reverseExceptions,
	"reverseFr":  reverseFrames,
	"sortedKeys": sortedKeys,
	"stringify":  stringify,
	"list":       func(xs ...string) []string { return xs },
	"deref":      func(b *bool) bool { return b != nil && *b },
	"add":        func(a, b int) int { return a + b },
	"sub":        func(a, b int) int { return a - b },
	"basename":   path.Base,
	"barHeight":  barHeight,
	"maxInt":     maxInt,
	"seq": func(n int) []int {
		s := make([]int, n)
		for i := range s {
			s[i] = i
		}
		return s
	},
	"hasFrames": func(e event.Exception) bool { return e.Stacktrace != nil && len(e.Stacktrace.Frames) > 0 },
	"frameHasContext": func(f event.Frame) bool {
		return f.ContextLine != "" || len(f.PreContext) > 0 || len(f.PostContext) > 0
	},
	"anyInApp": func(e event.Exception) bool {
		if e.Stacktrace == nil {
			return false
		}
		for _, f := range e.Stacktrace.Frames {
			if f.IsInApp() {
				return true
			}
		}
		return false
	},
	"plural": func(n int, one, many string) string {
		if n == 1 {
			return one
		}
		return many
	},
}

func ago(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < 0:
		return "just now"
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo ago", int(d.Hours()/24/30))
	default:
		return fmt.Sprintf("%dy ago", int(d.Hours()/24/365))
	}
}

func prettyJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func stringify(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return t
	case json.Number, float64, int, int64, bool:
		return fmt.Sprint(t)
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

// reverseExceptions shows the raised exception first, root cause last.
func reverseExceptions(in []event.Exception) []event.Exception {
	out := make([]event.Exception, len(in))
	for i, e := range in {
		out[len(in)-1-i] = e
	}
	return out
}

// reverseFrames shows the crash site first (most recent call first).
func reverseFrames(st *event.Stacktrace) []event.Frame {
	if st == nil {
		return nil
	}
	out := make([]event.Frame, len(st.Frames))
	for i, f := range st.Frames {
		out[len(st.Frames)-1-i] = f
	}
	return out
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func maxInt(xs []int) int {
	m := 0
	for _, x := range xs {
		if x > m {
			m = x
		}
	}
	return m
}

// barHeight maps a count to a percentage height class (0-100 in steps of 5)
// so the activity chart works under a strict CSP without inline styles.
func barHeight(v, max int) string {
	if max == 0 || v == 0 {
		return "h0"
	}
	pct := (v*100/max + 4) / 5 * 5
	if pct < 5 {
		pct = 5
	}
	return fmt.Sprintf("h%d", pct)
}
