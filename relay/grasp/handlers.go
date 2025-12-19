package grasp

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip19"
)

var asciiPattern = regexp.MustCompile(`^[\w-.]+$`)

// handleGitRequest validates .git suffix and decodes npub, then calls the handler
func (gs *GraspServer) handleGitRequest(
	w http.ResponseWriter,
	r *http.Request,
	base http.Handler,
	handler func(http.ResponseWriter,
		*http.Request,
		nostr.PubKey,
		string,
	),
) {
	npub := r.PathValue("npub")
	repoWithGit := r.PathValue("repo")

	// validate .git suffix
	if !strings.HasSuffix(repoWithGit, ".git") {
		base.ServeHTTP(w, r)
		return
	}

	repoName := strings.TrimSuffix(repoWithGit, ".git")

	// validate repo name
	if !asciiPattern.MatchString(repoName) {
		http.Error(w, "invalid repository name", 400)
		return
	}

	// decode npub to pubkey
	_, value, err := nip19.Decode(npub)
	if err != nil {
		http.Error(w, "invalid npub", 400)
		return
	}
	pk, ok := value.(nostr.PubKey)
	if !ok {
		http.Error(w, "invalid npub", 400)
		return
	}

	handler(w, r, pk, repoName)
}

// serveRepoPage serves a webpage for the repository
func (gs *GraspServer) serveRepoPage(w http.ResponseWriter, r *http.Request, npub, repoName string) {
	w.Header().Set("Content-Type", "text/html")
	html := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
	<title>%s/%s - NIP-34 Git Repository</title>
	<style>
		body { font-family: sans-serif; max-width: 800px; margin: 50px auto; padding: 20px; }
		h1 { color: #333; }
		code { background: #f4f4f4; padding: 2px 6px; border-radius: 3px; }
		pre { background: #f4f4f4; padding: 15px; border-radius: 5px; overflow-x: auto; }
		.info { background: #e7f3ff; padding: 15px; border-left: 4px solid #2196F3; margin: 20px 0; }
	</style>
</head>
<body>
	<h1>Repository: %s/%s</h1>
	<div class="info">
		<p>This is a NIP-34 git repository served over Nostr.</p>
	</div>
	<h2>Clone this repository</h2>
	<p>Use a git-nostr client to clone:</p>
	<pre>git clone %s/%s/%s.git</pre>
	<h2>Browse</h2>
	<p>Use a git-nostr web client or Nostr client to browse this repository.</p>
</body>
</html>`, npub, repoName, npub, repoName, r.Host, npub, repoName)
	fmt.Fprint(w, html)
}
