import { cp, mkdir, readdir, rm } from "node:fs/promises";
import { extname, join } from "node:path";

const websiteDir = import.meta.dir;
const outputDir = join(websiteDir, "dist");
const docsDir = join(websiteDir, "docs");
const staticExtensions = new Set([".css", ".html", ".ico", ".js", ".png", ".svg", ".webp"]);

async function run(command: string[], cwd: string) {
  const process = Bun.spawn(command, {
    cwd,
    stdout: "inherit",
    stderr: "inherit",
  });
  const exitCode = await process.exited;
  if (exitCode !== 0) {
    throw new Error(`${command.join(" ")} exited with code ${exitCode}`);
  }
}

await rm(outputDir, { recursive: true, force: true });
await mkdir(outputDir, { recursive: true });

const entries = await readdir(websiteDir, { withFileTypes: true });
for (const entry of entries) {
  if (!entry.isFile() || !staticExtensions.has(extname(entry.name))) {
    continue;
  }
  await cp(join(websiteDir, entry.name), join(outputDir, entry.name));
}

await run(["bun", "install", "--frozen-lockfile"], docsDir);
await run(["bun", "run", "build"], docsDir);

console.log(`Website build complete: ${outputDir}`);
