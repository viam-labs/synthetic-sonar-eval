import { formatTs } from '../utils/formatTs';

interface TimeSliderProps {
  index: number;
  maxIndex: number;
  currentTs: number;
  playing: boolean;
  speed: number;
  shownCount: number;
  totalCount: number;
  shownRealCount: number;
  totalRealCount: number;
  onIndexChange: (index: number) => void;
  onTogglePlay: () => void;
  onSpeedChange: (speed: number) => void;
  onReset: () => void;
}

const SPEED_OPTIONS = [0.5, 1, 2, 4, 8];

export function TimeSlider({
  index,
  maxIndex,
  currentTs,
  playing,
  speed,
  shownCount,
  totalCount,
  shownRealCount,
  totalRealCount,
  onIndexChange,
  onTogglePlay,
  onSpeedChange,
  onReset,
}: TimeSliderProps) {
  return (
    <div className="time-slider">
      <div className="time-slider__controls">
        <button onClick={onTogglePlay} disabled={maxIndex === 0}>
          {playing ? 'Pause' : 'Play'}
        </button>
        <button onClick={onReset} disabled={maxIndex === 0}>
          Reset
        </button>
        <label className="time-slider__speed">
          Speed
          <select value={speed} onChange={(e) => onSpeedChange(Number(e.target.value))}>
            {SPEED_OPTIONS.map((s) => (
              <option key={s} value={s}>
                {s}x
              </option>
            ))}
          </select>
        </label>
        <span className="time-slider__count">
          {shownCount} / {totalCount} markers ({shownRealCount} / {totalRealCount} real)
        </span>
      </div>
      <div className="time-slider__row">
        <input
          type="range"
          min={0}
          max={maxIndex}
          value={index}
          onChange={(e) => onIndexChange(Number(e.target.value))}
          disabled={maxIndex === 0}
        />
        <span className="time-slider__ts">{maxIndex === 0 ? '—' : formatTs(currentTs)}</span>
      </div>
    </div>
  );
}
