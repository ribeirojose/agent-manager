package settings

import uitheme "github.com/YoanWai/agent-manager/internal/ui/theme"

// Spec is one key Settings writes and the values it takes; the root hands
// the list to the shell as store.SettingSpec.
type Spec struct {
	Key     string
	Values  []string
	Default string
	// List marks a key holding a comma-separated subset of Values.
	List bool
	// Row is the label of the Settings row that steps this key, empty for
	// a key the shell alone sets.
	Row string
}

// Specs is every key Settings writes, so the shell checks a value against
// the same choices the modal steps through. tools are the agent CLIs.
func Specs(tools []string) []Spec {
	palettes := make([]string, len(uitheme.Palettes))
	for i, theme := range uitheme.Palettes {
		palettes[i] = theme.Name
	}
	onOff := []string{"on", "off"}
	return []Spec{
		{Key: DefaultToolSetting, Values: tools, Row: "default tool"},
		{Key: ThemeSetting, Values: palettes, Default: uitheme.Palettes[0].Name, Row: "theme"},
		{Key: ThemeAutoSetting, Values: onOff, Default: "off", Row: "theme follows OS"},
		{Key: BackgroundSetting, Values: []string{"theme", "terminal"}, Default: "theme", Row: "background"},
		{Key: ListDensitySetting, Values: []string{"compact", "comfortable"}, Default: "compact", Row: "list density"},
		{Key: SessionLayoutSetting, Values: []string{"split", "full"}, Default: "split", Row: "sessions layout"},
		{Key: HideHeaderSetting, Values: onOff, Default: "off", Row: "header"},
		{Key: HideStatsSetting, Values: onOff, Default: "off", Row: "computer stats"},
		{Key: DiffLayoutSetting, Values: []string{"split", "unified"}, Default: "split", Row: "review layout"},
		{Key: QuickCloseSetting, Values: []string{"stay", "close"}, Default: "stay", Row: "after quick prompt"},
		{Key: FocusKeySetting, Values: []string{"focus", "attach"}, Default: "focus", Row: "session keys"},
		{Key: ArrowStepSetting, Values: onOff, Default: "on", Row: "←→ step in/out"},
		{Key: MouseSetting, Values: onOff, Default: "on", Row: "mouse"},
		{Key: WorktreeSetting, Values: onOff, Default: "off", Row: "spawn in worktree"},
		{Key: BaseFetchSetting, Values: onOff, Default: "on", Row: "fetch on spawn"},
		{Key: CoordinationSetting, Values: []string{"on-request", "proactive"}, Default: "on-request", Row: "coordination"},
		{Key: NotificationsSetting, Values: onOff, Default: "on", Row: "notifications"},
		{Key: NotifyFinishedSetting, Values: onOff, Default: "off", Row: "notify on finish"},
		{Key: NotifyCommandSetting},
		{Key: EditorSetting, Row: "editor"},
		{Key: HiddenToolsSetting, Values: tools, List: true, Row: "CLIs"},
	}
}
