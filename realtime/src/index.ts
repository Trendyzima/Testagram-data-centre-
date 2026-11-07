export interface Env {
  REALTIME: DurableObjectNamespace;
  REALTIME_TICKET_SECRET: string;
  REALTIME_PUBLISH_SECRET: string;
  REALTIME_ALLOWED_ORIGINS: string;
}

type TicketClaims = {
  iss: string;
  aud: string;
  sub: string;
  conversation_id: string;
  scope: string;
  jti: string;
  iat: number;
  exp: number;
};

type RealtimeEvent = {
  event_id: string;
  conversation_id: string;
  event_type: "message.created" | "message.updated" | "message.deleted" | "conversation.updated";
  occurred_at: string;
  payload: Record<string, unknown>;
};

const MAX_EVENT_BYTES = 64 * 1024;
const encoder = new TextEncoder();

function b64urlToBytes(value: string): Uint8Array {
  const padded = value.replace(/-/g, "+").replace(/_/g, "/") + "=".repeat((4 - value.length % 4) % 4);
  const binary = atob(padded);
  return Uint8Array.from(binary, (c) => c.charCodeAt(0));
}

function allowedOrigin(origin: string | null, env: Env): boolean {
  if (!origin) return false;
  return env.REALTIME_ALLOWED_ORIGINS.split(",").map((v) => v.trim()).filter(Boolean).includes(origin);
}

async function verifyHmac(secret: string, message: Uint8Array, signature: string): Promise<boolean> {
  if (!secret || secret.length < 32 || !signature) return false;
  try {
    const key = await crypto.subtle.importKey("raw", encoder.encode(secret), { name: "HMAC", hash: "SHA-256" }, false, ["verify"]);
    return await crypto.subtle.verify("HMAC", key, b64urlToBytes(signature), message);
  } catch {
    return false;
  }
}

async function verifyTicket(secret: string, token: string, conversationID: string): Promise<TicketClaims | null> {
  const parts = token.split(".");
  if (parts.length !== 2 || parts[0].length > 4096 || parts[1].length > 128) return null;
  if (!(await verifyHmac(secret, encoder.encode(parts[0]), parts[1]))) return null;
  try {
    const claims = JSON.parse(new TextDecoder().decode(b64urlToBytes(parts[0]))) as TicketClaims;
    const now = Math.floor(Date.now() / 1000);
    if (
      claims.iss !== "testagram-backend" ||
      claims.aud !== "testagram-realtime" ||
      claims.scope !== "conversation:read" ||
      !claims.sub || !claims.jti ||
      claims.conversation_id !== conversationID ||
      !Number.isSafeInteger(claims.iat) || !Number.isSafeInteger(claims.exp) ||
      claims.iat > now + 30 || claims.exp <= now ||
      claims.exp <= claims.iat || claims.exp - claims.iat > 300
    ) return null;
    return claims;
  } catch {
    return null;
  }
}

function json(data: unknown, status = 200): Response {
  return new Response(JSON.stringify(data), { status, headers: { "content-type": "application/json; charset=utf-8", "cache-control": "no-store" } });
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url);

    if (url.pathname === "/internal/v1/realtime/publish" && request.method === "POST") {
      const raw = new Uint8Array(await request.arrayBuffer());
      if (raw.byteLength === 0 || raw.byteLength > MAX_EVENT_BYTES) return json({ error: "invalid event size" }, 413);
      const signature = request.headers.get("x-testagram-signature") ?? "";
      if (!(await verifyHmac(env.REALTIME_PUBLISH_SECRET, raw, signature))) return json({ error: "unauthorized" }, 401);
      let event: RealtimeEvent;
      try { event = JSON.parse(new TextDecoder().decode(raw)) as RealtimeEvent; }
      catch { return json({ error: "invalid event" }, 400); }
      if (
        !event.event_id || event.event_id.length > 128 ||
        !event.conversation_id || event.conversation_id.length > 128 ||
        !["message.created", "message.updated", "message.deleted", "conversation.updated"].includes(event.event_type) ||
        !event.occurred_at || typeof event.payload !== "object" || event.payload === null
      ) return json({ error: "invalid event envelope" }, 400);
      const id = env.REALTIME.idFromName(event.conversation_id);
      return env.REALTIME.get(id).fetch("https://realtime.internal/publish", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: raw,
      });
    }

    const match = url.pathname.match(/^\/v1\/realtime\/conversations\/([A-Za-z0-9_-]{1,128})$/);
    if (!match) return json({ error: "not found" }, 404);
    if (request.method !== "GET" || request.headers.get("Upgrade")?.toLowerCase() !== "websocket") {
      return json({ error: "websocket upgrade required" }, 426);
    }
    if (!allowedOrigin(request.headers.get("Origin"), env)) return json({ error: "origin not allowed" }, 403);
    const authorization = request.headers.get("Authorization") ?? "";
    if (!authorization.startsWith("Bearer ")) return json({ error: "realtime ticket required" }, 401);
    const claims = await verifyTicket(env.REALTIME_TICKET_SECRET, authorization.slice(7).trim(), match[1]);
    if (!claims) return json({ error: "invalid or expired realtime ticket" }, 401);

    const id = env.REALTIME.idFromName(match[1]);
    return env.REALTIME.get(id).fetch("https://realtime.internal/connect", {
      method: "GET",
      headers: {
        Upgrade: "websocket",
        "x-testagram-conversation-id": claims.conversation_id,
        "x-testagram-subject": claims.sub,
      },
    });
  },
};

export class ConversationRoom {
  private readonly state: DurableObjectState;

  constructor(state: DurableObjectState, _env: Env) {
    this.state = state;
  }

  async fetch(request: Request): Promise<Response> {
    const url = new URL(request.url);
    if (url.pathname === "/connect") {
      if (request.headers.get("Upgrade")?.toLowerCase() !== "websocket") return json({ error: "websocket required" }, 426);
      const conversationID = request.headers.get("x-testagram-conversation-id") ?? "";
      const subject = request.headers.get("x-testagram-subject") ?? "";
      if (!conversationID || !subject) return json({ error: "missing verified identity" }, 401);
      const pair = new WebSocketPair();
      const client = pair[0];
      const server = pair[1];
      this.state.acceptWebSocket(server, [conversationID]);
      server.serializeAttachment({ conversationID, subject });
      return new Response(null, { status: 101, webSocket: client });
    }

    if (url.pathname === "/publish" && request.method === "POST") {
      const raw = new Uint8Array(await request.arrayBuffer());
      if (raw.byteLength === 0 || raw.byteLength > MAX_EVENT_BYTES) return json({ error: "invalid event size" }, 413);
      let event: RealtimeEvent;
      try { event = JSON.parse(new TextDecoder().decode(raw)) as RealtimeEvent; }
      catch { return json({ error: "invalid event" }, 400); }
      if (!event.event_id || !event.conversation_id || typeof event.payload !== "object" || event.payload === null) {
        return json({ error: "invalid event envelope" }, 400);
      }
      const envelope = JSON.stringify({ type: event.event_type, event_id: event.event_id, occurred_at: event.occurred_at, payload: event.payload });
      let delivered = 0;
      for (const socket of this.state.getWebSockets(event.conversation_id)) {
        try { socket.send(envelope); delivered++; }
        catch { try { socket.close(1011, "delivery failed"); } catch { /* closed socket */ } }
      }
      return json({ accepted: true, delivered });
    }
    return json({ error: "not found" }, 404);
  }

  webSocketMessage(socket: WebSocket, message: string | ArrayBuffer): void {
    // Client sockets are receive-only for application events. All durable writes
    // must go through Backend/Neon before Backend signs a publish event.
    if (typeof message === "string" && message.length <= 128) {
      try {
        const value = JSON.parse(message) as { type?: string };
        if (value.type === "ping") { socket.send(JSON.stringify({ type: "pong", at: new Date().toISOString() })); return; }
      } catch { /* invalid client frame */ }
    }
    try { socket.close(1008, "client frames are not accepted"); } catch { /* closed socket */ }
  }

  webSocketClose(socket: WebSocket, code: number, reason: string, wasClean: boolean): void {
    try { socket.close(code, reason); } catch { /* already closed */ }
  }

  webSocketError(socket: WebSocket): void {
    try { socket.close(1011, "websocket error"); } catch { /* already closed */ }
  }
}
