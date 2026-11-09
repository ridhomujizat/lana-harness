import { useEffect, useState } from "react";
import type { Graph, MessageChannel, NodeRun, ScriptSuggestion, WfNode } from "../api/workflows";
import { getRun, getWorkflowChannels, listRuns } from "../api/workflows";
import { errorMessage } from "../api/client";
import { Ic } from "../ui/icons";
import {
  Button,
  Checkbox,
  Field,
  Input,
  Select,
  Tabs,
  Textarea,
  Toggle,
} from "../ui/gr8r";
import { messageChannelIds, nodeMeta } from "./workflow-catalog";
import type { FieldSpec } from "./workflow-catalog";
import { prettyValue } from "./workflow-common";
import { agentOptionIcon } from "./agent-avatar";
import { ConvertBanner, ConvertDialog, ScriptTab } from "./workflow-script";

/* The node inspector of design workflows-2: Parameters, Last output and On
   failure. Edits go straight into the draft graph; the editor saves. */

interface InspectorProps {
  workflowId: string;
  graph: Graph;
  node: WfNode;
  agents: string[];
  /* Spec #44: set when the daemon suggests converting this agent step. */
  suggestion?: ScriptSuggestion;
  onDismissSuggestion?: () => void;
  onChange: (node: WfNode) => void;
  onDelete: () => void;
  onClose: () => void;
}

function JsonField({
  value,
  onChange,
  id,
}: {
  value: unknown;
  onChange: (value: unknown) => void;
  id: string;
}) {
  const [text, setText] = useState(() =>
    value === undefined || value === null || value === "" ? "" : prettyValue(value)
  );
  const [bad, setBad] = useState(false);
  return (
    <>
      <Textarea
        id={id}
        className="mono wf-code"
        rows={5}
        value={text}
        invalid={bad}
        onChange={(event) => {
          const next = event.target.value;
          setText(next);
          if (!next.trim()) {
            setBad(false);
            onChange(undefined);
            return;
          }
          try {
            onChange(JSON.parse(next));
            setBad(false);
          } catch {
            setBad(true);
          }
        }}
      />
      {bad ? <span className="wf-field-err">Not valid JSON yet.</span> : null}
    </>
  );
}

/* A value that is JSON when it parses (arrays, numbers, booleans, objects) and
   a plain string otherwise, so both ["a","b"] and {{ state.x }} work. */
function ValueField({
  value,
  onChange,
  id,
  placeholder,
}: {
  value: unknown;
  onChange: (value: unknown) => void;
  id: string;
  placeholder?: string;
}) {
  const [text, setText] = useState(() =>
    value === undefined || value === null ? "" : typeof value === "string" ? value : JSON.stringify(value)
  );
  return (
    <Input
      id={id}
      className="mono"
      value={text}
      placeholder={placeholder}
      onChange={(event) => {
        const next = event.target.value;
        setText(next);
        const trimmed = next.trim();
        if (/^[[{]|^-?\d|^(true|false|null)$/.test(trimmed)) {
          try {
            onChange(JSON.parse(trimmed));
            return;
          } catch {
            /* Not JSON yet: keep the text as a string. */
          }
        }
        onChange(next);
      }}
    />
  );
}

function ToolsField({
  value,
  onChange,
}: {
  value: unknown;
  onChange: (value: string[]) => void;
}) {
  const tools = Array.isArray(value) ? value.map(String) : [];
  const [draft, setDraft] = useState("");
  const add = () => {
    const name = draft.trim();
    if (name && !tools.includes(name)) {
      onChange([...tools, name]);
    }
    setDraft("");
  };
  return (
    <div className="wf-tools">
      {tools.map((tool) => (
        <span className="wf-tool" key={tool}>
          {tool}
          <button
            type="button"
            aria-label={"Remove " + tool}
            onClick={() => onChange(tools.filter((item) => item !== tool))}
          >
            <Ic name="x" size={11} />
          </button>
        </span>
      ))}
      <input
        className="wf-tool-add"
        value={draft}
        placeholder="+ Add tool"
        aria-label="Add tool"
        onChange={(event) => setDraft(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === "Enter") {
            event.preventDefault();
            add();
          }
        }}
        onBlur={add}
      />
    </div>
  );
}

/* The Message node's "Send to" checklist (spec #33). The channel list is read
   once from the endpoint; an old node without a channels param starts on the
   run-time default (telegram) and the first change writes the explicit list.
   A selected channel that needs an agent reveals the agent picker, and at
   least one channel must stay selected. */
function MessageChannelsField({
  node,
  agents,
  set,
}: {
  node: WfNode;
  agents: string[];
  set: (name: string, value: unknown) => void;
}) {
  const [channels, setChannels] = useState<MessageChannel[] | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    let live = true;
    getWorkflowChannels()
      .then((list) => {
        if (live) {
          setChannels(list);
        }
      })
      .catch((failure: unknown) => {
        if (live) {
          setError(errorMessage(failure));
        }
      });
    return () => {
      live = false;
    };
  }, []);

  const selected = messageChannelIds(node.params);
  const agent = typeof node.params.agent === "string" ? node.params.agent : "";
  const rows = channels || [];
  const needsAgent = rows.filter(
    (channel) => selected.includes(channel.id) && channel.needs_agent
  );

  const toggle = (id: string, checked: boolean) => {
    let next = selected.filter((value) => value !== id);
    if (checked) {
      next = next.concat([id]);
    }
    if (next.length === 0) {
      return;
    }
    const order = rows.map((channel) => channel.id);
    next.sort((left, right) => order.indexOf(left) - order.indexOf(right));
    set("channels", next);
  };

  return (
    <>
      <Field label="Send to">
        <div className="wf-channels">
          {rows.map((channel) => {
            const checked = selected.includes(channel.id);
            const disabled =
              (!channel.available && !checked) ||
              (checked && selected.length === 1);
            return (
              <div className="wf-channel" key={channel.id}>
                <label className="wf-channel-row">
                  <Checkbox
                    checked={checked}
                    disabled={disabled}
                    onChange={(nextChecked) => toggle(channel.id, nextChecked)}
                  />
                  <span>{channel.label}</span>
                </label>
                {!channel.available && channel.reason ? (
                  <span className="wf-channel-why muted">{channel.reason}</span>
                ) : null}
              </div>
            );
          })}
          {error ? <span className="wf-field-err">{error}</span> : null}
        </div>
      </Field>
      {needsAgent.length > 0 ? (
        <Field
          label="Agent"
          hint="The channel posts as this agent."
          error={agent ? undefined : "Pick an agent for " + needsAgent[0]!.label + "."}
          htmlFor={"wf-f-" + node.id + "-agent"}
        >
          <Select
            id={"wf-f-" + node.id + "-agent"}
            value={agent}
            placeholder="Pick a bot"
            options={agents.map((name) => ({ value: name, label: name, icon: agentOptionIcon(name) }))}
            onChange={(next) => set("agent", next || undefined)}
          />
        </Field>
      ) : null}
    </>
  );
}

function ParamField({
  spec,
  node,
  agents,
  set,
}: {
  spec: FieldSpec;
  node: WfNode;
  agents: string[];
  set: (name: string, value: unknown) => void;
}) {
  if (spec.kind === "channels") {
    return <MessageChannelsField node={node} agents={agents} set={set} />;
  }
  const id = "wf-f-" + node.id + "-" + spec.name;
  const value = node.params[spec.name];
  let control;
  switch (spec.kind) {
    case "textarea":
      control = (
        <Textarea
          id={id}
          className={spec.mono ? "mono" : undefined}
          rows={4}
          value={value === undefined || value === null ? "" : String(value)}
          onChange={(event) => set(spec.name, event.target.value)}
        />
      );
      break;
    case "number":
      control = (
        <Input
          id={id}
          type="number"
          value={value === undefined || value === null ? "" : String(value)}
          onChange={(event) =>
            set(spec.name, event.target.value === "" ? undefined : Number(event.target.value))
          }
        />
      );
      break;
    case "bool":
      return (
        <label className="wf-toggle-row" htmlFor={id}>
          <Toggle
            id={id}
            checked={value !== false}
            onChange={(checked) => set(spec.name, checked)}
          />
          <span>{spec.label}</span>
        </label>
      );
    case "select":
      control = (
        <Select
          id={id}
          value={value === undefined ? "" : String(value)}
          options={(spec.options || []).map((option) => ({ value: option, label: option }))}
          onChange={(next) => set(spec.name, next)}
        />
      );
      break;
    case "agent":
      control = (
        <Select
          id={id}
          value={value === undefined ? "" : String(value)}
          placeholder="Pick a bot"
          options={[
            ...(spec.required ? [] : [{ value: "", label: "—" }]),
            ...agents.map((agent) => ({ value: agent, label: agent, icon: agentOptionIcon(agent) })),
          ]}
          onChange={(next) => set(spec.name, next || undefined)}
        />
      );
      break;
    case "tools":
      control = <ToolsField value={value} onChange={(next) => set(spec.name, next)} />;
      break;
    case "json":
      control = (
        <JsonField
          key={node.id + spec.name}
          id={id}
          value={value}
          onChange={(next) => set(spec.name, next)}
        />
      );
      break;
    case "list":
      control = (
        <Input
          id={id}
          value={Array.isArray(value) ? value.join(", ") : ""}
          placeholder="bug, question"
          onChange={(event) =>
            set(
              spec.name,
              event.target.value
                .split(",")
                .map((part) => part.trim())
                .filter(Boolean)
            )
          }
        />
      );
      break;
    case "value":
      control = (
        <ValueField
          key={node.id + spec.name}
          id={id}
          value={value}
          placeholder={spec.placeholder}
          onChange={(next) => set(spec.name, next)}
        />
      );
      break;
    default:
      control = (
        <Input
          id={id}
          className={spec.mono ? "mono" : undefined}
          value={value === undefined || value === null ? "" : typeof value === "string" ? value : JSON.stringify(value)}
          placeholder={spec.placeholder}
          onChange={(event) => set(spec.name, event.target.value)}
        />
      );
  }
  return (
    <Field label={spec.label + (spec.required ? "" : "")} hint={spec.help} htmlFor={id}>
      {control}
    </Field>
  );
}

function LastOutput({ workflowId, nodeId }: { workflowId: string; nodeId: string }) {
  const [rows, setRows] = useState<NodeRun[] | null>(null);
  const [runNumber, setRunNumber] = useState(0);
  useEffect(() => {
    let live = true;
    listRuns(workflowId, { limit: 5 })
      .then(async (response) => {
        for (const run of response.runs) {
          const detail = await getRun(workflowId, run.id);
          const mine = detail.nodes.filter((row) => row.node_id === nodeId);
          if (mine.length > 0) {
            if (live) {
              setRows(mine);
              setRunNumber(run.number);
            }
            return;
          }
        }
        if (live) {
          setRows([]);
        }
      })
      .catch(() => live && setRows([]));
    return () => {
      live = false;
    };
  }, [workflowId, nodeId]);
  if (rows === null) {
    return <p className="muted">Loading…</p>;
  }
  if (rows.length === 0) {
    return <p className="muted">This node has not run yet.</p>;
  }
  return (
    <div className="wf-lastout">
      <p className="muted">From run #{runNumber}.</p>
      {rows.slice(0, 5).map((row) => (
        <div key={row.item_index}>
          {row.item_index >= 0 ? <h4>Item {row.item_index + 1}</h4> : null}
          <pre className="wf-json">{prettyValue(row.output ?? row.error)}</pre>
        </div>
      ))}
    </div>
  );
}

const FAILURE_OPTIONS = [
  { value: "default", label: "Workflow default" },
  { value: "guardian", label: "Retry, then guardian decides" },
  { value: "stop", label: "Retry, then stop" },
  { value: "continue", label: "Retry, then continue without it" },
];

export function NodeInspector({
  workflowId,
  graph,
  node,
  agents,
  suggestion,
  onDismissSuggestion,
  onChange,
  onDelete,
  onClose,
}: InspectorProps) {
  const [tab, setTab] = useState("params");
  const [converting, setConverting] = useState(false);
  const isScript = node.type === "script_parse";
  const meta = nodeMeta(node.type);
  const set = (name: string, value: unknown) => {
    const params = { ...node.params };
    if (value === undefined) {
      delete params[name];
    } else {
      params[name] = value;
    }
    onChange({ ...node, params });
  };
  const loops = graph.nodes.filter((item) => item.type === "loop" && item.id !== node.id);
  const policy = node.on_failure || {};
  return (
    <div className="wf-insp">
      <header className="wf-insp-h">
        <span className={"wf-ico tint-" + meta.tint}>
          <Ic name={meta.icon} size={18} />
        </span>
        <div className="wf-insp-title">
          <b className="trunc">{node.name || meta.label}</b>
          <span className="muted trunc">
            {meta.label} · id {node.id}
          </span>
        </div>
        <button type="button" className="wf-x" aria-label="Close" onClick={onClose}>
          <Ic name="x" size={16} />
        </button>
      </header>
      <Tabs
        className="wf-insp-tabs"
        value={tab}
        onChange={setTab}
        items={[
          { value: "params", label: "Parameters" },
          ...(isScript ? [{ value: "script", label: "Script" }] : []),
          { value: "output", label: "Last output" },
          { value: "failure", label: "On failure" },
        ]}
      />
      <div className="wf-insp-b">
        {tab === "params" ? (
          <>
            {suggestion && !isScript ? (
              <ConvertBanner
                suggestion={suggestion}
                onConvert={() => setConverting(true)}
                onDismiss={() => onDismissSuggestion?.()}
              />
            ) : null}
            <Field label="Name" htmlFor={"wf-name-" + node.id}>
              <Input
                id={"wf-name-" + node.id}
                value={node.name}
                onChange={(event) => onChange({ ...node, name: event.target.value })}
              />
            </Field>
            {meta.fields.map((spec) => (
              <ParamField key={spec.name} spec={spec} node={node} agents={agents} set={set} />
            ))}
            {node.type !== "loop" && !node.type.startsWith("trigger.") && loops.length > 0 ? (
              <Field label="Inside loop" hint="Body nodes run once per loop item." htmlFor={"wf-parent-" + node.id}>
                <Select
                  id={"wf-parent-" + node.id}
                  value={node.parent || ""}
                  options={[
                    { value: "", label: "Not in a loop" },
                    ...loops.map((loop) => ({ value: loop.id, label: loop.name || loop.id })),
                  ]}
                  onChange={(next) => onChange({ ...node, parent: next || undefined })}
                />
              </Field>
            ) : null}
            <label className="wf-toggle-row">
              <Toggle
                checked={!node.disabled}
                onChange={(checked) => onChange({ ...node, disabled: !checked || undefined })}
              />
              <span>Node enabled</span>
            </label>
          </>
        ) : null}
        {tab === "script" && isScript ? (
          <ScriptTab workflowId={workflowId} node={node} onChange={onChange} />
        ) : null}
        {tab === "output" ? <LastOutput workflowId={workflowId} nodeId={node.id} /> : null}
        {tab === "failure" ? (
          <>
            <Field label="If this node fails" htmlFor={"wf-fail-" + node.id}>
              <Select
                id={"wf-fail-" + node.id}
                value={policy.mode || "default"}
                options={FAILURE_OPTIONS}
                onChange={(mode) =>
                  onChange({
                    ...node,
                    on_failure: mode === "default" && policy.retries === undefined ? undefined : { ...policy, mode },
                  })
                }
              />
            </Field>
            <Field
              label="Retries"
              hint="Only timeouts, rate limits, 5xx and network errors are retried. Approval nodes never retry."
              htmlFor={"wf-retries-" + node.id}
            >
              <Input
                id={"wf-retries-" + node.id}
                type="number"
                min={0}
                max={5}
                value={policy.retries === undefined ? "" : String(policy.retries)}
                placeholder="workflow default"
                onChange={(event) =>
                  onChange({
                    ...node,
                    on_failure: {
                      ...policy,
                      retries: event.target.value === "" ? undefined : Number(event.target.value),
                    },
                  })
                }
              />
            </Field>
            <Field
              label="Fallback output"
              hint="The guardian can use this JSON instead of the node output."
              htmlFor={"wf-fb-" + node.id}
            >
              <JsonField
                key={node.id + "fallback"}
                id={"wf-fb-" + node.id}
                value={policy.fallback}
                onChange={(fallback) => onChange({ ...node, on_failure: { ...policy, fallback } })}
              />
            </Field>
          </>
        ) : null}
      </div>
      {converting ? (
        <ConvertDialog
          workflowId={workflowId}
          node={node}
          onClose={() => setConverting(false)}
          onReplace={(next) => {
            setConverting(false);
            onChange(next);
            setTab("script");
          }}
        />
      ) : null}
      <footer className="wf-insp-f">
        <Button variant="danger-ghost" icon="trash-2" onClick={onDelete}>
          Delete node
        </Button>
      </footer>
    </div>
  );
}
