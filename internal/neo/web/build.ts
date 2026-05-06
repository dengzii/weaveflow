import { watch } from "fs";
import { resolve } from "path";

const isDev = process.argv.includes("--watch");
const BACKEND = process.env.NEO_BACKEND ?? "http://127.0.0.1:9090";
const DEV_PORT = parseInt(process.env.DEV_PORT ?? "3000");
const distDir = resolve(import.meta.dir, "dist");

// Whether to include debug tools in the bundle (default: true in dev, false in prod)
const includeDebug = process.env.INCLUDE_DEBUG !== "false";

const twBin = resolve(import.meta.dir, "node_modules/.bin/tailwindcss");

async function buildCSS(): Promise<boolean> {
  const args = [twBin, "-i", "app.css", "-o", `${distDir}/app.css`];
  if (!isDev) args.push("--minify");
  const proc = Bun.spawn(args, {
    cwd: import.meta.dir,
    stdout: "inherit",
    stderr: "inherit",
  });
  const code = await proc.exited;
  return code === 0;
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
      // Injected constant — when false, bundler eliminates debug-only code branches
      INCLUDE_DEBUG: JSON.stringify(includeDebug),
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
  console.log(`[build] done in ${elapsed}ms (debug=${includeDebug})`);
  return true;
}

if (isDev) {
  // Start Tailwind in watch mode (background process)
  Bun.spawn([twBin, "-i", "app.css", "-o", `${distDir}/app.css`, "--watch"], {
    cwd: import.meta.dir,
    stdout: "inherit",
    stderr: "inherit",
  });

  await buildJS();

  const WS_BACKEND = BACKEND.replace(/^http/, "ws");

  interface WsProxyData {
    path: string;
    backend: WebSocket | null;
    queue: Array<string | ArrayBuffer | Uint8Array>;
  }

  Bun.serve<WsProxyData>({
    port: DEV_PORT,

    // WebSocket proxy: relay frames between the browser and the Go backend.
    websocket: {
      open(ws) {
        const backendUrl = WS_BACKEND + ws.data.path;
        const backend = new WebSocket(backendUrl);
        backend.binaryType = "arraybuffer";
        backend.onopen = () => {
          ws.data.backend = backend;
          for (const msg of ws.data.queue) backend.send(msg);
          ws.data.queue = [];
        };
        backend.onmessage = (e) => ws.send(e.data as string | ArrayBuffer);
        backend.onclose = (e) => ws.close(e.code, e.reason || "backend closed");
        backend.onerror = () => ws.close(1011, "backend error");
      },
      message(ws, message) {
        if (ws.data.backend?.readyState === WebSocket.OPEN) {
          ws.data.backend.send(message);
        } else {
          ws.data.queue.push(message);
        }
      },
      close(ws, code, reason) {
        ws.data.backend?.close(code, reason);
      },
    },

    async fetch(req, server) {
      const url = new URL(req.url);

      // Upgrade WebSocket connections and relay them to the backend.
      if (req.headers.get("upgrade")?.toLowerCase() === "websocket") {
        const isProxied =
          url.pathname.startsWith("/neo/") ||
          (includeDebug && url.pathname.startsWith("/api/"));
        if (isProxied) {
          const ok = server.upgrade(req, {
            data: { path: url.pathname + url.search, backend: null, queue: [] },
          });
          if (ok) return undefined;
        }
      }

      // Proxy Neo agent API
      if (url.pathname.startsWith("/neo/")) {
        const target = BACKEND + url.pathname + url.search;
        return fetch(target, {
          method: req.method,
          headers: req.headers,
          body: req.body,
          // @ts-ignore
          duplex: "half",
        });
      }

      // Proxy debug replay API to neo backend (replay routes served by neo)
      if (includeDebug && url.pathname.startsWith("/api/")) {
        const target = BACKEND + url.pathname + url.search;
        return fetch(target, {
          method: req.method,
          headers: req.headers,
          body: req.body,
          // @ts-ignore
          duplex: "half",
        });
      }

      if (url.pathname !== "/" && url.pathname !== "/index.html") {
        const file = Bun.file(resolve(distDir, url.pathname.slice(1)));
        if (await file.exists()) return new Response(file);
      }

      return new Response(Bun.file(resolve(import.meta.dir, "index.html")));
    },
  });

  console.log(`[dev] http://127.0.0.1:${DEV_PORT}/`);
  console.log(`[dev] proxying /neo/* → ${BACKEND}`);
  if (includeDebug) console.log(`[dev] proxying /api/* → ${BACKEND} (debug replay)`);

  // Rebuild JS on source changes (CSS is handled by Tailwind watch)
  watch(import.meta.dir, { recursive: true }, async (_event, filename) => {
    if (!filename) return;
    if (!filename.endsWith(".ts") && !filename.endsWith(".tsx")) return;
    if (filename === "build.ts") return;
    if (filename.startsWith("dist")) return;
    process.stdout.write(`\n[watch] ${filename} changed — rebuilding…\n`);
    await buildJS().catch((err) => console.error("[watch] error:", err));
  });
} else {
  // Bun.build() rewrites the output directory, so running CSS and JS builds
  // in parallel can drop app.css from dist on Windows.
  const jsOk = await buildJS();
  const cssOk = await buildCSS();
  process.exit(cssOk && jsOk ? 0 : 1);
}
