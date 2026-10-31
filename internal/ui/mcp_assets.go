package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/btelolabs/btelo-forge/internal/assets"
)

// mcpAssetTools are the asset manager's tools, in every domain's server: files are what each kind of work starts
// from and ends in.
var mcpAssetTools = []string{"assets_browse", "assets_list", "assets_tag", "assets_categorize", "assets_trash"}

// assetView is a file as the MCP tools list it.
type assetView struct {
	Path       string   `json:"path"`
	Kind       string   `json:"kind"`
	Bytes      int64    `json:"bytes,omitempty"`
	Modified   string   `json:"modified"`
	Width      int      `json:"width,omitempty"`
	Height     int      `json:"height,omitempty"`
	Seconds    float64  `json:"seconds,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	Categories []string `json:"categories,omitempty"`
}

// kindIDs name the kinds for the MCP tools, in assets.Kinds' order.
var kindIDs = []string{"image", "animation", "video", "audio", "canvas", "edit", "mind_map", "office", "markdown", "code", "text", "other"}

func kindID(k assets.Kind) string {
	if k == assets.Folder {
		return "folder"
	}
	if i := slices.Index(assets.Kinds, k); i >= 0 {
		return kindIDs[i]
	}
	return "other"
}

func (a *App) addAssetTools(s *mcp.Server) {
	type browseIn struct {
		Dir string `json:"dir,omitempty" jsonschema:"folder to show; the resource folder when omitted"`
	}
	tool(s, a, "assets_browse", "Show a folder in the file manager tab as a grid of pictures with tags (Eagle-like: pictures, videos, sounds, canvases, .btcut edits, Markdown, code and texts), opening the tab if needed.",
		func(in browseIn) (any, error) {
			if in.Dir != "" {
				if st, err := os.Stat(in.Dir); err != nil || !st.IsDir() {
					return nil, fmt.Errorf("%s is not a folder", in.Dir)
				}
			}
			p := a.openAssets(in.Dir)
			if p == nil {
				return nil, fmt.Errorf("the export folder cannot be found")
			}
			return map[string]any{"dir": p.dir, "recent": a.fileHistory().Match("", time.Now())}, nil
		})

	type listIn struct {
		Dir      string `json:"dir,omitempty" jsonschema:"folder to list; the asset manager's folder, else the export folder, when omitted"`
		Kind     string `json:"kind,omitempty" jsonschema:"only files of this kind: image, animation, video, audio, canvas, edit, markdown, code, text, other or folder"`
		Tag      string `json:"tag,omitempty" jsonschema:"only files with this tag"`
		Category string `json:"category,omitempty" jsonschema:"only files in this category"`
		Search   string `json:"search,omitempty" jsonschema:"words that must all be in the name, tags or categories"`
		Limit    int    `json:"limit,omitempty" jsonschema:"at most this many, newest first (default 200)"`
	}
	tool(s, a, "assets_list", "List a folder's files with their kind, size, picture size or length (when known), tags and categories, newest first; also every tag and category in use.",
		func(in listIn) (any, error) {
			dir := in.Dir
			if dir == "" && a.files != nil && !a.files.pane().atDrives() {
				dir = a.files.pane().dir
			}
			if dir == "" {
				d, err := a.ensureResourceFolders()
				if err != nil {
					return nil, err
				}
				dir = d
			}
			entries, err := assets.Scan(dir)
			if err != nil {
				return nil, err
			}
			if in.Kind != "" && in.Kind != "folder" && !slices.Contains(kindIDs, in.Kind) {
				return nil, fmt.Errorf("unknown kind %q", in.Kind)
			}
			lib := a.assetLibrary()
			a.fileThumbs.init(a.cacheDir)
			slices.SortFunc(entries, func(x, y assets.Entry) int { return y.Mod.Compare(x.Mod) })
			limit := in.Limit
			if limit <= 0 {
				limit = 200
			}
			words := strings.Fields(strings.ToLower(in.Search))
			out := []assetView{}
			for _, e := range entries {
				it := lib.Of(e.Path)
				hay := strings.ToLower(e.Name + " " + strings.Join(it.Tags, " ") + " " + strings.Join(it.Categories, " "))
				switch {
				case in.Kind != "" && kindID(e.Kind) != in.Kind,
					in.Tag != "" && !slices.ContainsFunc(it.Tags, func(t string) bool { return strings.EqualFold(t, in.Tag) }),
					in.Category != "" && !slices.ContainsFunc(it.Categories, func(c string) bool { return strings.EqualFold(c, in.Category) }),
					slices.ContainsFunc(words, func(w string) bool { return !strings.Contains(hay, w) }):
					continue
				}
				v := assetView{Path: e.Path, Kind: kindID(e.Kind), Bytes: e.Size, Modified: e.Mod.Format(time.RFC3339), Tags: it.Tags, Categories: it.Categories}
				if info, ok := a.fileThumbs.store.Peek(e); ok {
					v.Width, v.Height, v.Seconds = info.W, info.H, info.Duration
				}
				out = append(out, v)
				if len(out) == limit {
					break
				}
			}
			var tags, cats []string
			for _, t := range lib.Tags() {
				tags = append(tags, t.Name)
			}
			for _, c := range lib.Categories() {
				cats = append(cats, c.Name)
			}
			return map[string]any{"dir": dir, "files": out, "tags": tags, "categories": cats}, nil
		})

	type tagIn struct {
		Paths  []string `json:"paths" jsonschema:"absolute paths of the files"`
		Add    []string `json:"add,omitempty" jsonschema:"tags to add"`
		Remove []string `json:"remove,omitempty" jsonschema:"tags to remove"`
	}
	tool(s, a, "assets_tag", "Add tags to files and remove tags from them; tags belong to the files wherever they are browsed from.",
		func(in tagIn) (any, error) {
			if len(in.Paths) == 0 {
				return nil, fmt.Errorf("name at least one file in paths")
			}
			lib := a.assetLibrary()
			for _, t := range in.Add {
				if err := lib.AddTag(in.Paths, t); err != nil {
					return nil, fmt.Errorf("tag %q: %w", t, err)
				}
			}
			for _, t := range in.Remove {
				if err := lib.RemoveTag(in.Paths, t); err != nil {
					return nil, err
				}
			}
			a.assetLibVersion++
			out := map[string][]string{}
			for _, p := range in.Paths {
				out[p] = lib.Of(p).Tags
			}
			return out, nil
		})

	type categorizeIn struct {
		Paths    []string `json:"paths,omitempty" jsonschema:"absolute paths of the files"`
		Category string   `json:"category" jsonschema:"the category; made when it does not exist and in is true"`
		In       bool     `json:"in" jsonschema:"true puts the files in the category, false takes them out"`
		Delete   bool     `json:"delete,omitempty" jsonschema:"delete the category itself (the files stay); paths and in are then ignored"`
	}
	tool(s, a, "assets_categorize", "Put files in a category or take them out, making the category when needed, or delete a category.",
		func(in categorizeIn) (any, error) {
			lib := a.assetLibrary()
			defer func() { a.assetLibVersion++ }()
			if in.Delete {
				return map[string]bool{"deleted": true}, lib.DeleteCategory(in.Category)
			}
			exists := slices.ContainsFunc(lib.Categories(), func(c assets.Count) bool { return strings.EqualFold(c.Name, in.Category) })
			if !exists && in.In {
				if _, err := lib.NewCategory(in.Category); err != nil {
					return nil, err
				}
			}
			if len(in.Paths) > 0 {
				if err := lib.SetCategory(in.Paths, in.Category, in.In); err != nil {
					return nil, err
				}
			}
			return map[string]any{"categories": lib.Categories()}, nil
		})

	type trashIn struct {
		Paths []string `json:"paths" jsonschema:"absolute paths of the files or folders"`
	}
	tool(s, a, "assets_trash", "Move files or folders to the Recycle Bin (they can be restored from there), forgetting their tags and categories.",
		func(in trashIn) (any, error) {
			if len(in.Paths) == 0 {
				return nil, fmt.Errorf("name at least one file in paths")
			}
			for _, p := range in.Paths {
				if !filepath.IsAbs(p) {
					return nil, fmt.Errorf("%s is not an absolute path", p)
				}
				if _, err := os.Stat(p); err != nil {
					return nil, err
				}
			}
			ops, err := a.fileOps()
			if err != nil {
				return nil, err
			}
			if slices.Contains(in.Paths, a.preview.path) {
				a.closeFilePreview()
			}
			if err := ops.Trash(in.Paths); err != nil {
				return nil, err
			}
			a.assetLibrary().Forget(in.Paths)
			a.assetLibVersion++
			var dirs []string
			for _, p := range in.Paths {
				dirs = append(dirs, filepath.Dir(p))
			}
			a.refreshPanes(dirs)
			return map[string]any{"trashed": in.Paths}, nil
		})
}
