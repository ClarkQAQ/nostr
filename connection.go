package nostr

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	ws "github.com/coder/websocket"
)

var ErrDisconnected = errors.New("<disconnected>")

// Connection represents a websocket connection to a Nostr relay.
type connection struct {
	conn         *ws.Conn
	cancel       context.CancelCauseFunc
	writeQueue   chan writeRequest
	closed       *atomic.Bool
	closedNotify chan struct{}
	closeMutex   sync.Mutex
}

type writeRequest struct {
	msg    []byte
	answer chan error
}

func newConnection(
	ctx context.Context,
	cancel context.CancelCauseFunc,
	url string,
	handleMessage func(string),
	requestHeader http.Header,
	tlsConfig *tls.Config,
) (*connection, error) {
	debugLogf("{%s} connecting!\n", url)

	dialCtx := ctx
	if _, ok := dialCtx.Deadline(); !ok {
		// if no timeout is set, force it to 7 seconds
		timeoutCtx, timeoutCancel := context.WithTimeoutCause(ctx, 7*time.Second, errors.New("connection took too long"))
		defer timeoutCancel()
		dialCtx = timeoutCtx
	}

	c, _, err := ws.Dial(dialCtx, url, getConnectionOptions(requestHeader, tlsConfig))
	if err != nil {
		return nil, err
	}
	c.SetReadLimit(2 << 24) // 33MB

	// this will tell if the connection is closed

	// ping every 29 seconds
	ticker := time.NewTicker(29 * time.Second)

	// main websocket loop
	writeQueue := make(chan writeRequest)
	readQueue := make(chan string)

	conn := &connection{
		conn:         c,
		cancel:       cancel,
		writeQueue:   writeQueue,
		closed:       &atomic.Bool{},
		closedNotify: make(chan struct{}),
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				conn.doClose(ws.StatusNormalClosure, "")
				debugLogf("{%s} closing!, context done: '%s'\n", url, context.Cause(ctx))
				return
			case <-conn.closedNotify:
				return
			case <-ticker.C:
				ctx, cancel := context.WithTimeoutCause(ctx, time.Millisecond*800, errors.New("ping took too long"))
				err := c.Ping(ctx)
				cancel()
				if err != nil {
					debugLogf("{%s} closing!, ping failed: '%s'\n", url, err)
					conn.doClose(ws.StatusAbnormalClosure, "ping took too long")
					return
				}
			case wr := <-writeQueue:
				debugLogf("{%s} sending '%v'\n", url, string(wr.msg))
				ctx, cancel := context.WithTimeoutCause(ctx, time.Second*10, errors.New("write took too long"))
				err := c.Write(ctx, ws.MessageText, wr.msg)
				cancel()
				if err != nil {
					debugLogf("{%s} closing!, write failed: '%s'\n", url, err)
					conn.doClose(ws.StatusAbnormalClosure, "write failed")
					if wr.answer != nil {
						wr.answer <- err
					}
					return
				}
				if wr.answer != nil {
					close(wr.answer)
				}
			case msg := <-readQueue:
				debugLogf("{%s} received %v\n", url, msg)
				handleMessage(msg)
			}
		}
	}()

	// read loop -- loops back to the main loop
	go func() {
		buf := new(bytes.Buffer)

		for {
			buf.Reset()

			_, reader, err := c.Reader(ctx)
			if err != nil {
				debugLogf("{%s} closing!, reader failure: '%s'\n", url, err)
				conn.doClose(ws.StatusAbnormalClosure, "failed to get reader")
				return
			}
			if _, err := io.Copy(buf, reader); err != nil {
				debugLogf("{%s} closing!, read failure: '%s'\n", url, err)
				conn.doClose(ws.StatusAbnormalClosure, "failed to read")
				return
			}

			readQueue <- buf.String()
		}
	}()

	return conn, nil
}

func (c *connection) doClose(code ws.StatusCode, reason string) {
	wasClosed := c.closed.Swap(true)
	if !wasClosed {
		c.conn.Close(code, reason)
		c.cancel(fmt.Errorf("doClose(): %s", reason))
		c.closeMutex.Lock()
		close(c.closedNotify)
		close(c.writeQueue)
		c.closeMutex.Unlock()
	}
}
