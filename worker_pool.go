package main

import (
	"context"
	"log"
	"sync"
	"time"
)

// WorkerPool manages a pool of worker goroutines to process books concurrently
type WorkerPool struct {
	maxWorkers   int
	taskQueue    chan Audiobook
	resultChan   chan error
	wg           sync.WaitGroup
	ctx          context.Context
	cancel       context.CancelFunc
	successCount int
	totalTasks   int
	mu           sync.Mutex
}

// NewWorkerPool creates a new worker pool with the specified number of workers
func NewWorkerPool(maxWorkers int) *WorkerPool {
	ctx, cancel := context.WithCancel(context.Background())
	return &WorkerPool{
		maxWorkers: maxWorkers,
		taskQueue:  make(chan Audiobook, 100), // Buffer to avoid blocking
		resultChan: make(chan error, 100),
		ctx:        ctx,
		cancel:     cancel,
	}
}

// Start initializes the worker pool and starts the workers
func (wp *WorkerPool) Start() {
	for i := 0; i < wp.maxWorkers; i++ {
		wp.wg.Add(1)
		go wp.worker(i + 1)
	}
}

// Stop gracefully shuts down the worker pool
func (wp *WorkerPool) Stop() {
	close(wp.taskQueue)
	wp.cancel()
	wp.wg.Wait()
	close(wp.resultChan)
}

// AddTask adds a book to the task queue
func (wp *WorkerPool) AddTask(book Audiobook) {
	wp.mu.Lock()
	wp.totalTasks++
	wp.mu.Unlock()
	wp.taskQueue <- book
}

// Results returns a channel to receive results from workers
func (wp *WorkerPool) Results() <-chan error {
	return wp.resultChan
}

// SuccessCount returns the number of successfully processed tasks
func (wp *WorkerPool) SuccessCount() int {
	wp.mu.Lock()
	defer wp.mu.Unlock()
	return wp.successCount
}

// worker is the main worker goroutine that processes books
func (wp *WorkerPool) worker(id int) {
	defer wp.wg.Done()
	
	for {
		select {
		case book, ok := <-wp.taskQueue:
			if !ok {
				// Channel closed, no more tasks
				return
			}
			
			// Process the book
			err := syncToHardcover(book)
			if err == nil {
				wp.mu.Lock()
				wp.successCount++
				wp.mu.Unlock()
				log.Printf("[Worker %d] Synced book: %s (Progress: %.2f%%)", id, book.Title, book.Progress*100)
			} else {
				log.Printf("[Worker %d] Failed to sync book '%s' (Progress: %.2f%%): %v", 
					id, book.Title, book.Progress*100, err)
			}
			
			// Send result
			select {
			case wp.resultChan <- err:
			case <-wp.ctx.Done():
				return
			}
			
		case <-wp.ctx.Done():
			// Context cancelled, exit
			return
		}
	}
}

// ProcessBooks processes a slice of books concurrently using the worker pool
func ProcessBooks(books []Audiobook, maxWorkers int) (int, int) {
	if len(books) == 0 {
		return 0, 0
	}
	
	// Limit number of workers to number of books if necessary
	if maxWorkers > len(books) {
		maxWorkers = len(books)
	}
	if maxWorkers < 1 {
		maxWorkers = 1
	}
	
	// Create and start worker pool
	pool := NewWorkerPool(maxWorkers)
	pool.Start()
	defer pool.Stop()
	
	// Add all books to the task queue in a separate goroutine
	go func() {
		for _, book := range books {
			pool.AddTask(book)
		}
	}()
	
	// Process results
	errors := 0
	for range books {
		select {
		case err := <-pool.Results():
			if err != nil {
				errors++
			}
		}
	}
	
	successCount := pool.SuccessCount()
	return successCount, errors
}
