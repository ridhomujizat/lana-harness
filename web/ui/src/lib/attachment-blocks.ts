/* REQ-CHF-001: an owner message carries each non-image attachment as one text
   block written by internal/attach. The block starts with a fixed header, so the
   dashboard can show the file as a card instead of printing its contents:

     Attachment <path> (<media type>):\n<text>
     Attachment <path> (PDF, <n> pages):\n<text>
     Attachment <path> (PDF, <n> pages) has no text layer. …
     Attachment <path> (<media type>) was saved, but this file type cannot be read.
     Attachment <path> could not be read: <reason>.

   A long body ends with a "(Showing the first N characters. …)" notice. */

export type MessageFileStatus = "ok" | "scan" | "unsupported" | "error";

export interface MessageFile {
  /* The workspace path, e.g. attachments/2026-11-09/notes.md. */
  path: string;
  name: string;
  /* The media type from the header; "application/pdf" for PDFs. */
  mediaType: string;
  /* Page count of a PDF; 0 otherwise. */
  pages: number;
  /* The text the agent read, without the cut notice. */
  text: string;
  /* The agent only got the first part of the text. */
  truncated: boolean;
  status: MessageFileStatus;
  /* The reason a file could not be read. */
  note: string;
}

const READ = /^Attachment (\S.*) \(([^()\n]*)\):\n([\s\S]*)$/;
const SCAN = /^Attachment (\S.*) \(PDF, (\d+) pages\) has no text layer\./;
const UNSUPPORTED =
  /^Attachment (\S.*) \(([^()\n]*)\) was saved, but this file type cannot be read\.$/;
const FAILED = /^Attachment (\S.*?) could not be read: ([\s\S]*?)\.?$/;
const PDF_HEADER = /^PDF, (\d+) pages$/;
const CUT_NOTICE = /\n\(Showing the first \d+ characters\. Use read_file on [\s\S]* to read more\.\)$/;

function baseName(path: string): string {
  const parts = path.split("/");
  return parts[parts.length - 1] || path;
}

function file(
  path: string,
  mediaType: string,
  status: MessageFileStatus,
  extra: Partial<MessageFile> = {}
): MessageFile {
  return {
    path,
    name: baseName(path),
    mediaType,
    pages: 0,
    text: "",
    truncated: false,
    status,
    note: "",
    ...extra,
  };
}

/* parseAttachmentBlock reads one text block. It returns null for any text that
   is not an attachment block, so owner text is never taken for a file. */
export function parseAttachmentBlock(text: string): MessageFile | null {
  if (!text.startsWith("Attachment ")) {
    return null;
  }
  const scan = SCAN.exec(text);
  if (scan) {
    return file(scan[1] ?? "", "application/pdf", "scan", {
      pages: Number(scan[2]) || 0,
    });
  }
  const read = READ.exec(text);
  if (read) {
    const header = read[2] ?? "";
    let body = read[3] ?? "";
    const truncated = CUT_NOTICE.test(body);
    if (truncated) {
      body = body.replace(CUT_NOTICE, "");
    }
    const pdf = PDF_HEADER.exec(header);
    return file(read[1] ?? "", pdf ? "application/pdf" : header, "ok", {
      pages: pdf ? Number(pdf[1]) || 0 : 0,
      text: body,
      truncated,
    });
  }
  const unsupported = UNSUPPORTED.exec(text);
  if (unsupported) {
    return file(unsupported[1] ?? "", unsupported[2] ?? "", "unsupported");
  }
  const failed = FAILED.exec(text);
  if (failed && !failed[1]?.includes("\n")) {
    return file(failed[1] ?? "", "", "error", { note: failed[2] ?? "" });
  }
  return null;
}

/* isAttachmentBlock reports whether a content block is an attachment text block. */
export function isAttachmentBlock(block: unknown): boolean {
  if (!block || typeof block !== "object") {
    return false;
  }
  const value = block as Record<string, unknown>;
  return (
    value.type === "text" &&
    typeof value.text === "string" &&
    parseAttachmentBlock(value.text) !== null
  );
}

/* ------------------------------------------------------------------ */
/* Labels                                                             */
/* ------------------------------------------------------------------ */

export type FileFamily = "doc" | "pdf" | "data" | "image" | "code" | "other";

export function fileExtension(name: string): string {
  const dot = name.lastIndexOf(".");
  return dot > 0 ? name.slice(dot + 1).toLowerCase() : "";
}

const DOC = new Set(["md", "markdown", "txt", "text", "rtf", "doc", "docx", "odt", "log"]);
const DATA = new Set(["csv", "tsv", "json", "jsonl", "xls", "xlsx", "ods", "yaml", "yml", "xml", "toml"]);
const IMAGE = new Set(["png", "jpg", "jpeg", "gif", "webp", "svg", "heic", "bmp"]);
const CODE = new Set(["go", "ts", "tsx", "js", "jsx", "py", "rs", "java", "rb", "sh", "sql", "html", "css", "c", "h", "cpp", "swift", "kt"]);

export function fileFamily(name: string, mediaType = ""): FileFamily {
  const ext = fileExtension(name);
  if (ext === "pdf" || mediaType === "application/pdf") return "pdf";
  if (IMAGE.has(ext) || mediaType.startsWith("image/")) return "image";
  if (DATA.has(ext)) return "data";
  if (CODE.has(ext)) return "code";
  if (DOC.has(ext) || mediaType.startsWith("text/")) return "doc";
  return "other";
}

/* The short label on the type tile: MD, PDF, CSV … at most four letters. */
export function fileTypeLabel(name: string, mediaType = ""): string {
  const ext = fileExtension(name);
  if (ext === "markdown") return "MD";
  if (ext && ext.length <= 4) return ext.toUpperCase();
  if (mediaType === "application/pdf") return "PDF";
  if (mediaType.startsWith("image/")) return "IMG";
  return "FILE";
}

const TYPE_NAMES: Record<string, string> = {
  md: "Markdown",
  markdown: "Markdown",
  txt: "Text",
  log: "Log",
  pdf: "PDF",
  csv: "CSV",
  tsv: "TSV",
  json: "JSON",
  jsonl: "JSON lines",
  yaml: "YAML",
  yml: "YAML",
  xml: "XML",
  xls: "Spreadsheet",
  xlsx: "Spreadsheet",
  ods: "Spreadsheet",
  doc: "Document",
  docx: "Document",
  html: "HTML",
};

/* The type word in a card's meta line, e.g. "Markdown" or "Spreadsheet". */
export function fileTypeName(name: string, mediaType = ""): string {
  const ext = fileExtension(name);
  const known = TYPE_NAMES[ext];
  if (known) return known;
  const family = fileFamily(name, mediaType);
  if (family === "image") return "Image";
  if (family === "code") return ext.toUpperCase();
  if (mediaType.startsWith("text/")) return "Text";
  return ext ? ext.toUpperCase() : "File";
}

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return "";
  if (bytes < 1024) return bytes + " B";
  const kb = bytes / 1024;
  if (kb < 10) return kb.toFixed(1).replace(/\.0$/, "") + " KB";
  if (kb < 1024) return Math.round(kb) + " KB";
  const mb = kb / 1024;
  return (mb < 10 ? mb.toFixed(1).replace(/\.0$/, "") : String(Math.round(mb))) + " MB";
}

export function textBytes(text: string): number {
  if (typeof TextEncoder !== "undefined") {
    return new TextEncoder().encode(text).length;
  }
  return text.length;
}

/* fileMeta is the second line of a stored file's card. */
export function fileMeta(file: MessageFile): { text: string; warn: boolean } {
  const type = fileTypeName(file.name, file.mediaType);
  switch (file.status) {
    case "scan":
      return { text: "Scan · no text layer", warn: true };
    case "unsupported":
      return { text: type + " · can't be read", warn: true };
    case "error":
      return { text: "Could not be read", warn: true };
    default:
      if (file.pages > 0) {
        return { text: "PDF · " + file.pages + (file.pages === 1 ? " page" : " pages"), warn: false };
      }
      return {
        text: type + " · " + formatBytes(textBytes(file.text)) + (file.truncated ? "+" : ""),
        warn: false,
      };
  }
}
