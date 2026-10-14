import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { Scenario } from "@/events/schema";
import { MCPServerPanel, usersOf } from "./MCPServerPanel";

const scenario = {
  id: "service-case",
  code: "sc",
  title: "Service case",
  summary: "",
  color: "#2a78d6",
  closing: "",
  plan: [],
  orchestrator: { id: "sc-orchestrator", name: "Desk", description: "", city: "G", country: "DE", lat: 1, lon: 1 },
  agents: [
    { id: "sc-field-service", name: "Field Service", description: "", city: "F", country: "BR", lat: 1, lon: 1, feature: "restricted", mode: "scripted", skills: [],
      script: [{ tool: { server: "sc-erp", name: "restricted_shutdown_turbine", expect: "denied" } }, { tool: { server: "sc-erp", name: "list_technicians" } }] },
    { id: "sc-diagnosis", name: "Diagnosis", description: "", city: "G", country: "DE", lat: 1, lon: 1, feature: "streaming", mode: "llm", skills: [],
      script: [{ tools: ["sc-historian", "sc-erp"], prompt_from: { server: "sc-historian", name: "diagnose" } }] },
  ],
  mcp_servers: [
    { id: "sc-erp", name: "ERP & Spare Parts", description: "Stock.", city: "São Paulo", country: "BR", lat: 1, lon: 1, role: "ops", resources: [], prompts: [],
      tools: [
        { name: "list_technicians", description: "Technicians", params: [{ name: "region", type: "string" }] },
        { name: "restricted_shutdown_turbine", description: "Shut down", params: [{ name: "asset", type: "string" }], write: true },
      ] },
  ],
} as unknown as Scenario;

describe("MCPServerPanel", () => {
  it("lists who uses a server and how", () => {
    expect(usersOf(scenario, "sc-erp")).toEqual([
      { agent: "Field Service", how: ["tries restricted_shutdown_turbine (refused)", "calls list_technicians"] },
      { agent: "Diagnosis", how: ["lets the model pick tools"] },
    ]);
  });

  it("shows prefixed tool names and flags the write tool", () => {
    render(<MCPServerPanel server={scenario.mcp_servers[0]} scenario={scenario} scenarios={[scenario]} view={undefined} />);
    expect(screen.getByText("erp_list_technicians(region)")).toBeInTheDocument();
    expect(screen.getByText("hidden and refused by the gateway")).toBeInTheDocument();
  });
});
