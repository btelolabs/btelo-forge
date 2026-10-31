package ui

import "github.com/btelolabs/btelo-forge/internal/gfx"

// The palette is a ladder of cool, near-black greys — chrome, panels, controls, hover, selection — with hairline
// borders between them. Colour is kept for what needs to stand out: the light primary action, the blue of focus,
// progress and on-canvas handles, and the hues that tell timeline items apart.
var (
	colBackground = gfx.Hex(0x0c0d10) // the window chrome: tab strip, menu bar, status bar
	colPanel      = gfx.Hex(0x131418) // panels and the work area
	colSurface    = gfx.Hex(0x1b1c21) // controls and cards
	colSurfaceHi  = gfx.Hex(0x24262c) // a control under the mouse
	colSelected   = gfx.Hex(0x2b2d35) // the chosen row, chip, tool or menu item
	colBorder     = gfx.Hex(0x25272d) // hairlines
	colBorderHi   = gfx.Hex(0x383a42) // hairlines of a control under the mouse, or chosen
	colText       = gfx.Hex(0xe6e7ea)
	colDim        = gfx.Hex(0x9a9ca5)
	colFaint      = gfx.Hex(0x5c5f68)
	colPrimary    = gfx.Hex(0xe6e7ea) // the one main action of a place
	colPrimaryHi  = gfx.Hex(0xffffff)
	colOnPrimary  = gfx.Hex(0x0c0d10)
	colAccent     = gfx.Hex(0x4c8dff) // focus, progress, live state, handles on the canvas
	colAccentHi   = gfx.Hex(0x74a6ff)
	colOnAccent   = gfx.Hex(0xffffff) // text and icons on blue
	colDanger     = gfx.Hex(0xf0605d)
	colDangerText = gfx.Hex(0xffb4b2) // error messages, readable on the dark greys
	colSuccess    = gfx.Hex(0x3ecf8e)
	colWarning    = gfx.Hex(0xf2c879)
	colVideo      = gfx.Hex(0x262b33)
	colVideoUpper = gfx.Hex(0x2c3340)
	colMusic      = gfx.Hex(0x1d4a3d)
	colMusicHi    = gfx.Hex(0x26604f)
	colCaption    = gfx.Hex(0x5a46a8)
	colCaptionHi  = gfx.Hex(0x6c57c0)
	colImage      = gfx.Hex(0x8a5526)
	colImageHi    = gfx.Hex(0xa1652e)
	colPlayhead   = gfx.Hex(0xf5f6f8)
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
	icPin      = "\uf10d"
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
	icVoice    = "\ue91f"
	icInfo     = "\ue88e"
	icRecord   = "\ue061"
	icCamera   = "\ue412"
	// icFullscreen and icFullscreenExit enter and leave player mode.
	icFullscreen     = "\ue5d0"
	icFullscreenExit = "\ue5d1"
	// The asset manager's symbols.
	icDescription = "\ue873"
	icCode        = "\ue86f"
	icFile        = "\ue24d"
	icLabel       = "\ue892"
	icLabelOff    = "\ue9b6"
	icInbox       = "\ue156"
	icMouse       = "\ue323"
	icCategory    = "\ue574"
	icBack        = "\ue5c4"
	icForward     = "\ue5c8"
	icUpward      = "\ue5d8"
	icFolderOpen  = "\ue2c7"
	icSort        = "\ue164"
	icLink        = "\ue157"
)

// textColors are the caption colour swatches.
var textColors = [][3]uint8{
	{255, 255, 255}, {20, 20, 20}, {255, 214, 10}, {255, 92, 92}, {80, 200, 120}, {80, 160, 255}, {255, 140, 200}, {255, 160, 60},
}
