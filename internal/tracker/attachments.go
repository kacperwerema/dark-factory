package tracker

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// MaxAttachmentBytes is the largest image an upload or issue-cli attach
// takes.
const MaxAttachmentBytes = 10 << 20

// attachmentExt maps the image types an attachment may be to the extension
// it is saved and served with.
var attachmentExt = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// AttachmentNamePattern is the only file name the viewer's /attachments/
// serves: what SaveAttachment writes, a hash and an image extension.
var AttachmentNamePattern = regexp.MustCompile(`^[0-9a-f]{32}\.(png|jpg|gif|webp)$`)

// AttachmentExt returns the extension data is saved under, from its sniffed
// type, never a file name: GIF89a<script>… named x.html sniffs as an image
// but would be served as HTML on the viewer's origin (docs/API/security.md).
// ok is false when data is not a PNG, JPEG, GIF or WebP image.
func AttachmentExt(data []byte) (ext string, ok bool) {
	ext, ok = attachmentExt[http.DetectContentType(data)]
	return ext, ok
}

// AttachmentsDir is where an issues folder keeps its attachments.
func AttachmentsDir(issueDir string) string {
	return filepath.Join(issueDir, "attachments")
}

// SaveAttachment writes data under <issueDir>/attachments/ as
// <first 16 bytes of its sha256><ext> and returns that name. The same bytes
// give the same name, so a second save writes nothing.
func SaveAttachment(issueDir string, data []byte) (string, error) {
	ext, ok := AttachmentExt(data)
	if !ok {
		return "", fmt.Errorf("only PNG, JPEG, GIF or WebP images allowed")
	}
	if len(data) > MaxAttachmentBytes {
		return "", fmt.Errorf("file too large (max 10MB)")
	}
	hash := sha256.Sum256(data)
	name := fmt.Sprintf("%x%s", hash[:16], ext)
	dir := AttachmentsDir(issueDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dest := filepath.Join(dir, name)
	if _, err := os.Stat(dest); os.IsNotExist(err) {
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			return "", err
		}
	}
	return name, nil
}

// AttachmentURL is the root-relative URL the viewer serves an attachment
// at.
func AttachmentURL(projectSlug, name string) string {
	return "/p/" + projectSlug + "/attachments/" + name
}

// AttachmentMarkdown is the markdown image link issue-cli attach prints for
// an attachment; alt text can't close the link's brackets.
func AttachmentMarkdown(projectSlug, name, alt string) string {
	alt = strings.NewReplacer("[", "(", "]", ")", "\n", " ").Replace(alt)
	return fmt.Sprintf("![%s](%s)", alt, AttachmentURL(projectSlug, name))
}
