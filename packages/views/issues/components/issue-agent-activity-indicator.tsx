"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  HoverCard,
  HoverCardTrigger,
  HoverCardContent,
} from "@multica/ui/components/ui/hover-card";
import { useWorkspaceId } from "@multica/core/hooks";
import { agentTaskSnapshotOptions } from "@multica/core/agents";
import type { AgentTask } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { AgentAvatarStack } from "../../agents/components/agent-avatar-stack";
import { AgentActivityHoverContent } from "../../agents/components/agent-activity-hover-content";

interface IssueAgentActivityIndicatorProps {
  issueId: string;
  size?: number;
}

/**
 * Small "is there an agent working on this issue right now" badge — shown
 * in the top-right of board cards and right after the identifier in list
 * rows. Derives state from the workspace-wide agent task snapshot:
 *
 *   - has ≥1 running task  → avatar stack, full opacity
 *   - 0 running, ≥1 queued → avatar stack, half opacity (drops opacity
 *                              instead of swapping icons to keep agent
 *                              identity visible)
 *   - nothing               → return null (no chrome, no placeholder)
 *
 * Hover opens AgentActivityHoverContent which lists every active task
 * (running + queued) with a per-task status dot + duration. No links to
 * issue detail — the user clicks the card itself for that.
 *
 * The component re-renders on every snapshot invalidation (WS task:*
 * events drive it via use-realtime-sync). 30s staleTime is the offline
 * fallback only.
 */
export function IssueAgentActivityIndicator({
  issueId,
  size = 18,
}: IssueAgentActivityIndicatorProps) {
  const wsId = useWorkspaceId();
  const { data: snapshot = [] } = useQuery(agentTaskSnapshotOptions(wsId));

  const { runningTasks, queuedTasks, agentIds, opacity } = useMemo(() => {
    const running: AgentTask[] = [];
    const queued: AgentTask[] = [];
    for (const t of snapshot) {
      if (t.issue_id !== issueId) continue;
      if (t.status === "running") running.push(t);
      else if (t.status === "queued" || t.status === "dispatched")
        queued.push(t);
      // Terminal statuses are intentionally ignored — they belong on the
      // issue history, not the live indicator.
    }
    // Stack heads: prefer running. If 0 running, fall back to queued. This
    // keeps each visual state visually distinct (queued-only is dimmer)
    // while always offering a face to hover.
    const primary = running.length > 0 ? running : queued;
    const uniqueAgents = [...new Set(primary.map((t) => t.agent_id))];
    return {
      runningTasks: running,
      queuedTasks: queued,
      agentIds: uniqueAgents,
      opacity: (running.length > 0 ? "full" : "half") as "full" | "half",
    };
  }, [snapshot, issueId]);

  if (agentIds.length === 0) return null;
  const hoverTasks = [...runningTasks, ...queuedTasks];

  // When at least one task is actually running, wrap the stack in a soft
  // brand ring + slow pulse so the card reads "something is happening here"
  // at a glance — a single static avatar is too easy to miss in a dense
  // board. Queued-only (half-opacity) state already signals "waiting" via
  // the lighter avatars; no extra pulse there.
  const isRunning = opacity === "full";

  return (
    <HoverCard>
      <HoverCardTrigger
        render={
          <span
            className={cn(
              "inline-flex shrink-0 items-center rounded-full",
              // Soft "alive" breath: a visible brand ring at /70 paired
              // with Tailwind's default 2s pulse. Earlier attempts at
              // long cycles or near-transparent rings made the cue
              // disappear; keeping the base opaque enough to read and
              // the cadence at the system default gives a gentle
              // breath without strobing.
              isRunning &&
                "ring-1 ring-brand/70 animate-pulse motion-reduce:animate-none",
            )}
          />
        }
      >
        <AgentAvatarStack agentIds={agentIds} size={size} opacity={opacity} />
      </HoverCardTrigger>
      <HoverCardContent align="end" className="w-72">
        <AgentActivityHoverContent tasks={hoverTasks} />
      </HoverCardContent>
    </HoverCard>
  );
}
