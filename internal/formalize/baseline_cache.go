package formalize

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"sync"

	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

// CheckSession owns a drafting checker's authority and successful baseline
// work. Keep it only for that session; final checking uses CheckWithContext.
// Neither caller-supplied artifacts nor returned evidence can seed its memo.
type CheckSession struct {
	authority CheckContext
	baseline  baselineMemo
}

func NewCheckSession(c CheckContext) *CheckSession {
	c.Baseline = detachedSnapshot(c.Baseline)
	if c.Baseline != nil {
		c.Baseline.Chart, c.Baseline.Limitation = nil, nil
	}
	c.Choices = maps.Clone(c.Choices)
	c.Revisions = slices.Clone(c.Revisions)
	return &CheckSession{authority: c}
}

func (s *CheckSession) Check(ctx context.Context, ws string, tc toolchain.Toolchain) (*Proposal, *verify.Report, error) {
	return checkWithContext(ctx, ws, tc, s.authority, &s.baseline)
}

type baselineEntry struct {
	done     chan struct{}
	snapshot *statechart.ComparisonSnapshot
}

type baselineMemo struct {
	mu      sync.Mutex
	entries map[[sha256.Size]byte]*baselineEntry
}

// The model gate derives its TLC configuration from the complete lock. The
// remaining runner settings are fixed in this process, except for these
// tool and container inputs. Hash the jar's bytes, not just its path or pin.
func baselineKey(s *statechart.ComparisonSnapshot, tc toolchain.Toolchain) ([sha256.Size]byte, error) {
	var zero [sha256.Size]byte
	f, err := os.Open(tc.TLCJar)
	if err != nil {
		return zero, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return zero, err
	}
	cpus, err := toolchain.ContainerCPUs()
	if err != nil {
		return zero, err
	}
	source := *s
	source.Chart, source.Limitation = nil, nil
	b, err := json.Marshal(struct {
		Source                        statechart.ComparisonSnapshot
		Image, Jar, JarSHA, Release   string
		CPUs, Platform, Host, Context string
	}{source, tc.JavaImage, tc.TLCJar, fmt.Sprintf("%x", h.Sum(nil)), toolchain.TLCRelease,
		cpus, os.Getenv("DOCKER_DEFAULT_PLATFORM"), os.Getenv("DOCKER_HOST"), os.Getenv("DOCKER_CONTEXT")})
	if err != nil {
		return zero, err
	}
	return sha256.Sum256(b), nil
}

func (m *baselineMemo) check(ctx context.Context, s *statechart.ComparisonSnapshot, tc toolchain.Toolchain) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m == nil {
		return checkBaseline(ctx, s, tc)
	}
	for {
		key, err := baselineKey(s, tc)
		if err != nil {
			return err
		}
		m.mu.Lock()
		if err := ctx.Err(); err != nil {
			m.mu.Unlock()
			return err
		}
		if entry := m.entries[key]; entry != nil {
			m.mu.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-entry.done:
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.snapshot == nil {
				// Failed or canceled work belongs to its caller. A waiter
				// retries with its own context instead of inheriting failure.
				continue
			}
			*s = *detachedSnapshot(entry.snapshot)
			return ctx.Err()
		}
		entry := &baselineEntry{done: make(chan struct{})}
		if m.entries == nil {
			m.entries = map[[sha256.Size]byte]*baselineEntry{}
		}
		m.entries[key] = entry
		m.mu.Unlock()

		err = checkBaseline(ctx, s, tc)
		if err == nil {
			var checkedKey [sha256.Size]byte
			checkedKey, err = baselineKey(s, tc)
			if err == nil && checkedKey != key {
				err = fmt.Errorf("baseline tool inputs changed during validation; check again")
			}
		}
		m.mu.Lock()
		if err == nil {
			err = ctx.Err()
		}
		if err == nil {
			entry.snapshot = detachedSnapshot(s)
		} else {
			delete(m.entries, key)
		}
		close(entry.done)
		m.mu.Unlock()
		return err
	}
}
