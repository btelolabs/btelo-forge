//go:build darwin

package ui

// defaultHotkeys number the actions in the order Settings shows them, Cmd+Shift with the digit on macOS.
// The screenshots saved as files and pausing have none: most people want one kind of screenshot at hand, and
// pausing has its button on the recording toolbar.
func defaultHotkeys() hotkeySettings {
	digit := func(d uint16) Hotkey { return Hotkey{Cmd: true, Shift: true, Key: '0' + d} }
	return hotkeySettings{Window: digit(1), Draw: digit(2), Screenshot: digit(3), Record: digit(4)}
}

func hotkeyRefusal(u *ctx, key string) string {
	return u.tf("%s would get in the way of typing; add Cmd, Ctrl or Alt, or use F1 to F24", key)
}
