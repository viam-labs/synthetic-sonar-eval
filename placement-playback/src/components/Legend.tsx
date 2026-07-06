interface LegendProps {
  selected: 'real' | 'synthetic' | null;
  onSelect: (type: 'real' | 'synthetic') => void;
}

export function Legend({ selected, onSelect }: LegendProps) {
  return (
    <div className="flex items-center gap-2 text-sm font-medium text-slate-200">
      <button
        onClick={() => onSelect('real')}
        title="Show all real markers"
        className={`flex items-center gap-1.5 rounded-full border px-2.5 py-1 transition-colors ${
          selected === 'real'
            ? 'border-sky-400/60 bg-sky-400/15 text-sky-200'
            : 'border-slate-700 bg-slate-900/60 hover:border-slate-600 hover:bg-slate-800/60'
        }`}
      >
        <span className="h-2 w-2 rounded-full bg-sky-400" />
        real
      </button>
      <button
        onClick={() => onSelect('synthetic')}
        title="Show all synthetic markers"
        className={`flex items-center gap-1.5 rounded-full border px-2.5 py-1 transition-colors ${
          selected === 'synthetic'
            ? 'border-orange-400/60 bg-orange-400/15 text-orange-200'
            : 'border-slate-700 bg-slate-900/60 hover:border-slate-600 hover:bg-slate-800/60'
        }`}
      >
        <span className="h-2 w-2 rounded-full border border-dashed border-orange-300 bg-orange-400/70" />
        synthetic
      </button>
    </div>
  );
}
