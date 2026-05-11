package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Feishu OAuth (自建应用 web scan-code flow).
// Reference endpoints:
//   auth page : https://open.feishu.cn/open-apis/authen/v1/index
//   token API : https://open.feishu.cn/open-apis/authen/v1/access_token
//   userinfo  : https://open.feishu.cn/open-apis/authen/v1/user_info
//   app token : https://open.feishu.cn/open-apis/auth/v3/app_access_token/internal
//
// We authenticate end users via the authen/v1/access_token endpoint which
// takes the authorization "code" + the app_access_token and returns a
// user_access_token plus identity fields.

const (
	feishuHost        = "https://open.feishu.cn"
	feishuAuthorize   = feishuHost + "/open-apis/authen/v1/index"
	feishuAppToken    = feishuHost + "/open-apis/auth/v3/app_access_token/internal"
	feishuAccessToken = feishuHost + "/open-apis/authen/v1/access_token"
	feishuUserInfo    = feishuHost + "/open-apis/authen/v1/user_info"
)

// OAuthClient handles the Feishu OAuth dance.
type OAuthClient struct {
	AppID       string
	AppSecret   string
	RedirectURL string

	http *http.Client

	// cached app_access_token
	appToken     string
	appTokenExp  time.Time
}

// NewOAuthClient builds a client. All three fields are required.
func NewOAuthClient(appID, appSecret, redirectURL string) *OAuthClient {
	return &OAuthClient{
		AppID:       appID,
		AppSecret:   appSecret,
		RedirectURL: redirectURL,
		http:        &http.Client{Timeout: 15 * time.Second},
	}
}

// IsConfigured reports whether all required fields are set.
func (c *OAuthClient) IsConfigured() bool {
	return c != nil && c.AppID != "" && c.AppSecret != "" && c.RedirectURL != ""
}

// AuthorizeURL returns the Feishu 扫码登录 page URL for the given state.
func (c *OAuthClient) AuthorizeURL(state string) string {
	q := url.Values{}
	q.Set("app_id", c.AppID)
	q.Set("redirect_uri", c.RedirectURL)
	q.Set("state", state)
	return feishuAuthorize + "?" + q.Encode()
}

// FeishuUser is the identity returned by authen/v1/user_info.
type FeishuUser struct {
	Name      string `json:"name"`
	EnName    string `json:"en_name"`
	AvatarURL string `json:"avatar_url"`
	OpenID    string `json:"open_id"`
	UnionID   string `json:"union_id"`
	UserID    string `json:"user_id"`
	Email     string `json:"email"`
	Mobile    string `json:"mobile"`
}

// ExchangeCode swaps a one-time code for a FeishuUser identity.
func (c *OAuthClient) ExchangeCode(ctx context.Context, code string) (*FeishuUser, error) {
	appToken, err := c.getAppAccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("app_access_token: %w", err)
	}

	// Step 1: code -> user_access_token
	body, _ := json.Marshal(map[string]string{
		"grant_type": "authorization_code",
		"code":       code,
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, feishuAccessToken, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+appToken)

	tokResp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("access_token request: %w", err)
	}
	var tokParsed struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			AccessToken string `json:"access_token"`
			TokenType   string `json:"token_type"`
			ExpiresIn   int    `json:"expires_in"`
			OpenID      string `json:"open_id"`
			UnionID     string `json:"union_id"`
			UserID      string `json:"user_id"`
			Name        string `json:"name"`
			EnName      string `json:"en_name"`
			AvatarURL   string `json:"avatar_url"`
			Email       string `json:"email"`
			Mobile      string `json:"mobile"`
		} `json:"data"`
	}
	if err := json.Unmarshal(tokResp, &tokParsed); err != nil {
		return nil, fmt.Errorf("decode access_token: %w (body=%s)", err, truncate(tokResp))
	}
	if tokParsed.Code != 0 {
		return nil, fmt.Errorf("feishu access_token err code=%d msg=%s", tokParsed.Code, tokParsed.Msg)
	}
	if tokParsed.Data.AccessToken == "" {
		return nil, fmt.Errorf("feishu access_token empty (body=%s)", truncate(tokResp))
	}

	// In practice authen/v1/access_token already returns the identity we need,
	// but we also hit user_info to fill any missing fields (email/mobile are
	// sometimes absent from the token payload).
	u := &FeishuUser{
		Name:      tokParsed.Data.Name,
		EnName:    tokParsed.Data.EnName,
		AvatarURL: tokParsed.Data.AvatarURL,
		OpenID:    tokParsed.Data.OpenID,
		UnionID:   tokParsed.Data.UnionID,
		UserID:    tokParsed.Data.UserID,
		Email:     tokParsed.Data.Email,
		Mobile:    tokParsed.Data.Mobile,
	}

	if u.Email == "" || u.Mobile == "" || u.Name == "" {
		if extra, err := c.fetchUserInfo(ctx, tokParsed.Data.AccessToken); err == nil {
			if u.Name == "" {
				u.Name = extra.Name
			}
			if u.AvatarURL == "" {
				u.AvatarURL = extra.AvatarURL
			}
			if u.Email == "" {
				u.Email = extra.Email
			}
			if u.Mobile == "" {
				u.Mobile = extra.Mobile
			}
			if u.OpenID == "" {
				u.OpenID = extra.OpenID
			}
			if u.UnionID == "" {
				u.UnionID = extra.UnionID
			}
		}
	}

	if u.OpenID == "" {
		return nil, fmt.Errorf("feishu did not return open_id")
	}
	return u, nil
}

func (c *OAuthClient) fetchUserInfo(ctx context.Context, userAccessToken string) (*FeishuUser, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, feishuUserInfo, nil)
	req.Header.Set("Authorization", "Bearer "+userAccessToken)
	body, err := c.do(req)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Code int        `json:"code"`
		Msg  string     `json:"msg"`
		Data FeishuUser `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	if parsed.Code != 0 {
		return nil, fmt.Errorf("user_info code=%d msg=%s", parsed.Code, parsed.Msg)
	}
	return &parsed.Data, nil
}

func (c *OAuthClient) getAppAccessToken(ctx context.Context) (string, error) {
	if c.appToken != "" && time.Now().Before(c.appTokenExp) {
		return c.appToken, nil
	}
	body, _ := json.Marshal(map[string]string{
		"app_id":     c.AppID,
		"app_secret": c.AppSecret,
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, feishuAppToken, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := c.do(req)
	if err != nil {
		return "", err
	}
	var parsed struct {
		Code           int    `json:"code"`
		Msg            string `json:"msg"`
		AppAccessToken string `json:"app_access_token"`
		Expire         int    `json:"expire"`
	}
	if err := json.Unmarshal(resp, &parsed); err != nil {
		return "", err
	}
	if parsed.Code != 0 || parsed.AppAccessToken == "" {
		return "", fmt.Errorf("app_access_token code=%d msg=%s", parsed.Code, parsed.Msg)
	}

	c.appToken = parsed.AppAccessToken
	// renew slightly before expiry
	if parsed.Expire > 60 {
		c.appTokenExp = time.Now().Add(time.Duration(parsed.Expire-60) * time.Second)
	} else {
		c.appTokenExp = time.Now().Add(5 * time.Minute)
	}
	return c.appToken, nil
}

func (c *OAuthClient) do(req *http.Request) ([]byte, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, truncate(body))
	}
	return body, nil
}

func truncate(b []byte) string {
	s := string(b)
	s = strings.TrimSpace(s)
	if len(s) > 400 {
		return s[:400] + "...(truncated)"
	}
	return s
}
