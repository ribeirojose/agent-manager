package quick

import (
	"strings"
	"testing"

	"github.com/YoanWai/agent-manager/internal/ui/spawn"
	tea "github.com/charmbracelet/bubbletea"
)

// fakePrompt is a one-line prompt box holding typed text.
type fakePrompt struct {
	text     string
	released bool
}

func (p *fakePrompt) Focus()                             {}
func (p *fakePrompt) Blur()                              {}
func (p *fakePrompt) Fit(int, int)                       {}
func (p *fakePrompt) View() string                       { return p.text }
func (p *fakePrompt) StepRow(tea.KeyMsg) (tea.Cmd, bool) { return nil, false }
func (p *fakePrompt) ChipKey(tea.KeyMsg) (tea.Cmd, bool) { return nil, false }
func (p *fakePrompt) Type(msg tea.KeyMsg) tea.Cmd        { p.text += string(msg.Runes); return nil }
func (p *fakePrompt) Release()                           { p.released = true }
func (p *fakePrompt) Pasting() bool                      { return false }
func (p *fakePrompt) Message() string                    { return strings.TrimSpace(p.text) }

// fakeHost drives the quick bar without a root model, on a CLI with no
// choices to pick.
type fakeHost struct {
	prompt   *fakePrompt
	target   Target
	selected bool
	settings map[string]string
	errs     []string
	dialog   *Dialog
}

func (h *fakeHost) ChoiceCaps(string) spawn.Caps        { return spawn.Caps{} }
func (h *fakeHost) ChoiceAnswer(string) *spawn.Answer   { return nil }
func (h *fakeHost) ChoiceSetting(key string) string     { return h.settings[key] }
func (h *fakeHost) SaveChoiceSetting(key, value string) { h.settings[key] = value }
func (h *fakeHost) ReportErr(text string)               { h.errs = append(h.errs, text) }
func (h *fakeHost) ClearErr()                           {}
func (h *fakeHost) Prompt() spawn.Prompt                { return h.prompt }
func (h *fakeHost) Target() (Target, bool)              { return h.target, h.selected }
func (h *fakeHost) Dialog() (Dialog, bool) {
	if h.dialog == nil {
		return Dialog{}, false
	}
	return *h.dialog, true
}

// The bar types into its prompt and hands the root a request for the
// rest: a cursor step, a worktree toggle, an answer, or a spawn.
func TestQuickBarThroughAFakeHost(t *testing.T) {
	h := &fakeHost{prompt: &fakePrompt{}, settings: map[string]string{}}
	q := NewBar(h, []string{"claude"}, 0, false)
	if _, request := q.HandleKey(h, tea.KeyMsg{Type: tea.KeyEnter}); request != (Request{}) || len(h.errs) != 1 {
		t.Fatalf("enter with nothing selected asked for %+v: %v", request, h.errs)
	}
	q.HandleKey(h, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ship it"), Paste: true})
	if _, request := q.HandleKey(h, tea.KeyMsg{Type: tea.KeyUp}); request.Move != -1 {
		t.Fatalf("up on a one-row prompt asked for %+v", request)
	}
	if _, request := q.HandleKey(h, tea.KeyMsg{Type: tea.KeyCtrlT}); !request.Toggle {
		t.Fatalf("ctrl+t asked for %+v", request)
	}
	h.target, h.selected = Target{IsGroup: true, Group: "backend"}, true
	_, request := q.HandleKey(h, tea.KeyMsg{Type: tea.KeyEnter})
	if got := request.Spawn; got == nil || *got != (spawn.Request{Tool: "claude", Group: "backend", Prompt: "ship it"}) {
		t.Fatalf("spawn = %+v", request.Spawn)
	}
	q.SetWorktree(true)
	if _, request = q.HandleKey(h, tea.KeyMsg{Type: tea.KeyEnter}); !request.Spawn.WorktreeSet || !request.Spawn.Worktree {
		t.Fatalf("a toggled worktree was not asked for: %+v", request.Spawn)
	}
	if q.Legend(h)[0] != [2]string{"↵", "send"} {
		t.Fatalf("legend = %v", q.Legend(h))
	}
	h.target = Target{Group: "backend"}
	_, request = q.HandleKey(h, tea.KeyMsg{Type: tea.KeyEnter})
	if request != (Request{Send: true, Text: "ship it"}) {
		t.Fatalf("send = %+v", request)
	}
	if _, request = q.HandleKey(h, tea.KeyMsg{Type: tea.KeyEsc}); request != (Request{Close: true}) || !h.prompt.released {
		t.Fatalf("esc asked for %+v", request)
	}
}

// A session waiting on a numbered choice takes its digit, enter on an empty
// prompt chooses what the pane's own enter would, and typed text, which
// would land in the dialog, is refused.
func TestQuickBarAnswersADialog(t *testing.T) {
	h := &fakeHost{prompt: &fakePrompt{}, settings: map[string]string{}, target: Target{Group: "backend"}, selected: true}
	h.dialog = &Dialog{Question: "Cats or dogs?", Options: []string{"Cats", "Dogs", "Chat about this"}, Selected: 1}
	q := NewBar(h, []string{"claude"}, 0, false)
	digit := func(key string) Request {
		_, request := q.HandleKey(h, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		return request
	}
	if request := digit("3"); request != (Request{Choose: 3}) {
		t.Fatalf("3 asked for %+v", request)
	}
	if request := digit("4"); request != (Request{}) || h.prompt.text != "4" {
		t.Fatalf("a digit past the options asked for %+v and typed %q", request, h.prompt.text)
	}
	if request := digit("1"); request != (Request{}) || h.prompt.text != "41" {
		t.Fatalf("a digit into a draft asked for %+v and typed %q", request, h.prompt.text)
	}
	if _, request := q.HandleKey(h, tea.KeyMsg{Type: tea.KeyEnter}); request != (Request{}) || len(h.errs) != 1 {
		t.Fatalf("enter with text on a dialog asked for %+v: %v", request, h.errs)
	}
	h.prompt.text = ""
	if _, request := q.HandleKey(h, tea.KeyMsg{Type: tea.KeyEnter}); request != (Request{Choose: 2}) {
		t.Fatalf("enter on an empty prompt asked for %+v", request)
	}
	if legend := q.Legend(h); legend[0] != [2]string{"1-3", "choose"} {
		t.Fatalf("legend = %v", legend)
	}
	h.target = Target{IsGroup: true, Group: "backend"}
	if request := digit("1"); request.Choose != 0 {
		t.Fatalf("a digit on a group asked for %+v", request)
	}
}

// Each option the bar paints under the question is a stretch a click
// chooses.
func TestQuickBarDialogOptionsAreClickable(t *testing.T) {
	h := &fakeHost{prompt: &fakePrompt{}, settings: map[string]string{}, target: Target{Group: "backend"}, selected: true}
	h.dialog = &Dialog{Question: "Cats or dogs?", Options: []string{"Cats", "Dogs"}}
	q := NewBar(h, []string{"claude"}, 0, false)
	lines := strings.Split(q.View(h, Facts{Answer: "answer│ agent", LabelWidth: 8}, 60, 8), "\n")
	for i, option := range h.dialog.Options {
		line := 2 + i
		if !strings.Contains(lines[line], option) {
			t.Fatalf("line %d = %q, want option %q", line, lines[line], option)
		}
		hit, ok := q.HitAt(5, line)
		if !ok || hit.Action != ClickOption {
			t.Fatalf("no option stretch on line %d: %+v", line, q.Hits())
		}
		if request := q.Click(h, hit); request != (Request{Choose: i + 1}) {
			t.Fatalf("click on %q asked for %+v", option, request)
		}
	}
	if !strings.Contains(lines[1], "Cats or dogs?") {
		t.Fatalf("question line = %q", lines[1])
	}
}
