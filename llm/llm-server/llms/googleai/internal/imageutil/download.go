package imageutil

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// downloadImageData downloads the content from the given URL and returns the
// image type and data. The image type is the second part of the response's
// MIME (e.g. "png" from "image/png").
func DownloadImageData(url string) (string, []byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", nil, fmt.Errorf("failed to fetch image from url: %w", err)
	}
	defer func() {
		errClose := resp.Body.Close()
		if errClose != nil {
			// Optionally log the error, or handle as needed
			fmt.Printf("error closing response body: %v\n", errClose)
		}
	}()

	urlData, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, fmt.Errorf("failed to read image bytes: %w", err)
	}

	mimeType := resp.Header.Get("Content-Type")

	parts := strings.Split(mimeType, "/")
	if len(parts) != 2 {
		return "", nil, fmt.Errorf("invalid mime type %v", mimeType)
	}

	return parts[1], urlData, nil
}
