package core

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Image creates an ImagePart from a local file path or, if ref starts with
// http:// or https://, a remote URL.
func Image(ref string) ImagePart {
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return ImageURL(ref)
	}
	return ImagePart{Path: ref}
}

// ImageURL creates an ImagePart from a remote URL. The URL is passed through
// to the API untouched; no bytes are downloaded locally.
func ImageURL(url string) ImagePart {
	return ImagePart{URL: url}
}

// ImageBytes creates an ImagePart from raw image bytes. mediaType may be
// empty, in which case it is sniffed from the data.
func ImageBytes(data []byte, mediaType string) ImagePart {
	return ImagePart{Data: data, MediaType: mediaType}
}

// resolvedImage is an ImagePart after local resolution.
type resolvedImage struct {
	// URL is non-empty when the image is remote.
	URL string
	// Data is non-nil for local images.
	Data []byte
	// MediaType is the detected or given MIME type, e.g. "image/png".
	MediaType string
}

// resolveImage loads the image bytes and determines its MIME type. Resolution
// is lazy: it happens once per request, at provider conversion time.
func resolveImage(p ImagePart) (resolvedImage, error) {
	if p.URL != "" {
		return resolvedImage{URL: p.URL}, nil
	}
	if p.Data == nil && p.Path == "" {
		return resolvedImage{}, fmt.Errorf("callable: image part has no path, url or data")
	}

	var data []byte
	var err error
	mediaType := p.MediaType
	if p.Data != nil {
		data = p.Data
	} else {
		data, err = os.ReadFile(p.Path)
		if err != nil {
			return resolvedImage{}, fmt.Errorf("callable: read image %s: %w", p.Path, err)
		}
	}
	if mediaType == "" && p.Path != "" {
		mediaType = mediaTypeFromExtension(p.Path)
	}
	if mediaType == "" {
		mediaType = http.DetectContentType(data)
	}
	if !strings.HasPrefix(mediaType, "image/") {
		// http.DetectContentType returns "application/octet-stream" for
		// formats it cannot identify.
		return resolvedImage{}, fmt.Errorf("callable: unsupported image media type %q (supported: jpeg, png, gif, webp)", mediaType)
	}
	return resolvedImage{Data: data, MediaType: mediaType}, nil
}

// dataURL renders the image as a data: URL for OpenAI-style providers.
func (r resolvedImage) dataURL() string {
	return "data:" + r.MediaType + ";base64," + base64.StdEncoding.EncodeToString(r.Data)
}

func mediaTypeFromExtension(path string) string {
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(path), ".")) {
	case "jpg", "jpeg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	default:
		return ""
	}
}
