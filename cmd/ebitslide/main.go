package main

import (
	"fmt"
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

	imageState *ui.ImageState

	ScannerService   *service.ScannerService
	scanCompleteChan chan bool
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
		}
	}

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

	return nil
}

// GetImageFullPath returns the full path of the currently displayed image.
func (g *Game) GetImageFullPath() string {
	return g.currentImagePath
}

func (g *Game) Draw(screen *ebiten.Image) {
	if g.CurrentImage != nil {
		// Get logical screen and image dimensions.
		screenWidth, screenHeight := screen.Size()
		imageWidth, imageHeight := g.CurrentImage.Size()

		// Calculate the scaling factor to fit the image within the screen while preserving aspect ratio.
		// This is often called "letterboxing" or "pillarboxing".
		scaleX := float64(screenWidth) / float64(imageWidth)
		scaleY := float64(screenHeight) / float64(imageHeight)
		scale := scaleX // Assume we scale by width.
		if scaleY < scaleX {
			scale = scaleY // If scaling by height is more restrictive, use that.
		}

		// Calculate the new dimensions of the scaled image.
		scaledWidth := float64(imageWidth) * scale
		scaledHeight := float64(imageHeight) * scale

		// Calculate the top-left position to center the image on the screen.
		x := (float64(screenWidth) - scaledWidth) / 2
		y := (float64(screenHeight) - scaledHeight) / 2

		op := &ebiten.DrawImageOptions{}
		op.GeoM.Scale(scale, scale) // Apply scaling.
		op.GeoM.Translate(x, y)     // Apply translation to center.
		screen.DrawImage(g.CurrentImage, op)
	} else {
		ebitenutil.DebugPrint(screen, "No image to display or image failed to load.")
	}
	// Display debug info without spamming the console log
	modeStr := "Sequential"
	if g.imageState.IsRandom() {
		modeStr = "Random"
	}
	ebitenutil.DebugPrint(screen, fmt.Sprintf("Path: %s\nMode: %s\n%s", g.currentImagePath, modeStr, g.imageState.Dump()))
}

func (g *Game) Layout(outsideWidth, outsideHeight int) (screenWidth, screenHeight int) {
	// By returning the window's dimensions, we make the logical screen size
	// the same as the window size. This gives us a 1:1 pixel mapping and
	// ensures our scaling calculations in Draw are for the full resolution.
	return outsideWidth, outsideHeight
}

// initServices initializes the database and all backend services.
func (g *Game) initServices() error {
	fileScanner := scan.FileScannerImpl{}
	g.ScannerService = service.NewScannerService(&fileScanner)
	//g.ImageService = service.NewImageService()

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
	ebiten.SetWindowSize(1920, 1080)
	ebiten.SetWindowTitle("Hello, World!")
	game := &Game{
		imageState:       ui.NewImageState(),
		scanCompleteChan: make(chan bool, 1),
	}
	if err := game.initServices(); err != nil {
		log.Fatalf("Failed to initialize services: %v", err)
	}

	// Start initial scan and wait for it to complete or timeout
	go game.runInitialScanAndWait(".")

	if err := ebiten.RunGame(game); err != nil {
		log.Fatal(err)
	}
}
