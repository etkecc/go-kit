package kit

import (
	"sync"
	"testing"
	"time"
)

func TestWaitGroup(t *testing.T) {
	tests := []struct {
		name   string
		run    func(*WaitGroup) int
		expect int
	}{
		{
			name: "NewWaitGroup not nil",
			run: func(w *WaitGroup) int {
				if w == nil || w.wg == nil {
					return 0
				}
				return 1
			},
			expect: 1,
		},
		{
			name: "Do multiple funcs",
			run: func(w *WaitGroup) int {
				count := 0
				fn := func() {
					time.Sleep(10 * time.Millisecond)
					count++
				}
				w.Do(fn, fn, fn)
				w.Wait()
				return count
			},
			expect: 3,
		},
		{
			name: "Do empty funcs",
			run: func(w *WaitGroup) int {
				start := time.Now()
				w.Do()
				w.Wait()
				if time.Since(start) > time.Second {
					return 0
				}
				return 1
			},
			expect: 1,
		},
		{
			name: "Get underlying WaitGroup",
			run: func(w *WaitGroup) int {
				if w.Get() == nil {
					return 0
				}
				return 1
			},
			expect: 1,
		},
		{
			name: "Concurrent increment",
			run: func(w *WaitGroup) int {
				var mu sync.Mutex
				count := 0
				increment := func() {
					mu.Lock()
					count++
					mu.Unlock()
				}
				funcs := make([]func(), 100)
				for i := 0; i < 100; i++ {
					funcs[i] = increment
				}
				w.Do(funcs...)
				w.Wait()
				return count
			},
			expect: 100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := NewWaitGroup()
			got := tt.run(w)
			if got != tt.expect {
				t.Fatalf("expected %d, got %d", tt.expect, got)
			}
		})
	}
}
