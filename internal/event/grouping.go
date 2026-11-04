package event

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"regexp"
	"strings"
)

// Fingerprint computes the grouping hash for an event. Events with the same
// fingerprint in the same project are rolled up into one issue.
//
// Strategy (in order):
//  1. A client-supplied `fingerprint` wins. "{{ default }}" entries expand to
//     the computed default, so SDK users can extend rather than replace it.
//  2. Exceptions: type + in-app frames (module/file + function). Line numbers
//     are deliberately ignored: they shift on every deploy.
//  3. Exceptions without usable frames: type + normalized value.
//  4. Messages: the normalized message template.
func Fingerprint(e *Event) string {
	def := defaultComponents(e)
	parts := def
	if len(e.Fingerprint) > 0 {
		parts = nil
		for _, f := range e.Fingerprint {
			if isDefaultMarker(f) {
				parts = append(parts, def...)
			} else {
				parts = append(parts, "custom:"+f)
			}
		}
	}
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

func isDefaultMarker(s string) bool {
	s = strings.ReplaceAll(s, " ", "")
	return s == "{{default}}"
}

func defaultComponents(e *Event) []string {
	var parts []string
	hasException := false
	for _, ex := range e.Exceptions {
		if ex.Type == "" && ex.Value == "" && ex.Stacktrace == nil {
			continue
		}
		hasException = true
		frames := groupingFrames(ex.Stacktrace)
		if ex.Type != "" {
			parts = append(parts, "type:"+ex.Type)
		}
		if len(frames) > 0 {
			parts = append(parts, frames...)
		} else {
			parts = append(parts, "value:"+NormalizeMessage(ex.Value))
		}
	}
	if !hasException || (len(parts) == 0) {
		msg := e.MessageTemplate
		if msg == "" {
			msg = e.Message
		}
		parts = append(parts, "message:"+NormalizeMessage(msg))
	} else if e.Exceptions[len(e.Exceptions)-1].Type == "" && e.MessageTemplate != "" {
		// Thread-stack message events: the message is the primary signal.
		parts = append([]string{"message:" + NormalizeMessage(e.MessageTemplate)}, parts...)
	}
	return parts
}

// groupingFrames returns stable identifiers for the frames that matter.
// Prefer in-app frames; if the SDK marked none, use all frames.
func groupingFrames(st *Stacktrace) []string {
	if st == nil {
		return nil
	}
	var inApp, all []string
	for _, f := range st.Frames {
		id := frameIdentity(f)
		if id == "" {
			continue
		}
		all = append(all, id)
		if f.IsInApp() {
			inApp = append(inApp, id)
		}
	}
	if len(inApp) > 0 {
		return inApp
	}
	// Cap so a deep framework stack doesn't make grouping hypersensitive.
	if len(all) > 30 {
		all = all[len(all)-30:]
	}
	return all
}

var hashedBundleRe = regexp.MustCompile(`[.-][0-9a-f]{6,}(\.(?:min\.)?js)$`)

func frameIdentity(f Frame) string {
	loc := f.Module
	if loc == "" {
		loc = f.Filename
		if loc == "" {
			loc = f.AbsPath
		}
		// Strip query strings and cache-busting hashes from JS bundle names
		// (app.3f9a2c1b.js -> app.js) so redeploys don't create new issues.
		if i := strings.IndexAny(loc, "?#"); i >= 0 {
			loc = loc[:i]
		}
		loc = hashedBundleRe.ReplaceAllString(loc, "$1")
		if strings.Contains(loc, "://") {
			loc = path.Base(loc)
		}
	}
	fn := f.Function
	if fn == "?" || fn == "<anonymous>" {
		fn = ""
	}
	if loc == "" && fn == "" {
		return ""
	}
	if fn == "" && f.ContextLine != "" {
		// Anonymous functions: the source line is the best stable signal.
		return "frame:" + loc + "|" + strings.TrimSpace(f.ContextLine)
	}
	return "frame:" + loc + "|" + fn
}

var (
	uuidRe    = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	hexRe     = regexp.MustCompile(`(?i)\b0x[0-9a-f]+\b|\b[0-9a-f]{16,}\b`)
	ipRe      = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}(?::\d+)?\b`)
	isoTimeRe = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?\b`)
	// No trailing \b: numbers glued to units ("3000ms", "512MB") must still
	// be normalized. Digits inside identifiers ("utf8") are replaced too,
	// which is harmless because it's applied consistently.
	numRe    = regexp.MustCompile(`\d+(?:\.\d+)?`)
	quotedRe = regexp.MustCompile(`'[^']{1,200}'|"[^"]{1,200}"`)
)

// NormalizeMessage replaces volatile tokens (ids, numbers, addresses,
// timestamps, quoted values) with placeholders so that "User 42 not found"
// and "User 97 not found" group together.
func NormalizeMessage(s string) string {
	s = firstLine(s)
	s = uuidRe.ReplaceAllString(s, "<uuid>")
	s = isoTimeRe.ReplaceAllString(s, "<time>")
	s = ipRe.ReplaceAllString(s, "<ip>")
	s = hexRe.ReplaceAllString(s, "<hex>")
	s = quotedRe.ReplaceAllString(s, "<str>")
	s = numRe.ReplaceAllString(s, "<n>")
	return s
}
