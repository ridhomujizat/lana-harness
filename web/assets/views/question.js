import {
  html,
  useState,
  useEffect,
  useMemo,
  useRef,
} from "/assets/vendor/preact-htm.js";
import { Markdown } from "/assets/markdown.js";

/* Inline question card of REQ-ASK-012. It sits in the same slot as the
   approval card, above the composer, and is never modal: it does not take
   focus when a request opens. The card is driven by the open request that
   session.js derives from the session log (question/asked plus the matching
   tool/call input), so a reconnect or a page load shows the same request. */

let cardCounter = 0;

function asObject(value) {
  if (!value) {
    return {};
  }
  if (typeof value === "string") {
    try {
      const parsed = JSON.parse(value);
      return parsed && typeof parsed === "object" ? parsed : {};
    } catch (cause) {
      return {};
    }
  }
  return typeof value === "object" ? value : {};
}

/* The SSE event and the tool input share the tool input shape: question,
   header, multi_select, and options with label and description. */
export function normalizeQuestions(raw) {
  if (!Array.isArray(raw)) {
    return [];
  }
  const questions = [];
  for (const item of raw) {
    const value = item && typeof item === "object" ? item : {};
    const options = [];
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

function questionsFromInput(input) {
  const value = asObject(input);
  return normalizeQuestions(value.questions);
}

function eventPayload(event) {
  return event && event.payload && typeof event.payload === "object"
    ? event.payload
    : {};
}

/* Returns the open request of a session, or null. A request is open when its
   question/asked event has no question/answered event. The question text is
   not copied into question/asked, so it comes from the tool/call input with
   the same call_id. extraEvents may be the child-events map of the team view. */
export function openQuestionFromEvents(events, extraEvents) {
  const all = [];
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

  const calls = new Map();
  for (const event of all) {
    const payload = eventPayload(event);
    if (event.kind === "tool/call" && payload.call_id) {
      calls.set(payload.call_id, payload);
    }
  }

  const answered = new Set();
  for (const event of all) {
    const payload = eventPayload(event);
    if (event.kind === "question/answered" && payload.request_id) {
      answered.add(payload.request_id);
    }
  }

  let open = null;
  let openSeq = -1;
  for (const event of all) {
    const payload = eventPayload(event);
    if (event.kind !== "question/asked" || !payload.request_id) {
      continue;
    }
    if (answered.has(payload.request_id)) {
      continue;
    }
    const call = calls.get(payload.call_id);
    const questions = call ? questionsFromInput(call.input) : [];
    if (questions.length === 0) {
      continue;
    }
    const seq = Number(event.seq) || 0;
    if (open && seq < openSeq) {
      continue;
    }
    open = {
      request_id: payload.request_id,
      call_id: payload.call_id || "",
      expires_at: Number(payload.expires_at) || 0,
      turn_id: event.turn_id || payload.turn_id || "",
      questions,
    };
    openSeq = seq;
  }
  return open;
}

function answeredAnswer(answer) {
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

function firstUnanswered(answers) {
  for (let index = 0; index < answers.length; index += 1) {
    if (!answeredAnswer(answers[index])) {
      return index;
    }
  }
  return -1;
}

function answerBody(answers) {
  return answers.map((answer, index) => {
    if (answer.skipped) {
      return { question: index, skipped: true };
    }
    const selected = Array.from(new Set(answer.selected || []))
      .filter((value) => Number.isInteger(value) && value >= 0)
      .sort((left, right) => left - right);
    const other = String(answer.other || "").trim().slice(0, 500);
    if (selected.length > 0) {
      const entry = { question: index, selected };
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

function responderLabel(responder) {
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

function waitingText(responder, pid) {
  if (responder === "cli") {
    const value = Number(pid);
    if (Number.isInteger(value) && value > 0) {
      return "Waiting for an answer in lana chat (PID " + value + ")";
    }
    return "Waiting for an answer in lana chat";
  }
  return "Waiting for an answer in " + responderLabel(responder);
}

function CheckIcon() {
  return html`<svg
    class="question-check-icon"
    viewBox="0 0 12 10"
    aria-hidden="true"
    focusable="false"
  >
    <path
      d="M1 5l3 3 7-7"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"
    ></path>
  </svg>`;
}

function CardIcon({ name }) {
  return html`<img
    class="question-icon"
    src=${"/assets/icons/" + name + ".svg"}
    alt=""
    aria-hidden="true"
  />`;
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
}) {
  const questions = useMemo(
    () =>
      normalizeQuestions(
        question && Array.isArray(question.questions) ? question.questions : []
      ),
    [question]
  );
  const ids = useMemo(() => {
    cardCounter += 1;
    const count = cardCounter;
    return {
      title: "question-title-" + count,
      hint: "question-hint-" + count,
      progress: "question-progress-" + count,
      waiting: "question-waiting-" + count,
      group: "question-group-" + count,
    };
  }, []);

  const [index, setIndex] = useState(0);
  const [answers, setAnswers] = useState(() =>
    questions.map(() => ({ selected: [], other: "", skipped: false }))
  );
  const [otherOpen, setOtherOpen] = useState(false);
  const [otherDraft, setOtherDraft] = useState("");
  const [error, setError] = useState("");
  const [pending, setPending] = useState(false);
  const [liveText, setLiveText] = useState("");

  const cardRef = useRef(null);
  const otherInputRef = useRef(null);
  const elseButtonRef = useRef(null);
  const otherSnapshotRef = useRef({ selected: [], other: "", skipped: false });
  const otherClosingRef = useRef(false);
  const expiredRef = useRef(false);

  const total = questions.length;
  const safeIndex = total > 0 ? Math.min(index, total - 1) : 0;
  const current = total > 0 ? questions[safeIndex] : null;
  const currentAnswer = answers[safeIndex] || {
    selected: [],
    other: "",
    skipped: false,
  };
  const working = Boolean(busy) || pending;
  const last = safeIndex >= total - 1;
  const groupName =
    "question-" + (question ? question.request_id : "open") + "-" + safeIndex;
  const progressText =
    "Question " + (safeIndex + 1) + " of " + total + ": " + (current ? current.text : "");
  const describedBy = [
    ids.progress,
    current && current.multiSelect ? ids.hint : "",
    readOnly ? ids.waiting : "",
  ]
    .filter(Boolean)
    .join(" ");

  useEffect(() => {
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

  function replaceAnswer(answer, source = answers) {
    return source.map((item, itemIndex) =>
      itemIndex === safeIndex ? answer : item
    );
  }

  function focusOption(optionIndex) {
    const node = cardRef.current;
    if (!node) {
      return;
    }
    const inputs = Array.from(node.querySelectorAll(".question-option-input"));
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

  function goTo(nextIndex, nextAnswers, focusOptions) {
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

  async function submit(nextAnswers) {
    if (working) {
      return;
    }
    setPending(true);
    setError("");
    let result;
    try {
      result = onSubmit
        ? await onSubmit(question, { answers: answerBody(nextAnswers) })
        : { ok: false, message: "Could not send. Try again." };
    } catch (cause) {
      result = { ok: false, message: "Could not send. Try again." };
    }
    setPending(false);
    if (result && result.ok === false) {
      setError(result.message || "Could not send. Try again.");
    }
  }

  function submitIfComplete(nextAnswers) {
    const missing = firstUnanswered(nextAnswers);
    if (missing >= 0) {
      goTo(missing, nextAnswers, true);
      setError("Answer every question, or skip it.");
      return;
    }
    submit(nextAnswers);
  }

  function afterAnswer(nextAnswers) {
    if (safeIndex < total - 1) {
      goTo(safeIndex + 1, nextAnswers, true);
      return;
    }
    submitIfComplete(nextAnswers);
  }

  function chooseOption(optionIndex) {
    if (readOnly || working || !current) {
      return;
    }
    setError("");
    const answer = currentAnswer;
    if (current.multiSelect) {
      const selected = answer.selected.includes(optionIndex)
        ? answer.selected.filter((value) => value !== optionIndex)
        : answer.selected.concat([optionIndex]).sort((left, right) => left - right);
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

  function updateOther(value) {
    setOtherDraft(value);
    const text = String(value || "");
    const nextAnswer = { ...currentAnswer, other: text, skipped: false };
    if (!current.multiSelect && text.trim()) {
      nextAnswer.selected = [];
    }
    setAnswers(replaceAnswer(nextAnswer));
  }

  function closeOther(focusRow) {
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
    if (!current.multiSelect) {
      nextAnswer.selected = [];
    }
    const next = replaceAnswer(nextAnswer);
    setAnswers(next);
    setOtherOpen(false);
    setOtherDraft("");
    if (current.multiSelect) {
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
      if (!current.multiSelect) {
        nextAnswer.selected = [];
      }
      setAnswers(replaceAnswer(nextAnswer));
    }
    setOtherOpen(false);
    setOtherDraft("");
  }

  function handleOtherKeyDown(event) {
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
    let result;
    try {
      result = await onSkipAll(question);
    } catch (cause) {
      result = { ok: false, message: "Could not send. Try again." };
    }
    setPending(false);
    if (result && result.ok === false) {
      setError(result.message || "Could not send. Try again.");
    }
  }

  function handleKeyDown(event) {
    if (readOnly || working) {
      return;
    }
    const target = event.target;
    const isTextInput =
      target && target.tagName === "INPUT" && target.type === "text";
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
        target &&
        target.classList &&
        target.classList.contains("question-option-input")
      ) {
        event.preventDefault();
        chooseOption(Number(target.dataset.option));
      }
      return;
    }
    const inputs = cardRef.current
      ? Array.from(cardRef.current.querySelectorAll(".question-option-input"))
      : [];
    const position = inputs.indexOf(target);
    if (position < 0 || inputs.length === 0) {
      return;
    }
    event.preventDefault();
    const delta = event.key === "ArrowDown" ? 1 : -1;
    inputs[(position + delta + inputs.length) % inputs.length].focus();
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
      return html`<div class="question-option is-static" key=${"option-" + optionIndex}>
        <span class="question-key" aria-hidden="true">
          <span class="question-number">${optionIndex + 1}</span>
        </span>
        <span class="question-option-text">
          <span class="question-option-label" id=${labelId}>${option.label}</span>
          ${option.description
            ? html`<span class="question-option-description" id=${descriptionId}>${option.description}</span>`
            : null}
        </span>
      </div>`;
    }
    return html`<label class="question-option" key=${"option-" + optionIndex}>
      <input
        class="question-option-input"
        type=${current.multiSelect ? "checkbox" : "radio"}
        name=${groupName}
        checked=${checked}
        data-option=${optionIndex}
        aria-labelledby=${labelId}
        aria-describedby=${option.description ? descriptionId : undefined}
        onClick=${current.multiSelect ? undefined : () => chooseOption(optionIndex)}
        onChange=${current.multiSelect ? () => chooseOption(optionIndex) : undefined}
      />
      <span class="question-key" aria-hidden="true">
        <span class="question-number">${optionIndex + 1}</span>
        <${CheckIcon} />
      </span>
      <span class="question-option-text">
        <span class="question-option-label" id=${labelId}>${option.label}</span>
        ${option.description
          ? html`<span class="question-option-description" id=${descriptionId}>${option.description}</span>`
          : null}
      </span>
    </label>`;
  });

  return html`<section
    ref=${cardRef}
    class="question-card"
    tabindex="-1"
    role="group"
    aria-labelledby=${ids.title}
    aria-describedby=${describedBy || undefined}
    onKeyDown=${handleKeyDown}
  >
    <div class="question-head">
      <div class="question-title-wrap">
        ${current.header
          ? html`<p class="question-header">${current.header}</p>`
          : null}
        <div
          class="question-title"
          id=${ids.title}
          role="heading"
          aria-level="2"
        >
          ${current.text
            ? html`<${Markdown} text=${current.text} />`
            : "Question"}
        </div>
        ${current.multiSelect
          ? html`<p class="question-hint" id=${ids.hint}>Select all that apply</p>`
          : null}
      </div>
      <div class="question-pager">
        <button
          type="button"
          class="question-icon-button"
          aria-label="Previous question"
          title="Previous question"
          disabled=${working || safeIndex === 0}
          onClick=${() => {
            setError("");
            goTo(safeIndex - 1, answers, false);
          }}
        >
          <${CardIcon} name="chevron-left" />
        </button>
        <span class="question-progress" id=${ids.progress}>${safeIndex + 1} of ${total}</span>
        <button
          type="button"
          class="question-icon-button"
          aria-label="Next question"
          title="Next question"
          disabled=${working || last}
          onClick=${() => {
            setError("");
            goTo(safeIndex + 1, answers, false);
          }}
        >
          <${CardIcon} name="chevron-right" />
        </button>
        ${readOnly
          ? null
          : html`<button
              type="button"
              class="question-icon-button question-close"
              aria-label="Skip all questions"
              title="Skip all questions"
              disabled=${working}
              onClick=${skipAll}
            >
              <${CardIcon} name="close" />
            </button>`}
      </div>
    </div>

    <div
      class=${"question-options" + (current.multiSelect ? " question-options-multi" : " question-options-single")}
      role=${current.multiSelect ? "group" : "radiogroup"}
      aria-labelledby=${ids.title}
      aria-describedby=${current.multiSelect ? ids.hint : undefined}
    >
      ${options}
    </div>

    ${error ? html`<p class="question-error">${error}</p>` : null}

    ${readOnly
      ? html`<p class="question-waiting" id=${ids.waiting}>
          ${waitingText(responder, responderPid)}
        </p>`
      : html`<div class="question-actions">
          ${otherOpen
            ? html`<div class="question-else is-open">
                <span class="question-else-icon" aria-hidden="true">
                  <${CardIcon} name="edit" />
                </span>
                <input
                  ref=${otherInputRef}
                  class="question-else-input"
                  type="text"
                  maxlength="500"
                  placeholder="Type your answer"
                  aria-label="Something else"
                  value=${otherDraft}
                  disabled=${working}
                  onInput=${(event) => updateOther(event.target.value)}
                  onKeyDown=${handleOtherKeyDown}
                  onBlur=${handleOtherBlur}
                />
              </div>`
            : html`<button
                ref=${elseButtonRef}
                type="button"
                class="question-else-button"
                disabled=${working}
                onClick=${openOther}
              >
                <span class="question-else-icon" aria-hidden="true">
                  <${CardIcon} name="edit" />
                </span>
                <span class=${"question-else-label" + (storedOther ? " is-filled" : "")}>
                  ${storedOther || "Something else"}
                </span>
              </button>`}
          <div class="question-buttons">
            <button
              type="button"
              class="button-ui button-ui-secondary"
              disabled=${working}
              onClick=${skipCurrent}
            >
              Skip
            </button>
            ${current.multiSelect
              ? html`<button
                  type="button"
                  class="button-ui button-ui-primary"
                  disabled=${working || !canNext}
                  onClick=${nextMulti}
                >
                  ${last ? "Submit" : "Next"}
                </button>`
              : null}
          </div>
        </div>`}

    <p class="sr-only" aria-live="polite" aria-atomic="true">${live}</p>
  </section>`;
}
