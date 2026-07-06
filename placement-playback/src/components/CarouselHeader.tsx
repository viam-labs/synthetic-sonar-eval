interface CarouselHeaderProps {
  label: string;
  index: number; // -1 if the current time is before this source's first frame (display only)
  total: number;
  canPrev: boolean;
  canNext: boolean;
  onPrev: () => void;
  onNext: () => void;
  dotClassName?: string;
}

export function CarouselHeader({
  label,
  index,
  total,
  canPrev,
  canNext,
  onPrev,
  onNext,
  dotClassName = 'bg-emerald-400',
}: CarouselHeaderProps) {
  return (
    <div className="flex items-center justify-between gap-2 border-b border-slate-800 px-3 py-1.5">
      <div className="flex min-w-0 items-center gap-1.5">
        <span className={`h-1.5 w-1.5 shrink-0 animate-pulse rounded-full ${dotClassName}`} />
        <span className="truncate text-xs font-semibold tracking-wide text-slate-200 uppercase">{label}</span>
      </div>
      <div className="flex shrink-0 items-center gap-1">
        <button
          onClick={onPrev}
          disabled={!canPrev}
          aria-label={`Previous ${label} frame`}
          title={`Previous ${label} frame`}
          className="flex h-5 w-5 items-center justify-center rounded text-slate-300 transition-colors hover:bg-slate-800 disabled:cursor-not-allowed disabled:opacity-30"
        >
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2.5} className="h-3 w-3">
            <path strokeLinecap="round" strokeLinejoin="round" d="M15 19l-7-7 7-7" />
          </svg>
        </button>
        <span className="w-10 text-center font-mono text-[11px] text-slate-400">
          {index + 1}/{total}
        </span>
        <button
          onClick={onNext}
          disabled={!canNext}
          aria-label={`Next ${label} frame`}
          title={`Next ${label} frame`}
          className="flex h-5 w-5 items-center justify-center rounded text-slate-300 transition-colors hover:bg-slate-800 disabled:cursor-not-allowed disabled:opacity-30"
        >
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2.5} className="h-3 w-3">
            <path strokeLinecap="round" strokeLinejoin="round" d="M9 5l7 7-7 7" />
          </svg>
        </button>
      </div>
    </div>
  );
}
