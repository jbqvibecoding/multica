package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"

	"github.com/multica-ai/multica/server/internal/daemon/terminal"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// terminalBridge adapts the daemon-side terminal.Manager to the daemonws
// WebSocket transport. Per session it:
//
//   - relays PtySession.Output() → terminal.data frames (daemon→server)
//   - relays PtySession.ExitC()  → terminal.exit frames
//   - tears the bridge goroutine down when Done() fires
//
// frameSender is the daemon's currently-active WS writer (see
// Daemon.sendWSFrame). It returns false when no connection is active or the
// outbound queue is saturated; we drop the frame in that case rather than
// stall the reader, because the next reconnect would unstick us anyway.
type terminalBridge struct {
	manager *terminal.Manager
	logger  *slog.Logger
	send    func([]byte) bool

	mu       sync.Mutex
	sessions map[string]*terminalRoute
}

type terminalRoute struct {
	session *terminal.PtySession
	cancel  context.CancelFunc
}

func newTerminalBridge(mgr *terminal.Manager, logger *slog.Logger, send func([]byte) bool) *terminalBridge {
	return &terminalBridge{
		manager:  mgr,
		logger:   logger,
		send:     send,
		sessions: make(map[string]*terminalRoute),
	}
}

// handleFrame dispatches a single terminal.* envelope from the server. The
// caller already decoded protocol.Message; we receive the inner type+payload.
func (b *terminalBridge) handleFrame(msgType string, payload json.RawMessage) {
	switch msgType {
	case protocol.MessageTypeTerminalOpen:
		var p protocol.TerminalOpenPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			b.logger.Debug("terminal.open invalid payload", "error", err)
			return
		}
		b.handleOpen(p)
	case protocol.MessageTypeTerminalData:
		var p protocol.TerminalDataPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			b.logger.Debug("terminal.data invalid payload", "error", err)
			return
		}
		b.handleData(p)
	case protocol.MessageTypeTerminalResize:
		var p protocol.TerminalResizePayload
		if err := json.Unmarshal(payload, &p); err != nil {
			b.logger.Debug("terminal.resize invalid payload", "error", err)
			return
		}
		b.handleResize(p)
	case protocol.MessageTypeTerminalClose:
		var p protocol.TerminalClosePayload
		if err := json.Unmarshal(payload, &p); err != nil {
			b.logger.Debug("terminal.close invalid payload", "error", err)
			return
		}
		b.handleClose(p)
	}
}

func (b *terminalBridge) handleOpen(p protocol.TerminalOpenPayload) {
	info := terminal.TaskInfo{
		TaskID:         p.TaskID,
		WorkspaceID:    p.WorkspaceID,
		IssueID:        p.IssueID,
		WorkDir:        p.WorkDir,
		PriorSessionID: p.PriorSessionID,
	}
	sess, err := b.manager.OpenWithInfo(context.Background(), info, terminal.OpenParams{
		TaskID:      p.TaskID,
		WorkspaceID: p.WorkspaceID,
		UserID:      p.UserID,
		Cols:        p.Cols,
		Rows:        p.Rows,
	})
	if err != nil {
		b.sendError(p.RequestID, "", mapTerminalError(err), err.Error())
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	b.mu.Lock()
	b.sessions[sess.ID()] = &terminalRoute{session: sess, cancel: cancel}
	b.mu.Unlock()

	b.sendFrame(protocol.MessageTypeTerminalOpened, protocol.TerminalOpenedPayload{
		RequestID: p.RequestID,
		SessionID: sess.ID(),
		WorkDir:   sess.WorkDir(),
		Shell:     sess.Shell(),
	})

	go b.pump(ctx, sess)
}

func (b *terminalBridge) handleData(p protocol.TerminalDataPayload) {
	sess, err := b.manager.Get(p.SessionID)
	if err != nil {
		b.sendError("", p.SessionID, protocol.TerminalErrorCodeSessionNotFound, err.Error())
		return
	}
	data, err := base64.StdEncoding.DecodeString(p.DataB64)
	if err != nil {
		b.logger.Debug("terminal.data invalid base64", "error", err, "session_id", p.SessionID)
		return
	}
	if _, err := sess.Write(data); err != nil {
		b.logger.Debug("terminal.data write failed", "error", err, "session_id", p.SessionID)
	}
}

func (b *terminalBridge) handleResize(p protocol.TerminalResizePayload) {
	sess, err := b.manager.Get(p.SessionID)
	if err != nil {
		b.sendError("", p.SessionID, protocol.TerminalErrorCodeSessionNotFound, err.Error())
		return
	}
	if err := sess.Resize(p.Cols, p.Rows); err != nil {
		b.logger.Debug("terminal.resize failed", "error", err, "session_id", p.SessionID)
	}
}

func (b *terminalBridge) handleClose(p protocol.TerminalClosePayload) {
	sess, err := b.manager.Get(p.SessionID)
	if err != nil {
		// Already gone — nothing to do; the server side has already received
		// a terminal.exit frame (or will, through the pump goroutine).
		return
	}
	reason := p.Reason
	if reason == "" {
		reason = "client_close"
	}
	sess.Close(reason)
}

// pump bridges one session's output channel onto the WS as terminal.data
// frames, and emits a terminal.exit when the child exits. Returns when
// either the session is fully torn down or ctx is cancelled.
func (b *terminalBridge) pump(ctx context.Context, sess *terminal.PtySession) {
	sessionID := sess.ID()
	defer func() {
		b.mu.Lock()
		delete(b.sessions, sessionID)
		b.mu.Unlock()
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case chunk, ok := <-sess.Output():
			if !ok {
				// Output closed → child exited and waitLoop finalized.
				// ExitC was already delivered (or about to be); pull it once
				// non-blocking and forward, then exit the pump.
				var info terminal.ExitInfo
				select {
				case info = <-sess.ExitC():
				default:
				}
				b.sendFrame(protocol.MessageTypeTerminalExit, protocol.TerminalExitPayload{
					SessionID: sessionID,
					ExitCode:  info.ExitCode,
					Reason:    info.Reason,
				})
				<-sess.Done()
				return
			}
			b.sendFrame(protocol.MessageTypeTerminalData, protocol.TerminalDataPayload{
				SessionID: sessionID,
				DataB64:   base64.StdEncoding.EncodeToString(chunk),
			})
		}
	}
}

// closeAll tears down every live session. Called when the daemon
// disconnects from the server: the browser proxy will fail downstream,
// and a reconnect cannot resurrect the pre-existing PTYs because the
// session_ids only existed in the prior WS context.
func (b *terminalBridge) closeAll(reason string) {
	b.mu.Lock()
	routes := make([]*terminalRoute, 0, len(b.sessions))
	for _, r := range b.sessions {
		routes = append(routes, r)
	}
	b.mu.Unlock()
	for _, r := range routes {
		r.cancel()
		r.session.Close(reason)
	}
}

func (b *terminalBridge) sendFrame(msgType string, payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		b.logger.Debug("terminal frame marshal failed", "error", err, "type", msgType)
		return
	}
	frame, err := json.Marshal(protocol.Message{Type: msgType, Payload: raw})
	if err != nil {
		b.logger.Debug("terminal envelope marshal failed", "error", err, "type", msgType)
		return
	}
	if !b.send(frame) {
		b.logger.Debug("terminal frame dropped: ws disconnected or backed up", "type", msgType)
	}
}

func (b *terminalBridge) sendError(requestID, sessionID, code, message string) {
	b.sendFrame(protocol.MessageTypeTerminalError, protocol.TerminalErrorPayload{
		RequestID: requestID,
		SessionID: sessionID,
		Code:      code,
		Message:   message,
	})
}

// mapTerminalError translates the terminal package's sentinel errors into
// protocol error codes the browser proxy can render. Anything we don't
// recognise falls back to TerminalErrorCodeInternal — drop information
// rather than surface internal wrap text to the user.
func mapTerminalError(err error) string {
	switch {
	case errors.Is(err, terminal.ErrWorkspaceMismatch):
		return protocol.TerminalErrorCodeWorkspaceMismatch
	case errors.Is(err, terminal.ErrTaskNotFound):
		return protocol.TerminalErrorCodeTaskNotFound
	case errors.Is(err, terminal.ErrSessionNotFound):
		return protocol.TerminalErrorCodeSessionNotFound
	case errors.Is(err, terminal.ErrUnsupportedOS):
		return protocol.TerminalErrorCodeUnsupportedOS
	case errors.Is(err, terminal.ErrSpawnFailed):
		return protocol.TerminalErrorCodeSpawnFailed
	}
	return protocol.TerminalErrorCodeInternal
}
