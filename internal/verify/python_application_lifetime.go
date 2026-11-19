package verify

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// A changing lease in the read-only runtime mount carries owner liveness
// across both launchers and Docker. It contains no portable evidence. The
// runner and its watchdogs hold a shared lock until they exit; the owner
// takes it exclusively before releasing the invocation's private resources.
type pythonApplicationLifetime struct {
	cancel context.CancelFunc
	done   chan error
	lock   *os.File
}

func (s *PythonApplicationSession) invocationContext(ctx context.Context) (context.Context, func(), error) {
	s.lifetimeMu.Lock()
	defer s.lifetimeMu.Unlock()
	if s.closing {
		return nil, nil, fmt.Errorf("Python application session is closed")
	}
	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	return ctx, func() {
		cancel()
		s.lifetimeMu.Lock()
		s.cancel = nil
		s.lifetimeMu.Unlock()
	}, nil
}

func startPythonApplicationLifetime(ctx context.Context, runtime, output, execution string, cancel context.CancelFunc) (*pythonApplicationLifetime, error) {
	lock, err := os.OpenFile(filepath.Join(output, "lifetime.lock"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("starting Python application lifetime protection: %w", err)
	}
	lease := filepath.Join(runtime, "lifetime")
	next := lease + ".next"
	var sequence uint64
	write := func() error {
		sequence++
		if err := os.WriteFile(next, []byte(fmt.Sprintf("%s %d\n", execution, sequence)), 0o600); err != nil {
			return err
		}
		return os.Rename(next, lease)
	}
	if err := write(); err != nil {
		lock.Close()
		return nil, fmt.Errorf("starting Python application lifetime protection: %w", errors.Join(err, os.RemoveAll(next)))
	}
	ctx, stop := context.WithCancel(ctx)
	life := &pythonApplicationLifetime{cancel: stop, done: make(chan error, 1), lock: lock}
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		var err error
	loop:
		for {
			select {
			case <-ctx.Done():
				break loop
			case <-ticker.C:
				if err = write(); err != nil {
					cancel()
					break loop
				}
			}
		}
		remove := os.Remove(lease)
		if os.IsNotExist(remove) {
			remove = nil
		}
		life.done <- errors.Join(err, remove, os.RemoveAll(next))
	}()
	return life, nil
}

func (l *pythonApplicationLifetime) stop() error {
	l.cancel()
	err := <-l.done
	// Withdrawal normally stops the watchdogs on their next poll. A failed
	// withdrawal still expires after three seconds without a fresh lease.
	deadline := time.Now().Add(4 * time.Second)
	for {
		locked := syscall.Flock(int(l.lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if locked == nil {
			break
		}
		if !errors.Is(locked, syscall.EWOULDBLOCK) || time.Now().After(deadline) {
			err = errors.Join(err, fmt.Errorf("joining Python application lifetime protection: %w", locked))
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Native exit cleanup runs after Python's own finalizers. Its return
	// value cannot change the interpreter's status, so require both helpers'
	// completion bytes in the held file after joining them. Read that same
	// descriptor, never an application-replaceable path. Executors supplying
	// process results without launching Python never claim the lock.
	info, statErr := l.lock.Stat()
	err = errors.Join(err, statErr)
	if statErr == nil && info.Size() != 0 {
		var status [3]byte
		_, readErr := l.lock.ReadAt(status[:], 0)
		if readErr != nil || info.Size() != 3 || string(status[:]) != "S11" {
			err = errors.Join(err, fmt.Errorf("Python application lifetime watchdogs did not finish cleanly"), readErr)
		}
	}
	err = errors.Join(err, l.lock.Close())
	if err != nil {
		return fmt.Errorf("Python application lifetime cleanup: %w", err)
	}
	return nil
}
