package plumbing

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type checkStatusKey struct{}

// WithCheckStatus reports when a build waits for checks and when it is admitted.
// A wait that ends with the build stays visible until the build itself ends.
func WithCheckStatus(ctx context.Context, status func(waiting bool)) context.Context {
	return context.WithValue(ctx, checkStatusKey{}, status)
}

// CheckQueue belongs to one watcher. All its plumbing builds share the slots,
// including test calls from separate MCP processes. The watcher owns execution
// so a child that exits cannot release a slot before sandbox cleanup finishes.
type CheckQueue struct {
	listener net.Listener
	dir      string
	slots    chan struct{}
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	builds   map[string]*checkBuild
	wg       sync.WaitGroup
	close    sync.Once
	closeErr error
}

// NewCheckQueue starts a private local endpoint with limit simultaneous checks.
func NewCheckQueue(limit int) (*CheckQueue, error) {
	if limit < 1 {
		return nil, fmt.Errorf("the check limit must be positive")
	}
	dir, err := os.MkdirTemp("", "inv-check-")
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("unix", filepath.Join(dir, "queue"))
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	q := &CheckQueue{listener: listener, dir: dir, slots: make(chan struct{}, limit), ctx: ctx, cancel: cancel, builds: map[string]*checkBuild{}}
	q.wg.Add(1)
	go func() {
		defer q.wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			q.wg.Add(1)
			go func() {
				defer q.wg.Done()
				q.serve(conn)
			}()
		}
	}()
	return q, nil
}

// Close cancels pending and active checks and waits for their cleanup.
func (q *CheckQueue) Close() error {
	q.close.Do(func() {
		q.cancel()
		q.listener.Close()
		q.wg.Wait()
		q.closeErr = os.RemoveAll(q.dir)
	})
	return q.closeErr
}

type checkBuild struct {
	queue   *CheckQueue
	id      string
	ctx     context.Context
	budget  context.Context
	cancel  context.CancelFunc
	stop    func() bool
	sandbox Sandbox
	plan    *Plan // nil for planning; both modes may retain sandbox.Original
	wg      sync.WaitGroup
	mu      sync.Mutex
	waiting int
	expired bool
	status  func(bool)
}

func (q *CheckQueue) register(ctx context.Context, sandbox Sandbox, plan *Plan) (*checkBuild, error) {
	if plan != nil {
		data, err := json.Marshal(plan)
		if err != nil {
			return nil, err
		}
		var captured Plan
		if err := json.Unmarshal(data, &captured); err != nil {
			return nil, err
		}
		plan = &captured
	}
	if sandbox.Repository != nil {
		captured := *sandbox.Repository
		captured.Adapters = maps.Clone(captured.Adapters)
		sandbox.Repository = &captured
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.ctx.Err(); err != nil {
		return nil, fmt.Errorf("the check queue is unavailable: %w", err)
	}
	budget := ctx
	ctx, cancel := context.WithCancel(ctx)
	b := &checkBuild{queue: q, id: rand.Text(), ctx: ctx, budget: budget, cancel: cancel, sandbox: sandbox, plan: plan}
	b.sandbox.Checks = nil
	b.status, _ = ctx.Value(checkStatusKey{}).(func(bool))
	b.stop = context.AfterFunc(q.ctx, cancel)
	q.builds[b.id] = b
	return b, nil
}

// RegisterPlan gives the planning tool an endpoint whose owner retains the
// original repository and captured snapshot. Requests carry only a workspace.
func (q *CheckQueue) RegisterPlan(ctx context.Context, sandbox Sandbox, repo string) (*CheckClient, func(), error) {
	if repo == "" || sandbox.Repository == nil || sandbox.Repository.Snapshot == nil {
		return nil, nil, errors.New("configured planning needs a repository and captured snapshot")
	}
	sandbox.Original = repo
	b, err := q.register(ctx, sandbox, nil)
	if err != nil {
		return nil, nil, err
	}
	return &CheckClient{Address: q.listener.Addr().String(), Build: b.id}, func() { b.finish() }, nil
}

func (b *checkBuild) finish() bool {
	if b == nil {
		return false
	}
	b.queue.mu.Lock()
	delete(b.queue.builds, b.id)
	b.queue.mu.Unlock()
	b.mu.Lock()
	b.expired = b.expired || b.waiting > 0 && errors.Is(b.budget.Err(), context.DeadlineExceeded)
	b.mu.Unlock()
	b.cancel()
	b.stop()
	b.wg.Wait()
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.expired
}

func (b *checkBuild) wait(start bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if start {
		b.waiting++
		if b.waiting == 1 && b.status != nil {
			b.status(true)
		}
		return
	}
	b.waiting--
	if b.ctx.Err() != nil || b.budget.Err() != nil {
		b.expired = b.expired || errors.Is(b.budget.Err(), context.DeadlineExceeded)
		return
	}
	if b.waiting == 0 && b.status != nil {
		b.status(false)
	}
}

func (b *checkBuild) run(ctx context.Context, root string) (Result, error) {
	if b.plan == nil {
		return Result{}, errors.New("a planning registration cannot run a build")
	}
	if err := b.admit(ctx); err != nil {
		return Result{}, err
	}
	defer func() { <-b.queue.slots }()
	return b.sandbox.Run(ctx, root, b.plan)
}

func (b *checkBuild) check(ctx context.Context, ws string) (*Checked, error) {
	if b.plan != nil {
		return nil, errors.New("a build registration cannot replace its plan")
	}
	if err := b.admit(ctx); err != nil {
		return nil, err
	}
	defer func() { <-b.queue.slots }()
	return Check(ctx, ws, b.sandbox.Original, b.sandbox)
}

func (b *checkBuild) admit(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case b.queue.slots <- struct{}{}:
	default:
		b.wait(true)
		select {
		case b.queue.slots <- struct{}{}:
			b.wait(false)
		case <-ctx.Done():
			b.wait(false)
			return fmt.Errorf("waiting for checks: %w", ctx.Err())
		}
	}
	if err := ctx.Err(); err != nil {
		<-b.queue.slots
		return err
	}
	return nil
}

// CheckClient identifies a registered build on the watcher's queue. A missing
// endpoint or expired registration fails the check; it never runs locally.
type CheckClient struct {
	Address string
	Build   string
}

type checkRequest struct {
	Build     string
	Root      string
	Workspace string
}

type checkResponse struct {
	Result  Result
	Checked *Checked
	Error   string
	Cause   string
}

func (q *CheckQueue) serve(conn net.Conn) {
	defer conn.Close()
	stop := context.AfterFunc(q.ctx, func() { conn.Close() })
	defer stop()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var request checkRequest
	if err := json.NewDecoder(conn).Decode(&request); err != nil {
		return
	}
	conn.SetReadDeadline(time.Time{})
	q.mu.Lock()
	b := q.builds[request.Build]
	if b != nil {
		b.wg.Add(1)
	}
	q.mu.Unlock()
	var response checkResponse
	if b == nil {
		response.Error = "the check queue has no active build for this call"
	} else {
		defer b.wg.Done()
		ctx, cancel := context.WithCancel(b.ctx)
		defer cancel()
		// Losing the MCP process cancels its request, while the watcher keeps
		// the slot until Sandbox.Run has removed the container and its copy.
		gone := make(chan struct{})
		go func() {
			io.Copy(io.Discard, conn)
			cancel()
			close(gone)
		}()
		defer func() { conn.Close(); <-gone }()
		var err error
		if request.Workspace != "" {
			response.Checked, err = b.check(ctx, request.Workspace)
		} else {
			response.Result, err = b.run(ctx, request.Root)
		}
		if err != nil {
			response.Error = err.Error()
			if errors.Is(err, context.DeadlineExceeded) {
				response.Cause = "deadline"
			} else if errors.Is(err, context.Canceled) {
				response.Cause = "canceled"
			}
		}
	}
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	json.NewEncoder(conn).Encode(response)
}

func (c *CheckClient) run(ctx context.Context, root string) (Result, error) {
	response, err := c.call(ctx, checkRequest{Build: c.Build, Root: root})
	return response.Result, err
}

func (c *CheckClient) check(ctx context.Context, ws string) (*Checked, error) {
	response, err := c.call(ctx, checkRequest{Build: c.Build, Workspace: ws})
	if err == nil && response.Checked == nil {
		err = errors.New("the check queue returned no planning evidence")
	}
	return response.Checked, err
}

func (c *CheckClient) call(ctx context.Context, request checkRequest) (checkResponse, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", c.Address)
	if err != nil {
		return checkResponse{}, fmt.Errorf("the check queue is unavailable: %w", err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return checkResponse{}, fmt.Errorf("requesting checks: %w", err)
	}
	var response checkResponse
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		if ctx.Err() != nil {
			return checkResponse{}, ctx.Err()
		}
		return checkResponse{}, fmt.Errorf("the check queue stopped: %w", err)
	}
	if response.Error != "" {
		err := errors.New(response.Error)
		switch response.Cause {
		case "deadline":
			err = fmt.Errorf("%s: %w", response.Error, context.DeadlineExceeded)
		case "canceled":
			err = fmt.Errorf("%s: %w", response.Error, context.Canceled)
		}
		return response, err
	}
	return response, nil
}
