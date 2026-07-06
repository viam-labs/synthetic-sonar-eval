import type { ReactNode } from 'react';
import { formatTs } from '../utils/formatTs';

const MIN_SPEED = 1;
const MAX_SPEED = 1000;

function clampSpeed(value: number): number {
  return Math.min(MAX_SPEED, Math.max(MIN_SPEED, Math.round(value)));
}

interface TimeSliderProps {
  currentTs: number;
  minTs: number;
  maxTs: number;
  playDirection: 0 | 1 | -1;
  speed: number;
  shownCount: number;
  totalCount: number;
  totalRealCount: number;
  totalSyntheticCount: number;
  onSeek: (ts: number) => void;
  onTogglePlay: (direction: 1 | -1) => void;
  onSpeedChange: (speed: number) => void;
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
        <input
          type="range"
          min={minTs}
          max={maxTs}
          step={1}
          value={currentTs}
          onChange={(e) => onSeek(Number(e.target.value))}
          disabled={disabled}
          className="h-1.5 w-full flex-1 cursor-pointer accent-sky-400 disabled:cursor-not-allowed disabled:opacity-40"
        />
        <span className="w-48 shrink-0 text-right font-mono text-sm font-medium whitespace-nowrap text-slate-200">
          {disabled ? '—' : formatTs(currentTs)}
        </span>
      </div>

      <div className="flex flex-wrap items-center gap-4">
        <div className="flex items-center gap-2">
          <IconButton onClick={onReset} disabled={disabled} label="Reset to start">
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
