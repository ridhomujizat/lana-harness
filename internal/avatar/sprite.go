// Package avatar renders and checks the pixel-art agent avatars of spec 62: a
// 16×20 two-layer pixel sprite assembled from parts (species, hair, hair
// colour, skin, eyes, accessory, outfit, pants) and seeded by the agent name,
// wearing the agent colour as its shirt. Render turns Parts into a Sprite,
// RandomParts picks one from a seed, ValidateSprite checks the shape rules,
// SVG draws the frame, and Create asks a model through the router and falls
// back to the local parts.
package avatar

// Sprite is a 16×20 two-layer pixel sprite (REQ-PXA-003).
type Sprite struct {
	// Palette maps each one-character key to its "#rrggbb" colour (lower
	// case). A sprite uses at most 24 keys.
	Palette map[string]string `json:"palette"`
	// Head is 20 rows of 16 characters, each '.' or a palette key. It is
	// drawn over the body layer.
	Head []string `json:"head"`
	// Body is the same shape, drawn first.
	Body []string `json:"body"`
}

// Parts are the catalog values a sprite is assembled from (REQ-PXA-001).
type Parts struct {
	Species        string `json:"species"`    // human, robot
	Hair           string `json:"hair"`       // short, bob, spiky, long, ponytail, bun, curly, buzz, mohawk, swoop
	HairColor      string `json:"hair_color"` // black, dark, brown, blonde, ginger, silver, pink, blue, lilac, mint
	Skin           string `json:"skin"`       // porcelain, light, warm, tan, brown, deep
	Eyes           string `json:"eyes"`       // dot, happy, sleepy
	Accessory      string `json:"accessory"`  // none, glasses, shades, headphones, beanie, cap, hood
	Outfit         string `json:"outfit"`     // plain, stripe, collar, jacket, tie
	Pants          string `json:"pants"`      // denim, charcoal, khaki, black, olive
	Blush          bool   `json:"blush"`
	Color          string `json:"color"`           // agent colour: token or "#rrggbb"; the shirt colour
	AccessoryColor string `json:"accessory_color"` // "#rrggbb"
}

// Avatar document kinds (API-PXA-002).
const (
	// KindParts is an avatar assembled from parts; its sprite is re-rendered
	// from the parts and the saved colour.
	KindParts = "parts"
	// KindDrawn is an avatar a model drew pixel by pixel; its sprite is kept
	// as it is.
	KindDrawn = "drawn"
)

// MaxDocumentSize bounds a saved avatar.json (REQ-PXA-009).
const MaxDocumentSize = 16 << 10

// Document is a saved or generated avatar (API-PXA-002).
type Document struct {
	Kind   string `json:"kind"`
	Parts  *Parts `json:"parts,omitempty"`
	Sprite Sprite `json:"sprite"`
	Prompt string `json:"prompt,omitempty"`
}

// spriteW and spriteH are the grid size in cells (Definitions).
const (
	spriteW = 16
	spriteH = 20
)

// CatalogItem is one choice of a catalog field (API-PXA-001).
type CatalogItem struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// CatalogData is the avatar catalog (API-PXA-001): one list of choices per
// part, with English labels.
type CatalogData struct {
	Species   []CatalogItem `json:"species"`
	Hair      []CatalogItem `json:"hair"`
	HairColor []CatalogItem `json:"hair_color"`
	Skin      []CatalogItem `json:"skin"`
	Eyes      []CatalogItem `json:"eyes"`
	Accessory []CatalogItem `json:"accessory"`
	Outfit    []CatalogItem `json:"outfit"`
	Pants     []CatalogItem `json:"pants"`
}

// Order of the catalog values, also the order RandomParts draws from.
var (
	speciesOrder   = []string{"human", "robot"}
	hairOrder      = []string{"short", "bob", "spiky", "long", "ponytail", "bun", "curly", "buzz", "mohawk", "swoop"}
	hairColorOrder = []string{"black", "dark", "brown", "blonde", "ginger", "silver", "pink", "blue", "lilac", "mint"}
	skinOrder      = []string{"porcelain", "light", "warm", "tan", "brown", "deep"}
	eyesOrder      = []string{"dot", "happy", "sleepy"}
	accessoryOrder = []string{"none", "glasses", "shades", "headphones", "beanie", "cap", "hood"}
	outfitOrder    = []string{"plain", "stripe", "collar", "jacket", "tie"}
	pantsOrder     = []string{"denim", "charcoal", "khaki", "black", "olive"}
)

// Catalog returns the avatar choices with English labels (API-PXA-001).
func Catalog() CatalogData {
	return CatalogData{
		Species: items(speciesOrder, map[string]string{
			"human": "Human", "robot": "Robot",
		}),
		Hair: items(hairOrder, map[string]string{
			"short": "Short", "bob": "Bob", "spiky": "Spiky", "long": "Long",
			"ponytail": "Ponytail", "bun": "Bun", "curly": "Curly", "buzz": "Buzz",
			"mohawk": "Mohawk", "swoop": "Swoop",
		}),
		HairColor: items(hairColorOrder, map[string]string{
			"black": "Black", "dark": "Dark", "brown": "Brown", "blonde": "Blonde",
			"ginger": "Ginger", "silver": "Silver", "pink": "Pink", "blue": "Blue",
			"lilac": "Lilac", "mint": "Mint",
		}),
		Skin: items(skinOrder, map[string]string{
			"porcelain": "Porcelain", "light": "Light", "warm": "Warm",
			"tan": "Tan", "brown": "Brown", "deep": "Deep",
		}),
		Eyes: items(eyesOrder, map[string]string{
			"dot": "Dot", "happy": "Happy", "sleepy": "Sleepy",
		}),
		Accessory: items(accessoryOrder, map[string]string{
			"none": "None", "glasses": "Glasses", "shades": "Sunglasses",
			"headphones": "Headphones", "beanie": "Beanie", "cap": "Cap",
			"hood": "Cat hood",
		}),
		Outfit: items(outfitOrder, map[string]string{
			"plain": "Plain", "stripe": "Stripe", "collar": "Collar",
			"jacket": "Jacket", "tie": "Tie",
		}),
		Pants: items(pantsOrder, map[string]string{
			"denim": "Denim", "charcoal": "Charcoal", "khaki": "Khaki",
			"black": "Black", "olive": "Olive",
		}),
	}
}

func items(order []string, labels map[string]string) []CatalogItem {
	out := make([]CatalogItem, len(order))
	for i, id := range order {
		out[i] = CatalogItem{ID: id, Label: labels[id]}
	}
	return out
}
