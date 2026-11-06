package main

import "testing"

func TestParseModels(t *testing.T) {
	tests := []struct {
		name     string
		list     string
		fallback string
		want     []string
		wantErr  bool
	}{
		{name: "fallback", list: "", fallback: "deepseek-flash", want: []string{"deepseek-flash"}},
		{name: "list overrides fallback", list: "a,b", fallback: "fallback", want: []string{"a", "b"}},
		{name: "single list", list: "a", fallback: "fallback", want: []string{"a"}},
		{name: "trims and dedupes", list: " a , b ,, a ", fallback: "fallback", want: []string{"a", "b"}},
		{name: "whitespace list uses fallback", list: "  ", fallback: "fallback", want: []string{"fallback"}},
		{name: "empty fallback", list: "", fallback: "", wantErr: true},
		{name: "only separators", list: ",", fallback: "fallback", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseModels(tt.list, tt.fallback)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseModels(%q, %q) = %v, want an error", tt.list, tt.fallback, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseModels(%q, %q): %v", tt.list, tt.fallback, err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("parseModels(%q, %q) = %v, want %v", tt.list, tt.fallback, got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("parseModels(%q, %q) = %v, want %v", tt.list, tt.fallback, got, tt.want)
				}
			}
		})
	}
}
