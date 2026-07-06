import { formatTs } from '../utils/formatTs';
import { frameAgeMs, stalenessColor } from '../utils/frames';

interface FrameGalleryCardProps {
  label: string;
  dotClassName: string;
  frame: { ts: number; mimeType: string; dataBase64: string } | null;
  currentTs: number;
}

export function FrameGalleryCard({ label, dotClassName, frame, currentTs }: FrameGalleryCardProps) {
  const ageMs = frame ? frameAgeMs(frame.ts, currentTs) : 0;

  return (
    <div className="w-64 min-w-0 flex-1 overflow-hidden rounded-2xl border border-slate-700/70 bg-slate-900/90 shadow-xl">
      <div className="flex items-center gap-1.5 border-b border-slate-800 px-3 py-1.5">
        <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${dotClassName}`} />
        <span className="truncate text-xs font-semibold tracking-wide text-slate-200 uppercase">{label}</span>
      </div>
      {frame ? (
        <img
          className="block aspect-square w-full bg-black object-contain"
          src={`data:${frame.mimeType};base64,${frame.dataBase64}`}
          alt={`${label} frame`}
        />
      ) : (
        <div className="flex aspect-square w-full items-center justify-center bg-black text-xs text-slate-600">
          no frame yet
        </div>
      )}
      <div
        className="px-3 py-1.5 text-center font-mono text-xs font-medium"
        style={{ color: frame ? stalenessColor(ageMs) : undefined }}
      >
        {frame ? formatTs(frame.ts) : '—'}
      </div>
    </div>
  );
}
