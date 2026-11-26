package ui

import (
	"image/color"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// Theme chooses the interface's colour scheme.
type Theme string

const (
	ThemeSystem Theme = "system"
	ThemeLight  Theme = "light"
	ThemeDark   Theme = "dark"
)

var themes = []Theme{ThemeSystem, ThemeLight, ThemeDark}

type themePalette struct {
	background color.RGBA
	panel      color.RGBA
	surface    color.RGBA
	surfaceHi  color.RGBA
	selected   color.RGBA
	border     color.RGBA
	borderHi   color.RGBA
	text       color.RGBA
	dim        color.RGBA
	faint      color.RGBA
	primary    color.RGBA
	primaryHi  color.RGBA
	onPrimary  color.RGBA
	accent     color.RGBA
	accentHi   color.RGBA
	onAccent   color.RGBA
	danger     color.RGBA
	dangerText color.RGBA
	success    color.RGBA
	warning    color.RGBA
	video      color.RGBA
	videoUpper color.RGBA
	music      color.RGBA
	musicHi    color.RGBA
	caption    color.RGBA
	captionHi  color.RGBA
	image      color.RGBA
	imageHi    color.RGBA
	playhead   color.RGBA
	well       color.RGBA
	shadow     color.RGBA
	scrim      color.RGBA
}

var darkPalette = themePalette{
	background: gfx.Hex(0x0c0d10),
	panel:      gfx.Hex(0x131418),
	surface:    gfx.Hex(0x1b1c21),
	surfaceHi:  gfx.Hex(0x24262c),
	selected:   gfx.Hex(0x2b2d35),
	border:     gfx.Hex(0x25272d),
	borderHi:   gfx.Hex(0x383a42),
	text:       gfx.Hex(0xe6e7ea),
	dim:        gfx.Hex(0x9a9ca5),
	faint:      gfx.Hex(0x5c5f68),
	primary:    gfx.Hex(0xe6e7ea),
	primaryHi:  gfx.Hex(0xffffff),
	onPrimary:  gfx.Hex(0x0c0d10),
	accent:     gfx.Hex(0x4c8dff),
	accentHi:   gfx.Hex(0x74a6ff),
	onAccent:   gfx.Hex(0xffffff),
	danger:     gfx.Hex(0xf0605d),
	dangerText: gfx.Hex(0xffb4b2),
	success:    gfx.Hex(0x3ecf8e),
	warning:    gfx.Hex(0xf2c879),
	video:      gfx.Hex(0x262b33),
	videoUpper: gfx.Hex(0x2c3340),
	music:      gfx.Hex(0x1d4a3d),
	musicHi:    gfx.Hex(0x26604f),
	caption:    gfx.Hex(0x5a46a8),
	captionHi:  gfx.Hex(0x6c57c0),
	image:      gfx.Hex(0x8a5526),
	imageHi:    gfx.Hex(0xa1652e),
	playhead:   gfx.Hex(0xf5f6f8),
	well:       gfx.Mix(gfx.Hex(0x1b1c21), gfx.Hex(0x000000), 0.3),
	shadow:     gfx.Alpha(gfx.Hex(0x000000), 0.4),
	scrim:      gfx.Alpha(gfx.Hex(0x000000), 0.55),
}

var lightPalette = themePalette{
	background: gfx.Hex(0xeeeff3),
	panel:      gfx.Hex(0xf8f9fb),
	surface:    gfx.Hex(0xffffff),
	surfaceHi:  gfx.Hex(0xedeff3),
	selected:   gfx.Hex(0xe3e6ed),
	border:     gfx.Hex(0xe0e3e9),
	borderHi:   gfx.Hex(0xc2c6d1),
	text:       gfx.Hex(0x16181d),
	dim:        gfx.Hex(0x5a5e6b),
	faint:      gfx.Hex(0x737887),
	primary:    gfx.Hex(0x181a20),
	primaryHi:  gfx.Hex(0x2c303a),
	onPrimary:  gfx.Hex(0xffffff),
	accent:     gfx.Hex(0x2563eb),
	accentHi:   gfx.Hex(0x1d4ed8),
	onAccent:   gfx.Hex(0xffffff),
	danger:     gfx.Hex(0xdc3535),
	dangerText: gfx.Hex(0xb91c1c),
	success:    gfx.Hex(0x15803d),
	warning:    gfx.Hex(0xb45309),
	video:      gfx.Hex(0xdde2ec),
	videoUpper: gfx.Hex(0xcfd6e4),
	music:      gfx.Hex(0xcdece1),
	musicHi:    gfx.Hex(0xb8e3d4),
	caption:    gfx.Hex(0xdfd8f7),
	captionHi:  gfx.Hex(0xcfc5f2),
	image:      gfx.Hex(0xf5e2cc),
	imageHi:    gfx.Hex(0xedd2b4),
	playhead:   gfx.Hex(0x16181d),
	well:       gfx.Hex(0xf0f2f6),
	shadow:     gfx.Alpha(gfx.Hex(0x000000), 0.12),
	scrim:      gfx.Alpha(gfx.Hex(0x000000), 0.28),
}

var darkTheme = true

// isDarkTheme reports whether the active palette is the dark one.
func isDarkTheme() bool { return darkTheme }

// applyTheme switches the active UI palette between dark and light.
func applyTheme(dark bool) {
	darkTheme = dark
	p := darkPalette
	if !dark {
		p = lightPalette
	}
	colBackground = p.background
	colPanel = p.panel
	colSurface = p.surface
	colSurfaceHi = p.surfaceHi
	colSelected = p.selected
	colBorder = p.border
	colBorderHi = p.borderHi
	colText = p.text
	colDim = p.dim
	colFaint = p.faint
	colPrimary = p.primary
	colPrimaryHi = p.primaryHi
	colOnPrimary = p.onPrimary
	colAccent = p.accent
	colAccentHi = p.accentHi
	colOnAccent = p.onAccent
	colDanger = p.danger
	colDangerText = p.dangerText
	colSuccess = p.success
	colWarning = p.warning
	colVideo = p.video
	colVideoUpper = p.videoUpper
	colMusic = p.music
	colMusicHi = p.musicHi
	colCaption = p.caption
	colCaptionHi = p.captionHi
	colImage = p.image
	colImageHi = p.imageHi
	colPlayhead = p.playhead
	colWell = p.well
	colShadow = p.shadow
	gfxShade = p.scrim
}

// The palette is a ladder of neutral greys — chrome, panels, controls, hover, selection — with hairline
// borders between them. Colour is kept for what needs to stand out: the primary action, the blue of focus,
// progress and on-canvas handles, and the hues that tell timeline items apart.
var (
	colBackground = darkPalette.background // the window chrome: tab strip, menu bar, status bar
	colPanel      = darkPalette.panel      // panels and the work area
	colSurface    = darkPalette.surface    // controls and cards
	colSurfaceHi  = darkPalette.surfaceHi  // a control under the mouse
	colSelected   = darkPalette.selected   // the chosen row, chip, tool or menu item
	colBorder     = darkPalette.border     // hairlines
	colBorderHi   = darkPalette.borderHi   // hairlines of a control under the mouse, or chosen
	colText       = darkPalette.text
	colDim        = darkPalette.dim
	colFaint      = darkPalette.faint
	colPrimary    = darkPalette.primary // the one main action of a place
	colPrimaryHi  = darkPalette.primaryHi
	colOnPrimary  = darkPalette.onPrimary
	colAccent     = darkPalette.accent // focus, progress, live state, handles on the canvas
	colAccentHi   = darkPalette.accentHi
	colOnAccent   = darkPalette.onAccent // text and icons on blue
	colDanger     = darkPalette.danger
	colDangerText = darkPalette.dangerText // error messages, readable on the chrome greys
	colSuccess    = darkPalette.success
	colWarning    = darkPalette.warning
	colVideo      = darkPalette.video
	colVideoUpper = darkPalette.videoUpper
	colMusic      = darkPalette.music
	colMusicHi    = darkPalette.musicHi
	colCaption    = darkPalette.caption
	colCaptionHi  = darkPalette.captionHi
	colImage      = darkPalette.image
	colImageHi    = darkPalette.imageHi
	colPlayhead   = darkPalette.playhead
	colWell       = darkPalette.well
	colShadow     = darkPalette.shadow
	gfxShade      = darkPalette.scrim
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
	// The video player's.
	icReplay10  = "\ue059"
	icForward10 = "\ue056"
	icEdit      = "\ue3c9"
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
