package paint

import (
	"errors"
	"fmt"
	"image"
	"regexp"
	"strconv"
	"strings"
)

// Slices: layers, groups and artboards named to export as PNG files of their own, by the rules of the Krita
// named-assets plugin, with a name ending in .png marking one (K in the editor adds or takes it away):
//
//	fish.png                → fish.png, cut to what the layer shows
//	assets/fish.png         → assets/fish.png
//	assets/fish_300x200.png → assets/fish.png, 300×200
//	200x300/assets          → a folder group, not a file: what is directly in it goes to assets/ at 200×300,
//	                          marked or not; fish_50x50 in it → assets/fish.png, 50×50
//
// The paths are relative to the canvas file's folder. A size never scales: the picture is padded with transparency
// or cropped about the centre of what it shows. A size in the name comes before the nearest folder's, which comes
// before the one inherited from folder groups around it; a folder group within another adds its folders and gives
// its own size. An artboard is its frame, background and all, unless given a size. A group in a folder group
// exports as one picture, what is in it not on its own unless marked. Hidden layers, and those in hidden groups,
// are left out.

// SliceRole is what a name makes of a node.
type SliceRole int

const (
	SliceFile   SliceRole = iota // exports as a PNG file
	SliceFolder                  // a folder group: what is directly in it exports into its folders, at its size
)

// SizeFrom is where a slice's size comes from.
type SizeFrom int

const (
	SizeOwn      SizeFrom = iota // none: the size of what it shows (an artboard's frame)
	SizeInName                   // its own name's _WxH
	SizeInPath                   // a WxH folder in its own name
	SizeInFolder                 // a folder group it is in
)

// Slice is a node a name makes a slice or a folder group of.
type Slice struct {
	ID     int
	Name   string
	Role   SliceRole
	Path   string      // a file's, as assets/fish.png; a folder group's folders, as assets/ (empty for the top)
	Size   image.Point // zero for none
	From   SizeFrom
	Board  bool  // an artboard, which exports its frame
	Hidden bool  // left out: hidden, or in a hidden group
	Err    error // why the name cannot export; the other fields are then what could be read
}

// Slice limits, as the Krita plugin's: no side over 16384 pixels and no more than 64M pixels.
const (
	sliceMaxSide   = 16384
	sliceMaxPixels = 64 << 20
)

// SliceProblem is why a name cannot export.
type SliceProblem int

const (
	SliceAbsolute  SliceProblem = iota // a leading / or a backslash
	SliceEmptyPart                     // an empty folder or file name, as a//b or a trailing /
	SliceDots                          // . or .. as a folder
	SliceSpaces                        // a part starting or ending with a space, or ending with a dot
	SliceBadChar                       // a character files cannot have
	SliceReserved                      // a name Windows keeps, as CON or LPT1
	SliceSizeOnly                      // a file name that is only a size, which is a folder group's
	SliceTooBig                        // a size over the limits
	SliceDuplicate                     // another slice exports to the same file
)

// SliceError is a name that cannot export: the problem, and the part of the name (or the other slice's name) it is in.
type SliceError struct {
	Problem SliceProblem
	Part    string
	Size    image.Point
}

func (e *SliceError) Error() string {
	switch e.Problem {
	case SliceAbsolute:
		return "use a relative folder/name, without backslashes or a leading /"
	case SliceEmptyPart:
		return "an empty folder or file name"
	case SliceDots:
		return fmt.Sprintf("%q cannot be a folder", e.Part)
	case SliceSpaces:
		return fmt.Sprintf("%q starts or ends with a space, or ends with a dot", e.Part)
	case SliceBadChar:
		return fmt.Sprintf("%q has a character files cannot have", e.Part)
	case SliceReserved:
		return fmt.Sprintf("%q is a name Windows keeps", e.Part)
	case SliceSizeOnly:
		return fmt.Sprintf("%q alone is a folder group's size, not a file name", e.Part)
	case SliceTooBig:
		return fmt.Sprintf("%d×%d is over 16384 pixels a side or 64M pixels", e.Size.X, e.Size.Y)
	}
	return fmt.Sprintf("%q exports to the same file", e.Part)
}

var (
	// ErrSliceEmpty means a slice shows nothing, so there is no picture to export.
	ErrSliceEmpty = errors.New("the slice shows nothing")

	sliceSuffixSize = regexp.MustCompile(`_(\d+)[xX](\d+)$`)
	sliceFolderSize = regexp.MustCompile(`^(\d+)[xX](\d+)$`)
	sliceReserved   = regexp.MustCompile(`(?i)^(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])(\.|$)`)
)

// IsSliceName reports whether a name marks its node as a slice: it ends with .png.
func IsSliceName(name string) bool {
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(name)), ".png")
}

// SliceName is name marked as a slice, or with on false no longer marked.
func SliceName(name string, on bool) string {
	name = strings.TrimSpace(name)
	switch {
	case on && !IsSliceName(name):
		return name + ".png"
	case !on && IsSliceName(name):
		return strings.TrimSpace(name[:len(name)-len(".png")])
	}
	return name
}

// MarkSlices marks the nodes ids as slices, adding .png to their names, or with on false takes the mark away, as
// one step.
func (d *Doc) MarkSlices(ids []int, on bool) {
	m := d.Mark()
	changed := false
	for _, id := range ids {
		if n := d.Node(id); n != nil && n != d.Root {
			if name := SliceName(n.Name, on); name != n.Name {
				n.Name, changed = name, true
			}
		}
	}
	if !changed {
		return
	}
	step := "Mark as slice"
	if !on {
		step = "Unmark as slice"
	}
	d.Commit(step, m)
}

// Slices are the picture's slices and folder groups, top first as the layers panel lists them.
func (d *Doc) Slices() []Slice {
	var out []Slice
	used := map[string]int{} // a file's path, folded, to its first slice in out
	var walk func(g *Node, shown bool, prefix []string, size image.Point, from SizeFrom, direct bool)
	walk = func(g *Node, shown bool, prefix []string, size image.Point, from SizeFrom, direct bool) {
		for _, n := range g.Children {
			visible := shown && n.Visible
			name := strings.TrimSpace(n.Name)
			if n.IsGroup() && !IsSliceName(name) && hasSizeFolder(name) {
				s := Slice{ID: n.ID, Name: n.Name, Role: SliceFolder, Board: n.IsBoard(), Hidden: !visible}
				folders, fsize, ffrom, err := sliceFolders(name, size, from, true)
				s.Size, s.From, s.Err = fsize, ffrom, err
				s.Path = strings.Join(append(append([]string{}, prefix...), folders...), "/")
				if s.Path != "" {
					s.Path += "/"
				}
				out = append(out, s)
				if err == nil {
					walk(n, visible, append(append([]string{}, prefix...), folders...), fsize, ffrom, true)
				}
				continue
			}
			if IsSliceName(name) || direct {
				s := sliceFile(n, prefix, size, from)
				s.Hidden = !visible
				if s.Err == nil && !s.Hidden {
					key := strings.ToLower(s.Path)
					if i, ok := used[key]; ok {
						s.Err = &SliceError{Problem: SliceDuplicate, Part: out[i].Name}
					} else {
						used[key] = len(out)
					}
				}
				out = append(out, s)
			}
			// What is in a group exports with it; inside, only what is marked, and folder groups, are found.
			walk(n, visible, prefix, size, from, false)
		}
	}
	walk(d.Root, true, nil, image.Point{}, SizeOwn, false)
	return out
}

// SliceOf is what node id's name makes of it, and whether it is a slice or folder group.
func (d *Doc) SliceOf(id int) (Slice, bool) {
	for _, s := range d.Slices() {
		if s.ID == id {
			return s, true
		}
	}
	return Slice{}, false
}

// hasSizeFolder reports whether a name has a WxH part among its folders, or is one, which makes a group a folder
// group.
func hasSizeFolder(name string) bool {
	for _, part := range strings.Split(name, "/") {
		if sliceFolderSize.MatchString(part) {
			return true
		}
	}
	return false
}

// sliceFile reads a marked node's name, or one directly in a folder group, under the folder group's folders and size.
func sliceFile(n *Node, prefix []string, size image.Point, from SizeFrom) Slice {
	s := Slice{ID: n.ID, Name: n.Name, Role: SliceFile, Board: n.IsBoard(), Size: size, From: from}
	name := strings.TrimSpace(n.Name)
	if IsSliceName(name) {
		name = name[:len(name)-len(".png")]
	}
	if strings.HasPrefix(name, "/") || strings.Contains(name, `\`) {
		s.Path, s.Err = name, &SliceError{Problem: SliceAbsolute}
		return s
	}
	parts := strings.Split(name, "/")
	folders, fsize, ffrom, err := sliceFolders(strings.Join(parts[:len(parts)-1], "/"), size, from, false)
	s.Size, s.From = fsize, ffrom
	stem := parts[len(parts)-1]
	if err == nil {
		if m := sliceSuffixSize.FindStringSubmatch(stem); m != nil {
			s.Size, s.From = sliceSize(m), SizeInName
			err = checkSliceSize(s.Size)
			stem = stem[:len(stem)-len(m[0])]
		}
	}
	if err == nil {
		switch {
		case sliceFolderSize.MatchString(stem):
			err = &SliceError{Problem: SliceSizeOnly, Part: stem}
		default:
			err = checkSlicePart(stem)
		}
	}
	s.Path = strings.Join(append(append(append([]string{}, prefix...), folders...), stem+".png"), "/")
	s.Err = err
	return s
}

// sliceFolders reads the folders of a path, the WxH among them giving the size and going from the folders; whole is
// a folder group's whole name, whose last part is a folder too.
func sliceFolders(path string, size image.Point, from SizeFrom, whole bool) ([]string, image.Point, SizeFrom, error) {
	if path == "" {
		return nil, size, from, nil
	}
	if strings.HasPrefix(path, "/") || strings.Contains(path, `\`) {
		return nil, size, from, &SliceError{Problem: SliceAbsolute}
	}
	var folders []string
	for _, part := range strings.Split(path, "/") {
		if m := sliceFolderSize.FindStringSubmatch(part); m != nil {
			size = sliceSize(m)
			from = SizeInPath
			if whole {
				from = SizeInFolder
			}
			if err := checkSliceSize(size); err != nil {
				return folders, size, from, err
			}
			continue
		}
		if err := checkSlicePart(part); err != nil {
			return folders, size, from, err
		}
		folders = append(folders, part)
	}
	return folders, size, from, nil
}

func sliceSize(m []string) image.Point {
	w, _ := strconv.Atoi(m[1])
	h, _ := strconv.Atoi(m[2])
	return image.Pt(w, h)
}

func checkSliceSize(p image.Point) error {
	if p.X <= 0 || p.Y <= 0 || p.X > sliceMaxSide || p.Y > sliceMaxSide || p.X*p.Y > sliceMaxPixels {
		return &SliceError{Problem: SliceTooBig, Size: p}
	}
	return nil
}

// checkSlicePart refuses a folder or file name no system can keep safely, or that would leave the canvas's folder.
func checkSlicePart(part string) error {
	switch {
	case part == "":
		return &SliceError{Problem: SliceEmptyPart}
	case part == "." || part == "..":
		return &SliceError{Problem: SliceDots, Part: part}
	case part != strings.TrimSpace(part) || strings.HasSuffix(part, "."):
		return &SliceError{Problem: SliceSpaces, Part: part}
	case strings.ContainsAny(part, `<>:"|?*`) || strings.ContainsFunc(part, func(r rune) bool { return r < 0x20 }):
		return &SliceError{Problem: SliceBadChar, Part: part}
	case sliceReserved.MatchString(part):
		return &SliceError{Problem: SliceReserved, Part: part}
	}
	return nil
}

// SlicePicture is slice s as its file has it: the node alone, cut to what it shows (an artboard to its frame), then
// padded with transparency or cropped to its size about the centre, never scaled.
func (d *Doc) SlicePicture(s Slice) (*image.RGBA, error) {
	if s.Err != nil {
		return nil, s.Err
	}
	n := d.Node(s.ID)
	if n == nil {
		return nil, ErrNoLayer
	}
	var img *image.RGBA
	if n.IsBoard() {
		b, err := d.BoardPicture(n.ID)
		if err != nil {
			return nil, err
		}
		img = b
	} else {
		shown := *n
		shown.Visible = true
		if shown.Blend == BlendPassThrough {
			shown.Blend = BlendNormal // seen on its own
		}
		reach, bounded := contentReach(&shown)
		if !bounded {
			reach = d.limit()
		}
		if reach.Empty() {
			return nil, ErrSliceEmpty
		}
		if err := checkSliceSize(reach.Size()); err != nil {
			return nil, err
		}
		full := image.NewRGBA(reach)
		d.compositeInto(full, &Node{Children: []*Node{&shown}}, reach, 0)
		used := opaqueBounds(full)
		if used.Empty() {
			return nil, ErrSliceEmpty
		}
		img = full.SubImage(used).(*image.RGBA)
	}
	if s.Size == (image.Point{}) {
		out := image.NewRGBA(image.Rectangle{Max: img.Rect.Size()})
		copyRGBA(out, image.Point{}, img)
		return out, nil
	}
	out := image.NewRGBA(image.Rectangle{Max: s.Size})
	at := image.Pt((s.Size.X-img.Rect.Dx())/2, (s.Size.Y-img.Rect.Dy())/2) // an odd pixel goes right and down
	copyRGBA(out, at, img)
	return out, nil
}

// opaqueBounds is the smallest rectangle holding every pixel of img that is not fully transparent.
func opaqueBounds(img *image.RGBA) image.Rectangle {
	r := img.Rect
	minX, minY, maxX, maxY := r.Max.X, r.Max.Y, r.Min.X, r.Min.Y
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := img.Pix[img.PixOffset(r.Min.X, y) : img.PixOffset(r.Min.X, y)+4*r.Dx()]
		for x := 0; x < r.Dx(); x++ {
			if row[4*x+3] != 0 {
				minX, maxX = min(minX, r.Min.X+x), max(maxX, r.Min.X+x+1)
				minY, maxY = min(minY, y), y+1
			}
		}
	}
	if maxX <= minX {
		return image.Rectangle{}
	}
	return image.Rect(minX, minY, maxX, maxY)
}

// copyRGBA puts src's pixels into dst with src's corner at at, cut to dst.
func copyRGBA(dst *image.RGBA, at image.Point, src *image.RGBA) {
	r := image.Rectangle{Min: at, Max: at.Add(src.Rect.Size())}.Intersect(dst.Rect)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		sx, sy := src.Rect.Min.X+r.Min.X-at.X, src.Rect.Min.Y+y-at.Y
		copy(dst.Pix[dst.PixOffset(r.Min.X, y):dst.PixOffset(r.Max.X, y)], src.Pix[src.PixOffset(sx, sy):src.PixOffset(sx+r.Dx(), sy)])
	}
}
