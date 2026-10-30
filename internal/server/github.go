package server

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

type gitHubIdentity struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
}

// validateGitHubToken asks GitHub to validate that the device-flow token was
// issued by this OAuth app and returns the stable GitHub account identity.
func validateGitHubToken(ctx context.Context, clientID, clientSecret, token string) (*gitHubIdentity, error) {
	if token == "" {
		return nil, fmt.Errorf("empty GitHub token")
	}
	body, err := json.Marshal(map[string]string{"access_token": token})
	if err != nil {
		return nil, err
	}
	endpoint := "https://api.github.com/applications/" + url.PathEscape(clientID) + "/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(clientID, clientSecret)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "inletd")

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub token validation returned %s", resp.Status)
	}
	var result struct {
		App struct {
			ClientID string `json:"client_id"`
		} `json:"app"`
		User gitHubIdentity `json:"user"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&result); err != nil {
		return nil, err
	}
	if result.App.ClientID != clientID || result.User.ID <= 0 || strings.TrimSpace(result.User.Login) == "" {
		return nil, fmt.Errorf("GitHub returned an incomplete or mismatched identity")
	}
	return &result.User, nil
}
