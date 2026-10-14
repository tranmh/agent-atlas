import { GATEWAY_URL } from "./env";

/** A minimal raw MCP (Streamable HTTP) client for tests: JSON-RPC over POST, SSE or JSON answers. */
export class RawMCP {
  private session = "";
  private id = 0;
  constructor(
    private readonly code: string,
    private readonly caller: string,
  ) {}

  async post(method: string, params?: unknown, notification = false): Promise<{ status: number; messages: any[] }> {
    const body: Record<string, unknown> = { jsonrpc: "2.0", method };
    if (params !== undefined) body.params = params;
    if (!notification) body.id = ++this.id;
    const headers: Record<string, string> = {
      "Content-Type": "application/json",
      Accept: "application/json, text/event-stream",
      "X-Caller": this.caller,
      "Mcp-Protocol-Version": "2025-06-18",
    };
    if (this.session) headers["Mcp-Session-Id"] = this.session;
    const res = await fetch(`${GATEWAY_URL}/mcp/${this.code}`, { method: "POST", headers, body: JSON.stringify(body) });
    this.session = res.headers.get("mcp-session-id") ?? this.session;
    const text = await res.text();
    const messages: any[] = [];
    if ((res.headers.get("content-type") ?? "").includes("text/event-stream")) {
      for (const block of text.split(/\r?\n\r?\n/)) {
        const data = block
          .split(/\r?\n/)
          .filter((l) => l.startsWith("data:"))
          .map((l) => l.slice(5).trim())
          .join("\n");
        if (data) messages.push(JSON.parse(data));
      }
    } else if (text.trim()) {
      try {
        messages.push(JSON.parse(text));
      } catch {
        messages.push({ text });
      }
    }
    return { status: res.status, messages };
  }

  async open(): Promise<number> {
    const r = await this.post("initialize", { protocolVersion: "2025-06-18", capabilities: {}, clientInfo: { name: "e2e", version: "1" } });
    if (r.status === 200) await this.post("notifications/initialized", {}, true);
    return r.status;
  }

  /** The final JSON-RPC response of a call (after any progress notifications). */
  async call(method: string, params?: unknown): Promise<{ status: number; result?: any; error?: any; progress: any[] }> {
    const r = await this.post(method, params);
    const final = r.messages.find((m) => m.id !== undefined) ?? {};
    return { status: r.status, result: final.result, error: final.error, progress: r.messages.filter((m) => m.method === "notifications/progress") };
  }
}
