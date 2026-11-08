export interface Env {
  MEDIA_BUCKET: R2Bucket;
  MEDIA_URL_SECRET: string;
  FRONTEND_ORIGINS: string;
}

const encoder = new TextEncoder();
const OBJECT_KEY = /^v1\/[0-9a-f]{32}$/;

function allowedOrigin(origin: string | null, env: Env): boolean {
  if (!origin) return true;
  return env.FRONTEND_ORIGINS.split(',').map((value) => value.trim()).filter(Boolean).includes(origin);
}
function corsHeaders(origin: string | null): Headers {
  const headers = new Headers();
  if (origin) {
    headers.set('Access-Control-Allow-Origin', origin);
    headers.set('Access-Control-Allow-Methods', 'GET, OPTIONS');
    headers.set('Access-Control-Allow-Headers', 'Range');
    headers.set('Access-Control-Expose-Headers', 'Accept-Ranges, Content-Length, Content-Range, ETag');
    headers.set('Vary', 'Origin');
  }
  return headers;
}
function equalConstantTime(a: string, b: string): boolean {
  if (a.length !== b.length) return false;
  let difference = 0;
  for (let i = 0; i < a.length; i += 1) difference |= a.charCodeAt(i) ^ b.charCodeAt(i);
  return difference === 0;
}
function toHex(bytes: ArrayBuffer): string {
  return Array.from(new Uint8Array(bytes), (value) => value.toString(16).padStart(2, '0')).join('');
}
async function verifySignature(key: string, expiry: number, signature: string, secret: string): Promise<boolean> {
  if (secret.length < 32 || !/^[0-9a-f]{64}$/.test(signature)) return false;
  const now = Math.floor(Date.now() / 1000);
  if (!Number.isSafeInteger(expiry) || expiry <= now || expiry > now + 120) return false;
  const cryptoKey = await crypto.subtle.importKey('raw', encoder.encode(secret), { name: 'HMAC', hash: 'SHA-256' }, false, ['sign']);
  const signed = await crypto.subtle.sign('HMAC', cryptoKey, encoder.encode('GET\n' + key + '\n' + expiry));
  return equalConstantTime(toHex(signed), signature);
}
function parseRange(value: string | null, size: number): { offset: number; length: number; start: number; end: number } | null | false {
  if (!value) return null;
  const match = /^bytes=(\d*)-(\d*)$/.exec(value.trim());
  if (!match || (!match[1] && !match[2])) return false;
  let start: number;
  let end: number;
  if (!match[1]) {
    const suffix = Number(match[2]);
    if (!Number.isSafeInteger(suffix) || suffix <= 0) return false;
    start = Math.max(0, size - suffix);
    end = size - 1;
  } else {
    start = Number(match[1]);
    end = match[2] ? Number(match[2]) : size - 1;
  }
  if (!Number.isSafeInteger(start) || !Number.isSafeInteger(end) || start < 0 || end < start || start >= size) return false;
  end = Math.min(end, size - 1);
  return { offset: start, length: end - start + 1, start, end };
}
function response(body: BodyInit | null, status: number, headers: Headers): Response {
  headers.set('Cache-Control', 'private, no-store, max-age=0');
  headers.set('Pragma', 'no-cache');
  headers.set('X-Content-Type-Options', 'nosniff');
  headers.set('Cross-Origin-Resource-Policy', 'cross-origin');
  return new Response(body, { status, headers });
}
export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url);
    const origin = request.headers.get('Origin');
    const headers = corsHeaders(origin);
    if (!allowedOrigin(origin, env)) return response('origin not allowed', 403, headers);
    if (request.method === 'OPTIONS') {
      if (!origin) return response('origin required', 403, headers);
      if (request.headers.get('Access-Control-Request-Method') !== 'GET') return response('preflight method not allowed', 403, headers);
      const requestedHeaders = (request.headers.get('Access-Control-Request-Headers') || '').split(',').map((value) => value.trim().toLowerCase()).filter(Boolean);
      if (requestedHeaders.some((value) => value !== 'range')) return response('preflight header not allowed', 403, headers);
      headers.set('Access-Control-Max-Age', '600');
      return response(null, 204, headers);
    }
    if (request.method !== 'GET') return response('method not allowed', 405, headers);
    const prefix = '/private/';
    if (!url.pathname.startsWith(prefix)) return response('not found', 404, headers);
    const key = url.pathname.slice(prefix.length);
    if (!OBJECT_KEY.test(key)) return response('not found', 404, headers);
    const expiry = Number(url.searchParams.get('e'));
    const signature = url.searchParams.get('s') || '';
    if (!await verifySignature(key, expiry, signature, env.MEDIA_URL_SECRET)) return response('invalid or expired media signature', 403, headers);
    let metadata: R2Object | null;
    try {
      metadata = await env.MEDIA_BUCKET.head(key);
    } catch {
      // Do not turn an R2 outage into an unhandled Worker exception. The caller
      // can retry or ask Backend for the verified B2 replica.
      headers.set('Retry-After', '5');
      return response('media storage temporarily unavailable', 503, headers);
    }
    if (!metadata) return response('media not found', 404, headers);
    const range = parseRange(request.headers.get('Range'), metadata.size);
    if (range === false) {
      headers.set('Content-Range', 'bytes */' + metadata.size);
      return response(null, 416, headers);
    }
    if (range) {
      let ranged: R2ObjectBody | null;
      try {
        ranged = await env.MEDIA_BUCKET.get(key, { range: { offset: range.offset, length: range.length } });
      } catch {
        headers.set('Retry-After', '5');
        return response('media storage temporarily unavailable', 503, headers);
      }
      if (!ranged) return response('media not found', 404, headers);
      metadata.writeHttpMetadata(headers);
      headers.set('ETag', metadata.httpEtag);
      headers.set('Accept-Ranges', 'bytes');
      headers.set('Content-Length', String(range.length));
      headers.set('Content-Range', 'bytes ' + range.start + '-' + range.end + '/' + metadata.size);
      headers.set('Content-Disposition', 'inline');
      return response(ranged.body, 206, headers);
    }
    let object: R2ObjectBody | null;
    try {
      object = await env.MEDIA_BUCKET.get(key);
    } catch {
      headers.set('Retry-After', '5');
      return response('media storage temporarily unavailable', 503, headers);
    }
    if (!object) return response('media not found', 404, headers);
    metadata.writeHttpMetadata(headers);
    headers.set('ETag', metadata.httpEtag);
    headers.set('Accept-Ranges', 'bytes');
    headers.set('Content-Length', String(metadata.size));
    headers.set('Content-Disposition', 'inline');
    return response(object.body, 200, headers);
  },
};
