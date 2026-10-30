package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/ynui/inlet/internal/config"
	"github.com/ynui/inlet/internal/labels"
)

type deviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
	Error           string `json:"error"`
	Description     string `json:"error_description"`
}

func cmdLogin(args []string) {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, loginUsage) }
	serverFlag := fs.String("server", "", "inlet server URL")
	labelFlag := fs.String("label", "", "name for this client (defaults to this computer's name)")
	noBrowser := fs.Bool("no-browser", false, "print the GitHub code without opening a browser")
	if err := fs.Parse(args); err != nil {
		return
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "inlet login: unexpected argument")
		fs.Usage()
		os.Exit(2)
	}

	cc := CConfig{}
	if err := config.Load("config.json", &cc); err != nil {
		loginFail(err)
	}
	if *serverFlag != "" {
		cc.Server = *serverFlag
	}
	if cc.Server == "" {
		cc.Server = defaultServerURL
	} else {
		var err error
		cc.Server, err = normalizeServerURL(cc.Server)
		if err != nil {
			loginFail(fmt.Errorf("invalid server URL: %w", err))
		}
	}
	if !secureLoginServer(cc.Server) {
		loginFail(fmt.Errorf("GitHub login requires a secure server URL (wss:// or https://); localhost is allowed over ws:// or http://"))
	}

	label := strings.TrimSpace(*labelFlag)
	if label == "" {
		label, _ = os.Hostname()
		if label == "" {
			label = "client"
		}
	}
	label, err := labels.Normalize(label)
	if err != nil {
		loginFail(err)
	}

	httpClient := &http.Client{Timeout: 15 * time.Second}
	info, err := signupInfo(httpClient, cc.Server)
	if err != nil {
		loginFail(err)
	}
	if !info.GitHubEnabled || info.GitHubClientID == "" {
		loginFail(fmt.Errorf("GitHub login is not enabled on %s; ask its administrator for an invite", cc.Server))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	githubToken, err := githubDeviceLogin(ctx, httpClient, info.GitHubClientID, !*noBrowser)
	if err != nil {
		loginFail(err)
	}

	endpoint, err := signupURL(cc.Server)
	if err != nil {
		loginFail(err)
	}
	body, err := json.Marshal(map[string]string{"githubToken": githubToken, "label": label})
	if err != nil {
		loginFail(err)
	}
	resp, err := httpClient.Post(endpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		loginFail(fmt.Errorf("inlet signup request failed: %w", err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		loginFail(fmt.Errorf("login: %s: %s", resp.Status, bytes.TrimSpace(message)))
	}
	var out struct {
		Token        string `json:"token"`
		ID           string `json:"id"`
		PublicURL    string `json:"publicUrl"`
		AccountLogin string `json:"accountLogin"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		loginFail(fmt.Errorf("decode inlet login response: %w", err))
	}
	cc.Token = out.Token
	if err := config.Save("config.json", cc); err != nil {
		loginFail(fmt.Errorf("GitHub login succeeded, but saving client config failed: %w", err))
	}
	fmt.Printf("✓ Signed in with GitHub as @%s. Token saved.\nserver: %s\npublic url: %s\nnext: inlet http <local-port>\n",
		out.AccountLogin, cc.Server, out.PublicURL)
}

func signupInfo(client *http.Client, server string) (struct {
	Open           bool   `json:"open"`
	GitHubEnabled  bool   `json:"githubEnabled"`
	GitHubClientID string `json:"githubClientID"`
}, error) {
	var info struct {
		Open           bool   `json:"open"`
		GitHubEnabled  bool   `json:"githubEnabled"`
		GitHubClientID string `json:"githubClientID"`
	}
	endpoint, err := signupURL(server)
	if err != nil {
		return info, err
	}
	resp, err := client.Get(endpoint)
	if err != nil {
		return info, fmt.Errorf("check inlet signup endpoint: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return info, fmt.Errorf("inlet signup endpoint returned %s", resp.Status)
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&info)
	return info, err
}

func githubDeviceLogin(ctx context.Context, client *http.Client, clientID string, openBrowser bool) (string, error) {
	form := url.Values{"client_id": {clientID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://github.com/login/device/code", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("start GitHub device login: %w", err)
	}
	var device deviceCodeResponse
	err = json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&device)
	resp.Body.Close()
	if err != nil {
		return "", fmt.Errorf("read GitHub device login response: %w", err)
	}
	if resp.StatusCode != http.StatusOK || device.Error != "" || device.DeviceCode == "" || device.UserCode == "" {
		if device.Description != "" {
			return "", fmt.Errorf("GitHub device login: %s", device.Description)
		}
		return "", fmt.Errorf("GitHub device login returned %s", resp.Status)
	}
	if device.Interval < 1 {
		device.Interval = 5
	}
	if device.ExpiresIn < 1 {
		device.ExpiresIn = 900
	}
	fmt.Printf("GitHub code: %s\n", device.UserCode)
	if openBrowser {
		if err := openBrowserURL(device.VerificationURI); err != nil {
			fmt.Printf("Couldn't open a browser: %v\n", err)
			fmt.Printf("Open %s and enter the code above.\n", device.VerificationURI)
		} else {
			fmt.Printf("Complete sign-in in the browser that opened. This terminal will continue when you're done.\n")
		}
	} else {
		fmt.Printf("Open %s and enter the code above to authorize inlet.\n", device.VerificationURI)
	}

	deadline := time.Now().Add(time.Duration(device.ExpiresIn) * time.Second)
	interval := time.Duration(device.Interval) * time.Second
	for time.Now().Before(deadline) {
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", ctx.Err()
		case <-timer.C:
		}
		token, pollErr := pollGitHubDevice(ctx, client, clientID, device.DeviceCode)
		if pollErr == nil {
			return token, nil
		}
		var pending *devicePendingError
		if errors.As(pollErr, &pending) {
			if pending.slowDown {
				interval += 5 * time.Second
			}
			continue
		}
		return "", pollErr
	}
	return "", fmt.Errorf("GitHub device code expired; run 'inlet login' to try again")
}

func openBrowserURL(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

type devicePendingError struct {
	slowDown bool
}

func (e *devicePendingError) Error() string { return "GitHub authorization is still pending" }

func pollGitHubDevice(ctx context.Context, client *http.Client, clientID, deviceCode string) (string, error) {
	form := url.Values{
		"client_id":   {clientID},
		"device_code": {deviceCode},
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://github.com/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("poll GitHub device login: %w", err)
	}
	var token struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&token)
	resp.Body.Close()
	if err != nil {
		return "", fmt.Errorf("read GitHub token response: %w", err)
	}
	if token.AccessToken != "" {
		return token.AccessToken, nil
	}
	switch token.Error {
	case "authorization_pending":
		return "", &devicePendingError{}
	case "slow_down":
		return "", &devicePendingError{slowDown: true}
	case "expired_token":
		return "", fmt.Errorf("GitHub device code expired; run 'inlet login' to try again")
	case "access_denied":
		return "", fmt.Errorf("GitHub login was denied")
	default:
		if token.Description != "" {
			return "", fmt.Errorf("GitHub login failed: %s", token.Description)
		}
		return "", fmt.Errorf("GitHub token request returned %s", resp.Status)
	}
}

func loginFail(err error) {
	fmt.Fprintln(os.Stderr, "inlet login:", err)
	os.Exit(1)
}

func secureLoginServer(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Scheme == "wss" || u.Scheme == "https" {
		return true
	}
	if u.Scheme != "ws" && u.Scheme != "http" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

const loginUsage = `inlet login - sign in with GitHub and save a client token

Usage:
  inlet login [--server <url>] [--label <name>] [--no-browser]

The client opens GitHub in your browser and shows a one-time code. Approve
inlet there; this terminal continues when sign-in completes. The GitHub access
token is exchanged for an inlet token and is not saved locally. Use --no-browser
on a remote or headless machine.
`
