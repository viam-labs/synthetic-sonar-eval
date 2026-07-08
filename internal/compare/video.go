package compare

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// MakeMontageVideo encodes the sequence of group_%04d.png montage images
// under montageDir into an MP4 at outPath via ffmpeg. Every montage in a run
// must share identical pixel dimensions (see MakeMontage's colWidths) —
// libx264 requires a constant frame size across the whole encode.
func MakeMontageVideo(montageDir, outPath string, fps int) error {
	if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
		return err
	}
	cmd := exec.Command("ffmpeg", "-y",
		"-framerate", fmt.Sprintf("%d", fps),
		"-i", filepath.Join(montageDir, "group_%04d.png"),
		"-vf", "scale=trunc(iw/2)*2:trunc(ih/2)*2",
		"-c:v", "libx264",
		"-pix_fmt", "yuv420p",
		"-crf", "18",
		outPath,
	)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
