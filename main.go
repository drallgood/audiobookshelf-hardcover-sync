// audiobookshelf-hardcover-sync
//
// This project has been restructured to follow Go project layout standards.
// Please use the appropriate command from the cmd/ directory:
//
// Main sync service:
//   go run ./cmd/audiobookshelf-hardcover-sync
//
// Edition management tool:
//   go run ./cmd/edition-tool
//
// Lookup tool (authors, narrators, publishers):
//   go run ./cmd/lookup-tool
//
// Image upload tool:
//   go run ./cmd/image-tool
//
// The old monolithic CLI has been moved to archive/cmd/legacy/main.go
// and is kept for reference only. Please migrate to the new command structure.
package main

import "fmt"

func main() {
	fmt.Println("This project has been restructured. Please use the appropriate command from the cmd/ directory.")
	fmt.Println("\nAvailable commands:")
	fmt.Println("  go run ./cmd/audiobookshelf-hardcover-sync - Main sync service")
	fmt.Println("  go run ./cmd/edition-tool        - Edition management")
	fmt.Println("  go run ./cmd/lookup-tool        - Lookup authors/narrators/publishers")
	fmt.Println("  go run ./cmd/image-tool         - Upload images to Hardcover")
	fmt.Println("\nFor more information, see the README.md file.")
}
