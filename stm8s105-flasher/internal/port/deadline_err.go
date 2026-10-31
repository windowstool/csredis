package port

import "os"

// osDeadline is the error Read returns when the deadline expires.
var osDeadline = os.ErrDeadlineExceeded
