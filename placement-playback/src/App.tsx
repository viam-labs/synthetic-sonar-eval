import { useEffect, useMemo, useRef, useState } from 'react';
import { MapView } from './components/MapView';
import { TimeSlider } from './components/TimeSlider';
import { Legend } from './components/Legend';
import { FileLoader } from './components/FileLoader';
import { ImagePanel } from './components/ImagePanel';
import { MarkerListPanel } from './components/MarkerListPanel';
import { parsePlaybackFile } from './utils/parseReadings';
import type { ImageFrame, Reading } from './types';

const TICK_MS = 50;
// A marker still gets the "just dropped" pulse animation for this long (in
// simulated time) after its own ts, so continuous scrubbing/playback still
// reads as markers appearing rather than just popping into a static list.
const DROP_WINDOW_MS = 3_000;
// Only show a camera frame if one was captured within this long of the
// current scrubber position — otherwise the frame is considered stale.
const IMAGE_STALE_MS = 60_000;

export default function App() {
  const [readings, setReadings] = useState<Reading[]>([]);
  const [images, setImages] = useState<ImageFrame[]>([]);
  const [fileName, setFileName] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [currentTs, setCurrentTs] = useState(0);
  // 0 = paused, 1 = playing forward, -1 = playing in reverse.
  const [playDirection, setPlayDirection] = useState<0 | 1 | -1>(0);
  const [speed, setSpeed] = useState(1);
  const [selectedMarkerType, setSelectedMarkerType] = useState<'real' | 'synthetic' | null>(null);

  const minTs = useMemo(() => (readings.length ? Math.min(...readings.map((r) => r.ts)) : 0), [readings]);
  const maxTs = useMemo(() => (readings.length ? Math.max(...readings.map((r) => r.ts)) : 0), [readings]);

  const visible = useMemo(() => readings.filter((r) => r.ts <= currentTs), [readings, currentTs]);
  const justDropped = useMemo(
    () => readings.filter((r) => r.ts <= currentTs && currentTs - r.ts <= DROP_WINDOW_MS),
    [readings, currentTs],
  );
  // Already sorted ascending by ts (parsePlaybackFile guarantees this).
  const realMarkers = useMemo(() => readings.filter((r) => !r.is_synthetic), [readings]);
  const syntheticMarkers = useMemo(() => readings.filter((r) => r.is_synthetic), [readings]);
  // images is sorted ascending by ts, so the last one at/before currentTs is the most recent frame;
  // drop it once it's older than IMAGE_STALE_MS so a long-gone frame doesn't linger forever.
  const currentImage = useMemo(() => {
    const candidate = images.filter((img) => img.ts <= currentTs).at(-1);
    if (!candidate || currentTs - candidate.ts > IMAGE_STALE_MS) return null;
    return candidate;
  }, [images, currentTs]);

  const lastFrameRef = useRef<number | null>(null);

  useEffect(() => {
    if (playDirection === 0 || maxTs <= minTs) return;
    lastFrameRef.current = null;
    const id = window.setInterval(() => {
      const now = performance.now();
      const last = lastFrameRef.current ?? now;
      const deltaMs = now - last;
      lastFrameRef.current = now;
      setCurrentTs((t) => {
        // 1x = actual real time: 1ms of wall-clock elapses as 1ms of the dataset's own timeline.
        const next = t + deltaMs * speed * playDirection;
        if (next >= maxTs) {
          setPlayDirection(0);
          return maxTs;
        }
        if (next <= minTs) {
          setPlayDirection(0);
          return minTs;
        }
        return next;
      });
    }, TICK_MS);
    return () => window.clearInterval(id);
  }, [playDirection, speed, minTs, maxTs]);

  function jumpToTime(ts: number) {
    setPlayDirection(0);
    setCurrentTs(ts);
  }

  function loadPlayback(list: Reading[], frames: ImageFrame[], name: string | null) {
    if (list.length === 0) {
      setError('No valid readings found (need at least latitude and longitude fields).');
      return;
    }
    setError(null);
    setFileName(name);
    setReadings(list);
    setImages(frames);
    setCurrentTs(Math.min(...list.map((r) => r.ts)));
    setPlayDirection(0);
  }

  return (
    <div className="flex h-full flex-col bg-slate-950 text-slate-100">
      <header className="flex shrink-0 items-center justify-between border-b border-slate-800/80 bg-slate-900/40 px-6 py-3">
        <div className="flex items-baseline gap-3">
          <h1 className="flex items-center gap-2 text-base font-semibold tracking-tight text-white">
            <span className="inline-block h-2 w-2 rounded-full bg-sky-400 shadow-[0_0_8px_var(--color-sky-400)]" />
            placement&#8209;playback
          </h1>
          <span className="hidden text-sm text-slate-300 sm:inline">Marker &amp; camera playback</span>
        </div>
        <Legend
          selected={selectedMarkerType}
          onSelect={(type) => setSelectedMarkerType((cur) => (cur === type ? null : type))}
        />
      </header>

      <div className="flex min-h-0 flex-1">
        <aside className="w-72 shrink-0 overflow-y-auto border-r border-slate-800/80 bg-slate-900/20 p-4">
          <FileLoader
            onLoad={(text, name) => {
              const { readings, images } = parsePlaybackFile(text);
              loadPlayback(readings, images, name);
            }}
            fileName={fileName}
            error={error}
          />
        </aside>

        <main className="relative min-w-0 flex-1">
          <MapView allReadings={readings} visible={visible} justDropped={justDropped} onJumpToTime={jumpToTime} />
          <ImagePanel image={currentImage} />
        </main>

        {selectedMarkerType && (
          <MarkerListPanel
            type={selectedMarkerType}
            markers={selectedMarkerType === 'real' ? realMarkers : syntheticMarkers}
            currentTs={currentTs}
            onJumpToTime={jumpToTime}
            onClose={() => setSelectedMarkerType(null)}
          />
        )}
      </div>

      <footer className="shrink-0 border-t border-slate-800/80 bg-slate-900/40 px-6 py-3">
        <TimeSlider
          currentTs={currentTs}
          minTs={minTs}
          maxTs={maxTs}
          playDirection={playDirection}
          speed={speed}
          shownCount={visible.length}
          totalCount={readings.length}
          totalRealCount={realMarkers.length}
          totalSyntheticCount={syntheticMarkers.length}
          onSeek={jumpToTime}
          onTogglePlay={(direction) => setPlayDirection((d) => (d === direction ? 0 : direction))}
          onSpeedChange={setSpeed}
          onReset={() => {
            setPlayDirection(0);
            setCurrentTs(minTs);
          }}
        />
      </footer>
    </div>
  );
}
