import { createContext, useCallback, useContext, useEffect, useState } from "react";
import type { ReactNode } from "react";
import { Ic } from "../icons";
import { IconButton } from "./primitives";
import { cn } from "./util";

export type ToastTone = "ok" | "error" | "info";

export interface ToastAction {
  label: string;
  onClick?: () => void;
}

export interface ToastOptions {
  title: ReactNode;
  body?: ReactNode;
  tone?: ToastTone;
  action?: ToastAction;
  duration?: number;
}

interface ToastItem extends ToastOptions {
  id: number;
}

interface ToastContextValue {
  show: (options: ToastOptions) => void;
}

const ToastContext = createContext<ToastContextValue | null>(null);

const DEFAULT_DURATION = 4200;
const LEAVE_DURATION = 160;

let toastSeq = 0;

const TONE_ICON: Record<ToastTone, { name: string; className?: string }> = {
  ok: { name: "circle-check", className: "ok" },
  error: { name: "circle-alert", className: "bad" },
  info: { name: "info" },
};

function Toast({ item, onDone }: { item: ToastItem; onDone: (id: number) => void }) {
  const [leaving, setLeaving] = useState(false);

  useEffect(() => {
    let removeTimer: ReturnType<typeof setTimeout> | undefined;
    const leaveTimer = setTimeout(() => {
      setLeaving(true);
      removeTimer = setTimeout(() => onDone(item.id), LEAVE_DURATION);
    }, item.duration ?? DEFAULT_DURATION);
    return () => {
      clearTimeout(leaveTimer);
      if (removeTimer) {
        clearTimeout(removeTimer);
      }
    };
  }, [item.id, item.duration, onDone]);

  const tone = item.tone ?? "ok";
  const icon = TONE_ICON[tone];

  return (
    <div className={cn("toast", leaving && "out")} role="status">
      <Ic name={icon.name} size={15} className={icon.className} />
      <span className="grow">
        {item.title}
        {item.body ? <span className="toast-body"> {item.body}</span> : null}
      </span>
      {item.action ? (
        <button
          type="button"
          className="ta"
          onClick={() => {
            item.action?.onClick?.();
            onDone(item.id);
          }}
        >
          {item.action.label}
        </button>
      ) : null}
      <IconButton
        icon="x"
        size="xs"
        label="Dismiss"
        style={{ color: "inherit", opacity: 0.6 }}
        onClick={() => onDone(item.id)}
      />
    </div>
  );
}

export function ToastProvider({ children }: { children?: ReactNode }) {
  const [toasts, setToasts] = useState<ToastItem[]>([]);

  const show = useCallback((options: ToastOptions) => {
    toastSeq += 1;
    setToasts((prev) => [...prev, { ...options, id: toastSeq }]);
  }, []);

  const remove = useCallback((id: number) => {
    setToasts((prev) => prev.filter((toast) => toast.id !== id));
  }, []);

  return (
    <ToastContext.Provider value={{ show }}>
      {children}
      <div id="toasts">
        {toasts.map((toast) => (
          <Toast key={toast.id} item={toast} onDone={remove} />
        ))}
      </div>
    </ToastContext.Provider>
  );
}

export function useToast(): ToastContextValue {
  const context = useContext(ToastContext);
  if (!context) {
    throw new Error("useToast must be used within a ToastProvider");
  }
  return context;
}
