package claudechat

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestKeyBytes(t *testing.T) {
	cases := []struct {
		name string
		want []byte
		ok   bool
	}{
		{"enter", []byte("\r"), true},
		{"esc", []byte("\x1b"), true},
		{"tab", []byte("\t"), true},
		{"shift+tab", []byte("\x1b[Z"), true},
		{"up", []byte("\x1b[A"), true},
		{"down", []byte("\x1b[B"), true},
		{"right", []byte("\x1b[C"), true},
		{"left", []byte("\x1b[D"), true},
		{"backspace", []byte("\x7f"), true},
		{"ctrl+c", []byte("\x03"), true},
		{"ctrl+u", []byte("\x15"), true},
		{"space", []byte(" "), true},
		{"0", []byte("0"), true},
		{"7", []byte("7"), true},
		{"9", []byte("9"), true},
		{"text:hello world", []byte("hello world"), true},
		{"text:1", []byte("1"), true},
		{"", nil, false},
		{"enter ", nil, false},
		{"10", nil, false},
		{"ctrl+z", nil, false},
		{"text:", nil, false},
		{"text:\x03", nil, false},
		{"text:ok\nno", nil, false},
		{"text:" + strings.Repeat("a", 201), nil, false},
	}
	for _, c := range cases {
		got, err := KeyBytes(c.name)
		if c.ok {
			if err != nil {
				t.Errorf("KeyBytes(%q) error = %v", c.name, err)
				continue
			}
			if !bytes.Equal(got, c.want) {
				t.Errorf("KeyBytes(%q) = %q, want %q", c.name, got, c.want)
			}
			continue
		}
		if !errors.Is(err, ErrInvalidKey) {
			t.Errorf("KeyBytes(%q) error = %v, want ErrInvalidKey", c.name, err)
		}
	}
}

func TestKeyBytesTextMaxLength(t *testing.T) {
	body := make([]byte, 200)
	for i := range body {
		body[i] = 'a'
	}
	got, err := KeyBytes("text:" + string(body))
	if err != nil || len(got) != 200 {
		t.Fatalf("200-byte text: got %d bytes, err %v", len(got), err)
	}
}

func TestPasteBytes(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		paths []string
		want  string
	}{
		{"text only", "hi", nil, "\x1b[200~hi\x1b[201~"},
		{"plain path", "hi", []string{"/a/b.png"}, "\x1b[200~hi /a/b.png\x1b[201~"},
		{"space quoted", "hi", []string{"/a b/c.png"}, "\x1b[200~hi '/a b/c.png'\x1b[201~"},
		{"quote escaped", "", []string{"/a'b.png"}, "\x1b[200~'/a'\\''b.png'\x1b[201~"},
		{"metachar quoted", "x", []string{"/a$b"}, "\x1b[200~x '/a$b'\x1b[201~"},
		{"empty path skipped", "hi", []string{""}, "\x1b[200~hi\x1b[201~"},
	}
	for _, c := range cases {
		got := string(PasteBytes(c.text, c.paths))
		if got != c.want {
			t.Errorf("%s: PasteBytes = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestValidMode(t *testing.T) {
	for _, m := range []string{"default", "acceptEdits", "plan", "auto", "bypassPermissions"} {
		if !ValidMode(m) {
			t.Errorf("ValidMode(%q) = false", m)
		}
	}
	for _, m := range []string{"", "Plan", "accept-edits", "yolo"} {
		if ValidMode(m) {
			t.Errorf("ValidMode(%q) = true", m)
		}
	}
}

func TestModelArg(t *testing.T) {
	for _, m := range []string{"opus", "sonnet", "haiku", "fable", "claude-opus-5-5", "claude-3-5-sonnet[1m]"} {
		if got, ok := ModelArg(m); !ok || got != m {
			t.Errorf("ModelArg(%q) = %q, %v", m, got, ok)
		}
	}
	for _, m := range []string{"", "Opus", "claude opus", "a/b", "new\nmodel", strings.Repeat("a", 65)} {
		if got, ok := ModelArg(m); ok {
			t.Errorf("ModelArg(%q) = %q, true; want invalid", m, got)
		}
	}
}

func TestEffortArg(t *testing.T) {
	for _, e := range []string{"low", "medium", "high", "xhigh", "max"} {
		if !EffortArg(e) {
			t.Errorf("EffortArg(%q) = false", e)
		}
	}
	for _, e := range []string{"", "highest", "LOW", "extra"} {
		if EffortArg(e) {
			t.Errorf("EffortArg(%q) = true", e)
		}
	}
}
