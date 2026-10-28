package ui

import (
	"context"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/btelolabs/btelo-forge/internal/files"
	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// promptKind is what a file manager prompt asks for.
type promptKind int

const (
	promptJump     promptKind = iota // a folder visited, found by a few letters
	promptRename                     // a new name for the item under the cursor
	promptNew                        // the name of a new file or folder
	promptPath                       // a folder or file to go to, typed or pasted where the list's folder is shown
	promptTag                        // a tag to give the items the commands act on
	promptCategory                   // the name of a new category
	promptLabel                      // a new name for a tag or a category
)

// filesPrompt is a line the file manager asks the user to type.
type filesPrompt struct {
	kind    promptKind
	text    string
	matches []string // the folders matching, best first, for a jump; the tags in use matching, for a tag
	pick    int      // the match chosen
	target  string   // the item renamed, or the tag or category
	// paths are the items a tag is given to; category says whether a label renamed is a category.
	paths    []string
	category bool
}

// promptID is the prompt's text box.
const promptID = "files/prompt"

// openPrompt asks for a folder to jump to, a new name, or the name of a new item.
func (a *App) openPrompt(fd *filesDoc, kind promptKind) {
	u := &a.u
	p := fd.pane()
	pr := &filesPrompt{kind: kind}
	switch kind {
	case promptJump:
		pr.matches = a.fileHistory().Match("", time.Now())
	case promptRename:
		e := p.current()
		if a.noDrives(p) || p.search != nil || e == nil {
			return
		}
		if job := a.busyJob(p.path(e)); job != nil {
			a.toastOnly("files/busy", u.tf("%s is being worked on: %s", e.Name, job.title))
			return
		}
		pr.text, pr.target = e.Name, p.path(e)
	case promptPath:
		a.closeSearch(p)
		pr.text = p.dir
	case promptNew:
		if a.noDrives(p) || p.search != nil {
			return
		}
		if job := a.busyJob(p.dir); job != nil {
			a.toastOnly("files/busy", u.tf("%s is being worked on: %s", filepath.Base(p.dir), job.title))
			return
		}
	case promptTag:
		if a.noDrives(p) {
			return
		}
		pr.paths = a.actedOn(fd)
		if len(pr.paths) == 0 {
			return
		}
		pr.matches = a.tagMatches("", pr.paths)
		pr.pick = -1
	}
	fd.prompt = pr
	u.beginEdit(promptID, pr.text, false, utf8.RuneCountInString(pr.text), utf8.RuneCountInString(pr.text))
	if kind == promptRename {
		// The name is chosen without its extension, as Explorer does, so typing replaces just the name.
		stem := strings.TrimSuffix(pr.text, filepath.Ext(pr.text))
		if stem == "" {
			stem = pr.text
		}
		u.edit.anchor, u.edit.caret = 0, utf8.RuneCountInString(stem)
	}
	u.edit.anchor = min(u.edit.anchor, u.edit.caret)
	switch kind {
	case promptPath:
		u.edit.anchor = 0 // all of it chosen, so a path pasted replaces it
	case promptJump, promptNew, promptTag, promptCategory:
		u.edit.anchor = u.edit.caret
	}
}

// openLabelRename asks for a new name for a category, or a tag.
func (a *App) openLabelRename(fd *filesDoc, category bool, name string) {
	fd.prompt = &filesPrompt{kind: promptLabel, text: name, target: name, category: category}
	a.u.focusEdit(promptID, name, false)
}

// closePrompt ends the prompt without doing anything.
func (a *App) closePrompt(fd *filesDoc) {
	fd.prompt = nil
	if a.u.focus == promptID {
		a.u.focus = ""
	}
}

// promptKeys takes the keys that answer the prompt before its text box sees them: Enter, Esc, and for a jump the
// keys that choose among the folders.
func (a *App) promptKeys(fd *filesDoc) {
	u := &a.u
	pr := fd.prompt
	var rest []Key
	for _, k := range u.keys {
		switch {
		case k.Code == keyEnter:
			a.answerPrompt(fd)
			u.keys = nil
			return
		case k.Code == keyEscape:
			a.closePrompt(fd)
			u.keys = nil
			return
		case listed(pr.kind) && (k.Code == keyDown || k.Ctrl && (k.Code == 'J' || k.Code == 'N')):
			pr.pick = min(pr.pick+1, max(len(pr.matches)-1, 0))
		case listed(pr.kind) && (k.Code == keyUp || k.Ctrl && (k.Code == 'K' || k.Code == 'P')):
			pr.pick = max(pr.pick-1, min(0, pr.pick))
		case k.Code == keyTab:
		default:
			rest = append(rest, k)
		}
	}
	u.keys = rest
}

// listed reports whether a prompt offers a list to choose from under its box: folders visited, or tags in use.
func listed(kind promptKind) bool { return kind == promptJump || kind == promptTag }

// answerPrompt does what the prompt asked for with what was typed.
func (a *App) answerPrompt(fd *filesDoc) {
	u := &a.u
	pr := fd.prompt
	p := fd.pane()
	a.closePrompt(fd)
	switch pr.kind {
	case promptJump:
		if pr.pick >= len(pr.matches) {
			return
		}
		dir := pr.matches[pr.pick]
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			a.fileHistory().Forget(dir)
			a.fm.historyDirty = true
			a.toast(u.tf("%s is no longer there", dir), true)
			return
		}
		a.goTo(p, dir, "")
	case promptRename:
		name := strings.TrimSpace(pr.text)
		if name == "" || name == filepath.Base(pr.target) {
			return
		}
		to, err := files.Rename(pr.target, name)
		if err != nil {
			a.toast(u.tf("%s could not be renamed: %v", filepath.Base(pr.target), a.describeFileError(err)), true)
			return
		}
		a.pushUndo(files.Action{Kind: files.Renamed, Pairs: []files.Pair{{From: pr.target, To: to}}},
			u.tf("Renaming “%s” to “%s”", filepath.Base(pr.target), name))
		a.renameInAnalyses(pr.target, to)
		a.libraryErr(a.assetLibrary().Move(pr.target, to))
		a.assetLibVersion++
		a.fm.clip.paths = renamedPaths(a.fm.clip.paths, pr.target, to)
		p.land = name
		a.refreshPanes([]string{filepath.Dir(to)})
	case promptPath:
		a.goToTyped(p, pr.text)
	case promptNew:
		name := strings.TrimSpace(pr.text)
		if name == "" {
			return
		}
		path, err := files.Create(p.dir, name)
		if err != nil {
			a.toast(u.tf("%s could not be made: %v", name, a.describeFileError(err)), true)
			return
		}
		a.pushUndo(files.Action{Kind: files.Copied, Pairs: []files.Pair{{To: path}}}, u.tf("Making “%s”", name))
		if parent := a.analysis(p.dir); parent != nil {
			if info, err := os.Stat(path); err == nil && info.IsDir() {
				parent.Attach(&files.Node{Name: name, Mod: info.ModTime()})
			} else {
				parent.Grow(0, 1)
			}
		}
		p.land = name
		a.refreshPanes([]string{p.dir})
	case promptTag:
		name := strings.TrimSpace(pr.text)
		if pr.pick >= 0 && pr.pick < len(pr.matches) {
			name = pr.matches[pr.pick]
		}
		if name != "" {
			a.addTag(pr.paths, name)
		}
	case promptCategory:
		if strings.TrimSpace(pr.text) == "" {
			return
		}
		if _, err := a.assetLibrary().NewCategory(pr.text); err != nil {
			a.toast(a.describe(err), true)
		}
		a.assetLibVersion++
	case promptLabel:
		name := strings.Join(strings.Fields(pr.text), " ")
		if name == "" || name == pr.target {
			return
		}
		lib := a.assetLibrary()
		var err error
		what := showTag
		if pr.category {
			err, what = lib.RenameCategory(pr.target, name), showCategory
		} else {
			err = lib.RenameTag(pr.target, name)
		}
		if err != nil {
			a.toast(a.describe(err), true)
			return
		}
		a.assetLibVersion++
		a.renameFilter(what, pr.target, name)
	}
}

// goToTyped goes to a path typed or pasted in the list's bar: a folder, or a file's folder with the cursor on it.
// The quotes Explorer's Copy as path adds are dropped, %VARIABLES% and ~ are filled in, and a relative path is from
// the list's folder.
func (a *App) goToTyped(p *filesPane, text string) {
	path := expandPath(text)
	if path == "" || files.SamePath(path, p.dir) {
		return
	}
	if !filepath.IsAbs(path) && p.dir != thisPC {
		path = filepath.Join(p.dir, path)
	}
	info, err := os.Stat(path)
	switch {
	case err != nil:
		a.toast(a.u.tf("%s cannot be found", path), true)
	case info.IsDir():
		a.goTo(p, path, "")
	default:
		a.goTo(p, filepath.Dir(path), filepath.Base(path))
	}
}

// expandPath is a path as typed or pasted, made ready to use: trimmed of spaces and the quotes around it, with
// environment variables written %NAME% or $NAME, and ~ for the home folder, filled in.
func expandPath(text string) string {
	path := strings.TrimSpace(text)
	if len(path) >= 2 && (path[0] == '"' && path[len(path)-1] == '"' || path[0] == '\'' && path[len(path)-1] == '\'') {
		path = strings.TrimSpace(path[1 : len(path)-1])
	}
	path = percentVar.ReplaceAllStringFunc(path, func(m string) string {
		if v, ok := os.LookupEnv(m[1 : len(m)-1]); ok {
			return v
		}
		return m
	})
	path = os.Expand(path, func(name string) string {
		if v, ok := os.LookupEnv(name); ok {
			return v
		}
		return "$" + name
	})
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			path = home + path[1:]
		}
	}
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

// percentVar is an environment variable as Windows writes it, %NAME%.
var percentVar = regexp.MustCompile(`%[A-Za-z_][A-Za-z0-9_()]*%`)

// renameInAnalyses gives an analysed folder its new name.
func (a *App) renameInAnalyses(from, to string) {
	for _, t := range a.fm.trees {
		if files.SamePath(t.Path(), from) {
			t.Name = to
			return
		}
		if n := t.Find(from); n != nil {
			n.Name = filepath.Base(to)
			return
		}
	}
}

// renamedPaths are paths with an item renamed, and what is inside it.
func renamedPaths(paths []string, from, to string) []string {
	for i, p := range paths {
		if files.Within(p, from) {
			rel, _ := filepath.Rel(from, p)
			paths[i] = filepath.Join(to, rel)
		}
	}
	return paths
}

// drawRenameBox is the new name being typed, in its row.
func (a *App) drawRenameBox(fd *filesDoc, r gfx.Rect) {
	if _, done := a.u.textBox(promptID, r, &fd.prompt.text, "", false); done && fd.prompt != nil {
		a.closePrompt(fd) // a click elsewhere
	}
}

// drawPromptBox is a prompt over the lists: a title, the text box, and for a jump the folders that match.
func (a *App) drawPromptBox(fd *filesDoc, lists gfx.Rect) {
	u := &a.u
	pr := fd.prompt
	title, placeholder := u.t("Jump to a folder visited"), u.t("Type part of its name")
	switch pr.kind {
	case promptNew:
		title, placeholder = u.t("New file or folder"), u.t("A name with an extension makes a file, without one a folder")
	case promptTag:
		title, placeholder = u.tf("Tag %s", a.countFiles(pr.paths)), u.t("Type a new tag, or pick one in use")
	case promptCategory:
		title, placeholder = u.t("New category"), u.t("Category name")
	case promptLabel:
		title, placeholder = u.tf("Rename the tag %s", pr.target), pr.target
		if pr.category {
			title = u.tf("Rename the category %s", pr.target)
		}
	}
	rowH := u.px(30)
	shown := 0
	if listed(pr.kind) {
		shown = min(len(pr.matches), 10)
	}
	w := min(u.px(640), lists.W-u.px(32))
	h := u.px(12+22+8+36+12) + float32(shown)*rowH
	if pr.kind == promptJump && shown == 0 {
		h += rowH
	}
	box := gfx.Rect{X: lists.X + (lists.W-w)/2, Y: lists.Y + u.px(56), W: w, H: h}
	u.c.Outline(box.Inset(-u.px(2)), u.px(6), gfx.Alpha(colBlack, 0.4))
	panel(u, box)
	in := box.Inset(u.px(12))
	u.label(u.bold(13), rectAt(in.X, in.Y, in.W, u.px(22)), title, colText, 0)
	field := rectAt(in.X, in.Y+u.px(30), in.W, u.px(36))
	changed, done := u.textBox(promptID, field, &pr.text, placeholder, false)
	if done && fd.prompt == pr {
		a.closePrompt(fd) // a click elsewhere
		return
	}
	if !listed(pr.kind) {
		return
	}
	if changed {
		// The box was sized for the matches before this letter; it takes the new ones' size at the next frame,
		// which an edit asks for at once.
		if pr.kind == promptTag {
			pr.matches, pr.pick = a.tagMatches(pr.text, pr.paths), -1
		} else {
			pr.matches, pr.pick = a.fileHistory().Match(pr.text, time.Now()), 0
		}
		shown = min(shown, len(pr.matches))
	}
	y := field.Bottom() + u.px(8)
	if shown == 0 {
		if pr.kind == promptJump {
			u.label(u.face(12.5), rectAt(in.X, y, in.W, rowH), u.t("No folder visited matches"), colFaint, 0)
		}
		return
	}
	if pr.kind == promptTag {
		a.drawTagMatches(fd, pr, rectAt(in.X, y, in.W, float32(shown)*rowH), rowH)
		return
	}
	for i := range shown {
		dir := pr.matches[i]
		row := rectAt(in.X, y+float32(i)*rowH, in.W, rowH)
		id := fmt.Sprintf("files/jump/%d", i)
		if u.hover(id, row) && (u.mx != fd.mouseX || u.my != fd.mouseY || u.pressed) {
			pr.pick = i
		}
		if i == pr.pick {
			u.c.Fill(row, colSelected)
		}
		u.icon(icFmFolder, rectAt(row.X+u.px(4), row.Y, u.px(22), row.H), 15, gfx.Hex(0xe8b75b))
		f := u.face(12.5)
		base := filepath.Base(dir)
		parent := filepath.Dir(dir)
		x := u.c.Text(u.bold(12.5), row.X+u.px(32), row.Y+(row.H-f.Height())/2, base, colText)
		u.c.Text(f, x+u.px(10), row.Y+(row.H-f.Height())/2, ellipsizeStart(f, parent, row.Right()-x-u.px(14)), colFaint)
		if u.clicked(id, row) {
			pr.pick = i
			a.answerPrompt(fd)
			return
		}
	}
}

// filesSearch is a search typed in a list, in place of its folder's listing: by name, or in the files' text.
type filesSearch struct {
	grep    bool
	query   string
	root    string
	focused bool // the text box has the keys; once Enter is pressed they move among what was found
	pick    int
	top     float32

	index   *files.Index
	hits    []files.Hit
	total   int
	asked   string // the query the hits are for
	seen    int    // how many names the index had when the hits were found
	finding bool

	mu      sync.Mutex
	lines   []files.Line
	pending []files.Line
	stats   files.GrepStats
	cancel  context.CancelFunc
	changed time.Time // when the query last changed, for waiting until typing pauses
	ran     string    // the query the content search runs for
	running bool
}

// searchMax is how many results a search keeps.
const searchMax = 5000

// openSearch starts a search by name or by content in the list's folder; pressed again, it goes back to typing.
func (a *App) openSearch(fd *filesDoc, grep bool) {
	p := fd.pane()
	if a.noDrives(p) {
		return
	}
	if p.search != nil && p.search.grep == grep {
		p.search.focused = true
		a.u.focusEdit(a.searchID(fd), p.search.query, false)
		return
	}
	a.closeSearch(p)
	s := &filesSearch{grep: grep, root: p.dir, focused: true}
	p.search = s
	if !grep {
		s.index = files.NewIndex(p.dir)
		ctx, cancel := context.WithCancel(a.life)
		s.cancel = cancel
		hidden := !a.settings.Files.HideHidden
		a.background(func(context.Context) { s.index.Build(ctx, hidden) })
	}
	a.u.focusEdit(a.searchID(fd), "", false)
}

func (a *App) searchID(fd *filesDoc) string { return fmt.Sprintf("files/search/%d", fd.at) }

// closeSearch stops a list's search and shows its folder again.
func (a *App) closeSearch(p *filesPane) {
	if s := p.search; s != nil && s.cancel != nil {
		s.cancel()
	}
	p.search = nil
}

// move moves the choice among what was found.
func (s *filesSearch) move(by int) {
	n := len(s.hits)
	if s.grep {
		n = len(s.lines)
	}
	if n == 0 {
		return
	}
	s.pick = min(max(s.pick+by, 0), n-1)
}

// chosen is the path of what is chosen among what was found, and for a line of text, its number.
func (s *filesSearch) chosen() (string, int) {
	if s.grep {
		if s.pick < len(s.lines) {
			l := s.lines[s.pick]
			return filepath.Join(s.root, l.Rel), l.No
		}
		return "", 0
	}
	if s.pick < len(s.hits) {
		return filepath.Join(s.root, s.hits[s.pick].Rel), 0
	}
	return "", 0
}

// openFound goes to what was found: its folder, with the cursor on it.
func (a *App) openFound(fd *filesDoc, p *filesPane) {
	path, _ := p.search.chosen()
	if path == "" {
		return
	}
	a.closeSearch(p)
	a.goTo(p, filepath.Dir(path), filepath.Base(path))
}

// searchTick keeps a search's results in step with what was typed: names are filtered again as the index grows,
// and the text of the files searched again once typing pauses.
func (a *App) searchTick(p *filesPane) {
	s := p.search
	if !s.grep {
		n := s.index.Len()
		if s.finding || s.query == s.asked && (n == s.seen || time.Since(s.changed) < 150*time.Millisecond && !s.index.Done()) {
			return
		}
		query, ix := s.query, s.index
		s.finding = true
		a.background(func(ctx context.Context) {
			hits, total := ix.Filter(ctx, query, searchMax)
			a.post(func() {
				s.finding = false
				if p.search != s {
					return
				}
				if s.asked != query {
					s.pick, s.top = 0, 0
				}
				s.hits, s.total, s.asked, s.seen = hits, total, query, n
				s.pick = min(s.pick, max(len(hits)-1, 0))
				s.changed = time.Now()
			})
		})
		return
	}
	s.mu.Lock()
	if len(s.pending) > 0 {
		s.lines = append(s.lines, s.pending[:min(len(s.pending), searchMax-len(s.lines))]...)
		s.pending = s.pending[:0]
	}
	full := len(s.lines) >= searchMax
	s.mu.Unlock()
	if full && s.cancel != nil {
		s.cancel()
	}
	if s.query == s.ran || time.Since(s.changed) < 250*time.Millisecond {
		return
	}
	if s.cancel != nil {
		s.cancel()
	}
	s.ran = s.query
	s.mu.Lock()
	s.lines, s.pending, s.pick, s.top = nil, nil, 0, 0
	s.mu.Unlock()
	s.stats.Files.Store(0)
	if s.query == "" {
		s.running = false
		return
	}
	ctx, cancel := context.WithCancel(a.life)
	s.cancel, s.running = cancel, true
	query, root, hidden := s.query, s.root, !a.settings.Files.HideHidden
	a.background(func(context.Context) {
		files.Grep(ctx, root, query, hidden, &s.stats, func(l files.Line) {
			if ctx.Err() != nil {
				return
			}
			s.mu.Lock()
			s.pending = append(s.pending, l)
			s.mu.Unlock()
		})
		a.post(func() {
			if s.ran == query {
				s.running = false
			}
		})
	})
}

// searching reports whether a search is running whose results arrive without telling.
func (a *App) searching() bool {
	for _, d := range a.docs {
		if d.files == nil {
			continue
		}
		for _, p := range d.files.panes {
			if s := p.search; s != nil && (s.running || s.finding || !s.grep && !s.index.Done()) {
				return true
			}
		}
	}
	return false
}

// drawSearch is a search in a list: the text box where its folder was, and what was found under it.
func (a *App) drawSearch(fd *filesDoc, i int, p *filesPane, head, body gfx.Rect, moved bool) {
	u := &a.u
	s := p.search
	id := fmt.Sprintf("files/search/%d", i)
	u.c.Fill(head, colBackground)
	glyph := icSearch
	placeholder := u.t("Find files by name: type part of it")
	if s.grep {
		placeholder = u.t("Find text in the files: type what to find")
	}
	u.icon(glyph, rectAt(head.X+u.px(8), head.Y, u.px(22), head.H), 16, colAccentHi)
	info := a.searchInfo(s)
	inf := u.face(11.5)
	iw := inf.Measure(info) + u.px(16)
	box := rectAt(head.X+u.px(34), head.Y+u.px(4), head.W-u.px(42)-iw, head.H-u.px(8))
	if s.focused {
		if u.focus != id {
			s.focused = false
		} else {
			a.searchKeys(fd, p)
		}
	}
	if p.search != s {
		return // Esc left the search
	}
	if s.focused || u.focus == id {
		changed, done := u.textBox(id, box, &s.query, placeholder, false)
		if changed {
			s.changed = time.Now()
		}
		if done {
			s.focused = false
		}
	} else {
		bx := box
		u.c.Fill(bx, colSurface)
		u.c.Outline(bx, max(1, u.px(1)), colBorder)
		u.label(u.face(13.5), bx.Inset(u.px(10)), s.query, colText, 0)
		if u.clicked(id+"/box", bx) {
			a.openSearch(fd, s.grep)
		}
	}
	u.label(inf, rectAt(head.Right()-iw, head.Y, iw-u.px(10), head.H), info, colDim, 1)
	a.searchTick(p)

	rowH := u.fileRowH()
	if s.grep {
		rowH = u.px(26)
	}
	n := len(s.hits)
	if s.grep {
		n = len(s.lines)
	}
	visible := int(body.H / rowH)
	if fd.at == i {
		fd.pageRows = visible
	}
	if u.hover(id+"/list", body) && u.wheel != 0 {
		s.top -= u.wheel * 3
		u.wheel = 0
	}
	if !moved || !s.focused { // keep the choice in view after keys moved it
		if c := float32(s.pick); c < s.top {
			s.top = c
		} else if c >= s.top+float32(visible) {
			s.top = c - float32(visible) + 1
		}
	}
	s.top = min(max(s.top, 0), float32(max(n-visible, 0)))
	if n == 0 {
		msg := ""
		switch {
		case s.query == "":
			msg = u.t("Type to search")
		case s.running || s.finding || !s.grep && !s.index.Done():
			msg = u.t("Searching…")
		default:
			msg = u.t("Nothing found")
		}
		u.label(u.face(13), rectAt(body.X+u.px(16), body.Y+u.px(16), body.W-u.px(32), u.px(24)), msg, colDim, 0.5)
		return
	}
	u.c.Push(body)
	first := int(s.top)
	for row := first; row < n && float32(row-first)*rowH < body.H; row++ {
		r := rectAt(body.X, body.Y+float32(row-first)*rowH, body.W, rowH)
		rid := fmt.Sprintf("%s/row/%d", id, row)
		if u.hover(rid, r) && moved && fd.at == i {
			s.pick = row
		}
		if row == s.pick {
			u.c.Fill(r, colSelected)
			u.c.Fill(rectAt(r.X, r.Y, u.px(2), r.H), colAccent)
		}
		if s.grep {
			a.drawGrepRow(s.lines[row], r, s.query)
		} else {
			a.drawFoundRow(s, s.hits[row], r)
		}
		if u.press(rid, r) {
			s.pick = row
			if u.double {
				a.openFound(fd, p)
				u.letGo(rid)
				break
			}
		}
		u.letGo(rid)
	}
	u.c.Pop()
}

// searchKeys takes, while the search's text box has the keys, those that move among the results or go to one.
func (a *App) searchKeys(fd *filesDoc, p *filesPane) {
	u := &a.u
	s := p.search
	var rest []Key
	for _, k := range u.keys {
		switch {
		case k.Code == keyEscape:
			a.closeSearch(p)
			u.focus = ""
			u.keys = nil
			return
		case k.Code == keyEnter:
			// Enter leaves the text box for the results, where hjkl move and Enter goes to the one chosen.
			s.focused = false
			u.focus = ""
			u.keys = nil
			return
		case k.Code == keyDown || k.Ctrl && (k.Code == 'J' || k.Code == 'N'):
			s.move(1)
		case k.Code == keyUp || k.Ctrl && (k.Code == 'K' || k.Code == 'P'):
			s.move(-1)
		default:
			rest = append(rest, k)
		}
	}
	u.keys = rest
}

// searchInfo is how far a search has come.
func (a *App) searchInfo(s *filesSearch) string {
	u := &a.u
	if s.grep {
		s.mu.Lock()
		n := len(s.lines)
		s.mu.Unlock()
		text := u.tf("Found: %s · files searched: %s", countText(int64(n)), countText(s.stats.Files.Load()))
		if s.running {
			text = u.t("Searching…") + " " + text
		}
		return text
	}
	text := u.tf("Found: %s · names read: %s", countText(int64(s.total)), countText(int64(s.index.Len())))
	if !s.index.Done() {
		text = u.t("Reading names…") + " " + text
	}
	return text
}

// drawFoundRow is a name found: its symbol, the folder it is in, dim, and its name.
func (a *App) drawFoundRow(s *filesSearch, h files.Hit, r gfx.Rect) {
	u := &a.u
	e := files.Entry{Name: filepath.Base(h.Rel), Dir: h.Dir}
	path := filepath.Join(s.root, h.Rel)
	icon := rectAt(r.X+u.px(8), r.Y+u.px(2), r.H-u.px(4), r.H-u.px(4))
	if !e.Dir && pictured(e.Name) {
		a.drawFileThumb("files/thumb/"+path, icon, path)
	} else {
		glyph, col := fileGlyph(&e)
		u.icon(glyph, icon, 18, col)
	}
	f := u.face(13)
	x := icon.Right() + u.px(10)
	y := r.Y + (r.H-f.Height())/2
	dir := filepath.Dir(h.Rel)
	nameW := f.Measure(e.Name)
	room := r.Right() - u.px(12) - x
	if dir != "." {
		dirText := f.Ellipsize(dir+string(filepath.Separator), max(room-nameW, room*0.3))
		x = u.c.Text(f, x, y, dirText, colFaint)
	}
	a.drawMarked(f, x, y, r.Right()-u.px(12)-x, e.Name, s.query, colText)
}

// drawGrepRow is a line found: the file and line number, dim, then the line with what was found marked.
func (a *App) drawGrepRow(l files.Line, r gfx.Rect, query string) {
	u := &a.u
	f := u.mono(12)
	y := r.Y + (r.H-f.Height())/2
	where := fmt.Sprintf("%s:%d", filepath.ToSlash(l.Rel), l.No)
	ww := min(f.Measure(where), r.W*0.4)
	x := u.c.Text(f, r.X+u.px(12), y, f.Ellipsize(where, ww), colAccentHi) + u.px(12)
	text := strings.TrimLeft(l.Text, " ")
	at := l.At - (len(l.Text) - len(text))
	room := r.Right() - u.px(12) - x
	// A match far along the line is brought into view.
	if at > 0 && f.Measure(text[:at]) > room*0.6 {
		cut := at
		for cut > 0 && f.Measure(text[cut:at]) < room*0.3 {
			_, size := utf8.DecodeLastRuneInString(text[:cut])
			cut -= size
		}
		text, at = "…"+text[cut:], at-cut+len("…")
	}
	if l.Len > 0 && at >= 0 && at+l.Len <= len(text) {
		x0 := x + f.Measure(text[:at])
		w := f.Measure(text[at : at+l.Len])
		u.c.Fill(gfx.Rect{X: x0, Y: r.Y + u.px(4), W: w, H: r.H - u.px(8)}, gfx.Alpha(colWarning, 0.35))
	}
	u.c.Push(rectAt(x, r.Y, room, r.H))
	u.c.Text(f, x, y, text, colText)
	u.c.Pop()
}

// drawMarked draws text with the words of a query marked where they appear.
func (a *App) drawMarked(f *gfx.Face, x, y, w float32, text, query string, col color.RGBA) {
	text = f.Ellipsize(text, w)
	low := strings.ToLower(text)
	for _, word := range strings.Fields(strings.ToLower(query)) {
		if i := strings.Index(low, word); i >= 0 && len(low) == len(text) {
			x0 := x + f.Measure(text[:i])
			a.u.c.Fill(gfx.Rect{X: x0, Y: y - a.u.px(2), W: f.Measure(text[i : i+len(word)]), H: f.Height() + a.u.px(4)}, gfx.Alpha(colWarning, 0.3))
		}
	}
	a.u.c.Text(f, x, y, text, col)
}

// filesConfirm asks before deleting.
type filesConfirm struct {
	paths   []string
	forever bool // asked with Shift: deleting for good is what Enter does
	canBin  bool // every item is on a drive with a Recycle Bin
	// again is the second question deleting for good asks, once the first was answered: it cannot be undone.
	again bool
}

// askDelete asks before deleting the marked items, or the one under the cursor: to the Recycle Bin, where undo
// can bring them back, or for good.
func (a *App) askDelete(fd *filesDoc, forever bool) {
	p := fd.pane()
	if a.noDrives(p) {
		return
	}
	if p.search != nil {
		return
	}
	paths := p.selected()
	if len(paths) == 0 {
		return
	}
	if job := a.busyAny(paths); job != nil {
		a.toastOnly("files/busy", a.u.tf("%s is being worked on: %s", filepath.Base(paths[0]), job.title))
		return
	}
	c := &filesConfirm{paths: paths, forever: forever, canBin: true}
	for _, path := range paths {
		if !files.CanTrash(path) {
			c.canBin = false
			break
		}
	}
	fd.confirm = c
}

// confirmDelete deletes what the dialog asked about. Deleting for good asks a second time first.
func (a *App) confirmDelete(fd *filesDoc, forever bool) {
	c := fd.confirm
	if (forever || !c.canBin) && !c.again {
		c.again = true
		return
	}
	fd.confirm = nil
	p := fd.pane()
	p.marked = map[string]bool{}
	a.fm.clip.paths = slicesDeleteWithin(a.fm.clip.paths, c.paths)
	if forever || !c.canBin {
		a.startJob(jobDelete, c.paths, "")
		return
	}
	a.startJob(jobRecycle, c.paths, "")
}

// slicesDeleteWithin drops the paths that are, or are in, any of gone.
func slicesDeleteWithin(paths, gone []string) []string {
	var out []string
	for _, p := range paths {
		keep := true
		for _, g := range gone {
			if files.Within(p, g) {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, p)
		}
	}
	return out
}

// confirmKeys answers the dialog: Enter does what it offers first, Shift+Enter deletes for good, Esc cancels.
func (a *App) confirmKeys(fd *filesDoc) {
	u := &a.u
	c := fd.confirm
	for _, k := range u.keys {
		if u.repeated(k) {
			continue // Enter held down answers one question, not both
		}
		switch {
		case k.Code == keyEscape:
			fd.confirm = nil
		case k.Code == keyEnter && c.again:
			a.confirmDelete(fd, true)
		case k.Code == keyEnter && k.Shift:
			a.confirmDelete(fd, true)
		case k.Code == keyEnter:
			a.confirmDelete(fd, c.forever)
		default:
			continue
		}
		break
	}
	u.keys, u.typed = nil, nil
}

// drawConfirm is the dialog that asks before deleting: what will go, how large it is when known, and the choice
// between the Recycle Bin and deleting for good.
func (a *App) drawConfirm(fd *filesDoc, r gfx.Rect) {
	u := &a.u
	c := fd.confirm
	u.c.Fill(r, gfx.Alpha(colBlack, 0.45))
	if c.again {
		a.drawConfirmAgain(fd, r)
		return
	}
	shown := min(len(c.paths), 6)
	w := min(u.px(520), r.W-u.px(32))
	h := u.px(20+26+12+18+20+22+48) + float32(shown)*u.px(24)
	if len(c.paths) > shown {
		h += u.px(24)
	}
	box := rectCentered(r, w, h)
	panel(u, box)
	in := box.Inset(u.px(20))
	title := u.tf("Delete %s?", itemsText(u, len(c.paths), filepath.Base(c.paths[0])))
	u.label(u.bold(15), rectAt(in.X, in.Y, in.W, u.px(26)), title, colText, 0)
	y := in.Y + u.px(38)
	var size int64
	known := true
	for i, path := range c.paths {
		info, err := os.Lstat(path)
		e := files.Entry{Name: filepath.Base(path)}
		if err == nil {
			e.Dir = info.IsDir()
			if e.Dir {
				if n := a.analysis(path); n != nil {
					size += n.Size
				} else {
					known = false
				}
			} else {
				size += info.Size()
			}
		}
		if i < shown {
			glyph, col := fileGlyph(&e)
			u.icon(glyph, rectAt(in.X, y, u.px(20), u.px(24)), 14, col)
			u.label(u.face(13), rectAt(in.X+u.px(28), y, in.W-u.px(28), u.px(24)), e.Name, colText, 0)
			y += u.px(24)
		}
	}
	if len(c.paths) > shown {
		u.label(u.face(12.5), rectAt(in.X+u.px(28), y, in.W, u.px(24)), u.tf("and %d more", len(c.paths)-shown), colDim, 0)
		y += u.px(24)
	}
	y += u.px(8)
	var note string
	switch {
	case known && size > 0:
		note = u.tf("%s in all.", sizeText(size))
	case !known:
		note = u.t("Press i on a folder to see its size first.")
	}
	switch {
	case !c.canBin:
		note += " " + u.t("This drive has no Recycle Bin: deleting is for good and cannot be undone.")
	case c.forever:
		note += " " + u.t("Deleting for good cannot be undone.")
	default:
		note += " " + u.t("The Recycle Bin keeps them: u brings them back.")
	}
	a.paragraph(in.X, y, in.W, strings.TrimSpace(note), 12, colDim)

	by := box.Bottom() - u.px(20) - u.px(34)
	cancel := u.t("Cancel") + "  Esc"
	cw := u.buttonWidth("", cancel)
	if u.button("files/confirm/cancel", rectAt(box.Right()-u.px(20)-cw, by, cw, u.px(34)), "", cancel, ghost, true) {
		fd.confirm = nil
		return
	}
	x := box.Right() - u.px(20) - cw - u.px(8)
	forever := u.t("Delete for good")
	if !c.forever && c.canBin {
		forever += "  Shift+Enter"
	} else {
		forever += "  Enter"
	}
	fw := u.buttonWidth("", forever)
	x -= fw
	if u.button("files/confirm/forever", rectAt(x, by, fw, u.px(34)), "", forever, danger, true) {
		a.confirmDelete(fd, true)
		return
	}
	if c.canBin && !c.forever {
		bin := u.t("Move to the Recycle Bin") + "  Enter"
		bw := u.buttonWidth("", bin)
		x -= bw + u.px(8)
		if u.button("files/confirm/bin", rectAt(x, by, bw, u.px(34)), "", bin, primary, true) {
			a.confirmDelete(fd, false)
		}
	} else if c.canBin {
		bin := u.t("Move to the Recycle Bin")
		bw := u.buttonWidth("", bin)
		x -= bw + u.px(8)
		if u.button("files/confirm/bin", rectAt(x, by, bw, u.px(34)), "", bin, plain, true) {
			a.confirmDelete(fd, false)
		}
	}
}

// ellipsizeStart shortens text to a width from its start, as a path is best shortened, keeping its end.
func ellipsizeStart(f *gfx.Face, text string, w float32) string {
	if f.Measure(text) <= w {
		return text
	}
	rs := []rune(text)
	for i := 1; i < len(rs); i++ {
		if s := "…" + string(rs[i:]); f.Measure(s) <= w {
			return s
		}
	}
	return ""
}

// drawConfirmAgain is the second question before deleting for good: what goes, said once more, that it cannot be
// undone, and Enter to delete or Esc to keep the files.
func (a *App) drawConfirmAgain(fd *filesDoc, r gfx.Rect) {
	u := &a.u
	c := fd.confirm
	w := min(u.px(480), r.W-u.px(32))
	text := u.t("They do not go to the Recycle Bin and cannot be brought back, not even with u.")
	tw := w - u.px(40)
	lines := len(u.face(12.5).Wrap(text, tw))
	h := u.px(20+26+12+20+48) + float32(lines)*u.face(12.5).Height()*1.35
	box := rectCentered(r, w, h)
	panel(u, box)
	u.c.Fill(rectAt(box.X, box.Y, box.W, u.px(3)), colDanger)
	in := box.Inset(u.px(20))
	title := u.tf("Delete %s for good?", itemsText(u, len(c.paths), filepath.Base(c.paths[0])))
	u.label(u.bold(15), rectAt(in.X, in.Y, in.W, u.px(26)), title, colDangerText, 0)
	a.paragraph(in.X, in.Y+u.px(38), tw, text, 12.5, colText)

	by := box.Bottom() - u.px(20) - u.px(34)
	cancel := u.t("Keep them") + "  Esc"
	cw := u.buttonWidth("", cancel)
	if u.button("files/confirm2/cancel", rectAt(box.Right()-u.px(20)-cw, by, cw, u.px(34)), "", cancel, plain, true) {
		fd.confirm = nil
		return
	}
	del := u.t("Delete for good") + "  Enter"
	dw := u.buttonWidth("", del)
	if u.button("files/confirm2/delete", rectAt(box.Right()-u.px(20)-cw-u.px(8)-dw, by, dw, u.px(34)), "", del, danger, true) {
		a.confirmDelete(fd, true)
	}
}
