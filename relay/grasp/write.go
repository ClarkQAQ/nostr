package grasp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip34"
)

func (gs *GraspServer) handleGitReceivePack(
	w http.ResponseWriter,
	r *http.Request,
	pubkey nostr.PubKey,
	repoName string,
) {
	// for receive-pack (push), validate authorization via NIP-34 events
	body := &bytes.Buffer{}
	io.Copy(body, r.Body)

	if err := gs.validatePush(r.Context(), pubkey, repoName, body.Bytes()); err != nil {
		w.Header().Set("content-type", "text/plain; charset=UTF-8")
		w.WriteHeader(403)
		fmt.Fprintf(w, "unauthorized push: %v\n", err)
		return
	}

	if gs.OnWrite != nil {
		reject, msg := gs.OnWrite(r.Context(), pubkey, repoName)
		if reject {
			w.Header().Set("content-type", "text/plain; charset=UTF-8")
			w.WriteHeader(403)
			fmt.Fprintf(w, "%s\n", msg)
			return
		}
	}

	repoPath := gs.getRepositoryPath(pubkey, repoName)

	// initialize git repo if it doesn't exist
	if _, err := os.Stat(repoPath); os.IsNotExist(err) {
		if err := os.MkdirAll(repoPath, 0755); err != nil {
			w.Header().Set("content-type", "text/plain; charset=UTF-8")
			w.WriteHeader(500)
			fmt.Fprintf(w, "failed to create repository: %s\n", err)
			return
		}

		cmd := exec.Command("git", "init", "--bare")
		cmd.Dir = repoPath
		if output, err := cmd.CombinedOutput(); err != nil {
			w.Header().Set("content-type", "text/plain; charset=UTF-8")
			w.WriteHeader(500)
			fmt.Fprintf(w, "failed to initialize repository: %s, output: %s\n", err, string(output))
			return
		}

		// disable denyNonFastForwards and denyCurrentBranch to allow force pushes
		for _, config := range []struct {
			key   string
			value string
		}{
			{"receive.denyNonFastForwards", "false"},
			{"receive.denyCurrentBranch", "updateInstead"},
			{"uploadpack.allowReachableSHA1InWant", "true"},
			{"uploadpack.allowTipSHA1InWant", "true"},
		} {
			cmd = exec.Command("git", "config", config.key, config.value)
			cmd.Dir = repoPath
			if output, err := cmd.CombinedOutput(); err != nil {
				w.Header().Set("content-type", "text/plain; charset=UTF-8")
				w.WriteHeader(500)
				fmt.Fprintf(w, "failed to configure repository with %s=%s: %s, output: %s\n",
					config.key, config.value, err, string(output))
				return
			}
		}
	}

	w.Header().Set("Content-Type", "application/x-git-receive-pack-result")
	w.Header().Set("Connection", "Keep-Alive")
	w.Header().Set("Cache-Control", "no-cache, max-age=0, must-revalidate")

	if err := gs.runReceivePack(w, r, repoPath, io.NopCloser(bytes.NewReader(body.Bytes()))); err != nil {
		w.Header().Set("content-type", "text/plain; charset=UTF-8")
		w.WriteHeader(403)
		fmt.Fprintf(w, "runReceivePack: %v\n", err)
		return
	}

	// update HEAD per state announcement
	if err := gs.updateHEAD(r.Context(), pubkey, repoName, repoPath); err != nil {
		w.Header().Set("content-type", "text/plain; charset=UTF-8")
		w.WriteHeader(403)
		fmt.Fprintf(w, "failed to update HEAD: %v\n", err)
		return
	}

	// cleanup merged patches
	go gs.cleanupMergedPatches(r.Context(), pubkey, repoName, repoPath)
}

// runReceivePack executes git-receive-pack for push operations
func (gs *GraspServer) runReceivePack(w http.ResponseWriter, r *http.Request, repoPath string, bodyReader io.ReadCloser) error {
	cmd := exec.Command("git", "receive-pack", "--stateless-rpc", ".")
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
		return fmt.Errorf("failed to start git-receive-pack: %w", err)
	}

	wg := sync.WaitGroup{}

	// copy input to stdin
	wg.Go(func() {
		defer stdinPipe.Close()
		if _, err := io.Copy(stdinPipe, bodyReader); err != nil {
			gs.Log("failed to copy to stdin pipe: %s", err)
		}
	})

	// copy output to response
	wg.Go(func() {
		defer stdoutPipe.Close()
		if _, err := io.Copy(gs.newWriteFlusher(w), stdoutPipe); err != nil {
			gs.Log("failed to copy to write flusher: %s", err)
		}
	})

	wg.Wait()

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("git-receive-pack failed: %w, stderr: %s", err, stderr.String())
	}

	return nil
}

// updateHEAD updates the repository HEAD based on the latest state announcement
func (gs *GraspServer) updateHEAD(ctx context.Context, pubkey nostr.PubKey, repoName, repoPath string) error {
	if gs.Relay.QueryStored == nil {
		return fmt.Errorf("no QueryStored function")
	}

	// query for the latest state event
	var latestState *nip34.RepositoryState
	for evt := range gs.Relay.QueryStored(ctx, nostr.Filter{
		Kinds:   []nostr.Kind{nostr.KindRepositoryState},
		Authors: []nostr.PubKey{pubkey},
		Tags:    nostr.TagMap{"d": []string{repoName}},
		Limit:   1,
	}) {
		state := nip34.ParseRepositoryState(evt)
		latestState = &state
		break
	}

	if latestState == nil || latestState.HEAD == "" {
		// no state or no HEAD specified
		return nil
	}

	// verify the HEAD branch exists in the state
	if _, exists := latestState.Branches[latestState.HEAD]; !exists {
		return fmt.Errorf("HEAD branch %s not found in state", latestState.HEAD)
	}

	// update HEAD using git symbolic-ref
	cmd := exec.Command("git", "symbolic-ref", "HEAD", "refs/heads/"+latestState.HEAD)
	cmd.Dir = repoPath
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to update HEAD: %w, output: %s", err, string(output))
	}
	return nil
}

// cleanupMergedPatches removes refs/nostr/<event-id> refs that have been merged into branches
func (gs *GraspServer) cleanupMergedPatches(ctx context.Context, pubkey nostr.PubKey, repoName, repoPath string) {
	// use background context since request context will be cancelled
	ctx = context.Background()

	// wait 20 minutes before cleanup to allow events to propagate
	time.Sleep(20 * time.Minute)

	if gs.Relay.QueryStored == nil {
		return
	}

	// get current state to know which branches exist
	var state *nip34.RepositoryState
	for evt := range gs.Relay.QueryStored(ctx, nostr.Filter{
		Kinds:   []nostr.Kind{nostr.KindRepositoryState},
		Authors: []nostr.PubKey{pubkey},
		Tags:    nostr.TagMap{"d": []string{repoName}},
		Limit:   1,
	}) {
		parsed := nip34.ParseRepositoryState(evt)
		state = &parsed
		break
	}

	if state == nil {
		return
	}

	// list all refs/nostr/* refs
	cmd := exec.Command("git", "for-each-ref", "--format=%(refname)", "refs/nostr")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		// no refs/nostr refs, nothing to clean up
		return
	}

	refs := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, ref := range refs {
		if ref == "" {
			continue
		}

		eventId := strings.TrimPrefix(ref, "refs/nostr/")
		id, err := nostr.IDFromHex(eventId)
		if err != nil {
			return
		}

		// check if there's still a valid patch event with a "c" tag referencing this commit
		hasValidEvent := false
		for evt := range gs.Relay.QueryStored(ctx, nostr.Filter{
			IDs: []nostr.ID{id},
		}) {
			// check if event has a "c" tag
			for _, tag := range evt.Tags {
				if tag[0] == "c" && len(tag) > 1 {
					hasValidEvent = true
					break
				}
			}
			break
		}

		if !hasValidEvent {
			// no valid event, delete the ref
			cmd := exec.Command("git", "update-ref", "-d", ref)
			cmd.Dir = repoPath
			if err := cmd.Run(); err != nil {
				gs.Log("failed to delete ref %s: %s\n", ref, err)
			} else {
				gs.Log("deleted ref %s (no corresponding event)\n", ref)
			}
			continue
		}

		// check if the commit is merged into any branch
		for branchName, commitId := range state.Branches {
			// get the commit ID for this ref
			cmd := exec.Command("git", "rev-parse", ref)
			cmd.Dir = repoPath
			refCommit, err := cmd.Output()
			if err != nil {
				continue
			}

			// check if ref commit is ancestor of branch head
			cmd = exec.Command("git", "merge-base", "--is-ancestor", strings.TrimSpace(string(refCommit)), commitId)
			cmd.Dir = repoPath
			if err := cmd.Run(); err == nil {
				// it's merged! delete the ref
				cmd := exec.Command("git", "update-ref", "-d", ref)
				cmd.Dir = repoPath
				if err := cmd.Run(); err != nil {
					gs.Log("failed to delete ref %s: %s\n", ref, err)
				} else {
					gs.Log("deleted ref %s (merged into %s)\n", ref, branchName)
				}
				break
			}
		}
	}
}
