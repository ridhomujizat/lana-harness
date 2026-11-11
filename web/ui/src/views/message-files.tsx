import { useEffect, useState } from "react";
import {
  fileFamily,
  fileMeta,
  fileTypeLabel,
  fileTypeName,
  formatBytes,
  type MessageFile,
} from "../lib/attachment-blocks";
import { Markdown } from "../lib/markdown";
import { Button, IconButton, Modal, Seg } from "../ui/gr8r";
import { Ic } from "../ui/icons";

/* REQ-CHF-002..REQ-CHF-005: the files of an owner message. Files sit above the
   text bubble as cards; images stay thumbnails. A stored file's card opens a
   preview of the served file (API-ATT-003). */

/* UserAttachment is the renderable form of an attachment: a local object URL
   for a pending file, or a served URL for a stored one. */
export interface UserAttachment {
  key: string;
  name: string;
  /* The served URL (stored files and images) or an image's object URL. */
  src: string;
  alt: string;
  isImage: boolean;
  /* The workspace path of a stored file. */
  path?: string;
  /* The text block the agent got for a stored non-image file. */
  file?: MessageFile;
  /* The card's second line, e.g. "Markdown · 14 KB". */
  meta?: string;
  warn?: boolean;
}

/* More than this many files fold into "+N more". */
const FOLD_OVER = 6;
const FOLD_SHOW = 4;

export function FileTypeTile({
  name,
  mediaType = "",
  size = "md",
}: {
  name: string;
  mediaType?: string;
  size?: "md" | "lg";
}) {
  return (
    <span
      className={"ft-tile ft-" + fileFamily(name, mediaType) + (size === "lg" ? " ft-lg" : "")}
      aria-hidden="true"
    >
      {fileTypeLabel(name, mediaType)}
    </span>
  );
}

function FileCard({
  attachment,
  onOpen,
}: {
  attachment: UserAttachment;
  onOpen: (() => void) | null;
}) {
  const mediaType = attachment.file?.mediaType || "";
  const body = (
    <>
      <FileTypeTile name={attachment.name} mediaType={mediaType} />
      <span className="mf-card-text">
        <span className="mf-card-name" title={attachment.name}>
          {attachment.name}
        </span>
        {attachment.meta ? (
          <span className={"mf-card-meta" + (attachment.warn ? " is-warn" : "")}>
            {attachment.meta}
          </span>
        ) : null}
      </span>
    </>
  );
  if (!onOpen) {
    return <span className="mf-card">{body}</span>;
  }
  return (
    <button
      type="button"
      className="mf-card is-button"
      aria-label={"Open " + attachment.name}
      onClick={onOpen}
    >
      {body}
    </button>
  );
}

/* MessageFiles lays out the cards of one owner message, right aligned. */
export function MessageFiles({ attachments }: { attachments: UserAttachment[] }) {
  const [expanded, setExpanded] = useState(false);
  const [open, setOpen] = useState<number | null>(null);
  const images = attachments.filter((item) => item.isImage && item.src);
  const files = attachments.filter((item) => !(item.isImage && item.src));
  const folded = !expanded && files.length > FOLD_OVER;
  const shown = folded ? files.slice(0, FOLD_SHOW) : files;
  const hidden = files.slice(FOLD_SHOW);
  const previewable = files.filter((item) => item.src && item.file);
  const openFile = open === null ? null : previewable[open] || null;

  return (
    <div className="mf">
      {images.length > 0 ? (
        <div className="mf-images">
          {images.map((image) => (
            <a
              className="chat-attachment-image"
              key={image.key}
              href={image.src}
              target="_blank"
              rel="noopener noreferrer"
            >
              <img src={image.src} loading="lazy" alt={image.alt} />
            </a>
          ))}
        </div>
      ) : null}
      {files.length > 0 ? (
        <div className={"mf-files" + (files.length === 1 ? " is-single" : "")}>
          {shown.map((item) => {
            const index = previewable.indexOf(item);
            return (
              <FileCard
                key={item.key}
                attachment={item}
                onOpen={index >= 0 ? () => setOpen(index) : null}
              />
            );
          })}
          {folded ? (
            <button
              type="button"
              className="mf-more"
              onClick={() => setExpanded(true)}
            >
              <span className="mf-more-count">
                {"+ " + hidden.length + " more files"}
              </span>
              <span className="mf-more-names">
                {hidden.map((item) => item.name).join(", ")}
              </span>
            </button>
          ) : null}
        </div>
      ) : null}
      {openFile && open !== null ? (
        <FilePreview
          attachment={openFile}
          index={open}
          count={previewable.length}
          onMove={(next) => setOpen(next)}
          onClose={() => setOpen(null)}
        />
      ) : null}
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* Preview                                                            */
/* ------------------------------------------------------------------ */

type Loaded =
  | { state: "loading" }
  | { state: "ready"; text: string; bytes: number }
  | { state: "failed" };

type PreviewKind = "markdown" | "text" | "pdf" | "none";

function previewKind(file: MessageFile): PreviewKind {
  if (file.status !== "ok") {
    return file.mediaType === "application/pdf" ? "pdf" : "none";
  }
  if (file.pages > 0 || file.mediaType === "application/pdf") {
    return "pdf";
  }
  const name = file.name.toLowerCase();
  if (name.endsWith(".md") || name.endsWith(".markdown") || file.mediaType === "text/markdown") {
    return "markdown";
  }
  return "text";
}

function lineCount(text: string): number {
  if (!text) return 0;
  return text.split("\n").length;
}

/* useFileText fetches a text file through the session route. The block text
   the agent got stays the fallback when the request fails. */
function useFileText(src: string, enabled: boolean): Loaded {
  /* The result is kept with its URL, so moving to another file reads as
     loading until that file's request settles. */
  const [result, setResult] = useState<{ src: string; loaded: Loaded } | null>(null);
  useEffect(() => {
    if (!enabled) {
      return undefined;
    }
    let live = true;
    fetch(src, { credentials: "same-origin" })
      .then((response) => {
        if (!response.ok) {
          throw new Error("status " + response.status);
        }
        return response.blob();
      })
      .then((blob) => blob.text().then((text) => ({ text, bytes: blob.size })))
      .then((value) => {
        if (live) setResult({ src, loaded: { state: "ready", ...value } });
      })
      .catch(() => {
        if (live) setResult({ src, loaded: { state: "failed" } });
      });
    return () => {
      live = false;
    };
  }, [src, enabled]);
  return result && result.src === src ? result.loaded : { state: "loading" };
}

export function FilePreview({
  attachment,
  index,
  count,
  onMove,
  onClose,
}: {
  attachment: UserAttachment;
  index: number;
  count: number;
  onMove: (index: number) => void;
  onClose: () => void;
}) {
  const file = attachment.file as MessageFile;
  const kind = previewKind(file);
  const textual = kind === "markdown" || kind === "text";
  const loaded = useFileText(attachment.src, textual);
  /* View and "Copied" belong to one file; another file starts fresh. */
  const [viewState, setViewState] = useState({ key: "", view: "preview", copied: false });
  const own = viewState.key === attachment.key;
  const view = own ? viewState.view : "preview";
  const copied = own && viewState.copied;
  const setView = (next: "preview" | "source") =>
    setViewState({ key: attachment.key, view: next, copied });
  const setCopied = (next: boolean) =>
    setViewState({ key: attachment.key, view, copied: next });

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null;
      if (target && (target.tagName === "INPUT" || target.tagName === "TEXTAREA")) {
        return;
      }
      if (event.key === "ArrowLeft" && index > 0) onMove(index - 1);
      if (event.key === "ArrowRight" && index < count - 1) onMove(index + 1);
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [index, count, onMove]);

  const text = loaded.state === "ready" ? loaded.text : file.text;
  const fallback = textual && loaded.state === "failed";

  const metaParts: string[] = [];
  if (kind === "pdf" && file.pages > 0) {
    metaParts.push("PDF · " + file.pages + (file.pages === 1 ? " page" : " pages"));
  } else if (loaded.state === "ready") {
    metaParts.push(fileTypeName(file.name, file.mediaType) + " · " + formatBytes(loaded.bytes));
    metaParts.push(lineCount(loaded.text) + " lines");
  } else {
    metaParts.push(attachment.meta || fileMeta(file).text);
  }

  async function copyPath() {
    try {
      await navigator.clipboard.writeText(file.path);
      setCopied(true);
    } catch {
      setCopied(false);
    }
  }

  const canToggle = kind === "markdown";

  let body;
  if (kind === "none") {
    body = (
      <div className="fp-empty">
        <p>
          {file.status === "error"
            ? "Lana could not read this file" + (file.note ? ": " + file.note + "." : ".")
            : "Lana saved this file but cannot show this type."}
        </p>
        <p className="muted">Download it to open it on this device.</p>
      </div>
    );
  } else if (kind === "pdf") {
    /* The dashboard forbids framing, so a PDF shows the text the agent read,
       page by page, and opens in its own tab. */
    body =
      file.status === "scan" ? (
        <div className="fp-empty">
          <p>This PDF is a scan with no text layer, so the agent could not read it.</p>
          <p className="muted">Open it to look at the pages, or send photos of them instead.</p>
        </div>
      ) : (
        <PdfPages text={file.text} truncated={file.truncated} />
      );
  } else if (loaded.state === "loading" && !file.text) {
    body = <p className="muted">Loading…</p>;
  } else if (kind === "markdown" && view === "preview") {
    body = (
      <div className="fp-md">
        <Markdown text={text} />
      </div>
    );
  } else {
    body = <pre className="fp-source">{text}</pre>;
  }

  return (
    <Modal
      size="lg"
      className="file-preview"
      onClose={onClose}
      title={
        <span className="fp-title">
          <FileTypeTile name={file.name} mediaType={file.mediaType} size="lg" />
          <span className="fp-title-text">
            <span className="fp-name" title={file.name}>
              {file.name}
            </span>
            <span className="fp-meta">{metaParts.join(" · ")}</span>
          </span>
        </span>
      }
      footer={
        <>
          <span className="fp-count">
            {count > 1 ? "File " + (index + 1) + " of " + count + " in this message" : "1 file in this message"}
          </span>
          <span className="sp" />
          {count > 1 ? (
            <>
              <IconButton
                icon="chevron-left"
                size="sm"
                label="Previous file"
                disabled={index === 0}
                onClick={() => onMove(index - 1)}
              />
              <IconButton
                icon="chevron-right"
                size="sm"
                label="Next file"
                disabled={index >= count - 1}
                onClick={() => onMove(index + 1)}
              />
            </>
          ) : null}
        </>
      }
    >
      <div className="fp-bar">
        {canToggle ? (
          <Seg
            options={[
              { value: "preview", label: "Preview" },
              { value: "source", label: "Source" },
            ]}
            value={view}
            onChange={(value) => setView(value === "source" ? "source" : "preview")}
          />
        ) : (
          <span />
        )}
        <span className="fp-actions">
          {kind === "pdf" ? (
            <a
              className="btn btn-secondary btn-sm"
              href={attachment.src}
              target="_blank"
              rel="noopener noreferrer"
            >
              <Ic name="external-link" size={14} />
              Open PDF
            </a>
          ) : null}
          <Button size="sm" icon="copy" onClick={copyPath}>
            {copied ? "Copied" : "Copy path"}
          </Button>
          <a className="btn btn-secondary btn-sm" href={attachment.src} download={file.name}>
            <Ic name="download" size={14} />
            Download
          </a>
        </span>
      </div>
      {fallback ? (
        <p className="fp-note">
          The file could not be loaded, so this shows the text the agent read
          {file.truncated ? " (the first part only)." : "."}
        </p>
      ) : null}
      <div className="fp-body">{body}</div>
    </Modal>
  );
}

const PAGE_MARK = /^--- page (\d+) ---$/;

/* PdfPages splits the extracted text on the "--- page N ---" lines that
   internal/attach writes. */
function PdfPages({ text, truncated }: { text: string; truncated: boolean }) {
  const pages: { n: string; lines: string[] }[] = [];
  for (const line of text.split("\n")) {
    const mark = PAGE_MARK.exec(line);
    if (mark) {
      pages.push({ n: mark[1] ?? "", lines: [] });
    } else if (pages.length > 0) {
      pages[pages.length - 1]?.lines.push(line);
    } else {
      pages.push({ n: "", lines: [line] });
    }
  }
  return (
    <div className="fp-pages">
      {pages.map((page, index) => (
        <section className="fp-page" key={index}>
          {page.n ? <h4 className="fp-page-n">{"Page " + page.n}</h4> : null}
          <pre className="fp-source">{page.lines.join("\n").trim()}</pre>
        </section>
      ))}
      {truncated ? <p className="fp-note">The agent got the first part of this PDF only.</p> : null}
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* Long text                                                          */
/* ------------------------------------------------------------------ */

const FOLD_LINES = 12;
const FOLD_CHARS = 1200;

/* REQ-CHF-006: an owner text over 12 lines folds with a fade. */
export function FoldText({ text }: { text: string }) {
  const [open, setOpen] = useState(false);
  const lines = lineCount(text);
  const long = lines > FOLD_LINES || text.length > FOLD_CHARS;
  if (!long) {
    return <span className="chat-bubble-text">{text}</span>;
  }
  return (
    <>
      <span className={"chat-bubble-text" + (open ? "" : " is-folded")}>{text}</span>
      <button
        type="button"
        className="chat-bubble-more"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
      >
        {open ? "Show less" : "Show more · " + lines + (lines === 1 ? " line" : " lines")}
      </button>
    </>
  );
}
