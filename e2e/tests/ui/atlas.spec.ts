import { expect, test, type Page } from "@playwright/test";
import { VIZ_URL } from "../helpers/env";

async function open(page: Page) {
  await page.goto("/");
  await expect(page.getByTestId("scenario-chips").locator("li")).toHaveCount(5);
  await expect(page.locator(".atlas-pin")).toHaveCount(35); // 5 orchestrators + 20 agents + 10 MCP servers
  await expect(page.getByTestId("connection")).toHaveAttribute("data-state", "live");
}

async function run(page: Page, scenario: string, opts: { breakpoints?: boolean } = {}) {
  await page.getByLabel("Scenario to run").selectOption(scenario);
  await page.getByLabel("Agent speed").selectOption("4");
  const bp = page.getByRole("checkbox", { name: "Breakpoints" });
  if (opts.breakpoints) await bp.check();
  else await bp.uncheck();
  await page.getByTestId("run-button").click();
}

test("map, chips and legend render; the gateway hub sits above the map", async ({ page }) => {
  await open(page);
  await expect(page.locator("canvas.atlas-flows")).toHaveCount(1);
  await expect(page.getByText("Legend")).toBeVisible();
  await expect(page.locator('.atlas-pin--orchestrator')).toHaveCount(5);
});

test("a run animates: events, narration, pin states, chip progress", async ({ page }) => {
  await open(page);
  await run(page, "sc");
  await expect(page.getByTestId("narration")).toContainText("Service Desk", { timeout: 20_000 });
  await expect(page.locator('[data-kind="card_fetch"]').first()).toBeVisible({ timeout: 20_000 });
  await expect(page.locator(".atlas-pin--working").first()).toBeVisible({ timeout: 20_000 });
  await expect(page.getByTestId("chip-sc")).toContainText("finished", { timeout: 90_000 });
  await expect(page.locator('[data-kind="policy_denied"]').first()).toBeVisible();
});

test("replay: pause, step, inspect payload and task, go live again", async ({ page }) => {
  await open(page);
  await run(page, "pm");
  await expect(page.getByTestId("chip-pm")).toContainText("finished", { timeout: 90_000 });
  await page.getByTestId("play-toggle").click(); // pause → replay mode
  await expect(page.getByTestId("live-button")).not.toHaveClass(/border-critical/);
  const before = await page.getByTestId("playhead-clock").textContent();
  await page.locator('[data-kind="card_fetch"]').first().click();
  await expect(page.getByTestId("payload-panel")).toContainText("rewrote");
  await expect(page.getByTestId("json-view")).toContainText("supportedInterfaces");
  expect(await page.getByTestId("playhead-clock").textContent()).not.toBe(before);
  await page.getByTestId("step-forward").click();
  await expect(page.getByTestId("payload-panel")).toBeVisible();
  // A task's state machine.
  await page.locator('[data-kind="task_state"]').first().click();
  await page.getByTestId("tab-task").click();
  await expect(page.getByTestId("task-panel").locator("svg[role=img]")).toBeVisible();
  await page.getByTestId("live-button").click();
  await expect(page.getByTestId("live-button")).toHaveClass(/border-critical/);
});

test("solo a scenario dims the others", async ({ page }) => {
  await open(page);
  await page.getByTestId("chip-tr").click();
  await expect(page.getByTestId("chip-tr")).toHaveAttribute("aria-pressed", "true");
  const opacities = await page.locator(".leaflet-marker-icon").evaluateAll((els) => els.map((e) => (e as HTMLElement).style.opacity));
  expect(opacities.filter((o) => o === "1")).toHaveLength(7);
  expect(opacities.filter((o) => o === "0.22")).toHaveLength(28);
  await page.getByTestId("chip-tr").click();
});

test("breakpoints: the gate prompt holds the run until Continue", async ({ page }) => {
  // Abort runs left waiting at a gate by earlier (failed) attempts.
  for (let i = 0; i < 3; i++) {
    await page.request.post(`${VIZ_URL}/api/gate/release`, { data: { all: true, action: "abort" } });
    await page.waitForTimeout(500);
  }
  await open(page);
  // Live mode plays 1.5 s behind: wait until the aborted gates are gone from the UI too.
  await expect(page.getByTestId("gate-prompt")).toHaveCount(0, { timeout: 15_000 });
  await run(page, "ex", { breakpoints: true });
  const prompt = page.getByTestId("gate-prompt");
  const credit = prompt.locator("li", { hasText: "before credit" });
  await expect(credit).toBeVisible({ timeout: 20_000 });
  await credit.getByTestId("gate-continue").click();
  // The credit step runs the model with MCP tools; give it time.
  await expect(prompt).toContainText("before export", { timeout: 90_000 });
  // Let the rest of the run go: keep releasing gates through the API until it finishes.
  await expect
    .poll(
      async () => {
        await page.request.post(`${VIZ_URL}/api/gate/release`, { data: { all: true } });
        return page.getByTestId("chip-ex").textContent();
      },
      { timeout: 120_000, intervals: [1000] },
    )
    .toContain("finished");
});

test("clicking an agent shows its live card through the gateway", async ({ page }) => {
  await open(page);
  await page.locator('.atlas-pin[data-agent="Logistics"]').click();
  await expect(page.getByTestId("agent-panel")).toContainText("push notifications");
  await expect(page.getByTestId("agent-panel").getByTestId("json-view")).toContainText("localhost:8000/agents/sc-logistics/invoke");
});

test("the bundled sample replays without a live run", async ({ page }) => {
  await open(page);
  await page.getByTestId("source-menu").selectOption("sample");
  await expect(page.getByTestId("connection")).toHaveAttribute("data-state", "replay");
  await page.getByTestId("play-toggle").click();
  await page.waitForTimeout(2000);
  await page.getByTestId("play-toggle").click();
  await expect(page.getByTestId("event-list").locator("li").first()).toBeVisible();
  await page.getByTestId("source-menu").selectOption("live");
  await expect(page.getByTestId("connection")).toHaveAttribute("data-state", "live");
});

test("MCP servers are hexagons with their tools, resources and users", async ({ page }) => {
  await open(page);
  await expect(page.locator(".atlas-pin--mcp")).toHaveCount(10);
  await page.getByTestId("zoom-europe").click();
  await page.locator('.atlas-pin[data-agent="Tariff & Sanctions DB"]').click();
  const panel = page.getByTestId("mcp-panel");
  await expect(panel).toContainText("tariffs_file_declaration(order)");
  await expect(panel).toContainText("tariffs+tariffs://ex/rules/dual-use");
  await expect(panel).toContainText("Credit Check");
  await page.getByRole("button", { name: "World" }).click();
});

test("MCP events have their own filter and explanations", async ({ page }) => {
  await open(page);
  await page.getByLabel("Scenario to run").selectOption("tr");
  await page.getByLabel("Agent speed").selectOption("4");
  await page.getByRole("checkbox", { name: "Breakpoints" }).uncheck();
  await page.getByTestId("run-button").click();
  await expect(page.locator('[data-kind="mcp_progress"]').first()).toBeVisible({ timeout: 60_000 });
  await page.getByRole("button", { name: "A2A", exact: true }).click(); // hide A2A, keep MCP
  await page.locator('[data-kind="mcp_denied"]').first().click({ timeout: 60_000 });
  await expect(page.getByTestId("payload-panel")).toContainText("hides write tools from tools/list");
  await page.getByRole("button", { name: "A2A", exact: true }).click();
});
