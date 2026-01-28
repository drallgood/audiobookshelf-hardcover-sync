package sync

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
)

// BatchBookLookup represents a batch lookup request for books
type BatchBookLookup struct {
	Books   []models.AudiobookshelfBook
	Results map[string]*models.HardcoverBook // keyed by book ID
	Errors  map[string]error                 // keyed by book ID
	mutex   sync.RWMutex
}

// NewBatchBookLookup creates a new batch book lookup
func NewBatchBookLookup(books []models.AudiobookshelfBook) *BatchBookLookup {
	return &BatchBookLookup{
		Books:   books,
		Results: make(map[string]*models.HardcoverBook),
		Errors:  make(map[string]error),
	}
}

// AddResult adds a successful lookup result
func (b *BatchBookLookup) AddResult(bookID string, book *models.HardcoverBook) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	b.Results[bookID] = book
}

// AddError adds a failed lookup result
func (b *BatchBookLookup) AddError(bookID string, err error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	b.Errors[bookID] = err
}

// GetResult gets a lookup result
func (b *BatchBookLookup) GetResult(bookID string) (*models.HardcoverBook, error, bool) {
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	
	if book, exists := b.Results[bookID]; exists {
		return book, nil, true
	}
	
	if err, exists := b.Errors[bookID]; exists {
		return nil, err, true
	}
	
	return nil, nil, false
}

// BatchProcessBooks processes multiple books with optimized batching and caching
func (s *Service) BatchProcessBooks(ctx context.Context, books []models.AudiobookshelfBook, userProgress *models.AudiobookshelfUserProgress) error {
	if len(books) == 0 {
		return nil
	}

	s.log.Info("Starting batch book processing", map[string]interface{}{
		"total_books": len(books),
	})

	// Phase 1: Pre-filter books that don't need syncing (if incremental sync is enabled)
	booksToProcess := make([]models.AudiobookshelfBook, 0, len(books))
	skippedCount := 0

	for _, book := range books {
		if s.config.Sync.Incremental {
			// Calculate current progress and status
			currentProgress := 0.0
			if book.Media.Duration > 0 {
				// For finished books, use 1.0 (100%) instead of CurrentTime/Duration
				// because Audiobookshelf sometimes reports CurrentTime as 0 for finished books
				if book.Progress.IsFinished && book.Progress.FinishedAt > 0 {
					currentProgress = 1.0
				} else {
					currentProgress = book.Progress.CurrentTime / book.Media.Duration
				}
			}
			currentStatus := s.determineBookStatus(currentProgress, book.Progress.IsFinished, book.Progress.FinishedAt)
			
			// Check if this book needs syncing
			minChangeThreshold := float64(s.config.Sync.MinChangeThreshold) / book.Media.Duration
			if !s.state.NeedsSync(book.ID, currentProgress, currentStatus, minChangeThreshold) {
				skippedCount++
				continue
			}
		}
		
		booksToProcess = append(booksToProcess, book)
	}

	s.log.Info("Pre-filtering complete", map[string]interface{}{
		"original_count":  len(books),
		"to_process":      len(booksToProcess),
		"skipped":         skippedCount,
		"skip_percentage": fmt.Sprintf("%.1f%%", float64(skippedCount)/float64(len(books))*100),
	})

	if len(booksToProcess) == 0 {
		s.log.Info("No books need processing after pre-filtering")
		return nil
	}

	// Phase 2: Collect unique ASINs for batch lookup optimization
	uniqueASINs := make(map[string]bool)
	asinToBooks := make(map[string][]models.AudiobookshelfBook)
	
	for _, book := range booksToProcess {
		if book.Media.Metadata.ASIN != "" {
			if !uniqueASINs[book.Media.Metadata.ASIN] {
				uniqueASINs[book.Media.Metadata.ASIN] = true
			}
			asinToBooks[book.Media.Metadata.ASIN] = append(asinToBooks[book.Media.Metadata.ASIN], book)
		}
	}

	s.log.Info("ASIN analysis complete", map[string]interface{}{
		"unique_asins":     len(uniqueASINs),
		"books_with_asin":  len(asinToBooks),
		"deduplication":    fmt.Sprintf("%.1f%% reduction", float64(len(booksToProcess)-len(uniqueASINs))/float64(len(booksToProcess))*100),
	})

	// Phase 3: Process books with optimized lookup strategy
	batch := NewBatchBookLookup(booksToProcess)
	processedCount := 0
	
	// Process books in smaller batches to avoid overwhelming the API
	batchSize := 50 // Process 50 books at a time
	for i := 0; i < len(booksToProcess); i += batchSize {
		end := i + batchSize
		if end > len(booksToProcess) {
			end = len(booksToProcess)
		}
		
		batchBooks := booksToProcess[i:end]
		s.log.Debug("Processing book batch", map[string]interface{}{
			"batch_start": i + 1,
			"batch_end":   end,
			"batch_size":  len(batchBooks),
		})
		
		// Process each book in the batch
		for _, book := range batchBooks {
			// Respect context cancellation
			select {
			case <-ctx.Done():
				s.log.Warn("Batch processing canceled by context", nil)
				return ctx.Err()
			default:
			}

			if err := s.processBook(ctx, book, userProgress); err != nil {
				batch.AddError(book.ID, err)
				s.log.Warn("Failed to process book in batch", map[string]interface{}{
					"book_id": book.ID,
					"title":   book.Media.Metadata.Title,
					"error":   err.Error(),
				})
			} else {
				processedCount++
			}
		}
		
		// Add a small delay between batches to be respectful to the API
		if end < len(booksToProcess) {
			// Context-aware delay
			select {
			case <-ctx.Done():
				s.log.Warn("Batch delay canceled by context", nil)
				return ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
	}

	s.log.Info("Batch processing complete", map[string]interface{}{
		"total_books":     len(booksToProcess),
		"processed":       processedCount,
		"failed":          len(booksToProcess) - processedCount,
		"success_rate":    fmt.Sprintf("%.1f%%", float64(processedCount)/float64(len(booksToProcess))*100),
	})

	return nil
}

// PreloadASINCache preloads the ASIN cache with books that are likely to be accessed
func (s *Service) PreloadASINCache(ctx context.Context, books []models.AudiobookshelfBook) error {
	// Collect unique ASINs that aren't already cached
	asinsToPreload := make([]string, 0)
	
	for _, book := range books {
		if book.Media.Metadata.ASIN != "" {
			if _, exists := s.getASINFromCache(book.Media.Metadata.ASIN); !exists {
				asinsToPreload = append(asinsToPreload, book.Media.Metadata.ASIN)
			}
		}
	}
	
	if len(asinsToPreload) == 0 {
		s.log.Debug("No ASINs need preloading - all are already cached")
		return nil
	}
	
	s.log.Info("Preloading ASIN cache", map[string]interface{}{
		"asins_to_preload": len(asinsToPreload),
	})
	
	// Note: Since Hardcover doesn't support batch ASIN lookups in their API,
	// we'll rely on the existing caching mechanism during normal processing.
	// This method serves as a placeholder for future batch API support.
	
	return nil
}

// OptimizeCache performs cache maintenance and optimization
func (s *Service) OptimizeCache() {
	s.log.Debug("Starting cache optimization")
	
	// Clean expired entries from persistent cache
	if s.persistentCache != nil {
		removed := s.persistentCache.CleanExpired()
		if removed > 0 {
			s.log.Info("Cleaned expired cache entries", map[string]interface{}{
				"removed_entries": removed,
			})
		}
	}
	
	// Log current cache statistics
	s.logASINCacheStats()
	
	s.log.Debug("Cache optimization complete")
}
