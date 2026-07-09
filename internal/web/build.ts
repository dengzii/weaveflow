import { watch } from "fs";
import { resolve } from "path";

const isDev = process.argv.includes("--watch");
const BACKEND = process.env.WEAVEFLOW_BACKEND ?? "http://127.0.0.1:8080";
const DEV_PORT = parseInt(process.env.DEV_PORT ?? "3031");
const DEV_IDLE_TIMEOUT = parseInt(process.env.DEV_IDLE_TIMEOUT ?? "255");
const distDir = resolve(import.meta.dir, "dist");

const twBin = resolve(import.meta.dir, "node_modules/.bin/tailwindcss");
const apiPrefixes = ["/graph", "/registry", "/tools", "/runs", "/checkpoints", "/events"];
const hopByHopHeaderNames = new Set([
  "connection",
  "keep-alive",
  "proxy-authenticate",
  "proxy-authorization",
  "te",
  "trailer",
  "transfer-encoding",
  "upgrade",
]);

function filteredProxyHeaders(headers: Headers, removeContentLength = false): Headers {
  const next = new Headers();
  headers.forEach((value, key) => {
    const normalized = key.toLowerCase();
    if (hopByHopHeaderNames.has(normalized)) return;
    if (normalized === "host") return;
    if (removeContentLength && normalized === "content-length") return;
    next.set(key, value);
  });
  return next;
}

function requestAllowsBody(method: string): boolean {
  const upper = method.toUpperCase();
  return upper !== "GET" && upper !== "HEAD";
}

function shouldProxy(pathname: string): boolean {
  return apiPrefixes.some((prefix) => pathname === prefix || pathname.startsWith(prefix + "/"));
}

async function proxyHttpRequest(req: Request, target: string): Promise<Response> {
  const requestHeaders = filteredProxyHeaders(req.headers, true);
  const pathname = new URL(req.url).pathname;
  const isSSEStream = pathname === "/events/stream";
  if (isSSEStream) {
    requestHeaders.set("accept-encoding", "identity");
  }

  const upstreamInit: RequestInit & { duplex?: string } = {
    method: req.method,
    headers: requestHeaders,
    body: requestAllowsBody(req.method) ? req.body : undefined,
    duplex: "half",
  };
  if (!isSSEStream) {
    upstreamInit.signal = req.signal;
  }

  try {
    const upstream = await fetch(target, upstreamInit);
    return new Response(upstream.body, {
      status: upstream.status,
      statusText: upstream.statusText,
      headers: filteredProxyHeaders(upstream.headers, true),
    });
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    return new Response(`proxy request failed: ${message}`, {
      status: 502,
      headers: { "content-type": "text/plain; charset=utf-8" },
    });
  }
}

async function buildCSS(): Promise<boolean> {
  const args = [twBin, "-i", "app.css", "-o", `${distDir}/app.css`];
  if (!isDev) args.push("--minify");
  const proc = Bun.spawn(args, {
    cwd: import.meta.dir,
    stdout: "inherit",
    stderr: "inherit",
  });
  return (await proc.exited) === 0;
}

async function buildJS(): Promise<boolean> {
  const start = performance.now();
  const result = await Bun.build({
    entrypoints: [resolve(import.meta.dir, "main.tsx")],
    outdir: distDir,
    target: "browser",
    minify: !isDev,
    sourcemap: isDev ? "linked" : "none",
    naming: { entry: "app.[ext]" },
    define: {
      "process.env.NODE_ENV": JSON.stringify(isDev ? "development" : "production"),
      "import.meta.env": JSON.stringify({ MODE: isDev ? "development" : "production" }),
      "import.meta.env.MODE": JSON.stringify(isDev ? "development" : "production"),
    },
  });

  const elapsed = (performance.now() - start).toFixed(0);
  if (!result.success) {
    console.error(`[build] JS failed (${elapsed}ms)`);
    for (const log of result.logs) console.error(" ", log);
    return false;
  }
  for (const output of result.outputs) {
    const kb = (output.size / 1024).toFixed(1);
    console.log(`[build] ${output.path.split(/[\\/]/).at(-1)}  ${kb} KB`);
  }
  console.log(`[build] done in ${elapsed}ms`);
  return true;
}

if (isDev) {
  // Bun.build() can rewrite the output directory, so write CSS after JS to keep
  // dist/app.css present for the dev server.
  await buildJS();
  await buildCSS();

  const tailwindWatcher = Bun.spawn([twBin, "-i", "app.css", "-o", `${distDir}/app.css`, "--watch"], {
    cwd: import.meta.dir,
    stdout: "inherit",
    stderr: "inherit",
  });
  tailwindWatcher.unref();

  Bun.serve({
    port: DEV_PORT,
    idleTimeout: DEV_IDLE_TIMEOUT,
    async fetch(req) {
      const url = new URL(req.url);

      if (shouldProxy(url.pathname)) {
        return proxyHttpRequest(req, BACKEND + url.pathname + url.search);
      }

      if (url.pathname !== "/" && url.pathname !== "/index.html") {
        const file = Bun.file(resolve(distDir, url.pathname.slice(1)));
        if (await file.exists()) return new Response(file);
        if (url.pathname.split("/").at(-1)?.includes(".")) {
          return new Response("not found", {
            status: 404,
            headers: { "content-type": "text/plain; charset=utf-8" },
          });
        }
      }

      return new Response(Bun.file(resolve(import.meta.dir, "index.html")));
    },
  });

  console.log(`[dev] http://127.0.0.1:${DEV_PORT}/`);
  console.log(`[dev] proxying server API -> ${BACKEND}`);

  watch(import.meta.dir, { recursive: true }, async (_event, filename) => {
    if (!filename) return;
    if (!filename.endsWith(".ts") && !filename.endsWith(".tsx")) return;
    if (filename === "build.ts") return;
    if (filename.startsWith("dist")) return;
    process.stdout.write(`\n[watch] ${filename} changed, rebuilding\n`);
    try {
      const jsOk = await buildJS();
      if (jsOk) await buildCSS();
    } catch (err) {
      console.error("[watch] error:", err);
    }
  });
} else {
  const jsOk = await buildJS();
  const cssOk = await buildCSS();
  process.exit(cssOk && jsOk ? 0 : 1);
}
