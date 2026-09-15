import { resolve, join, normalize } from "node:path";
import { stat } from "node:fs/promises";

const DIST = resolve(import.meta.dir, "dist");
const FTP_UPSTREAM = process.env.FTP_UPSTREAM ?? "http://ftp-server:7000";
const PORT = Number(process.env.PORT ?? 9001);

const HEADERS_TO_STRIP = new Set(["host", "connection", "content-length"]);

function mimeFor(p) {
  if (p.endsWith(".html")) return "text/html; charset=utf-8";
  if (p.endsWith(".js") || p.endsWith(".mjs")) return "application/javascript; charset=utf-8";
  if (p.endsWith(".css")) return "text/css; charset=utf-8";
  if (p.endsWith(".json")) return "application/json; charset=utf-8";
  if (p.endsWith(".svg")) return "image/svg+xml";
  if (p.endsWith(".png")) return "image/png";
  if (p.endsWith(".jpg") || p.endsWith(".jpeg")) return "image/jpeg";
  if (p.endsWith(".woff2")) return "font/woff2";
  if (p.endsWith(".ico")) return "image/x-icon";
  return "application/octet-stream";
}

async function serveStatic(pathname) {
  let decoded;
  try {
    decoded = decodeURIComponent(pathname);
  } catch {
    return new Response("Bad Request", { status: 400 });
  }
  const safe = normalize(decoded).replace(/^([/\\])+/, "");
  const full = join(DIST, safe);
  if (!full.startsWith(DIST)) return new Response("Forbidden", { status: 403 });
  try {
    const s = await stat(full);
    if (s.isDirectory()) throw new Error("dir");
    const f = Bun.file(full);
    return new Response(f, { headers: { "content-type": mimeFor(full) } });
  } catch {
    // SPA fallback
    return new Response(Bun.file(join(DIST, "index.html")), {
      headers: { "content-type": "text/html; charset=utf-8" },
    });
  }
}

async function proxyApi(req) {
  const url = new URL(req.url);
  const target = `${FTP_UPSTREAM}${url.pathname}${url.search}`;
  const headers = new Headers();
  for (const [k, v] of req.headers) {
    if (!HEADERS_TO_STRIP.has(k.toLowerCase())) headers.set(k, v);
  }
  const init = { method: req.method, headers, redirect: "manual" };
  if (req.method !== "GET" && req.method !== "HEAD") {
    init.body = req.body;
    init.duplex = "half";
  }
  const upstream = await fetch(target, init);
  if (upstream.status >= 300 && upstream.status < 400) {
    const outHeaders = new Headers();
    for (const [k, v] of upstream.headers) {
      if (k.toLowerCase() === "set-cookie") continue;
      outHeaders.set(k, v);
    }
    const setCookies = typeof upstream.headers.getSetCookie === "function" ? upstream.headers.getSetCookie() : [];
    for (const c of setCookies) outHeaders.append("set-cookie", c);
    return new Response(null, { status: upstream.status, headers: outHeaders });
  }
  return upstream;
}

Bun.serve({
  port: PORT,
  hostname: "0.0.0.0",
  fetch(req) {
    const url = new URL(req.url);
    if (url.pathname.startsWith("/api/")) return proxyApi(req);
    return serveStatic(url.pathname);
  },
});

console.log(`[web] listening on :${PORT} (dist=${DIST}, upstream=${FTP_UPSTREAM})`);