package model

import (
	"os"
	"path/filepath"
	"testing"
)

var pngHeader = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0}

func TestMediaTypeFromExtension(t *testing.T) {
	cases := map[string]string{
		"a.jpg":  "image/jpeg",
		"a.jpeg": "image/jpeg",
		"a.PNG":  "image/png",
		"a.gif":  "image/gif",
		"a.webp": "image/webp",
		"a.txt":  "",
	}
	for path, want := range cases {
		if got := mediaTypeFromExtension(path); got != want {
			t.Errorf("mediaTypeFromExtension(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestResolveImagePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pic.png")
	if err := os.WriteFile(path, pngHeader, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := ResolveImage(Image(path))
	if err != nil {
		t.Fatalf("ResolveImage: %v", err)
	}
	if res.MediaType != "image/png" {
		t.Errorf("media type = %q", res.MediaType)
	}
	if len(res.Data) != len(pngHeader) {
		t.Errorf("data length = %d", len(res.Data))
	}
	// 12 bytes (PNG signature + 4 zero bytes) encode to exactly 16 base64 chars.
	if res.DataURL() != "data:image/png;base64,iVBORw0KGgoAAAAA" {
		t.Errorf("DataURL = %q", res.DataURL())
	}
}

func TestResolveImageBytesSniffing(t *testing.T) {
	res, err := ResolveImage(ImageBytes(pngHeader, ""))
	if err != nil {
		t.Fatalf("ResolveImage: %v", err)
	}
	if res.MediaType != "image/png" {
		t.Errorf("media type = %q, want image/png (sniffed)", res.MediaType)
	}
}

func TestResolveImageURLPassthrough(t *testing.T) {
	res, err := ResolveImage(ImageURL("https://example.com/a.jpg"))
	if err != nil {
		t.Fatalf("ResolveImage: %v", err)
	}
	if res.URL != "https://example.com/a.jpg" || res.Data != nil {
		t.Errorf("resolved = %+v", res)
	}
}

func TestResolveImageInvalid(t *testing.T) {
	if _, err := ResolveImage(ImagePart{}); err == nil {
		t.Error("empty image part should fail")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("just text"), 0o644); err != nil {
		t.Fatal(err)
	}
	// .txt extension yields no media type and sniffing yields text/plain.
	if _, err := ResolveImage(Image(path)); err == nil {
		t.Error("text file should fail image resolution")
	}
}
