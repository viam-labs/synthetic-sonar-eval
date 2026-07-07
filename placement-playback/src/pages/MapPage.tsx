import { useState } from 'react';
import { MapView } from '../components/MapView';
import { FileLoader } from '../components/FileLoader';
import { ImagePanel } from '../components/ImagePanel';
import { SonarPanel } from '../components/SonarPanel';
import { MarkerListPanel } from '../components/MarkerListPanel';
import { useDataset } from '../context/DatasetContext';
import { usePlayback } from '../context/PlaybackContext';
import { useUI } from '../context/UIContext';

export function MapPage() {
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
  const { readings, fileName, error, isLoaderOpen, loadFromText, openLoader } = useDataset();
  const {
    currentTs,
    visible,
    justDropped,
    filteredImages,
    filteredSonarFrames,
    realMarkers,
    syntheticMarkers,
    jumpToTime,
  } = usePlayback();
  const { selectedMarkerType, clearSelectedMarkerType } = useUI();

  return (
    <div className="flex min-h-0 flex-1">
      {sidebarCollapsed && !isLoaderOpen ? (
        <div className="flex w-6 shrink-0 justify-center border-r border-slate-800/80 bg-slate-900/20 pt-3">
          <button
            onClick={() => setSidebarCollapsed(false)}
            aria-label="Expand side panel"
            title="Expand side panel"
            className="flex h-8 w-6 items-center justify-center rounded-full text-slate-400 transition-colors hover:bg-slate-800 hover:text-white"
          >
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.8} className="h-3.5 w-3.5">
              <path strokeLinecap="round" strokeLinejoin="round" d="M9 6l6 6-6 6" />
            </svg>
          </button>
        </div>
      ) : (
        <div className="flex w-104 shrink-0 flex-col border-r border-slate-800/80 bg-slate-900/20">
          {!isLoaderOpen && (
            <div className="flex shrink-0 items-center border-b border-slate-800/80 px-2 py-1.5">
              <button
                onClick={() => setSidebarCollapsed(true)}
                aria-label="Collapse side panel"
                title="Collapse side panel"
                className="flex h-6 w-6 items-center justify-center rounded-md text-slate-400 transition-colors hover:bg-slate-800 hover:text-white"
              >
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.8} className="h-3.5 w-3.5">
                  <path strokeLinecap="round" strokeLinejoin="round" d="M15 6l-6 6 6 6" />
                </svg>
              </button>
            </div>
          )}
          <aside className="min-h-0 flex-1 overflow-y-auto p-4">
            {isLoaderOpen ? (
              <FileLoader onLoad={loadFromText} fileName={fileName} error={error} />
            ) : (
              <div className="flex flex-col gap-3">
                <div className="flex items-center justify-between gap-2">
                  <p className="truncate text-xs text-slate-400" title={fileName ?? undefined}>
                    {fileName}
                  </p>
                  <button
                    onClick={openLoader}
                    className="shrink-0 text-xs font-medium text-sky-300 hover:text-sky-200 hover:underline"
                  >
                    Change file
                  </button>
                </div>
                <ImagePanel images={filteredImages} currentTs={currentTs} onJumpToTime={jumpToTime} />
                <SonarPanel sonarFrames={filteredSonarFrames} currentTs={currentTs} onJumpToTime={jumpToTime} />
              </div>
            )}
          </aside>
        </div>
      )}

      <main className="relative min-w-0 flex-1">
        <MapView allReadings={readings} visible={visible} justDropped={justDropped} onJumpToTime={jumpToTime} />
      </main>

      {selectedMarkerType && (
        <MarkerListPanel
          type={selectedMarkerType}
          markers={selectedMarkerType === 'real' ? realMarkers : syntheticMarkers}
          currentTs={currentTs}
          onJumpToTime={jumpToTime}
          onClose={clearSelectedMarkerType}
        />
      )}
    </div>
  );
}
