package core

import (
	"strings"
	"unicode"
)

// splitLabel shortens a workspace label for a key. When the label's first
// word is shared with another visible label (typically the repo name, as in
// "myrepo TICKET-320" / "myrepo 1.0.6"), the title drops it and the word is
// returned as prefix for use as a subtitle.
func splitLabel(label string, all []string) (title, prefix string) {
	first, rest, ok := strings.Cut(strings.TrimSpace(label), " ")
	rest = strings.TrimSpace(rest)
	if !ok || rest == "" {
		return label, ""
	}
	shared := 0
	for _, l := range all {
		if f, _, _ := strings.Cut(strings.TrimSpace(l), " "); f == first {
			shared++
		}
	}
	if shared < 2 {
		return label, ""
	}
	return rest, first
}

func displayTitle(label string, all []string) string {
	t, _ := splitLabel(label, all)
	return t
}

// MeaningfulLines extracts readable lines from terminal text: it drops blank
// lines, box-drawing borders, bare prompts and agent TUI footers.
func MeaningfulLines(text string) []string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimFunc(l, func(r rune) bool { return unicode.IsSpace(r) || isBox(r) })
		if l == "" || isChrome(l) {
			continue
		}
		out = append(out, strings.Join(strings.Fields(l), " "))
	}
	return out
}

func isBox(r rune) bool { return r >= 0x2500 && r <= 0x259F }

func isChrome(l string) bool {
	switch l {
	case "❯", ">", "$", "%", "#", "›":
		return true
	}
	box := 0
	n := 0
	for _, r := range l {
		n++
		if isBox(r) {
			box++
		}
	}
	if box*2 >= n {
		return true
	}
	for _, s := range []string{"esc to interrupt", "? for shortcuts", "⏵⏵", "auto mode on", "shift+tab to cycle"} {
		if strings.Contains(l, s) {
			return true
		}
	}
	return false
}

// InspectLines picks what a long-press shows: an agent's "※ recap:" summary
// when present, else the last two output lines (ignoring the input box).
func InspectLines(lines []string, title string) (line1, line2 string) {
	for i := len(lines) - 1; i >= 0; i-- {
		if rest, ok := strings.CutPrefix(lines[i], "※ recap:"); ok {
			return title + " · recap", strings.TrimSpace(rest)
		}
	}
	var out []string
	for _, l := range lines {
		if !strings.HasPrefix(l, "❯") {
			out = append(out, l)
		}
	}
	switch len(out) {
	case 0:
		return title, "(no output)"
	case 1:
		return title, out[0]
	}
	return out[len(out)-2], out[len(out)-1]
}

// PromptLine picks the line most likely to be the pending question: the last
// one ending in "?", else the last meaningful line.
func PromptLine(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasSuffix(lines[i], "?") {
			return lines[i]
		}
	}
	if len(lines) == 0 {
		return "(no prompt text found)"
	}
	return lines[len(lines)-1]
}
