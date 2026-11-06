package tui

import "github.com/charmbracelet/lipgloss"

var (
	colorAccent   = lipgloss.AdaptiveColor{Light: "25", Dark: "39"}
	colorMuted    = lipgloss.AdaptiveColor{Light: "243", Dark: "244"}
	colorDim      = lipgloss.AdaptiveColor{Light: "246", Dark: "240"}
	colorOK       = lipgloss.AdaptiveColor{Light: "28", Dark: "42"}
	colorErr      = lipgloss.AdaptiveColor{Light: "160", Dark: "203"}
	colorWarn     = lipgloss.AdaptiveColor{Light: "136", Dark: "214"}
	colorInline   = lipgloss.AdaptiveColor{Light: "236", Dark: "252"}
	colorInlineBG = lipgloss.AdaptiveColor{Light: "254", Dark: "237"}
	// The logo strokes follow the web mark: one ink color, the back stroke
	// at reduced strength.
	colorLogoFront = lipgloss.AdaptiveColor{Light: "235", Dark: "255"}
	colorLogoBack  = lipgloss.AdaptiveColor{Light: "247", Dark: "243"}
)

var (
	styleAccent  = lipgloss.NewStyle().Foreground(colorAccent)
	styleMuted   = lipgloss.NewStyle().Foreground(colorMuted)
	styleDim     = lipgloss.NewStyle().Foreground(colorDim)
	styleOK      = lipgloss.NewStyle().Foreground(colorOK)
	styleErr     = lipgloss.NewStyle().Foreground(colorErr)
	styleWarn    = lipgloss.NewStyle().Foreground(colorWarn)
	styleBold    = lipgloss.NewStyle().Bold(true)
	styleItalic  = lipgloss.NewStyle().Italic(true)
	styleCode    = lipgloss.NewStyle().Foreground(colorInline).Background(colorInlineBG)
	styleUser    = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	styleTool    = lipgloss.NewStyle().Bold(true)
	styleHeading = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)

	styleLogoFront = lipgloss.NewStyle().Foreground(colorLogoFront)
	styleLogoBack  = lipgloss.NewStyle().Foreground(colorLogoBack)
)
