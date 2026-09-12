package cdp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/gorilla/websocket"
)

type EventCallback func(event RawEvent)

type RawEvent struct {
	SessionID string          `json:"sessionId,omitempty"`
	Method    string          `json:"method"`
	Params    json.RawMessage `json:"params,omitempty"`
}

type LateResponseHandler func(result json.RawMessage, cdpErr *CDPError)

type Client struct {
	wsURL        string
	conn         *websocket.Conn
	writeMu      sync.Mutex
	mu           sync.Mutex
	pending      map[int64]chan *rawResponse
	lateHandlers map[int64]LateResponseHandler
	closed       bool
	onEvent      EventCallback
	done         chan struct{}
	doneOnce     sync.Once
	reqIDSeq     atomic.Int64
}

type rawRequest struct {
	ID        int64  `json:"id"`
	SessionID string `json:"sessionId,omitempty"`
	Method    string `json:"method"`
	Params    any    `json:"params,omitempty"`
}

type rawResponse struct {
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *CDPError       `json:"error,omitempty"`
}

type CDPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *CDPError) Error() string {
	return fmt.Sprintf("CDP error %d: %s", e.Code, e.Message)
}

func Dial(ctx context.Context, wsURL string, onEvent EventCallback) (*Client, error) {
	if err := ValidateEndpointSecurity(wsURL); err != nil {
		return nil, err
	}

	dialer := websocket.Dialer{
		// Personal Chrome intentionally parks the HTTP Upgrade while its
		// per-session remote-debugging approval sheet is open. Do not impose a
		// transport-local timeout here; the caller context controls cancellation.
		HandshakeTimeout: 0,
		Proxy:            http.ProxyFromEnvironment,
	}

	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("dial CDP root %s: %w", wsURL, err)
	}

	client := &Client{
		wsURL:        wsURL,
		conn:         conn,
		pending:      make(map[int64]chan *rawResponse),
		lateHandlers: make(map[int64]LateResponseHandler),
		onEvent:      onEvent,
		done:         make(chan struct{}),
	}

	go client.readLoop()
	return client, nil
}

func (c *Client) readLoop() {
	defer func() {
		c.mu.Lock()
		c.closed = true
		for id, ch := range c.pending {
			delete(c.pending, id)
			close(ch)
		}
		for id := range c.lateHandlers {
			delete(c.lateHandlers, id)
		}
		c.mu.Unlock()
		_ = c.conn.Close()
		c.doneOnce.Do(func() { close(c.done) })
	}()

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			return
		}

		var generic struct {
			ID        *int64          `json:"id,omitempty"`
			SessionID string          `json:"sessionId,omitempty"`
			Method    string          `json:"method,omitempty"`
			Params    json.RawMessage `json:"params,omitempty"`
			Result    json.RawMessage `json:"result,omitempty"`
			Error     *CDPError       `json:"error,omitempty"`
		}

		if err := json.Unmarshal(message, &generic); err != nil {
			continue
		}

		// If it has an ID, it's a command reply
		if generic.ID != nil {
			reqID := *generic.ID
			c.mu.Lock()
			ch, ok := c.pending[reqID]
			if ok {
				delete(c.pending, reqID)
			}
			lateHandler, isLate := c.lateHandlers[reqID]
			if isLate {
				delete(c.lateHandlers, reqID)
			}
			c.mu.Unlock()

			if ok && ch != nil {
				ch <- &rawResponse{
					ID:     reqID,
					Result: generic.Result,
					Error:  generic.Error,
				}
			} else if isLate && lateHandler != nil {
				lateHandler(generic.Result, generic.Error)
			}
			// If not in pending or late, it was timed out / orphaned without handler; discard cleanly.
			continue
		}

		// If it has a method, it's an event
		if generic.Method != "" && c.onEvent != nil {
			c.onEvent(RawEvent{
				SessionID: generic.SessionID,
				Method:    generic.Method,
				Params:    generic.Params,
			})
		}
	}
}

// Send executes a CDP command. If sessionID is non-empty, the command is executed within that target session.
func (c *Client) Send(ctx context.Context, method string, params any, sessionID string) (json.RawMessage, error) {
	return c.SendWithLateHandler(ctx, method, params, sessionID, nil)
}

// SendWithLateHandler executes a CDP command. If the request times out locally and onLate is non-nil,
// any response that arrives later for this request ID will invoke onLate.
func (c *Client) SendWithLateHandler(ctx context.Context, method string, params any, sessionID string, onLate LateResponseHandler) (json.RawMessage, error) {
	reqID := c.reqIDSeq.Add(1)
	ch := make(chan *rawResponse, 1)

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errors.New("CDP client is closed")
	}
	c.pending[reqID] = ch
	c.mu.Unlock()

	req := rawRequest{
		ID:        reqID,
		SessionID: sessionID,
		Method:    method,
		Params:    params,
	}

	payload, err := json.Marshal(req)
	if err != nil {
		c.mu.Lock()
		delete(c.pending, reqID)
		c.mu.Unlock()
		return nil, fmt.Errorf("marshal CDP request: %w", err)
	}

	c.writeMu.Lock()
	err = c.conn.WriteMessage(websocket.TextMessage, payload)
	c.writeMu.Unlock()
	if err != nil {
		c.mu.Lock()
		delete(c.pending, reqID)
		c.mu.Unlock()
		return nil, fmt.Errorf("send CDP request %s: %w", method, err)
	}

	select {
	case resp, ok := <-ch:
		if !ok || resp == nil {
			return nil, errors.New("CDP connection closed while awaiting response")
		}
		if resp.Error != nil {
			return nil, resp.Error
		}
		if len(resp.Result) == 0 {
			return json.RawMessage("{}"), nil
		}
		return resp.Result, nil
	case <-ctx.Done():
		c.mu.Lock()
		_, stillPending := c.pending[reqID]
		if stillPending {
			delete(c.pending, reqID)
			if onLate != nil {
				c.lateHandlers[reqID] = onLate
			}
			c.mu.Unlock()
			return nil, fmt.Errorf("CDP command %s timed out after deadline: %w", method, ctx.Err())
		}
		c.mu.Unlock()

		// Pending is already absent: readLoop claimed the response at the same instant ctx.Done occurred!
		// Drain and process ch as a normal response rather than installing a useless late handler.
		select {
		case resp, ok := <-ch:
			if !ok || resp == nil {
				return nil, errors.New("CDP connection closed while awaiting response")
			}
			if resp.Error != nil {
				return nil, resp.Error
			}
			if len(resp.Result) == 0 {
				return json.RawMessage("{}"), nil
			}
			return resp.Result, nil
		default:
			return nil, fmt.Errorf("CDP command %s timed out after deadline: %w", method, ctx.Err())
		}
	}
}

func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	for id, ch := range c.pending {
		delete(c.pending, id)
		close(ch)
	}
	for id := range c.lateHandlers {
		delete(c.lateHandlers, id)
	}
	c.mu.Unlock()
	return c.conn.Close()
}

func (c *Client) IsClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// Done is closed when the browser-root CDP WebSocket is no longer usable.
func (c *Client) Done() <-chan struct{} {
	return c.done
}
