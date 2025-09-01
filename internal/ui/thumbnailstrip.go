package ui

import (
	"container/list"
	"image"
	"image/color"
	"os"
	"sync"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/vector"
	"github.com/nicky-ayoub/ebitslide/internal/service"
)

const (
	viewportWidth      = 11 // Must be an odd number for a clear center
	thumbSize          = 80
	thumbSpacing       = 10
	stripHeight        = thumbSize + 2*thumbSpacing
	thumbnailCacheSize = 200 // Max number of thumbnails to keep in memory
)

// lruEntry is the value stored in the LRU cache's linked list.
type lruEntry struct {
	key   string
	value *ebiten.Image
}

// lruCache is a simple LRU cache for ebiten.Image thumbnails.
// It is not thread-safe and is intended to be used only from the main game thread.
type lruCache struct {
	maxSize int
	ll      *list.List
	cache   map[string]*list.Element
}

// newLRUCache creates a new LRU cache.
func newLRUCache(maxSize int) *lruCache {
	return &lruCache{
		maxSize: maxSize,
		ll:      list.New(),
		cache:   make(map[string]*list.Element),
	}
}

// Put adds a value to the cache, evicting the least recently used item if full.
func (c *lruCache) Put(key string, value *ebiten.Image) {
	if elem, ok := c.cache[key]; ok {
		// Key exists, update value and move to front.
		c.ll.MoveToFront(elem)
		elem.Value.(*lruEntry).value = value
		return
	}

	// New key. Add to front.
	elem := c.ll.PushFront(&lruEntry{key: key, value: value})
	c.cache[key] = elem

	// Evict if cache is over size.
	if c.ll.Len() > c.maxSize {
		elem := c.ll.Back()
		if elem != nil {
			c.ll.Remove(elem)
			entry := elem.Value.(*lruEntry)
			delete(c.cache, entry.key)
			entry.value.Deallocate() // Free VRAM
		}
	}
}

// Get retrieves a value from the cache without changing its recency.
func (c *lruCache) Get(key string) (*ebiten.Image, bool) {
	if elem, ok := c.cache[key]; ok {
		return elem.Value.(*lruEntry).value, true
	}
	return nil, false
}

// Touch marks an item as most recently used by moving it to the front of the list.
func (c *lruCache) Touch(key string) {
	if elem, ok := c.cache[key]; ok {
		c.ll.MoveToFront(elem)
	}
}

// thumbnailJob represents a request to load a thumbnail.
type thumbnailJob struct {
	path string
}

// thumbnailResult holds a decoded image, ready to be converted to an ebiten.Image.
type thumbnailResult struct {
	path string
	img  image.Image
}

// ThumbnailStrip manages the state and rendering of the bottom thumbnail bar.
type ThumbnailStrip struct {
	imageState   *ImageState
	imageService *service.ImageService

	thumbCache    *lruCache
	pendingJobs   map[string]bool
	jobQueue      chan thumbnailJob
	resultQueue   chan thumbnailResult
	pendingJobsMu sync.Mutex

	selectionBox *ebiten.Image
}

// NewThumbnailStrip creates and initializes a new thumbnail strip UI component.
func NewThumbnailStrip(is *ImageState, ivs *service.ImageService) *ThumbnailStrip {
	ts := &ThumbnailStrip{
		imageState:   is,
		imageService: ivs,
		thumbCache:   newLRUCache(thumbnailCacheSize),
		pendingJobs:  make(map[string]bool),
		jobQueue:     make(chan thumbnailJob, 50),
		resultQueue:  make(chan thumbnailResult, 50),
	}

	// Create the selection box image
	ts.selectionBox = ebiten.NewImage(thumbSize, thumbSize)
	borderColor := color.RGBA{R: 0xff, G: 0xff, B: 0, A: 0xff} // Yellow
	vector.StrokeRect(ts.selectionBox, 0, 0, float32(thumbSize), float32(thumbSize), 3, borderColor, false)

	// Start background loader goroutines
	go ts.loader()
	go ts.loader()

	return ts
}

// Height returns the total height of the thumbnail strip.
func (ts *ThumbnailStrip) Height() int {
	return stripHeight
}

// loader is a background worker that processes thumbnail loading jobs.
func (ts *ThumbnailStrip) loader() {
	for job := range ts.jobQueue {
		// Try to get the efficient embedded EXIF thumbnail first.
		img, err := ts.imageService.GetEmbeddedThumbnail(job.path)
		if err != nil {
			// Fallback: load the full image file and decode it.
			file, openErr := os.Open(job.path)
			if openErr != nil {
				ts.pendingJobsMu.Lock()
				delete(ts.pendingJobs, job.path) // Un-pend on error so it can be retried
				ts.pendingJobsMu.Unlock()
				continue // Cannot open, skip.
			}
			decodedImg, _, decodeErr := image.Decode(file)
			file.Close()
			if decodeErr != nil {
				ts.pendingJobsMu.Lock()
				delete(ts.pendingJobs, job.path) // Un-pend on error
				ts.pendingJobsMu.Unlock()
				continue // Cannot decode, skip.
			}
			img = decodedImg
		}

		// Send the decoded standard image back to the main thread for processing.
		ts.resultQueue <- thumbnailResult{path: job.path, img: img}
	}
}

// Update handles click events, processes loaded thumbnails, and queues new ones.
// It returns the new view index if a thumbnail is clicked, otherwise it returns the provided currentIndex.
func (ts *ThumbnailStrip) Update(currentIndex int) int {
	// 1. Process any results that have come back from the loader goroutines.
	// This must be done in the main thread as ebiten.Image creation is not thread-safe.
	processing := true
	for processing {
		select {
		case result := <-ts.resultQueue:
			ebitenImg := ebiten.NewImageFromImage(result.img)
			ts.thumbCache.Put(result.path, ebitenImg)

			ts.pendingJobsMu.Lock()
			delete(ts.pendingJobs, result.path)
			ts.pendingJobsMu.Unlock()
		default:
			processing = false
		}
	}

	// 2. Determine which thumbnails are needed for the current view.
	viewportItems, _ := ts.imageState.GetViewportItems(currentIndex, viewportWidth)

	// 3. Queue jobs for any missing thumbnails and mark visible ones as recently used.
	for _, vpItem := range viewportItems {
		path := vpItem.Item.Path

		_, inCache := ts.thumbCache.Get(path)

		if inCache {
			ts.thumbCache.Touch(path) // Mark as used
			continue
		}

		ts.pendingJobsMu.Lock()
		_, isPending := ts.pendingJobs[path]
		if !isPending {
			ts.pendingJobs[path] = true
			select {
			case ts.jobQueue <- thumbnailJob{path: path}:
			default:
				// Job queue is full, we'll try again on the next frame.
				// To avoid a lock-up, we must release the pendingJobs lock.
				delete(ts.pendingJobs, path)
			}
		}
		ts.pendingJobsMu.Unlock()
	}

	// 4. Handle Mouse Click
	if inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		if len(viewportItems) == 0 {
			return currentIndex // No items to click
		}

		// Re-calculate geometry for hit detection
		screenWidth, screenHeight := ebiten.WindowSize()
		totalWidth := len(viewportItems)*(thumbSize+thumbSpacing) - thumbSpacing
		startX := (screenWidth - totalWidth) / 2
		startY := screenHeight - stripHeight + thumbSpacing

		mouseX, mouseY := ebiten.CursorPosition()

		for i, vpItem := range viewportItems {
			thumbX := startX + i*(thumbSize+thumbSpacing)
			thumbY := startY

			// Check if mouse is within the bounds of this thumbnail slot
			if mouseX >= thumbX && mouseX < thumbX+thumbSize &&
				mouseY >= thumbY && mouseY < thumbY+thumbSize {
				// Click detected on this thumbnail.
				// The view index of this item is stored in vpItem.ViewIndex.
				return vpItem.ViewIndex
			}
		}
	}

	return currentIndex // No click or click outside thumbnails, return original index
}

// Draw renders the thumbnail strip onto the bottom of the screen.
func (ts *ThumbnailStrip) Draw(screen *ebiten.Image) {
	viewportItems, centerIdxInViewport := ts.imageState.GetViewportItems(ts.imageState.GetCurrentIndex(), viewportWidth)
	if len(viewportItems) == 0 {
		return
	}

	// Calculate the total width of the strip to center it.
	totalWidth := len(viewportItems)*(thumbSize+thumbSpacing) - thumbSpacing
	screenWidth, _ := screen.Size()
	startX := (screenWidth - totalWidth) / 2
	startY := screen.Bounds().Dy() - stripHeight + thumbSpacing

	for i, vpItem := range viewportItems {
		thumb, exists := ts.thumbCache.Get(vpItem.Item.Path)
		if !exists {
			continue // Don't draw if not loaded yet.
		}

		op := &ebiten.DrawImageOptions{}

		// Scale the thumbnail to fit the thumbSize box, preserving aspect ratio.
		imgW, imgH := thumb.Size()
		scale := float64(thumbSize) / float64(imgW)
		if hScale := float64(thumbSize) / float64(imgH); hScale < scale {
			scale = hScale
		}
		op.GeoM.Scale(scale, scale)

		// Center the thumbnail within its slot.
		scaledW, scaledH := float64(imgW)*scale, float64(imgH)*scale
		tx := float64(startX+i*(thumbSize+thumbSpacing)) + (thumbSize-scaledW)/2
		ty := float64(startY) + (thumbSize-scaledH)/2
		op.GeoM.Translate(tx, ty)

		screen.DrawImage(thumb, op)

		// Draw selection box over the centered item.
		if i == centerIdxInViewport {
			selOp := &ebiten.DrawImageOptions{}
			selOp.GeoM.Translate(float64(startX+i*(thumbSize+thumbSpacing)), float64(startY))
			screen.DrawImage(ts.selectionBox, selOp)
		}
	}
}
