package schema

import (
	"unicode"
	"unicode/utf8"
)

// isTrimmed checks if strings.TrimSpace(v) == v, i.e. that the value doesn't have dangling spaces
func isTrimmed(s string) bool {
	if len(s) == 0 {
		return true
	}

	first, _ := utf8.DecodeRuneInString(s)
	if unicode.IsSpace(first) {
		return false
	}

	last, _ := utf8.DecodeLastRuneInString(s)
	if unicode.IsSpace(last) {
		return false
	}

	return true
}
