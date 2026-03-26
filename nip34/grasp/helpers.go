package grasp

import (
	"net/url"
	"strings"

	"fiatjaf.com/nostr/nip19"
)

func IsGraspURL(u string) bool {
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}

	if strings.Count(parsed.Path, "/") != 2 || len(parsed.Path) < 65 {
		return false
	}

	if prefix, _, err := nip19.Decode(parsed.Path[1:64]); err != nil || prefix != "npub" {
		return false
	}

	return true
}
