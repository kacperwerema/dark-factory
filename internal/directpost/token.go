package directpost

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// WarnDays is how close an end date makes token check fail.
const WarnDays = 7

// MetaScopes are the permissions a Meta publisher's page token needs.
var MetaScopes = []string{"pages_manage_posts", "pages_read_engagement", "instagram_basic", "instagram_content_publish"}

// Check is token check: whether the key works and until when, never the
// key. bad is something invalid or ending within WarnDays.
func (c *Client) Check(ctx context.Context) (lines []string, bad bool, err error) {
	switch c.Platform {
	case InstagramReels, FacebookReels:
		return c.checkMeta(ctx)
	case TikTok:
		info, err := c.tiktokCreator(ctx)
		if err != nil {
			return nil, true, err
		}
		return []string{"tiktok: the token works", info.String()}, false, nil
	case Pinterest:
		return c.checkPinterest(ctx)
	}
	return nil, true, fmt.Errorf("token check: %q is not %s", c.Platform, strings.Join(Platforms, ", "))
}

func (c *Client) days(t time.Time) int { return int(t.Sub(c.now()).Hours() / 24) }

func (c *Client) checkMeta(ctx context.Context) ([]string, bool, error) {
	if c.secret(MetaPageToken) == "" {
		return []string{"meta: no " + MetaPageToken}, true, nil
	}
	if c.secret(MetaAppID) == "" || c.secret(MetaAppSecret) == "" {
		return []string{"meta: " + MetaAppID + " and " + MetaAppSecret + " are needed to check the page token (debug_token)"}, true, nil
	}
	var r struct {
		Data struct {
			IsValid             bool     `json:"is_valid"`
			Type                string   `json:"type"`
			ProfileID           string   `json:"profile_id"`
			ExpiresAt           int64    `json:"expires_at"`
			DataAccessExpiresAt int64    `json:"data_access_expires_at"`
			Scopes              []string `json:"scopes"`
		} `json:"data"`
	}
	q := url.Values{"input_token": {c.secret(MetaPageToken)}, "access_token": {c.secret(MetaAppID) + "|" + c.secret(MetaAppSecret)}}
	if err := c.graphGet(ctx, c.Hosts.Graph, "/debug_token", q, "debug_token", &r); err != nil {
		return nil, true, err
	}
	d := r.Data
	if !d.IsValid {
		return []string{"meta: the page token is NOT valid: the owner runs token refresh in their own shell"}, true, nil
	}
	var out []string
	bad := false
	if d.Type != "" && d.Type != "PAGE" {
		out = append(out, fmt.Sprintf("meta: this is a %s token; publishing needs the page's token (token refresh stores it)", d.Type))
		bad = true
	}
	if page := c.account(AccountPageID); page != "" && d.ProfileID != "" && d.ProfileID != page {
		out = append(out, "meta: the token belongs to another page than account: page_id")
		bad = true
	}
	if d.ExpiresAt == 0 {
		out = append(out, "meta: the page token is valid and does not expire")
	} else {
		e := time.Unix(d.ExpiresAt, 0)
		out = append(out, fmt.Sprintf("meta: the page token is valid until %s (%d days left)", e.UTC().Format("2006-01-02"), c.days(e)))
		bad = bad || c.days(e) < WarnDays
	}
	if d.DataAccessExpiresAt != 0 {
		e := time.Unix(d.DataAccessExpiresAt, 0)
		out = append(out, fmt.Sprintf("meta: the app's data access lasts until %s (%d days left; the owner renews it with token refresh)", e.UTC().Format("2006-01-02"), c.days(e)))
		bad = bad || c.days(e) < WarnDays
	}
	var missing []string
	for _, s := range MetaScopes {
		if !contains(d.Scopes, s) {
			missing = append(missing, s)
		}
	}
	if len(missing) > 0 {
		out = append(out, "meta: missing permissions "+strings.Join(missing, ", "))
		bad = true
	} else {
		out = append(out, "meta: permissions ok ("+strings.Join(MetaScopes, ", ")+")")
	}
	if page := c.account(AccountPageID); page != "" {
		var p struct {
			IG *struct {
				ID string `json:"id"`
			} `json:"instagram_business_account"`
		}
		if err := c.graphGet(ctx, c.Hosts.Graph, "/"+url.PathEscape(page), url.Values{"fields": {"instagram_business_account"}}, "page", &p); err != nil {
			return out, true, err
		}
		ig := c.account(AccountIGUserID)
		switch {
		case p.IG == nil || p.IG.ID == "":
			if c.Platform == InstagramReels {
				out = append(out, "meta: the page has no Instagram business account linked")
				bad = true
			}
		case ig != "" && p.IG.ID != ig:
			out = append(out, "meta: account: ig_user_id is not the Instagram account linked to the page")
			bad = true
		default:
			out = append(out, "meta: the page's Instagram account is linked")
		}
	}
	return out, bad, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (c *Client) checkPinterest(ctx context.Context) ([]string, bool, error) {
	if c.secret(PinterestToken) == "" {
		return []string{"pinterest: no " + PinterestToken}, true, nil
	}
	var u struct {
		Username    string `json:"username"`
		AccountType string `json:"account_type"`
	}
	if err := c.pinterestCall(ctx, "GET", "/user_account", nil, "account", &u); err != nil {
		return nil, true, err
	}
	out := []string{fmt.Sprintf("pinterest: the token works, account %s (%s)", orDash(u.Username), orDash(u.AccountType))}
	bad := false
	if b := c.account(AccountBoard); b != "" {
		if _, err := c.pinterestBoard(ctx, b); err != nil {
			out = append(out, err.Error())
			bad = true
		} else {
			out = append(out, fmt.Sprintf("pinterest: board %q is there", b))
		}
	}
	if c.secret(PinterestRefreshTok) == "" {
		out = append(out, "pinterest: no "+PinterestRefreshTok+": token refresh can't renew the token when it ends")
	}
	return out, bad, nil
}

// Refresh is token refresh: the new values by secret name, for the caller
// to hand to Keychain (tracker.SecretWrite); never printed. userToken is
// Meta's: a user token the owner just made (Graph API Explorer), traded
// for a long-lived one and then the page's token.
func (c *Client) Refresh(ctx context.Context, userToken string) (map[string]string, error) {
	switch c.Platform {
	case InstagramReels, FacebookReels:
		return c.refreshMeta(ctx, userToken)
	case TikTok:
		for _, k := range []string{TikTokClientKey, TikTokClientSecret, TikTokRefreshToken} {
			if c.secret(k) == "" {
				return nil, fmt.Errorf("tiktok: token refresh needs %s", k)
			}
		}
		t, err := c.tiktokRefresh(ctx)
		if err != nil {
			return nil, err
		}
		out := map[string]string{TikTokAccessToken: t.Access}
		if t.Refresh != "" {
			out[TikTokRefreshToken] = t.Refresh
		}
		return out, nil
	case Pinterest:
		for _, k := range []string{PinterestAppID, PinterestAppSecret, PinterestRefreshTok} {
			if c.secret(k) == "" {
				return nil, fmt.Errorf("pinterest: token refresh needs %s", k)
			}
		}
		t, err := c.pinterestRefresh(ctx)
		if err != nil {
			return nil, err
		}
		out := map[string]string{PinterestToken: t.Access}
		if t.Refresh != "" {
			out[PinterestRefreshTok] = t.Refresh
		}
		return out, nil
	}
	return nil, fmt.Errorf("token refresh: %q is not %s", c.Platform, strings.Join(Platforms, ", "))
}

func (c *Client) refreshMeta(ctx context.Context, userToken string) (map[string]string, error) {
	userToken = strings.TrimSpace(userToken)
	page := c.account(AccountPageID)
	switch {
	case userToken == "":
		return nil, fmt.Errorf("meta: token refresh reads a user token on stdin (Graph API Explorer, with %s)", strings.Join(MetaScopes, ", "))
	case c.secret(MetaAppID) == "" || c.secret(MetaAppSecret) == "":
		return nil, fmt.Errorf("meta: token refresh needs %s and %s", MetaAppID, MetaAppSecret)
	case page == "":
		return nil, fmt.Errorf("meta: token refresh needs account: page_id, to pick the page's token")
	}
	// The pasted token is redacted from here on too.
	if c.Secrets == nil {
		c.Secrets = map[string]string{}
	}
	c.Secrets["meta-user-token"] = userToken
	var long struct {
		AccessToken string `json:"access_token"`
	}
	q := url.Values{
		"grant_type":        {"fb_exchange_token"},
		"client_id":         {c.secret(MetaAppID)},
		"client_secret":     {c.secret(MetaAppSecret)},
		"fb_exchange_token": {userToken},
		"access_token":      {userToken},
	}
	if err := c.graphGet(ctx, c.Hosts.Graph, "/oauth/access_token", q, "long-lived token", &long); err != nil {
		return nil, err
	}
	if long.AccessToken == "" {
		return nil, c.fail("long-lived token", 0, "", "the answer has no access_token", "", KindToken)
	}
	c.Secrets["meta-long-token"] = long.AccessToken
	var acc struct {
		Data []struct {
			ID          string `json:"id"`
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	if err := c.graphGet(ctx, c.Hosts.Graph, "/me/accounts", url.Values{"fields": {"id,access_token"}, "access_token": {long.AccessToken}}, "pages", &acc); err != nil {
		return nil, err
	}
	for _, p := range acc.Data {
		if p.ID == page && p.AccessToken != "" {
			c.Secrets["meta-page-token-new"] = p.AccessToken
			return map[string]string{MetaPageToken: p.AccessToken}, nil
		}
	}
	return nil, fmt.Errorf("meta: the user token has no role on page account: page_id (it reaches %d pages)", len(acc.Data))
}
