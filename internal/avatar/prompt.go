package avatar

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Fallback user messages when the owner gave no description (REQ-PXA-005,
// REQ-PXA-006).
const (
	partsFallbackPrompt = "Surprise me with a friendly, cute character."
	drawFallbackPrompt  = "Draw a friendly, cute character with a new look."
)

// partsSystemPrompt lists the catalog and asks for one JSON parts object
// (REQ-PXA-005).
func partsSystemPrompt() string {
	c := Catalog()
	var b strings.Builder
	b.WriteString("You pick the parts of a friendly chibi pixel-art avatar from a description of its owner.\n")
	b.WriteString("Answer with ONE JSON object and nothing else: no prose, no code fence.\n")
	b.WriteString(`{"species": "...", "hair": "...", "hair_color": "...", "skin": "...", "eyes": "...", "accessory": "...", "outfit": "...", "pants": "...", "blush": true, "accessory_color": "#rrggbb"}`)
	b.WriteString("\n\nUse only these ids:\n")
	writeCatalogField(&b, "species", c.Species)
	writeCatalogField(&b, "hair", c.Hair)
	writeCatalogField(&b, "hair_color", c.HairColor)
	writeCatalogField(&b, "skin", c.Skin)
	writeCatalogField(&b, "eyes", c.Eyes)
	writeCatalogField(&b, "accessory", c.Accessory)
	writeCatalogField(&b, "outfit", c.Outfit)
	writeCatalogField(&b, "pants", c.Pants)
	b.WriteString("- blush: true or false\n")
	b.WriteString("- accessory_color: any lower-case #rrggbb colour\n")
	b.WriteString("\nMatch the owner's description. Answer only the JSON object.\n")
	return b.String()
}

// writeCatalogField writes one catalog line: "- name: id, id, ...".
func writeCatalogField(b *strings.Builder, name string, items []CatalogItem) {
	b.WriteString("- " + name + ": ")
	for i, item := range items {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(item.ID)
	}
	b.WriteString("\n")
}

// partsUserPrompt is the user message of the parts mode: the description (or
// a fallback) and the seed (REQ-PXA-005).
func partsUserPrompt(desc string, seed int64) string {
	return promptWithSeed(desc, partsFallbackPrompt, seed)
}

// drawSystemPrompt explains the sprite format and the template rules of
// REQ-PXA-003 and shows the agent's default sprite as a worked example
// (REQ-PXA-006).
func drawSystemPrompt(agent, color string) string {
	example, err := json.MarshalIndent(DefaultFor(agent, color).Sprite, "", "  ")
	if err != nil {
		example = []byte("{}")
	}
	var b strings.Builder
	b.WriteString("You draw a friendly chibi pixel-art character as a sprite on a 16-wide, 20-tall grid (x to the right, y down).\n")
	b.WriteString("Answer with ONE JSON object and nothing else: no prose, no code fence.\n")
	b.WriteString(`{"palette": {"<key>": "#rrggbb", ...}, "head": ["<16 chars>", ... 20 rows], "body": ["<16 chars>", ... 20 rows]}`)
	b.WriteString("\n\nThe rules, follow them exactly:\n")
	b.WriteString("- Two layers: \"body\" is drawn first, \"head\" over it. Each layer is exactly 20 strings of exactly 16 characters.\n")
	b.WriteString("- Each character is '.' for an empty cell or one palette key. A palette key is a single character [a-zA-Z0-9]; a sprite uses at most 24 keys.\n")
	b.WriteString("- Palette colours are lower-case #rrggbb. The key 'e' marks the eye cells and the key 's' the main skin (or metal) tone; both keys are required.\n")
	b.WriteString("- Every filled body cell must stay inside x 3..12, y 12..17 (the shirt, arms, pants, legs and shoes).\n")
	b.WriteString("- Every filled head cell must be at or above y 16; hair and accessories may use x 0..15 and y 0..16.\n")
	b.WriteString("- The head layer must show a full head in x 3..12, y 3..11 (at least 30 filled cells there) and 2..8 eye cells ('e') in y 4..11.\n")
	b.WriteString("- Recommended: a chin line in row y 12, x 5..10, in a dark colour.\n")
	b.WriteString("- Do not draw any outline: the renderer adds the outline around the filled cells.\n")
	b.WriteString("- Keep the proportions of the example below EXACTLY. Change only the hair, the face details, the accessories, the clothes and the colours.\n")
	b.WriteString("\nExample:\n")
	b.Write(example)
	b.WriteString("\n\nAnswer only the JSON object.\n")
	return b.String()
}

// drawUserPrompt is the user message of the free-draw mode: the description
// (or a fallback) and the seed (REQ-PXA-006).
func drawUserPrompt(desc string, seed int64) string {
	return promptWithSeed(desc, drawFallbackPrompt, seed)
}

// modeUserPrompt is the user message of a mode: the description (or fallback)
// and the seed (REQ-PXA-005, REQ-PXA-006).
func modeUserPrompt(req Request, seed int64) string {
	if req.Mode == ModeAIDraw {
		return drawUserPrompt(req.Prompt, seed)
	}
	return partsUserPrompt(req.Prompt, seed)
}

// refusalPrompt is the user message that hands a refused reply back to the
// model on the retry (REQ-PXA-006, REQ-PXA-007).
func refusalPrompt(reason string) string {
	return fmt.Sprintf("Your answer was refused: %s. Answer again with the corrected JSON object only, keeping the same template.", reason)
}

// promptWithSeed joins a trimmed description (or fallback) with the seed.
func promptWithSeed(desc, fallback string, seed int64) string {
	desc = strings.TrimSpace(desc)
	if desc == "" {
		desc = fallback
	}
	return fmt.Sprintf("%s\nVariation seed: %d", desc, seed)
}

// partsReply is the JSON object the model answers in the parts mode
// (REQ-PXA-005). Blush is a pointer so a missing value is filled from the
// seed rather than read as false.
type partsReply struct {
	Species        string `json:"species"`
	Hair           string `json:"hair"`
	HairColor      string `json:"hair_color"`
	Skin           string `json:"skin"`
	Eyes           string `json:"eyes"`
	Accessory      string `json:"accessory"`
	Outfit         string `json:"outfit"`
	Pants          string `json:"pants"`
	Blush          *bool  `json:"blush"`
	AccessoryColor string `json:"accessory_color"`
}

// parseParts reads the first JSON object of a reply and fills every missing or
// unknown value from RandomParts(seed) (REQ-PXA-005). A reply with no JSON
// object is an error, so the caller retries.
func parseParts(reply string, seed int64) (Parts, error) {
	obj, ok := firstJSONObject(reply)
	if !ok {
		return Parts{}, errors.New("avatar: the reply has no JSON object")
	}
	var r partsReply
	if err := json.Unmarshal([]byte(obj), &r); err != nil {
		return Parts{}, fmt.Errorf("avatar: the parts reply is not JSON: %w", err)
	}
	base := RandomParts(seed)
	p := Parts{
		Species:        pickIn(speciesOrder, r.Species, base.Species),
		Hair:           pickIn(hairOrder, r.Hair, base.Hair),
		HairColor:      pickIn(hairColorOrder, r.HairColor, base.HairColor),
		Skin:           pickIn(skinOrder, r.Skin, base.Skin),
		Eyes:           pickIn(eyesOrder, r.Eyes, base.Eyes),
		Accessory:      pickIn(accessoryOrder, r.Accessory, base.Accessory),
		Outfit:         pickIn(outfitOrder, r.Outfit, base.Outfit),
		Pants:          pickIn(pantsOrder, r.Pants, base.Pants),
		Blush:          base.Blush,
		Color:          base.Color,
		AccessoryColor: base.AccessoryColor,
	}
	if r.Blush != nil {
		p.Blush = *r.Blush
	}
	if validHex(r.AccessoryColor) {
		p.AccessoryColor = strings.ToLower(r.AccessoryColor)
	}
	return p, nil
}

// parseDrawn reads the first JSON object of a reply as a sprite, lower-cases
// its palette, repairs it against the worked example's palette, and validates
// it (REQ-PXA-006). A reply with no JSON object or an unrepairable sprite is an
// error, so the caller retries.
func parseDrawn(reply string, example map[string]string) (Sprite, error) {
	obj, ok := firstJSONObject(reply)
	if !ok {
		return Sprite{}, errors.New("avatar: the reply has no JSON object")
	}
	var s Sprite
	if err := json.Unmarshal([]byte(obj), &s); err != nil {
		return Sprite{}, fmt.Errorf("avatar: the sprite reply is not JSON: %w", err)
	}
	palette := make(map[string]string, len(s.Palette))
	for k, v := range s.Palette {
		palette[k] = strings.ToLower(v)
	}
	s.Palette = palette
	if err := repairDrawn(&s, example); err != nil {
		return Sprite{}, err
	}
	if err := ValidateSprite(s); err != nil {
		return Sprite{}, err
	}
	return s, nil
}

// repairDrawn pads a model reply toward the sprite shape and fills palette
// keys it uses but did not declare from the worked example's palette
// (REQ-PXA-006). It only runs on a model reply: ValidateSprite stays strict for
// a saved sprite. A row shorter than 16 characters is right-padded with '.',
// a layer with fewer than 20 rows with empty rows at the bottom; a longer row
// or layer is refused. A key in neither palette stays, so ValidateSprite
// refuses it.
func repairDrawn(sprite *Sprite, example map[string]string) error {
	layers := []struct {
		name string
		rows *[]string
	}{
		{"head", &sprite.Head},
		{"body", &sprite.Body},
	}
	for _, layer := range layers {
		rows := *layer.rows
		if len(rows) > spriteH {
			return fmt.Errorf("avatar: the %s layer has %d rows, more than %d", layer.name, len(rows), spriteH)
		}
		fixed := make([]string, spriteH)
		for y := 0; y < spriteH; y++ {
			if y >= len(rows) {
				fixed[y] = strings.Repeat(".", spriteW)
				continue
			}
			row := rows[y]
			if len(row) > spriteW {
				return fmt.Errorf("avatar: the %s layer row %d has %d characters, more than %d", layer.name, y, len(row), spriteW)
			}
			fixed[y] = row + strings.Repeat(".", spriteW-len(row))
		}
		*layer.rows = fixed
	}
	for _, rows := range [][]string{sprite.Head, sprite.Body} {
		for _, row := range rows {
			for i := 0; i < len(row); i++ {
				c := row[i]
				if c == '.' {
					continue
				}
				key := string(c)
				if _, ok := sprite.Palette[key]; ok {
					continue
				}
				if color, ok := example[key]; ok {
					sprite.Palette[key] = color
				}
			}
		}
	}
	return nil
}

// pickIn returns value when it is one of list, else fallback.
func pickIn(list []string, value, fallback string) string {
	if inList(list, value) {
		return value
	}
	return fallback
}

// firstJSONObject returns the first balanced {...} object in s, ignoring code
// fences and prose around it. It tracks string literals so braces inside a
// string do not count.
func firstJSONObject(s string) (string, bool) {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return "", false
	}
	depth, inString, escaped := 0, false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}
	return "", false
}
