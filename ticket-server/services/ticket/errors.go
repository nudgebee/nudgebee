package ticket

import "errors"

// ErrNotSupported indicates the operation is not supported by this platform.
var ErrNotSupported = errors.New("operation not supported by this platform")
