package status

import (
	"regexp"
	"strings"
)

// Dialog is a numbered choice an agent waits on: its question, its options
// in order, and the one its cursor is on.
type Dialog struct {
	Question string
	Options  []string
	Selected int
}

// Equal reports whether two reads show the same choice with the cursor on
// the same option, which a key sequence built from one needs of the other.
func (d Dialog) Equal(other Dialog) bool {
	if d.Question != other.Question || d.Selected != other.Selected || len(d.Options) != len(other.Options) {
		return false
	}
	for i := range d.Options {
		if d.Options[i] != other.Options[i] {
			return false
		}
	}
	return true
}

// Keys are the tmux key names that move the cursor from the selected option
// to option (counted from 0) and choose it. Arrows rather than an option's
// digit, since what a digit does differs between tools and their dialogs.
func (d Dialog) Keys(option int) []string {
	step, n := "Down", option-d.Selected
	if n < 0 {
		step, n = "Up", -n
	}
	keys := make([]string, 0, n+1)
	for range n {
		keys = append(keys, step)
	}
	return append(keys, "Enter")
}

// Most options a dialog can show and still be answered by one digit key.
const maxDialogOptions = 9

// Rows a dialog's frame may draw under its last option: its footer, a box's
// bottom edge, a spinner saying it waits.
const dialogRowsBelow = 6

// Rows between two options: descriptions under one, and a rule some options
// sit under.
const dialogRowsBetween = 3

var (
	// dialogOption is a row of a numbered choice: an optional box edge, the
	// cursor marker on the selected one, the number and the label.
	dialogOption = regexp.MustCompile(`^[\s│┃]*([❯›●>])?\s*(\d)\.\s+(.*?)\s*[│┃]?\s*$`)
	dialogEdge   = regexp.MustCompile(`^[\s│┃╭╰─┌└]*$`)
)

// Dialog reads the numbered choice a waiting pane shows at its bottom. ok is
// false unless the tool's rules see the pane waiting and its options run
// 1 to n with the cursor on exactly one, so a numbered message an agent or
// the user wrote is never taken for one.
func (e *Engine) Dialog(tool, pane string) (Dialog, bool) {
	if e.TypingHold(tool, pane) != Waiting {
		return Dialog{}, false
	}
	return parseDialog(pane)
}

func parseDialog(pane string) (Dialog, bool) {
	rows := strings.Split(strings.TrimRight(pane, "\n "), "\n")
	last := -1
	for i := len(rows) - 1; i >= 0 && i >= len(rows)-1-dialogRowsBelow; i-- {
		if dialogOption.MatchString(rows[i]) {
			last = i
			break
		}
	}
	if last == -1 {
		return Dialog{}, false
	}
	var options []string
	selected, marks, want, first := -1, 0, -1, -1
	gap := 0
	for i := last; i >= 0; i-- {
		m := dialogOption.FindStringSubmatch(rows[i])
		if m == nil {
			if gap++; gap > dialogRowsBetween {
				break
			}
			continue
		}
		number := int(m[2][0] - '0')
		if want != -1 && number != want {
			break
		}
		gap = 0
		options = append(options, m[3])
		if m[1] != "" {
			marks++
			selected = number - 1
		}
		first, want = i, number-1
		if number == 1 {
			break
		}
	}
	if want != 0 || marks != 1 || len(options) < 2 || len(options) > maxDialogOptions {
		return Dialog{}, false
	}
	for i, j := 0, len(options)-1; i < j; i, j = i+1, j-1 {
		options[i], options[j] = options[j], options[i]
	}
	return Dialog{Question: questionAbove(rows, first), Options: options, Selected: selected}, true
}

// questionAbove is the nearest row above the first option with text in it,
// without the box it may be drawn in.
func questionAbove(rows []string, first int) string {
	for i := first - 1; i >= 0 && i >= first-1-dialogRowsBetween; i-- {
		if dialogEdge.MatchString(rows[i]) {
			continue
		}
		return strings.TrimSpace(strings.Trim(strings.TrimSpace(rows[i]), "│┃"))
	}
	return ""
}
