package grasp

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"fiatjaf.com/nostr"
)

// handleInfoRefs handles the git info/refs endpoint
func (gs *GraspServer) handleInfoRefs(
	w http.ResponseWriter,
	r *http.Request,
	pubkey nostr.PubKey,
	repoName string,
) {
	if !gs.repoExists(r.Context(), pubkey, repoName) {
		w.Header().Set("content-type", "text/plain; charset=UTF-8")
		w.WriteHeader(404)
		fmt.Fprintf(w, "repository announcement event not found during info-refs\n")
		return
	}

	repoPath := gs.getRepositoryPath(pubkey, repoName)
	serviceName := r.URL.Query().Get("service")

	w.Header().Set("Connection", "Keep-Alive")
	w.Header().Set("Cache-Control", "no-cache, max-age=0, must-revalidate")
	w.Header().Set("Content-Type", "application/x-"+serviceName+"-advertisement")

	if _, err := os.Stat(repoPath); os.IsNotExist(err) {
		// if the repo doesn't exist that's because it wasn't pushed yet, so return an empty response

		// service advertisement header: packet-line with "# service=<service-name>\n"
		serviceLine := fmt.Sprintf("# service=%s\n", serviceName)
		// write packet line
		length := len(serviceLine) + 4
		fmt.Fprintf(w, "%04x%s", length, serviceLine)

		// flush
		w.Write([]byte("0000"))

		// another flush packet to indicate end of refs
		w.Write([]byte("0000"))

		return
	}

	if err := gs.runInfoRefs(w, r, serviceName, repoPath); err != nil {
		gs.Log("error on info-refs rpc: %s\n", err)
		return
	}
}

// runInfoRefs executes git-upload-pack with --http-backend-info-refs
func (gs *GraspServer) runInfoRefs(w http.ResponseWriter, r *http.Request, serviceName, repoPath string) error {
	cmd := exec.Command(serviceName, "--stateless-rpc", "--http-backend-info-refs", ".")
	cmd.Dir = repoPath
	cmd.Env = append(os.Environ(), fmt.Sprintf("GIT_PROTOCOL=%s", r.Header.Get("Git-Protocol")))

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	// write pack line header only if not git protocol v2
	if !strings.Contains(r.Header.Get("Git-Protocol"), "version=2") {
		// packLine
		s := "# service=" + serviceName + "\n"
		if _, err := fmt.Fprintf(w, "%04x%s", len(s)+4, s); err != nil {
			return fmt.Errorf("failed to write pack line: %w", err)
		}

		// packFlush
		if _, err := fmt.Fprint(w, "0000"); err != nil {
			return fmt.Errorf("failed to flush pack: %w", err)
		}
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start %s: %w, %s", serviceName, err, stderr.String())
	}

	io.Copy(gs.newWriteFlusher(w), stdoutPipe)
	stdoutPipe.Close()

	if err := cmd.Wait(); err != nil {
		gs.Log("%s failed: %w, stderr: %s", serviceName, err, stderr.String())
		return fmt.Errorf("%s failed: %w, stderr: %s", serviceName, err, stderr.String())
	}

	return nil
}
