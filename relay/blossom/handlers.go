package blossom

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nipb0/blossom"
	"github.com/gabriel-vasile/mimetype"
)

type mirrorRequest struct {
	URL string `json:"url"`
}

// helper to validate authorization header and action tag
func (bs *BlossomServer) checkAuth(w http.ResponseWriter, r *http.Request, action string) (*nostr.Event, bool) {
	auth, e := readAuthorization(r)
	if e != nil {
		blossomError(w, "invalid \"Authorization\": "+e.Error(), http.StatusBadRequest)
		return nil, false
	}

	if auth == nil {
		blossomError(w, "missing \"Authorization\" header", http.StatusUnauthorized)
		return nil, false
	}

	if auth.Tags.FindWithValue("t", action) == nil {
		blossomError(w, fmt.Sprintf("invalid \"Authorization\" event \"t\" tag for action %q", action), http.StatusForbidden)
		return nil, false
	}

	return auth, true
}

// handleUploadCheck verifies if an upload would be accepted without receiving the body.
func (bs *BlossomServer) handleUploadCheck(w http.ResponseWriter, r *http.Request) {
	auth, ok := bs.checkAuth(w, r, "upload")
	if !ok {
		return
	}

	hash := r.Header.Get("X-SHA-256")
	if len(hash) != 64 {
		blossomError(w, "missing or invalid X-SHA-256 header", http.StatusBadRequest)
		return
	}

	size, _ := strconv.ParseInt(r.Header.Get("X-Content-Length"), 10, 64)
	if size < 1 {
		blossomError(w, "missing or invalid X-Content-Length header", http.StatusBadRequest)
		return
	}

	mimeType := r.Header.Get("X-Content-Type")
	if mimeType == "" {
		blossomError(w, "missing or invalid X-Content-Type header", http.StatusBadRequest)
		return
	}

	if bs.RejectUpload != nil {
		if reject, reason, code := bs.RejectUpload(r.Context(), auth, hash, size, mimeType); reject {
			blossomError(w, reason, code)
			return
		}
	}
}

func (bs BlossomServer) handleUpload(w http.ResponseWriter, r *http.Request) {
	auth, ok := bs.checkAuth(w, r, "upload")
	if !ok {
		return
	}

	hash, ok := findTagValue(auth.Tags, "x")
	if !ok {
		blossomError(w, "auth no \"x\" tag found", http.StatusBadRequest)
		return
	}

	size, _ := strconv.ParseInt(r.Header.Get("Content-Length"), 10, 64)
	if size < 1 {
		blossomError(w, "missing Content-Length header", http.StatusBadRequest)
		return
	}

	mimeType := r.Header.Get("Content-Type")
	if mimeType == "" {
		blossomError(w, "missing or invalid Content-Type header", http.StatusBadRequest)
		return
	}

	defer func() {
		_, _ = io.CopyN(io.Discard, r.Body, size)
		_ = r.Body.Close()
	}()

	bs.handleStoreBlob(w, r, auth, r.Body, hash, size, mimeType)
}

func (bs BlossomServer) handleMirror(w http.ResponseWriter, r *http.Request) {
	auth, ok := bs.checkAuth(w, r, "upload")
	if !ok {
		return
	}

	hash, ok := findTagValue(auth.Tags, "x")
	if !ok {
		blossomError(w, "auth no \"x\" tag found", http.StatusBadRequest)
		return
	}

	var req mirrorRequest
	if e := json.NewDecoder(r.Body).Decode(&req); e != nil {
		blossomError(w, "invalid JSON body: "+e.Error(), http.StatusBadRequest)
		return
	}

	u, e := url.Parse(req.URL)
	if e != nil {
		blossomError(w, "invalid URL: "+e.Error(), http.StatusBadRequest)
		return
	}

	if bs.RejectMirror != nil {
		if reject, reason, code := bs.RejectMirror(r.Context(), auth, hash, u); reject {
			blossomError(w, reason, code)
			return
		}
	}

	// download the blob
	resp, e := mirrorHTTPClient.Get(u.String())
	if e != nil {
		blossomError(w, "failed to download from url: "+e.Error(), http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		blossomError(w, fmt.Sprintf("upstream server returned error: %d %s", resp.StatusCode, resp.Status), http.StatusBadGateway)
		return
	}

	size := resp.ContentLength
	contentLengthStr := resp.Header.Get("Content-Length")
	if parsedSize, _ := strconv.ParseInt(contentLengthStr, 10, 64); parsedSize > size {
		size = parsedSize
	}

	if size < 1 {
		blossomError(w, fmt.Sprintf("invalid \"Content-Length\": %d", size), http.StatusBadRequest)
		return
	}

	bs.handleStoreBlob(w, r, auth, resp.Body, hash, size, resp.Header.Get("Content-Type"))
}

// handleSaveBlob handles the common logic of hashing, saving to temp, and finalizing storage.
func (bs *BlossomServer) handleStoreBlob(w http.ResponseWriter, r *http.Request, auth *nostr.Event, reader io.Reader, hash string, size int64, mimeType string) {
	if bd, e := bs.Store.Get(r.Context(), hash, nostr.HTTPHostURL(r, nostr.DefaultIPChecker)); e != nil {
		blossomError(w, "failed to get metadata: "+e.Error(), http.StatusInternalServerError)
		return
	} else if bd != nil {
		mimeType = bd.Type

		if bs.RejectUpload != nil {
			if reject, reason, code := bs.RejectUpload(r.Context(), auth, hash, size, mimeType); reject {
				blossomError(w, reason, code)
				return
			}
		}
	} else {
		if mimeType == "" {
			// Peek for magic numbers from the RESPONSE body
			peekBytes := make([]byte, 1024)
			n, e := io.ReadFull(reader, peekBytes)
			if e != nil && !errors.Is(e, io.EOF) && !errors.Is(e, io.ErrUnexpectedEOF) {
				blossomError(w, "failed to read upstream body: "+e.Error(), http.StatusBadGateway)
				return
			}
			peekBytes = peekBytes[:n]
			reader = io.MultiReader(bytes.NewReader(peekBytes), reader)
			if m := mimetype.Detect(peekBytes); m != nil {
				mimeType = m.String()
			}
		}

		if bs.RejectUpload != nil {
			if reject, reason, code := bs.RejectUpload(r.Context(), auth, hash, size, mimeType); reject {
				blossomError(w, reason, code)
				return
			}
		}

		hasher := sha256.New()

		if bs.StoreBlob != nil {
			if e := bs.StoreBlob(r.Context(), hash, mimeType, size,
				io.TeeReader(io.LimitReader(reader, size), hasher)); e != nil {
				blossomError(w, "failed to save blob: "+e.Error(), http.StatusInternalServerError)
				return
			}
		}

		if bodyHash := nostr.HexEncodeToString(hasher.Sum(nil)); hash != bodyHash {
			if bs.DeleteBlob != nil {
				if e := bs.DeleteBlob(r.Context(), hash, mimeType); e != nil {
					blossomError(w, "failed to delete metadata: "+e.Error(), http.StatusInternalServerError)
					return
				}
			}

			blossomError(w, fmt.Sprintf("blob hash does not match any \"x\" tag in authorization event: got %q, want %q",
				hash, bodyHash), http.StatusBadRequest)
			return
		}
	}

	bd := blossom.BlobDescriptor{
		URL:      nostr.HTTPHostURL(r, nostr.DefaultIPChecker, hash+blossom.ExtensionByMimeType(mimeType)).String(),
		SHA256:   hash,
		Size:     size,
		Type:     mimeType,
		Uploaded: nostr.Now(),
	}

	if e := bs.Store.Keep(r.Context(), bd, auth.PubKey); e != nil {
		blossomError(w, "failed to save metadata: "+e.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if e := json.NewEncoder(w).Encode(bd); e != nil {
		blossomError(w, "failed to write response: "+e.Error(), http.StatusInternalServerError)
		return
	}
}

// handleGetBlob retrieves a blob.
func (bs BlossomServer) handleGetBlob(w http.ResponseWriter, r *http.Request) {
	hash, mimeType, ok := parseHashPath(r.URL.Path)
	if !ok {
		blossomError(w, "invalid /<sha256>[.ext] path", http.StatusBadRequest)
		return
	}

	auth, e := readAuthorization(r)
	if e != nil {
		blossomError(w, "invalid \"Authorization\": "+e.Error(), http.StatusBadRequest)
		return
	}

	if auth != nil {
		if auth.Tags.FindWithValue("t", "get") == nil {
			blossomError(w, "invalid \"Authorization\" event \"t\" tag", http.StatusForbidden)
			return
		}

		if auth.Tags.FindWithValue("x", hash) == nil &&
			auth.Tags.FindWithValue("server", nostr.HTTPHostURL(r, nostr.DefaultIPChecker).String()) == nil {
			blossomError(w, "invalid \"Authorization\" event \"x\" or \"server\" tag", http.StatusForbidden)
			return
		}
	}

	var modtime time.Time
	if bd, _ := bs.Store.Get(r.Context(), hash, nostr.HTTPHostURL(r, nostr.DefaultIPChecker)); bd != nil {
		mimeType = bd.Type
		modtime = bd.Uploaded.Time()
	}

	if bs.RejectGet != nil {
		if reject, reason, code := bs.RejectGet(r.Context(), auth, hash, mimeType); reject {
			blossomError(w, reason, code)
			return
		}
	}

	if bs.LoadBlob != nil {
		reader, redirectURL, e := bs.LoadBlob(r.Context(), hash, mimeType)
		if e != nil {
			blossomError(w, "load failed: "+e.Error(), http.StatusInternalServerError)
			return
		}

		if reader != nil {
			defer reader.Close()
			w.Header().Set("ETag", hash)
			w.Header().Set("Cache-Control", "public, max-age=604800, must-revalidate")
			http.ServeContent(w, r, hash+blossom.ExtensionByMimeType(mimeType), modtime, reader)
			return
		}

		if redirectURL != nil {
			if !strings.Contains(redirectURL.Path, hash) {
				blossomError(w, "redirect url doesn't contain the file hash", http.StatusInternalServerError)
				return
			}

			w.Header().Set("Expires", "0")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")

			http.Redirect(w, r, redirectURL.String(), http.StatusTemporaryRedirect)
			return
		}
	}

	if bs.CustomBlobNotFound != nil {
		bs.CustomBlobNotFound(w, r, hash, mimeType)
		return
	}

	blossomError(w, "file not found", http.StatusNotFound)
}

// handleHasBlob checks if a blob exists (HEAD request).
func (bs BlossomServer) handleHasBlob(w http.ResponseWriter, r *http.Request) {
	hash, _, ok := parseHashPath(r.URL.Path)
	if !ok {
		blossomError(w, "invalid /<sha256>[.ext] path", http.StatusBadRequest)
		return
	}

	if bd, e := bs.Store.Get(r.Context(), hash, nostr.HTTPHostURL(r, nostr.DefaultIPChecker)); e != nil {
		blossomError(w, "query failed: "+e.Error(), http.StatusInternalServerError)
		return
	} else if bd != nil {
		w.Header().Set("Content-Length", strconv.FormatInt(bd.Size, 10))
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Type", bd.Type)
		return
	}

	blossomError(w, "file not found", http.StatusNotFound)
}

// handleList lists blobs for a pubkey.
func (bs BlossomServer) handleList(w http.ResponseWriter, r *http.Request) {
	auth, e := readAuthorization(r)
	if e != nil {
		blossomError(w, "invalid \"Authorization\": "+e.Error(), http.StatusBadRequest)
		return
	}

	if auth != nil {
		if auth.Tags.FindWithValue("t", "list") == nil {
			blossomError(w, "invalid \"Authorization\" event \"t\" tag", http.StatusForbidden)
			return
		}
	}

	pubkey, e := nostr.PubKeyFromHex(r.URL.Path[6:])
	if e != nil {
		blossomError(w, "invalid pubkey: "+e.Error(), http.StatusBadRequest)
		return
	}

	if bs.RejectList != nil {
		if reject, reason, code := bs.RejectList(r.Context(), auth, pubkey); reject {
			blossomError(w, reason, code)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")

	if _, e := w.Write([]byte{'['}); e != nil {
		blossomError(w, e.Error(), http.StatusInternalServerError)
		return
	}
	enc := json.NewEncoder(w)
	first := true
	for bd := range bs.Store.List(r.Context(), pubkey, nostr.HTTPHostURL(r, nostr.DefaultIPChecker)) {
		if !first {
			if _, e := w.Write([]byte{','}); e != nil {
				blossomError(w, e.Error(), http.StatusInternalServerError)
				return
			}
		}
		first = false
		if e := enc.Encode(bd); e != nil {
			blossomError(w, e.Error(), http.StatusInternalServerError)
			return
		}
	}
	if _, e := w.Write([]byte{']'}); e != nil {
		blossomError(w, e.Error(), http.StatusInternalServerError)
		return
	}
}

// handleDelete deletes a blob.
func (bs BlossomServer) handleDelete(w http.ResponseWriter, r *http.Request) {
	auth, e := readAuthorization(r)
	if e != nil {
		blossomError(w, "invalid \"Authorization\": "+e.Error(), http.StatusBadRequest)
		return
	}

	if auth != nil {
		if auth.Tags.FindWithValue("t", "delete") == nil {
			blossomError(w, "invalid \"Authorization\" event \"t\" tag", http.StatusForbidden)
			return
		}
	}

	hash, mimeType, ok := parseHashPath(r.URL.Path)
	if !ok {
		blossomError(w, "invalid /<sha256>[.ext] path", http.StatusBadRequest)
		return
	}

	if auth.Tags.FindWithValue("x", hash) == nil &&
		auth.Tags.FindWithValue("server", nostr.HTTPHostURL(r, nostr.DefaultIPChecker).String()) == nil {
		blossomError(w, "invalid \"Authorization\" event \"x\" or \"server\" tag", http.StatusForbidden)
		return
	}

	if bd, _ := bs.Store.Get(r.Context(), hash, nostr.HTTPHostURL(r, nostr.DefaultIPChecker)); bd != nil {
		mimeType = bd.Type
	}

	if bs.RejectDelete != nil {
		if reject, reason, code := bs.RejectDelete(r.Context(), auth, hash, mimeType); reject {
			blossomError(w, reason, code)
			return
		}
	}

	if e := bs.Store.Delete(r.Context(), hash, auth.PubKey); e != nil {
		blossomError(w, "delete of blob entry failed: "+e.Error(), http.StatusInternalServerError)
		return
	}

	// Check if any other user owns this blob before physical deletion
	if bd, e := bs.Store.Get(r.Context(), hash, nostr.HTTPHostURL(r, nostr.DefaultIPChecker)); e == nil && bd == nil {
		if bs.DeleteBlob != nil {
			if e := bs.DeleteBlob(r.Context(), hash, mimeType); e != nil {
				blossomError(w, "failed to delete blob: "+e.Error(), http.StatusInternalServerError)
				return
			}
		}
	}
}

// handleReport handles reporting of objectionable content.
func (bs BlossomServer) handleReport(w http.ResponseWriter, r *http.Request) {
	var evt nostr.Event
	if e := json.NewDecoder(r.Body).Decode(&evt); e != nil {
		blossomError(w, "can't parse event", http.StatusBadRequest)
		return
	}

	if !evt.VerifySignature() || evt.Kind != nostr.KindReporting {
		blossomError(w, "invalid report event is provided", http.StatusBadRequest)
		return
	}

	if bs.ReceiveReport != nil {
		if e := bs.ReceiveReport(r.Context(), evt); e != nil {
			blossomError(w, "failed to receive report: "+e.Error(), http.StatusInternalServerError)
			return
		}
	}
}

// handleMedia redirects legacy media paths to upload.
func (bs BlossomServer) handleMedia(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/upload", http.StatusTemporaryRedirect)
}
