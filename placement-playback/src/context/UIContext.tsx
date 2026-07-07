import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from 'react';

type MarkerType = 'real' | 'synthetic';

interface UIContextValue {
  selectedMarkerType: MarkerType | null;
  toggleMarkerType: (type: MarkerType) => void;
  clearSelectedMarkerType: () => void;
}

const UIContext = createContext<UIContextValue | null>(null);

export function UIProvider({ children }: { children: ReactNode }) {
  const [selectedMarkerType, setSelectedMarkerType] = useState<MarkerType | null>(null);

  const toggleMarkerType = useCallback((type: MarkerType) => {
    setSelectedMarkerType((cur) => (cur === type ? null : type));
  }, []);

  const clearSelectedMarkerType = useCallback(() => setSelectedMarkerType(null), []);

  const value = useMemo(
    () => ({ selectedMarkerType, toggleMarkerType, clearSelectedMarkerType }),
    [selectedMarkerType, toggleMarkerType, clearSelectedMarkerType],
  );

  return <UIContext.Provider value={value}>{children}</UIContext.Provider>;
}

export function useUI(): UIContextValue {
  const ctx = useContext(UIContext);
  if (!ctx) throw new Error('useUI must be used within a UIProvider');
  return ctx;
}
