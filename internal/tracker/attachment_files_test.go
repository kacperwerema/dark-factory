package tracker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCleanFileName(t *testing.T) {
	for in, want := range map[string]string{
		"cpu.pprof":             "cpu.pprof",
		"dir/../top output.txt": "top-output.txt",
		".hidden.txt":           "hidden.txt",
		"__x.log":               "x.log",
		"żółw.txt":              "w.txt",
		"...":                   "",
		"a/b/c.json":            "c.json",
	} {
		if got := CleanFileName(in); got != want {
			t.Errorf("CleanFileName(%q) = %q, want %q", in, got, want)
		}
	}
	long := CleanFileName(strings.Repeat("a", 200) + ".txt")
	if len(long) != 100 || filepath.Ext(long) != ".txt" {
		t.Errorf("long name = %q (%d bytes)", long, len(long))
	}
}

func TestAttachmentKind(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	for want, data := range map[string][]byte{
		"image": png,
		"text":  []byte("Showing: 12s\nżółw\n"),
		"file":  {0x1f, 0x8b, 0x08, 0x00, 0x00},
	} {
		if got := AttachmentKind(data); got != want {
			t.Errorf("AttachmentKind(%q) = %q, want %q", data, got, want)
		}
	}
	if IsTextData([]byte("a\x00b")) || IsTextData([]byte{0xff, 0xfe}) {
		t.Error("NUL or invalid UTF-8 read as text")
	}
}

// A file that is no image is saved as attachments/<hash>/<clean name>, once
// for the same bytes; an image name on other bytes is refused.
func TestSaveAttachmentFile(t *testing.T) {
	dir := t.TempDir()
	rel, err := SaveAttachmentFile(dir, "/x/cpu prof.pprof", []byte{0x1f, 0x8b, 1})
	if err != nil {
		t.Fatal(err)
	}
	if !AttachmentFilePattern.MatchString(rel) || filepath.Base(rel) != "cpu-prof.pprof" {
		t.Fatalf("rel = %q", rel)
	}
	again, err := SaveAttachmentFile(dir, "cpu prof.pprof", []byte{0x1f, 0x8b, 1})
	if err != nil || again != rel {
		t.Errorf("again = %q, %v; want %q", again, err, rel)
	}
	if _, err := os.Stat(filepath.Join(AttachmentsDir(dir), filepath.FromSlash(rel))); err != nil {
		t.Error(err)
	}
	if _, err := SaveAttachmentFile(dir, "fake.png", []byte("<svg/>")); err == nil {
		t.Error("fake.png holding text was saved")
	}
	if _, err := SaveAttachmentFile(dir, "...", []byte("x")); err == nil {
		t.Error("a name with nothing left was saved")
	}
}

// EvidenceName keeps a non-image's own name and extension, with the step in
// front, and refuses one named like an image.
func TestEvidenceNameAnyFile(t *testing.T) {
	got, err := EvidenceName("out/top.txt", "AC1", []byte("Showing\n"))
	if err != nil || got != "AC1-top.txt" {
		t.Errorf("top.txt = %q, %v", got, err)
	}
	got, err = EvidenceName("cpu profile.pprof", "", []byte{0x1f, 0x8b})
	if err != nil || got != "cpu-profile.pprof" {
		t.Errorf("cpu profile.pprof = %q, %v", got, err)
	}
	for _, name := range []string{"fake.png", "x.SVG", "y.jpeg"} {
		if _, err := EvidenceName(name, "", []byte("text")); err == nil {
			t.Errorf("%s holding text: want refused", name)
		}
	}
	if _, err := EvidenceName("_x.txt", "", []byte("t")); err == nil {
		t.Error("_x.txt: want refused")
	}
}
