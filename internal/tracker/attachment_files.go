package tracker

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

// A file attachment is any file that is no image (a CPU profile, a text
// capture): issue-cli attach saves it as <issues>/attachments/<hash>/<name>,
// the hash of its bytes as a folder so the name stays as written
// (go tool pprof cpu.pprof), and the viewer serves it as text or as a
// download, never as a page (docs/API/security.md).

// AttachmentFilePattern is the only path under /attachments/ the viewer
// serves for a file attachment: a hash folder and a clean name.
var AttachmentFilePattern = regexp.MustCompile(`^[0-9a-f]{32}/[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

// maxCleanName is the longest name CleanFileName keeps, in bytes.
const maxCleanName = 100

// CleanFileName keeps the base of name to ASCII letters, digits, ".", "_"
// and "-" (any other rune becomes "-"), drops leading dots, underscores and
// dashes and keeps at most 100 bytes, the extension kept when it is cut.
// "" when nothing is left.
func CleanFileName(name string) string {
	base := filepath.Base(filepath.Clean(name))
	var b strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.TrimLeft(b.String(), "._-")
	if len(out) > maxCleanName {
		ext := path.Ext(out)
		if len(ext) > 16 {
			ext = ""
		}
		out = out[:maxCleanName-len(ext)] + ext
	}
	return out
}

// IsTextData reports whether data reads as text: valid UTF-8 with no NUL
// byte. The viewer shows such a file in the browser as text/plain; anything
// else is a download.
func IsTextData(data []byte) bool {
	return utf8.Valid(data) && bytes.IndexByte(data, 0) < 0
}

// AttachmentKind is "image" for data that sniffs as an attachment image,
// "text" for text (IsTextData) and "file" for anything else.
func AttachmentKind(data []byte) string {
	if _, ok := AttachmentExt(data); ok {
		return "image"
	}
	if IsTextData(data) {
		return "text"
	}
	return "file"
}

// imageExts are the extensions the viewer's /files/ route serves as images;
// a file that is no image may not be saved under one (LooksLikeImageName).
var imageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".svg": true, ".avif": true}

// LooksLikeImageName reports whether name's extension is one the viewer
// serves as an image.
func LooksLikeImageName(name string) bool {
	return imageExts[strings.ToLower(filepath.Ext(name))]
}

// SaveAttachmentFile writes data, a file that is no image, as
// <issueDir>/attachments/<hash>/<clean name> and returns "<hash>/<name>".
// The same bytes under the same name are saved once.
func SaveAttachmentFile(issueDir, name string, data []byte) (string, error) {
	if len(data) > MaxAttachmentBytes {
		return "", fmt.Errorf("file too large (max 10MB)")
	}
	clean := CleanFileName(name)
	if clean == "" {
		return "", fmt.Errorf("a file name needs a letter or digit")
	}
	if _, ok := AttachmentExt(data); ok {
		return "", fmt.Errorf("an image is saved with SaveAttachment")
	}
	if LooksLikeImageName(clean) {
		return "", fmt.Errorf("named like an image but the bytes are no PNG, JPEG, GIF or WebP")
	}
	hash := sha256.Sum256(data)
	rel := fmt.Sprintf("%x/%s", hash[:16], clean)
	dir := filepath.Join(AttachmentsDir(issueDir), fmt.Sprintf("%x", hash[:16]))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dest := filepath.Join(dir, clean)
	if _, err := os.Lstat(dest); os.IsNotExist(err) {
		tmp, err := os.CreateTemp(dir, ".save-*")
		if err != nil {
			return "", err
		}
		defer os.Remove(tmp.Name())
		if _, err := tmp.Write(data); err != nil {
			tmp.Close()
			return "", err
		}
		if err := tmp.Close(); err != nil {
			return "", err
		}
		if err := os.Chmod(tmp.Name(), 0o644); err != nil {
			return "", err
		}
		if err := os.Rename(tmp.Name(), dest); err != nil {
			return "", err
		}
	}
	return rel, nil
}

// AttachmentFileMarkdown is a plain link (no "!") to a file attachment.
func AttachmentFileMarkdown(projectSlug, rel string) string {
	return fmt.Sprintf("[%s](%s)", path.Base(rel), AttachmentURL(projectSlug, rel))
}
