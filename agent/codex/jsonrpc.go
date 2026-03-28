package codex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// JSON-RPC 2.0 message types for communicating with codex mcp-server.

type jsonrpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *jsonrpcError) Error() string {
	return fmt.Sprintf("JSON-RPC error %d: %s", e.Code, e.Message)
}

// rpcClient manages JSON-RPC communication over stdin/stdout pipes.
type rpcClient struct {
	writer  io.Writer
	writeMu sync.Mutex
	nextID  atomic.Int64

	// pending tracks in-flight requests: id → response channel.
	pendingMu sync.Mutex
	pending   map[int64]chan *jsonrpcMessage

	// Callbacks for server-initiated requests and notifications.
	onRequest      func(msg *jsonrpcMessage) // elicitation/create
	onNotification func(msg *jsonrpcMessage) // codex/event
}

func newRPCClient(writer io.Writer) *rpcClient {
	return &rpcClient{
		writer:  writer,
		pending: make(map[int64]chan *jsonrpcMessage),
	}
}

// call sends a JSON-RPC request and waits for the response.
func (c *rpcClient) call(method string, params any) (*jsonrpcMessage, error) {
	id := c.nextID.Add(1)
	ch := make(chan *jsonrpcMessage, 1)

	c.pendingMu.Lock()
	c.pending[id] = ch
	c.pendingMu.Unlock()

	if err := c.send(&jsonrpcMessage{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  method,
		Params:  mustMarshal(params),
	}); err != nil {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, err
	}

	resp := <-ch
	if resp == nil {
		return nil, fmt.Errorf("connection closed")
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	return resp, nil
}

// notify sends a JSON-RPC notification (no id, no response expected).
func (c *rpcClient) notify(method string, params any) error {
	return c.send(&jsonrpcMessage{
		JSONRPC: "2.0",
		Method:  method,
		Params:  mustMarshal(params),
	})
}

// respond sends a JSON-RPC response to a server-initiated request.
func (c *rpcClient) respond(id int64, result any) error {
	return c.send(&jsonrpcMessage{
		JSONRPC: "2.0",
		ID:      &id,
		Result:  mustMarshal(result),
	})
}

func (c *rpcClient) send(msg *jsonrpcMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.writer.Write(data)
	return err
}

// readLoop reads from the reader and dispatches messages.
// Blocks until the reader is closed or an error occurs.
func (c *rpcClient) readLoop(reader *bufio.Reader) {
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			break
		}

		var msg jsonrpcMessage
		if json.Unmarshal(line, &msg) != nil {
			continue // skip non-JSON lines
		}

		switch {
		case msg.ID != nil && msg.Method == "":
			// Response to a request we sent.
			c.pendingMu.Lock()
			ch, ok := c.pending[*msg.ID]
			if ok {
				delete(c.pending, *msg.ID)
			}
			c.pendingMu.Unlock()
			if ok {
				ch <- &msg
			}

		case msg.ID != nil && msg.Method != "":
			// Server-initiated request (e.g. elicitation/create).
			if c.onRequest != nil {
				c.onRequest(&msg)
			}

		case msg.Method != "":
			// Notification (e.g. codex/event).
			if c.onNotification != nil {
				c.onNotification(&msg)
			}
		}
	}

	// Close all pending channels on reader shutdown.
	c.pendingMu.Lock()
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.pendingMu.Unlock()
}

// cancelAll sends notifications/canceled for all pending requests.
func (c *rpcClient) cancelAll() {
	c.pendingMu.Lock()
	ids := make([]int64, 0, len(c.pending))
	for id := range c.pending {
		ids = append(ids, id)
	}
	c.pendingMu.Unlock()

	for _, id := range ids {
		_ = c.notify("notifications/cancelled", map[string]any{"requestId": id}) //nolint:misspell // MCP protocol-defined method name
	}
}

func mustMarshal(v any) json.RawMessage {
	if v == nil {
		return json.RawMessage("null")
	}
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return data
}
