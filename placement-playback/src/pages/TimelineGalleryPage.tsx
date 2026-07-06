import { useEffect, useMemo, useState } from 'react';
import type { ImageFrame, SonarFrame } from '../types';
import { formatTs } from '../utils/formatTs';
import { lastFrameAtOrBefore, shortSensorLabel } from '../utils/frames';
import { MultiTrackTimeline, type TimelineTrack } from '../components/MultiTrackTimeline';
import { FrameGalleryCard } from '../components/FrameGalleryCard';

interface TimelineGalleryPageProps {
  images: ImageFrame[];
  sonarFrames: SonarFrame[];
  minTs: number;
  maxTs: number;
  currentTs: number;
  onSeek: (ts: number) => void;
}

export function TimelineGalleryPage({
  images,
  sonarFrames,
  minTs,
  maxTs,
  currentTs,
  onSeek,
}: TimelineGalleryPageProps) {
  const [zoomRange, setZoomRange] = useState<[number, number] | null>(null);

  // A freshly loaded / re-fetched dataset invalidates any prior zoom window.
  useEffect(() => {
    setZoomRange(null);
  }, [minTs, maxTs]);

  const viewMinTs = zoomRange ? zoomRange[0] : minTs;
  const viewMaxTs = zoomRange ? zoomRange[1] : maxTs;
  const isZoomed = zoomRange !== null;

  const sensorNames = useMemo(() => Array.from(new Set(sonarFrames.map((f) => f.sensorName))).sort(), [sonarFrames]);

  const sonarBySensor = useMemo(() => {
    const map = new Map<string, SonarFrame[]>();
    for (const name of sensorNames) {
      map.set(
        name,
        sonarFrames.filter((f) => f.sensorName === name),
      );
    }
    return map;
  }, [sonarFrames, sensorNames]);

  const tracks: TimelineTrack[] = useMemo(() => {
    const t: TimelineTrack[] = [{ label: 'screen', moments: images.map((i) => i.ts), dotClassName: 'bg-emerald-400' }];
    for (const name of sensorNames) {
      t.push({
        label: shortSensorLabel(name),
        moments: (sonarBySensor.get(name) ?? []).map((f) => f.ts),
        dotClassName: 'bg-sky-400',
      });
    }
    return t;
  }, [images, sensorNames, sonarBySensor]);

  const latestImage = lastFrameAtOrBefore(images, currentTs);

  const fullSpan = Math.max(maxTs - minTs, 1);
  const allMoments = useMemo(() => tracks.flatMap((t) => t.moments), [tracks]);

  function recenterZoom(clickedTs: number) {
    const halfWidth = (viewMaxTs - viewMinTs) / 2;
    setZoomRange([Math.max(minTs, clickedTs - halfWidth), Math.min(maxTs, clickedTs + halfWidth)]);
  }

  return (
    <div className="flex flex-1 flex-col gap-8 overflow-y-auto p-6">
      <section>
        <div className="mb-3 flex items-baseline justify-between gap-3">
          <div className="flex items-baseline gap-3">
            <h2 className="text-sm font-semibold text-slate-200">Timeline</h2>
            <span className="text-xs text-slate-500">
              {isZoomed ? 'Drag to zoom further · ' : 'Drag to zoom in · '}click to seek
            </span>
          </div>
          <div className="flex items-center gap-3">
            {isZoomed && (
              <>
                <span className="font-mono text-xs text-slate-500">
                  {formatTs(viewMinTs)} – {formatTs(viewMaxTs)}
                </span>
                <button
                  onClick={() => setZoomRange(null)}
                  className="rounded border border-slate-700 px-2 py-0.5 text-xs font-medium text-slate-300 transition-colors hover:border-slate-500 hover:bg-slate-800"
                >
                  Reset zoom
                </button>
              </>
            )}
            <span className="font-mono text-xs text-slate-400">{minTs < maxTs ? formatTs(currentTs) : '—'}</span>
          </div>
        </div>
        {tracks.some((t) => t.moments.length > 0) ? (
          <>
            <MultiTrackTimeline
              tracks={tracks}
              minTs={viewMinTs}
              maxTs={viewMaxTs}
              currentTs={currentTs}
              onSeek={onSeek}
              onZoom={(start, end) => setZoomRange([start, end])}
            />
            {isZoomed && (
              <div
                className="relative mt-2 ml-[76px] h-3 cursor-pointer rounded bg-slate-800/50"
                title="Click to re-center the zoom window"
                onClick={(e) => {
                  const rect = e.currentTarget.getBoundingClientRect();
                  const fraction = Math.min(1, Math.max(0, (e.clientX - rect.left) / rect.width));
                  recenterZoom(minTs + fraction * fullSpan);
                }}
              >
                {allMoments.map((ts, i) => (
                  <span
                    key={i}
                    className="absolute top-1/2 h-1.5 w-px -translate-y-1/2 bg-slate-500"
                    style={{ left: `${((ts - minTs) / fullSpan) * 100}%` }}
                  />
                ))}
                <div
                  className="pointer-events-none absolute inset-y-0 rounded-sm bg-sky-400/30 ring-1 ring-sky-300/70"
                  style={{
                    left: `${((viewMinTs - minTs) / fullSpan) * 100}%`,
                    width: `${((viewMaxTs - viewMinTs) / fullSpan) * 100}%`,
                  }}
                />
              </div>
            )}
          </>
        ) : (
          <p className="text-sm text-slate-500">No image or sonar data loaded.</p>
        )}
      </section>

      <section>
        <h2 className="mb-3 text-sm font-semibold text-slate-200">Latest frame per source</h2>
        <div className="flex flex-wrap gap-3">
          <FrameGalleryCard
            label="screen"
            dotClassName="bg-emerald-400"
            frame={latestImage}
            currentTs={currentTs}
          />
          {sensorNames.map((name) => (
            <FrameGalleryCard
              key={name}
              label={shortSensorLabel(name)}
              dotClassName="bg-sky-400"
              frame={lastFrameAtOrBefore(sonarBySensor.get(name) ?? [], currentTs)}
              currentTs={currentTs}
            />
          ))}
        </div>
      </section>
    </div>
  );
}
