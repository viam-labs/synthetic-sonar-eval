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
    <div
      className={`file-loader ${dragOver ? 'file-loader--drag' : ''}`}
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
      <div className="file-loader__label">
        {fileName
          ? `Loaded: ${fileName}`
          : 'Click or drop a readings.json file (e.g. from make markers)'}
      </div>
      {error && <div className="file-loader__error">{error}</div>}
    </div>
  );
}
