package terminal

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakePTY is a Spawner-served stand-in for a real PTY. Tests push child
// output via WriteFromChild and read client input via ReadFromClient.
type fakePTY struct {
	t *testing.T

	// child -> client (output queue, read by readLoop)
	childToClient chan []byte

	// client -> child (writes captured into a buffer slice under mu)
	mu          sync.Mutex
	clientWrote [][]byte
	cols, rows  uint16

	// closeOnce coordinates teardown
	closeOnce sync.Once
	closeCh   chan struct{}

	// waitDone signals Wait can return. Defaults closed by Close.
	waitOnce sync.Once
	waitDone chan struct{}
	// waitDelay (optional) sleeps inside Wait AFTER waitDone fires.
	// Lets tests prove Manager.Close waits for session finalize rather
	// than just for s.Close() to return.
	waitDelay time.Duration
	exitCode  int32
	resizedCh chan [2]uint16
	closed    atomic.Bool
	// waitCount tracks how many times Wait() was invoked. Lets tests
	// assert the cleanup path reaped the child even when no session was
	// ever registered (Manager.Close racing Open).
	waitCount atomic.Int32
}

func newFakePTY(t *testing.T, cols, rows uint16) *fakePTY {
	return &fakePTY{
		t:             t,
		childToClient: make(chan []byte, 8),
		cols:          cols,
		rows:          rows,
		closeCh:       make(chan struct{}),
		waitDone:      make(chan struct{}),
		resizedCh:     make(chan [2]uint16, 8),
	}
}

func (p *fakePTY) Read(b []byte) (int, error) {
	select {
	case chunk, ok := <-p.childToClient:
		if !ok {
			return 0, io.EOF
		}
		n := copy(b, chunk)
		return n, nil
	case <-p.closeCh:
		return 0, io.EOF
	}
}

func (p *fakePTY) Write(b []byte) (int, error) {
	if p.closed.Load() {
		return 0, io.ErrClosedPipe
	}
	p.mu.Lock()
	c := make([]byte, len(b))
	copy(c, b)
	p.clientWrote = append(p.clientWrote, c)
	p.mu.Unlock()
	return len(b), nil
}

func (p *fakePTY) Resize(cols, rows uint16) error {
	if p.closed.Load() {
		return io.ErrClosedPipe
	}
	p.mu.Lock()
	p.cols, p.rows = cols, rows
	p.mu.Unlock()
	select {
	case p.resizedCh <- [2]uint16{cols, rows}:
	default:
	}
	return nil
}

func (p *fakePTY) Wait() (int, error) {
	p.waitCount.Add(1)
	<-p.waitDone
	if p.waitDelay > 0 {
		time.Sleep(p.waitDelay)
	}
	return int(atomic.LoadInt32(&p.exitCode)), nil
}

func (p *fakePTY) Close() error {
	p.closeOnce.Do(func() {
		p.closed.Store(true)
		close(p.closeCh)
		close(p.childToClient)
		p.waitOnce.Do(func() { close(p.waitDone) })
	})
	return nil
}

// pushChildOutput simulates the shell writing bytes to its stdout/stderr.
func (p *fakePTY) pushChildOutput(b []byte) {
	select {
	case p.childToClient <- b:
	case <-time.After(time.Second):
		p.t.Fatalf("childToClient send timed out — readLoop not draining")
	}
}

func (p *fakePTY) writes() [][]byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([][]byte, len(p.clientWrote))
	copy(out, p.clientWrote)
	return out
}

func (p *fakePTY) size() (uint16, uint16) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cols, p.rows
}

// fakeSpawner records each spawn so tests can inspect injected env / cwd.
type fakeSpawner struct {
	t        *testing.T
	spawnsMu sync.Mutex
	spawns   []SpawnRequest
	make     func(*testing.T, SpawnRequest) (*fakePTY, error)
}

func (s *fakeSpawner) Start(req SpawnRequest) (PTY, error) {
	s.spawnsMu.Lock()
	s.spawns = append(s.spawns, req)
	s.spawnsMu.Unlock()
	pty, err := s.make(s.t, req)
	if err != nil {
		return nil, err
	}
	return pty, nil
}

func (s *fakeSpawner) lastRequest() SpawnRequest {
	s.spawnsMu.Lock()
	defer s.spawnsMu.Unlock()
	if len(s.spawns) == 0 {
		return SpawnRequest{}
	}
	return s.spawns[len(s.spawns)-1]
}

// helper: build a Manager with a default fake spawner and a single task.
type fixture struct {
	mgr     *Manager
	spawner *fakeSpawner
	tasks   map[string]TaskInfo
	now     func() time.Time
	clockMu sync.Mutex
	clock   time.Time
}

func newFixture(t *testing.T, opts ...func(*ManagerConfig)) *fixture {
	f := &fixture{
		tasks: map[string]TaskInfo{
			"task-1": {
				TaskID:         "task-1",
				WorkspaceID:    "ws-A",
				IssueID:        "issue-1",
				WorkDir:        t.TempDir(),
				PriorSessionID: "claude-session-xyz",
			},
		},
		clock: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
	}
	f.now = func() time.Time {
		f.clockMu.Lock()
		defer f.clockMu.Unlock()
		return f.clock
	}
	f.spawner = &fakeSpawner{
		t:    t,
		make: func(tt *testing.T, req SpawnRequest) (*fakePTY, error) { return newFakePTY(tt, req.Cols, req.Rows), nil },
	}
	cfg := ManagerConfig{
		ShellPath:   "/usr/bin/bash",
		ShellArgs:   []string{"-l"},
		IdleTimeout: 0,
		Spawner:     f.spawner,
		Now:         f.now,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	lookup := func(_ context.Context, id string) (TaskInfo, error) {
		info, ok := f.tasks[id]
		if !ok {
			return TaskInfo{}, ErrTaskNotFound
		}
		return info, nil
	}
	f.mgr = NewManager(cfg, lookup)
	return f
}

func (f *fixture) advance(d time.Duration) {
	f.clockMu.Lock()
	f.clock = f.clock.Add(d)
	f.clockMu.Unlock()
}

// drainPTY pulls the *fakePTY back out of the spawner so tests can drive it.
func (f *fixture) lastPTY(t *testing.T) *fakePTY {
	t.Helper()
	req := f.spawner.lastRequest()
	if req.Shell == "" {
		t.Fatal("no spawn recorded")
	}
	// The Spawner.make closure always returns a *fakePTY; the manager
	// wraps it as a PTY interface and we don't retain the concrete in
	// the manager. Re-acquire via the registry by walking sessions.
	for _, id := range f.mgr.Sessions() {
		s, err := f.mgr.Get(id)
		if err == nil {
			if fp, ok := s.pty.(*fakePTY); ok {
				return fp
			}
		}
	}
	t.Fatal("no fake PTY found in any registered session")
	return nil
}

func TestManager_OpenSpawnsWithInjectedEnvAndCwd(t *testing.T) {
	f := newFixture(t)
	defer f.mgr.Close()

	sess, err := f.mgr.Open(context.Background(), OpenParams{
		TaskID:      "task-1",
		WorkspaceID: "ws-A",
		UserID:      "user-42",
		Cols:        120,
		Rows:        40,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if sess.ID() == "" {
		t.Fatal("session ID empty")
	}
	if got := sess.WorkDir(); got != f.tasks["task-1"].WorkDir {
		t.Errorf("workdir = %q, want %q", got, f.tasks["task-1"].WorkDir)
	}

	req := f.spawner.lastRequest()
	if req.Cwd != f.tasks["task-1"].WorkDir {
		t.Errorf("spawn cwd = %q, want %q", req.Cwd, f.tasks["task-1"].WorkDir)
	}
	if req.Cols != 120 || req.Rows != 40 {
		t.Errorf("spawn size = %dx%d, want 120x40", req.Cols, req.Rows)
	}

	wantEnv := map[string]string{
		"MULTICA_WORKSPACE_ID": "ws-A",
		"MULTICA_TASK_ID":      "task-1",
		"MULTICA_ISSUE_ID":     "issue-1",
		"MULTICA_USER_ID":      "user-42",
		"CLAUDE_SESSION_ID":    "claude-session-xyz",
	}
	envMap := map[string]string{}
	for _, kv := range req.Env {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				envMap[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	for k, want := range wantEnv {
		if got := envMap[k]; got != want {
			t.Errorf("env %s = %q, want %q", k, got, want)
		}
	}
}

func TestManager_DefaultSize(t *testing.T) {
	f := newFixture(t)
	defer f.mgr.Close()

	_, err := f.mgr.Open(context.Background(), OpenParams{
		TaskID:      "task-1",
		WorkspaceID: "ws-A",
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	req := f.spawner.lastRequest()
	if req.Cols != 80 || req.Rows != 24 {
		t.Errorf("default size = %dx%d, want 80x24", req.Cols, req.Rows)
	}
}

func TestManager_RejectsCrossWorkspace(t *testing.T) {
	f := newFixture(t)
	defer f.mgr.Close()

	_, err := f.mgr.Open(context.Background(), OpenParams{
		TaskID:      "task-1",
		WorkspaceID: "ws-B-not-the-tasks-workspace",
	})
	if !errors.Is(err, ErrWorkspaceMismatch) {
		t.Fatalf("Open err = %v, want ErrWorkspaceMismatch", err)
	}
	if got := len(f.mgr.Sessions()); got != 0 {
		t.Errorf("Sessions after rejected open = %d, want 0", got)
	}
}

func TestManager_OpenWithInfoBypassesLookup(t *testing.T) {
	// Manager is built with a nil lookup; OpenWithInfo must still succeed
	// when the server-supplied TaskInfo is well-formed. This is the path
	// daemonws hits in production — the daemon has no task cache, so the
	// server resolves task metadata and embeds it on the protocol.
	spawner := &fakeSpawner{
		t:    t,
		make: func(tt *testing.T, req SpawnRequest) (*fakePTY, error) { return newFakePTY(tt, req.Cols, req.Rows), nil },
	}
	mgr := NewManager(ManagerConfig{
		ShellPath: "/usr/bin/bash",
		ShellArgs: []string{"-l"},
		Spawner:   spawner,
	}, nil)
	defer mgr.Close()

	dir := t.TempDir()
	info := TaskInfo{
		TaskID:         "task-via-ws",
		WorkspaceID:    "ws-A",
		IssueID:        "issue-1",
		WorkDir:        dir,
		PriorSessionID: "claude-session-xyz",
	}
	sess, err := mgr.OpenWithInfo(context.Background(), info, OpenParams{
		TaskID:      "task-via-ws",
		WorkspaceID: "ws-A",
		UserID:      "user-1",
	})
	if err != nil {
		t.Fatalf("OpenWithInfo: %v", err)
	}
	if sess.WorkDir() != dir {
		t.Errorf("WorkDir = %q, want %q", sess.WorkDir(), dir)
	}
	req := spawner.lastRequest()
	if req.Cwd != dir {
		t.Errorf("spawn cwd = %q, want %q", req.Cwd, dir)
	}
	gotEnv := map[string]string{}
	for _, kv := range req.Env {
		eq := -1
		for i, c := range kv {
			if c == '=' {
				eq = i
				break
			}
		}
		if eq > 0 {
			gotEnv[kv[:eq]] = kv[eq+1:]
		}
	}
	if gotEnv["MULTICA_WORKSPACE_ID"] != "ws-A" {
		t.Errorf("MULTICA_WORKSPACE_ID = %q, want ws-A", gotEnv["MULTICA_WORKSPACE_ID"])
	}
	if gotEnv["MULTICA_ISSUE_ID"] != "issue-1" {
		t.Errorf("MULTICA_ISSUE_ID = %q, want issue-1", gotEnv["MULTICA_ISSUE_ID"])
	}
	if gotEnv["MULTICA_TASK_ID"] != "task-via-ws" {
		t.Errorf("MULTICA_TASK_ID = %q, want task-via-ws", gotEnv["MULTICA_TASK_ID"])
	}
	if gotEnv["CLAUDE_SESSION_ID"] != "claude-session-xyz" {
		t.Errorf("CLAUDE_SESSION_ID = %q, want claude-session-xyz", gotEnv["CLAUDE_SESSION_ID"])
	}
}

func TestManager_OpenWithInfoRejectsCrossWorkspace(t *testing.T) {
	// OpenWithInfo skips the lookup, but the workspace_id check on the
	// caller-supplied OpenParams vs. the supplied TaskInfo still has to
	// run — otherwise a misrouted frame from one workspace could attach
	// to another workspace's workdir.
	spawner := &fakeSpawner{
		t:    t,
		make: func(tt *testing.T, req SpawnRequest) (*fakePTY, error) { return newFakePTY(tt, req.Cols, req.Rows), nil },
	}
	mgr := NewManager(ManagerConfig{Spawner: spawner}, nil)
	defer mgr.Close()

	_, err := mgr.OpenWithInfo(context.Background(), TaskInfo{
		TaskID:      "task-via-ws",
		WorkspaceID: "ws-A",
		WorkDir:     t.TempDir(),
	}, OpenParams{
		TaskID:      "task-via-ws",
		WorkspaceID: "ws-B",
	})
	if !errors.Is(err, ErrWorkspaceMismatch) {
		t.Fatalf("err = %v, want ErrWorkspaceMismatch", err)
	}
	if got := len(mgr.Sessions()); got != 0 {
		t.Errorf("Sessions after rejected OpenWithInfo = %d, want 0", got)
	}
}

func TestManager_RejectsUnknownTask(t *testing.T) {
	f := newFixture(t)
	defer f.mgr.Close()

	_, err := f.mgr.Open(context.Background(), OpenParams{
		TaskID:      "does-not-exist",
		WorkspaceID: "ws-A",
	})
	if !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("Open err = %v, want ErrTaskNotFound", err)
	}
}

func TestSession_DataRoundTrip(t *testing.T) {
	f := newFixture(t)
	defer f.mgr.Close()

	sess, err := f.mgr.Open(context.Background(), OpenParams{
		TaskID:      "task-1",
		WorkspaceID: "ws-A",
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	pty := f.lastPTY(t)

	// client → child
	if _, err := sess.Write([]byte("ls -al\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// child → client
	pty.pushChildOutput([]byte("total 0\n"))

	select {
	case got := <-sess.Output():
		if string(got) != "total 0\n" {
			t.Errorf("Output chunk = %q, want %q", got, "total 0\n")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Output chunk")
	}

	writes := pty.writes()
	if len(writes) != 1 || string(writes[0]) != "ls -al\n" {
		t.Errorf("recorded writes = %#v, want one 'ls -al\\n'", writes)
	}
}

func TestSession_Resize(t *testing.T) {
	f := newFixture(t)
	defer f.mgr.Close()

	sess, err := f.mgr.Open(context.Background(), OpenParams{
		TaskID:      "task-1",
		WorkspaceID: "ws-A",
		Cols:        80, Rows: 24,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	pty := f.lastPTY(t)

	if err := sess.Resize(132, 50); err != nil {
		t.Fatalf("Resize: %v", err)
	}

	c, r := sess.Size()
	if c != 132 || r != 50 {
		t.Errorf("Size = %dx%d, want 132x50", c, r)
	}
	gc, gr := pty.size()
	if gc != 132 || gr != 50 {
		t.Errorf("PTY size = %dx%d, want 132x50", gc, gr)
	}
}

func TestSession_CloseDeregistersAndDelivers(t *testing.T) {
	f := newFixture(t)
	defer f.mgr.Close()

	sess, err := f.mgr.Open(context.Background(), OpenParams{
		TaskID:      "task-1",
		WorkspaceID: "ws-A",
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	id := sess.ID()

	sess.Close("user_requested")

	select {
	case info := <-sess.ExitC():
		if info.Reason != "user_requested" {
			t.Errorf("exit reason = %q, want user_requested", info.Reason)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ExitC")
	}

	// Output should close once exit fires; verify by ranging.
	drained := false
	for range sess.Output() {
		drained = true
	}
	_ = drained

	<-sess.Done()

	// Session must be deregistered.
	if _, err := f.mgr.Get(id); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("Get after Close = %v, want ErrSessionNotFound", err)
	}
}

func TestManager_IdleTimeoutSweep(t *testing.T) {
	f := newFixture(t, func(c *ManagerConfig) {
		c.IdleTimeout = 30 * time.Minute
	})
	defer f.mgr.Close()

	sess, err := f.mgr.Open(context.Background(), OpenParams{
		TaskID:      "task-1",
		WorkspaceID: "ws-A",
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// 29 minutes — still active.
	f.advance(29 * time.Minute)
	f.mgr.CheckIdle()
	if _, err := f.mgr.Get(sess.ID()); err != nil {
		t.Fatalf("session evicted before idle timeout: %v", err)
	}

	// Cross the threshold.
	f.advance(2 * time.Minute)
	f.mgr.CheckIdle()

	select {
	case info := <-sess.ExitC():
		if info.Reason != "idle_timeout" {
			t.Errorf("exit reason = %q, want idle_timeout", info.Reason)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for idle close")
	}

	if _, err := f.mgr.Get(sess.ID()); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("session not deregistered after idle sweep")
	}
}

func TestManager_CloseTearsDownAllSessions(t *testing.T) {
	f := newFixture(t)
	s1, err := f.mgr.Open(context.Background(), OpenParams{TaskID: "task-1", WorkspaceID: "ws-A"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s2, err := f.mgr.Open(context.Background(), OpenParams{TaskID: "task-1", WorkspaceID: "ws-A"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	f.mgr.Close()

	for _, s := range []*PtySession{s1, s2} {
		select {
		case <-s.Done():
		case <-time.After(2 * time.Second):
			t.Fatalf("session %s did not tear down", s.ID())
		}
	}
	if got := len(f.mgr.Sessions()); got != 0 {
		t.Errorf("Sessions after Manager.Close = %d, want 0", got)
	}

	// Subsequent opens must be rejected.
	if _, err := f.mgr.Open(context.Background(), OpenParams{TaskID: "task-1", WorkspaceID: "ws-A"}); !errors.Is(err, ErrManagerClosed) {
		t.Errorf("Open after Close = %v, want ErrManagerClosed", err)
	}
}

func TestSession_CloseWithFullOutputBufferDoesNotPanic(t *testing.T) {
	// Regression: Close used to race with readLoop's "output <- chunk"
	// when the channel was full. waitLoop closed output unconditionally,
	// which could panic on send-to-closed-channel. The new lifecycle
	// has waitLoop wait on a WaitGroup so readLoop's blocked send
	// unblocks via <-stop before the close runs.
	f := newFixture(t)
	defer f.mgr.Close()
	// Override on the existing spawner so newFixture's wiring (and
	// f.spawner.lastRequest tracking) still works.
	f.spawner.make = func(tt *testing.T, req SpawnRequest) (*fakePTY, error) {
		p := newFakePTY(tt, req.Cols, req.Rows)
		// Give the child-side queue plenty of room so the test can
		// saturate the *session* output buffer before childToClient
		// back-pressures the producer goroutine.
		p.childToClient = make(chan []byte, 256)
		return p, nil
	}

	sess, err := f.mgr.Open(context.Background(), OpenParams{TaskID: "task-1", WorkspaceID: "ws-A"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	pty := f.lastPTY(t)

	// Pump enough chunks to fill session.output (cap 64) and queue more
	// on childToClient; readLoop ends up blocked on output <- chunk.
	// Don't drain sess.Output() — that's the whole point. Producer runs
	// to completion (and exits) BEFORE Close, otherwise producer's send
	// races Close's pty.Close which closes childToClient.
	producerDone := make(chan struct{})
	go func() {
		defer close(producerDone)
		for i := 0; i < 200; i++ {
			select {
			case pty.childToClient <- []byte("x"):
			case <-time.After(50 * time.Millisecond):
				return
			}
		}
	}()
	<-producerDone

	// Should not panic, should not hang.
	sess.Close("user_requested")

	select {
	case <-sess.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done did not converge after Close with saturated output buffer")
	}

	if _, err := f.mgr.Get(sess.ID()); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("session not deregistered after Close")
	}

	// ExitC must have fired before Done — required by the Output() doc
	// contract ("channel closes after the child exits and a value has
	// been delivered on ExitC()").
	select {
	case info := <-sess.ExitC():
		if info.Reason != "user_requested" {
			t.Errorf("exit reason = %q, want user_requested", info.Reason)
		}
	default:
		t.Error("ExitC was empty after Done — finalize order violated")
	}
}

func TestManager_OpenPropagatesUnsupportedOS(t *testing.T) {
	// Regression: Manager.Open used fmt.Errorf("%w: %v", ErrSpawnFailed, err)
	// which swallowed the inner sentinel. The protocol layer needs
	// errors.Is to match both ErrSpawnFailed and ErrUnsupportedOS so it
	// can map to terminal.error code "unsupported_os" instead of a
	// generic "spawn_failed". Switched to double-%w; both must match.
	f := newFixture(t)
	defer f.mgr.Close()

	f.spawner.make = func(_ *testing.T, _ SpawnRequest) (*fakePTY, error) {
		return nil, ErrUnsupportedOS
	}

	_, err := f.mgr.Open(context.Background(), OpenParams{TaskID: "task-1", WorkspaceID: "ws-A"})
	if err == nil {
		t.Fatal("Open returned nil err with failing spawner")
	}
	if !errors.Is(err, ErrUnsupportedOS) {
		t.Errorf("errors.Is(err, ErrUnsupportedOS) = false; err = %v", err)
	}
	if !errors.Is(err, ErrSpawnFailed) {
		t.Errorf("errors.Is(err, ErrSpawnFailed) = false; err = %v", err)
	}
}

func TestSession_WriteUpdatesLastIO(t *testing.T) {
	f := newFixture(t, func(c *ManagerConfig) {
		c.IdleTimeout = 30 * time.Minute
	})
	defer f.mgr.Close()

	sess, err := f.mgr.Open(context.Background(), OpenParams{TaskID: "task-1", WorkspaceID: "ws-A"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	f.advance(20 * time.Minute)
	if _, err := sess.Write([]byte("echo hi\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	f.advance(20 * time.Minute) // total 40min, but 20 min since last IO
	f.mgr.CheckIdle()

	if _, err := f.mgr.Get(sess.ID()); err != nil {
		t.Fatalf("session evicted despite recent write: %v", err)
	}
}

func TestSession_DoneFiresAfterDeregister(t *testing.T) {
	// Locks the finalize-order contract from Round 2 review:
	//   ExitC → close(output) → onClose/deregister → close(done)
	// External waiters (daemonws bridge, GC hook, audit) use `<-Done()`
	// as the signal that the session is fully torn down. Any consumer
	// querying the manager immediately after Done() must observe the
	// session deregistered.
	f := newFixture(t)
	defer f.mgr.Close()

	sess, err := f.mgr.Open(context.Background(), OpenParams{TaskID: "task-1", WorkspaceID: "ws-A"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	id := sess.ID()

	sess.Close("user_requested")
	<-sess.Done()

	if _, err := f.mgr.Get(id); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("Get after <-Done() = %v, want ErrSessionNotFound (finalize order violated)", err)
	}
}

func TestManager_CloseConcurrentReentryWaitsForFinalize(t *testing.T) {
	// Regression for Round 3 review: a late Close() caller used to return
	// immediately when it saw m.closed==true, even though the first caller
	// was still in the middle of waiting for each session's Done(). That
	// broke the "Manager.Close returning means everything is drained"
	// contract for every caller but the first. With closeDone, all callers
	// now share the same finalize barrier.
	f := newFixture(t)
	f.spawner.make = func(tt *testing.T, req SpawnRequest) (*fakePTY, error) {
		p := newFakePTY(tt, req.Cols, req.Rows)
		// Long enough that the second goroutine's Close call definitely
		// observes the first one mid-flight rather than already-finished.
		p.waitDelay = 200 * time.Millisecond
		return p, nil
	}

	s1, err := f.mgr.Open(context.Background(), OpenParams{TaskID: "task-1", WorkspaceID: "ws-A"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	const callers = 4
	var wg sync.WaitGroup
	wg.Add(callers)
	sessionsAfter := make([]int, callers)
	doneClosed := make([]bool, callers)
	for i := 0; i < callers; i++ {
		i := i
		go func() {
			defer wg.Done()
			f.mgr.Close()
			sessionsAfter[i] = len(f.mgr.Sessions())
			select {
			case <-s1.Done():
				doneClosed[i] = true
			default:
				doneClosed[i] = false
			}
		}()
	}
	wg.Wait()

	for i := 0; i < callers; i++ {
		if sessionsAfter[i] != 0 {
			t.Errorf("caller %d: Sessions() after Close = %d, want 0", i, sessionsAfter[i])
		}
		if !doneClosed[i] {
			t.Errorf("caller %d: session Done not closed when Close returned", i)
		}
	}
}

func TestManager_OpenAfterCloseReapsSpawnedPTY(t *testing.T) {
	// Regression for Round 3 review: Manager.Open's cleanup path used to
	// only call pty.Close() when it lost the race with Manager.Close —
	// no Wait(), so on a real unix PTY the killed child stayed as a zombie
	// (waitLoop never ran because sess.start() never ran). The fix calls
	// pty.Wait() synchronously to reap.
	f := newFixture(t)

	inSpawn := make(chan struct{})
	releaseSpawn := make(chan struct{})
	spawnedPTY := make(chan *fakePTY, 1)
	f.spawner.make = func(tt *testing.T, req SpawnRequest) (*fakePTY, error) {
		close(inSpawn)
		<-releaseSpawn
		p := newFakePTY(tt, req.Cols, req.Rows)
		// Allow Wait() to return immediately once Close fires its waitDone.
		spawnedPTY <- p
		return p, nil
	}

	openDone := make(chan error, 1)
	go func() {
		_, err := f.mgr.Open(context.Background(), OpenParams{TaskID: "task-1", WorkspaceID: "ws-A"})
		openDone <- err
	}()

	// Wait until Open is parked inside the spawner, then close the manager
	// with zero registered sessions. Open will lose the race when it
	// reacquires the mu after the spawn returns.
	<-inSpawn
	f.mgr.Close()

	// Let the spawn finish; Open should now hit the closed-manager cleanup
	// path: pty.Close + pty.Wait + return ErrManagerClosed.
	close(releaseSpawn)

	select {
	case err := <-openDone:
		if !errors.Is(err, ErrManagerClosed) {
			t.Fatalf("Open after Close = %v, want ErrManagerClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Open did not return after Manager.Close + spawn release")
	}

	pty := <-spawnedPTY
	if got := pty.waitCount.Load(); got < 1 {
		t.Fatalf("pty.Wait() called %d times in cleanup path, want >=1 — child not reaped", got)
	}
}

func TestManager_CloseWaitsForSessionFinalize(t *testing.T) {
	// Manager.Close used to only wait for s.Close() (which just initiates
	// teardown — signals stop, closes the PTY). The waitLoop finalizer
	// could still be running after Manager.Close returned, leaving the
	// sessions map non-empty briefly. With Round 2 review's fix, each
	// goroutine in Manager.Close additionally `<-s.Done()` so the manager
	// is fully drained by the time Close returns. We inject a Wait delay
	// to make the difference observable: without the fix, the session map
	// is still populated when Manager.Close returns and `<-s.Done()` would
	// block.
	f := newFixture(t)
	f.spawner.make = func(tt *testing.T, req SpawnRequest) (*fakePTY, error) {
		p := newFakePTY(tt, req.Cols, req.Rows)
		p.waitDelay = 150 * time.Millisecond
		return p, nil
	}

	s1, err := f.mgr.Open(context.Background(), OpenParams{TaskID: "task-1", WorkspaceID: "ws-A"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s2, err := f.mgr.Open(context.Background(), OpenParams{TaskID: "task-1", WorkspaceID: "ws-A"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	f.mgr.Close()

	// After Close returns: registry empty AND every Done is already closed.
	if got := len(f.mgr.Sessions()); got != 0 {
		t.Errorf("Sessions after Manager.Close = %d, want 0", got)
	}
	for _, s := range []*PtySession{s1, s2} {
		select {
		case <-s.Done():
		default:
			t.Errorf("session %s Done not closed when Manager.Close returned", s.ID())
		}
		if _, err := f.mgr.Get(s.ID()); !errors.Is(err, ErrSessionNotFound) {
			t.Errorf("session %s still registered after Manager.Close: %v", s.ID(), err)
		}
	}
}
