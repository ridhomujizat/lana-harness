import { useContext, useEffect, useId, useMemo, useRef, useState } from "react";
import type {
  ButtonHTMLAttributes,
  KeyboardEvent as ReactKeyboardEvent,
  ReactNode,
} from "react";
import { FieldContext, Popover, cn, useEscapeGuard } from "../ui/gr8r";
import type { SelectOption } from "../ui/gr8r";
import { Ic } from "../ui/icons";

/* The model picker: a searchable, provider-grouped list of the router
   choices (combos, API providers, Claude Code and Antigravity accounts). The
   trigger looks like the Gr8r Select so forms keep their size; the popup holds
   a search box (the combobox) over a listbox whose options are reached with
   aria-activedescendant, so focus never leaves the search box. */

export interface ModelOption extends SelectOption {
  /* The muted second line. Defaults to the address (value) for a grouped
     option whose label is not the address itself. */
  meta?: string;
  /* A small muted note on the right, such as "4 levels". */
  hint?: string;
  /* A small tag after the label, such as "Legacy". */
  tag?: string;
  /* Always listed, whatever the search: an action such as "Other…". */
  sticky?: boolean;
}

export const CURRENT_GROUP = "Current";
export const AGENT_DEFAULT_GROUP = "Agent default";
const COMBOS_LABEL = "Combos";
/* With an empty search and more models than this, the groups other than
   Combos and the selected model's group start collapsed. */
export const COLLAPSE_THRESHOLD = 40;
export const COLLAPSE_STORAGE_KEY = "lana.modelPicker.collapsed";

/* The effort hint of a model with known levels. */
export function effortHint(efforts: string[] | undefined): string | undefined {
  if (!efforts || efforts.length === 0) {
    return undefined;
  }
  return efforts.length === 1 ? "1 level" : efforts.length + " levels";
}

function optionMeta(option: ModelOption): string {
  if (option.meta !== undefined) {
    return option.meta;
  }
  return option.group && option.value && option.value !== option.label ? option.value : "";
}

/* True when every word of the query appears in the option's label, address,
   group, or meta, ignoring case. */
export function matchesModel(option: ModelOption, query: string): boolean {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) {
    return true;
  }
  const hay = [option.label, option.value, option.group || "", option.meta || ""]
    .join(" ")
    .toLowerCase();
  return words.every((word) => hay.includes(word));
}

interface Section {
  key: string;
  /* "" for the ungrouped options before or after the groups. */
  group: string;
  icon?: ReactNode;
  options: ModelOption[];
}

/* Ungrouped options before the first group stay on top and those after the
   last group stay at the bottom. In between: Current, Agent default, Combos,
   then the selected model's group, then the other groups in their order. */
function buildSections(options: ModelOption[], value: string): Section[] {
  const lead: ModelOption[] = [];
  const tail: ModelOption[] = [];
  const groups = new Map<string, Section>();
  for (const option of options) {
    if (!option.group) {
      (groups.size > 0 ? tail : lead).push(option);
      continue;
    }
    const section = groups.get(option.group);
    if (section) {
      section.options.push(option);
      if (!section.icon) {
        section.icon = option.groupIcon ?? option.icon;
      }
    } else {
      groups.set(option.group, {
        key: "g:" + option.group,
        group: option.group,
        icon:
          option.groupIcon ??
          (option.group === COMBOS_LABEL ? <Ic name="layers" size={14} /> : option.icon),
        options: [option],
      });
    }
  }
  const selectedGroup = options.find((option) => option.value === value)?.group || "";
  const first = [CURRENT_GROUP, AGENT_DEFAULT_GROUP, COMBOS_LABEL, selectedGroup].filter(
    (group, index, list) => group && groups.has(group) && list.indexOf(group) === index
  );
  const order = first.concat(Array.from(groups.keys()).filter((group) => !first.includes(group)));
  const sections: Section[] = [];
  if (lead.length > 0) {
    sections.push({ key: "lead", group: "", options: lead });
  }
  for (const group of order) {
    const section = groups.get(group);
    if (section) {
      sections.push(section);
    }
  }
  if (tail.length > 0) {
    sections.push({ key: "tail", group: "", options: tail });
  }
  return sections;
}

function readCollapsed(): Record<string, boolean> {
  try {
    const raw = window.localStorage.getItem(COLLAPSE_STORAGE_KEY);
    const parsed: unknown = raw ? JSON.parse(raw) : null;
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
      const out: Record<string, boolean> = {};
      for (const [key, flag] of Object.entries(parsed as Record<string, unknown>)) {
        if (typeof flag === "boolean") {
          out[key] = flag;
        }
      }
      return out;
    }
  } catch {
    /* storage blocked or bad JSON: start from the defaults */
  }
  return {};
}

function writeCollapsed(state: Record<string, boolean>): void {
  try {
    window.localStorage.setItem(COLLAPSE_STORAGE_KEY, JSON.stringify(state));
  } catch {
    /* storage blocked: the state lasts until the picker closes */
  }
}

export interface ModelPickerPanelProps {
  options: ModelOption[];
  value: string;
  onPick: (value: string) => void;
  /* Shown under the list, such as the composer's free-text model form. */
  footer?: ReactNode;
  className?: string;
  id?: string;
  /* Closes the popup on Escape. The key then stops here, so a modal holding
     the picker stays open. */
  onEscape?: () => void;
}

/* The search box and the grouped list. The composer puts it in its own
   popover; ModelPicker wraps it with a Select-like trigger. */
export function ModelPickerPanel({
  options,
  value,
  onPick,
  footer,
  className,
  id,
  onEscape,
}: ModelPickerPanelProps) {
  const [query, setQuery] = useState("");
  const [active, setActive] = useState<string | null>(null);
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>(readCollapsed);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const scrollToActive = useRef(true);
  const escapeGuardRef = useEscapeGuard();
  const baseId = "mp-" + useId().replace(/[^A-Za-z0-9_-]/g, "");
  const listId = baseId + "-list";

  const sections = useMemo(() => buildSections(options, value), [options, value]);
  const modelCount = options.filter((option) => option.group).length;
  const autoCollapse = modelCount > COLLAPSE_THRESHOLD;
  const selectedGroup = options.find((option) => option.value === value)?.group || "";
  const searching = query.trim() !== "";

  function isCollapsed(group: string): boolean {
    if (!group) {
      return false;
    }
    const stored = collapsed[group];
    if (stored !== undefined) {
      return stored;
    }
    return (
      autoCollapse &&
      group !== selectedGroup &&
      group !== COMBOS_LABEL &&
      group !== CURRENT_GROUP &&
      group !== AGENT_DEFAULT_GROUP
    );
  }

  /* The sections as shown: with a search, only matching options (sticky ones
     always) and every group with a match expanded. */
  const shown = sections
    .map((section) => {
      const matched = searching
        ? section.options.filter((option) => option.sticky || matchesModel(option, query))
        : section.options;
      const expanded = searching || !isCollapsed(section.group);
      return { section, matched, expanded };
    })
    .filter((entry) => entry.matched.length > 0);
  const visible = shown.flatMap((entry) => (entry.expanded ? entry.matched : []));
  const anyMatch = !searching || shown.some((entry) => entry.matched.some((option) => !option.sticky));

  const activeOption =
    (active !== null && visible.find((option) => option.value === active)) ||
    (searching ? visible[0] : visible.find((option) => option.value === value) || visible[0]) ||
    null;
  const idOf = (option: ModelOption) => baseId + "-o-" + options.indexOf(option);
  const activeId = activeOption ? idOf(activeOption) : undefined;

  useEffect(() => {
    inputRef.current?.focus({ preventScroll: true });
    /* Keep the list height while filtering so an anchored popover placed
       above its trigger does not float away from it as the list shrinks. */
    const list = listRef.current;
    if (list && list.clientHeight > 0) {
      list.style.minHeight = list.clientHeight + "px";
    }
  }, []);

  useEffect(() => {
    if (!scrollToActive.current || !activeId) {
      return;
    }
    scrollToActive.current = false;
    const node = document.getElementById(activeId);
    if (node && typeof node.scrollIntoView === "function") {
      node.scrollIntoView({ block: "nearest" });
    }
  }, [activeId]);

  function toggle(group: string) {
    if (searching) {
      return;
    }
    const next = { ...collapsed, [group]: !isCollapsed(group) };
    setCollapsed(next);
    writeCollapsed(next);
  }

  function move(step: number) {
    if (visible.length === 0) {
      return;
    }
    const index = activeOption ? visible.indexOf(activeOption) : -1;
    const nextIndex =
      index < 0 ? (step > 0 ? 0 : visible.length - 1) : Math.max(0, Math.min(visible.length - 1, index + step));
    const next = visible[nextIndex];
    if (next) {
      scrollToActive.current = true;
      setActive(next.value);
    }
  }

  function handleKeyDown(event: ReactKeyboardEvent<HTMLDivElement>) {
    const { key } = event;
    const target = event.target as HTMLElement;
    const inFooter = Boolean(target.closest(".mp-footer"));
    if (key === "Escape" && onEscape) {
      escapeGuardRef.current = true;
      event.preventDefault();
      event.stopPropagation();
      onEscape();
    } else if (key === "ArrowDown" || key === "ArrowUp") {
      if (inFooter) {
        return;
      }
      event.preventDefault();
      event.stopPropagation();
      inputRef.current?.focus({ preventScroll: true });
      move(key === "ArrowDown" ? 1 : -1);
    } else if (key === "Enter" && target === inputRef.current) {
      event.preventDefault();
      if (activeOption) {
        onPick(activeOption.value);
      }
    } else if (
      key.length === 1 &&
      !event.metaKey &&
      !event.ctrlKey &&
      !event.altKey &&
      target !== inputRef.current &&
      !inFooter &&
      key !== " "
    ) {
      /* Typing on a group heading goes to the search box. */
      event.preventDefault();
      setQuery((current) => current + key);
      setActive(null);
      inputRef.current?.focus({ preventScroll: true });
    }
  }

  return (
    <div id={id} className={cn("mp-panel", className)} onKeyDown={handleKeyDown}>
      <div className="pin mp-search">
        <Ic name="search" size={14} />
        <input
          ref={inputRef}
          type="text"
          role="combobox"
          aria-label="Search models"
          aria-expanded="true"
          aria-controls={listId}
          aria-autocomplete="list"
          aria-activedescendant={activeId}
          placeholder="Search models"
          autoComplete="off"
          spellCheck={false}
          value={query}
          onChange={(event) => {
            setQuery(event.target.value);
            setActive(null);
            scrollToActive.current = true;
          }}
        />
        {query ? (
          <button
            type="button"
            className="mp-clear"
            aria-label="Clear search"
            onClick={() => {
              setQuery("");
              setActive(null);
              inputRef.current?.focus({ preventScroll: true });
            }}
          >
            <Ic name="x" size={13} />
          </button>
        ) : null}
      </div>
      {!anyMatch ? (
        <p className="mp-empty" role="status">
          {"No models match “" + query.trim() + "”"}
        </p>
      ) : null}
      <div ref={listRef} id={listId} className="mp-list" role="listbox" aria-label="Models">
        {shown.map(({ section, matched, expanded }) => {
          const rows = expanded
            ? matched.map((option) => {
                const selected = option.value === value;
                const meta = optionMeta(option);
                return (
                  <div
                    key={section.key + "/" + option.value}
                    id={idOf(option)}
                    role="option"
                    aria-selected={selected}
                    aria-label={option.label}
                    data-value={option.value}
                    className={cn("mi", "mp-opt", option === activeOption && "hl", selected && "on")}
                    onMouseMove={() => {
                      if (option !== activeOption) {
                        setActive(option.value);
                      }
                    }}
                    onMouseDown={(event) => event.preventDefault()}
                    onClick={() => onPick(option.value)}
                  >
                    {option.icon ? <span className="select-icon">{option.icon}</span> : null}
                    <span className="mp-text">
                      <span className="mp-name">
                        <span className="trunc">{option.label}</span>
                        {option.tag ? <span className="tag">{option.tag}</span> : null}
                      </span>
                      {meta ? <span className="mp-meta mono trunc">{meta}</span> : null}
                    </span>
                    {option.hint ? <span className="mp-hint">{option.hint}</span> : null}
                    <span className="ck mp-ck">{selected ? <Ic name="check" size={14} /> : null}</span>
                  </div>
                );
              })
            : null;
          if (!section.group) {
            return (
              <div key={section.key} role="presentation" className="mp-group mp-loose">
                {rows}
              </div>
            );
          }
          const count = searching ? matched.length : section.options.length;
          return (
            <div key={section.key} role="group" aria-label={section.group} className="mp-group">
              <button
                type="button"
                className="mp-gh"
                aria-expanded={expanded}
                aria-label={
                  section.group + ", " + count + (count === 1 ? " model" : " models")
                }
                onClick={() => toggle(section.group)}
              >
                <Ic name={expanded ? "chevron-down" : "chevron-right"} size={13} />
                {section.icon ? <span className="select-icon">{section.icon}</span> : null}
                <span className="trunc grow">{section.group}</span>
                <span className="mp-count num">{count}</span>
              </button>
              {rows}
            </div>
          );
        })}
      </div>
      {footer ? <div className="mp-footer">{footer}</div> : null}
    </div>
  );
}

export interface ModelPickerProps
  extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, "onChange" | "value"> {
  options: ModelOption[];
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  size?: "md" | "sm";
}

/* A drop-in for the Gr8r Select on model fields. */
export function ModelPicker({
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
}: ModelPickerProps) {
  const [open, setOpen] = useState(false);
  const btnRef = useRef<HTMLButtonElement>(null);
  const field = useContext(FieldContext);
  const popId = "mp-pop-" + useId().replace(/[^A-Za-z0-9_-]/g, "");
  const selected = options.find((option) => option.value === value);
  const label = selected ? selected.label : placeholder ?? "";
  const group = selected && selected.group && selected.group !== CURRENT_GROUP ? selected.group : "";

  function pick(next: string) {
    setOpen(false);
    if (next !== value) {
      onChange(next);
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
        className={cn("btn", "btn-secondary", "select-btn", "mp-trigger", size === "sm" && "btn-sm", className)}
        role="combobox"
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-controls={open ? popId : undefined}
        aria-describedby={ariaDescribedBy ?? field?.describedBy}
        disabled={disabled}
        onClick={() => setOpen(!open)}
        onKeyDown={(event) => {
          if (!open && (event.key === "ArrowDown" || event.key === "ArrowUp")) {
            event.preventDefault();
            setOpen(true);
          }
        }}
      >
        {selected && selected.icon ? <span className="select-icon">{selected.icon}</span> : null}
        <span className="trunc grow mp-trigger-label">
          {label}
          {group ? <span className="mp-trigger-group">{group}</span> : null}
        </span>
        <Ic name="chevron-down" size={size === "sm" ? 13 : 14} />
      </button>
      <Popover anchorRef={btnRef} open={open && !disabled} onClose={() => setOpen(false)} role="dialog" className="mp-pop">
        <ModelPickerPanel id={popId} options={options} value={value} onPick={pick} onEscape={() => setOpen(false)} />
      </Popover>
    </>
  );
}
