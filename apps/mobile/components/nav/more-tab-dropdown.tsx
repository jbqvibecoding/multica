/**
 * MoreTabDropdownAnchor — the popover that opens when the More tab is
 * tapped. Mounted as a sibling to the Tabs view, NOT as the tab button
 * itself: that way the real More tab button stays a standard React
 * Navigation `PlatformPressable` (icon + "More" label, full visual
 * parity with Inbox / My Issues / Chat).
 *
 * The wrapper View is absolute-positioned over the More tab's screen
 * rect (right 25%, bottom = safe-area, height = tab bar). It uses
 * `pointerEvents="box-none"` so taps pass through to the real tab
 * button underneath; we open the dropdown imperatively from the tab's
 * `listeners.tabPress` via the exposed `TriggerRef.open()`. The
 * @rn-primitives Trigger measures its own layout inside `open()`, so
 * the popover anchors to this invisible Pressable's rect — i.e.
 * directly above the More tab.
 *
 * Why ref-controlled instead of `asChild` on the tab button: a previous
 * attempt wrapped a custom tabBarButton in `<DropdownMenu.Root>` +
 * Trigger asChild. RN's BottomTabItem wraps the returned button in
 * `<View style={{flex:1}}>` and expects a single Pressable child. Our
 * Root introduced an extra wrapping `View` with no flex:1, collapsing
 * the More cell and stripping the label. The Option B pattern here
 * leaves the real tab button entirely alone.
 */
import { useMemo } from "react";
import { ActivityIndicator, Image, Pressable, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { router, usePathname } from "expo-router";
import { useQuery } from "@tanstack/react-query";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import type { TriggerRef } from "@rn-primitives/dropdown-menu";
import type { User, Workspace } from "@multica/core/types";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
} from "@/components/ui/dropdown-menu";
import { Text } from "@/components/ui/text";
import { workspaceListOptions } from "@/data/queries/workspaces";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { cn } from "@/lib/utils";

// iOS bottom tab bar default height (above safe-area). React Navigation
// doesn't expose this as a layout constant, but the value is stable
// across Expo Router 55 / RN Screens 4 — see BottomTabBar.tsx in
// @react-navigation/bottom-tabs (`styles.tab` has no explicit height;
// the container settles at 49 from the inner padding + icon size).
const TAB_BAR_HEIGHT = 49;

const ICON_COLOR = "#3f3f46";
const ICON_MUTED = "#71717a";

interface NavItem {
  label: string;
  icon: keyof typeof Ionicons.glyphMap;
  /** Path under /:slug/ — final href is `/${slug}${path}`. */
  path: string;
}

const NAV_ITEMS: NavItem[] = [
  { label: "Issues", icon: "list-outline", path: "/more/issues" },
  { label: "Projects", icon: "cube-outline", path: "/more/projects" },
];

export function MoreTabDropdownAnchor({
  triggerRef,
}: {
  triggerRef: React.RefObject<TriggerRef | null>;
}) {
  const insets = useSafeAreaInsets();
  const slug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const user = useAuthStore((s) => s.user);
  const pathname = usePathname();
  const currentWorkspace = useCurrentWorkspace(slug);

  const isActive = (path: string) => {
    if (!slug) return false;
    const target = `/${slug}${path}`;
    return pathname === target || pathname.startsWith(target + "/");
  };

  return (
    <View
      pointerEvents="box-none"
      style={{
        position: "absolute",
        right: 0,
        bottom: insets.bottom,
        width: "25%",
        height: TAB_BAR_HEIGHT,
      }}
    >
      <DropdownMenu>
        <DropdownMenuTrigger ref={triggerRef} asChild>
          {/* Invisible, non-tappable: the real tab button below catches
              all touches; we open this trigger imperatively via ref.
              The Pressable just provides a measurable rect for the
              popover to anchor against. */}
          <Pressable
            pointerEvents="none"
            accessibilityElementsHidden
            importantForAccessibility="no-hide-descendants"
            style={{ width: "100%", height: "100%" }}
          />
        </DropdownMenuTrigger>

        <DropdownMenuContent
          side="top"
          align="end"
          sideOffset={6}
          className="w-64 p-2"
        >
          <UserCard
            user={user}
            onPress={() => slug && router.push(`/${slug}/more/settings`)}
          />

          <DropdownMenuSeparator />

          <WorkspaceSwitcher
            activeSlug={slug}
            currentWorkspaceName={currentWorkspace?.name}
          />

          <DropdownMenuSeparator />

          {NAV_ITEMS.map((item) => (
            <DropdownMenuItem
              key={item.path}
              onPress={() => slug && router.push(`/${slug}${item.path}`)}
              className={cn(
                "h-9",
                isActive(item.path) && "bg-secondary",
              )}
            >
              <Ionicons name={item.icon} size={16} color={ICON_COLOR} />
              <Text className="text-sm text-foreground">{item.label}</Text>
            </DropdownMenuItem>
          ))}
        </DropdownMenuContent>
      </DropdownMenu>
    </View>
  );
}

function UserCard({
  user,
  onPress,
}: {
  user: User | null;
  onPress: () => void;
}) {
  const initial = (user?.name ?? user?.email ?? "U").charAt(0).toUpperCase();
  return (
    <DropdownMenuItem onPress={onPress} className="h-12 gap-3">
      {user?.avatar_url ? (
        <Image
          source={{ uri: user.avatar_url }}
          className="size-8 rounded-full bg-muted"
        />
      ) : (
        <View className="size-8 rounded-full bg-muted items-center justify-center">
          <Text className="text-xs font-medium text-muted-foreground">
            {initial}
          </Text>
        </View>
      )}
      <View className="flex-1 min-w-0">
        <Text
          className="text-sm font-medium text-foreground"
          numberOfLines={1}
        >
          {user?.name ?? "—"}
        </Text>
        {user?.email ? (
          <Text
            className="text-xs text-muted-foreground"
            numberOfLines={1}
          >
            {user.email}
          </Text>
        ) : null}
      </View>
      <Ionicons name="chevron-forward" size={14} color={ICON_MUTED} />
    </DropdownMenuItem>
  );
}

function WorkspaceSwitcher({
  activeSlug,
  currentWorkspaceName,
}: {
  activeSlug: string | null;
  currentWorkspaceName: string | undefined;
}) {
  const { data, isLoading } = useQuery(workspaceListOptions());

  if (isLoading) {
    return (
      <View className="py-2 items-center">
        <ActivityIndicator />
      </View>
    );
  }

  // Single workspace? Show its name as a static label-style row.
  if (!data || data.length <= 1) {
    return (
      <View className="px-2 py-2 flex-row items-center gap-2">
        <Ionicons name="business" size={14} color={ICON_MUTED} />
        <Text className="text-xs text-muted-foreground" numberOfLines={1}>
          {currentWorkspaceName ?? "Workspace"}
        </Text>
      </View>
    );
  }

  return (
    <View>
      <View className="px-2 pt-1 pb-1">
        <Text className="text-[11px] uppercase tracking-wider text-muted-foreground">
          Workspace
        </Text>
      </View>
      {data.map((ws) => {
        const active = ws.slug === activeSlug;
        return (
          <DropdownMenuItem
            key={ws.id}
            onPress={() => {
              if (active) return;
              router.replace(`/${ws.slug}/inbox`);
            }}
            className="h-9"
          >
            <Ionicons
              name={active ? "checkmark-circle" : "ellipse-outline"}
              size={14}
              color={active ? ICON_COLOR : ICON_MUTED}
            />
            <Text
              className="flex-1 text-sm text-foreground"
              numberOfLines={1}
            >
              {ws.name}
            </Text>
          </DropdownMenuItem>
        );
      })}
    </View>
  );
}

function useCurrentWorkspace(slug: string | null): Workspace | undefined {
  const { data } = useQuery(workspaceListOptions());
  return useMemo(
    () => (slug ? data?.find((w) => w.slug === slug) : undefined),
    [data, slug],
  );
}
