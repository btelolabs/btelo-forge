package assets

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"unicode/utf8"
)

// Library is what the user has said about files: their tags and categories, and the categories made. It is kept in
// one JSON file and written whenever it changes. Files are known by their path: tags follow a file the app renames,
// moves or deletes, and stay with the old path when it is moved outside the app.
type Library struct {
	path string
	data libraryFile
}

type libraryFile struct {
	Version    int              `json:"version"`
	Categories []string         `json:"categories"`
	Items      map[string]*Item `json:"items"`
}

// Item is what is said about one file.
type Item struct {
	Path       string   `json:"path"`
	Tags       []string `json:"tags,omitempty"`
	Categories []string `json:"categories,omitempty"`
}

const libraryVersion = 1

// MaxName is the longest a tag or category name may be, in characters.
const MaxName = 40

// Errors a change can be refused with.
var (
	ErrEmptyName  = errors.New("the name is empty")
	ErrLongName   = errors.New("the name is too long")
	ErrNameExists = errors.New("a category of that name already exists")
	ErrNoCategory = errors.New("no such category")
)

// OpenLibrary reads the library kept at path; a missing file is an empty library, a damaged one an error (the file
// is left as it is, so nothing is lost by writing over it).
func OpenLibrary(path string) (*Library, error) {
	l := &Library{path: path, data: libraryFile{Version: libraryVersion, Items: map[string]*Item{}}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &l.data); err != nil {
		return nil, err
	}
	if l.data.Items == nil {
		l.data.Items = map[string]*Item{}
	}
	return l, nil
}

// save writes the library atomically.
func (l *Library) save() error {
	data, err := json.MarshalIndent(l.data, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return err
	}
	tmp := l.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, l.path)
}

// key is how a path is known: cleaned, and without case where the file system ignores it.
func key(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.ToLower(path)
	}
	return path
}

// cleanName trims a tag or category name and checks it.
func cleanName(name string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	switch {
	case name == "":
		return "", ErrEmptyName
	case utf8.RuneCountInString(name) > MaxName:
		return "", ErrLongName
	}
	return name, nil
}

// Of is what is said about path; the zero Item when nothing is.
func (l *Library) Of(path string) Item {
	if it, ok := l.data.Items[key(path)]; ok {
		return Item{Path: it.Path, Tags: slices.Clone(it.Tags), Categories: slices.Clone(it.Categories)}
	}
	return Item{Path: path}
}

// update changes path's item with fn, dropping it once nothing is said about it, and saves.
func (l *Library) update(path string, fn func(*Item)) error {
	k := key(path)
	it, ok := l.data.Items[k]
	if !ok {
		it = &Item{Path: filepath.Clean(path)}
	}
	fn(it)
	if len(it.Tags) == 0 && len(it.Categories) == 0 {
		delete(l.data.Items, k)
	} else {
		l.data.Items[k] = it
	}
	return l.save()
}

// sameName finds name in list ignoring case, as people mean "Logo" and "logo" to be one tag.
func sameName(list []string, name string) int {
	return slices.IndexFunc(list, func(s string) bool { return strings.EqualFold(s, name) })
}

// AddTag tags the files with name.
func (l *Library) AddTag(paths []string, name string) error {
	name, err := cleanName(name)
	if err != nil {
		return err
	}
	// A tag already used elsewhere keeps the spelling it has there.
	for _, t := range l.Tags() {
		if strings.EqualFold(t.Name, name) {
			name = t.Name
		}
	}
	for _, p := range paths {
		if err := l.update(p, func(it *Item) {
			if sameName(it.Tags, name) < 0 {
				it.Tags = append(it.Tags, name)
			}
		}); err != nil {
			return err
		}
	}
	return nil
}

// RemoveTag takes the tag name off the files.
func (l *Library) RemoveTag(paths []string, name string) error {
	for _, p := range paths {
		if err := l.update(p, func(it *Item) {
			if i := sameName(it.Tags, name); i >= 0 {
				it.Tags = slices.Delete(it.Tags, i, i+1)
			}
		}); err != nil {
			return err
		}
	}
	return nil
}

// Count is a name with how many files carry it.
type Count struct {
	Name  string
	Files int
}

// Tags lists every tag in use with its number of files, by name.
func (l *Library) Tags() []Count {
	counts := map[string]int{}
	spelling := map[string]string{}
	for _, it := range l.data.Items {
		for _, t := range it.Tags {
			k := strings.ToLower(t)
			counts[k]++
			if _, ok := spelling[k]; !ok {
				spelling[k] = t
			}
		}
	}
	out := make([]Count, 0, len(counts))
	for k, n := range counts {
		out = append(out, Count{spelling[k], n})
	}
	slices.SortFunc(out, func(a, b Count) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return out
}

// RenameTag renames a tag on every file that has it, merging it into another tag of the new name.
func (l *Library) RenameTag(old, name string) error {
	name, err := cleanName(name)
	if err != nil {
		return err
	}
	for _, it := range l.data.Items {
		if i := sameName(it.Tags, old); i >= 0 {
			it.Tags = slices.Delete(it.Tags, i, i+1)
			if sameName(it.Tags, name) < 0 {
				it.Tags = slices.Insert(it.Tags, i, name)
			}
		}
	}
	return l.save()
}

// DeleteTag takes a tag off every file.
func (l *Library) DeleteTag(name string) error {
	for k, it := range l.data.Items {
		if i := sameName(it.Tags, name); i >= 0 {
			it.Tags = slices.Delete(it.Tags, i, i+1)
			if len(it.Tags) == 0 && len(it.Categories) == 0 {
				delete(l.data.Items, k)
			}
		}
	}
	return l.save()
}

// Categories lists the categories made, in their order, with how many files each holds.
func (l *Library) Categories() []Count {
	out := make([]Count, len(l.data.Categories))
	for i, c := range l.data.Categories {
		out[i].Name = c
	}
	for _, it := range l.data.Items {
		for _, c := range it.Categories {
			if i := sameName(l.data.Categories, c); i >= 0 {
				out[i].Files++
			}
		}
	}
	return out
}

// NewCategory makes an empty category at the end of the list.
func (l *Library) NewCategory(name string) (string, error) {
	name, err := cleanName(name)
	if err != nil {
		return "", err
	}
	if sameName(l.data.Categories, name) >= 0 {
		return "", ErrNameExists
	}
	l.data.Categories = append(l.data.Categories, name)
	return name, l.save()
}

// RenameCategory renames a category and its files' membership.
func (l *Library) RenameCategory(old, name string) error {
	name, err := cleanName(name)
	if err != nil {
		return err
	}
	i := sameName(l.data.Categories, old)
	if i < 0 {
		return ErrNoCategory
	}
	if j := sameName(l.data.Categories, name); j >= 0 && j != i {
		return ErrNameExists
	}
	l.data.Categories[i] = name
	for _, it := range l.data.Items {
		if k := sameName(it.Categories, old); k >= 0 {
			it.Categories[k] = name
		}
	}
	return l.save()
}

// DeleteCategory removes a category; its files stay where they are.
func (l *Library) DeleteCategory(name string) error {
	i := sameName(l.data.Categories, name)
	if i < 0 {
		return ErrNoCategory
	}
	l.data.Categories = slices.Delete(l.data.Categories, i, i+1)
	for k, it := range l.data.Items {
		if j := sameName(it.Categories, name); j >= 0 {
			it.Categories = slices.Delete(it.Categories, j, j+1)
			if len(it.Tags) == 0 && len(it.Categories) == 0 {
				delete(l.data.Items, k)
			}
		}
	}
	return l.save()
}

// SetCategory puts the files in the category, or takes them out of it.
func (l *Library) SetCategory(paths []string, name string, in bool) error {
	i := sameName(l.data.Categories, name)
	if i < 0 {
		return ErrNoCategory
	}
	name = l.data.Categories[i]
	for _, p := range paths {
		if err := l.update(p, func(it *Item) {
			j := sameName(it.Categories, name)
			switch {
			case in && j < 0:
				it.Categories = append(it.Categories, name)
			case !in && j >= 0:
				it.Categories = slices.Delete(it.Categories, j, j+1)
			}
		}); err != nil {
			return err
		}
	}
	return nil
}

// within reports whether path is dir or inside it, as keys.
func within(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, strings.TrimSuffix(dir, string(filepath.Separator))+string(filepath.Separator))
}

// Forget drops what is said about files that are gone, such as those just deleted, and about the files inside the
// folders among them.
func (l *Library) Forget(paths []string) error {
	_, err := l.Take(paths)
	return err
}

// Take drops what is said about the files and the files inside the folders among them, as Forget does, and
// returns it, so that Put can give it back when they come back from the Recycle Bin.
func (l *Library) Take(paths []string) ([]Item, error) {
	var taken []Item
	for _, p := range paths {
		gone := key(p)
		for k, it := range l.data.Items {
			if within(k, gone) {
				taken = append(taken, Item{Path: it.Path, Tags: slices.Clone(it.Tags), Categories: slices.Clone(it.Categories)})
				delete(l.data.Items, k)
			}
		}
	}
	if len(taken) == 0 {
		return nil, nil
	}
	return taken, l.save()
}

// Put gives back what Take took, joining what was said about the files since.
func (l *Library) Put(items []Item) error {
	if len(items) == 0 {
		return nil
	}
	for _, it := range items {
		k := key(it.Path)
		now, ok := l.data.Items[k]
		if !ok {
			now = &Item{Path: filepath.Clean(it.Path)}
			l.data.Items[k] = now
		}
		for _, t := range it.Tags {
			if sameName(now.Tags, t) < 0 {
				now.Tags = append(now.Tags, t)
			}
		}
		for _, c := range it.Categories {
			if sameName(now.Categories, c) < 0 && slices.ContainsFunc(l.data.Categories, func(s string) bool { return strings.EqualFold(s, c) }) {
				now.Categories = append(now.Categories, c)
			}
		}
		if len(now.Tags) == 0 && len(now.Categories) == 0 {
			delete(l.data.Items, k)
		}
	}
	return l.save()
}

// Move carries what is said about a file to its new path, after it was renamed or moved; a folder's files go with
// it.
func (l *Library) Move(from, to string) error {
	from, to = filepath.Clean(from), filepath.Clean(to)
	fk := key(from)
	moved := map[string]*Item{}
	for k, it := range l.data.Items {
		if p := filepath.Clean(it.Path); within(k, fk) && len(p) >= len(from) {
			it.Path = to + p[len(from):] // the path inside the folder, or nothing for the file itself
			moved[k] = it
		}
	}
	if len(moved) == 0 {
		return nil
	}
	for k := range moved {
		delete(l.data.Items, k)
	}
	for _, it := range moved {
		l.data.Items[key(it.Path)] = it
	}
	return l.save()
}
