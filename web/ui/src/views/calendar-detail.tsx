import { useMemo, useRef, useState } from "react";
import type { CSSProperties, ReactNode } from "react";
import { errorMessage } from "../api/client";
import type {
  Action,
  Entry,
  Item,
  ItemColor,
  ItemDetail,
  ItemInput,
  ItemKind,
  RepeatFreq,
  Weekday,
} from "../api/calendar";
import { ApiError } from "../api/client";
import { Ic } from "../ui/icons";
import {
  Badge,
  Button,
  Checkbox,
  Drawer,
  Field,
  IconButton,
  Input,
  Menu,
  MenuItem,
  MenuSeparator,
  Modal,
  Seg,
  Select,
  Textarea,
  Toggle,
  cn,
} from "../ui/gr8r";
import type { BadgeTone } from "../ui/gr8r";
import { AgentFace, ago } from "./workflow-common";
import { dayKey, dayTitle, instantOf, isTimed, parseDay, timeOf } from "./calendar-model";
import { entryColor, entryIcon, entryWhen } from "./calendar-views";

/* The entry popover, the item editor and the small move/scope dialogs of the
   Calendar page (spec #35 REQ-CAL-001, -003, -004, -005, -006, -010). */

export const KIND_WORDS: Record<ItemKind, string> = {
  event: "Event",
  task: "Task",
  reminder: "Reminder",
  note: "Note",
  habit: "Habit",
};

export const KIND_ICONS: Record<ItemKind, string> = {
  event: "calendar",
  task: "square-check",
  reminder: "bell",
  note: "sticky-note",
  habit: "repeat",
};

const FIRING_TONES: Record<string, BadgeTone> = {
  scheduled: "gray",
  running: "accent",
  done: "green",
  failed: "red",
  missed: "red",
  dismissed: "gray",
};

export function byText(by: string): string {
  if (!by || by === "owner") {
    return "you";
  }
  if (by === "google") {
    return "Google Calendar";
  }
  return by.startsWith("agent:") ? by.slice(6) : by;
}

export function actionText(action: Action | null, workflowName?: (id: string) => string): string {
  if (!action) {
    return "";
  }
  switch (action.type) {
    case "agent_prompt":
      return action.agent + " runs: " + action.prompt;
    case "run_workflow":
      return "Run workflow " + (workflowName ? workflowName(action.workflow_id) : action.workflow_id);
    default:
      return "Send to " + (action.target === "telegram" ? "Telegram" : "web") + ": " + action.text;
  }
}

function whenLong(entry: Entry): string {
  const day = entry.date ? parseDay(entry.date) : new Date(entry.start);
  let text = dayTitle(day);
  if (entry.end_date && entry.end_date !== entry.date) {
    text += " – " + dayTitle(parseDay(entry.end_date));
  }
  return text + " · " + entryWhen(entry);
}

export interface EntryActions {
  onComplete: (done: boolean) => void;
  onMove: () => void;
  onSkip: () => void;
  onReset: () => void;
  onRun: () => void;
  onDismiss: () => void;
  onDelete: () => void;
  onRunWorkflow: () => void;
  onSave: (fields: ItemInput) => Promise<void>;
}

const REPEAT_WORDS: Record<RepeatFreq, string> = {
  none: "Does not repeat",
  daily: "Every day",
  weekdays: "Every weekday",
  weekly: "Weekly",
  monthly: "Monthly",
  yearly: "Every year",
};

function repeatText(item: Item | null, entry: Entry): string {
  if (!item) {
    return entry.repeating ? "Repeats" : "Does not repeat";
  }
  let text = REPEAT_WORDS[item.repeat.freq] || "Repeats";
  if (item.repeat.freq === "weekly" && item.repeat.days.length > 0) {
    text += " on " + item.repeat.days.map((day) => day[0]!.toUpperCase() + day.slice(1)).join(", ");
  }
  if (item.repeat.until) {
    text += " until " + dayTitle(parseDay(item.repeat.until));
  }
  return text;
}

function sourceWord(entry: Entry): string {
  switch (entry.source) {
    case "workflow_slot":
      return "Workflow slot";
    case "workflow_run":
      return "Run #" + (entry.workflow?.run_number || "");
    case "google":
      return entry.google?.calendar_name || "Google Calendar";
    default:
      return KIND_WORDS[entry.kind as ItemKind] || entry.kind;
  }
}

function clockLong(ms: number): string {
  const date = new Date(ms);
  return dayTitle(date) + " " + String(date.getHours()).padStart(2, "0") + ":" + String(date.getMinutes()).padStart(2, "0");
}

/* The side panel an entry opens in, like gr8r-studio's task drawer: a header
   of actions, the title, a property list and sections below. */
export function EntryDrawer({
  entry,
  detail,
  onClose,
  actions,
  fallback,
  workflowName,
  agents,
  workflows,
}: {
  entry: Entry;
  detail: ItemDetail | null;
  onClose: () => void;
  actions: EntryActions;
  fallback: boolean;
  workflowName: (id: string) => string;
  agents: string[];
  workflows: { id: string; name: string }[];
}) {
  const [full, setFull] = useState(false);
  const [moreOpen, setMoreOpen] = useState(false);
  const moreRef = useRef<HTMLSpanElement>(null);
  const item = entry.source === "item";
  const slot = entry.source === "workflow_slot";
  const run = entry.source === "workflow_run";
  const firing = entry.firing;
  const canEdit = !fallback && !entry.readonly && item;
  const canComplete = !fallback && item && (entry.kind === "task" || entry.kind === "habit");
  const done = entry.status === "done" || Boolean(entry.habit?.done);
  const canOverride = !fallback && !entry.readonly && ((item && entry.repeating) || slot);
  const overridden = entry.moved || entry.status === "skipped";
  const rerun = !fallback && item && firing && ["missed", "failed", "dismissed"].includes(firing.status);
  const by = byText(entry.created_by);
  const assignee = entry.assignee && entry.assignee !== "owner" ? entry.assignee : "";
  const full_item = detail?.item || null;
  const editable = Boolean(canEdit && full_item);
  const openWorkflow = () => {
    const id = encodeURIComponent(entry.workflow?.id || "");
    window.location.hash = run ? "#/workflows/" + id + "/runs/" + entry.workflow?.run_number : "#/workflows/" + id;
  };
  const moreItems: ReactNode[] = [];
  if ((canEdit || canOverride) && entry.status !== "skipped") {
    moreItems.push(
      <MenuItem key="move" icon="move" onClick={() => { setMoreOpen(false); actions.onMove(); }}>
        Move…
      </MenuItem>
    );
  }
  if (canOverride && !overridden) {
    moreItems.push(
      <MenuItem key="skip" icon="skip-forward" onClick={() => { setMoreOpen(false); actions.onSkip(); }}>
        Skip this one
      </MenuItem>
    );
  }
  if (canOverride && overridden) {
    moreItems.push(
      <MenuItem key="reset" icon="undo-2" onClick={() => { setMoreOpen(false); actions.onReset(); }}>
        Reset to {slot ? "schedule" : "series"}
      </MenuItem>
    );
  }
  if (slot || run) {
    moreItems.push(
      <MenuItem key="open" icon="external-link" onClick={openWorkflow}>
        {run ? "Open run" : "Open workflow"}
      </MenuItem>
    );
  }
  if (canEdit) {
    moreItems.push(<MenuSeparator key="sep" />);
    moreItems.push(
      <MenuItem key="delete" icon="trash-2" danger onClick={() => { setMoreOpen(false); actions.onDelete(); }}>
        Delete{entry.repeating ? " series" : ""}
      </MenuItem>
    );
  }

  let primary: ReactNode = null;
  if (canComplete) {
    primary = (
      <Button
        size="sm"
        variant={done ? "secondary" : "ghost"}
        icon={done ? "circle-check" : "circle"}
        className={done ? "cdr-done" : undefined}
        onClick={() => actions.onComplete(!done)}
      >
        {entry.kind === "habit" ? (done ? "Done today" : "Tick today") : done ? "Completed" : "Mark complete"}
      </Button>
    );
  } else if (rerun) {
    primary = (
      <Button size="sm" variant="primary" icon="play" onClick={actions.onRun}>
        Run now
      </Button>
    );
  } else if (slot && !fallback) {
    primary = (
      <Button size="sm" variant="ghost" icon="play" onClick={actions.onRunWorkflow}>
        <span className="hide-m">Run workflow now</span>
      </Button>
    );
  }

  const prop = (icon: string, label: string, value: ReactNode) => (
    <>
      <dt>
        <Ic name={icon} size={14} />
        {label}
      </dt>
      <dd>{value}</dd>
    </>
  );

  return (
    <Drawer
      title={sourceWord(entry) + ": " + entry.title}
      full={full}
      onClose={onClose}
      className="cdr"
      header={
        <>
          <span className="pillbtn cdr-kind" style={{ "--c": entryColor(entry) } as CSSProperties}>
            <span className="pdot" />
            <Ic name={entryIcon(entry)} size={13} />
            {sourceWord(entry)}
          </span>
          <span className="sp" />
          {primary}
          <IconButton
            icon={full ? "minimize-2" : "maximize-2"}
            size="sm"
            className="hide-m"
            tip={full ? "Side panel" : "Full page"}
            label="Toggle full page"
            onClick={() => setFull((value) => !value)}
          />
          {moreItems.length > 0 ? (
            <span ref={moreRef} className="cal-new">
              <IconButton icon="ellipsis" size="sm" label="More" aria-haspopup="menu" aria-expanded={moreOpen} onClick={() => setMoreOpen((open) => !open)} />
            </span>
          ) : null}
          <Menu anchorRef={moreRef} open={moreOpen} onClose={() => setMoreOpen(false)} width={210}>
            {moreItems}
          </Menu>
        </>
      }
    >
      {firing && firing.status === "missed" ? (
        <div className="alert danger cdr-alert">
          <Ic name="triangle-alert" size={15} />
          <span>
            Missed: lana was off at {clockLong(firing.at)}, so it did not run.{" "}
            <button type="button" className="link" onClick={actions.onRun}>
              Run now
            </button>{" "}
            ·{" "}
            <button type="button" className="link" onClick={actions.onDismiss}>
              Dismiss
            </button>
          </span>
        </div>
      ) : null}
      {firing && firing.status === "failed" ? (
        <div className="alert danger cdr-alert">
          <Ic name="circle-x" size={15} />
          <span>
            Failed{firing.error ? ": " + firing.error : ""}.{" "}
            <button type="button" className="link" onClick={actions.onRun}>
              Try again
            </button>
          </span>
        </div>
      ) : null}
      {entry.kind === "task" && done ? (
        <div className="alert ok cdr-alert">
          <Ic name="circle-check" size={15} />
          <span>
            Completed.{" "}
            <button type="button" className="link" onClick={() => actions.onComplete(false)}>
              Reopen task
            </button>
          </span>
        </div>
      ) : null}
      {entry.moved ? (
        <div className="alert info cdr-alert">
          <Ic name="move" size={15} />
          <span>
            Moved from {clockLong(entry.occurrence)}. {slot ? "The workflow's schedule is unchanged." : "The rest of the series is unchanged."}
          </span>
        </div>
      ) : null}
      {entry.status === "skipped" ? (
        <div className="alert info cdr-alert">
          <Ic name="skip-forward" size={15} />
          <span>
            Skipped this time.{" "}
            <button type="button" className="link" onClick={actions.onReset}>
              Bring it back
            </button>
          </span>
        </div>
      ) : null}

      {editable && full_item ? (
        <ItemDrawerForm
          key={full_item.id + ":" + full_item.updated_at}
          item={full_item}
          entry={entry}
          agents={agents}
          workflows={workflows}
          onSave={actions.onSave}
          extra={
            <>
              {prop(
                "user-plus",
                "Added by",
                <span className="row" style={{ gap: 6 }}>
                  {entry.created_by.startsWith("agent:") ? <AgentFace name={by} /> : null}
                  {by === "you" ? "You" : by}
                </span>
              )}
              {firing
                ? prop(
                    "activity",
                    "Last firing",
                    <Badge tone={FIRING_TONES[firing.status] || "gray"} dot>
                      {firing.status === "missed" ? "Missed" : firing.status}
                    </Badge>
                  )
                : null}
              {entry.habit ? prop("flame", "Streak", <span className="num">🔥 {entry.habit.streak} day{entry.habit.streak === 1 ? "" : "s"}</span>) : null}
              {entry.google
                ? prop(
                    "refresh-cw",
                    "Google",
                    <span>
                      {entry.google.calendar_name || "Google Calendar"} ·{" "}
                      {entry.google.sync === "synced" ? "Synced" : entry.google.sync === "pending" ? "Syncing" : "Not synced"}
                    </span>
                  )
                : null}
            </>
          }
        />
      ) : (
        <>
          <h3 className={cn("ttl-edit cdr-ttl", done && "done")}>{entry.title}</h3>
      <dl className="kv cdr-kv">
        {prop("calendar", "When", <span className="num">{whenLong(entry)}</span>)}
        {item ? prop("repeat", "Repeat", <span>{repeatText(full_item, entry)}</span>) : null}
        {item
          ? prop(
              "user",
              "For",
              assignee ? (
                <span className="row" style={{ gap: 6 }}>
                  <AgentFace name={assignee} />
                  {assignee}
                </span>
              ) : (
                <span>Me</span>
              )
            )
          : null}
        {item || entry.source === "google"
          ? prop(
              "user-plus",
              "Added by",
              <span className="row" style={{ gap: 6 }}>
                {entry.created_by.startsWith("agent:") ? <AgentFace name={by} /> : null}
                {by === "you" ? "You" : by}
              </span>
            )
          : null}
        {item && entry.kind !== "note"
          ? prop(
              "zap",
              "When due",
              entry.action ? <span>{actionText(entry.action, workflowName)}</span> : <span className="faint">Just shows it</span>
            )
          : null}
        {firing
          ? prop(
              "activity",
              "Last firing",
              <Badge tone={FIRING_TONES[firing.status] || "gray"} dot>
                {firing.status === "missed" ? "Missed" : firing.status}
              </Badge>
            )
          : null}
        {entry.kind === "task" ? prop("circle-dot", "Status", <Badge tone={done ? "green" : "gray"}>{done ? "Done" : "Open"}</Badge>) : null}
        {entry.habit ? prop("flame", "Streak", <span className="num">🔥 {entry.habit.streak} day{entry.habit.streak === 1 ? "" : "s"}</span>) : null}
        {entry.workflow
          ? prop(
              "workflow",
              "Workflow",
              <button type="button" className="pillbtn" onClick={openWorkflow}>
                {entry.workflow.name}
                <Ic name="arrow-up-right" size={12} />
              </button>
            )
          : null}
        {entry.workflow?.guardian ? prop("shield", "Guardian", <span>{entry.workflow.guardian}</span>) : null}
        {run
          ? prop(
              "circle-dot",
              "Run",
              <Badge tone={entry.status === "failed" ? "red" : entry.status === "succeeded" ? "green" : "amber"} dot>
                #{entry.workflow?.run_number} · {entry.status}
              </Badge>
            )
          : null}
        {entry.workflow?.every_minutes ? prop("timer", "Interval", <span>Every {entry.workflow.every_minutes} min</span>) : null}
        {entry.google
          ? prop(
              "refresh-cw",
              "Google",
              <span>
                {entry.google.calendar_name || "Google Calendar"} ·{" "}
                {entry.google.sync === "synced" ? "Synced" : entry.google.sync === "pending" ? "Syncing" : "Not synced"}
              </span>
            )
          : null}
      </dl>
          {item && full_item?.notes ? (
            <div className="dsec">
              <div className="dsec-h">
                <h3>Notes</h3>
              </div>
              <p className="cdr-notes-ro">{full_item.notes}</p>
            </div>
          ) : null}
        </>
      )}

      {item && detail && detail.firings.length > 0 ? (
        <div className="dsec">
          <div className="dsec-h">
            <h3>Runs</h3>
            <span className="cnt num">{detail.firings.length}</span>
          </div>
          {detail.firings.map((one) => (
            <div className="act-line" key={one.at}>
              <span className="ico">
                <Ic name={one.status === "done" ? "circle-check" : one.status === "running" ? "loader" : "circle-dot"} size={13} />
              </span>
              <span className="grow">
                <Badge tone={FIRING_TONES[one.status] || "gray"}>{one.status}</Badge> {one.error ? <span className="muted">{one.error}</span> : null}
                {one.session_id ? (
                  <a className="link cdr-open" href={"#/sessions/" + encodeURIComponent(one.session_id)}>
                    Open session
                  </a>
                ) : null}
              </span>
              <time className="faint num">{clockLong(one.at)}</time>
            </div>
          ))}
        </div>
      ) : null}

      {item && detail && detail.history.length > 0 ? (
        <div className="dsec">
          <div className="dsec-h">
            <h3>Activity</h3>
            <span className="cnt num">{detail.history.length}</span>
          </div>
          {detail.history.map((line, index) => (
            <div className="act-line" key={index}>
              <span className="ico">
                {line.by.startsWith("agent:") ? <AgentFace name={byText(line.by)} /> : <Ic name="user" size={13} />}
              </span>
              <span className="grow">
                <b>{byText(line.by) === "you" ? "You" : byText(line.by)}</b> {line.summary.toLowerCase()}
              </span>
              <time className="faint num">{ago(line.at)}</time>
            </div>
          ))}
        </div>
      ) : null}

      {slot || run ? (
        <div className="dsec">
          <p className="muted cdr-hint">
            {slot
              ? "Dragging this slot or Move… changes this one run only. Change the schedule itself in the workflow editor."
              : "Runs are recorded here automatically. Open the run to see every node."}
          </p>
          <Button size="sm" icon="external-link" onClick={openWorkflow}>
            {run ? "Open run" : "Open workflow"}
          </Button>
        </div>
      ) : null}
    </Drawer>
  );
}

/* ------------------------------------------------------------------ */
/* Move and scope dialogs                                             */
/* ------------------------------------------------------------------ */

export function MoveDialog({
  entry,
  onClose,
  onMove,
}: {
  entry: Entry;
  onClose: () => void;
  onMove: (day: Date, minute: number | undefined) => Promise<void>;
}) {
  const timed = isTimed(entry);
  const [date, setDate] = useState(entry.date || dayKey(new Date(entry.start)));
  const [time, setTime] = useState(timed ? timeOf(entry.start) : "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function submit() {
    if (!/^\d{4}-\d{2}-\d{2}$/.test(date)) {
      setError("Pick a date.");
      return;
    }
    let minute: number | undefined;
    if (timed) {
      const match = /^(\d{1,2}):(\d{2})$/.exec(time);
      if (!match) {
        setError("Pick a time.");
        return;
      }
      minute = Number(match[1]) * 60 + Number(match[2]);
    }
    setBusy(true);
    try {
      await onMove(parseDay(date), minute);
      onClose();
    } catch (failure) {
      setError(errorMessage(failure));
      setBusy(false);
    }
  }

  return (
    <Modal
      title={"Move “" + entry.title + "”"}
      subtitle={entry.repeating ? "Only this occurrence moves." : undefined}
      size="sm"
      onClose={onClose}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" loading={busy} onClick={() => void submit()}>
            Move
          </Button>
        </>
      }
    >
      <div className="cform-row">
        <Field label="Date" className="grow">
          <Input type="date" value={date} onChange={(event) => setDate(event.target.value)} data-autofocus />
        </Field>
        {timed ? (
          <Field label="Time">
            <Input type="time" value={time} onChange={(event) => setTime(event.target.value)} />
          </Field>
        ) : null}
      </div>
      {error ? <p className="cform-err">{error}</p> : null}
    </Modal>
  );
}

export function ScopeDialog({
  entry,
  onClose,
  onThis,
  onAll,
}: {
  entry: Entry;
  onClose: () => void;
  onThis: () => void;
  onAll: () => void;
}) {
  return (
    <Modal
      title="Move a repeating item"
      subtitle={"“" + entry.title + "” repeats."}
      size="sm"
      onClose={onClose}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={onAll}>All of them</Button>
          <Button variant="primary" onClick={onThis} data-autofocus>
            This one only
          </Button>
        </>
      }
    >
      <p className="muted" style={{ margin: 0 }}>
        Move only this occurrence, or shift the whole series by the same amount?
      </p>
    </Modal>
  );
}

/* ------------------------------------------------------------------ */
/* Item editor                                                        */
/* ------------------------------------------------------------------ */

const COLOR_LIST: ItemColor[] = ["indigo", "blue", "teal", "green", "amber", "rose", "violet", "gray"];
const WEEKDAYS: Weekday[] = ["mon", "tue", "wed", "thu", "fri", "sat", "sun"];
const DEFAULT_COLOR: Record<ItemKind, ItemColor> = {
  event: "blue",
  task: "violet",
  reminder: "amber",
  note: "gray",
  habit: "green",
};

interface FormState {
  kind: ItemKind;
  title: string;
  notes: string;
  allDay: boolean;
  date: string;
  endDate: string;
  start: string;
  end: string;
  time: string;
  freq: RepeatFreq;
  days: Weekday[];
  until: string;
  assignee: string;
  color: ItemColor;
  done: boolean;
  actionType: "" | Action["type"];
  agent: string;
  prompt: string;
  workflowId: string;
  input: string;
  target: "telegram" | "web";
  text: string;
}

const pad = (value: number) => String(value).padStart(2, "0");

function minuteText(minute: number): string {
  return pad(Math.floor(minute / 60) % 24) + ":" + pad(minute % 60);
}

function formFrom(item: Item | null, kind: ItemKind, day: Date, minute?: number): FormState {
  const startMinute = minute ?? 9 * 60;
  const blank: FormState = {
    kind,
    title: "",
    notes: "",
    allDay: false,
    date: dayKey(day),
    endDate: dayKey(day),
    start: minuteText(startMinute),
    end: minuteText(Math.min(startMinute + 60, 23 * 60 + 59)),
    time: minute !== undefined ? minuteText(minute) : "",
    freq: kind === "habit" ? "daily" : "none",
    days: [],
    until: "",
    assignee: "owner",
    color: DEFAULT_COLOR[kind],
    done: false,
    actionType: "",
    agent: "",
    prompt: "",
    workflowId: "",
    input: "",
    target: "telegram",
    text: "",
  };
  if (!item) {
    return blank;
  }
  const action = item.action;
  return {
    ...blank,
    kind: item.kind,
    title: item.title,
    notes: item.notes,
    allDay: item.all_day,
    date: item.date || (item.start ? dayKey(new Date(item.start)) : blank.date),
    endDate: item.end_date || item.date || (item.end ? dayKey(new Date(item.end)) : blank.date),
    start: item.start ? timeOf(item.start) : blank.start,
    end: item.end ? timeOf(item.end) : blank.end,
    time: item.time,
    freq: item.repeat?.freq || "none",
    days: item.repeat?.days || [],
    until: item.repeat?.until || "",
    assignee: item.assignee || "owner",
    color: (item.color || DEFAULT_COLOR[item.kind]) as ItemColor,
    done: item.state === "done",
    actionType: action ? action.type : "",
    agent: action && action.type === "agent_prompt" ? action.agent : "",
    prompt: action && action.type === "agent_prompt" ? action.prompt : "",
    workflowId: action && action.type === "run_workflow" ? action.workflow_id : "",
    input: action && action.type === "run_workflow" && action.input !== undefined ? JSON.stringify(action.input, null, 2) : "",
    target: action && action.type === "send_message" ? action.target : "telegram",
    text: action && action.type === "send_message" ? action.text : "",
  };
}

type Errors = Record<string, string>;

function toInput(form: FormState): { input: ItemInput; errors: Errors } {
  const errors: Errors = {};
  const title = form.title.trim();
  if (!title) {
    errors.title = "Give it a title.";
  }
  const input: ItemInput = {
    kind: form.kind,
    title,
    notes: form.notes,
    assignee: form.assignee,
    color: form.color,
    start: 0,
    end: 0,
    all_day: false,
    date: "",
    end_date: "",
    time: "",
    repeat: { freq: "none", days: [], until: "" },
    action: null,
  };
  if (form.kind !== "note") {
    input.repeat = {
      freq: form.freq,
      days: form.freq === "weekly" ? form.days : [],
      until: form.freq === "none" ? "" : form.until,
    };
    if (form.freq === "weekly" && form.days.length === 0) {
      errors.repeat = "Pick at least one weekday.";
    }
  }
  switch (form.kind) {
    case "event":
      if (form.allDay) {
        input.all_day = true;
        input.date = form.date;
        input.end_date = form.endDate || form.date;
        if (input.end_date < input.date) {
          errors.when = "The end date is before the start date.";
        }
      } else {
        input.start = instantOf(form.date, form.start);
        input.end = instantOf(form.date, form.end);
        if (!input.start || !input.end) {
          errors.when = "Pick a start and end time.";
        } else if (input.end <= input.start) {
          errors.when = "The end is before the start.";
        }
      }
      break;
    case "reminder":
      input.start = instantOf(form.date, form.start);
      if (!input.start) {
        errors.when = "Pick a date and time.";
      }
      break;
    case "task":
      input.date = form.date;
      input.time = form.time;
      input.state = form.done ? "done" : "open";
      break;
    case "habit":
      input.date = form.date;
      input.time = form.time;
      if (form.freq !== "daily" && form.freq !== "weekly") {
        errors.repeat = "A habit repeats daily or on chosen weekdays.";
      }
      break;
    default:
      input.date = form.date;
  }
  if (!input.date && !input.start) {
    errors.when = errors.when || "Pick a date.";
  }
  if (form.kind !== "note" && form.actionType) {
    if (form.actionType === "agent_prompt") {
      if (!form.agent || !form.prompt.trim()) {
        errors.action = "Pick an agent and write what it should do.";
      }
      input.action = { type: "agent_prompt", agent: form.agent, prompt: form.prompt.trim() };
    } else if (form.actionType === "run_workflow") {
      let parsed: unknown;
      if (form.input.trim()) {
        try {
          parsed = JSON.parse(form.input);
        } catch {
          errors.action = "The input is not valid JSON.";
        }
      }
      if (!form.workflowId) {
        errors.action = "Pick a workflow.";
      }
      input.action = { type: "run_workflow", workflow_id: form.workflowId, ...(parsed !== undefined ? { input: parsed } : {}) };
    } else {
      if (!form.text.trim()) {
        errors.action = "Write the message.";
      }
      input.action = { type: "send_message", target: form.target, text: form.text.trim() };
    }
  }
  return { input, errors };
}

function serverErrors(failure: unknown): Errors {
  const out: Errors = {};
  if (failure instanceof ApiError) {
    for (const detail of failure.details as Array<{ field?: string; message?: string }>) {
      if (detail && detail.field) {
        const field = detail.field.startsWith("action") ? "action" : detail.field.startsWith("repeat") ? "repeat" : ["start", "end", "date", "end_date", "time"].includes(detail.field) ? "when" : detail.field;
        out[field] = detail.message || "Not valid.";
      }
    }
  }
  if (Object.keys(out).length === 0) {
    out.form = errorMessage(failure);
  }
  return out;
}

export interface EditorProps {
  item: Item | null;
  kind: ItemKind;
  day: Date;
  minute?: number;
  agents: string[];
  workflows: { id: string; name: string }[];
  onClose: () => void;
  onSave: (input: ItemInput) => Promise<void>;
}

export function ItemEditor({ item, kind, day, minute, agents, workflows, onClose, onSave }: EditorProps) {
  const [form, setForm] = useState<FormState>(() => formFrom(item, kind, day, minute));
  const [errors, setErrors] = useState<Errors>({});
  const [busy, setBusy] = useState(false);
  const set = <K extends keyof FormState>(key: K, value: FormState[K]) => setForm((prev) => ({ ...prev, [key]: value }));

  async function submit() {
    const { input, errors: found } = toInput(form);
    setErrors(found);
    if (Object.keys(found).length > 0) {
      return;
    }
    setBusy(true);
    try {
      if (item) {
        delete input.kind;
      }
      await onSave(input);
      onClose();
    } catch (failure) {
      setErrors(serverErrors(failure));
      setBusy(false);
    }
  }

  const agentOptions = [{ value: "owner", label: "Me" }, ...agents.map((name) => ({ value: name, label: name }))];
  const repeatOptions = repeatOptionsFor(form);

  let when: ReactNode;
  switch (form.kind) {
    case "event":
      when = (
        <>
          <div className="cform-row">
            <Field label="Date" className="grow">
              <Input type="date" value={form.date} onChange={(event) => set("date", event.target.value)} />
            </Field>
            {form.allDay ? (
              <Field label="Until" className="grow">
                <Input type="date" value={form.endDate} onChange={(event) => set("endDate", event.target.value)} />
              </Field>
            ) : (
              <>
                <Field label="Start">
                  <Input type="time" value={form.start} onChange={(event) => set("start", event.target.value)} />
                </Field>
                <Field label="End">
                  <Input type="time" value={form.end} onChange={(event) => set("end", event.target.value)} />
                </Field>
              </>
            )}
          </div>
          <label className="cform-toggle">
            <Toggle checked={form.allDay} onChange={(value) => set("allDay", value)} /> All day
          </label>
        </>
      );
      break;
    case "reminder":
      when = (
        <div className="cform-row">
          <Field label="Date" className="grow">
            <Input type="date" value={form.date} onChange={(event) => set("date", event.target.value)} />
          </Field>
          <Field label="Time">
            <Input type="time" value={form.start} onChange={(event) => set("start", event.target.value)} />
          </Field>
        </div>
      );
      break;
    case "task":
    case "habit":
      when = (
        <div className="cform-row">
          <Field label={form.kind === "task" ? "Due" : "Starts"} className="grow">
            <Input type="date" value={form.date} onChange={(event) => set("date", event.target.value)} />
          </Field>
          <Field label="Time" hint="Optional">
            <Input type="time" value={form.time} onChange={(event) => set("time", event.target.value)} />
          </Field>
        </div>
      );
      break;
    default:
      when = (
        <Field label="Date">
          <Input type="date" value={form.date} onChange={(event) => set("date", event.target.value)} />
        </Field>
      );
  }

  return (
    <Modal
      title={(item ? "Edit " : "New ") + KIND_WORDS[form.kind].toLowerCase()}
      subtitle={item ? "Added by " + byText(item.created_by) : undefined}
      size="lg"
      onClose={onClose}
      className="cform"
      footer={
        <>
          {errors.form ? <span className="cform-err grow">{errors.form}</span> : null}
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" loading={busy} onClick={() => void submit()}>
            {item ? "Save" : "Add"}
          </Button>
        </>
      }
    >
      {!item ? (
        <Seg
          className="cform-kinds"
          value={form.kind}
          onChange={(value) => {
            const next = value as ItemKind;
            setForm((prev) => ({
              ...prev,
              kind: next,
              color: DEFAULT_COLOR[next],
              freq: next === "habit" ? (prev.freq === "weekly" ? "weekly" : "daily") : prev.freq,
            }));
          }}
          options={(Object.keys(KIND_WORDS) as ItemKind[]).map((value) => ({
            value,
            label: KIND_WORDS[value],
            icon: KIND_ICONS[value],
          }))}
        />
      ) : null}
      <Field label="Title" error={errors.title}>
        <Input
          value={form.title}
          maxLength={200}
          placeholder={
            form.kind === "habit" ? "Read 20 minutes" : form.kind === "task" ? "Pay electricity" : form.kind === "reminder" ? "Take the laundry out" : ""
          }
          onChange={(event) => set("title", event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") {
              void submit();
            }
          }}
          data-autofocus
        />
      </Field>
      <div className="cform-sec">
        {when}
        {errors.when ? <p className="cform-err">{errors.when}</p> : null}
      </div>

      {form.kind !== "note" ? (
        <div className="cform-sec">
          <Field label="Repeat" error={errors.repeat}>
            <Select value={form.freq} options={repeatOptions} onChange={(value) => set("freq", value as RepeatFreq)} />
          </Field>
          {form.freq === "weekly" ? (
            <div className="cform-days" role="group" aria-label="Weekdays">
              {WEEKDAYS.map((day) => {
                const on = form.days.includes(day);
                return (
                  <button
                    key={day}
                    type="button"
                    className={cn("cform-day", on && "on")}
                    aria-pressed={on}
                    onClick={() => set("days", on ? form.days.filter((value) => value !== day) : [...form.days, day])}
                  >
                    {day[0]!.toUpperCase() + day.slice(1, 2)}
                  </button>
                );
              })}
            </div>
          ) : null}
          {form.freq !== "none" ? (
            <Field label="Ends on" hint="Optional">
              <Input type="date" value={form.until} onChange={(event) => set("until", event.target.value)} />
            </Field>
          ) : null}
        </div>
      ) : null}

      <div className="cform-row">
        <Field label="For" className="grow">
          <Select value={form.assignee} options={agentOptions} onChange={(value) => set("assignee", value)} />
        </Field>
        <Field label="Color">
          <div className="cform-colors" role="radiogroup" aria-label="Color">
            {COLOR_LIST.map((color) => (
              <button
                key={color}
                type="button"
                role="radio"
                aria-checked={form.color === color}
                aria-label={color}
                className={cn("cform-color", form.color === color && "on")}
                style={{ "--c": entryColor({ color, kind: form.kind, source: "item" } as Entry) } as CSSProperties}
                onClick={() => set("color", color)}
              />
            ))}
          </div>
        </Field>
      </div>

      {form.kind === "task" && item ? (
        <label className="cform-toggle">
          <Checkbox checked={form.done} onChange={(value) => set("done", value)} /> Done
        </label>
      ) : null}

      {form.kind !== "note" ? (
        <div className="cform-sec cform-action">
          <Field label="When it's due" hint="Lana runs this once per occurrence. Missed ones wait for you.">
            <Select
              value={form.actionType}
              onChange={(value) => set("actionType", value as FormState["actionType"])}
              options={ACTION_OPTIONS}
            />
          </Field>
          {form.actionType === "agent_prompt" ? (
            <>
              <Field label="Agent">
                <Select
                  value={form.agent}
                  placeholder="Pick an agent"
                  options={agents.map((name) => ({ value: name, label: name }))}
                  onChange={(value) => set("agent", value)}
                />
              </Field>
              <Field label="Instruction" hint="Runs in the agent's room with its own tools.">
                <Textarea
                  rows={3}
                  value={form.prompt}
                  maxLength={4000}
                  placeholder="Recap this week's spending and post it in your room."
                  onChange={(event) => set("prompt", event.target.value)}
                />
              </Field>
            </>
          ) : null}
          {form.actionType === "run_workflow" ? (
            <>
              <Field label="Workflow">
                <Select
                  value={form.workflowId}
                  placeholder="Pick a workflow"
                  options={workflows.map((workflow) => ({ value: workflow.id, label: workflow.name }))}
                  onChange={(value) => set("workflowId", value)}
                />
              </Field>
              <Field label="Input (JSON)" hint="Optional">
                <Textarea rows={3} className="mono" value={form.input} onChange={(event) => set("input", event.target.value)} />
              </Field>
            </>
          ) : null}
          {form.actionType === "send_message" ? (
            <>
              <Seg
                value={form.target}
                onChange={(value) => set("target", value as FormState["target"])}
                options={[
                  { value: "telegram", label: "Telegram", icon: "send" },
                  { value: "web", label: "Web", icon: "monitor" },
                ]}
              />
              <Field label="Message">
                <Textarea rows={2} value={form.text} maxLength={2000} onChange={(event) => set("text", event.target.value)} />
              </Field>
            </>
          ) : null}
          {errors.action ? <p className="cform-err">{errors.action}</p> : null}
        </div>
      ) : null}

      <Field label="Notes" hint="Optional">
        <Textarea rows={3} value={form.notes} maxLength={10000} onChange={(event) => set("notes", event.target.value)} />
      </Field>
    </Modal>
  );
}

function repeatOptionsFor(form: FormState) {
  return [
    ...(form.kind === "habit" ? [] : [{ value: "none", label: "Does not repeat" }]),
    { value: "daily", label: "Every day" },
    ...(form.kind === "habit" ? [] : [{ value: "weekdays", label: "Every weekday (Mon–Fri)" }]),
    { value: "weekly", label: "Weekly on…" },
    ...(form.kind === "habit"
      ? []
      : [
          { value: "monthly", label: "Monthly on day " + Number(form.date.slice(8) || 1) },
          { value: "yearly", label: "Every year" },
        ]),
  ];
}

const ACTION_OPTIONS = [
  { value: "", label: "Just show it" },
  { value: "agent_prompt", label: "An agent does something" },
  { value: "run_workflow", label: "Run a workflow" },
  { value: "send_message", label: "Send me a message" },
];

/* Editing inside the entry drawer (owner request 2026-11-05: no edit modal).
   Every field is inline, gr8r-style; a save bar appears once something
   changed. Repeating items edit the whole series. */
function ItemDrawerForm({
  item,
  entry,
  agents,
  workflows,
  onSave,
  extra,
}: {
  item: Item;
  entry: Entry;
  agents: string[];
  workflows: { id: string; name: string }[];
  onSave: (fields: ItemInput) => Promise<void>;
  extra: ReactNode;
}) {
  const initial = useMemo(() => formFrom(item, item.kind, new Date(), undefined), [item]);
  const [form, setForm] = useState<FormState>(initial);
  const [errors, setErrors] = useState<Errors>({});
  const [busy, setBusy] = useState(false);
  const set = <K extends keyof FormState>(key: K, value: FormState[K]) => setForm((prev) => ({ ...prev, [key]: value }));
  const dirty = JSON.stringify(form) !== JSON.stringify(initial);

  async function save() {
    const { input, errors: found } = toInput(form);
    setErrors(found);
    if (Object.keys(found).length > 0) {
      return;
    }
    delete input.kind;
    setBusy(true);
    try {
      await onSave(input);
    } catch (failure) {
      setErrors(serverErrors(failure));
      setBusy(false);
    }
  }

  const row = (icon: string, label: string, value: ReactNode, error?: string) => (
    <>
      <dt>
        <Ic name={icon} size={14} />
        {label}
      </dt>
      <dd>
        {value}
        {error ? <span className="cform-err cdr-err">{error}</span> : null}
      </dd>
    </>
  );
  const date = (key: "date" | "endDate", label: string) => (
    <Input type="date" className="cdr-in" aria-label={label} value={form[key]} onChange={(event) => set(key, event.target.value)} />
  );
  const time = (key: "start" | "end" | "time", label: string) => (
    <Input type="time" className="cdr-in cdr-time" aria-label={label} value={form[key]} onChange={(event) => set(key, event.target.value)} />
  );

  let when: ReactNode;
  switch (form.kind) {
    case "event":
      when = (
        <>
          {date("date", "Date")}
          {form.allDay ? (
            <>
              <span className="faint">to</span>
              {date("endDate", "End date")}
            </>
          ) : (
            <>
              {time("start", "Start")}
              <span className="faint">–</span>
              {time("end", "End")}
            </>
          )}
          <label className="cform-toggle cdr-allday">
            <Toggle checked={form.allDay} onChange={(value) => set("allDay", value)} /> All day
          </label>
        </>
      );
      break;
    case "reminder":
      when = (
        <>
          {date("date", "Date")}
          {time("start", "Time")}
        </>
      );
      break;
    case "task":
    case "habit":
      when = (
        <>
          {date("date", form.kind === "task" ? "Due date" : "Start date")}
          {time("time", "Time")}
        </>
      );
      break;
    default:
      when = date("date", "Date");
  }

  return (
    <div className="cdr-form">
      <textarea
        className="ttl-edit"
        rows={1}
        aria-label="Title"
        value={form.title}
        maxLength={200}
        onChange={(event) => set("title", event.target.value)}
        onKeyDown={(event) => {
          if (event.key === "Enter") {
            event.preventDefault();
            void save();
          }
        }}
      />
      {errors.title ? <p className="cform-err">{errors.title}</p> : null}

      <dl className="kv cdr-kv">
        {row("calendar", form.freq !== "none" && form.kind !== "note" ? "Starts" : form.kind === "task" ? "Due" : "When", when, errors.when)}
        {form.kind !== "note"
          ? row(
              "repeat",
              "Repeat",
              <>
                <Select size="sm" className="cdr-sel" value={form.freq} options={repeatOptionsFor(form)} onChange={(value) => set("freq", value as RepeatFreq)} />
                {form.freq === "weekly" ? (
                  <span className="cform-days" role="group" aria-label="Weekdays">
                    {WEEKDAYS.map((day) => {
                      const on = form.days.includes(day);
                      return (
                        <button
                          key={day}
                          type="button"
                          className={cn("cform-day", on && "on")}
                          aria-pressed={on}
                          onClick={() => set("days", on ? form.days.filter((value) => value !== day) : [...form.days, day])}
                        >
                          {day[0]!.toUpperCase() + day.slice(1, 2)}
                        </button>
                      );
                    })}
                  </span>
                ) : null}
                {form.freq !== "none" ? (
                  <>
                    <span className="faint">until</span>
                    <Input type="date" className="cdr-in" aria-label="Ends on" value={form.until} onChange={(event) => set("until", event.target.value)} />
                  </>
                ) : null}
              </>,
              errors.repeat
            )
          : null}
        {row(
          "user",
          "For",
          <Select
            size="sm"
            className="cdr-sel"
            aria-label="For"
            value={form.assignee}
            options={[{ value: "owner", label: "Me" }, ...agents.map((name) => ({ value: name, label: name }))]}
            onChange={(value) => set("assignee", value)}
          />
        )}
        {form.kind === "task"
          ? row(
              "circle-dot",
              "Status",
              <label className="cform-toggle">
                <Checkbox checked={form.done} onChange={(value) => set("done", value)} /> Done
              </label>
            )
          : null}
        {row(
          "palette",
          "Color",
          <span className="cform-colors" role="radiogroup" aria-label="Color">
            {COLOR_LIST.map((color) => (
              <button
                key={color}
                type="button"
                role="radio"
                aria-checked={form.color === color}
                aria-label={color}
                className={cn("cform-color", form.color === color && "on")}
                style={{ "--c": entryColor({ color, kind: form.kind, source: "item" } as Entry) } as CSSProperties}
                onClick={() => set("color", color)}
              />
            ))}
          </span>
        )}
        {form.kind !== "note"
          ? row(
              "zap",
              "When due",
              <Select
                size="sm"
                className="cdr-sel"
                aria-label="When due"
                value={form.actionType}
                options={ACTION_OPTIONS}
                onChange={(value) => set("actionType", value as FormState["actionType"])}
              />
            )
          : null}
        {extra}
      </dl>

      {form.kind !== "note" && form.actionType ? (
        <div className="cform-sec cform-action cdr-action">
          {form.actionType === "agent_prompt" ? (
            <>
              <Field label="Agent">
                <Select
                  value={form.agent}
                  placeholder="Pick an agent"
                  options={agents.map((name) => ({ value: name, label: name }))}
                  onChange={(value) => set("agent", value)}
                />
              </Field>
              <Field label="Instruction" hint="Runs in the agent's room with its own tools.">
                <Textarea rows={3} value={form.prompt} maxLength={4000} onChange={(event) => set("prompt", event.target.value)} />
              </Field>
            </>
          ) : null}
          {form.actionType === "run_workflow" ? (
            <>
              <Field label="Workflow">
                <Select
                  value={form.workflowId}
                  placeholder="Pick a workflow"
                  options={workflows.map((workflow) => ({ value: workflow.id, label: workflow.name }))}
                  onChange={(value) => set("workflowId", value)}
                />
              </Field>
              <Field label="Input (JSON)" hint="Optional">
                <Textarea rows={3} className="mono" value={form.input} onChange={(event) => set("input", event.target.value)} />
              </Field>
            </>
          ) : null}
          {form.actionType === "send_message" ? (
            <>
              <Seg
                value={form.target}
                onChange={(value) => set("target", value as FormState["target"])}
                options={[
                  { value: "telegram", label: "Telegram", icon: "send" },
                  { value: "web", label: "Web", icon: "monitor" },
                ]}
              />
              <Field label="Message">
                <Textarea rows={2} value={form.text} maxLength={2000} onChange={(event) => set("text", event.target.value)} />
              </Field>
            </>
          ) : null}
          <p className="muted cdr-hint">Lana runs this once per occurrence. Missed ones wait for you.</p>
          {errors.action ? <p className="cform-err">{errors.action}</p> : null}
        </div>
      ) : null}

      <div className="dsec">
        <div className="dsec-h">
          <h3>Notes</h3>
        </div>
        <Textarea
          className="cdr-notes"
          rows={4}
          value={form.notes}
          maxLength={10000}
          placeholder="Add notes…"
          aria-label="Notes"
          onChange={(event) => set("notes", event.target.value)}
        />
      </div>

      {entry.repeating ? <p className="muted cdr-hint cdr-series">Changes here apply to the whole series.</p> : null}

      {dirty ? (
        <div className="cdr-bar" role="region" aria-label="Unsaved changes">
          {errors.form ? <span className="cform-err grow">{errors.form}</span> : <span className="muted grow">Unsaved changes</span>}
          <Button
            size="sm"
            variant="ghost"
            onClick={() => {
              setForm(initial);
              setErrors({});
            }}
          >
            Discard
          </Button>
          <Button size="sm" variant="primary" loading={busy} onClick={() => void save()}>
            Save changes
          </Button>
        </div>
      ) : null}
    </div>
  );
}
