import { useState, type MouseEvent as ReactMouseEvent } from 'react';

export interface TimelineTrack {
  label: string;
  moments: number[]; // this source's frame timestamps, ascending
  dotClassName: string;
}

interface MultiTrackTimelineProps {
  tracks: TimelineTrack[];
  minTs: number;
  maxTs: number;
  currentTs: number;
  onSeek: (ts: number) => void;
  /** Called with (start, end) when the user drag-selects a sub-range to zoom into. */
  onZoom?: (start: number, end: number) => void;
}

// Below this drag distance, a mousedown+mouseup is treated as a click-to-seek instead of a
// range selection — otherwise every ordinary click would be swallowed as a zero-width zoom.
const DRAG_THRESHOLD_PX = 5;

export function MultiTrackTimeline({ tracks, minTs, maxTs, currentTs, onSeek, onZoom }: MultiTrackTimelineProps) {
  const span = Math.max(maxTs - minTs, 1);
  const playheadPct = Math.min(100, Math.max(0, ((currentTs - minTs) / span) * 100));
  const [dragPct, setDragPct] = useState<{ startPct: number; endPct: number } | null>(null);

  function handleMouseDown(e: ReactMouseEvent<HTMLDivElement>) {
    if (e.button !== 0) return;
    const rect = e.currentTarget.getBoundingClientRect();
    const tsAt = (clientX: number) => {
      const fraction = Math.min(1, Math.max(0, (clientX - rect.left) / rect.width));
      return minTs + fraction * span;
    };
    const pctOf = (ts: number) => ((ts - minTs) / span) * 100;
    const startClientX = e.clientX;
    const startTs = tsAt(startClientX);
    setDragPct({ startPct: pctOf(startTs), endPct: pctOf(startTs) });

    function onMove(ev: MouseEvent) {
      const ts = tsAt(ev.clientX);
      setDragPct({ startPct: pctOf(Math.min(startTs, ts)), endPct: pctOf(Math.max(startTs, ts)) });
    }
    function onUp(ev: MouseEvent) {
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
      setDragPct(null);
      const draggedPx = Math.abs(ev.clientX - startClientX);
      const endTs = tsAt(ev.clientX);
      if (draggedPx < DRAG_THRESHOLD_PX || !onZoom) {
        onSeek(endTs);
        return;
      }
      const lo = Math.min(startTs, endTs);
      const hi = Math.max(startTs, endTs);
      if (hi - lo > 0) onZoom(lo, hi);
    }
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
  }

  return (
    <div className="flex flex-col gap-2.5">
      {tracks.map((track) => (
        <div key={track.label} className="flex items-center gap-3">
          <span className="w-16 shrink-0 text-right font-mono text-xs tracking-wide text-slate-400 uppercase">
            {track.label}
          </span>
          <div
            className="relative h-7 flex-1 cursor-crosshair rounded bg-slate-800/70 transition-colors select-none hover:bg-slate-800"
            onMouseDown={handleMouseDown}
          >
            {track.moments.map((ts, i) => (
              <span
                // Index, not ts: two frames from the same source can legitimately share a
                // millisecond-rounded timestamp, which would otherwise collide as a React key.
                key={i}
                className={`absolute top-1/2 h-3.5 w-[3px] -translate-x-1/2 -translate-y-1/2 rounded-full opacity-80 ${track.dotClassName}`}
                style={{ left: `${((ts - minTs) / span) * 100}%` }}
              />
            ))}
            <div
              className="pointer-events-none absolute inset-y-0 w-0.5 bg-white shadow-[0_0_4px_rgba(255,255,255,0.8)]"
              style={{ left: `${playheadPct}%` }}
            />
            {dragPct && (
              <div
                className="pointer-events-none absolute inset-y-0 rounded-sm bg-sky-400/25 ring-1 ring-sky-300/60"
                style={{ left: `${dragPct.startPct}%`, width: `${dragPct.endPct - dragPct.startPct}%` }}
              />
            )}
          </div>
        </div>
      ))}
    </div>
  );
}
