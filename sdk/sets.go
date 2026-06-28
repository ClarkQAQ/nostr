package sdk

import (
	"context"
	"slices"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/sdk/cache"
	cache_memory "fiatjaf.com/nostr/sdk/cache/memory"
)

// this is similar to lists.go and inherits code from that.

type GenericSets[V comparable, I TagItemWithValue[V]] struct {
	PubKey nostr.PubKey  `json:"-"`
	Events []nostr.Event `json:"-"`

	Sets map[string][]I
}

func fetchGenericSets[V comparable, I TagItemWithValue[V]](
	sys *System,
	ctx context.Context,
	pubkey nostr.PubKey,
	actualKind nostr.Kind,
	addressableIndex addressableIndex,
	parseTag func(nostr.Tag) (I, bool),
	cache cache.Cache32[GenericSets[V, I]],
) (fl GenericSets[V, I], fromInternal bool) {
	n := pubkey[7]
	lockIdx := (nostr.Kind(n) + actualKind) % 60
	genericListMutexes[lockIdx].Lock()

	if valueWasJustCached[lockIdx].CompareAndSwap(true, false) {
		time.Sleep(time.Millisecond * 10)
	}

	genericListMutexes[lockIdx].Unlock()

	if v, ok := cache.Get(pubkey); ok {
		return v, true
	}

	v := GenericSets[V, I]{PubKey: pubkey}

	events := slices.Collect(
		sys.Store.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{actualKind}, Authors: []nostr.PubKey{pubkey}}, 100),
	)
	if len(events) != 0 {
		sets := parseSetsFromEvents(events, parseTag)
		v.Events = events
		v.Sets = sets

		lastFetchKey := makeLastFetchKey(actualKind, pubkey)
		lastFetchData, _ := sys.KVStore.Get(lastFetchKey)
		if lastFetchData == nil || nostr.Now()-decodeTimestamp(lastFetchData) > getLocalStoreRefreshDaysForKind(actualKind)*24*60*60 {
			newV := tryFetchSetsFromNetwork(ctx, sys, pubkey, addressableIndex, parseTag)

			v = *newV
			for _, evt := range newV.Events {
				sys.Store.ReplaceEvent(evt)
			}

			sys.KVStore.Set(lastFetchKey, encodeTimestamp(nostr.Now()))
		}

		cache.SetWithTTL(pubkey, v, time.Hour*6)
		valueWasJustCached[lockIdx].Store(true)

		return v, true
	}

	if newV := tryFetchSetsFromNetwork(ctx, sys, pubkey, addressableIndex, parseTag); newV != nil {
		v = *newV

		for _, evt := range newV.Events {
			sys.Store.ReplaceEvent(evt)
		}

		lastFetchKey := makeLastFetchKey(actualKind, pubkey)
		sys.KVStore.Set(lastFetchKey, encodeTimestamp(nostr.Now()))
	}

	cache.SetWithTTL(pubkey, v, time.Hour*6)
	valueWasJustCached[lockIdx].Store(true)

	return v, false
}

func tryFetchSetsFromNetwork[V comparable, I TagItemWithValue[V]](
	ctx context.Context,
	sys *System,
	pubkey nostr.PubKey,
	addressableIndex addressableIndex,
	parseTag func(nostr.Tag) (I, bool),
) *GenericSets[V, I] {
	events, err := sys.addressableLoaders[addressableIndex].Load(ctx, pubkey)
	if err != nil {
		return nil
	}

	v := &GenericSets[V, I]{
		PubKey: pubkey,
		Events: events,
		Sets:   parseSetsFromEvents(events, parseTag),
	}
	for _, evt := range events {
		sys.Publisher.Publish(ctx, evt)
	}
	return v
}

func parseSetsFromEvents[V comparable, I TagItemWithValue[V]](
	events []nostr.Event,
	parseTag func(nostr.Tag) (I, bool),
) map[string][]I {
	sets := make(map[string][]I, len(events))
	for _, evt := range events {
		items := make([]I, 0, len(evt.Tags))
		for _, tag := range evt.Tags {
			item, ok := parseTag(tag)
			if ok {
				if slices.IndexFunc(items, func(i I) bool { return i.Value() == item.Value() }) == -1 {
					items = append(items, item)
				}
			}
		}
		sets[evt.Tags.GetD()] = items
	}
	return sets
}

// -- set fetch methods

func (sys *System) FetchFollowSets(ctx context.Context, pubkey nostr.PubKey) GenericSets[nostr.PubKey, ProfileRef] {
	sys.followSetsCacheOnce.Do(func() {
		if sys.FollowSetsCache == nil {
			sys.FollowSetsCache = cache_memory.New[GenericSets[nostr.PubKey, ProfileRef]](1000)
		}
	})

	ml, _ := fetchGenericSets(sys, ctx, pubkey, 30000, kind_30000, parseProfileRef, sys.FollowSetsCache)
	return ml
}

func (sys *System) FetchRelaySets(ctx context.Context, pubkey nostr.PubKey) GenericSets[string, RelayURL] {
	sys.relaySetsCacheOnce.Do(func() {
		if sys.RelaySetsCache == nil {
			sys.RelaySetsCache = cache_memory.New[GenericSets[string, RelayURL]](1000)
		}
	})

	ml, _ := fetchGenericSets(sys, ctx, pubkey, 30002, kind_30002, parseRelayURL, sys.RelaySetsCache)
	return ml
}

func (sys *System) FetchTopicSets(ctx context.Context, pubkey nostr.PubKey) GenericSets[string, Topic] {
	sys.topicSetsCacheOnce.Do(func() {
		if sys.TopicSetsCache == nil {
			sys.TopicSetsCache = cache_memory.New[GenericSets[string, Topic]](1000)
		}
	})

	ml, _ := fetchGenericSets(sys, ctx, pubkey, 30015, kind_30015, parseTopicString, sys.TopicSetsCache)
	return ml
}

func (sys *System) FetchEmojiSets(ctx context.Context, pubkey nostr.PubKey) GenericSets[string, Emoji] {
	sys.emojiSetsCacheOnce.Do(func() {
		if sys.EmojiSetsCache == nil {
			sys.EmojiSetsCache = cache_memory.New[GenericSets[string, Emoji]](1000)
		}
	})

	fl, _ := fetchGenericSets(sys, ctx, pubkey, 30030, kind_30030, parseEmojiTag, sys.EmojiSetsCache)
	return fl
}
