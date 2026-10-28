# The canvas engine

The canvas editor (`internal/paint`, drawn by `internal/ui/art*.go`) is a layered picture on an infinite canvas, with
pixel layers in the manner of Photoshop and vector layers in the manner of Affinity Designer. This note fixes the
design so that changes keep to it.

## Goals

- **Infinite canvas.** A new canvas has no size. Painting, placing and moving work anywhere; nothing is clipped to a
  frame. Export takes the content's bounds, a selection, or an artboard.
- **Cost follows the screen, not the picture.** Drawing a frame costs about the same whatever the canvas's extent or
  zoom. Memory follows what has been painted, not the area it spans.
- **Undo is cheap.** A step keeps only what it changed.
- **An inactive window costs nothing.** No timers, no redraws while nothing changes (see `nextFrame`).

## Pixels: tiles

A pixel layer is a `Raster`: a sparse map of 256×256 tiles of premultiplied RGBA, keyed by tile coordinates. Missing
tiles are transparent. A layer mask and the selection are `Mask` rasters of 8-bit tiles with a value for everywhere
no tile is (a mask's fill, or 255 for an inverted selection), so they too are infinite.

- **Copy on write.** `Snapshot` copies the tile map and freezes every tile; writing to a frozen tile clones it first.
  The history keeps snapshots, so an undo step holds only the tiles its change touched, and there are no pixel
  patches.
- **Levels of detail.** Each raster keeps, on demand, halved copies of its tiles (mip levels), dropped when the tiles
  under them change. Drawing at a zoom below 1 reads the level that matches, so zooming out on a large canvas reads
  about as many pixels as the screen has.
- **Regions.** Operations read and write rectangles (`Read`, `Write`, `Each`), so filters, transforms and blends work
  on any region without caring about tiles.

## Drawing the view

`Doc.Render(dst, view)` composites the layers straight into the window's pixels for a view (the canvas point at the
window's corner and the zoom): pixel layers from the matching level, vector layers rendered at the window's
resolution, blend modes and masks as in Photoshop. The editor keeps no picture of the whole canvas; it renders what
the window shows, and only the parts that changed.

## Vectors

A vector layer holds one object, as Affinity Designer's curves and shapes are each a layer (`Vector` in
`vector.go`): a curve of Bézier nodes (a point and two handles, sharp or smooth), or a smart shape (rectangle with
corner radius, ellipse, polygon, star) that keeps its parameters until it becomes a curve. Each has a map from its
own space to the canvas, a fill and a stroke (solid or linear/radial gradient through stops; stroke width, centred,
inside or outside, caps, joins, miter limit, dashes) and a fill rule. Objects are values never changed in place.

It is rendered at whatever scale it is shown (`Render`): the outline is flattened to within a fifth of a pixel,
filled with exact horizontal coverage on eight sub-rows (non-zero or even-odd), and stroked by turning each segment,
corner and cap into a polygon wound the same way and filling those non-zero; inside and outside strokes are a
doubled stroke multiplied by the fill's coverage or its complement. So it is sharp at every zoom and in export.

Transforms (Free Transform, the move tool, canvas flips, turns and resizes) only change an object's map, so they
are exact. Pixel tools refuse a vector layer (`ErrVectorLayer`); Layer › Rasterize, merging and flattening draw it
into pixels.

Tools, in Affinity Designer's manner: the shape tools make vector layers (fill the foreground colour, stroke the
background colour), drawn live as they are dragged; the pen (P) draws curves (click for a sharp node, drag for a
smooth one, click the first node to close, Enter or Escape to finish); the node tool (A) edits any vector layer's
nodes and handles (Shift adds to the chosen nodes, a box chooses those in it, Alt breaks a smooth node, a
double-click toggles it, a click on the outline adds a node, Delete removes, the arrows nudge) and turns a smart
shape into a curve on its first edit; its context bar edits the fill and stroke (paint, width, alignment, caps,
joins, dashes). An edit under way is a `VectorSession`: shown as it goes, recorded as one step when it ends, and a
curve left with no outline takes its layer with it. The pencil (N) draws freehand and fits the line with a few
smooth curves (`FitPath` in `fit.go`, Schneider's method: least-squares cubics with Newton reparameterisation,
split at the worst point, sharp at corners).

Booleans (`boolean.go`; Layer › Boolean and the node tool's bar, on the chosen vector layers: Ctrl+click in the
layers panel or Shift+click with the node tool chooses more) add, subtract (the bottom less those above),
intersect or exclude. The outlines are flattened in canvas space and snapped to a 1/256-pixel grid; every edge is
cut where another crosses it or ends on it, one shared point per crossing; a piece is kept, turned with the
result's inside on its left, when the result covers one side of it and not the other, judged against the cut
pieces themselves so the test agrees with the geometry; the pieces are joined into rings and fitted with curves
again. The result takes the bottom object's place and style.

Snapping (`art_snap.go`, View › Snapping) draws a point being placed or dragged by the pen, the node tool, the
shape tool or the artboard tool to other objects' nodes and to the corners, side middles and centres of objects,
artboards and the frame, else along their edges and middle lines each way on its own.

## Retouching, adjustment layers, layer styles and warps

The retouching brushes (`retouch.go`) are dodge, burn and sponge (O) and blur, sharpen and smudge (R). All but
smudge follow the brush's model: the stroke's coverage lays on a full effect worked out from the pixels as they
were when the stroke began (dodge and burn by tone range with an exposure, the sponge to or from grey, blur three box
blurs, sharpen an unsharp mask from the same blur), so one stroke never goes past its strength. Smudge carries a
patch of colour from dab to dab, picking up what it leaves, so it works on the pixels as they are. They refuse a
layer mask.

An adjustment layer (`adjustlayer.go`) holds an `Adjustment` (brightness/contrast, levels, curves, hue/saturation,
posterize, threshold, invert, black & white) instead of pixels. Compositing applies it, pixel by pixel, to what is
under it so far in its group (in a pass-through group, everything under it), through its own opacity, blend mode and
mask; the pixels under it never change. It comes with a white mask, chosen, so painting decides where it applies.
Pixel work refuses it; merging it down applies it to the layer under it. Its settings are edited in the same dialogs
as the adjustments that change pixels, live, and recorded as one step.

Layer styles (`style.go`) are effects drawn from a layer's shape (its transparency after its mask) whenever it is
shown, so they follow every change: drop shadow and outer glow under the layer, each with its own blend mode; inner
shadow, inner glow and colour overlay on it, within its shape; and a stroke outside, inside or centred on its edge.
Growing the shape uses an exact Euclidean distance transform (Felzenszwalb and Huttenlocher), so strokes and spreads
are round; softening is three box blurs. Effects widen a layer's reach, so culling, export bounds, merging (which
draws them into pixels) and the view's redraw after painting take them in.

Distort, Perspective, Skew and Warp (`mesh.go`) map the content by a mesh over its bounds: each cell by the
homography that takes its corners where they went, exact for a quadrilateral and, sampled 24 × 24 from Warp's
bicubic Bézier grid, smooth for a warp. Pixels are sampled bilinearly, a source pixel beyond the edge so the edges are
soft; layer masks and the selection go with them; a vector object's segments are split in four and every node and
handle mapped, so it stays a vector. In Free Transform, Ctrl on a corner distorts, as in Photoshop.

## Artboards

An artboard (`Board` in `board.go`) is a named, fixed-size area of the infinite canvas with its own background,
exported as a picture of its own; a canvas can have any number, alongside or instead of a frame. Layers are not
inside boards: a board shows what lies on it, and moving a board carries the layers that lie wholly on it. The
artboard tool shares the move tool's place (Shift+V): a drag makes a board, a press chooses one and a drag moves
it, its handles resize it, and its options name it, size it from presets or typed numbers, set its background and
export it. Boards are part of the picture's state, so they undo, and resizing or turning the canvas maps them. The
view darkens the pasteboard beyond the frame and the boards.

## Files

`.btcanvas` is a zip: `doc.json` with the frame, paper, artboards, layer tree (names, visibility, opacity, blend
modes, locks, groups, text, vector objects with every paint and stroke setting) and the selection's description,
and each raster's and mask's tiles. It keeps everything the picture is; only the history is left out. Version 4
added the artboards and version 5 adjustment layers and layer styles; earlier versions are still read, and a file newer than the program is refused rather than
read in part and saved back without what it did not understand.
