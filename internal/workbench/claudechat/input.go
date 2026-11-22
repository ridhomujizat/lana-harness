package claudechat

import (
	"errors"
	"regexp"
	"strings"
)

// ErrInvalidKey is returned by KeyBytes for a key name it does not know
// (spec 71 REQ-WC-032).
var ErrInvalidKey = errors.New("invalid key")

// MaxSayBytes is the largest say body the daemon accepts (REQ-WC-031).
const MaxSayBytes = 64 << 10

// maxTextKeyBytes is the longest text:<...> key KeyBytes accepts (REQ-WC-032).
const maxTextKeyBytes = 200

// namedKeys maps a key name to its terminal escape sequence (REQ-WC-032).
var namedKeys = map[string]string{
	"enter":     "\r",
	"esc":       "\x1b",
	"tab":       "\t",
	"shift+tab": "\x1b[Z",
	"up":        "\x1b[A",
	"down":      "\x1b[B",
	"right":     "\x1b[C",
	"left":      "\x1b[D",
	"backspace": "\x7f",
	"ctrl+c":    "\x03",
	"ctrl+u":    "\x15",
	"space":     " ",
}

// KeyBytes turns a named key into the bytes written to the pane's pty. A single
// digit types itself and text:<s> types s as-is (1–200 bytes, no control
// characters). Anything else is ErrInvalidKey.
func KeyBytes(name string) ([]byte, error) {
	if seq, ok := namedKeys[name]; ok {
		return []byte(seq), nil
	}
	if len(name) == 1 && name[0] >= '0' && name[0] <= '9' {
		return []byte(name), nil
	}
	if rest, ok := strings.CutPrefix(name, "text:"); ok {
		if len(rest) < 1 || len(rest) > maxTextKeyBytes {
			return nil, ErrInvalidKey
		}
		for i := 0; i < len(rest); i++ {
			if rest[i] < 0x20 {
				return nil, ErrInvalidKey
			}
		}
		return []byte(rest), nil
	}
	return nil, ErrInvalidKey
}

// PasteBytes builds a bracketed paste of text followed by the attachment paths,
// space-separated, each shell-quoted when it holds a space or a shell
// metacharacter (REQ-WC-031).
func PasteBytes(text string, paths []string) []byte {
	body := text
	for _, p := range paths {
		if p == "" {
			continue
		}
		if body == "" {
			body = shellQuote(p)
			continue
		}
		body += " " + shellQuote(p)
	}
	return []byte("\x1b[200~" + body + "\x1b[201~")
}

// shellSpecials are the characters that make a path need single-quoting.
const shellSpecials = " \t\n\r\"'`$&|;<>()*?[]{}!#~\\"

// shellQuote returns p unchanged when it is safe to type, else single-quoted
// with any embedded single quote escaped.
func shellQuote(p string) string {
	if !strings.ContainsAny(p, shellSpecials) {
		return p
	}
	return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
}

// ValidMode reports whether m is one of the permission modes the chat can set
// (REQ-WC-033).
func ValidMode(m string) bool {
	switch m {
	case "default", "acceptEdits", "plan", "auto", "bypassPermissions":
		return true
	}
	return false
}

// modelPattern is a full model id: lowercase letters, digits, dots, dashes and
// square brackets, up to 64 characters (REQ-WC-034).
var modelPattern = regexp.MustCompile(`^[a-z0-9.\-\[\]]{1,64}$`)

// ModelArg validates a model argument: an alias or a full model id. It returns
// the model to type and whether it is valid (REQ-WC-034).
func ModelArg(m string) (string, bool) {
	switch m {
	case "opus", "sonnet", "haiku", "fable":
		return m, true
	}
	if modelPattern.MatchString(m) {
		return m, true
	}
	return "", false
}

// EffortArg reports whether e is a valid effort level (REQ-WC-034).
func EffortArg(e string) bool {
	switch e {
	case "low", "medium", "high", "xhigh", "max":
		return true
	}
	return false
}
