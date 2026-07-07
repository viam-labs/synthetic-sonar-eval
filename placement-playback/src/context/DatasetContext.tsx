import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from 'react';
import type { ImageFrame, Reading, SonarFrame } from '../types';
import { parsePlaybackFile } from '../utils/parseReadings';

interface DatasetContextValue {
  readings: Reading[];
  images: ImageFrame[];
  sonarFrames: SonarFrame[];
  fileName: string | null;
  error: string | null;
  /** Whether the file-loader UI should be shown in place of the loaded data's sidebar. */
  isLoaderOpen: boolean;
  loadFromText: (text: string, name: string) => void;
  /** Reopens the file-loader UI without discarding the currently loaded dataset. */
  openLoader: () => void;
}

const DatasetContext = createContext<DatasetContextValue | null>(null);

export function DatasetProvider({ children }: { children: ReactNode }) {
  const [readings, setReadings] = useState<Reading[]>([]);
  const [images, setImages] = useState<ImageFrame[]>([]);
  const [sonarFrames, setSonarFrames] = useState<SonarFrame[]>([]);
  const [fileName, setFileName] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [isLoaderOpen, setIsLoaderOpen] = useState(true);

  const loadFromText = useCallback((text: string, name: string) => {
    const { readings, images, sonarFrames } = parsePlaybackFile(text);
    if (readings.length === 0) {
      setError('No valid readings found (need at least latitude and longitude fields).');
      return;
    }
    setError(null);
    setFileName(name);
    setReadings(readings);
    setImages(images);
    setSonarFrames(sonarFrames);
    setIsLoaderOpen(false);
  }, []);

  const openLoader = useCallback(() => {
    setIsLoaderOpen(true);
    setFileName(null);
  }, []);

  const value = useMemo(
    () => ({ readings, images, sonarFrames, fileName, error, isLoaderOpen, loadFromText, openLoader }),
    [readings, images, sonarFrames, fileName, error, isLoaderOpen, loadFromText, openLoader],
  );

  return <DatasetContext.Provider value={value}>{children}</DatasetContext.Provider>;
}

export function useDataset(): DatasetContextValue {
  const ctx = useContext(DatasetContext);
  if (!ctx) throw new Error('useDataset must be used within a DatasetProvider');
  return ctx;
}
