package searcher

import "strings"

var asciiLowerTable [256]byte

func init() {
	for i := 0; i < 256; i++ {
		c := byte(i)
		if c >= 'A' && c <= 'Z' {
			asciiLowerTable[i] = c + 32
		} else {
			asciiLowerTable[i] = c
		}
	}
}

type Searcher struct {
	pattern string
	skip    [256]int
	patLen  int
}

func NewSearcher(substr string) *Searcher {
	patLen := len(substr)
	if patLen < 1 {
		return &Searcher{patLen: 0}
	}
	lowerPattern := strings.ToLower(substr)
	var skip [256]int
	for i := range skip {
		skip[i] = patLen
	}
	for i := 0; i < patLen-1; i++ {
		skip[lowerPattern[i]] = patLen - 1 - i
	}
	return &Searcher{pattern: lowerPattern, skip: skip, patLen: patLen}
}

func (s *Searcher) Contains(text string) bool {
	if s.patLen < 1 {
		return false
	}
	textLen := len(text)
	if textLen < s.patLen {
		return false
	}
	i := s.patLen - 1
	for i < textLen {
		textChar := text[i]
		lowerTextChar := asciiLowerTable[textChar]
		if lowerTextChar == s.pattern[s.patLen-1] {
			j := s.patLen - 2
			k := i - 1
			match := true
			for j >= 0 {
				if asciiLowerTable[text[k]] != s.pattern[j] {
					match = false
					break
				}
				j--
				k--
			}
			if match {
				return true
			}
		}
		i += s.skip[lowerTextChar]
	}
	return false
}
