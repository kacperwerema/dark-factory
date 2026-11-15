package outputs

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeFfmpeg puts a stand-in ffmpeg first on PATH: it writes "jpg" to its
// last argument (the poster) and logs its arguments to log.
func fakeFfmpeg(t *testing.T) (log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "args.log")
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\nfor a; do last=$a; done\nprintf jpg > \"$last\"\n"
	if err := os.WriteFile(filepath.Join(dir, "ffmpeg"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+"/bin:/usr/bin")
	return log
}

// noFfmpeg leaves only the system folders on PATH (no Homebrew ffmpeg).
func noFfmpeg(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
}

func sparse(t *testing.T, name string, size int64) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return path
}

func TestAddVideoMakesAPoster(t *testing.T) {
	p := project(t)
	log := fakeFfmpeg(t)
	src := filepath.Join(t.TempDir(), "final.mp4")
	writeFile(t, src, "mp4")
	o, err := Add(p, AddInput{Department: "product", Title: "Draft", Kind: "Video", File: src}, now)
	if err != nil {
		t.Fatal(err)
	}
	if o.Kind != KindVideo || o.Icon() != "🎬" || !o.IsVideo() || o.IsImage() || o.PosterErr != nil {
		t.Errorf("video = %+v", o)
	}
	if want := "product/" + o.ID + "-final.mp4" + PosterSuffix; o.Poster != want || o.Versions[0].Poster != want {
		t.Errorf("poster = %q (v1 %q), want %q", o.Poster, o.Versions[0].Poster, want)
	}
	if b, err := os.ReadFile(filepath.Join(FilesDir(p), filepath.FromSlash(o.Poster))); err != nil || string(b) != "jpg" {
		t.Errorf("poster file: %q %v", b, err)
	}
	args, _ := os.ReadFile(log)
	if !strings.Contains(string(args), "-ss 1 -i ") || !strings.Contains(string(args), "scale=640:-2") {
		t.Errorf("ffmpeg args = %s", args)
	}

	// A new version gets its own poster.
	v2 := filepath.Join(t.TempDir(), "final-v2.webm")
	writeFile(t, v2, "webm")
	o, err = Revise(p, o.ID, v2, "shorter", "agent", now)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(o.Poster, "-final-v2.webm"+PosterSuffix) || o.Versions[1].Poster != o.Poster || o.Versions[0].Poster == o.Poster {
		t.Errorf("revised poster = %q, versions %+v", o.Poster, o.Versions)
	}
	if _, err := Revise(p, o.ID, writeTemp(t, "notes.md"), "", "agent", now); err == nil || !strings.Contains(err.Error(), ".mp4 or .webm") {
		t.Errorf("revise a video with a .md: %v", err)
	}
}

func writeTemp(t *testing.T, name string) string {
	path := filepath.Join(t.TempDir(), name)
	writeFile(t, path, "x")
	return path
}

func TestAddVideoWithoutFfmpeg(t *testing.T) {
	p := project(t)
	noFfmpeg(t)
	o, err := Add(p, AddInput{Department: "product", Title: "Draft", Kind: "video", File: writeTemp(t, "final.mp4")}, now)
	if err != nil {
		t.Fatalf("add must not fail without ffmpeg: %v", err)
	}
	if o.Poster != "" || !errors.Is(o.PosterErr, ErrNoFfmpeg) {
		t.Errorf("poster = %q, err %v", o.Poster, o.PosterErr)
	}
	// Not a video: no poster and no reason.
	doc, err := Add(p, AddInput{Department: "product", Title: "Doc", Kind: "report", File: writeTemp(t, "r.md")}, now)
	if err != nil || doc.Poster != "" || doc.PosterErr != nil {
		t.Errorf("doc = %+v, %v", doc, err)
	}
}

func TestAddRefusesOverLimit(t *testing.T) {
	p := project(t)
	noFfmpeg(t)
	// A video may be 200 MB; anything else stays at 25 MB.
	if _, err := Add(p, AddInput{Department: "product", Title: "Long", Kind: "video", File: sparse(t, "long.mp4", 150<<20)}, now); err != nil {
		t.Errorf("150 MB video refused: %v", err)
	}
	cases := []struct {
		in   AddInput
		want string
	}{
		{AddInput{Kind: "video", File: sparse(t, "master-4k.mp4", MaxVideoBytes+1)}, "is 200 MB, over the shelf's 200 MB for a video: export a smaller cut"},
		{AddInput{Kind: "design", File: sparse(t, "poster.png", 30<<20)}, "is 30 MB, over the shelf's 25 MB: put it somewhere"},
		{AddInput{Kind: "video", File: writeTemp(t, "final.mov")}, "--kind video plays .mp4 or .webm on the shelf, got .mov: convert it (ffmpeg -i final.mov final.mp4)"},
		{AddInput{Kind: "video", Path: "out/final.mp4"}, "copy it there with --file"},
	}
	for _, c := range cases {
		c.in.Department, c.in.Title = "product", "t"
		if _, err := Add(p, c.in, now); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: err = %v, want %q", c.in, err, c.want)
		}
	}
	// A video link is a plain link.
	if o, err := Add(p, AddInput{Department: "product", Title: "Cut", Kind: "video", URL: "https://videos.example/cut"}, now); err != nil || o.IsVideo() {
		t.Errorf("video link: %+v %v", o, err)
	}
	if list, _ := Load(p); len(list) != 2 {
		t.Errorf("shelf = %v, want the 150 MB video and the link", ids(list))
	}
}

// With the real ffmpeg, a video shorter than 1 s still gets its first frame.
func TestMakePosterShortVideo(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	dir := t.TempDir()
	video := filepath.Join(dir, "short.mp4")
	out, err := exec.Command("ffmpeg", "-v", "error", "-nostdin", "-f", "lavfi", "-i", "testsrc=size=160x90:rate=10:duration=0.5", "-pix_fmt", "yuv420p", video).CombinedOutput()
	if err != nil {
		t.Skipf("ffmpeg cannot make a test video: %v %s", err, out)
	}
	poster := filepath.Join(dir, "short.jpg")
	if err := MakePoster(video, poster); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(poster); err != nil || st.Size() == 0 {
		t.Errorf("poster: %v", err)
	}
	broken := filepath.Join(dir, "broken.mp4")
	writeFile(t, broken, "not a video")
	if err := MakePoster(broken, filepath.Join(dir, "b.jpg")); err == nil || !strings.Contains(err.Error(), "ffmpeg failed") {
		t.Errorf("broken video: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "b.jpg")); err == nil {
		t.Error("a failed run left a poster")
	}
}
