package service

import (
	"github.com/nicky-ayoub/ebitslide/internal/scan"
)

// FileScanner abstracts file scanning.
type FileScanner interface {
	Run(dir string, logger scan.LoggerFunc) <-chan scan.FileItem
}

// Service is the main entry point for business logic.
type ScannerService struct {
	FileScan   FileScanner
	Extensions map[string]bool // Supported image extensions
}

// NewService constructs a new Service.
func NewScannerService(fileScan FileScanner) *ScannerService {
	return &ScannerService{
		FileScan:   fileScan,
		Extensions: map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".gif": true},
	}
}
