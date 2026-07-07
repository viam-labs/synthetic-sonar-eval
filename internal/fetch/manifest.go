package fetch

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// ManifestEntry records one downloaded file with its capture timestamp, so
// callers can rebuild frame lists (e.g. markerplayback's ImageFrame/
// SonarFrame) from a cached download without re-querying the network.
type ManifestEntry struct {
	Type          string `json:"type"` // "tabular" or "binary"
	Path          string `json:"path"`
	TimeCaptured  string `json:"timeCaptured"`
	ResourceName  string `json:"resourceName,omitempty"`
	ComponentName string `json:"componentName,omitempty"`
}

// Manifest tracks every downloaded file for a single download directory,
// deduplicating entries by path and flushing to disk on every Add.
type Manifest struct {
	mu      sync.Mutex
	path    string
	entries []ManifestEntry
	seen    map[string]bool
}

// LoadManifest reads an existing manifest.json, or returns an empty one if
// the file doesn't exist yet.
func LoadManifest(path string) (*Manifest, error) {
	m := &Manifest{path: path, seen: map[string]bool{}}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &m.entries); err != nil {
		return nil, fmt.Errorf("corrupt manifest: %w", err)
	}
	for _, e := range m.entries {
		m.seen[e.Path] = true
	}
	return m, nil
}

// Add appends entries that haven't been recorded yet, then flushes to disk.
func (m *Manifest) Add(entries []ManifestEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range entries {
		if !m.seen[e.Path] {
			m.entries = append(m.entries, e)
			m.seen[e.Path] = true
		}
	}
	return atomicWriteJSON(m.path, m.entries)
}

// Entries returns a copy of all recorded manifest entries.
func (m *Manifest) Entries() []ManifestEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ManifestEntry, len(m.entries))
	copy(out, m.entries)
	return out
}

func atomicWriteJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
