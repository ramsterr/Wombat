package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type cdpResponse struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *cdpError       `json:"error"`
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *cdpError) Error() string {
	return fmt.Sprintf("browser: %s (%d)", e.Message, e.Code)
}

type rpcResult struct {
	result json.RawMessage
	err    error
}

type cdp struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
	mu      sync.Mutex
	next    int
	pending map[int]chan rpcResult
	err     error
	done    chan struct{}

	closeOnce sync.Once
	doneOnce  sync.Once
}

func dialCDP(ctx context.Context, target string) (*cdp, error) {
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.DialContext(ctx, target, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to browser: %w", err)
	}
	conn.SetReadLimit(64 << 20)
	c := &cdp{conn: conn, pending: map[int]chan rpcResult{}, done: make(chan struct{})}
	go c.readLoop()
	return c, nil
}

func (c *cdp) Call(ctx context.Context, method string, params, result any) error {
	id := c.nextID()
	ch := make(chan rpcResult, 1)
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return err
	}
	c.pending[id] = ch
	c.mu.Unlock()

	message := map[string]any{"id": id, "method": method}
	if params != nil {
		message["params"] = params
	}
	data, err := json.Marshal(message)
	if err != nil {
		c.remove(id)
		return err
	}
	c.writeMu.Lock()
	err = c.conn.WriteMessage(websocket.TextMessage, data)
	c.writeMu.Unlock()
	if err != nil {
		c.remove(id)
		return fmt.Errorf("browser request %s: %w", method, err)
	}
	select {
	case <-ctx.Done():
		c.remove(id)
		return ctx.Err()
	case <-c.done:
		c.remove(id)
		return c.terminalError()
	case r := <-ch:
		if r.err != nil {
			return r.err
		}
		if result != nil && len(r.result) > 0 && string(r.result) != "null" {
			if err := json.Unmarshal(r.result, result); err != nil {
				return fmt.Errorf("decode browser response %s: %w", method, err)
			}
		}
		return nil
	}
}

func (c *cdp) nextID() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.next++
	return c.next
}

func (c *cdp) remove(id int) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *cdp) readLoop() {
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			c.markDone(err)
			return
		}
		var msg cdpResponse
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if msg.ID == 0 {
			continue
		}
		c.mu.Lock()
		ch := c.pending[msg.ID]
		delete(c.pending, msg.ID)
		c.mu.Unlock()
		if ch == nil {
			continue
		}
		if msg.Error != nil {
			ch <- rpcResult{err: msg.Error}
		} else {
			ch <- rpcResult{result: msg.Result}
		}
	}
}

func (c *cdp) markDone(err error) {
	c.doneOnce.Do(func() {
		c.mu.Lock()
		if c.err == nil {
			c.err = err
		}
		for id, ch := range c.pending {
			delete(c.pending, id)
			ch <- rpcResult{err: c.err}
		}
		c.mu.Unlock()
		close(c.done)
	})
}

func (c *cdp) terminalError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return c.err
	}
	return errors.New("browser connection closed")
}

func (c *cdp) Close() {
	c.closeOnce.Do(func() {
		_ = c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		_ = c.conn.Close()
		c.markDone(errors.New("browser connection closed"))
	})
}
