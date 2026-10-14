import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { border, path, TaskStatePanel } from "./TaskStatePanel";

describe("TaskStatePanel", () => {
  it("clips edges at the pill border", () => {
    expect(border({ x: 0, y: 0 }, { x: 100, y: 0 }).x).toBeCloseTo(49);
    expect(border({ x: 0, y: 0 }, { x: 0, y: 100 }).y).toBeCloseTo(16);
  });

  it("infers working between input-required and completed", () => {
    const p = path(["input-required", "completed"]);
    expect([...p.inferred]).toEqual(["working"]);
    expect([...p.taken].sort()).toEqual(["input-required>working", "working>completed"]);
    expect(path(["submitted", "working", "completed"]).inferred.size).toBe(0);
  });

  it("marks the current state and lists the history", () => {
    render(
      <TaskStatePanel
        scenarios={[]}
        onEvent={() => undefined}
        task={{
          taskId: "t-123456789012345",
          agent: "sc-spare-parts",
          scenario: "sc",
          runId: "r1",
          step: "parts",
          current: "completed",
          history: [
            { state: "input-required", t: 1000, eventId: 1 },
            { state: "note:working", t: 1500, note: "Checking stock", eventId: 2 },
            { state: "completed", t: 2000, eventId: 3 },
          ],
        }}
      />,
    );
    expect(screen.getByRole("img", { name: /current state completed/ })).toBeInTheDocument();
    expect(document.querySelector('[data-current="true"]')?.getAttribute("data-state")).toBe("completed");
    expect(screen.getByText("“Checking stock”")).toBeInTheDocument();
  });

  it("explains when there is no task", () => {
    render(<TaskStatePanel scenarios={[]} onEvent={() => undefined} task={null} />);
    expect(screen.getByText(/never become tasks/)).toBeInTheDocument();
  });
});
