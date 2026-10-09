package files

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"mime/multipart"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

func encodePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 7), G: uint8(y * 5), B: 120, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func encodeJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{R: 200, G: uint8(x), B: uint8(y), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

// headerFor wraps raw bytes in a multipart.FileHeader without needing a server.
func headerFor(t *testing.T, filename string, data []byte) *multipart.FileHeader {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="image"; filename="`+filename+`"`)
	h.Set("Content-Type", "application/octet-stream")
	part, err := w.CreatePart(h)
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	reader := multipart.NewReader(&body, w.Boundary())
	form, err := reader.ReadForm(int64(len(body.Bytes())) + 1024)
	if err != nil {
		t.Fatalf("read form: %v", err)
	}
	return form.File["image"][0]
}

// ─── validation ───────────────────────────────────────────────────────────────

func TestValidateUploadAcceptsPNGAndJPEG(t *testing.T) {
	for name, data := range map[string][]byte{
		"photo.png": encodePNG(t, 32, 32),
		"photo.jpg": encodeJPEG(t, 32, 32),
	} {
		if err := ValidateUpload(headerFor(t, name, data), 8); err != nil {
			t.Errorf("%s rejected: %v", name, err)
		}
	}
}

func TestValidateUploadRejectsNonImage(t *testing.T) {
	for name, data := range map[string][]byte{
		"notes.txt":       []byte("this is definitely not an image, just plain text"),
		"empty.jpg":       {},
		"script.jpg":      []byte("#!/bin/sh\necho pwned\n"),
		"truncated.png":   {0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A},
		"pdf-renamed.png": []byte("%PDF-1.7\n1 0 obj\n"),
	} {
		err := ValidateUpload(headerFor(t, name, data), 8)
		if err == nil {
			t.Errorf("%s should be rejected", name)
			continue
		}
		if !isOneOf(err, ErrInvalidMagicBytes, ErrNotDecodable) {
			t.Errorf("%s: unexpected error %v", name, err)
		}
	}
}

func TestValidateUploadRejectsOversizedFile(t *testing.T) {
	data := encodePNG(t, 64, 64)
	// maxMB=0 means the limit is 0 bytes: everything is too large.
	err := ValidateUpload(headerFor(t, "big.png", data), 0)
	if err == nil || !isOneOf(err, ErrFileTooLarge) {
		t.Fatalf("err = %v, want ErrFileTooLarge", err)
	}
}

func TestGetMimeType(t *testing.T) {
	if got := GetMimeType(encodePNG(t, 4, 4)); got != "image/png" {
		t.Errorf("png mime = %q", got)
	}
	if got := GetMimeType(encodeJPEG(t, 4, 4)); got != "image/jpeg" {
		t.Errorf("jpeg mime = %q", got)
	}
	if got := GetMimeType([]byte("hello world")); !strings.HasPrefix(got, "text/plain") {
		t.Errorf("text mime = %q", got)
	}
	if got := GetMimeType(nil); got == "" {
		t.Error("mime detection must never return empty")
	}
}

// ─── storage ──────────────────────────────────────────────────────────────────

type byteReaderFile struct{ *bytes.Reader }

func (byteReaderFile) Close() error { return nil }

func TestSaveUploadStripsMetadataAndStoresJpeg(t *testing.T) {
	dir := t.TempDir()
	pngBytes := encodePNG(t, 48, 48)

	filename, path, err := SaveUpload(byteReaderFile{bytes.NewReader(pngBytes)}, dir)
	if err != nil {
		t.Fatalf("SaveUpload: %v", err)
	}
	if !strings.HasSuffix(filename, ".jpg") {
		t.Errorf("stored filename %q must end in .jpg (EXIF stripped by re-encode)", filename)
	}
	if filepath.Dir(path) != dir {
		t.Errorf("path %q must live inside %q", path, dir)
	}

	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read stored file: %v", err)
	}
	if !bytes.HasPrefix(stored, []byte{0xFF, 0xD8, 0xFF}) {
		t.Error("stored bytes are not a JPEG")
	}
	if bytes.HasPrefix(stored, []byte{0x89, 0x50, 0x4E, 0x47}) {
		t.Error("PNG container was not re-encoded")
	}
	img, _, err := image.Decode(bytes.NewReader(stored))
	if err != nil {
		t.Fatalf("stored file must be decodable: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 48 || b.Dy() != 48 {
		t.Errorf("dimensions = %dx%d, want 48x48", b.Dx(), b.Dy())
	}
}

func TestSaveUploadGeneratesUniqueNames(t *testing.T) {
	dir := t.TempDir()
	seen := map[string]bool{}
	for i := 0; i < 5; i++ {
		name, _, err := SaveUpload(byteReaderFile{bytes.NewReader(encodePNG(t, 8, 8))}, dir)
		if err != nil {
			t.Fatalf("SaveUpload #%d: %v", i, err)
		}
		if seen[name] {
			t.Fatalf("duplicate filename %q — uploads would overwrite each other", name)
		}
		seen[name] = true
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 5 {
		t.Errorf("expected 5 files on disk, got %d", len(entries))
	}
}

func TestSaveUploadRejectsUndecodableInput(t *testing.T) {
	if _, _, err := SaveUpload(byteReaderFile{bytes.NewReader([]byte("not an image"))}, t.TempDir()); err == nil {
		t.Fatal("garbage input must not be written to disk")
	}
}

func isOneOf(err error, targets ...error) bool {
	for _, target := range targets {
		if strings.Contains(err.Error(), target.Error()) {
			return true
		}
	}
	return false
}
