package atlassian

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// PostAsync submits a Jira mutation without following its 303 task redirect.
// It returns the task ID (empty for synchronous success). Redirect destinations
// are never requested here, preventing credentials from reaching another host.
func (c *Client) PostAsync(ctx context.Context, path string, body any) (string, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	copyClient := *c
	transportClient := *c.httpClient
	transportClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	copyClient.httpClient = &transportClient
	resp, err := copyClient.Do(ctx, http.MethodPost, path, payload)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusSeeOther && (resp.StatusCode < 200 || resp.StatusCode >= 300) {
		return "", fmt.Errorf("POST %s: unexpected status %d: %s", path, resp.StatusCode, data)
	}
	var task struct {
		ID     string `json:"id"`
		TaskID string `json:"taskId"`
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &task); err != nil {
			return "", fmt.Errorf("decoding mutation response: %w", err)
		}
	}
	if task.TaskID != "" {
		return task.TaskID, nil
	}
	if resp.StatusCode != http.StatusSeeOther {
		return "", nil
	}
	if task.ID != "" {
		return task.ID, nil
	}
	location, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		return "", err
	}
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return "", err
	}
	if location.IsAbs() && (location.Scheme != base.Scheme || location.Host != base.Host) {
		return "", fmt.Errorf("untrusted task redirect %q", location.Host)
	}
	prefix := "/rest/api/3/task/"
	if !strings.HasPrefix(location.Path, prefix) {
		return "", fmt.Errorf("async response missing task ID")
	}
	id := strings.TrimPrefix(location.Path, prefix)
	if id == "" || strings.Contains(id, "/") {
		return "", fmt.Errorf("invalid task redirect")
	}
	return id, nil
}
