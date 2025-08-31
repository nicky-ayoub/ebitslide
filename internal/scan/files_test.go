// filepath: /home/nicky/src/go/fyslide/internal/scan/files_test.go
package scan

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestNewFileItem(t *testing.T) {
	path := "test/path"
	// Create a dummy FileInfo for testing
	dummyInfo, err := os.Stat(".") // Stat current dir as a placeholder
	if err != nil {
		t.Fatalf("Failed to create dummy FileInfo: %v", err)
	}
	item := NewFileItem(path, dummyInfo)
	if item.Path != path {
		t.Errorf("expected Path %s, got %s", path, item.Path)
	}
	if item.Info == nil {
		t.Errorf("expected Info to be non-nil, got nil")
	}
}

func TestIsImage(t *testing.T) {
	tests := []struct {
		name     string
		expected bool
	}{
		{"image.PNG", true},
		{"image.jpg", true},
		{"image.jpeg", true},
		{"image.gif", true},
		{"image.txt", false},
		{"image", false},
		{".jpeg", true}, // Test with only extension
	}

	for _, test := range tests {
		result := isImage(test.name)
		if result != test.expected {
			t.Errorf("isImage(%s) = %v; want %v", test.name, result, test.expected)
		}
	}
}

func TestRun(t *testing.T) {
	rootDir, err := os.MkdirTemp("", "testRunDir")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(rootDir)

	// --- Setup test file structure ---
	// Top-level files
	topImage1 := filepath.Join(rootDir, "image1.png")
	topImage2 := filepath.Join(rootDir, "image2.JPG") // Test case insensitivity for extension
	topText := filepath.Join(rootDir, "document.txt")
	topEmptyImage := filepath.Join(rootDir, "empty.gif") // 0-byte image

	// Subdirectory 1
	subDir1 := filepath.Join(rootDir, "sub1")
	err = os.Mkdir(subDir1, 0755)
	if err != nil {
		t.Fatalf("Failed to create subDir1: %v", err)
	}
	subImage1 := filepath.Join(subDir1, "image3.jpeg")
	subText1 := filepath.Join(subDir1, "notes.md")

	// Subdirectory 2 (empty)
	subDir2 := filepath.Join(rootDir, "sub2")
	err = os.Mkdir(subDir2, 0755)
	if err != nil {
		t.Fatalf("Failed to create subDir2: %v", err)
	}

	// Subdirectory within subDir1
	subSubDir := filepath.Join(subDir1, "subsub")
	err = os.Mkdir(subSubDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create subSubDir: %v", err)
	}
	subSubImage1 := filepath.Join(subSubDir, "image4.PNG")

	// Files to create (path: content_size)
	filesToCreate := map[string]int{
		topImage1:     10,
		topImage2:     10,
		topText:       10,
		topEmptyImage: 0, // 0-byte file, should be skipped
		subImage1:     10,
		subText1:      10,
		subSubImage1:  10,
	}

	// Define expected image paths (0-byte files are skipped by findImageFiles)
	expectedImagePathsRel := []string{topImage1, topImage2, subImage1, subSubImage1}
	expectedImagePathsAbs := make([]string, len(expectedImagePathsRel))
	for i, p := range expectedImagePathsRel {
		absP, err := filepath.Abs(p)
		if err != nil {
			t.Fatalf("Failed to get absolute path for %s: %v", p, err)
		}
		expectedImagePathsAbs[i] = absP
	}
	sort.Strings(expectedImagePathsAbs) // Sort for consistent comparison

	for path, size := range filesToCreate {
		content := make([]byte, size)
		if size > 0 {
			content[0] = 'a' // ensure not empty if size > 0
		}
		err = os.WriteFile(path, content, 0644)
		if err != nil {
			t.Fatalf("Failed to write test file %s: %v", path, err)
		}
	}

	// --- Act ---
	// Define a logger for the test
	testLogger := func(message string) {
		t.Logf("ScanTestLogger: %s", message)
	}

	itemsChan := Run(rootDir, testLogger)
	var foundItems FileItems

	timeout := time.After(5 * time.Second) // Timeout for channel reading
	done := false
	for !done {
		select {
		case item, ok := <-itemsChan:
			if !ok { // Channel closed
				done = true
				continue
			}
			foundItems = append(foundItems, item)
		case <-timeout:
			t.Fatal("TestRun timed out waiting for items from channel")
			done = true
		}
	}

	// --- Assert ---
	if len(foundItems) != len(expectedImagePathsAbs) {
		t.Errorf("Run() found %d image files, want %d", len(foundItems), len(expectedImagePathsAbs))
		t.Logf("Expected paths: %v", expectedImagePathsAbs)
		var actualPaths []string
		for _, fi := range foundItems {
			actualPaths = append(actualPaths, fi.Path)
		}
		t.Logf("Actual paths: %v", actualPaths)
		// No return here, continue to check content if possible
	}

	var actualFoundPaths []string
	for _, item := range foundItems {
		actualFoundPaths = append(actualFoundPaths, item.Path)
		if item.Info == nil {
			t.Errorf("FileItem for %s has nil FileInfo", item.Path)
		} else {
			if item.Info.IsDir() {
				t.Errorf("FileItem for %s is a directory, should be a file", item.Path)
			}
			// 0-byte files should have been skipped by `findImageFiles`
			if item.Info.Size() == 0 {
				t.Errorf("FileItem for %s has 0 size, should have been skipped or have size > 0", item.Path)
			}
		}
		// Check if path is absolute
		if !filepath.IsAbs(item.Path) {
			t.Errorf("FileItem path %s is not absolute", item.Path)
		}
	}
	sort.Strings(actualFoundPaths)

	// Compare the sorted slices of paths
	if len(actualFoundPaths) == len(expectedImagePathsAbs) { // Only compare content if lengths match
		for i := range actualFoundPaths {
			if actualFoundPaths[i] != expectedImagePathsAbs[i] {
				t.Errorf("Mismatch in found paths.\nExpected: %v\nGot:      %v", expectedImagePathsAbs, actualFoundPaths)
				break
			}
		}
	} else { // Length mismatch already reported, but good to log details again if needed or for clarity
		t.Logf("Path list length mismatch prevented detailed path comparison.")
	}
}
