package port

import "time"

// timeDeadline is an alias so the Linux and Darwin files can name the
// standard-library type without each importing it under a different idea.
type timeDeadline = time.Time
