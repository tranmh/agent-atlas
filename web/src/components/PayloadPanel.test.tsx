import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { PayloadPanel } from "./PayloadPanel";

describe("PayloadPanel", () => {
  it("explains the event and shows its payload", () => {
    render(
      <PayloadPanel
        scenarios={[]}
        onTask={vi.fn()}
        event={{ id: 7, t: 1, ts: "", source: "orchestrator", kind: "policy_denied", scenario: "sc", http_status: 403, note: "refused", payload: { status: 403, body: "authorization failed" } }}
      />,
    );
    expect(screen.getByText(/CEL authorization policy/)).toBeInTheDocument();
    expect(screen.getAllByText("403")).toHaveLength(2);
    expect(screen.getByTestId("json-view")).toHaveTextContent('"body": "authorization failed"');
  });

  it("links gateway events to Jaeger", () => {
    render(<PayloadPanel scenarios={[]} onTask={vi.fn()} event={{ id: 8, t: 1, ts: "", source: "gateway", kind: "gateway_access", payload: { "trace.id": "abc" } }} />);
    expect(screen.getByText("open in Jaeger")).toHaveAttribute("href", "http://localhost:16686/trace/abc");
  });
});
