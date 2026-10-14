"use client";

import type { AtlasEvent, Scenario } from "@/events/schema";
import { JAEGER_URL } from "@/lib/api";
import { clock, kindInfo, shortId } from "@/lib/format";
import { stateLook } from "@/map/colors";
import { JsonView } from "./JsonView";
import { ScenarioBadge } from "./ScenarioBadge";

/** What a kind teaches, shown above the payload. */
const EXPLAIN: Record<string, string> = {
  card_fetch:
    "Discovery: the orchestrator reads the agent card at /.well-known/agent-card.json through the gateway. The gateway rewrote supportedInterfaces[].url to its own address, so every later call goes through it too.",
  rpc_request: "A JSON-RPC 2.0 request exactly as it left the orchestrator. A2A v1.0 method names: SendMessage, SendStreamingMessage, GetTask.",
  rpc_response: "The JSON-RPC response as the orchestrator received it (through the gateway).",
  stream_event: "One Server-Sent Events frame of SendStreamingMessage. Each frame is a JSON-RPC response carrying a task, a statusUpdate or an artifactUpdate.",
  task_state: "Task lifecycle: submitted → working ⇄ input-required → completed / failed / canceled / rejected. Derived from the wire data.",
  policy_denied: "The gateway's CEL authorization policy refused the call with HTTP 403. The agent never saw it; no task was created.",
  push_received: "Push notification: the agent POSTs task events to the webhook the orchestrator registered (pushConfig). It goes directly, not through the gateway.",
  agent_request: "The same request as it arrived at the agent, behind the gateway: note the headers the gateway passed on.",
  agent_work: "What the agent is doing. Streaming clients see these as status updates; blocking clients only see the final result.",
  fault_injected: "Chaos: the agent answered 503 to the first attempt. The gateway's retry policy resends the request; the orchestrator never notices.",
  gateway_access: "agentgateway's own access log (OTLP): it parsed the A2A method and task state out of the traffic.",
  llm_request: "The agent asks the model through the gateway's OpenAI-compatible LLM route (/v1/chat/completions → Ollama).",
  llm_response: "The model's answer, returned through the gateway.",
  llm_unavailable: "The model could not be reached; the agent used its scripted answer instead. The A2A flow is unaffected.",
  breakpoint: "Breakpoint mode: the orchestrator asked the visualizer whether to continue and waits.",
};

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  if (children === undefined || children === null || children === "") return null;
  return (
    <>
      <dt className="text-ink-3">{label}</dt>
      <dd className="min-w-0 break-words font-mono text-[11.5px]">{children}</dd>
    </>
  );
}

export function PayloadPanel({ event, scenarios, onTask }: { event: AtlasEvent | null; scenarios: Scenario[]; onTask: (taskId: string) => void }) {
  if (!event) {
    return <p className="p-4 text-sm text-ink-3">Select an event in the list, or click a moving message on the map, to see its payload.</p>;
  }
  const info = kindInfo(event.kind);
  const look = event.state_to ? stateLook(event.state_to) : null;
  const payload = event.payload as Record<string, unknown> | undefined;
  const traceId = payload && typeof payload === "object" ? (payload["trace.id"] as string | undefined) : undefined;
  return (
    <div className="space-y-3 p-3" data-testid="payload-panel">
      <div className="flex flex-wrap items-center gap-2">
        <ScenarioBadge code={event.scenario} scenarios={scenarios} />
        <span className={`text-base font-semibold ${event.kind === "policy_denied" ? "text-critical" : ""}`}>
          {info.icon} {info.label}
        </span>
        <span className="rounded bg-surface-0 px-1.5 text-[11px] text-ink-2">source: {event.source}</span>
        {look && look.glyph ? (
          <span className="rounded px-1.5 text-[11px] text-white" style={{ background: look.color }}>
            {look.glyph} {look.label}
          </span>
        ) : null}
      </div>
      {event.note ? <p className="text-sm">{event.note}</p> : null}
      {EXPLAIN[event.kind] ? <p className="rounded-md border border-line bg-surface-0 p-2 text-xs text-ink-2">{EXPLAIN[event.kind]}</p> : null}
      <dl className="grid grid-cols-[6.5rem_1fr] gap-x-2 gap-y-0.5 text-xs">
        <Field label="time">{clock(event.t)}</Field>
        <Field label="from → to">{event.from || event.to ? `${event.from ?? "?"} → ${event.to ?? "?"}` : ""}</Field>
        <Field label="via">{event.via}</Field>
        <Field label="method">{event.method}</Field>
        <Field label="run / step">{event.run_id ? `${event.run_id}${event.step ? " / " + event.step : ""}` : ""}</Field>
        <Field label="task">
          {event.task_id ? (
            <button type="button" className="underline decoration-dotted" onClick={() => onTask(event.task_id!)} title={event.task_id}>
              {shortId(event.task_id, 13)}
            </button>
          ) : null}
        </Field>
        <Field label="context">{event.context_id ? <span title={event.context_id}>{shortId(event.context_id, 13)}</span> : null}</Field>
        <Field label="state">{event.state_from || event.state_to ? `${event.state_from || "·"} → ${event.state_to || "·"}` : ""}</Field>
        <Field label="HTTP">{event.http_status ? String(event.http_status) : ""}</Field>
        <Field label="duration">{event.duration_ms ? `${event.duration_ms} ms` : ""}</Field>
        <Field label="trace">
          {traceId ? (
            <a className="underline" href={`${JAEGER_URL}/trace/${traceId}`} target="_blank" rel="noreferrer">
              open in Jaeger
            </a>
          ) : null}
        </Field>
      </dl>
      {event.payload !== undefined ? (
        <div>
          <div className="mb-1 flex items-center justify-between text-xs text-ink-3">
            <span>payload{event.payload_truncated ? " (truncated)" : ""}</span>
            <button type="button" className="underline" onClick={() => navigator.clipboard?.writeText(JSON.stringify(event.payload, null, 2))}>
              copy
            </button>
          </div>
          <JsonView value={event.payload} />
        </div>
      ) : null}
    </div>
  );
}
