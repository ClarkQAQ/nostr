package blossom

import (
	"mime"
	"net/http"

	"fiatjaf.com/nostr"
	"github.com/gabriel-vasile/mimetype"
)

const DefaultContentType = "application/octet-stream"

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

func ExtensionByMimeType(mt string) string {
	if mt == "" {
		return ""
	}

	if m := mimetype.Lookup(mt); m != nil {
		return m.Extension()
	}

	if exts, _ := mime.ExtensionsByType(mt); len(exts) > 0 {
		return exts[0]
	}

	return ""
}
