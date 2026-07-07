import { useState, type MouseEvent as ReactMouseEvent } from 'react';
import { datetimeLocalValueToTs, formatTs, isEpochTs, tsToDatetimeLocalValue } from '../utils/formatTs';

interface EditableBoundaryProps {
  ts: number;
  minTs: number;
  maxTs: number;
  align: 'left' | 'right';
  onCommit: (ts: number) => void;
}

/** A range boundary label that turns into a date/time (or raw-value) input on click. */
function EditableBoundary({ ts, minTs, maxTs, align, onCommit }: EditableBoundaryProps) {
  const [draft, setDraft] = useState<string | null>(null);
  const epoch = isEpochTs(ts);

  function startEditing() {
    setDraft(epoch ? tsToDatetimeLocalValue(ts) : String(ts));
  }

  function commit() {
    if (draft !== null && draft !== '') {
      const parsed = epoch ? datetimeLocalValueToTs(draft, ts) : Number(draft);
      if (Number.isFinite(parsed)) onCommit(Math.min(maxTs, Math.max(minTs, parsed)));
    }
    setDraft(null);
  }

  if (draft !== null) {
    return (
      <input
        type={epoch ? 'datetime-local' : 'number'}
        step={epoch ? 1 : undefined}
        autoFocus
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={commit}
        onKeyDown={(e) => {
          if (e.key === 'Enter') commit();
          if (e.key === 'Escape') setDraft(null);
        }}
        className={`w-40 shrink-0 rounded-md border border-sky-400/50 bg-slate-900 px-1.5 py-0.5 font-mono text-xs text-white focus:outline-none ${
          align === 'right' ? 'text-right' : ''
        }`}
      />
    );
  }

  return (
    <button
      onClick={startEditing}
      title="Click to set an exact time"
      className={`w-40 shrink-0 rounded-md px-1.5 py-0.5 font-mono text-xs whitespace-nowrap text-slate-300 transition-colors hover:bg-slate-800 hover:text-white ${
        align === 'right' ? 'text-right' : ''
      }`}
    >
      {formatTs(ts)}
    </button>
  );
}

interface TimeRangeNavProps {
  /** Full extent of the loaded dataset — the outer bounds the range handles can't leave. */
  minTs: number;
  maxTs: number;
  /** The active filter: every screen (map, gallery, panels) only shows data inside this window. */
  rangeStart: number;
  rangeEnd: number;
  onRangeChange: (start: number, end: number) => void;
}

// Below this drag distance, a press+release on bare track is treated as an accidental click
// rather than an intentional (but tiny) range selection.
const DRAG_THRESHOLD_PX = 4;

export function TimeRangeNav({ minTs, maxTs, rangeStart, rangeEnd, onRangeChange }: TimeRangeNavProps) {
  const disabled = maxTs <= minTs;
  const span = Math.max(maxTs - minTs, 1);
  const pct = (ts: number) => Math.min(100, Math.max(0, ((ts - minTs) / span) * 100));
  const startPct = pct(rangeStart);
  const endPct = pct(rangeEnd);
  const isFullRange = rangeStart <= minTs && rangeEnd >= maxTs;

  function tsAt(rect: DOMRect, clientX: number) {
    const fraction = Math.min(1, Math.max(0, (clientX - rect.left) / rect.width));
    return minTs + fraction * span;
  }

  function handleHandleDrag(which: 'start' | 'end') {
    return (e: ReactMouseEvent<HTMLDivElement>) => {
      if (disabled) return;
      e.preventDefault();
      e.stopPropagation();
      const rect = (e.currentTarget.parentElement as HTMLElement).getBoundingClientRect();

      function onMove(ev: MouseEvent) {
        const ts = tsAt(rect, ev.clientX);
        if (which === 'start') onRangeChange(Math.min(ts, rangeEnd), rangeEnd);
        else onRangeChange(rangeStart, Math.max(ts, rangeStart));
      }
      function onUp() {
        window.removeEventListener('mousemove', onMove);
        window.removeEventListener('mouseup', onUp);
      }
      window.addEventListener('mousemove', onMove);
      window.addEventListener('mouseup', onUp);
    };
  }

  // Press+drag on bare track carves out a brand new range, replacing whatever was selected —
  // this is the one control every screen's data now flows through.
  function handleTrackMouseDown(e: ReactMouseEvent<HTMLDivElement>) {
    if (disabled) return;
    const rect = e.currentTarget.getBoundingClientRect();
    const startClientX = e.clientX;
    const startTs = tsAt(rect, startClientX);

    function onMove(ev: MouseEvent) {
      const ts = tsAt(rect, ev.clientX);
      onRangeChange(Math.min(startTs, ts), Math.max(startTs, ts));
    }
    function onUp(ev: MouseEvent) {
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
      // A near-zero-distance press is almost certainly an accidental click, not an attempt to
      // select a (near) zero-width range — leave the existing range alone.
      if (Math.abs(ev.clientX - startClientX) < DRAG_THRESHOLD_PX) {
        onRangeChange(rangeStart, rangeEnd);
      }
    }
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
  }

  return (
    <div className="flex flex-1 items-center gap-3">
      {disabled ? (
        <span className="w-40 shrink-0 font-mono text-xs whitespace-nowrap text-slate-300">—</span>
      ) : (
        <EditableBoundary
          ts={rangeStart}
          minTs={minTs}
          maxTs={rangeEnd}
          align="left"
          onCommit={(ts) => onRangeChange(ts, rangeEnd)}
        />
      )}
      <div
        className="relative h-1.5 flex-1 cursor-crosshair rounded-full bg-slate-800"
        onMouseDown={handleTrackMouseDown}
      >
        <div
          className="pointer-events-none absolute inset-y-0 rounded-full bg-sky-400/30"
          style={{ left: `${startPct}%`, width: `${Math.max(0, endPct - startPct)}%` }}
        />
        {!disabled && (
          <>
            <div
              role="slider"
              aria-label="Range start"
              aria-valuemin={minTs}
              aria-valuemax={maxTs}
              aria-valuenow={rangeStart}
              onMouseDown={handleHandleDrag('start')}
              className="absolute top-1/2 z-10 h-3.5 w-3.5 -translate-x-1/2 -translate-y-1/2 cursor-grab rounded-full border-2 border-sky-300 bg-slate-900 shadow active:cursor-grabbing"
              style={{ left: `${startPct}%` }}
            />
            <div
              role="slider"
              aria-label="Range end"
              aria-valuemin={minTs}
              aria-valuemax={maxTs}
              aria-valuenow={rangeEnd}
              onMouseDown={handleHandleDrag('end')}
              className="absolute top-1/2 z-10 h-3.5 w-3.5 -translate-x-1/2 -translate-y-1/2 cursor-grab rounded-full border-2 border-sky-300 bg-slate-900 shadow active:cursor-grabbing"
              style={{ left: `${endPct}%` }}
            />
          </>
        )}
      </div>
      {disabled ? (
        <span className="w-40 shrink-0 text-right font-mono text-xs whitespace-nowrap text-slate-300">—</span>
      ) : (
        <EditableBoundary
          ts={rangeEnd}
          minTs={rangeStart}
          maxTs={maxTs}
          align="right"
          onCommit={(ts) => onRangeChange(rangeStart, ts)}
        />
      )}
      <button
        onClick={() => onRangeChange(minTs, maxTs)}
        disabled={disabled || isFullRange}
        className="shrink-0 rounded-full border border-slate-700 px-2 py-0.5 text-xs font-medium text-slate-300 transition-colors hover:border-slate-500 hover:bg-slate-800 disabled:cursor-not-allowed disabled:opacity-30"
      >
        Reset range
      </button>
    </div>
  );
}
