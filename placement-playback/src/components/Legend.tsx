export function Legend() {
  return (
    <div className="legend">
      <div className="legend__item">
        <span className="legend__swatch legend__swatch--real" />
        real
      </div>
      <div className="legend__item">
        <span className="legend__swatch legend__swatch--synthetic" />
        synthetic
      </div>
    </div>
  );
}
