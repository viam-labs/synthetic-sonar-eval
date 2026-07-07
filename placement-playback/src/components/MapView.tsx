import { useEffect, useRef, useState } from 'react';
import { CircleMarker, MapContainer, Marker, Polyline, Popup, TileLayer, Tooltip, useMap } from 'react-leaflet';
import { divIcon, latLngBounds } from 'leaflet';
import type { Reading } from '../types';
import { formatTs } from '../utils/formatTs';
import { bearingDegrees, compassPoint, formatDistance, formatDuration, haversineDistanceMeters } from '../utils/geo';

// A glowing ring around the most recently placed marker (as of the scrubber's current time).
// Purely decorative — pointer-events are disabled so it never blocks clicking the marker itself.
const CURRENT_POSITION_RING_ICON = divIcon({
  className: '',
  html: `<div class="current-position-ring"></div>`,
  iconSize: [26, 26],
  iconAnchor: [13, 13],
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

interface ReadingPopupProps {
  r: Reading;
  onJumpToTime: (ts: number) => void;
  pendingAnchor: Reading | null;
  onStartMeasure: (r: Reading) => void;
  onFinishMeasure: (r: Reading) => void;
  onCancelMeasure: () => void;
}

function ReadingPopup({ r, onJumpToTime, pendingAnchor, onStartMeasure, onFinishMeasure, onCancelMeasure }: ReadingPopupProps) {
  const map = useMap();
  const isAnchor = pendingAnchor?.marker_id === r.marker_id;

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
        {isAnchor ? (
          <button
            onClick={() => {
              onCancelMeasure();
              map.closePopup();
            }}
            className="mt-1.5 w-full rounded-md border border-slate-600 bg-slate-800/60 px-2 py-1 text-sm font-medium text-slate-300 transition-colors hover:bg-slate-800"
          >
            Cancel measuring
          </button>
        ) : (
          <button
            onClick={() => {
              if (pendingAnchor) onFinishMeasure(r);
              else onStartMeasure(r);
              map.closePopup();
            }}
            className="mt-1.5 w-full rounded-md border border-violet-400/30 bg-violet-400/10 px-2 py-1 text-sm font-medium text-violet-200 transition-colors hover:bg-violet-400/20"
          >
            {pendingAnchor ? 'Measure to here' : 'Measure from here'}
          </button>
        )}
      </div>
    </Popup>
  );
}

export function MapView({ allReadings, visible, justDropped, onJumpToTime }: MapViewProps) {
  const justDroppedIds = new Set(justDropped.map((r) => r.marker_id));
  // visible is sorted ascending by ts, so the last entry is wherever the journey currently stands.
  const currentPosition = visible.at(-1);

  const [pendingAnchor, setPendingAnchor] = useState<Reading | null>(null);
  const [measurement, setMeasurement] = useState<{ a: Reading; b: Reading } | null>(null);

  function startMeasure(r: Reading) {
    setMeasurement(null);
    setPendingAnchor(r);
  }
  function finishMeasure(r: Reading) {
    if (!pendingAnchor) return;
    setMeasurement({ a: pendingAnchor, b: r });
    setPendingAnchor(null);
  }
  function cancelMeasure() {
    setPendingAnchor(null);
  }
  function clearMeasurement() {
    setMeasurement(null);
  }

  const popupProps = {
    onJumpToTime,
    pendingAnchor,
    onStartMeasure: startMeasure,
    onFinishMeasure: finishMeasure,
    onCancelMeasure: cancelMeasure,
  };

  const distance = measurement ? haversineDistanceMeters(measurement.a, measurement.b) : 0;
  const bearing = measurement ? bearingDegrees(measurement.a, measurement.b) : 0;

  return (
    <div className="relative h-full w-full">
      {pendingAnchor && (
        <div className="absolute top-3 left-1/2 z-[1000] flex -translate-x-1/2 items-center gap-3 rounded-full border border-violet-400/30 bg-slate-900/95 px-4 py-1.5 text-sm text-violet-100 shadow-2xl">
          <span>
            Measuring from <span className="font-mono font-semibold">{pendingAnchor.marker_id}</span> — click another marker
          </span>
          <button
            onClick={cancelMeasure}
            className="rounded-full border border-slate-600 px-2 py-0.5 text-xs font-medium text-slate-300 transition-colors hover:bg-slate-800"
          >
            Cancel
          </button>
        </div>
      )}
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
                color: pendingAnchor?.marker_id === r.marker_id ? '#a78bfa' : colorFor(r),
                fillColor: colorFor(r),
                fillOpacity: r.is_synthetic ? 0.4 : 0.85,
                weight: pendingAnchor?.marker_id === r.marker_id ? 3 : 2,
                dashArray: r.is_synthetic ? '3 2' : undefined,
              }}
            >
              <ReadingPopup r={r} {...popupProps} />
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
            <ReadingPopup r={r} {...popupProps} />
          </Marker>
        ))}
        {currentPosition && (
          <Marker
            position={[currentPosition.latitude, currentPosition.longitude]}
            icon={CURRENT_POSITION_RING_ICON}
            interactive={false}
            zIndexOffset={1000}
          />
        )}
        {measurement && (
          <Polyline
            positions={[
              [measurement.a.latitude, measurement.a.longitude],
              [measurement.b.latitude, measurement.b.longitude],
            ]}
            pathOptions={{ color: '#a78bfa', weight: 3, dashArray: '6 5' }}
          >
            <Tooltip permanent direction="center" className="measure-tooltip" opacity={1}>
              {formatDistance(distance)}
            </Tooltip>
            <Popup>
              <div className="min-w-52">
                <div className="mb-1.5 font-mono text-sm font-semibold text-white">Measurement</div>
                <dl className="space-y-0.5 text-sm text-slate-300">
                  <div className="flex justify-between gap-3">
                    <dt>from</dt>
                    <dd className="font-mono text-slate-100">{measurement.a.marker_id}</dd>
                  </div>
                  <div className="flex justify-between gap-3">
                    <dt>to</dt>
                    <dd className="font-mono text-slate-100">{measurement.b.marker_id}</dd>
                  </div>
                  <div className="flex justify-between gap-3">
                    <dt>distance</dt>
                    <dd className="font-mono text-slate-100">{formatDistance(distance)}</dd>
                  </div>
                  <div className="flex justify-between gap-3">
                    <dt>bearing</dt>
                    <dd className="font-mono text-slate-100">
                      {bearing.toFixed(0)}° {compassPoint(bearing)}
                    </dd>
                  </div>
                  <div className="flex justify-between gap-3">
                    <dt>time gap</dt>
                    <dd className="font-mono text-slate-100">{formatDuration(Math.abs(measurement.b.ts - measurement.a.ts))}</dd>
                  </div>
                  <div className="flex justify-between gap-3">
                    <dt>depth Δ</dt>
                    <dd className="font-mono text-slate-100">{Math.abs(measurement.b.depth - measurement.a.depth).toFixed(2)}</dd>
                  </div>
                </dl>
                <button
                  onClick={clearMeasurement}
                  className="mt-2.5 w-full rounded-md border border-slate-600 bg-slate-800/60 px-2 py-1 text-sm font-medium text-slate-300 transition-colors hover:bg-slate-800"
                >
                  Clear measurement
                </button>
              </div>
            </Popup>
          </Polyline>
        )}
      </MapContainer>
    </div>
  );
}
