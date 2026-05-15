/**
 * Session-switch sheet — opens from the chat header's center title press.
 *
 * Migrated to the shared SheetShell (iOS pageSheet) — see apps/mobile/
 * CLAUDE.md Lesson #6 for why this content type belongs in a pageSheet,
 * not the project's older transparent-fade Modal pattern.
 *
 * Interactions per row:
 *   - Tap          → switch active session + close sheet
 *   - Long-press   → confirm alert → delete session
 *
 * Footer row: "Switch agent →" → opens the agent picker sheet.
 *
 * Archived sessions render in the same flat list with a small "archived"
 * label suffix. We don't hide them (parity rule: web shows N sessions →
 * mobile shows N sessions). The chat screen disables send for them.
 */
import { Alert, Pressable, ScrollView, View } from "react-native";
import type { ChatSession } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { SheetShell } from "@/components/ui/sheet-shell";
import { cn } from "@/lib/utils";

interface Props {
  visible: boolean;
  sessions: ChatSession[];
  activeSessionId: string | null;
  onSelectSession: (session: ChatSession) => void;
  onDeleteSession: (sessionId: string) => void;
  onOpenAgentPicker: () => void;
  onClose: () => void;
}

export function SessionSheet({
  visible,
  sessions,
  activeSessionId,
  onSelectSession,
  onDeleteSession,
  onOpenAgentPicker,
  onClose,
}: Props) {
  const confirmDelete = (session: ChatSession) => {
    Alert.alert(
      "Delete this chat?",
      session.title || "Untitled chat",
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Delete",
          style: "destructive",
          onPress: () => onDeleteSession(session.id),
        },
      ],
      { cancelable: true },
    );
  };

  return (
    <SheetShell visible={visible} onClose={onClose} title="Chats">
      <ScrollView
        className="flex-1"
        showsVerticalScrollIndicator={false}
      >
        {sessions.length === 0 ? (
          <View className="px-4 py-8">
            <Text className="text-sm text-muted-foreground text-center">
              No chats yet.
            </Text>
          </View>
        ) : (
          sessions.map((session) => {
            const selected = session.id === activeSessionId;
            const archived = session.status === "archived";
            return (
              <Pressable
                key={session.id}
                onPress={() => {
                  onSelectSession(session);
                  onClose();
                }}
                onLongPress={() => confirmDelete(session)}
                className={cn(
                  "flex-row items-center gap-3 px-4 py-3 active:bg-secondary",
                  selected && "bg-secondary/60",
                )}
              >
                {/* Unread dot — has_unread comes from the server and
                 *  WS chat:done invalidations keep it fresh. Sized
                 *  +reserved-width whether visible or not so the
                 *  avatar column stays aligned across read/unread
                 *  rows. */}
                <View
                  className={cn(
                    "h-2 w-2 rounded-full",
                    session.has_unread ? "bg-primary" : "bg-transparent",
                  )}
                />
                <ActorAvatar
                  type="agent"
                  id={session.agent_id}
                  size={32}
                  showPresence
                />
                <View className="flex-1">
                  <Text
                    className={cn(
                      "text-sm text-foreground",
                      session.has_unread && "font-semibold",
                    )}
                    numberOfLines={1}
                  >
                    {session.title || "Untitled chat"}
                  </Text>
                  {archived ? (
                    <Text className="text-xs text-muted-foreground mt-0.5">
                      archived
                    </Text>
                  ) : null}
                </View>
                {selected ? (
                  <Text className="text-sm text-primary font-semibold">✓</Text>
                ) : null}
              </Pressable>
            );
          })
        )}

        <Pressable
          onPress={() => {
            onOpenAgentPicker();
            onClose();
          }}
          className="flex-row items-center justify-between px-4 py-3 border-t border-border active:bg-secondary"
        >
          <Text className="text-sm text-foreground">Switch agent</Text>
          <Text className="text-sm text-muted-foreground">→</Text>
        </Pressable>
      </ScrollView>
    </SheetShell>
  );
}
