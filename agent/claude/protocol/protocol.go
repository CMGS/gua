package protocol

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

// Envelope types.
const (
	TypeRegister          = "register"           // bridge → dispatcher: identity registration
	TypeChannelEvent      = "channel_event"      // dispatcher → bridge: push to Claude Code
	TypeToolCall          = "tool_call"          // bridge → dispatcher: Claude called gua_reply
	TypePermissionRequest = "permission_request" // bridge → dispatcher: Claude wants approval
	TypePermissionReply   = "permission_reply"   // dispatcher → bridge: user replied y/n
)

// Envelope is the message format for dispatcher ↔ bridge communication over Unix socket.
type Envelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// Register is sent by the bridge to identify which user it serves.
type Register struct {
	UserID string `json:"user_id"`
}

// ChannelEvent is sent from dispatcher to bridge, forwarded as MCP notification.
type ChannelEvent struct {
	Content string            `json:"content"`
	Meta    map[string]string `json:"meta"`
}

// ToolCall is sent from bridge to dispatcher when Claude calls the reply tool.
type ToolCall struct {
	SenderID string `json:"sender_id"`
	Text     string `json:"text"`
	FilePath string `json:"file_path,omitempty"`
}

// Permission carries permission request/reply data.
type Permission struct {
	RequestID    string `json:"request_id"`
	ToolName     string `json:"tool_name,omitempty"`
	Description  string `json:"description,omitempty"`
	InputPreview string `json:"input_preview,omitempty"`
	Behavior     string `json:"behavior,omitempty"` // "allow" or "deny"
	TmuxPrompt   string `json:"-"`                  // not serialized; captured from tmux pane
}

// WriteEnvelope encodes and writes a JSON-line envelope to the writer.
func WriteEnvelope(w io.Writer, typ string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	env := Envelope{Type: typ, Payload: raw}
	data, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}

	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}

// ReadEnvelope reads and decodes a JSON-line envelope from the reader.
func ReadEnvelope(r *bufio.Reader) (*Envelope, error) {
	line, err := r.ReadBytes('\n')
	if err != nil {
		return nil, err
	}

	env := &Envelope{}
	if err := json.Unmarshal(line, env); err != nil {
		return nil, fmt.Errorf("unmarshal envelope: %w", err)
	}
	return env, nil
}

// DecodePayload unmarshals the envelope payload into the target type.
func DecodePayload[T any](env *Envelope) (*T, error) {
	v := new(T)
	if err := json.Unmarshal(env.Payload, v); err != nil {
		return nil, fmt.Errorf("decode %s payload: %w", env.Type, err)
	}
	return v, nil
}
