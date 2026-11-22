package attachments

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tmc/langchaingo/llms"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
)

const (
	defaultImageMaxPerMessage = 4
	defaultImageMaxSizeMB     = 1.5
)

var allowedImageMIMETypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
}

// GetImageMaxPerMessage returns the maximum number of images allowed per message.
func GetImageMaxPerMessage() int {
	return config.Config.GetInt("llm_server_image_max_per_message", defaultImageMaxPerMessage)
}

// GetImageMaxSizeMB returns the maximum allowed image size in megabytes.
func GetImageMaxSizeMB() float64 {
	return config.Config.GetFloat64("llm_server_image_max_size_mb", defaultImageMaxSizeMB)
}

// GetAllowedImageMIMETypes returns the sorted list of accepted image MIME types.
// Exposed so callers (e.g. the ai_list_models capability response) can advertise
// the allowlist to clients without duplicating it.
func GetAllowedImageMIMETypes() []string {
	types := make([]string, 0, len(allowedImageMIMETypes))
	for t := range allowedImageMIMETypes {
		types = append(types, t)
	}
	sort.Strings(types)
	return types
}

// ValidateImages checks that the given images conform to all constraints:
// count limit, mutual exclusivity of data/url, MIME allowlist, size limit, and SSRF safety.
// Returns nil if all images are valid, or an error describing the first violation.
//
// Side effect: ValidateImages mutates the input slice. For data-only inputs it sets
// MIMEType from sniffed bytes. For URL inputs it fetches the image, replaces URL with
// inline base64 data, and sets MIMEType from the response. Callers that need the
// original payload preserved must pass a copy.
//
// URL inputs are fetched concurrently with a bounded per-image timeout to limit
// the total wall-clock cost on the request hot path.
func ValidateImages(ctx context.Context, images []ImageAttachment) error {
	if len(images) == 0 {
		return nil
	}

	maxCount := GetImageMaxPerMessage()
	if maxCount > 0 && len(images) > maxCount {
		return fmt.Errorf("too many images: %d provided, maximum is %d", len(images), maxCount)
	}

	maxSizeBytes := int(GetImageMaxSizeMB() * 1024 * 1024)

	// First pass: validate data-only images and check exclusivity. Cheap, sequential.
	urlIndices := make([]int, 0)
	for i := range images {
		img := &images[i]
		hasData := img.Data != ""
		hasURL := img.URL != ""

		if hasData == hasURL {
			return fmt.Errorf("image[%d]: exactly one of data or url must be set", i)
		}

		if hasData {
			raw := stripDataURIPrefix(img.Data)
			decoded, err := decodeBase64(raw)
			if err != nil {
				return fmt.Errorf("image[%d]: invalid base64 data: %w", i, err)
			}
			if len(decoded) > maxSizeBytes {
				return fmt.Errorf("image[%d]: decoded size %d bytes exceeds maximum %d bytes", i, len(decoded), maxSizeBytes)
			}

			detected := detectMIMEType(decoded)
			if img.MIMEType == "" {
				img.MIMEType = detected
			} else if img.MIMEType != detected {
				return fmt.Errorf("image[%d]: declared mime_type %q does not match detected %q", i, img.MIMEType, detected)
			}

			if !allowedImageMIMETypes[img.MIMEType] {
				return fmt.Errorf("image[%d]: unsupported mime_type %q, allowed: image/png, image/jpeg", i, img.MIMEType)
			}
		} else {
			urlIndices = append(urlIndices, i)
		}
	}

	// Second pass: fetch URL images concurrently. Each fetch is bounded by
	// urlFetchTimeout so the request goroutine is never blocked beyond that
	// regardless of how many images are submitted.
	if len(urlIndices) > 0 {
		var wg sync.WaitGroup
		errs := make([]error, len(urlIndices))
		for k, idx := range urlIndices {
			wg.Add(1)
			go func(slot int, imgIdx int) {
				defer wg.Done()
				img := &images[imgIdx]
				mimeType, data, err := common.FetchImageSafely(ctx, img.URL, common.SafeFetchOptions{
					MaxSizeBytes:        int64(maxSizeBytes),
					Timeout:             urlFetchTimeout,
					AllowedMIMEPrefixes: []string{"image/"},
				})
				if err != nil {
					errs[slot] = fmt.Errorf("image[%d]: %w", imgIdx, err)
					return
				}
				if img.MIMEType != "" && img.MIMEType != mimeType {
					errs[slot] = fmt.Errorf("image[%d]: declared mime_type %q does not match downloaded %q", imgIdx, img.MIMEType, mimeType)
					return
				}
				if !allowedImageMIMETypes[mimeType] {
					errs[slot] = fmt.Errorf("image[%d]: unsupported mime_type %q, allowed: image/png, image/jpeg", imgIdx, mimeType)
					return
				}
				img.MIMEType = mimeType
				img.Data = base64.StdEncoding.EncodeToString(data)
				img.URL = ""
			}(k, idx)
		}
		wg.Wait()
		for _, e := range errs {
			if e != nil {
				return e
			}
		}
	}

	return nil
}

const urlFetchTimeout = 8 * time.Second

// detectMIMEType sniffs the actual MIME type from raw image bytes.
func detectMIMEType(data []byte) string {
	ct := http.DetectContentType(data)
	// DetectContentType may return params like "text/plain; charset=utf-8", strip them
	if idx := strings.Index(ct, ";"); idx != -1 {
		ct = ct[:idx]
	}
	return strings.TrimSpace(ct)
}

// decodeBase64 attempts to decode base64 data, trying standard encoding first,
// then URL-safe encoding, and finally with padding normalization.
// This handles input from frontends that may use URL-safe alphabet or omit padding.
func decodeBase64(data string) ([]byte, error) {
	// Try standard encoding first (most common)
	if decoded, err := base64.StdEncoding.DecodeString(data); err == nil {
		return decoded, nil
	}
	// Try URL-safe encoding (some frontends use - and _ instead of + and /)
	if decoded, err := base64.URLEncoding.DecodeString(data); err == nil {
		return decoded, nil
	}
	// Try without padding (common in web APIs)
	if decoded, err := base64.RawStdEncoding.DecodeString(data); err == nil {
		return decoded, nil
	}
	if decoded, err := base64.RawURLEncoding.DecodeString(data); err == nil {
		return decoded, nil
	}
	return nil, fmt.Errorf("data is not valid base64 in any supported encoding")
}

// stripDataURIPrefix removes a data URI prefix from base64 data.
// Handles: "data:image/png;base64,<b64>", "data:image/png;<b64>", or raw "<b64>".
func stripDataURIPrefix(data string) string {
	// data:image/png;base64,iVBOR...
	if idx := strings.Index(data, "base64,"); idx != -1 {
		return data[idx+7:]
	}
	// data:image/png;iVBOR...
	if strings.HasPrefix(data, "data:") {
		if idx := strings.Index(data, ";"); idx != -1 {
			return data[idx+1:]
		}
	}
	return data
}

// ImageAttachmentToContentPart converts an ImageAttachment to a langchaingo ContentPart.
// For base64 data: decodes and returns BinaryContent.
// For URL: returns ImageURLContent.
// Returns a text fallback if conversion fails.
func ImageAttachmentToContentPart(img ImageAttachment) llms.ContentPart {
	if img.Data != "" {
		raw := stripDataURIPrefix(img.Data)
		decoded, err := decodeBase64(raw)
		if err != nil {
			slog.Warn("image: failed to decode base64 image data", "error", err)
			return llms.TextContent{Text: "[Image could not be decoded]"}
		}
		mimeType := img.MIMEType
		if mimeType == "" {
			mimeType = detectMIMEType(decoded)
		}
		return llms.BinaryContent{
			MIMEType: mimeType,
			Data:     decoded,
		}
	}
	if img.URL != "" {
		return llms.ImageURLContent{URL: img.URL}
	}
	return llms.TextContent{Text: "[Image could not be decoded]"}
}

// AppendImagesToLastHumanMessage appends image content parts to the last human message in the list.
// If no human message exists, a new one is added. This is the shared helper used by all planners.
func AppendImagesToLastHumanMessage(mcList []llms.MessageContent, images []ImageAttachment) []llms.MessageContent {
	if len(images) == 0 {
		return mcList
	}

	// Convert images to content parts
	imageParts := make([]llms.ContentPart, 0, len(images))
	for _, img := range images {
		imageParts = append(imageParts, ImageAttachmentToContentPart(img))
	}

	// Find the last human message and append image parts to it
	for i := len(mcList) - 1; i >= 0; i-- {
		if mcList[i].Role == llms.ChatMessageTypeHuman {
			mcList[i].Parts = append(mcList[i].Parts, imageParts...)
			return mcList
		}
	}

	// No human message found — create one with images
	mcList = append(mcList, llms.MessageContent{
		Role:  llms.ChatMessageTypeHuman,
		Parts: imageParts,
	})
	return mcList
}

// ContentHashForImage computes the same content-addressed hash used when the
// attachment was persisted (see AttachmentDAO.SaveAttachments), so a request
// image can be matched back to its stored attachment record. Returns "" for
// an image with neither Data nor URL set.
func ContentHashForImage(img ImageAttachment) string {
	if img.Data != "" {
		return computeContentHash([]byte(stripDataURIPrefix(img.Data)))
	}
	if img.URL != "" {
		return computeContentHash([]byte(img.URL))
	}
	return ""
}

// ImageAttachment represents an image attached to an agent request.
// Exactly one of Data or URL must be set.
type ImageAttachment struct {
	Data     string `json:"data,omitempty"`      // Base64-encoded image data
	URL      string `json:"url,omitempty"`       // URL pointing to the image
	MIMEType string `json:"mime_type,omitempty"` // MIME type — only image/png and image/jpeg are accepted
}
