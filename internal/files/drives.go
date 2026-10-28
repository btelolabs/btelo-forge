package files

// Drive is a drive the computer has, listed above the drives' own folders: on Windows each letter, as This PC lists
// them.
type Drive struct {
	Path        string // its root, such as C:\
	Name        string // C:
	Label       string // the name the drive was given, if any
	Total, Free int64  // bytes; zero when it cannot be read, as a card reader without a card
}
