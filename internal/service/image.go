// Package service provides image loading and metadata extraction services.
package service

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/gif"  // Register GIF decoder
	_ "image/jpeg" // Register JPEG decoder
	_ "image/png"  // Register PNG decoder
	"os"
	"time"

	"github.com/rwcarlsen/goexif/exif"
)

// ImageInfo holds metadata about an image.
type ImageInfo struct {
	Width    int
	Height   int
	Size     int64
	ModTime  time.Time
	EXIFData map[string]string
}

// ImageService provides methods for loading and decoding images.
type ImageService struct{}

// NewImageService creates a new ImageService.
func NewImageService() *ImageService {
	return &ImageService{}
}

// GetImageInfo reads an image file and extracts metadata without decoding the full image,
// which is significantly more performant.
func (is *ImageService) GetImageInfo(path string) (*ImageInfo, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening file: %w", err)
	}
	defer file.Close()

	// Efficiently get image dimensions without decoding the entire image.
	config, _, err := image.DecodeConfig(file)
	if err != nil {
		return nil, fmt.Errorf("decoding image config: %w", err)
	}

	// Reset file pointer to read EXIF data
	if _, err := file.Seek(0, 0); err != nil {
		return nil, fmt.Errorf("seeking file for exif: %w", err)
	}

	exifData, _ := exif.Decode(file) // Ignore error, EXIF might not be present

	fileInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("getting file stats: %w", err)
	}

	info := &ImageInfo{
		Width:    config.Width,
		Height:   config.Height,
		Size:     fileInfo.Size(),
		ModTime:  fileInfo.ModTime(),
		EXIFData: make(map[string]string),
	}

	if exifData != nil {
		// Extract specific EXIF fields
		if camModel, err := exifData.Get(exif.Model); err == nil {
			info.EXIFData["Camera Model"] = camModel.String()
		}
		if fNum, err := exifData.Get(exif.FNumber); err == nil {
			numer, denom, _ := fNum.Rat2(0)
			info.EXIFData["F-Number"] = fmt.Sprintf("f/%.1f", float64(numer)/float64(denom))
		}
		if expTime, err := exifData.Get(exif.ExposureTime); err == nil {
			numer, denom, _ := expTime.Rat2(0)
			info.EXIFData["Exposure Time"] = fmt.Sprintf("%d/%d s", numer, denom)
		}
	}

	return info, nil
}

// GetEmbeddedThumbnail attempts to read an embedded EXIF thumbnail from an image file.
// It returns the decoded thumbnail image or an error if one is not found or cannot be decoded.
func (is *ImageService) GetEmbeddedThumbnail(path string) (image.Image, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening file for thumbnail: %w", err)
	}
	defer file.Close()

	x, err := exif.Decode(file)
	if err != nil {
		return nil, errors.New("no EXIF data found")
	}

	thumbBytes, err := x.JpegThumbnail()
	if err != nil {
		return nil, fmt.Errorf("no JPEG thumbnail in EXIF: %w", err)
	}

	img, _, err := image.Decode(bytes.NewReader(thumbBytes))
	return img, err
}
