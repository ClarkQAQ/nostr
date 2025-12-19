package grasp

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/relay"
)

type GraspServer struct {
	ServiceURL    string
	RepositoryDir string

	Relay *relay.Relay
	Log   func(str string, args ...any)

	OnWrite func(context.Context, nostr.PubKey, string) (reject bool, reason string)
	OnRead  func(context.Context, nostr.PubKey, string) (reject bool, reason string)
}

// New creates a new GraspServer and registers its handlers on the relay's router
func New(rl *relay.Relay, repositoryDir string) *GraspServer {
	gs := &GraspServer{
		Relay:         rl,
		RepositoryDir: repositoryDir,
		Log: func(str string, args ...any) {
			fmt.Fprintf(os.Stderr, str, args...)
		},
	}

	rl.Info.AddSupportedNIP(34)
	rl.Info.SupportedGrasps = append(rl.Info.SupportedGrasps, "GRASP-01")

	base := rl.Router()
	mux := http.NewServeMux()

	// use specific route patterns for git endpoints
	mux.HandleFunc("GET /{npub}/{repo}/info/refs", func(w http.ResponseWriter, r *http.Request) {
		gs.handleGitRequest(w, r, base, gs.handleInfoRefs)
	})

	mux.HandleFunc("POST /{npub}/{repo}/git-upload-pack", func(w http.ResponseWriter, r *http.Request) {
		gs.handleGitRequest(w, r, base, gs.handleGitUploadPack)
	})

	mux.HandleFunc("POST /{npub}/{repo}/git-receive-pack", func(w http.ResponseWriter, r *http.Request) {
		gs.handleGitRequest(w, r, base, gs.handleGitReceivePack)
	})

	mux.HandleFunc("GET /{npub}/{repo}", func(w http.ResponseWriter, r *http.Request) {
		gs.handleGitRequest(w, r, base, func(w http.ResponseWriter, r *http.Request, pubkey nostr.PubKey, repoName string) {
			if r.URL.RawQuery == "" {
				if gs.repoExists(r.Context(), pubkey, repoName) {
					gs.serveRepoPage(w, r, r.PathValue("npub"), repoName)
				} else {
					http.NotFound(w, r)
				}
			} else {
				base.ServeHTTP(w, r)
			}
		})
	})

	// fallback handler for all other paths
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		base.ServeHTTP(w, r)
	})

	rl.SetRouter(mux)

	return gs
}
