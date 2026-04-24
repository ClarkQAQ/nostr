package sdk

import (
	"context"
	"encoding/binary"
	"sync"
	"time"

	"fiatjaf.com/nostr"
	"github.com/FastFilter/xorfilter"
	"golang.org/x/sync/errgroup"
)

func PubKeyToShid(pubkey nostr.PubKey) uint64 {
	return binary.BigEndian.Uint64(pubkey[16:24])
}

type wotCall struct {
	id          uint64 // basically the pubkey we're targeting here
	mutex       sync.Mutex
	resultbacks []chan WotXorFilter // all callers waiting for results
	done        chan struct{}       // this is closed when this call is fully resolved and deleted
}

const wotCallsSize = 16

var (
	wotCallsMutex   sync.Mutex
	wotCallsInPlace [wotCallsSize]*wotCall
)

func (sys *System) LoadWoTFilter(ctx context.Context, pubkey nostr.PubKey) WotXorFilter {
	id := PubKeyToShid(pubkey)
	pos := int(id % wotCallsSize)

start:
	wotCallsMutex.Lock()
	wc := wotCallsInPlace[pos]
	if wc == nil {
		// we are the first to call at this position
		wc = &wotCall{
			id:          id,
			resultbacks: make([]chan WotXorFilter, 0),
			done:        make(chan struct{}),
		}
		wotCallsInPlace[pos] = wc
		wotCallsMutex.Unlock()
		goto actualcall
	} else {
		wotCallsMutex.Unlock()
	}

	wc.mutex.Lock()
	if wc.id == id {
		// there is already a call for this exact pubkey ongoing, so we just wait and copy the results
		resch := make(chan WotXorFilter)
		wc.resultbacks = append(wc.resultbacks, resch)
		wc.mutex.Unlock()
		select {
		case res := <-resch:
			return res
		}
	} else {
		wc.mutex.Unlock()
		// there is already a call in this place, but it's for a different pubkey, so wait
		<-wc.done
		// when it's done restart
		goto start
	}

actualcall:
	var res WotXorFilter
	m := sys.loadWoT(ctx, pubkey)
	res = makeWoTFilter(m)
	wc.mutex.Lock()
	for _, ch := range wc.resultbacks {
		ch <- res
	}

	wotCallsMutex.Lock()
	wotCallsInPlace[pos] = nil
	wc.mutex.Unlock()
	close(wc.done)
	wotCallsMutex.Unlock()

	return res
}

func (sys *System) loadWoT(ctx context.Context, pubkey nostr.PubKey) chan nostr.PubKey {
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(45)

	res := make(chan nostr.PubKey)

	go func() {
		for _, f := range sys.FetchFollowList(ctx, pubkey).Items {
			g.Go(func() error {
				res <- f.Pubkey

				ctx, cancel := context.WithTimeout(ctx, time.Second*7)
				defer cancel()

				ff := sys.FetchFollowList(ctx, f.Pubkey).Items
				for _, f2 := range ff {
					res <- f2.Pubkey
				}
				return nil
			})
		}
	}()

	go func() {
		g.Wait()
		close(res)
	}()

	return res
}

func makeWoTFilter(m chan nostr.PubKey) WotXorFilter {
	shids := make([]uint64, 0, 60000)
	shidMap := make(map[uint64]struct{}, 60000)
	for pk := range m {
		shid := PubKeyToShid(pk)
		if _, alreadyAdded := shidMap[shid]; !alreadyAdded {
			shidMap[shid] = struct{}{}
			shids = append(shids, shid)
		}
	}

	if len(shids) == 0 {
		return WotXorFilter{}
	}

	xf, err := xorfilter.Populate(shids)
	if err != nil {
		nostr.InfoLogger.Println("failed to populate filter", len(shids), err)
		return WotXorFilter{}
	}
	return WotXorFilter{len(shids), *xf}
}

type WotXorFilter struct {
	Items int
	xorfilter.Xor8
}

func (wxf WotXorFilter) Contains(pubkey nostr.PubKey) bool {
	if wxf.Items == 0 {
		return false
	}
	return wxf.Xor8.Contains(PubKeyToShid(pubkey))
}
