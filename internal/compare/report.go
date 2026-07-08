package compare

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"strconv"

	"synthetic-sonar-eval/internal/detector"
)

// FanResult is one fan's contribution to a Group: the nearest render's
// detections, or Present=false if that fan has no renders anywhere in the
// dataset.
type FanResult struct {
	Present      bool                 `json:"present"`
	Path         string               `json:"path,omitempty"`
	Timestamp    string               `json:"timestamp,omitempty"`
	DeltaSeconds float64              `json:"delta_seconds,omitempty"`
	FishCount    int                  `json:"fish_count,omitempty"`
	Detections   []detector.Detection `json:"detections,omitempty"`
}

// FrameResult is a single frame's detections, as recorded in a Group.
type FrameResult struct {
	Path       string               `json:"path"`
	Timestamp  string               `json:"timestamp"`
	FishCount  int                  `json:"fish_count"`
	Detections []detector.Detection `json:"detections"`
}

// Group is one screen1 frame plus its nearest-in-time render from each fan.
type Group struct {
	GroupIndex int                  `json:"group_index"`
	Screen1    FrameResult          `json:"screen1"`
	Fans       map[string]FanResult `json:"fans"`
	FanSumFish int                  `json:"fan_sum_fish"`
}

// Summary is the run-level metadata written alongside Groups in counts.json.
type Summary struct {
	ModelDir            string         `json:"model_dir"`
	Confidence          float64        `json:"confidence"`
	FishClass           string         `json:"fish_class"`
	ScreenshotStripDist *float64       `json:"screenshot_strip_dist"`
	RenderStripDist     *float64       `json:"render_strip_dist"`
	NGroups             int            `json:"n_groups"`
	TotalScreen1Fish    int            `json:"total_screen1_fish"`
	TotalFanSumFish     int            `json:"total_fan_sum_fish"`
	RendersPerFan       map[string]int `json:"renders_per_fan"`
}

// CountsReport is the full contents of counts.json.
type CountsReport struct {
	Summary Summary `json:"summary"`
	Groups  []Group `json:"groups"`
}

// WriteCountsJSON writes report to path as indented JSON.
func WriteCountsJSON(path string, report CountsReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// WriteCountsCSV writes a per-group summary CSV to path.
func WriteCountsCSV(path string, groups []Group) error {
	if len(groups) == 0 {
		return nil
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	header := []string{"group_index", "screen1_timestamp", "screen1_fish"}
	for _, fan := range FanResources {
		header = append(header, fan+"_fish")
	}
	for _, fan := range FanResources {
		header = append(header, fan+"_delta_s")
	}
	header = append(header, "fan_sum_fish")
	if err := w.Write(header); err != nil {
		return err
	}

	for _, g := range groups {
		row := []string{
			strconv.Itoa(g.GroupIndex),
			g.Screen1.Timestamp,
			strconv.Itoa(g.Screen1.FishCount),
		}
		for _, fan := range FanResources {
			if fr := g.Fans[fan]; fr.Present {
				row = append(row, strconv.Itoa(fr.FishCount))
			} else {
				row = append(row, "")
			}
		}
		for _, fan := range FanResources {
			if fr := g.Fans[fan]; fr.Present {
				row = append(row, strconv.FormatFloat(fr.DeltaSeconds, 'f', 3, 64))
			} else {
				row = append(row, "")
			}
		}
		row = append(row, strconv.Itoa(g.FanSumFish))
		if err := w.Write(row); err != nil {
			return err
		}
	}
	return w.Error()
}
