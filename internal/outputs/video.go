package outputs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Video outputs: an .mp4 or .webm file on the shelf plays in the reader and
// on the Station (internal/viewer shelf.go, shelf_read.go). Output add and
// revise copy it under MaxVideoBytes and, when ffmpeg is on PATH, write a
// poster next to it. The viewer never runs ffmpeg: it only serves files.

// KindVideo is the kind of a video output.
const KindVideo = "video"

// MaxVideoBytes is the largest video (.mp4, .webm) output add copies onto
// the shelf; every other file keeps MaxFileBytes.
const MaxVideoBytes = 200 << 20

// PosterSuffix is appended to a video's shelf file for its poster.
const PosterSuffix = ".poster.jpg"

// posterTimeout bounds one ffmpeg run: a broken file must not hang add.
const posterTimeout = 30 * time.Second

// ErrNoFfmpeg is MakePoster's error when ffmpeg is not on PATH.
var ErrNoFfmpeg = errors.New("ffmpeg is not on PATH")

// videoTypes are the video files the shelf plays, by extension.
var videoTypes = map[string]string{".mp4": "video/mp4", ".webm": "video/webm"}

// VideoType is the media type of a video file the shelf plays ("" for any
// other file). Go's own mime table has neither type.
func VideoType(name string) string {
	return videoTypes[strings.ToLower(filepath.Ext(name))]
}

// IsVideo reports whether the output's file or path is a video the shelf
// plays (.mp4, .webm), whatever its kind.
func (o *Output) IsVideo() bool {
	name := o.File
	if name == "" {
		name = o.Path
	}
	return VideoType(name) != ""
}

// limitFor is the shelf's size limit for a file and its words in a refusal.
func limitFor(name string) (int64, string) {
	if VideoType(name) != "" {
		return MaxVideoBytes, fmt.Sprintf("%d MB for a video", MaxVideoBytes>>20)
	}
	return MaxFileBytes, fmt.Sprintf("%d MB", MaxFileBytes>>20)
}

// checkVideoInput refuses a video output the shelf could not play.
func checkVideoInput(in AddInput) error {
	if in.Kind != KindVideo {
		return nil
	}
	switch {
	case strings.TrimSpace(in.Path) != "":
		return fmt.Errorf("--kind video plays from the shelf only: copy it there with --file %s (a --path file is not served)", in.Path)
	case strings.TrimSpace(in.File) != "" && VideoType(in.File) == "":
		ext := strings.ToLower(filepath.Ext(in.File))
		if ext == "" {
			ext = "a file with no extension"
		}
		base := strings.TrimSuffix(filepath.Base(in.File), filepath.Ext(in.File))
		return fmt.Errorf("--kind video plays .mp4 or .webm on the shelf, got %s: convert it (ffmpeg -i %s %s.mp4) or use another --kind", ext, filepath.Base(in.File), base)
	}
	return nil
}

// MakePoster writes dst, a 640 px wide JPEG of video's frame at 1 s (the
// first frame for a shorter video), with ffmpeg from PATH. ErrNoFfmpeg when
// it isn't there; any other failure names ffmpeg's first error line.
func MakePoster(video, dst string) error {
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		return ErrNoFfmpeg
	}
	run := func(seek bool) error {
		ctx, cancel := context.WithTimeout(context.Background(), posterTimeout)
		defer cancel()
		args := []string{"-v", "error", "-nostdin", "-y"}
		if seek {
			args = append(args, "-ss", "1")
		}
		args = append(args, "-i", video, "-frames:v", "1", "-vf", "scale=640:-2:out_range=full", "-q:v", "4", dst)
		out, err := exec.CommandContext(ctx, bin, args...).CombinedOutput()
		if ctx.Err() != nil {
			return fmt.Errorf("ffmpeg took over %s", posterTimeout)
		}
		if err != nil {
			line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0])
			if line == "" {
				line = err.Error()
			}
			return fmt.Errorf("ffmpeg failed: %s", line)
		}
		return nil
	}
	written := func() bool {
		st, err := os.Stat(dst)
		return err == nil && st.Size() > 0
	}
	if err := run(true); err == nil && written() {
		return nil
	}
	// Shorter than 1 s: the seek found no frame; take the first one.
	if err := run(false); err != nil {
		os.Remove(dst)
		return err
	}
	if !written() {
		os.Remove(dst)
		return errors.New("ffmpeg wrote no frame")
	}
	return nil
}

// posterFor makes the poster of a video shelf file rel (relative to
// FilesDir) and returns its path relative to FilesDir; "" and the reason
// when there is none. Not a video: "" and nil.
func posterFor(p *tracker.Project, rel string) (string, error) {
	if VideoType(rel) == "" {
		return "", nil
	}
	poster := rel + PosterSuffix
	dir := FilesDir(p)
	if err := MakePoster(filepath.Join(dir, filepath.FromSlash(rel)), filepath.Join(dir, filepath.FromSlash(poster))); err != nil {
		return "", err
	}
	return poster, nil
}
