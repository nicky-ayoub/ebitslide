package ui

// InputState holds the polled state of inputs for a single frame.
// This separates input polling from input handling logic.
type InputState struct {
	Quit                bool
	ToggleFullscreen    bool
	ToggleThumbnails    bool
	ToggleSlideshow     bool
	ToggleRandom        bool
	NextImage           bool
	PrevImage           bool
	ResetViewFitHeight  bool
	ResetViewActualSize bool

	// Mouse state
	WheelY          float64
	LeftClickStart  bool // Left mouse button just pressed
	RightClickStart bool // Right mouse button just pressed
	PanActive       bool // Left mouse button is being held down
	MouseX, MouseY  int
}
