package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Contact represents a Feishu user for the assignee picker.
type Contact struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Avatar string `json:"avatar,omitempty"`
	Email  string `json:"email,omitempty"`
}

// SearchContacts searches the organization's users via app_access_token.
// For a small VC team we list all users in the root department and filter
// by name client-side. This avoids the user_access_token requirement of
// the /search/v1/user endpoint.
//
// Required application-identity permission: contact:department.base:readonly
// and contact:user.base:readonly (应用身份权限, NOT 用户身份权限).
func (c *OAuthClient) SearchContacts(ctx context.Context, query string) ([]Contact, error) {
	appToken, err := c.getAppAccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("app_access_token: %w", err)
	}

	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil, nil
	}

	users, err := c.listDepartmentUsers(ctx, appToken, "0")
	if err != nil {
		return nil, err
	}

	var results []Contact
	for _, u := range users {
		name := strings.ToLower(u.Name)
		email := strings.ToLower(u.Email)
		enName := strings.ToLower(u.EnName)
		if strings.Contains(name, query) || strings.Contains(email, query) || strings.Contains(enName, query) {
			results = append(results, Contact{
				ID:     u.OpenID,
				Name:   u.Name,
				Avatar: u.Avatar.AvatarOrigin,
				Email:  u.Email,
			})
		}
	}
	return results, nil
}

type feishuDeptUser struct {
	OpenID string `json:"open_id"`
	Name   string `json:"name"`
	EnName string `json:"en_name"`
	Email  string `json:"email"`
	Avatar struct {
		AvatarOrigin string `json:"avatar_origin"`
		Avatar72     string `json:"avatar_72"`
	} `json:"avatar"`
}

func (c *OAuthClient) listDepartmentUsers(ctx context.Context, appToken, deptID string) ([]feishuDeptUser, error) {
	var all []feishuDeptUser
	pageToken := ""

	for {
		q := url.Values{}
		q.Set("department_id", deptID)
		q.Set("page_size", "50")
		if pageToken != "" {
			q.Set("page_token", pageToken)
		}

		apiURL := feishuHost + "/open-apis/contact/v3/users/find_by_department?" + q.Encode()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
		req.Header.Set("Authorization", "Bearer "+appToken)

		body, err := c.do(req)
		if err != nil {
			return nil, fmt.Errorf("list dept users: %w", err)
		}

		var resp struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
			Data struct {
				HasMore   bool             `json:"has_more"`
				PageToken string           `json:"page_token"`
				Items     []feishuDeptUser `json:"items"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("decode dept users: %w", err)
		}
		if resp.Code != 0 {
			return nil, fmt.Errorf("feishu dept users code=%d msg=%s", resp.Code, resp.Msg)
		}

		all = append(all, resp.Data.Items...)
		if !resp.Data.HasMore || resp.Data.PageToken == "" {
			break
		}
		pageToken = resp.Data.PageToken
	}

	return all, nil
}
