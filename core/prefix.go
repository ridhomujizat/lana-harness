package core

import "sort"

// ResolvePrefix memilih satu session dari hasil FindPrefix.
func ResolvePrefix(matches []SessionInfo, prefix string) (SessionInfo, error) {
	if len(prefix) < 6 {
		return SessionInfo{}, ErrPrefixTooShort
	}
	if len(matches) == 0 {
		return SessionInfo{}, ErrNotFound
	}
	if len(matches) > 1 {
		candidates := make([]string, 0, len(matches))
		for _, match := range matches {
			candidates = append(candidates, match.ID)
		}
		sort.Strings(candidates)
		return SessionInfo{}, &AmbiguousPrefixError{Candidates: candidates}
	}
	return matches[0], nil
}
