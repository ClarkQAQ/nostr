package nostr

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	ws "github.com/coder/websocket"
)

var ErrDisconnected = errors.New("<disconnected>")

type writeRequest struct {
	msg    []byte
	answer chan error
}

func (r *Relay) newConnection(ctx context.Context, tlsConfig *tls.Config) error {
	debugLogf("{%s} connecting!\n", r.URL)

	dialCtx := ctx
	if _, ok := dialCtx.Deadline(); !ok {
		// if no timeout is set, force it to 7 seconds
		timeoutCtx, timeoutCancel := context.WithTimeoutCause(ctx, 7*time.Second, errors.New("connection took too long"))
		defer timeoutCancel()
		dialCtx = timeoutCtx
	}

	c, _, err := ws.Dial(dialCtx, r.URL, getConnectionOptions(r.requestHeader, tlsConfig))
	if err != nil {
		return err
	}
	c.SetReadLimit(2 << 24) // 33MB

	// this will tell if the connection is closed

	// ping every 29 seconds
	ticker := time.NewTicker(29 * time.Second)

	// main websocket loop
	readQueue := make(chan string)

	r.conn = c
	r.writeQueue = make(chan writeRequest)
	r.closed = &atomic.Bool{}
	r.closedNotify = make(chan struct{})

	go func() {
		pingAttempt := 0

		for {
			select {
			case <-ctx.Done():
				r.closeConnection(ws.StatusNormalClosure, "")
				debugLogf("{%s} closing!, context done: '%s'\n", r.URL, context.Cause(ctx))
				return
			case <-r.closedNotify:
				return
			case <-ticker.C:
				debugLogf("{%s} pinging\n", r.URL)
				ctx, cancel := context.WithTimeoutCause(ctx, time.Millisecond*800, errors.New("ping took too long"))
				err := c.Ping(ctx)
				cancel()

				if err != nil {
					pingAttempt++
					debugLogf("{%s} error writing ping (attempt %d): %v", r.URL, pingAttempt, err)

					if pingAttempt >= 3 {
						debugLogf("{%s} error writing ping after multiple attempts; closing websocket", r.URL)
						err = r.Close() // this should trigger a context cancelation
						if err != nil {
							debugLogf("{%s} failed to close relay: %v", r.URL, err)
						}
					}

					continue
				}

				// ping was OK
				debugLogf("{%s} ping OK", r.URL)
				pingAttempt = 0
			case wr := <-r.writeQueue:
				debugLogf("{%s} sending '%v'\n", r.URL, string(wr.msg))
				ctx, cancel := context.WithTimeoutCause(ctx, time.Second*10, errors.New("write took too long"))
				err := c.Write(ctx, ws.MessageText, wr.msg)
				cancel()
				if err != nil {
					debugLogf("{%s} closing!, write failed: '%s'\n", r.URL, err)
					r.closeConnection(ws.StatusAbnormalClosure, "write failed")
					if wr.answer != nil {
						wr.answer <- err
					}
					return
				}
				if wr.answer != nil {
					close(wr.answer)
				}
			case msg := <-readQueue:
				debugLogf("{%s} received %v\n", r.URL, msg)
				r.handleMessage(msg)
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
				debugLogf("{%s} closing!, reader failure: '%s'\n", r.URL, err)
				r.closeConnection(ws.StatusAbnormalClosure, "failed to get reader")
				return
			}
			if _, err := io.Copy(buf, reader); err != nil {
				debugLogf("{%s} closing!, read failure: '%s'\n", r.URL, err)
				r.closeConnection(ws.StatusAbnormalClosure, "failed to read")
				return
			}

			readQueue <- buf.String()
		}
	}()

	return nil
}

func (r *Relay) closeConnection(code ws.StatusCode, reason string) {
	wasClosed := r.closed.Swap(true)
	if !wasClosed {
		r.conn.Close(code, reason)
		r.connectionContextCancel(fmt.Errorf("doClose(): %s", reason))
		r.closeMutex.Lock()
		close(r.closedNotify)
		close(r.writeQueue)
		r.conn = nil
		r.closeMutex.Unlock()
	}
}
