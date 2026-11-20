package avatar

import (
	"fmt"
	"strings"
)

// grid is a sprite layer: spriteH rows of spriteW cells, '.' is empty.
type grid [spriteH][spriteW]byte

func newGrid() *grid {
	g := &grid{}
	for y := 0; y < spriteH; y++ {
		for x := 0; x < spriteW; x++ {
			g[y][x] = '.'
		}
	}
	return g
}

func (g *grid) set(x, y int, key byte) {
	if x < 0 || x >= spriteW || y < 0 || y >= spriteH {
		return
	}
	g[y][x] = key
}

// Render assembles a sprite from parts, exactly like the prototype
// avatar-pixel.html (REQ-PXA-001): it returns the body layer, the head layer
// over it, and only the palette keys the sprite uses. The outline is not
// added; the renderer computes it. An unknown part value or a bad
// accessory_colour is an error.
func Render(p Parts) (Sprite, error) {
	if err := validateParts(p); err != nil {
		return Sprite{}, err
	}
	isRobot := p.Species == "robot"
	sk := skins[p.Skin]
	if isRobot {
		sk = robotMetal
	}
	shirt := ColorHex(p.Color, "light")
	shirtShade := shade(shirt)
	shirtLight := mix(shirt, "#ffffff", 0.7)
	shirtLightShade := shadeT(shirtLight, 0.15)
	pants := pantsColors[p.Pants]
	pantsShade := shadeT(pants, 0.25)
	hairBase, hairShade := hairColors[p.HairColor][0], hairColors[p.HairColor][1]
	eye := out
	if isRobot {
		eye = colGlow
	}
	accessory := strings.ToLower(p.AccessoryColor)
	if p.Accessory == "hood" {
		// The cat hood takes the shirt colour (REQ-PXA-001).
		accessory = shirt
	}
	accessoryShade := shade(accessory)
	blush := mix(sk[0], colBlush, 0.65)

	body := newGrid()
	head := newGrid()

	// Body layer: shirt, outfit pattern, arms, hands, pants, legs, shoes.
	for y := 13; y <= 14; y++ {
		for x := 5; x <= 10; x++ {
			body.set(x, y, shirtKey(x))
		}
	}
	switch p.Outfit {
	case "stripe":
		for x := 5; x <= 10; x++ {
			if x == 5 {
				body.set(x, 14, 'L')
			} else {
				body.set(x, 14, 'l')
			}
		}
	case "collar":
		body.set(7, 13, 'l')
		body.set(8, 13, 'l')
	case "jacket":
		body.set(7, 13, 'w')
		body.set(8, 13, 'w')
		body.set(7, 14, 'w')
		body.set(8, 14, 'W')
	case "tie":
		body.set(7, 13, 'w')
		body.set(9, 13, 'w')
		body.set(8, 13, 'r')
		body.set(8, 14, 'r')
	}
	body.set(4, 13, 'C')
	body.set(11, 13, 'c')
	body.set(4, 14, 'S')
	body.set(11, 14, 's')
	for x := 5; x <= 10; x++ {
		if x == 5 {
			body.set(x, 15, 'P')
		} else {
			body.set(x, 15, 'p')
		}
	}
	for _, x := range []int{5, 6, 9, 10} {
		if x == 5 {
			body.set(x, 16, 'P')
		} else {
			body.set(x, 16, 'p')
		}
		body.set(x, 17, 'o')
	}

	// Head layer: head skin, chin line, face, hair, accessories.
	for y := 3; y <= 11; y++ {
		x0, x1 := 3, 12
		if y == 3 || y == 11 {
			x0, x1 = 4, 11
		}
		for x := x0; x <= x1; x++ {
			if x <= 4 {
				head.set(x, y, 'S')
			} else {
				head.set(x, y, 's')
			}
		}
	}
	for x := 5; x <= 10; x++ {
		head.set(x, 12, 'k')
	}

	if isRobot {
		for x := 5; x <= 12; x++ {
			for y := 7; y <= 8; y++ {
				head.set(x, y, 'k')
			}
		}
		for _, x := range []int{7, 11} {
			head.set(x, 7, 'e')
			head.set(x, 8, 'e')
		}
		head.set(8, 0, 'x')
		head.set(8, 1, 'B')
		head.set(8, 2, 'B')
		head.set(2, 8, 'B')
		head.set(13, 8, 'B')
		head.set(9, 10, 'B')
		head.set(10, 10, 'B')
	} else {
		if p.Accessory != "shades" {
			eyes(head, p.Eyes)
		}
		if p.Blush {
			head.set(5, 9, 'b')
			head.set(12, 9, 'b')
		}
		head.set(9, 10, 'm')
	}

	if !isRobot {
		hair := p.Hair
		if hats[p.Accessory] && hatReplace[hair] {
			// A hat or hood needs a flat head (REQ-PXA-001).
			hair = "short"
		}
		stamp(head, hairMasks[hair], func(c int, ch byte) byte {
			if ch == 'd' || c <= 3 {
				return 'H'
			}
			return 'h'
		})
	}

	if mask, ok := accessoryMasks[p.Accessory]; ok && (!isRobot || p.Accessory != "hood") {
		stamp(head, mask, func(c int, ch byte) byte {
			switch ch {
			case 'a':
				if c <= 2 {
					return 'A'
				}
				return 'a'
			case 'A':
				return 'A'
			case 'g':
				return 'g'
			case 'w':
				return 'w'
			default:
				return 'k'
			}
		})
	}
	if !isRobot && p.Accessory == "glasses" {
		for x := 6; x <= 12; x++ {
			head.set(x, 7, 'f')
		}
		for _, x := range []int{6, 8, 10, 12} {
			head.set(x, 8, 'f')
		}
		// The lenses stay eyes: (7,8) and (11,8) are the eye colour.
		head.set(7, 8, 'e')
		head.set(11, 8, 'e')
	}
	if !isRobot && p.Accessory == "shades" {
		for x := 5; x <= 12; x++ {
			head.set(x, 7, 'k')
		}
		for _, x := range []int{6, 7, 8, 10, 11, 12} {
			head.set(x, 8, 'k')
		}
		head.set(7, 7, 'n')
		head.set(11, 7, 'n')
		// Every sprite has eyes: mark the dark lenses (REQ-PXA-003).
		head.set(7, 8, 'e')
		head.set(11, 8, 'e')
	}

	hex := map[byte]string{
		'k': out, 's': sk[0], 'S': sk[1], 'e': eye,
		'h': hairBase, 'H': hairShade,
		'c': shirt, 'C': shirtShade, 'l': shirtLight, 'L': shirtLightShade,
		'w': colWhite, 'W': colWhite2, 'r': colTie,
		'p': pants, 'P': pantsShade, 'o': colShoe,
		'a': accessory, 'A': accessoryShade, 'g': colBand, 'f': colFrame, 'n': colShine,
		'B': colBolt, 'b': blush, 'm': colMouth, 'x': strings.ToLower(p.AccessoryColor),
	}

	// Keep only the keys the sprite uses.
	used := map[byte]bool{}
	for y := 0; y < spriteH; y++ {
		for x := 0; x < spriteW; x++ {
			if c := body[y][x]; c != '.' {
				used[c] = true
			}
			if c := head[y][x]; c != '.' {
				used[c] = true
			}
		}
	}
	palette := make(map[string]string, len(used))
	for k := range used {
		palette[string(k)] = hex[k]
	}
	sprite := Sprite{Palette: palette, Head: make([]string, spriteH), Body: make([]string, spriteH)}
	for y := 0; y < spriteH; y++ {
		sprite.Head[y] = string(head[y][:])
		sprite.Body[y] = string(body[y][:])
	}
	return sprite, nil
}

// shirtKey is 'C' on the left column of the shirt, 'c' elsewhere.
func shirtKey(x int) byte {
	if x == 5 {
		return 'C'
	}
	return 'c'
}

// hats are the accessories that sit on the head and need flat hair.
var hats = map[string]bool{"beanie": true, "cap": true, "hood": true}

// hatReplace are the tall hair styles a hat or hood replaces with short hair.
var hatReplace = map[string]bool{"bun": true, "mohawk": true, "spiky": true, "curly": true}

// eyes draws the human eyes (frame 0). The colour is the 'e' key.
func eyes(head *grid, style string) {
	for _, x := range []int{7, 11} {
		switch style {
		case "sleepy":
			head.set(x-1, 8, 'e')
			head.set(x, 8, 'e')
		case "happy":
			c := x
			if x == 7 {
				c = 6
			}
			head.set(c-1, 8, 'e')
			head.set(c, 7, 'e')
			head.set(c+1, 8, 'e')
		default: // dot
			head.set(x, 7, 'e')
			head.set(x, 8, 'e')
		}
	}
}

// stamp draws a 14×16 mask at x = c + 1, y = r, mapping each character with
// keyOf. Empty cells are skipped.
func stamp(g *grid, mask []string, keyOf func(c int, ch byte) byte) {
	for r, row := range mask {
		for c := 0; c < len(row); c++ {
			if row[c] == '.' {
				continue
			}
			g.set(c+1, r, keyOf(c, row[c]))
		}
	}
}

// validateParts checks every catalog field and the accessory colour
// (REQ-PXA-001). The colour is resolved by ColorHex, so an unknown token is
// not an error here.
func validateParts(p Parts) error {
	if p.Species != "human" && p.Species != "robot" {
		return fmt.Errorf("avatar: unknown species %q", p.Species)
	}
	if _, ok := hairMasks[p.Hair]; !ok {
		return fmt.Errorf("avatar: unknown hair %q", p.Hair)
	}
	if _, ok := hairColors[p.HairColor]; !ok {
		return fmt.Errorf("avatar: unknown hair colour %q", p.HairColor)
	}
	if _, ok := skins[p.Skin]; !ok {
		return fmt.Errorf("avatar: unknown skin %q", p.Skin)
	}
	if p.Eyes != "dot" && p.Eyes != "happy" && p.Eyes != "sleepy" {
		return fmt.Errorf("avatar: unknown eyes %q", p.Eyes)
	}
	if !inList(accessoryOrder, p.Accessory) {
		return fmt.Errorf("avatar: unknown accessory %q", p.Accessory)
	}
	if !inList(outfitOrder, p.Outfit) {
		return fmt.Errorf("avatar: unknown outfit %q", p.Outfit)
	}
	if _, ok := pantsColors[p.Pants]; !ok {
		return fmt.Errorf("avatar: unknown pants %q", p.Pants)
	}
	if !validHex(p.AccessoryColor) {
		return fmt.Errorf("avatar: bad accessory colour %q", p.AccessoryColor)
	}
	return nil
}

func inList(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}
