/**
 * Issue-detail attribute chip row. Linear iOS-inspired layout: each
 * editable attribute renders as a tappable chip; tapping opens a picker
 * sheet; selecting fires the corresponding mutation with optimistic
 * update.
 *
 * Attributes covered:
 *   - status (tap → StatusPickerSheet → useUpdateIssue)
 *   - priority (tap → PriorityPickerSheet → useUpdateIssue)
 *   - assignee (tap → AssigneePickerSheet → useUpdateIssue)
 *   - project (read-only chip — picker deferred until web ships one)
 *   - labels (tap → LabelPickerSheet → useAttachLabel / useDetachLabel,
 *            multi-select)
 *   - due_date (tap → DueDatePickerSheet → useUpdateIssue)
 *
 * Empty values render as `dimmed` placeholder chips ("Label" / "Due
 * date" / "Unassigned" / etc) so users discover the surface; project
 * chip is hidden when empty (no picker to open).
 */
import { useMemo, useState } from "react";
import { View } from "react-native";
import { useQuery } from "@tanstack/react-query";
import type { Issue, IssuePriority, IssueStatus, Label } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { StatusIcon } from "@/components/ui/status-icon";
import { PriorityIcon } from "@/components/ui/priority-icon";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { ProjectIcon } from "@/components/ui/project-icon";
import { AttributeChip } from "./attribute-chip";
import { StatusPickerSheet } from "./pickers/status-picker-sheet";
import { PriorityPickerSheet } from "./pickers/priority-picker-sheet";
import {
  AssigneePickerSheet,
  type AssigneeValue,
} from "./pickers/assignee-picker-sheet";
import { LabelPickerSheet } from "./pickers/label-picker-sheet";
import { DueDatePickerSheet } from "./pickers/due-date-picker-sheet";
import {
  useAttachLabel,
  useDetachLabel,
  useUpdateIssue,
} from "@/data/mutations/issues";
import { useActorLookup } from "@/data/use-actor-name";
import { findProject, projectListOptions } from "@/data/queries/projects";
import { useWorkspaceStore } from "@/data/workspace-store";
import { STATUS_LABEL } from "@/lib/issue-status";

const PRIORITY_LABEL: Record<IssuePriority, string> = {
  urgent: "Urgent",
  high: "High",
  medium: "Medium",
  low: "Low",
  none: "Priority",
};

function formatDueDate(iso: string | null): string | null {
  if (!iso) return null;
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return null;
  return d.toLocaleDateString("en-US", { month: "short", day: "numeric" });
}

export function AttributeRow({ issue }: { issue: Issue }) {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const { getName } = useActorLookup();
  const updateIssue = useUpdateIssue(issue.id);
  const attachLabel = useAttachLabel(issue.id);
  const detachLabel = useDetachLabel(issue.id);

  const [statusOpen, setStatusOpen] = useState(false);
  const [priorityOpen, setPriorityOpen] = useState(false);
  const [assigneeOpen, setAssigneeOpen] = useState(false);
  const [labelOpen, setLabelOpen] = useState(false);
  const [dueOpen, setDueOpen] = useState(false);

  // Project read-only — fetch list to look up the title + icon. Cheap
  // (cached after first issue-detail visit).
  const { data: projects = [] } = useQuery(projectListOptions(wsId));
  const project = useMemo(
    () => findProject(projects, issue.project_id),
    [projects, issue.project_id],
  );

  const labels = issue.labels ?? [];

  // --- handlers ---
  const onStatus = (next: IssueStatus) =>
    updateIssue.mutate({ status: next });
  const onPriority = (next: IssuePriority) =>
    updateIssue.mutate({ priority: next });
  const onAssignee = (next: AssigneeValue) => {
    if (next === null) {
      updateIssue.mutate({ assignee_type: null, assignee_id: null });
    } else {
      updateIssue.mutate({ assignee_type: next.type, assignee_id: next.id });
    }
  };
  const onAttach = (label: Label) => attachLabel.mutate({ label });
  const onDetach = (labelId: string) => detachLabel.mutate({ labelId });
  const onDue = (next: string | null) => updateIssue.mutate({ due_date: next });

  const assigneeValue: AssigneeValue =
    issue.assignee_type && issue.assignee_id
      ? { type: issue.assignee_type, id: issue.assignee_id }
      : null;

  const assigneeName = assigneeValue
    ? getName(assigneeValue.type, assigneeValue.id)
    : null;
  const dueLabel = formatDueDate(issue.due_date);

  return (
    <View className="flex-row flex-wrap gap-2">
      {/* Status — always shown */}
      <AttributeChip
        icon={<StatusIcon status={issue.status} size={14} />}
        label={STATUS_LABEL[issue.status]}
        variant="filled"
        onPress={() => setStatusOpen(true)}
      />

      {/* Priority — always shown; "none" still tappable so user can change */}
      <AttributeChip
        icon={<PriorityIcon priority={issue.priority} size={14} />}
        label={PRIORITY_LABEL[issue.priority]}
        variant={issue.priority === "none" ? "dimmed" : "filled"}
        onPress={() => setPriorityOpen(true)}
      />

      {/* Assignee — shows avatar + name, or "Unassigned" placeholder */}
      {assigneeValue ? (
        <AttributeChip
          icon={
            <ActorAvatar
              type={assigneeValue.type}
              id={assigneeValue.id}
              size={16}
              showPresence
            />
          }
          label={assigneeName ?? "Unknown"}
          variant="filled"
          onPress={() => setAssigneeOpen(true)}
        />
      ) : (
        <AttributeChip
          icon={
            <View className="size-4 rounded-full border border-dashed border-muted-foreground/40" />
          }
          label="Assignee"
          variant="dimmed"
          onPress={() => setAssigneeOpen(true)}
        />
      )}

      {/* Each existing label as its own chip — tap deletes (calls detach
          directly to avoid a sheet round-trip for the most common action). */}
      {labels.map((label) => (
        <AttributeChip
          key={label.id}
          icon={
            <View
              className="size-2.5 rounded-full"
              style={{ backgroundColor: label.color }}
            />
          }
          label={label.name}
          variant="filled"
          onPress={() => setLabelOpen(true)}
        />
      ))}
      {labels.length === 0 ? (
        <AttributeChip
          icon={<Text className="text-xs text-muted-foreground/70">◯</Text>}
          label="Label"
          variant="dimmed"
          onPress={() => setLabelOpen(true)}
        />
      ) : null}

      {/* Project — read-only chip; picker deferred. Hidden when empty. */}
      {project ? (
        <AttributeChip
          icon={<ProjectIcon icon={project.icon} size="sm" />}
          label={project.title}
          variant="filled"
        />
      ) : null}

      {/* Due date */}
      <AttributeChip
        icon={<Text className="text-xs text-muted-foreground/80">📅</Text>}
        label={dueLabel ?? "Due date"}
        variant={dueLabel ? "filled" : "dimmed"}
        onPress={() => setDueOpen(true)}
      />

      {/* --- Sheets (mounted lazily by visible state) --- */}
      <StatusPickerSheet
        visible={statusOpen}
        value={issue.status}
        onChange={onStatus}
        onClose={() => setStatusOpen(false)}
      />
      <PriorityPickerSheet
        visible={priorityOpen}
        value={issue.priority}
        onChange={onPriority}
        onClose={() => setPriorityOpen(false)}
      />
      <AssigneePickerSheet
        visible={assigneeOpen}
        value={assigneeValue}
        onChange={onAssignee}
        onClose={() => setAssigneeOpen(false)}
      />
      <LabelPickerSheet
        visible={labelOpen}
        attached={labels}
        onAttach={onAttach}
        onDetach={onDetach}
        onClose={() => setLabelOpen(false)}
      />
      <DueDatePickerSheet
        visible={dueOpen}
        value={issue.due_date}
        onChange={onDue}
        onClose={() => setDueOpen(false)}
      />
    </View>
  );
}
