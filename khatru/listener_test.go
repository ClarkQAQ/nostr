package khatru

import (
	"strings"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/stretchr/testify/require"
)

func idFromSeqUpper(seq int) string { return idFromSeq(seq, 65, 90) }
func idFromSeqLower(seq int) string { return idFromSeq(seq, 97, 122) }
func idFromSeq(seq int, min, max int) string {
	maxSeq := max - min + 1
	nLetters := seq/maxSeq + 1
	result := strings.Builder{}
	result.Grow(nLetters)
	for l := 0; l < nLetters; l++ {
		letter := rune(seq%maxSeq + min)
		result.WriteRune(letter)
	}
	return result.String()
}

func moduloOrder[T any](items []T, seed int) []T {
	remaining := append([]T(nil), items...)
	ordered := make([]T, 0, len(items))
	for len(remaining) > 0 {
		idx := seed % len(remaining)
		ordered = append(ordered, remaining[idx])
		remaining = append(remaining[:idx], remaining[idx+1:]...)
		seed++
	}
	return ordered
}

func TestListenerSetupAndRemoveOnce(t *testing.T) {
	rl := NewRelay()

	ws1 := &WebSocket{Context: rl.ctx}
	ws2 := &WebSocket{Context: rl.ctx}

	f1 := nostr.Filter{Kinds: []nostr.Kind{1}}
	f2 := nostr.Filter{Kinds: []nostr.Kind{2}}
	f3 := nostr.Filter{Kinds: []nostr.Kind{3}}

	rl.clients[ws1] = nil
	rl.clients[ws2] = nil

	var cancel func(cause error) = nil

	t.Run("adding listeners", func(t *testing.T) {
		rl.addListener(ws1, "1a", f1, cancel)
		rl.addListener(ws1, "1b", f2, cancel)
		rl.addListener(ws2, "2a", f3, cancel)
		rl.addListener(ws1, "1c", f3, cancel)

		require.Equal(t, map[*WebSocket][]listenerSpec{
			ws1: {
				{1, "1a", cancel},
				{2, "1b", cancel},
				{4, "1c", cancel},
			},
			ws2: {
				{3, "2a", cancel},
			},
		}, rl.clients)
	})

	t.Run("removing a client", func(t *testing.T) {
		rl.removeClientAndListeners(ws1)

		require.Equal(t, map[*WebSocket][]listenerSpec{
			ws2: {
				{3, "2a", cancel},
			},
		}, rl.clients)
	})
}

func TestListenerMoreConvolutedCase(t *testing.T) {
	rl := NewRelay()

	ws1 := &WebSocket{Context: rl.ctx}
	ws2 := &WebSocket{Context: rl.ctx}
	ws3 := &WebSocket{Context: rl.ctx}
	ws4 := &WebSocket{Context: rl.ctx}

	f1 := nostr.Filter{Kinds: []nostr.Kind{1}}
	f2 := nostr.Filter{Kinds: []nostr.Kind{2}}
	f3 := nostr.Filter{Kinds: []nostr.Kind{3}}

	rl.clients[ws1] = nil
	rl.clients[ws2] = nil
	rl.clients[ws3] = nil
	rl.clients[ws4] = nil

	var cancel func(cause error) = nil

	t.Run("adding listeners", func(t *testing.T) {
		rl.addListener(ws1, "c", f1, cancel)
		rl.addListener(ws2, "b", f2, cancel)
		rl.addListener(ws3, "a", f3, cancel)
		rl.addListener(ws4, "d", f3, cancel)
		rl.addListener(ws2, "b", f1, cancel)

		require.Equal(t, map[*WebSocket][]listenerSpec{
			ws1: {
				{1, "c", cancel},
			},
			ws2: {
				{2, "b", cancel},
				{5, "b", cancel},
			},
			ws3: {
				{3, "a", cancel},
			},
			ws4: {
				{4, "d", cancel},
			},
		}, rl.clients)
	})

	t.Run("removing a client", func(t *testing.T) {
		rl.removeClientAndListeners(ws2)

		require.Equal(t, map[*WebSocket][]listenerSpec{
			ws1: {
				{1, "c", cancel},
			},
			ws3: {
				{3, "a", cancel},
			},
			ws4: {
				{4, "d", cancel},
			},
		}, rl.clients)
	})

	t.Run("reorganize the first case differently and then remove again", func(t *testing.T) {
		rl.clients = map[*WebSocket][]listenerSpec{
			ws1: {
				{2, "c", cancel},
			},
			ws2: {
				{3, "b", cancel},
				{5, "b", cancel},
			},
			ws3: {
				{1, "a", cancel},
			},
			ws4: {
				{4, "d", cancel},
			},
		}

		rl.removeClientAndListeners(ws2)

		require.Equal(t, map[*WebSocket][]listenerSpec{
			ws1: {
				{2, "c", cancel},
			},
			ws3: {
				{1, "a", cancel},
			},
			ws4: {
				{4, "d", cancel},
			},
		}, rl.clients)
	})
}

func TestListenerMoreStuffWithMultipleRelays(t *testing.T) {
	rl := NewRelay()

	ws1 := &WebSocket{Context: rl.ctx}
	ws2 := &WebSocket{Context: rl.ctx}
	ws3 := &WebSocket{Context: rl.ctx}
	ws4 := &WebSocket{Context: rl.ctx}

	f1 := nostr.Filter{Kinds: []nostr.Kind{1}}
	f2 := nostr.Filter{Kinds: []nostr.Kind{2}}
	f3 := nostr.Filter{Kinds: []nostr.Kind{3}}

	rl.clients[ws1] = nil
	rl.clients[ws2] = nil
	rl.clients[ws3] = nil
	rl.clients[ws4] = nil

	var cancel func(cause error) = nil

	t.Run("adding listeners", func(t *testing.T) {
		rl.addListener(ws1, "c", f1, cancel)
		rl.addListener(ws2, "b", f2, cancel)
		rl.addListener(ws3, "a", f3, cancel)
		rl.addListener(ws4, "d", f3, cancel)
		rl.addListener(ws4, "e", f3, cancel)
		rl.addListener(ws3, "a", f3, cancel)
		rl.addListener(ws4, "e", f3, cancel)
		rl.addListener(ws3, "f", f3, cancel)
		rl.addListener(ws1, "g", f1, cancel)
		rl.addListener(ws2, "g", f2, cancel)

		require.Equal(t, map[*WebSocket][]listenerSpec{
			ws1: {
				{1, "c", cancel},
				{9, "g", cancel},
			},
			ws2: {
				{2, "b", cancel},
				{10, "g", cancel},
			},
			ws3: {
				{3, "a", cancel},
				{6, "a", cancel},
				{8, "f", cancel},
			},
			ws4: {
				{4, "d", cancel},
				{5, "e", cancel},
				{7, "e", cancel},
			},
		}, rl.clients)
	})

	t.Run("removing a subscription id", func(t *testing.T) {
		// removing 'd' from ws4
		rl.clients[ws4][0].cancel = func(cause error) {} // set since removing will call it
		rl.removeListenerId(ws4, "d")

		require.Equal(t, map[*WebSocket][]listenerSpec{
			ws1: {
				{1, "c", cancel},
				{9, "g", cancel},
			},
			ws2: {
				{2, "b", cancel},
				{10, "g", cancel},
			},
			ws3: {
				{3, "a", cancel},
				{6, "a", cancel},
				{8, "f", cancel},
			},
			ws4: {
				{5, "e", cancel},
				{7, "e", cancel},
			},
		}, rl.clients)
	})

	t.Run("removing another subscription id", func(t *testing.T) {
		// removing 'a' from ws3
		rl.clients[ws3][0].cancel = func(cause error) {} // set since removing will call it
		rl.clients[ws3][1].cancel = func(cause error) {} // set since removing will call it
		rl.removeListenerId(ws3, "a")

		require.Equal(t, map[*WebSocket][]listenerSpec{
			ws1: {
				{1, "c", cancel},
				{9, "g", cancel},
			},
			ws2: {
				{2, "b", cancel},
				{10, "g", cancel},
			},
			ws3: {
				{8, "f", cancel},
			},
			ws4: {
				{5, "e", cancel},
				{7, "e", cancel},
			},
		}, rl.clients)
	})

	t.Run("removing a connection", func(t *testing.T) {
		rl.removeClientAndListeners(ws2)

		require.Equal(t, map[*WebSocket][]listenerSpec{
			ws1: {
				{1, "c", cancel},
				{9, "g", cancel},
			},
			ws3: {
				{8, "f", cancel},
			},
			ws4: {
				{5, "e", cancel},
				{7, "e", cancel},
			},
		}, rl.clients)
	})

	t.Run("removing another subscription id", func(t *testing.T) {
		// removing 'e' from ws4
		rl.clients[ws4][0].cancel = func(cause error) {} // set since removing will call it
		rl.clients[ws4][1].cancel = func(cause error) {} // set since removing will call it
		rl.removeListenerId(ws4, "e")

		require.Equal(t, map[*WebSocket][]listenerSpec{
			ws1: {
				{1, "c", cancel},
				{9, "g", cancel},
			},
			ws3: {
				{8, "f", cancel},
			},
			ws4: {},
		}, rl.clients)
	})
}

func TestRandomListenerClientRemoving(t *testing.T) {
	rl := NewRelay()

	f := nostr.Filter{Kinds: []nostr.Kind{1}}
	cancel := func(cause error) {}

	websockets := make([]*WebSocket, 0, 20)

	l := 0

	for i := 0; i < 20; i++ {
		ws := &WebSocket{Context: rl.ctx}
		websockets = append(websockets, ws)
		rl.clients[ws] = nil
	}

	for j := 0; j < 20; j++ {
		for i := 0; i < 20; i++ {
			ws := websockets[i]
			w := idFromSeqUpper(i)

			if (i+j)%2 == 0 {
				l++
				rl.addListener(ws, w+":"+idFromSeqLower(j), f, cancel)
			}
		}
	}

	require.Len(t, rl.clients, 20)
	ssidCount := 0
	for _, specs := range rl.clients {
		ssidCount += len(specs)
	}
	require.Equal(t, l, ssidCount)

	for ws := range rl.clients {
		rl.removeClientAndListeners(ws)
	}

	require.Len(t, rl.clients, 0)
	ssidCount = 0
	for _, specs := range rl.clients {
		ssidCount += len(specs)
	}
	require.Equal(t, 0, ssidCount)
}

func TestRandomListenerIdRemoving(t *testing.T) {
	rl := NewRelay()

	f := nostr.Filter{Kinds: []nostr.Kind{1}}
	cancel := func(cause error) {}

	websockets := make([]*WebSocket, 0, 20)

	type wsid struct {
		ws *WebSocket
		id string
	}

	subs := make([]wsid, 0, 20*20)
	extra := 0

	for i := 0; i < 20; i++ {
		ws := &WebSocket{Context: rl.ctx}
		websockets = append(websockets, ws)
		rl.clients[ws] = nil
	}

	for j := 0; j < 20; j++ {
		for i := 0; i < 20; i++ {
			ws := websockets[i]
			w := idFromSeqUpper(i)

			if (i+j)%2 == 0 {
				id := w + ":" + idFromSeqLower(j)
				rl.addListener(ws, id, f, cancel)
				subs = append(subs, wsid{ws, id})

				if (i+j)%5 == 0 {
					rl.addListener(ws, id, f, cancel)
					extra++
				}
			}
		}
	}

	require.Len(t, rl.clients, 20)
	ssidCount := 0
	for _, specs := range rl.clients {
		ssidCount += len(specs)
	}
	require.Equal(t, len(subs)+extra, ssidCount)

	for _, wsidToRemove := range moduloOrder(subs, 20) {
		rl.removeListenerId(wsidToRemove.ws, wsidToRemove.id)
	}

	ssidCount = 0
	for _, specs := range rl.clients {
		ssidCount += len(specs)
	}
	require.Equal(t, 0, ssidCount)
	require.Len(t, rl.clients, 20)
	for _, specs := range rl.clients {
		require.Len(t, specs, 0)
	}
}

func TestRouterListenersPabloCrash(t *testing.T) {
	rl := NewRelay()

	ws1 := &WebSocket{Context: rl.ctx}
	ws2 := &WebSocket{Context: rl.ctx}
	ws3 := &WebSocket{Context: rl.ctx}

	rl.clients[ws1] = nil
	rl.clients[ws2] = nil
	rl.clients[ws3] = nil

	f := nostr.Filter{Kinds: []nostr.Kind{1}}
	cancel := func(cause error) {}

	rl.addListener(ws1, ":1", f, cancel)
	rl.addListener(ws2, ":1", f, cancel)
	rl.addListener(ws3, "a", f, cancel)
	rl.addListener(ws3, "b", f, cancel)
	rl.addListener(ws3, "c", f, cancel)

	rl.removeClientAndListeners(ws1)
	rl.removeClientAndListeners(ws3)
}
