package plumbing

import "context"

// RepositoryProcessTransport is an optional host capability. It moves opaque
// bytes, not acceptance evidence, and owns every handle until Close.
type RepositoryProcessTransport interface {
	RepositoryAcceptanceFileEnvironment
	Stage(context.Context, string, []byte) (string, error)
	Prepare(context.Context, RepositoryProcessSpec) (RepositoryPreparedProcess, error)
}

type RepositoryProcessSpec struct {
	Argv     []string
	Env      map[string]string
	EventEnv string
	// Directory is a clean part-relative working directory; empty means the
	// owning part. Go test binaries run in their package directory.
	Directory string
}

type RepositoryPreparedProcess interface {
	Start(context.Context) (RepositoryProcess, error)
	Close(context.Context) error
}

type RepositoryProcess interface {
	Wait(context.Context) (RepositoryProcessOutput, error)
	Snapshot() RepositoryProcessOutput
	Close(context.Context) error
}

type RepositoryProcessOutput struct {
	Events, Stdout, Stderr   []byte
	ExitCode                 int
	Started, Exited, Drained bool
}
