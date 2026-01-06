package nip86

import (
	"encoding/json"
	"fmt"
	"net"

	"fiatjaf.com/nostr"
)

func DecodeRequest(req Request) (MethodParams, error) {
	switch req.Method {
	case "supportedmethods":
		return SupportedMethods{}, nil
	case "banpubkey":
		if len(req.Params) == 0 {
			return nil, fmt.Errorf("invalid number of params for '%s'", req.Method)
		}

		var pk nostr.PubKey
		if e := json.Unmarshal(req.Params[0], &pk); e != nil {
			return nil, fmt.Errorf("invalid pubkey param for '%s'", req.Method)
		}

		var reason string
		if len(req.Params) >= 2 {
			if e := json.Unmarshal(req.Params[1], &reason); e != nil {
				return nil, fmt.Errorf("invalid reason param for '%s'", req.Method)
			}
		}

		return BanPubKey{pk, reason}, nil
	case "listbannedpubkeys":
		return ListBannedPubKeys{}, nil
	case "allowpubkey":
		if len(req.Params) == 0 {
			return nil, fmt.Errorf("invalid number of params for '%s'", req.Method)
		}

		var pk nostr.PubKey
		if e := json.Unmarshal(req.Params[0], &pk); e != nil {
			return nil, fmt.Errorf("invalid pubkey param for '%s'", req.Method)
		}

		var reason string
		if len(req.Params) >= 2 {
			if e := json.Unmarshal(req.Params[1], &reason); e != nil {
				return nil, fmt.Errorf("invalid reason param for '%s'", req.Method)
			}
		}

		return AllowPubKey{pk, reason}, nil
	case "listallowedpubkeys":
		return ListAllowedPubKeys{}, nil
	case "listeventsneedingmoderation":
		return ListEventsNeedingModeration{}, nil
	case "allowevent":
		if len(req.Params) == 0 {
			return nil, fmt.Errorf("invalid number of params for '%s'", req.Method)
		}

		var id nostr.ID
		if e := json.Unmarshal(req.Params[0], &id); e != nil {
			return nil, fmt.Errorf("invalid id param for '%s'", req.Method)
		}

		var reason string
		if len(req.Params) >= 2 {
			if e := json.Unmarshal(req.Params[1], &reason); e != nil {
				return nil, fmt.Errorf("invalid reason param for '%s'", req.Method)
			}
		}

		return AllowEvent{id, reason}, nil
	case "banevent":
		if len(req.Params) == 0 {
			return nil, fmt.Errorf("invalid number of params for '%s'", req.Method)
		}

		var id nostr.ID
		if e := json.Unmarshal(req.Params[0], &id); e != nil {
			return nil, fmt.Errorf("invalid id param for '%s'", req.Method)
		}

		var reason string
		if len(req.Params) >= 2 {
			if e := json.Unmarshal(req.Params[1], &reason); e != nil {
				return nil, fmt.Errorf("invalid reason param for '%s'", req.Method)
			}
		}

		return BanEvent{id, reason}, nil
	case "listbannedevents":
		return ListBannedEvents{}, nil
	case "listallowedevents":
		return ListAllowedEvents{}, nil
	case "listdisallowedkinds":
		return ListDisallowedKinds{}, nil
	case "changerelayname":
		if len(req.Params) == 0 {
			return nil, fmt.Errorf("invalid number of params for '%s'", req.Method)
		}

		var name string
		if e := json.Unmarshal(req.Params[0], &name); e != nil {
			return nil, fmt.Errorf("invalid name param for '%s'", req.Method)
		}

		return ChangeRelayName{name}, nil
	case "changerelaydescription":
		if len(req.Params) == 0 {
			return nil, fmt.Errorf("invalid number of params for '%s'", req.Method)
		}

		var desc string
		if e := json.Unmarshal(req.Params[0], &desc); e != nil {
			return nil, fmt.Errorf("invalid description param for '%s'", req.Method)
		}

		return ChangeRelayDescription{desc}, nil
	case "changerelayicon":
		if len(req.Params) == 0 {
			return nil, fmt.Errorf("invalid number of params for '%s'", req.Method)
		}

		var url string
		if e := json.Unmarshal(req.Params[0], &url); e != nil {
			return nil, fmt.Errorf("invalid icon url param for '%s'", req.Method)
		}

		return ChangeRelayIcon{url}, nil
	case "allowkind":
		if len(req.Params) == 0 {
			return nil, fmt.Errorf("invalid number of params for '%s'", req.Method)
		}

		var kind int
		if e := json.Unmarshal(req.Params[0], &kind); e != nil {
			return nil, fmt.Errorf("invalid kind param for '%s'", req.Method)
		}

		return AllowKind{kind}, nil
	case "disallowkind":
		if len(req.Params) == 0 {
			return nil, fmt.Errorf("invalid number of params for '%s'", req.Method)
		}

		var kind int
		if e := json.Unmarshal(req.Params[0], &kind); e != nil {
			return nil, fmt.Errorf("invalid kind param for '%s'", req.Method)
		}

		return DisallowKind{kind}, nil
	case "listallowedkinds":
		return ListAllowedKinds{}, nil
	case "blockip":
		if len(req.Params) == 0 {
			return nil, fmt.Errorf("invalid number of params for '%s'", req.Method)
		}

		var ipstr string
		if e := json.Unmarshal(req.Params[0], &ipstr); e != nil {
			return nil, fmt.Errorf("invalid ip param for '%s'", req.Method)
		}

		ip := net.ParseIP(ipstr)
		if ip == nil {
			return nil, fmt.Errorf("invalid ip param for '%s'", req.Method)
		}

		var reason string
		if len(req.Params) >= 2 {
			if e := json.Unmarshal(req.Params[1], &reason); e != nil {
				return nil, fmt.Errorf("invalid reason param for '%s'", req.Method)
			}
		}

		return BlockIP{ip, reason}, nil
	case "unblockip":
		if len(req.Params) == 0 {
			return nil, fmt.Errorf("invalid number of params for '%s'", req.Method)
		}

		var ipstr string
		if e := json.Unmarshal(req.Params[0], &ipstr); e != nil {
			return nil, fmt.Errorf("invalid ip param for '%s'", req.Method)
		}

		ip := net.ParseIP(ipstr)
		if ip == nil {
			return nil, fmt.Errorf("invalid ip param for '%s'", req.Method)
		}

		var reason string
		if len(req.Params) >= 2 {
			if e := json.Unmarshal(req.Params[1], &reason); e != nil {
				return nil, fmt.Errorf("invalid reason param for '%s'", req.Method)
			}
		}

		return UnblockIP{ip, reason}, nil
	case "listblockedips":
		return ListBlockedIPs{}, nil
	case "grantadmin":
		if len(req.Params) < 2 {
			return nil, fmt.Errorf("invalid number of params for '%s'", req.Method)
		}

		var pk nostr.PubKey
		if e := json.Unmarshal(req.Params[0], &pk); e != nil {
			return nil, fmt.Errorf("invalid pubkey param for '%s'", req.Method)
		}

		var allowedMethods []string
		if e := json.Unmarshal(req.Params[1], &allowedMethods); e != nil {
			return nil, fmt.Errorf("invalid allowed methods param for '%s'", req.Method)
		}

		return GrantAdmin{
			Pubkey:       pk,
			AllowMethods: allowedMethods,
		}, nil
	case "revokeadmin":
		if len(req.Params) < 2 {
			return nil, fmt.Errorf("invalid number of params for '%s'", req.Method)
		}

		var pk nostr.PubKey
		if e := json.Unmarshal(req.Params[0], &pk); e != nil {
			return nil, fmt.Errorf("invalid pubkey param for '%s'", req.Method)
		}

		var disallowedMethods []string
		if e := json.Unmarshal(req.Params[1], &disallowedMethods); e != nil {
			return nil, fmt.Errorf("invalid disallowed methods param for '%s'", req.Method)
		}

		return RevokeAdmin{
			Pubkey:          pk,
			DisallowMethods: disallowedMethods,
		}, nil
	case "stats":
		return Stats{}, nil
	default:
		return req, nil
	}
}

type MethodParams interface {
	MethodName() string
}

var (
	_ MethodParams = (*SupportedMethods)(nil)
	_ MethodParams = (*BanPubKey)(nil)
	_ MethodParams = (*ListBannedPubKeys)(nil)
	_ MethodParams = (*AllowPubKey)(nil)
	_ MethodParams = (*ListAllowedPubKeys)(nil)
	_ MethodParams = (*ListEventsNeedingModeration)(nil)
	_ MethodParams = (*AllowEvent)(nil)
	_ MethodParams = (*BanEvent)(nil)
	_ MethodParams = (*ListBannedEvents)(nil)
	_ MethodParams = (*ChangeRelayName)(nil)
	_ MethodParams = (*ChangeRelayDescription)(nil)
	_ MethodParams = (*ChangeRelayIcon)(nil)
	_ MethodParams = (*AllowKind)(nil)
	_ MethodParams = (*DisallowKind)(nil)
	_ MethodParams = (*ListAllowedKinds)(nil)
	_ MethodParams = (*BlockIP)(nil)
	_ MethodParams = (*UnblockIP)(nil)
	_ MethodParams = (*ListBlockedIPs)(nil)
	_ MethodParams = (*ListAllowedEvents)(nil)
	_ MethodParams = (*ListDisallowedKinds)(nil)
	_ MethodParams = (*GrantAdmin)(nil)
	_ MethodParams = (*RevokeAdmin)(nil)
	_ MethodParams = (*Stats)(nil)
)

type SupportedMethods struct{}

func (SupportedMethods) MethodName() string { return "supportedmethods" }

type BanPubKey struct {
	PubKey nostr.PubKey
	Reason string
}

func (BanPubKey) MethodName() string { return "banpubkey" }

type ListBannedPubKeys struct{}

func (ListBannedPubKeys) MethodName() string { return "listbannedpubkeys" }

type AllowPubKey struct {
	PubKey nostr.PubKey
	Reason string
}

func (AllowPubKey) MethodName() string { return "allowpubkey" }

type ListAllowedPubKeys struct{}

func (ListAllowedPubKeys) MethodName() string { return "listallowedpubkeys" }

type ListEventsNeedingModeration struct{}

func (ListEventsNeedingModeration) MethodName() string { return "listeventsneedingmoderation" }

type AllowEvent struct {
	ID     nostr.ID
	Reason string
}

func (AllowEvent) MethodName() string { return "allowevent" }

type BanEvent struct {
	ID     nostr.ID
	Reason string
}

func (BanEvent) MethodName() string { return "banevent" }

type ListBannedEvents struct{}

func (ListBannedEvents) MethodName() string { return "listbannedevents" }

type ChangeRelayName struct {
	Name string
}

func (ChangeRelayName) MethodName() string { return "changerelayname" }

type ChangeRelayDescription struct {
	Description string
}

func (ChangeRelayDescription) MethodName() string { return "changerelaydescription" }

type ChangeRelayIcon struct {
	IconURL string
}

func (ChangeRelayIcon) MethodName() string { return "changerelayicon" }

type AllowKind struct {
	Kind int
}

func (AllowKind) MethodName() string { return "allowkind" }

type DisallowKind struct {
	Kind int
}

func (DisallowKind) MethodName() string { return "disallowkind" }

type ListAllowedKinds struct{}

func (ListAllowedKinds) MethodName() string { return "listallowedkinds" }

type BlockIP struct {
	IP     net.IP
	Reason string
}

func (BlockIP) MethodName() string { return "blockip" }

type UnblockIP struct {
	IP     net.IP
	Reason string
}

func (UnblockIP) MethodName() string { return "unblockip" }

type ListBlockedIPs struct{}

func (ListBlockedIPs) MethodName() string { return "listblockedips" }

type ListAllowedEvents struct{}

func (ListAllowedEvents) MethodName() string { return "listallowedevents" }

type ListDisallowedKinds struct{}

func (ListDisallowedKinds) MethodName() string { return "listdisallowedkinds" }

type GrantAdmin struct {
	Pubkey       nostr.PubKey
	AllowMethods []string
}

func (GrantAdmin) MethodName() string { return "grantadmin" }

type RevokeAdmin struct {
	Pubkey          nostr.PubKey
	DisallowMethods []string
}

func (RevokeAdmin) MethodName() string { return "revokeadmin" }

type Stats struct{}

func (Stats) MethodName() string { return "stats" }
