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
      !["conversation:read", "media:signal"].includes(claims.scope) ||
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

async function readBoundedBody(request: Request, maximum: number): Promise<Uint8Array> {
  const reader = request.body?.getReader();
  if (!reader) return new Uint8Array();
  const chunks: Uint8Array[] = [];
  let total = 0;
  try {
    while (true) {
      const part = await reader.read();
      if (part.done) break;
      total += part.value.byteLength;
      if (total > maximum) {
        await reader.cancel();
        throw new Error("request body too large");
      }
      chunks.push(part.value);
    }
  } finally {
    try { reader.releaseLock(); } catch { /* reader already released */ }
  }
  const result = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) { result.set(chunk, offset); offset += chunk.byteLength; }
  return result;
}

function json(data: unknown, status = 200): Response {
  return new Response(JSON.stringify(data), { status, headers: { "content-type": "application/json; charset=utf-8", "cache-control": "no-store" } });
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url);

    if (url.pathname === "/internal/v1/realtime/publish" && request.method === "POST") {
      let raw: Uint8Array;
      try { raw = await readBoundedBody(request, MAX_EVENT_BYTES); }
      catch { return json({ error: "event body too large" }, 413); }
      if (raw.byteLength === 0) return json({ error: "empty event body" }, 400);
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

    const match = url.pathname.match(/^\/v1\/realtime\/(conversations|rooms)\/([A-Za-z0-9_-]{1,128})$/);
    if (!match) return json({ error: "not found" }, 404);
    if (request.method !== "GET" || request.headers.get("Upgrade")?.toLowerCase() !== "websocket") {
      return json({ error: "websocket upgrade required" }, 426);
    }
    if (!allowedOrigin(request.headers.get("Origin"), env)) return json({ error: "origin not allowed" }, 403);
    // The browser WebSocket API cannot set Authorization headers. Carry the
    // short-lived ticket in a requested subprotocol, but never echo that token
    // back as the negotiated protocol or place it in the URL.
    const offeredProtocols = (request.headers.get("Sec-WebSocket-Protocol") ?? "")
      .split(",").map((value) => value.trim()).filter(Boolean);
    const protocol = "testagram.realtime.v1";
    const ticketProtocols = offeredProtocols.filter((value) => value.startsWith("ticket."));
    if (!offeredProtocols.includes(protocol) || ticketProtocols.length !== 1) {
      return json({ error: "realtime protocol and ticket are required" }, 401);
    }
    const ticket = ticketProtocols[0].slice("ticket.".length);
    const roomID = match[2];
    const expectedScope = match[1] === "rooms" ? "media:signal" : "conversation:read";
    const claims = await verifyTicket(env.REALTIME_TICKET_SECRET, ticket, roomID);
    if (!claims || claims.scope !== expectedScope) return json({ error: "invalid or expired realtime ticket" }, 401);

    const id = env.REALTIME.idFromName(roomID);
    return env.REALTIME.get(id).fetch("https://realtime.internal/connect", {
      method: "GET",
      headers: {
        Upgrade: "websocket",
        "Sec-WebSocket-Protocol": protocol,
        "x-testagram-conversation-id": claims.conversation_id,
        "x-testagram-subject": claims.sub,
        "x-testagram-scope": claims.scope,
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
      const scope = request.headers.get("x-testagram-scope") ?? "";
      if (!conversationID || !subject || !["conversation:read", "media:signal"].includes(scope)) return json({ error: "missing verified identity" }, 401);
      let userConnections = 0;
      for (const socket of this.state.getWebSockets(conversationID)) {
        const attachment = socket.deserializeAttachment() as { subject?: string } | null;
        if (attachment?.subject === subject) userConnections++;
      }
      if (userConnections >= 5) return json({ error: "connection limit reached" }, 429);
      const pair = new WebSocketPair();
      const client = pair[0];
      const server = pair[1];
      this.state.acceptWebSocket(server, [conversationID]);
      server.serializeAttachment({ conversationID, subject, scope });
      if (scope === "media:signal") this.broadcastRoomPresence(conversationID);
      return new Response(null, { status: 101, webSocket: client, headers: { "Sec-WebSocket-Protocol": "testagram.realtime.v1" } });
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

  private broadcastRoomPresence(roomID: string): void {
    const sockets = this.state.getWebSockets(roomID);
    const envelope = JSON.stringify({ type: "presence", participant_count: sockets.length });
    for (const peer of sockets) {
      try { peer.send(envelope); }
      catch { try { peer.close(1011, "presence delivery failed"); } catch { /* closed */ } }
    }
  }

  webSocketMessage(socket: WebSocket, message: string | ArrayBuffer): void {
    const attachment = socket.deserializeAttachment() as { conversationID?: string; subject?: string; scope?: string } | null;
    if (typeof message !== "string" || message.length > 64 * 1024) {
      try { socket.close(1009, "signal frame too large"); } catch { /* closed */ }
      return;
    }
    let value: Record<string, unknown>;
    try { value = JSON.parse(message) as Record<string, unknown>; }
    catch { try { socket.close(1008, "invalid frame"); } catch { /* closed */ } return; }
    if (value.type === "ping" && attachment?.scope === "conversation:read") {
      try { socket.send(JSON.stringify({ type: "pong", at: new Date().toISOString() })); } catch { /* closed */ }
      return;
    }
    if (attachment?.scope !== "media:signal" || !attachment.conversationID || !attachment.subject) {
      try { socket.close(1008, "client frames are not accepted"); } catch { /* closed */ }
      return;
    }
    const type = value.type;
    let payload: Record<string, unknown> | null = null;
    if ((type === "offer" || type === "answer") && typeof value.sdp === "string" && value.sdp.length > 0 && value.sdp.length <= 32000) {
      payload = { type, sdp: value.sdp, sender_id: attachment.subject };
    } else if (type === "candidate" && typeof value.candidate === "object" && value.candidate !== null) {
      const candidate = value.candidate as Record<string, unknown>;
      if (typeof candidate.candidate === "string" && candidate.candidate.length <= 4096 &&
          (candidate.sdpMid === undefined || candidate.sdpMid === null || typeof candidate.sdpMid === "string") &&
          (candidate.sdpMLineIndex === undefined || candidate.sdpMLineIndex === null || Number.isInteger(candidate.sdpMLineIndex))) {
        payload = { type, candidate, sender_id: attachment.subject };
      }
    }
    if (!payload) { try { socket.close(1008, "unsupported signaling frame"); } catch { /* closed */ } return; }
    const envelope = JSON.stringify(payload);
    for (const peer of this.state.getWebSockets(attachment.conversationID)) {
      if (peer === socket) continue;
      try { peer.send(envelope); }
      catch { try { peer.close(1011, "signal delivery failed"); } catch { /* closed */ } }
    }
  }

  webSocketClose(socket: WebSocket, code: number, reason: string, wasClean: boolean): void {
    const attachment = socket.deserializeAttachment() as { conversationID?: string; scope?: string } | null;
    try { socket.close(code, reason); } catch { /* already closed */ }
    if (attachment?.scope === "media:signal" && attachment.conversationID) this.broadcastRoomPresence(attachment.conversationID);
  }

  webSocketError(socket: WebSocket): void {
    try { socket.close(1011, "websocket error"); } catch { /* already closed */ }
  }
}
