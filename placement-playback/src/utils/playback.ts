import type { ImageFrame, Reading, SonarFrame } from '../types';

export interface TimeExtent {
  minTs: number;
  maxTs: number;
}

/** Earliest/latest event across all three data sources — the full extent a loaded dataset spans. */
export function computeExtent(readings: Reading[], images: ImageFrame[], sonarFrames: SonarFrame[]): TimeExtent {
  const allTs = [...readings.map((r) => r.ts), ...images.map((i) => i.ts), ...sonarFrames.map((f) => f.ts)];
  if (allTs.length === 0) return { minTs: 0, maxTs: 0 };
  return { minTs: Math.min(...allTs), maxTs: Math.max(...allTs) };
}

export interface PlaybackTick {
  ts: number;
  /** True if this tick ran into the range boundary and playback should stop. */
  stopped: boolean;
}

/**
 * Advances the playhead by one tick of wall-clock time. Clamps to [rangeStart, rangeEnd] and
 * flags when playback hit a boundary so the caller can stop it.
 */
export function advancePlaybackTick(
  current: number,
  deltaMs: number,
  speed: number,
  direction: 1 | -1,
  rangeStart: number,
  rangeEnd: number,
): PlaybackTick {
  // 1x = actual real time: 1ms of wall-clock elapses as 1ms of the dataset's own timeline.
  const next = current + deltaMs * speed * direction;
  if (next >= rangeEnd) return { ts: rangeEnd, stopped: true };
  // Reverse playback stops at the range's left edge rather than the dataset's start — once the
  // playhead meets rangeStart there's nothing left to reveal going further back.
  if (next <= rangeStart) return { ts: rangeStart, stopped: true };
  return { ts: next, stopped: false };
}
