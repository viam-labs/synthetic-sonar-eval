import type { ImageFrame, Reading, SonarFrame, TimelineTrack } from '../types';
import { shortSensorLabel } from './frames';

/** Builds the gallery page's per-source timeline tracks (real/synthetic markers, screen, each sonar sensor). */
export function buildTimelineTracks(
  images: ImageFrame[],
  sensorNames: string[],
  sonarBySensor: Map<string, SonarFrame[]>,
  realMarkers: Reading[],
  syntheticMarkers: Reading[],
): TimelineTrack[] {
  const tracks: TimelineTrack[] = [
    { label: 'real', moments: realMarkers.map((r) => r.ts), dotClassName: 'bg-sky-400' },
    { label: 'synthetic', moments: syntheticMarkers.map((r) => r.ts), dotClassName: 'bg-orange-400' },
    { label: 'screen', moments: images.map((i) => i.ts), dotClassName: 'bg-emerald-400' },
  ];
  for (const name of sensorNames) {
    tracks.push({
      label: shortSensorLabel(name),
      moments: (sonarBySensor.get(name) ?? []).map((f) => f.ts),
      dotClassName: 'bg-violet-400',
    });
  }
  return tracks;
}
