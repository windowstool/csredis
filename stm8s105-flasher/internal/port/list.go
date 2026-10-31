package port

// List returns serial port names the operating system currently exposes.
func List() ([]string, error) { return list() }
