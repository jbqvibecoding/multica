"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { Terminal as XTerminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { getApi } from "@multica/core/api";
import { Button } from "@multica/ui/components/ui/button";
import "@xterm/xterm/css/xterm.css";

// Protocol message types — kept in lockstep with
// server/pkg/protocol/messages.go. Strings are stable across daemon /
// server / browser, so duplicating them client-side is OK; if we ever
// regenerate types from Go we can swap these out.
const MSG_TERMINAL_DATA = "terminal.data";
const MSG_TERMINAL_RESIZE = "terminal.resize";
const MSG_TERMINAL_CLOSE = "terminal.close";
const MSG_TERMINAL_OPENED = "terminal.opened";
const MSG_TERMINAL_EXIT = "terminal.exit";
const MSG_TERMINAL_ERROR = "terminal.error";

interface Envelope {
  type: string;
  payload: unknown;
}

interface OpenedPayload {
  request_id: string;
  session_id: string;
  work_dir: string;
  shell: string;
}

interface DataPayload {
  session_id: string;
  data_b64: string;
}

interface ExitPayload {
  session_id: string;
  exit_code: number;
  reason?: string;
}

interface ErrorPayload {
  request_id?: string;
  session_id?: string;
  code: string;
  message: string;
}

// Detect Electron — server-side render guard plus the desktop preload
// surface check. Mirrors the pattern used elsewhere in the desktop app;
// the Terminal panel is intentionally desktop-only because the daemon
// only runs on a developer machine.
function isDesktopRuntime(): boolean {
  return typeof window !== "undefined" && "desktopAPI" in window;
}

interface TerminalPanelProps {
  issueId: string;
  workspaceId: string;
}

export function TerminalPanel({ issueId, workspaceId }: TerminalPanelProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const termRef = useRef<XTerminal | null>(null);
  const fitRef = useRef<FitAddon | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const sessionIdRef = useRef<string>("");

  const [status, setStatus] = useState<
    "idle" | "connecting" | "connected" | "closed" | "error"
  >("idle");
  const [errorMessage, setErrorMessage] = useState<string>("");
  const [reconnectKey, setReconnectKey] = useState(0);

  const wsUrl = useMemo(() => deriveTerminalWsUrl(issueId, workspaceId), [
    issueId,
    workspaceId,
  ]);

  useEffect(() => {
    if (!isDesktopRuntime()) return;
    if (!containerRef.current) return;

    const term = new XTerminal({
      convertEol: true,
      cursorBlink: true,
      fontFamily:
        "ui-monospace, SFMono-Regular, Menlo, Monaco, 'Cascadia Mono', 'Roboto Mono', 'Courier New', monospace",
      fontSize: 13,
      theme: { background: "#0b0b0b", foreground: "#e6e6e6" },
      // Scrollback large enough to read a verbose `cargo build` or `git
      // log` without auto-clipping the top.
      scrollback: 5000,
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(containerRef.current);
    fit.fit();
    termRef.current = term;
    fitRef.current = fit;

    term.writeln("\x1b[90mconnecting to daemon…\x1b[0m");

    setStatus("connecting");
    const ws = new WebSocket(wsUrl);
    wsRef.current = ws;

    ws.onopen = () => {
      // Cookie auth carries the session by default. If we ever flip to
      // token-mode (no cookie), this is where we'd send an `auth` frame
      // mirroring realtime/ws-client.ts. Server falls back gracefully.
      setStatus("connected");
    };

    ws.onerror = () => {
      // The browser only surfaces a generic Event; the server sends a
      // structured terminal.error frame which we already render below.
      // Keep this minimal so we don't double-up the error UI.
      setStatus("error");
    };

    ws.onclose = (ev) => {
      setStatus("closed");
      term.writeln(
        `\r\n\x1b[90mconnection closed (code=${ev.code})${
          ev.reason ? ` reason=${ev.reason}` : ""
        }\x1b[0m`,
      );
    };

    ws.onmessage = (ev) => {
      let env: Envelope;
      try {
        env = JSON.parse(typeof ev.data === "string" ? ev.data : "");
      } catch {
        return;
      }
      switch (env.type) {
        case MSG_TERMINAL_OPENED: {
          const p = env.payload as OpenedPayload;
          sessionIdRef.current = p.session_id;
          term.writeln(
            `\x1b[90mattached to ${p.shell} (cwd: ${p.work_dir})\x1b[0m`,
          );
          // Send an initial resize matching the terminal's actual size,
          // because the server-side open uses default 80x24 until we tell
          // it otherwise.
          const cols = term.cols;
          const rows = term.rows;
          ws.send(
            JSON.stringify({
              type: MSG_TERMINAL_RESIZE,
              payload: {
                session_id: p.session_id,
                cols,
                rows,
              },
            }),
          );
          break;
        }
        case MSG_TERMINAL_DATA: {
          const p = env.payload as DataPayload;
          if (typeof p.data_b64 !== "string") break;
          const decoded = atobToUint8(p.data_b64);
          // xterm.js accepts Uint8Array; we avoid the latin1 round-trip
          // that would otherwise mangle UTF-8 PTY output.
          term.write(decoded);
          break;
        }
        case MSG_TERMINAL_EXIT: {
          const p = env.payload as ExitPayload;
          term.writeln(
            `\r\n\x1b[90mprocess exited (code=${p.exit_code}${
              p.reason ? `, reason=${p.reason}` : ""
            })\x1b[0m`,
          );
          ws.close();
          break;
        }
        case MSG_TERMINAL_ERROR: {
          const p = env.payload as ErrorPayload;
          setErrorMessage(`${p.code}: ${p.message}`);
          term.writeln(`\r\n\x1b[31m${p.code}: ${p.message}\x1b[0m`);
          break;
        }
      }
    };

    // Forward keystrokes as terminal.data with base64 of the UTF-8 bytes.
    const dataSub = term.onData((data) => {
      if (ws.readyState !== WebSocket.OPEN) return;
      if (!sessionIdRef.current) return;
      ws.send(
        JSON.stringify({
          type: MSG_TERMINAL_DATA,
          payload: {
            session_id: sessionIdRef.current,
            data_b64: utf8ToBase64(data),
          },
        }),
      );
    });

    const resizeSub = term.onResize(({ cols, rows }) => {
      if (ws.readyState !== WebSocket.OPEN) return;
      if (!sessionIdRef.current) return;
      ws.send(
        JSON.stringify({
          type: MSG_TERMINAL_RESIZE,
          payload: {
            session_id: sessionIdRef.current,
            cols,
            rows,
          },
        }),
      );
    });

    // Observe container size and re-fit so the PTY size tracks the panel
    // (the right sidebar can be resized at runtime).
    const ro = new ResizeObserver(() => {
      try {
        fit.fit();
      } catch {
        // fit() throws when the container has zero height during teardown;
        // ignore — the next mount will rebind.
      }
    });
    ro.observe(containerRef.current);

    return () => {
      dataSub.dispose();
      resizeSub.dispose();
      ro.disconnect();
      try {
        if (sessionIdRef.current && ws.readyState === WebSocket.OPEN) {
          ws.send(
            JSON.stringify({
              type: MSG_TERMINAL_CLOSE,
              payload: { session_id: sessionIdRef.current, reason: "panel_unmount" },
            }),
          );
        }
      } catch {
        // ws may be already closing; nothing to do.
      }
      ws.close();
      term.dispose();
      termRef.current = null;
      fitRef.current = null;
      wsRef.current = null;
      sessionIdRef.current = "";
    };
  }, [wsUrl, reconnectKey]);

  if (!isDesktopRuntime()) {
    return (
      <div className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">
        The terminal is only available in the Multica Desktop app. It attaches
        to the PTY hosted by the local daemon that ran the agent task.
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center justify-between text-xs text-muted-foreground">
        <span>
          Status: <span className="font-medium">{status}</span>
          {errorMessage ? (
            <span className="ml-2 text-destructive">— {errorMessage}</span>
          ) : null}
        </span>
        <Button
          variant="ghost"
          size="sm"
          onClick={() => {
            setErrorMessage("");
            setReconnectKey((n) => n + 1);
          }}
        >
          Reconnect
        </Button>
      </div>
      <div
        ref={containerRef}
        className="h-[360px] w-full overflow-hidden rounded-md border bg-black"
      />
    </div>
  );
}

function deriveTerminalWsUrl(issueId: string, workspaceId: string): string {
  // The API client knows the http(s) base URL; flip the scheme to ws(s)
  // and target the proxy endpoint registered in router.go. Falls back to
  // the page origin if for some reason the API base is empty (dev
  // environments where the API lives on the same host).
  let base = "";
  try {
    base = getApi().getBaseUrl();
  } catch {
    base = "";
  }
  if (!base && typeof window !== "undefined") {
    base = window.location.origin;
  }
  const url = new URL(base);
  if (url.protocol === "https:") {
    url.protocol = "wss:";
  } else if (url.protocol === "http:") {
    url.protocol = "ws:";
  }
  url.pathname = url.pathname.replace(/\/$/, "") +
    `/ws/issues/${encodeURIComponent(issueId)}/terminal`;
  url.search = `?workspace_id=${encodeURIComponent(workspaceId)}&cols=120&rows=30`;
  return url.toString();
}

function utf8ToBase64(s: string): string {
  if (typeof TextEncoder !== "undefined") {
    const bytes = new TextEncoder().encode(s);
    let bin = "";
    bytes.forEach((b) => {
      bin += String.fromCharCode(b);
    });
    return btoa(bin);
  }
  // Fallback for old runtimes: assume latin1.
  return btoa(s);
}

function atobToUint8(s: string): Uint8Array {
  const bin = atob(s);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) {
    out[i] = bin.charCodeAt(i);
  }
  return out;
}
