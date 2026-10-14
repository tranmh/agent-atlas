import { expect, test } from "@playwright/test";
import { VIZ_URL } from "../helpers/env";

/** `make screenshots`: docs/screenshots/*.png from the running stack. */
const out = (name: string) => `../docs/screenshots/${name}.png`;

test("screenshots", async ({ page }) => {
  test.setTimeout(300_000);
  await page.goto("/");
  await expect(page.locator(".atlas-pin")).toHaveCount(35);
  await page.getByLabel("Scenario to run").selectOption("all");
  await page.getByLabel("Agent speed").selectOption("1");
  await page.getByRole("checkbox", { name: "Chaos" }).check();
  await page.getByRole("checkbox", { name: "Breakpoints" }).uncheck();
  await page.getByTestId("run-button").click();
  await page.waitForTimeout(14_000);
  await page.screenshot({ path: out("world-all-scenarios") });

  await page.getByTestId("zoom-central").click();
  await page.waitForTimeout(6_000);
  await page.screenshot({ path: out("central-europe") });

  // Wait for the runs to finish, then inspect in replay mode.
  await expect(page.getByTestId("chip-sc")).toContainText("finished", { timeout: 180_000 });
  await expect(page.getByTestId("chip-tr")).toContainText("finished", { timeout: 180_000 });
  await page.getByTestId("play-toggle").click();
  await page.getByTestId("chip-sc").click(); // solo the service case
  await page.locator('[data-kind="card_fetch"]').first().click();
  await page.waitForTimeout(800);
  await page.screenshot({ path: out("agent-card-rewritten") });

  await page.locator('[data-kind="policy_denied"]').first().click();
  await page.waitForTimeout(1200);
  await page.screenshot({ path: out("policy-denied") });

  await page.locator('[data-kind="task_state"]', { hasText: "input-required → completed" }).first().click();
  await page.getByTestId("tab-task").click();
  await page.waitForTimeout(500);
  await page.screenshot({ path: out("task-state-machine") });

  await page.getByTestId("tab-gateway").click();
  await page.waitForTimeout(500);
  await page.screenshot({ path: out("gateway-log") });

  // MCP: the model's tool choice, a refused write tool, an MCP server.
  const choice = page.locator('[data-kind="llm_tool_choice"]').first();
  if (await choice.count()) {
    await choice.click();
    await page.waitForTimeout(1200);
    await page.screenshot({ path: out("mcp-tool-choice") });
  }
  const denied = page.locator('[data-kind="mcp_denied"]').first();
  if (await denied.count()) {
    await denied.click();
    await page.waitForTimeout(1200);
    await page.screenshot({ path: out("mcp-denied") });
  }
  await page.getByRole("button", { name: "World" }).click();
  await page.waitForTimeout(800);
  await page.locator('.atlas-pin[data-agent="ERP & Spare Parts"]').click();
  await page.waitForTimeout(600);
  await page.screenshot({ path: out("mcp-server") });
  await page.getByTestId("chip-sc").click();

  // Breakpoint prompt, live.
  await page.getByTestId("live-button").click();
  await page.getByLabel("Scenario to run").selectOption("pm");
  await page.getByRole("checkbox", { name: "Chaos" }).uncheck();
  await page.getByRole("checkbox", { name: "Breakpoints" }).check();
  await page.getByTestId("run-button").click();
  await expect(page.getByTestId("gate-prompt")).toBeVisible({ timeout: 20_000 });
  await page.getByTestId("gate-continue").click();
  await expect(page.getByTestId("gate-prompt")).toContainText("before plan", { timeout: 30_000 });
  await page.waitForTimeout(1500);
  await page.screenshot({ path: out("breakpoint") });
  await expect
    .poll(
      async () => {
        await page.request.post(`${VIZ_URL}/api/gate/release`, { data: { all: true } });
        return page.getByTestId("chip-pm").textContent();
      },
      { timeout: 120_000, intervals: [1000] },
    )
    .toContain("finished");
});
