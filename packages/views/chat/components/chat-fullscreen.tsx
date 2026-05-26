"use client";

import type { ReactNode } from "react";
import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Minimize2, Plus } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import { useWorkspaceId } from "@multica/core/hooks";
import { useChatStore } from "@multica/core/chat";
import { pendingChatTasksOptions } from "@multica/core/chat/queries";
import {
  useDeleteChatSession,
  useUpdateChatSession,
} from "@multica/core/chat/mutations";
import { SessionListItem } from "./session-list-item";
import { ChatMessageList, ChatMessageSkeleton } from "./chat-message-list";
import { ChatInput } from "./chat-input";
import { ContextAnchorCard } from "./context-anchor";
import { OfflineBanner } from "./offline-banner";
import { NoAgentBanner } from "./no-agent-banner";
import { useT } from "../../i18n";
import type { Agent, ChatMessage, ChatPendingTask, ChatSession } from "@multica/core/types";
import type { AgentAvailability } from "@multica/core/agents";
import type { UploadResult } from "@multica/core/hooks/use-file-upload";

interface ChatFullscreenProps {
  sessions: ChatSession[];
  agents: Agent[];
  activeSessionId: string | null;
  activeAgent: Agent | null;
  messages: ChatMessage[];
  messagesLoading: boolean;
  pendingTask: ChatPendingTask | null | undefined;
  pendingTaskId: string | null;
  availability: AgentAvailability | undefined;
  noAgent: boolean;
  isSessionArchived: boolean;
  onSend: (content: string, attachmentIds?: string[]) => void;
  onUploadFile: (file: File) => Promise<UploadResult | null>;
  onStop: () => void;
  onSelectSession: (session: ChatSession) => void;
  onNewChat: () => void;
  agentDropdown: ReactNode;
  contextAnchorButton: ReactNode;
}

export function ChatFullscreen({
  sessions,
  agents,
  activeSessionId,
  activeAgent,
  messages,
  messagesLoading,
  pendingTask,
  pendingTaskId,
  availability,
  noAgent,
  isSessionArchived,
  onSend,
  onUploadFile,
  onStop,
  onSelectSession,
  onNewChat,
  agentDropdown,
  contextAnchorButton,
}: ChatFullscreenProps) {
  const { t } = useT("chat");
  const wsId = useWorkspaceId();
  const setFullscreen = useChatStore((s) => s.setFullscreen);

  const agentById = useMemo(() => new Map(agents.map((a) => [a.id, a])), [agents]);
  const { data: pending } = useQuery(pendingChatTasksOptions(wsId));
  const inFlightSessionIds = useMemo(
    () => new Set((pending?.tasks ?? []).map((t) => t.chat_session_id)),
    [pending],
  );

  const deleteSession = useDeleteChatSession();
  const updateSession = useUpdateChatSession();
  const setActiveSession = useChatStore((s) => s.setActiveSession);
  const [renamingId, setRenamingId] = useState<string | null>(null);
  const [pendingDelete, setPendingDelete] = useState<ChatSession | null>(null);
  const formatTimeAgo = useFormatTimeAgo();

  const { active, archived } = useMemo(() => {
    const active: ChatSession[] = [];
    const archived: ChatSession[] = [];
    for (const s of sessions) {
      if (s.status === "archived") archived.push(s);
      else active.push(s);
    }
    return { active, archived };
  }, [sessions]);

  const handleConfirmDelete = () => {
    if (!pendingDelete) return;
    const sessionId = pendingDelete.id;
    if (activeSessionId === sessionId) setActiveSession(null);
    deleteSession.mutate(sessionId, {
      onSettled: () => setPendingDelete(null),
    });
  };

  const handleSubmitRename = (sessionId: string, raw: string) => {
    const trimmed = raw.trim();
    const current = sessions.find((s) => s.id === sessionId);
    setRenamingId(null);
    if (!trimmed || trimmed === current?.title) return;
    updateSession.mutate({ sessionId, title: trimmed });
  };

  const showSkeleton = !!activeSessionId && messagesLoading;
  const hasMessages = messages.length > 0 || !!pendingTaskId;

  const currentSession = activeSessionId
    ? sessions.find((s) => s.id === activeSessionId)
    : null;
  const sessionTitle = currentSession?.title?.trim() || t(($) => $.window.untitled);

  return (
    <>
      <div className="fixed inset-0 z-50 flex bg-sidebar">
        {/* Left sidebar — session list */}
        <div className="flex w-64 shrink-0 flex-col border-r">
          <div className="flex items-center justify-between border-b px-3 py-2.5">
            <span className="text-sm font-medium text-muted-foreground">
              {t(($) => $.window.active_group)}
            </span>
            <Tooltip>
              <TooltipTrigger
                render={
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    className="rounded-full text-muted-foreground"
                    onClick={onNewChat}
                  />
                }
              >
                <Plus />
              </TooltipTrigger>
              <TooltipContent side="right">
                {t(($) => $.window.new_chat_tooltip)}
              </TooltipContent>
            </Tooltip>
          </div>
          <div className="flex-1 overflow-y-auto p-1.5 space-y-0.5">
            {[...active, ...archived].map((session) => (
              <SessionListItem
                key={session.id}
                session={session}
                agent={agentById.get(session.agent_id) ?? null}
                isCurrent={session.id === activeSessionId}
                isRunning={inFlightSessionIds.has(session.id)}
                isRenaming={renamingId === session.id}
                formatTimeAgo={formatTimeAgo}
                onSelect={() => onSelectSession(session)}
                onStartRename={() => setRenamingId(session.id)}
                onSubmitRename={(value) => handleSubmitRename(session.id, value)}
                onCancelRename={() => setRenamingId(null)}
                onDelete={() => setPendingDelete(session)}
              />
            ))}
          </div>
        </div>

        {/* Right content area */}
        <div className="flex flex-1 flex-col min-w-0">
          {/* Header */}
          <div className="flex items-center justify-between border-b px-4 py-2.5">
            <div className="flex items-center gap-2 min-w-0">
              <span className="text-sm font-medium truncate">{sessionTitle}</span>
            </div>
            <Tooltip>
              <TooltipTrigger
                render={
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    className="text-muted-foreground"
                    onClick={() => setFullscreen(false)}
                  />
                }
              >
                <Minimize2 />
              </TooltipTrigger>
              <TooltipContent side="bottom">
                {t(($) => $.window.restore_tooltip)}
              </TooltipContent>
            </Tooltip>
          </div>

          {/* Messages */}
          {showSkeleton ? (
            <ChatMessageSkeleton />
          ) : hasMessages ? (
            <ChatMessageList
              messages={messages}
              pendingTask={pendingTask}
              availability={availability}
            />
          ) : (
            <div className="flex flex-1 items-center justify-center">
              <p className="text-sm text-muted-foreground">
                {t(($) => $.empty_state.returning_subtitle)}
              </p>
            </div>
          )}

          {/* Banners */}
          {noAgent ? (
            <NoAgentBanner />
          ) : (
            <OfflineBanner agentName={activeAgent?.name} availability={availability} />
          )}

          {/* Input */}
          <ChatInput
            onSend={onSend}
            onUploadFile={onUploadFile}
            onStop={onStop}
            isRunning={!!pendingTaskId}
            disabled={isSessionArchived}
            noAgent={noAgent}
            agentName={activeAgent?.name}
            topSlot={<ContextAnchorCard />}
            leftAdornment={agentDropdown}
            rightAdornment={contextAnchorButton}
          />
        </div>
      </div>

      {/* Delete confirmation */}
      <AlertDialog
        open={!!pendingDelete}
        onOpenChange={(open) => {
          if (!open && !deleteSession.isPending) setPendingDelete(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t(($) => $.session_history.delete_dialog.title)}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {pendingDelete?.title
                ? t(($) => $.session_history.delete_dialog.description_with_title, {
                    title: pendingDelete.title,
                  })
                : t(($) => $.session_history.delete_dialog.description_default)}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deleteSession.isPending}>
              {t(($) => $.session_history.delete_dialog.cancel)}
            </AlertDialogCancel>
            <AlertDialogAction
              onClick={handleConfirmDelete}
              disabled={deleteSession.isPending}
              className="bg-destructive text-white hover:bg-destructive/90"
            >
              {deleteSession.isPending
                ? t(($) => $.session_history.delete_dialog.confirming)
                : t(($) => $.session_history.delete_dialog.confirm)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}

function useFormatTimeAgo(): (dateStr: string) => string {
  const { t } = useT("chat");
  return (dateStr: string) => {
    const date = new Date(dateStr);
    const now = new Date();
    const diffMs = now.getTime() - date.getTime();
    const diffMins = Math.floor(diffMs / 60000);
    const diffHours = Math.floor(diffMs / 3600000);
    const diffDays = Math.floor(diffMs / 86400000);

    if (diffMins < 1) return t(($) => $.session_history.time.just_now);
    if (diffMins < 60) return t(($) => $.session_history.time.minutes, { count: diffMins });
    if (diffHours < 24) return t(($) => $.session_history.time.hours, { count: diffHours });
    if (diffDays < 7) return t(($) => $.session_history.time.days, { count: diffDays });
    return date.toLocaleDateString();
  };
}
