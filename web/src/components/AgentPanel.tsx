"use client";

import { useEffect, useState } from "react";
import type { Agent, Place, Scenario } from "@/events/schema";
import { fetchAgentCard } from "@/lib/api";
import { STATUS_LOOK } from "@/map/colors";
import type { AgentView } from "@/replay/reduce";
import { JsonView } from "./JsonView";
import { MCPServerPanel } from "./MCPServerPanel";
import { ScenarioBadge } from "./ScenarioBadge";

const FEATURE_TEXT: Record<string, string> = {
  streaming: "Streams its work with SendStreamingMessage (Server-Sent Events). Its gateway route retries 503s, which the Chaos switch demonstrates.",
  multi_turn: "Asks a question (task state input-required); the orchestrator answers with a second message in the same task.",
  long_task_push: "Works for a long time: the orchestrator gets an immediate answer (returnImmediately) and push notifications on its webhook, then reads the result with GetTask. Its route has a 2 s delay policy.",
  restricted: "Sits behind a CEL authorization policy in the gateway: only its own orchestrator may call it, and never a restricted-* skill.",
};

/** How an agent's script uses MCP, in words. */
function mcpUse(agent: Agent): string[] {
  const out = new Set<string>();
  for (const st of agent.script) {
    if (st.tool) out.add(st.tool.expect === "denied" ? `tries the write tool ${st.tool.name} (refused by the gateway)` : `calls ${st.tool.name} on ${st.tool.server}`);
    if (st.resource) out.add(`reads ${st.resource.uri}`);
    if (st.tools) out.add(`lets the model pick tools from ${st.tools.join(" and ")}`);
    if (st.prompt_from) out.add(`fetches its system prompt ${st.prompt_from.name} from ${st.prompt_from.server}`);
  }
  return [...out];
}

export function AgentPanel({ id, scenarios, view, live }: { id: string | null; scenarios: Scenario[]; view: AgentView | undefined; live: boolean }) {
  const [card, setCard] = useState<unknown>(null);
  const [cardError, setCardError] = useState<string | null>(null);
  const scenario = scenarios.find((s) => s.orchestrator.id === id || s.agents.some((a) => a.id === id) || s.mcp_servers.some((m) => m.id === id));
  const mcpServer = scenario?.mcp_servers.find((m) => m.id === id);
  const agent: Agent | undefined = scenario?.agents.find((a) => a.id === id);
  const place: Place | undefined = agent ?? (scenario?.orchestrator.id === id ? scenario?.orchestrator : undefined);

  useEffect(() => {
    setCard(null);
    setCardError(null);
    if (!agent || !live) return;
    let stop = false;
    fetchAgentCard(agent.id)
      .then((c) => !stop && setCard(c))
      .catch((e: Error) => !stop && setCardError(e.message));
    return () => {
      stop = true;
    };
  }, [agent, live]);

  if (scenario && mcpServer) return <MCPServerPanel server={mcpServer} scenario={scenario} scenarios={scenarios} view={view} />;
  if (!id || !scenario || !place) return <p className="p-4 text-sm text-ink-3">Click a pin on the map to see the agent, its role and its agent card.</p>;
  const look = STATUS_LOOK[view?.status ?? "idle"];
  return (
    <div className="space-y-3 p-3" data-testid="agent-panel">
      <div className="flex flex-wrap items-center gap-2">
        <ScenarioBadge code={scenario.code} scenarios={scenarios} />
        <span className="text-base font-semibold">{place.name}</span>
        <span className="text-sm text-ink-2">
          {place.city}, {place.country}
        </span>
        <span className="rounded px-1.5 text-[11px] text-white" style={{ background: look.color }}>
          {look.glyph} {look.label}
        </span>
      </div>
      <p className="text-sm">{place.description}</p>
      {view?.note ? <p className="rounded-md border border-line bg-surface-0 p-2 text-sm">“{view.note}”</p> : null}
      {agent ? (
        <>
          <p className="text-xs text-ink-2">
            <strong>Shows:</strong> {FEATURE_TEXT[agent.feature]} {agent.mode === "llm" ? "Its answer comes from a local LLM, called through the gateway's LLM route." : ""}
          </p>
          {mcpUse(agent).length > 0 ? (
            <p className="text-xs text-ink-2">
              <strong>Uses MCP:</strong> {mcpUse(agent).join("; ")}.
            </p>
          ) : null}
          <ul className="text-xs">
            {agent.skills.map((sk) => (
              <li key={sk.id}>
                <code className={sk.id.startsWith("restricted-") ? "text-critical" : ""}>{sk.id}</code> {sk.description}
              </li>
            ))}
          </ul>
          <div>
            <div className="mb-1 text-xs text-ink-3">Agent card, fetched through the gateway now (note the rewritten URL):</div>
            {!live ? <p className="text-xs text-ink-3">Only available live.</p> : cardError ? <p className="text-xs text-critical">{cardError}</p> : card ? <JsonView value={card} /> : <p className="text-xs text-ink-3">loading…</p>}
          </div>
        </>
      ) : (
        <p className="text-xs text-ink-2">The orchestrator is an A2A client. It only knows the gateway URL, discovers each agent through its card and delegates the plan:</p>
      )}
      {!agent ? (
        <ol className="list-decimal space-y-1 pl-5 text-xs">
          {scenario.plan.map((p) => (
            <li key={p.step}>
              <strong>{p.step}</strong> → {p.agent} ({p.call}
              {p.expect ? ", expected to be denied" : ""}): {p.text}
            </li>
          ))}
        </ol>
      ) : null}
    </div>
  );
}
