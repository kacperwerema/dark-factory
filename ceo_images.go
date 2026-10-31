package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// Images in the CEO chat: the owner pastes, drops or attaches an image in the
// box, POST …/lead/image keeps it in the lead's chat folder
// (.agent-logs/<lead>/chat/images/<hash>.<ext>, next to the chat's text
// files) and answers its absolute path; the send ends the prompt with one
// "[Image: <path>]" line per image, which Claude Code reads with its Read
// tool. The owner's turn shows those lines back as thumbnails.

const (
	// ceoImageMax caps one image, in bytes.
	ceoImageMax = 10 << 20
	// ceoImagesPerMessage caps the images in one message.
	ceoImagesPerMessage = 5
)

// ceoImageExts are the image types the chat takes, by sniffed content type.
// SVG is not one: it can carry script.
var ceoImageExts = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// ceoImageName is a stored image's file name: the content hash and its type.
var ceoImageName = regexp.MustCompile(`^[0-9a-f]{32}\.(png|jpg|gif|webp)$`)

// ceoImageLine finds an image line in a turn's text. The pane may wrap a
// long path, so the path is read with its whitespace removed.
var ceoImageLine = regexp.MustCompile(`\[Image:\s*([^\]]+)\]`)

// CEOImage is one image in the owner's turn.
type CEOImage struct {
	Path string `json:"path"`
	URL  string `json:"url"`
}

// ceoImageDir is where the project's CEO chat keeps its images, absolute.
func ceoImageDir(proj *tracker.Project) string {
	root := projectRoot(proj)
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	return filepath.Join(root, ".agent-logs", tracker.LeadSessionName(proj), "chat", "images")
}

// ceoImageURL is the thumbnail's URL through the /files/ route.
func ceoImageURL(proj *tracker.Project, prefix, name string) string {
	return prefix + "/files/.agent-logs/" + tracker.LeadSessionName(proj) + "/chat/images/" + name
}

// ceoStoredImage is the stored image a path names, or false when the path is
// not a file in the project's chat images folder.
func ceoStoredImage(proj *tracker.Project, path string) (string, bool) {
	dir := ceoImageDir(proj)
	path = filepath.Clean(path)
	name := filepath.Base(path)
	if filepath.Dir(path) != dir || !ceoImageName.MatchString(name) {
		return "", false
	}
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	return name, true
}

// ceoImageLines is the prompt's tail for the images sent with a message, one
// "[Image: <path>]" line each. Every path must be a stored chat image.
func ceoImageLines(proj *tracker.Project, paths []string) (string, error) {
	if len(paths) > ceoImagesPerMessage {
		return "", fmt.Errorf("at most %d images in one message", ceoImagesPerMessage)
	}
	var lines []string
	for _, p := range paths {
		if _, ok := ceoStoredImage(proj, p); !ok {
			return "", fmt.Errorf("not a chat image: %s", p)
		}
		lines = append(lines, "[Image: "+filepath.Clean(p)+"]")
	}
	return strings.Join(lines, "\n"), nil
}

// ceoTurnImages takes the image lines out of an owner's message: the text
// without them, and the images that are stored chat images. A line naming
// anything else stays in the text.
func ceoTurnImages(proj *tracker.Project, prefix, text string) (string, []CEOImage) {
	var imgs []CEOImage
	rest := ceoImageLine.ReplaceAllStringFunc(text, func(m string) string {
		path := strings.Join(strings.Fields(ceoImageLine.FindStringSubmatch(m)[1]), "")
		name, ok := ceoStoredImage(proj, path)
		if !ok {
			return m
		}
		imgs = append(imgs, CEOImage{Path: filepath.Clean(path), URL: ceoImageURL(proj, prefix, name)})
		return ""
	})
	return strings.TrimSpace(rest), imgs
}

// withTurnAttachments fills the owner's turns' Images and Files and drops
// their lines from the text.
func withTurnAttachments(proj *tracker.Project, prefix string, turns []CEOTurn) {
	for i, t := range turns {
		if t.From != "you" || len(t.Text) == 0 {
			continue
		}
		turns[i].Text[0], turns[i].Images = ceoTurnImages(proj, prefix, t.Text[0])
		turns[i].Text[0], turns[i].Files = ceoTurnFiles(proj, prefix, turns[i].Text[0])
	}
}

// handleCEOImage serves POST /p/<project>/lead/image, multipart with the
// image in "file": keeps it in the chat images folder and answers {path,
// url}. 413 over 10 MB, 415 when it isn't a PNG, JPEG, GIF or WebP.
func (s *Server) handleCEOImage(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	r.Body = http.MaxBytesReader(w, r.Body, ceoImageMax+64<<10)
	file, _, err := r.FormFile("file")
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			leadChatError(w, http.StatusRequestEntityTooLarge, "too-large", "image is over 10 MB")
		} else {
			leadChatError(w, http.StatusBadRequest, "error", "no image in the request (multipart field file)")
		}
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, ceoImageMax+1))
	if err != nil {
		leadChatError(w, http.StatusBadRequest, "error", "could not read the image")
		return
	}
	if len(data) > ceoImageMax {
		leadChatError(w, http.StatusRequestEntityTooLarge, "too-large", "image is over 10 MB")
		return
	}
	ext, ok := ceoImageExts[http.DetectContentType(data)]
	if !ok {
		leadChatError(w, http.StatusUnsupportedMediaType, "not-image", "only PNG, JPEG, GIF or WebP images")
		return
	}
	hash := sha256.Sum256(data)
	name := fmt.Sprintf("%x%s", hash[:16], ext)
	dir := ceoImageDir(proj)
	dest := filepath.Join(dir, name)
	if _, err := os.Stat(dest); err != nil {
		err = os.MkdirAll(dir, 0755)
		if err == nil {
			err = os.WriteFile(dest, data, 0644)
		}
		if err != nil {
			leadChatError(w, http.StatusInternalServerError, "error", "could not save the image: "+err.Error())
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(CEOImage{Path: dest, URL: ceoImageURL(proj, prefix, name)})
}
