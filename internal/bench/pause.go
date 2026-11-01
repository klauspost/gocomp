package bench

import (
	"context"
	"sync"
	"time"
)

// Pause holds an Engine between passes, never inside a timed region, so a
// paused run measures exactly what an unpaused one would. A nil *Pause never
// holds.
type Pause struct {
	mu     sync.Mutex
	resume chan struct{} // non-nil while paused; closed to resume
	from   time.Time     // when the current hold began, zero when not holding
	held   time.Duration // finished holds
}

// Set pauses or resumes. A pass already under way runs to completion first.
func (p *Pause) Set(on bool) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case on && p.resume == nil:
		p.resume = make(chan struct{})
	case !on && p.resume != nil:
		close(p.resume)
		p.resume = nil
	}
}

func (p *Pause) Paused() bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.resume != nil
}

// Held reports whether the run has actually stopped, rather than been asked to
// and still finishing a pass, and the total time it has spent stopped.
func (p *Pause) Held() (bool, time.Duration) {
	if p == nil {
		return false, 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.from.IsZero() {
		return false, p.held
	}
	return true, p.held + time.Since(p.from)
}

// Wait blocks while paused. It returns ctx's error, so it stands in for the
// cancellation check at each point it is safe to stop.
func (p *Pause) Wait(ctx context.Context) error {
	if p == nil {
		return ctx.Err()
	}
	p.mu.Lock()
	ch := p.resume
	if ch != nil {
		p.from = time.Now()
	}
	p.mu.Unlock()
	if ch == nil {
		return ctx.Err()
	}
	select {
	case <-ch:
	case <-ctx.Done():
	}
	p.mu.Lock()
	p.held += time.Since(p.from)
	p.from = time.Time{}
	p.mu.Unlock()
	return ctx.Err()
}
