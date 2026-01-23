package blossom

import (
	"io"
	"mime"

	"github.com/gabriel-vasile/mimetype"
)

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

func NopSeekCloser(r io.ReadSeeker) io.ReadSeekCloser {
	return nopSeekCloser{r}
}

type nopSeekCloser struct {
	io.ReadSeeker
}

func (nopSeekCloser) Close() error { return nil }
