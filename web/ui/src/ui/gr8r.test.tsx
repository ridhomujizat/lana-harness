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
  Select,
  Tabs,
  Toggle,
  ToastProvider,
  useToast,
} from "./gr8r";
import { Dialog } from "./ui";

afterEach(() => {
  vi.useRealTimers();
});

/* jsdom has no modal dialog; showModal only needs to set open here. */
function stubModalDialog(): void {
  const proto = HTMLDialogElement.prototype;
  if (typeof proto.showModal !== "function") {
    proto.showModal = () => {};
  }
  if (typeof proto.close !== "function") {
    proto.close = () => {};
  }
  vi.spyOn(HTMLDialogElement.prototype, "showModal").mockImplementation(
    function (this: HTMLDialogElement) {
      this.open = true;
    }
  );
  vi.spyOn(HTMLDialogElement.prototype, "close").mockImplementation(
    function (this: HTMLDialogElement) {
      this.open = false;
    }
  );
}

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

describe("Select", () => {
  const options = [
    { value: "a", label: "Alpha" },
    { value: "b", label: "Beta" },
  ];

  function rect(values: Partial<DOMRect>): DOMRect {
    return {
      top: 0,
      bottom: 0,
      left: 0,
      right: 0,
      width: 0,
      height: 0,
      x: 0,
      y: 0,
      toJSON: () => ({}),
      ...values,
    } as DOMRect;
  }

  function renderSelect(): void {
    render(
      <Select
        aria-label="Model"
        value="a"
        options={options}
        onChange={() => {}}
      />
    );
  }

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("shows the menu through the Popover API and hides it on close", () => {
    const show = vi.spyOn(HTMLElement.prototype, "showPopover");
    const hide = vi.spyOn(HTMLElement.prototype, "hidePopover");

    renderSelect();
    const trigger = screen.getByRole("combobox", { name: "Model" });
    expect(show).not.toHaveBeenCalled();

    fireEvent.click(trigger);
    const menu = screen.getByRole("listbox");
    expect(show).toHaveBeenCalledTimes(1);
    expect(menu.getAttribute("popover")).toBe("manual");

    fireEvent.click(screen.getByRole("option", { name: "Beta" }));
    expect(screen.queryByRole("listbox")).toBeNull();
    expect(hide).toHaveBeenCalledTimes(1);
  });

  it("flips the menu above the trigger when there is no room below", () => {
    vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockReturnValue(300);
    vi.spyOn(HTMLElement.prototype, "offsetWidth", "get").mockReturnValue(200);
    vi.spyOn(window, "innerHeight", "get").mockReturnValue(400);
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue(
      rect({ top: 350, bottom: 370, left: 40, right: 240, width: 200, height: 20 })
    );

    renderSelect();
    fireEvent.click(screen.getByRole("combobox", { name: "Model" }));

    expect(screen.getByRole("listbox").style.top).toBe("46px");
  });

  it("caps the menu height to the available space below the trigger", () => {
    vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockReturnValue(300);
    vi.spyOn(HTMLElement.prototype, "offsetWidth", "get").mockReturnValue(200);
    vi.spyOn(window, "innerHeight", "get").mockReturnValue(400);
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue(
      rect({ top: 100, bottom: 120, left: 40, right: 240, width: 200, height: 20 })
    );

    renderSelect();
    fireEvent.click(screen.getByRole("combobox", { name: "Model" }));

    const menu = screen.getByRole("listbox");
    expect(menu.style.maxHeight).toBe("268px");
    expect(menu.style.top).toBe("124px");
  });

  it("closes only the menu on Escape, leaving an open dialog alone", () => {
    const onClose = vi.fn();
    stubModalDialog();

    render(
      <Dialog title="Create" onClose={onClose}>
        <Select aria-label="Model" value="a" options={options} onChange={() => {}} />
      </Dialog>
    );
    fireEvent.click(screen.getByRole("combobox", { name: "Model" }));
    fireEvent.keyDown(screen.getByRole("option", { name: "Alpha" }), {
      key: "Escape",
    });
    expect(screen.queryByRole("listbox")).toBeNull();
    expect(screen.getByRole("dialog", { name: "Create" })).toBeTruthy();
    expect(onClose).not.toHaveBeenCalled();
  });

  /* A modal dialog makes everything outside it inert, so a menu portaled to
     <body> paints on top but takes no clicks or wheel scrolls. */
  it("renders the menu inside the open dialog that holds the trigger", () => {
    stubModalDialog();
    const onChange = vi.fn();
    const onClose = vi.fn();
    render(
      <Dialog title="Create" onClose={onClose}>
        <Select aria-label="Model" value="a" options={options} onChange={onChange} />
      </Dialog>
    );
    const dialog = screen.getByRole("dialog", { name: "Create" });
    const trigger = screen.getByRole("combobox", { name: "Model" });

    fireEvent.click(trigger);
    const menu = screen.getByRole("listbox");
    expect(menu.parentElement).toBe(dialog);

    const beta = screen.getByRole("option", { name: "Beta" });
    fireEvent.pointerDown(beta);
    fireEvent.click(beta);
    expect(onChange).toHaveBeenCalledWith("b");
    expect(screen.queryByRole("listbox")).toBeNull();
    expect(document.activeElement).toBe(trigger);
    expect(screen.getByRole("dialog", { name: "Create" })).toBe(dialog);
    expect(onClose).not.toHaveBeenCalled();
  });

  it("renders the menu in the body when no dialog is open", () => {
    renderSelect();
    fireEvent.click(screen.getByRole("combobox", { name: "Model" }));
    expect(screen.getByRole("listbox").parentElement).toBe(document.body);
  });

  it("keeps its scroll position when the menu itself scrolls", () => {
    vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockReturnValue(300);
    vi.spyOn(window, "innerHeight", "get").mockReturnValue(400);
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue(
      rect({ top: 100, bottom: 120, left: 40, right: 240, width: 200, height: 20 })
    );
    renderSelect();
    fireEvent.click(screen.getByRole("combobox", { name: "Model" }));
    const menu = screen.getByRole("listbox");
    expect(menu.style.maxHeight).toBe("268px");

    /* placeAnchored clears the cap to measure; a scroll inside the menu must
       not trigger it, or the list snaps back to the top. */
    menu.style.maxHeight = "1px";
    fireEvent.scroll(menu);
    expect(menu.style.maxHeight).toBe("1px");

    fireEvent.scroll(window);
    expect(menu.style.maxHeight).toBe("268px");
  });
});

describe("Menu inside a modal dialog", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  function Harness({ onPick }: { onPick: () => void }) {
    const anchorRef = useRef<HTMLButtonElement>(null);
    const [open, setOpen] = useState(false);
    return (
      <Dialog title="Edit" onClose={() => {}}>
        <button type="button" ref={anchorRef} onClick={() => setOpen(true)}>
          More
        </button>
        <Menu anchorRef={anchorRef} open={open} onClose={() => setOpen(false)}>
          <MenuItem
            onClick={() => {
              onPick();
              setOpen(false);
            }}
          >
            Rename
          </MenuItem>
        </Menu>
      </Dialog>
    );
  }

  it("portals the menu into the dialog and lets an item be picked", () => {
    stubModalDialog();
    const onPick = vi.fn();
    render(<Harness onPick={onPick} />);
    const dialog = screen.getByRole("dialog", { name: "Edit" });

    fireEvent.click(screen.getByRole("button", { name: "More" }));
    const menu = screen.getByRole("menu");
    expect(menu.parentElement).toBe(dialog);
    expect(menu.getAttribute("popover")).toBe("manual");

    const item = screen.getByRole("menuitem", { name: "Rename" });
    fireEvent.pointerDown(item);
    fireEvent.click(item);
    expect(onPick).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("menu")).toBeNull();
    expect(screen.getByRole("dialog", { name: "Edit" })).toBe(dialog);
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
