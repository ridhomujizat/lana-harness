import { useCallback, useEffect, useState } from "react";
import type { CSSProperties } from "react";
import {
  connectGoogle,
  disconnectGoogle,
  getGoogle,
  getSettings,
  putGoogle,
  putGoogleClient,
  putSettings,
  rotateICS,
  syncGoogle,
} from "../api/calendar";
import type { CalendarSettings, GoogleLink, GoogleState } from "../api/calendar";
import { errorMessage } from "../api/client";
import { Ic } from "../ui/icons";
import { Badge, Button, Checkbox, Drawer, Field, Input, Radio, Seg, Toggle, useToast } from "../ui/gr8r";
import type { BadgeTone } from "../ui/gr8r";
import { ago } from "./workflow-common";

/* Calendar settings drawer (spec #35 API-CAL-009..014): time zone, week start,
   daily Telegram agenda, the ICS feed and the Google Calendar link. */

const GOOGLE_WORDS: Record<GoogleState, [string, BadgeTone]> = {
  off: ["Not linked", "gray"],
  linked: ["Linked", "green"],
  syncing: ["Syncing", "accent"],
  needs_reauth: ["Sign in again", "amber"],
  error: ["Sync error", "red"],
};

export function CalendarSettingsDrawer({ onClose, onChanged }: { onClose: () => void; onChanged: () => void }) {
  const toast = useToast();
  const [settings, setSettings] = useState<CalendarSettings | null>(null);
  const [google, setGoogle] = useState<GoogleLink | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState("");
  const [clientId, setClientId] = useState("");
  const [clientSecret, setClientSecret] = useState("");

  const load = useCallback(async () => {
    try {
      const [s, g] = await Promise.all([getSettings(), getGoogle()]);
      setSettings(s);
      setGoogle(g);
      setError("");
    } catch (failure) {
      setError(errorMessage(failure));
    }
  }, []);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- the first update happens after the await
    void load();
  }, [load]);

  async function act(name: string, run: () => Promise<unknown>, done?: string) {
    setBusy(name);
    try {
      await run();
      if (done) {
        toast.show({ title: done, tone: "ok" });
      }
      await load();
      onChanged();
    } catch (failure) {
      toast.show({ title: "Could not save", body: errorMessage(failure), tone: "error" });
    } finally {
      setBusy("");
    }
  }

  function saveGeneral(next: CalendarSettings) {
    setSettings(next);
    void act("general", () => putSettings({ timezone: next.timezone, week_start: next.week_start, agenda: next.agenda }));
  }

  const browserZone = Intl.DateTimeFormat().resolvedOptions().timeZone;

  return (
    <Drawer
      title="Calendar settings"
      onClose={onClose}
      className="csettings"
      header={
        <span className="row" style={{ gap: 8 }}>
          <Ic name="settings" size={15} />
          <b>Calendar settings</b>
        </span>
      }
    >
      {error ? <p className="cform-err">{error}</p> : null}
      {!settings && !error ? <p className="muted">Loading…</p> : null}
      {settings ? (
        <section className="cset-sec">
          <h3 className="cset-h">General</h3>
          <Field
            label="Time zone"
            hint={settings.timezone !== browserZone ? "This browser is on " + browserZone + "; times show in browser time." : "Repeats and the agenda use this zone."}
          >
            <Input
              defaultValue={settings.timezone}
              key={settings.timezone}
              onBlur={(event) => {
                const value = event.target.value.trim();
                if (value && value !== settings.timezone) {
                  saveGeneral({ ...settings, timezone: value });
                }
              }}
            />
          </Field>
          <Field label="Week starts on">
            <Seg
              value={String(settings.week_start)}
              onChange={(value) => saveGeneral({ ...settings, week_start: Number(value) })}
              options={[
                { value: "1", label: "Monday" },
                { value: "0", label: "Sunday" },
              ]}
            />
          </Field>
          <div className="cset-line">
            <div className="grow">
              <b>Daily agenda on Telegram</b>
              <p className="muted">Today&apos;s plan, habits and anything missed yesterday. Also on /agenda.</p>
            </div>
            <Input
              type="time"
              className="cset-time"
              aria-label="Agenda time"
              value={settings.agenda.time}
              disabled={!settings.agenda.enabled}
              onChange={(event) => saveGeneral({ ...settings, agenda: { ...settings.agenda, time: event.target.value } })}
            />
            <Toggle
              aria-label="Daily agenda"
              checked={settings.agenda.enabled}
              onChange={(value) => saveGeneral({ ...settings, agenda: { ...settings.agenda, enabled: value } })}
            />
          </div>
        </section>
      ) : null}

      {settings ? (
        <section className="cset-sec">
          <h3 className="cset-h">Subscribe (ICS)</h3>
          <p className="muted">Read-only feed for Apple or Google Calendar. Anyone with the link can read it.</p>
          <div className="row" style={{ gap: 6 }}>
            <Input readOnly value={settings.ics.url} className="mono grow" aria-label="ICS feed URL" onFocus={(event) => event.target.select()} />
            <Button
              size="sm"
              icon="copy"
              onClick={() => {
                void navigator.clipboard?.writeText(settings.ics.url);
                toast.show({ title: "Feed link copied", tone: "ok" });
              }}
            >
              Copy
            </Button>
            <Button size="sm" variant="ghost" icon="refresh-cw" loading={busy === "ics"} onClick={() => void act("ics", rotateICS, "New link made. The old one stopped working.")}>
              New link
            </Button>
          </div>
        </section>
      ) : null}

      {google ? (
        <section className="cset-sec">
          <div className="row" style={{ gap: 8 }}>
            <h3 className="cset-h grow">Google Calendar</h3>
            <Badge tone={GOOGLE_WORDS[google.state][1]} dot>
              {GOOGLE_WORDS[google.state][0]}
            </Badge>
          </div>
          <p className="muted">
            Two-way: Google events show here, and events you or your agents add go to the target calendar.
          </p>
          {!google.client_configured ? (
            <div className="cset-box">
              <p className="muted" style={{ marginTop: 0 }}>
                Create an OAuth client (type Desktop) in Google Cloud Console, enable the Calendar API, then paste it here.
                It is kept in lana&apos;s secret store.
              </p>
              <Field label="Client ID">
                <Input value={clientId} onChange={(event) => setClientId(event.target.value)} />
              </Field>
              <Field label="Client secret">
                <Input type="password" value={clientSecret} onChange={(event) => setClientSecret(event.target.value)} />
              </Field>
              <Button
                variant="primary"
                size="sm"
                disabled={!clientId.trim() || !clientSecret.trim()}
                loading={busy === "client"}
                onClick={() => void act("client", () => putGoogleClient(clientId.trim(), clientSecret.trim()), "Google client saved")}
              >
                Save client
              </Button>
            </div>
          ) : null}
          {google.client_configured && (google.state === "off" || google.state === "needs_reauth") ? (
            <Button
              variant="primary"
              icon="log-in"
              loading={busy === "connect"}
              onClick={() =>
                void act("connect", async () => {
                  const { auth_url } = await connectGoogle();
                  window.location.href = auth_url;
                })
              }
            >
              {google.state === "needs_reauth" ? "Sign in to Google again" : "Connect Google Calendar"}
            </Button>
          ) : null}
          {google.error ? <p className="cform-err">{google.error}</p> : null}
          {google.state !== "off" && google.calendars.length > 0 ? (
            <>
              <p className="cset-acc">
                <Ic name="user" size={13} /> {google.account}
                {google.last_sync ? <span className="muted"> · synced {ago(google.last_sync)}</span> : null}
              </p>
              <div className="cset-cals" role="table" aria-label="Google calendars">
                <div className="cset-cal cset-cal-h" role="row">
                  <span>Show</span>
                  <span>Calendar</span>
                  <span>New events go here</span>
                </div>
                {google.calendars.map((cal) => (
                  <div className="cset-cal" role="row" key={cal.id}>
                    <Checkbox
                      aria-label={"Show " + cal.name}
                      checked={cal.selected}
                      onChange={(value) =>
                        void act("cal", () =>
                          putGoogle(
                            google.calendars.map((c) => ({ id: c.id, selected: c.id === cal.id ? value : c.selected })),
                            google.target
                          )
                        )
                      }
                    />
                    <span className="row trunc" style={{ gap: 6 }}>
                      <span className="pdot" style={{ "--c": cal.color } as CSSProperties} />
                      <span className="trunc">{cal.name}</span>
                      {!cal.writable ? <Badge tone="gray">Read only</Badge> : null}
                    </span>
                    <Radio
                      aria-label={"Target " + cal.name}
                      name="gcal-target"
                      checked={google.target === cal.id}
                      disabled={!cal.writable || !cal.selected}
                      onChange={() =>
                        void act("cal", () =>
                          putGoogle(
                            google.calendars.map((c) => ({ id: c.id, selected: c.selected })),
                            cal.id
                          )
                        )
                      }
                    />
                  </div>
                ))}
              </div>
              <div className="row" style={{ gap: 6 }}>
                <Button size="sm" icon="refresh-cw" loading={busy === "sync"} onClick={() => void act("sync", syncGoogle, "Sync started")}>
                  Sync now
                </Button>
                <Button size="sm" variant="danger-ghost" icon="unlink" loading={busy === "disconnect"} onClick={() => void act("disconnect", disconnectGoogle, "Google Calendar disconnected")}>
                  Disconnect
                </Button>
              </div>
            </>
          ) : null}
        </section>
      ) : null}
    </Drawer>
  );
}
