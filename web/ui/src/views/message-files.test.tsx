import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { fileMeta, parseAttachmentBlock } from "../lib/attachment-blocks";
import { userMessageText } from "../lib/format";
import { buildTurns, UserBubble } from "./turn";
import { storedAttachments } from "./session";
import type { SessionEvent } from "../api/sessions";

const MD =
  "Attachment attachments/2026-11-09/05-backend-roadmap (1).md (text/markdown):\n# 05. Backend Roadmap\n\nSome text.";
const PDF =
  "Attachment attachments/2026-11-09/slides.pdf (PDF, 18 pages):\n--- page 1 ---\nOMNIX";
const SCAN =
  "Attachment attachments/scan.pdf (PDF, 2 pages) has no text layer. It is probably a scan: ask the owner to send photos or screenshots of the pages instead.";
const OTHER =
  "Attachment attachments/a.zip (application/zip) was saved, but this file type cannot be read.";
const FAILED = "Attachment attachments/b.png could not be read: image is too large.";
const LONG =
  "Attachment attachments/long.txt (text/plain):\nabc\n(Showing the first 3 characters. Use read_file on attachments/long.txt with offset to read more.)";

describe("parseAttachmentBlock", () => {
  it("reads a text file with parentheses in its name", () => {
    const file = parseAttachmentBlock(MD);
    expect(file).toMatchObject({
      path: "attachments/2026-11-09/05-backend-roadmap (1).md",
      name: "05-backend-roadmap (1).md",
      mediaType: "text/markdown",
      status: "ok",
      text: "# 05. Backend Roadmap\n\nSome text.",
      truncated: false,
    });
  });

  it("reads PDFs, scans, unsupported and failed files", () => {
    expect(parseAttachmentBlock(PDF)).toMatchObject({ pages: 18, status: "ok", mediaType: "application/pdf" });
    expect(parseAttachmentBlock(SCAN)).toMatchObject({ pages: 2, status: "scan", name: "scan.pdf" });
    expect(parseAttachmentBlock(OTHER)).toMatchObject({ status: "unsupported", mediaType: "application/zip" });
    expect(parseAttachmentBlock(FAILED)).toMatchObject({ status: "error", note: "image is too large", name: "b.png" });
  });

  it("strips the cut notice and marks the text as truncated", () => {
    const file = parseAttachmentBlock(LONG);
    expect(file?.text).toBe("abc");
    expect(file?.truncated).toBe(true);
    expect(fileMeta(file!).text).toBe("Text · 3 B+");
  });

  it("leaves owner text alone", () => {
    expect(parseAttachmentBlock("Attachment is fine, thanks")).toBeNull();
    expect(parseAttachmentBlock("pelajari materi ini")).toBeNull();
  });

  it("names the card meta", () => {
    expect(fileMeta(parseAttachmentBlock(PDF)!).text).toBe("PDF · 18 pages");
    expect(fileMeta(parseAttachmentBlock(SCAN)!)).toEqual({ text: "Scan · no text layer", warn: true });
  });
});

function userEvent(texts: string[]): SessionEvent {
  return {
    seq: 1,
    kind: "user/message",
    turn_id: "t1",
    created_at: 1,
    payload: {
      context_blocks: 1,
      content: [{ type: "text", text: "<context>\ndate\n</context>" }, ...texts.map((text) => ({ type: "text", text }))],
    },
  };
}

function userItem(items: ReturnType<typeof buildTurns>) {
  const item = items.find((entry) => entry.kind === "user");
  return item && item.kind === "user" ? item : undefined;
}

describe("owner message with files", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("keeps file contents out of the owner text", () => {
    const event = userEvent(["pelajari materi ini", MD, PDF]);
    expect(userMessageText(event.payload as Record<string, unknown>)).toBe("pelajari materi ini");
    const items = buildTurns([event]);
    const user = userItem(items);
    expect(user?.text).toBe("pelajari materi ini");
    expect(user?.attachments?.map((ref) => ref.path)).toEqual([
      "attachments/2026-11-09/05-backend-roadmap (1).md",
      "attachments/2026-11-09/slides.pdf",
    ]);
  });

  it("shows files as cards above the bubble and no empty bubble", () => {
    const event = userEvent([MD]);
    const items = buildTurns([event]);
    const refs = userItem(items)?.attachments;
    const { container } = render(<UserBubble text="" attachments={storedAttachments("s1", refs)} />);
    expect(container.querySelectorAll(".mf-card").length).toBe(1);
    expect(container.querySelector(".chat-bubble")).toBeNull();
    expect(screen.getByText("Markdown · 33 B")).toBeTruthy();
    expect(container.textContent).not.toContain("Backend Roadmap");
  });

  it("folds more than six files into a +N more tile", () => {
    const blocks = Array.from({ length: 8 }, (_, i) => "Attachment attachments/f" + i + ".md (text/markdown):\nx");
    const items = buildTurns([userEvent(["semua", ...blocks])]);
    const refs = userItem(items)?.attachments;
    const { container } = render(<UserBubble text="semua" attachments={storedAttachments("s1", refs)} />);
    expect(container.querySelectorAll(".mf-card").length).toBe(4);
    fireEvent.click(screen.getByText("+ 4 more files"));
    expect(container.querySelectorAll(".mf-card").length).toBe(8);
  });

  it("opens a preview that renders the served markdown", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response("# Served title\n\nbody text", { status: 200 })
    );
    vi.stubGlobal("fetch", fetchMock);
    const items = buildTurns([userEvent(["lihat", MD, PDF])]);
    const refs = userItem(items)?.attachments;
    render(<UserBubble text="lihat" attachments={storedAttachments("s1", refs)} />);
    fireEvent.click(screen.getByLabelText("Open 05-backend-roadmap (1).md"));
    await waitFor(() => expect(screen.getByRole("heading", { name: "Served title" })).toBeTruthy());
    expect(fetchMock.mock.calls[0]?.[0]).toBe(
      "/v1/sessions/s1/attachments/2026-11-09/05-backend-roadmap%20(1).md"
    );
    expect(screen.getByText("File 1 of 2 in this message")).toBeTruthy();
    fireEvent.click(screen.getByLabelText("Next file"));
    expect(screen.getByText("File 2 of 2 in this message")).toBeTruthy();
    expect(screen.getByText("Page 1")).toBeTruthy();
    expect(screen.getByText("OMNIX")).toBeTruthy();
    expect(screen.getByText("Open PDF")).toBeTruthy();
  });

  it("falls back to the text the agent read when the file cannot load", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("", { status: 404 })));
    const items = buildTurns([userEvent([MD])]);
    const refs = userItem(items)?.attachments;
    render(<UserBubble text="" attachments={storedAttachments("s1", refs)} />);
    fireEvent.click(screen.getByLabelText("Open 05-backend-roadmap (1).md"));
    await waitFor(() => expect(screen.getByText(/could not be loaded/)).toBeTruthy());
    expect(screen.getByRole("heading", { name: "05. Backend Roadmap" })).toBeTruthy();
  });

  it("folds a long owner text", () => {
    const text = Array.from({ length: 20 }, (_, i) => "line " + i).join("\n");
    const { container } = render(<UserBubble text={text} />);
    expect(container.querySelector(".chat-bubble-text.is-folded")).toBeTruthy();
    fireEvent.click(screen.getByText("Show more · 20 lines"));
    expect(container.querySelector(".chat-bubble-text.is-folded")).toBeNull();
    expect(screen.getByText("Show less")).toBeTruthy();
  });
});

describe("files-only send", () => {
  it("hides the (attachments) placeholder text", () => {
    const items = buildTurns([userEvent(["(attachments)", MD])]);
    const refs = userItem(items)?.attachments;
    const { container } = render(
      <UserBubble text={userItem(items)?.text || ""} attachments={storedAttachments("s1", refs)} />
    );
    expect(container.querySelector(".chat-bubble")).toBeNull();
    expect(container.querySelectorAll(".mf-card").length).toBe(1);
  });
});
