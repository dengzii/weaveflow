interface ApiEnvelope<T> {
  data?: T;
  error?: string;
}

export async function api<T>(url: string): Promise<T> {
  const response = await fetch(url);
  const payload: ApiEnvelope<T> = await response
    .json()
    .catch(() => ({ error: "invalid response" }));
  if (!response.ok || payload.error) {
    throw new Error(payload.error ?? `request failed: ${response.status}`);
  }
  return payload.data as T;
}

export function buildUrl(
  path: string,
  cacheDir: string,
  extra: Record<string, string> = {}
): string {
  const url = new URL(path, window.location.origin);
  url.searchParams.set("cache_dir", cacheDir);
  for (const [key, value] of Object.entries(extra)) {
    url.searchParams.set(key, value);
  }
  return url.toString();
}
