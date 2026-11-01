// Package clock is the two clocks the Python code used, in seconds,
// and a fake of both for tests.
//
// Wall is time.time(): it jumps when someone sets the system clock.
// Mono is time.monotonic(): it never goes back. Python's start values
// for "long ago" (0.0 and -EVERY against an uptime-sized clock) are
// math.Inf(-1) in Go, so they hold from the first second.
package clock

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Clock is the two clocks the Python code used, in seconds.
type Clock interface {
	Wall() float64 // time.time(): jumps with the system clock
	Mono() float64 // time.monotonic(): never goes back
	Sleep(ctx context.Context, seconds float64) error
	After(seconds float64) <-chan struct{}
}

// Real is the computer's clocks.
func Real() Clock { return real{} }

var start = time.Now()

type real struct{}

func (real) Wall() float64 {
	return float64(time.Now().UnixNano()) / 1e9
}

// Mono counts from when the program started; only differences mean
// anything, as with time.monotonic().
func (real) Mono() float64 { return time.Since(start).Seconds() }

func (real) Sleep(ctx context.Context, seconds float64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if seconds <= 0 {
		return nil
	}
	t := time.NewTimer(dur(seconds))
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (real) After(seconds float64) <-chan struct{} {
	ch := make(chan struct{})
	if seconds <= 0 {
		close(ch)
		return ch
	}
	time.AfterFunc(dur(seconds), func() { close(ch) })
	return ch
}

func dur(seconds float64) time.Duration {
	if seconds > 1e9 {
		return time.Duration(1e9) * time.Second
	}
	return time.Duration(seconds * float64(time.Second))
}

// Fake is a clock that moves only when told. It is safe to use from
// many goroutines.
type Fake struct {
	mu      sync.Mutex
	wall    float64
	mono    float64
	waiters []waiter
}

type waiter struct {
	at float64 // on the monotonic clock
	ch chan struct{}
}

// NewFake starts a fake clock at these readings.
func NewFake(wall, mono float64) *Fake {
	return &Fake{wall: wall, mono: mono}
}

// Wall is the fake wall clock.
func (f *Fake) Wall() float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.wall
}

// Mono is the fake monotonic clock.
func (f *Fake) Mono() float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mono
}

// Advance moves both clocks on, and wakes every sleeper whose time
// has come, earliest first.
func (f *Fake) Advance(seconds float64) {
	f.mu.Lock()
	f.wall += seconds
	f.mono += seconds
	var due, rest []waiter
	for _, w := range f.waiters {
		if w.at <= f.mono {
			due = append(due, w)
		} else {
			rest = append(rest, w)
		}
	}
	f.waiters = rest
	f.mu.Unlock()
	sort.SliceStable(due, func(i, j int) bool {
		return due[i].at < due[j].at
	})
	for _, w := range due {
		close(w.ch)
	}
}

// SetWall sets the wall clock only, as when someone sets the system
// clock. Sleepers go by the monotonic clock, so none wakes.
func (f *Fake) SetWall(wall float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.wall = wall
}

// Waiting is how many Sleep and After calls wait for Advance now.
// Tests use it to know a goroutine has reached its sleep.
func (f *Fake) Waiting() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.waiters)
}

// After gives a channel that closes once the fake monotonic clock has
// moved on by seconds.
func (f *Fake) After(seconds float64) <-chan struct{} {
	ch := make(chan struct{})
	f.mu.Lock()
	defer f.mu.Unlock()
	if seconds <= 0 {
		close(ch)
		return ch
	}
	f.waiters = append(f.waiters, waiter{at: f.mono + seconds, ch: ch})
	return ch
}

// Sleep waits until Advance has moved the clock on by seconds, or
// ctx ends.
func (f *Fake) Sleep(ctx context.Context, seconds float64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if seconds <= 0 {
		return nil
	}
	ch := f.After(seconds)
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		f.drop(ch)
		return ctx.Err()
	}
}

func (f *Fake) drop(ch <-chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, w := range f.waiters {
		if w.ch == ch {
			f.waiters = append(f.waiters[:i], f.waiters[i+1:]...)
			return
		}
	}
}
