package avatar

import "math/rand/v2"

// weightedItem is one choice with its pick weight.
type weightedItem struct {
	value  string
	weight float64
}

// wpick draws a value from a weighted list with r (REQ-PXA-002).
func wpick(r *rand.Rand, list []weightedItem) string {
	total := 0.0
	for _, item := range list {
		total += item.weight
	}
	x := r.Float64() * total
	for _, item := range list {
		x -= item.weight
		if x < 0 {
			return item.value
		}
	}
	return list[len(list)-1].value
}

// equal weights every value of a list with weight 1.
func equal(list []string) []weightedItem {
	out := make([]weightedItem, len(list))
	for i, v := range list {
		out[i] = weightedItem{value: v, weight: 1}
	}
	return out
}

// RandomParts picks every part from seed with the prototype weights
// (REQ-PXA-002). The same seed gives the same parts on every machine. The
// colour is a preset; callers may override it afterwards.
func RandomParts(seed int64) Parts {
	r := rand.New(rand.NewPCG(uint64(seed), 0x9e3779b97f4a7c15))
	p := Parts{
		Species: wpick(r, []weightedItem{{"human", 88}, {"robot", 12}}),
		Skin:    wpick(r, equal(skinOrder)),
		Hair: wpick(r, []weightedItem{
			{"short", 3}, {"bob", 2}, {"spiky", 2}, {"long", 2}, {"ponytail", 2},
			{"bun", 1}, {"curly", 2}, {"buzz", 1}, {"mohawk", 1}, {"swoop", 2},
		}),
		HairColor: wpick(r, []weightedItem{
			{"black", 3}, {"dark", 3}, {"brown", 3}, {"blonde", 2}, {"ginger", 2},
			{"silver", 1}, {"pink", 1}, {"blue", 1}, {"lilac", 1}, {"mint", 1},
		}),
		Eyes: wpick(r, []weightedItem{{"dot", 6}, {"happy", 3}, {"sleepy", 2}}),
		Accessory: wpick(r, []weightedItem{
			{"none", 9}, {"glasses", 3}, {"shades", 2}, {"headphones", 3},
			{"beanie", 2}, {"cap", 2}, {"hood", 2},
		}),
		Outfit: wpick(r, equal(outfitOrder)),
		Pants:  wpick(r, equal(pantsOrder)),
		Blush:  r.Float64() < 0.6,
		Color:  pickColor(r),
	}
	accColor := pickColor(r)
	if accColor == p.Color {
		// A second colour equal to the first is lifted toward white
		// (REQ-PXA-002).
		accColor = mix(accColor, "#ffffff", 0.55)
	}
	p.AccessoryColor = accColor
	return p
}

func pickColor(r *rand.Rand) string {
	return colorPresets[int(r.Float64()*float64(len(colorPresets)))]
}
