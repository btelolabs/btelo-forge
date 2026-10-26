package ui

import "github.com/btelolabs/btelo-video-cut/internal/gfx"

var (
	colBackground = gfx.Hex(0x0f1112)
	colPanel      = gfx.Hex(0x171a1c)
	colSurface    = gfx.Hex(0x202427)
	colSurfaceHi  = gfx.Hex(0x2a2f33)
	colBorder     = gfx.Hex(0x2b3034)
	colText       = gfx.Hex(0xeceff0)
	colDim        = gfx.Hex(0x9ba3a8)
	colFaint      = gfx.Hex(0x666e74)
	colAccent     = gfx.Hex(0x1fbf75)
	colAccentHi   = gfx.Hex(0x34d68a)
	colOnAccent   = gfx.Hex(0xffffff) // text and icons on green
	colDanger     = gfx.Hex(0xf0605d)
	colSuccess    = gfx.Hex(0x34d68a)
	colWarning    = gfx.Hex(0xf2c879)
	colVideo      = gfx.Hex(0x2b3a43)
	colVideoUpper = gfx.Hex(0x30414f)
	colMusic      = gfx.Hex(0x17614a)
	colMusicHi    = gfx.Hex(0x1f7a5d)
	colCaption    = gfx.Hex(0x6b4fc4)
	colCaptionHi  = gfx.Hex(0x8065d8)
	colImage      = gfx.Hex(0xa0612a)
	colImageHi    = gfx.Hex(0xbb7535)
	colPlayhead   = gfx.Hex(0xffd84d)
	colBlack      = gfx.Hex(0x000000)
	colWhite      = gfx.Hex(0xffffff)
)

// Icons are Material Icons code points (the font is embedded in the gfx package).
const (
	icPlay     = "\ue037"
	icPause    = "\ue034"
	icCut      = "\ue14e"
	icDelete   = "\ue872"
	icUndo     = "\ue166"
	icRestore  = "\ue8ba"
	icRedo     = "\ue15a"
	icAdd      = "\ue145"
	icMusic    = "\ue405"
	icFont     = "\ue262"
	icVolume   = "\ue050"
	icMute     = "\ue04f"
	icFolder   = "\ue2c8"
	icVideo    = "\ue04b"
	icImage    = "\ue3f4"
	icSearch   = "\ue8b6"
	icZoomIn   = "\ue8ff"
	icZoomOut  = "\ue900"
	icCheck    = "\ue5ca"
	icClose    = "\ue5cd"
	icStop     = "\ue047"
	icExport   = "\uf09b"
	icCopy     = "\ue14d"
	icFit      = "\uea10"
	icSpeed    = "\ue9e4"
	icImport   = "\ueb82"
	icSettings = "\ue8b8"
	icEye      = "\ue8f4"
	icHide     = "\ue8f5"
	icPaste    = "\ue14f"
	icSubtitle = "\ue048"
	icInfo     = "\ue88e"
	icPanelOut = "\ueac9"
	icPanelIn  = "\ueac3"
)

// textColors are the caption colour swatches.
var textColors = [][3]uint8{
	{255, 255, 255}, {20, 20, 20}, {255, 214, 10}, {255, 92, 92}, {80, 200, 120}, {80, 160, 255}, {255, 140, 200}, {255, 160, 60},
}
