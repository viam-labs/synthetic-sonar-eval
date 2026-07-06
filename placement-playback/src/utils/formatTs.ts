/** Formats a reading's `ts` as a date when it plausibly looks like a real epoch, otherwise as a raw sequence value. */
export function formatTs(ts: number): string {
  if (ts > 1e12) return new Date(ts).toLocaleString();
  if (ts > 1e9) return new Date(ts * 1000).toLocaleString();
  return `t=${ts}`;
}
