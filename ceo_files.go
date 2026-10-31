package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// Files in the CEO chat: anything that isn't an image the chat shows as a
// thumbnail (a .zip, a PDF, notes). POST …/lead/file (owner only) streams it
// into the project's chat files folder
// (.agent-logs/<lead>/chat/files/<hash>/<name>, next to the images) and
// answers its absolute path; the send ends the prompt with one
// "[File: <path>]" line per file, which the agent reads with its own tools.
// The owner's turn shows those lines back as chips that download the file
// (GET …/lead/file/<hash>/<name>, owner only, always as an attachment).
//
// The viewer never opens what it stores: a zip is bytes, never unpacked;
// an .html or .svg is never served as a page. Every read and write inside
// the folder goes through os.Root, so a symlink can't lead out of it.

const (
	// ceoFileMax caps one file, in bytes.
	ceoFileMax = 50 << 20
	// ceoFilesPerMessage caps the files in one message (images count apart).
	ceoFilesPerMessage = 5
	// ceoFileNameMax caps a stored name, in bytes.
	ceoFileNameMax = 120
	// ceoFileExtMax is the longest tail after the last dot kept as the
	// extension when a long name is cut.
	ceoFileExtMax = 16
	// ceoFileTempAge is how old a left-over upload temp file is before the
	// next upload removes it.
	ceoFileTempAge = time.Hour
	// ceoFileTempPrefix starts the temp file an upload streams into.
	ceoFileTempPrefix = ".up-"
	// ceoFileTransfer is how long one upload or download may take: the
	// server's 30 s read and 60 s write limits would cut a 50 MB file on a
	// slow link, so these two routes get their own deadline.
	ceoFileTransfer = 10 * time.Minute
	// ceoFileBadgeMax caps the chip's type badge (it is 40 px wide).
	ceoFileBadgeMax = 5
)

// ceoFileDeadlines gives a file transfer its own read and write deadline.
// A writer that can't set them (a test recorder) keeps the server's.
func ceoFileDeadlines(w http.ResponseWriter) {
	rc := http.NewResponseController(w)
	until := time.Now().Add(ceoFileTransfer)
	rc.SetReadDeadline(until)
	rc.SetWriteDeadline(until)
}

// ceoFileRel is a stored file relative to the files folder: the content
// hash's folder and the cleaned name (ceoFileName). Upload, send, turn and
// download all use this one pattern.
var ceoFileRel = regexp.MustCompile(`^([0-9a-f]{32})/([A-Za-z0-9_-][A-Za-z0-9._-]{0,119})$`)

// ceoFileLine finds a file line in a turn's text. The pane may wrap a long
// path, so the path is read with its whitespace removed.
var ceoFileLine = regexp.MustCompile(`\[File:\s*([^\]]+)\]`)

// CEOFile is one file in the owner's turn, or the answer to an upload.
type CEOFile struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Size int64  `json:"size"`
	// Type is the chip's badge: the extension upper-cased, FILE without one.
	Type string `json:"type"`
	URL  string `json:"url"`
}

// SizeText is Size for people: "812 KB", "3.4 MB".
func (f CEOFile) SizeText() string { return ceoSizeText(f.Size) }

func ceoSizeText(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d B", n)
}

// ceoFileDir is where the project's chats keep their files, absolute: the
// project lead's chat folder, for the CEO's chat and department chats alike
// (as images, ceoImageDir).
func ceoFileDir(proj *tracker.Project) string {
	return filepath.Join(filepath.Dir(ceoImageDir(proj)), "files")
}

// ceoFileName is the stored name for a file the browser called original:
// the part after the last / or \, every byte outside ASCII A-Za-z0-9._-
// replaced by _, leading dots dropped, at most 120 bytes keeping an
// extension of up to 16 bytes, "file" when nothing is left.
func ceoFileName(original string) string {
	if i := strings.LastIndexAny(original, `/\`); i >= 0 {
		original = original[i+1:]
	}
	b := []byte(original)
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			b[i] = '_'
		}
	}
	name := strings.TrimLeft(string(b), ".")
	if name == "" {
		return "file"
	}
	if len(name) > ceoFileNameMax {
		ext := ""
		if i := strings.LastIndex(name, "."); i > 0 && len(name)-i-1 >= 1 && len(name)-i-1 <= ceoFileExtMax {
			ext = name[i:]
		}
		name = name[:ceoFileNameMax-len(ext)] + ext
	}
	return name
}

// ceoFileType is a stored name's badge: its extension upper-cased and cut
// to 5 characters, FILE when it has none.
func ceoFileType(name string) string {
	if i := strings.LastIndex(name, "."); i > 0 && i < len(name)-1 {
		ext := strings.ToUpper(name[i+1:])
		if len(ext) > ceoFileBadgeMax {
			ext = ext[:ceoFileBadgeMax]
		}
		return ext
	}
	return "FILE"
}

// ceoFileURL is a stored file's download URL.
func ceoFileURL(prefix, rel string) string {
	return prefix + "/lead/file/" + rel
}

// ceoFileStore opens the files folder as an os.Root, making it (0700) when
// create is set. The folder itself must be a real folder, not a link.
func ceoFileStore(proj *tracker.Project, create bool) (*os.Root, string, error) {
	dir := ceoFileDir(proj)
	if create {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, "", err
		}
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, "", err
	}
	if !info.IsDir() {
		return nil, "", fmt.Errorf("%s is not a folder", dir)
	}
	root, err := os.OpenRoot(dir)
	return root, dir, err
}

// ceoStoredFileRel checks rel (hash/name) names a stored file inside root:
// the pattern, the hash folder a real folder and the file a regular file,
// none of them links. It answers the file's size.
func ceoStoredFileRel(root *os.Root, rel string) (int64, bool) {
	m := ceoFileRel.FindStringSubmatch(rel)
	if m == nil {
		return 0, false
	}
	if info, err := root.Lstat(m[1]); err != nil || !info.IsDir() {
		return 0, false
	}
	info, err := root.Lstat(rel)
	if err != nil || !info.Mode().IsRegular() {
		return 0, false
	}
	return info.Size(), true
}

// ceoStoredFile is the stored file an absolute path names, or false when it
// is not a file in the project's chat files folder.
func ceoStoredFile(proj *tracker.Project, path string) (CEOFile, bool) {
	root, dir, err := ceoFileStore(proj, false)
	if err != nil {
		return CEOFile{}, false
	}
	defer root.Close()
	path = filepath.Clean(path)
	rel, err := filepath.Rel(dir, path)
	if err != nil || !filepath.IsAbs(path) {
		return CEOFile{}, false
	}
	rel = filepath.ToSlash(rel)
	size, ok := ceoStoredFileRel(root, rel)
	if !ok {
		return CEOFile{}, false
	}
	name := rel[strings.IndexByte(rel, '/')+1:]
	return CEOFile{Path: path, Name: name, Size: size, Type: ceoFileType(name)}, true
}

// ceoFileLines is the prompt's tail for the files sent with a message, one
// "[File: <path>]" line each. Every path must be a stored chat file.
func ceoFileLines(proj *tracker.Project, paths []string) (string, error) {
	if len(paths) > ceoFilesPerMessage {
		return "", fmt.Errorf("at most %d files in one message", ceoFilesPerMessage)
	}
	var lines []string
	for _, p := range paths {
		f, ok := ceoStoredFile(proj, p)
		if !ok {
			return "", fmt.Errorf("not a chat file: %s", p)
		}
		lines = append(lines, "[File: "+f.Path+"]")
	}
	return strings.Join(lines, "\n"), nil
}

// ceoTurnFiles takes the file lines out of an owner's message: the text
// without them, and the files that are stored chat files. A line naming
// anything else stays in the text. The pane may have wrapped the path, so
// it is compared with the folder with whitespace removed from both: a
// project root with a space in it still matches.
func ceoTurnFiles(proj *tracker.Project, prefix, text string) (string, []CEOFile) {
	if !strings.Contains(text, "[File:") {
		return text, nil
	}
	dir := ceoFileDir(proj)
	squashedDir := strings.Join(strings.Fields(dir), "") + "/"
	var files []CEOFile
	rest := ceoFileLine.ReplaceAllStringFunc(text, func(m string) string {
		path := strings.Join(strings.Fields(ceoFileLine.FindStringSubmatch(m)[1]), "")
		if !strings.HasPrefix(path, squashedDir) {
			return m
		}
		f, ok := ceoStoredFile(proj, filepath.Join(dir, path[len(squashedDir):]))
		if !ok {
			return m
		}
		rel, _ := filepath.Rel(dir, f.Path)
		f.URL = ceoFileURL(prefix, filepath.ToSlash(rel))
		files = append(files, f)
		return ""
	})
	return strings.TrimSpace(rest), files
}

// ceoSweepTemps removes upload temp files older than ceoFileTempAge: what an
// upload cut short by a crash left behind.
func ceoSweepTemps(root *os.Root) {
	d, err := root.Open(".")
	if err != nil {
		return
	}
	entries, _ := d.ReadDir(-1)
	d.Close()
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ceoFileTempPrefix) {
			continue
		}
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > ceoFileTempAge {
			root.Remove(e.Name())
		}
	}
}

// handleCEOFile serves POST /p/<project>/lead/file, multipart with the file
// in "file" (owner only): streams it into the chat files folder and answers
// {path, name, size, type, url}. 413 over 50 MB, 400 when empty or missing.
// The body is read part by part (never ParseMultipartForm, which spills big
// parts to the system temp folder); the file goes to a temp file in the
// store, hashed as it is written, then renamed to <hash>/<name>.
func (s *Server) handleCEOFile(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	tooBig := func(err error) bool {
		var mb *http.MaxBytesError
		return errors.As(err, &mb)
	}
	overLimit := "file is over 50 MB"
	ceoFileDeadlines(w)
	mr, err := r.MultipartReader()
	if err != nil {
		leadChatError(w, http.StatusBadRequest, "error", "no file in the request (multipart field file)")
		return
	}
	var part io.Reader
	var original string
	for {
		p, err := mr.NextPart()
		if err != nil {
			if tooBig(err) {
				leadChatError(w, http.StatusRequestEntityTooLarge, "too-large", overLimit)
			} else {
				leadChatError(w, http.StatusBadRequest, "error", "no file in the request (multipart field file)")
			}
			return
		}
		if p.FormName() == "file" {
			part, original = p, p.FileName()
			break
		}
		p.Close()
	}
	name := ceoFileName(original)

	root, dir, err := ceoFileStore(proj, true)
	if err != nil {
		leadChatError(w, http.StatusInternalServerError, "error", "could not open the chat files folder: "+err.Error())
		return
	}
	defer root.Close()
	ceoSweepTemps(root)

	tmp := ceoFileTempPrefix + mustRandomHex(8)
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		leadChatError(w, http.StatusInternalServerError, "error", "could not save the file: "+err.Error())
		return
	}
	kept := false
	defer func() {
		if !kept {
			root.Remove(tmp)
		}
	}()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(part, ceoFileMax+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil && tooBig(err), n > ceoFileMax:
		leadChatError(w, http.StatusRequestEntityTooLarge, "too-large", overLimit)
		return
	case err != nil:
		leadChatError(w, http.StatusBadRequest, "error", "could not read the file")
		return
	case n == 0:
		leadChatError(w, http.StatusBadRequest, "empty", "the file is empty")
		return
	}

	hash := hex.EncodeToString(h.Sum(nil)[:16])
	if err := root.Mkdir(hash, 0700); err != nil && !errors.Is(err, fs.ErrExist) {
		leadChatError(w, http.StatusInternalServerError, "error", "could not save the file: "+err.Error())
		return
	}
	if info, err := root.Lstat(hash); err != nil || !info.IsDir() {
		leadChatError(w, http.StatusInternalServerError, "error", "could not save the file: the hash folder is not a folder")
		return
	}
	rel := hash + "/" + name
	if _, ok := ceoStoredFileRel(root, rel); !ok {
		if err := root.Rename(tmp, rel); err != nil {
			leadChatError(w, http.StatusInternalServerError, "error", "could not save the file: "+err.Error())
			return
		}
		kept = true
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(CEOFile{
		Path: filepath.Join(dir, hash, name), Name: name, Size: n, Type: ceoFileType(name), URL: ceoFileURL(prefix, rel),
	})
}

// handleCEOFileGet serves GET /p/<project>/lead/file/<hash>/<name> (owner
// only): the stored file as a download, never as a page. 404 for anything
// that isn't a stored chat file.
func (s *Server) handleCEOFileGet(w http.ResponseWriter, r *http.Request, proj *tracker.Project, rest string) {
	rel := strings.TrimPrefix(rest, "lead/file/")
	ceoFileDeadlines(w)
	root, _, err := ceoFileStore(proj, false)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer root.Close()
	if _, ok := ceoStoredFileRel(root, rel); !ok {
		http.NotFound(w, r)
		return
	}
	f, err := root.Open(rel)
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
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": info.Name()}))
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, "", info.ModTime(), f)
}
