package avatar

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestGallery writes an HTML page of local avatars for a visual check when
// AVATAR_GALLERY names the output file. Extra SVG files (one per line) in
// AVATAR_GALLERY_EXTRA are added as a "model" row.
func TestGallery(t *testing.T) {
	out := os.Getenv("AVATAR_GALLERY")
	if out == "" {
		t.Skip("AVATAR_GALLERY is not set")
	}
	colors := []string{"#111111", "#12b5a6", "#9b5cff", "#1a8cff", "#ff3d9a", "#ff6a00", "#ff9f00", "#9a6a3f", "#7a7a7a"}
	count := 24
	if v, err := strconv.Atoi(os.Getenv("AVATAR_GALLERY_COUNT")); err == nil {
		count = v
	}
	start := int64(1)
	if v, err := strconv.ParseInt(os.Getenv("AVATAR_GALLERY_START"), 10, 64); err == nil {
		start = v
	}
	cell := func(svg, color, label string) string {
		return fmt.Sprintf(`<figure><div class="av" style="color:%s">%s</div><figcaption>%s</figcaption></figure>`, color, svg, label)
	}
	var local []string
	for i := 0; i < count; i++ {
		seed := start + int64(i)
		local = append(local, cell(Generate(seed), colors[i%len(colors)], fmt.Sprintf("%d %s", seed, FamilyFor(seed))))
	}
	var model []string
	if extra := os.Getenv("AVATAR_GALLERY_EXTRA"); extra != "" {
		for i, path := range strings.Fields(extra) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			model = append(model, cell(string(data), colors[(i+3)%len(colors)], "model "+strconv.Itoa(i+1)))
		}
	}
	section := func(theme string) string {
		s := `<section class="` + theme + `"><h2>` + theme + ` · local</h2><div class="grid">` + strings.Join(local, "") + `</div>`
		if len(model) > 0 {
			s += `<h2>` + theme + ` · model</h2><div class="grid">` + strings.Join(model, "") + `</div>`
		}
		return s + `</section>`
	}
	page := `<!doctype html><meta charset="utf-8"><style>
body{margin:0;font:12px system-ui}
section{padding:16px}
.light{background:#f3f4f6;color:#222}.dark{background:#16171b;color:#ddd}
.grid{display:flex;flex-wrap:wrap;gap:14px}
figure{margin:0;width:84px;text-align:center}
.av{width:64px;height:64px;margin:0 auto}.big .av{width:96px;height:96px}.big figure{width:110px}
.av svg{width:100%;height:100%;display:block}
figcaption{margin-top:4px;font-size:10px;opacity:.7}
h2{font-size:13px;margin:4px 0 10px}
</style>` + section("light") + section("dark")
	if err := os.WriteFile(out, []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
}
