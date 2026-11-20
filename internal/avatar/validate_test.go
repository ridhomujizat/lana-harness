package avatar

import (
	"strings"
	"testing"
)

// AC-PXA-002: ValidateSprite refuses every wrong shape with a reason.
func TestValidateSpriteRefuses(t *testing.T) {
	cases := map[string]func(*Sprite){
		"wrong row count":     func(s *Sprite) { s.Head = s.Head[:spriteH-1] },
		"wrong row length":    func(s *Sprite) { s.Body[0] = s.Body[0][:spriteW-1] },
		"unknown key":         func(s *Sprite) { setHead(s, 5, 5, 'z') },
		"bad hex":             func(s *Sprite) { s.Palette["k"] = "#xyz123" },
		"25 colours":          func(s *Sprite) { s.Palette = bigPalette(25) },
		"body cell at (2,13)": func(s *Sprite) { setBody(s, 2, 13, 'c') },
		"head cell at y 17":   func(s *Sprite) { setHead(s, 5, 17, 's') },
		"1 eye":               func(s *Sprite) { setHead(s, 7, 7, 's'); setHead(s, 11, 7, 's'); setHead(s, 11, 8, 's') },
		"9 eyes": func(s *Sprite) {
			for _, x := range []int{5, 6, 8, 9, 10} {
				setHead(s, x, 7, 'e')
			}
		},
		"eye at y 4":            func(s *Sprite) { setHead(s, 7, 4, 'e') },
		"tiny head":             tinyHead,
		"10-cell body":          func(s *Sprite) { tenCellBody(s) },
		"missing s":             func(s *Sprite) { delete(s.Palette, "s") },
		"missing e":             func(s *Sprite) { delete(s.Palette, "e") },
		"key not one character": func(s *Sprite) { s.Palette["zz"] = "#000000" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			sprite := cloneSprite(baseSprite(t))
			mutate(&sprite)
			err := ValidateSprite(sprite)
			if err == nil {
				t.Fatalf("%s: ValidateSprite accepted a bad sprite", name)
			}
			if !strings.HasPrefix(err.Error(), "avatar:") {
				t.Fatalf("%s: error %q has no reason", name, err)
			}
		})
	}
}

// bigPalette is n single-character keys with a valid colour.
func bigPalette(n int) map[string]string {
	keys := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	out := make(map[string]string, n)
	for i := 0; i < n && i < len(keys); i++ {
		out[string(keys[i])] = "#000000"
	}
	return out
}

// tinyHead leaves only the eyes in the head box.
func tinyHead(s *Sprite) {
	for y := 3; y <= 11; y++ {
		s.Head[y] = strings.Repeat(".", spriteW)
	}
	setHead(s, 7, 7, 'e')
	setHead(s, 7, 8, 'e')
}

// tenCellBody leaves ten filled body cells.
func tenCellBody(s *Sprite) {
	for y := 12; y <= 17; y++ {
		s.Body[y] = strings.Repeat(".", spriteW)
	}
	for x := 5; x <= 10; x++ {
		setBody(s, x, 13, 'c')
	}
	for x := 5; x <= 8; x++ {
		setBody(s, x, 14, 'c')
	}
}

// REQ-PXA-009: a parts avatar is re-rendered with the new colour.
func TestNormalizeParts(t *testing.T) {
	d := DefaultFor("coder", "")
	got, err := Normalize(d, "#123456")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindParts || got.Parts == nil || got.Parts.Color != "#123456" {
		t.Fatalf("Normalize = %+v", got)
	}
	want, err := Render(*got.Parts)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSprite(got.Sprite); err != nil {
		t.Fatalf("re-rendered sprite: %v", err)
	}
	if got.Sprite.Head[7] != want.Head[7] || got.Sprite.Palette["c"] != want.Palette["c"] {
		t.Fatalf("Normalize did not re-render the sprite")
	}
	// No colour keeps the part colour.
	again, err := Normalize(d, "")
	if err != nil || again.Parts.Color != d.Parts.Color {
		t.Fatalf("Normalize without colour = %+v, %v", again.Parts, err)
	}
}

// A parts avatar with a bad value is refused.
func TestNormalizePartsRefuses(t *testing.T) {
	p := baseParts()
	p.Hair = "mullet"
	if _, err := Normalize(Document{Kind: KindParts, Parts: &p}, ""); err == nil {
		t.Fatal("Normalize accepted a bad part")
	}
	if _, err := Normalize(Document{Kind: KindParts}, ""); err == nil {
		t.Fatal("Normalize accepted parts-less parts avatar")
	}
}

// REQ-PXA-009: a drawn sprite is lower-cased, validated, and its prompt trimmed.
func TestNormalizeDrawn(t *testing.T) {
	sprite := baseSprite(t)
	sprite.Palette["c"] = "#7C5CFF"
	d := Document{Kind: KindDrawn, Sprite: sprite, Prompt: strings.Repeat("x", maxPrompt+50), Parts: &Parts{}}
	got, err := Normalize(d, "#111111")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindDrawn || got.Parts != nil {
		t.Fatalf("drawn Normalize kept parts or changed kind: %+v", got)
	}
	if got.Sprite.Palette["c"] != "#7c5cff" {
		t.Fatalf("palette not lower-cased: %q", got.Sprite.Palette["c"])
	}
	if len([]rune(got.Prompt)) != maxPrompt {
		t.Fatalf("prompt not trimmed: %d runes", len([]rune(got.Prompt)))
	}
	if err := ValidateSprite(got.Sprite); err != nil {
		t.Fatalf("normalized drawn sprite: %v", err)
	}
}

func TestNormalizeDrawnRefuses(t *testing.T) {
	sprite := baseSprite(t)
	sprite.Body[0] = sprite.Body[0][:spriteW-1]
	if _, err := Normalize(Document{Kind: KindDrawn, Sprite: sprite}, ""); err == nil {
		t.Fatal("Normalize accepted a bad drawn sprite")
	}
}

func TestNormalizeUnknownKind(t *testing.T) {
	if _, err := Normalize(Document{Kind: "blob"}, ""); err == nil {
		t.Fatal("Normalize accepted an unknown kind")
	}
}
