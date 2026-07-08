// Package fetch holds the download logic shared by cmd/download (which owns
// all downloading, in either sequence-based or time-range-based mode) and
// cmd/markerplayback (which fetches marker readings itself, then calls into
// this package to ensure the underlying screen images / sonar readings for
// that time range are downloaded before rendering and predicting on them).
package fetch

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ViamEndpoint is the Viam app gRPC endpoint used by both download modes.
const ViamEndpoint = "app.viam.com:443"

// MaxQueryWindow caps how much history a single time-range download can
// pull, since the sonar/image resources here can hold vastly more data than
// is practical to fetch in one go (some sensors have 250k+ pings across just
// a few days).
const MaxQueryWindow = 3 * 24 * time.Hour

// hashLen is how many hex characters of the sha256 digest to use for the
// cache-key folder name — enough to avoid collisions in practice, short
// enough to stay human-scannable in a path.
const hashLen = 12

// Hash returns a short, stable hex digest of the given parameter strings,
// used as the cache-key folder name under <output>/<part-id>/<hash>/.
func Hash(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(h[:])[:hashLen]
}

// ResolveDir returns the cache directory for a given part ID + hash key
// under outputDir. Callers should check DirHasContent on the specific
// sub-directories they care about (e.g. images/, tabular/) rather than
// treating this directory's mere existence as "already downloaded" — it may
// have been created by a prior run that crashed partway through.
func ResolveDir(outputDir, partID, hash string) string {
	return filepath.Join(outputDir, SanitizeName(partID), hash)
}

// DirHasContent reports whether path exists and already contains at least
// one entry. A directory that exists but is empty (e.g. created up-front by
// os.MkdirAll before any file was written) does not count as downloaded.
func DirHasContent(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	return len(entries) > 0, nil
}

// SanitizeName replaces path-unsafe characters in a resource/component name.
func SanitizeName(name string) string {
	return strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_").Replace(name)
}

// SanitizeTimestamp turns an RFC3339(-ish) timestamp string into a
// filesystem-safe filename fragment.
func SanitizeTimestamp(ts string) string {
	return strings.NewReplacer(":", "-", ".", "-", " ", "_", "/", "-").Replace(ts)
}
