import { createServer } from "node:http";
import { Readable } from "node:stream";
import { resolve, join, normalize } from "node:path";
import { stat } from "node:fs/promises";

const DIST = resolve(import.meta.dir, "dist");
const FTP_UPSTREAM = process.env.FTP_UPSTREAM ?? "http://ftp-server:7000";
const PORT = Number(process.env.PORT ?? 9001);
const MAX_HEADER_SIZE = Number(process.env.WEB_MAX_HEADER_SIZE ?? 64 * 1024);

const HEADERS_TO_STRIP = new Set([
  "host",
  "connection",
  "content-length",
  "transfer-encoding",
]);

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
    return { status: 400, headers: { "content-type": "text/plain" }, body: "Bad Request" };
  }
  const safe = normalize(decoded).replace(/^([/\\])+/, "");
  const full = join(DIST, safe);
  if (!full.startsWith(DIST)) {
    return { status: 403, headers: { "content-type": "text/plain" }, body: "Forbidden" };
  }
  try {
    const s = await stat(full);
    if (s.isDirectory()) throw new Error("dir");
    const file = Bun.file(full);
    return {
      status: 200,
      headers: { "content-type": mimeFor(full), "content-length": String(s.size) },
      body: Buffer.from(await file.arrayBuffer()),
    };
  } catch {
    const file = Bun.file(join(DIST, "index.html"));
    return {
      status: 200,
      headers: { "content-type": "text/html; charset=utf-8" },
      body: Buffer.from(await file.arrayBuffer()),
    };
  }
}

async function proxyApi(req, url) {
  const target = FTP_UPSTREAM + url.pathname + url.search;
  const headers = new Headers();
  for (const [key, value] of Object.entries(req.headers)) {
    if (HEADERS_TO_STRIP.has(key.toLowerCase()) || value === undefined) continue;
    if (Array.isArray(value)) headers.set(key, value.join(", "));
    else headers.set(key, String(value));
  }
  const init = { method: req.method, headers, redirect: "manual" };
  if (req.method !== "GET" && req.method !== "HEAD") {
    init.body = Readable.toWeb(req);
    init.duplex = "half";
  }
  const upstream = await fetch(target, init);
  const outHeaders = {};
  for (const [key, value] of upstream.headers) {
    if (key.toLowerCase() === "set-cookie") continue;
    outHeaders[key] = value;
  }
  const setCookies = typeof upstream.headers.getSetCookie === "function" ? upstream.headers.getSetCookie() : [];
  if (setCookies.length > 0) outHeaders["set-cookie"] = setCookies;
  return {
    status: upstream.status,
    headers: outHeaders,
    body: Buffer.from(await upstream.arrayBuffer()),
  };
}

const server = createServer({ maxHeaderSize: MAX_HEADER_SIZE }, async (req, res) => {
  try {
    const url = new URL(req.url ?? "/", "http://" + (req.headers.host ?? "localhost"));
    const result = url.pathname.startsWith("/api/")
      ? await proxyApi(req, url)
      : await serveStatic(url.pathname);
    res.writeHead(result.status, result.headers);
    if (req.method === "HEAD") res.end();
    else res.end(result.body);
  } catch (err) {
    console.error("[web] request failed", err);
    if (!res.headersSent) res.writeHead(502, { "content-type": "text/plain" });
    res.end("Bad Gateway");
  }
});

server.listen(PORT, "0.0.0.0", () => {
  console.log("[web] listening on :" + PORT + " (dist=" + DIST + ", upstream=" + FTP_UPSTREAM + ", maxHeaderSize=" + MAX_HEADER_SIZE + ")");
});
