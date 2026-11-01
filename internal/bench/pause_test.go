package bench

import (
	"context"
	"testing"
	"time"
)

func waitHeld(t *testing.T, p *Pause) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; {
		if held, _ := p.Held(); held {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the engine never stopped")
		}
		time.Sleep(time.Millisecond)
	}
}

func pauseEngine(t *testing.T, p *Pause, events chan Event) *Engine {
	return &Engine{
		Cfg: Config{
			Threads: []int{1}, SrcThreads: 1, Passes: 2, MaxPasses: 4,
			MinWall: 10 * time.Millisecond, MaxJob: time.Minute, Encode: true, Decode: true,
		},
		Data: testData(t), Variants: testVariants(t)[:1], Events: events, Pause: p,
	}
}

// Nothing may be measured while paused, and resuming must finish the run as
// though it never stopped.
func TestPauseHoldsBetweenPasses(t *testing.T) {
	p := &Pause{}
	p.Set(true)
	events := make(chan Event, 4096)
	eng := pauseEngine(t, p, events)
	done := make(chan []Result)
	go func() {
		res, _ := eng.Run(context.Background())
		done <- res
	}()

	waitHeld(t, p)
	time.Sleep(20 * time.Millisecond)
	for len(events) > 0 {
		if ev := <-events; ev.Kind == "pass" || ev.Kind == "result" {
			t.Fatalf("measured while paused: %+v", ev)
		}
	}

	p.Set(false)
	res := <-done
	if len(res) == 0 {
		t.Fatal("no results after resuming")
	}
	for _, r := range res {
		if r.Err != "" {
			t.Errorf("%s %s: %s", r.Op, r.Codec, r.Err)
		}
	}
	if held, d := p.Held(); held || d < 20*time.Millisecond {
		t.Errorf("Held() = %v, %s after resuming from a 20ms+ pause", held, d)
	}
}

// Cancelling a paused run must not wait for a resume that may never come.
func TestCancelReleasesPause(t *testing.T) {
	p := &Pause{}
	p.Set(true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() {
		_, err := pauseEngine(t, p, nil).Run(ctx)
		done <- err
	}()
	waitHeld(t, p)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a cancelled run reported success")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancel did not release the pause")
	}
}
