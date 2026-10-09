// Package files handles multipart upload validation and safe storage.
// Images are re-encoded as JPEG after decoding to strip EXIF/GPS metadata
// before writing to disk. Filenames are UUIDs to prevent path traversal.
package files

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // register PNG decoder
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// Supported magic byte signatures.
var (
	magicJPEG = []byte{0xFF, 0xD8, 0xFF}
	magicPNG  = []byte{0x89, 0x50, 0x4E, 0x47}
)

// ErrInvalidMagicBytes is returned when the uploaded file does not have a
// recognized JPEG or PNG header.
var ErrInvalidMagicBytes = errors.New("files: unsupported file type (must be JPEG or PNG)")

// ErrFileTooLarge is returned when the upload exceeds the caller-specified limit.
var ErrFileTooLarge = errors.New("files: file exceeds maximum allowed size")

// ErrNotDecodable is returned when image.Decode fails on the uploaded bytes.
var ErrNotDecodable = errors.New("files: image cannot be decoded")

// ValidateUpload checks the multipart.FileHeader without consuming the file
// body permanently. It verifies:
//  1. File size is within maxMB.
//  2. Magic bytes are JPEG or PNG.
//  3. The file is decodable by the standard library image package.
func ValidateUpload(fh *multipart.FileHeader, maxMB int) error {
	maxBytes := int64(maxMB) << 20
	if fh.Size > maxBytes {
		return fmt.Errorf("%w: got %.2f MB, limit %d MB",
			ErrFileTooLarge, float64(fh.Size)/float64(1<<20), maxMB)
	}

	f, err := fh.Open()
	if err != nil {
		return fmt.Errorf("files: open upload: %w", err)
	}
	defer f.Close()

	// Read enough bytes for magic check + full decode.
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return fmt.Errorf("files: read upload: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return ErrFileTooLarge
	}

	if !hasValidMagic(data) {
		return ErrInvalidMagicBytes
	}

	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return fmt.Errorf("%w: %v", ErrNotDecodable, err)
	}
	return nil
}

// SaveUpload decodes src, re-encodes as JPEG (stripping EXIF/GPS), and writes
// the result to uploadsDir/<uuid>.jpg.
//
// The src reader position is assumed to be at the beginning (call Seek(0,0) if
// needed). Returns the generated filename and absolute path.
//
// Path traversal safety: the final path is verified to be within uploadsDir
// using filepath.Rel.
func SaveUpload(src multipart.File, uploadsDir string) (filename, path string, err error) {
	data, err := io.ReadAll(src)
	if err != nil {
		return "", "", fmt.Errorf("files: read src: %w", err)
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", "", fmt.Errorf("files: decode image: %w", err)
	}

	// Re-encode as JPEG — drops all EXIF, ICC profile, GPS tags.
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 88}); err != nil {
		return "", "", fmt.Errorf("files: re-encode jpeg: %w", err)
	}

	id, err := uuid.NewRandom()
	if err != nil {
		return "", "", fmt.Errorf("files: generate uuid: %w", err)
	}
	filename = id.String() + ".jpg"

	// Validate path confinement.
	absDir, err := filepath.Abs(uploadsDir)
	if err != nil {
		return "", "", fmt.Errorf("files: abs uploads dir: %w", err)
	}
	destPath := filepath.Join(absDir, filename)
	rel, err := filepath.Rel(absDir, destPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", "", fmt.Errorf("files: path traversal detected: %q", destPath)
	}

	if err := os.MkdirAll(absDir, 0o750); err != nil {
		return "", "", fmt.Errorf("files: create uploads dir: %w", err)
	}

	f, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o640)
	if err != nil {
		return "", "", fmt.Errorf("files: create output file: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(buf.Bytes()); err != nil {
		return "", "", fmt.Errorf("files: write output file: %w", err)
	}
	return filename, destPath, nil
}

// GetMimeType performs a quick magic-byte mime detection for the first 512
// bytes of data. It returns "image/jpeg", "image/png", or the result of the
// standard library http.DetectContentType.
func GetMimeType(data []byte) string {
	if len(data) >= 3 && bytes.HasPrefix(data, magicJPEG) {
		return "image/jpeg"
	}
	if len(data) >= 4 && bytes.HasPrefix(data, magicPNG) {
		return "image/png"
	}
	return http.DetectContentType(data)
}

// hasValidMagic returns true when data starts with a recognised image magic.
func hasValidMagic(data []byte) bool {
	if len(data) >= 4 && bytes.HasPrefix(data, magicPNG) {
		return true
	}
	if len(data) >= 3 && bytes.HasPrefix(data, magicJPEG) {
		return true
	}
	return false
}
