package khatru

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

func (rl *Relay) HandleNIP11(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/nostr+json")

	info := *rl.Info

	if nil != rl.DeleteEvent {
		info.AddSupportedNIP("9")
	}
	if nil != rl.Count {
		info.AddSupportedNIP("45")
	}
	if rl.Negentropy {
		info.AddSupportedNIP("77")
	}

	// resolve relative icon and banner URLs against base URL
	baseURL := rl.getBaseURL(r)
	if info.Icon != "" && !strings.HasPrefix(info.Icon, "http://") && !strings.HasPrefix(info.Icon, "https://") {
		info.Icon = strings.TrimSuffix(baseURL, "/") + "/" + strings.TrimPrefix(info.Icon, "/")
	}
	if info.Banner != "" && !strings.HasPrefix(info.Banner, "http://") && !strings.HasPrefix(info.Banner, "https://") {
		info.Banner = strings.TrimSuffix(baseURL, "/") + "/" + strings.TrimPrefix(info.Banner, "/")
	}

	if nil != rl.OverwriteRelayInformation {
		info = rl.OverwriteRelayInformation(r.Context(), r, info)
	}

	fmt.Println(info.SupportedNIPs)
	fmt.Println(info.SupportedNIPs[0] == "1")

	json.NewEncoder(w).Encode(info)
}
