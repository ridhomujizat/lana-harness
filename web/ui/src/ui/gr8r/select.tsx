import {
  Fragment,
  useCallback,
  useContext,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import type {
  ButtonHTMLAttributes,
  KeyboardEvent as ReactKeyboardEvent,
  ReactNode,
} from "react";
import { createPortal } from "react-dom";
import { Ic } from "../icons";
import { FieldContext } from "./primitives";
import {
  cn,
  hidePopover,
  overlayContainer,
  placeAnchored,
  showPopover,
  trackAnchor,
  useEscapeGuard,
} from "./util";

/* Gr8r renders every dropdown as a button + .pop menu instead of the browser's
   native list (ui/select.js). This port follows the WAI-ARIA select-only
   combobox pattern so it can replace every native <select>: the trigger is the
   combobox and the popup is a listbox of options. */
export interface SelectOption {
  value: string;
  label: string;
  /* Consecutive options with the same group render under one heading. */
  group?: string;
  /* A small mark before the label, such as a provider logo. */
  icon?: ReactNode;
  /* A small mark before the group heading; the first option of a group
     sets it. */
  groupIcon?: ReactNode;
}

interface OptionGroup {
  group: string;
  icon?: ReactNode;
  options: SelectOption[];
}

function groupOptions(options: SelectOption[]): OptionGroup[] {
  const groups: OptionGroup[] = [];
  for (const option of options) {
    const group = option.group || "";
    const last = groups[groups.length - 1];
    if (last && last.group === group) {
      last.options.push(option);
    } else {
      groups.push({ group, icon: option.groupIcon, options: [option] });
    }
  }
  return groups;
}

export interface SelectProps
  extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, "onChange" | "value"> {
  options: SelectOption[];
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  size?: "md" | "sm";
}

export function Select({
  options,
  value,
  onChange,
  placeholder,
  size = "md",
  className,
  id,
  name,
  disabled,
  "aria-describedby": ariaDescribedBy,
  ...rest
}: SelectProps) {
  const [open, setOpen] = useState(false);
  /* Where the menu is portaled: the open dialog holding the trigger, else
     <body>. Chosen when the menu opens. */
  const [host, setHost] = useState<HTMLElement | null>(null);
  const btnRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const typeahead = useRef({ query: "", at: 0 });
  const field = useContext(FieldContext);
  const menuId = "select-menu-" + useId().replace(/[^A-Za-z0-9_-]/g, "");
  const selected = options.find((option) => option.value === value);
  const label = selected ? selected.label : placeholder ?? "";
  const escapeGuardRef = useEscapeGuard();

  const optionEls = (menu: HTMLElement) =>
    Array.from(menu.querySelectorAll<HTMLButtonElement>('[role="option"]'));

  /* Show the menu in the top layer, place it under the trigger and move focus
     to the current option when it opens. */
  useLayoutEffect(() => {
    const menu = menuRef.current;
    const anchor = btnRef.current;
    if (!open || !menu || !anchor) {
      return undefined;
    }
    showPopover(menu);
    placeAnchored(menu, anchor);
    const items = optionEls(menu);
    const preferred =
      items.find((item) => item.getAttribute("aria-selected") === "true") ||
      items[0];
    preferred?.focus({ preventScroll: true });

    const untrack = trackAnchor(menu, () => placeAnchored(menu, anchor));
    return () => {
      untrack();
      hidePopover(menu);
    };
  }, [open]);

  useEffect(() => {
    if (!open) {
      return undefined;
    }
    const onPointer = (event: PointerEvent) => {
      const target = event.target as Node | null;
      if (menuRef.current?.contains(target) || btnRef.current?.contains(target)) {
        return;
      }
      setOpen(false);
    };
    document.addEventListener("pointerdown", onPointer, true);
    return () => document.removeEventListener("pointerdown", onPointer, true);
  }, [open]);

  const close = useCallback((refocus: boolean) => {
    setOpen(false);
    if (refocus) {
      btnRef.current?.focus({ preventScroll: true });
    }
  }, []);

  const pick = useCallback(
    (next: string) => {
      setOpen(false);
      if (next !== value) {
        onChange(next);
      }
      btnRef.current?.focus({ preventScroll: true });
    },
    [onChange, value]
  );

  function openMenu() {
    if (!disabled) {
      setHost(overlayContainer(btnRef.current));
      setOpen(true);
    }
  }

  function handleTriggerKeyDown(event: ReactKeyboardEvent<HTMLButtonElement>) {
    const key = event.key;
    if (key === "Enter" || key === " " || key === "ArrowDown" || key === "ArrowUp") {
      event.preventDefault();
      openMenu();
    } else if (key === "Escape" && open) {
      escapeGuardRef.current = true;
      event.preventDefault();
      close(true);
    }
  }

  function handleMenuKeyDown(event: ReactKeyboardEvent<HTMLDivElement>) {
    const menu = menuRef.current;
    if (!menu) {
      return;
    }
    const items = optionEls(menu);
    if (items.length === 0) {
      return;
    }
    const key = event.key;
    const index = items.indexOf(document.activeElement as HTMLButtonElement);
    if (key === "ArrowDown") {
      event.preventDefault();
      items[Math.min(items.length - 1, index + 1)]?.focus({ preventScroll: true });
    } else if (key === "ArrowUp") {
      event.preventDefault();
      items[index <= 0 ? 0 : index - 1]?.focus({ preventScroll: true });
    } else if (key === "Home") {
      event.preventDefault();
      items[0]?.focus({ preventScroll: true });
    } else if (key === "End") {
      event.preventDefault();
      items[items.length - 1]?.focus({ preventScroll: true });
    } else if (key === "Enter" || key === " ") {
      event.preventDefault();
      const active = document.activeElement;
      const option = items.find((item) => item === active);
      if (option && option.dataset.value !== undefined) {
        pick(option.dataset.value);
      }
    } else if (key === "Escape") {
      escapeGuardRef.current = true;
      event.preventDefault();
      close(true);
    } else if (key === "Tab") {
      setOpen(false);
    } else if (
      key.length === 1 &&
      !event.metaKey &&
      !event.ctrlKey &&
      !event.altKey
    ) {
      const now = Date.now();
      const query =
        now - typeahead.current.at < 700
          ? typeahead.current.query + key
          : key;
      typeahead.current = { query, at: now };
      const match = items.find((item) =>
        (item.textContent || "").trim().toLowerCase().startsWith(query.toLowerCase())
      );
      match?.focus({ preventScroll: true });
    }
  }

  return (
    <>
      {name ? <input type="hidden" name={name} value={value} /> : null}
      <button
        {...rest}
        ref={btnRef}
        type="button"
        id={id ?? field?.id}
        className={cn(
          "btn",
          "btn-secondary",
          "select-btn",
          size === "sm" && "btn-sm",
          className
        )}
        role="combobox"
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={menuId}
        aria-describedby={ariaDescribedBy ?? field?.describedBy}
        disabled={disabled}
        onClick={() => {
          if (open) {
            close(false);
          } else {
            openMenu();
          }
        }}
        onKeyDown={handleTriggerKeyDown}
      >
        {selected && selected.icon ? <span className="select-icon">{selected.icon}</span> : null}
        <span className="trunc grow">{label}</span>
        <Ic name="chevron-down" size={size === "sm" ? 13 : 14} />
      </button>
      {open && host
        ? createPortal(
            <div
              ref={menuRef}
              id={menuId}
              className="pop select-menu enter"
              role="listbox"
              popover="manual"
              style={{ left: 0, top: 0 }}
              onKeyDown={handleMenuKeyDown}
            >
              {groupOptions(options).map((entry, groupIndex) => {
                const items = entry.options.map((option) => (
                  <button
                    key={option.value}
                    type="button"
                    role="option"
                    data-value={option.value}
                    aria-selected={option.value === value}
                    className="mi"
                    onClick={() => pick(option.value)}
                  >
                    {option.icon ? <span className="select-icon">{option.icon}</span> : null}
                    <span className="trunc grow">{option.label}</span>
                    {option.value === value ? (
                      <span className="ck">
                        <Ic name="check" size={14} />
                      </span>
                    ) : null}
                  </button>
                ));
                if (!entry.group) {
                  return <Fragment key={"g" + groupIndex}>{items}</Fragment>;
                }
                return (
                  <div key={"g" + groupIndex} role="group" aria-label={entry.group}>
                    <div className="mh" aria-hidden="true">
                      {entry.icon ? <span className="select-icon">{entry.icon}</span> : null}
                      {entry.group}
                    </div>
                    {items}
                  </div>
                );
              })}
            </div>,
            host
          )
        : null}
    </>
  );
}
