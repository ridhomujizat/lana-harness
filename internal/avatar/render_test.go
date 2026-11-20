package avatar

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
)

// REQ-PXA-001: every catalog value renders a sprite that validates.
func TestCatalogValuesRenderAndValidate(t *testing.T) {
	cat := Catalog()
	fields := []struct {
		name  string
		items []CatalogItem
		set   func(*Parts, string)
	}{
		{"species", cat.Species, func(p *Parts, v string) { p.Species = v }},
		{"hair", cat.Hair, func(p *Parts, v string) { p.Hair = v }},
		{"hair_color", cat.HairColor, func(p *Parts, v string) { p.HairColor = v }},
		{"skin", cat.Skin, func(p *Parts, v string) { p.Skin = v }},
		{"eyes", cat.Eyes, func(p *Parts, v string) { p.Eyes = v }},
		{"accessory", cat.Accessory, func(p *Parts, v string) { p.Accessory = v }},
		{"outfit", cat.Outfit, func(p *Parts, v string) { p.Outfit = v }},
		{"pants", cat.Pants, func(p *Parts, v string) { p.Pants = v }},
	}
	for _, f := range fields {
		if len(f.items) == 0 {
			t.Fatalf("catalog %s is empty", f.name)
		}
		for _, item := range f.items {
			if item.Label == "" {
				t.Fatalf("catalog %s value %q has no label", f.name, item.ID)
			}
			p := baseParts()
			f.set(&p, item.ID)
			sprite, err := Render(p)
			if err != nil {
				t.Fatalf("%s=%s: Render: %v", f.name, item.ID, err)
			}
			if err := ValidateSprite(sprite); err != nil {
				t.Fatalf("%s=%s: ValidateSprite: %v", f.name, item.ID, err)
			}
		}
	}
}

// REQ-PXA-001: the tricky hair × accessory × eyes combinations all validate
// (hats replace tall hair, sunglasses keep eyes).
func TestCatalogCrossProductValidates(t *testing.T) {
	for _, hair := range hairOrder {
		for _, acc := range accessoryOrder {
			for _, eyes := range eyesOrder {
				p := baseParts()
				p.Hair, p.Accessory, p.Eyes = hair, acc, eyes
				sprite, err := Render(p)
				if err != nil {
					t.Fatalf("hair=%s acc=%s eyes=%s: %v", hair, acc, eyes, err)
				}
				if err := ValidateSprite(sprite); err != nil {
					t.Fatalf("hair=%s acc=%s eyes=%s: %v", hair, acc, eyes, err)
				}
			}
		}
	}
	for _, acc := range accessoryOrder {
		for _, skin := range skinOrder {
			p := baseParts()
			p.Species, p.Accessory, p.Skin = "robot", acc, skin
			sprite, err := Render(p)
			if err != nil {
				t.Fatalf("robot acc=%s skin=%s: %v", acc, skin, err)
			}
			if err := ValidateSprite(sprite); err != nil {
				t.Fatalf("robot acc=%s skin=%s: %v", acc, skin, err)
			}
		}
	}
}

// AC-PXA-001: 500 random seeds all render valid sprites.
func TestRandomPartsSeedsValidate(t *testing.T) {
	for seed := int64(1); seed <= 500; seed++ {
		sprite, err := Render(RandomParts(seed))
		if err != nil {
			t.Fatalf("seed %d: Render: %v", seed, err)
		}
		if err := ValidateSprite(sprite); err != nil {
			t.Fatalf("seed %d: ValidateSprite: %v", seed, err)
		}
	}
}

// AC-PXA-001: different seeds give different sprites, and a seed is stable.
func TestRandomPartsDistinctAndStable(t *testing.T) {
	seen := map[string]bool{}
	for seed := int64(1); seed <= 20; seed++ {
		sprite, err := Render(RandomParts(seed))
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(sprite)
		if err != nil {
			t.Fatal(err)
		}
		seen[string(data)] = true
	}
	if len(seen) < 15 {
		t.Fatalf("20 seeds gave %d distinct sprites, want at least 15", len(seen))
	}
	if first, second := RandomParts(7), RandomParts(7); first != second {
		t.Fatal("RandomParts is not stable for one seed")
	}
}

// REQ-PXA-002: the default avatar is stable per name and its JSON is pinned.
func TestDefaultForPinned(t *testing.T) {
	first, second := DefaultFor("coder", ""), DefaultFor("coder", "")
	data, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	again, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(again) {
		t.Fatal("DefaultFor is not stable")
	}
	const want = "36781333bf75b4ccb434d63c28ff96e2a9d727af72d0d87f6e575239a2151744"
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("DefaultFor(coder, \"\") = %s, want %s", got, want)
	}
	// A colour override changes the shirt colour.
	colored := DefaultFor("coder", "#123456")
	if colored.Parts == nil || colored.Parts.Color != "#123456" {
		t.Fatalf("DefaultFor colour override = %+v", colored.Parts)
	}
	if err := ValidateSprite(colored.Sprite); err != nil {
		t.Fatalf("coloured default: %v", err)
	}
}

// REQ-PXA-002: the seed of a name is the contract.
func TestSeedForName(t *testing.T) {
	if got := SeedForName("coder"); got != 1524374639 {
		t.Fatalf("SeedForName(coder) = %d", got)
	}
}

// REQ-PXA-001: an unknown value in any field is refused.
func TestRenderRefuses(t *testing.T) {
	cases := map[string]func(*Parts){
		"species":         func(p *Parts) { p.Species = "alien" },
		"hair":            func(p *Parts) { p.Hair = "mullet" },
		"hair_color":      func(p *Parts) { p.HairColor = "rainbow" },
		"skin":            func(p *Parts) { p.Skin = "green" },
		"eyes":            func(p *Parts) { p.Eyes = "angry" },
		"accessory":       func(p *Parts) { p.Accessory = "crown" },
		"outfit":          func(p *Parts) { p.Outfit = "dress" },
		"pants":           func(p *Parts) { p.Pants = "cargo" },
		"accessory_color": func(p *Parts) { p.AccessoryColor = "red" },
		"accessory_hex":   func(p *Parts) { p.AccessoryColor = "#12345" },
	}
	for name, mutate := range cases {
		p := baseParts()
		mutate(&p)
		if _, err := Render(p); err == nil {
			t.Fatalf("%s: Render accepted an unknown value", name)
		}
	}
}
