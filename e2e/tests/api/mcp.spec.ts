import { expect, test } from "@playwright/test";
import { collectRun, openEvents, startRun } from "../helpers/atlas";
import { RawMCP } from "../helpers/mcp";
import { VIZ_URL } from "../helpers/env";

interface Scenario {
  code: string;
  mcp_servers: { id: string; tools: { name: string; write?: boolean }[]; resources?: { uri: string }[]; prompts?: { name: string }[] }[];
}

let scenarios: Scenario[] = [];
test.beforeAll(async () => {
  scenarios = await (await fetch(`${VIZ_URL}/api/scenarios`)).json();
});

test("each scenario's MCP route multiplexes two servers and hides the write tool", async () => {
  for (const s of scenarios) {
    const mcp = new RawMCP(s.code, `${s.code}-e2e`);
    expect(await mcp.open(), s.code).toBe(200);
    const tools = (await mcp.call("tools/list")).result.tools.map((t: { name: string }) => t.name).sort();
    const expected = s.mcp_servers
      .flatMap((m) => m.tools.filter((t) => !t.write).map((t) => `${m.id.split("-")[1]}_${t.name}`))
      .sort();
    expect(tools, s.code).toEqual(expected);
    const resources = (await mcp.call("resources/list")).result.resources.map((r: { uri: string }) => r.uri);
    for (const m of s.mcp_servers) for (const r of m.resources ?? []) expect(resources).toContain(`${m.id.split("-")[1]}+${r.uri}`);
    const prompts = (await mcp.call("prompts/list")).result.prompts.map((p: { name: string }) => p.name);
    expect(prompts.length, s.code).toBeGreaterThan(0);
  }
});

test("the gateway refuses the write tool and foreign callers", async () => {
  const mcp = new RawMCP("sc", "sc-e2e");
  await mcp.open();
  const denied = await mcp.call("tools/call", { name: "erp_restricted_shutdown_turbine", arguments: { asset: "T3" } });
  expect(denied.status).toBe(400);
  expect(JSON.stringify(denied.error ?? {})).toContain("Unknown tool");
  const foreign = new RawMCP("sc", "ex-e2e");
  expect(await foreign.open()).toBe(403);
});

test("a slow tool streams progress notifications through the gateway", async () => {
  const mcp = new RawMCP("sc", "sc-e2e");
  await mcp.open();
  const res = await mcp.call("tools/call", { name: "erp_quote_freight", arguments: { part: "SKF 23148", destination: "Foz do Iguaçu" }, _meta: { progressToken: "e2e-1" } });
  expect(res.status).toBe(200);
  expect(res.progress.map((p) => p.params.progress)).toEqual([1, 2, 3, 4]);
  expect(res.result.content[0].text).toContain("Best quote for SKF 23148");
});

test("a run uses MCP: resources, tools, progress, a refused write tool, and the model's tool choice", async () => {
  test.setTimeout(150_000);
  const stream = await openEvents();
  const [runId] = await startRun({ scenario: "sc", speed: 8 });
  const evs = await collectRun(stream, runId, 120_000);
  await stream.close();
  const kinds = new Map<string, number>();
  for (const e of evs) kinds.set(e.kind, (kinds.get(e.kind) ?? 0) + 1);
  for (const k of ["mcp_request", "mcp_response", "mcp_progress", "mcp_denied", "mcp_server_call"]) expect(kinds.get(k) ?? 0, k).toBeGreaterThan(0);
  expect(evs.some((e) => e.kind === "mcp_request" && e.method === "resources/read")).toBeTruthy();
  // The LLM agent let the model pick a tool, or (Ollama down) fell back to the script.
  expect((kinds.get("llm_tool_choice") ?? 0) + (kinds.get("llm_unavailable") ?? 0)).toBeGreaterThan(0);
  // The refused write tool never reached the server.
  expect(evs.some((e) => e.kind === "mcp_server_call" && JSON.stringify(e.payload).includes("restricted_"))).toBeFalsy();
  // The gateway's own log names the MCP server and tool.
  expect(evs.some((e) => e.kind === "gateway_access" && e.method?.startsWith("MCP tools/call"))).toBeTruthy();
});
