package ui

import "github.com/YoanWai/agent-manager/internal/store"

// defaultSplitLayout reports whether review mode should open in split
// (side-by-side) layout. Split is the default; a stored "unified" choice
// opts out. A store error is surfaced but still yields the split default.
func (m *Model) defaultSplitLayout() bool {
	chosen, err := m.services.store.Setting(diffLayoutSetting)
	if err != nil {
		m.reportErr("reading diff layout setting: " + err.Error())
		return true
	}
	return chosen != "unified"
}

// storedComfortableRows reads the persisted list density. Compact is the
// default; a stored "comfortable" choice gives every entry a second line.
func storedComfortableRows(st *store.Store) bool {
	chosen, err := st.Setting(listDensitySetting)
	if err != nil {
		return false
	}
	return chosen == "comfortable"
}

// storedFullLayout reads the persisted sessions layout. Split is the
// default; a stored "full" choice gives the rail the whole width.
func storedFullLayout(st *store.Store) bool {
	chosen, err := st.Setting(sessionLayoutSetting)
	if err != nil {
		return false
	}
	return chosen == "full"
}

func storedHideHeader(st *store.Store) bool {
	chosen, err := st.Setting(hideHeaderSetting)
	if err != nil {
		return false
	}
	return chosen == "on"
}

func storedHideStats(st *store.Store) bool {
	chosen, err := st.Setting(hideStatsSetting)
	if err != nil {
		return false
	}
	return chosen == "on"
}

// storedMouseDisabled reads the persisted mouse-reporting choice. On is the
// default; only an explicit "off" gives the rail back to the terminal.
func storedMouseDisabled(st *store.Store) bool {
	chosen, err := st.Setting(mouseSetting)
	if err != nil {
		return false
	}
	return chosen == "off"
}

// storedTerminalBackground reads the background row. A store error is
// surfaced but still yields the painted default.
func (m *Model) storedTerminalBackground() bool {
	chosen, err := m.services.store.Setting(backgroundSetting)
	if err != nil {
		m.reportErr("reading background setting: " + err.Error())
	}
	return chosen == "terminal"
}

// storedBaseFetchOff reads the persisted fetch-on-spawn choice. On is the
// default; only an explicit "off" skips the fetch.
func storedBaseFetchOff(st *store.Store) bool {
	chosen, err := st.Setting(baseFetchSetting)
	if err != nil {
		return false
	}
	return chosen == "off"
}

// enterFocuses reports which key opens a session where. Enter focuses the
// preview and A attaches full screen by default; a stored "attach" choice
// swaps the pair. Cached on the model because the footer reads it every
// frame.
func (m *Model) enterFocuses() bool {
	return m.prefs.focusOnEnter
}

// storedFocusOnEnter reads the persisted key choice. A read failure yields
// the default pairing.
func storedFocusOnEnter(st *store.Store) bool {
	chosen, err := st.Setting(focusKeySetting)
	if err != nil {
		return true
	}
	return chosen != "attach"
}

// storedArrowStep reads the persisted ←→ step choice. On is the default;
// only an explicit "off" turns the pair off.
func storedArrowStep(st *store.Store) bool {
	chosen, err := st.Setting(arrowStepSetting)
	if err != nil {
		return true
	}
	return chosen != "off"
}

func storedNotifications(st *store.Store) bool {
	chosen, err := st.Setting(notificationsSetting)
	if err != nil {
		return true
	}
	return chosen != "off"
}

func storedNotifyFinished(st *store.Store) bool {
	chosen, err := st.Setting(notifyFinishedSetting)
	if err != nil {
		return false
	}
	return chosen == "on"
}

func (m *Model) applyCachedSettingsPrefs() {
	m.prefs.focusOnEnter = m.settings.cache.value(focusKeySetting) != "attach"
	m.prefs.arrowStep = m.settings.cache.value(arrowStepSetting) != "off"
	m.prefs.comfortableRows = m.settings.cache.value(listDensitySetting) == "comfortable"
	m.prefs.fullLayout = m.settings.cache.value(sessionLayoutSetting) == "full"
	m.prefs.hideHeader = m.settings.cache.value(hideHeaderSetting) == "on"
	m.prefs.hideStats = m.settings.cache.value(hideStatsSetting) == "on"
	m.prefs.mouseDisabled = m.settings.cache.value(mouseSetting) == "off"
	m.prefs.terminalBackground = m.settings.cache.value(backgroundSetting) == "terminal"
	m.prefs.baseFetchOff = m.settings.cache.value(baseFetchSetting) == "off"
	m.services.editor = m.settings.cache.value(editorSetting)
}
