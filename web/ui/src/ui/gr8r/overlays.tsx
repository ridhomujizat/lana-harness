import { useEffect, useId, useLayoutEffect, useRef } from "react";
import type {
  ButtonHTMLAttributes,
  KeyboardEvent as ReactKeyboardEvent,
  ReactNode,
  RefObject,
} from "react";
import { createPortal } from "react-dom";
import { Ic } from "../icons";
import { IconButton, Kbd } from "./primitives";
import {
  cn,
  focusableElements,
  hidePopover,
  overlayContainer,
  placeAnchored,
  showPopover,
  trackAnchor,
  useEscapeGuard,
} from "./util";

/* ------------------------------------------------------------------ */
/* Popover (Gr8r .pop.floating + placePop)                            */
/* ------------------------------------------------------------------ */

export interface PopoverProps {
  anchorRef: RefObject<HTMLElement | null>;
  open: boolean;
  onClose: () => void;
  role?: string;
  className?: string;
  width?: number | string;
  children: ReactNode;
}

export function Popover({
  anchorRef,
  open,
  onClose,
  role = "menu",
  className,
  width,
  children,
}: PopoverProps) {
  const menuRef = useRef<HTMLDivElement>(null);
  const escapeGuardRef = useEscapeGuard();

  useLayoutEffect(() => {
    const menu = menuRef.current;
    if (!open || !menu) {
      return undefined;
    }
    showPopover(menu);
    if (anchorRef.current) {
      placeAnchored(menu, anchorRef.current);
    }
    const items = menu.querySelectorAll<HTMLElement>(".mi");
    const preferred = menu.querySelector<HTMLElement>(
      '[aria-checked="true"], [aria-selected="true"]'
    );
    (preferred || items[0])?.focus({ preventScroll: true });

    const untrack = trackAnchor(menu, () => {
      if (anchorRef.current) {
        placeAnchored(menu, anchorRef.current);
      }
    });
    return () => {
      untrack();
      hidePopover(menu);
    };
  }, [open, anchorRef]);

  useEffect(() => {
    if (!open) {
      return undefined;
    }
    const onPointer = (event: PointerEvent) => {
      const target = event.target as Node | null;
      if (menuRef.current?.contains(target)) {
        return;
      }
      if (anchorRef.current?.contains(target)) {
        return;
      }
      onClose();
    };
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        escapeGuardRef.current = true;
        event.preventDefault();
        onClose();
      }
    };
    document.addEventListener("pointerdown", onPointer, true);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("pointerdown", onPointer, true);
      document.removeEventListener("keydown", onKey);
    };
  }, [open, onClose, anchorRef, escapeGuardRef]);

  /* Return focus to the trigger when the popover closes or unmounts. */
  useEffect(() => {
    if (!open) {
      return undefined;
    }
    const anchor = anchorRef.current;
    return () => {
      if (anchor && typeof anchor.focus === "function") {
        anchor.focus({ preventScroll: true });
      }
    };
  }, [open, anchorRef]);

  function handleKeyDown(event: ReactKeyboardEvent<HTMLDivElement>) {
    const { key } = event;
    if (key !== "ArrowDown" && key !== "ArrowUp" && key !== "Home" && key !== "End") {
      return;
    }
    const menu = menuRef.current;
    if (!menu) {
      return;
    }
    const items = Array.from(menu.querySelectorAll<HTMLElement>(".mi")).filter(
      (el) => !el.hasAttribute("disabled")
    );
    if (items.length === 0) {
      return;
    }
    event.preventDefault();
    const active = document.activeElement;
    const index = items.indexOf(active as HTMLElement);
    let next = index;
    if (key === "ArrowDown") {
      next = index < 0 ? 0 : Math.min(items.length - 1, index + 1);
    } else if (key === "ArrowUp") {
      next = index < 0 ? items.length - 1 : Math.max(0, index - 1);
    } else if (key === "Home") {
      next = 0;
    } else {
      next = items.length - 1;
    }
    items[next]?.focus({ preventScroll: true });
  }

  if (!open) {
    return null;
  }
  return createPortal(
    <div
      ref={menuRef}
      className={cn("pop", "floating", "enter", className)}
      role={role}
      popover="manual"
      style={{ left: 0, top: 0, width }}
      onKeyDown={handleKeyDown}
    >
      {children}
    </div>,
    /* The anchor is mounted before the popover opens; it decides whether the
       menu goes into an open modal dialog (see overlayContainer). */
    // eslint-disable-next-line react-hooks/refs -- read once per open render, never written
    overlayContainer(anchorRef.current)
  );
}

/* ------------------------------------------------------------------ */
/* Menu                                                               */
/* ------------------------------------------------------------------ */

export function Menu(props: PopoverProps) {
  return <Popover {...props} role="menu" />;
}

export interface MenuItemProps
  extends ButtonHTMLAttributes<HTMLButtonElement> {
  icon?: string;
  danger?: boolean;
  kbd?: string;
  checked?: boolean;
  right?: ReactNode;
}

export function MenuItem({
  icon,
  danger,
  kbd,
  checked,
  right,
  className,
  children,
  type,
  ...rest
}: MenuItemProps) {
  return (
    <button
      type={type ?? "button"}
      role="menuitem"
      className={cn("mi", danger && "danger", className)}
      {...rest}
    >
      {icon ? <Ic name={icon} size={15} /> : null}
      <span className="trunc">{children}</span>
      {kbd ? (
        <span className="r">
          <Kbd>{kbd}</Kbd>
        </span>
      ) : right ? (
        <span className="r">{right}</span>
      ) : null}
      {checked ? (
        <span className="ck">
          <Ic name="check" size={14} />
        </span>
      ) : null}
    </button>
  );
}

export function MenuSeparator() {
  return <div className="msep" role="separator" />;
}

/* ------------------------------------------------------------------ */
/* Modal                                                              */
/* ------------------------------------------------------------------ */

export interface ModalProps {
  title: ReactNode;
  subtitle?: ReactNode;
  size?: "sm" | "lg";
  dismissible?: boolean;
  onClose?: () => void;
  footer?: ReactNode;
  className?: string;
  children?: ReactNode;
}

export function Modal({
  title,
  subtitle,
  size,
  dismissible = true,
  onClose,
  footer,
  className,
  children,
}: ModalProps) {
  const modalRef = useRef<HTMLDivElement>(null);
  const returnFocusRef = useRef<Element | null>(null);
  const titleId = "modal-title-" + useId().replace(/[^A-Za-z0-9_-]/g, "");

  useEffect(() => {
    returnFocusRef.current = document.activeElement;
    const modal = modalRef.current;
    if (modal) {
      const preferred = modal.querySelector<HTMLElement>("[data-autofocus]");
      const target = preferred || focusableElements(modal)[0];
      target?.focus({ preventScroll: true });
    }
    return () => {
      const previous = returnFocusRef.current;
      if (previous instanceof HTMLElement && typeof previous.focus === "function") {
        previous.focus({ preventScroll: true });
      }
    };
  }, []);

  useEffect(() => {
    if (!dismissible) {
      return undefined;
    }
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        onClose?.();
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [dismissible, onClose]);

  function handleKeyDown(event: ReactKeyboardEvent<HTMLDivElement>) {
    if (event.key !== "Tab") {
      return;
    }
    const modal = modalRef.current;
    if (!modal) {
      return;
    }
    const focusable = focusableElements(modal);
    if (focusable.length === 0) {
      event.preventDefault();
      return;
    }
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    if (!first || !last) {
      return;
    }
    const active = document.activeElement;
    if (event.shiftKey && active === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && active === last) {
      event.preventDefault();
      first.focus();
    }
  }

  return createPortal(
    <>
      <div className="scrim enter" onClick={dismissible ? onClose : undefined} />
      <div
        className="modal-wrap"
        onMouseDown={(event) => {
          if (dismissible && event.target === event.currentTarget) {
            onClose?.();
          }
        }}
      >
        <div
          ref={modalRef}
          className={cn("modal", "enter", size, className)}
          role="dialog"
          aria-modal="true"
          aria-labelledby={titleId}
          onKeyDown={handleKeyDown}
        >
          <div className="modal-h">
            <div>
              <h2 id={titleId}>{title}</h2>
              {subtitle ? (
                <div className="muted" style={{ fontSize: 12.5, marginTop: 2 }}>
                  {subtitle}
                </div>
              ) : null}
            </div>
            {dismissible ? (
              <IconButton icon="x" size="sm" label="Close" onClick={onClose} />
            ) : null}
          </div>
          <div className="modal-b">{children}</div>
          {footer ? <div className="modal-f">{footer}</div> : null}
        </div>
      </div>
    </>,
    document.body
  );
}

/* ------------------------------------------------------------------ */
/* Drawer                                                             */
/* ------------------------------------------------------------------ */

export interface DrawerProps {
  title: ReactNode;
  full?: boolean;
  dismissible?: boolean;
  onClose?: () => void;
  header?: ReactNode;
  className?: string;
  children?: ReactNode;
}

export function Drawer({
  title,
  full,
  dismissible = true,
  onClose,
  header,
  className,
  children,
}: DrawerProps) {
  const drawerRef = useRef<HTMLElement>(null);
  const titleId = "drawer-title-" + useId().replace(/[^A-Za-z0-9_-]/g, "");

  useEffect(() => {
    const previous = document.activeElement;
    drawerRef.current?.focus({ preventScroll: true });
    return () => {
      if (previous instanceof HTMLElement && typeof previous.focus === "function") {
        previous.focus({ preventScroll: true });
      }
    };
  }, []);

  useEffect(() => {
    if (!dismissible) {
      return undefined;
    }
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        onClose?.();
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [dismissible, onClose]);

  return createPortal(
    <>
      {full ? (
        <div
          className="drawer-scrim full enter"
          onClick={dismissible ? onClose : undefined}
        />
      ) : null}
      <aside
        ref={drawerRef}
        className={cn("drawer", "enter", "fx-tabs", full && "full", className)}
        role="dialog"
        aria-modal={full ? true : undefined}
        aria-labelledby={titleId}
        tabIndex={-1}
      >
        <h2 className="sr" id={titleId}>
          {title}
        </h2>
        <div className="drawer-h">
          {header}
          <span className="sp" />
          {dismissible ? (
            <IconButton
              icon="x"
              size="sm"
              tip="Close  Esc"
              label="Close"
              onClick={onClose}
            />
          ) : null}
        </div>
        <div className="drawer-b">{children}</div>
      </aside>
    </>,
    document.body
  );
}
