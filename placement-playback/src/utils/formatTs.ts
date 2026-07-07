/** Formats a reading's `ts` as a date when it plausibly looks like a real epoch, otherwise as a raw sequence value. */
export function formatTs(ts: number): string {
  if (ts > 1e12) return new Date(ts).toLocaleString();
  if (ts > 1e9) return new Date(ts * 1000).toLocaleString();
  return `t=${ts}`;
}

/** True when `ts` plausibly represents a real epoch (seconds or milliseconds) rather than a raw sequence index. */
export function isEpochTs(ts: number): boolean {
  return ts > 1e9;
}

/**
 * Converts an epoch `ts` (seconds or ms — same heuristic as formatTs) to a value suitable for
 * an <input type="datetime-local">, rendered in the browser's local timezone.
 */
export function tsToDatetimeLocalValue(ts: number): string {
  const ms = ts > 1e12 ? ts : ts * 1000;
  const d = new Date(ms);
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

/**
 * Inverse of tsToDatetimeLocalValue. `sampleTs` (any ts from the same dataset) determines
 * whether the result should be in seconds or milliseconds, matching the dataset's own scale.
 */
export function datetimeLocalValueToTs(value: string, sampleTs: number): number {
  const ms = new Date(value).getTime();
  return sampleTs > 1e12 ? ms : ms / 1000;
}
