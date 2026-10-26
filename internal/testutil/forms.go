package testutil

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	frameworkprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/provider"
)

const FormsTestCloudID = "33398233-27d5-48f6-9dea-d0ea33d97528"
const FormsTestPrefix = "/jira/forms/cloud/" + FormsTestCloudID

type formsTestTransport struct {
	target    *url.URL
	transport http.RoundTripper
}

func (t formsTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Fail closed: tests can never fall through to real Jira or any other host.
	if req.URL.Scheme != "https" || (req.URL.Host != "api.atlassian.com" && req.URL.Host != "forms-test.atlassian.net") {
		return nil, fmt.Errorf("unexpected test host")
	}
	clone := req.Clone(req.Context())
	u := *req.URL
	u.Scheme = t.target.Scheme
	u.Host = t.target.Host
	clone.URL = &u
	return t.transport.RoundTrip(clone)
}

// FormsClient exercises real discovery and fixed-host routing over local HTTP.
func FormsClient(t *testing.T, handler http.HandlerFunc) *atlassian.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Error("unexpected query parameters")
		}
		user, token, ok := r.BasicAuth()
		if !ok || user != "forms-test@example.org" || token != "forms-test-token" {
			t.Error("incorrect test authentication")
		}
		if r.Header.Get("User-Agent") != "terraform-provider-atlassian/test" || r.Header.Get("Accept") != "application/json" {
			t.Error("missing shared client headers")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/_edge/tenant_info" {
			if r.Method != "GET" || r.Host != "forms-test.atlassian.net" {
				t.Error("incorrect tenant discovery")
			}
			fmt.Fprintf(w, `{"cloudId":%q}`, FormsTestCloudID)
			return
		}
		if r.Host != "api.atlassian.com" {
			t.Error("Forms credentials routed to unexpected host")
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	client, err := atlassian.NewClient(atlassian.ClientConfig{URL: "https://forms-test.atlassian.net", User: "forms-test@example.org", Token: "forms-test-token", Version: "test", HTTPClient: &http.Client{Transport: formsTestTransport{target: target, transport: server.Client().Transport}}})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

type configuredTestProvider struct {
	frameworkprovider.Provider
	client *atlassian.Client
}

func (p *configuredTestProvider) Configure(_ context.Context, _ frameworkprovider.ConfigureRequest, resp *frameworkprovider.ConfigureResponse) {
	resp.ResourceData = p.client
	resp.DataSourceData = p.client
}

// ProtoV6ProviderFactoriesWithClient retains the actual provider registration
// and schema, injecting only the client so gateway tests stay mock-backed.
func ProtoV6ProviderFactoriesWithClient(client *atlassian.Client) map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){"atlassian": providerserver.NewProtocol6WithError(&configuredTestProvider{Provider: provider.New("test")(), client: client})}
}
