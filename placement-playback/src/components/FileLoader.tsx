import { useRef, useState } from 'react';
import type { DragEvent } from 'react';

interface FileLoaderProps {
  onLoad: (text: string, name: string) => void;
  fileName: string | null;
  error: string | null;
}

export function FileLoader({ onLoad, fileName, error }: FileLoaderProps) {
  const inputRef = useRef<HTMLInputElement>(null);
  const [dragOver, setDragOver] = useState(false);

  async function handleFiles(files: FileList | null) {
    const file = files?.[0];
    if (!file) return;
    const text = await file.text();
    onLoad(text, file.name);
  }

  function handleDrop(e: DragEvent<HTMLDivElement>) {
    e.preventDefault();
    setDragOver(false);
    handleFiles(e.dataTransfer.files);
  }

  return (
    <div>
      <div
        className={`group flex cursor-pointer flex-col items-center gap-3 rounded-xl border border-dashed px-4 py-8 text-center transition-colors ${
          dragOver
            ? 'border-sky-400 bg-sky-400/10'
            : 'border-slate-700 bg-slate-900/40 hover:border-slate-500 hover:bg-slate-900/60'
        }`}
        onClick={() => inputRef.current?.click()}
        onDragOver={(e) => {
          e.preventDefault();
          setDragOver(true);
        }}
        onDragLeave={() => setDragOver(false)}
        onDrop={handleDrop}
      >
        <input
          ref={inputRef}
          type="file"
          accept=".json,.ndjson,.jsonl,.txt"
          hidden
          onChange={(e) => handleFiles(e.target.files)}
        />
        <svg
          className={`h-6 w-6 shrink-0 ${dragOver ? 'text-sky-400' : 'text-slate-400 group-hover:text-slate-300'}`}
          fill="none"
          viewBox="0 0 24 24"
          strokeWidth={1.5}
          stroke="currentColor"
        >
          <path
            strokeLinecap="round"
            strokeLinejoin="round"
            d="M3 16.5v2.25A2.25 2.25 0 0 0 5.25 21h13.5A2.25 2.25 0 0 0 21 18.75V16.5M16.5 12 12 16.5m0 0L7.5 12m4.5 4.5V3"
          />
        </svg>
        {fileName ? (
          <p className="text-sm font-medium text-white wrap-break-word">Loaded {fileName}</p>
        ) : (
          <p className="text-sm text-slate-300">
            Drop a <span className="font-mono text-slate-100">readings.json</span> file, or click to browse
            <br />
            <span className="text-xs text-slate-400">e.g. from `make markers`</span>
          </p>
        )}
      </div>
      {error && (
        <div className="mt-3 rounded-lg border border-red-900/60 bg-red-950/40 px-3 py-2 text-sm text-red-300">
          {error}
        </div>
      )}
    </div>
  );
}
