package jira

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
)

// Verification polling bounds. Package-level variables so tests can shorten
// them; defaults follow the repository's bounded-polling conventions.
var (
	screenTabFieldOrderVerifyTimeout  = 30 * time.Second
	screenTabFieldOrderVerifyInterval = 200 * time.Millisecond
)

// screenTabFieldOrderID builds the synthetic resource/data-source ID
// "{screenId}/{tabId}".
func screenTabFieldOrderID(screenID, tabID string) string {
	return screenID + "/" + tabID
}

// parseScreenTabFieldOrderID parses a "{screenId}/{tabId}" ID.
func parseScreenTabFieldOrderID(id string) (screenID, tabID string, err error) {
	parts := strings.SplitN(id, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.Contains(parts[1], "/") {
		return "", "", fmt.Errorf("expected ID in the format {screen_id}/{tab_id}, got %q", id)
	}
	return parts[0], parts[1], nil
}

// moveFieldRequest is the documented MoveFieldBean. Only the unambiguous
// named position "First" is used, applied in reverse desired order.
type moveFieldRequest struct {
	Position string `json:"position"`
}

// getScreenTabFieldIDs reads the current ordered field IDs of a screen tab
// from the documented GET endpoint. A documented 404 (missing screen or tab)
// is surfaced as (nil, 404, nil). Remote responses with empty or duplicate
// field IDs are rejected instead of being translated into corrupt state.
func getScreenTabFieldIDs(ctx context.Context, client *atlassian.Client, screenID, tabID string) ([]string, int, error) {
	apiPath := fmt.Sprintf("/rest/api/3/screens/%s/tabs/%s/fields",
		atlassian.PathEscape(screenID),
		atlassian.PathEscape(tabID),
	)

	var fields []screenTabFieldAPIResponse
	statusCode, err := client.GetWithStatus(ctx, apiPath, &fields)
	if err != nil {
		return nil, statusCode, fmt.Errorf("reading screen tab fields: %w", err)
	}
	if statusCode == http.StatusNotFound {
		return nil, statusCode, nil
	}
	if fields == nil {
		return nil, statusCode, fmt.Errorf("screen tab fields response is null; expected an array (use [] for an empty tab)")
	}

	ids := make([]string, 0, len(fields))
	seen := make(map[string]bool, len(fields))
	var duplicates []string
	for _, f := range fields {
		if f.ID == "" {
			return nil, statusCode, fmt.Errorf(
				"screen tab fields response contains an entry without an ID (name=%q); refusing to store a malformed remote order", f.Name)
		}
		if seen[f.ID] {
			duplicates = append(duplicates, f.ID)
			continue
		}
		seen[f.ID] = true
		ids = append(ids, f.ID)
	}
	if len(duplicates) > 0 {
		return nil, statusCode, fmt.Errorf(
			"screen tab fields response contains duplicate field IDs %v; refusing to store an ambiguous remote order", duplicates)
	}

	return ids, statusCode, nil
}

// screenTabFieldOrderMembershipError reports a mismatch between the configured
// field_ids and the tab's actual membership. It is returned before any move
// request is sent: this resource owns order only, never membership.
type screenTabFieldOrderMembershipError struct {
	ScreenID string
	TabID    string
	Missing  []string // configured but not on the tab
	Extra    []string // on the tab but not configured
}

func (e *screenTabFieldOrderMembershipError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "field_ids must list every field on screen %s tab %s exactly once, in the desired order. ",
		e.ScreenID, e.TabID)
	b.WriteString("This resource only orders existing tab fields; it never adds or removes them. ")
	if len(e.Missing) > 0 {
		fmt.Fprintf(&b, "Configured but not placed on the tab: %v. ", e.Missing)
	}
	if len(e.Extra) > 0 {
		fmt.Fprintf(&b, "Placed on the tab but not configured: %v. ", e.Extra)
	}
	b.WriteString("Add or remove atlassian_jira_screen_tab_field placement resources (and depends_on them), then apply again.")
	return b.String()
}

// screenTabFieldMembershipDiff returns configured IDs missing from the tab and
// tab members absent from the configuration. Order is ignored.
func screenTabFieldMembershipDiff(desired, actual []string) (missing, extra []string) {
	desiredSet := make(map[string]bool, len(desired))
	for _, id := range desired {
		desiredSet[id] = true
	}
	actualSet := make(map[string]bool, len(actual))
	for _, id := range actual {
		actualSet[id] = true
	}
	for _, id := range desired {
		if !actualSet[id] {
			missing = append(missing, id)
		}
	}
	for _, id := range actual {
		if !desiredSet[id] {
			extra = append(extra, id)
		}
	}
	return missing, extra
}

// screenTabFieldOrderEqual reports whether two field ID slices are equal in
// both contents and order.
func screenTabFieldOrderEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// waitForScreenTabFieldMembership tolerates stale reads after dependent
// placements are created or removed. No move is allowed until the complete
// membership matches; permanent mismatches are diagnosed after a bounded wait.
func waitForScreenTabFieldMembership(ctx context.Context, client *atlassian.Client, screenID, tabID string, desired []string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, screenTabFieldOrderVerifyTimeout)
	defer cancel()

	var lastMismatch error
	for {
		actual, statusCode, err := getScreenTabFieldIDs(ctx, client, screenID, tabID)
		if err != nil {
			if ctx.Err() != nil && lastMismatch != nil {
				return nil, fmt.Errorf("waiting for screen tab membership: %w (%v)", lastMismatch, ctx.Err())
			}
			return nil, err
		}
		if statusCode == http.StatusNotFound {
			lastMismatch = fmt.Errorf("screen %s or tab %s not found (HTTP 404); ensure the screen and tab exist and depend on their field placement resources", screenID, tabID)
		} else if missing, extra := screenTabFieldMembershipDiff(desired, actual); len(missing) > 0 || len(extra) > 0 {
			lastMismatch = &screenTabFieldOrderMembershipError{
				ScreenID: screenID,
				TabID:    tabID,
				Missing:  missing,
				Extra:    extra,
			}
		} else {
			return actual, nil
		}

		timer := time.NewTimer(screenTabFieldOrderVerifyInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("waiting for screen tab membership: %w (%v)", lastMismatch, ctx.Err())
		case <-timer.C:
		}
	}
}

// reconcileScreenTabFieldOrder enforces the desired field order on a screen
// tab using only documented public API operations:
//
//  1. Preflight GET of the tab's ordered fields, polling for placement
//     visibility. Malformed responses fail immediately; persistent membership
//     mismatches fail after a bounded wait, before any write.
//  2. If the remote order already matches, no writes are issued.
//  3. Otherwise every configured field is moved to "First", iterating the
//     desired list in reverse, which deterministically produces exactly the
//     desired sequence without relying on relative positions.
//  4. The observed order is re-read with bounded polling to tolerate Jira's
//     eventual consistency before the desired order is persisted to state.
//
// The function is idempotent, so re-running after a partial failure is safe.
func reconcileScreenTabFieldOrder(ctx context.Context, client *atlassian.Client, screenID, tabID string, desired []string) error {
	actual, err := waitForScreenTabFieldMembership(ctx, client, screenID, tabID, desired)
	if err != nil {
		return err
	}

	if screenTabFieldOrderEqual(actual, desired) {
		return nil
	}

	for i := len(desired) - 1; i >= 0; i-- {
		movePath := fmt.Sprintf("/rest/api/3/screens/%s/tabs/%s/fields/%s/move",
			atlassian.PathEscape(screenID),
			atlassian.PathEscape(tabID),
			atlassian.PathEscape(desired[i]),
		)
		if err := client.Post(ctx, movePath, moveFieldRequest{Position: "First"}, nil); err != nil {
			applied := len(desired) - 1 - i
			return fmt.Errorf(
				"moving field %q to the first position (after %d of %d moves): %w. The tab may be partially ordered; the apply is safe to re-run",
				desired[i], applied, len(desired), err)
		}
	}

	return verifyScreenTabFieldOrder(ctx, client, screenID, tabID, desired)
}

// verifyScreenTabFieldOrder re-reads the tab's field order until it matches
// the desired sequence or a bounded timeout elapses, following the
// repository's bounded eventual-consistency polling pattern.
func verifyScreenTabFieldOrder(ctx context.Context, client *atlassian.Client, screenID, tabID string, desired []string) error {
	ctx, cancel := context.WithTimeout(ctx, screenTabFieldOrderVerifyTimeout)
	defer cancel()

	for {
		actual, statusCode, err := getScreenTabFieldIDs(ctx, client, screenID, tabID)
		if err == nil && statusCode == http.StatusOK && screenTabFieldOrderEqual(actual, desired) {
			return nil
		}

		timer := time.NewTimer(screenTabFieldOrderVerifyInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
				return fmt.Errorf(
					"screen tab field order for screen %s tab %s could not be verified within %s (last read error: %v); state was not updated with the planned order",
					screenID, tabID, screenTabFieldOrderVerifyTimeout, err)
			}
			return fmt.Errorf(
				"screen tab field order for screen %s tab %s did not converge to the planned order within %s; the moves were accepted but not yet visible. state was not updated; apply again to re-verify",
				screenID, tabID, screenTabFieldOrderVerifyTimeout)
		case <-timer.C:
		}
	}
}
