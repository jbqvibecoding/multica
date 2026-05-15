/**
 * Project lead picker. Single-select over members + agents, with a top
 * "Unassigned" row to clear. Search bar filters by name.
 *
 * Container: iOS pageSheet via shared `<SheetShell>` (CLAUDE.md Lesson #6).
 * Search input at the top of the body; SectionList (Members / Agents)
 * below.
 */
import { useMemo, useState } from "react";
import {
  ActivityIndicator,
  Pressable,
  SectionList,
  TextInput,
  View,
} from "react-native";
import { useQuery } from "@tanstack/react-query";
import { Ionicons } from "@expo/vector-icons";
import type { Agent, MemberWithUser } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { MOBILE_PLACEHOLDER_COLOR } from "@/components/ui/input-tokens";
import { SheetShell } from "@/components/ui/sheet-shell";
import { agentListOptions } from "@/data/queries/agents";
import { memberListOptions } from "@/data/queries/members";
import { useWorkspaceStore } from "@/data/workspace-store";
import { cn } from "@/lib/utils";

export interface LeadValue {
  type: "member" | "agent";
  id: string;
}

interface Props {
  visible: boolean;
  value: LeadValue | null;
  onChange: (next: LeadValue | null) => void;
  onClose: () => void;
}

type RowItem =
  | { kind: "member"; member: MemberWithUser }
  | { kind: "agent"; agent: Agent };

export function ProjectLeadPickerSheet({
  visible,
  value,
  onChange,
  onClose,
}: Props) {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const { data: members, isLoading: loadingMembers } = useQuery(
    memberListOptions(wsId),
  );
  const { data: agents, isLoading: loadingAgents } = useQuery(
    agentListOptions(wsId),
  );

  const [query, setQuery] = useState("");

  const sections = useMemo(() => {
    const q = query.trim().toLowerCase();
    const memberRows: RowItem[] = (members ?? [])
      .filter((m) => !q || m.name.toLowerCase().includes(q))
      .map((m) => ({ kind: "member" as const, member: m }));
    const agentRows: RowItem[] = (agents ?? [])
      .filter((a) => !q || a.name.toLowerCase().includes(q))
      .map((a) => ({ kind: "agent" as const, agent: a }));
    const out: Array<{ title: string; data: RowItem[] }> = [];
    if (memberRows.length > 0)
      out.push({ title: "Members", data: memberRows });
    if (agentRows.length > 0)
      out.push({ title: "Agents", data: agentRows });
    return out;
  }, [members, agents, query]);

  const pick = (next: LeadValue | null) => {
    onChange(next);
    onClose();
  };

  const matches = (item: RowItem) => {
    if (!value) return false;
    if (item.kind === "member") {
      return value.type === "member" && value.id === item.member.user_id;
    }
    return value.type === "agent" && value.id === item.agent.id;
  };

  return (
    <SheetShell visible={visible} onClose={onClose} title="Project Lead">
      <View className="px-3 pt-2 pb-2 border-b border-border">
        <TextInput
          value={query}
          onChangeText={setQuery}
          placeholder="Search members or agents"
          placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
          className="text-sm text-foreground bg-secondary/50 rounded-md px-3 py-2"
          autoCapitalize="none"
          autoCorrect={false}
        />
      </View>
      {loadingMembers || loadingAgents ? (
        <View className="px-3 py-8 items-center">
          <ActivityIndicator />
        </View>
      ) : (
        <SectionList
          sections={sections}
          className="flex-1"
          keyboardShouldPersistTaps="handled"
          automaticallyAdjustKeyboardInsets
          keyExtractor={(item) =>
            item.kind === "member"
              ? `m-${item.member.user_id}`
              : `a-${item.agent.id}`
          }
          ListHeaderComponent={
            <UnassignedRow
              checked={value === null}
              onPress={() => pick(null)}
            />
          }
          renderSectionHeader={({ section }) => (
            <View className="bg-popover px-3 pt-2 pb-1">
              <Text className="text-[11px] uppercase tracking-wider text-muted-foreground/70">
                {section.title}
              </Text>
            </View>
          )}
          renderItem={({ item }) =>
            item.kind === "member" ? (
              <PickerRow
                name={item.member.name}
                type="member"
                id={item.member.user_id}
                checked={matches(item)}
                onPress={() =>
                  pick({ type: "member", id: item.member.user_id })
                }
              />
            ) : (
              <PickerRow
                name={item.agent.name}
                type="agent"
                id={item.agent.id}
                checked={matches(item)}
                onPress={() => pick({ type: "agent", id: item.agent.id })}
              />
            )
          }
          ListEmptyComponent={
            <View className="px-3 py-6 items-center">
              <Text className="text-xs text-muted-foreground text-center">
                {query
                  ? "No matches."
                  : "No members or agents in this workspace yet."}
              </Text>
            </View>
          }
        />
      )}
    </SheetShell>
  );
}

function UnassignedRow({
  checked,
  onPress,
}: {
  checked: boolean;
  onPress: () => void;
}) {
  return (
    <Pressable
      onPress={onPress}
      className={cn(
        "flex-row items-center gap-3 px-3 py-2.5 border-b border-border active:bg-secondary",
        checked && "bg-secondary",
      )}
    >
      <Ionicons
        name="close-circle-outline"
        size={20}
        color={MOBILE_PLACEHOLDER_COLOR}
      />
      <Text className="flex-1 text-sm text-muted-foreground">Unassigned</Text>
      {checked ? (
        <Text className="text-xs text-muted-foreground">✓</Text>
      ) : null}
    </Pressable>
  );
}

function PickerRow({
  name,
  type,
  id,
  checked,
  onPress,
}: {
  name: string;
  type: "member" | "agent";
  id: string;
  checked: boolean;
  onPress: () => void;
}) {
  return (
    <Pressable
      onPress={onPress}
      className={cn(
        "flex-row items-center gap-3 px-3 py-2.5 active:bg-secondary",
        checked && "bg-secondary",
      )}
    >
      <ActorAvatar type={type} id={id} size={24} showPresence />
      <Text className="flex-1 text-sm text-foreground" numberOfLines={1}>
        {name}
      </Text>
      {checked ? (
        <Text className="text-xs text-muted-foreground">✓</Text>
      ) : null}
    </Pressable>
  );
}
