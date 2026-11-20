package app

import "testing"

// TestTileWidth is spec 58, window design: a program tile is landscape, a
// little wider than it is tall, so the sizes its callers pass give the
// widths GoRex draws (16 -> 21, 18 -> 24).
func TestTileWidth(t *testing.T) {
	cases := []struct {
		size, want float32
	}{
		{15, 20},
		{16, 21},
		{18, 24},
		{28, 37},
	}
	for _, tc := range cases {
		if got := tileWidth(tc.size); got != tc.want {
			t.Errorf("tileWidth(%v) = %v, want %v", tc.size, got, tc.want)
		}
	}
}
