import { useEffect, useMemo, useRef, useState } from 'react';
import { MapView } from './components/MapView';
import { TimeSlider } from './components/TimeSlider';
import { Legend } from './components/Legend';
import { FileLoader } from './components/FileLoader';
import { parseReadingsFile } from './utils/parseReadings';
import type { Reading } from './types';
import './App.css';

const TICK_MS = 400;

export default function App() {
  const [readings, setReadings] = useState<Reading[]>([]);
  const [fileName, setFileName] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [index, setIndex] = useState(0);
  const [playing, setPlaying] = useState(false);
  const [speed, setSpeed] = useState(1);

  const uniqueTs = useMemo(() => Array.from(new Set(readings.map((r) => r.ts))).sort((a, b) => a - b), [readings]);
  const maxIndex = Math.max(uniqueTs.length - 1, 0);
  const currentTs = uniqueTs[index] ?? 0;

  const visible = useMemo(() => readings.filter((r) => r.ts <= currentTs), [readings, currentTs]);
  const justDropped = useMemo(() => readings.filter((r) => r.ts === currentTs), [readings, currentTs]);
  const totalRealCount = useMemo(() => readings.filter((r) => !r.is_synthetic).length, [readings]);
  const shownRealCount = useMemo(() => visible.filter((r) => !r.is_synthetic).length, [visible]);

  const intervalRef = useRef<number | null>(null);

  useEffect(() => {
    if (!playing) return;
    intervalRef.current = window.setInterval(() => {
      setIndex((i) => {
        if (i >= maxIndex) {
          setPlaying(false);
          return i;
        }
        return i + 1;
      });
    }, TICK_MS / speed);
    return () => {
      if (intervalRef.current !== null) window.clearInterval(intervalRef.current);
    };
  }, [playing, speed, maxIndex]);

  function loadReadings(list: Reading[], name: string | null) {
    if (list.length === 0) {
      setError('No valid readings found (need at least latitude and longitude fields).');
      return;
    }
    setError(null);
    setFileName(name);
    setReadings(list);
    setIndex(0);
    setPlaying(false);
  }

  return (
    <div className="app">
      <header className="app__header">
        <h1>placement-playback</h1>
        <Legend />
      </header>
      <div className="app__body">
        <aside className="app__sidebar">
          <FileLoader
            onLoad={(text, name) => loadReadings(parseReadingsFile(text), name)}
            fileName={fileName}
            error={error}
          />
        </aside>
        <main className="app__map">
          <MapView allReadings={readings} visible={visible} justDropped={justDropped} />
        </main>
      </div>
      <footer className="app__footer">
        <TimeSlider
          index={index}
          maxIndex={maxIndex}
          currentTs={currentTs}
          playing={playing}
          speed={speed}
          shownCount={visible.length}
          totalCount={readings.length}
          shownRealCount={shownRealCount}
          totalRealCount={totalRealCount}
          onIndexChange={(i) => {
            setPlaying(false);
            setIndex(i);
          }}
          onTogglePlay={() => setPlaying((p) => !p)}
          onSpeedChange={setSpeed}
          onReset={() => {
            setPlaying(false);
            setIndex(0);
          }}
        />
      </footer>
    </div>
  );
}
