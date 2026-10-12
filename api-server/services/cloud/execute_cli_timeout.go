package cloud

import (
	"context"
	"errors"
	"net"
	"time"
)

// executeCliTimeout is how long one /execute_cli call may take before the client
// gives up. A var so tests can shorten it.
var executeCliTimeout = 60 * time.Second

// isClientTimeout reports whether err is the client giving up because the
// collector did not answer in time. Such a command already used its whole time
// limit, so running it again would only spend that time again for the same
// result; a quick connection failure, by contrast, is worth retrying.
func isClientTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
