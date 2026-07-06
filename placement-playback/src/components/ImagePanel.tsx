import type { ImageFrame } from '../types';
import { formatTs } from '../utils/formatTs';

interface ImagePanelProps {
  image: ImageFrame | null;
}

export function ImagePanel({ image }: ImagePanelProps) {
  if (!image) return null;

  return (
    <div className="absolute top-3 right-3 z-500 w-[45%] min-w-80 max-w-160 overflow-hidden rounded-2xl border border-slate-700/70 bg-slate-900/90 shadow-2xl backdrop-blur">
      <div className="flex items-center gap-1.5 border-b border-slate-800 px-3 py-1.5">
        <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-emerald-400" />
        <span className="text-xs font-semibold tracking-wide text-slate-200 uppercase">Camera feed</span>
      </div>
      <img
        className="block max-h-[60vh] w-full bg-black object-contain"
        src={`data:${image.mimeType};base64,${image.dataBase64}`}
        alt="camera frame"
      />
      <div className="px-3 py-1.5 font-mono text-sm font-medium text-slate-200">{formatTs(image.ts)}</div>
    </div>
  );
}
