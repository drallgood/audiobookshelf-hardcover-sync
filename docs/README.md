# Documentation

This folder contains detailed documentation about specific features and fixes implemented in the AudioBookShelf to Hardcover sync tool.

## Files

### CONDITIONAL_SYNC_DEMO.md
Explains the conditional sync logic that prevents unnecessary API calls by only syncing books when:
- The book doesn't exist in the user's Hardcover library
- The reading status has changed
- The progress has changed significantly

### DUPLICATE_READS_FIX.md
Documents the fix for duplicate `user_book_reads` entries that were being created when syncing the same book multiple times on the same day. The fix includes:
- New `checkExistingUserBookRead()` function
- Enhanced sync logic to update existing entries instead of creating duplicates
- Proper handling of books with past `started_at` dates

## Main Documentation

For general usage and setup instructions, see the main [README.md](../README.md) in the project root.
