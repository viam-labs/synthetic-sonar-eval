import { useMemo } from 'react';
import { formatTs } from '../utils/formatTs';
import { groupBySensor, lastFrameAtOrBefore, sensorNamesOf, shortSensorLabel } from '../utils/frames';
import { buildTimelineTracks } from '../utils/timeline';
import { MultiTrackTimeline } from '../components/MultiTrackTimeline';
import { FrameGalleryCard } from '../components/FrameGalleryCard';
import { usePlayback } from '../context/PlaybackContext';

export function GalleryPage() {
  const {
    filteredImages: images,
    filteredSonarFrames: sonarFrames,
    realMarkers,
    syntheticMarkers,
    rangeStart,
    rangeEnd,
    currentTs,
    jumpToTime,
  } = usePlayback();

  const sensorNames = useMemo(() => sensorNamesOf(sonarFrames), [sonarFrames]);
  const sonarBySensor = useMemo(() => groupBySensor(sonarFrames, sensorNames), [sonarFrames, sensorNames]);

  const tracks = useMemo(
    () => buildTimelineTracks(images, sensorNames, sonarBySensor, realMarkers, syntheticMarkers),
    [images, sensorNames, sonarBySensor, realMarkers, syntheticMarkers],
  );

  const latestImage = lastFrameAtOrBefore(images, currentTs);

  return (
    <div className="flex flex-1 flex-col gap-8 overflow-y-auto p-6">
      <section>
        <div className="mb-3 flex items-baseline justify-between gap-3">
          <div className="flex items-baseline gap-3">
            <h2 className="text-sm font-semibold text-slate-200">Timeline</h2>
            <span className="text-xs text-slate-500">Click or drag to seek</span>
          </div>
          <span className="font-mono text-xs text-slate-400">
            {rangeStart < rangeEnd ? formatTs(currentTs) : '—'}
          </span>
        </div>
        {tracks.some((t) => t.moments.length > 0) ? (
          <MultiTrackTimeline
            tracks={tracks}
            minTs={rangeStart}
            maxTs={rangeEnd}
            currentTs={currentTs}
            onSeek={jumpToTime}
          />
        ) : (
          <p className="text-sm text-slate-500">No marker, image, or sonar data in the selected range.</p>
        )}
      </section>

      <section>
        <h2 className="mb-3 text-sm font-semibold text-slate-200">Latest frame per source</h2>
        <div className="flex flex-wrap gap-3">
          <FrameGalleryCard label="screen" dotClassName="bg-emerald-400" frame={latestImage} currentTs={currentTs} />
          {sensorNames.map((name) => (
            <FrameGalleryCard
              key={name}
              label={shortSensorLabel(name)}
              dotClassName="bg-violet-400"
              frame={lastFrameAtOrBefore(sonarBySensor.get(name) ?? [], currentTs)}
              currentTs={currentTs}
            />
          ))}
        </div>
      </section>
    </div>
  );
}
