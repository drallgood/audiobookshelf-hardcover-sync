package util

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSemaphore(t *testing.T) {
	maxConcurrent := 2
	totalRequests := 5
	t.Logf("Starting test with maxConcurrent=%d, totalRequests=%d", maxConcurrent, totalRequests)

	// Create a semaphore with maxConcurrent tokens
	sem := make(chan struct{}, maxConcurrent)

	// Pre-fill the semaphore with tokens
	for i := 0; i < maxConcurrent; i++ {
		sem <- struct{}{}
	}

	var (
		active int32
		mu     sync.Mutex
		done   = make(chan struct{})
	)

	// Start a goroutine to monitor active requests
	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				mu.Lock()
				currActive := atomic.LoadInt32(&active)
				if currActive > int32(maxConcurrent) {
					t.Errorf("Active requests %d exceeds maxConcurrent %d", currActive, maxConcurrent)
				}
				if currActive > 0 {
					t.Logf("Current active requests: %d", currActive)
				}
				mu.Unlock()
			case <-done:
				return
			}
		}
	}()

	// Start test goroutines
	var wg sync.WaitGroup
	for i := 0; i < totalRequests; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			t.Logf("Goroutine %d: attempting to acquire semaphore", id)

			// Try to acquire semaphore
			<-sem // Acquire semaphore
			atomic.AddInt32(&active, 1)
			t.Logf("Goroutine %d: acquired semaphore (active: %d)", id, atomic.LoadInt32(&active))

			// Simulate work
			time.Sleep(100 * time.Millisecond)

			// Release semaphore and update active count
			atomic.AddInt32(&active, -1)
			sem <- struct{}{}
			t.Logf("Goroutine %d: released semaphore (active: %d)", id, atomic.LoadInt32(&active))
		}(i)
	}

	// Wait for all goroutines to complete or timeout
	wg.Wait()
	close(done)

	t.Logf("All goroutines completed")
}
