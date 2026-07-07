import type { ReactNode, MouseEvent as ReactMouseEvent } from 'react';
import { formatTs } from '../utils/formatTs';

const MIN_SPEED = 1;
const MAX_SPEED = 1000;

function clampSpeed(value: number): number {
  return Math.min(MAX_SPEED, Math.max(MIN_SPEED, Math.round(value)));
}

interface TimeSliderProps {
  /** The live playhead that advances during playback, bounded by the navbar's selected range. */
  currentTs: number;
  /** Left/right bounds for the playhead — the range selected in the TimeRangeNav. */
  minTs: number;
  maxTs: number;
  playDirection: 0 | 1 | -1;
  speed: number;
  shownCount: number;
  totalCount: number;
  totalRealCount: number;
  totalSyntheticCount: number;
  /** Moves the playhead (dragging/clicking the track or the handle); pauses playback. */
  onSeek: (ts: number) => void;
  onTogglePlay: (direction: 1 | -1) => void;
  onSpeedChange: (speed: number) => void;
  /** Rewinds the playhead back to the range start. */
  onReset: () => void;
}

function IconButton({
  onClick,
  disabled,
  label,
  active,
  children,
}: {
  onClick: () => void;
  disabled: boolean;
  label: string;
  active?: boolean;
  children: ReactNode;
}) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      aria-label={label}
      title={label}
      className={`flex h-8 w-8 shrink-0 items-center justify-center rounded-full border transition-colors disabled:cursor-not-allowed disabled:opacity-30 ${
        active
          ? 'border-sky-400/50 bg-sky-400/15 text-sky-300 hover:bg-sky-400/25'
          : 'border-slate-700 bg-slate-800/60 text-slate-100 hover:border-slate-600 hover:bg-slate-800'
      }`}
    >
      {children}
    </button>
  );
}

function PauseIcon() {
  return (
    <svg viewBox="0 0 24 24" fill="currentColor" className="h-4 w-4">
      <path d="M7 5.5h3v13H7zM14 5.5h3v13h-3z" />
    </svg>
  );
}

interface TrackProps {
  minTs: number;
  maxTs: number;
  currentTs: number;
  disabled: boolean;
  onSeek: (ts: number) => void;
}

function Track({ minTs, maxTs, currentTs, disabled, onSeek }: TrackProps) {
  const span = Math.max(maxTs - minTs, 1);
  const pct = (ts: number) => Math.min(100, Math.max(0, ((ts - minTs) / span) * 100));
  const currentPct = pct(currentTs);

  function tsAt(rect: DOMRect, clientX: number) {
    const fraction = Math.min(1, Math.max(0, (clientX - rect.left) / rect.width));
    return minTs + fraction * span;
  }

  function handleTrackMouseDown(e: ReactMouseEvent<HTMLDivElement>) {
    if (disabled) return;
    const rect = e.currentTarget.getBoundingClientRect();
    onSeek(tsAt(rect, e.clientX));

    // Also let a press-and-drag on bare track continuously scrub the playhead, matching how a
    // native range input behaves (not just a single click-to-seek).
    function onMove(ev: MouseEvent) {
      onSeek(tsAt(rect, ev.clientX));
    }
    function onUp() {
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
    }
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
  }

  function handleHandleMouseDown(e: ReactMouseEvent<HTMLDivElement>) {
    if (disabled) return;
    e.preventDefault();
    e.stopPropagation();
    const rect = (e.currentTarget.parentElement as HTMLElement).getBoundingClientRect();

    function onMove(ev: MouseEvent) {
      onSeek(tsAt(rect, ev.clientX));
    }
    function onUp() {
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
    }
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
  }

  return (
    <div
      className="relative h-1.5 flex-1 cursor-pointer rounded-full bg-slate-800"
      // Triggered on press, not click: a handle-drag that gets clamped can end its mouseup over
      // bare track (the handle didn't visually follow the cursor that far), and a click-based
      // handler would misread that release as a stray seek. mousedown has no such ambiguity —
      // dragging the handle stops propagation on ITS OWN mousedown, so this never double-fires.
      onMouseDown={handleTrackMouseDown}
    >
      <div
        className="pointer-events-none absolute inset-y-0 left-0 rounded-full bg-sky-400/40"
        style={{ width: `${currentPct}%` }}
      />
      {!disabled && (
        <div
          role="slider"
          aria-label="Playhead"
          aria-valuemin={minTs}
          aria-valuemax={maxTs}
          aria-valuenow={currentTs}
          onMouseDown={handleHandleMouseDown}
          className="absolute top-1/2 z-20 h-4 w-4 -translate-x-1/2 -translate-y-1/2 cursor-grab rounded-full border-2 border-sky-300 bg-slate-900 shadow active:cursor-grabbing"
          style={{ left: `${currentPct}%` }}
        />
      )}
    </div>
  );
}

export function TimeSlider({
  currentTs,
  minTs,
  maxTs,
  playDirection,
  speed,
  shownCount,
  totalCount,
  totalRealCount,
  totalSyntheticCount,
  onSeek,
  onTogglePlay,
  onSpeedChange,
  onReset,
}: TimeSliderProps) {
  const disabled = maxTs <= minTs;
  return (
    <div className="flex flex-col gap-2.5">
      <div className="flex items-center gap-3">
        <span className="w-40 shrink-0 font-mono text-xs whitespace-nowrap text-slate-400">
          {disabled ? '—' : formatTs(minTs)}
        </span>
        <Track minTs={minTs} maxTs={maxTs} currentTs={currentTs} disabled={disabled} onSeek={onSeek} />
        <span className="w-40 shrink-0 text-right font-mono text-sm font-medium whitespace-nowrap text-sky-200">
          {disabled ? '—' : formatTs(currentTs)}
        </span>
      </div>

      <div className="flex flex-wrap items-center gap-4">
        <div className="flex items-center gap-2">
          <IconButton onClick={onReset} disabled={disabled} label="Rewind to range start">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.8} className="h-4 w-4">
              <path strokeLinecap="round" strokeLinejoin="round" d="M4 5v5h5M4.05 13a8 8 0 1 0 .5-4.5" />
            </svg>
          </IconButton>
          <IconButton
            onClick={() => onTogglePlay(-1)}
            disabled={disabled}
            label={playDirection === -1 ? 'Pause' : 'Play in reverse'}
            active={playDirection === -1}
          >
            {playDirection === -1 ? (
              <PauseIcon />
            ) : (
              <svg viewBox="0 0 24 24" fill="currentColor" className="mr-0.5 h-4 w-4 scale-x-[-1]">
                <path d="M7 4.5v15l13-7.5z" />
              </svg>
            )}
          </IconButton>
          <IconButton
            onClick={() => onTogglePlay(1)}
            disabled={disabled}
            label={playDirection === 1 ? 'Pause' : 'Play'}
            active={playDirection === 1}
          >
            {playDirection === 1 ? (
              <PauseIcon />
            ) : (
              <svg viewBox="0 0 24 24" fill="currentColor" className="ml-0.5 h-4 w-4">
                <path d="M7 4.5v15l13-7.5z" />
              </svg>
            )}
          </IconButton>
        </div>

        <label className="flex items-center gap-2 text-sm font-medium text-slate-200">
          Speed
          <input
            type="range"
            min={MIN_SPEED}
            max={MAX_SPEED}
            step={1}
            value={speed}
            onChange={(e) => onSpeedChange(Number(e.target.value))}
            className="h-1.5 w-28 cursor-pointer accent-sky-400"
          />
          <input
            type="number"
            min={MIN_SPEED}
            max={MAX_SPEED}
            step={1}
            value={speed}
            onChange={(e) => {
              const value = Number(e.target.value);
              if (Number.isFinite(value)) onSpeedChange(clampSpeed(value));
            }}
            className="w-14 rounded-md border border-slate-700 bg-slate-900 px-1.5 py-0.5 font-mono text-sm text-white focus:border-sky-400 focus:outline-none"
          />
          <span className="text-slate-400">x</span>
        </label>

        <div className="ml-auto flex items-center gap-3 text-sm font-medium text-slate-200">
          <span className="rounded-full border border-slate-700 bg-slate-900/60 px-3 py-1">
            <span className="text-white">{shownCount}</span> / {totalCount} markers
          </span>
          <span className="flex items-center gap-1.5 rounded-full border border-slate-700 bg-slate-900/60 px-3 py-1">
            <span className="h-2 w-2 rounded-full bg-sky-400" />
            <span className="text-white">{totalRealCount}</span> real
          </span>
          <span className="flex items-center gap-1.5 rounded-full border border-slate-700 bg-slate-900/60 px-3 py-1">
            <span className="h-2 w-2 rounded-full border border-dashed border-orange-300 bg-orange-400/70" />
            <span className="text-white">{totalSyntheticCount}</span> synthetic
          </span>
        </div>
      </div>
    </div>
  );
}
