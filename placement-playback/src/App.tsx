import { useEffect, useMemo, useRef, useState } from 'react';
import { MapView } from './components/MapView';
import { TimeSlider } from './components/TimeSlider';
import { Legend } from './components/Legend';
import { FileLoader } from './components/FileLoader';
import { ImagePanel } from './components/ImagePanel';
import { SonarPanel } from './components/SonarPanel';
import { MarkerListPanel } from './components/MarkerListPanel';
import { TimelineGalleryPage } from './pages/TimelineGalleryPage';
import { parsePlaybackFile } from './utils/parseReadings';
import type { ImageFrame, Reading, SonarFrame } from './types';

const TICK_MS = 50;
// A marker still gets the "just dropped" pulse animation for this long (in
// simulated time) after its own ts, so continuous scrubbing/playback still
// reads as markers appearing rather than just popping into a static list.
const DROP_WINDOW_MS = 3_000;

export default function App() {
  const [readings, setReadings] = useState<Reading[]>([]);
  const [images, setImages] = useState<ImageFrame[]>([]);
  const [sonarFrames, setSonarFrames] = useState<SonarFrame[]>([]);
  const [fileName, setFileName] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [currentTs, setCurrentTs] = useState(0);
  // 0 = paused, 1 = playing forward, -1 = playing in reverse.
  const [playDirection, setPlayDirection] = useState<0 | 1 | -1>(0);
  const [speed, setSpeed] = useState(1);
  const [selectedMarkerType, setSelectedMarkerType] = useState<'real' | 'synthetic' | null>(null);
  const [showLoader, setShowLoader] = useState(true);
  const [page, setPage] = useState<'map' | 'gallery'>('map');

  // The timeline spans the earliest/latest event across readings, camera frames, and sonar
  // frames, not just readings — otherwise images/sonar pings outside the marker time range
  // would never be reachable by scrubbing.
  const allTs = useMemo(
    () => [...readings.map((r) => r.ts), ...images.map((i) => i.ts), ...sonarFrames.map((f) => f.ts)],
    [readings, images, sonarFrames],
  );
  const minTs = useMemo(() => (allTs.length ? Math.min(...allTs) : 0), [allTs]);
  const maxTs = useMemo(() => (allTs.length ? Math.max(...allTs) : 0), [allTs]);

  const visible = useMemo(() => readings.filter((r) => r.ts <= currentTs), [readings, currentTs]);
  const justDropped = useMemo(
    () => readings.filter((r) => r.ts <= currentTs && currentTs - r.ts <= DROP_WINDOW_MS),
    [readings, currentTs],
  );
  // Already sorted ascending by ts (parsePlaybackFile guarantees this).
  const realMarkers = useMemo(() => readings.filter((r) => !r.is_synthetic), [readings]);
  const syntheticMarkers = useMemo(() => readings.filter((r) => r.is_synthetic), [readings]);

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

  function loadPlayback(list: Reading[], frames: ImageFrame[], sonar: SonarFrame[], name: string | null) {
    if (list.length === 0) {
      setError('No valid readings found (need at least latitude and longitude fields).');
      return;
    }
    setError(null);
    setFileName(name);
    setReadings(list);
    setImages(frames);
    setSonarFrames(sonar);
    const allTs = [...list.map((r) => r.ts), ...frames.map((f) => f.ts), ...sonar.map((f) => f.ts)];
    setCurrentTs(Math.min(...allTs));
    setPlayDirection(0);
    setShowLoader(false);
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
        <div className="flex items-center gap-4">
          {!showLoader && (
            <div className="flex items-center gap-1 rounded-full border border-slate-700 bg-slate-900/60 p-0.5 text-sm">
              <button
                onClick={() => setPage('map')}
                className={`rounded-full px-3 py-1 font-medium transition-colors ${
                  page === 'map' ? 'bg-sky-400/20 text-sky-200' : 'text-slate-400 hover:text-slate-200'
                }`}
              >
                Map
              </button>
              <button
                onClick={() => setPage('gallery')}
                className={`rounded-full px-3 py-1 font-medium transition-colors ${
                  page === 'gallery' ? 'bg-sky-400/20 text-sky-200' : 'text-slate-400 hover:text-slate-200'
                }`}
              >
                Gallery
              </button>
            </div>
          )}
          <Legend
            selected={selectedMarkerType}
            onSelect={(type) => setSelectedMarkerType((cur) => (cur === type ? null : type))}
          />
        </div>
      </header>

      {!showLoader && page === 'gallery' ? (
        <TimelineGalleryPage
          images={images}
          sonarFrames={sonarFrames}
          minTs={minTs}
          maxTs={maxTs}
          currentTs={currentTs}
          onSeek={jumpToTime}
        />
      ) : (
        <div className="flex min-h-0 flex-1">
          <aside className="w-104 shrink-0 overflow-y-auto border-r border-slate-800/80 bg-slate-900/20 p-4">
            {showLoader ? (
              <FileLoader
                onLoad={(text, name) => {
                  const { readings, images, sonarFrames } = parsePlaybackFile(text);
                  loadPlayback(readings, images, sonarFrames, name);
                }}
                fileName={fileName}
                error={error}
              />
            ) : (
              <div className="flex flex-col gap-3">
                <div className="flex items-center justify-between gap-2">
                  <p className="truncate text-xs text-slate-400" title={fileName ?? undefined}>
                    {fileName}
                  </p>
                  <button
                    onClick={() => {
                      setShowLoader(true);
                      setFileName(null);
                    }}
                    className="shrink-0 text-xs font-medium text-sky-300 hover:text-sky-200 hover:underline"
                  >
                    Change file
                  </button>
                </div>
                <ImagePanel images={images} currentTs={currentTs} onJumpToTime={jumpToTime} />
                <SonarPanel sonarFrames={sonarFrames} currentTs={currentTs} onJumpToTime={jumpToTime} />
              </div>
            )}
          </aside>

          <main className="relative min-w-0 flex-1">
            <MapView allReadings={readings} visible={visible} justDropped={justDropped} onJumpToTime={jumpToTime} />
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
      )}

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
