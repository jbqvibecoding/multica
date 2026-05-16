package daemon

import (
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/terminal"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// captureSender is the test stand-in for the daemon's outbound WS writer.
// Frames are kept in order so the test can wait for a specific message type
// to appear.
type captureSender struct {
	mu     sync.Mutex
	frames [][]byte
}

func (c *captureSender) send(frame []byte) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := make([]byte, len(frame))
	copy(cp, frame)
	c.frames = append(c.frames, cp)
	return true
}

func (c *captureSender) waitFor(t *testing.T, msgType string, timeout time.Duration) protocol.Message {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		for _, f := range c.frames {
			var m protocol.Message
			if err := json.Unmarshal(f, &m); err == nil && m.Type == msgType {
				c.mu.Unlock()
				return m
			}
		}
		c.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for frame of type %q (saw %d frames)", msgType, len(c.frames))
	return protocol.Message{}
}

// fakeBridgePTY is a minimal PTY for the bridge integration: it lets the
// test push child output and read writes back. Wait blocks until Close.
type fakeBridgePTY struct {
	out      chan []byte
	mu       sync.Mutex
	written  []byte
	cols     uint16
	rows     uint16
	closeCh  chan struct{}
	exit     int
	waitOnce sync.Once
	waitDone chan struct{}
}

func newFakeBridgePTY(cols, rows uint16) *fakeBridgePTY {
	return &fakeBridgePTY{
		out:      make(chan []byte, 4),
		cols:     cols,
		rows:     rows,
		closeCh:  make(chan struct{}),
		waitDone: make(chan struct{}),
	}
}

func (p *fakeBridgePTY) Read(b []byte) (int, error) {
	select {
	case chunk, ok := <-p.out:
		if !ok {
			return 0, errEOF
		}
		n := copy(b, chunk)
		return n, nil
	case <-p.closeCh:
		return 0, errEOF
	}
}

func (p *fakeBridgePTY) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.written = append(p.written, b...)
	return len(b), nil
}

func (p *fakeBridgePTY) Resize(cols, rows uint16) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cols = cols
	p.rows = rows
	return nil
}

func (p *fakeBridgePTY) Close() error {
	p.waitOnce.Do(func() {
		close(p.closeCh)
		close(p.waitDone)
	})
	return nil
}

func (p *fakeBridgePTY) Wait() (int, error) {
	<-p.waitDone
	return p.exit, nil
}

type stringErr string

func (e stringErr) Error() string { return string(e) }

const errEOF = stringErr("EOF")

func TestTerminalBridge_OpenSendsOpenedFrameWithServerSuppliedWorkdir(t *testing.T) {
	tmp := t.TempDir()

	pty := newFakeBridgePTY(80, 24)
	spawner := &stubSpawner{pty: pty}
	mgr := terminal.NewManager(terminal.ManagerConfig{
		Spawner: spawner,
		Logger:  slog.Default(),
	}, nil)
	defer mgr.Close()

	sender := &captureSender{}
	bridge := newTerminalBridge(mgr, slog.Default(), sender.send)

	openPayload, err := json.Marshal(protocol.TerminalOpenPayload{
		RequestID:      "req-1",
		TaskID:         "task-via-ws",
		WorkspaceID:    "ws-A",
		UserID:         "user-1",
		IssueID:        "issue-1",
		WorkDir:        tmp,
		PriorSessionID: "claude-xyz",
		Cols:           120,
		Rows:           30,
	})
	if err != nil {
		t.Fatalf("marshal open: %v", err)
	}

	bridge.handleFrame(protocol.MessageTypeTerminalOpen, openPayload)

	openedMsg := sender.waitFor(t, protocol.MessageTypeTerminalOpened, time.Second)
	var opened protocol.TerminalOpenedPayload
	if err := json.Unmarshal(openedMsg.Payload, &opened); err != nil {
		t.Fatalf("opened payload: %v", err)
	}
	if opened.RequestID != "req-1" {
		t.Errorf("opened.request_id = %q, want req-1", opened.RequestID)
	}
	if opened.SessionID == "" {
		t.Errorf("opened.session_id is empty")
	}
	if opened.WorkDir != tmp {
		t.Errorf("opened.work_dir = %q, want %q", opened.WorkDir, tmp)
	}
}

func TestTerminalBridge_OpenWithoutWorkdirEmitsTaskNotFound(t *testing.T) {
	// The server is supposed to resolve task.work_dir from its DB before
	// forwarding terminal.open. If it forgets / fails, the daemon must
	// not silently fall through to spawning bash in CWD — it has to
	// surface a structured terminal.error and never call the spawner.
	pty := newFakeBridgePTY(80, 24)
	spawner := &stubSpawner{pty: pty}
	mgr := terminal.NewManager(terminal.ManagerConfig{
		Spawner: spawner,
		Logger:  slog.Default(),
	}, nil)
	defer mgr.Close()

	sender := &captureSender{}
	bridge := newTerminalBridge(mgr, slog.Default(), sender.send)

	openPayload, _ := json.Marshal(protocol.TerminalOpenPayload{
		RequestID:   "req-2",
		TaskID:      "task-evil",
		WorkspaceID: "ws-B",
		WorkDir:     "", // server failed to resolve
		Cols:        80,
		Rows:        24,
	})

	bridge.handleFrame(protocol.MessageTypeTerminalOpen, openPayload)

	errMsg := sender.waitFor(t, protocol.MessageTypeTerminalError, time.Second)
	var errPayload protocol.TerminalErrorPayload
	if err := json.Unmarshal(errMsg.Payload, &errPayload); err != nil {
		t.Fatalf("error payload: %v", err)
	}
	if errPayload.Code != protocol.TerminalErrorCodeTaskNotFound {
		t.Errorf("error code = %q, want %q", errPayload.Code, protocol.TerminalErrorCodeTaskNotFound)
	}
	if errPayload.RequestID != "req-2" {
		t.Errorf("error request_id = %q, want req-2", errPayload.RequestID)
	}
	if spawner.callCount() != 0 {
		t.Errorf("spawner was invoked %d times despite resolve failure", spawner.callCount())
	}
}

func TestTerminalBridge_DataAndExitRoundTrip(t *testing.T) {
	tmp := t.TempDir()

	pty := newFakeBridgePTY(80, 24)
	spawner := &stubSpawner{pty: pty}
	mgr := terminal.NewManager(terminal.ManagerConfig{
		Spawner: spawner,
		Logger:  slog.Default(),
	}, nil)
	defer mgr.Close()

	sender := &captureSender{}
	bridge := newTerminalBridge(mgr, slog.Default(), sender.send)

	openPayload, _ := json.Marshal(protocol.TerminalOpenPayload{
		RequestID:   "req-3",
		TaskID:      "task-3",
		WorkspaceID: "ws-A",
		WorkDir:     tmp,
		Cols:        80,
		Rows:        24,
	})
	bridge.handleFrame(protocol.MessageTypeTerminalOpen, openPayload)
	openedMsg := sender.waitFor(t, protocol.MessageTypeTerminalOpened, time.Second)
	var opened protocol.TerminalOpenedPayload
	_ = json.Unmarshal(openedMsg.Payload, &opened)
	sessionID := opened.SessionID

	// Push child output → bridge should emit terminal.data on the WS.
	pty.out <- []byte("hello\n")
	dataMsg := sender.waitFor(t, protocol.MessageTypeTerminalData, time.Second)
	var dp protocol.TerminalDataPayload
	if err := json.Unmarshal(dataMsg.Payload, &dp); err != nil {
		t.Fatalf("data payload: %v", err)
	}
	if dp.SessionID != sessionID {
		t.Errorf("data session_id = %q, want %q", dp.SessionID, sessionID)
	}
	decoded, err := base64.StdEncoding.DecodeString(dp.DataB64)
	if err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if string(decoded) != "hello\n" {
		t.Errorf("data bytes = %q, want %q", decoded, "hello\n")
	}

	// Send a write the other direction. The bridge should base64-decode
	// and call PTY.Write.
	inboundData, _ := json.Marshal(protocol.TerminalDataPayload{
		SessionID: sessionID,
		DataB64:   base64.StdEncoding.EncodeToString([]byte("ls\n")),
	})
	bridge.handleFrame(protocol.MessageTypeTerminalData, inboundData)

	// Allow Write to settle.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		pty.mu.Lock()
		got := string(pty.written)
		pty.mu.Unlock()
		if got == "ls\n" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	pty.mu.Lock()
	if string(pty.written) != "ls\n" {
		t.Errorf("PTY received %q, want %q", pty.written, "ls\n")
	}
	pty.mu.Unlock()

	// Close from the client side. The bridge should propagate via
	// session.Close → waitLoop → terminal.exit.
	closePayload, _ := json.Marshal(protocol.TerminalClosePayload{
		SessionID: sessionID,
		Reason:    "test",
	})
	bridge.handleFrame(protocol.MessageTypeTerminalClose, closePayload)

	sender.waitFor(t, protocol.MessageTypeTerminalExit, time.Second)
}

// stubSpawner returns a single pre-built PTY on the first Start. callCount
// lets tests assert that no spawn happened on a reject path.
type stubSpawner struct {
	pty   *fakeBridgePTY
	mu    sync.Mutex
	calls int
}

func (s *stubSpawner) Start(_ terminal.SpawnRequest) (terminal.PTY, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	return s.pty, nil
}

func (s *stubSpawner) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}
