package main

import (
	"flag"
	"fmt"
	"image"
	"log"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/nicky-ayoub/ebitslide/internal/scan"
	"github.com/nicky-ayoub/ebitslide/internal/service"
	"github.com/nicky-ayoub/ebitslide/internal/ui"
)

type Game struct {
	CurrentImage *ebiten.Image
	// currentIndex int // This is now managed by imageState
	currentImagePath    string // Track the path of the image in CurrentImage
	loadingImagePath    string // Track the path of the image currently being loaded
	mainImageJobChan    chan string
	mainImageResultChan chan mainImageResult
	imageToDeallocate   *ebiten.Image

	imageState            *ui.ImageState
	thumbnailStrip        *ui.ThumbnailStrip
	thumbnailStripVisible bool

	ScannerService   *service.ScannerService
	ImageService     *service.ImageService
	scanCompleteChan chan bool

	// Viewport state for zoom and pan
	zoom                       float64
	panX, panY                 float64
	isPanning                  bool
	panStartX, panStartY       int
	panStartPanX, panStartPanY float64

	// Slideshow state
	slideshowActive   bool
	slideshowInterval time.Duration
	slideshowTimer    *time.Timer
}

// mainImageResult holds the result of a background image loading operation.
type mainImageResult struct {
	img  *ebiten.Image
	path string
	err  error
}

// inputState holds the polled state of inputs for a single frame.
// This separates input polling from input handling logic.
type inputState struct {
	quit                bool
	toggleFullscreen    bool
	toggleThumbnails    bool
	toggleSlideshow     bool
	toggleRandom        bool
	nextImage           bool
	prevImage           bool
	resetViewFitHeight  bool
	resetViewActualSize bool

	// Mouse state
	wheelY         float64
	panStart       bool // Left mouse button just pressed
	panActive      bool // Left mouse button is being held down
	mouseX, mouseY int
}

// pollInput gathers all raw input events for the current frame into an inputState struct.
func (g *Game) pollInput() inputState {
	_, wheelY := ebiten.Wheel()
	mx, my := ebiten.CursorPosition()
	return inputState{
		quit:                inpututil.IsKeyJustPressed(ebiten.KeyQ) || inpututil.IsKeyJustPressed(ebiten.KeyEscape),
		toggleFullscreen:    inpututil.IsKeyJustPressed(ebiten.KeyF11),
		toggleThumbnails:    inpututil.IsKeyJustPressed(ebiten.KeyT),
		toggleSlideshow:     inpututil.IsKeyJustPressed(ebiten.KeyS),
		nextImage:           inpututil.IsKeyJustPressed(ebiten.KeyRight),
		prevImage:           inpututil.IsKeyJustPressed(ebiten.KeyLeft),
		toggleRandom:        inpututil.IsKeyJustPressed(ebiten.KeyR),
		resetViewFitHeight:  inpututil.IsKeyJustPressed(ebiten.KeyF),
		resetViewActualSize: inpututil.IsKeyJustPressed(ebiten.KeyO),

		// Mouse state
		wheelY:    wheelY,
		panStart:  inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft),
		panActive: ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft),
		mouseX:    mx,
		mouseY:    my,
	}
}

func (g *Game) Update() error {
	// Deallocate an image that was replaced in the previous frame.
	// This is done at the start of the frame to ensure it's not in use by Draw.
	if g.imageToDeallocate != nil {
		g.imageToDeallocate.Deallocate()
		g.imageToDeallocate = nil
	}

	// 1. Poll all keyboard input at the beginning of the frame.
	input := g.pollInput()

	// 2. Handle non-state-dependent inputs immediately.
	if input.quit {
		return ebiten.Termination
	}
	if input.toggleFullscreen {
		ebiten.SetFullscreen(!ebiten.IsFullscreen())
	}
	if input.toggleThumbnails {
		g.thumbnailStripVisible = !g.thumbnailStripVisible
	}
	if input.toggleSlideshow {
		g.toggleSlideshow()
	}

	// 3. Update game state (slideshow timer)
	if g.slideshowActive && g.slideshowTimer != nil {
		select {
		case <-g.slideshowTimer.C:
			g.navigate(1)
			g.slideshowTimer.Reset(g.slideshowInterval)
		default:
			// Timer has not fired
		}
	}

	// 4. Process results from the background image loader
	select {
	case result := <-g.mainImageResultChan:
		// Only apply the result if it's for the image we are currently waiting for.
		if result.path == g.loadingImagePath {
			if result.err != nil {
				log.Printf("Error loading image %s: %v", result.path, result.err)
				if g.CurrentImage != nil {
					g.imageToDeallocate = g.CurrentImage
				}
				g.CurrentImage = nil
				g.currentImagePath = ""
			} else {
				// Swap the old image with the new one.
				if g.CurrentImage != nil {
					g.imageToDeallocate = g.CurrentImage // Mark old image for deallocation
				}
				g.CurrentImage = result.img
				g.currentImagePath = result.path
				g.resetViewToFitHeight()
			}
			g.loadingImagePath = "" // We're done loading.
		} else if result.img != nil {
			// This is a stale result for an image we no longer want. Deallocate it immediately.
			result.img.Deallocate()
		}
	default:
		// No image loaded this frame.
	}

	// 5. Check for image changes and request a new image to be loaded
	item := g.imageState.GetCurrentItem()
	if item == nil {
		// No item selected, clear everything if there's something to clear.
		if g.CurrentImage != nil || g.loadingImagePath != "" {
			if g.CurrentImage != nil {
				g.imageToDeallocate = g.CurrentImage
			}
			g.CurrentImage = nil
			g.currentImagePath = ""
			g.loadingImagePath = "" // Cancel any load in progress.
		}
	} else if item.Path != g.currentImagePath && item.Path != g.loadingImagePath {
		// A new image needs to be loaded. Keep the old image displayed while the new one loads.
		// Just kick off the load. The result will be handled above.
		g.loadingImagePath = item.Path
		g.mainImageJobChan <- item.Path
	}

	// 6. Handle all state-dependent input (keyboard and mouse).
	g.handleStatefulInput(input)

	// 7. Update UI components.
	if g.thumbnailStrip != nil && g.thumbnailStripVisible {
		newIndex := g.thumbnailStrip.Update(g.imageState.GetCurrentIndex())
		if newIndex != g.imageState.GetCurrentIndex() {
			g.imageState.SetIndex(newIndex)
			g.resetSlideshowTimer()
		}
	}

	return nil
}

// handleStatefulInput manages all input that depends on the current game state,
// such as the loaded image. It uses the pre-polled input state.
func (g *Game) handleStatefulInput(input inputState) {
	// --- Keyboard controls that depend on state ---
	if input.resetViewActualSize {
		g.resetViewActualSize()
	}
	if input.resetViewFitHeight {
		g.resetViewToFitHeight()
	}

	imageCount := g.imageState.GetCurrentImageCount()
	if imageCount > 0 {
		if input.nextImage {
			g.navigate(1)
			g.resetSlideshowTimer()
		}
		if input.prevImage {
			g.navigate(-1)
			g.resetSlideshowTimer()
		}
	}

	if input.toggleRandom {
		g.imageState.ToggleRandomMode(g.currentImagePath)
	}

	// --- Zooming with mouse wheel ---
	if input.wheelY != 0 {
		// The point on the image under the cursor, before zoom
		imageX := (float64(input.mouseX) - g.panX) / g.zoom
		imageY := (float64(input.mouseY) - g.panY) / g.zoom

		// Apply zoom
		zoomFactor := 1.1
		if input.wheelY < 0 {
			g.zoom /= zoomFactor
		} else {
			g.zoom *= zoomFactor
		}

		// Clamp zoom to reasonable limits
		g.zoom = clamp(g.zoom, 0.05, 20.0)

		// Adjust pan so the point under the cursor stays in the same screen location
		g.panX = float64(input.mouseX) - imageX*g.zoom
		g.panY = float64(input.mouseY) - imageY*g.zoom
	}

	// --- Panning with mouse drag ---
	// Start panning
	if input.panStart {
		// Only start panning if the cursor is over the main image, not the thumbnail strip.
		_, mainImageHeight := g.getMainImageScreenSize()
		if input.mouseY < mainImageHeight {
			g.isPanning = true
			g.panStartX, g.panStartY = input.mouseX, input.mouseY
			g.panStartPanX, g.panStartPanY = g.panX, g.panY
		}
	}

	// Update panning
	if g.isPanning {
		if input.panActive {
			dx := input.mouseX - g.panStartX
			dy := input.mouseY - g.panStartY
			g.panX = g.panStartPanX + float64(dx)
			g.panY = g.panStartPanY + float64(dy)
		} else {
			// Stop panning when the button is released
			g.isPanning = false
		}
	}
}

// clamp restricts a value to a given range.
func clamp(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// navigate moves the view index by a given delta, wrapping around the list.
// A positive delta moves forward, a negative delta moves backward.
func (g *Game) navigate(delta int) {
	imageCount := g.imageState.GetCurrentImageCount()
	if imageCount > 0 {
		currentIndex := g.imageState.GetCurrentIndex()
		// The formula `(a % n + n) % n` handles negative numbers correctly for modular arithmetic.
		newIndex := (currentIndex + delta%imageCount + imageCount) % imageCount
		g.imageState.SetIndex(newIndex)
	}
}

// resetSlideshowTimer resets the slideshow timer if slideshow mode is active.
func (g *Game) resetSlideshowTimer() {
	if g.slideshowActive && g.slideshowTimer != nil {
		g.slideshowTimer.Reset(g.slideshowInterval)
	}
}

// toggleSlideshow turns the slideshow mode on or off.
func (g *Game) toggleSlideshow() {
	g.slideshowActive = !g.slideshowActive

	if g.slideshowActive {
		// When slideshow is activated, hide the thumbnail strip for a cleaner view.
		g.thumbnailStripVisible = false
		// Just turned ON.
		// If there's no timer, create one. Then reset it to start the countdown.
		if g.slideshowTimer == nil {
			g.slideshowTimer = time.NewTimer(g.slideshowInterval)
		} else {
			g.slideshowTimer.Reset(g.slideshowInterval)
		}
	} else {
		// Just turned OFF.
		// Stop the timer if it exists.
		if g.slideshowTimer != nil {
			g.slideshowTimer.Stop()
		}
	}
}

// GetImageFullPath returns the full path of the currently displayed image.
func (g *Game) GetImageFullPath() string {
	return g.currentImagePath
}

func (g *Game) Draw(screen *ebiten.Image) {
	// Reserve space for the thumbnail strip at the bottom
	stripHeight := 0
	if g.thumbnailStrip != nil && g.thumbnailStripVisible {
		stripHeight = g.thumbnailStrip.Height()
	}
	mainImageHeight := screen.Bounds().Dy() - stripHeight

	// Create a sub-image for the main drawing area to avoid drawing over the strip area.
	mainImageScreen := screen.SubImage(image.Rect(0, 0, screen.Bounds().Dx(), mainImageHeight)).(*ebiten.Image)

	if g.CurrentImage != nil {
		op := &ebiten.DrawImageOptions{}
		// Apply the current zoom and pan state
		op.GeoM.Scale(g.zoom, g.zoom)
		op.GeoM.Translate(g.panX, g.panY)

		mainImageScreen.DrawImage(g.CurrentImage, op)
	} else if g.loadingImagePath != "" {
		ebitenutil.DebugPrint(screen, fmt.Sprintf("Loading: %s", g.loadingImagePath))
	} else {
		ebitenutil.DebugPrint(screen, "No image selected or image failed to load.")
	}
	// Display debug info without spamming the console log
	modeStr := "Sequential"
	if g.imageState.IsRandom() {
		modeStr = "Random"
	}
	slideshowStr := ""
	if g.slideshowActive {
		slideshowStr = " (Slideshow ON)"
	}
	ebitenutil.DebugPrint(screen, fmt.Sprintf("Path: %s\nMode: %s%s\nIndex: %d\nCount: %d",
		g.currentImagePath,
		modeStr,
		slideshowStr,
		g.imageState.GetCurrentIndex(),
		g.imageState.GetCurrentImageCount()))

	// Draw the thumbnail strip at the bottom
	if g.thumbnailStrip != nil && g.thumbnailStripVisible {
		g.thumbnailStrip.Draw(screen)
	}
}

func (g *Game) Layout(outsideWidth, outsideHeight int) (screenWidth, screenHeight int) {
	// By returning the window's dimensions, we make the logical screen size
	// the same as the window size. This gives us a 1:1 pixel mapping and
	// ensures our scaling calculations in Draw are for the full resolution.
	return outsideWidth, outsideHeight
}

// getMainImageScreenSize is a helper to get the dimensions of the main drawing area.
func (g *Game) getMainImageScreenSize() (int, int) {
	stripHeight := 0
	if g.thumbnailStrip != nil && g.thumbnailStripVisible {
		stripHeight = g.thumbnailStrip.Height()
	}
	w, h := ebiten.WindowSize()
	return w, h - stripHeight
}

// resetViewToFitHeight calculates the zoom and pan to make the image fit the screen height.
func (g *Game) resetViewToFitHeight() {
	if g.CurrentImage == nil {
		return
	}

	// Get dimensions
	screenWidth, screenHeight := g.getMainImageScreenSize()
	imageWidth, imageHeight := g.CurrentImage.Size()

	// Calculate zoom
	g.zoom = float64(screenHeight) / float64(imageHeight)

	// Calculate pan to center horizontally
	scaledWidth := float64(imageWidth) * g.zoom
	g.panX = (float64(screenWidth) - scaledWidth) / 2
	g.panY = 0 // Aligned to top
}

// resetViewActualSize sets zoom to 1:1 and centers the image.
func (g *Game) resetViewActualSize() {
	if g.CurrentImage == nil {
		return
	}
	g.zoom = 1.0

	screenWidth, screenHeight := g.getMainImageScreenSize()
	imageWidth, imageHeight := g.CurrentImage.Size()
	g.panX = (float64(screenWidth) - float64(imageWidth)) / 2
	g.panY = (float64(screenHeight) - float64(imageHeight)) / 2
}

// initServices initializes the database and all backend services.
func (g *Game) initServices() error {
	fileScanner := scan.FileScannerImpl{}
	g.ScannerService = service.NewScannerService(&fileScanner)
	g.ImageService = service.NewImageService()

	return nil
}
func (g *Game) AddLogMessage(msg string) {
	fmt.Println(msg)
}

// mainImageLoader is a background worker that loads full-size images.
func (g *Game) mainImageLoader() {
	for path := range g.mainImageJobChan {
		img, _, err := ebitenutil.NewImageFromFile(path)
		// Send the result back to the main thread.
		g.mainImageResultChan <- mainImageResult{
			img:  img,
			path: path,
			err:  err,
		}
	}
}

// loadImages scans the given root directory for image files in a background goroutine
// and populates the main image list.
func (g *Game) loadImages(root string) {
	// Signal completion when this function exits, no matter how.
	defer func() {
		select {
		case g.scanCompleteChan <- true:
		default:
		}
	}()

	g.imageState.Clear()

	imageChan := g.ScannerService.FileScan.Run(root, g.AddLogMessage)

	const batchSize = 1000
	const batchTimeout = 100 * time.Millisecond

	batch := make(scan.FileItems, 0, batchSize)
	ticker := time.NewTicker(batchTimeout)
	defer ticker.Stop()

	// Loop to process images from the channel
	for {
		select {
		case item, ok := <-imageChan:
			if !ok { // Channel is closed, scanner is done.
				// Add any remaining items in the final batch.
				if len(batch) > 0 {
					g.imageState.AddImages(batch)
					g.imageState.SyncPermutationManager()
				}
				// Finalize and exit the function.
				msg := fmt.Sprintf("Loaded %d images from %s", g.imageState.GetCurrentImageCount(), root)
				g.AddLogMessage(msg)
				return // Exit the function, defer will signal completion.
			}
			batch = append(batch, item)
			if len(batch) >= batchSize {
				g.imageState.AddImages(batch)
				g.imageState.SyncPermutationManager()
				batch = make(scan.FileItems, 0, batchSize) // Reset batch, keeping capacity.
			}
		case <-ticker.C:
			// Timeout reached, add whatever is in the batch to update the UI count.
			if len(batch) > 0 {
				g.imageState.AddImages(batch)
				g.imageState.SyncPermutationManager()
				batch = make(scan.FileItems, 0, batchSize)
			}
		}
	}
}

// runInitialScanAndWait starts the background image scan and waits for it to
// find at least one image or times out.
func (g *Game) runInitialScanAndWait(dir string) {
	go g.loadImages(dir)

	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var count int
	for {
		select {
		case <-g.scanCompleteChan:
			g.AddLogMessage("Initial scan completed.")
			count = g.imageState.GetCurrentImageCount()
			g.AddLogMessage(fmt.Sprintf("Found %d images.", count))
			// Update the label one last time
			return // Exit the wait loop
		case <-timeout.C:
			g.AddLogMessage("Timeout waiting for images to load. Please check the directory.")
			return // Exit the wait loop
		case <-ticker.C:
			// This is our polling tick
			count = g.imageState.GetCurrentImageCount()
		}
		if count >= 100000 {
			g.AddLogMessage(fmt.Sprintf("Sufficient images found [%d]. Starting application...", count))
			return // Exit the wait loop
		}
	}
}

func main() {
	// Define command-line flags
	slideshowInterval := flag.Duration("interval", 3*time.Second, "Slideshow interval duration (e.g., '5s', '1m')")
	imageDirFlag := flag.String("dir", ".", "Directory to scan for images. Can also be provided as a positional argument.")
	flag.Parse()

	// Determine the directory to scan and if slideshow should start automatically.
	dirPath := *imageDirFlag
	dirFlagIsSet := false
	startSlideshow := false
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "dir":
			dirFlagIsSet = true
		case "interval":
			startSlideshow = true
		}
	})

	// If -dir is not used, check for a positional argument.
	if !dirFlagIsSet && flag.NArg() > 0 {
		dirPath = flag.Arg(0)
	}

	ebiten.SetWindowSize(1920, 980)
	ebiten.SetWindowTitle("Hello, World!")
	game := &Game{
		imageState:            ui.NewImageState(),
		scanCompleteChan:      make(chan bool, 1),
		zoom:                  1.0, // Default zoom, will be reset on first image load
		thumbnailStripVisible: true,
		slideshowActive:       false, // Will be activated by toggleSlideshow if needed.
		slideshowInterval:     *slideshowInterval,
		mainImageJobChan:      make(chan string, 1),
		mainImageResultChan:   make(chan mainImageResult, 1),
		imageToDeallocate:     nil,
		// thumbnailStrip is initialized after services
	}
	if err := game.initServices(); err != nil {
		log.Fatalf("Failed to initialize services: %v", err)
	}

	// If starting in slideshow mode, toggle it on, which handles all related state.
	if startSlideshow {
		game.toggleSlideshow()
	}

	// Start the background worker for loading main images.
	go game.mainImageLoader()

	// Start initial scan and wait for it to complete or timeout
	go game.runInitialScanAndWait(dirPath)

	// Initialize UI components that depend on services
	game.thumbnailStrip = ui.NewThumbnailStrip(game.imageState, game.ImageService)

	if err := ebiten.RunGame(game); err != nil {
		log.Fatal(err)
	}
}
