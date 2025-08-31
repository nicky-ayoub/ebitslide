// Package scan operates on files in a directory and its subdirectories
package scan

import (
	"fmt"
	"io/fs"
	"log"
	"path/filepath"
	"strings"
)

// FileScanner defines the interface for scanning files.
type FileScanner interface {
	Run(dir string, logger LoggerFunc) <-chan FileItem
}

// FileScannerImpl is a concrete implementation of FileScanner.
type FileScannerImpl struct{}

// Run delegates to the package-level Run function.
func (f *FileScannerImpl) Run(dir string, logger LoggerFunc) <-chan FileItem {
	return Run(dir, logger)
}

// FileItem represents a file item. Just a path for now
type FileItem struct {
	Path string
	Info fs.FileInfo
}

// FileItems is a slice of FileItem
type FileItems []FileItem

// LoggerFunc defines a function signature for logging messages.
type LoggerFunc func(message string)

// NewFileItem creates a new FileItem
func NewFileItem(path string, fi fs.FileInfo) FileItem {
	return FileItem{
		Path: path,
		Info: fi,
	}

}

// findImageFiles recursively scans dir for supported image files and sends them to the out channel.
// It closes the out channel when done.
func findImageFiles(dir string, out chan<- FileItem, logger LoggerFunc) {
	defer close(out) // Ensure channel is closed when WalkDir finishes or panics

	logMsg := func(format string, args ...interface{}) {
		if logger != nil {
			logger(fmt.Sprintf(format, args...))
		} else {
			log.Printf(format, args...) // Fallback
		}
	}

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			logMsg("Scan: Error accessing path %q: %v", path, err)
			if d != nil && d.IsDir() && path != dir { // Don't skip the root dir on error
				return filepath.SkipDir // Skip problematic directory
			}
			return nil // Continue if possible, or return err to stop
		}

		if !d.IsDir() && isImage(d.Name()) {
			// Get FileInfo. d.Info() is efficient.
			info, infoErr := d.Info()
			if infoErr != nil {
				logMsg("Scan: Error getting FileInfo for %q: %v", path, infoErr)
				return nil // Skip this file
			}
			if info.Size() > 0 { // Ensure it's not an empty file
				out <- NewFileItem(path, info)
			}
		}
		return nil
	})

	if err != nil {
		// Log the error from WalkDir itself, if any.
		// The channel will still be closed by defer.
		logMsg("Scan: Error walking directory %s: %v", dir, err)
	}
}

// Run is the entry point for the package. It now returns a channel
// from which FileItems can be read. The scanning happens in a new goroutine.
func Run(dir string, logger LoggerFunc) <-chan FileItem {
	out := make(chan FileItem, 100) // Buffered channel for some decoupling

	logMsg := func(format string, args ...interface{}) {
		if logger != nil {
			logger(fmt.Sprintf(format, args...))
		} else {
			log.Printf(format, args...) // Fallback
		}
	}

	go func() {
		absDir, err := filepath.Abs(dir)
		if err != nil {
			logMsg("Scan: Error getting absolute path for %s: %v. Aborting scan.", dir, err)
			close(out) // Close channel to signal error and stop processing
			return     // Do not proceed with findImageFiles
		}
		findImageFiles(absDir, out, logger)
	}()

	return out
}

func isImage(fileName string) bool {
	switch strings.ToLower(filepath.Ext(fileName)) {
	case ".png", ".jpg", ".jpeg", ".gif":
		return true
	default:
		return false
	}
}
