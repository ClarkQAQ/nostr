package nostr

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"iter"
	"log"
	"math"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	ws "github.com/coder/websocket"
	"github.com/puzpuzpuz/xsync/v3"
)

var subscriptionIDCounter atomic.Int64

// Relay represents a connection to a Nostr relay.
type Relay struct {
	closeMutex sync.Mutex

	URL           string
	requestHeader http.Header // e.g. for origin header

	// websocket connection
	conn         *ws.Conn
	writeQueue   chan writeRequest
	closed       *atomic.Bool
	closedNotify chan struct{}

	Subscriptions *xsync.MapOf[int64, *Subscription]

	ConnectionError         error
	connectionContext       context.Context // will be canceled when the connection closes
	connectionContextCancel context.CancelCauseFunc

	challenge                     string       // NIP-42 challenge, we only keep the last
	noticeHandler                 func(string) // NIP-01 NOTICEs
	customHandler                 func(string) // nonstandard unparseable messages
	okCallbacks                   map[ID]okcallback
	okCallbacksMutex              sync.Mutex
	subscriptionChannelCloseQueue chan *Subscription

	// custom things that aren't often used
	//
	AssumeValid bool // this will skip verifying signatures for events received from this relay
}

// NewRelay returns a new relay. It takes a context that, when canceled, will close the relay connection.
func NewRelay(ctx context.Context, url string, opts RelayOptions) *Relay {
	ctx, cancel := context.WithCancelCause(ctx)
	r := &Relay{
		URL:                           NormalizeURL(url),
		connectionContext:             ctx,
		connectionContextCancel:       cancel,
		Subscriptions:                 xsync.NewMapOf[int64, *Subscription](),
		okCallbacks:                   make(map[ID]okcallback, 20),
		subscriptionChannelCloseQueue: make(chan *Subscription),
		requestHeader:                 opts.RequestHeader,
		customHandler:                 opts.CustomHandler,
		noticeHandler:                 opts.NoticeHandler,
	}

	return r
}

// RelayConnect returns a relay object connected to url.
//
// The given subscription is only used during the connection phase. Once successfully connected, cancelling ctx has no effect.
//
// The ongoing relay connection uses a background context. To close the connection, call r.Close().
// If you need fine grained long-term connection contexts, use NewRelay() instead.
func RelayConnect(ctx context.Context, url string, opts RelayOptions) (*Relay, error) {
	r := NewRelay(context.Background(), url, opts)
	err := r.Connect(ctx)
	return r, err
}

type RelayOptions struct {
	// NoticeHandler just takes notices and is expected to do something with them.
	// When not given defaults to logging the notices.
	NoticeHandler func(notice string)

	// CustomHandler, if given, must be a function that handles any relay message
	// that couldn't be parsed as a standard envelope.
	CustomHandler func(data string)

	// RequestHeader sets the HTTP request header of the websocket preflight request
	RequestHeader http.Header
}

// String just returns the relay URL.
func (r *Relay) String() string {
	return r.URL
}

// Context retrieves the context that is associated with this relay connection.
// It will be closed when the relay is disconnected.
func (r *Relay) Context() context.Context { return r.connectionContext }

// IsConnected returns true if the connection to this relay seems to be active.
func (r *Relay) IsConnected() bool { return !r.closed.Load() }

// Connect tries to establish a websocket connection to r.URL.
// If the context expires before the connection is complete, an error is returned.
// Once successfully connected, context expiration has no effect: call r.Close
// to close the connection.
//
// The given context here is only used during the connection phase. The long-living
// relay connection will be based on the context given to NewRelay().
func (r *Relay) Connect(ctx context.Context) error {
	return r.ConnectWithTLS(ctx, nil)
}

// ConnectWithTLS is like Connect(), but takes a special tls.Config if you need that.
func (r *Relay) ConnectWithTLS(ctx context.Context, tlsConfig *tls.Config) error {
	if r.connectionContext == nil || r.Subscriptions == nil {
		return fmt.Errorf("relay must be initialized with a call to NewRelay()")
	}

	if r.URL == "" {
		return fmt.Errorf("invalid relay URL '%s'", r.URL)
	}

	if err := r.newConnection(ctx, tlsConfig); err != nil {
		return fmt.Errorf("error opening websocket to '%s': %w", r.URL, err)
	}

	return nil
}

func (r *Relay) handleMessage(message string) {
	// if this is an "EVENT" we will have this preparser logic that should speed things up a little
	// as we skip handling duplicate events
	subid := extractSubID(message)
	sub, ok := r.Subscriptions.Load(subIdToSerial(subid))
	if ok {
		if sub.checkDuplicate != nil {
			if sub.checkDuplicate(extractEventID(message[10+len(subid):]), r.URL) {
				return
			}
		} else if sub.checkDuplicateReplaceable != nil {
			if sub.checkDuplicateReplaceable(
				ReplaceableKey{extractEventPubKey(message), extractDTag(message)},
				extractTimestamp(message),
			) {
				return
			}
		}
	}

	envelope, err := ParseMessage(message)
	if envelope == nil {
		if r.customHandler != nil && err == UnknownLabel {
			go r.customHandler(message)
		}
		return
	}

	switch env := envelope.(type) {
	case *NoticeEnvelope:
		// see WithNoticeHandler
		if r.noticeHandler != nil {
			r.noticeHandler(string(*env))
		} else {
			log.Printf("NOTICE from %s: '%s'\n", r.URL, string(*env))
		}
	case *AuthEnvelope:
		if env.Challenge == nil {
			return
		}
		r.challenge = *env.Challenge
	case *EventEnvelope:
		// we already have the subscription from the pre-check above, so we can just reuse it
		if sub == nil {
			// InfoLogger.Printf("{%s} no subscription with id '%s'\n", r.URL, *env.SubscriptionID)
			return
		} else {
			// check if the event matches the desired filter, ignore otherwise
			if !sub.match(env.Event) {
				InfoLogger.Printf("{%s} filter does not match: %v ~ %v\n", r.URL, sub.Filter, env.Event)
				return
			}

			// check signature, ignore invalid, except from trusted (AssumeValid) relays
			if !r.AssumeValid {
				if !env.Event.VerifySignature() {
					InfoLogger.Printf("{%s} bad signature on %s\n", r.URL, env.Event.ID)
					return
				}
			}

			// dispatch this to the internal .events channel of the subscription
			sub.dispatchEvent(env.Event)
		}
	case *EOSEEnvelope:
		if subscription, ok := r.Subscriptions.Load(subIdToSerial(string(*env))); ok {
			subscription.dispatchEose()
		}
	case *ClosedEnvelope:
		if subscription, ok := r.Subscriptions.Load(subIdToSerial(env.SubscriptionID)); ok {
			subscription.handleClosed(env.Reason)
		}
	case *CountEnvelope:
		if subscription, ok := r.Subscriptions.Load(subIdToSerial(env.SubscriptionID)); ok && env.Count != nil && subscription.countResult != nil {
			subscription.countResult <- *env
		}
	case *OKEnvelope:
		r.okCallbacksMutex.Lock()
		if okCallback, exist := r.okCallbacks[env.EventID]; exist {
			okCallback(env.OK, env.Reason)
		} else {
			InfoLogger.Printf("{%s} got an unexpected OK message for event %s", r.URL, env.EventID)
		}
		r.okCallbacksMutex.Unlock()
	}
}

// Write queues an arbitrary message to be sent to the relay.
func (r *Relay) Write(msg []byte) {
	r.closeMutex.Lock()
	defer r.closeMutex.Unlock()
	select {
	case <-r.closedNotify:
		return
	default:
	}

	select {
	case <-r.connectionContext.Done():
	case r.writeQueue <- writeRequest{msg: msg, answer: nil}:
	}
}

// WriteWithError is like Write, but returns an error if the write fails (and the connection gets closed).
func (r *Relay) WriteWithError(msg []byte) error {
	ch := make(chan error)
	r.closeMutex.Lock()
	defer r.closeMutex.Unlock()
	select {
	case <-r.closedNotify:
		return fmt.Errorf("failed to write to %s: <closed>", r.URL)
	default:
	}
	select {
	case <-r.connectionContext.Done():
		return fmt.Errorf("failed to write to %s: %w", r.URL, context.Cause(r.connectionContext))
	case r.writeQueue <- writeRequest{msg: msg, answer: ch}:
	}
	return <-ch
}

// Publish sends an "EVENT" command to the relay r as in NIP-01 and waits for an OK response.
func (r *Relay) Publish(ctx context.Context, event Event) error {
	return r.publish(ctx, event.ID, &EventEnvelope{Event: event})
}

// Auth sends an "AUTH" command client->relay as in NIP-42 and waits for an OK response.
//
// You don't have to build the AUTH event yourself, this function takes a function to which the
// event that must be signed will be passed, so it's only necessary to sign that.
func (r *Relay) Auth(ctx context.Context, sign func(context.Context, *Event) error) error {
	authEvent := Event{
		CreatedAt: Now(),
		Kind:      KindClientAuthentication,
		Tags: Tags{
			Tag{"relay", r.URL},
			Tag{"challenge", r.challenge},
		},
		Content: "",
	}
	if err := sign(ctx, &authEvent); err != nil {
		return fmt.Errorf("error signing auth event: %w", err)
	}

	return r.publish(ctx, authEvent.ID, &AuthEnvelope{Event: authEvent})
}

// publish can be used both for EVENT and for AUTH
func (r *Relay) publish(ctx context.Context, id ID, env Envelope) error {
	var err error
	var cancel context.CancelFunc

	if _, ok := ctx.Deadline(); !ok {
		// if no timeout is set, force it to 7 seconds
		ctx, cancel = context.WithTimeoutCause(ctx, 7*time.Second, fmt.Errorf("given up waiting for an OK"))
		defer cancel()
	} else {
		// otherwise make the context cancellable so we can stop everything upon receiving an "OK"
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
	}

	// listen for an OK callback
	gotOk := make(chan bool, 1)
	handleOk := func(ok bool, reason string) {
		err = fmt.Errorf("msg: %s", reason)
		gotOk <- ok
	}

	r.okCallbacksMutex.Lock()
	if previous, exists := r.okCallbacks[id]; !exists {
		// normal path: there is nothing listening for this id, so we register this function
		r.okCallbacks[id] = func(ok bool, reason string) {
			handleOk(ok, reason)

			// and when it's called the mutex will be locked
			// so we just eliminate it
			delete(r.okCallbacks, id)
		}
	} else {
		// if the same event is published twice there will be something here already
		// so we make a function that concatenates both
		r.okCallbacks[id] = func(ok bool, reason string) {
			// we call this with an informative helper for the developer
			handleOk(ok, fmt.Sprintf("published twice: %s", reason))

			// then we replace it with the previous (and when this is called it will nuke itself accordingly)
			r.okCallbacks[id] = previous
		}
	}
	r.okCallbacksMutex.Unlock()

	// publish event
	envb, _ := env.MarshalJSON()
	if err := r.WriteWithError(envb); err != nil {
		return err
	}

	for {
		select {
		case ok := <-gotOk:
			if ok {
				return nil
			}
			return err
		case <-ctx.Done():
			r.okCallbacksMutex.Lock()
			if cb, _ := r.okCallbacks[id]; cb != nil {
				cb(false, "timeout")
			}
			r.okCallbacksMutex.Unlock()
			return fmt.Errorf("publish: %w", context.Cause(ctx))
		case <-r.connectionContext.Done():
			r.okCallbacks = make(map[ID]okcallback)
			return fmt.Errorf("relay: %w", context.Cause(r.connectionContext))
		}
	}
}

// Subscribe sends a "REQ" command to the relay r as in NIP-01.
// Events are returned through the channel sub.Events.
// The subscription is closed when context ctx is cancelled ("CLOSE" in NIP-01).
//
// Remember to cancel subscriptions, either by calling `.Unsub()` on them or ensuring their `context.Context` will be canceled at some point.
// Failure to do that will result in a huge number of halted goroutines being created.
func (r *Relay) Subscribe(ctx context.Context, filter Filter, opts SubscriptionOptions) (*Subscription, error) {
	sub := r.PrepareSubscription(ctx, filter, opts)

	if r.conn == nil {
		return nil, fmt.Errorf("not connected to %s", r.URL)
	}

	if err := sub.Fire(); err != nil {
		return nil, fmt.Errorf("couldn't subscribe to %v at %s: %w", filter, r.URL, err)
	}

	go func() {
		select {
		case <-r.closedNotify:
			sub.unsub(ErrDisconnected)
		case <-ctx.Done():
		}
	}()

	return sub, nil
}

// PrepareSubscription creates a subscription, but doesn't fire it.
//
// Remember to cancel subscriptions, either by calling `.Unsub()` on them or ensuring their `context.Context` will be canceled at some point.
// Failure to do that will result in a huge number of halted goroutines being created.
func (r *Relay) PrepareSubscription(ctx context.Context, filter Filter, opts SubscriptionOptions) *Subscription {
	current := subscriptionIDCounter.Add(1)
	ctx, cancel := context.WithCancelCause(ctx)

	sub := &Subscription{
		Relay:             r,
		Context:           ctx,
		cancel:            cancel,
		counter:           current,
		Events:            make(chan Event),
		EndOfStoredEvents: make(chan struct{}, 1),
		ClosedReason:      make(chan string, 1),
		Filter:            filter,
		match:             filter.Matches,
	}

	sub.checkDuplicate = opts.CheckDuplicate
	sub.checkDuplicateReplaceable = opts.CheckDuplicateReplaceable

	// subscription id computation
	buf := subIdPool.Get().([]byte)[:0]
	buf = strconv.AppendInt(buf, sub.counter, 10)
	buf = append(buf, ':')
	buf = append(buf, opts.Label...)
	defer subIdPool.Put(buf)
	sub.id = string(buf)

	// we track subscriptions only by their counter, no need for the full id
	r.Subscriptions.Store(int64(sub.counter), sub)

	// start counting down for dispatching the fake EOSE
	if opts.MaxWaitForEOSE != math.MaxInt64 {
		if opts.MaxWaitForEOSE == 0 {
			opts.MaxWaitForEOSE = time.Second * 7
		}

		go func() {
			time.Sleep(opts.MaxWaitForEOSE)
			sub.dispatchEose()
		}()
	}

	// start handling events, eose, unsub etc:
	go sub.start()

	return sub
}

// implement Querier interface
func (r *Relay) QueryEvents(filter Filter) iter.Seq[Event] {
	ctx, cancel := context.WithCancel(r.connectionContext)

	return func(yield func(Event) bool) {
		defer cancel()

		sub, err := r.Subscribe(ctx, filter, SubscriptionOptions{Label: "queryevents"})
		if err != nil {
			return
		}

		for {
			select {
			case evt := <-sub.Events:
				if !yield(evt) {
					return
				}
			case <-sub.EndOfStoredEvents:
				return
			case <-sub.ClosedReason:
				return
			case <-ctx.Done():
				return
			}
		}
	}
}

// Count sends a "COUNT" command to the relay and returns the count of events matching the filters.
func (r *Relay) Count(
	ctx context.Context,
	filter Filter,
	opts SubscriptionOptions,
) (uint32, []byte, error) {
	v, err := r.countInternal(ctx, filter, opts)
	if err != nil {
		return 0, nil, err
	}

	return *v.Count, v.HyperLogLog, nil
}

func (r *Relay) countInternal(ctx context.Context, filter Filter, opts SubscriptionOptions) (CountEnvelope, error) {
	sub := r.PrepareSubscription(ctx, filter, opts)
	sub.countResult = make(chan CountEnvelope)

	if err := sub.Fire(); err != nil {
		return CountEnvelope{}, err
	}

	defer sub.unsub(errors.New("countInternal() ended"))

	if _, ok := ctx.Deadline(); !ok {
		// if no timeout is set, force it to 7 seconds
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeoutCause(ctx, 7*time.Second, errors.New("countInternal took too long"))
		defer cancel()
	}

	for {
		select {
		case count := <-sub.countResult:
			return count, nil
		case <-ctx.Done():
			return CountEnvelope{}, ctx.Err()
		}
	}
}

// Close closes the relay connection.
func (r *Relay) Close() error {
	return r.close(errors.New("Close() called"))
}

func (r *Relay) close(reason error) error {
	r.closeMutex.Lock()
	defer r.closeMutex.Unlock()

	if r.connectionContextCancel == nil {
		return fmt.Errorf("relay already closed")
	}

	if r.conn == nil {
		return fmt.Errorf("relay not connected")
	}

	r.connectionContextCancel(reason)

	return nil
}

var subIdPool = sync.Pool{
	New: func() any { return make([]byte, 0, 15) },
}

type okcallback func(ok bool, reason string)
