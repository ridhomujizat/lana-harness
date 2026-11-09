// Package selfupdate holds the parts of the self-update pipeline that do not
// need the daemon: semantic version compare, the GitHub release client, the
// channel filter, the download with checksum, the eligibility check, the
// update backups, and the state files under $LANA_HOME/updates (spec 36).
//
// It has no dependency on web, lana, or cmd. Callers pass an *http.Client,
// paths, and the running version, so tests drive it with httptest and temp
// dirs.
package selfupdate

import (
	"strconv"
	"strings"
)

// Version is a parsed Semantic Versioning 2.0 version. Build metadata is
// dropped because it does not take part in precedence.
type Version struct {
	Major, Minor, Patch int
	// Pre holds the dot-separated prerelease identifiers; nil for a release.
	Pre []string
}

// ParseVersion parses "1.2.3", "v1.2.3-beta.1", or "1.2.3+build". It reports
// false for anything else, including "dev".
func ParseVersion(s string) (Version, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		if !validIdentifiers(s[i+1:], false) {
			return Version{}, false
		}
		s = s[:i]
	}
	core, pre, hasPre := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return Version{}, false
	}
	var nums [3]int
	for i, part := range parts {
		n, ok := numericIdentifier(part)
		if !ok {
			return Version{}, false
		}
		nums[i] = n
	}
	v := Version{Major: nums[0], Minor: nums[1], Patch: nums[2]}
	if hasPre {
		if !validIdentifiers(pre, true) {
			return Version{}, false
		}
		v.Pre = strings.Split(pre, ".")
	}
	return v, true
}

// String renders the version without a leading "v".
func (v Version) String() string {
	s := strconv.Itoa(v.Major) + "." + strconv.Itoa(v.Minor) + "." + strconv.Itoa(v.Patch)
	if len(v.Pre) > 0 {
		s += "-" + strings.Join(v.Pre, ".")
	}
	return s
}

// Prerelease reports whether the version has a prerelease part.
func (v Version) Prerelease() bool { return len(v.Pre) > 0 }

// Compare returns -1, 0, or 1 following SemVer 2.0 precedence:
// 0.1.0-beta.1 < 0.1.0-beta.2 < 0.1.0 < 0.1.1 < 0.2.0-beta.1.
func Compare(a, b Version) int {
	for _, pair := range [][2]int{{a.Major, b.Major}, {a.Minor, b.Minor}, {a.Patch, b.Patch}} {
		if c := cmpInt(pair[0], pair[1]); c != 0 {
			return c
		}
	}
	switch {
	case len(a.Pre) == 0 && len(b.Pre) == 0:
		return 0
	case len(a.Pre) == 0:
		return 1
	case len(b.Pre) == 0:
		return -1
	}
	for i := 0; i < len(a.Pre) && i < len(b.Pre); i++ {
		if c := compareIdentifier(a.Pre[i], b.Pre[i]); c != 0 {
			return c
		}
	}
	return cmpInt(len(a.Pre), len(b.Pre))
}

// SameVersion reports whether two version strings name the same version. An
// unparsable side is compared as text without a leading "v".
func SameVersion(a, b string) bool {
	va, okA := ParseVersion(a)
	vb, okB := ParseVersion(b)
	if okA && okB {
		return Compare(va, vb) == 0
	}
	return strings.TrimPrefix(strings.TrimSpace(a), "v") == strings.TrimPrefix(strings.TrimSpace(b), "v")
}

// compareIdentifier compares two prerelease identifiers: numeric ones as
// numbers, numeric below alphanumeric, alphanumeric ones in ASCII order.
func compareIdentifier(a, b string) int {
	na, aNum := numericIdentifier(a)
	nb, bNum := numericIdentifier(b)
	switch {
	case aNum && bNum:
		return cmpInt(na, nb)
	case aNum:
		return -1
	case bNum:
		return 1
	}
	return strings.Compare(a, b)
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// numericIdentifier parses a numeric identifier without a leading zero.
func numericIdentifier(s string) (int, bool) {
	if s == "" || (len(s) > 1 && s[0] == '0') {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

// validIdentifiers checks dot-separated identifiers of [0-9A-Za-z-]. A
// prerelease numeric identifier must not have a leading zero.
func validIdentifiers(s string, prerelease bool) bool {
	if s == "" {
		return false
	}
	for _, id := range strings.Split(s, ".") {
		if id == "" {
			return false
		}
		allDigits := true
		for i := 0; i < len(id); i++ {
			c := id[i]
			switch {
			case c >= '0' && c <= '9':
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '-':
				allDigits = false
			default:
				return false
			}
		}
		if prerelease && allDigits && len(id) > 1 && id[0] == '0' {
			return false
		}
	}
	return true
}
