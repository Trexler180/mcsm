import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Node } from "@/lib/types";
import {
  isMemoryAllocationValid,
  MemoryAllocation,
} from "./memory-allocation";

const node = {
  id: "node-1",
  name: "Primary node",
  fqdn: "localhost",
  port: 8080,
  scheme: "http",
  memory_mb: 16_384,
  mem_used_mb: 10_240,
  disk_gb: null,
  cpu_cores: null,
  location: null,
  created_at: "2026-08-10T00:00:00Z",
  last_seen: "2026-08-10T00:00:00Z",
  disk_used_gb: null,
  cpu_pct: null,
  uptime_seconds: null,
  os: null,
  arch: null,
  agent_version: null,
} satisfies Node;

afterEach(cleanup);

describe("MemoryAllocation", () => {
  it("uses currently free memory as the slider range and shows host totals", () => {
    render(<MemoryAllocation node={node} value="4096" onChange={vi.fn()} />);

    expect(screen.getByText("6 GB free")).toBeInTheDocument();
    expect(screen.getByText("10 GB used · 16 GB total")).toBeInTheDocument();
    expect(screen.getByText("6 GB available")).toBeInTheDocument();
    expect(screen.getByLabelText("Maximum RAM slider")).toHaveAttribute(
      "max",
      "6144",
    );
    expect(screen.getByText(/host keeps 2 GB free/)).toBeInTheDocument();
  });

  it("updates the maximum and keeps starting RAM below it", () => {
    const onChange = vi.fn();
    const onMinimumChange = vi.fn();
    render(
      <MemoryAllocation
        node={node}
        value="4096"
        onChange={onChange}
        minimumValue="2048"
        onMinimumChange={onMinimumChange}
      />,
    );

    fireEvent.change(screen.getByLabelText("Maximum RAM slider"), {
      target: { value: "1024" },
    });
    expect(onChange).toHaveBeenCalledWith("1024");
    expect(onMinimumChange).toHaveBeenCalledWith("1024");
  });

  it("warns when a typed allocation is above free memory", () => {
    render(<MemoryAllocation node={node} value="8192" onChange={vi.fn()} />);
    expect(screen.getByText(/2 GB over currently free memory/)).toBeInTheDocument();
  });

  it("uses physical capacity when live usage is unavailable", () => {
    render(
      <MemoryAllocation
        node={{ ...node, mem_used_mb: null }}
        value="4096"
        onChange={vi.fn()}
      />,
    );
    expect(screen.getByText("16 GB total")).toBeInTheDocument();
    expect(screen.getByText("Live usage unavailable")).toBeInTheDocument();
    expect(screen.getByText("16 GB total capacity")).toBeInTheDocument();
    expect(screen.getByLabelText("Maximum RAM slider")).toBeEnabled();
    expect(screen.getByLabelText("Maximum RAM slider")).toHaveAttribute(
      "max",
      "16384",
    );
  });
});

describe("isMemoryAllocationValid", () => {
  it("rejects a minimum above maximum and allocations above host total", () => {
    expect(isMemoryAllocationValid("2048", "4096", 16_384)).toBe(false);
    expect(isMemoryAllocationValid("32768", "512", 16_384)).toBe(false);
    expect(isMemoryAllocationValid("4096", "512", 16_384)).toBe(true);
  });
});
