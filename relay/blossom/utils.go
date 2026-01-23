package blossom

import (
	"mime"
	"net/http"
	"strings"

	"fiatjaf.com/nostr"
)

func blossomError(w http.ResponseWriter, msg string, code int) {
	w.Header().Add("X-Reason", msg)
	w.WriteHeader(code)
}

func findTagValue(tags nostr.Tags, key string) (string, bool) {
	tag := tags.Find(key)
	if len(tag) > 1 {
		return tag[1], true
	}

	return "", false
}

func parseHashPath(path string) (string, string, bool) {
	if i := strings.LastIndexByte(path, '/'); i > -1 {
		path = path[i+1:]
	}

	if i := strings.IndexByte(path, '.'); i > -1 {
		return path[:i], mime.TypeByExtension(path[i:]),
			isValid32ByteHex(path[:i])
	}

	return path, "", isValid32ByteHex(path)
}

func isHashPath(path string) bool {
	if i := strings.LastIndexByte(path, '/'); i > -1 {
		path = path[i+1:]
	}

	if i := strings.IndexByte(path, '.'); i > -1 {
		path = path[:i]
	}

	return isValid32ByteHex(path)
}

func isValid32ByteHex(s string) bool {
	if len(s) != 64 {
		return false
	}

	for i := 0; i < 64; i++ {
		c := s[i]
		// only allow digits and letters a-f
		if !((c-'0' < 10) || (c-'a' < 6)) {
			return false
		}
	}

	return true
}
