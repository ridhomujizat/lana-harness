import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { ClipboardEvent, DragEvent } from "react";
import { MAX_ATTACHMENT_BYTES, MAX_ATTACHMENTS } from "../api/attachments";
import { Ic } from "../ui/icons";
import { Chip, IconButton } from "../ui/gr8r";

/* The shared attachment affordances of the session and new-session composers
   (REQ-ATT-006). Files are held as drafts until they are uploaded; image
   previews use object URLs that are revoked when a chip is removed or the
   view unmounts. */

export interface DraftAttachment {
  id: string;
  file: File;
  /* The object URL of an image preview; empty for other files. */
  url: string;
  isImage: boolean;
}

let attachmentSeq = 0;

function nextAttachmentId(): string {
  attachmentSeq += 1;
  return "attachment-" + attachmentSeq;
}

function isImageFile(file: File): boolean {
  return file.type.startsWith("image/");
}

function createPreviewURL(file: File): string {
  if (typeof URL !== "undefined" && typeof URL.createObjectURL === "function") {
    return URL.createObjectURL(file);
  }
  return "";
}

function revokeURL(url: string): void {
  if (url && typeof URL.revokeObjectURL === "function") {
    URL.revokeObjectURL(url);
  }
}

/* revokeAttachmentURLs frees the object URLs of items that are no longer shown
   anywhere, for example once the sent message reaches the thread. */
export function revokeAttachmentURLs(items: DraftAttachment[]): void {
  for (const item of items) {
    revokeURL(item.url);
  }
}

export interface AttachmentDraft {
  items: DraftAttachment[];
  notice: string;
  addFiles: (files: File[]) => void;
  remove: (id: string) => void;
  clear: () => void;
  restore: (items: DraftAttachment[]) => void;
}

/* useAttachmentDraft holds the chosen files. Adding checks the limits of
   REQ-ATT-001 and names the file that was refused. */
export function useAttachmentDraft(): AttachmentDraft {
  const [items, setItems] = useState<DraftAttachment[]>([]);
  const [notice, setNotice] = useState("");
  const itemsRef = useRef<DraftAttachment[]>([]);
  const urlsRef = useRef<string[]>([]);

  useEffect(() => {
    return () => {
      for (const url of urlsRef.current) {
        revokeURL(url);
      }
      urlsRef.current = [];
    };
  }, []);

  const addFiles = useCallback((files: File[]) => {
    const next = itemsRef.current.slice();
    const problems: string[] = [];
    for (const file of files) {
      if (next.length >= MAX_ATTACHMENTS) {
        problems.push(
          file.name +
            " was not added: a message can carry at most " +
            MAX_ATTACHMENTS +
            " files."
        );
        continue;
      }
      if (file.size > MAX_ATTACHMENT_BYTES) {
        problems.push(file.name + " is over the 20 MB limit.");
        continue;
      }
      const isImage = isImageFile(file);
      const url = isImage ? createPreviewURL(file) : "";
      if (url) {
        urlsRef.current.push(url);
      }
      next.push({ id: nextAttachmentId(), file, url, isImage });
    }
    itemsRef.current = next;
    setItems(next);
    setNotice(problems.join(" "));
  }, []);

  const remove = useCallback((id: string) => {
    const next: DraftAttachment[] = [];
    for (const item of itemsRef.current) {
      if (item.id === id) {
        revokeURL(item.url);
        urlsRef.current = urlsRef.current.filter((url) => url !== item.url);
        continue;
      }
      next.push(item);
    }
    itemsRef.current = next;
    setItems(next);
  }, []);

  const clear = useCallback(() => {
    itemsRef.current = [];
    setItems([]);
    setNotice("");
  }, []);

  const restore = useCallback((restored: DraftAttachment[]) => {
    itemsRef.current = restored;
    setItems(restored);
    setNotice("");
  }, []);

  return { items, notice, addFiles, remove, clear, restore };
}

/* AttachmentChips shows the chosen files above the composer. Each chip has a
   remove button labelled with the file name. */
export function AttachmentChips({
  items,
  onRemove,
}: {
  items: DraftAttachment[];
  onRemove: (id: string) => void;
}) {
  if (items.length === 0) {
    return null;
  }
  return (
    <ul className="attach-chips" aria-label="Attached files">
      {items.map((item) => (
        <li key={item.id}>
          <Chip
            className="attach-chip"
            removeLabel={"Remove " + item.file.name}
            onRemove={() => onRemove(item.id)}
          >
            {item.isImage && item.url ? (
              <img
                className="attach-chip-thumb"
                src={item.url}
                alt={item.file.name}
              />
            ) : (
              <span className="attach-chip-icon" aria-hidden="true">
                <Ic name="file" size={13} />
              </span>
            )}
            <span className="attach-chip-name">{item.file.name}</span>
          </Chip>
        </li>
      ))}
    </ul>
  );
}

/* AttachButton is the paperclip that opens a multi-file picker. */
export function AttachButton({
  disabled,
  onFiles,
}: {
  disabled: boolean;
  onFiles: (files: File[]) => void;
}) {
  const inputRef = useRef<HTMLInputElement>(null);
  return (
    <>
      <IconButton
        icon="paperclip"
        size="sm"
        tip="Attach files"
        label="Attach files"
        disabled={disabled}
        onClick={() => {
          if (inputRef.current) {
            inputRef.current.click();
          }
        }}
      />
      <input
        ref={inputRef}
        className="attach-input"
        type="file"
        multiple
        tabIndex={-1}
        aria-hidden="true"
        onChange={(event) => {
          const input = event.target;
          const files = input.files ? Array.from(input.files) : [];
          if (files.length > 0) {
            onFiles(files);
          }
          input.value = "";
        }}
      />
    </>
  );
}

/* AttachmentDropOverlay is the visible drop state of the session view. */
export function AttachmentDropOverlay({ active }: { active: boolean }) {
  if (!active) {
    return null;
  }
  return (
    <div className="attach-drop" role="status">
      <div className="attach-drop-inner">
        <Ic name="paperclip" size={24} />
        <p className="attach-drop-text">Drop files to attach</p>
      </div>
    </div>
  );
}

function hasFiles(data: DataTransfer | null): boolean {
  if (!data) {
    return false;
  }
  if (data.types) {
    return Array.from(data.types).includes("Files");
  }
  return Boolean(data.files && data.files.length > 0);
}

export interface FileDropHandlers {
  onDragEnter: (event: DragEvent<HTMLElement>) => void;
  onDragOver: (event: DragEvent<HTMLElement>) => void;
  onDragLeave: (event: DragEvent<HTMLElement>) => void;
  onDrop: (event: DragEvent<HTMLElement>) => void;
}

/* useFileDrop tracks the drag state of a container and hands the dropped files
   to onFiles. */
export function useFileDrop(onFiles: (files: File[]) => void): {
  active: boolean;
  handlers: FileDropHandlers;
} {
  const [active, setActive] = useState(false);
  const depthRef = useRef(0);

  const handlers = useMemo<FileDropHandlers>(
    () => ({
      onDragEnter: (event) => {
        if (!hasFiles(event.dataTransfer)) {
          return;
        }
        event.preventDefault();
        depthRef.current += 1;
        setActive(true);
      },
      onDragOver: (event) => {
        if (!hasFiles(event.dataTransfer)) {
          return;
        }
        event.preventDefault();
      },
      onDragLeave: (event) => {
        event.preventDefault();
        depthRef.current = Math.max(0, depthRef.current - 1);
        if (depthRef.current === 0) {
          setActive(false);
        }
      },
      onDrop: (event) => {
        event.preventDefault();
        depthRef.current = 0;
        setActive(false);
        const files = event.dataTransfer ? Array.from(event.dataTransfer.files) : [];
        if (files.length > 0) {
          onFiles(files);
        }
      },
    }),
    [onFiles]
  );

  return { active, handlers };
}

/* imageFilesFromClipboard returns the image files of a paste event. */
export function imageFilesFromClipboard(data: DataTransfer | null): File[] {
  if (!data) {
    return [];
  }
  const files: File[] = [];
  for (const file of Array.from(data.files || [])) {
    if (file.type.startsWith("image/")) {
      files.push(file);
    }
  }
  return files;
}

/* pasteImageFiles is the textarea paste handler shared by the session and
   new-session composers (REQ-ATT-006): a pasted image becomes a draft file. */
export function pasteImageFiles(
  event: ClipboardEvent<HTMLTextAreaElement>,
  onFiles: (files: File[]) => void
): void {
  const files = imageFilesFromClipboard(event.clipboardData);
  if (files.length > 0) {
    event.preventDefault();
    onFiles(files);
  }
}
