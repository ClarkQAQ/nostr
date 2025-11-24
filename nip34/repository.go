package nip34

import (
	"fmt"

	"fiatjaf.com/nostr"
)

type Repository struct {
	nostr.Event

	ID                     string
	Name                   string
	Description            string
	Web                    []string
	Clone                  []string
	Relays                 []string
	EarliestUniqueCommitID string
	Maintainers            []nostr.PubKey
}

func ParseRepository(event nostr.Event) Repository {
	repo := Repository{
		Event: event,
	}

	for _, tag := range event.Tags {
		if len(tag) < 2 {
			continue
		}
		switch tag[0] {
		case "d":
			repo.ID = tag[1]
		case "name":
			repo.Name = tag[1]
		case "description":
			repo.Description = tag[1]
		case "web":
			repo.Web = append(repo.Web, tag[1:]...)
		case "clone":
			repo.Clone = append(repo.Clone, tag[1:]...)
		case "relays":
			repo.Relays = append(repo.Relays, tag[1:]...)
		case "r":
			repo.EarliestUniqueCommitID = tag[1]
		case "maintainers":
			for _, pkh := range tag[1:] {
				if pk, err := nostr.PubKeyFromHex(pkh); err == nil {
					repo.Maintainers = append(repo.Maintainers, pk)
				}
			}
		}
	}

	return repo
}

func (r Repository) ToEvent() nostr.Event {
	tags := make(nostr.Tags, 0, 10)

	tags = append(tags, nostr.Tag{"d", r.ID})

	if r.Name != "" {
		tags = append(tags, nostr.Tag{"name", r.Name})
	}
	if r.Description != "" {
		tags = append(tags, nostr.Tag{"description", r.Description})
	}
	if r.EarliestUniqueCommitID != "" {
		tags = append(tags, nostr.Tag{"r", r.EarliestUniqueCommitID, "euc"})
	}
	if len(r.Maintainers) > 0 {
		tag := make(nostr.Tag, 1, 1+len(r.Maintainers))
		tag[0] = "maintainers"
		for _, pk := range r.Maintainers {
			tag = append(tag, pk.Hex())
		}
		tags = append(tags, tag)
	}
	if len(r.Web) > 0 {
		tag := make(nostr.Tag, 1, 1+len(r.Web))
		tag[0] = "web"
		tag = append(tag, r.Web...)
		tags = append(tags, tag)
	}
	if len(r.Clone) > 0 {
		tag := make(nostr.Tag, 1, 1+len(r.Clone))
		tag[0] = "clone"
		tag = append(tag, r.Clone...)
		tags = append(tags, tag)
	}
	if len(r.Relays) > 0 {
		tag := make(nostr.Tag, 1, 1+len(r.Relays))
		tag[0] = "relays"
		tag = append(tag, r.Relays...)
		tags = append(tags, tag)
	}

	return nostr.Event{
		Kind:      nostr.KindRepositoryAnnouncement,
		Tags:      tags,
		CreatedAt: nostr.Now(),
	}
}

func (r Repository) String() string {
	return fmt.Sprintf("Repository{ID: %s, Name: %s, Description: %s, Web: %v, Clone: %v, Relays: %v, EarliestUniqueCommitID: %s, Maintainers: %v}", r.ID, r.Name, r.Description, r.Web, r.Clone, r.Relays, r.EarliestUniqueCommitID, r.Maintainers)
}

func (r Repository) Equals(other Repository) bool {
	if r.ID != other.ID || r.Name != other.Name || r.Description != other.Description {
		return false
	}
	if r.EarliestUniqueCommitID != other.EarliestUniqueCommitID {
		return false
	}
	if len(r.Web) != len(other.Web) || len(r.Clone) != len(other.Clone) ||
		len(r.Relays) != len(other.Relays) || len(r.Maintainers) != len(other.Maintainers) {
		return false
	}
	for i := range r.Web {
		if r.Web[i] != other.Web[i] {
			return false
		}
	}
	for i := range r.Clone {
		if r.Clone[i] != other.Clone[i] {
			return false
		}
	}
	for i := range r.Relays {
		if r.Relays[i] != other.Relays[i] {
			return false
		}
	}
	for i := range r.Maintainers {
		if r.Maintainers[i] != other.Maintainers[i] {
			return false
		}
	}
	return true
}
