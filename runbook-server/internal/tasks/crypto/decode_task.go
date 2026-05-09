package crypto

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"nudgebee/runbook/internal/tasks/types"
)

type CryptoDecodeTask struct{}

func (t *CryptoDecodeTask) GetName() string {
	return "crypto.decode"
}

func (t *CryptoDecodeTask) GetDescription() string {
	return "Decode data from Base64 or Hex format."
}

func (t *CryptoDecodeTask) GetDisplayName() string {
	return "Decode"
}

func (t *CryptoDecodeTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"data": {
				Type:        types.PropertyTypeString,
				Description: "The data to decode",
				Required:    true,
			},
			"algorithm": {
				Type:        types.PropertyTypeString,
				Description: "The decoding algorithm (base64, hex)",
				Required:    true,
				Options:     []string{"base64", "hex"},
			},
		},
	}
}

func (t *CryptoDecodeTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"data": {
				Type:        types.PropertyTypeString,
				Description: "The decoded data",
				Required:    true,
			},
		},
	}
}

func (t *CryptoDecodeTask) Execute(ctx types.TaskContext, params map[string]any) (any, error) {
	data, ok := params["data"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid data format")
	}

	algo, ok := params["algorithm"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid algorithm format")
	}

	var result []byte
	var err error

	switch algo {
	case "base64":
		result, err = base64.StdEncoding.DecodeString(data)
	case "hex":
		result, err = hex.DecodeString(data)
	default:
		return nil, fmt.Errorf("unsupported algorithm: %s", algo)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to decode: %w", err)
	}

	return map[string]any{
		"data": string(result),
	}, nil
}
