package atlassian

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
)

type formsDiscovery struct {
	mu sync.Mutex
	id string
}

var cloudIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Forms returns a client scoped to the documented Basic-auth Forms gateway.
// There is deliberately no configurable gateway URL. Transport injection in
// ClientConfig permits local testing without changing the credential boundary.
func (c *Client) Forms(ctx context.Context) (*Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.formsDiscovery.mu.Lock()
	id := c.formsDiscovery.id
	c.formsDiscovery.mu.Unlock()
	if id == "" {
		// Discovery is documented for the tenant origin, not a Jira API base path.
		u, err := url.Parse(c.baseURL)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Scheme != "https" {
			return nil, fmt.Errorf("Forms discovery requires an HTTPS tenant origin URL without credentials, path, query, or fragment")
		}
		discovery := c.scopedClient(c.baseURL)
		resp, err := discovery.Do(ctx, http.MethodGet, "/_edge/tenant_info", nil)
		if err != nil {
			return nil, fmt.Errorf("discovering Forms cloud ID: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("discovering Forms cloud ID: HTTP %d", resp.StatusCode)
		}
		var tenant struct {
			CloudID string `json:"cloudId"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&tenant); err != nil || !cloudIDPattern.MatchString(tenant.CloudID) {
			return nil, fmt.Errorf("tenant discovery returned an invalid cloudId")
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id = tenant.CloudID
		// Cache successes only. Concurrent discoveries are safe and cancellation of
		// one caller cannot poison subsequent calls (unlike sync.Once).
		c.formsDiscovery.mu.Lock()
		c.formsDiscovery.id = id
		c.formsDiscovery.mu.Unlock()
	}
	return c.scopedClient("https://api.atlassian.com/jira/forms/cloud/" + id), nil
}

func (c *Client) scopedClient(base string) *Client {
	hc := *c.httpClient
	hc.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{baseURL: base, user: c.user, token: c.token, version: c.version, httpClient: &hc, forms: true, formsDiscovery: c.formsDiscovery}
}

func validFormsPath(path string) bool {
	u, err := url.Parse(path)
	if err != nil || u.IsAbs() || u.Host != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return false
	}
	for _, part := range strings.Split(strings.TrimPrefix(u.Path, "/"), "/") {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "\\\r\n") {
			return false
		}
	}
	return path == "/_edge/tenant_info" || strings.HasPrefix(path, "/project/")
}

func (c *Client) statusError(method, path string, status int, body []byte) error {
	if c.forms {
		// Responses (including proxies) may echo authorization or submitted data.
		return fmt.Errorf("Forms %s: unexpected HTTP status %d", method, status)
	}
	return fmt.Errorf("%s %s: unexpected status %d: %s", method, path, status, string(body))
}
