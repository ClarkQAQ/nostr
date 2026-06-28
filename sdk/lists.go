package sdk

import (
	"context"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/sdk/cache"
	cache_memory "fiatjaf.com/nostr/sdk/cache/memory"
)

type GenericList[V comparable, I TagItemWithValue[V]] struct {
	PubKey nostr.PubKey `json:"-"` // must always be set otherwise things will break
	Event  *nostr.Event `json:"-"` // may be empty if a contact list event wasn't found

	Items []I
}

type TagItemWithValue[V comparable] interface {
	Value() V
}

var (
	genericListMutexes = [60]sync.Mutex{}
	valueWasJustCached = [60]atomic.Bool{}
)

func fetchGenericList[V comparable, I TagItemWithValue[V]](
	sys *System,
	ctx context.Context,
	pubkey nostr.PubKey,
	actualKind nostr.Kind,
	replaceableIndex replaceableIndex,
	parseTag func(nostr.Tag) (I, bool),
	cache cache.Cache32[GenericList[V, I]],
) (fl GenericList[V, I], fromInternal bool) {
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

	v := GenericList[V, I]{PubKey: pubkey}

	for evt := range sys.Store.QueryEvents(nostr.Filter{
		Kinds:   []nostr.Kind{actualKind},
		Authors: []nostr.PubKey{pubkey},
	}, 1) {
		items := parseItemsFromEventTags(evt, parseTag)
		v.Event = &evt
		v.Items = items

		lastFetchKey := makeLastFetchKey(actualKind, pubkey)
		lastFetchData, _ := sys.KVStore.Get(lastFetchKey)
		if lastFetchData == nil || nostr.Now()-decodeTimestamp(lastFetchData) > getLocalStoreRefreshDaysForKind(actualKind)*24*60*60 {
			newV := tryFetchListFromNetwork(ctx, sys, pubkey, replaceableIndex, parseTag)
			if newV != nil && newV.Event.CreatedAt > v.Event.CreatedAt {
				v = *newV
				sys.Store.ReplaceEvent(*v.Event)
			}

			sys.KVStore.Set(lastFetchKey, encodeTimestamp(nostr.Now()))
		}

		cache.SetWithTTL(pubkey, v, time.Hour*6)
		valueWasJustCached[lockIdx].Store(true)

		return v, true
	}

	if newV := tryFetchListFromNetwork(ctx, sys, pubkey, replaceableIndex, parseTag); newV != nil {
		v = *newV

		lastFetchKey := makeLastFetchKey(actualKind, pubkey)
		sys.KVStore.Set(lastFetchKey, encodeTimestamp(nostr.Now()))
		sys.Store.ReplaceEvent(*v.Event)
	}

	cache.SetWithTTL(pubkey, v, time.Hour*6)
	valueWasJustCached[lockIdx].Store(true)

	return v, false
}

func tryFetchListFromNetwork[V comparable, I TagItemWithValue[V]](
	ctx context.Context,
	sys *System,
	pubkey nostr.PubKey,
	replaceableIndex replaceableIndex,
	parseTag func(nostr.Tag) (I, bool),
) *GenericList[V, I] {
	evt, err := sys.replaceableLoaders[replaceableIndex].Load(ctx, pubkey)
	if err != nil {
		return nil
	}

	v := &GenericList[V, I]{
		PubKey: pubkey,
		Event:  &evt,
		Items:  parseItemsFromEventTags(evt, parseTag),
	}
	sys.Publisher.Publish(ctx, evt)

	return v
}

func parseItemsFromEventTags[V comparable, I TagItemWithValue[V]](
	evt nostr.Event,
	parseTag func(nostr.Tag) (I, bool),
) []I {
	result := make([]I, 0, len(evt.Tags))
	for _, tag := range evt.Tags {
		item, ok := parseTag(tag)
		if ok {
			if slices.IndexFunc(result, func(i I) bool { return i.Value() == item.Value() }) == -1 {
				result = append(result, item)
			}
		}
	}
	return result
}

func getLocalStoreRefreshDaysForKind(kind nostr.Kind) nostr.Timestamp {
	switch kind {
	case 0:
		return 7
	case 3:
		return 1
	default:
		return 3
	}
}

// -- profile-based list items

type ProfileRef struct {
	Pubkey  nostr.PubKey
	Relay   string
	Petname string
}

func (f ProfileRef) Value() nostr.PubKey { return f.Pubkey }

func (sys *System) FetchFollowList(ctx context.Context, pubkey nostr.PubKey) GenericList[nostr.PubKey, ProfileRef] {
	sys.followListCacheOnce.Do(func() {
		if sys.FollowListCache == nil {
			sys.FollowListCache = cache_memory.New[GenericList[nostr.PubKey, ProfileRef]](1000)
		}
	})

	fl, _ := fetchGenericList(sys, ctx, pubkey, 3, kind_3, parseProfileRef, sys.FollowListCache)
	return fl
}

func (sys *System) FetchMuteList(ctx context.Context, pubkey nostr.PubKey) GenericList[nostr.PubKey, ProfileRef] {
	sys.muteListCacheOnce.Do(func() {
		if sys.MuteListCache == nil {
			sys.MuteListCache = cache_memory.New[GenericList[nostr.PubKey, ProfileRef]](1000)
		}
	})

	ml, _ := fetchGenericList(sys, ctx, pubkey, 10000, kind_10000, parseProfileRef, sys.MuteListCache)
	return ml
}

func (sys *System) FetchMediaFollowList(ctx context.Context, pubkey nostr.PubKey) GenericList[nostr.PubKey, ProfileRef] {
	sys.mediaFollowListCacheOnce.Do(func() {
		if sys.MediaFollowListCache == nil {
			sys.MediaFollowListCache = cache_memory.New[GenericList[nostr.PubKey, ProfileRef]](1000)
		}
	})

	ml, _ := fetchGenericList(sys, ctx, pubkey, 10020, kind_10020, parseProfileRef, sys.MediaFollowListCache)
	return ml
}

func (sys *System) FetchGoodWikiAuthorList(ctx context.Context, pubkey nostr.PubKey) GenericList[nostr.PubKey, ProfileRef] {
	sys.goodWikiAuthorListCacheOnce.Do(func() {
		if sys.GoodWikiAuthorListCache == nil {
			sys.GoodWikiAuthorListCache = cache_memory.New[GenericList[nostr.PubKey, ProfileRef]](1000)
		}
	})

	ml, _ := fetchGenericList(sys, ctx, pubkey, 10101, kind_10101, parseProfileRef, sys.GoodWikiAuthorListCache)
	return ml
}

func (sys *System) FetchGitAuthorList(ctx context.Context, pubkey nostr.PubKey) GenericList[nostr.PubKey, ProfileRef] {
	sys.gitAuthorListCacheOnce.Do(func() {
		if sys.GitAuthorListCache == nil {
			sys.GitAuthorListCache = cache_memory.New[GenericList[nostr.PubKey, ProfileRef]](1000)
		}
	})

	ml, _ := fetchGenericList(sys, ctx, pubkey, 10017, kind_10017, parseProfileRef, sys.GitAuthorListCache)
	return ml
}

func parseProfileRef(tag nostr.Tag) (fw ProfileRef, ok bool) {
	if len(tag) < 2 {
		return fw, false
	}
	if tag[0] != "p" {
		return fw, false
	}

	pubkey, err := nostr.PubKeyFromHex(tag[1])
	if err != nil {
		return fw, false
	}
	fw.Pubkey = pubkey

	if len(tag) > 2 {
		if _, err := url.Parse(tag[2]); err == nil {
			fw.Relay = nostr.NormalizeURL(tag[2])
		}

		if len(tag) > 3 {
			fw.Petname = strings.TrimSpace(tag[3])
		}
	}

	return fw, true
}

// -- event-based list items

type EventRef struct{ nostr.Pointer }

func (e EventRef) Value() string { return e.Pointer.AsTagReference() }

func (sys *System) FetchBookmarkList(ctx context.Context, pubkey nostr.PubKey) GenericList[string, EventRef] {
	sys.bookmarkListCacheOnce.Do(func() {
		if sys.BookmarkListCache == nil {
			sys.BookmarkListCache = cache_memory.New[GenericList[string, EventRef]](1000)
		}
	})

	ml, _ := fetchGenericList(sys, ctx, pubkey, 10003, kind_10003, parseEventRef, sys.BookmarkListCache)
	return ml
}

func (sys *System) FetchProfileBadgesList(ctx context.Context, pubkey nostr.PubKey) GenericList[string, EventRef] {
	sys.profileBadgesListCacheOnce.Do(func() {
		if sys.ProfileBadgesListCache == nil {
			sys.ProfileBadgesListCache = cache_memory.New[GenericList[string, EventRef]](1000)
		}
	})

	ml, _ := fetchGenericList(sys, ctx, pubkey, 10008, kind_10008, parseEventRef, sys.ProfileBadgesListCache)
	return ml
}

func (sys *System) FetchPinList(ctx context.Context, pubkey nostr.PubKey) GenericList[string, EventRef] {
	sys.pinListCacheOnce.Do(func() {
		if sys.PinListCache == nil {
			sys.PinListCache = cache_memory.New[GenericList[string, EventRef]](1000)
		}
	})

	ml, _ := fetchGenericList(sys, ctx, pubkey, 10001, kind_10001, parseEventRef, sys.PinListCache)
	return ml
}

func (sys *System) FetchGitRepositoryList(ctx context.Context, pubkey nostr.PubKey) GenericList[string, EventRef] {
	sys.gitRepositoryListCacheOnce.Do(func() {
		if sys.GitRepositoryListCache == nil {
			sys.GitRepositoryListCache = cache_memory.New[GenericList[string, EventRef]](1000)
		}
	})

	ml, _ := fetchGenericList(sys, ctx, pubkey, 10018, kind_10018, parseEventRef, sys.GitRepositoryListCache)
	return ml
}

func parseEventRef(tag nostr.Tag) (evr EventRef, ok bool) {
	if len(tag) < 2 {
		return evr, false
	}
	switch tag[0] {
	case "e":
		pointer, err := nostr.EventPointerFromTag(tag)
		if err != nil {
			return evr, false
		}
		evr.Pointer = pointer
	case "a":
		pointer, err := nostr.EntityPointerFromTag(tag)
		if err != nil {
			return evr, false
		}
		evr.Pointer = pointer
	default:
		return evr, false
	}

	return evr, true
}

// -- relay-based list items

type Relay struct {
	URL    string
	Inbox  bool
	Outbox bool
}

func (r Relay) Value() string { return r.URL }

type RelayURL string

func (r RelayURL) Value() string { return string(r) }

func (sys *System) FetchRelayList(ctx context.Context, pubkey nostr.PubKey) GenericList[string, Relay] {
	ml, _ := fetchGenericList(sys, ctx, pubkey, 10002, kind_10002, parseRelayFromKind10002, sys.RelayListCache)
	return ml
}

func (sys *System) FetchBlockedRelayList(ctx context.Context, pubkey nostr.PubKey) GenericList[string, RelayURL] {
	sys.blockedRelayListCacheOnce.Do(func() {
		if sys.BlockedRelayListCache == nil {
			sys.BlockedRelayListCache = cache_memory.New[GenericList[string, RelayURL]](1000)
		}
	})

	ml, _ := fetchGenericList(sys, ctx, pubkey, 10006, kind_10006, parseRelayURL, sys.BlockedRelayListCache)
	return ml
}

func (sys *System) FetchSearchRelayList(ctx context.Context, pubkey nostr.PubKey) GenericList[string, RelayURL] {
	sys.searchRelayListCacheOnce.Do(func() {
		if sys.SearchRelayListCache == nil {
			sys.SearchRelayListCache = cache_memory.New[GenericList[string, RelayURL]](1000)
		}
	})

	ml, _ := fetchGenericList(sys, ctx, pubkey, 10007, kind_10007, parseRelayURL, sys.SearchRelayListCache)
	return ml
}

func (sys *System) FetchDMRelayList(ctx context.Context, pubkey nostr.PubKey) GenericList[string, RelayURL] {
	sys.dmRelayListCacheOnce.Do(func() {
		if sys.DMRelayListCache == nil {
			sys.DMRelayListCache = cache_memory.New[GenericList[string, RelayURL]](1000)
		}
	})

	ml, _ := fetchGenericList(sys, ctx, pubkey, 10050, kind_10050, parseRelayURL, sys.DMRelayListCache)
	return ml
}

func (sys *System) FetchGoodWikiRelayList(ctx context.Context, pubkey nostr.PubKey) GenericList[string, RelayURL] {
	sys.goodWikiRelayListCacheOnce.Do(func() {
		if sys.GoodWikiRelayListCache == nil {
			sys.GoodWikiRelayListCache = cache_memory.New[GenericList[string, RelayURL]](1000)
		}
	})

	ml, _ := fetchGenericList(sys, ctx, pubkey, 10102, kind_10102, parseRelayURL, sys.GoodWikiRelayListCache)
	return ml
}

func (sys *System) FetchRelayFeedsList(ctx context.Context, pubkey nostr.PubKey) GenericList[string, RelayURL] {
	sys.relayFeedsListCacheOnce.Do(func() {
		if sys.RelayFeedsListCache == nil {
			sys.RelayFeedsListCache = cache_memory.New[GenericList[string, RelayURL]](1000)
		}
	})

	ml, _ := fetchGenericList(sys, ctx, pubkey, 10012, kind_10012, parseRelayURL, sys.RelayFeedsListCache)
	return ml
}

func parseRelayFromKind10002(tag nostr.Tag) (rl Relay, ok bool) {
	if len(tag) < 2 {
		return rl, false
	}

	if u := tag[1]; u != "" && tag[0] == "r" {
		if !nostr.IsValidRelayURL(u) {
			return rl, false
		}
		u := nostr.NormalizeURL(u)

		relay := Relay{
			URL: u,
		}

		if len(tag) == 2 {
			relay.Inbox = true
			relay.Outbox = true
		} else if tag[2] == "write" {
			relay.Outbox = true
		} else if tag[2] == "read" {
			relay.Inbox = true
		}

		return relay, true
	}

	return rl, false
}

func parseRelayURL(tag nostr.Tag) (rl RelayURL, ok bool) {
	if len(tag) < 2 {
		return rl, false
	}

	if u := tag[1]; u != "" && tag[0] == "relay" {
		if !nostr.IsValidRelayURL(u) {
			return rl, false
		}
		u := nostr.NormalizeURL(u)
		return RelayURL(u), true
	}

	return rl, false
}

// -- topic-based list items

type Topic string

func (r Topic) Value() string { return string(r) }

func (sys *System) FetchTopicList(ctx context.Context, pubkey nostr.PubKey) GenericList[string, Topic] {
	sys.topicListCacheOnce.Do(func() {
		if sys.TopicListCache == nil {
			sys.TopicListCache = cache_memory.New[GenericList[string, Topic]](1000)
		}
	})

	ml, _ := fetchGenericList(sys, ctx, pubkey, 10015, kind_10015, parseTopicString, sys.TopicListCache)
	return ml
}

func parseTopicString(tag nostr.Tag) (t Topic, ok bool) {
	if len(tag) < 2 {
		return t, false
	}
	if t := tag[1]; t != "" && tag[0] == "t" {
		return Topic(t), true
	}

	return t, false
}

// -- other list items

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

// -- emoji-based list items

type Emoji struct {
	Shortcode string
	ImageURL  string
}

func (e Emoji) Value() string { return e.Shortcode }

func (sys *System) FetchEmojiList(ctx context.Context, pubkey nostr.PubKey) GenericList[string, Emoji] {
	sys.emojiListCacheOnce.Do(func() {
		if sys.EmojiListCache == nil {
			sys.EmojiListCache = cache_memory.New[GenericList[string, Emoji]](1000)
		}
	})

	fl, _ := fetchGenericList(sys, ctx, pubkey, 10030, kind_10030, parseEmojiTag, sys.EmojiListCache)
	return fl
}

func parseEmojiTag(tag nostr.Tag) (Emoji, bool) {
	if len(tag) < 3 {
		return Emoji{}, false
	}
	if tag[0] != "emoji" {
		return Emoji{}, false
	}
	return Emoji{Shortcode: tag[1], ImageURL: tag[2]}, true
}

// -- podcast-based list items

type PodcastRef struct {
	PubKey nostr.PubKey
	URL    string
}

func (p PodcastRef) Value() string {
	if p.PubKey != (nostr.PubKey{}) {
		return p.PubKey.String()
	}
	return p.URL
}

func (sys *System) FetchFavoritePodcastsList(ctx context.Context, pubkey nostr.PubKey) GenericList[string, PodcastRef] {
	sys.podcastFavoriteListCacheOnce.Do(func() {
		if sys.PodcastFavoriteListCache == nil {
			sys.PodcastFavoriteListCache = cache_memory.New[GenericList[string, PodcastRef]](1000)
		}
	})

	fl, _ := fetchGenericList(sys, ctx, pubkey, 10054, kind_10054, parsePodcastRef, sys.PodcastFavoriteListCache)
	return fl
}

func (sys *System) FetchAuthoredPodcastsList(ctx context.Context, pubkey nostr.PubKey) GenericList[string, PodcastRef] {
	sys.authoredPodcastListCacheOnce.Do(func() {
		if sys.AuthoredPodcastListCache == nil {
			sys.AuthoredPodcastListCache = cache_memory.New[GenericList[string, PodcastRef]](1000)
		}
	})

	fl, _ := fetchGenericList(sys, ctx, pubkey, 10064, kind_10064, parsePodcastRef, sys.AuthoredPodcastListCache)
	return fl
}

func parsePodcastRef(tag nostr.Tag) (PodcastRef, bool) {
	if len(tag) < 2 {
		return PodcastRef{}, false
	}
	switch tag[0] {
	case "p":
		pubkey, err := nostr.PubKeyFromHex(tag[1])
		if err != nil {
			return PodcastRef{}, false
		}
		return PodcastRef{PubKey: pubkey}, true
	case "url":
		return PodcastRef{URL: tag[1]}, true
	}
	return PodcastRef{}, false
}
