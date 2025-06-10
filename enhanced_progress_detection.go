package main

// Enhanced progress detection using the /api/me endpoint for accurate progress data
func enhanceProgressDetection(audiobooks []Audiobook) []Audiobook {
	debugLog("Running enhanced progress detection on %d audiobooks using /api/me endpoint", len(audiobooks))
	
	// Fetch comprehensive user data from /api/me
	authorizeData, err := fetchAuthorizeData()
	if err != nil {
		debugLog("Failed to fetch user data from /api/me: %v", err)
		return audiobooks
	}
	
	enhanced := make([]Audiobook, len(audiobooks))
	copy(enhanced, audiobooks)
	
	itemsWithProgress := 0
	itemsWithFinished := 0
	
	for i := range enhanced {
		// Look up progress data by library item ID
		if progress := getMediaProgressByLibraryItemID(authorizeData, enhanced[i].ID); progress != nil {
			debugLog("Found authorize data for '%s': isFinished=%t, progress=%.4f, currentTime=%.2f", 
				enhanced[i].Title, progress.IsFinished, progress.Progress, progress.CurrentTime)
			
			if progress.IsFinished {
				enhanced[i].Progress = 1.0
				enhanced[i].CurrentTime = progress.CurrentTime
				itemsWithFinished++
			} else if progress.Progress > enhanced[i].Progress {
				enhanced[i].Progress = progress.Progress
				enhanced[i].CurrentTime = progress.CurrentTime
				itemsWithProgress++
			}
		} else {
			debugLog("No authorize progress data found for '%s' (ID: %s)", enhanced[i].Title, enhanced[i].ID)
		}
	}
	
	debugLog("Enhanced progress detection found %d items with progress, %d items with finished flags", itemsWithProgress, itemsWithFinished)
	return enhanced
}

// Enhanced finished book detection - determines if a book is actually finished despite showing 0% progress
func enhanceFinishedBookDetection(title string, libraryItemID string, currentProgress float64) bool {
	debugLog("Running enhanced finished book detection for '%s' (%.0f%% progress)", title, currentProgress*100)
	
	// Fetch user data from /api/me 
	authorizeData, err := fetchAuthorizeData()
	if err != nil {
		debugLog("Enhanced detection failed for '%s': %v", title, err)
		return false
	}
	
	// Check if this book is marked as finished in the user data
	if progress := getMediaProgressByLibraryItemID(authorizeData, libraryItemID); progress != nil {
		if progress.IsFinished {
			debugLog("Enhanced detection: book '%s' is marked as finished in /api/me data (finishedAt: %d)", title, progress.FinishedAt)
			return true
		} else {
			debugLog("Enhanced detection: book '%s' found but not finished (progress: %.4f)", title, progress.Progress)
			return false
		}
	}
	
	debugLog("Enhanced detection: no authorize data found for '%s'", title)
	return false
}
