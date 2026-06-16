package main

import (
	"strconv"
	"strings"
)

// fanSampleGrid is the wire format for sonar amplitude data. Only cells with
// amplitude strictly above the frame noise floor (MinAmp) are stored in Amps,
// keyed by "beam_sample" (e.g. "0_76").
type fanSampleGrid struct {
	PingNumber     uint32             `json:"ping_number"`
	Latitude       float64            `json:"latitude"`
	Longitude      float64            `json:"longitude"`
	HeadingDeg     float64            `json:"heading_deg"`
	PingRange      float64            `json:"ping_range"`
	PingTilt       float64            `json:"ping_tilt"`
	NBeams         int                `json:"n_beams"`
	NSamples       int                `json:"n_samples"`
	RangePerSample float64            `json:"range_per_sample"`
	CosTilt        float64            `json:"cos_tilt"`
	AZSorted       []float64          `json:"az_sorted"`
	MinAmp         float32            `json:"min_amp"`
	Amps           map[string]float32 `json:"amps"`
}

func parseAmpKey(key string) (beam, sample int, ok bool) {
	parts := strings.SplitN(key, "_", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	b, err1 := strconv.Atoi(parts[0])
	s, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return b, s, true
}
