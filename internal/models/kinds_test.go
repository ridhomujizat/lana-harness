package models

import "testing"

// Spec 80: two API connections share a pool group exactly when they share
// kind and effective base URL (base_url, else the kind's default, without a
// trailing "/" and with the scheme and host in lower case).
func TestPoolGroup(t *testing.T) {
	same := [][2][2]string{
		{{"commandcode", ""}, {"commandcode", "https://api.commandcode.ai"}},
		{{"commandcode", ""}, {"commandcode", "HTTPS://API.CommandCode.ai/"}},
		{{"deepseek", "https://api.deepseek.com/v1/"}, {"deepseek", ""}},
		{{"openai_compat", "http://127.0.0.1:8091/v1"}, {"openai_compat", "http://127.0.0.1:8091/v1/"}},
	}
	for _, pair := range same {
		a, b := PoolGroup(pair[0][0], pair[0][1]), PoolGroup(pair[1][0], pair[1][1])
		if a != b {
			t.Errorf("%v and %v: %q != %q", pair[0], pair[1], a, b)
		}
	}
	different := [][2][2]string{
		{{"openai_compat", "http://127.0.0.1:8091/v1"}, {"openai_compat", "http://127.0.0.1:8092/v1"}},
		{{"openai", ""}, {"openai_compat", "https://api.openai.com/v1"}},
		{{"openai_compat", "http://host/A"}, {"openai_compat", "http://host/a"}},
	}
	for _, pair := range different {
		a, b := PoolGroup(pair[0][0], pair[0][1]), PoolGroup(pair[1][0], pair[1][1])
		if a == b {
			t.Errorf("%v and %v share group %q", pair[0], pair[1], a)
		}
	}
}
