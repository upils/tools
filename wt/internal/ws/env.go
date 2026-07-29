package ws

import "os"

// inheritEnv is a seam so tests can pin the child environment.
var inheritEnv = func() []string { return os.Environ() }
