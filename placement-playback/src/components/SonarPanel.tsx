import { useMemo } from 'react';
import type { SonarFrame } from '../types';
import { formatTs } from '../utils/formatTs';
import {
  frameAgeMs,
  indexAtOrBefore,
  lastFrameAtOrBefore,
  nextDistinctTs,
  prevDistinctTs,
  shortSensorLabel,
  stalenessColor,
} from '../utils/frames';
import { CarouselHeader } from './CarouselHeader';

interface SonarPanelProps {
  sonarFrames: SonarFrame[];
  currentTs: number;
  onJumpToTime: (ts: number) => void;
}

function SonarSensorCard({
  sensorName,
  frames,
  currentTs,
  onJumpToTime,
}: {
  sensorName: string;
  frames: SonarFrame[];
  currentTs: number;
  onJumpToTime: (ts: number) => void;
}) {
  const index = indexAtOrBefore(frames, currentTs);
  const frame = lastFrameAtOrBefore(frames, currentTs);
  const ageMs = frame ? frameAgeMs(frame.ts, currentTs) : 0;
  const prevTs = prevDistinctTs(frames, currentTs);
  const nextTs = nextDistinctTs(frames, currentTs);

  function prev() {
    if (prevTs !== null) onJumpToTime(prevTs);
  }
  function next() {
    if (nextTs !== null) onJumpToTime(nextTs);
  }

  return (
    <div className="w-full overflow-hidden rounded-2xl border border-slate-700/70 bg-slate-900/90 shadow-2xl">
      <CarouselHeader
        label={shortSensorLabel(sensorName)}
        dotClassName="bg-sky-400"
        index={index}
        total={frames.length}
        canPrev={prevTs !== null}
        canNext={nextTs !== null}
        onPrev={prev}
        onNext={next}
      />
      {frame ? (
        <img
          className="block aspect-square w-full bg-black object-contain"
          src={`data:${frame.mimeType};base64,${frame.dataBase64}`}
          alt={`${sensorName} sonar frame`}
        />
      ) : (
        <div className="flex aspect-square w-full items-center justify-center bg-black text-xs text-slate-600">
          no frame yet
        </div>
      )}
      <div
        className="px-3 py-1.5 font-mono text-sm font-medium"
        style={{ color: frame ? stalenessColor(ageMs) : undefined }}
      >
        {frame ? formatTs(frame.ts) : '—'}
      </div>
    </div>
  );
}

export function SonarPanel({ sonarFrames, currentTs, onJumpToTime }: SonarPanelProps) {
  const sensorNames = useMemo(() => Array.from(new Set(sonarFrames.map((f) => f.sensorName))).sort(), [sonarFrames]);

  if (sensorNames.length === 0) return null;

  return (
    <div className="flex flex-col gap-3">
      {sensorNames.map((sensorName) => (
        <SonarSensorCard
          key={sensorName}
          sensorName={sensorName}
          frames={sonarFrames.filter((f) => f.sensorName === sensorName)}
          currentTs={currentTs}
          onJumpToTime={onJumpToTime}
        />
      ))}
    </div>
  );
}
