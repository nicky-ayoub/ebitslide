package ui

import (
	"fmt"
	"sync"

	"github.com/nicky-ayoub/ebitslide/internal/scan"
)

// ViewportItem is a helper struct for the thumbnail strip, bundling an image
// with its index in the current view (shuffled or sequential).
type ViewportItem struct {
	Item      scan.FileItem
	ViewIndex int
}

// ImageState manages the state of the image collection, including the full list,
// filtered list, current index, and filtering/randomization status.
type ImageState struct {
	mu sync.RWMutex

	// The original, full list of images
	images scan.FileItems
	// Manages the permutation for the original images for random mode
	permutationManager *scan.PermutationManager

	// The list when a filter is active
	filteredImages scan.FileItems
	// Manages the permutation for the filtered images for random mode
	filteredPermutationManager *scan.PermutationManager

	// The current view index into the active list (sequential or shuffled)
	index int

	// Flag to indicate if filtering is active
	isFiltered bool
	// The tag currently being filtered by
	currentFilterTag string

	// Flag to indicate if random mode is active
	random bool
}

// NewImageState creates a new ImageState manager.
func NewImageState() *ImageState {
	is := &ImageState{
		images: make(scan.FileItems, 0),
		random: true, // Default to random on
	}
	// Initialize the permutation manager for the main list.
	// It starts empty but is ready to be synced as data is added.
	is.permutationManager = scan.NewPermutationManager(&is.images)
	return is
}

// AddImage is a thread-safe method to add an image to the main list.
func (is *ImageState) Clear() {
	is.mu.Lock()
	defer is.mu.Unlock()
	is.images = nil
}

// AddImage is a thread-safe method to add an image to the main list.
func (is *ImageState) AddImage(item scan.FileItem) {
	is.mu.Lock()
	defer is.mu.Unlock()
	is.images = append(is.images, item)
}

// AddImages is a thread-safe method to add a batch of images to the main list.
func (is *ImageState) AddImages(items scan.FileItems) {
	is.mu.Lock()
	defer is.mu.Unlock()
	is.images = append(is.images, items...)
}

// SyncPermutationManager ensures the main permutation manager is up-to-date
// with the current list of images. This should be called after a bulk add.
func (is *ImageState) SyncPermutationManager() {
	is.mu.Lock()
	defer is.mu.Unlock()
	is.permutationManager.SyncNewData()
}

// getCurrentListUnlocked returns the active image list. It is not thread-safe
// and must be called from a method that holds a lock.
func (is *ImageState) getCurrentListUnlocked() scan.FileItems {
	if is.isFiltered {
		return is.filteredImages
	}
	return is.images
}

// getCurrentImageCountUnlocked returns the count of the active image list.
// It is not thread-safe and must be called from a method that holds a lock.
func (is *ImageState) getCurrentImageCountUnlocked() int {
	return len(is.getCurrentListUnlocked())
}

// GetCurrentImageCount returns the count of the active image list in a thread-safe manner.
func (is *ImageState) GetCurrentImageCount() int {
	is.mu.RLock()
	defer is.mu.RUnlock()
	return is.getCurrentImageCountUnlocked()
}

// GetCurrentIndex returns the current view index.
func (is *ImageState) GetCurrentIndex() int {
	is.mu.RLock()
	defer is.mu.RUnlock()
	return is.index
}

// SetIndex sets the current view index.
func (is *ImageState) SetIndex(i int) {
	is.mu.Lock()
	defer is.mu.Unlock()
	is.index = i
}

// getActivePermutationManagerUnlocked returns the permutation manager for the active list.
// It is not thread-safe and must be called from a method that holds a lock.
func (is *ImageState) getActivePermutationManagerUnlocked() *scan.PermutationManager {
	if is.isFiltered {
		return is.filteredPermutationManager
	}
	return is.permutationManager
}

// GetActivePermutationManager returns the permutation manager for the active list in a thread-safe manner.
func (is *ImageState) GetActivePermutationManager() *scan.PermutationManager {
	is.mu.RLock()
	defer is.mu.RUnlock()
	return is.getActivePermutationManagerUnlocked()
}

// IsRandom returns true if random mode is active.
func (is *ImageState) IsRandom() bool {
	is.mu.RLock()
	defer is.mu.RUnlock()
	return is.random
}

// IsFiltered returns true if a filter is active.
func (is *ImageState) IsFiltered() bool {
	is.mu.RLock()
	defer is.mu.RUnlock()
	return is.isFiltered
}

// GetCurrentItem returns the FileItem for the current index, or nil if invalid
func (is *ImageState) GetCurrentItem() *scan.FileItem {
	is.mu.RLock()
	defer is.mu.RUnlock()
	item, err := is.getItemByViewIndexUnlocked(is.index)
	if err != nil {
		// This is a common case (e.g., empty list), so logging might be too noisy.
		// The caller should handle the nil case gracefully.
		return nil
	}
	return item
}

// getItemByViewIndexUnlocked retrieves a FileItem from the active view (sequential or random)
// using a specific view index. It is not thread-safe and must be called from a method that holds a lock.
func (is *ImageState) getItemByViewIndexUnlocked(viewIndex int) (*scan.FileItem, error) {
	// 1. Determine the active data sources based on the filter state.
	activeList := &is.images
	activeManager := is.getActivePermutationManagerUnlocked()

	if is.isFiltered {
		activeList = &is.filteredImages
	}

	// 2. Check for an empty or uninitialized data source.
	if activeList == nil || len(*activeList) == 0 {
		return nil, fmt.Errorf("active list is empty or not initialized")
	}

	// 3. Retrieve the item based on the current mode (random or sequential).
	if is.random {
		if activeManager == nil {
			return nil, fmt.Errorf("random mode is on but PermutationManager is not initialized")
		}
		item, err := activeManager.GetDataByShuffledIndex(viewIndex)
		if err != nil {
			return nil, fmt.Errorf("error getting data for shuffled index %d: %w", viewIndex, err)
		}
		return &item, nil
	}

	// Default to sequential mode retrieval.
	if viewIndex < 0 || viewIndex >= len(*activeList) {
		return nil, fmt.Errorf("sequential index %d out of bounds", viewIndex)
	}
	return &(*activeList)[viewIndex], nil
}

// GetItemByViewIndex retrieves a FileItem from the active view (sequential or random)
// using a specific view index. This is the core data retrieval logic.
func (is *ImageState) GetItemByViewIndex(viewIndex int) (*scan.FileItem, error) {
	is.mu.RLock()
	defer is.mu.RUnlock()
	return is.getItemByViewIndexUnlocked(viewIndex)
}

// ToggleRandomMode switches the random mode on or off and calculates the new
// index to keep the same image in view.
func (is *ImageState) ToggleRandomMode(currentPath string) {
	is.mu.Lock()
	defer is.mu.Unlock()

	is.random = !is.random

	if currentPath == "" {
		is.index = 0
		return
	}

	newIndex := -1
	activeList := is.getCurrentListUnlocked()

	// Find the sequential index of the current item in the active list.
	sequentialIndexInList := -1
	for i, item := range activeList {
		if item.Path == currentPath {
			sequentialIndexInList = i
			break
		}
	}

	if sequentialIndexInList == -1 {
		// This can happen if the list changed underneath; reset to the start.
		is.index = 0
	} else {
		if is.random { // Switched TO random mode
			activeManager := is.getActivePermutationManagerUnlocked()
			if activeManager != nil {
				if !is.isFiltered {
					activeManager.SyncNewData()
				}
				shuffledIndex, err := activeManager.GetShuffledIndex(sequentialIndexInList)
				if err == nil {
					newIndex = shuffledIndex
				}
			}
		} else { // Switched TO sequential mode
			newIndex = sequentialIndexInList
		}
		is.index = newIndex
	}
}

// ApplyFilter sets the image state to a filtered view.
func (is *ImageState) ApplyFilter(items scan.FileItems, tag string) {
	is.mu.Lock()
	defer is.mu.Unlock()

	is.filteredImages = items
	is.filteredPermutationManager = scan.NewPermutationManager(&is.filteredImages)
	is.isFiltered = true
	is.currentFilterTag = tag
	is.index = 0 // Always start at the beginning of a new filter
}

// ClearFilter resets the image state to the full, unfiltered view.
// It attempts to find the current image in the main list and set the index
// to maintain the user's position.
func (is *ImageState) ClearFilter(currentPath string) {
	is.mu.Lock()
	defer is.mu.Unlock()

	if !is.isFiltered {
		return
	}

	// Find the sequential index of the current image in the main list
	// before we change the state.
	newIndex := 0 // Default to 0 if not found
	if currentPath != "" {
		sequentialIndexInMainList := -1
		for i, item := range is.images {
			if item.Path == currentPath {
				sequentialIndexInMainList = i
				break
			}
		}

		if sequentialIndexInMainList != -1 {
			if is.random {
				// If random mode is on, find the corresponding shuffled index in the main manager
				if is.permutationManager != nil {
					is.permutationManager.SyncNewData()
					shuffledIndex, err := is.permutationManager.GetShuffledIndex(sequentialIndexInMainList)
					if err == nil {
						newIndex = shuffledIndex
					}
				}
			} else {
				// If not in random mode, the new index is just the sequential one.
				newIndex = sequentialIndexInMainList
			}
		}
	}

	// Now, reset the filter state
	is.isFiltered = false
	is.currentFilterTag = ""
	is.filteredImages = nil
	is.filteredPermutationManager = nil

	// Set the new index
	is.index = newIndex
}

// RemoveImage removes an image by its path from all relevant lists, adjusts the
// internal index, and returns true if the active list became empty.
func (is *ImageState) RemoveImage(path string) (listBecameEmpty bool) {
	is.mu.Lock()
	defer is.mu.Unlock()

	// --- Find the view index of the item to be removed BEFORE any changes ---
	// This is crucial for correctly adjusting the index later.
	viewIndexOfItemToRemove := -1
	countBeforeRemoval := is.getCurrentImageCountUnlocked()
	for i := 0; i < countBeforeRemoval; i++ {
		item, err := is.getItemByViewIndexUnlocked(i)
		if err == nil && item != nil && item.Path == path {
			viewIndexOfItemToRemove = i
			break
		}
	}

	// --- 1. Remove from the main image list (is.images) ---
	originalIndexToRemove := -1
	for i, item := range is.images {
		if item.Path == path {
			originalIndexToRemove = i
			break
		}
	}
	if originalIndexToRemove != -1 {
		is.images = append(is.images[:originalIndexToRemove], is.images[originalIndexToRemove+1:]...)
		if is.permutationManager != nil {
			is.permutationManager.DataRemoved(originalIndexToRemove)
		}
	}

	// --- 2. Remove from the filtered list if active ---
	if is.isFiltered {
		filteredIndexToRemove := -1
		for i, item := range is.filteredImages {
			if item.Path == path {
				filteredIndexToRemove = i
				break
			}
		}
		if filteredIndexToRemove != -1 {
			is.filteredImages = append(is.filteredImages[:filteredIndexToRemove], is.filteredImages[filteredIndexToRemove+1:]...)
			if is.filteredPermutationManager != nil {
				is.filteredPermutationManager.DataRemoved(filteredIndexToRemove)
			}
		}
	}

	// --- 3. Adjust index and determine return values ---
	countAfterRemoval := is.getCurrentImageCountUnlocked()
	if countAfterRemoval == 0 {
		is.index = -1
		return true
	}

	// If the removed item was before the current one in the view, decrement the index.
	if viewIndexOfItemToRemove != -1 && is.index > viewIndexOfItemToRemove {
		is.index--
	}

	// If the index is now out of bounds (e.g., we deleted the last item), clamp it.
	if is.index >= countAfterRemoval {
		is.index = countAfterRemoval - 1
	}
	return false
}

// GetViewportItems returns a slice of ViewportItems representing the current viewport
// for the thumbnail strip, along with the index of the central item within that slice.
func (is *ImageState) GetViewportItems(centerIndex int, windowSize int) ([]ViewportItem, int) {
	is.mu.RLock()
	defer is.mu.RUnlock()

	count := is.getCurrentImageCountUnlocked()
	if count == 0 {
		return []ViewportItem{}, -1
	}

	halfWindow := windowSize / 2
	start := centerIndex - halfWindow
	end := centerIndex + halfWindow

	// Adjust viewport if it goes out of bounds.
	if start < 0 {
		end -= start // equivalent to end += abs(start)
		start = 0
	}
	if end >= count {
		start -= (end - (count - 1))
		end = count - 1
	}
	// Final check in case the list is smaller than the window.
	if start < 0 {
		start = 0
	}

	items := make([]ViewportItem, 0, end-start+1)
	for i := start; i <= end; i++ {
		item, err := is.getItemByViewIndexUnlocked(i)
		if err == nil && item != nil {
			items = append(items, ViewportItem{Item: *item, ViewIndex: i})
		}
	}

	newCenterIndex := centerIndex - start
	return items, newCenterIndex
}

func (is *ImageState) Dump() string {
	s := fmt.Sprintf("ImageState {\nRandom:%v\nIndex:%d\nImages:%d\n}",
		is.random, is.index, len(is.images))

	return s

}
