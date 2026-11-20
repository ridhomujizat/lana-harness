package avatar

import "testing"

// baseParts is a valid starting point for render tests.
func baseParts() Parts {
	return Parts{
		Species:        "human",
		Hair:           "short",
		HairColor:      "dark",
		Skin:           "light",
		Eyes:           "dot",
		Accessory:      "none",
		Outfit:         "plain",
		Pants:          "denim",
		Color:          "#7c5cff",
		AccessoryColor: "#f45bd0",
	}
}

// baseSprite renders baseParts and fails the test if it is invalid.
func baseSprite(t *testing.T) Sprite {
	t.Helper()
	sprite, err := Render(baseParts())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if err := ValidateSprite(sprite); err != nil {
		t.Fatalf("ValidateSprite(base): %v", err)
	}
	return sprite
}

// cloneSprite deep-copies a sprite so a test can tamper with one layer.
func cloneSprite(s Sprite) Sprite {
	out := Sprite{
		Palette: make(map[string]string, len(s.Palette)),
		Head:    append([]string(nil), s.Head...),
		Body:    append([]string(nil), s.Body...),
	}
	for k, v := range s.Palette {
		out.Palette[k] = v
	}
	return out
}

// setHead writes one head cell of a sprite.
func setHead(s *Sprite, x, y int, c byte) {
	row := []byte(s.Head[y])
	row[x] = c
	s.Head[y] = string(row)
}

// setBody writes one body cell of a sprite.
func setBody(s *Sprite, x, y int, c byte) {
	row := []byte(s.Body[y])
	row[x] = c
	s.Body[y] = string(row)
}
