import { expect, test } from "@playwright/test";
import { GATEWAY_URL, VIZ_URL } from "../helpers/env";

interface Scenario {
  code: string;
  orchestrator: { id: string };
  agents: { id: string; feature: string; skills: { id: string }[] }[];
}

let scenarios: Scenario[] = [];

test.beforeAll(async () => {
  scenarios = await (await fetch(`${VIZ_URL}/api/scenarios`)).json();
});

test("five scenarios with four agents each", () => {
  expect(scenarios.map((s) => s.code).sort()).toEqual(["ex", "kh", "pm", "sc", "tr"]);
  for (const s of scenarios) expect(s.agents).toHaveLength(4);
});

test("the gateway rewrites every agent card to point at itself", async () => {
  for (const s of scenarios) {
    for (const a of s.agents) {
      const res = await fetch(`${GATEWAY_URL}/agents/${a.id}/.well-known/agent-card.json`);
      expect(res.status, a.id).toBe(200);
      const card = await res.json();
      expect(card.supportedInterfaces[0].url).toBe(`${GATEWAY_URL}/agents/${a.id}/invoke`);
      expect(card.capabilities).toMatchObject({ streaming: true, pushNotifications: true });
    }
  }
});

test("restricted routes only accept their orchestrator and no restricted- skills", async () => {
  const s = scenarios.find((x) => x.code === "sc")!;
  const agent = s.agents.find((a) => a.feature === "restricted")!;
  const allowed = agent.skills.find((k) => !k.id.startsWith("restricted-"))!.id;
  const denied = agent.skills.find((k) => k.id.startsWith("restricted-"))!.id;
  const call = (caller: string, skill: string) =>
    fetch(`${GATEWAY_URL}/agents/${agent.id}/invoke`, {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-Caller": caller },
      body: JSON.stringify({
        jsonrpc: "2.0",
        id: 1,
        method: "SendMessage",
        params: { message: { messageId: `e2e-${Date.now()}-${caller}-${skill}`, role: "ROLE_USER", parts: [{ text: "e2e" }], metadata: { skill, speed: 50 } } },
      }),
    });
  expect((await call(s.orchestrator.id, denied)).status).toBe(403);
  expect((await call("ex-orchestrator", allowed)).status).toBe(403);
  const ok = await call(s.orchestrator.id, allowed);
  expect(ok.status).toBe(200);
  expect((await ok.json()).result.task.status.state).toBe("TASK_STATE_COMPLETED");
});
