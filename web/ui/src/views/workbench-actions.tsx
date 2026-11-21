import { useEffect, useMemo, useRef, useState, type ReactNode, type TouchEvent as ReactTouchEvent } from "react";

import { Button, Input, Menu, MenuItem, MenuSeparator, Modal } from "../ui/gr8r";

/* Spec 55: rename and close for spaces and tabs. One menu and one dialog at
   a time live in the page; rows and tabs open them by right-click, the ⋯
   button, a double-click (rename), or a long press on a phone. */

export type MenuTarget =
  | { kind: "space"; spaceId: string; anchor: HTMLElement }
  | { kind: "tab"; spaceId: string; tabId: string; anchor: HTMLElement };

export type DialogState =
  | { kind: "rename-space"; spaceId: string; value: string }
  | { kind: "rename-tab"; spaceId: string; tabId: string; value: string }
  | { kind: "close-space"; spaceId: string; label: string; terminals: number }
  | null;

/* A long press (500 ms without moving) on touch screens. Plain handlers (no
   hook) so list rows can use them; the press state lives per element. */
const presses = new WeakMap<HTMLElement, { timer: ReturnType<typeof setTimeout>; x: number; y: number; fired: boolean }>();

export function longPress(onLong: (el: HTMLElement) => void) {
  const cancel = (el: HTMLElement) => {
    const p = presses.get(el);
    if (p) clearTimeout(p.timer);
  };
  return {
    onTouchStart: (e: ReactTouchEvent<HTMLElement>) => {
      const t = e.touches[0];
      if (!t) return;
      const el = e.currentTarget;
      cancel(el);
      const press = {
        x: t.clientX,
        y: t.clientY,
        fired: false,
        timer: setTimeout(() => {
          press.fired = true;
          if (navigator.vibrate) navigator.vibrate(10);
          onLong(el);
        }, 500),
      };
      presses.set(el, press);
    },
    onTouchMove: (e: ReactTouchEvent<HTMLElement>) => {
      const t = e.touches[0];
      const p = presses.get(e.currentTarget);
      if (!t || !p) return;
      if (Math.abs(t.clientX - p.x) > 8 || Math.abs(t.clientY - p.y) > 8) cancel(e.currentTarget);
    },
    onTouchEnd: (e: ReactTouchEvent<HTMLElement>) => {
      const p = presses.get(e.currentTarget);
      cancel(e.currentTarget);
      /* The press opened the menu: do not also run the tap. */
      if (p?.fired) e.preventDefault();
    },
    onTouchCancel: (e: ReactTouchEvent<HTMLElement>) => cancel(e.currentTarget),
  };
}

export function ActionMenu({
  target,
  onClose,
  onRename,
  onCloseTarget,
  extra,
}: {
  target: MenuTarget;
  onClose: () => void;
  onRename: () => void;
  onCloseTarget: () => void;
  /* Items above Rename, such as the phone's text size (spec 66). */
  extra?: ReactNode;
}) {
  /* The menu anchors to the element that opened it. */
  const anchorRef = useMemo(() => ({ current: target.anchor as HTMLElement | null }), [target.anchor]);
  const what = target.kind === "space" ? "space" : "tab";
  return (
    <Menu anchorRef={anchorRef} open onClose={onClose} className="wb-menu">
      {extra}
      <MenuItem
        icon="pencil"
        onClick={() => {
          onClose();
          onRename();
        }}
      >
        Rename {what}
      </MenuItem>
      <MenuSeparator />
      <MenuItem
        icon="x"
        danger
        onClick={() => {
          onClose();
          onCloseTarget();
        }}
      >
        Close {what}
      </MenuItem>
    </Menu>
  );
}

export function RenameDialog({
  title,
  initial,
  placeholder,
  hint,
  onCancel,
  onSave,
}: {
  title: string;
  initial: string;
  placeholder: string;
  hint: string;
  onCancel: () => void;
  onSave: (value: string) => void;
}) {
  const [value, setValue] = useState(initial);
  const formRef = useRef<HTMLFormElement | null>(null);
  /* On a phone the keyboard covers the lower half of the layout viewport:
     keep the dialog inside the visible part, near its top. */
  useEffect(() => {
    const vv = window.visualViewport;
    const wrap = formRef.current?.closest<HTMLElement>(".modal-wrap");
    if (!vv || !wrap) return;
    const place = () => {
      wrap.style.top = vv.offsetTop + "px";
      wrap.style.height = vv.height + "px";
      wrap.style.bottom = "auto";
      wrap.style.paddingTop = window.innerHeight - vv.height > 120 ? "12px" : "";
    };
    place();
    vv.addEventListener("resize", place);
    vv.addEventListener("scroll", place);
    return () => {
      vv.removeEventListener("resize", place);
      vv.removeEventListener("scroll", place);
    };
  }, []);
  return (
    <Modal
      title={title}
      size="sm"
      className="wb-rename-modal"
      onClose={onCancel}
      footer={
        <>
          <Button onClick={onCancel}>Cancel</Button>
          <Button variant="primary" onClick={() => onSave(value)}>
            Save
          </Button>
        </>
      }
    >
      <form
        ref={formRef}
        className="wb-rename"
        onSubmit={(e) => {
          e.preventDefault();
          onSave(value);
        }}
      >
        <Input
          value={value}
          placeholder={placeholder}
          onChange={(e) => setValue(e.target.value)}
          data-autofocus
          aria-label={title}
          maxLength={60}
        />
        <p className="muted wb-rename-hint">{hint}</p>
      </form>
    </Modal>
  );
}

export function CloseSpaceDialog({
  label,
  terminals,
  onCancel,
  onConfirm,
}: {
  label: string;
  terminals: number;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  return (
    <Modal
      title={"Close " + label + "?"}
      size="sm"
      onClose={onCancel}
      footer={
        <>
          <Button onClick={onCancel}>Cancel</Button>
          <Button variant="danger" onClick={onConfirm} data-autofocus>
            Close space
          </Button>
        </>
      }
    >
      <p>
        {terminals === 1
          ? "Its terminal is stopped, with whatever runs in it."
          : "Its " + terminals + " terminals are stopped, with whatever runs in them."}
      </p>
    </Modal>
  );
}
