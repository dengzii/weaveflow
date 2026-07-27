import { watch } from "fs";
import { resolve } from "path";

const isDev = process.argv.includes("--watch");
const DEV_PORT = parseInt(process.env.DEV_PORT ?? "3031");
const DEV_IDLE_TIMEOUT = parseInt(process.env.DEV_IDLE_TIMEOUT ?? "255");
const distDir = resolve(import.meta.dir, "dist");

const twBin = resolve(import.meta.dir, "node_modules/.bin/tailwindcss");
const staticAssets = ["index.html", "config.js"];

async function copyStaticAssets(): Promise<boolean> {
  try {
    await Promise.all(
      staticAssets.map((filename) =>
        Bun.write(resolve(distDir, filename), Bun.file(resolve(import.meta.dir, filename)))
      )
    );
    return true;
  } catch (error) {
    console.error("[build] static assets failed", error);
    return false;
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
  // the static assets present for the dev server.
  await buildJS();
  await buildCSS();
  await copyStaticAssets();

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

      return new Response(Bun.file(resolve(distDir, "index.html")));
    },
  });

  console.log(`[dev] http://127.0.0.1:${DEV_PORT}/`);

  watch(import.meta.dir, { recursive: true }, async (_event, filename) => {
    if (!filename) return;
    if (staticAssets.includes(filename)) {
      await copyStaticAssets();
      return;
    }
    if (!filename.endsWith(".ts") && !filename.endsWith(".tsx")) return;
    if (filename === "build.ts") return;
    if (filename.startsWith("dist")) return;
    process.stdout.write(`\n[watch] ${filename} changed, rebuilding\n`);
    try {
      const jsOk = await buildJS();
      if (jsOk) {
        await buildCSS();
        await copyStaticAssets();
      }
    } catch (err) {
      console.error("[watch] error:", err);
    }
  });
} else {
  const jsOk = await buildJS();
  const cssOk = await buildCSS();
  const staticOk = await copyStaticAssets();
  process.exit(cssOk && jsOk && staticOk ? 0 : 1);
}
