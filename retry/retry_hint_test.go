package retry

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"
)

// Each case runs in a synctest bubble: the fake clock advances only when every
// goroutine is durably blocked, so a sleep of d costs exactly d in virtual time
// and time.Since reads back the real wait with no scheduler slop and no real sleep.

// A hint from After overrides the linear step. The step is set absurdly large, so an
// un-honored hint would show up as a multi-second wait; the honored hint is exact
// because jitter is off.
func TestDoCtx_HintHonoredOverLinearStep(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		hint := 500 * time.Millisecond
		r := New(WithMaxRetries(2), WithDelayStep(10*time.Second), WithJitter(false))

		start := time.Now()
		err := r.DoCtx(context.Background(), func() error {
			return After(hint)
		})
		elapsed := time.Since(start)

		if elapsed != hint {
			t.Errorf("expected wait %v (hint overrides the 10s linear step), got %v", hint, elapsed)
		}
		if err == nil {
			t.Error("expected terminal error after retries exhausted")
		}
	})
}

// With jitter on, the hint is a floor plus up to 10%: wait lands in [hint, hint*1.1).
func TestDoCtx_HintJitterBand(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		hint := 1 * time.Second
		r := New(WithMaxRetries(2), WithDelayStep(time.Minute), WithJitter(true))

		start := time.Now()
		_ = r.DoCtx(context.Background(), func() error {
			return After(hint)
		})
		elapsed := time.Since(start)

		if elapsed < hint || elapsed >= hint+hint/10 {
			t.Errorf("expected wait in [%v, %v), got %v", hint, hint+hint/10, elapsed)
		}
	})
}

// Backward-compat: an error that does not implement DelayHinter follows the linear
// schedule byte-for-byte. Three attempts means two sleeps: step*1 + step*2.
func TestDoCtx_PlainErrorLinearUnchanged(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		step := 2 * time.Second
		r := New(WithMaxRetries(3), WithDelayStep(step), WithJitter(false))

		start := time.Now()
		_ = r.DoCtx(context.Background(), func() error {
			return errors.New("plain")
		})
		elapsed := time.Since(start)

		want := step*1 + step*2
		if elapsed != want {
			t.Errorf("expected linear schedule %v, got %v", want, elapsed)
		}
	})
}

// A non-positive hint is ignored and the linear step applies, so After(0) can never
// collapse the backoff to zero and turn a retry storm loose.
func TestDoCtx_HintNonPositiveIgnored(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		step := 4 * time.Second
		r := New(WithMaxRetries(2), WithDelayStep(step), WithJitter(false))

		start := time.Now()
		_ = r.DoCtx(context.Background(), func() error {
			return After(0)
		})
		elapsed := time.Since(start)

		if elapsed != step {
			t.Errorf("expected linear fallback %v for a non-positive hint, got %v", step, elapsed)
		}
	})
}

// After's value satisfies DelayHinter and unwraps through a wrapping error, so a
// caller that adds context with %w still lands the hint.
func TestAfter_MatchesThroughWrap(t *testing.T) {
	var dh DelayHinter
	wrapped := fmt.Errorf("calling upstream: %w", After(3*time.Second))
	if !errors.As(wrapped, &dh) {
		t.Fatal("expected wrapped After error to satisfy DelayHinter via errors.As")
	}
	if got := dh.SuggestedRetryDelay(); got != 3*time.Second {
		t.Errorf("expected 3s hint through the wrap, got %v", got)
	}
}
