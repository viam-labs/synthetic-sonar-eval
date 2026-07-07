// Only treat a frame as "live" if it was captured within this long of the current scrubber
// position — otherwise it's considered stale (but still navigable via carousel/timeline).
export const FRAME_STALE_MS = 1_000;

export interface Timestamped {
  ts: number;
}

/** Index of the last item at/before targetTs in a ts-ascending array; -1 if before all of them. */
export function indexAtOrBefore<T extends Timestamped>(items: T[], targetTs: number): number {
  let index = -1;
  for (let i = 0; i < items.length; i++) {
    if (items[i].ts <= targetTs) index = i;
    else break;
  }
  return index;
}

/** The item at/before targetTs, or null if there isn't one or it's older than FRAME_STALE_MS. */
export function currentFrame<T extends Timestamped>(items: T[], targetTs: number): T | null {
  const index = indexAtOrBefore(items, targetTs);
  if (index < 0) return null;
  const item = items[index];
  return targetTs - item.ts <= FRAME_STALE_MS ? item : null;
}

/** The most recent item at/before targetTs, regardless of staleness. */
export function lastFrameAtOrBefore<T extends Timestamped>(items: T[], targetTs: number): T | null {
  const index = indexAtOrBefore(items, targetTs);
  return index >= 0 ? items[index] : null;
}

/**
 * Timestamp of the nearest item strictly before targetTs, or null if there isn't one. Carousel
 * prev/next must step to a genuinely different timestamp rather than to items[index ± 1] — two
 * items can legitimately share an identical (millisecond-rounded) ts, and jumping to a shared ts
 * re-resolves back to the same index via indexAtOrBefore's "ties go to the last match" rule,
 * permanently stalling prev() the moment it tries to step across such a pair.
 */
export function prevDistinctTs<T extends Timestamped>(items: T[], targetTs: number): number | null {
  for (let i = items.length - 1; i >= 0; i--) {
    if (items[i].ts < targetTs) return items[i].ts;
  }
  return null;
}

/** Timestamp of the nearest item strictly after targetTs, or null if there isn't one. */
export function nextDistinctTs<T extends Timestamped>(items: T[], targetTs: number): number | null {
  for (let i = 0; i < items.length; i++) {
    if (items[i].ts > targetTs) return items[i].ts;
  }
  return null;
}

export function frameAgeMs(frameTs: number, currentTs: number): number {
  return Math.max(0, currentTs - frameTs);
}

/** Text color for a frame timestamp: neutral when fresh, progressively redder with age. */
export function stalenessColor(ageMs: number): string {
  if (ageMs <= FRAME_STALE_MS) return 'rgb(226, 232, 240)'; // slate-200
  const maxAge = 30_000;
  const t = Math.min(1, (ageMs - FRAME_STALE_MS) / (maxAge - FRAME_STALE_MS));
  const r = Math.round(226 + (248 - 226) * t);
  const g = Math.round(232 + (113 - 232) * t);
  const b = Math.round(240 + (113 - 240) * t);
  return `rgb(${r}, ${g}, ${b})`;
}

// "horizontal-h3-1-sensor" -> "h3-1", "horizontal-h-sensor" -> "h"
export function shortSensorLabel(sensorName: string): string {
  return sensorName.replace(/^horizontal-/, '').replace(/-sensor$/, '');
}

export function sensorNamesOf(frames: { sensorName: string }[]): string[] {
  return Array.from(new Set(frames.map((f) => f.sensorName))).sort();
}

/** Buckets frames by sensorName; `names` should come from sensorNamesOf(frames). */
export function groupBySensor<T extends { sensorName: string }>(frames: T[], names: string[]): Map<string, T[]> {
  const map = new Map<string, T[]>();
  for (const name of names) map.set(name, frames.filter((f) => f.sensorName === name));
  return map;
}
