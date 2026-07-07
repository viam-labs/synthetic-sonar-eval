import { NavLink, Outlet } from 'react-router-dom';
import { Legend } from '../components/Legend';
import { TimeRangeNav } from '../components/TimeRangeNav';
import { TimeSlider } from '../components/TimeSlider';
import { MapPage } from '../pages/MapPage';
import { useDataset } from '../context/DatasetContext';
import { usePlayback } from '../context/PlaybackContext';
import { useUI } from '../context/UIContext';

function NavTab({ to, children }: { to: string; children: string }) {
  return (
    <NavLink
      to={to}
      end
      className={({ isActive }) =>
        `rounded-full px-3 py-1 font-medium transition-colors ${
          isActive ? 'bg-sky-400/20 text-sky-200' : 'text-slate-400 hover:text-slate-200'
        }`
      }
    >
      {children}
    </NavLink>
  );
}

export function AppLayout() {
  const { isLoaderOpen } = useDataset();
  const {
    currentTs,
    rangeStart,
    rangeEnd,
    minTs,
    maxTs,
    playDirection,
    speed,
    filteredReadings,
    visible,
    realMarkers,
    syntheticMarkers,
    jumpToTime,
    updateRange,
    togglePlay,
    setSpeed,
    resetToRangeStart,
  } = usePlayback();
  const { selectedMarkerType, toggleMarkerType } = useUI();

  return (
    <div className="flex h-full flex-col bg-slate-950 text-slate-100">
      <header className="flex shrink-0 flex-col gap-3 border-b border-slate-800/80 bg-slate-900/40 px-6 py-3">
        <div className="flex items-center justify-between gap-4">
          <div className="flex items-baseline gap-3">
            <h1 className="flex items-center gap-2 text-base font-semibold tracking-tight text-white">
              <span className="inline-block h-2 w-2 rounded-full bg-sky-400 shadow-[0_0_8px_var(--color-sky-400)]" />
              placement&#8209;playback
            </h1>
            <span className="hidden text-sm text-slate-300 sm:inline">Marker &amp; camera playback</span>
          </div>
          <div className="flex items-center gap-4">
            {!isLoaderOpen && (
              <div className="flex items-center gap-1 rounded-full border border-slate-700 bg-slate-900/60 p-0.5 text-sm">
                <NavTab to="/">Map</NavTab>
                <NavTab to="/gallery">Gallery</NavTab>
              </div>
            )}
            <Legend selected={selectedMarkerType} onSelect={toggleMarkerType} />
          </div>
        </div>
        {!isLoaderOpen && (
          <TimeRangeNav minTs={minTs} maxTs={maxTs} rangeStart={rangeStart} rangeEnd={rangeEnd} onRangeChange={updateRange} />
        )}
      </header>

      {/* While the file loader is open, always show the map layout (with the loader in its
          sidebar) regardless of which route was last active — there's nothing to browse yet. */}
      {isLoaderOpen ? <MapPage /> : <Outlet />}

      <footer className="shrink-0 border-t border-slate-800/80 bg-slate-900/40 px-6 py-3">
        <TimeSlider
          currentTs={currentTs}
          minTs={rangeStart}
          maxTs={rangeEnd}
          playDirection={playDirection}
          speed={speed}
          shownCount={visible.length}
          totalCount={filteredReadings.length}
          totalRealCount={realMarkers.length}
          totalSyntheticCount={syntheticMarkers.length}
          onSeek={jumpToTime}
          onTogglePlay={togglePlay}
          onSpeedChange={setSpeed}
          onReset={resetToRangeStart}
        />
      </footer>
    </div>
  );
}
