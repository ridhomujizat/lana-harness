import type { ReactNode } from "react";
import { LanaMark } from "../ui/icons";
import { cn } from "../ui/gr8r";

/* The Gr8r onboarding shell shared by the setup wizard and the Unlock step
   in views/login.tsx. Every step is a hash route under #/setup/{step};
   app/App.tsx owns the route table. */

export const STEP_TOTAL = 5;

/* "provider" is the rev 2 name of the connect step. */
const STEP_NUMBER: Record<string, number> = {
  unlock: 1,
  provider: 2,
  connect: 2,
  model: 3,
  agent: 4,
  persona: 4,
  ready: 5,
};

export function go(step: string): void {
  window.location.hash = "#/setup/" + step;
}

function setupAddress(): string {
  return window.location.host || "127.0.0.1:18300";
}

/* The step progress of the Gr8r onboarding card: one bar per step, the bars
   up to the current step filled, the current one growing in. */
function Steps({ step }: { step: string }) {
  const current = STEP_NUMBER[step] || 1;
  return (
    <>
      <div
        className="steps"
        role="progressbar"
        aria-valuemin={1}
        aria-valuemax={STEP_TOTAL}
        aria-valuenow={current}
        aria-label="Setup progress"
      >
        {Array.from({ length: STEP_TOTAL }, (_unused, index) => (
          <i
            key={index}
            className={cn(index + 1 <= current && "on", index + 1 === current && "cur")}
          ></i>
        ))}
      </div>
      <div className="steps-sub faint">
        Step {current} of {STEP_TOTAL}
      </div>
    </>
  );
}

export interface SetupShellProps {
  step: string;
  size?: string;
  variant?: "steps" | "door";
  children?: ReactNode;
}

/* The Gr8r auth/onboarding shell: a full-page column with the brand bar, a
   centred card, and a quiet footer. The door variant is the pre-login card
   (login, welcome, forgot); the steps variant is the setup wizard and adds the
   progress bars. */
export function SetupShell({
  step,
  size = "regular",
  variant = "steps",
  children,
}: SetupShellProps) {
  const door = variant === "door";
  return (
    <div className="auth">
      <div className="auth-top">
        <span className="brand">
          <LanaMark size={22} />
          <span className="lana-word">LΛNΛ</span>
        </span>
      </div>
      <div className="auth-c">
        <div
          className={cn(
            "auth-card",
            door ? "fx-auth" : "onb fx-step",
            size === "wide" && "onb-wide"
          )}
        >
          {door ? null : <Steps step={step} />}
          {children}
        </div>
      </div>
      <div className="auth-foot">
        <span className="faint">
          {door
            ? "lana on this computer · only this computer can open this page."
            : "Local only · " + setupAddress()}
        </span>
      </div>
    </div>
  );
}

export function StepHeading({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <>
      <h1>{title}</h1>
      <p className="sub">{children}</p>
    </>
  );
}
