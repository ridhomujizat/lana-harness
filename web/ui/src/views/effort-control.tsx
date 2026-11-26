import { useEffect, useRef, useState } from "react";
import type { KeyboardEvent } from "react";
import { Select } from "../ui/gr8r";
import type { SelectOption } from "../ui/gr8r";
import type { EffortLevels } from "./effort";
import { effortName } from "./effort";

/* Spec 76 (REQ-MES-006): the effort control beside a model picker. "Default"
   plus the picked model's own levels, as a segmented radio group on desktop
   and a select on a phone; hidden when the model has none. A saved level the
   model does not list (an older config) stays selected as its own option,
   marked "(not offered by this model)", until the owner changes it. */

export const NOT_OFFERED = "(not offered by this model)";

const PHONE_QUERY = "(max-width: 640px)";

function phoneNow(): boolean {
  return typeof window !== "undefined" && typeof window.matchMedia === "function"
    ? window.matchMedia(PHONE_QUERY).matches
    : false;
}

function usePhoneWidth(): boolean {
  const [phone, setPhone] = useState(phoneNow);
  useEffect(() => {
    if (typeof window === "undefined" || typeof window.matchMedia !== "function") {
      return undefined;
    }
    const query = window.matchMedia(PHONE_QUERY);
    const update = () => setPhone(query.matches);
    update();
    if (typeof query.addEventListener === "function") {
      query.addEventListener("change", update);
      return () => query.removeEventListener("change", update);
    }
    return undefined;
  }, []);
  return phone;
}

/* "Default (High)" when the model default is known, else "Default". */
export function defaultEffortLabel(defaultEffort: string | undefined): string {
  return defaultEffort ? "Default (" + effortName(defaultEffort) + ")" : "Default";
}

export interface EffortControlProps {
  /* The picked model's levels; null or no levels hides the control. */
  levels: EffortLevels | null | undefined;
  /* The saved reasoning_effort; "" is the model default. */
  value: string;
  onChange: (value: string) => void;
  /* The accessible name of the group or select. */
  label?: string;
  id?: string;
  disabled?: boolean;
  /* A select at every width, for tight rows. */
  compact?: boolean;
  /* Optional tooltip text per level, such as the catalog descriptions. */
  descriptions?: Record<string, string>;
}

export function EffortControl({
  levels,
  value,
  onChange,
  label = "Reasoning effort",
  id,
  disabled,
  compact,
  descriptions,
}: EffortControlProps) {
  const phone = usePhoneWidth();
  const groupRef = useRef<HTMLDivElement | null>(null);
  const efforts = levels ? levels.efforts : [];
  if (!levels || efforts.length === 0) {
    return null;
  }
  const current = String(value || "");
  const unsupported = current !== "" && !efforts.includes(current);
  const defaultLabel = defaultEffortLabel(levels.default_effort);
  const items: { value: string; label: string; title?: string; stale?: boolean }[] = [
    {
      value: "",
      label: defaultLabel,
      title: levels.default_effort
        ? "The model's default: " + effortName(levels.default_effort)
        : "The model's default",
    },
    ...efforts.map((level) => ({
      value: level,
      label: effortName(level),
      title: descriptions && descriptions[level] ? descriptions[level] : undefined,
    })),
  ];
  if (unsupported) {
    items.push({
      value: current,
      label: effortName(current) + " " + NOT_OFFERED,
      title: "Saved level that this model does not list",
      stale: true,
    });
  }

  if (compact || phone) {
    const options: SelectOption[] = items.map((item) => ({
      value: item.value,
      label: item.label,
    }));
    return (
      <div className="effort-control">
        <Select
          id={id}
          aria-label={label}
          size={compact ? "sm" : "md"}
          value={current}
          options={options}
          disabled={disabled}
          onChange={onChange}
        />
      </div>
    );
  }

  const selectedIndex = Math.max(
    0,
    items.findIndex((item) => item.value === current)
  );

  function move(event: KeyboardEvent<HTMLDivElement>) {
    const step =
      event.key === "ArrowRight" || event.key === "ArrowDown"
        ? 1
        : event.key === "ArrowLeft" || event.key === "ArrowUp"
          ? -1
          : 0;
    const edge = event.key === "Home" ? 0 : event.key === "End" ? items.length - 1 : -1;
    if (!step && edge < 0) {
      return;
    }
    event.preventDefault();
    const next =
      edge >= 0 ? edge : (selectedIndex + step + items.length) % items.length;
    const target = items[next];
    if (!target) {
      return;
    }
    onChange(target.value);
    const buttons = groupRef.current
      ? groupRef.current.querySelectorAll<HTMLButtonElement>('[role="radio"]')
      : null;
    if (buttons && buttons[next]) {
      buttons[next].focus();
    }
  }

  return (
    <div className="effort-control">
      <div
        ref={groupRef}
        id={id}
        className="seg effort-seg"
        role="radiogroup"
        aria-label={label}
        aria-disabled={disabled || undefined}
        onKeyDown={disabled ? undefined : move}
      >
        {items.map((item, index) => {
          const on = index === selectedIndex;
          return (
            <button
              key={item.value || "default"}
              type="button"
              role="radio"
              aria-checked={on}
              className={(on ? "on" : "") + (item.stale ? " effort-stale" : "") || undefined}
              tabIndex={on ? 0 : -1}
              title={item.title}
              disabled={disabled}
              onClick={() => onChange(item.value)}
            >
              {item.label}
            </button>
          );
        })}
      </div>
    </div>
  );
}
