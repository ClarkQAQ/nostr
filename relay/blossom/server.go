package blossom

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"

	"fiatjaf.com/nostr"
)

type BlossomServer struct {
	Store BlobIndex

	StoreBlob     func(ctx context.Context, sha256 string, mimeType string, size int64, reader io.Reader) error
	LoadBlob      func(ctx context.Context, sha256 string, mimeType string) (io.ReadSeekCloser, *url.URL, error)
	DeleteBlob    func(ctx context.Context, sha256 string, mimeType string) error
	ReceiveReport func(ctx context.Context, reportEvt nostr.Event) error

	RejectMirror func(ctx context.Context, auth *nostr.Event, sha256 string, url *url.URL) (bool, string, int)
	RejectUpload func(ctx context.Context, auth *nostr.Event, sha256 string, size int64, mimeType string) (bool, string, int)
	RejectGet    func(ctx context.Context, auth *nostr.Event, sha256 string, mimeType string) (bool, string, int)
	RejectList   func(ctx context.Context, auth *nostr.Event, pubkey nostr.PubKey) (bool, string, int)
	RejectDelete func(ctx context.Context, auth *nostr.Event, sha256 string, mimeType string) (bool, string, int)

	CustomBlobNotFound func(w http.ResponseWriter, r *http.Request, sha256 string, mimeType string)
}

// ServerOption represents a functional option for configuring a BlossomServer
type ServerOption func(*BlossomServer)

// New creates a new BlossomServer with the given relay and service URL
// Optional configuration can be provided via functional options
func New() *BlossomServer {
	bs := &BlossomServer{}
	return bs
}

func (bs *BlossomServer) HandleMatcher(w http.ResponseWriter, r *http.Request) (http.HandlerFunc, bool) {
	switch {
	case r.URL.Path == "/upload":
		switch r.Method {
		case "HEAD":
			return bs.handleUploadCheck, true
		case "PUT":
			return bs.handleUpload, true
		}
	case r.URL.Path == "/media":
		return bs.handleMedia, true
	case r.URL.Path == "/mirror" && r.Method == "PUT":
		return bs.handleMirror, true
	case strings.HasPrefix(r.URL.Path, "/list/") && r.Method == "GET":
		return bs.handleList, true
	case isHashPath(r.URL.Path):
		switch r.Method {
		case "HEAD":
			return bs.handleHasBlob, true
		case "GET":
			return bs.handleGetBlob, true
		case "DELETE":
			return bs.handleDelete, true
		}
	case r.URL.Path == "/report" && r.Method == "PUT":
		return bs.handleReport, true
	}

	return nil, false
}

func (bs *BlossomServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if handle, ok := bs.HandleMatcher(w, r); ok && handle != nil {
		handle.ServeHTTP(w, r)
		return
	}

	http.NotFound(w, r)
}

func (bs *BlossomServer) getBaseURL(r *http.Request, paths ...string) *url.URL {
	u := &url.URL{
		Scheme: "http",
		Host:   r.Host,
	}

	if r.TLS != nil {
		u.Scheme = "https"
	}

	if fh := r.Header.Get("X-Forwarded-Host"); fh != "" {
		u.Host = fh
	}

	if fp := r.Header.Get("X-Forwarded-Proto"); fp != "" {
		u.Scheme = fp
	}

	if len(paths) > 0 {
		u = u.JoinPath(paths...)
	}

	return u
}
