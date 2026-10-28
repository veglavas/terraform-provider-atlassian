package jira

import "time"

// SetScreenTabFieldOrderVerifySettings overrides the bounded verification
// polling bounds and returns a restore function. It exists so tests in the
// jira_test package can exercise convergence and timeout behavior quickly.
func SetScreenTabFieldOrderVerifySettings(timeout, interval time.Duration) (restore func()) {
	origTimeout := screenTabFieldOrderVerifyTimeout
	origInterval := screenTabFieldOrderVerifyInterval

	screenTabFieldOrderVerifyTimeout = timeout
	screenTabFieldOrderVerifyInterval = interval

	return func() {
		screenTabFieldOrderVerifyTimeout = origTimeout
		screenTabFieldOrderVerifyInterval = origInterval
	}
}
