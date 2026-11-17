package viewer

import (
	"io"
	"mime"
	"net/http"
	"os"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// sniffTextBytes is how much of a file serveUntrustedFile reads to tell
// text from binary.
const sniffTextBytes = 8 << 10

// serveUntrustedFile serves a stored file that is no image (a file
// attachment, an evidence file) so the browser never runs it: text
// (tracker.IsTextData on its first 8KB) as text/plain; charset=utf-8,
// inline, anything else as application/octet-stream with
// Content-Disposition: attachment. Both carry nosniff and a sandbox CSP.
// path must already be confined and checked as a regular file; name is the
// download's file name.
func serveUntrustedFile(w http.ResponseWriter, r *http.Request, path, name string) {
	f, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	head := make([]byte, sniffTextBytes)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		http.Error(w, "read error", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	if textHead(head, info.Size() > int64(n)) {
		h.Set("Content-Type", "text/plain; charset=utf-8")
		h.Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": name}))
	} else {
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	}
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	http.ServeContent(w, r, "", info.ModTime(), f)
}

// textHead is tracker.IsTextData on a file's first bytes; when the read cut
// the file (more), up to 3 trailing bytes of a split UTF-8 rune are allowed.
func textHead(head []byte, more bool) bool {
	if tracker.IsTextData(head) {
		return true
	}
	if !more {
		return false
	}
	for cut := 1; cut <= 3 && cut < len(head); cut++ {
		if tracker.IsTextData(head[:len(head)-cut]) {
			return true
		}
	}
	return false
}
