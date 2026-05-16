package daemonws

import (
	"encoding/json"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type collectingSink struct {
	frames [][]byte
}

func (s *collectingSink) Deliver(frame []byte) bool {
	cp := make([]byte, len(frame))
	copy(cp, frame)
	s.frames = append(s.frames, cp)
	return true
}

func TestTerminalRouter_RoutesByRequestThenSession(t *testing.T) {
	router := NewTerminalRouter()
	sink := &collectingSink{}
	router.Register("req-1", sink)

	// terminal.error before the daemon picked a session_id: routed on
	// request_id. This is the failure path browsers see when the daemon
	// can't spawn a PTY (e.g. ErrUnsupportedOS on windows).
	errFrame := mustEncode(t, protocol.MessageTypeTerminalError, protocol.TerminalErrorPayload{
		RequestID: "req-1",
		Code:      protocol.TerminalErrorCodeUnsupportedOS,
		Message:   "no PTY on windows",
	})
	router.Route(errFrame, protocol.MessageTypeTerminalError, mustPayload(t, errFrame))

	if got, want := len(sink.frames), 1; got != want {
		t.Fatalf("delivered = %d, want %d", got, want)
	}

	// Re-key to session_id after a hypothetical terminal.opened.
	router.Register("sess-1", sink)
	router.Unregister("req-1")

	dataFrame := mustEncode(t, protocol.MessageTypeTerminalData, protocol.TerminalDataPayload{
		SessionID: "sess-1",
		DataB64:   "Zm9vYmFy",
	})
	router.Route(dataFrame, protocol.MessageTypeTerminalData, mustPayload(t, dataFrame))
	if got, want := len(sink.frames), 2; got != want {
		t.Fatalf("after data delivered = %d, want %d", got, want)
	}

	// Frames for an unknown session must drop silently — never panic, and
	// never leak into the wrong sink.
	strayFrame := mustEncode(t, protocol.MessageTypeTerminalExit, protocol.TerminalExitPayload{
		SessionID: "sess-2-unknown",
		ExitCode:  0,
	})
	router.Route(strayFrame, protocol.MessageTypeTerminalExit, mustPayload(t, strayFrame))
	if got, want := len(sink.frames), 2; got != want {
		t.Fatalf("stray frame delivered to wrong sink: %d", got)
	}
}

func TestTerminalRouter_UnknownSessionDropsSilently(t *testing.T) {
	router := NewTerminalRouter()
	frame := mustEncode(t, protocol.MessageTypeTerminalData, protocol.TerminalDataPayload{
		SessionID: "ghost",
		DataB64:   "Zm9v",
	})
	// Should not panic / not deliver anywhere.
	router.Route(frame, protocol.MessageTypeTerminalData, mustPayload(t, frame))
}

func mustEncode(t *testing.T, msgType string, payload any) []byte {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	frame, err := json.Marshal(protocol.Message{Type: msgType, Payload: raw})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return frame
}

func mustPayload(t *testing.T, envelope []byte) json.RawMessage {
	t.Helper()
	var m protocol.Message
	if err := json.Unmarshal(envelope, &m); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	return m.Payload
}
