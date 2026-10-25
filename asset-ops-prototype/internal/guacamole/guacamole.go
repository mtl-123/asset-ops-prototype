// Package guacamole creates RDP connections through the Apache Guacamole REST
// API and builds the opaque client URL used by the Guacamole web application.
//
// The client URL never contains a password: it is the base64 form of
// "<connection id>\0c\0<data source>" (Guacamole 0.9.8+). Guacamole stores the
// connection parameters server-side, so the browser only receives an opaque
// identifier. The Guacamole web application still requires its own login unless
// the deployment is configured with a no-auth/SSO extension.
package guacamole

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Client struct {
	base       string
	username   string
	password   string
	dataSource string
	http       *http.Client

	mu       sync.Mutex
	token    string
	tokenDS  string
	tokenAt  time.Time
	tokenTTL time.Duration
}

type tokenResponse struct {
	AuthToken  string `json:"authToken"`
	DataSource string `json:"dataSource"`
}

type connection struct {
	Identifier       string            `json:"identifier"`
	ParentIdentifier string            `json:"parentIdentifier"`
	Name             string            `json:"name"`
	Protocol         string            `json:"protocol"`
	Parameters       map[string]string `json:"parameters"`
	Attributes       map[string]string `json:"attributes"`
}

type connectionPayload struct {
	ParentIdentifier string            `json:"parentIdentifier"`
	Name             string            `json:"name"`
	Protocol         string            `json:"protocol"`
	Parameters       map[string]string `json:"parameters"`
	Attributes       map[string]string `json:"attributes"`
}

// New validates the Guacamole base URL and prepares a REST client. The base URL
// is the Guacamole web application root (commonly ending in /guacamole/).
func New(rawURL, username, password, dataSource string) (*Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("guacamole: invalid base URL")
	}
	parsed.Fragment, parsed.RawQuery = "", ""
	if strings.TrimSpace(username) == "" || strings.TrimSpace(password) == "" {
		return nil, errors.New("guacamole: admin username and password are required")
	}
	return &Client{
		base:       strings.TrimRight(parsed.String(), "/") + "/",
		username:   username,
		password:   password,
		dataSource: strings.TrimSpace(dataSource),
		http:       &http.Client{Timeout: 20 * time.Second},
		tokenTTL:   25 * time.Minute,
	}, nil
}

func (c *Client) authenticate(ctx context.Context) (string, string, error) {
	form := url.Values{"username": {c.username}, "password": {c.password}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"api/tokens", strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := c.http.Do(request)
	if err != nil {
		return "", "", fmt.Errorf("guacamole: authenticate: %w", err)
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("guacamole: authenticate: HTTP %d", response.StatusCode)
	}
	var token tokenResponse
	if err := json.Unmarshal(payload, &token); err != nil || token.AuthToken == "" {
		return "", "", errors.New("guacamole: authentication response is invalid")
	}
	dataSource := token.DataSource
	if dataSource == "" {
		dataSource = c.dataSource
	}
	if dataSource == "" {
		return "", "", errors.New("guacamole: data source is missing")
	}
	c.mu.Lock()
	c.token, c.tokenDS, c.tokenAt = token.AuthToken, dataSource, time.Now()
	c.mu.Unlock()
	return token.AuthToken, dataSource, nil
}

func (c *Client) authToken(ctx context.Context) (string, string, error) {
	c.mu.Lock()
	if c.token != "" && time.Since(c.tokenAt) < c.tokenTTL {
		token, dataSource := c.token, c.tokenDS
		c.mu.Unlock()
		return token, dataSource, nil
	}
	c.mu.Unlock()
	return c.authenticate(ctx)
}

func (c *Client) invalidateToken() {
	c.mu.Lock()
	c.token = ""
	c.mu.Unlock()
}

// do performs one authenticated API call. A 401 clears the cached token and
// retries once, which handles Guacamole token expiry without surfacing it to
// the user as a failed connection.
func (c *Client) do(ctx context.Context, method string, path func(dataSource string) string, body any, out any) error {
	for attempt := 0; attempt < 2; attempt++ {
		token, dataSource, err := c.authToken(ctx)
		if err != nil {
			return err
		}
		var reader io.Reader
		if body != nil {
			raw, err := json.Marshal(body)
			if err != nil {
				return err
			}
			reader = strings.NewReader(string(raw))
		}
		request, err := http.NewRequestWithContext(ctx, method, c.base+strings.TrimLeft(path(dataSource), "/")+"?token="+url.QueryEscape(token), reader)
		if err != nil {
			return err
		}
		if body != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		response, err := c.http.Do(request)
		if err != nil {
			return fmt.Errorf("guacamole: request: %w", err)
		}
		payload, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		response.Body.Close()
		if response.StatusCode == http.StatusUnauthorized && attempt == 0 {
			c.invalidateToken()
			continue
		}
		if response.StatusCode < 200 || response.StatusCode > 299 {
			var detail struct {
				Message             string `json:"message"`
				TranslatableMessage string `json:"translatableMessage"`
			}
			_ = json.Unmarshal(payload, &detail)
			message := strings.TrimSpace(detail.TranslatableMessage)
			if message == "" {
				message = strings.TrimSpace(detail.Message)
			}
			if message == "" {
				message = http.StatusText(response.StatusCode)
			}
			return fmt.Errorf("guacamole: %s", message)
		}
		if out != nil && len(payload) > 0 {
			if err := json.Unmarshal(payload, out); err != nil {
				return fmt.Errorf("guacamole: decode response: %w", err)
			}
		}
		return nil
	}
	return errors.New("guacamole: authentication expired")
}

// EnsureConnection creates or updates a deterministic per-asset RDP connection
// and returns the opaque browser URL. Passwords stay in Guacamole's connection
// store; they are never included in the URL.
func (c *Client) EnsureConnection(ctx context.Context, assetID string, parameters map[string]string) (string, error) {
	for _, required := range []string{"hostname", "port", "username", "password"} {
		if strings.TrimSpace(parameters[required]) == "" {
			return "", fmt.Errorf("guacamole: missing RDP parameter %s", required)
		}
	}
	name := "qiyun-" + strings.TrimSpace(assetID)
	if name == "qiyun-" {
		return "", errors.New("guacamole: asset id is required")
	}
	payload := connectionPayload{
		ParentIdentifier: "ROOT", Name: name, Protocol: "rdp",
		Parameters: parameters, Attributes: map[string]string{},
	}
	if payload.Parameters["security"] == "" {
		payload.Parameters["security"] = "nla"
	}
	if payload.Parameters["ignore-cert"] == "" {
		payload.Parameters["ignore-cert"] = "true"
	}
	if payload.Parameters["resize-method"] == "" {
		payload.Parameters["resize-method"] = "display-update"
	}

	var existing map[string]connection
	if err := c.do(ctx, http.MethodGet, func(dataSource string) string {
		return "api/session/data/" + url.PathEscape(dataSource) + "/connections"
	}, nil, &existing); err != nil {
		return "", err
	}
	identifier := ""
	for id, item := range existing {
		if item.Name == name {
			identifier = id
			break
		}
	}
	if identifier != "" {
		if err := c.do(ctx, http.MethodPut, func(dataSource string) string {
			return "api/session/data/" + url.PathEscape(dataSource) + "/connections/" + url.PathEscape(identifier)
		}, payload, nil); err != nil {
			return "", err
		}
	} else {
		var created struct {
			Identifier string `json:"identifier"`
		}
		if err := c.do(ctx, http.MethodPost, func(dataSource string) string {
			return "api/session/data/" + url.PathEscape(dataSource) + "/connections"
		}, payload, &created); err != nil {
			return "", err
		}
		identifier = created.Identifier
	}
	if identifier == "" {
		return "", errors.New("guacamole: connection identifier is missing")
	}
	token, dataSource, err := c.authToken(ctx)
	_ = token
	if err != nil {
		return "", err
	}
	return c.ClientURL(identifier, dataSource), nil
}

// ClientURL builds the Guacamole 0.9.8+ client URL. The fragment contains
// base64("<identifier>\0c\0<data source>") and therefore no credentials.
func (c *Client) ClientURL(identifier, dataSource string) string {
	raw := identifier + "\x00c\x00" + dataSource
	encoded := base64.StdEncoding.EncodeToString([]byte(raw))
	return c.base + "#/client/" + encoded
}
