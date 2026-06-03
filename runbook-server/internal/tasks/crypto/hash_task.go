package crypto

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"nudgebee/runbook/internal/tasks/types"
)

type CryptoHashTask struct{}

func (t *CryptoHashTask) GetName() string {
	return "crypto.hash"
}

func (t *CryptoHashTask) GetDescription() string {
	return "Generate a hash digest (MD5, SHA-1, SHA-256, SHA-512)."
}

func (t *CryptoHashTask) GetDisplayName() string {
	return "Hash"
}

func (t *CryptoHashTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"data": {
				Type:        types.PropertyTypeString,
				Description: "The data to hash",
				Required:    true,
			},
			"algorithm": {
				Type:        types.PropertyTypeString,
				Description: "The hashing algorithm (md5, sha1, sha256, sha512)",
				Required:    true,
				Options:     []string{"md5", "sha1", "sha256", "sha512"},
			},
		},
	}
}

func (t *CryptoHashTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"data": {
				Type:        types.PropertyTypeString,
				Description: "The hashed data (hex encoded)",
				Required:    true,
			},
		},
	}
}

func (t *CryptoHashTask) Execute(ctx types.TaskContext, params map[string]any) (any, error) {
	data, ok := params["data"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid data format")
	}

	algo, ok := params["algorithm"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid algorithm format")
	}

	var hash []byte

	switch algo {
	case "md5":
		h := md5.Sum([]byte(data))
		hash = h[:]
	case "sha1":
		h := sha1.Sum([]byte(data))
		hash = h[:]
	case "sha256":
		h := sha256.Sum256([]byte(data))
		hash = h[:]
	case "sha512":
		h := sha512.Sum512([]byte(data))
		hash = h[:]
	default:
		return nil, fmt.Errorf("unsupported algorithm: %s", algo)
	}

	return map[string]any{
		"data": hex.EncodeToString(hash),
	}, nil
}
