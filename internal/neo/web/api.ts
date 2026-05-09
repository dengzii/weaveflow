export async function apiFetch<T>(url: string, init?: RequestInit): Promise<T> {
  const resp = await fetch(url, init);
  if (!resp.ok) {
    const text = await resp.text();
    throw new Error(text || resp.statusText);
  }
  return resp.json() as Promise<T>;
}

export async function apiAction(url: string, init?: RequestInit): Promise<void> {
  await apiFetch(url, init);
}
