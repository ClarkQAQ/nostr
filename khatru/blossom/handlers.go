package blossom

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nipb0/blossom"
	"github.com/liamg/magic"
)

type mirrorRequest struct {
	URL string `json:"url"`
}

func (bs BlossomServer) handleUploadCheck(w http.ResponseWriter, r *http.Request) {
	auth, err := readAuthorization(r)
	if err != nil {
		blossomError(w, err.Error(), 400)
		return
	}
	if auth == nil {
		blossomError(w, "missing \"Authorization\" header", 401)
		return
	}
	if auth.Tags.FindWithValue("t", "upload") == nil {
		blossomError(w, "invalid \"Authorization\" event \"t\" tag", 403)
		return
	}

	mimetype := r.Header.Get("X-Content-Type")
	ext := blossom.GetExtension(mimetype)

	// get the file size from the incoming header
	size, _ := strconv.Atoi(r.Header.Get("X-Content-Length"))

	if bs.RejectUpload != nil {
		reject, reason, code := bs.RejectUpload(r.Context(), auth, size, ext)
		if reject {
			blossomError(w, reason, code)
			return
		}
	}
}

func (bs BlossomServer) handleUpload(w http.ResponseWriter, r *http.Request) {
	auth, err := readAuthorization(r)
	if err != nil {
		blossomError(w, "invalid \"Authorization\": "+err.Error(), 404)
		return
	}
	if auth == nil {
		blossomError(w, "missing \"Authorization\" header", 401)
		return
	}
	if auth.Tags.FindWithValue("t", "upload") == nil {
		blossomError(w, "invalid \"Authorization\" event \"t\" tag", 403)
		return
	}

	// get the file size from the incoming header
	size, _ := strconv.Atoi(r.Header.Get("Content-Length"))
	if size <= 0 {
		blossomError(w, "missing \"Content-Length\" header", 400)
		return
	}

	// read first bytes of upload so we can find out the filetype
	b := make([]byte, min(50, size), size+1 /* the extra 1 is for checking the validity of the Content-Length */)
	if n, err := r.Body.Read(b); err != nil && n != size {
		blossomError(w, "failed to read initial bytes of upload body: "+err.Error(), 400)
		return
	}
	var ext string
	if ft, _ := magic.Lookup(b); ft != nil {
		ext = "." + ft.Extension
	} else {
		// if we can't find, use the filetype given by the upload header
		mimetype := r.Header.Get("Content-Type")
		ext = blossom.GetExtension(mimetype)
	}

	// special case of android apk -- if we see a .zip but they say it's .apk we trust them
	if ext == ".zip" && blossom.GetExtension(r.Header.Get("Content-Type")) == ".apk" {
		ext = ".apk"
	}

	// run the reject hooks
	if nil != bs.RejectUpload {
		reject, reason, code := bs.RejectUpload(r.Context(), auth, size, ext)
		if reject {
			blossomError(w, reason, code)
			return
		}
	}

	// if it passes then we have to read the entire thing into memory so we can compute the sha256
	// we will only read as much as specified in the Content-Length header
	if size > len(b) {
		alreadyRead := len(b)
		for {
			n, err := r.Body.Read(b[alreadyRead : size+1])
			alreadyRead += n
			if err == io.EOF {
				break
			} else if err != nil && err != io.EOF {
				blossomError(w, "failed to read upload body: "+err.Error(), 400)
				return
			}
			if alreadyRead > size {
				blossomError(w, "file is bigger than was specified in Content-Length", 400)
				return
			}
		}
		if alreadyRead != size {
			blossomError(w, fmt.Sprintf("got a %d bytes but Content-Length said %d", alreadyRead, size), 400)
			return
		}
		b = b[0:size]
	}

	hash := sha256.Sum256(b)
	hhash := nostr.HexEncodeToString(hash[:])
	mimeType := mime.TypeByExtension(ext)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	// keep track of the blob descriptor
	bd := blossom.BlobDescriptor{
		URL:      bs.ServiceURL + "/" + hhash + ext,
		SHA256:   hhash,
		Size:     len(b),
		Type:     mimeType,
		Uploaded: nostr.Now(),
	}
	if err := bs.Store.Keep(r.Context(), bd, auth.PubKey); err != nil {
		blossomError(w, "failed to save blob descriptor: "+err.Error(), 400)
		return
	}

	// save actual blob
	if nil != bs.StoreBlob {
		if err := bs.StoreBlob(r.Context(), hhash, ext, b); err != nil {
			blossomError(w, "failed to save: "+err.Error(), 500)
			return
		}
	}

	// return response
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(bd)
}

func (bs BlossomServer) handleGetBlob(w http.ResponseWriter, r *http.Request) {
	spl := strings.SplitN(r.URL.Path, ".", 2)
	hhash := spl[0]
	if len(hhash) != 65 {
		blossomError(w, "invalid /<sha256>[.ext] path", 400)
		return
	}
	hhash = hhash[1:]

	// check for an authorization tag, if any
	auth, err := readAuthorization(r)
	if err != nil {
		blossomError(w, err.Error(), 400)
		return
	}

	// if there is one, we check if it has the extra requirements
	if auth != nil {
		if auth.Tags.FindWithValue("t", "get") == nil {
			blossomError(w, "invalid \"Authorization\" event \"t\" tag", 403)
			return
		}

		if auth.Tags.FindWithValue("x", hhash) == nil &&
			auth.Tags.FindWithValue("server", bs.ServiceURL) == nil {
			blossomError(w, "invalid \"Authorization\" event \"x\" or \"server\" tag", 403)
			return
		}
	}

	var ext string
	bd, err := bs.Store.Get(r.Context(), hhash)
	if err != nil {
		// can't find the BlobDescriptor, try to get the extension from the URL
		if len(spl) == 2 {
			ext = spl[1]
		}
	} else if bd != nil {
		ext = blossom.GetExtension(bd.Type)
	}

	if nil != bs.RejectGet {
		reject, reason, code := bs.RejectGet(r.Context(), auth, hhash, ext)
		if reject {
			blossomError(w, reason, code)
			return
		}
	}

	if bs.LoadBlob != nil {
		reader, redirectURL, err := bs.LoadBlob(r.Context(), hhash, ext)
		if err == nil && redirectURL != nil {
			// check that the redirectURL contains the hash of the file
			if ok, _ := regexp.MatchString(`\b`+hhash+`\b`, redirectURL.String()); !ok {
				blossomError(w, "redirect url doesn't contain the file hash", 500)
				return
			}

			w.Header().Set("ETag", hhash)
			w.Header().Set("Cache-Control", "public, max-age=604800, immutable")
			http.Redirect(w, r, redirectURL.String(), http.StatusTemporaryRedirect)
			return
		}

		if reader != nil {
			// use unix epoch as the time if we can't find the descriptor
			// as described in the http.ServeContent documentation
			t := time.Unix(0, 0)
			descriptor, err := bs.Store.Get(r.Context(), hhash)
			if err == nil && descriptor != nil {
				t = descriptor.Uploaded.Time()
			}
			w.Header().Set("ETag", hhash)
			w.Header().Set("Cache-Control", "public, max-age=604800, immutable")
			name := hhash
			if ext != "" {
				name += ext
			}
			http.ServeContent(w, r, name, t, reader)
			return
		}
	}

	blossomError(w, "file not found", 404)
}

func (bs BlossomServer) handleHasBlob(w http.ResponseWriter, r *http.Request) {
	spl := strings.SplitN(r.URL.Path, ".", 2)
	hhash := spl[0]
	if len(hhash) != 65 {
		blossomError(w, "invalid /<sha256>[.ext] path", 400)
		return
	}
	hhash = hhash[1:]

	bd, err := bs.Store.Get(r.Context(), hhash)
	if err != nil {
		blossomError(w, "failed to query: "+err.Error(), 500)
		return
	}

	if bd == nil {
		blossomError(w, "file not found", 404)
		return
	}

	w.Header().Set("Content-Length", strconv.Itoa(bd.Size))
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Type", bd.Type)
}

func (bs BlossomServer) handleList(w http.ResponseWriter, r *http.Request) {
	// check for an authorization tag, if any
	auth, err := readAuthorization(r)
	if err != nil {
		blossomError(w, err.Error(), 400)
		return
	}

	// if there is one, we check if it has the extra requirements
	if auth != nil {
		if auth.Tags.FindWithValue("t", "list") == nil {
			blossomError(w, "invalid \"Authorization\" event \"t\" tag", 403)
			return
		}
	}

	pubkey, err := nostr.PubKeyFromHex(r.URL.Path[6:])

	if nil != bs.RejectList {
		reject, reason, code := bs.RejectList(r.Context(), auth, pubkey)
		if reject {
			blossomError(w, reason, code)
			return
		}
	}

	w.Write([]byte{'['})
	enc := json.NewEncoder(w)
	first := true
	for bd := range bs.Store.List(r.Context(), pubkey) {
		if !first {
			w.Write([]byte{','})
		} else {
			first = false
		}
		enc.Encode(bd)
	}
	w.Write([]byte{']'})
}

func (bs BlossomServer) handleDelete(w http.ResponseWriter, r *http.Request) {
	auth, err := readAuthorization(r)
	if err != nil {
		blossomError(w, err.Error(), 400)
		return
	}

	if auth != nil {
		if auth.Tags.FindWithValue("t", "delete") == nil {
			blossomError(w, "invalid \"Authorization\" event \"t\" tag", 403)
			return
		}
	}

	spl := strings.SplitN(r.URL.Path, ".", 2)
	hhash := spl[0]
	if len(hhash) != 65 {
		blossomError(w, "invalid /<sha256>[.ext] path", 400)
		return
	}
	hhash = hhash[1:]
	if auth.Tags.FindWithValue("x", hhash) == nil &&
		auth.Tags.FindWithValue("server", bs.ServiceURL) == nil {
		blossomError(w, "invalid \"Authorization\" event \"x\" or \"server\" tag", 403)
		return
	}

	var ext string
	bd, err := bs.Store.Get(r.Context(), hhash)
	if err != nil {
		// can't find the BlobDescriptor, try to get the extension from the URL
		if len(spl) == 2 {
			ext = spl[1]
		}
	} else if bd != nil {
		ext = blossom.GetExtension(bd.Type)
	}

	// should we accept this delete?
	if nil != bs.RejectDelete {
		reject, reason, code := bs.RejectDelete(r.Context(), auth, hhash, ext)
		if reject {
			blossomError(w, reason, code)
			return
		}
	}

	// delete the entry that links this blob to this author
	if err := bs.Store.Delete(r.Context(), hhash, auth.PubKey); err != nil {
		blossomError(w, "delete of blob entry failed: "+err.Error(), 500)
		return
	}

	// we will actually only delete the file if no one else owns it
	if bd, err := bs.Store.Get(r.Context(), hhash); err == nil && bd == nil {
		if nil != bs.DeleteBlob {
			if err := bs.DeleteBlob(r.Context(), hhash, ext); err != nil {
				blossomError(w, "failed to delete blob: "+err.Error(), 500)
				return
			}
		}
	}
}

func (bs BlossomServer) handleReport(w http.ResponseWriter, r *http.Request) {
	var body []byte
	_, err := r.Body.Read(body)
	if err != nil {
		blossomError(w, "can't read request body", 400)
		return
	}

	var evt nostr.Event
	if err := json.Unmarshal(body, &evt); err != nil {
		blossomError(w, "can't parse event", 400)
		return
	}

	if !evt.VerifySignature() {
		blossomError(w, "invalid report event is provided", 400)
		return
	}

	if evt.Kind != nostr.KindReporting {
		blossomError(w, "invalid report event is provided", 400)
		return
	}

	if bs.ReceiveReport != nil {
		if err := bs.ReceiveReport(r.Context(), evt); err != nil {
			blossomError(w, "failed to receive report: "+err.Error(), 500)
			return
		}
	}
}

func (bs BlossomServer) handleMirror(w http.ResponseWriter, r *http.Request) {
	auth, err := readAuthorization(r)
	if err != nil {
		blossomError(w, "invalid \"Authorization\": "+err.Error(), 400)
		return
	}
	if auth == nil {
		blossomError(w, "missing \"Authorization\" header", 401)
		return
	}
	if auth.Tags.FindWithValue("t", "upload") == nil {
		blossomError(w, "invalid \"Authorization\" event \"t\" tag", 403)
		return
	}

	var req mirrorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		blossomError(w, "invalid request body: "+err.Error(), 400)
		return
	}

	// download the blob
	resp, err := http.Get(req.URL)
	if err != nil {
		blossomError(w, "failed to download from url: "+err.Error(), 503)
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		blossomError(w, "failed to read response body: "+err.Error(), 503)
		return
	}

	// calculate sha256
	hash := sha256.Sum256(body)
	hhash := nostr.HexEncodeToString(hash[:])

	// verify hash against x tag
	if auth.Tags.FindWithValue("x", hhash) == nil {
		blossomError(w, "blob hash does not match any \"x\" tag in authorization event", 403)
		return
	}

	// determine content type and extension
	var ext string
	contentType := resp.Header.Get("Content-Type")
	if contentType != "" {
		ext = blossom.GetExtension(contentType)
	} else if ft, _ := magic.Lookup(body); ft != nil {
		ext = "." + ft.Extension
	} else if idx := strings.LastIndex(req.URL, "."); idx != -1 {
		ext = req.URL[idx:]
	}

	// run reject hook if defined
	if bs.RejectUpload != nil {
		reject, reason, code := bs.RejectUpload(r.Context(), auth, len(body), ext)
		if reject {
			blossomError(w, reason, code)
			return
		}
	}

	// keep track of the blob descriptor
	bd := blossom.BlobDescriptor{
		URL:      bs.ServiceURL + "/" + hhash + ext,
		SHA256:   hhash,
		Size:     len(body),
		Type:     contentType,
		Uploaded: nostr.Now(),
	}
	if err := bs.Store.Keep(r.Context(), bd, auth.PubKey); err != nil {
		blossomError(w, "failed to save event: "+err.Error(), 400)
		return
	}

	// save actual blob
	if bs.StoreBlob != nil {
		if err := bs.StoreBlob(r.Context(), hhash, ext, body); err != nil {
			blossomError(w, "failed to save: "+err.Error(), 500)
			return
		}
	}

	// return response
	json.NewEncoder(w).Encode(bd)
}

func (bs BlossomServer) handleMedia(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/upload", 307)
	return
}

func (bs BlossomServer) handleNegentropy(w http.ResponseWriter, r *http.Request) {
}
