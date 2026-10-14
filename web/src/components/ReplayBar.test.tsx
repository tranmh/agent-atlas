import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ReplayBar } from "./ReplayBar";

const snapshot = { version: 1, mode: "replay" as const, playing: false, speed: 1, cursor: 4, count: 10, start: 1000, end: 11000 };

describe("ReplayBar", () => {
  it("drives the engine", () => {
    const h = { onToggle: vi.fn(), onStepBack: vi.fn(), onStepForward: vi.fn(), onSeek: vi.fn(), onSpeed: vi.fn(), onLive: vi.fn() };
    render(<ReplayBar snapshot={snapshot} playhead={6000} canLive {...h} />);
    expect(screen.getByTestId("play-toggle")).toHaveTextContent("Play");
    fireEvent.click(screen.getByTestId("play-toggle"));
    fireEvent.click(screen.getByTestId("step-forward"));
    fireEvent.change(screen.getByTestId("scrubber"), { target: { value: "2000" } });
    fireEvent.change(screen.getByLabelText("Replay speed"), { target: { value: "0.25" } });
    fireEvent.click(screen.getByTestId("live-button"));
    expect(h.onToggle).toHaveBeenCalled();
    expect(h.onStepForward).toHaveBeenCalled();
    expect(h.onSeek).toHaveBeenCalledWith(3000);
    expect(h.onSpeed).toHaveBeenCalledWith(0.25);
    expect(h.onLive).toHaveBeenCalled();
    expect(screen.getByText("5/10")).toBeInTheDocument();
  });
});
