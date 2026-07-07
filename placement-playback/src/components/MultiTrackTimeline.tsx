import type { MouseEvent as ReactMouseEvent } from 'react';
import type { TimelineTrack } from '../types';

interface MultiTrackTimelineProps {
  tracks: TimelineTrack[];
  minTs: number;
  maxTs: number;
  currentTs: number;
  onSeek: (ts: number) => void;
}

export function MultiTrackTimeline({ tracks, minTs, maxTs, currentTs, onSeek }: MultiTrackTimelineProps) {
  const span = Math.max(maxTs - minTs, 1);
  const playheadPct = Math.min(100, Math.max(0, ((currentTs - minTs) / span) * 100));

  function handleMouseDown(e: ReactMouseEvent<HTMLDivElement>) {
    if (e.button !== 0) return;
    const rect = e.currentTarget.getBoundingClientRect();
    const tsAt = (clientX: number) => {
      const fraction = Math.min(1, Math.max(0, (clientX - rect.left) / rect.width));
      return minTs + fraction * span;
    };
    onSeek(tsAt(e.clientX));

    function onMove(ev: MouseEvent) {
      onSeek(tsAt(ev.clientX));
    }
    function onUp() {
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
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
            className="relative h-7 flex-1 cursor-pointer rounded bg-slate-800/70 transition-colors select-none hover:bg-slate-800"
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
          </div>
        </div>
      ))}
    </div>
  );
}
