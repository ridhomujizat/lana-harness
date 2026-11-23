import { useState } from "react";
import { Ic } from "../ui/icons";
import { Markdown } from "../lib/markdown";
import type { ChatDialog, ChatItem } from "../api/workbench-chat";

/* Spec 71 REQ-WC-054: the cards that answer Claude Code from the chat. The
   options come from the pane's screen (the daemon's mirror); clicking one
   sends its key to the terminal. Plain elements, not the agent chat
   components (owner decision 3). */

export interface CardActions {
  keys: (keys: string[]) => void;
  openTerminal: () => void;
  disabled?: boolean;
}

/* Claude Code dialogs are lists with a ❯ cursor: picking option i moves the
   cursor with the arrow keys from the selected one and presses Enter. */
export function pickKeys(dialog: ChatDialog | null | undefined, index: number, after: string[] = ["enter"]): string[] {
  const from = Math.max(0, dialog?.options.findIndex((o) => o.selected) ?? 0);
  const delta = index - from;
  const moves = Array.from({ length: Math.abs(delta) }, () => (delta > 0 ? "down" : "up"));
  return [...moves, ...after];
}

function OpenTerminal({ onClick }: { onClick: () => void }) {
  return (
    <button type="button" className="wbc-link" onClick={onClick}>
      Open terminal <Ic name="external-link" size={13} />
    </button>
  );
}

function OptionList({ dialog, actions, tone }: { dialog: ChatDialog; actions: CardActions; tone: "amber" | "blue" }) {
  return (
    <div className="wbc-options">
      {dialog.options.map((o, i) => (
        <button
          key={o.key + ":" + i}
          type="button"
          className={"wbc-option wbc-option-" + tone + (o.selected ? " is-selected" : "")}
          disabled={actions.disabled}
          onClick={() => actions.keys(pickKeys(dialog, i))}
        >
          <kbd>{o.key}</kbd>
          <span>{o.label}</span>
        </button>
      ))}
    </div>
  );
}

/* A permission prompt ("Do you want to proceed?"): the pending tool's command
   from the transcript when there is one, else the dialog's own lines. */
export function PermissionCard({ dialog, pending, actions }: { dialog: ChatDialog; pending?: ChatItem; actions: CardActions }) {
  const command = pending?.summary?.target || "";
  const lines = command ? [] : dialog.lines.filter((l) => l.trim());
  const verb = pending?.name || dialog.lines[0] || "";
  return (
    <div className="wbc-card wbc-card-amber" role="group" aria-label="Claude needs permission">
      <div className="wbc-card-head">
        <Ic name="triangle-alert" size={15} />
        <div>
          <div className="wbc-card-title">{pending ? "Claude wants to use " + (pending.name || "a tool") : dialog.title || "Claude needs you"}</div>
          {pending ? <div className="wbc-card-sub">{verb}</div> : null}
        </div>
      </div>
      {command ? <pre className="wbc-command">{command}</pre> : null}
      {lines.length ? (
        <pre className="wbc-command">{lines.join("\n")}</pre>
      ) : null}
      {pending ? <div className="wbc-card-q">{dialog.title}</div> : null}
      {dialog.options.length ? (
        <OptionList dialog={dialog} actions={actions} tone="amber" />
      ) : null}
      <div className="wbc-card-foot">
        <span>{dialog.options.length ? "Options read from the terminal screen · click to answer" : "These options could not be read from the screen."}</span>
        <OpenTerminal onClick={actions.openTerminal} />
      </div>
    </div>
  );
}

/* Claude is blocked but nothing could be parsed (REQ-WC-058). */
export function FallbackCard({ actions }: { actions: CardActions }) {
  return (
    <div className="wbc-card wbc-card-amber">
      <div className="wbc-card-head">
        <Ic name="triangle-alert" size={15} />
        <div>
          <div className="wbc-card-title">Claude is waiting for an answer in the terminal</div>
          <div className="wbc-card-text">This dialog can't be read from the screen. Answer it in the terminal.</div>
        </div>
      </div>
      <div className="wbc-card-actions">
        <button type="button" className="wbc-btn wbc-btn-primary" onClick={actions.openTerminal}>
          <Ic name="square-terminal" size={14} /> Open terminal
        </button>
      </div>
    </div>
  );
}

/* AskUserQuestion (decision 7): tabs per question, radio or checkbox options
   from the transcript, an Other row. A choice sends the option's number;
   Next / Submit sends Enter, Skip sends Esc. */
export function QuestionCard({ item, dialog, actions }: { item: ChatItem; dialog?: ChatDialog | null; actions: CardActions }) {
  const questions = item.questions ?? [];
  const [tab, setTab] = useState(0);
  const [picked, setPicked] = useState<Record<number, number[]>>({});
  const [other, setOther] = useState("");
  const q = questions[Math.min(tab, questions.length - 1)];
  if (!q) return null;
  const chosen = picked[tab] ?? [];
  /* The screen's option for a transcript option: same label, else the same
     position. */
  const screenIndex = (label: string, i: number) => {
    const at = dialog?.options.findIndex((o) => o.label.trim().toLowerCase() === label.trim().toLowerCase()) ?? -1;
    return at >= 0 ? at : i;
  };
  const choose = (i: number) => {
    const next = q.multi_select ? (chosen.includes(i) ? chosen.filter((x) => x !== i) : [...chosen, i]) : [i];
    setPicked({ ...picked, [tab]: next });
    actions.keys(pickKeys(dialog, screenIndex(q.options[i]!.label, i)));
    if (!q.multi_select && tab < questions.length - 1) setTab(tab + 1);
  };
  const sendOther = () => {
    const text = other.trim();
    if (!text) return;
    const typeAt = dialog?.options.findIndex((o) => /^type something/i.test(o.label)) ?? -1;
    actions.keys(pickKeys(dialog, typeAt >= 0 ? typeAt : q.options.length, ["text:" + text.slice(0, 200), "enter"]));
    setOther("");
  };
  return (
    <div className="wbc-card wbc-card-violet" role="group" aria-label="Claude asks">
      <div className="wbc-card-head">
        <Ic name="message-square" size={15} />
        <div className="wbc-card-title">Claude asks</div>
        <span className="wb-spacer" />
        {questions.length > 1 ? (
          <div className="wbc-tabs" role="tablist">
            {questions.map((qq, i) => (
              <button key={i} type="button" role="tab" aria-selected={i === tab} className={i === tab ? "is-active" : ""} onClick={() => setTab(i)}>
                {qq.header || "Question " + (i + 1)}
                {picked[i]?.length ? <Ic name="check" size={12} /> : null}
              </button>
            ))}
          </div>
        ) : null}
      </div>
      <div className="wbc-q-title">{q.question}</div>
      <div className="wbc-q-hint">{q.multi_select ? "Pick any" : "Pick one"}</div>
      <div className="wbc-q-options">
        {q.options.map((o, i) => {
          const on = chosen.includes(i);
          return (
            <button key={i} type="button" className={"wbc-q-option" + (on ? " is-on" : "")} disabled={actions.disabled} onClick={() => choose(i)}>
              <span className={q.multi_select ? "wbc-check" : "wbc-radio"} aria-hidden="true">
                {on ? (q.multi_select ? <Ic name="check" size={11} /> : <i />) : null}
              </span>
              <span className="wbc-q-body">
                <span className="wbc-q-label">
                  {o.label.replace(/\s*\(Recommended\)\s*$/i, "")}
                  {/\(Recommended\)\s*$/i.test(o.label) ? <em className="wbc-tag">Recommended</em> : null}
                </span>
                {o.description ? <span className="wbc-q-desc">{o.description}</span> : null}
              </span>
            </button>
          );
        })}
        <div className="wbc-q-option is-other">
          <span className="wbc-radio" aria-hidden="true" />
          <span className="wbc-q-label">Other:</span>
          <input
            value={other}
            placeholder="type your own answer…"
            disabled={actions.disabled}
            onChange={(e) => setOther(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                sendOther();
              }
            }}
          />
        </div>
      </div>
      <div className="wbc-card-foot">
        <span>
          {questions.length > 1 ? tab + 1 + "/" + questions.length + " · " : ""}
          {dialog?.options.length ? "Answers go to the terminal as keys" : "Read from the transcript"} · Skip = Esc
        </span>
        <span className="wbc-card-btns">
          <button type="button" className="wbc-btn" disabled={actions.disabled} onClick={() => actions.keys(["esc"])}>
            Skip
          </button>
          <button type="button" className="wbc-btn wbc-btn-primary" disabled={actions.disabled} onClick={() => actions.keys(["enter"])}>
            {tab >= questions.length - 1 ? "Submit" : "Next"}
          </button>
        </span>
      </div>
    </div>
  );
}

/* ExitPlanMode (decision 7): the plan from the transcript as markdown, the
   approval options from the screen, and a feedback field. */
export function PlanCard({ item, dialog, actions }: { item: ChatItem; dialog?: ChatDialog | null; actions: CardActions }) {
  const [open, setOpen] = useState(false);
  const [feedback, setFeedback] = useState("");
  const pending = item.state === "pending";
  const lines = (item.plan || "").split("\n");
  const long = lines.length > 14;
  const shown = open || !long ? item.plan || "" : lines.slice(0, 14).join("\n");
  /* "Tell Claude what to change" (or the last option) takes the feedback. */
  const changeAt = dialog ? Math.max(0, dialog.options.findIndex((o) => /change|keep planning|^no\b/i.test(o.label))) : -1;
  const last = dialog?.options[changeAt >= 0 ? changeAt : dialog.options.length - 1];
  return (
    <div className={"wbc-card wbc-card-blue" + (pending ? "" : " is-done")}>
      <div className="wbc-card-head">
        <Ic name="pause" size={14} />
        <div className="wbc-card-title">{pending ? "Plan ready" : item.state === "approved" ? "Plan approved" : "Plan sent back"}</div>
        <span className="wb-spacer" />
        {long ? (
          <button type="button" className="wbc-link" onClick={() => setOpen(!open)}>
            {open ? "Collapse" : "Open full"} <Ic name={open ? "chevron-up" : "chevron-down"} size={13} />
          </button>
        ) : null}
      </div>
      <div className="wbc-plan-body">
        <Markdown text={shown} />
        {long && !open ? <div className="wbc-more">… {lines.length - 14} more lines</div> : null}
      </div>
      {pending && dialog?.options.length ? (
        <>
          <div className="wbc-card-q">{dialog.title || "Proceed with this plan?"}</div>
          <OptionList dialog={dialog} actions={actions} tone="blue" />
          {last ? (
            <input
              className="wbc-feedback"
              value={feedback}
              disabled={actions.disabled}
              placeholder="Or tell Claude what to change…"
              onChange={(e) => setFeedback(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && feedback.trim()) {
                  e.preventDefault();
                  actions.keys(pickKeys(dialog, changeAt, ["text:" + feedback.trim().slice(0, 200), "enter"]));
                  setFeedback("");
                }
              }}
            />
          ) : null}
        </>
      ) : pending ? (
        <div className="wbc-card-foot">
          <span>Waiting for the approval prompt…</span>
          <OpenTerminal onClick={actions.openTerminal} />
        </div>
      ) : null}
    </div>
  );
}

/* AskUserQuestion while it is open: Claude Code writes the question to the
   transcript only once it is answered, so the card is built from the
   screen (tabs, options with descriptions, checkboxes). Choosing moves the
   cursor and presses Enter; "Other" picks "Type something." and types. */
export function ScreenQuestionCard({ dialog, actions }: { dialog: ChatDialog; actions: CardActions }) {
  const [other, setOther] = useState("");
  const typeAt = dialog.options.findIndex((o) => /^type something/i.test(o.label));
  const chatAt = dialog.options.findIndex((o) => /^chat about this/i.test(o.label));
  const multi = dialog.options.some((o) => o.checked !== undefined && o.checked);
  const choices = dialog.options.map((o, i) => ({ o, i })).filter(({ i }) => i !== typeAt && i !== chatAt);
  /* With a preview, a click moves the cursor (the preview follows it) and
     Choose answers; without one, a click answers at once. */
  const preview = dialog.preview || "";
  const cursorAt = Math.max(0, dialog.options.findIndex((o) => o.selected));
  const onPick = (i: number) => actions.keys(preview ? pickKeys(dialog, i, []) : pickKeys(dialog, i));
  const sendOther = () => {
    const text = other.trim();
    if (!text || typeAt < 0) return;
    actions.keys(pickKeys(dialog, typeAt, ["enter", "text:" + text.slice(0, 200), "enter"]));
    setOther("");
  };
  return (
    <div className="wbc-card wbc-card-violet" role="group" aria-label="Claude asks">
      <div className="wbc-card-head">
        <Ic name="message-square" size={15} />
        <div className="wbc-card-title">Claude asks</div>
        <span className="wb-spacer" />
        {dialog.tabs?.length ? (
          <div className="wbc-tabs">
            {dialog.tabs.map((t, i) => (
              <span key={i} className={"wbc-tab" + (t.done ? " is-done" : "")}>
                {t.done ? <Ic name="check" size={12} /> : null}
                {t.label}
              </span>
            ))}
          </div>
        ) : null}
      </div>
      <div className="wbc-q-title">{dialog.title}</div>
      <div className={preview ? "wbc-q-split" : undefined}>
      <div className="wbc-q-options">
        {choices.map(({ o, i }) => {
          const on = multi ? !!o.checked : o.selected;
          const recommended = /\(Recommended\)\s*$/i.test(o.label);
          return (
            <button
              key={i}
              type="button"
              className={"wbc-q-option" + (on ? " is-on" : "") + (o.selected ? " is-cursor" : "")}
              disabled={actions.disabled}
              onClick={() => onPick(i)}
              onDoubleClick={() => preview && actions.keys(["enter"])}
            >
              <span className={multi ? "wbc-check" : "wbc-radio"} aria-hidden="true">
                {on ? multi ? <Ic name="check" size={11} /> : <i /> : null}
              </span>
              <span className="wbc-q-body">
                <span className="wbc-q-label">
                  {o.label.replace(/\s*\(Recommended\)\s*$/i, "")}
                  {recommended ? <em className="wbc-tag">Recommended</em> : null}
                </span>
                {o.description ? <span className="wbc-q-desc">{o.description}</span> : null}
              </span>
            </button>
          );
        })}
        {typeAt >= 0 ? (
          <div className="wbc-q-option is-other">
            <span className="wbc-radio" aria-hidden="true" />
            <span className="wbc-q-label">Other:</span>
            <input
              value={other}
              placeholder="type your own answer…"
              disabled={actions.disabled}
              onChange={(e) => setOther(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  sendOther();
                }
              }}
            />
          </div>
        ) : null}
      </div>
      {preview ? <pre className="wbc-q-preview">{preview}</pre> : null}
      </div>
      <div className="wbc-card-foot">
        <span>
          Read from the terminal screen
          {chatAt >= 0 ? (
            <>
              {" · "}
              <button type="button" className="wbc-link" disabled={actions.disabled} onClick={() => actions.keys(pickKeys(dialog, chatAt))}>
                Chat about this
              </button>
            </>
          ) : null}
        </span>
        <span className="wbc-card-btns">
          <button type="button" className="wbc-btn" disabled={actions.disabled} onClick={() => actions.keys(["esc"])}>
            Skip
          </button>
          {preview ? (
            <button type="button" className="wbc-btn wbc-btn-primary" disabled={actions.disabled} onClick={() => actions.keys(["enter"])}>
              Choose {dialog.options[cursorAt]?.label.replace(/\s*\(Recommended\)\s*$/i, "") || ""}
            </button>
          ) : null}
          {dialog.tabs && dialog.tabs.length > 1 ? (
            <button type="button" className="wbc-btn wbc-btn-primary" disabled={actions.disabled} onClick={() => actions.keys(["tab"])}>
              Next
            </button>
          ) : null}
        </span>
      </div>
    </div>
  );
}
