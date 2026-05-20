/**
 * Issues belonging to a project — List + Board view modes.
 *
 * Status grouping uses full `BOARD_STATUSES` (six visible groups, cancelled
 * excluded) to match web `packages/views/projects/components/project-detail.tsx`.
 * The earlier mobile-only "Open / Done" two-bucket layout was a parity
 * violation: same status enum value would appear in different visible
 * groups on mobile vs web. Cancelled is omitted on both clients.
 *
 * View modes:
 *   - List: vertical SectionList-shape — status header + `IssueRow` per
 *     issue. Default; matches my-issues / Issues default.
 *   - Board: horizontal column scroll, one column per status header +
 *     stacked rows. Linear iOS / Things use the same small-screen kanban
 *     pattern (vertical stacking inside each column, horizontal navigation
 *     between columns). Columns are 280pt wide — enough room for an
 *     identifier + a truncated title at typical iPhone widths.
 *
 * View mode is local component state — no cross-screen need, no Zustand.
 */
import { useMemo, useState } from "react";
import { ScrollView, View } from "react-native";
import SegmentedControl from "@react-native-segmented-control/segmented-control";
import { useQuery } from "@tanstack/react-query";
import { router } from "expo-router";
import type { Issue, IssueStatus } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";
import { StatusIcon } from "@/components/ui/status-icon";
import { IssueRow } from "@/components/issue/issue-row";
import { IssuesLoading } from "@/components/issue/issues-loading";
import { projectIssuesOptions } from "@/data/queries/projects";
import { useWorkspaceStore } from "@/data/workspace-store";
import { BOARD_STATUSES, STATUS_LABEL } from "@/lib/issue-status";

type ViewMode = "list" | "board";

interface Props {
  projectId: string;
}

export function ProjectRelatedIssues({ projectId }: Props) {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const { data, isLoading, error, refetch } = useQuery(
    projectIssuesOptions(wsId, projectId),
  );

  const [viewMode, setViewMode] = useState<ViewMode>("list");

  const byStatus = useMemo(() => {
    const m = new Map<IssueStatus, Issue[]>();
    for (const status of BOARD_STATUSES) m.set(status, []);
    for (const issue of data ?? []) {
      const list = m.get(issue.status);
      if (list) list.push(issue);
    }
    return m;
  }, [data]);

  const navigateToIssue = (id: string) => {
    if (wsSlug) router.push(`/${wsSlug}/issue/${id}`);
  };

  if (isLoading) return <IssuesLoading />;

  if (error) {
    return (
      <View className="px-4 py-6 gap-3">
        <Text className="text-sm text-destructive">
          Failed to load issues:{" "}
          {error instanceof Error ? error.message : "unknown error"}
        </Text>
        <Button variant="outline" onPress={() => refetch()}>
          <Text>Retry</Text>
        </Button>
      </View>
    );
  }

  if ((data?.length ?? 0) === 0) {
    return (
      <View className="px-4 py-6">
        <Text className="text-sm text-muted-foreground">No issues yet.</Text>
      </View>
    );
  }

  return (
    <View>
      <View className="px-4 pt-2 pb-2">
        <SegmentedControl
          values={["List", "Board"]}
          selectedIndex={viewMode === "list" ? 0 : 1}
          onChange={(e) =>
            setViewMode(
              e.nativeEvent.selectedSegmentIndex === 0 ? "list" : "board",
            )
          }
        />
      </View>
      {viewMode === "list" ? (
        <ListView byStatus={byStatus} onPressIssue={navigateToIssue} />
      ) : (
        <BoardView byStatus={byStatus} onPressIssue={navigateToIssue} />
      )}
    </View>
  );
}

function ListView({
  byStatus,
  onPressIssue,
}: {
  byStatus: Map<IssueStatus, Issue[]>;
  onPressIssue: (id: string) => void;
}) {
  return (
    <View>
      {BOARD_STATUSES.map((status) => {
        const issues = byStatus.get(status) ?? [];
        if (issues.length === 0) return null;
        return (
          <View key={status}>
            <SectionHeader status={status} count={issues.length} />
            {issues.map((issue) => (
              <IssueRow
                key={issue.id}
                issue={issue}
                onPress={() => onPressIssue(issue.id)}
              />
            ))}
          </View>
        );
      })}
    </View>
  );
}

function BoardView({
  byStatus,
  onPressIssue,
}: {
  byStatus: Map<IssueStatus, Issue[]>;
  onPressIssue: (id: string) => void;
}) {
  // Outer detail screen is a vertical ScrollView; this nests a horizontal
  // ScrollView. Different axes — RN handles the gesture priority fine.
  // Per-column scroll is delegated to the outer vertical scroll (no inner
  // FlatList) so the user's vertical thumb gesture flows through to the
  // detail page's primary scroll.
  return (
    <ScrollView
      horizontal
      showsHorizontalScrollIndicator={false}
      contentContainerClassName="px-4 gap-3 pb-2"
    >
      {BOARD_STATUSES.map((status) => {
        const issues = byStatus.get(status) ?? [];
        return (
          <View
            key={status}
            className="w-72 bg-secondary/30 rounded-lg overflow-hidden"
          >
            <View className="flex-row items-center gap-2 px-3 py-2 bg-secondary/60">
              <StatusIcon status={status} size={14} />
              <Text className="text-xs uppercase tracking-wider text-muted-foreground font-medium flex-1">
                {STATUS_LABEL[status]}
              </Text>
              <Text className="text-xs text-muted-foreground/60">
                {issues.length}
              </Text>
            </View>
            {issues.length === 0 ? (
              <View className="px-3 py-6">
                <Text className="text-xs text-muted-foreground/60 text-center">
                  Empty
                </Text>
              </View>
            ) : (
              issues.map((issue) => (
                <IssueRow
                  key={issue.id}
                  issue={issue}
                  onPress={() => onPressIssue(issue.id)}
                />
              ))
            )}
          </View>
        );
      })}
    </ScrollView>
  );
}

function SectionHeader({
  status,
  count,
}: {
  status: IssueStatus;
  count: number;
}) {
  return (
    <View className="flex-row items-center gap-2 px-4 py-2 bg-background">
      <StatusIcon status={status} size={14} />
      <Text className="text-xs uppercase tracking-wider text-muted-foreground font-medium">
        {STATUS_LABEL[status]}
      </Text>
      <Text className="text-xs text-muted-foreground/60">{count}</Text>
    </View>
  );
}
