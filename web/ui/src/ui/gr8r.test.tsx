import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { useRef, useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  Avatar,
  Badge,
  Button,
  Checkbox,
  Field,
  IconButton,
  Input,
  Menu,
  MenuItem,
  Modal,
  Seg,
  Tabs,
  Toggle,
  ToastProvider,
  useToast,
} from "./gr8r";

afterEach(() => {
  vi.useRealTimers();
});

describe("Button", () => {
  it("renders the Gr8r variant, size and loading classes", () => {
    render(
      <Button variant="primary" size="lg" loading>
        Save
      </Button>
    );
    const button = screen.getByRole("button", { name: "Save" });
    expect(button.className).toContain("btn");
    expect(button.className).toContain("btn-primary");
    expect(button.className).toContain("btn-lg");
    expect(button.className).toContain("is-loading");
  });

  it("marks a disabled button and passes className through", () => {
    render(
      <Button disabled className="extra">
        Delete
      </Button>
    );
    const button = screen.getByRole("button", { name: "Delete" }) as HTMLButtonElement;
    expect(button.disabled).toBe(true);
    expect(button.className).toContain("extra");
  });

  it("renders an anchor when as=a", () => {
    render(
      <Button as="a" href="#/home" variant="ghost">
        Home
      </Button>
    );
    const link = screen.getByRole("link", { name: "Home" });
    expect(link.getAttribute("href")).toBe("#/home");
    expect(link.className).toContain("btn-ghost");
  });
});

describe("IconButton", () => {
  it("exposes the tip as data-tip and the accessible name", () => {
    render(<IconButton icon="star" tip="Favorite" />);
    const button = screen.getByRole("button", { name: "Favorite" });
    expect(button.getAttribute("data-tip")).toBe("Favorite");
  });

  it("adds the size class and data-tip-pos", () => {
    render(<IconButton icon="x" size="xs" tip="Close" tipPos="right" />);
    const button = screen.getByRole("button", { name: "Close" });
    expect(button.className).toContain("ibtn-xs");
    expect(button.getAttribute("data-tip-pos")).toBe("right");
  });
});

describe("Badge", () => {
  it("applies the tone and renders a dot", () => {
    render(
      <Badge tone="green" dot>
        Active
      </Badge>
    );
    const badge = screen.getByText("Active").closest(".badge");
    expect(badge).not.toBeNull();
    expect(badge?.className).toContain("green");
    expect(badge?.querySelector(".dot")).not.toBeNull();
  });
});

describe("Avatar", () => {
  it("shows the initials and the requested size", () => {
    render(<Avatar name="Ada Lovelace" size="lg" />);
    const avatar = screen.getByLabelText("Ada Lovelace");
    expect(avatar.textContent).toBe("AL");
    expect(avatar.className).toContain("lg");
  });

  it("renders the empty state with the user icon", () => {
    const { container } = render(<Avatar name="" />);
    const avatar = container.querySelector(".av.empty");
    expect(avatar).not.toBeNull();
    expect(avatar?.getAttribute("data-tip")).toBe("Unassigned");
  });
});

describe("Seg", () => {
  it("marks the selected button and calls onChange", () => {
    const onChange = vi.fn();
    render(
      <Seg
        value="a"
        onChange={onChange}
        options={[
          { value: "a", label: "A" },
          { value: "b", label: "B" },
        ]}
      />
    );
    const a = screen.getByRole("tab", { name: "A" });
    expect(a.className).toContain("on");
    expect(a.getAttribute("aria-selected")).toBe("true");
    fireEvent.click(screen.getByRole("tab", { name: "B" }));
    expect(onChange).toHaveBeenCalledWith("b");
  });
});

describe("Tabs", () => {
  it("marks the selected tab, renders counts and calls onChange", () => {
    const onChange = vi.fn();
    render(
      <Tabs
        value="overview"
        onChange={onChange}
        items={[
          { value: "overview", label: "Overview" },
          { value: "board", label: "Board", count: 3 },
        ]}
      />
    );
    const overview = screen.getByRole("tab", { name: "Overview" });
    expect(overview.className).toContain("on");
    expect(overview.getAttribute("aria-selected")).toBe("true");
    expect(screen.getByRole("tab", { name: /Board/ })).toBeTruthy();
    fireEvent.click(screen.getByRole("tab", { name: /Board/ }));
    expect(onChange).toHaveBeenCalledWith("board");
  });
});

describe("Toggle and Checkbox", () => {
  it("reflects and reports controlled state", () => {
    function Harness() {
      const [on, setOn] = useState(false);
      const [checked, setChecked] = useState(true);
      return (
        <>
          <Toggle checked={on} onChange={setOn} aria-label="notifications" />
          <Checkbox checked={checked} onChange={setChecked} aria-label="agree" round />
        </>
      );
    }
    render(<Harness />);
    const toggle = screen.getByRole("checkbox", {
      name: "notifications",
    }) as HTMLInputElement;
    expect(toggle.checked).toBe(false);
    fireEvent.click(toggle);
    const toggleAfter = screen.getByRole("checkbox", {
      name: "notifications",
    }) as HTMLInputElement;
    expect(toggleAfter.checked).toBe(true);

    const check = screen.getByRole("checkbox", { name: "agree" }) as HTMLInputElement;
    expect(check.checked).toBe(true);
    expect(check.className).toContain("round");
    fireEvent.click(check);
    const checkAfter = screen.getByRole("checkbox", {
      name: "agree",
    }) as HTMLInputElement;
    expect(checkAfter.checked).toBe(false);
  });
});

describe("Field", () => {
  it("wires the label, hint and error to the input", () => {
    render(
      <Field label="Email" hint="We never share it." error="Enter a valid email">
        <Input />
      </Field>
    );
    const input = screen.getByLabelText("Email") as HTMLInputElement;
    expect(input.className).toContain("is-error");
    expect(input.getAttribute("aria-invalid")).toBe("true");
    const describedBy = input.getAttribute("aria-describedby");
    expect(describedBy).toBeTruthy();
    const ids = (describedBy ?? "").split(" ");
    expect(ids).toHaveLength(2);
    for (const id of ids) {
      expect(document.getElementById(id)).not.toBeNull();
    }
  });
});

describe("Menu", () => {
  function Harness() {
    const [open, setOpen] = useState(false);
    const ref = useRef<HTMLButtonElement>(null);
    return (
      <>
        <button ref={ref} type="button" onClick={() => setOpen(true)}>
          Open
        </button>
        <Menu anchorRef={ref} open={open} onClose={() => setOpen(false)}>
          <MenuItem>First</MenuItem>
          <MenuItem>Second</MenuItem>
          <MenuItem>Third</MenuItem>
        </Menu>
      </>
    );
  }

  it("opens, moves focus with the arrow keys, and closes on Escape", () => {
    render(<Harness />);
    const trigger = screen.getByRole("button", { name: "Open" });
    fireEvent.click(trigger);

    const items = screen.getAllByRole("menuitem");
    expect(items).toHaveLength(3);
    expect(document.activeElement).toBe(items[0]);

    fireEvent.keyDown(items[0]!, { key: "ArrowDown" });
    expect(document.activeElement).toBe(items[1]);

    fireEvent.keyDown(items[1]!, { key: "Escape" });
    expect(screen.queryAllByRole("menuitem")).toHaveLength(0);
    expect(document.activeElement).toBe(trigger);
  });
});

describe("Modal", () => {
  function Harness() {
    const [open, setOpen] = useState(true);
    return (
      <>
        {open ? (
          <Modal
            title="Confirm"
            onClose={() => setOpen(false)}
            footer={<Button>Save</Button>}
          >
            <Input aria-label="Name" />
          </Modal>
        ) : null}
      </>
    );
  }

  it("is modal, traps focus and closes on Escape", () => {
    render(<Harness />);
    const dialog = screen.getByRole("dialog", { name: "Confirm" });
    expect(dialog.getAttribute("aria-modal")).toBe("true");
    expect(dialog.contains(document.activeElement)).toBe(true);

    const save = within(dialog).getByRole("button", { name: "Save" });
    const close = within(dialog).getByRole("button", { name: "Close" });
    save.focus();
    fireEvent.keyDown(save, { key: "Tab" });
    expect(document.activeElement).toBe(close);

    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.queryByRole("dialog")).toBeNull();
  });
});

describe("useToast", () => {
  function Harness() {
    const { show } = useToast();
    return (
      <button
        type="button"
        onClick={() => show({ title: "Task saved", tone: "ok" })}
      >
        Go
      </button>
    );
  }

  it("shows a toast and auto-dismisses it", () => {
    vi.useFakeTimers();
    render(
      <ToastProvider>
        <Harness />
      </ToastProvider>
    );

    fireEvent.click(screen.getByRole("button", { name: "Go" }));
    const toast = screen.getByText("Task saved").closest(".toast");
    expect(toast).not.toBeNull();
    expect(toast?.getAttribute("role")).toBe("status");

    act(() => {
      vi.advanceTimersByTime(4200);
    });
    expect(screen.getByText("Task saved").closest(".toast")?.className).toContain(
      "out"
    );

    act(() => {
      vi.advanceTimersByTime(200);
    });
    expect(screen.queryByText("Task saved")).toBeNull();
  });
});
