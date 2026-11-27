//go:build !darwin

package ui

// defaultHotkeys number the actions in the order Settings shows them, Ctrl+Shift with the digit, avoiding the
// combinations other programs often take, such as Ctrl+Alt+R and NVIDIA's Alt+F9. The screenshots saved as files
// and pausing have none: most people want one kind of screenshot at hand, and pausing has its button on the
// recording toolbar.
func defaultHotkeys() hotkeySettings {
	digit := func(d uint16) Hotkey { return Hotkey{Ctrl: true, Shift: true, Key: '0' + d} }
	return hotkeySettings{Window: digit(1), Draw: digit(2), Screenshot: digit(3), Record: digit(4)}
}

func hotkeyRefusal(u *ctx, key string) string {
	return u.tf("%s would get in the way of typing; add Ctrl or Alt, or use F1 to F24", key)
}
