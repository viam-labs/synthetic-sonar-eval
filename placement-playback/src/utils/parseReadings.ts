import type { Reading } from '../types';

function normalize(arr: unknown[]): Reading[] {
  const readings: Reading[] = [];
  arr.forEach((raw, i) => {
    if (!raw || typeof raw !== 'object') return;
    const r = raw as Record<string, unknown>;
    if (typeof r.latitude !== 'number' || typeof r.longitude !== 'number') return;
    readings.push({
      depth: typeof r.depth === 'number' ? r.depth : 0,
      is_synthetic: Boolean(r.is_synthetic),
      latitude: r.latitude,
      longitude: r.longitude,
      marker_id: typeof r.marker_id === 'string' ? r.marker_id : `reading-${i}`,
      ts: typeof r.ts === 'number' ? r.ts : i,
    });
  });
  return readings.sort((a, b) => a.ts - b.ts);
}

/** Accepts a JSON array, a JSON object with a `readings` array field, or NDJSON (one reading per line). */
export function parseReadingsFile(text: string): Reading[] {
  try {
    const parsed: unknown = JSON.parse(text);
    if (Array.isArray(parsed)) return normalize(parsed);
    if (parsed && typeof parsed === 'object') {
      const obj = parsed as Record<string, unknown>;
      if (Array.isArray(obj.readings)) return normalize(obj.readings);
    }
  } catch {
    // not a single JSON document — fall through to NDJSON parsing
  }

  const readings: unknown[] = [];
  for (const line of text.split('\n')) {
    const trimmed = line.trim();
    if (!trimmed) continue;
    try {
      readings.push(JSON.parse(trimmed));
    } catch {
      // skip malformed lines
    }
  }
  return normalize(readings);
}
