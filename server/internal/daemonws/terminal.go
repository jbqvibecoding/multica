package daemonws

import (
	"encoding/json"
	"errors"
	"log/slog"
	"sync"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// TerminalSink receives terminal.* frames addressed to a single browser
// WebSocket connection. The handler implementation owns the frame queue;
// implementations must be non-blocking — the hub drops the frame if Deliver
// returns false rather than back up the daemon read pump.
type TerminalSink interface {
	Deliver(frame []byte) bool
}

// TerminalRouter is the daemonws-side multiplex for terminal.* frames coming
// back from a daemon. Browser proxy connections register under their pending
// request_id and, after terminal.opened arrives, re-register under the
// session_id the daemon picked.
type TerminalRouter struct {
	mu    sync.RWMutex
	sinks map[string]TerminalSink
}

// NewTerminalRouter constructs an empty router. The Hub owns the only
// instance in production; tests can build their own.
func NewTerminalRouter() *TerminalRouter {
	return &TerminalRouter{sinks: make(map[string]TerminalSink)}
}

// Register installs sink under the given key. The key is either a
// request_id (before the daemon assigns a session_id) or a session_id
// (after the open ack). Re-registering an existing key replaces the sink.
func (r *TerminalRouter) Register(key string, sink TerminalSink) {
	if r == nil || key == "" || sink == nil {
		return
	}
	r.mu.Lock()
	r.sinks[key] = sink
	r.mu.Unlock()
}

// Unregister removes the sink for key, if any.
func (r *TerminalRouter) Unregister(key string) {
	if r == nil || key == "" {
		return
	}
	r.mu.Lock()
	delete(r.sinks, key)
	r.mu.Unlock()
}

// Route extracts the routing key (request_id or session_id) from a
// terminal.* frame and forwards the raw frame to the registered sink.
// Unknown keys are dropped silently — the daemon-side session ultimately
// observes the dead client via send-side errors / idle timeout.
func (r *TerminalRouter) Route(frame []byte, msgType string, payload json.RawMessage) {
	if r == nil {
		return
	}
	key := terminalRouteKey(msgType, payload)
	if key == "" {
		return
	}
	r.mu.RLock()
	sink := r.sinks[key]
	r.mu.RUnlock()
	if sink == nil {
		return
	}
	if !sink.Deliver(frame) {
		slog.Debug("daemon ws terminal frame dropped: slow sink", "type", msgType, "key", key)
	}
}

func terminalRouteKey(msgType string, payload json.RawMessage) string {
	switch msgType {
	case protocol.MessageTypeTerminalOpened, protocol.MessageTypeTerminalError:
		var p struct {
			RequestID string `json:"request_id,omitempty"`
			SessionID string `json:"session_id,omitempty"`
		}
		if err := json.Unmarshal(payload, &p); err != nil {
			return ""
		}
		// terminal.error may carry either request_id (pre-open failure) or
		// session_id (post-open failure). Prefer request_id so the proxy
		// receives the failure on the same key it registered.
		if p.RequestID != "" {
			return p.RequestID
		}
		return p.SessionID
	case protocol.MessageTypeTerminalData,
		protocol.MessageTypeTerminalClose,
		protocol.MessageTypeTerminalExit:
		var p struct {
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(payload, &p); err != nil {
			return ""
		}
		return p.SessionID
	}
	return ""
}

// SetTerminalRouter installs an explicit router. Tests use this to inject a
// fake; production code can rely on the auto-created router exposed by
// TerminalRouter() below.
func (h *Hub) SetTerminalRouter(r *TerminalRouter) {
	if h == nil {
		return
	}
	h.termMu.Lock()
	h.termRouter = r
	h.termMu.Unlock()
}

// TerminalRouter returns the hub's router, creating one on first access.
// The browser proxy WS handler grabs this to register per-session sinks;
// production wiring does not need an explicit SetTerminalRouter call.
func (h *Hub) TerminalRouter() *TerminalRouter {
	if h == nil {
		return nil
	}
	h.termMu.RLock()
	r := h.termRouter
	h.termMu.RUnlock()
	if r != nil {
		return r
	}
	h.termMu.Lock()
	defer h.termMu.Unlock()
	if h.termRouter == nil {
		h.termRouter = NewTerminalRouter()
	}
	return h.termRouter
}

// terminalRouter is the internal read-only accessor used by handleFrame.
// It returns nil if no router has been configured, which short-circuits
// dispatch — the auto-create only happens through the public accessor.
func (h *Hub) terminalRouter() *TerminalRouter {
	h.termMu.RLock()
	defer h.termMu.RUnlock()
	return h.termRouter
}

// ErrNoDaemonForRuntime is returned by SendToRuntime when no daemon is
// currently connected for the given runtime_id. The browser proxy uses this
// to fail the open request with a clear error.
var ErrNoDaemonForRuntime = errors.New("daemonws: no daemon connected for runtime")

// SendToRuntime delivers a raw frame to one daemon connection serving
// runtimeID. If multiple daemons are registered (rare — usually one per
// runtime), the first one wins. Returns ErrNoDaemonForRuntime when no
// connection exists, or a "buffer full" error when the daemon's outbound
// queue is saturated — callers should surface that as a transient failure
// to the browser rather than retrying tightly.
func (h *Hub) SendToRuntime(runtimeID string, frame []byte) error {
	if h == nil || runtimeID == "" {
		return ErrNoDaemonForRuntime
	}
	h.mu.RLock()
	var target *client
	for c := range h.byRuntime[runtimeID] {
		target = c
		break
	}
	h.mu.RUnlock()
	if target == nil {
		return ErrNoDaemonForRuntime
	}
	if !target.trySend(frame) {
		return errors.New("daemonws: daemon send buffer full")
	}
	return nil
}

// trySend pushes frame onto the client's outbound queue without blocking.
// Returns false if the buffer is saturated. We deliberately do not evict
// the connection here — terminal back-pressure should slow the producing
// browser/server side, not tear down the entire daemonws connection (which
// would also break heartbeat + wakeup delivery for unrelated runtimes).
func (c *client) trySend(frame []byte) bool {
	select {
	case c.send <- frame:
		return true
	default:
		return false
	}
}
