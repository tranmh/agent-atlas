"use client";

import type { MCPServer, Scenario } from "@/events/schema";
import type { AgentView } from "@/replay/reduce";
import { ScenarioBadge } from "./ScenarioBadge";

/** Agents of the scenario that use this MCP server, and how. */
export function usersOf(scenario: Scenario, serverId: string): { agent: string; how: string[] }[] {
  const out: { agent: string; how: string[] }[] = [];
  for (const a of scenario.agents) {
    const how = new Set<string>();
    for (const st of a.script) {
      if (st.tool?.server === serverId) how.add(st.tool.expect === "denied" ? `tries ${st.tool.name} (refused)` : `calls ${st.tool.name}`);
      if (st.resource?.server === serverId) how.add("reads a resource");
      if (st.tools?.includes(serverId)) how.add("lets the model pick tools");
      if (st.prompt_from?.server === serverId) how.add(`fetches prompt ${st.prompt_from.name}`);
    }
    if (how.size > 0) out.push({ agent: a.name, how: [...how] });
  }
  return out;
}

/** An MCP server: its tools, resources and prompts as the scenario defines them. */
export function MCPServerPanel({ server, scenario, scenarios, view }: { server: MCPServer; scenario: Scenario; scenarios: Scenario[]; view: AgentView | undefined }) {
  const target = server.id.split("-").slice(1).join("-");
  return (
    <div className="space-y-3 p-3" data-testid="mcp-panel">
      <div className="flex flex-wrap items-center gap-2">
        <ScenarioBadge code={scenario.code} scenarios={scenarios} />
        <span className="text-base font-semibold">⬡ {server.name}</span>
        <span className="text-sm text-ink-2">
          {server.city}, {server.country}
        </span>
        <span className="rounded bg-surface-0 px-1.5 text-[11px] text-ink-2">MCP server · {server.role}</span>
      </div>
      <p className="text-sm">{server.description}</p>
      <p className="text-xs text-ink-2">
        An MCP server offers tools, resources and prompts to agents. Agents reach it only through the gateway route <code>/mcp/{scenario.code}</code>, which multiplexes it with
        the scenario&apos;s other MCP server: its names appear there as <code>{target}_…</code> and its resource URIs as <code>{target}+…</code>.
      </p>
      {view?.note ? <p className="rounded-md border border-line bg-surface-0 p-2 text-sm">“{view.note}”</p> : null}
      <div>
        <h3 className="text-xs font-semibold">Tools</h3>
        <ul className="space-y-1 text-xs">
          {server.tools.map((t) => (
            <li key={t.name}>
              <code className={t.write ? "text-critical" : ""}>
                {target}_{t.name}({t.params.map((p) => p.name).join(", ")})
              </code>{" "}
              {t.description}
              {t.progress ? <span className="ml-1 rounded bg-surface-0 px-1">progress ×{t.progress.steps}</span> : null}
              {t.write ? <span className="ml-1 rounded bg-critical px-1 text-white">hidden and refused by the gateway</span> : null}
            </li>
          ))}
        </ul>
      </div>
      {server.resources.length > 0 ? (
        <div>
          <h3 className="text-xs font-semibold">Resources</h3>
          <ul className="text-xs">
            {server.resources.map((r) => (
              <li key={r.uri}>
                <code>
                  {target}+{r.uri}
                </code>{" "}
                {r.name}
              </li>
            ))}
          </ul>
        </div>
      ) : null}
      {server.prompts.length > 0 ? (
        <div>
          <h3 className="text-xs font-semibold">Prompts</h3>
          <ul className="text-xs">
            {server.prompts.map((p) => (
              <li key={p.name}>
                <code>
                  {target}_{p.name}
                </code>{" "}
                {p.description}
              </li>
            ))}
          </ul>
        </div>
      ) : null}
      <div>
        <h3 className="text-xs font-semibold">Used by</h3>
        <ul className="text-xs">
          {usersOf(scenario, server.id).map((u) => (
            <li key={u.agent}>
              <strong>{u.agent}</strong> {u.how.join(", ")}
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}
