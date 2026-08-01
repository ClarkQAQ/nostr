package relay

import (
	"context"
	"iter"
	"log"
	"net/http"
	"os"
	"time"

	"fiatjaf.com/lib/channelmutex"
	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore"
	"fiatjaf.com/nostr/nip11"
	"fiatjaf.com/nostr/nip45/hyperloglog"
	"github.com/fasthttp/websocket"
)

func NewRelay() *Relay {
	ctx, cancel := context.WithCancelCause(context.Background())

	rl := &Relay{
		ctx:    ctx,
		cancel: cancel,

		Log: log.New(os.Stderr, "[relay] ", log.LstdFlags),

		Info: &nip11.RelayInformationDocument{
			Software:      "https://pkg.go.dev/fiatjaf.com/nostr/relay",
			Version:       "n/a",
			SupportedNIPs: []int{1, 11, 42, 70, 86},
		},

		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin:     func(r *http.Request) bool { return true },
		},

		clients:      make(map[*WebSocket][]listenerSpec, 100),
		clientsMutex: channelmutex.New(),

		dispatcher: newDispatcher(),

		serveMux: &http.ServeMux{},

		WriteWait:      10 * time.Second,
		PongWait:       60 * time.Second,
		PingPeriod:     30 * time.Second,
		MaxMessageSize: 512000,

		MaxAuthenticatedClients: 8,
	}

	return rl
}

type Relay struct {
	ctx    context.Context
	cancel context.CancelCauseFunc

	// hooks that will be called at various times
	OnEvent                   func(ctx context.Context, event nostr.Event) (reject bool, msg string)
	StoreEvent                func(ctx context.Context, event nostr.Event) error
	ReplaceEvent              func(ctx context.Context, event nostr.Event) ([]nostr.Event, error)
	DeleteEvent               func(ctx context.Context, id nostr.ID) error
	OnEventSaved              func(ctx context.Context, event nostr.Event)
	OnEventDeleted            func(ctx context.Context, deleted nostr.Event)
	OnEphemeralEvent          func(ctx context.Context, event nostr.Event)
	OnRequest                 func(ctx context.Context, filter nostr.Filter) (reject bool, msg string)
	OnCount                   func(ctx context.Context, filter nostr.Filter) (reject bool, msg string)
	QueryStored               func(ctx context.Context, filter nostr.Filter) iter.Seq2[nostr.Event, error]
	Count                     func(ctx context.Context, filter nostr.Filter) (int64, error)
	CountHLL                  func(ctx context.Context, filter nostr.Filter, offset int) (int64, *hyperloglog.HyperLogLog, error)
	RejectConnection          func(r *http.Request) bool
	OnConnect                 func(ctx context.Context)
	OnDisconnect              func(ctx context.Context)
	OverwriteRelayInformation func(ctx context.Context, r *http.Request, info nip11.RelayInformationDocument) nip11.RelayInformationDocument
	PreventBroadcast          func(ws *WebSocket, filter nostr.Filter, event nostr.Event) bool

	// this can be ignored unless you know what you're doing
	ChallengePrefix string

	// setting up handlers here will enable these methods
	ManagementAPI RelayManagementAPI

	// editing info will affect the NIP-11 responses
	Info *nip11.RelayInformationDocument

	// Default logger, as set by NewServer, is a stdlib logger prefixed with "[relay] ",
	// outputting to stderr.
	Log *log.Logger

	// for establishing websockets
	upgrader websocket.Upgrader

	// keep a connection reference to all connected clients for Server.Shutdown
	// also used for keeping track of who is listening to what
	clients      map[*WebSocket][]listenerSpec
	dispatcher   dispatcher
	clientsMutex *channelmutex.Mutex

	// set this to true to support negentropy
	Negentropy bool

	// in case you call Server.Start
	Addr       string
	serveMux   *http.ServeMux
	httpServer *http.Server

	// websocket options
	WriteWait                 time.Duration // Time allowed to write a message to the peer.
	PongWait                  time.Duration // Time allowed to read the next pong message from the peer.
	PingPeriod                time.Duration // Send pings to peer with this period. Must be less than pongWait.
	MaxMessageSize            int64         // Maximum message size allowed from peer.
	MaxSubscriptionsPerClient int           // Maximum number of subscriptions per client
	MaxSubidLength            int           // Maximum size of a subscription id
	MaxAuthenticatedClients   int

	// NIP-40 expiration manager
	expirationManager *expirationManager
}

// UseEventstore hooks up an eventstore.Store into the relay in the default way.
// It should be used in 85% of the cases, when you don't want to do any complicated scheme with your event storage.
//
// maxQueryLimit is the default max limit to be enforced when querying events, to prevent users for downloading way
// too much, setting it to something like 500 or 1000 should be ok in most cases.
func (rl *Relay) UseEventstore(store eventstore.Store, maxQueryLimit int) {
	rl.QueryStored = func(ctx context.Context, filter nostr.Filter) iter.Seq2[nostr.Event, error] {
		maxLimit := maxQueryLimit
		if IsNegentropySession(ctx) {
			maxLimit = maxQueryLimit * 20
		}

		return store.QueryEvents(ctx, filter, maxLimit)
	}
	rl.Count = func(ctx context.Context, filter nostr.Filter) (int64, error) {
		return store.CountEvents(ctx, filter)
	}
	rl.StoreEvent = func(ctx context.Context, event nostr.Event) error {
		return store.SaveEvent(ctx, event)
	}
	rl.ReplaceEvent = func(ctx context.Context, event nostr.Event) ([]nostr.Event, error) {
		return store.ReplaceEvent(ctx, event)
	}
	rl.DeleteEvent = func(ctx context.Context, id nostr.ID) error {
		return store.DeleteEvent(ctx, id)
	}

	// only when using the eventstore we automatically set up the expiration manager
	rl.StartExpirationManager(func(ctx context.Context, filter nostr.Filter) iter.Seq2[nostr.Event, error] {
		return rl.QueryStored(ctx, filter)
	}, func(ctx context.Context, id nostr.ID) error {
		return rl.DeleteEvent(ctx, id)
	}, func(ctx context.Context, evt nostr.Event) {
		if rl.OnEventDeleted != nil {
			rl.OnEventDeleted(ctx, evt)
		}
	})
}

// Stats returns the current number of connected clients and open listeners.
func (rl *Relay) Stats() (clients, listeners int) {
	rl.clientsMutex.Lock()
	defer rl.clientsMutex.Unlock()

	for _, specs := range rl.clients {
		listeners += len(specs)
	}

	return len(rl.clients), listeners
}

func (rl *Relay) Router() *http.ServeMux {
	return rl.serveMux
}

func (rl *Relay) SetRouter(mux *http.ServeMux) {
	rl.serveMux = mux
}
