package grasp

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"

	"fiatjaf.com/nostr"
)

func (gs *GraspServer) handleGitUploadPack(
	w http.ResponseWriter,
	r *http.Request,
	pubkey nostr.PubKey,
	repoName string,
) {
	repoPath := gs.getRepositoryPath(pubkey, repoName)

	// for upload-pack (pull), check if repository exists
	if !gs.repoExists(r.Context(), pubkey, repoName) {
		w.Header().Set("content-type", "text/plain; charset=UTF-8")
		w.WriteHeader(404)
		fmt.Fprintf(w, "repository announcement event not found during upload-pack\n")
		return
	}

	if gs.OnRead != nil {
		reject, msg := gs.OnRead(r.Context(), pubkey, repoName)
		if reject {
			w.Header().Set("content-type", "text/plain; charset=UTF-8")
			w.WriteHeader(403)
			fmt.Fprintf(w, "%s\n", msg)
			return
		}
	}

	const expectedContentType = "application/x-git-upload-pack-request"
	contentType := r.Header.Get("Content-Type")
	if contentType != expectedContentType {
		w.Header().Set("content-type", "text/plain; charset=UTF-8")
		w.WriteHeader(415)
		fmt.Fprintf(w, "expected Content-Type: '%s', but received '%s'\n", expectedContentType, contentType)
		return
	}

	var bodyReader io.ReadCloser = r.Body
	if r.Header.Get("Content-Encoding") == "gzip" {
		gzipReader, err := gzip.NewReader(r.Body)
		if err != nil {
			w.Header().Set("content-type", "text/plain; charset=UTF-8")
			w.WriteHeader(500)
			fmt.Fprintf(w, "failed to create gzip reader, handler: UploadPack, error: %v\n", err)
			return
		}
		defer gzipReader.Close()
		bodyReader = gzipReader
	}

	w.Header().Set("Content-Type", "application/x-git-upload-pack-result")
	w.Header().Set("Connection", "Keep-Alive")
	w.Header().Set("Cache-Control", "no-cache, max-age=0, must-revalidate")

	if err := gs.runUploadPack(w, r, repoPath, bodyReader); err != nil {
		w.Header().Set("content-type", "text/plain; charset=UTF-8")
		w.WriteHeader(403)
		fmt.Fprintf(w, "failed to execute git-upload-pack, handler: UploadPack, error: %v\n", err)
		return
	}
}

// runUploadPack executes git-upload-pack for pull operations
func (gs *GraspServer) runUploadPack(w http.ResponseWriter, r *http.Request, repoPath string, bodyReader io.ReadCloser) error {
	cmd := exec.Command("git", "upload-pack", "--stateless-rpc", ".")
	cmd.Dir = repoPath
	cmd.Env = append(os.Environ(), fmt.Sprintf("GIT_PROTOCOL=%s", r.Header.Get("Git-Protocol")))

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start git-upload-pack: %w", err)
	}

	// copy input to stdin
	go func() {
		defer stdinPipe.Close()
		io.Copy(stdinPipe, bodyReader)
	}()

	// copy output to response
	io.Copy(gs.newWriteFlusher(w), stdoutPipe)
	stdoutPipe.Close()

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("git-upload-pack failed: %w, stderr: %s", err, stderr.String())
	}

	return nil
}
