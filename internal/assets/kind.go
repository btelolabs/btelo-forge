// Package assets is the asset manager's model: what is in a folder, what kind each file is, the tags and
// categories given to files, and small pictures of them kept on disk so a folder opens at once the second time.
package assets

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/library"
	"github.com/btelolabs/btelo-forge/internal/mind"
	"github.com/btelolabs/btelo-forge/internal/paint"
	"github.com/btelolabs/btelo-forge/internal/project"
)

// Kind is what a file is, by its name.
type Kind int

const (
	Other Kind = iota
	Folder
	Image
	Animation // a GIF; an animated WebP is an Image found to be animated once it is read
	Video
	Audio
	Canvas // a drawing saved by the app
	Edit   // a video edit saved by the app
	Markdown
	Code
	Text
	MindMap // a mind map saved by the app
	Office  // a Word, Excel, PowerPoint or PDF document
	Model   // a 3D model
)

// Kinds lists the kinds files are filtered by, in the order the asset manager shows them.
var Kinds = []Kind{Image, Animation, Video, Audio, Canvas, Edit, MindMap, Office, Model, Markdown, Code, Text, Other}

// Extensions of each kind; a file whose extension is in none of them is Other.
var (
	ImageExtensions     = []string{".png", ".jpg", ".jpeg", ".webp", ".bmp", ".tif", ".tiff", ".ico", ".jfif"}
	AnimationExtensions = []string{".gif"}
	VideoExtensions     = []string{".mp4", ".mov", ".mkv", ".avi", ".webm", ".m4v", ".wmv", ".flv", ".ts", ".mts", ".m2ts", ".3gp", ".mpg", ".mpeg"}
	MarkdownExtensions  = []string{".md", ".markdown", ".mdown", ".mkd"}
	CodeExtensions      = []string{".go", ".js", ".mjs", ".cjs", ".ts", ".tsx", ".jsx", ".py", ".java", ".kt", ".kts", ".c", ".h",
		".cc", ".cpp", ".cxx", ".hpp", ".hh", ".cs", ".rs", ".rb", ".php", ".swift", ".m", ".mm", ".scala", ".lua", ".dart",
		".sh", ".bash", ".zsh", ".ps1", ".psm1", ".bat", ".cmd", ".sql", ".json", ".jsonc", ".yaml", ".yml", ".toml", ".xml",
		".html", ".htm", ".css", ".scss", ".less", ".vue", ".svelte", ".svg", ".ini", ".cfg", ".conf", ".gradle", ".r", ".pl",
		".vb", ".fs", ".zig", ".nim", ".ex", ".exs", ".erl", ".hs", ".clj", ".proto", ".graphql", ".tf", ".dockerfile", ".mod"}
	OfficeExtensions = []string{".docx", ".doc", ".xlsx", ".xlsm", ".xls", ".csv", ".tsv", ".pptx", ".ppt", ".pdf"}
	ModelExtensions  = []string{".glb", ".gltf", ".fbx"}
	TextExtensions   = []string{".txt", ".log", ".srt", ".vtt", ".ass", ".lrc", ".nfo", ".rst", ".tex", ".env"}
)

// codeNames are files without an extension that hold code.
var codeNames = []string{"dockerfile", "makefile", "license", "readme", ".gitignore", ".gitattributes", ".editorconfig"}

// KindOf is the kind of the file called name.
func KindOf(name string) Kind {
	ext := strings.ToLower(filepath.Ext(name))
	switch {
	case slices.Contains(ImageExtensions, ext):
		return Image
	case slices.Contains(AnimationExtensions, ext):
		return Animation
	case slices.Contains(VideoExtensions, ext):
		return Video
	case slices.Contains(library.AudioExtensions, ext):
		return Audio
	case ext == paint.Ext:
		return Canvas
	case ext == project.EditExt:
		return Edit
	case ext == mind.Ext:
		return MindMap
	case slices.Contains(OfficeExtensions, ext):
		return Office
	case slices.Contains(ModelExtensions, ext):
		return Model
	case slices.Contains(MarkdownExtensions, ext):
		return Markdown
	case slices.Contains(CodeExtensions, ext), slices.Contains(codeNames, strings.ToLower(filepath.Base(name))):
		return Code
	case slices.Contains(TextExtensions, ext):
		return Text
	}
	return Other
}

// Readable reports whether k is shown as its text.
func (k Kind) Readable() bool { return k == Markdown || k == Code || k == Text }

// Visual reports whether k has a picture of its own.
func (k Kind) Visual() bool {
	return k == Image || k == Animation || k == Video || k == Canvas || k == Edit || k == MindMap
}
