import { useEffect, useRef } from 'react';
import { CircleMarker, MapContainer, Marker, Popup, TileLayer, Tooltip, useMap } from 'react-leaflet';
import { divIcon, latLngBounds } from 'leaflet';
import type { Reading } from '../types';
import { formatTs } from '../utils/formatTs';

// A simple boat silhouette (mast + sail + hull) marking the current position in the journey —
// the location of the most recently placed marker as of the scrubber's current time.
const BOAT_ICON = divIcon({
  className: '',
  html: `<div class="boat-marker">
    <svg viewBox="0 0 24 24" width="16" height="16" fill="white">
      <rect x="11.25" y="2" width="1.5" height="12" />
      <path d="M12 2 L18 13 L12 13 Z" />
      <path d="M3 15 L21 15 L18 20.5 L6 20.5 Z" />
    </svg>
  </div>`,
  iconSize: [30, 30],
  iconAnchor: [15, 15],
});

interface MapViewProps {
  allReadings: Reading[];
  visible: Reading[];
  justDropped: Reading[];
  onJumpToTime: (ts: number) => void;
}

/** Fits the map to the dataset's extent once per newly loaded dataset (not on every playback tick). */
function FitBounds({ points }: { points: Reading[] }) {
  const map = useMap();
  const fittedFor = useRef<Reading[] | null>(null);

  useEffect(() => {
    if (points.length === 0 || fittedFor.current === points) return;
    fittedFor.current = points;
    const bounds = latLngBounds(points.map((r) => [r.latitude, r.longitude]));
    map.fitBounds(bounds, { padding: [30, 30] });
  }, [points, map]);

  return null;
}

const REAL_COLOR = '#38bdf8';
const SYNTHETIC_COLOR = '#fb923c';

function colorFor(r: Reading) {
  return r.is_synthetic ? SYNTHETIC_COLOR : REAL_COLOR;
}

function ReadingPopup({ r, onJumpToTime }: { r: Reading; onJumpToTime: (ts: number) => void }) {
  const map = useMap();

  return (
    <Popup>
      <div className="min-w-44">
        <div className="mb-1.5 flex items-center justify-between gap-2">
          <span className="font-mono text-sm font-semibold text-white">{r.marker_id}</span>
          <span
            className={`rounded-full px-2 py-0.5 text-xs font-semibold ${
              r.is_synthetic ? 'bg-orange-400/20 text-orange-200' : 'bg-sky-400/20 text-sky-200'
            }`}
          >
            {r.is_synthetic ? 'synthetic' : 'real'}
          </span>
        </div>
        <dl className="space-y-0.5 text-sm text-slate-300">
          <div className="flex justify-between gap-3">
            <dt>depth</dt>
            <dd className="font-mono text-slate-100">{r.depth.toFixed(2)}</dd>
          </div>
          <div className="flex justify-between gap-3">
            <dt>ts</dt>
            <dd className="font-mono text-slate-100">{formatTs(r.ts)}</dd>
          </div>
          <div className="flex justify-between gap-3">
            <dt>lat/lon</dt>
            <dd className="font-mono text-slate-100">
              {r.latitude.toFixed(4)}, {r.longitude.toFixed(4)}
            </dd>
          </div>
        </dl>
        <button
          onClick={() => {
            onJumpToTime(r.ts);
            map.closePopup();
          }}
          className="mt-2.5 w-full rounded-md border border-sky-400/30 bg-sky-400/10 px-2 py-1 text-sm font-medium text-sky-200 transition-colors hover:bg-sky-400/20"
        >
          Jump to this time
        </button>
      </div>
    </Popup>
  );
}

export function MapView({ allReadings, visible, justDropped, onJumpToTime }: MapViewProps) {
  const justDroppedIds = new Set(justDropped.map((r) => r.marker_id));
  // visible is sorted ascending by ts, so the last entry is wherever the journey currently stands.
  const boatPosition = visible.at(-1);

  return (
    <MapContainer
      center={[20, 0]}
      zoom={2}
      minZoom={1.5}
      worldCopyJump
      preferCanvas
      style={{ width: '100%', height: '100%' }}
    >
      <TileLayer
        attribution='&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors'
        url="https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png"
      />
      <FitBounds points={allReadings} />
      {visible
        .filter((r) => !justDroppedIds.has(r.marker_id))
        // Real readings are typically rare relative to synthetic ones and easy to bury
        // under overlapping synthetic dots, so draw them last (on top) and larger.
        .sort((a, b) => Number(b.is_synthetic) - Number(a.is_synthetic))
        .map((r) => (
          <CircleMarker
            key={r.marker_id}
            center={[r.latitude, r.longitude]}
            radius={r.is_synthetic ? 5 : 7}
            pathOptions={{
              color: colorFor(r),
              fillColor: colorFor(r),
              fillOpacity: r.is_synthetic ? 0.4 : 0.85,
              weight: r.is_synthetic ? 2 : 2,
              dashArray: r.is_synthetic ? '3 2' : undefined,
            }}
          >
            <ReadingPopup r={r} onJumpToTime={onJumpToTime} />
          </CircleMarker>
        ))}
      {justDropped.map((r) => (
        <Marker
          key={r.marker_id}
          position={[r.latitude, r.longitude]}
          icon={divIcon({
            className: '',
            html: `<div class="drop-marker ${r.is_synthetic ? 'drop-marker--synthetic' : 'drop-marker--real'}"></div>`,
            iconSize: [18, 18],
            iconAnchor: [9, 9],
          })}
        >
          <ReadingPopup r={r} onJumpToTime={onJumpToTime} />
        </Marker>
      ))}
      {boatPosition && (
        <Marker
          position={[boatPosition.latitude, boatPosition.longitude]}
          icon={BOAT_ICON}
          zIndexOffset={1000}
        >
          <Tooltip direction="top" offset={[0, -16]}>
            Current position · {boatPosition.marker_id}
          </Tooltip>
        </Marker>
      )}
    </MapContainer>
  );
}
