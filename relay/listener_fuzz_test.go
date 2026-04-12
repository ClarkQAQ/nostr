package relay

import (
	"math/rand"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/stretchr/testify/require"
)

func FuzzRandomListenerClientRemoving(f *testing.F) {
	f.Add(uint(20), uint(20), uint(1))
	f.Fuzz(func(t *testing.T, utw uint, ubs uint, ualf uint) {
		totalWebsockets := int(utw)
		baseSubs := int(ubs)
		addListenerFreq := int(ualf) + 1

		rl := NewRelay()

		f := nostr.Filter{Kinds: []nostr.Kind{1}}
		cancel := func(cause error) {}

		websockets := make([]*WebSocket, 0, totalWebsockets*baseSubs)

		l := 0

		for i := 0; i < totalWebsockets; i++ {
			ws := &WebSocket{Context: rl.ctx}
			websockets = append(websockets, ws)
			rl.clients[ws] = nil
		}

		s := 0
		for j := 0; j < baseSubs; j++ {
			for i := 0; i < totalWebsockets; i++ {
				ws := websockets[i]
				w := idFromSeqUpper(i)

				if s%addListenerFreq == 0 {
					l++
					rl.addListener(ws, w+":"+idFromSeqLower(j), f, cancel)
				}

				s++
			}
		}

		require.Len(t, rl.clients, totalWebsockets)
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
	})
}

func FuzzRandomListenerIdRemoving(f *testing.F) {
	f.Add(uint(20), uint(20), uint(1), uint(4))
	f.Fuzz(func(t *testing.T, utw uint, ubs uint, ualf uint, ualef uint) {
		totalWebsockets := int(utw)
		baseSubs := int(ubs)
		addListenerFreq := int(ualf) + 1
		addExtraListenerFreq := int(ualef) + 1

		if totalWebsockets > 1024 || baseSubs > 1024 {
			return
		}

		rl := NewRelay()

		f := nostr.Filter{Kinds: []nostr.Kind{1}}
		cancel := func(cause error) {}
		websockets := make([]*WebSocket, 0, totalWebsockets)

		type wsid struct {
			ws *WebSocket
			id string
		}

		subs := make([]wsid, 0, totalWebsockets*baseSubs)
		extra := 0

		for i := 0; i < totalWebsockets; i++ {
			ws := &WebSocket{Context: rl.ctx}
			websockets = append(websockets, ws)
			rl.clients[ws] = nil
		}

		s := 0
		for j := 0; j < baseSubs; j++ {
			for i := 0; i < totalWebsockets; i++ {
				ws := websockets[i]
				w := idFromSeqUpper(i)

				if s%addListenerFreq == 0 {
					id := w + ":" + idFromSeqLower(j)
					rl.addListener(ws, id, f, cancel)
					subs = append(subs, wsid{ws, id})

					if s%addExtraListenerFreq == 0 {
						rl.addListener(ws, id, f, cancel)
						extra++
					}
				}

				s++
			}
		}

		require.Len(t, rl.clients, totalWebsockets)
		ssidCount := 0
		for _, specs := range rl.clients {
			ssidCount += len(specs)
		}
		require.Equal(t, len(subs)+extra, ssidCount)

		rand.Shuffle(len(subs), func(i, j int) {
			subs[i], subs[j] = subs[j], subs[i]
		})
		for _, wsidToRemove := range subs {
			rl.removeListenerId(wsidToRemove.ws, wsidToRemove.id)
		}

		ssidCount = 0
		for _, specs := range rl.clients {
			ssidCount += len(specs)
		}
		require.Equal(t, 0, ssidCount)
		require.Len(t, rl.clients, totalWebsockets)
		for _, specs := range rl.clients {
			require.Len(t, specs, 0)
		}
	})
}

func FuzzRouterListenersPabloCrash(f *testing.F) {
	f.Add(uint(6), uint(2), uint(20))
	f.Fuzz(func(t *testing.T, totalConns uint, subFreq uint, subIterations uint) {
		totalConns++
		subFreq++
		subIterations++

		rl := NewRelay()

		conns := make([]*WebSocket, int(totalConns))
		for i := 0; i < int(totalConns); i++ {
			ws := &WebSocket{Context: rl.ctx}
			conns[i] = ws
			rl.clients[ws] = make([]listenerSpec, 0, subIterations)
		}

		f := nostr.Filter{Kinds: []nostr.Kind{1}}
		cancel := func(cause error) {}

		type wsid struct {
			ws *WebSocket
			id string
		}

		s := 0
		subs := make([]wsid, 0, subIterations*totalConns)
		for i, conn := range conns {
			w := idFromSeqUpper(i)
			for j := 0; j < int(subIterations); j++ {
				id := w + ":" + idFromSeqLower(j)
				if s%int(subFreq) == 0 {
					rl.addListener(conn, id, f, cancel)
					subs = append(subs, wsid{conn, id})
				}
				s++
			}
		}

		for _, wsid := range subs {
			rl.removeListenerId(wsid.ws, wsid.id)
		}

		for _, wsid := range subs {
			require.Len(t, rl.clients[wsid.ws], 0)
		}
	})
}
