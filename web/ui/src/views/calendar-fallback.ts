import type { Entry, EntryWorkflow } from "../api/calendar";
import type { Run, Workflow } from "../api/workflows";
import { nextCron } from "./cron-next";
import { addDays, dayKey, startOfDay } from "./calendar-model";

/* Workflow entries built in the browser (design "Fallback"): used when the
   daemon has no calendar backend yet, and by the preview fixtures. Follows
   REQ-CAL-008: schedule slots in the future part of the range, interval
   slots when the interval is 60 min or more, else one all-day row per day,
   and one entry per run that started in the range. */

const MIN = 60000;
const SLOT_LIMIT = 600;

function blank(): Omit<Entry, "key" | "source" | "title" | "start"> {
  return {
    kind: "workflow",
    end: 0,
    all_day: false,
    date: "",
    end_date: "",
    color: "indigo",
    status: "",
    moved: false,
    occurrence: 0,
    item_id: "",
    repeating: true,
    assignee: "",
    created_by: "",
    action: null,
    firing: null,
    habit: null,
    workflow: null,
    google: null,
    readonly: false,
  };
}

function wfInfo(workflow: Workflow, nodeId: string, trigger: EntryWorkflow["trigger"], every = 0): EntryWorkflow {
  return {
    id: workflow.id,
    name: workflow.name,
    guardian: workflow.guardian,
    node_id: nodeId,
    trigger,
    every_minutes: every,
    run_number: 0,
    run_status: "",
  };
}

export interface SlotOverride {
  op: "move" | "skip";
  to: number;
}

export function workflowEntries(
  workflows: Workflow[],
  runs: Run[],
  from: number,
  to: number,
  now: number,
  overrides: Map<string, SlotOverride> = new Map()
): Entry[] {
  const out: Entry[] = [];
  for (const workflow of workflows) {
    if (workflow.status !== "active") {
      continue;
    }
    for (const node of workflow.graph.nodes) {
      if (node.params.enabled === false) {
        continue;
      }
      const ref = workflow.id + "/" + node.id;
      const push = (at: number, trigger: EntryWorkflow["trigger"]) => {
        const override = overrides.get(ref + "@" + at);
        const start = override && override.op === "move" ? override.to : at;
        out.push({
          ...blank(),
          key: "wf:" + ref + ":" + at,
          source: "workflow_slot",
          title: workflow.name,
          start,
          date: dayKey(new Date(start)),
          end_date: dayKey(new Date(start)),
          status: override && override.op === "skip" ? "skipped" : "scheduled",
          moved: Boolean(override && override.op === "move"),
          occurrence: at,
          workflow: wfInfo(workflow, node.id, trigger),
        });
      };
      if (node.type === "trigger.schedule") {
        const cron = String(node.params.cron || "");
        const zone = String(node.params.timezone || "");
        let t = Math.max(from, now) - 1;
        for (let count = 0; count < SLOT_LIMIT; count += 1) {
          t = nextCron(cron, zone, t);
          if (!t || t >= to) {
            break;
          }
          push(t, "schedule");
        }
      }
      if (node.type === "trigger.interval") {
        const minutes = Math.max(1, Number(node.params.minutes) || 1);
        if (minutes < 60) {
          for (let day = startOfDay(new Date(Math.max(from, now))); day.getTime() < to; day = addDays(day, 1)) {
            const key = dayKey(day);
            out.push({
              ...blank(),
              key: "wf:" + ref + ":" + key,
              source: "workflow_slot",
              title: workflow.name,
              start: day.getTime(),
              all_day: true,
              date: key,
              end_date: key,
              status: "scheduled",
              occurrence: day.getTime(),
              readonly: true,
              workflow: wfInfo(workflow, node.id, "interval", minutes),
            });
          }
          continue;
        }
        const last = runs.find((run) => run.workflow_id === workflow.id && run.trigger === "interval");
        let t = last ? last.started_at + minutes * MIN : now;
        while (t < Math.max(from, now)) {
          t += minutes * MIN;
        }
        for (let count = 0; t < to && count < SLOT_LIMIT; count += 1, t += minutes * MIN) {
          push(Math.floor(t / MIN) * MIN, "interval");
        }
      }
    }
  }
  const names = new Map(workflows.map((workflow) => [workflow.id, workflow]));
  for (const run of runs) {
    if (run.started_at < from || run.started_at >= to) {
      continue;
    }
    const workflow = names.get(run.workflow_id);
    out.push({
      ...blank(),
      key: "run:" + run.id,
      source: "workflow_run",
      title: workflow ? workflow.name : run.workflow_id,
      start: run.started_at,
      end: run.ended_at > run.started_at ? run.ended_at : 0,
      date: dayKey(new Date(run.started_at)),
      end_date: dayKey(new Date(run.started_at)),
      status: run.status,
      occurrence: run.started_at,
      repeating: false,
      readonly: true,
      color: run.status === "failed" ? "rose" : run.status === "succeeded" ? "green" : "amber",
      workflow: {
        id: run.workflow_id,
        name: workflow ? workflow.name : run.workflow_id,
        guardian: workflow ? workflow.guardian : "",
        node_id: "",
        trigger: "schedule",
        every_minutes: 0,
        run_number: run.number,
        run_status: run.status,
      },
    });
  }
  return out;
}
