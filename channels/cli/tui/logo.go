package tui

import "strings"

// logoBack and logoFront are the two strokes of the lana mark
// (web/ui/public/icons/logo-mark.svg) drawn with quadrant block characters:
// the back stroke "/" is the dimmed one and the front stroke "\" covers it
// where they meet at the apex. Each row of both halves joins into one row of
// logoWidth cells.
var (
	logoBack = []string{
		"    ▟",
		"   ▟█",
		"  ▟███",
		" ▟███▘",
		"▟███▘",
	}
	logoFront = []string{
		"███▙    ",
		"████▙   ",
		"▀███▙  ",
		" ▝███▙ ",
		"   ▝███▙",
	}
)

// logoWidth is the cell width of every logo row.
const logoWidth = 13

// logoWord is the letter-spaced wordmark of the web sidebar.
const logoWord = "L Λ N Λ"

// Logo returns the mark as rows of logoWidth cells. The strokes are styled
// with the theme colors; lipgloss drops the styling when the output has no
// color support.
func Logo() []string {
	rows := make([]string, len(logoBack))
	for i := range logoBack {
		rows[i] = styleLogoBack.Render(logoBack[i]) + styleLogoFront.Render(logoFront[i])
	}
	return rows
}

// LogoText returns the mark and the wordmark side by side, for plain command
// output such as `lana --help`.
func LogoText() string {
	mark := Logo()
	var b strings.Builder
	for i, row := range mark {
		b.WriteString("  ")
		b.WriteString(row)
		if i == len(mark)/2 {
			b.WriteString("   ")
			b.WriteString(styleLogoFront.Bold(true).Render(logoWord))
		}
		b.WriteString("\n")
	}
	return b.String()
}
