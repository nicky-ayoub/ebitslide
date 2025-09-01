package main

import (
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
	currentImagePath string // Track the path of the image in CurrentImage

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
}

func (g *Game) Update() error {
	// Handle quit input first.
	if inpututil.IsKeyJustPressed(ebiten.KeyQ) || inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		return ebiten.Termination
	}

	// Handle fullscreen toggle with F11 key.
	if inpututil.IsKeyJustPressed(ebiten.KeyF11) {
		ebiten.SetFullscreen(!ebiten.IsFullscreen())
	}

	// Toggle thumbnail strip visibility with 'T' key.
	if inpututil.IsKeyJustPressed(ebiten.KeyT) {
		g.thumbnailStripVisible = !g.thumbnailStripVisible
	}

	// This is where game logic should go.
	// We check if the image we should be displaying has changed.
	item := g.imageState.GetCurrentItem()
	if item == nil {
		// No item, so clear the current image
		if g.CurrentImage != nil {
			g.CurrentImage.Deallocate()
			g.CurrentImage = nil
			g.currentImagePath = ""
		}
		return nil
	}

	// If the item's path is different from what we have loaded, load the new one.
	if item.Path != g.currentImagePath {
		// Deallocate previous image to free GPU memory
		if g.CurrentImage != nil {
			g.CurrentImage.Deallocate()
		}

		img, _, err := ebitenutil.NewImageFromFile(item.Path)
		if err != nil {
			log.Printf("Error loading image %s: %v", item.Path, err)
			g.CurrentImage = nil    // Clear image on error
			g.currentImagePath = "" // Reset path
		} else {
			g.CurrentImage = img
			g.currentImagePath = item.Path
			g.resetViewToFitHeight() // Reset view when a new image is loaded
		}
	}

	// --- Viewport (Zoom/Pan) and Navigation Input Handling ---
	g.handleViewportInput()

	// --- Input Handling ---
	imageCount := g.imageState.GetCurrentImageCount()
	if imageCount > 0 {
		// Go to the next image (Right Arrow)
		if inpututil.IsKeyJustPressed(ebiten.KeyRight) {
			currentIndex := g.imageState.GetCurrentIndex()
			nextIndex := (currentIndex + 1) % imageCount
			g.imageState.SetIndex(nextIndex)
		}
		// Go to the previous image (Left Arrow)
		if inpututil.IsKeyJustPressed(ebiten.KeyLeft) {
			currentIndex := g.imageState.GetCurrentIndex()
			// Add imageCount to ensure the result is non-negative before the modulo.
			prevIndex := (currentIndex - 1 + imageCount) % imageCount
			g.imageState.SetIndex(prevIndex)
		}
	}

	// Toggle random mode on/off with the 'R' key.
	if inpututil.IsKeyJustPressed(ebiten.KeyR) {
		g.imageState.ToggleRandomMode(g.currentImagePath)
	}

	// Update the thumbnail strip and handle clicks
	if g.thumbnailStrip != nil && g.thumbnailStripVisible {
		newIndex := g.thumbnailStrip.Update(g.imageState.GetCurrentIndex())
		if newIndex != g.imageState.GetCurrentIndex() {
			g.imageState.SetIndex(newIndex)
		}
	}

	return nil
}

// handleViewportInput manages zoom, pan, and view reset controls.
func (g *Game) handleViewportInput() {
	// Reset view to actual size (1:1)
	if inpututil.IsKeyJustPressed(ebiten.KeyO) {
		g.resetViewActualSize()
	}

	// Reset view to fit screen height
	if inpututil.IsKeyJustPressed(ebiten.KeyF) {
		g.resetViewToFitHeight()
	}

	// --- Zooming with mouse wheel ---
	_, dy := ebiten.Wheel()
	if dy != 0 {
		mx, my := ebiten.CursorPosition()

		// The point on the image under the cursor, before zoom
		imageX := (float64(mx) - g.panX) / g.zoom
		imageY := (float64(my) - g.panY) / g.zoom

		// Apply zoom
		zoomFactor := 1.1
		if dy < 0 {
			g.zoom /= zoomFactor
		} else {
			g.zoom *= zoomFactor
		}

		// Clamp zoom to reasonable limits
		if g.zoom < 0.05 {
			g.zoom = 0.05
		}
		if g.zoom > 20.0 {
			g.zoom = 20.0
		}

		// Adjust pan so the point under the cursor stays in the same screen location
		g.panX = float64(mx) - imageX*g.zoom
		g.panY = float64(my) - imageY*g.zoom
	}

	// --- Panning with mouse drag ---
	// Start panning
	if inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		mx, my := ebiten.CursorPosition()
		// Only start panning if the cursor is over the main image, not the thumbnail strip.
		_, mainImageHeight := g.getMainImageScreenSize()
		if my < mainImageHeight {
			g.isPanning = true
			g.panStartX, g.panStartY = mx, my
			g.panStartPanX, g.panStartPanY = g.panX, g.panY
		}
	}

	// Update panning
	if g.isPanning {
		if ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft) {
			mx, my := ebiten.CursorPosition()
			dx := mx - g.panStartX
			dy := my - g.panStartY
			g.panX = g.panStartPanX + float64(dx)
			g.panY = g.panStartPanY + float64(dy)
		} else {
			// Stop panning when the button is released
			g.isPanning = false
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
	} else {
		ebitenutil.DebugPrint(screen, "No image to display or image failed to load.")
	}
	// Display debug info without spamming the console log
	modeStr := "Sequential"
	if g.imageState.IsRandom() {
		modeStr = "Random"
	}
	ebitenutil.DebugPrint(screen, fmt.Sprintf("Path: %s\nMode: %s\n%s", g.currentImagePath, modeStr, g.imageState.Dump()))

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
			g.AddLogMessage(fmt.Sprint("Sufficient images found [%d]. Starting application...", count))
			return // Exit the wait loop
		}
	}
}

func main() {
	ebiten.SetWindowSize(1920, 980)
	ebiten.SetWindowTitle("Hello, World!")
	game := &Game{
		imageState:            ui.NewImageState(),
		scanCompleteChan:      make(chan bool, 1),
		zoom:                  1.0, // Default zoom, will be reset on first image load
		thumbnailStripVisible: true,
		// thumbnailStrip is initialized after services
	}
	if err := game.initServices(); err != nil {
		log.Fatalf("Failed to initialize services: %v", err)
	}

	// Start initial scan and wait for it to complete or timeout
	go game.runInitialScanAndWait(".")

	// Initialize UI components that depend on services
	game.thumbnailStrip = ui.NewThumbnailStrip(game.imageState, game.ImageService)

	if err := ebiten.RunGame(game); err != nil {
		log.Fatal(err)
	}
}
