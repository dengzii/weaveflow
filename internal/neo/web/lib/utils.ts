import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";
import { marked } from "marked";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

marked.setOptions({ breaks: true, gfm: true });

// During streaming the accumulated text may have an unclosed fenced code block.
// When that happens, marked treats everything after the opening fence as code,
// breaking the rest of the markdown. This function closes the open fence so the
// parser sees valid input at every intermediate state.
function closeOpenFences(src: string): string {
  const lines = src.split("\n");
  let inFence = false;
  let fenceChar = "";
  let fenceLen = 0;

  for (const line of lines) {
    const m = line.match(/^(`{3,}|~{3,})/);
    if (!m) continue;
    const char = m[1][0];
    const len = m[1].length;
    if (!inFence) {
      inFence = true;
      fenceChar = char;
      fenceLen = len;
    } else if (char === fenceChar && len >= fenceLen) {
      inFence = false;
    }
  }

  return inFence ? src + "\n" + fenceChar.repeat(fenceLen) : src;
}

export function renderMd(src: string): string {
  try { return marked.parse(closeOpenFences(src)) as string; }
  catch { return src.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;"); }
}
