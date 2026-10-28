# Mind maps

The mind map tab (`internal/mind`, drawn by `internal/ui/mind*.go`) is an infinite board on which mind maps and
flowcharts live together: every object is a node, topics grow into trees, and connectors join any two nodes. This
note fixes the design so that changes keep to it.

## Goals

- **One board for both ways of thinking.** A tree's topic can have connectors like a flowchart's box, and a flowchart's
  box can grow a tree. Nothing is a separate document type.
- **Everything is a node.** Topics (words in a shape: a mind map's topic, a flowchart's box, free text), tables with
  formulas, pictures (stills, animated GIF/WebP, canvases), videos, canvases' smart objects and other mind maps.
- **Hundreds of thousands of nodes.** A frame costs what the screen shows; an edit costs what it changes, not the size
  of the map.
- **Small files.** Pictures, videos, smart objects and nested maps are referred to, never copied. A tree's node has no
  place saved: its tree places it.
- **Undo is cheap.** A step keeps only the nodes and connectors it touched.

## The model

`Map` holds nodes and connectors in slices indexed by id (ids are never reused, so undo can bring a node back under
its own id). A `Node`'s exported fields are what is saved; `lay` is what the layout worked out. A node with
`Parent == 0` is free: it has `X`, `Y` and may be the root of a tree, whose `Layout` (map, right, left, down, up, list)
decides how its descendants are placed. `Edge` is a connector between any two nodes, with sides chosen to face each
other unless fixed, a route (curve, elbow, straight), arrowheads, dashes and a label.

The look follows the map's theme (`lookOf`); the default, Midnight, is MindNode on macOS in its dark appearance. The board is plain. A tree's root is a light,
rounded bubble with a soft shadow; every other topic is its words resting on a line of its branch's colour, and the
branch flows into that line, tapering from the parent's thickness to the child's (`branchWidth`). In trees that grow
up or down (org charts) topics are soft bubbles of their branch's colour instead, which branches can reach from
above and below. Free topics with a shape of their own are flowchart boxes; pictures, videos, nested maps and tables
are flat cards with rounded corners; only the root casts a shadow. Branch colours are a soft, bright palette, one per main branch.

## Layout: the trees are the index

Each node keeps its box size, its offset from its parent (`rel`) and the box around its whole subtree relative to its
own top-left (`sub`). Children are stacked along the way the tree grows, so their subtrees follow one another along
that axis.

- **Incremental.** A change marks the node dirty and the path up to its root. `Layout` re-measures only dirty nodes
  (wrapped words are cached) and restacks the children of each node on the path: an edit costs the depth times the
  siblings along the path, a few microseconds in a 300 000-node tree. Offsets are relative, so moving a subtree moves
  nothing below it.
- **Changed areas.** While restacking, the old and new boxes of whatever moved are recorded (`Changed`), so the view
  draws again only the tiles over them.
- **Finding what is in view.** Free nodes are kept by the boxes of their trees in a grid of cells in levels (an item
  goes in the level whose cells are at least its size, in the cell of its top-left corner). From a root the query
  descends the tree, and at each node finds the children in range by halves (`childRange`): finding what is in a
  small view of any map is logarithmic per level. Connectors are kept in a grid of their own, their paths worked out
  again when nodes may have moved.

## Drawing

`Render` draws the part of a map in the canvas's clip through a `View` (zoom and offset). It only reads the map, so
several goroutines can draw parts of one laid-out map at once.

- **Levels of detail.** A subtree smaller than three pixels is a dot of its branch's colour and is not descended into;
  a box smaller than three pixels is a filled rectangle; words smaller than 4.5 pixels are grey bars. Drawing the whole
  of a 300 000-node map takes about as long as drawing a screenful of it.
- **Branches.** Up to 48 children get smooth tapering bands (`ribbon`: a curve offset to both sides, filled by the
  non-zero rule, with round ends); a node with more sends them from a trunk, drawn whole however many of the children
  lie out of view, so a node with a huge number of children costs only those in view.
- **Tiles.** The view is drawn from 256-pixel tiles at the view's zoom (`Tiles`), cut at whole multiples of their size
  from the board's origin, so panning only moves them. Missing tiles are drawn on every core at once, nearest the
  middle of the view first, within a frame's budget; tiles of another zoom are stretched over a missing tile's place
  until it is ready; once the view is whole, the tiles around it are drawn ahead. Edits invalidate only the tiles over
  the areas `Changed` reports.
- **Live overlay.** What moves every frame is drawn over the tiles: selection, handles, the drag's ghost and drop
  place, a connector being drawn, the caret, animated pictures' frames and a video playing in its node.
- **Pictures.** `Pictures` is asked from the tile workers and never waits: a picture not ready is drawn as loading and
  read in the background at the step of size it is shown at (192, 640, 2048, 4096 pixels), the nearest step ready
  standing in meanwhile; the nodes showing it are drawn again when it arrives. They are kept within a memory budget.

## Connectors

A connector leaves and enters by the sides of its ends that face each other, along a curve, right angles or a
straight line. Dragging its middle (the handle shown on the chosen connector, or the line itself) sets its `Bend`:
the path passes through the point it was dragged to, and keeps that shape as its ends move. A connector the user has
not shaped finds its own way round the nodes between its ends (`routeAround`): when its plain path would cross one,
the sixteen pairs of sides and then detours further and further to either side are tried, and the shortest path
that crosses nothing is kept. Testing a path descends only the subtrees each of its pieces passes through.

Paths are worked out again when an end moves or the connector changes, and when something changes where it runs.
Routing has a budget for each frame: what does not fit is drawn plain for a frame or two and routed in the frames
after (`Routing`), so an edit in a large map never waits for every connector. A map read from a file is routed
whole before it is shown.

## Tasks

A topic can be a task (`Task`, `Done`), as in MindNode: a branch is made a task list whole. A task with no tasks
under it shows a box to tick; one with tasks under it a ring that fills as they are done, ticked when all are. The
counts are kept by the layout from the children's (`countTasks`), so they cost nothing to show. Ticking a task with
tasks under it ticks them all. Outlines carry tasks as `- [ ]` and `- [x]`.

## Themes

A map is drawn in one of six themes (`Themes`), kept in its file: Midnight (dark, words over coloured lines, after
MindNode), Paper (white), Pastel (light, every topic a soft bubble), Ocean (deep blue), Sepia (warm parchment) and
Neon (dark, glowing bubbles). A theme gives the board's colours, the branch colours, whether topics are bubbles and
how thick branches are; changing it is a step, and every node is measured again.

## Nested maps

A `Nested` node shows another map file whole. A map is drawn inside the chain of maps that show it: one already in
its chain holds itself and is drawn as a warning node in its place, and so is one deeper than `MaxNest` — never an
error, and drawing always ends. A nested map's picture remembers every map file read to draw it, so saving any of them
draws it again in every open map that shows it.

## Smart objects

A `Smart` node shows a canvas's smart object by its link id, through the picture the canvas editor keeps of every
smart object (`<link id>.png` in the smart objects folder). Each time a smart object changes on any canvas, that
picture is written again and the maps showing it read it again. A smart object copied on a canvas is pasted onto a map
with Ctrl+V; a double-click opens it in a canvas tab.

## Files

A `.btmind` file is `BTMIND`, a version byte, then the map deflated: variable-length numbers, places and sizes in
quarters of a unit, nodes tree by tree with each node before its children (so the children's order needs no saving),
optional fields behind a flags word. Files under the map's folder are saved relative to it, so a folder of maps and
their files moves together. 100 000 topics take about 5 bytes each. Saving reads the map on the UI goroutine
(`Snapshot`) and compresses and writes it on another.

## Editing

Every change is a step (`Begin`/`Commit` group several): a step keeps the before and after images of the nodes and
connectors it touched. Typing and dragging change the map live outside the history (`Live`), and are made a step when
they end. The MCP tools (`mcp_mind.go`, served at `/mcp/mind`) make each call one step, and a batch that fails part way
is taken back and forgotten (`Discard`).
