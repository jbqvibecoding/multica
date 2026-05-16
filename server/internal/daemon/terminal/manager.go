package terminal

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

// DefaultIdleTimeout is the recommended IdleTimeout for production
// daemon wiring. Callers must set ManagerConfig.IdleTimeout to this
// (or any positive duration) explicitly; zero/negative disables the
// idle sweep.
const DefaultIdleTimeout = 60 * time.Minute

// TaskInfo is the subset of task state the Manager needs to set up a PTY.
// The daemon resolves a TaskID into TaskInfo via TaskLookup at open time.
type TaskInfo struct {
	TaskID         string
	WorkspaceID    string
	IssueID        string
	WorkDir        string
	PriorSessionID string // injected as CLAUDE_SESSION_ID for `claude --resume`
}

// TaskLookup resolves a TaskID into the workdir + workspace required to
// open a PTY. Returns ErrTaskNotFound when the task is unknown. Lookups
// hit the daemon's local task cache in production; tests supply a stub.
type TaskLookup func(ctx context.Context, taskID string) (TaskInfo, error)

// OpenParams is the input to Manager.Open.
type OpenParams struct {
	// TaskID identifies the workdir the PTY should run in.
	TaskID string
	// WorkspaceID is the workspace the caller is acting on behalf of.
	// Open rejects the request if it does not match the task's workspace
	// (cross-workspace ACL — clients never see other workspaces' workdirs).
	WorkspaceID string
	// UserID is the human user who opened the terminal. Logged in audit
	// records; the PTY itself runs as the daemon process owner.
	UserID string
	// Cols/Rows seed the initial PTY window size. Zero values default to 80x24.
	Cols uint16
	Rows uint16
}

// ManagerConfig tunes Manager behaviour. Zero values are sensible defaults.
type ManagerConfig struct {
	// Shell to spawn for each session. Defaults to "bash" with "-l".
	// Overridable for tests; the production daemon hardcodes bash for now
	// (RFC open question #4 — shell selection deferred to a later release).
	ShellPath string
	ShellArgs []string

	// IdleTimeout closes a session that has had no I/O for this long.
	// Zero or negative disables the sweep entirely. Production daemon
	// wiring should pass DefaultIdleTimeout explicitly; we intentionally
	// don't default here so callers stay in control (the docs page for
	// this package previously said "0 disables" while NewManager silently
	// rewrote 0 to 60min — those two have to agree).
	IdleTimeout time.Duration

	// Spawner overrides PTY spawning. Defaults to ptyStartShell which
	// shells out to creack/pty. Tests inject a fake to avoid forking.
	Spawner Spawner

	// Now returns the current time. Defaults to time.Now. Tests inject a
	// fake clock to drive IdleTimeout deterministically.
	Now func() time.Time

	// Logger receives operational events. Defaults to slog.Default().
	Logger *slog.Logger
}

// Manager owns all live PtySessions on this daemon. It is safe for
// concurrent use.
type Manager struct {
	cfg    ManagerConfig
	lookup TaskLookup

	mu       sync.Mutex
	sessions map[string]*PtySession
	closed   bool
	// closeDone is closed by the first Close() caller AFTER finalize
	// finishes (every session deregistered, Done() closed). Subsequent
	// concurrent callers wait on it instead of racing past, so all
	// Close() returns share the same "manager fully drained" guarantee.
	closeDone chan struct{}
}

// NewManager constructs a Manager. lookup may be nil in tests that only
// exercise direct session APIs.
func NewManager(cfg ManagerConfig, lookup TaskLookup) *Manager {
	if cfg.ShellPath == "" {
		cfg.ShellPath = "bash"
		cfg.ShellArgs = []string{"-l"}
	}
	// IdleTimeout intentionally not defaulted — see ManagerConfig.
	if cfg.Spawner == nil {
		cfg.Spawner = realSpawner{}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Manager{
		cfg:       cfg,
		lookup:    lookup,
		sessions:  make(map[string]*PtySession),
		closeDone: make(chan struct{}),
	}
}

// Open spawns a new PTY session for the given task. The returned
// session is also registered with the manager and retrievable via Get.
func (m *Manager) Open(ctx context.Context, p OpenParams) (*PtySession, error) {
	if m.lookup == nil {
		return nil, fmt.Errorf("terminal: Manager has no TaskLookup configured")
	}
	info, err := m.lookup(ctx, p.TaskID)
	if err != nil {
		return nil, err
	}
	if info.WorkspaceID != p.WorkspaceID {
		return nil, ErrWorkspaceMismatch
	}
	if info.WorkDir == "" {
		return nil, ErrTaskNotFound
	}
	return m.openWith(info, p)
}

// OpenWithInfo spawns a PTY against a caller-supplied TaskInfo, bypassing
// the configured TaskLookup. Production daemon wiring takes this path
// because the server resolves task metadata from its DB before forwarding
// terminal.open over the daemonws hub — the daemon trusts the server-
// authenticated payload and does not have its own task cache. The
// workspace mismatch check still runs so a server bug or a relayed frame
// cannot cross workspace boundaries.
func (m *Manager) OpenWithInfo(_ context.Context, info TaskInfo, p OpenParams) (*PtySession, error) {
	if info.WorkspaceID != p.WorkspaceID {
		return nil, ErrWorkspaceMismatch
	}
	if info.WorkDir == "" {
		return nil, ErrTaskNotFound
	}
	if info.TaskID == "" {
		info.TaskID = p.TaskID
	}
	return m.openWith(info, p)
}

func (m *Manager) openWith(info TaskInfo, p OpenParams) (*PtySession, error) {
	cols, rows := normalizeSize(p.Cols, p.Rows)
	env := buildEnv(info, p.UserID)

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, ErrManagerClosed
	}
	m.mu.Unlock()

	startedAt := m.cfg.Now()
	pty, err := m.cfg.Spawner.Start(SpawnRequest{
		Shell:   m.cfg.ShellPath,
		Args:    m.cfg.ShellArgs,
		Cwd:     info.WorkDir,
		Env:     env,
		Cols:    cols,
		Rows:    rows,
		Started: startedAt,
	})
	if err != nil {
		// Double-%w so errors.Is matches both ErrSpawnFailed AND any
		// sentinel the spawner surfaced (notably ErrUnsupportedOS from
		// the windows stub — the protocol layer needs to distinguish
		// "no PTY on this OS" from generic spawn failures).
		return nil, fmt.Errorf("%w: %w", ErrSpawnFailed, err)
	}

	sess := &PtySession{
		id:          uuid.NewString(),
		taskID:      info.TaskID,
		workspaceID: info.WorkspaceID,
		issueID:     info.IssueID,
		workDir:     info.WorkDir,
		userID:      p.UserID,
		shellPath:   m.cfg.ShellPath,
		cols:        cols,
		rows:        rows,
		pty:         pty,
		output:      make(chan []byte, 64),
		exit:        make(chan ExitInfo, 1),
		done:        make(chan struct{}),
		stop:        make(chan struct{}),
		now:         m.cfg.Now,
		idleTimeout: m.cfg.IdleTimeout,
		startedAt:   startedAt,
		lastIO:      startedAt,
		logger:      m.cfg.Logger.With("session_id_pending", true, "task_id", info.TaskID),
		onClose:     func(id string) { m.deregister(id) },
	}
	sess.logger = m.cfg.Logger.With("session_id", sess.id, "task_id", info.TaskID)

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		_ = pty.Close()
		// We won that race: spawn succeeded but the manager closed before
		// we could register the session, so waitLoop never runs. Reap the
		// child synchronously here — pty.Close fires SIGHUP/SIGKILL but
		// only Wait() collects the exit status, otherwise the unix child
		// stays around as a zombie until the daemon process dies.
		_, _ = pty.Wait()
		return nil, ErrManagerClosed
	}
	m.sessions[sess.id] = sess
	m.mu.Unlock()

	sess.start()
	return sess, nil
}

// Get returns the session with the given id, or ErrSessionNotFound.
func (m *Manager) Get(id string) (*PtySession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[id]
	if !ok {
		return nil, ErrSessionNotFound
	}
	return sess, nil
}

// Sessions returns a snapshot of currently registered session IDs.
func (m *Manager) Sessions() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	return ids
}

// Close tears down every live session and refuses subsequent Open calls.
// Safe to call concurrently from multiple goroutines: the first caller
// runs the actual teardown, the rest block on closeDone until that
// teardown is fully observable. Every Close() return — first or Nth —
// thus carries the same "manager drained, every session finalized"
// guarantee that downstream GC/audit cleanup depends on.
func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		done := m.closeDone
		m.mu.Unlock()
		<-done
		return
	}
	m.closed = true
	live := make([]*PtySession, 0, len(m.sessions))
	for _, s := range m.sessions {
		live = append(live, s)
	}
	m.mu.Unlock()
	// Parallel: each session.Close blocks for the unix spawner's
	// SIGHUP→grace→SIGKILL window. Running serially would multiply
	// shutdown latency by N sessions. We additionally wait on each
	// session's Done() so Manager.Close returning is a hard guarantee
	// that every session finalized (output closed, deregistered, done
	// fired) — downstream GC/audit cleanup relies on this.
	var wg sync.WaitGroup
	for _, s := range live {
		wg.Add(1)
		go func(s *PtySession) {
			defer wg.Done()
			s.Close("manager_shutdown")
			<-s.Done()
		}(s)
	}
	wg.Wait()
	close(m.closeDone)
}

// CheckIdle walks every session and closes those whose idle interval
// has elapsed. The daemon's existing GC loop calls this periodically;
// each session also self-monitors via its own timer for cases where the
// outer loop runs at a coarser cadence than IdleTimeout.
func (m *Manager) CheckIdle() {
	if m.cfg.IdleTimeout <= 0 {
		return
	}
	m.mu.Lock()
	sessions := make([]*PtySession, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	m.mu.Unlock()

	now := m.cfg.Now()
	for _, s := range sessions {
		if now.Sub(s.LastIO()) >= m.cfg.IdleTimeout {
			s.Close("idle_timeout")
		}
	}
}

func (m *Manager) deregister(id string) {
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
}

func normalizeSize(cols, rows uint16) (uint16, uint16) {
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}
	return cols, rows
}

func buildEnv(info TaskInfo, userID string) []string {
	env := []string{
		"MULTICA_WORKSPACE_ID=" + info.WorkspaceID,
		"MULTICA_TASK_ID=" + info.TaskID,
	}
	if info.IssueID != "" {
		env = append(env, "MULTICA_ISSUE_ID="+info.IssueID)
	}
	if userID != "" {
		env = append(env, "MULTICA_USER_ID="+userID)
	}
	if info.PriorSessionID != "" {
		// Injected so `claude --resume $CLAUDE_SESSION_ID` continues the
		// same session that the agent run was using (see RFC §Resume).
		env = append(env, "CLAUDE_SESSION_ID="+info.PriorSessionID)
	}
	return env
}
