package sdk

import (
	"context"

	"fiatjaf.com/nostr"
	cache_memory "fiatjaf.com/nostr/sdk/cache/memory"
)

type BlossomURL string

func (r BlossomURL) Value() string { return string(r) }

func (sys *System) FetchBlossomServerList(ctx context.Context, pubkey nostr.PubKey) GenericList[string, BlossomURL] {
	sys.blossomServerListCacheOnce.Do(func() {
		if sys.BlossomServerListCache == nil {
			sys.BlossomServerListCache = cache_memory.New[GenericList[string, BlossomURL]](1000)
		}
	})

	ml, _ := fetchGenericList(sys, ctx, pubkey, 10101, kind_10101, func(t nostr.Tag) (BlossomURL, bool) {
		if len(t) < 2 {
			return "", false
		}

		nm, err := nostr.NormalizeHTTPURL(t[1])
		if err != nil {
			return "", false
		}

		return BlossomURL(nm), true
	}, sys.BlossomServerListCache)
	return ml
}
