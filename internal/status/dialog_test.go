package status

import (
	"slices"
	"testing"
)

// The panes are the real captures the classification tests hold: claude's
// question dialog (Claude Code v2.1.281), codex's approval modal and
// request-user-input selection, and gemini's boxed tool confirmation.
func TestDialogReadsTheChoiceAWaitingPaneShows(t *testing.T) {
	engine := defaultEngine(t)
	claudeAbove := "❯ Use the AskUserQuestion tool right away to ask me whether I prefer cats or dogs. Nothing else.\n" +
		"  ⎿  8 skills available\n" +
		"────────────\n" +
		" ☐ Pet pref\n" +
		"\n" +
		"Do you prefer cats or dogs?\n" +
		"\n"
	claudeOptions := []string{"Cats", "Dogs", "Type something.", "Chat about this"}
	cases := []struct {
		name, tool, pane string
		want             Dialog
	}{
		{"claude question, first option", "claude",
			claudeAbove + "❯ 1. Cats\n     You prefer cats\n  2. Dogs\n     You prefer dogs\n  3. Type something.\n────────────\n  4. Chat about this\n\nEnter to select · ↑/↓ to navigate · Esc to cancel",
			Dialog{"Do you prefer cats or dogs?", claudeOptions, 0}},
		{"claude question, option under the rule", "claude",
			claudeAbove + "  1. Cats\n     You prefer cats\n  2. Dogs\n     You prefer dogs\n  3. Type something.\n────────────\n❯ 4. Chat about this\n\nEnter to select · ↑/↓ to navigate · Esc to cancel",
			Dialog{"Do you prefer cats or dogs?", claudeOptions, 3}},
		{"claude permission", "claude",
			"Do you want to proceed?\n ❯ 1. Yes\n   2. No, and tell Claude what to do differently",
			Dialog{"Do you want to proceed?", []string{"Yes", "No, and tell Claude what to do differently"}, 0}},
		{"codex approval", "codex",
			"  $ echo hello world\n\n› 1. Yes, proceed (y)\n  2. No, and tell Codex what to do differently (esc)\n\n  Press enter to confirm or esc to cancel",
			Dialog{"$ echo hello world", []string{"Yes, proceed (y)", "No, and tell Codex what to do differently (esc)"}, 0}},
		{"codex request-user-input", "codex",
			"  Choose an option.\n\n  › 1. Option 1  First choice.\n    2. Option 2  Second choice.\n\n  tab to add notes | enter to submit answer | esc to interrupt",
			Dialog{"Choose an option.", []string{"Option 1  First choice.", "Option 2  Second choice."}, 0}},
		{"gemini boxed confirmation", "gemini",
			"╭──────────────────────────────────────╮\n│ Edit example.txt                     │\n│ Apply this change?                   │\n│ ● 1. Allow once                      │\n│   2. Allow always                    │\n│   3. No, suggest changes (esc)       │\n╰──────────────────────────────────────╯\n⡏ Waiting for user confirmation...",
			Dialog{"Apply this change?", []string{"Allow once", "Allow always", "No, suggest changes (esc)"}, 0}},
	}
	for _, tc := range cases {
		got, ok := engine.Dialog(tc.tool, tc.pane)
		if !ok || !got.Equal(tc.want) {
			t.Errorf("%s: Dialog() = %+v ok=%v, want %+v", tc.name, got, ok, tc.want)
		}
	}
}

func TestDialogIgnoresNumberedTextThatIsNotAChoice(t *testing.T) {
	engine := defaultEngine(t)
	cases := []struct{ name, tool, pane string }{
		// 2026-08-23 real capture: a sent numbered message quoting a dialog,
		// while the turn answering it still runs.
		{"claude sent message quoting a dialog", "claude",
			"❯ 1. I think we should make a space for marketing & sales right? 2. lets\n" +
				"  create a local git? the other pane showed:\n\n" +
				"   ❯ 1. Yes\n     2. No\n   Enter to confirm · Esc to cancel\n\n" +
				"· Razzle-dazzling… (12m 25s · ↓ 30.1k tokens)\n\n" +
				"────\n❯ \n────\n  ⏵⏵ auto mode on (shift+tab to cycle)"},
		{"claude question left in a reply", "claude",
			"  We match \" ❯ 1.\" for dialogs. Should I apply it?\n✻ Crunched for 1m 5s\n────\n❯ \n────"},
		{"codex option-shaped draft", "codex",
			"• Working (0s • esc to interrupt)\n\n› 1. Yes, proceed (y)"},
	}
	for _, tc := range cases {
		if got, ok := engine.Dialog(tc.tool, tc.pane); ok {
			t.Errorf("%s: Dialog() = %+v, want none", tc.name, got)
		}
	}
	for name, pane := range map[string]string{
		"two selected":     "Pick one\n❯ 1. A\n❯ 2. B",
		"none selected":    "Pick one\n  1. A\n  2. B",
		"skips a number":   "Pick one\n❯ 1. A\n  3. B",
		"lone option":      "Pick one\n❯ 1. A",
		"options scrolled": "  stale text\n❯ 2. B\n  3. C",
	} {
		if got, ok := parseDialog(pane); ok {
			t.Errorf("%s: parseDialog() = %+v, want none", name, got)
		}
	}
}

func TestDialogKeysStepFromTheSelectedOption(t *testing.T) {
	d := Dialog{Options: []string{"a", "b", "c", "d"}, Selected: 1}
	for option, want := range map[int][]string{
		0: {"Up", "Enter"},
		1: {"Enter"},
		3: {"Down", "Down", "Enter"},
	} {
		if got := d.Keys(option); !slices.Equal(got, want) {
			t.Errorf("Keys(%d) = %v, want %v", option, got, want)
		}
	}
}
