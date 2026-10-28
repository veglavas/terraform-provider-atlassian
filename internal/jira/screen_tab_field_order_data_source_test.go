package jira_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/testutil"
)

func TestAccScreenTabFieldOrderDataSource_basic(t *testing.T) {
	mock := newScreenTabFieldOrderServer(t, []string{"summary", "customfield_10001", "customfield_10002"})
	url := mock.start()
	setupScreenTabFieldOrderEnv(t, url)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactories,
		CheckDestroy:             func(*terraform.State) error { return nil },
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
data "atlassian_jira_screen_tab_field_order" "d" {
  screen_id = %q
  tab_id    = %q
}`, orderTestScreenID, orderTestTabID),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.atlassian_jira_screen_tab_field_order.d", "id", orderTestKey),
					resource.TestCheckResourceAttr("data.atlassian_jira_screen_tab_field_order.d", "field_ids.#", "3"),
					// The remote order must be reported verbatim, first to last.
					resource.TestCheckResourceAttr("data.atlassian_jira_screen_tab_field_order.d", "field_ids.0", "summary"),
					resource.TestCheckResourceAttr("data.atlassian_jira_screen_tab_field_order.d", "field_ids.1", "customfield_10001"),
					resource.TestCheckResourceAttr("data.atlassian_jira_screen_tab_field_order.d", "field_ids.2", "customfield_10002"),
				),
			},
		},
	})
}

func TestAccScreenTabFieldOrderDataSource_emptyTab(t *testing.T) {
	mock := newScreenTabFieldOrderServer(t, nil)
	url := mock.start()
	setupScreenTabFieldOrderEnv(t, url)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactories,
		CheckDestroy:             func(*terraform.State) error { return nil },
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
data "atlassian_jira_screen_tab_field_order" "d" {
  screen_id = %q
  tab_id    = %q
}`, orderTestScreenID, orderTestTabID),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.atlassian_jira_screen_tab_field_order.d", "field_ids.#", "0"),
				),
			},
		},
	})
}

func TestAccScreenTabFieldOrderDataSource_notFound(t *testing.T) {
	mock := newScreenTabFieldOrderServer(t, nil)
	// No tab exists under screen 99999.
	mock.mu.Lock()
	delete(mock.tabs, orderTestScreenID)
	mock.mu.Unlock()
	url := mock.start()
	setupScreenTabFieldOrderEnv(t, url)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactories,
		CheckDestroy:             func(*terraform.State) error { return nil },
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
data "atlassian_jira_screen_tab_field_order" "d" {
  screen_id = %q
  tab_id    = %q
}`, orderTestScreenID, orderTestTabID),
				// A missing screen or tab is a diagnostic, not a removal.
				ExpectError: regexp.MustCompile("not found"),
			},
		},
	})
}

func TestAccScreenTabFieldOrderDataSource_malformedRemote(t *testing.T) {
	mock := newScreenTabFieldOrderServer(t, nil)
	mock.mu.Lock()
	mock.getRawBody = func(int) string {
		return `[{"id":"summary","name":"Summary"},{"id":"summary","name":"Summary"}]`
	}
	mock.mu.Unlock()
	url := mock.start()
	setupScreenTabFieldOrderEnv(t, url)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactories,
		CheckDestroy:             func(*terraform.State) error { return nil },
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
data "atlassian_jira_screen_tab_field_order" "d" {
  screen_id = %q
  tab_id    = %q
}`, orderTestScreenID, orderTestTabID),
				ExpectError: regexp.MustCompile("refusing"),
			},
		},
	})
}

func TestAccScreenTabFieldOrderDataSource_validation(t *testing.T) {
	mock := newScreenTabFieldOrderServer(t, []string{"summary"})
	url := mock.start()
	setupScreenTabFieldOrderEnv(t, url)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactories,
		CheckDestroy:             func(*terraform.State) error { return nil },
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
data "atlassian_jira_screen_tab_field_order" "d" {
  screen_id = ""
  tab_id    = %q
}`, orderTestTabID),
				ExpectError: regexp.MustCompile("length must be at least 1"),
			},
		},
	})
}
