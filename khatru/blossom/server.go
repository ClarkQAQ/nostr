package blossom

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"fiatjaf.com/nostr"
)

type BlossomServer struct {
	Store BlobIndex

	StoreBlob     func(ctx context.Context, sha256 string, ext string, body []byte) error
	LoadBlob      func(ctx context.Context, sha256 string, ext string) (io.ReadSeeker, *url.URL, error)
	DeleteBlob    func(ctx context.Context, sha256 string, ext string) error
	ReceiveReport func(ctx context.Context, reportEvt nostr.Event) error

	RejectUpload func(ctx context.Context, auth *nostr.Event, size int, ext string) (bool, string, int)
	RejectGet    func(ctx context.Context, auth *nostr.Event, sha256 string, ext string) (bool, string, int)
	RejectList   func(ctx context.Context, auth *nostr.Event, pubkey nostr.PubKey) (bool, string, int)
	RejectDelete func(ctx context.Context, auth *nostr.Event, sha256 string, ext string) (bool, string, int)
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
	case (len(r.URL.Path) == 65 || strings.Index(r.URL.Path, ".") == 65) &&
		!strings.HasPrefix(r.URL.Path[1:], "/"):
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

func (bs *BlossomServer) getBaseURL(r *http.Request) string {
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	if proto == "" {
		if host == "localhost" {
			proto = "http"
		} else if strings.Contains(host, ":") {
			// has a port number
			proto = "http"
		} else if _, err := strconv.Atoi(strings.ReplaceAll(host, ".", "")); err == nil {
			// it's a naked IP
			proto = "http"
		} else {
			proto = "https"
		}
	}
	return proto + "://" + host
}
