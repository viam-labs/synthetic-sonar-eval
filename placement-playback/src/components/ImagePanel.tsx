import type { ImageFrame } from '../types';
import { formatTs } from '../utils/formatTs';
import {
  frameAgeMs,
  indexAtOrBefore,
  lastFrameAtOrBefore,
  nextDistinctTs,
  prevDistinctTs,
  stalenessColor,
} from '../utils/frames';
import { CarouselHeader } from './CarouselHeader';

interface ImagePanelProps {
  images: ImageFrame[];
  currentTs: number;
  onJumpToTime: (ts: number) => void;
}

export function ImagePanel({ images, currentTs, onJumpToTime }: ImagePanelProps) {
  if (images.length === 0) return null;

  const index = indexAtOrBefore(images, currentTs);
  const image = lastFrameAtOrBefore(images, currentTs);
  const ageMs = image ? frameAgeMs(image.ts, currentTs) : 0;
  const prevTs = prevDistinctTs(images, currentTs);
  const nextTs = nextDistinctTs(images, currentTs);

  function prev() {
    if (prevTs !== null) onJumpToTime(prevTs);
  }
  function next() {
    if (nextTs !== null) onJumpToTime(nextTs);
  }

  return (
    <div className="w-full overflow-hidden rounded-2xl border border-slate-700/70 bg-slate-900/90 shadow-2xl">
      <CarouselHeader
        label="screen"
        index={index}
        total={images.length}
        canPrev={prevTs !== null}
        canNext={nextTs !== null}
        onPrev={prev}
        onNext={next}
      />
      {image ? (
        <img
          className="block aspect-square w-full bg-black object-contain"
          src={`data:${image.mimeType};base64,${image.dataBase64}`}
          alt="camera frame"
        />
      ) : (
        <div className="flex aspect-square w-full items-center justify-center bg-black text-xs text-slate-600">
          no frame yet
        </div>
      )}
      <div
        className="px-3 py-1.5 font-mono text-sm font-medium"
        style={{ color: image ? stalenessColor(ageMs) : undefined }}
      >
        {image ? formatTs(image.ts) : '—'}
      </div>
    </div>
  );
}
