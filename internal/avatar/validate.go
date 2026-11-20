package avatar

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// keyPattern is the shape of a palette key (REQ-PXA-003).
var keyPattern = regexp.MustCompile(`^[a-zA-Z0-9]$`)

// hexPattern is the shape of a palette colour after lower-casing (REQ-PXA-003).
var hexPattern = regexp.MustCompile(`^#[0-9a-f]{6}$`)

// ValidateSprite accepts only the sprite shape of the Definitions and the
// template rules of REQ-PXA-003. Every error says which rule failed.
func ValidateSprite(s Sprite) error {
	if len(s.Palette) < 1 || len(s.Palette) > 24 {
		return fmt.Errorf("avatar: the palette must have 1..24 colours, has %d", len(s.Palette))
	}
	for k, v := range s.Palette {
		if !keyPattern.MatchString(k) {
			return fmt.Errorf("avatar: palette key %q is not one character [a-zA-Z0-9]", k)
		}
		if !hexPattern.MatchString(strings.ToLower(v)) {
			return fmt.Errorf("avatar: palette colour %q of key %q is not #rrggbb", v, k)
		}
	}
	if _, ok := s.Palette["e"]; !ok {
		return errors.New(`avatar: the palette needs the eye key "e"`)
	}
	if _, ok := s.Palette["s"]; !ok {
		return errors.New(`avatar: the palette needs the skin key "s"`)
	}
	if err := checkRows(s.Head, "head"); err != nil {
		return err
	}
	if err := checkRows(s.Body, "body"); err != nil {
		return err
	}

	// Head layer: every cell in y 0..16, eyes in y 4..11, a real head.
	eyeCells, headBox := 0, 0
	for y, row := range s.Head {
		for x := 0; x < spriteW; x++ {
			c := row[x]
			if c == '.' {
				continue
			}
			if _, ok := s.Palette[string(c)]; !ok {
				return fmt.Errorf("avatar: the head layer uses key %q at (%d,%d) which is not in the palette", string(c), x, y)
			}
			if y > 16 {
				return fmt.Errorf("avatar: head cell at (%d,%d) is below y 16", x, y)
			}
			if c == 'e' {
				eyeCells++
				if y < 4 || y > 11 {
					return fmt.Errorf("avatar: eye cell at (%d,%d) is outside y 4..11", x, y)
				}
			}
			if x >= 3 && x <= 12 && y >= 3 && y <= 11 {
				headBox++
			}
		}
	}
	if eyeCells < 2 || eyeCells > 8 {
		return fmt.Errorf("avatar: the head layer needs 2..8 eye cells, has %d", eyeCells)
	}
	if headBox < 30 {
		return fmt.Errorf("avatar: the head layer needs at least 30 cells in x 3..12, y 3..11, has %d", headBox)
	}

	// Body layer: every cell in x 3..12, y 12..17.
	bodyCells := 0
	for y, row := range s.Body {
		for x := 0; x < spriteW; x++ {
			c := row[x]
			if c == '.' {
				continue
			}
			if _, ok := s.Palette[string(c)]; !ok {
				return fmt.Errorf("avatar: the body layer uses key %q at (%d,%d) which is not in the palette", string(c), x, y)
			}
			if x < 3 || x > 12 || y < 12 || y > 17 {
				return fmt.Errorf("avatar: body cell at (%d,%d) is outside x 3..12, y 12..17", x, y)
			}
			bodyCells++
		}
	}
	if bodyCells < 14 {
		return fmt.Errorf("avatar: the body layer needs at least 14 cells, has %d", bodyCells)
	}
	return nil
}

// checkRows checks that a layer is spriteH rows of spriteW characters.
func checkRows(rows []string, name string) error {
	if len(rows) != spriteH {
		return fmt.Errorf("avatar: the %s layer must have %d rows, has %d", name, spriteH, len(rows))
	}
	for y, row := range rows {
		if len(row) != spriteW {
			return fmt.Errorf("avatar: the %s layer row %d must have %d characters, has %d", name, y, spriteW, len(row))
		}
	}
	return nil
}

// Normalize checks an avatar document (REQ-PXA-009, API-PXA-003). A parts
// avatar is validated and re-rendered from the parts and color; a drawn sprite
// is lower-cased, validated, and its prompt trimmed to 500 runes. An unknown
// kind is an error.
func Normalize(d Document, color string) (Document, error) {
	switch d.Kind {
	case KindParts:
		if d.Parts == nil {
			return Document{}, errors.New("avatar: a parts avatar needs parts")
		}
		p := *d.Parts
		if err := validateParts(p); err != nil {
			return Document{}, err
		}
		if color != "" {
			p.Color = color
		}
		sprite, err := Render(p)
		if err != nil {
			return Document{}, err
		}
		return Document{Kind: KindParts, Parts: &p, Sprite: sprite}, nil
	case KindDrawn:
		palette := make(map[string]string, len(d.Sprite.Palette))
		for k, v := range d.Sprite.Palette {
			palette[k] = strings.ToLower(v)
		}
		sprite := Sprite{Palette: palette, Head: d.Sprite.Head, Body: d.Sprite.Body}
		if err := ValidateSprite(sprite); err != nil {
			return Document{}, err
		}
		return Document{Kind: KindDrawn, Sprite: sprite, Prompt: trimPrompt(d.Prompt)}, nil
	default:
		return Document{}, fmt.Errorf("avatar: unknown kind %q", d.Kind)
	}
}

// maxPrompt is the longest stored drawn-avatar prompt (API-PXA-002).
const maxPrompt = 500

func trimPrompt(prompt string) string {
	if runes := []rune(prompt); len(runes) > maxPrompt {
		return string(runes[:maxPrompt])
	}
	return prompt
}
