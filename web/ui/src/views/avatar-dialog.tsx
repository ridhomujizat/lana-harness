import { useEffect, useRef, useState } from "react";
import type { CSSProperties } from "react";
import {
  deleteAgentAvatar,
  generateAgentAvatar,
  putAgentAvatar,
} from "../api/agents";
import { ApiError, errorMessage } from "../api/client";
import { getRouter } from "../api/router";
import type { RouterChoice, RouterProvider } from "../api/router";
import {
  agentCssColor,
  isHexColor,
  parseAvatarSvg,
} from "../lib/avatar-svg";
import { setAgentAvatar, useAgentAvatar } from "../lib/avatar-store";
import { Ic } from "../ui/icons";
import { Avatar, Button, IconButton, Input, Modal, Select } from "../ui/gr8r";
import type { ButtonProps, SelectOption } from "../ui/gr8r";
import { choiceOptions } from "./agent-runtime";
import { claudeAccountNames, claudeTargetAccount } from "./router/claude-code";

/* Spec 43: the "Generate avatar" dialog. The owner draws blob avatars with the
   agent's model (or the built-in generator), picks a body color, steps back
   and forth through this session's results, then saves one or goes back to
   the letter avatar. The results live in component state only. */

export const HISTORY_LIMIT = 10;

export interface AvatarResult {
  svg: string;
  model: string;
  source: "model" | "local" | "current" | "default";
}

/* The six color tokens, then vivid presets from the reference palette. */
export const TOKEN_SWATCHES: readonly { value: string; label: string }[] = [
  { value: "brand", label: "Brand" },
  { value: "success", label: "Green" },
  { value: "warning", label: "Amber" },
  { value: "error", label: "Red" },
  { value: "blue", label: "Blue" },
  { value: "gray", label: "Gray" },
];

export const PRESET_SWATCHES: readonly { value: string; label: string }[] = [
  { value: "#111111", label: "Black" },
  { value: "#12b5a6", label: "Teal" },
  { value: "#9b5cff", label: "Violet" },
  { value: "#1a8cff", label: "Bright blue" },
  { value: "#ff3d9a", label: "Pink" },
  { value: "#ff6a00", label: "Orange" },
  { value: "#ff9f00", label: "Bright amber" },
  { value: "#9a6a3f", label: "Brown" },
  { value: "#7a7a7a", label: "Stone" },
];

const BACKGROUND_MODEL = "";

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

/* The router choices the generator may use: never a Claude Code model, nor a
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
  svg: string;
  color: string;
  restartRequired: boolean;
}

export interface AvatarDialogProps {
  name: string;
  /* The agent color today: a token or a "#rrggbb" hex. */
  color: string;
  /* The avatar SVG today (saved or default); "" only from an older server. */
  svg: string;
  /* "saved", "default", or "" when the server does not say. */
  source?: string;
  onClose: () => void;
  onSaved?: (result: AvatarSavedResult) => void;
}

export function AvatarDialog({
  name,
  color,
  svg,
  source = "",
  onClose,
  onSaved,
}: AvatarDialogProps) {
  const [history, setHistory] = useState<AvatarResult[]>(() =>
    svg && parseAvatarSvg(svg)
      ? [{ svg, model: "", source: source === "default" ? "default" : "current" }]
      : []
  );
  /* Whether the agent is on its default avatar, so Reset has nothing to do. */
  const [onDefault, setOnDefault] = useState(source === "default" || !svg);
  const [index, setIndex] = useState(() => (svg && parseAvatarSvg(svg) ? 0 : -1));
  const [bodyColor, setBodyColor] = useState(() =>
    isHexColor(color) ? normalizeHex(color) : color || "brand"
  );
  const [hexDraft, setHexDraft] = useState(() => (isHexColor(color) ? normalizeHex(color) : ""));
  const [model, setModel] = useState(BACKGROUND_MODEL);
  const [options, setOptions] = useState<SelectOption[]>([
    { value: BACKGROUND_MODEL, label: "Background model" },
  ]);
  const [busy, setBusy] = useState<"" | "generate" | "save" | "delete">("");
  const [error, setError] = useState("");
  const alive = useRef(true);

  useEffect(() => {
    alive.current = true;
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
  const cssColor = agentCssColor(bodyColor);
  const hexValue = isHexColor(bodyColor) ? normalizeHex(bodyColor) : "";
  const hexInvalid = hexDraft !== "" && normalizeHex(hexDraft) === "";
  const running = busy !== "";

  async function generate() {
    setBusy("generate");
    setError("");
    try {
      const body: { model?: string; seed: number } = { seed: randomSeed() };
      if (model) {
        body.model = model;
      }
      const result = await generateAgentAvatar(name, body);
      if (!alive.current) {
        return;
      }
      if (!result || !parseAvatarSvg(result.svg)) {
        setError("The generator returned a shape the dashboard cannot show. Try again.");
        return;
      }
      const entry: AvatarResult = {
        svg: result.svg,
        model: result.model || "",
        source: result.source === "local" ? "local" : "model",
      };
      const next = [...history, entry].slice(-HISTORY_LIMIT);
      setHistory(next);
      setIndex(next.length - 1);
    } catch (failure) {
      if (alive.current) {
        setError(errorMessage(failure));
      }
    } finally {
      if (alive.current) {
        setBusy("");
      }
    }
  }

  async function save() {
    if (!current) {
      return;
    }
    setBusy("save");
    setError("");
    try {
      const saved = await putAgentAvatar(name, { svg: current.svg, color: bodyColor });
      const savedSvg = saved && typeof saved.avatar_svg === "string" ? saved.avatar_svg : current.svg;
      const savedColor = saved && saved.color ? saved.color : bodyColor;
      setAgentAvatar(name, savedSvg, savedColor, (saved && saved.avatar_source) || "saved");
      if (onSaved) {
        onSaved({
          svg: savedSvg,
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
      const defaultSvg = reset && typeof reset.avatar_svg === "string" ? reset.avatar_svg : "";
      setAgentAvatar(name, defaultSvg, savedColor, "default");
      if (onSaved) {
        onSaved({ svg: defaultSvg, color: savedColor, restartRequired: false });
      }
      if (!alive.current) {
        return;
      }
      setOnDefault(true);
      if (defaultSvg && parseAvatarSvg(defaultSvg)) {
        const next = [
          ...history,
          { svg: defaultSvg, model: "", source: "default" as const },
        ].slice(-HISTORY_LIMIT);
        setHistory(next);
        setIndex(next.length - 1);
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

  function pickColor(value: string) {
    setBodyColor(value);
    setHexDraft(isHexColor(value) ? value : "");
  }

  function typeHex(value: string) {
    setHexDraft(value);
    const hex = normalizeHex(value);
    if (hex) {
      setBodyColor(hex);
    }
  }

  const sourceNote = !current
    ? "No avatar yet. Generate one to start."
    : current.source === "current"
      ? "The saved avatar."
      : current.source === "default"
        ? "The default avatar, drawn from the agent’s name."
      : current.source === "local"
        ? "Built-in generator (no model was available, or its drawing did not pass the checks)."
        : current.model
          ? "Drawn by " + current.model + "."
          : "Drawn by the background model.";

  return (
    <Modal
      title="Generate avatar"
      subtitle={"A blob character for " + name + "."}
      className="avatar-dialog"
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
      <div className="avatar-dialog-top">
        <div className="avatar-dialog-stage" aria-busy={busy === "generate"}>
          <Avatar
            className="avatar-dialog-preview"
            name={name}
            color={cssColor}
            svg={current ? current.svg : undefined}
            data-testid="avatar-preview"
          />
          {busy === "generate" ? (
            <span className="avatar-dialog-drawing" aria-hidden="true" />
          ) : null}
        </div>
        <div className="avatar-dialog-side">
          <p className="avatar-dialog-note" role="status" aria-live="polite">
            {busy === "generate" ? "Drawing a new avatar… this can take up to 40 seconds." : sourceNote}
          </p>
          <div className="avatar-dialog-actions">
            <Button
              variant="primary"
              icon="sparkles"
              loading={busy === "generate"}
              disabled={running}
              onClick={() => void generate()}
              data-autofocus
            >
              {busy === "generate" ? "Generating…" : history.some((item) => item.source === "model" || item.source === "local")
                  ? "Regenerate"
                  : "Generate"}
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
          <label className="avatar-dialog-label" htmlFor="avatar-model">
            Model
          </label>
          <Select
            id="avatar-model"
            size="sm"
            options={options}
            value={model}
            disabled={running}
            onChange={setModel}
          />
        </div>
      </div>

      {history.length > 0 ? (
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
                    : "Result " + (position + 1)) +
                (position === index ? ", selected" : "")
              }
              disabled={running}
              onClick={() => setIndex(position)}
            >
              <Avatar name={name} color={cssColor} svg={item.svg} size="lg" aria-hidden="true" />
            </button>
          ))}
        </div>
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
                disabled={busy === "save" || busy === "delete"}
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
            disabled={busy === "save" || busy === "delete"}
            onChange={(event) => typeHex(event.target.value)}
          />
          <input
            type="color"
            className="avatar-dialog-picker"
            aria-label="Pick a custom color"
            value={hexValue || "#9b5cff"}
            disabled={busy === "save" || busy === "delete"}
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

      {error ? (
        <p className="err avatar-dialog-error" role="alert">
          <Ic name="circle-alert" size={12} />
          {error}
        </p>
      ) : null}
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
  /* The row's avatar_svg and avatar_source; the store wins after a save. */
  svg?: string;
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
  svg,
  source,
  onSaved,
  label = "Generate avatar",
  compact,
  variant = "secondary",
  icon = "sparkles",
  className,
  ...rest
}: GenerateAvatarButtonProps) {
  const [open, setOpen] = useState(false);
  const stored = useAgentAvatar(name);
  const currentSvg = stored && stored.svg ? stored.svg : svg || "";
  const currentColor = stored && stored.color ? stored.color : color;
  const currentSource = stored && stored.svg ? stored.source : source || "";
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
          svg={currentSvg}
          source={currentSource}
          onClose={() => setOpen(false)}
          onSaved={onSaved}
        />
      ) : null}
    </>
  );
}
