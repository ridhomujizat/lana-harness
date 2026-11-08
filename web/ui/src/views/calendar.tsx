import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  clearOverride,
  completeItem,
  createItem,
  deleteItem,
  dismissFiring,
  getItem,
  isMissingBackend,
  listEntries,
  openCalendarStream,
  runFiring,
  saveOverride,
  updateItem,
} from "../api/calendar";
import type { Entry, GoogleState, Item, ItemDetail, ItemInput, ItemKind } from "../api/calendar";
import { errorMessage } from "../api/client";
import { getWorkflow, listRuns, listWorkflows, startRun } from "../api/workflows";
import type { WorkflowSummary } from "../api/workflows";
import { Ic } from "../ui/icons";
import { Button, ErrorState, IconButton, Menu, MenuItem, MenuSeparator, Modal, Seg, cn, useToast } from "../ui/gr8r";
import { workflowEntries } from "./calendar-fallback";
import {
  AGENDA_DAYS,
  VIEW_MODES,
  addDays,
  dayKey,
  groupByDay,
  isTimed,
  movedStart,
  parseDay,
  periodLabel,
  stepAnchor,
  viewRange,
} from "./calendar-model";
import type { ViewMode } from "./calendar-model";
import { AgendaView, DayView, MonthView, WeekView } from "./calendar-views";
import { EntryDrawer, ItemEditor, KIND_ICONS, KIND_WORDS, MoveDialog, ScopeDialog } from "./calendar-detail";
import { CalendarSettingsDrawer } from "./calendar-settings";
import { useAgentNames, useNow, useWorkflowStream } from "./workflow-common";

/* Calendar page (spec #35 REQ-CAL-010): one calendar for the owner's items,
   agents' items, workflow slots and runs, and Google events. View and date
   live in the URL (#/calendar?view=week&date=2026-11-05). */

type SourceFilter = "mine" | "agents" | "workflows" | "google";
const SOURCE_FILTERS: { value: SourceFilter; label: string; icon: string }[] = [
  { value: "mine", label: "My items", icon: "user" },
  { value: "agents", label: "Agents' items", icon: "bot" },
  { value: "workflows", label: "Workflows", icon: "workflow" },
  { value: "google", label: "Google Calendar", icon: "calendar" },
];
const ITEM_KINDS: ItemKind[] = ["event", "task", "reminder", "note", "habit"];
/* Hidden filter keys: a source ("mine") or an item kind ("kind:task"). */
const FILTER_KEY = "lana.calendar.hidden";

function readHidden(): Set<string> {
  try {
    const raw = window.localStorage.getItem(FILTER_KEY);
    const list = raw ? (JSON.parse(raw) as unknown) : [];
    if (Array.isArray(list)) {
      return new Set(list.filter((value): value is string => typeof value === "string"));
    }
  } catch {
    /* Storage off: everything shows. */
  }
  return new Set();
}

function isHidden(entry: Entry, hidden: Set<string>): boolean {
  if (hidden.has(sourceOf(entry))) {
    return true;
  }
  return entry.source === "item" && hidden.has("kind:" + entry.kind);
}

function sourceOf(entry: Entry): SourceFilter {
  if (entry.source === "workflow_slot" || entry.source === "workflow_run") {
    return "workflows";
  }
  if (entry.source === "google") {
    return "google";
  }
  return entry.created_by.startsWith("agent:") ? "agents" : "mine";
}

const pad = (value: number) => String(value).padStart(2, "0");
const minuteText = (minute: number) => pad(Math.floor(minute / 60)) + ":" + pad(minute % 60);
const shiftDate = (date: string, days: number) => (date ? dayKey(addDays(parseDay(date), days)) : date);
const dayDiff = (from: number, to: number) =>
  Math.round((parseDay(dayKey(new Date(to))).getTime() - parseDay(dayKey(new Date(from))).getTime()) / 86400000);

function refOf(entry: Entry): { source: "item" | "workflow"; ref: string } {
  if (entry.source === "workflow_slot" && entry.workflow) {
    return { source: "workflow", ref: entry.workflow.id + "/" + entry.workflow.node_id };
  }
  return { source: "item", ref: entry.item_id };
}

/* The PATCH that moves a whole item (a non-repeating one, or a series when
   the owner picks "All"), keeping its duration. */
function shiftFields(item: Pick<Item, "kind" | "start" | "end" | "all_day" | "date" | "end_date" | "time">, fromMs: number, toMs: number, minute?: number): ItemInput {
  const deltaMs = toMs - fromMs;
  const deltaDays = dayDiff(fromMs, toMs);
  switch (item.kind) {
    case "event":
      if (item.all_day) {
        return { date: shiftDate(item.date, deltaDays), end_date: shiftDate(item.end_date || item.date, deltaDays) };
      }
      return { start: item.start + deltaMs, end: item.end + deltaMs };
    case "reminder":
      return { start: item.start + deltaMs };
    case "task":
    case "habit":
      return { date: shiftDate(item.date, deltaDays), time: minute !== undefined ? minuteText(minute) : item.time };
    default:
      return { date: shiftDate(item.date, deltaDays) };
  }
}

interface Data {
  entries: Entry[];
  weekStart: number;
  fallback: boolean;
  missed: number;
  google: GoogleState;
}

interface EditorState {
  item: Item | null;
  kind: ItemKind;
  day: Date;
  minute?: number;
}

export function CalendarPage({ params }: { params: Record<string, string> }) {
  const toast = useToast();
  const now = useNow(60000);
  const today = useMemo(() => parseDay(dayKey(new Date(now))), [now]);
  const view: ViewMode = VIEW_MODES.includes(params.view as ViewMode) ? (params.view as ViewMode) : "week";
  const anchor = useMemo(() => parseDay(params.date || dayKey(new Date())), [params.date]);

  const [data, setData] = useState<Data | null>(null);
  const [error, setError] = useState("");
  const [workflows, setWorkflows] = useState<WorkflowSummary[]>([]);
  const [hidden, setHidden] = useState<Set<string>>(readHidden);
  const [filterOpen, setFilterOpen] = useState(false);
  const filterRef = useRef<HTMLSpanElement>(null);
  const [selected, setSelected] = useState<Entry | null>(null);
  const [detail, setDetail] = useState<ItemDetail | null>(null);
  const [editor, setEditor] = useState<EditorState | null>(null);
  const [moving, setMoving] = useState<Entry | null>(null);
  const [scope, setScope] = useState<{ entry: Entry; day: Date; minute?: number } | null>(null);
  const [deleting, setDeleting] = useState<Entry | null>(null);
  const [settingsOpen, setSettingsOpen] = useState(false);
  /* The day picked in the phone month view (its list shows under the grid). */
  const [pickedKey, setPickedKey] = useState("");
  const [newOpen, setNewOpen] = useState(false);
  const newRef = useRef<HTMLSpanElement>(null);
  const agents = useAgentNames().map((agent) => agent.name);

  const weekStart = data?.weekStart ?? 1;
  const picked = useMemo(() => {
    const chosen = pickedKey ? parseDay(pickedKey) : null;
    if (chosen && chosen.getMonth() === anchor.getMonth() && chosen.getFullYear() === anchor.getFullYear()) {
      return chosen;
    }
    return anchor.getMonth() === today.getMonth() && anchor.getFullYear() === today.getFullYear() ? today : anchor;
  }, [pickedKey, anchor, today]);
  const setPicked = (day: Date) => setPickedKey(dayKey(day));
  const range = useMemo(() => viewRange(view, anchor, weekStart), [view, anchor, weekStart]);
  const fromMs = range.from.getTime();
  const toMs = range.to.getTime();
  const fallbackRef = useRef(false);
  /* The range currently on screen, and the sequence number of the newest load.
     A response is used only when it is the latest request and its range is
     still the one shown: changing "Week starts on" moves the range while an
     older request (the settings reload, a debounced stream reload) is still in
     flight, and that stale response must not overwrite the new range. */
  const rangeRef = useRef({ fromMs, toMs });
  const loadSeq = useRef(0);
  useEffect(() => {
    rangeRef.current = { fromMs, toMs };
  }, [fromMs, toMs]);

  const load = useCallback(async () => {
    const seq = ++loadSeq.current;
    const asked = { fromMs, toMs };
    const stale = () =>
      seq !== loadSeq.current || rangeRef.current.fromMs !== asked.fromMs || rangeRef.current.toMs !== asked.toMs;
    const loadFallback = async (): Promise<Data> => {
      const list = await listWorkflows();
      setWorkflows(list.workflows);
      const [details, runs] = await Promise.all([
        Promise.all(list.workflows.map((row) => getWorkflow(row.id))),
        Promise.all(list.workflows.map((row) => listRuns(row.id, { limit: 50 }))),
      ]);
      const allRuns = runs.flatMap((response) => response.runs);
      return {
        entries: workflowEntries(details, allRuns, fromMs, toMs, Date.now()),
        weekStart: 1,
        fallback: true,
        missed: 0,
        google: "off",
      };
    };
    try {
      let next: Data;
      if (fallbackRef.current) {
        next = await loadFallback();
      } else {
        try {
          const response = await listEntries(fromMs, toMs);
          next = {
            entries: response.entries,
            weekStart: response.week_start === 0 ? 0 : 1,
            fallback: false,
            missed: response.missed,
            google: response.google?.state || "off",
          };
        } catch (failure) {
          if (!isMissingBackend(failure)) {
            throw failure;
          }
          fallbackRef.current = true;
          next = await loadFallback();
        }
      }
      if (stale()) {
        return;
      }
      setData(next);
      setError("");
    } catch (failure) {
      if (stale()) {
        return;
      }
      setError(errorMessage(failure));
    }
  }, [fromMs, toMs]);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- the first update happens after the await
    void load();
  }, [load]);

  useEffect(() => {
    listWorkflows()
      .then((list) => setWorkflows(list.workflows))
      .catch(() => {
        /* The workflow picker stays empty. */
      });
  }, []);

  /* Live updates (REQ-CAL-014): both streams reload the visible range,
     debounced so a burst of changes costs one read. */
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const reloadSoon = useCallback(() => {
    if (timer.current) {
      clearTimeout(timer.current);
    }
    timer.current = setTimeout(() => void load(), 300);
  }, [load]);
  useEffect(() => () => {
    if (timer.current) {
      clearTimeout(timer.current);
    }
  }, []);
  useWorkflowStream(() => reloadSoon());
  const fallback = Boolean(data?.fallback);
  useEffect(() => {
    if (fallback) {
      return undefined;
    }
    const stream = openCalendarStream(() => reloadSoon());
    return () => stream.close();
  }, [fallback, reloadSoon]);

  /* The drawer follows the latest copy of its entry, and closes when the
     entry is gone after a reload. */
  useEffect(() => {
    if (!selected || !data) {
      return;
    }
    const fresh = data.entries.find((entry) => entry.key === selected.key);
    if (fresh !== selected) {
      // eslint-disable-next-line react-hooks/set-state-in-effect -- sync with reloaded data
      setSelected(fresh || null);
    }
  }, [data, selected]);

  /* Marks the open entry in the grid (gr8r keeps the opened task lit). */
  useEffect(() => {
    const key = selected?.key || "";
    for (const el of document.querySelectorAll<HTMLElement>(".cal-body [data-key]")) {
      el.classList.toggle("sel", el.dataset.key === key);
    }
  });

  /* Item details (notes, repeat, runs, activity) for the open drawer; read
     again whenever the visible data reloads. */
  const selectedItem = selected && selected.source === "item" && !fallback ? selected.item_id : "";
  useEffect(() => {
    if (!selectedItem) {
      return undefined;
    }
    let live = true;
    getItem(selectedItem)
      .then((found) => {
        if (live) {
          setDetail(found);
        }
      })
      .catch(() => {
        /* The drawer shows what the entry carries. */
      });
    return () => {
      live = false;
    };
  }, [selectedItem, data]);

  function go(nextView: ViewMode, date: Date) {
    window.location.hash = "#/calendar?view=" + nextView + "&date=" + dayKey(date);
  }

  function saveHidden(next: Set<string>) {
    setHidden(next);
    try {
      window.localStorage.setItem(FILTER_KEY, JSON.stringify([...next]));
    } catch {
      /* Kept for this page only. */
    }
  }

  function toggleHidden(key: string) {
    const next = new Set(hidden);
    if (next.has(key)) {
      next.delete(key);
    } else {
      next.add(key);
    }
    saveHidden(next);
  }

  const shown = useMemo(
    () => (data ? data.entries.filter((entry) => !isHidden(entry, hidden)) : []),
    [data, hidden]
  );
  const byDay = useMemo(() => groupByDay(shown, range.days), [shown, range]);
  const habitsToday = useMemo(
    () => (data ? data.entries.filter((entry) => entry.kind === "habit" && entry.date === dayKey(today)) : []),
    [data, today]
  );
  const missedShown = useMemo(() => shown.filter((entry) => entry.firing?.status === "missed"), [shown]);
  const workflowName = (id: string) => workflows.find((workflow) => workflow.id === id)?.name || id;

  async function run(label: string, action: () => Promise<unknown>, done?: string) {
    try {
      await action();
      if (done) {
        toast.show({ title: done, tone: "ok" });
      }
      await load();
    } catch (failure) {
      toast.show({ title: label, body: errorMessage(failure), tone: "error" });
    }
  }

  function openEntry(entry: Entry) {
    setSelected(entry);
  }

  /* Moves an entry to a day (keeping its time) or to a minute of a day. */
  async function moveEntry(entry: Entry, day: Date, minute: number | undefined, scopeAll: boolean | null) {
    const to = movedStart(entry, day, isTimed(entry) || entry.source === "workflow_slot" ? minute : undefined);
    const from = entry.source === "item" && entry.repeating ? entry.occurrence : entry.start;
    if (entry.source === "workflow_slot" || (entry.source === "item" && entry.repeating && !scopeAll)) {
      const ref = refOf(entry);
      await saveOverride({ ...ref, occurrence: entry.occurrence, op: "move", to });
      return;
    }
    if (entry.source !== "item") {
      return;
    }
    if (entry.repeating) {
      const { item } = await getItem(entry.item_id);
      const baseFrom = entry.occurrence || entry.start;
      const fields = shiftFields(item, baseFrom, to, minute);
      await updateItem(item.id, fields);
      return;
    }
    const time = isTimed(entry) ? minuteText(new Date(entry.start).getHours() * 60 + new Date(entry.start).getMinutes()) : "";
    const fields = shiftFields(
      { kind: entry.kind as ItemKind, start: entry.start, end: entry.end, all_day: entry.all_day, date: entry.date, end_date: entry.end_date, time },
      from,
      to,
      minute
    );
    await updateItem(entry.item_id, fields);
  }

  function onDrop(key: string, day: Date, minute?: number) {
    const entry = data?.entries.find((candidate) => candidate.key === key);
    if (!entry || fallback || entry.readonly) {
      return;
    }
    const target = movedStart(entry, day, isTimed(entry) || entry.source === "workflow_slot" ? minute : undefined);
    if (target === entry.start) {
      return;
    }
    if (entry.source === "item" && entry.repeating) {
      setScope({ entry, day, minute });
      return;
    }
    void run("Could not move it", () => moveEntry(entry, day, minute, null));
  }

  function onAdd(day: Date, minute?: number) {
    if (fallback) {
      toast.show({ title: "Calendar items need the calendar service", body: "This daemon only shows workflow schedules.", tone: "info" });
      return;
    }
    setEditor({ item: null, kind: minute !== undefined ? "event" : "task", day, minute });
  }

  async function saveItem(input: ItemInput) {
    if (editor?.item) {
      await updateItem(editor.item.id, input);
      toast.show({ title: "Saved", tone: "ok" });
    } else {
      await createItem(input);
      toast.show({ title: KIND_WORDS[(input.kind || "event") as ItemKind] + " added", tone: "ok" });
    }
    await load();
  }

  const closeDrawer = () => setSelected(null);

  if (error && !data) {
    return (
      <main id="main" className="page flush cal-page">
        <ErrorState title="Could not load the calendar" text={error} action={<Button onClick={() => void load()}>Try again</Button>} />
      </main>
    );
  }

  const label = periodLabel(view, anchor, range);

  return (
    <main id="main" className={cn("page flush cal-page", selected && "has-drawer")}>
      <header className="ph">
        <div>
          <h1>Calendar</h1>
          <p>Your plans, your agents&apos; plans and every workflow run in one place.</p>
        </div>
        <div className="acts">
          <IconButton icon="settings" size="sm" label="Calendar settings" tip="Settings" disabled={fallback} onClick={() => setSettingsOpen(true)} />
          <span ref={newRef} className="cal-new">
          <Button
            variant="primary"
            size="sm"
            icon="plus"
            iconRight="chevron-down"
            aria-haspopup="menu"
            aria-expanded={newOpen}
            disabled={fallback}
            onClick={() => setNewOpen((open) => !open)}
          >
            New
          </Button>
          </span>
          <Menu anchorRef={newRef} open={newOpen} onClose={() => setNewOpen(false)} width={200}>
            {(Object.keys(KIND_WORDS) as ItemKind[]).map((kind) => (
              <MenuItem
                key={kind}
                icon={KIND_ICONS[kind]}
                onClick={() => {
                  setNewOpen(false);
                  setEditor({ item: null, kind, day: view === "month" || view === "agenda" ? today : anchor });
                }}
              >
                {KIND_WORDS[kind]}
              </MenuItem>
            ))}
          </Menu>
        </div>
      </header>

      {fallback ? (
        <div className="cal-banner info" role="status">
          <Ic name="info" size={15} />
          <span className="grow">
            The calendar service is not running in this daemon yet. Showing workflow schedules and runs only.
          </span>
        </div>
      ) : null}
      {data && data.missed > 0 ? (
        <div className="cal-banner warn" role="status">
          <Ic name="triangle-alert" size={15} />
          <span className="grow">
            {data.missed === 1 ? "1 action was" : data.missed + " actions were"} missed while lana was off. They did not run.
          </span>
          <Button size="sm" onClick={() => go("agenda", addDays(today, -7))}>
            Review
          </Button>
        </div>
      ) : null}
      {data && data.google === "needs_reauth" ? (
        <div className="cal-banner warn" role="status">
          <Ic name="calendar" size={15} />
          <span className="grow">Google Calendar needs you to sign in again. Your lana items are safe.</span>
          <Button size="sm" onClick={() => setSettingsOpen(true)}>
            Open settings
          </Button>
        </div>
      ) : null}

      {habitsToday.length > 0 && view !== "month" ? (
        <div className="cal-habits" aria-label="Today's habits">
          <span className="cal-habits-k">Today&apos;s habits</span>
          {habitsToday.map((entry) => (
            <button
              key={entry.key}
              type="button"
              className={cn("cal-habit", entry.habit?.done && "on")}
              aria-pressed={Boolean(entry.habit?.done)}
              onClick={() =>
                void run("Could not tick it", () => completeItem(entry.item_id, entry.occurrence, !entry.habit?.done))
              }
            >
              <Ic name={entry.habit?.done ? "circle-check" : "circle"} size={14} />
              <span className="trunc">{entry.title}</span>
              {entry.habit && entry.habit.streak > 0 ? <span className="num cal-habit-s">🔥 {entry.habit.streak}</span> : null}
            </button>
          ))}
        </div>
      ) : null}

      <div className="cal-toolbar">
        <Button size="sm" onClick={() => go(view, today)}>
          Today
        </Button>
        <div className="row" style={{ gap: 0 }}>
          <IconButton icon="chevron-left" size="sm" label="Previous" onClick={() => go(view, stepAnchor(view, anchor, -1))} />
          <IconButton icon="chevron-right" size="sm" label="Next" onClick={() => go(view, stepAnchor(view, anchor, 1))} />
        </div>
        <h2 className="cal-label" aria-live="polite">
          {label}
        </h2>
        <span className="sp" />
        <span ref={filterRef} className="cal-new">
          <Button
            size="sm"
            variant={hidden.size > 0 ? "secondary" : "ghost"}
            icon="list-filter"
            aria-haspopup="menu"
            aria-expanded={filterOpen}
            className={cn("cal-filter-btn", hidden.size > 0 && "on")}
            onClick={() => setFilterOpen((open) => !open)}
          >
            Filter
            {hidden.size > 0 ? <span className="cal-filter-n num">{hidden.size} hidden</span> : null}
          </Button>
        </span>
        <Menu anchorRef={filterRef} open={filterOpen} onClose={() => setFilterOpen(false)} width={220}>
          <div className="mh">Show</div>
          {SOURCE_FILTERS.map((filter) => (
            <MenuItem
              key={filter.value}
              role="menuitemcheckbox"
              aria-checked={!hidden.has(filter.value)}
              checked={!hidden.has(filter.value)}
              icon={filter.icon}
              onClick={() => toggleHidden(filter.value)}
            >
              {filter.label}
            </MenuItem>
          ))}
          <div className="mh">Item types</div>
          {ITEM_KINDS.map((kind) => (
            <MenuItem
              key={kind}
              role="menuitemcheckbox"
              aria-checked={!hidden.has("kind:" + kind)}
              checked={!hidden.has("kind:" + kind)}
              icon={KIND_ICONS[kind]}
              onClick={() => toggleHidden("kind:" + kind)}
            >
              {KIND_WORDS[kind]}
            </MenuItem>
          ))}
          {hidden.size > 0 ? (
            <>
              <MenuSeparator />
              <MenuItem icon="rotate-ccw" onClick={() => saveHidden(new Set())}>
                Show everything
              </MenuItem>
            </>
          ) : null}
        </Menu>
        <Seg
          value={view}
          onChange={(value) => go(value as ViewMode, anchor)}
          options={[
            { value: "month", label: "Month" },
            { value: "week", label: "Week" },
            { value: "day", label: "Day" },
            { value: "agenda", label: "Agenda" },
          ]}
        />
      </div>

      <div className={cn("cal-body", "cal-" + view, !data && "loading")} aria-busy={!data}>
        {view === "month" ? (
          <MonthView
            range={range}
            anchor={anchor}
            byDay={byDay}
            today={today}
            weekStart={weekStart}
            onOpen={openEntry}
            fallback={fallback}
            onDrop={onDrop}
            onAdd={onAdd}
            onMore={(day) => go("day", day)}
            picked={picked}
            onPick={setPicked}
          />
        ) : null}
        {view === "week" ? (
          <WeekView range={range} byDay={byDay} today={today} onOpen={openEntry} fallback={fallback} onDrop={onDrop} onAdd={onAdd} />
        ) : null}
        {view === "day" ? (
          <DayView
            day={anchor}
            entries={byDay.get(dayKey(anchor)) || []}
            now={now}
            onOpen={openEntry}
            fallback={fallback}
            onDrop={onDrop}
            onAdd={onAdd}
          />
        ) : null}
        {view === "agenda" ? (
          <AgendaView range={range} byDay={byDay} today={today} onOpen={openEntry} fallback={fallback} missedFirst={missedShown} />
        ) : null}
      </div>
      {view === "agenda" ? <p className="muted cal-foot">Showing {AGENDA_DAYS} days.</p> : null}

      {selected ? (
        <EntryDrawer
          key={selected.key}
          entry={selected}
          detail={detail && detail.item.id === selected.item_id ? detail : null}
          onClose={closeDrawer}
          fallback={fallback}
          workflowName={workflowName}
          agents={agents}
          workflows={workflows.map((workflow) => ({ id: workflow.id, name: workflow.name }))}
          actions={{
            onComplete: (done) => {
              void run("Could not save", () => completeItem(selected.item_id, selected.occurrence, done));
            },
            onMove: () => setMoving(selected),
            onSkip: () => {
              void run("Could not skip it", () => saveOverride({ ...refOf(selected), occurrence: selected.occurrence, op: "skip" }), "Skipped this one");
            },
            onReset: () => {
              const ref = refOf(selected);
              void run("Could not reset it", () => clearOverride(ref.source, ref.ref, selected.occurrence), "Back on schedule");
            },
            onRun: () => {
              void run("Could not run it", () => runFiring(selected.item_id, selected.occurrence), "Running now");
            },
            onDismiss: () => {
              void run("Could not dismiss it", () => dismissFiring(selected.item_id, selected.occurrence));
            },
            onDelete: () => setDeleting(selected),
            onRunWorkflow: () => {
              const id = selected.workflow?.id || "";
              void startRun(id)
                .then((started) => {
                  window.location.hash = "#/workflows/" + encodeURIComponent(id) + "/runs/" + started.number;
                })
                .catch((failure) => toast.show({ title: "Could not start a run", body: errorMessage(failure), tone: "error" }));
            },
            onSave: async (fields) => {
              await updateItem(selected.item_id, fields);
              toast.show({ title: "Saved", tone: "ok" });
              await load();
            },
          }}
        />
      ) : null}

      {editor ? (
        <ItemEditor
          key={editor.item?.id || "new-" + editor.kind}
          item={editor.item}
          kind={editor.kind}
          day={editor.day}
          minute={editor.minute}
          agents={agents}
          workflows={workflows.map((workflow) => ({ id: workflow.id, name: workflow.name }))}
          onClose={() => setEditor(null)}
          onSave={saveItem}
        />
      ) : null}

      {moving ? (
        <MoveDialog
          entry={moving}
          onClose={() => setMoving(null)}
          onMove={async (day, minute) => {
            await moveEntry(moving, day, minute, false);
            toast.show({ title: "Moved", tone: "ok" });
            await load();
          }}
        />
      ) : null}

      {scope ? (
        <ScopeDialog
          entry={scope.entry}
          onClose={() => setScope(null)}
          onThis={() => {
            const pending = scope;
            setScope(null);
            void run("Could not move it", () => moveEntry(pending.entry, pending.day, pending.minute, false), "Moved this one");
          }}
          onAll={() => {
            const pending = scope;
            setScope(null);
            void run("Could not move it", () => moveEntry(pending.entry, pending.day, pending.minute, true), "Moved the series");
          }}
        />
      ) : null}

      {deleting ? (
        <Modal
          title={"Delete “" + deleting.title + "”?"}
          subtitle={deleting.repeating ? "Every occurrence goes, including its history." : deleting.google ? "It is also deleted in Google Calendar." : undefined}
          size="sm"
          onClose={() => setDeleting(null)}
          footer={
            <>
              <Button variant="ghost" onClick={() => setDeleting(null)}>
                Cancel
              </Button>
              <Button
                variant="danger"
                data-autofocus
                onClick={() => {
                  const target = deleting;
                  setDeleting(null);
                  void run("Could not delete it", () => deleteItem(target.item_id), "Deleted");
                }}
              >
                Delete
              </Button>
            </>
          }
        >
          <p className="muted" style={{ margin: 0 }}>
            This cannot be undone.
          </p>
        </Modal>
      ) : null}

      {settingsOpen ? <CalendarSettingsDrawer onClose={() => setSettingsOpen(false)} onChanged={() => void load()} /> : null}
    </main>
  );
}
