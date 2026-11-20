import { useEffect, useRef, useState } from "react";
import type { CSSProperties } from "react";
import {
  deleteAgentAvatar,
  generateAgentAvatar,
  getAvatarCatalog,
  putAgentAvatar,
} from "../api/agents";
import type { AvatarCatalog, CatalogItem, GenerateAvatarRequest } from "../api/agents";
import { ApiError, errorMessage } from "../api/client";
import { getRouter } from "../api/router";
import type { RouterChoice, RouterProvider } from "../api/router";
import { agentCssColor, isAvatarDoc, isHexColor } from "../lib/pixel-avatar";
import type { AvatarDoc, AvatarParts } from "../lib/pixel-avatar";
import { setAgentAvatar, useAgentAvatar } from "../lib/avatar-store";
import { Ic } from "../ui/icons";
import {
  Button,
  Checkbox,
  IconButton,
  Input,
  Modal,
  Seg,
  Select,
  Textarea,
} from "../ui/gr8r";
import type { ButtonProps, SelectOption } from "../ui/gr8r";
import { AgentAvatar } from "./agent-avatar";
import { choiceOptions } from "./agent-runtime";
import { PixelSprite } from "./pixel-sprite";
import { claudeAccountNames, claudeTargetAccount } from "./router/claude-code";

/* Spec 62: the avatar dialog. The owner shapes the agent's pixel character:
   Randomize, pick each part, recolor, or ask a model to pick parts from a
   description or to draw a new character on the template; steps back and
   forth through this session's results; then saves one or goes back to the
   default. The results live in component state only. */

export const HISTORY_LIMIT = 10;
export const PROMPT_LIMIT = 500;

export type ResultSource = "current" | "default" | "random" | "parts" | "model" | "local";

export interface AvatarResult {
  avatar: AvatarDoc;
  model: string;
  source: ResultSource;
}

/* The six color tokens, then vivid presets. */
export const TOKEN_SWATCHES: readonly { value: string; label: string }[] = [
  { value: "brand", label: "Brand" },
  { value: "success", label: "Green" },
  { value: "warning", label: "Amber" },
  { value: "error", label: "Red" },
  { value: "blue", label: "Blue" },
  { value: "gray", label: "Gray" },
];

export const PRESET_SWATCHES: readonly { value: string; label: string }[] = [
  { value: "#7c5cff", label: "Violet" },
  { value: "#f45bd0", label: "Pink" },
  { value: "#ff6b2c", label: "Orange" },
  { value: "#3d7bff", label: "Bright blue" },
  { value: "#1fb37a", label: "Emerald" },
  { value: "#ff3d5e", label: "Coral" },
  { value: "#13b5c8", label: "Teal" },
  { value: "#f5a524", label: "Bright amber" },
  { value: "#8a867e", label: "Stone" },
];

/* The picker fields of the parts, in dialog order. */
export const PART_FIELDS: readonly { key: keyof AvatarCatalog & keyof AvatarParts; label: string }[] = [
  { key: "species", label: "Species" },
  { key: "hair", label: "Hair" },
  { key: "hair_color", label: "Hair color" },
  { key: "skin", label: "Skin" },
  { key: "eyes", label: "Eyes" },
  { key: "accessory", label: "Accessory" },
  { key: "outfit", label: "Outfit" },
  { key: "pants", label: "Pants" },
];

const BACKGROUND_MODEL = "";

type AiMode = "ai_parts" | "ai_draw";

/* "#ABC" and "abc123" become "#aabbcc" / "#abc123"; anything else is "". */
export function normalizeHex(value: string): string {
  let text = String(value || "").trim().toLowerCase();
  if (!text.startsWith("#")) {
    text = "#" + text;
  }
  if (/^#[0-9a-f]{3}$/.test(text)) {
    text = "#" + text.slice(1).split("").map((char) => char + char).join("");
  }
  return /^#[0-9a-f]{6}$/.test(text) ? text : "";
}

function randomSeed(): number {
  return 1 + Math.floor(Math.random() * 2147483646);
}

function colorLabel(value: string): string {
  const swatch =
    TOKEN_SWATCHES.find((entry) => entry.value === value) ||
    PRESET_SWATCHES.find((entry) => entry.value === value);
  return swatch ? swatch.label : value;
}

function itemOptions(items: CatalogItem[] | undefined, current: string): SelectOption[] {
  const list = (items || []).map((item) => ({ value: item.id, label: item.label }));
  if (current && !list.some((item) => item.value === current)) {
    list.unshift({ value: current, label: current });
  }
  return list;
}

/* The router choices a generation may use: never a Claude Code model, nor a
   combo that starts on one (the API answers invalid_model). */
function modelOptions(
  choices: RouterChoice[],
  providers: RouterProvider[],
  combos: Parameters<typeof claudeTargetAccount>[2]
): SelectOption[] {
  const names = claudeAccountNames(choices, providers);
  const allowed = choices.filter(
    (choice) => claudeTargetAccount(choice.value, choices, combos, names) === ""
  );
  return [
    { value: BACKGROUND_MODEL, label: "Background model" },
    ...choiceOptions(allowed, providers),
  ];
}

export interface AvatarSavedResult {
  avatar: AvatarDoc;
  color: string;
  restartRequired: boolean;
}

export interface AvatarDialogProps {
  name: string;
  /* The agent color today: a token or a "#rrggbb" hex. */
  color: string;
  /* The avatar today (saved or default); null only from an older server. */
  avatar: AvatarDoc | null;
  /* "saved", "default", or "" when the server does not say. */
  source?: string;
  onClose: () => void;
  onSaved?: (result: AvatarSavedResult) => void;
}

export function AvatarDialog({
  name,
  color,
  avatar,
  source = "",
  onClose,
  onSaved,
}: AvatarDialogProps) {
  const initial = isAvatarDoc(avatar) ? avatar : null;
  const [history, setHistory] = useState<AvatarResult[]>(() =>
    initial
      ? [{ avatar: initial, model: "", source: source === "default" ? "default" : "current" }]
      : []
  );
  const [index, setIndex] = useState(() => (initial ? 0 : -1));
  /* Whether the agent is on its default avatar, so Reset has nothing to do. */
  const [onDefault, setOnDefault] = useState(source === "default" || !initial);
  const [bodyColor, setBodyColor] = useState(() =>
    isHexColor(color) ? normalizeHex(color) : color || "brand"
  );
  const [hexDraft, setHexDraft] = useState(() => (isHexColor(color) ? normalizeHex(color) : ""));
  const [catalog, setCatalog] = useState<AvatarCatalog | null>(null);
  const [aiMode, setAiMode] = useState<AiMode>("ai_parts");
  const [prompt, setPrompt] = useState("");
  const [model, setModel] = useState(BACKGROUND_MODEL);
  const [options, setOptions] = useState<SelectOption[]>([
    { value: BACKGROUND_MODEL, label: "Background model" },
  ]);
  const [busy, setBusy] = useState<"" | "random" | "render" | "ai" | "save" | "delete">("");
  const [error, setError] = useState("");
  const alive = useRef(true);
  /* The newest render request; an older answer that lands late is dropped. */
  const renderTicket = useRef(0);

  useEffect(() => {
    alive.current = true;
    getAvatarCatalog()
      .then((data) => {
        if (alive.current && data) {
          setCatalog(data);
        }
      })
      .catch(() => {
        /* Without the catalog the pickers stay hidden; Randomize and AI work. */
      });
    getRouter()
      .then((router) => {
        if (alive.current && router) {
          setOptions(modelOptions(router.choices, router.providers, router.combos));
        }
      })
      .catch(() => {
        /* The background model still works without the list. */
      });
    return () => {
      alive.current = false;
    };
  }, []);

  const current = index >= 0 ? history[index] : undefined;
  const parts = current && current.avatar.kind === "parts" ? current.avatar.parts : undefined;
  const cssColor = agentCssColor(bodyColor);
  const hexValue = isHexColor(bodyColor) ? normalizeHex(bodyColor) : "";
  const hexInvalid = hexDraft !== "" && normalizeHex(hexDraft) === "";
  const running = busy !== "";
  const locked = busy === "save" || busy === "delete" || busy === "ai";

  function push(entry: AvatarResult) {
    const next = [...history.slice(0, index + 1), entry].slice(-HISTORY_LIMIT);
    setHistory(next);
    setIndex(next.length - 1);
  }

  /* replaceCurrent swaps the shown result in place (a recolor), keeping undo
     for real changes. */
  function replaceCurrent(entry: AvatarResult) {
    setHistory((list) => list.map((item, position) => (position === index ? entry : item)));
  }

  async function run(
    kind: "random" | "render" | "ai",
    body: GenerateAvatarRequest,
    place: "push" | "replace" = "push"
  ) {
    const ticket = ++renderTicket.current;
    setBusy(kind);
    setError("");
    try {
      const result = await generateAgentAvatar(name, body);
      if (!alive.current || ticket !== renderTicket.current) {
        return;
      }
      if (!result || !isAvatarDoc(result.avatar)) {
        setError("The server returned an avatar the dashboard cannot show. Try again.");
        return;
      }
      const source: ResultSource =
        kind === "random"
          ? "random"
          : kind === "render"
            ? "parts"
            : result.source === "local"
              ? "local"
              : "model";
      const entry: AvatarResult = { avatar: result.avatar, model: result.model || "", source };
      if (place === "replace" && current) {
        replaceCurrent({ ...entry, source: current.source, model: current.model });
      } else {
        push(entry);
      }
    } catch (failure) {
      if (alive.current) {
        setError(errorMessage(failure));
      }
    } finally {
      if (alive.current && ticket === renderTicket.current) {
        setBusy("");
      }
    }
  }

  function randomize() {
    void run("random", { mode: "random", seed: randomSeed(), color: bodyColor });
  }

  function pickPart(key: keyof AvatarParts, value: string | boolean) {
    if (!parts) {
      return;
    }
    const next = { ...parts, [key]: value, color: bodyColor } as AvatarParts;
    void run("render", { mode: "render", parts: next, color: bodyColor });
  }

  function generateWithAi() {
    const body: GenerateAvatarRequest = { mode: aiMode, seed: randomSeed(), color: bodyColor };
    const text = prompt.trim();
    if (text) {
      body.prompt = text.slice(0, PROMPT_LIMIT);
    }
    if (model) {
      body.model = model;
    }
    void run("ai", body);
  }

  /* A new color re-renders a parts avatar (the shirt wears it); a drawn one
     keeps its own palette and only its tile changes. */
  function applyColor(value: string) {
    setBodyColor(value);
    if (parts) {
      void run(
        "render",
        { mode: "render", parts: { ...parts, color: value }, color: value },
        "replace"
      );
    }
  }

  function pickColor(value: string) {
    setHexDraft(isHexColor(value) ? value : "");
    applyColor(value);
  }

  function typeHex(value: string) {
    setHexDraft(value);
    const hex = normalizeHex(value);
    if (hex) {
      applyColor(hex);
    }
  }

  async function save() {
    if (!current) {
      return;
    }
    setBusy("save");
    setError("");
    try {
      const saved = await putAgentAvatar(name, { avatar: current.avatar, color: bodyColor });
      const savedAvatar = saved && isAvatarDoc(saved.avatar) ? saved.avatar : current.avatar;
      const savedColor = saved && saved.color ? saved.color : bodyColor;
      setAgentAvatar(name, savedAvatar, savedColor, (saved && saved.avatar_source) || "saved");
      if (onSaved) {
        onSaved({
          avatar: savedAvatar,
          color: savedColor,
          restartRequired: Boolean(saved && saved.restart_required),
        });
      }
      onClose();
    } catch (failure) {
      if (alive.current) {
        setError(saveError(failure));
        setBusy("");
      }
    }
  }

  /* Reset to default: DELETE, then show the default avatar it returns. */
  async function resetToDefault() {
    setBusy("delete");
    setError("");
    try {
      const reset = await deleteAgentAvatar(name);
      const savedColor = reset && reset.color ? reset.color : color;
      const defaultAvatar = reset && isAvatarDoc(reset.avatar) ? reset.avatar : null;
      if (defaultAvatar) {
        setAgentAvatar(name, defaultAvatar, savedColor, "default");
        if (onSaved) {
          onSaved({ avatar: defaultAvatar, color: savedColor, restartRequired: false });
        }
      }
      if (!alive.current) {
        return;
      }
      setOnDefault(true);
      if (defaultAvatar) {
        push({ avatar: defaultAvatar, model: "", source: "default" });
        setBodyColor(isHexColor(savedColor) ? normalizeHex(savedColor) : savedColor);
        setHexDraft(isHexColor(savedColor) ? normalizeHex(savedColor) : "");
      }
      setBusy("");
    } catch (failure) {
      if (alive.current) {
        setError(errorMessage(failure));
        setBusy("");
      }
    }
  }

  const sourceNote = !current
    ? "No avatar yet. Randomize or generate one to start."
    : current.source === "current"
      ? "The saved avatar."
      : current.source === "default"
        ? "The default avatar, picked from the agent’s name."
        : current.source === "random"
          ? "A random character."
          : current.source === "parts"
            ? "Your pick of parts."
            : current.source === "local"
              ? "Random character (no model was available, or its answer did not pass the checks)."
              : (current.avatar.kind === "drawn" ? "Drawn by " : "Parts picked by ") +
                (current.model || "the background model") +
                ".";

  return (
    <Modal
      title="Agent avatar"
      subtitle={"A pixel character for " + name + "."}
      className="avatar-dialog"
      size="lg"
      dismissible={busy !== "save" && busy !== "delete"}
      onClose={onClose}
      footer={
        <>
          {!onDefault ? (
            <Button variant="ghost" disabled={running} onClick={() => void resetToDefault()}>
              {busy === "delete" ? "Resetting…" : "Reset to default"}
            </Button>
          ) : null}
          <span className="sp" />
          <Button variant="secondary" disabled={busy === "save" || busy === "delete"} onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="primary"
            disabled={!current || running}
            loading={busy === "save"}
            onClick={() => void save()}
          >
            {busy === "save" ? "Saving…" : "Save"}
          </Button>
        </>
      }
    >
      <div className="avatar-dialog-layout">
        <div className="avatar-dialog-aside">
          <div
            className="avatar-dialog-stage"
            aria-busy={busy === "ai" || busy === "random"}
            style={{ "--pxc": cssColor } as CSSProperties}
            data-testid="avatar-preview"
          >
            {current ? (
              <PixelSprite sprite={current.avatar.sprite} view="full" animate />
            ) : (
              <span className="avatar-dialog-empty">?</span>
            )}
            {busy === "ai" ? <span className="avatar-dialog-drawing" aria-hidden="true" /> : null}
          </div>
          <div className="avatar-dialog-side">
            <p className="avatar-dialog-note" role="status" aria-live="polite">
              {busy === "ai"
                ? aiMode === "ai_draw"
                  ? "Drawing a new character… this can take up to a minute."
                  : "Picking parts… this can take a few seconds."
                : sourceNote}
            </p>
            <div className="avatar-dialog-actions">
              <Button
                variant="primary"
                icon="shuffle"
                loading={busy === "random"}
                disabled={locked}
                onClick={randomize}
                data-autofocus
              >
                Randomize
              </Button>
              <IconButton
                icon="undo-2"
                label="Undo"
                disabled={running || index <= 0}
                onClick={() => setIndex((value) => Math.max(0, value - 1))}
              />
              <IconButton
                icon="redo-2"
                label="Redo"
                disabled={running || index < 0 || index >= history.length - 1}
                onClick={() => setIndex((value) => Math.min(history.length - 1, value + 1))}
              />
            </div>
            {current ? (
              <div className="avatar-dialog-sizes" aria-hidden="true">
                <AgentAvatar name={name} avatar={current.avatar} bodyColor={bodyColor} size="xl" />
                <AgentAvatar name={name} avatar={current.avatar} bodyColor={bodyColor} size="lg" />
                <AgentAvatar name={name} avatar={current.avatar} bodyColor={bodyColor} size="md" />
              </div>
            ) : null}
          </div>
          {history.length > 1 ? (
            <div className="avatar-dialog-strip" role="group" aria-label="Results of this session">
              {history.map((item, position) => (
                <button
                  key={position}
                  type="button"
                  className={"avatar-dialog-thumb" + (position === index ? " is-on" : "")}
                  aria-pressed={position === index}
                  aria-label={
                    (item.source === "current"
                      ? "Saved avatar"
                      : item.source === "default"
                        ? "Default avatar"
                        : "Result " + (position + 1)) + (position === index ? ", selected" : "")
                  }
                  disabled={running}
                  onClick={() => setIndex(position)}
                >
                  <AgentAvatar name={name} avatar={item.avatar} bodyColor={bodyColor} size="lg" aria-hidden="true" />
                </button>
              ))}
            </div>
          ) : null}

        </div>
        <div className="avatar-dialog-main">
          {catalog ? (
            <fieldset className="avatar-dialog-parts" disabled={locked}>
              <legend className="avatar-dialog-label">Parts</legend>
              {!parts && current ? (
                <p className="avatar-dialog-hint">
                  This character was drawn by a model. Randomize or pick a part to start from parts.
                </p>
              ) : null}
              <div className="avatar-dialog-part-grid">
                {PART_FIELDS.map((field) => {
                  const value = parts ? String(parts[field.key] ?? "") : "";
                  return (
                    <label key={field.key} className="avatar-dialog-part">
                      <span className="avatar-dialog-label">{field.label}</span>
                      <Select
                        size="sm"
                        aria-label={field.label}
                        options={itemOptions(catalog[field.key], value)}
                        value={value}
                        placeholder="—"
                        disabled={!parts || locked}
                        onChange={(next) => pickPart(field.key, next)}
                      />
                    </label>
                  );
                })}
                <label className="avatar-dialog-part avatar-dialog-blush">
                  <Checkbox
                    checked={Boolean(parts && parts.blush)}
                    disabled={!parts || locked}
                    onChange={(checked: boolean) => pickPart("blush", checked)}
                    aria-label="Blush"
                  />
                  <span className="avatar-dialog-label">Blush</span>
                </label>
              </div>
            </fieldset>
          ) : null}

          <fieldset className="avatar-dialog-colors">
            <legend className="avatar-dialog-label">Color</legend>
            <div className="avatar-dialog-swatches" role="group" aria-label="Color presets">
              {[...TOKEN_SWATCHES, ...PRESET_SWATCHES].map((entry) => {
                const on = bodyColor === entry.value;
                return (
                  <button
                    key={entry.value}
                    type="button"
                    className={"avatar-swatch" + (on ? " is-on" : "")}
                    style={{ "--sw": agentCssColor(entry.value) } as CSSProperties}
                    aria-pressed={on}
                    aria-label={entry.label}
                    title={entry.label}
                    disabled={locked}
                    onClick={() => pickColor(entry.value)}
                  />
                );
              })}
            </div>
            <div className="avatar-dialog-custom">
              <label className="avatar-dialog-label" htmlFor="avatar-hex">
                Custom
              </label>
              <Input
                id="avatar-hex"
                className="avatar-dialog-hex"
                type="text"
                value={hexDraft}
                placeholder="#ff6a00"
                maxLength={7}
                autoComplete="off"
                spellCheck={false}
                invalid={hexInvalid}
                aria-describedby={hexInvalid ? "avatar-hex-error" : undefined}
                disabled={locked}
                onChange={(event) => typeHex(event.target.value)}
              />
              <input
                type="color"
                className="avatar-dialog-picker"
                aria-label="Pick a custom color"
                value={hexValue || "#7c5cff"}
                disabled={locked}
                onChange={(event) => pickColor(normalizeHex(event.target.value) || bodyColor)}
              />
              <span className="avatar-dialog-current">{colorLabel(bodyColor)}</span>
            </div>
            {hexInvalid ? (
              <span className="err" id="avatar-hex-error">
                <Ic name="circle-alert" size={12} />
                Use a hex color like #ff6a00.
              </span>
            ) : null}
          </fieldset>

          <fieldset className="avatar-dialog-ai" disabled={locked}>
            <legend className="avatar-dialog-label">Generate with AI</legend>
            <Seg
              className="avatar-dialog-mode"
              aria-label="AI mode"
              options={[
                { value: "ai_parts", label: "Parts", title: "The model picks parts from your description" },
                { value: "ai_draw", label: "Free draw", title: "The model draws a new character on the template" },
              ]}
              value={aiMode}
              onChange={(value) => setAiMode(value === "ai_draw" ? "ai_draw" : "ai_parts")}
            />
            <Textarea
              id="avatar-prompt"
              className="avatar-dialog-prompt"
              rows={2}
              maxLength={PROMPT_LIMIT}
              value={prompt}
              placeholder={
                aiMode === "ai_draw"
                  ? "e.g. a wizard with a tall purple hat and a white beard"
                  : "e.g. silver hair, round glasses, a red cap"
              }
              aria-label="Description"
              onChange={(event) => setPrompt(event.target.value)}
            />
            <div className="avatar-dialog-ai-row">
              <Select
                id="avatar-model"
                size="sm"
                aria-label="Model"
                options={options}
                value={model}
                disabled={locked}
                onChange={setModel}
              />
              <Button
                variant="secondary"
                icon="sparkles"
                loading={busy === "ai"}
                disabled={running}
                onClick={generateWithAi}
              >
                {busy === "ai" ? "Generating…" : "Generate"}
              </Button>
            </div>
          </fieldset>

          {error ? (
            <p className="err avatar-dialog-error" role="alert">
              <Ic name="circle-alert" size={12} />
              {error}
            </p>
          ) : null}
        </div>
      </div>
    </Modal>
  );
}

function saveError(failure: unknown): string {
  if (failure instanceof ApiError && Array.isArray(failure.details)) {
    for (const detail of failure.details) {
      if (detail && typeof detail === "object") {
        const record = detail as { key?: unknown; message?: unknown };
        if (String(record.key || "").endsWith(".color") && record.message) {
          return "Color: " + String(record.message);
        }
      }
    }
  }
  return errorMessage(failure);
}

export interface GenerateAvatarButtonProps
  extends Omit<ButtonProps, "onClick" | "color"> {
  name: string;
  /* The agent color from the page's row; the store wins after a save. */
  color: string;
  /* The row's avatar and avatar_source; the store wins after a save. */
  avatar?: AvatarDoc | null;
  source?: string;
  onSaved?: (result: AvatarSavedResult) => void;
  label?: string;
  /* A small round icon button for the corner of a header avatar. */
  compact?: boolean;
}

/* A button that opens the dialog with the agent's current avatar. */
export function GenerateAvatarButton({
  name,
  color,
  avatar,
  source,
  onSaved,
  label = "Edit avatar",
  compact,
  variant = "secondary",
  icon = "sparkles",
  className,
  ...rest
}: GenerateAvatarButtonProps) {
  const [open, setOpen] = useState(false);
  const stored = useAgentAvatar(name);
  const currentAvatar = stored && isAvatarDoc(stored.avatar) ? stored.avatar : avatar || null;
  const currentColor = stored && stored.color ? stored.color : color;
  const currentSource = stored ? stored.source : source || "";
  return (
    <>
      {compact ? (
        <IconButton
          icon={icon}
          size="xs"
          label={label}
          className={"avatar-edit-badge" + (className ? " " + className : "")}
          onClick={() => setOpen(true)}
        />
      ) : (
        <Button
          variant={variant}
          icon={icon}
          className={className}
          onClick={() => setOpen(true)}
          {...rest}
        >
          {label}
        </Button>
      )}
      {open ? (
        <AvatarDialog
          name={name}
          color={currentColor}
          avatar={currentAvatar}
          source={currentSource}
          onClose={() => setOpen(false)}
          onSaved={onSaved}
        />
      ) : null}
    </>
  );
}
