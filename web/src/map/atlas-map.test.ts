import { describe, expect, it } from "vitest";
import type { Scenario } from "@/events/schema";
import { emptyView } from "@/replay/reduce";
import { AtlasMap } from "./atlas-map";

const scenario: Scenario = {
  id: "service-case",
  code: "sc",
  title: "Service case",
  summary: "",
  color: "#2a78d6",
  closing: "",
  orchestrator: { id: "sc-orchestrator", name: "Service Desk", description: "", city: "Göppingen", country: "DE", lat: 48.7, lon: 9.65 },
  agents: [{ id: "sc-diagnosis", name: "Diagnosis", description: "", city: "Göppingen", country: "DE", lat: 48.7, lon: 9.65, feature: "streaming", mode: "llm", skills: [] }],
  plan: [],
};

function mountMap(): { m: AtlasMap; el: HTMLDivElement } {
  const el = document.createElement("div");
  el.style.width = "800px";
  el.style.height = "600px";
  document.body.appendChild(el);
  const m = new AtlasMap().mount(el, { fetch: (async () => ({ ok: false })) as unknown as typeof fetch });
  return { m, el };
}

describe("AtlasMap", () => {
  it("creates one pin per orchestrator and agent", () => {
    const { m, el } = mountMap();
    m.setScenarios([scenario]);
    expect(el.querySelectorAll(".atlas-pin")).toHaveLength(2);
    m.destroy();
  });

  // Review W2: pins created after the view was folded must show the folded state.
  it("applies the last view to pins created afterwards", () => {
    const { m, el } = mountMap();
    const view = emptyView();
    view.agents.set("sc-diagnosis", { status: "completed" });
    m.setView(view);
    m.setScenarios([scenario]);
    m.draw({ playhead: 0, flights: [], view });
    expect(el.querySelector('[data-agent="Diagnosis"]')!.className).toContain("atlas-pin--completed");
    m.destroy();
  });
});
