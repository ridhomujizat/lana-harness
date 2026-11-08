import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type {
  DragEvent as ReactDragEvent,
  PointerEvent as ReactPointerEvent,
  ReactNode,
} from "react";
import type { Graph, WfEdge, WfNode } from "../api/workflows";
import { Ic } from "../ui/icons";
import {
  isTrigger,
  newEdgeId,
  nodeMeta,
  nodePorts,
  nodeSubtitle,
} from "./workflow-catalog";
import { AgentAvatar } from "./agent-avatar";

/* The workflow canvas of REQ-WF-011 (design workflows-2/3/5/7): node cards on a
   dot grid, orthogonal edges with port labels, dashed loop bodies, pan, zoom
   and Fit. The editor passes onChange to make it editable (drag nodes, connect
   handles, select and delete edges, drop palette items); the executions view
   passes per-node status badges instead. */

export const NODE_W = 248;
export const NODE_H = 64;
const GRID = 8;
const GROUP_PAD = 16;
const GROUP_TOP = 28;

export type NodeTone =
  | "ok"
  | "fail"
  | "run"
  | "wait"
  | "skip"
  | "pending"
  | "changed";

export interface NodeBadge {
  text: string;
  tone: "green" | "red" | "blue" | "amber" | "gray" | "violet";
}

export interface CanvasProps {
  graph: Graph;
  selectedId?: string;
  onSelect?: (id: string) => void;
  onChange?: (graph: Graph) => void;
  /* Called when a palette item is dropped at a canvas point. */
  onDropType?: (type: string, at: { x: number; y: number }) => void;
  tones?: Record<string, NodeTone>;
  badges?: Record<string, NodeBadge>;
  /* Extra pills next to a node, e.g. the guardian "Asking you". */
  sidePills?: Record<string, ReactNode>;
  empty?: ReactNode;
  footer?: ReactNode;
  ariaLabel?: string;
  /* Space covered by an overlay on the left (the palette), kept clear by Fit. */
  insetLeft?: number;
}

interface Box {
  x: number;
  y: number;
  w: number;
  h: number;
}

function snap(value: number): number {
  return Math.round(value / GRID) * GRID;
}

/* The dashed box around a loop's body nodes. */
export function loopBoxes(graph: Graph): Map<string, Box> {
  const boxes = new Map<string, Box>();
  for (const loop of graph.nodes) {
    if (loop.type !== "loop") {
      continue;
    }
    const kids = graph.nodes.filter((node) => node.parent === loop.id);
    if (kids.length === 0) {
      continue;
    }
    const minX = Math.min(...kids.map((n) => n.position.x)) - GROUP_PAD;
    const minY = Math.min(...kids.map((n) => n.position.y)) - GROUP_TOP;
    const maxX = Math.max(...kids.map((n) => n.position.x + NODE_W)) + GROUP_PAD;
    const maxY = Math.max(...kids.map((n) => n.position.y + NODE_H)) + GROUP_PAD;
    boxes.set(loop.id, { x: minX, y: minY, w: maxX - minX, h: maxY - minY });
  }
  return boxes;
}

function graphBounds(graph: Graph): Box {
  if (graph.nodes.length === 0) {
    return { x: 0, y: 0, w: 600, h: 400 };
  }
  const minX = Math.min(...graph.nodes.map((n) => n.position.x));
  const minY = Math.min(...graph.nodes.map((n) => n.position.y));
  const maxX = Math.max(...graph.nodes.map((n) => n.position.x + NODE_W));
  const maxY = Math.max(...graph.nodes.map((n) => n.position.y + NODE_H));
  return { x: minX - 40, y: minY - 40, w: maxX - minX + 80, h: maxY - minY + 80 };
}

/* The port a new edge from `source` gets: the first port not used yet. */
export function nextPort(graph: Graph, source: WfNode, target: WfNode): string {
  if (source.type === "loop") {
    return target.parent === source.id ? "body" : "done";
  }
  const ports = nodePorts(source);
  if (ports.length <= 1) {
    return ports[0] === "out" ? "" : ports[0] || "";
  }
  const used = new Set(
    graph.edges
      .filter((edge) => edge.from === source.id)
      .map((edge) => edge.from_port || "out")
  );
  return ports.find((port) => !used.has(port)) || ports[0] || "";
}

interface EdgeGeom {
  edge: WfEdge;
  d: string;
  label: string;
  lx: number;
  ly: number;
}

function edgeGeometry(graph: Graph, boxes: Map<string, Box>): EdgeGeom[] {
  const byId = new Map(graph.nodes.map((node) => [node.id, node]));
  const out: EdgeGeom[] = [];
  for (const edge of graph.edges) {
    const from = byId.get(edge.from);
    const to = byId.get(edge.to);
    if (!from || !to) {
      continue;
    }
    let sx = from.position.x + NODE_W / 2;
    let sy = from.position.y + NODE_H;
    const port = edge.from_port || "";
    /* The loop's done/out edges leave from the bottom of its body box. */
    const box = boxes.get(from.id);
    if (from.type === "loop" && port !== "body" && box) {
      sx = box.x + box.w / 2;
      sy = box.y + box.h;
    }
    const tx = to.position.x + NODE_W / 2;
    const ty = to.position.y;
    let d: string;
    let midY: number;
    if (ty > sy + 16) {
      midY = sy + Math.max(16, Math.min(28, (ty - sy) / 2));
      if (Math.abs(tx - sx) < 1) {
        d = `M${sx},${sy} V${ty}`;
      } else {
        const r = Math.min(8, Math.abs(tx - sx) / 2, (ty - midY) / 2);
        const dir = tx > sx ? 1 : -1;
        d =
          `M${sx},${sy} V${midY - r} Q${sx},${midY} ${sx + dir * r},${midY}` +
          ` H${tx - dir * r} Q${tx},${midY} ${tx},${midY + r} V${ty}`;
      }
    } else {
      /* A target above or beside the source: route around the right side. */
      const side = Math.max(from.position.x, to.position.x) + NODE_W + 32;
      midY = sy + 20;
      d = `M${sx},${sy} V${midY} H${side} V${ty - 20} H${tx} V${ty}`;
    }
    const label =
      port && port !== "out" && !(from.type === "loop") ? port : "";
    out.push({
      edge,
      d,
      label,
      lx: Math.abs(tx - sx) < 1 ? sx : (sx + tx) / 2 + (tx > sx ? 24 : -24),
      ly: midY,
    });
  }
  return out;
}

/* The dashed "next item" return path from a loop body back to its loop. */
function loopReturns(graph: Graph, boxes: Map<string, Box>) {
  const out: { id: string; d: string; lx: number; ly: number }[] = [];
  for (const loop of graph.nodes) {
    const box = boxes.get(loop.id);
    if (!box) {
      continue;
    }
    const kids = graph.nodes.filter((node) => node.parent === loop.id);
    const last = kids.reduce((a, b) => (b.position.y > a.position.y ? b : a));
    const rx = box.x + box.w + 24;
    const sy = last.position.y + NODE_H / 2;
    const sx = last.position.x + NODE_W;
    const ty = loop.position.y + NODE_H / 2;
    const tx = loop.position.x + NODE_W;
    out.push({
      id: loop.id,
      d: `M${sx},${sy} H${rx} V${ty} H${tx + 4}`,
      lx: rx,
      ly: (sy + ty) / 2,
    });
  }
  return out;
}

interface DragState {
  kind: "node" | "pan" | "connect";
  id?: string;
  startX: number;
  startY: number;
  originX: number;
  originY: number;
  moved: boolean;
}

export function WorkflowCanvas({
  graph,
  selectedId,
  onSelect,
  onChange,
  onDropType,
  tones,
  badges,
  sidePills,
  empty,
  footer,
  ariaLabel,
  insetLeft = 0,
}: CanvasProps) {
  const editable = Boolean(onChange);
  const wrapRef = useRef<HTMLDivElement>(null);
  const [zoom, setZoom] = useState(1);
  const [pan, setPan] = useState({ x: 40, y: 24 });
  const [drag, setDrag] = useState<DragState | null>(null);
  const [connectTo, setConnectTo] = useState<{ x: number; y: number } | null>(
    null
  );
  const [selectedEdge, setSelectedEdge] = useState<string>("");
  const fitted = useRef(false);

  const boxes = useMemo(() => loopBoxes(graph), [graph]);
  const edges = useMemo(() => edgeGeometry(graph, boxes), [graph, boxes]);
  const returns = useMemo(() => loopReturns(graph, boxes), [graph, boxes]);

  const toCanvas = useCallback(
    (clientX: number, clientY: number) => {
      const rect = wrapRef.current?.getBoundingClientRect();
      const left = rect ? rect.left : 0;
      const top = rect ? rect.top : 0;
      return {
        x: (clientX - left - pan.x) / zoom,
        y: (clientY - top - pan.y) / zoom,
      };
    },
    [pan, zoom]
  );

  const fit = useCallback(() => {
    const wrap = wrapRef.current;
    if (!wrap) {
      return;
    }
    const bounds = graphBounds(graph);
    const width = (wrap.clientWidth || 800) - insetLeft;
    const height = (wrap.clientHeight || 600) - 72;
    const next = Math.max(
      0.4,
      Math.min(1, width / bounds.w, height / bounds.h)
    );
    setZoom(next);
    setPan({
      x: insetLeft + (width - bounds.w * next) / 2 - bounds.x * next,
      y: 16 - bounds.y * next,
    });
  }, [graph, insetLeft]);

  useEffect(() => {
    if (!fitted.current && graph.nodes.length > 0) {
      fitted.current = true;
      fit();
    }
  }, [graph, fit]);

  useEffect(() => {
    if (!editable || !selectedEdge) {
      return;
    }
    const onKey = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null;
      if (target && /input|textarea|select/i.test(target.tagName)) {
        return;
      }
      if (event.key === "Delete" || event.key === "Backspace") {
        event.preventDefault();
        removeEdge(selectedEdge);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  function removeEdge(id: string) {
    if (!onChange) {
      return;
    }
    onChange({ ...graph, edges: graph.edges.filter((edge) => edge.id !== id) });
    setSelectedEdge("");
  }

  function setEdgePort(id: string, port: string) {
    if (!onChange) {
      return;
    }
    onChange({
      ...graph,
      edges: graph.edges.map((edge) =>
        edge.id === id ? { ...edge, from_port: port === "out" ? "" : port } : edge
      ),
    });
  }

  function onBackgroundDown(event: ReactPointerEvent<HTMLDivElement>) {
    if (event.button !== 0) {
      return;
    }
    setSelectedEdge("");
    setDrag({
      kind: "pan",
      startX: event.clientX,
      startY: event.clientY,
      originX: pan.x,
      originY: pan.y,
      moved: false,
    });
    (event.currentTarget as HTMLElement).setPointerCapture?.(event.pointerId);
  }

  function onNodeDown(event: ReactPointerEvent<HTMLDivElement>, node: WfNode) {
    event.stopPropagation();
    if (event.button !== 0) {
      return;
    }
    setSelectedEdge("");
    if (!editable) {
      onSelect?.(node.id);
      return;
    }
    setDrag({
      kind: "node",
      id: node.id,
      startX: event.clientX,
      startY: event.clientY,
      originX: node.position.x,
      originY: node.position.y,
      moved: false,
    });
    wrapRef.current?.setPointerCapture?.(event.pointerId);
  }

  function onHandleDown(event: ReactPointerEvent<HTMLSpanElement>, node: WfNode) {
    event.stopPropagation();
    if (!editable) {
      return;
    }
    setDrag({
      kind: "connect",
      id: node.id,
      startX: event.clientX,
      startY: event.clientY,
      originX: node.position.x + NODE_W / 2,
      originY: node.position.y + NODE_H,
      moved: false,
    });
    setConnectTo(toCanvas(event.clientX, event.clientY));
    wrapRef.current?.setPointerCapture?.(event.pointerId);
  }

  function onMove(event: ReactPointerEvent<HTMLDivElement>) {
    if (!drag) {
      return;
    }
    const dx = event.clientX - drag.startX;
    const dy = event.clientY - drag.startY;
    const moved = drag.moved || Math.abs(dx) + Math.abs(dy) > 3;
    if (moved !== drag.moved) {
      setDrag({ ...drag, moved });
    }
    if (drag.kind === "pan") {
      setPan({ x: drag.originX + dx, y: drag.originY + dy });
    } else if (drag.kind === "node" && moved && onChange) {
      const x = snap(drag.originX + dx / zoom);
      const y = snap(drag.originY + dy / zoom);
      onChange({
        ...graph,
        nodes: graph.nodes.map((node) =>
          node.id === drag.id ? { ...node, position: { x, y } } : node
        ),
      });
    } else if (drag.kind === "connect") {
      setConnectTo(toCanvas(event.clientX, event.clientY));
    }
  }

  function nodeAt(point: { x: number; y: number }): WfNode | undefined {
    return graph.nodes.find(
      (node) =>
        point.x >= node.position.x &&
        point.x <= node.position.x + NODE_W &&
        point.y >= node.position.y - 8 &&
        point.y <= node.position.y + NODE_H
    );
  }

  function onUp(event: ReactPointerEvent<HTMLDivElement>) {
    const current = drag;
    setDrag(null);
    setConnectTo(null);
    if (!current) {
      return;
    }
    if (current.kind === "node" && !current.moved && current.id) {
      onSelect?.(current.id);
    }
    if (current.kind === "pan" && !current.moved) {
      onSelect?.("");
    }
    if (current.kind === "connect" && onChange && current.id) {
      const target = nodeAt(toCanvas(event.clientX, event.clientY));
      const source = graph.nodes.find((node) => node.id === current.id);
      if (
        source &&
        target &&
        target.id !== source.id &&
        !isTrigger(target.type) &&
        !graph.edges.some((e) => e.from === source.id && e.to === target.id)
      ) {
        const port = nextPort(graph, source, target);
        const edge: WfEdge = { id: newEdgeId(graph), from: source.id, to: target.id };
        if (port && port !== "out") {
          edge.from_port = port;
        }
        onChange({ ...graph, edges: [...graph.edges, edge] });
      }
    }
  }

  /* Trackpad pinch and ctrl/cmd + wheel zoom the canvas only, never the page
     (owner report 2026-11-05). React registers wheel listeners as passive, so
     preventDefault there cannot stop the browser zoom: these are native
     listeners with passive: false. Safari sends pinch as gesture* events. */
  const viewRef = useRef({ zoom, pan });
  viewRef.current = { zoom, pan };
  useEffect(() => {
    const wrap = wrapRef.current;
    if (!wrap) {
      return undefined;
    }
    const zoomAround = (target: number, clientX: number, clientY: number) => {
      const current = viewRef.current;
      const rect = wrap.getBoundingClientRect();
      const cx = clientX - rect.left;
      const cy = clientY - rect.top;
      const next = Math.max(0.3, Math.min(2, target));
      const nextPan = {
        x: cx - ((cx - current.pan.x) * next) / current.zoom,
        y: cy - ((cy - current.pan.y) * next) / current.zoom,
      };
      viewRef.current = { zoom: next, pan: nextPan };
      setPan(nextPan);
      setZoom(next);
    };
    const onWheel = (event: WheelEvent) => {
      event.preventDefault();
      if (event.ctrlKey || event.metaKey) {
        /* Pinch deltas are small and fine-grained; a mouse wheel notch is
           about 100. exp keeps both smooth and symmetric. */
        const delta = Math.max(-50, Math.min(50, event.deltaY));
        zoomAround(viewRef.current.zoom * Math.exp(-delta * 0.01), event.clientX, event.clientY);
        return;
      }
      const current = viewRef.current;
      const nextPan = { x: current.pan.x - event.deltaX, y: current.pan.y - event.deltaY };
      viewRef.current = { zoom: current.zoom, pan: nextPan };
      setPan(nextPan);
    };
    let gestureZoom = 1;
    type GestureLike = Event & { scale?: number; clientX?: number; clientY?: number };
    const onGestureStart = (event: Event) => {
      event.preventDefault();
      gestureZoom = viewRef.current.zoom;
    };
    const onGestureChange = (event: Event) => {
      event.preventDefault();
      const gesture = event as GestureLike;
      const rect = wrap.getBoundingClientRect();
      zoomAround(
        gestureZoom * (gesture.scale || 1),
        gesture.clientX ?? rect.left + rect.width / 2,
        gesture.clientY ?? rect.top + rect.height / 2
      );
    };
    const stop = (event: Event) => event.preventDefault();
    wrap.addEventListener("wheel", onWheel, { passive: false });
    wrap.addEventListener("gesturestart", onGestureStart, { passive: false });
    wrap.addEventListener("gesturechange", onGestureChange, { passive: false });
    wrap.addEventListener("gestureend", stop, { passive: false });
    return () => {
      wrap.removeEventListener("wheel", onWheel);
      wrap.removeEventListener("gesturestart", onGestureStart);
      wrap.removeEventListener("gesturechange", onGestureChange);
      wrap.removeEventListener("gestureend", stop);
    };
  }, []);

  function zoomBy(factor: number) {
    const wrap = wrapRef.current;
    const cx = (wrap?.clientWidth || 800) / 2;
    const cy = (wrap?.clientHeight || 600) / 2;
    const next = Math.max(0.3, Math.min(2, zoom * factor));
    setPan({
      x: cx - ((cx - pan.x) * next) / zoom,
      y: cy - ((cy - pan.y) * next) / zoom,
    });
    setZoom(next);
  }

  function onDragOver(event: ReactDragEvent<HTMLDivElement>) {
    if (onDropType && event.dataTransfer.types.includes("application/x-lana-node")) {
      event.preventDefault();
      event.dataTransfer.dropEffect = "copy";
    }
  }

  function onDrop(event: ReactDragEvent<HTMLDivElement>) {
    const type = event.dataTransfer.getData("application/x-lana-node");
    if (!type || !onDropType) {
      return;
    }
    event.preventDefault();
    /* Synthetic drops (tests, some assistive tools) may carry no coordinates. */
    const cx = Number.isFinite(event.clientX) ? event.clientX : 0;
    const cy = Number.isFinite(event.clientY) ? event.clientY : 0;
    const point = toCanvas(cx, cy);
    onDropType(type, { x: snap(point.x - NODE_W / 2), y: snap(point.y - NODE_H / 2) });
  }

  const edgeSel = edges.find((geom) => geom.edge.id === selectedEdge);
  const edgeSource = edgeSel
    ? graph.nodes.find((node) => node.id === edgeSel.edge.from)
    : undefined;
  const connectFrom =
    drag && drag.kind === "connect" ? { x: drag.originX, y: drag.originY } : null;

  return (
    <div
      ref={wrapRef}
      className={"wf-canvas" + (drag && drag.kind === "pan" ? " panning" : "")}
      role="region"
      aria-label={ariaLabel || "Workflow canvas"}
      onPointerDown={onBackgroundDown}
      onPointerMove={onMove}
      onPointerUp={onUp}
      onDragOver={onDragOver}
      onDrop={onDrop}
      style={{
        backgroundPosition: `${pan.x}px ${pan.y}px`,
        backgroundSize: `${16 * zoom}px ${16 * zoom}px`,
      }}
    >
      <div
        className="wf-layer"
        style={{ transform: `translate(${pan.x}px, ${pan.y}px) scale(${zoom})` }}
      >
        {[...boxes.entries()].map(([id, box]) => (
          <div
            key={"box-" + id}
            className={"wf-loopbox" + (tones && tones[id] ? " t-" + tones[id] : "")}
            style={{ left: box.x, top: box.y, width: box.w, height: box.h }}
          >
            <span>Loop body</span>
          </div>
        ))}
        <svg className="wf-edges" width="1" height="1" aria-hidden="true">
          <defs>
            <marker
              id="wf-arrow"
              viewBox="0 0 10 10"
              refX="5"
              refY="9"
              markerWidth="7"
              markerHeight="7"
              orient="auto-start-reverse"
            >
              <path d="M1,3 L5,9 L9,3" fill="none" stroke="currentColor" strokeWidth="1.6" />
            </marker>
          </defs>
          {returns.map((ret) => (
            <path
              key={"ret-" + ret.id}
              d={ret.d}
              className={"wf-return" + (tones && tones[ret.id] === "ok" ? " ok" : "")}
              markerEnd="url(#wf-arrow)"
            />
          ))}
          {edges.map((geom) => {
            const tone = tones ? tones[geom.edge.from] : undefined;
            const dead =
              tones &&
              (tones[geom.edge.to] === "skip" || tones[geom.edge.to] === "pending");
            return (
              <g key={geom.edge.id}>
                <path
                  d={geom.d}
                  className="wf-hit"
                  onPointerDown={(event) => {
                    event.stopPropagation();
                    if (editable) {
                      setSelectedEdge(geom.edge.id);
                    }
                  }}
                />
                <path
                  d={geom.d}
                  className={
                    "wf-edge" +
                    (tone === "ok" && !dead ? " ok" : "") +
                    (dead ? " dead" : "") +
                    (selectedEdge === geom.edge.id ? " sel" : "")
                  }
                  markerEnd="url(#wf-arrow)"
                />
              </g>
            );
          })}
          {connectFrom && connectTo ? (
            <path
              d={`M${connectFrom.x},${connectFrom.y} L${connectTo.x},${connectTo.y}`}
              className="wf-edge drafting"
            />
          ) : null}
        </svg>
        {returns.map((ret) => (
          <span
            key={"retl-" + ret.id}
            className="wf-pill wf-pill-next"
            style={{ left: ret.lx, top: ret.ly }}
          >
            next item
          </span>
        ))}
        {edges
          .filter((geom) => geom.label)
          .map((geom) => (
            <span
              key={"lbl-" + geom.edge.id}
              className={"wf-pill port-" + geom.label}
              style={{ left: geom.lx, top: geom.ly }}
            >
              {geom.label}
            </span>
          ))}
        {graph.nodes.map((node) => {
          const meta = nodeMeta(node.type);
          const tone = tones ? tones[node.id] : undefined;
          const badge = badges ? badges[node.id] : undefined;
          return (
            <div
              key={node.id}
              className={
                "wf-node" +
                (selectedId === node.id ? " sel" : "") +
                (tone ? " t-" + tone : "") +
                (node.disabled ? " off" : "") +
                (editable ? " editable" : "")
              }
              style={{ left: node.position.x, top: node.position.y }}
              onPointerDown={(event) => onNodeDown(event, node)}
              role="button"
              tabIndex={0}
              aria-pressed={selectedId === node.id}
              aria-label={node.name + ", " + meta.label}
              onKeyDown={(event) => {
                if (event.key === "Enter" || event.key === " ") {
                  event.preventDefault();
                  onSelect?.(node.id);
                }
              }}
              data-node={node.id}
            >
              {!isTrigger(node.type) ? <span className="wf-port in" /> : null}
              {node.type === "bot" && typeof node.params?.agent === "string" && node.params.agent ? (
                <span className="wf-ico wf-ico-agent">
                  <AgentAvatar name={node.params.agent} size="lg" aria-hidden="true" />
                </span>
              ) : (
                <span className={"wf-ico tint-" + meta.tint}>
                  <Ic name={meta.icon} size={18} />
                </span>
              )}
              <span className="wf-node-text">
                <span className="wf-node-name trunc">{node.name || meta.label}</span>
                <span className="wf-node-sub trunc">{nodeSubtitle(node)}</span>
              </span>
              <span
                className="wf-port out"
                onPointerDown={(event) => onHandleDown(event, node)}
                title={editable ? "Drag to connect" : undefined}
              />
              {badge ? (
                <span className={"wf-badge b-" + badge.tone}>
                  <i />
                  {badge.text}
                </span>
              ) : null}
              {sidePills && sidePills[node.id] ? (
                <span className="wf-side">{sidePills[node.id]}</span>
              ) : null}
            </div>
          );
        })}
      </div>
      {graph.nodes.length === 0 && empty ? (
        <div className="wf-empty">{empty}</div>
      ) : null}
      {edgeSel && editable ? (
        <div
          className="wf-edgebar"
          onPointerDown={(event) => event.stopPropagation()}
          role="toolbar"
          aria-label="Edge"
        >
          {edgeSource && nodePorts(edgeSource).length > 1 ? (
            <span className="wf-edgeports">
              {(edgeSource.type === "loop" ? ["body", "done"] : nodePorts(edgeSource)).map(
                (port) => (
                  <button
                    key={port}
                    type="button"
                    className={
                      (edgeSel.edge.from_port || "out") === port ? "on" : undefined
                    }
                    onClick={() => setEdgePort(edgeSel.edge.id, port)}
                  >
                    {port}
                  </button>
                )
              )}
            </span>
          ) : null}
          <button
            type="button"
            className="wf-edgedel"
            onClick={() => removeEdge(edgeSel.edge.id)}
          >
            <Ic name="trash-2" size={14} />
            Delete edge
          </button>
        </div>
      ) : null}
      {footer ? <div className="wf-foot">{footer}</div> : null}
      <div
        className="wf-zoom"
        onPointerDown={(event) => event.stopPropagation()}
      >
        <button type="button" aria-label="Zoom out" onClick={() => zoomBy(0.85)}>
          <Ic name="minus" size={14} />
        </button>
        <span className="num">{Math.round(zoom * 100)}%</span>
        <button type="button" aria-label="Zoom in" onClick={() => zoomBy(1.15)}>
          <Ic name="plus" size={14} />
        </button>
        <button type="button" onClick={fit}>
          Fit
        </button>
      </div>
    </div>
  );
}

/* Places a new node under `anchor` (or at the bottom of the graph) and links
   it from the anchor when one is given. */
export function addNodeBelow(
  graph: Graph,
  node: WfNode,
  anchor?: WfNode
): Graph {
  const placed: WfNode = { ...node };
  if (anchor) {
    placed.position = { x: anchor.position.x, y: anchor.position.y + NODE_H + 40 };
    if (anchor.parent && !placed.parent && !isTrigger(node.type)) {
      placed.parent = anchor.parent;
    }
  } else if (graph.nodes.length > 0) {
    const bottom = graph.nodes.reduce((a, b) => (b.position.y > a.position.y ? b : a));
    placed.position = { x: bottom.position.x, y: bottom.position.y + NODE_H + 40 };
  }
  const next: Graph = { nodes: [...graph.nodes, placed], edges: [...graph.edges] };
  if (anchor && !isTrigger(node.type)) {
    const port = nextPort(next, anchor, placed);
    const edge: WfEdge = { id: newEdgeId(next), from: anchor.id, to: placed.id };
    if (port && port !== "out") {
      edge.from_port = port;
    }
    next.edges.push(edge);
  }
  return next;
}
