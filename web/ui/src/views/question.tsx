import { useEffect, useId, useMemo, useRef, useState } from "react";
import type { KeyboardEvent as ReactKeyboardEvent } from "react";
import type {
  QuestionAnswerEntry,
  QuestionAnswerRequest,
  QuestionEvent,
  QuestionOption,
  QuestionPrompt,
} from "../api/questions";
import type { SessionEvent } from "../api/sessions";
import { Markdown } from "../lib/markdown";

/* Inline question card of REQ-ASK-012. It sits in the same slot as the
   approval card, above the composer, and is never modal: it does not take
   focus when a request opens. The card is driven by the open request that
   session.js derives from the session log (question/asked plus the matching
   tool/call input), so a reconnect or a page load shows the same request. */

interface NormalizedOption {
  label: string;
  description: string;
}

export interface NormalizedQuestion {
  text: string;
  header: string;
  multiSelect: boolean;
  options: NormalizedOption[];
}

interface Answer {
  selected: number[];
  other: string;
  skipped: boolean;
}

interface SubmitResult {
  ok?: boolean;
  message?: string;
}

/* The open question also carries the call and turn ids of its question/asked
   event, which the SSE event itself does not name. */
export interface OpenQuestion extends QuestionEvent {
  call_id: string;
  turn_id: string;
}

function asObject(value: unknown): Record<string, unknown> {
  if (!value) {
    return {};
  }
  if (typeof value === "string") {
    try {
      const parsed: unknown = JSON.parse(value);
      return parsed && typeof parsed === "object"
        ? (parsed as Record<string, unknown>)
        : {};
    } catch {
      return {};
    }
  }
  return typeof value === "object" ? (value as Record<string, unknown>) : {};
}

/* The SSE event and the tool input share the tool input shape: question,
   header, multi_select, and options with label and description. */
export function normalizeQuestions(raw: unknown): NormalizedQuestion[] {
  if (!Array.isArray(raw)) {
    return [];
  }
  const questions: NormalizedQuestion[] = [];
  for (const item of raw) {
    const value = item && typeof item === "object" ? item : {};
    const options: NormalizedOption[] = [];
    for (const option of Array.isArray(value.options) ? value.options : []) {
      const entry = option && typeof option === "object" ? option : {};
      options.push({
        label: String(entry.label || ""),
        description: String(entry.description || ""),
      });
    }
    questions.push({
      text: String(value.question || value.text || ""),
      header: String(value.header || ""),
      multiSelect: Boolean(value.multi_select || value.multiSelect),
      options,
    });
  }
  return questions;
}

function toPrompts(questions: NormalizedQuestion[]): QuestionPrompt[] {
  return questions.map((question) => ({
    question: question.text,
    header: question.header,
    multi_select: question.multiSelect,
    options: question.options.map(
      (option): QuestionOption => ({
        label: option.label,
        description: option.description,
      })
    ),
  }));
}

function questionsFromInput(input: unknown): QuestionPrompt[] {
  const value = asObject(input);
  return toPrompts(normalizeQuestions(value.questions));
}

function eventPayload(event: SessionEvent): Record<string, unknown> {
  return event && event.payload && typeof event.payload === "object"
    ? event.payload
    : {};
}

function stringOf(value: unknown): string {
  return typeof value === "string" ? value : "";
}

/* Returns the open request of a session, or null. A request is open when its
   question/asked event has no question/answered event. The question text is
   not copied into question/asked, so it comes from the tool/call input with
   the same call_id. extraEvents may be the child-events map of the team view. */
export function openQuestionFromEvents(
  events: SessionEvent[] | null | undefined,
  extraEvents?:
    | SessionEvent[]
    | Record<string, SessionEvent[]>
    | null
): OpenQuestion | null {
  const all: SessionEvent[] = [];
  for (const event of events || []) {
    if (event) {
      all.push(event);
    }
  }
  if (Array.isArray(extraEvents)) {
    for (const event of extraEvents) {
      if (event) {
        all.push(event);
      }
    }
  } else if (extraEvents && typeof extraEvents === "object") {
    for (const key of Object.keys(extraEvents)) {
      const list = extraEvents[key];
      if (!Array.isArray(list)) {
        continue;
      }
      for (const event of list) {
        if (event) {
          all.push(event);
        }
      }
    }
  }

  const calls = new Map<string, Record<string, unknown>>();
  for (const event of all) {
    const payload = eventPayload(event);
    const callId = stringOf(payload.call_id);
    if (event.kind === "tool/call" && callId) {
      calls.set(callId, payload);
    }
  }

  const answered = new Set<string>();
  for (const event of all) {
    const payload = eventPayload(event);
    const requestId = stringOf(payload.request_id);
    if (event.kind === "question/answered" && requestId) {
      answered.add(requestId);
    }
  }

  let open: OpenQuestion | null = null;
  let openSeq = -1;
  for (const event of all) {
    const payload = eventPayload(event);
    const requestId = stringOf(payload.request_id);
    if (event.kind !== "question/asked" || !requestId) {
      continue;
    }
    if (answered.has(requestId)) {
      continue;
    }
    const call = calls.get(stringOf(payload.call_id));
    const questions = call ? questionsFromInput(call.input) : [];
    if (questions.length === 0) {
      continue;
    }
    const seq = Number(event.seq) || 0;
    if (open && seq < openSeq) {
      continue;
    }
    open = {
      request_id: requestId,
      call_id: stringOf(payload.call_id),
      expires_at: Number(payload.expires_at) || 0,
      turn_id: event.turn_id || stringOf(payload.turn_id) || "",
      questions,
    };
    openSeq = seq;
  }
  return open;
}

function answeredAnswer(answer: Answer | null | undefined): boolean {
  if (!answer) {
    return false;
  }
  if (answer.skipped) {
    return true;
  }
  return Boolean(
    (Array.isArray(answer.selected) && answer.selected.length > 0) ||
      String(answer.other || "").trim()
  );
}

function firstUnanswered(answers: Answer[]): number {
  for (let index = 0; index < answers.length; index += 1) {
    if (!answeredAnswer(answers[index])) {
      return index;
    }
  }
  return -1;
}

function answerBody(answers: Answer[]): QuestionAnswerEntry[] {
  return answers.map((answer, index) => {
    if (answer.skipped) {
      return { question: index, skipped: true };
    }
    const selected = Array.from(new Set(answer.selected || []))
      .filter((value) => Number.isInteger(value) && value >= 0)
      .sort((left, right) => left - right);
    const other = String(answer.other || "").trim().slice(0, 500);
    if (selected.length > 0) {
      const entry: QuestionAnswerEntry = { question: index, selected };
      if (other) {
        entry.other = other;
      }
      return entry;
    }
    if (other) {
      return { question: index, other };
    }
    return { question: index, skipped: true };
  });
}

function resultOf(value: unknown): SubmitResult {
  if (!value || typeof value !== "object") {
    return {};
  }
  const record = value as Record<string, unknown>;
  return {
    ok: typeof record.ok === "boolean" ? record.ok : undefined,
    message: typeof record.message === "string" ? record.message : undefined,
  };
}

function responderLabel(responder: string): string {
  if (responder === "cli") {
    return "lana chat";
  }
  if (responder === "telegram") {
    return "Telegram";
  }
  if (!responder) {
    return "another channel";
  }
  const text = String(responder);
  return text.charAt(0).toUpperCase() + text.slice(1);
}

function waitingText(responder: string, pid: number | null): string {
  if (responder === "cli") {
    const value = Number(pid);
    if (Number.isInteger(value) && value > 0) {
      return "Waiting for an answer in lana chat (PID " + value + ")";
    }
    return "Waiting for an answer in lana chat";
  }
  return "Waiting for an answer in " + responderLabel(responder);
}

function emptyAnswer(): Answer {
  return { selected: [], other: "", skipped: false };
}

function CheckIcon() {
  return (
    <svg
      className="question-check-icon"
      viewBox="0 0 12 10"
      aria-hidden="true"
      focusable="false"
    >
      <path
        d="M1 5l3 3 7-7"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
      ></path>
    </svg>
  );
}

function CardIcon({ name }: { name: string }) {
  return (
    <img
      className="question-icon"
      src={"/assets/icons/" + name + ".svg"}
      alt=""
      aria-hidden="true"
    />
  );
}

export interface QuestionCardProps {
  question: QuestionEvent;
  responder?: string;
  responderPid?: number | null;
  readOnly?: boolean;
  busy?: boolean;
  onSubmit?: (
    question: QuestionEvent,
    body: QuestionAnswerRequest
  ) => Promise<unknown>;
  onSkipAll?: (question: QuestionEvent) => Promise<unknown>;
  onExpire?: (question: QuestionEvent) => void;
}

export function QuestionCard({
  question,
  responder = "web",
  responderPid = null,
  readOnly = false,
  busy = false,
  onSubmit,
  onSkipAll,
  onExpire,
}: QuestionCardProps) {
  const questions = useMemo(
    () =>
      normalizeQuestions(
        question && Array.isArray(question.questions) ? question.questions : []
      ),
    [question]
  );
  const cardId = useId().replace(/[^A-Za-z0-9_-]/g, "");
  const ids = useMemo(
    () => ({
      title: "question-title-" + cardId,
      hint: "question-hint-" + cardId,
      progress: "question-progress-" + cardId,
      waiting: "question-waiting-" + cardId,
      group: "question-group-" + cardId,
    }),
    [cardId]
  );

  const [index, setIndex] = useState(0);
  const [answers, setAnswers] = useState<Answer[]>(() =>
    questions.map(() => ({ selected: [], other: "", skipped: false }))
  );
  const [otherOpen, setOtherOpen] = useState(false);
  const [otherDraft, setOtherDraft] = useState("");
  const [error, setError] = useState("");
  const [pending, setPending] = useState(false);
  const [liveText, setLiveText] = useState("");

  const cardRef = useRef<HTMLElement>(null);
  const otherInputRef = useRef<HTMLInputElement>(null);
  const elseButtonRef = useRef<HTMLButtonElement>(null);
  const otherSnapshotRef = useRef<Answer>(emptyAnswer());
  const otherClosingRef = useRef(false);
  const expiredRef = useRef(false);

  const total = questions.length;
  const safeIndex = total > 0 ? Math.min(index, total - 1) : 0;
  const current = total > 0 ? questions[safeIndex] || null : null;
  const currentAnswer = answers[safeIndex] || emptyAnswer();
  const working = Boolean(busy) || pending;
  const last = safeIndex >= total - 1;
  const groupName =
    "question-" + (question ? question.request_id : "open") + "-" + safeIndex;
  const progressText =
    "Question " +
    (safeIndex + 1) +
    " of " +
    total +
    ": " +
    (current ? current.text : "");
  const describedBy = [
    ids.progress,
    current && current.multiSelect ? ids.hint : "",
    readOnly ? ids.waiting : "",
  ]
    .filter(Boolean)
    .join(" ");

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- the visible live text follows the pager, as in question.js
    setLiveText(progressText);
  }, [progressText]);

  useEffect(() => {
    const expiresAt = question ? Number(question.expires_at) || 0 : 0;
    if (!expiresAt) {
      return undefined;
    }
    expiredRef.current = false;
    const delay = expiresAt - Date.now();
    const fire = () => {
      if (expiredRef.current) {
        return;
      }
      expiredRef.current = true;
      if (onExpire) {
        onExpire(question);
      }
    };
    if (delay <= 0) {
      const immediate = setTimeout(fire, 0);
      return () => clearTimeout(immediate);
    }
    const timer = setTimeout(fire, delay);
    return () => clearTimeout(timer);
  }, [question, onExpire]);

  function replaceAnswer(answer: Answer, source: Answer[] = answers): Answer[] {
    return source.map((item, itemIndex) =>
      itemIndex === safeIndex ? answer : item
    );
  }

  function focusOption(optionIndex: number) {
    const node = cardRef.current;
    if (!node) {
      return;
    }
    const inputs = Array.from(
      node.querySelectorAll<HTMLInputElement>(".question-option-input")
    );
    const target = inputs[optionIndex] || inputs[0];
    if (target) {
      target.focus();
    }
  }

  function focusElseRow() {
    window.setTimeout(() => {
      if (elseButtonRef.current) {
        elseButtonRef.current.focus();
      }
    }, 0);
  }

  function goTo(
    nextIndex: number,
    nextAnswers: Answer[],
    focusOptions: boolean
  ) {
    if (nextIndex < 0 || nextIndex >= total) {
      return;
    }
    setIndex(nextIndex);
    setAnswers(nextAnswers);
    setOtherOpen(false);
    setOtherDraft("");
    if (focusOptions) {
      window.setTimeout(() => focusOption(0), 0);
    }
  }

  async function submit(nextAnswers: Answer[]) {
    if (working) {
      return;
    }
    setPending(true);
    setError("");
    let result: SubmitResult;
    try {
      result = resultOf(
        onSubmit
          ? await onSubmit(question, { answers: answerBody(nextAnswers) })
          : { ok: false, message: "Could not send. Try again." }
      );
    } catch {
      result = { ok: false, message: "Could not send. Try again." };
    }
    setPending(false);
    if (result.ok === false) {
      setError(result.message || "Could not send. Try again.");
    }
  }

  function submitIfComplete(nextAnswers: Answer[]) {
    const missing = firstUnanswered(nextAnswers);
    if (missing >= 0) {
      goTo(missing, nextAnswers, true);
      setError("Answer every question, or skip it.");
      return;
    }
    void submit(nextAnswers);
  }

  function afterAnswer(nextAnswers: Answer[]) {
    if (safeIndex < total - 1) {
      goTo(safeIndex + 1, nextAnswers, true);
      return;
    }
    submitIfComplete(nextAnswers);
  }

  function chooseOption(optionIndex: number) {
    if (readOnly || working || !current) {
      return;
    }
    setError("");
    const answer = currentAnswer;
    if (current.multiSelect) {
      const selected = answer.selected.includes(optionIndex)
        ? answer.selected.filter((value) => value !== optionIndex)
        : answer.selected
            .concat([optionIndex])
            .sort((left, right) => left - right);
      setAnswers(replaceAnswer({ ...answer, selected, skipped: false }));
      return;
    }
    const next = replaceAnswer({
      ...answer,
      selected: [optionIndex],
      other: "",
      skipped: false,
    });
    setAnswers(next);
    afterAnswer(next);
  }

  function openOther() {
    if (readOnly || working) {
      return;
    }
    setError("");
    otherClosingRef.current = false;
    otherSnapshotRef.current = {
      selected: currentAnswer.selected.slice(),
      other: String(currentAnswer.other || ""),
      skipped: Boolean(currentAnswer.skipped),
    };
    setOtherDraft(String(currentAnswer.other || ""));
    setOtherOpen(true);
    window.setTimeout(() => {
      if (otherInputRef.current) {
        otherInputRef.current.focus();
      }
    }, 0);
  }

  function updateOther(value: string) {
    setOtherDraft(value);
    const text = String(value || "");
    const nextAnswer = { ...currentAnswer, other: text, skipped: false };
    if (!current || !current.multiSelect) {
      if (text.trim()) {
        nextAnswer.selected = [];
      }
    }
    setAnswers(replaceAnswer(nextAnswer));
  }

  function closeOther(focusRow: boolean) {
    otherClosingRef.current = true;
    setOtherOpen(false);
    setOtherDraft("");
    if (focusRow) {
      focusElseRow();
    }
  }

  function keepOther() {
    otherClosingRef.current = true;
    const text = String(otherDraft || "").trim().slice(0, 500);
    if (!text) {
      setAnswers(
        replaceAnswer({
          selected: otherSnapshotRef.current.selected.slice(),
          other: otherSnapshotRef.current.other,
          skipped: otherSnapshotRef.current.skipped,
        })
      );
      closeOther(true);
      return;
    }
    const nextAnswer = { ...currentAnswer, other: text, skipped: false };
    if (!current || !current.multiSelect) {
      nextAnswer.selected = [];
    }
    const next = replaceAnswer(nextAnswer);
    setAnswers(next);
    setOtherOpen(false);
    setOtherDraft("");
    if (current && current.multiSelect) {
      focusElseRow();
      return;
    }
    afterAnswer(next);
  }

  function handleOtherBlur() {
    if (!otherOpen) {
      return;
    }
    if (otherClosingRef.current) {
      otherClosingRef.current = false;
      setOtherOpen(false);
      setOtherDraft("");
      return;
    }
    const text = String(otherDraft || "").trim().slice(0, 500);
    if (!text) {
      setAnswers(
        replaceAnswer({
          selected: otherSnapshotRef.current.selected.slice(),
          other: otherSnapshotRef.current.other,
          skipped: otherSnapshotRef.current.skipped,
        })
      );
    } else if (text !== otherDraft) {
      const nextAnswer = { ...currentAnswer, other: text, skipped: false };
      if (!current || !current.multiSelect) {
        nextAnswer.selected = [];
      }
      setAnswers(replaceAnswer(nextAnswer));
    }
    setOtherOpen(false);
    setOtherDraft("");
  }

  function handleOtherKeyDown(event: ReactKeyboardEvent<HTMLInputElement>) {
    if (event.key === "Enter") {
      event.preventDefault();
      keepOther();
      return;
    }
    if (event.key === "Escape") {
      event.preventDefault();
      setAnswers(
        replaceAnswer({
          selected: otherSnapshotRef.current.selected.slice(),
          other: otherSnapshotRef.current.other,
          skipped: otherSnapshotRef.current.skipped,
        })
      );
      closeOther(true);
    }
  }

  function skipCurrent() {
    if (readOnly || working) {
      return;
    }
    setError("");
    otherClosingRef.current = true;
    setOtherOpen(false);
    setOtherDraft("");
    const next = replaceAnswer({ selected: [], other: "", skipped: true });
    setAnswers(next);
    if (safeIndex < total - 1) {
      goTo(safeIndex + 1, next, true);
      return;
    }
    submitIfComplete(next);
  }

  function nextMulti() {
    if (readOnly || working || !current || !current.multiSelect) {
      return;
    }
    if (!answeredAnswer(currentAnswer)) {
      return;
    }
    setError("");
    if (safeIndex < total - 1) {
      goTo(safeIndex + 1, answers, true);
      return;
    }
    submitIfComplete(answers);
  }

  async function skipAll() {
    if (readOnly || working || !onSkipAll) {
      return;
    }
    setPending(true);
    setError("");
    let result: SubmitResult;
    try {
      result = resultOf(await onSkipAll(question));
    } catch {
      result = { ok: false, message: "Could not send. Try again." };
    }
    setPending(false);
    if (result.ok === false) {
      setError(result.message || "Could not send. Try again.");
    }
  }

  function handleKeyDown(event: ReactKeyboardEvent<HTMLElement>) {
    if (readOnly || working) {
      return;
    }
    const target = event.target;
    const isTextInput =
      target instanceof HTMLInputElement && target.type === "text";
    if (isTextInput) {
      return;
    }
    if (event.altKey || event.ctrlKey || event.metaKey) {
      return;
    }
    if (/^[1-9]$/.test(event.key)) {
      const optionIndex = Number(event.key) - 1;
      if (current && optionIndex >= 0 && optionIndex < current.options.length) {
        event.preventDefault();
        chooseOption(optionIndex);
      }
      return;
    }
    if (event.key !== "ArrowDown" && event.key !== "ArrowUp") {
      if (
        event.key === "Enter" &&
        target instanceof HTMLElement &&
        target.classList.contains("question-option-input")
      ) {
        event.preventDefault();
        chooseOption(Number(target.dataset.option));
      }
      return;
    }
    const inputs = cardRef.current
      ? Array.from(
          cardRef.current.querySelectorAll<HTMLInputElement>(
            ".question-option-input"
          )
        )
      : [];
    const position =
      target instanceof HTMLInputElement ? inputs.indexOf(target) : -1;
    if (position < 0 || inputs.length === 0) {
      return;
    }
    event.preventDefault();
    const delta = event.key === "ArrowDown" ? 1 : -1;
    const nextInput = inputs[(position + delta + inputs.length) % inputs.length];
    if (nextInput) {
      nextInput.focus();
    }
  }

  if (!current) {
    return null;
  }

  const storedOther = String(currentAnswer.other || "").trim();
  const canNext = answeredAnswer(currentAnswer);
  const live = error || (working ? "Sending…" : liveText);

  const options = current.options.map((option, optionIndex) => {
    const descriptionId = ids.group + "-desc-" + safeIndex + "-" + optionIndex;
    const labelId = ids.group + "-label-" + safeIndex + "-" + optionIndex;
    const checked = Array.isArray(currentAnswer.selected)
      ? currentAnswer.selected.includes(optionIndex)
      : false;
    if (readOnly) {
      return (
        <div
          className="question-option is-static"
          key={"option-" + optionIndex}
        >
          <span className="question-key" aria-hidden="true">
            <span className="question-number">{optionIndex + 1}</span>
          </span>
          <span className="question-option-text">
            <span className="question-option-label" id={labelId}>
              {option.label}
            </span>
            {option.description ? (
              <span
                className="question-option-description"
                id={descriptionId}
              >
                {option.description}
              </span>
            ) : null}
          </span>
        </div>
      );
    }
    return (
      <label className="question-option" key={"option-" + optionIndex}>
        <input
          className="question-option-input"
          type={current.multiSelect ? "checkbox" : "radio"}
          name={groupName}
          checked={checked}
          data-option={optionIndex}
          aria-labelledby={labelId}
          aria-describedby={option.description ? descriptionId : undefined}
          onClick={
            current.multiSelect ? undefined : () => chooseOption(optionIndex)
          }
          /* A single-select radio keeps its click handler; React also wants an
             onChange for the controlled checked value. */
          onChange={
            current.multiSelect
              ? () => chooseOption(optionIndex)
              : () => undefined
          }
        />
        <span className="question-key" aria-hidden="true">
          <span className="question-number">{optionIndex + 1}</span>
          <CheckIcon />
        </span>
        <span className="question-option-text">
          <span className="question-option-label" id={labelId}>
            {option.label}
          </span>
          {option.description ? (
            <span className="question-option-description" id={descriptionId}>
              {option.description}
            </span>
          ) : null}
        </span>
      </label>
    );
  });

  return (
    <section
      ref={cardRef}
      className="question-card"
      tabIndex={-1}
      role="group"
      aria-labelledby={ids.title}
      aria-describedby={describedBy || undefined}
      onKeyDown={handleKeyDown}
    >
      <div className="question-head">
        <div className="question-title-wrap">
          {current.header ? (
            <p className="question-header">{current.header}</p>
          ) : null}
          <div
            className="question-title"
            id={ids.title}
            role="heading"
            aria-level={2}
          >
            {current.text ? <Markdown text={current.text} /> : "Question"}
          </div>
          {current.multiSelect ? (
            <p className="question-hint" id={ids.hint}>
              Select all that apply
            </p>
          ) : null}
        </div>
        <div className="question-pager">
          <button
            type="button"
            className="question-icon-button"
            aria-label="Previous question"
            title="Previous question"
            disabled={working || safeIndex === 0}
            onClick={() => {
              setError("");
              goTo(safeIndex - 1, answers, false);
            }}
          >
            <CardIcon name="chevron-left" />
          </button>
          <span className="question-progress" id={ids.progress}>
            {safeIndex + 1} of {total}
          </span>
          <button
            type="button"
            className="question-icon-button"
            aria-label="Next question"
            title="Next question"
            disabled={working || last}
            onClick={() => {
              setError("");
              goTo(safeIndex + 1, answers, false);
            }}
          >
            <CardIcon name="chevron-right" />
          </button>
          {readOnly ? null : (
            <button
              type="button"
              className="question-icon-button question-close"
              aria-label="Skip all questions"
              title="Skip all questions"
              disabled={working}
              onClick={() => void skipAll()}
            >
              <CardIcon name="close" />
            </button>
          )}
        </div>
      </div>

      <div
        className={
          "question-options" +
          (current.multiSelect
            ? " question-options-multi"
            : " question-options-single")
        }
        role={current.multiSelect ? "group" : "radiogroup"}
        aria-labelledby={ids.title}
        aria-describedby={current.multiSelect ? ids.hint : undefined}
      >
        {options}
      </div>

      {error ? <p className="question-error">{error}</p> : null}

      {readOnly ? (
        <p className="question-waiting" id={ids.waiting}>
          {waitingText(responder, responderPid)}
        </p>
      ) : (
        <div className="question-actions">
          {otherOpen ? (
            <div className="question-else is-open">
              <span className="question-else-icon" aria-hidden="true">
                <CardIcon name="edit" />
              </span>
              <input
                ref={otherInputRef}
                className="question-else-input"
                type="text"
                maxLength={500}
                placeholder="Type your answer"
                aria-label="Something else"
                value={otherDraft}
                disabled={working}
                onChange={(event) => updateOther(event.target.value)}
                onKeyDown={handleOtherKeyDown}
                onBlur={handleOtherBlur}
              />
            </div>
          ) : (
            <button
              ref={elseButtonRef}
              type="button"
              className="question-else-button"
              disabled={working}
              onClick={openOther}
            >
              <span className="question-else-icon" aria-hidden="true">
                <CardIcon name="edit" />
              </span>
              <span
                className={
                  "question-else-label" + (storedOther ? " is-filled" : "")
                }
              >
                {storedOther || "Something else"}
              </span>
            </button>
          )}
          <div className="question-buttons">
            <button
              type="button"
              className="button-ui button-ui-secondary"
              disabled={working}
              onClick={skipCurrent}
            >
              Skip
            </button>
            {current.multiSelect ? (
              <button
                type="button"
                className="button-ui button-ui-primary"
                disabled={working || !canNext}
                onClick={nextMulti}
              >
                {last ? "Submit" : "Next"}
              </button>
            ) : null}
          </div>
        </div>
      )}

      <p className="sr-only" aria-live="polite" aria-atomic="true">
        {live}
      </p>
    </section>
  );
}
