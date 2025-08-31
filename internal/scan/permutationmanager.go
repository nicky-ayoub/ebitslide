package scan // Renamed package to 'scan'

import (
	"fmt"
	"math/rand"
	"sync"
	"time"
)

// PermutationManager manages a slice of FileItems and its shuffled indexes.
// It provides a stable, randomized view of the data without altering the original slice.
type PermutationManager struct {
	mu              sync.RWMutex // Mutex to protect concurrent access to data and maps
	data            *FileItems   // The actual data slice where records (FileItems) are accumulated
	shuffledMap     []int        // Maps a shuffled index to its original index (shuffledMap[shuffledIdx] = originalIdx)
	reverseMap      []int        // Maps an original index to its current shuffled index (reverseMap[originalIdx] = shuffledIdx)
	rng             *rand.Rand   // Random number generator for shuffling
	lastKnownLength int          // Tracks the length of data when maps were last updated
}

// NewPermutationManager creates and initializes a new PermutationManager instance.
// It performs an initial shuffle of the indices of the provided data slice.
func NewPermutationManager(slice *FileItems) *PermutationManager {
	// Use a unique source for the random number generator based on current time
	source := rand.NewSource(time.Now().UnixNano())
	rng := rand.New(source)

	n := len(*slice)
	shuffledMap := make([]int, n)
	reverseMap := make([]int, n)

	// Create a permutation of indices from 0 to n-1.
	for i := 0; i < n; i++ {
		shuffledMap[i] = i
	}
	rng.Shuffle(n, func(i, j int) {
		shuffledMap[i], shuffledMap[j] = shuffledMap[j], shuffledMap[i]
	})

	// Create the reverse map based on the shuffled order.
	for shuffledIdx, originalIdx := range shuffledMap {
		reverseMap[originalIdx] = shuffledIdx
	}

	return &PermutationManager{
		data:            slice,
		shuffledMap:     shuffledMap,
		reverseMap:      reverseMap,
		rng:             rng,
		lastKnownLength: n,
	}
}

// SyncNewData checks if the external data slice has grown and updates the
// index maps accordingly. It identifies new items, creates a permutation of their
// indices, and appends them to the existing shuffled order.
func (im *PermutationManager) SyncNewData() {
	im.mu.Lock()         // Acquire a write lock to protect shared data during modification
	defer im.mu.Unlock() // Ensure the lock is released when the function exits

	currentLength := len(*im.data)
	if currentLength <= im.lastKnownLength {
		return // No new data to process
	}

	// 1. Create a sequence of the *new* original indices.
	numNewItems := currentLength - im.lastKnownLength
	newIndices := make([]int, numNewItems)
	for i := 0; i < numNewItems; i++ {
		newIndices[i] = im.lastKnownLength + i
	}

	// 2. Create a permutation of only these new indices.
	im.rng.Shuffle(len(newIndices), func(i, j int) {
		newIndices[i], newIndices[j] = newIndices[j], newIndices[i]
	})

	// 3. Append the shuffled new indices to the main shuffledMap.
	im.shuffledMap = append(im.shuffledMap, newIndices...)

	// 4. Grow and update the reverseMap.
	im.reverseMap = append(im.reverseMap, make([]int, numNewItems)...) // Grow slice
	for i, originalIdx := range newIndices {
		shuffledIdx := im.lastKnownLength + i
		im.reverseMap[originalIdx] = shuffledIdx
	}

	// 5. Update the last known length for the next sync.
	im.lastKnownLength = currentLength
}

// DataRemoved informs the manager that an item was removed from the underlying
// data slice at a specific index. It updates the internal maps efficiently
// without performing a full re-shuffle.
// The caller is responsible for having already removed the item from the data slice.
func (im *PermutationManager) DataRemoved(originalIndexRemoved int) error {
	im.mu.Lock()
	defer im.mu.Unlock()

	if originalIndexRemoved < 0 || originalIndexRemoved >= im.lastKnownLength {
		return fmt.Errorf("original index %d out of bounds for removal (last known length: %d)", originalIndexRemoved, im.lastKnownLength)
	}

	// 1. Find the shuffled index of the item that was removed.
	shuffledIndexToRemove := im.reverseMap[originalIndexRemoved]

	// 2. Remove the entry from the shuffled map.
	im.shuffledMap = append(im.shuffledMap[:shuffledIndexToRemove], im.shuffledMap[shuffledIndexToRemove+1:]...)

	// 3. The underlying data slice has shrunk. Any original index in our shuffledMap
	// that was greater than the one we removed is now off by one. We must decrement them.
	for i, origIdx := range im.shuffledMap {
		if origIdx > originalIndexRemoved {
			im.shuffledMap[i]--
		}
	}

	// 4. The reverse map is now invalid. Rebuild it from the updated shuffled map.
	// The new length is one less than before.
	newLength := im.lastKnownLength - 1
	im.reverseMap = make([]int, newLength)
	for newShuffledIdx, newOriginalIdx := range im.shuffledMap {
		im.reverseMap[newOriginalIdx] = newShuffledIdx
	}

	// 5. Update the last known length.
	im.lastKnownLength = newLength
	return nil
}

// GetShuffledIndex returns the current shuffled index for a given original index.
func (im *PermutationManager) GetShuffledIndex(originalIndex int) (int, error) {
	im.mu.RLock()         // Acquire a read lock
	defer im.mu.RUnlock() // Release the read lock

	if originalIndex < 0 || originalIndex >= len(im.reverseMap) {
		return -1, fmt.Errorf("original index %d out of bounds (current size: %d)", originalIndex, len(im.reverseMap))
	}
	// Use reverseMap to quickly find the shuffled position of an original index
	return im.reverseMap[originalIndex], nil
}

// GetDataByShuffledIndex retrieves the FileItem record based on its current shuffled index.
func (im *PermutationManager) GetDataByShuffledIndex(shuffledIndex int) (FileItem, error) {
	im.mu.RLock()         // Acquire a read lock
	defer im.mu.RUnlock() // Release the read lock

	if shuffledIndex < 0 || shuffledIndex >= len(im.shuffledMap) {
		return FileItem{}, fmt.Errorf("shuffled index %d out of bounds (current size: %d)", shuffledIndex, len(im.shuffledMap))
	}
	// First, find the original index corresponding to the given shuffled index
	originalIndex := im.shuffledMap[shuffledIndex]
	// Then, retrieve the data from the main data slice using its original index
	return (*im.data)[originalIndex], nil
}

// GetDataByOriginalIndex retrieves the FileItem record based on its original insertion index.
func (im *PermutationManager) GetDataByOriginalIndex(originalIndex int) (FileItem, error) {
	im.mu.RLock()         // Acquire a read lock
	defer im.mu.RUnlock() // Release the read lock

	if originalIndex < 0 || originalIndex >= len(*im.data) {
		return FileItem{}, fmt.Errorf("original index %d out of bounds (current size: %d)", originalIndex, len(*im.data))
	}
	return (*im.data)[originalIndex], nil
}

// Len returns the current number of records managed by the IndexManager.
func (im *PermutationManager) Len() int {
	im.mu.RLock()         // Acquire a read lock
	defer im.mu.RUnlock() // Release the read lock
	return len(im.shuffledMap)
}

// GetCurrentShuffledOrder returns a copy of the current shuffled order.
func (im *PermutationManager) GetCurrentShuffledOrder() []int {
	im.mu.RLock()
	defer im.mu.RUnlock()
	// Return a copy to prevent external modification of the internal slice
	orderCopy := make([]int, len(im.shuffledMap))
	copy(orderCopy, im.shuffledMap)
	return orderCopy
}
