import * as React from "react";
import type { ReactNode } from "react";
import { Children, cloneElement, isValidElement, useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { render as rtlRender, screen, type RenderOptions } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSquads from "../../locales/en/squads.json";

// Popover (Base UI) renders into a portal that is awkward to target with
// RTL. Replace with a simple state-driven open/close shim that surfaces the
// popover content inline whenever `open` is true (or after the trigger is
// clicked). The component logic under test (commit on Enter, commit on
// suggestion click, no commit on blur, Pencil/Loader2 swap) is what we need
// to exercise — Base UI's portal/positioning isn't.
vi.mock("@multica/ui/components/ui/popover", () => {
  function Popover({
    open,
    onOpenChange,
    children,
  }: {
    open?: boolean;
    onOpenChange?: (v: boolean) => void;
    children: ReactNode;
  }) {
    const [innerOpen, setInnerOpen] = useState(false);
    const isOpen = open ?? innerOpen;
    const setOpen = (v: boolean) => {
      setInnerOpen(v);
      onOpenChange?.(v);
    };
    return (
      <div data-testid="popover-root" data-open={isOpen ? "true" : "false"}>
        {Children.map(children, (child: ReactNode) =>
          isValidElement(child)
            ? cloneElement(
                child as React.ReactElement<{
                  __popoverOpen?: boolean;
                  __setPopoverOpen?: (v: boolean) => void;
                }>,
                { __popoverOpen: isOpen, __setPopoverOpen: setOpen },
              )
            : child,
        )}
      </div>
    );
  }
  function PopoverTrigger({
    render,
    __setPopoverOpen,
  }: {
    render: React.ReactElement;
    __setPopoverOpen?: (v: boolean) => void;
  }) {
    if (!isValidElement(render)) return null;
    return cloneElement(render as React.ReactElement<{ onClick?: (e: unknown) => void }>, {
      onClick: () => __setPopoverOpen?.(true),
    });
  }
  function PopoverContent({
    children,
    __popoverOpen,
    __setPopoverOpen,
  }: {
    children: ReactNode;
    __popoverOpen?: boolean;
    __setPopoverOpen?: (v: boolean) => void;
  }) {
    if (!__popoverOpen) return null;
    return (
      <div data-testid="popover-content">
        {/* Mock-only: drives the real Popover's outside-click → onOpenChange(false) path */}
        <button
          type="button"
          data-testid="mock-popover-close"
          onClick={() => __setPopoverOpen?.(false)}
        />
        {children}
      </div>
    );
  }
  return { Popover, PopoverTrigger, PopoverContent };
});

// Command (cmdk) wraps an input plus a list — simulate it minimally so the
// test can drive Enter/Escape on the input and click the suggestion items.
vi.mock("@multica/ui/components/ui/command", () => {
  function Command({ children }: { children: ReactNode }) {
    return <div role="listbox">{children}</div>;
  }
  function CommandInput({
    value,
    onValueChange,
    placeholder,
    onKeyDown,
    autoFocus,
  }: {
    value: string;
    onValueChange: (v: string) => void;
    placeholder?: string;
    onKeyDown?: (e: React.KeyboardEvent<HTMLInputElement>) => void;
    autoFocus?: boolean;
  }) {
    return (
      <input
        autoFocus={autoFocus}
        placeholder={placeholder}
        value={value ?? ""}
        onChange={(e) => onValueChange?.(e.target.value)}
        onKeyDown={onKeyDown}
      />
    );
  }
  function CommandList({ children }: { children: ReactNode }) {
    return <div>{children}</div>;
  }
  function CommandEmpty({ children }: { children: ReactNode }) {
    return <div>{children}</div>;
  }
  function CommandGroup({ children }: { children: ReactNode }) {
    return <div>{children}</div>;
  }
  function CommandItem({
    children,
    onSelect,
    value,
  }: {
    children: ReactNode;
    onSelect?: (v: string) => void;
    value?: string;
  }) {
    return (
      <button type="button" onClick={() => onSelect?.(value ?? "")}>
        {children}
      </button>
    );
  }
  return {
    Command,
    CommandInput,
    CommandList,
    CommandEmpty,
    CommandGroup,
    CommandItem,
  };
});

const TEST_RESOURCES = {
  en: { common: enCommon, squads: enSquads },
};

function I18nWrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
  );
}

function render(ui: React.ReactElement, options?: RenderOptions) {
  return rtlRender(ui, { wrapper: I18nWrapper, ...options });
}

import { RoleEditor } from "./squad-detail-page";

describe("RoleEditor (combobox)", () => {
  it("commits the typed role on Enter", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<RoleEditor value="" suggestions={[]} onSave={onSave} />);
    await userEvent.click(screen.getByRole("button", { name: /add role/i }));
    const input = screen.getByPlaceholderText(/type or pick/i);
    await userEvent.type(input, "Reviewer{Enter}");
    expect(onSave).toHaveBeenCalledWith("Reviewer");
  });

  it("commits a suggestion on click", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(
      <RoleEditor value="" suggestions={["Reviewer", "Implementer"]} onSave={onSave} />,
    );
    await userEvent.click(screen.getByRole("button", { name: /add role/i }));
    await userEvent.click(screen.getByRole("button", { name: "Reviewer" }));
    expect(onSave).toHaveBeenCalledWith("Reviewer");
  });

  it("does NOT commit on blur (clicking outside)", async () => {
    const onSave = vi.fn();
    render(<RoleEditor value="" suggestions={[]} onSave={onSave} />);
    await userEvent.click(screen.getByRole("button", { name: /add role/i }));
    await userEvent.type(screen.getByPlaceholderText(/type or pick/i), "Partial");
    await userEvent.click(document.body);
    expect(onSave).not.toHaveBeenCalled();
  });

  it("shows Loader2 when saving", () => {
    render(
      <RoleEditor value="Reviewer" suggestions={[]} saving onSave={() => Promise.resolve()} />,
    );
    expect(screen.getByTestId("role-editor-saving")).toBeInTheDocument();
  });

  it("renders Pencil icon as a persistent affordance when not saving", () => {
    render(
      <RoleEditor value="Reviewer" suggestions={[]} onSave={() => Promise.resolve()} />,
    );
    expect(screen.getByTestId("role-editor-pencil")).toBeInTheDocument();
  });

  it("discards the draft when the popover closes and does not commit it on reopen", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<RoleEditor value="" suggestions={[]} onSave={onSave} />);
    await userEvent.click(screen.getByRole("button", { name: /add role/i }));
    await userEvent.type(screen.getByPlaceholderText(/type or pick/i), "Part");
    await userEvent.click(screen.getByTestId("mock-popover-close"));
    await userEvent.click(screen.getByRole("button", { name: /add role/i }));
    const reopened = screen.getByPlaceholderText(/type or pick/i);
    expect(reopened).toHaveValue("");
    await userEvent.type(reopened, "{Enter}");
    expect(onSave).not.toHaveBeenCalled();
  });

  it("treats blank Enter as a no-op on an existing role (does not clear)", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<RoleEditor value="Reviewer" suggestions={[]} onSave={onSave} />);
    await userEvent.click(screen.getByRole("button", { name: /reviewer/i }));
    const input = screen.getByPlaceholderText(/type or pick/i);
    await userEvent.type(input, "{Enter}");
    expect(onSave).not.toHaveBeenCalled();
  });

  it("filters out the current value from suggestions", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(
      <RoleEditor value="Reviewer" suggestions={["Reviewer", "Implementer"]} onSave={onSave} />,
    );
    await userEvent.click(screen.getByRole("button", { name: /reviewer/i }));
    expect(screen.getByRole("button", { name: "Implementer" })).toBeInTheDocument();
    expect(screen.queryAllByRole("button", { name: "Reviewer" })).toHaveLength(1);
  });
});
