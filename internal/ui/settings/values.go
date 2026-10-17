package settings

import (
	"sort"
	"strings"

	uitheme "github.com/YoanWai/agent-manager/internal/ui/theme"
)

// Value is one store write in persist order. Proactive writes the
// coordination setting through the typed store call.
type Value struct {
	Key       string
	Value     string
	Proactive bool
}

// Readback is one committed key's read-back; Err means the read failed and
// the dialog keeps its current state for that key.
type Readback struct {
	Key   string
	Value string
	Err   error
}

// ReadbackKeys are the keys a partial failure reads back, in order; the
// coordination key follows them through its own spelling.
var ReadbackKeys = []string{
	DefaultToolSetting, ThemeSetting, ThemeAutoSetting, DiffLayoutSetting,
	QuickCloseSetting, FocusKeySetting, ArrowStepSetting, ListDensitySetting,
	SessionLayoutSetting, HideHeaderSetting, HideStatsSetting, MouseSetting,
	WorktreeSetting, NotificationsSetting, NotifyFinishedSetting,
	BackgroundSetting, BaseFetchSetting, EditorSetting,
}

// ParseHidden reads the stored hidden-CLI list.
func ParseHidden(raw string) map[string]bool {
	if raw == "" {
		return nil
	}
	hidden := make(map[string]bool)
	for _, part := range strings.Split(raw, ",") {
		name := strings.TrimSpace(part)
		if name != "" {
			hidden[name] = true
		}
	}
	if len(hidden) == 0 {
		return nil
	}
	return hidden
}

// MarkBaseline records the freshly built dialog as the state a save
// compares against.
func (s *State) MarkBaseline() {
	s.Baseline = make(map[string]string)
	for _, value := range s.Values() {
		s.Baseline[value.Key] = value.Value
	}
	hidden := strings.Join(s.HiddenList(), ",")
	s.BaselineHidden = &hidden
}

// ChangedValues is the write list narrowed to the keys the user changed in
// this dialog.
func (s *State) ChangedValues() []Value {
	all := s.Values()
	changed := all[:0:0]
	for _, value := range all {
		if before, ok := s.Baseline[value.Key]; !ok || before != value.Value {
			changed = append(changed, value)
		}
	}
	return changed
}

func (s *State) HiddenAtBaseline(hidden []string) bool {
	return s.BaselineHidden != nil && *s.BaselineHidden == strings.Join(hidden, ",")
}

// AdvanceBaseline moves the baseline to a save's captured values, so a
// dialog left open after it (the update row) writes only later changes.
func (s *State) AdvanceBaseline(values []Value, hidden []string) {
	if s.Baseline == nil {
		s.Baseline = make(map[string]string)
	}
	for _, value := range values {
		s.Baseline[value.Key] = value.Value
	}
	if hidden != nil {
		raw := strings.Join(hidden, ",")
		s.BaselineHidden = &raw
	}
}

// HiddenList is the picker's hidden set in persist order (sorted names,
// comma-joined by the worker).
func (s *State) HiddenList() []string {
	names := make([]string, 0, len(s.CLIHidden))
	for name, on := range s.CLIHidden {
		if on {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// Values copies the dialog's chosen preferences into the ordered write
// list the effect worker persists.
func (s *State) Values() []Value {
	values := make([]Value, 0, 16)
	if len(s.ToolNames) > 0 {
		values = append(values, Value{Key: DefaultToolSetting, Value: s.ToolNames[s.ToolIndex]})
	}
	// With auto-detect on, the picker shows the detected theme; the theme
	// key keeps the manual choice so turning auto off returns to it.
	manualTheme := uitheme.Palettes[s.ThemeIndex].Name
	if s.ThemeAuto {
		manualTheme = s.ManualTheme
	}
	values = append(values, Value{Key: ThemeSetting, Value: manualTheme})
	themeAuto := "off"
	if s.ThemeAuto {
		themeAuto = "on"
	}
	values = append(values, Value{Key: ThemeAutoSetting, Value: themeAuto})
	layout := "split"
	if !s.LayoutSplit {
		layout = "unified"
	}
	values = append(values, Value{Key: DiffLayoutSetting, Value: layout})
	quickClose := "stay"
	if s.QuickCloseSend {
		quickClose = "close"
	}
	values = append(values, Value{Key: QuickCloseSetting, Value: quickClose})
	focusKey := "focus"
	if !s.EnterFocuses {
		focusKey = "attach"
	}
	values = append(values, Value{Key: FocusKeySetting, Value: focusKey})
	arrowStep := "on"
	if !s.ArrowStep {
		arrowStep = "off"
	}
	values = append(values, Value{Key: ArrowStepSetting, Value: arrowStep})
	density := "compact"
	if s.ComfortableRows {
		density = "comfortable"
	}
	values = append(values, Value{Key: ListDensitySetting, Value: density})
	values = append(values, Value{Key: SessionLayoutSetting, Value: SessionLayoutValue(s.FullLayout)})
	hideHeader := "off"
	if s.HideHeader {
		hideHeader = "on"
	}
	values = append(values, Value{Key: HideHeaderSetting, Value: hideHeader})
	hideStats := "off"
	if s.HideStats {
		hideStats = "on"
	}
	values = append(values, Value{Key: HideStatsSetting, Value: hideStats})
	background := "theme"
	if s.TerminalBackground {
		background = "terminal"
	}
	values = append(values, Value{Key: BackgroundSetting, Value: background})
	mouseMode := "on"
	if s.MouseDisabled {
		mouseMode = "off"
	}
	values = append(values, Value{Key: MouseSetting, Value: mouseMode})
	worktreeChoice := "off"
	if s.WorktreeDefault {
		worktreeChoice = "on"
	}
	values = append(values, Value{Key: WorktreeSetting, Value: worktreeChoice})
	baseFetch := "on"
	if !s.BaseFetch {
		baseFetch = "off"
	}
	values = append(values, Value{Key: BaseFetchSetting, Value: baseFetch})
	proactive := "off"
	if s.Proactive {
		proactive = "on"
	}
	values = append(values, Value{Key: CoordinationSetting, Value: proactive, Proactive: true})
	notifications := "off"
	if s.Notifications {
		notifications = "on"
	}
	values = append(values, Value{Key: NotificationsSetting, Value: notifications})
	notifyFinished := "off"
	if s.NotifyFinished {
		notifyFinished = "on"
	}
	values = append(values, Value{Key: NotifyFinishedSetting, Value: notifyFinished})
	values = append(values, Value{Key: EditorSetting, Value: s.Editor.Line()})
	return values
}

// Restore applies committed store values back to a live dialog after a
// partial failure, so the dialog shows what persisted. Keys whose read
// failed keep their current state. editors is the last PATH probe, for the
// editor row the restored line rebuilds.
func (s *State) Restore(restored []Readback, hiddenRaw string, hiddenErr error, editors *EditorsProbe) {
	for _, value := range restored {
		if value.Err != nil {
			continue
		}
		switch value.Key {
		case DefaultToolSetting:
			for i, name := range s.ToolNames {
				if name == value.Value {
					s.ToolIndex = i
					break
				}
			}
		case ThemeSetting:
			s.ThemeIndex = uitheme.Index(value.Value)
			if !s.ThemeAuto {
				s.ManualTheme = value.Value
			}
		case ThemeAutoSetting:
			s.ThemeAuto = value.Value == "on"
		case DiffLayoutSetting:
			s.LayoutSplit = value.Value != "unified"
		case QuickCloseSetting:
			s.QuickCloseSend = value.Value == "close"
		case FocusKeySetting:
			s.EnterFocuses = value.Value != "attach"
		case ArrowStepSetting:
			s.ArrowStep = value.Value != "off"
		case ListDensitySetting:
			s.ComfortableRows = value.Value == "comfortable"
		case SessionLayoutSetting:
			s.FullLayout = value.Value == SessionLayoutValue(true)
		case HideHeaderSetting:
			s.HideHeader = value.Value == "on"
		case HideStatsSetting:
			s.HideStats = value.Value == "on"
		case MouseSetting:
			s.MouseDisabled = value.Value == "off"
		case WorktreeSetting:
			s.WorktreeDefault = value.Value == "on"
		case NotificationsSetting:
			s.Notifications = value.Value != "off"
		case NotifyFinishedSetting:
			s.NotifyFinished = value.Value == "on"
		case CoordinationSetting:
			s.Proactive = value.Value == "on"
		case BackgroundSetting:
			s.TerminalBackground = value.Value == "terminal"
		case BaseFetchSetting:
			s.BaseFetch = value.Value != "off"
		case EditorSetting:
			s.Editor = NewEditorRow(value.Value, editors)
		}
	}
	// An empty committed hidden state is a real state: it clears the
	// dialog's map. Only a failed read leaves the old map in place.
	if hiddenErr == nil {
		s.CLIHidden = ParseHidden(hiddenRaw)
		if s.CLIHidden == nil {
			s.CLIHidden = map[string]bool{}
		}
	}
	// The restored keys now show the store, so they match the baseline;
	// a key whose read failed leaves it, so the next save writes it.
	if s.Baseline == nil {
		s.Baseline = make(map[string]string)
	}
	shown := make(map[string]string)
	for _, value := range s.Values() {
		shown[value.Key] = value.Value
	}
	for _, value := range restored {
		if value.Err != nil {
			delete(s.Baseline, value.Key)
		} else if v, ok := shown[value.Key]; ok {
			s.Baseline[value.Key] = v
		}
	}
	s.BaselineHidden = nil
	if hiddenErr == nil {
		raw := strings.Join(s.HiddenList(), ",")
		s.BaselineHidden = &raw
	}
}
