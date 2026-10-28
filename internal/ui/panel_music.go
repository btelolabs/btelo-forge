package ui

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/library"
	"github.com/btelolabs/btelo-forge/internal/project"
)

// Choices of the music panel besides the configured sources.
const (
	srcAll   = "all"
	srcLocal = string(library.Local)
)

type musicPanel struct {
	source   string // srcAll, srcLocal or a source id
	query    string
	asked    string // query the remote search last ran with; set when typing ends
	genre    int
	scroll   float32
	adding   map[string]bool
	catalogs map[string][]library.Track
	loading  map[string]bool
	failed   string
	partial  string // sources that failed while others answered
	results  []library.Track
	pages    int
	page     int
	search   string // key of the search the results belong to
	busy     bool
	local    []library.Track
	loaded   bool
	shown    struct {
		source, query, search string
		genre, count, results int
		tracks                []library.Track
	}
}

func (m *musicPanel) init() {
	m.source, m.adding, m.catalogs, m.loading = srcAll, map[string]bool{}, map[string][]library.Track{}, map[string]bool{}
}

// reset forgets everything that came from the sources, after the configuration changed.
func (m *musicPanel) reset() {
	clear(m.catalogs)
	m.failed, m.partial, m.results, m.search, m.page, m.pages, m.genre, m.scroll = "", "", nil, "", 0, 0, 0, 0
	m.shown.tracks = nil
}

func (m *musicPanel) downloading() bool { return len(m.adding) > 0 || m.busy || len(m.loading) > 0 }

func (m *musicPanel) refreshLocal(a *App) {
	m.local = a.lib.LocalTracks()
	m.shown.tracks = nil
}

// musicChoice is one chip of the source row.
type musicChoice struct {
	id     string
	label  string
	source *library.Source // nil for All and My files
}

// musicChoices are All, the enabled music sources, the enabled effect sources and My files.
func (a *App) musicChoices() []musicChoice {
	u := &a.u
	out := []musicChoice{{id: srcAll, label: u.t("All")}}
	enabled := a.lib.Config().Enabled()
	for _, kind := range []string{library.KindMusic, library.KindEffects} {
		for _, s := range enabled {
			if s.Kind == kind {
				out = append(out, musicChoice{id: s.ID, label: s.Name.In(string(u.lang)), source: s})
			}
		}
	}
	return append(out, musicChoice{id: srcLocal, label: u.t("My files")})
}

// currentSource is the configured source picked in the panel, or nil for All and My files.
func (a *App) currentSource() *library.Source {
	if s, ok := a.lib.Config().Find(a.music.source); ok && !s.Disabled {
		return s
	}
	return nil
}

// remote reports whether the current choice is searched over the network when the query is confirmed.
func (a *App) remote() bool {
	if a.music.source == srcAll {
		return true
	}
	s := a.currentSource()
	return s != nil && s.Mode == library.ModeSearch
}

// musicTracks is the list the current source and filters show, rebuilt only when they change.
func (a *App) musicTracks() []library.Track {
	m := &a.music
	count := len(m.catalogs[m.source])
	if m.source == srcLocal {
		count = -len(m.local) - 1
	}
	sh := &m.shown
	if sh.tracks == nil || sh.source != m.source || sh.genre != m.genre || sh.query != m.query || sh.count != count ||
		sh.results != len(m.results) || sh.search != m.search {
		sh.source, sh.genre, sh.query, sh.count, sh.results, sh.search = m.source, m.genre, m.query, count, len(m.results), m.search
		sh.tracks = a.filterTracks()
		if sh.tracks == nil {
			sh.tracks = []library.Track{}
		}
	}
	return sh.tracks
}

func (a *App) filterTracks() []library.Track {
	m := &a.music
	if m.source == srcLocal {
		var out []library.Track
		for _, t := range m.local {
			if library.Matches(t, m.query) {
				out = append(out, t)
			}
		}
		return out
	}
	s := a.currentSource()
	if s == nil || s.Mode == library.ModeSearch {
		return m.results
	}
	genre := ""
	if m.genre > 0 && m.genre <= len(s.Filters) {
		genre = s.Filters[m.genre-1]
	}
	var out []library.Track
	for _, t := range m.catalogs[s.ID] {
		if (genre == "" || t.Genre == genre) && library.Matches(t, m.query) {
			out = append(out, t)
		}
	}
	return out
}

func (a *App) ensureCatalog() {
	m := &a.music
	if !m.loaded {
		m.loaded = true
		m.refreshLocal(a)
	}
	if m.source != srcAll && m.source != srcLocal && a.currentSource() == nil {
		m.source = srcAll
	}
	if s := a.currentSource(); s != nil && s.Mode == library.ModeCatalog && m.catalogs[s.ID] == nil && !m.loading[s.ID] && m.failed == "" {
		m.loading[s.ID] = true
		config, id := a.lib.Config(), s.ID
		a.background(func(ctx context.Context) {
			tracks, err := a.lib.Catalog(ctx, id)
			a.post(func() {
				delete(m.loading, id)
				if a.lib.Config() != config {
					return
				}
				if err != nil {
					m.failed = err.Error()
					return
				}
				m.catalogs[id] = tracks
				m.shown.tracks = nil
				a.rememberTracks(tracks)
			})
		})
	}
	if a.remote() {
		key := fmt.Sprintf("%s|%s", m.source, strings.TrimSpace(m.asked))
		if m.search != key && !m.busy {
			a.runSearch(key, 1)
		}
	}
}

// searchSource runs the remote search for a choice of the panel; it is safe to call from any goroutine.
func (a *App) searchSource(ctx context.Context, source, query string, page int) (library.Page, error) {
	if source == srcAll {
		return a.lib.SearchAll(ctx, library.KindMusic, query, page)
	}
	return a.lib.Search(ctx, source, query, "", page)
}

func (a *App) runSearch(key string, page int) {
	m := &a.music
	m.busy, m.failed, m.partial = true, "", ""
	source, query, config := m.source, strings.TrimSpace(m.asked), a.lib.Config()
	a.background(func(ctx context.Context) {
		res, err := a.searchSource(ctx, source, query, page)
		a.post(func() {
			m.busy = false
			if a.lib.Config() != config {
				return
			}
			m.search = key
			if err != nil && len(res.Tracks) == 0 {
				m.failed = err.Error()
				return
			}
			if err != nil {
				m.partial = err.Error()
			}
			if page == 1 {
				m.results, m.scroll = res.Tracks, 0
			} else {
				m.results = append(m.results, res.Tracks...)
			}
			m.page, m.pages = page, res.Pages
			a.rememberTracks(res.Tracks)
		})
	})
}

func (a *App) drawMusicPanel(r gfx.Rect) {
	u := &a.u
	m := &a.music
	a.ensureCatalog()
	y := r.Y
	if a.sel.kind == selMusic && !a.selectionCard() {
		if k, i, ok := a.p.FindMusic(a.sel.id); ok {
			y = a.drawSelectedMusic(gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(150)}, a.p.Music[k].Clips[i])
		}
	}
	choices := a.musicChoices()
	labels := make([]string, len(choices))
	current := 0
	for i, c := range choices {
		labels[i] = c.label
		if c.id == m.source {
			current = i
		}
	}
	pick, sh := u.flowChips("music/source", gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(28)}, labels, current)
	if pick != current {
		m.source, m.genre, m.scroll, m.failed, m.partial, m.asked = choices[pick].id, 0, 0, "", "", m.query
	}
	src := a.currentSource()
	lang := string(u.lang)
	y += sh + u.px(10)
	sr := gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(34)}
	var placeholder string
	switch {
	case m.source == srcAll:
		placeholder = u.t("Search every library, press Enter")
	case m.source == srcLocal:
		placeholder = u.t("Search imported sounds")
	case src.Hint.In(lang) != "":
		placeholder = src.Hint.In(lang)
	case src.Mode == library.ModeCatalog:
		placeholder = u.t("Search titles, genres or moods")
	case src.Kind == library.KindEffects:
		placeholder = u.t("Search effects, press Enter")
	default:
		placeholder = u.t("Search music, press Enter")
	}
	changed, done := u.textBox("music/query", sr, &m.query, placeholder, false)
	if changed {
		m.scroll = 0
	}
	if done {
		m.asked = m.query
	}
	y += u.px(42)
	switch {
	case src != nil && len(src.Filters) > 0:
		genres := append([]string{u.t("All")}, src.Filters...)
		for i := 1; i < len(genres); i++ {
			genres[i] = u.t(genres[i])
		}
		pick, h := u.flowChips("music/genre", gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(26)}, genres, m.genre)
		if pick != m.genre {
			m.genre, m.scroll = pick, 0
		}
		y += h + u.px(8)
	case m.source == srcLocal:
		if u.button("music/import", gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(32)}, icImport, u.t("Import sound files… (works with YouTube Audio Library downloads)"), plain, true) {
			a.chooseAudio()
		}
		y += u.px(40)
	}
	var note string
	switch {
	case m.source == srcAll:
		var names []string
		for _, c := range choices {
			if c.source != nil && c.source.Kind == library.KindMusic && !c.source.NotInAll {
				names = append(names, c.label)
			}
		}
		note = u.tf("%s together · only licences free for commercial use · credits are written on export", strings.Join(names, ", "))
	case m.source == srcLocal:
		note = u.t("The YouTube Audio Library needs a YouTube Studio sign-in; download there, then import here or drop files on the window")
	default:
		note = src.Note.In(lang)
	}
	if m.partial != "" {
		note = u.tf("Some libraries did not answer: %s", m.partial)
	}
	f := u.face(11)
	lines := f.Wrap(note, r.W)
	lines = lines[:min(len(lines), 4)]
	lineH := f.Height() * 1.15
	footer := float32(len(lines))*lineH + u.px(12)
	a.drawTrackList(gfx.Rect{X: r.X, Y: y, W: r.W, H: r.Bottom() - y - footer})
	for i, l := range lines {
		u.c.Text(f, r.X, r.Bottom()-footer+u.px(8)+float32(i)*lineH, l, colFaint)
	}
}

func (a *App) chooseAudio() {
	var patterns []string
	for _, e := range library.AudioExtensions {
		patterns = append(patterns, "*"+e)
	}
	if files := a.host.OpenFiles(a.u.t("Import sounds"), [][2]string{{a.u.t("Sound files"), strings.Join(patterns, ";")}}, true); len(files) > 0 {
		a.importAudio(files, false)
	}
}

func (a *App) drawTrackList(r gfx.Rect) {
	u := &a.u
	m := &a.music
	tracks := a.musicTracks()
	status := ""
	switch {
	case len(m.loading) > 0 || (m.busy && len(tracks) == 0):
		status = u.t("Loading the library…")
	case m.failed != "":
		status = u.tf("Loading failed: %s", m.failed)
	case len(tracks) == 0 && m.source == srcLocal:
		status = u.t("No imported sounds yet")
	case len(tracks) == 0:
		status = u.t("No matching sounds")
	}
	if status != "" {
		f := u.face(12.5)
		for i, l := range f.Wrap(status, r.W-u.px(20)) {
			u.c.Text(f, r.X+u.px(10), r.Y+u.px(16)+float32(i)*f.Height()*1.2, l, colDim)
		}
		if m.failed != "" && u.button("music/retry", gfx.Rect{X: r.X + u.px(10), Y: r.Y + u.px(70), W: u.px(90), H: u.px(30)}, "", u.t("Retry"), plain, true) {
			m.failed, m.search = "", ""
		}
		return
	}
	rowH := u.px(58)
	more := a.remote() && m.page < m.pages
	content := rowH * float32(len(tracks))
	if more {
		content += u.px(48)
	}
	if u.hover("music/list", r) && u.wheel != 0 {
		m.scroll -= u.wheel * rowH * 1.5
		u.wheel = 0
	}
	m.scroll = min(max(m.scroll, 0), max(content-r.H, 0))
	u.c.Push(r)
	defer u.c.Pop()
	first := int(m.scroll / rowH)
	for i := first; i < len(tracks); i++ {
		row := gfx.Rect{X: r.X, Y: r.Y + float32(i)*rowH - m.scroll, W: r.W, H: rowH - u.px(4)}
		if row.Y > r.Bottom() {
			break
		}
		a.drawTrackRow(row, tracks[i])
	}
	if more {
		mr := gfx.Rect{X: r.X, Y: r.Y + rowH*float32(len(tracks)) - m.scroll + u.px(6), W: r.W, H: u.px(34)}
		label := u.t("Load more")
		if m.busy {
			label = u.t("Loading…")
		}
		if u.button("music/more", mr, "", label, plain, !m.busy) {
			a.runSearch(m.search, m.page+1)
		}
	}
	if content > r.H {
		h := max(r.H*r.H/content, u.px(24))
		y := r.Y + (r.H-h)*m.scroll/(content-r.H)
		u.c.Fill(gfx.Rect{X: r.Right() - u.px(4), Y: y, W: u.px(3), H: h}, colSurfaceHi)
	}
}

func (a *App) drawTrackRow(r gfx.Rect, t library.Track) {
	u := &a.u
	key := t.Key()
	hot := u.hover("row/"+key, r)
	bg := colSurface
	if hot {
		bg = colSurfaceHi
	}
	u.c.Fill(r, bg)
	playing, heard := a.audition.Playing()
	pb := gfx.Rect{X: r.X + u.px(8), Y: r.Y + (r.H-u.px(34))/2, W: u.px(34), H: u.px(34)}
	glyph := icPlay
	if playing == key {
		glyph = icStop
	}
	pbg := colPrimary
	if u.hover("audition/"+key, pb) {
		pbg = colPrimaryHi
		u.cursor = CursorHand
	}
	u.c.Fill(pb, pbg)
	u.icon(glyph, pb, 12, colOnPrimary)
	u.hint("audition/"+key, u.t("Preview"), pb)
	if u.clicked("audition/"+key, pb) {
		a.auditionTrack(t)
	}
	add := gfx.Rect{X: r.Right() - u.px(84), Y: r.Y + (r.H-u.px(30))/2, W: u.px(76), H: u.px(30)}
	textX := pb.Right() + u.px(10)
	textW := add.X - textX - u.px(6)
	u.label(u.bold(13), gfx.Rect{X: textX, Y: r.Y + u.px(7), W: textW, H: u.px(20)}, t.Title, colText, 0)
	var meta []string
	if t.Duration > 0 {
		meta = append(meta, shortClock(math.Round(t.Duration)))
	}
	for _, s := range []string{u.t(t.Genre), u.translateWords(t.Mood), t.Artist} {
		if s != "" && len(meta) < 3 {
			meta = append(meta, s)
		}
	}
	info := strings.Join(meta, " · ")
	_, cached := a.lib.Cached(t)
	if read, total, ok := a.lib.Progress(t); ok {
		if total > 0 {
			info = u.tf("Downloading %d%%", read*100/total)
		} else {
			info = u.tf("Downloading %.1f MB", float64(read)/1e6)
		}
	} else if playing == key {
		info = u.tf("Playing %s", shortClock(math.Floor(heard)))
		if t.Duration > 0 {
			u.c.Fill(gfx.Rect{X: textX, Y: r.Bottom() - u.px(5), W: textW * float32(min(heard/t.Duration, 1)), H: u.px(2)}, colAccent)
		}
	}
	infoCol := colDim
	if cached && t.Provider != library.Local {
		u.icon(icCheck, gfx.Rect{X: textX, Y: r.Y + u.px(29), W: u.px(12), H: u.px(18)}, 9, colSuccess)
		textX += u.px(15)
		textW -= u.px(15)
	}
	u.label(u.face(11), gfx.Rect{X: textX, Y: r.Y + u.px(29), W: textW, H: u.px(18)}, info, infoCol, 0)
	adding := a.music.adding[key]
	label := u.t("Add")
	if adding {
		label = "…"
	}
	if u.button("add/"+key, add, icAdd, label, primary, !adding && a.p != nil) {
		a.addTrack(t)
	}
	tipText := u.t("Add at the playhead")
	if !cached {
		tipText = u.t("Download and add at the playhead (kept for next time)")
	}
	u.hint("add/"+key, tipText, add)
	if t.Provider == library.Local && hot && u.rightAt != nil && r.Contains(u.rightAt.X, u.rightAt.Y) {
		a.lib.RemoveLocal(t)
		a.music.refreshLocal(a)
		a.toast(u.tf("Removed %s from the list (the file is kept)", t.Title), false)
	}
}

// auditionTrack previews t: from the cache when downloaded, otherwise streamed while it downloads in the background.
func (a *App) auditionTrack(t library.Track) {
	key := t.Key()
	if playing, _ := a.audition.Playing(); playing == key {
		a.audition.Stop()
		return
	}
	a.player.Stop()
	if path, ok := a.lib.Cached(t); ok {
		a.audition.Play(key, path, "")
		return
	}
	a.audition.Play(key, t.URL, t.Referer)
	a.background(func(ctx context.Context) {
		if _, err := a.lib.Download(ctx, t); err != nil {
			a.post(func() { a.toast(t.Title+" "+err.Error(), true) })
		}
		a.host.Wake()
	})
}

// addTrack places t at the playhead, downloading it into the cache first when needed.
func (a *App) addTrack(t library.Track) {
	if path, ok := a.lib.Cached(t); ok {
		a.placeTrack(t, path)
		return
	}
	key := t.Key()
	a.music.adding[key] = true
	a.background(func(ctx context.Context) {
		path, err := a.lib.Download(ctx, t)
		a.post(func() {
			delete(a.music.adding, key)
			if err != nil {
				a.toast(t.Title+" "+err.Error(), true)
				return
			}
			a.placeTrack(t, path)
		})
	})
}

// drawSelectedMusic edits the selected clip and returns the y below it.
func (a *App) drawSelectedMusic(r gfx.Rect, m project.Music) float32 {
	u := &a.u
	card := gfx.Rect{X: r.X, Y: r.Y, W: r.W, H: r.H - u.px(12)}
	u.c.Fill(card, gfx.Mix(colSurface, colMusic, 0.35))
	in := card.Inset(u.px(12))
	u.label(u.bold(13.5), gfx.Rect{X: in.X, Y: in.Y, W: in.W - u.px(40), H: u.px(20)}, m.Title, colText, 0)
	if u.iconButton("music/del", gfx.Rect{X: in.Right() - u.px(30), Y: in.Y - u.px(5), W: u.px(30), H: u.px(30)}, icDelete, u.t("Remove from the timeline"), false, true) {
		a.deleteSelection()
		return r.Bottom()
	}
	u.label(u.face(11.5), gfx.Rect{X: in.X, Y: in.Y + u.px(22), W: in.W, H: u.px(18)},
		u.tf("%s – %s · length %s", clock(m.Start), clock(m.End()), clock(m.Length)), colDim, 0)
	y := in.Y + u.px(48)
	u.label(u.face(12.5), gfx.Rect{X: in.X, Y: y, W: u.px(40), H: u.px(28)}, u.t("Volume"), colDim, 0)
	vol := m.Volume
	sr := gfx.Rect{X: in.X + u.px(42), Y: y, W: in.W - u.px(96), H: u.px(28)}
	changed, done := u.slider("music/vol", sr, &vol, 0, 2, true)
	a.live(changed, done, func() { a.p.SetMusic(m.ID, vol, m.Fade) })
	u.label(u.face(12), gfx.Rect{X: sr.Right() + u.px(6), Y: y, W: u.px(48), H: u.px(28)}, fmt.Sprintf("%d%%", int(math.Round(vol*100))), colDim, 0)
	y += u.px(34)
	if fade := u.checkbox("music/fade", gfx.Rect{X: in.X, Y: y, W: u.px(130), H: u.px(26)}, u.t("Fade in/out"), m.Fade); fade != m.Fade {
		a.change(func() { a.p.SetMusic(m.ID, m.Volume, fade) })
	}
	if m.Credit != "" {
		u.label(u.face(11), gfx.Rect{X: in.X + u.px(140), Y: y, W: in.W - u.px(140), H: u.px(26)}, u.t("Needs credit · written to .credits.txt on export"), colFaint, 0)
	}
	return r.Bottom()
}
