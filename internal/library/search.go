package library

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Page is one page of results.
type Page struct {
	Tracks []Track
	Pages  int
}

// Catalog returns every track of a catalog source, fetched once and kept until the configuration changes.
func (l *Library) Catalog(ctx context.Context, id string) ([]Track, error) {
	l.mu.Lock()
	src, ok := l.config.Find(id)
	if !ok || src.Mode != ModeCatalog {
		l.mu.Unlock()
		return nil, fmt.Errorf("%q is not a catalog source", id)
	}
	if tracks, ok := l.catalogs[id]; ok {
		l.mu.Unlock()
		return tracks, nil
	}
	generation := l.generation
	l.mu.Unlock()
	vars := map[string]string{"query": "", "page": "1", "offset": "0", "limit": strconv.Itoa(src.PageSize), "fetch": strconv.Itoa(src.PageSize)}
	doc, err := l.request(ctx, src, &src.c.request, src.Request.CacheHours, 7*24, &scope{vars: vars, maps: src.Maps})
	if err != nil {
		return nil, fmt.Errorf("loading %s: %w", src.Name.In("en"), err)
	}
	tracks, _, err := l.tracksOf(ctx, src, doc, vars, 0)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", src.Name.In("en"), err)
	}
	l.mu.Lock()
	if l.generation == generation {
		l.catalogs[id] = tracks
	}
	l.mu.Unlock()
	return tracks, nil
}

// Search asks one source for a page of results; pages start at 1. A catalog source is searched locally, and filter
// narrows it to one of its genres.
func (l *Library) Search(ctx context.Context, id, query, filter string, page int) (Page, error) {
	page = max(page, 1)
	query = strings.TrimSpace(query)
	l.mu.Lock()
	src, ok := l.config.Find(id)
	l.mu.Unlock()
	if !ok {
		return Page{}, fmt.Errorf("unknown music source %q", id)
	}
	if src.Mode == ModeCatalog {
		all, err := l.Catalog(ctx, id)
		if err != nil {
			return Page{}, err
		}
		var match []Track
		for _, t := range all {
			if (filter == "" || t.Genre == filter) && Matches(t, query) {
				match = append(match, t)
			}
		}
		from := min((page-1)*src.PageSize, len(match))
		return Page{Tracks: match[from:min(from+src.PageSize, len(match))], Pages: max(1, (len(match)+src.PageSize-1)/src.PageSize)}, nil
	}
	key := fmt.Sprintf("%s\x00%s\x00%d", id, query, page)
	if p, ok := l.cachedPage(key); ok {
		return p, nil
	}
	vars := map[string]string{"query": query, "page": strconv.Itoa(page), "offset": strconv.Itoa((page - 1) * src.PageSize),
		"limit": strconv.Itoa(src.PageSize), "fetch": strconv.Itoa(src.PageSize + 1)}
	doc, err := l.request(ctx, src, &src.c.request, src.Request.CacheHours, 24, &scope{vars: vars, maps: src.Maps})
	if err != nil {
		return Page{}, fmt.Errorf("searching %s: %w", src.Name.In("en"), err)
	}
	tracks, pages, err := l.tracksOf(ctx, src, doc, vars, page)
	if err != nil {
		return Page{}, fmt.Errorf("reading %s: %w", src.Name.In("en"), err)
	}
	p := Page{Tracks: tracks, Pages: pages}
	l.keepPage(key, p)
	return p, nil
}

// SearchAll searches every enabled source of a kind that takes part in combined searches. Catalog sources lead
// the first page with their best matches; the other sources' results are interleaved. Sources that fail are
// reported in the error while the others still return.
func (l *Library) SearchAll(ctx context.Context, kind, query string, page int) (Page, error) {
	var sources []*Source
	for _, s := range l.Config().Enabled() {
		if s.Kind == kind && !s.NotInAll {
			sources = append(sources, s)
		}
	}
	type res struct {
		p   Page
		err error
	}
	results := make([]res, len(sources))
	var wg sync.WaitGroup
	for i, s := range sources {
		if s.Mode == ModeCatalog && page > 1 {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := l.Search(ctx, s.ID, query, "", page)
			if s.Mode == ModeCatalog {
				p.Tracks, p.Pages = p.Tracks[:min(len(p.Tracks), s.Featured)], 1
			}
			results[i] = res{p, err}
		}()
	}
	wg.Wait()
	var out Page
	var errs []error
	for i, r := range results {
		out.Pages = max(out.Pages, r.p.Pages)
		if r.err != nil {
			errs = append(errs, r.err)
		}
		if sources[i].Mode == ModeCatalog {
			out.Tracks = append(out.Tracks, r.p.Tracks...)
		}
	}
	for i := 0; ; i++ {
		added := false
		for k, r := range results {
			if sources[k].Mode != ModeCatalog && i < len(r.p.Tracks) {
				out.Tracks = append(out.Tracks, r.p.Tracks[i])
				added = true
			}
		}
		if !added {
			break
		}
	}
	return out, errors.Join(errs...)
}

// request renders and fetches a request and decodes its JSON answer.
func (l *Library) request(ctx context.Context, src *Source, r *compiledRequest, cacheHours, defaultHours float64, s *scope) (any, error) {
	address := r.url.render(s)
	q := url.Values{}
	names := make([]string, 0, len(r.params))
	for name := range r.params {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, t := range r.params[name] {
			if v := t.render(s); v != "" {
				q.Add(name, v)
			}
		}
	}
	if len(q) > 0 {
		sep := "?"
		if strings.Contains(address, "?") {
			sep = "&"
		}
		address += sep + q.Encode()
	}
	headers := map[string]string{}
	for name, t := range r.headers {
		headers[name] = t.render(s)
	}
	if cacheHours == 0 {
		cacheHours = defaultHours
	}
	data, err := l.fetchCached(ctx, address, headers, src.ID+"-"+hashName(address)+".json", time.Duration(cacheHours*float64(time.Hour)))
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("the answer is not JSON: %w", err)
	}
	return doc, nil
}

// tracksOf builds the tracks of a response and works out how many pages there are; page 0 means a whole catalog.
func (l *Library) tracksOf(ctx context.Context, src *Source, doc any, vars map[string]string, page int) ([]Track, int, error) {
	if src.Error != "" {
		if msg := toValue(walk(doc, splitPath(src.Error))).String(); msg != "" {
			return nil, 0, errors.New(msg)
		}
	}
	list, ok := walk(doc, splitPath(src.Results)).([]any)
	if !ok {
		if walk(doc, splitPath(src.Results)) == nil {
			list = nil
		} else {
			return nil, 0, fmt.Errorf("results %q is not a list", src.Results)
		}
	}
	pages := page
	switch {
	case page == 0:
		pages = 1
	case src.Pages != "":
		pages = number(walk(doc, splitPath(src.Pages)))
	case src.Total != "":
		pages = int(math.Ceil(float64(number(walk(doc, splitPath(src.Total)))) / float64(src.PageSize)))
	default:
		if len(list) > src.PageSize {
			list, pages = list[:src.PageSize], page+1
		}
	}
	var hits []any
	for _, item := range list {
		if matchesWhere(item, src.Where) {
			hits = append(hits, item)
		}
	}
	groups := make([][]Track, len(hits))
	if src.Expand == nil {
		for i, item := range hits {
			if t, ok := src.track(&scope{item: item, vars: vars, maps: src.Maps}); ok {
				groups[i] = []Track{t}
			}
		}
	} else {
		var wg sync.WaitGroup
		for i, hit := range hits {
			wg.Add(1)
			go func() {
				defer wg.Done()
				groups[i] = l.expand(ctx, src, hit, vars)
			}()
		}
		wg.Wait()
	}
	var tracks []Track
	for _, g := range groups {
		tracks = append(tracks, g...)
	}
	return tracks, max(pages, 1), nil
}

// expand fetches the document of one result and builds tracks from the list inside it; a result whose document
// cannot be fetched is skipped.
func (l *Library) expand(ctx context.Context, src *Source, hit any, vars map[string]string) []Track {
	e := src.Expand
	doc, err := l.request(ctx, src, &src.c.expand, e.CacheHours, 7*24, &scope{item: hit, vars: vars, maps: src.Maps})
	if err != nil {
		return nil
	}
	list, _ := walk(doc, splitPath(e.Results)).([]any)
	var out []Track
	for _, item := range list {
		if e.Limit > 0 && len(out) >= e.Limit {
			break
		}
		if !matchesWhere(item, e.Where) {
			continue
		}
		if t, ok := src.track(&scope{item: item, parent: hit, doc: doc, vars: vars, maps: src.Maps}); ok {
			out = append(out, t)
		}
	}
	return out
}

// track renders one result; it is false when the id, title or address is empty.
func (src *Source) track(s *scope) (Track, bool) {
	if len(src.Let) > 0 {
		s.lets = map[string]any{}
		for name, p := range src.Let {
			list, _ := walk(s.item, splitPath(p.From)).([]any)
			for _, x := range list {
				if matchesWhere(x, p.Where) {
					s.lets[name] = x
					break
				}
			}
		}
	}
	f := map[string]string{}
	for name, t := range src.c.fields {
		f[name] = strings.TrimSpace(t.render(s))
	}
	for _, r := range src.c.rules {
		if glob(r.matches, r.cond.render(s)) {
			for name, t := range r.set {
				f[name] = strings.TrimSpace(t.render(s))
			}
		}
	}
	if f["id"] == "" || f["title"] == "" || f["url"] == "" {
		return Track{}, false
	}
	ext := strings.ToLower(f["ext"])
	if ext == "" || len(ext) > 4 {
		ext = "mp3"
	}
	duration := lengthSeconds(f["duration"])
	if src.Fields.DurationUnit == "milliseconds" && !strings.Contains(f["duration"], ":") {
		duration /= 1000
	}
	return Track{
		Provider: Provider(src.ID),
		ID:       f["id"],
		Title:    f["title"],
		Artist:   f["artist"],
		Genre:    f["genre"],
		Mood:     f["mood"],
		Keywords: f["keywords"],
		Duration: duration,
		URL:      f["url"],
		Referer:  f["referer"],
		Ext:      ext,
		License:  f["license"],
		Credit:   f["credit"],
		Page:     f["page"],
	}, true
}

func splitPath(p string) []string {
	if p == "" {
		return nil
	}
	return strings.Split(p, ".")
}

func matchesWhere(item any, where map[string]string) bool {
	for p, pattern := range where {
		if !glob(pattern, toValue(walk(item, splitPath(p))).String()) {
			return false
		}
	}
	return true
}

func number(v any) int {
	f, _ := strconv.ParseFloat(scalar(v), 64)
	return int(f)
}

// clock parses "hh:mm:ss" or "mm:ss".
func clock(s string) float64 {
	total := 0.0
	for _, part := range strings.Split(s, ":") {
		v, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil {
			return 0
		}
		total = total*60 + v
	}
	return total
}

// lengthSeconds reads a length given in seconds or as h:mm:ss.
func lengthSeconds(s string) float64 {
	if strings.Contains(s, ":") {
		return clock(s)
	}
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}
