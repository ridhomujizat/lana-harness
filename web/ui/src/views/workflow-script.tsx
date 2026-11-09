import { useCallback, useEffect, useRef, useState } from "react";
import type {
  Decision,
  ScriptConvertResult,
  ScriptGenerateResult,
  ScriptProblem,
  ScriptSample,
  ScriptStat,
  ScriptSuggestion,
  ScriptTestResult,
  WfNode,
} from "../api/workflows";
import {
  answerDecision,
  convertNode,
  generateScript,
  getWorkflow,
  scriptSuggestions,
  testScript,
} from "../api/workflows";
import { Badge, Button, Field, Input, Modal, Select, Textarea, useToast } from "../ui/gr8r";
import { Ic } from "../ui/icons";
import { ago, prettyValue, tokens, useWorkflowStream } from "./workflow-common";

/* Spec #44 Script Parse UI: the Script tab of a script_parse node, the
   "Convert to Script Parse" banner of agent steps, and the convert preview. */

const DISMISS_KEY = "lana.wf.script-suggest.dismissed";

function errorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function readDismissed(): string[] {
  try {
    const raw = window.localStorage.getItem(DISMISS_KEY);
    const list = raw ? JSON.parse(raw) : [];
    return Array.isArray(list) ? list.filter((item) => typeof item === "string") : [];
  } catch {
    return [];
  }
}

function writeDismissed(list: string[]) {
  try {
    window.localStorage.setItem(DISMISS_KEY, JSON.stringify(list.slice(-200)));
  } catch {
    /* storage may be blocked; dismissal then lasts for this page only */
  }
}

/* useScriptSuggestions loads the convert suggestions of a workflow (API-SP-005)
   minus the ones this browser dismissed. */
export function useScriptSuggestions(workflowId: string) {
  const [rows, setRows] = useState<ScriptSuggestion[]>([]);
  const [dismissed, setDismissed] = useState<string[]>(() => readDismissed());
  const load = useCallback(() => {
    if (!workflowId) {
      return;
    }
    scriptSuggestions(workflowId)
      .then((response) => setRows(response.suggestions || []))
      .catch(() => setRows([]));
  }, [workflowId]);
  useEffect(load, [load]);
  useWorkflowStream((change) => {
    if (change.workflow_id === workflowId && !change.run_id) {
      load();
    }
  });
  const dismiss = (nodeId: string) => {
    const next = [...dismissed, workflowId + "/" + nodeId];
    setDismissed(next);
    writeDismissed(next);
  };
  const visible = rows.filter((row) => !dismissed.includes(workflowId + "/" + row.node_id));
  return { suggestions: visible, dismiss };
}

function Problems({ problems }: { problems: ScriptProblem[] }) {
  if (!problems || problems.length === 0) {
    return null;
  }
  return (
    <ul className="wf-script-problems">
      {problems.map((problem, index) => (
        <li key={index}>
          <code>{problem.code}</code> {problem.message}
        </li>
      ))}
    </ul>
  );
}

type SampleMode = "latest" | "run" | "paste";

function SamplePicker({
  id,
  mode,
  setMode,
  run,
  setRun,
  pasted,
  setPasted,
}: {
  id: string;
  mode: SampleMode;
  setMode: (mode: SampleMode) => void;
  run: string;
  setRun: (run: string) => void;
  pasted: string;
  setPasted: (text: string) => void;
}) {
  return (
    <div className="wf-script-sample">
      <Field label="Sample input" htmlFor={id + "-mode"}>
        <Select
          id={id + "-mode"}
          value={mode}
          options={[
            { value: "latest", label: "Latest run" },
            { value: "run", label: "A chosen run" },
            { value: "paste", label: "Paste a sample" },
          ]}
          onChange={(next) => setMode(next as SampleMode)}
        />
      </Field>
      {mode === "run" ? (
        <Field label="Run number" htmlFor={id + "-run"}>
          <Input
            id={id + "-run"}
            type="number"
            min={1}
            value={run}
            onChange={(event) => setRun(event.target.value)}
          />
        </Field>
      ) : null}
      {mode === "paste" ? (
        <Field label="Sample" hint="Text, or JSON when it parses as JSON." htmlFor={id + "-paste"}>
          <Textarea
            id={id + "-paste"}
            className="mono wf-code"
            rows={5}
            value={pasted}
            onChange={(event) => setPasted(event.target.value)}
          />
        </Field>
      ) : null}
    </div>
  );
}

function sampleOf(mode: SampleMode, run: string, pasted: string): ScriptSample {
  if (mode === "run" && Number(run) > 0) {
    return { run: Number(run) };
  }
  if (mode === "paste" && pasted.trim()) {
    try {
      return { input: JSON.parse(pasted) };
    } catch {
      return { input: pasted };
    }
  }
  return {};
}

/* Spec #45 drift codes, in owner words. */
const DRIFT_LABELS: Record<string, string> = {
  script_error: "script error",
  schema_drift: "wrong shape",
  empty_drift: "empty result",
  item_drift: "wrong item shape",
  no_script: "no script yet",
  profile_drift: "values look off",
  rule_drift: "rule broken",
  audit_drift: "audit mismatch",
};

export function driftLabel(code: string): string {
  return DRIFT_LABELS[code] || code;
}

export function eventLabel(event: Decision): string {
  const fix = event.fix || {};
  const drift = typeof fix.drift === "string" ? driftLabel(fix.drift) : "";
  switch (event.action) {
    case "script_update":
      return "Script updated to rev " + String(fix.rev_to ?? "?") + (drift ? " · " + drift : "");
    case "profile_accept":
      return "Data changed, script confirmed";
    case "rule_drop":
      return "Rule dropped: real data broke it";
    default:
      return "Agent fallback" + (drift ? " · " + drift : "");
  }
}

function eventIcon(action: string): string {
  switch (action) {
    case "script_update":
      return "refresh-cw";
    case "profile_accept":
      return "check";
    case "rule_drop":
      return "eraser";
    default:
      return "sparkles";
  }
}

function StatsCard({ stat }: { stat?: ScriptStat }) {
  if (!stat) {
    return <p className="muted">No runs yet. The first run learns the script when it is empty.</p>;
  }
  return (
    <div className="wf-script-stats">
      <div>
        <b>{stat.script_runs}</b>
        <span className="muted">script runs</span>
      </div>
      <div>
        <b>{stat.fallback_runs}</b>
        <span className="muted">agent fallbacks</span>
      </div>
      <div>
        <b>{tokens(stat.tokens_saved)}</b>
        <span className="muted">tokens saved</span>
      </div>
      {stat.audit_runs ? (
        <div>
          <b>{stat.audit_runs}</b>
          <span className="muted">audits</span>
        </div>
      ) : null}
      <div>
        <b>{stat.last_update ? ago(stat.last_update) : "—"}</b>
        <span className="muted">last update</span>
      </div>
    </div>
  );
}

/* ScriptTab is the Script tab of a script_parse node: the code, Generate and
   Test on a sample, the stats and the script history with Revert. */
export function ScriptTab({
  workflowId,
  node,
  onChange,
}: {
  workflowId: string;
  node: WfNode;
  onChange: (node: WfNode) => void;
}) {
  const toast = useToast();
  const [mode, setMode] = useState<SampleMode>("latest");
  const [run, setRun] = useState("");
  const [pasted, setPasted] = useState("");
  const [busy, setBusy] = useState<"" | "test" | "generate">("");
  const [tested, setTested] = useState<ScriptTestResult | null>(null);
  const [generated, setGenerated] = useState<ScriptGenerateResult | null>(null);
  const [error, setError] = useState("");
  const [stat, setStat] = useState<ScriptStat | undefined>();
  const [savedRev] = useState(() => Number(node.params.script_rev) || 0);
  const script = typeof node.params.script === "string" ? node.params.script : "";

  const loadStats = useCallback(() => {
    getWorkflow(workflowId)
      .then((workflow) => setStat(workflow.script_stats?.[node.id]))
      .catch(() => setStat(undefined));
  }, [workflowId, node.id]);
  useEffect(loadStats, [loadStats]);
  /* Runs emit many changes; reload the stats at most once per 2 s. */
  const pending = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(pending.current), []);
  useWorkflowStream((change) => {
    if (change.workflow_id === workflowId && pending.current === undefined) {
      pending.current = window.setTimeout(() => {
        pending.current = undefined;
        loadStats();
      }, 2000);
    }
  });

  const setScript = (next: string) => {
    onChange({
      ...node,
      params: { ...node.params, script: next, script_rev: savedRev + 1 },
    });
  };

  /* Generate may return the schemas with the guardian's rules (spec #45). */
  const applyGenerated = (result: ScriptGenerateResult) => {
    const params: Record<string, unknown> = {
      ...node.params,
      script: result.script,
      script_rev: savedRev + 1,
    };
    if (result.output_schema) params.output_schema = result.output_schema;
    if (result.item_schema) params.item_schema = result.item_schema;
    onChange({ ...node, params });
  };

  const auditEvery = Number(node.params.audit_every) || 0;
  const setAuditEvery = (raw: string) => {
    const n = Math.max(0, Math.min(1000, Math.floor(Number(raw) || 0)));
    const params: Record<string, unknown> = { ...node.params };
    if (n > 0) params.audit_every = n;
    else delete params.audit_every;
    onChange({ ...node, params });
  };

  const runTest = async () => {
    setBusy("test");
    setError("");
    setGenerated(null);
    try {
      setTested(await testScript(workflowId, node, sampleOf(mode, run, pasted)));
    } catch (err) {
      setTested(null);
      setError(errorText(err));
    } finally {
      setBusy("");
    }
  };

  const runGenerate = async () => {
    setBusy("generate");
    setError("");
    setTested(null);
    try {
      setGenerated(await generateScript(workflowId, node, sampleOf(mode, run, pasted)));
    } catch (err) {
      setGenerated(null);
      setError(errorText(err));
    } finally {
      setBusy("");
    }
  };

  const revert = async (event: Decision) => {
    try {
      await answerDecision(workflowId, event.id, "revert");
      toast.show({ tone: "ok", title: "Script reverted", body: "Saved as a new workflow version." });
      loadStats();
    } catch (err) {
      toast.show({ tone: "error", title: "Revert failed", body: errorText(err) });
    }
  };

  return (
    <div className="wf-script">
      <StatsCard stat={stat} />
      <Field
        label="Script"
        hint="JavaScript: function parse(input, ctx) returns one object. Empty = the next run learns it."
        htmlFor={"wf-script-" + node.id}
      >
        <Textarea
          id={"wf-script-" + node.id}
          className="mono wf-code wf-script-code"
          rows={14}
          spellCheck={false}
          value={script}
          placeholder={"function parse(input, ctx) {\n  return { items: [] };\n}"}
          onChange={(event) => setScript(event.target.value)}
        />
      </Field>
      <Field
        label="Audit every N runs"
        hint="0 = off. Each audit also runs the agent fallback (one fallback of tokens) to catch wrong values that still look valid."
        htmlFor={"wf-audit-" + node.id}
      >
        <Input
          id={"wf-audit-" + node.id}
          type="number"
          min={0}
          max={1000}
          inputMode="numeric"
          value={auditEvery ? String(auditEvery) : "0"}
          onChange={(event) => setAuditEvery(event.target.value)}
        />
      </Field>
      <SamplePicker
        id={"wf-sample-" + node.id}
        mode={mode}
        setMode={setMode}
        run={run}
        setRun={setRun}
        pasted={pasted}
        setPasted={setPasted}
      />
      <div className="wf-script-actions">
        <Button
          variant="primary"
          icon="sparkles"
          loading={busy === "generate"}
          disabled={busy !== ""}
          onClick={runGenerate}
        >
          Generate
        </Button>
        <Button
          icon="play"
          loading={busy === "test"}
          disabled={busy !== "" || !script.trim()}
          onClick={runTest}
        >
          Test
        </Button>
      </div>
      {error ? <p className="wf-field-err">{error}</p> : null}
      {tested ? (
        <div className="wf-script-result" aria-live="polite">
          <div className="wf-script-result-h">
            <Badge tone={tested.ok ? "green" : "red"} dot>
              {tested.ok ? "Passes the contract" : "Fails the contract"}
            </Badge>
            <span className="muted">
              {tested.duration_ms} ms · 0 tok
              {tested.sample.run ? " · run #" + tested.sample.run : ""}
            </span>
          </div>
          <Problems problems={tested.problems} />
          <pre className="wf-json">{prettyValue(tested.output)}</pre>
          {tested.logs.length > 0 ? <pre className="wf-json wf-script-logs">{tested.logs.join("\n")}</pre> : null}
        </div>
      ) : null}
      {generated ? (
        <div className="wf-script-result" aria-live="polite">
          <div className="wf-script-result-h">
            <Badge
              tone={!generated.feasible ? "amber" : generated.problems.length === 0 ? "green" : "red"}
              dot
            >
              {!generated.feasible
                ? "Needs the agent"
                : generated.problems.length === 0
                  ? "Script ready"
                  : "Script has problems"}
            </Badge>
            <span className="muted">
              {tokens(generated.tokens)} tok
              {generated.agreement !== undefined
                ? " · " + Math.round(generated.agreement * 100) + "% agreement"
                : ""}
            </span>
          </div>
          {!generated.feasible ? <p>{generated.reason}</p> : null}
          <Problems problems={generated.problems} />
          {generated.feasible && generated.script ? (
            <>
              <pre className="wf-json wf-script-code">{generated.script}</pre>
              <pre className="wf-json">{prettyValue(generated.output)}</pre>
              <Button
                variant="primary"
                icon="check"
                onClick={() => {
                  applyGenerated(generated);
                  setGenerated(null);
                }}
              >
                Use this script
              </Button>
            </>
          ) : null}
        </div>
      ) : null}
      <h4 className="wf-script-h">History</h4>
      {stat && stat.events.length > 0 ? (
        <ul className="wf-script-events">
          {stat.events.map((event) => (
            <li key={event.id}>
              <Ic name={eventIcon(event.action)} size={14} />
              <div className="wf-script-ev-b">
                <span>{eventLabel(event)}</span>
                <span className="muted">
                  Run #{event.run_number} · {tokens(event.tokens)} tok · {ago(event.created_at)}
                  {event.choice === "revert" ? " · reverted" : ""}
                </span>
                {event.reason ? <span className="muted trunc">{event.reason}</span> : null}
              </div>
              {event.action === "script_update" && event.choice === "" ? (
                <Button size="sm" variant="ghost" icon="undo-2" onClick={() => revert(event)}>
                  Revert
                </Button>
              ) : null}
            </li>
          ))}
        </ul>
      ) : (
        <p className="muted">No fallbacks or updates yet.</p>
      )}
    </div>
  );
}

/* ConvertBanner sits on the Parameters tab of an agent, bot or worker node
   that the daemon suggests converting (REQ-SP-018). */
export function ConvertBanner({
  suggestion,
  onConvert,
  onDismiss,
}: {
  suggestion: ScriptSuggestion;
  onConvert: () => void;
  onDismiss: () => void;
}) {
  return (
    <div className="wf-script-banner" role="note">
      <Ic name="braces" size={16} />
      <div className="wf-script-banner-b">
        <b>Convert to Script Parse</b>
        <span className="muted">
          {suggestion.runs} runs · ~{tokens(suggestion.avg_tokens)} tok per run · ~
          {tokens(suggestion.est_tokens_30d)} tok in the last 30 days
        </span>
      </div>
      <Button size="sm" variant="primary" onClick={onConvert}>
        Convert
      </Button>
      <button type="button" className="wf-x" aria-label="Dismiss suggestion" onClick={onDismiss}>
        <Ic name="x" size={14} />
      </button>
    </div>
  );
}

/* ConvertDialog asks the guardian for a script_parse replacement of an agent
   step (API-SP-004) and shows it with the per-run checks before Replace. */
export function ConvertDialog({
  workflowId,
  node,
  onReplace,
  onClose,
}: {
  workflowId: string;
  node: WfNode;
  onReplace: (next: WfNode) => void;
  onClose: () => void;
}) {
  const [result, setResult] = useState<ScriptConvertResult | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    let live = true;
    convertNode(workflowId, node.id)
      .then((response) => live && setResult(response))
      .catch((err) => live && setError(errorText(err)));
    return () => {
      live = false;
    };
  }, [workflowId, node.id]);

  const proposal = result?.node;
  const allOk = result ? result.checks.every((check) => check.ok) : false;
  return (
    <Modal
      title="Convert to Script Parse"
      subtitle={node.name || node.id}
      size="lg"
      onClose={onClose}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="primary"
            icon="replace"
            disabled={!result?.feasible || !proposal}
            onClick={() => proposal && onReplace({ ...proposal, position: node.position, name: node.name })}
          >
            Replace node
          </Button>
        </>
      }
    >
      {!result && !error ? <p className="muted">The guardian is writing a script from past runs…</p> : null}
      {error ? <p className="wf-field-err">{error}</p> : null}
      {result && !result.feasible ? (
        <div className="wf-script-result">
          <Badge tone="amber" dot>
            Keep the agent step
          </Badge>
          <p>{result.reason}</p>
        </div>
      ) : null}
      {result && result.feasible && proposal ? (
        <div className="wf-script-convert">
          <p className="muted">
            {allOk ? "The script reproduces every checked run." : "Some runs do not match; review before replacing."}{" "}
            {tokens(result.tokens)} tok spent.
          </p>
          <ul className="wf-script-checks">
            {result.checks.map((check) => (
              <li key={check.run}>
                <Badge tone={check.ok ? "green" : "red"} dot>
                  Run #{check.run}
                </Badge>
                <span className="muted">{Math.round(check.agreement * 100)}% agreement</span>
                <Problems problems={check.problems} />
              </li>
            ))}
          </ul>
          <h4 className="wf-script-h">Goal</h4>
          <p>{String(proposal.params.goal ?? "")}</p>
          <h4 className="wf-script-h">Input</h4>
          <pre className="wf-json">{prettyValue(proposal.params.input)}</pre>
          <h4 className="wf-script-h">Output schema</h4>
          <pre className="wf-json">{prettyValue(proposal.params.output_schema)}</pre>
          <h4 className="wf-script-h">Script</h4>
          <pre className="wf-json wf-script-code">{String(proposal.params.script ?? "")}</pre>
        </div>
      ) : null}
    </Modal>
  );
}
