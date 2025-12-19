package grasp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strings"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip34"
	"github.com/go-git/go-git/v5/plumbing/format/pktline"
)

const zeroRef = "0000000000000000000000000000000000000000"

// validatePush checks if a push is authorized via NIP-34 repository state events
func (gs *GraspServer) validatePush(
	ctx context.Context,
	pubkey nostr.PubKey,
	repoName string,
	bodyBytes []byte,
) error {
	// query for repository state events (kind 30618)
	if gs.Relay.QueryStored == nil {
		return errors.New("relay has no QueryStored function")
	}

	// check state
	var state nip34.RepositoryState
	for evt := range gs.Relay.QueryStored(ctx, nostr.Filter{
		Kinds:   []nostr.Kind{nostr.KindRepositoryState},
		Authors: []nostr.PubKey{pubkey},
		Tags:    nostr.TagMap{"d": []string{repoName}},
		Limit:   1,
	}) {
		state = nip34.ParseRepositoryState(evt)
	}
	if state.Event.ID == nostr.ZeroID {
		return fmt.Errorf("no state found for repository '%s'", repoName)
	}

	// get repository announcement to check maintainers
	var announcement nip34.Repository
	for evt := range gs.Relay.QueryStored(ctx, nostr.Filter{
		Kinds:   []nostr.Kind{nostr.KindRepositoryAnnouncement},
		Authors: []nostr.PubKey{pubkey},
		Tags:    nostr.TagMap{"d": []string{repoName}},
		Limit:   1,
	}) {
		announcement = nip34.ParseRepository(evt)
	}
	if announcement.Event.ID == nostr.ZeroID {
		return fmt.Errorf("no announcement found for repository '%s'", repoName)
	}

	// ensure pusher is authorized (owner or maintainer)
	if pubkey != announcement.PubKey && !slices.Contains(announcement.Maintainers, pubkey) {
		return fmt.Errorf("pusher '%s' is not authorized for repository '%s'", pubkey, repoName)
	}

	// parse pktline to extract and validate all push refs
	pkt := pktline.NewScanner(bytes.NewReader(bodyBytes))
	for pkt.Scan() {
		if err := pkt.Err(); err != nil {
			return fmt.Errorf("invalid pkt: %v", err)
		}
		line := string(pkt.Bytes())
		if len(line) < 40 {
			continue
		}

		spl := strings.Split(line, " ")
		from := spl[0]
		to := spl[1]
		ref := strings.TrimRight(spl[2], "\x00")

		// handle refs/nostr/<event-id> pushes
		if strings.HasPrefix(ref, "refs/nostr/") {
			// query for the event
			eventId := ref[11:]
			id, err := nostr.IDFromHex(eventId)
			if err != nil {
				return fmt.Errorf("push rejected: invalid event id %s", eventId)
			}
			var foundEvent bool
			for evt := range gs.Relay.QueryStored(ctx, nostr.Filter{
				IDs: []nostr.ID{id},
			}) {
				// check if event has a "c" tag matching the commit
				hasMatchingCommit := false
				for _, tag := range evt.Tags {
					if tag[0] == "c" && len(tag) > 1 && tag[1] == to {
						hasMatchingCommit = true
						break
					}
				}
				if !hasMatchingCommit {
					return fmt.Errorf("push rejected: event %s has different tip (expected %s)", eventId, to)
				}
				foundEvent = true
				break
			}
			if !foundEvent {
				return fmt.Errorf("push rejected: event %s not found", eventId)
			}
			continue
		}

		// validate branch pushes
		if strings.HasPrefix(ref, "refs/heads/") {
			branchName := ref[11:]
			// pushing a branch
			if commitId, exists := state.Branches[branchName]; exists && to == commitId {
				continue
			}
			// deleting a branch
			if _, exists := state.Branches[branchName]; to == zeroRef && !exists {
				continue
			}
			return fmt.Errorf("push unauthorized: ref %s %s->%s does not match state", ref, from, to)
		}

		// validate tag pushes
		if strings.HasPrefix(ref, "refs/tags/") {
			tagName := ref[10:]
			// pushing a tag
			if commitId, exists := state.Tags[tagName]; exists && to == commitId {
				continue
			}
			// deleting a tag
			if _, exists := state.Tags[tagName]; to == zeroRef && !exists {
				continue
			}
			return fmt.Errorf("push unauthorized: ref %s %s->%s does not match state", ref, from, to)
		}
	}

	return nil
}

func (gs *GraspServer) getRepositoryPath(pubkey nostr.PubKey, repoName string) string {
	return filepath.Join(gs.RepositoryDir, pubkey.Hex(), repoName)
}

// repoExists checks if a repository has an announcement event (kind 30617)
func (gs *GraspServer) repoExists(ctx context.Context, pubkey nostr.PubKey, repoName string) bool {
	for range gs.Relay.QueryStored(ctx, nostr.Filter{
		Kinds:   []nostr.Kind{nostr.KindRepositoryAnnouncement},
		Authors: []nostr.PubKey{pubkey},
		Tags:    nostr.TagMap{"d": []string{repoName}},
	}) {
		return true
	}
	return false
}

// newWriteFlusher creates a write flusher for streaming responses
func (gs *GraspServer) newWriteFlusher(w http.ResponseWriter) io.Writer {
	return writeFlusher{w.(interface {
		io.Writer
		http.Flusher
	})}
}

type writeFlusher struct {
	wf interface {
		io.Writer
		http.Flusher
	}
}

func (w writeFlusher) Write(p []byte) (int, error) {
	defer w.wf.Flush()
	return w.wf.Write(p)
}
