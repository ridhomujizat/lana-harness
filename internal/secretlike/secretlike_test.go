package secretlike

import "testing"

func TestMatch(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"openai key", "key is sk-abcdefghijklmnop1234", true},
		{"github token", "ghp_abcdefghijklmnopqrstuvwxyz0123", true},
		{"slack token", "xoxb-1234-5678", true},
		{"slack user token", "xoxp-abcdef", true},
		{"aws key", "AKIAIOSFODNN7EXAMPLE", true},
		{"pem header", "-----BEGIN RSA PRIVATE KEY-----", true},
		{"password assignment", "password: hunter22xyz", true},
		{"api key assignment", "api_key = supersecret", true},
		{"token assignment", "TOKEN:abcdefgh", true},
		{"passwd assignment", "passwd = letmein1", true},
		{"plain sentence", "Pak Budi signed the contract yesterday.", false},
		{"short token", "token: abc", false},
		{"empty", "", false},
		{"word password alone", "I forgot my password again", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Match(tt.text); got != tt.want {
				t.Errorf("Match(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}
