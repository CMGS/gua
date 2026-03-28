package codex

import (
	"context"
	"io"
	"sync"

	"github.com/CMGS/gua/agent"
	"github.com/CMGS/gua/runtime"
	"github.com/CMGS/gua/utils"
)

// pendingElicitation holds a server-initiated elicitation request awaiting user response.
type pendingElicitation struct {
	requestID int64
	kind      string // "exec-approval" or "patch-approval"
	message   string
	replyCh   chan string // "approved" or "denied"
}

type codexSession struct {
	userID   string
	workDir  string
	threadID string // codex MCP session threadId

	proc    *runtime.Process // tmux pane running codex mcp-server
	rpc     *rpcClient
	fifoIn  io.WriteCloser // gua writes JSON-RPC requests
	fifoOut io.ReadCloser  // gua reads JSON-RPC responses

	outCh  chan *agent.Response
	cancel context.CancelFunc

	approvalPolicy string     // "on-request" or "never"
	sandbox        string     // "workspace-write" or "danger-full-access"
	callMu         sync.Mutex // serializes tool calls to prevent threadID race

	// Elicitation queue for concurrent approval requests.
	elicitQueue utils.SyncQueue[pendingElicitation]
}

func (s *codexSession) pushResponse(resp *agent.Response) {
	defer func() { recover() }() //nolint:errcheck
	select {
	case s.outCh <- resp:
	default:
	}
}

func (s *codexSession) close() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.fifoIn != nil {
		_ = s.fifoIn.Close()
	}
	if s.fifoOut != nil {
		_ = s.fifoOut.Close()
	}
	if s.outCh != nil {
		close(s.outCh)
	}
}
