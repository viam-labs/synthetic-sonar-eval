export interface Reading {
  depth: number;
  is_synthetic: boolean;
  latitude: number;
  longitude: number;
  marker_id: string;
  ts: number;
}

export interface ImageFrame {
  ts: number;
  mimeType: string;
  dataBase64: string;
}

export interface SonarFrame {
  sensorName: string;
  ts: number;
  mimeType: string;
  dataBase64: string;
}

export interface TimelineTrack {
  label: string;
  moments: number[]; // this source's frame timestamps, ascending
  dotClassName: string;
}
