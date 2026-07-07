import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { useDataset } from './DatasetContext';
import { advancePlaybackTick, computeExtent } from '../utils/playback';
import type { ImageFrame, Reading, SonarFrame } from '../types';

const TICK_MS = 50;
// A marker still gets the "just dropped" pulse animation for this long (in simulated time) after
// its own ts, so continuous scrubbing/playback still reads as markers appearing rather than just
// popping into a static list.
const DROP_WINDOW_MS = 3_000;

interface PlaybackContextValue {
  currentTs: number;
  rangeStart: number;
  rangeEnd: number;
  minTs: number;
  maxTs: number;
  playDirection: 0 | 1 | -1;
  speed: number;
  filteredReadings: Reading[];
  filteredImages: ImageFrame[];
  filteredSonarFrames: SonarFrame[];
  visible: Reading[];
  justDropped: Reading[];
  realMarkers: Reading[];
  syntheticMarkers: Reading[];
  jumpToTime: (ts: number) => void;
  updateRange: (start: number, end: number) => void;
  togglePlay: (direction: 1 | -1) => void;
  setSpeed: (speed: number) => void;
  resetToRangeStart: () => void;
}

const PlaybackContext = createContext<PlaybackContextValue | null>(null);

export function PlaybackProvider({ children }: { children: ReactNode }) {
  const { readings, images, sonarFrames } = useDataset();

  const [currentTs, setCurrentTs] = useState(0);
  const [rangeStart, setRangeStart] = useState(0);
  const [rangeEnd, setRangeEnd] = useState(0);
  // 0 = paused, 1 = playing forward, -1 = playing in reverse.
  const [playDirection, setPlayDirection] = useState<0 | 1 | -1>(0);
  const [speed, setSpeed] = useState(1);

  const { minTs, maxTs } = useMemo(() => computeExtent(readings, images, sonarFrames), [readings, images, sonarFrames]);

  // Whenever a new dataset is loaded, reset the playhead and range to its full extent.
  useEffect(() => {
    setCurrentTs(minTs);
    setRangeStart(minTs);
    setRangeEnd(maxTs);
    setPlayDirection(0);
  }, [readings, minTs, maxTs]);

  const filteredReadings = useMemo(
    () => readings.filter((r) => r.ts >= rangeStart && r.ts <= rangeEnd),
    [readings, rangeStart, rangeEnd],
  );
  const filteredImages = useMemo(
    () => images.filter((i) => i.ts >= rangeStart && i.ts <= rangeEnd),
    [images, rangeStart, rangeEnd],
  );
  const filteredSonarFrames = useMemo(
    () => sonarFrames.filter((f) => f.ts >= rangeStart && f.ts <= rangeEnd),
    [sonarFrames, rangeStart, rangeEnd],
  );

  const visible = useMemo(() => filteredReadings.filter((r) => r.ts <= currentTs), [filteredReadings, currentTs]);
  const justDropped = useMemo(
    () => filteredReadings.filter((r) => r.ts <= currentTs && currentTs - r.ts <= DROP_WINDOW_MS),
    [filteredReadings, currentTs],
  );
  // Already sorted ascending by ts (parsePlaybackFile guarantees this).
  const realMarkers = useMemo(() => filteredReadings.filter((r) => !r.is_synthetic), [filteredReadings]);
  const syntheticMarkers = useMemo(() => filteredReadings.filter((r) => r.is_synthetic), [filteredReadings]);

  const lastFrameRef = useRef<number | null>(null);

  useEffect(() => {
    if (playDirection === 0 || rangeEnd <= rangeStart) return;
    lastFrameRef.current = null;
    const id = window.setInterval(() => {
      const now = performance.now();
      if (lastFrameRef.current === null) {
        // First tick after starting playback has no prior frame to diff against — just
        // establish the baseline. (Skipping the move-and-clamp below matters: at the instant
        // playback starts, currentTs often sits exactly at rangeStart, so a spurious
        // zero-delta "next" would equal rangeStart and immediately trip the stop condition.)
        lastFrameRef.current = now;
        return;
      }
      const deltaMs = now - lastFrameRef.current;
      lastFrameRef.current = now;
      setCurrentTs((t) => {
        const tick = advancePlaybackTick(t, deltaMs, speed, playDirection, rangeStart, rangeEnd);
        if (tick.stopped) setPlayDirection(0);
        return tick.ts;
      });
    }, TICK_MS);
    return () => window.clearInterval(id);
  }, [playDirection, speed, rangeStart, rangeEnd]);

  const jumpToTime = useCallback((ts: number) => {
    setPlayDirection(0);
    setCurrentTs(ts);
  }, []);

  // The one place a range change is applied: clamps the playhead back inside the new bounds and
  // stops playback so it doesn't keep running against a range that just moved out from under it.
  const updateRange = useCallback((start: number, end: number) => {
    setPlayDirection(0);
    setRangeStart(start);
    setRangeEnd(end);
    setCurrentTs((t) => Math.min(Math.max(t, start), end));
  }, []);

  const togglePlay = useCallback((direction: 1 | -1) => {
    setPlayDirection((d) => (d === direction ? 0 : direction));
  }, []);

  const resetToRangeStart = useCallback(() => {
    setPlayDirection(0);
    setCurrentTs(rangeStart);
  }, [rangeStart]);

  const value = useMemo(
    () => ({
      currentTs,
      rangeStart,
      rangeEnd,
      minTs,
      maxTs,
      playDirection,
      speed,
      filteredReadings,
      filteredImages,
      filteredSonarFrames,
      visible,
      justDropped,
      realMarkers,
      syntheticMarkers,
      jumpToTime,
      updateRange,
      togglePlay,
      setSpeed,
      resetToRangeStart,
    }),
    [
      currentTs,
      rangeStart,
      rangeEnd,
      minTs,
      maxTs,
      playDirection,
      speed,
      filteredReadings,
      filteredImages,
      filteredSonarFrames,
      visible,
      justDropped,
      realMarkers,
      syntheticMarkers,
      jumpToTime,
      updateRange,
      togglePlay,
      resetToRangeStart,
    ],
  );

  return <PlaybackContext.Provider value={value}>{children}</PlaybackContext.Provider>;
}

export function usePlayback() {
  const ctx = useContext(PlaybackContext);
  if (!ctx) throw new Error('usePlayback must be used within a PlaybackProvider');
  return ctx;
}
