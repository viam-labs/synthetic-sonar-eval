import { HashRouter, Route, Routes } from 'react-router-dom';
import { AppLayout } from './app/AppLayout';
import { MapPage } from './pages/MapPage';
import { GalleryPage } from './pages/GalleryPage';
import { DatasetProvider } from './context/DatasetContext';
import { PlaybackProvider } from './context/PlaybackContext';
import { UIProvider } from './context/UIContext';

export default function App() {
  return (
    <HashRouter>
      <DatasetProvider>
        <PlaybackProvider>
          <UIProvider>
            <Routes>
              <Route element={<AppLayout />}>
                <Route path="/" element={<MapPage />} />
                <Route path="/gallery" element={<GalleryPage />} />
              </Route>
            </Routes>
          </UIProvider>
        </PlaybackProvider>
      </DatasetProvider>
    </HashRouter>
  );
}
