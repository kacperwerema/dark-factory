package directpost

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Post is one reel to publish.
type Post struct {
	// File is the MP4 (or MOV) on disk.
	File string
	// Caption is the post's text: Instagram's caption, Facebook's
	// description, TikTok's title, Pinterest's description.
	Caption string
	// Title is Pinterest's pin title (Facebook's video title when set).
	Title string
	// Link is Pinterest's destination link.
	Link string
	// Board is Pinterest's board name; empty takes account: board.
	Board string
	// Privacy is TikTok's privacy level; empty is SELF_ONLY.
	Privacy string
	// CoverAtMS is the frame used as the cover, in milliseconds from the
	// start, when HasCover.
	CoverAtMS int
	HasCover  bool
	// AILabel marks AI-made content where the API can (TikTok's is_aigc);
	// elsewhere it is a note to set the label by hand.
	AILabel bool
}

// Result is what a publish did.
type Result struct {
	ID, URL string
	// Notes are things the owner still does by hand (an AI label the API
	// can't set, a private TikTok post).
	Notes []string
}

// Limits each platform puts on the text.
const (
	InstagramCaptionMax  = 2200
	InstagramHashtagsMax = 30
	TikTokTitleMax       = 2200
	PinterestTitleMax    = 100
	PinterestDescMax     = 800
)

var hashtag = regexp.MustCompile(`#\S+`)

// Problems is what the platform would refuse in p, before any call: the
// file, the text's limits, the fields it needs.
func Problems(platform string, p Post, account map[string]string) []string {
	var out []string
	switch ext := strings.ToLower(filepath.Ext(p.File)); {
	case p.File == "":
		out = append(out, "no --file <mp4>")
	case ext != ".mp4" && ext != ".mov":
		out = append(out, fmt.Sprintf("%s is not an .mp4 or .mov", filepath.Base(p.File)))
	default:
		if st, err := os.Stat(p.File); err != nil || st.IsDir() {
			out = append(out, fmt.Sprintf("no file %s", p.File))
		} else if st.Size() == 0 {
			out = append(out, fmt.Sprintf("%s is empty", filepath.Base(p.File)))
		}
	}
	n := utf8.RuneCountInString(p.Caption)
	switch platform {
	case InstagramReels:
		if n > InstagramCaptionMax {
			out = append(out, fmt.Sprintf("the caption has %d characters (Instagram: at most %d)", n, InstagramCaptionMax))
		}
		if h := len(hashtag.FindAllString(p.Caption, -1)); h > InstagramHashtagsMax {
			out = append(out, fmt.Sprintf("the caption has %d hashtags (Instagram: at most %d)", h, InstagramHashtagsMax))
		}
	case FacebookReels:
		if strings.TrimSpace(p.Caption) == "" {
			out = append(out, "no caption (--caption-file)")
		}
	case TikTok:
		if n > TikTokTitleMax {
			out = append(out, fmt.Sprintf("the caption has %d characters (TikTok: at most %d)", n, TikTokTitleMax))
		}
	case Pinterest:
		t := utf8.RuneCountInString(p.Title)
		switch {
		case t == 0:
			out = append(out, "no pin title (--title)")
		case t > PinterestTitleMax:
			out = append(out, fmt.Sprintf("the title has %d characters (Pinterest: at most %d)", t, PinterestTitleMax))
		}
		if n > PinterestDescMax {
			out = append(out, fmt.Sprintf("the caption has %d characters (Pinterest's description: at most %d)", n, PinterestDescMax))
		}
		if p.Board == "" && strings.TrimSpace(account[AccountBoard]) == "" {
			out = append(out, "no board (--board, or account: board: in the connector)")
		}
	}
	return out
}

// Publish posts p on c's platform.
func (c *Client) Publish(ctx context.Context, p Post) (Result, error) {
	if probs := Problems(c.Platform, p, c.Account); len(probs) > 0 {
		return Result{}, fmt.Errorf("%s: not sent: %s", c.Platform, strings.Join(probs, "; "))
	}
	switch c.Platform {
	case InstagramReels:
		return c.publishInstagram(ctx, p)
	case FacebookReels:
		return c.publishFacebook(ctx, p)
	case TikTok:
		return c.publishTikTok(ctx, p)
	case Pinterest:
		return c.publishPinterest(ctx, p)
	}
	return Result{}, fmt.Errorf("publish: %q is not %s", c.Platform, strings.Join(Platforms, ", "))
}

// Preflight is the read-only calls a dry run makes: Instagram's daily
// quota, TikTok's creator_info (what the account may post now). Each line
// says what it found; an error is a call that failed.
func (c *Client) Preflight(ctx context.Context, p Post) ([]string, error) {
	switch c.Platform {
	case InstagramReels:
		q, err := c.instagramQuota(ctx)
		if err != nil {
			return nil, err
		}
		return []string{q.String()}, nil
	case TikTok:
		info, err := c.tiktokCreator(ctx)
		if err != nil {
			return nil, err
		}
		lines := []string{info.String()}
		if _, err := info.privacy(p.Privacy); err != nil {
			lines = append(lines, err.Error())
		}
		return lines, nil
	}
	return nil, nil
}

// aiNote is the note for an AI label the platform's API can't set.
func aiNote(platform string, p Post) []string {
	if !p.AILabel {
		return nil
	}
	return []string{fmt.Sprintf("%s's API sets no AI label: turn it on by hand in the post's settings", platform)}
}
