package atlassian

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testFormsCloudID = "33398233-27d5-48f6-9dea-d0ea33d97528"

type formsRoundTrip func(*http.Request) (*http.Response, error)

func (f formsRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func formsClientTest(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	hc := &http.Client{Transport: formsRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "tenant.atlassian.net" && r.URL.Host != "api.atlassian.com" {
			t.Error("unexpected credential destination")
			return nil, fmt.Errorf("blocked test host")
		}
		user, token, ok := r.BasicAuth()
		if !ok || user != "forms@example.org" || token != "secret-token" {
			t.Error("authentication missing")
		}
		if r.Header.Get("User-Agent") != "terraform-provider-atlassian/test" {
			t.Error("user agent missing")
		}
		if r.URL.RawQuery != "" {
			t.Error("unexpected query")
		}
		clone := r.Clone(r.Context())
		u := *r.URL
		u.Scheme = target.Scheme
		u.Host = target.Host
		clone.URL = &u
		return server.Client().Transport.RoundTrip(clone)
	})}
	c, err := NewClient(ClientConfig{URL: "https://tenant.atlassian.net", User: "forms@example.org", Token: "secret-token", Version: "test", HTTPClient: hc})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func TestFormsDiscoveryCacheAndIsolation(t *testing.T) {
	var count atomic.Int32
	c := formsClientTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_edge/tenant_info" || r.Host != "tenant.atlassian.net" || r.Method != "GET" {
			t.Errorf("unexpected discovery %s %s", r.Method, r.URL)
		}
		count.Add(1)
		fmt.Fprintf(w, `{"cloudId":%q}`, testFormsCloudID)
	})
	for range 2 {
		f, err := c.Forms(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if f.BaseURL() != "https://api.atlassian.com/jira/forms/cloud/"+testFormsCloudID {
			t.Fatal("wrong gateway")
		}
	}
	if count.Load() != 1 || c.BaseURL() != "https://tenant.atlassian.net" {
		t.Fatal("discovery not cached or original client mutated")
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Forms(t.Context()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}
func TestFormsDiscoveryFailureNotCached(t *testing.T) {
	for _, body := range []string{`{"cloudId":"../evil"}`, `{"cloudId":"https://evil.test"}`, `{}`, `invalid`} {
		t.Run(body, func(t *testing.T) {
			var n atomic.Int32
			c := formsClientTest(t, func(w http.ResponseWriter, r *http.Request) {
				if n.Add(1) == 1 {
					_, _ = io.WriteString(w, body)
					return
				}
				fmt.Fprintf(w, `{"cloudId":%q}`, testFormsCloudID)
			})
			if _, err := c.Forms(t.Context()); err == nil {
				t.Fatal("invalid discovery accepted")
			}
			if _, err := c.Forms(t.Context()); err != nil {
				t.Fatal(err)
			}
			if n.Load() != 2 {
				t.Fatal("failed discovery cached")
			}
		})
	}
	c := formsClientTest(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprintf(w, `{"cloudId":%q}`, testFormsCloudID) })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.Forms(ctx); err == nil {
		t.Fatal("canceled discovery succeeded")
	}
	if _, err := c.Forms(t.Context()); err != nil {
		t.Fatal("cancellation cached", err)
	}
}
func TestFormsRoutingAndNoPostRetries(t *testing.T) {
	for _, status := range []int{200, 400, 401, 403, 404, 412, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var posts atomic.Int32
			c := formsClientTest(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/_edge/tenant_info" {
					fmt.Fprintf(w, `{"cloudId":%q}`, testFormsCloudID)
					return
				}
				if r.Method != "POST" || r.Host != "api.atlassian.com" || r.URL.Path != "/jira/forms/cloud/"+testFormsCloudID+"/project/JML/form" {
					t.Error("incorrect Forms route")
				}
				if r.Header.Get("Content-Type") != "application/json" {
					t.Error("missing content type")
				}
				b, _ := io.ReadAll(r.Body)
				if string(b) != `{"design":{}}` {
					t.Error("body not buffered correctly")
				}
				posts.Add(1)
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"echo":"secret-token"}`)
			})
			f, err := c.Forms(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			err = f.Post(t.Context(), "/project/JML/form", map[string]any{"design": map[string]any{}}, nil)
			if (err != nil) != (status != 200) {
				t.Fatal(err)
			}
			if err != nil && strings.Contains(err.Error(), "secret-token") {
				t.Fatal("credentials leaked")
			}
			if posts.Load() != 1 {
				t.Fatal("ambiguous POST replayed")
			}
			for _, path := range []string{"https://evil.test/", "//evil.test/", "/project/../evil", "/project/%2e%2e/evil", "/project/X/form?url=https://evil.test", "/project/X/form#fragment", "/project/X\\evil/form"} {
				if _, err := f.Do(t.Context(), "GET", path, nil); err == nil {
					t.Errorf("unsafe route accepted: %s", path)
				}
			}
		})
	}
}
func TestFormsRejectRedirects(t *testing.T) {
	for _, discovery := range []bool{true, false} {
		t.Run(fmt.Sprint(discovery), func(t *testing.T) {
			c := formsClientTest(t, func(w http.ResponseWriter, r *http.Request) {
				if !discovery && r.URL.Path == "/_edge/tenant_info" {
					fmt.Fprintf(w, `{"cloudId":%q}`, testFormsCloudID)
					return
				}
				// Even same-domain redirects must not carry credentials elsewhere.
				http.Redirect(w, r, "https://api.atlassian.com/elsewhere", http.StatusTemporaryRedirect)
			})
			f, err := c.Forms(t.Context())
			if discovery {
				if err == nil {
					t.Fatal("discovery redirect accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = f.Post(t.Context(), "/project/JML/form", map[string]any{}, nil); err == nil {
				t.Fatal("Forms redirect accepted")
			}
		})
	}
}
func TestFormsReadAndPutReuseRetries(t *testing.T) {
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		t.Run(method, func(t *testing.T) {
			var calls atomic.Int32
			c := formsClientTest(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/_edge/tenant_info" {
					fmt.Fprintf(w, `{"cloudId":%q}`, testFormsCloudID)
					return
				}
				if r.Method != method {
					t.Error("wrong method")
				}
				body, _ := io.ReadAll(r.Body)
				if method == "PUT" && string(body) != `{"design":{}}` {
					t.Error("retry lost body")
				}
				if calls.Add(1) == 1 {
					w.Header().Set("Retry-After", "1")
					w.WriteHeader(429)
					return
				}
				_, _ = io.WriteString(w, `{}`)
			})
			f, err := c.Forms(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			var body []byte
			if method == "PUT" {
				body = []byte(`{"design":{}}`)
			}
			resp, err := f.Do(ctx, method, "/project/JML/form", body)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if calls.Load() != 2 {
				t.Fatal("shared retry policy not used")
			}
		})
	}
}
func TestFormsDiscoveryRejectsPlainHTTPBeforeRequest(t *testing.T) {
	var calls atomic.Int32
	client := formsClientTest(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprintf(w, `{"cloudId":%q}`, testFormsCloudID)
	})
	client.baseURL = "http://tenant.atlassian.net"
	if _, err := client.Forms(t.Context()); err == nil {
		t.Fatal("HTTP tenant origin accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("credentials sent before rejecting HTTP origin")
	}
}

func TestFormsBadTenantOrigins(t *testing.T) {
	for _, origin := range []string{"https://name:secret@tenant.atlassian.net", "https://tenant.atlassian.net/path", "https://tenant.atlassian.net?query=secret", "https://tenant.atlassian.net#fragment", "ftp://tenant.atlassian.net"} {
		c, err := NewClient(ClientConfig{URL: origin, User: "test", Token: "test"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Forms(t.Context()); err == nil {
			t.Errorf("invalid tenant origin accepted")
		}
	}
}
