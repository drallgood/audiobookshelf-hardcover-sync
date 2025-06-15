package main

import (
	"context"
	"log"
	"sync"

	"github.com/drallgood/audiobookshelf-hardcover-sync/hardcover"
)

// WorkerPool manages a pool of worker goroutines to process books concurrently
type WorkerPool struct {
	maxWorkers   int
	taskQueue    chan *Audiobook
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
		taskQueue:  make(chan *Audiobook, 100), // Buffer to avoid blocking
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
func (wp *WorkerPool) AddTask(book *Audiobook) {
	if book == nil {
		return
	}
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

// ProcessBooks processes a slice of books using batch processing
func ProcessBooks(books []*Audiobook, maxWorkers int, hcClient *hardcover.BatchClient) (int, int) {
	if len(books) == 0 {
		return 0, 0
	}

	// Convert Audiobook slice to hardcover.Book slice for batch processing
	hcBooks := make([]*hardcover.Book, 0, len(books))
	bookMap := make(map[string]*Audiobook) // Map Hardcover book ID to Audiobook
	
	for _, book := range books {
		hcBook := &hardcover.Book{
			Title:    book.Title,
			Author:   book.Author,
			Progress: book.Progress * 100, // Convert to percentage
			// Add other fields as needed
		}
		hcBooks = append(hcBooks, hcBook)
		bookMap[hcBook.ID] = book
	}

	// Use BatchSyncBooks to process books in batches
	results, err := hcClient.BatchSyncBooks(context.Background(), hcBooks)
	if err != nil {
		log.Printf("Error during batch sync: %v", err)
		return 0, len(books) // All failed if batch fails
	}

	// Process results
	successCount := 0
	errors := 0
	for _, result := range results {
		if result.Error != nil {
			log.Printf("Failed to sync book %s: %v", result.BookID, result.Error)
			errors++
		} else {
			successCount++
			// Update the original book with synced data if needed
			if book, exists := bookMap[result.BookID]; exists {
				book.ID = result.BookID
				// Update other fields as needed
			}
		}
	}

	return successCount, errors
}
