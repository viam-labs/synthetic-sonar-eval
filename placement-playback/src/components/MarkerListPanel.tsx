import type { Reading } from '../types';
import { formatTs } from '../utils/formatTs';

interface MarkerListPanelProps {
  type: 'real' | 'synthetic';
  markers: Reading[];
  currentTs: number;
  onJumpToTime: (ts: number) => void;
  onClose: () => void;
}

export function MarkerListPanel({ type, markers, currentTs, onJumpToTime, onClose }: MarkerListPanelProps) {
  const isReal = type === 'real';
  const dotClass = isReal ? 'bg-sky-400' : 'bg-orange-400';
  const idClass = isReal ? 'text-sky-300' : 'text-orange-300';
  // The most recently dropped marker of this type — highlighted so it's easy to see where you are.
  const activeId = markers.filter((m) => m.ts <= currentTs).at(-1)?.marker_id;

  return (
    <aside className="flex w-80 shrink-0 flex-col border-l border-slate-800/80 bg-slate-900/30">
      <div className="flex shrink-0 items-center justify-between border-b border-slate-800/80 px-4 py-3">
        <h2 className="flex items-center gap-2 text-sm font-semibold text-slate-100">
          <span className={`h-2 w-2 rounded-full ${dotClass}`} />
          {isReal ? 'Real' : 'Synthetic'} markers
          <span className="font-normal text-slate-400">({markers.length})</span>
        </h2>
        <button
          onClick={onClose}
          aria-label="Close"
          title="Close"
          className="rounded-md p-1 text-slate-400 transition-colors hover:bg-slate-800 hover:text-slate-100"
        >
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.8} className="h-4 w-4">
            <path strokeLinecap="round" strokeLinejoin="round" d="M6 6l12 12M18 6L6 18" />
          </svg>
        </button>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto">
        <table className="w-full border-collapse text-left text-sm">
          <thead className="sticky top-0 bg-slate-900/95 text-xs text-slate-400">
            <tr>
              <th className="px-4 py-2 font-medium">marker</th>
              <th className="px-4 py-2 font-medium">time</th>
            </tr>
          </thead>
          <tbody>
            {markers.map((m) => (
              <tr
                key={m.marker_id}
                onClick={() => onJumpToTime(m.ts)}
                className={`cursor-pointer border-t border-slate-800/60 transition-colors hover:bg-slate-800/60 ${
                  m.marker_id === activeId ? 'bg-slate-800/80' : ''
                }`}
              >
                <td className={`px-4 py-1.5 font-mono ${idClass}`}>{m.marker_id}</td>
                <td className="px-4 py-1.5 font-mono text-slate-300">{formatTs(m.ts)}</td>
              </tr>
            ))}
            {markers.length === 0 && (
              <tr>
                <td colSpan={2} className="px-4 py-6 text-center text-slate-400">
                  No {type} markers in this dataset.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </aside>
  );
}
