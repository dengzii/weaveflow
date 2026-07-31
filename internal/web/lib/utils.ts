import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

export function formatTime(value?: string) {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return formatClockTime(date);
}

export function formatTimeMs(value?: string) {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return `${formatClockTime(date)}.${padNumber(date.getMilliseconds(), 3)}`;
}

export function stringifyJSON(value: unknown) {
  return JSON.stringify(value, null, 2);
}

export function parseJSON<T>(value: string): T {
  return JSON.parse(value) as T;
}

export function isPlainRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

export function cloneJSONValue<Value>(value: Value): Value {
  if (Array.isArray(value)) return value.map(cloneJSONValue) as Value;
  if (isPlainRecord(value)) {
    return Object.fromEntries(
      Object.entries(value).map(([key, item]) => [key, cloneJSONValue(item)])
    ) as Value;
  }
  return value;
}

function formatClockTime(date: Date): string {
  return [
    padNumber(date.getHours(), 2),
    padNumber(date.getMinutes(), 2),
    padNumber(date.getSeconds(), 2),
  ].join(":");
}

function padNumber(value: number, width: number): string {
  return String(value).padStart(width, "0");
}
