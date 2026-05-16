package protocol

import "encoding/json"

// Message is the envelope for all WebSocket messages.
type Message struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// TaskDispatchPayload is sent from server to daemon when a task is assigned.
type TaskDispatchPayload struct {
	TaskID      string `json:"task_id"`
	IssueID     string `json:"issue_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// TaskAvailablePayload is sent from server to daemon as a wakeup hint. The
// daemon still claims work through the existing HTTP claim endpoint.
type TaskAvailablePayload struct {
	RuntimeID string `json:"runtime_id"`
	TaskID    string `json:"task_id,omitempty"`
}

// TaskProgressPayload is sent from daemon to server during task execution.
type TaskProgressPayload struct {
	TaskID  string `json:"task_id"`
	Summary string `json:"summary"`
	Step    int    `json:"step,omitempty"`
	Total   int    `json:"total,omitempty"`
}

// TaskCompletedPayload is sent from daemon to server when a task finishes.
type TaskCompletedPayload struct {
	TaskID string `json:"task_id"`
	PRURL  string `json:"pr_url,omitempty"`
	Output string `json:"output,omitempty"`
}

// TaskMessagePayload represents a single agent execution message (tool call, text, etc.)
type TaskMessagePayload struct {
	TaskID  string         `json:"task_id"`
	IssueID string         `json:"issue_id,omitempty"`
	Seq     int            `json:"seq"`
	Type    string         `json:"type"`              // "text", "tool_use", "tool_result", "error"
	Tool    string         `json:"tool,omitempty"`    // tool name for tool_use/tool_result
	Content string         `json:"content,omitempty"` // text content
	Input   map[string]any `json:"input,omitempty"`   // tool input (tool_use only)
	Output  string         `json:"output,omitempty"`  // tool output (tool_result only)
}

// DaemonRegisterPayload is sent from daemon to server on connection.
type DaemonRegisterPayload struct {
	DaemonID string        `json:"daemon_id"`
	AgentID  string        `json:"agent_id"`
	Runtimes []RuntimeInfo `json:"runtimes"`
}

// RuntimeInfo describes an available agent runtime on the daemon's machine.
type RuntimeInfo struct {
	Type    string `json:"type"`
	Version string `json:"version"`
	Status  string `json:"status"`
}

// ChatMessagePayload is broadcast when a new chat message is created.
type ChatMessagePayload struct {
	ChatSessionID string `json:"chat_session_id"`
	MessageID     string `json:"message_id"`
	Role          string `json:"role"`
	Content       string `json:"content"`
	TaskID        string `json:"task_id,omitempty"`
	CreatedAt     string `json:"created_at"`
}

// ChatDonePayload is broadcast when an agent finishes responding to a chat
// message. Carries the freshly-persisted assistant ChatMessage so the client
// can write it into the messages cache inline — avoids a refetch round-trip
// during the live-timeline → AssistantMessage handoff that previously caused
// a visible flicker (#2123).
type ChatDonePayload struct {
	ChatSessionID string `json:"chat_session_id"`
	TaskID        string `json:"task_id"`
	MessageID     string `json:"message_id,omitempty"`
	Content       string `json:"content,omitempty"`
	ElapsedMs     int64  `json:"elapsed_ms,omitempty"`
	CreatedAt     string `json:"created_at,omitempty"`
}

// ChatSessionReadPayload is broadcast when the creator marks a session as read.
// Fires to other devices so their unread counts stay in sync.
type ChatSessionReadPayload struct {
	ChatSessionID string `json:"chat_session_id"`
}

// ChatSessionDeletedPayload is broadcast when a chat session is hard-deleted
// so other tabs/devices drop it from their session lists and reset the active
// pointer if it referenced the deleted session.
type ChatSessionDeletedPayload struct {
	ChatSessionID string `json:"chat_session_id"`
}

// ChatSessionUpdatedPayload is broadcast when a user-editable field on a
// chat session changes (today: title via inline rename). Other tabs/devices
// patch the session row in their cached list so the dropdown stays in sync
// without a full refetch.
type ChatSessionUpdatedPayload struct {
	ChatSessionID string `json:"chat_session_id"`
	Title         string `json:"title"`
	UpdatedAt     string `json:"updated_at"`
}

// DaemonHeartbeatRequestPayload is sent from daemon to server over WebSocket
// to update last_seen_at and pull pending actions for a single runtime.
// Mirrors the body of POST /api/daemon/heartbeat so both transports share
// identical semantics.
type DaemonHeartbeatRequestPayload struct {
	RuntimeID string `json:"runtime_id"`
}

// DaemonHeartbeatAckPayload is the server's reply to DaemonHeartbeatRequestPayload.
// JSON shape mirrors the HTTP heartbeat response so daemon code can decode either.
//
// RuntimeGone is the WebSocket replacement for the HTTP 404 "runtime not found"
// response. When the server discovers the runtime row was deleted (UI delete,
// 7-day offline GC), it sends back an ack with Status=HeartbeatStatusRuntimeGone
// and RuntimeGone=true rather than tearing down the connection with an error.
// The daemon reads this signal, prunes the stale runtime from its local state
// and re-registers; without it the dead UUID would keep heartbeating until the
// daemon process restarts.
type DaemonHeartbeatAckPayload struct {
	RuntimeID               string                                  `json:"runtime_id"`
	Status                  string                                  `json:"status"`
	RuntimeGone             bool                                    `json:"runtime_gone,omitempty"`
	PendingUpdate           *DaemonHeartbeatPendingUpdate           `json:"pending_update,omitempty"`
	PendingModelList        *DaemonHeartbeatPendingModelList        `json:"pending_model_list,omitempty"`
	PendingLocalSkills      *DaemonHeartbeatPendingLocalSkills      `json:"pending_local_skills,omitempty"`
	PendingLocalSkillImport *DaemonHeartbeatPendingLocalSkillImport `json:"pending_local_skill_import,omitempty"`
}

// HeartbeatStatusRuntimeGone is the ack Status used when the runtime row no
// longer exists server-side. Companion to DaemonHeartbeatAckPayload.RuntimeGone.
const HeartbeatStatusRuntimeGone = "runtime_gone"

// DaemonHeartbeatPendingUpdate describes a CLI-update action the daemon
// should run for the runtime.
type DaemonHeartbeatPendingUpdate struct {
	ID            string `json:"id"`
	TargetVersion string `json:"target_version"`
}

// DaemonHeartbeatPendingModelList describes a request for the daemon to
// enumerate the runtime's supported models.
type DaemonHeartbeatPendingModelList struct {
	ID string `json:"id"`
}

// DaemonHeartbeatPendingLocalSkills describes a request for the runtime's
// local-skill inventory.
type DaemonHeartbeatPendingLocalSkills struct {
	ID string `json:"id"`
}

// DaemonHeartbeatPendingLocalSkillImport describes a request to import a
// specific runtime local skill.
type DaemonHeartbeatPendingLocalSkillImport struct {
	ID       string `json:"id"`
	SkillKey string `json:"skill_key"`
}

// Terminal WS message types. These flow over the existing daemonws hub
// between client (web/desktop/CLI) and daemon. Bytes payloads are base64
// encoded so they can travel as JSON text frames without binary framing.
const (
	// TerminalOpen — client → daemon: request a new PTY session bound to a task workdir.
	MessageTypeTerminalOpen = "terminal.open"
	// TerminalOpened — daemon → client: ack carrying the session_id and resolved workdir.
	MessageTypeTerminalOpened = "terminal.opened"
	// TerminalData — bidirectional: PTY stdin (client→daemon) / stdout+stderr (daemon→client).
	MessageTypeTerminalData = "terminal.data"
	// TerminalResize — client → daemon: window-size change.
	MessageTypeTerminalResize = "terminal.resize"
	// TerminalClose — bidirectional: explicit teardown request / ack.
	MessageTypeTerminalClose = "terminal.close"
	// TerminalExit — daemon → client: child process exited; carries exit code and optional reason.
	MessageTypeTerminalExit = "terminal.exit"
	// TerminalError — daemon → client: open/resize/etc. failed; carries human-readable code+message.
	MessageTypeTerminalError = "terminal.error"
)

// TerminalOpenPayload requests a PTY session bound to the given task's
// workdir. WorkspaceID is the workspace the caller is acting in; the daemon
// must reject if it does not match the task's workspace.
//
// The server resolves WorkDir / IssueID / PriorSessionID from its own DB
// (the daemon has no persistent task cache) and embeds them here before
// forwarding to the daemon. The daemon trusts these fields because the
// daemonws connection is already authenticated and scoped — but it still
// rechecks WorkspaceID against the request body to catch a misrouted frame.
type TerminalOpenPayload struct {
	RequestID      string `json:"request_id"`
	TaskID         string `json:"task_id"`
	WorkspaceID    string `json:"workspace_id"`
	UserID         string `json:"user_id,omitempty"`
	IssueID        string `json:"issue_id,omitempty"`
	WorkDir        string `json:"work_dir,omitempty"`
	PriorSessionID string `json:"prior_session_id,omitempty"`
	Cols           uint16 `json:"cols"`
	Rows           uint16 `json:"rows"`
}

// TerminalOpenedPayload echoes the request_id and carries the session_id the
// client must include on subsequent data/resize/close frames.
type TerminalOpenedPayload struct {
	RequestID string `json:"request_id"`
	SessionID string `json:"session_id"`
	WorkDir   string `json:"work_dir"`
	Shell     string `json:"shell"`
}

// TerminalDataPayload carries raw PTY bytes in base64.
type TerminalDataPayload struct {
	SessionID string `json:"session_id"`
	DataB64   string `json:"data_b64"`
}

// TerminalResizePayload updates the PTY window size.
type TerminalResizePayload struct {
	SessionID string `json:"session_id"`
	Cols      uint16 `json:"cols"`
	Rows      uint16 `json:"rows"`
}

// TerminalClosePayload requests teardown. Reason is informational.
type TerminalClosePayload struct {
	SessionID string `json:"session_id"`
	Reason    string `json:"reason,omitempty"`
}

// TerminalExitPayload signals the child process exited.
type TerminalExitPayload struct {
	SessionID string `json:"session_id"`
	ExitCode  int    `json:"exit_code"`
	Reason    string `json:"reason,omitempty"`
}

// Terminal error codes returned in TerminalErrorPayload.Code.
const (
	TerminalErrorCodeWorkspaceMismatch = "workspace_mismatch"
	TerminalErrorCodeTaskNotFound      = "task_not_found"
	TerminalErrorCodeSessionNotFound   = "session_not_found"
	TerminalErrorCodeUnsupportedOS     = "unsupported_os"
	TerminalErrorCodeSpawnFailed       = "spawn_failed"
	TerminalErrorCodeInternal          = "internal"
)

// TerminalErrorPayload reports a failure. RequestID is set when the error
// is a response to a specific open request; SessionID is set when it
// references an already-established session.
type TerminalErrorPayload struct {
	RequestID string `json:"request_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Code      string `json:"code"`
	Message   string `json:"message"`
}
