package nip5a

import (
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"unsafe"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip19"
)

type SiteManifest struct {
	Event       *nostr.Event
	Pubkey      nostr.PubKey
	Root        bool
	Identifier  string
	Paths       map[string][32]byte
	Servers     []string
	Title       string
	Description string
	Source      string
}

func ParseSiteManifest(event *nostr.Event) (*SiteManifest, error) {
	sm := &SiteManifest{Event: event}

	switch event.Kind {
	case nostr.KindNsiteRoot:
		sm.Root = true
	case nostr.KindNsiteNamed:
		sm.Root = false
		for _, tag := range event.Tags {
			if len(tag) >= 2 && tag[0] == "d" {
				sm.Identifier = tag[1]
				break
			}
		}
		if sm.Identifier == "" {
			return nil, fmt.Errorf("named site manifest missing d tag")
		}
	default:
		return nil, fmt.Errorf("invalid site manifest kind: %d", event.Kind)
	}

	sm.Pubkey = event.PubKey
	sm.Paths = make(map[string][32]byte, len(event.Tags))

	for _, tag := range event.Tags {
		if len(tag) < 2 {
			continue
		}
		switch tag[0] {
		case "path":
			var hash [32]byte
			if len(tag[2]) != 64 {
				return nil, fmt.Errorf("invalid hash '%s' for path '%s'", tag[2], tag[1])
			}
			if _, err := hex.Decode(hash[:], unsafe.Slice(unsafe.StringData(tag[2]), 64)); err != nil {
				return nil, fmt.Errorf("invalid hash '%s' for path '%s'", tag[2], tag[1])
			}
			sm.Paths[NormalizePath(tag[1])] = hash
		case "server":
			sm.Servers = append(sm.Servers, tag[1])
		case "title":
			sm.Title = tag[1]
		case "description":
			sm.Description = tag[1]
		case "source":
			sm.Source = tag[1]
		}
	}

	if len(sm.Paths) == 0 {
		return sm, fmt.Errorf("nsite has zero paths listed")
	}

	return sm, nil
}

func (sm SiteManifest) ToEvent() nostr.Event {
	event := nostr.Event{
		PubKey:    sm.Pubkey,
		CreatedAt: nostr.Now(),
		Tags:      nostr.Tags{},
	}

	if sm.Root {
		event.Kind = nostr.KindNsiteRoot
	} else {
		event.Kind = nostr.KindNsiteNamed
		event.Tags = append(event.Tags, nostr.Tag{"d", sm.Identifier})
	}

	for path, hash := range sm.Paths {
		event.Tags = append(event.Tags, nostr.Tag{"path", NormalizePath(path), hex.EncodeToString(hash[:])})
	}
	for _, s := range sm.Servers {
		event.Tags = append(event.Tags, nostr.Tag{"server", s})
	}
	if sm.Title != "" {
		event.Tags = append(event.Tags, nostr.Tag{"title", sm.Title})
	}
	if sm.Description != "" {
		event.Tags = append(event.Tags, nostr.Tag{"description", sm.Description})
	}
	if sm.Source != "" {
		event.Tags = append(event.Tags, nostr.Tag{"source", sm.Source})
	}

	return event
}

//go:inline
func (sm *SiteManifest) GetHashForPath(path string) ([32]byte, bool) {
	path = NormalizePath(path)
	hash, ok := sm.Paths[path]
	return hash, ok
}

func DecodeSiteURL(label string) (pubkey nostr.PubKey, identifier string, isRoot bool, err error) {
	label, _, _ = strings.Cut(label, ".")

	if strings.HasPrefix(label, "npub1") {
		_, value, err := nip19.Decode(label)
		if err != nil {
			return nostr.ZeroPK, "", false, err
		}
		return value.(nostr.PubKey), "", true, nil
	}

	if len(label) < 51 || len(label) > 63 || strings.HasSuffix(label, "-") {
		return nostr.ZeroPK, "", false, fmt.Errorf("invalid site label format")
	}

	pubkeyB36 := label[:50]
	dTag := label[50:]
	if !regexp.MustCompile(`^[a-z0-9-]{1,13}$`).MatchString(dTag) {
		return nostr.ZeroPK, "", false, fmt.Errorf("invalid dtag format")
	}

	pk, err := PubKeyFromBase36(pubkeyB36)
	if err != nil {
		return nostr.ZeroPK, "", false, err
	}

	return pk, dTag, false, nil
}
