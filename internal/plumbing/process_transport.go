package plumbing

import "context"

// RepositoryProcessTransport is an optional host capability. It owns private
// helpers and every prepared or started process until Close, including handles
// returned alongside errors. It interprets none of the three output channels.
type RepositoryProcessTransport interface {
	RepositoryAcceptanceFileEnvironment
	Stage(context.Context, string, []byte) (string, error)
	Prepare(context.Context, RepositoryProcessSpec) (RepositoryPreparedProcess, error)
}

type RepositoryProcessSpec struct {
	Argv     []string
	Env      map[string]string
	EventEnv string
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

// An ordinary nonzero exit is output, not a transport error. Drained means all
// three channels reached EOF; forcibly closing a reader cannot assert it.
type RepositoryProcessOutput struct {
	Events, Stdout, Stderr   []byte
	ExitCode                 int
	Started, Exited, Drained bool
}
