package crypto

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"nudgebee/runbook/internal/tasks/types"
)

type CryptoEncodeTask struct{}

func (t *CryptoEncodeTask) GetName() string {
	return "crypto.encode"
}

func (t *CryptoEncodeTask) GetDescription() string {
	return "Encode data to Base64 or Hex format."
}

func (t *CryptoEncodeTask) GetDisplayName() string {
	return "Encode"
}

func (t *CryptoEncodeTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"data": {
				Type:        types.PropertyTypeString,
				Description: "The data to encode",
				Required:    true,
			},
			"algorithm": {
				Type:        types.PropertyTypeString,
				Description: "The encoding algorithm (base64, hex)",
				Required:    true,
				Options:     []string{"base64", "hex"},
			},
		},
	}
}

func (t *CryptoEncodeTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"data": {
				Type:        types.PropertyTypeString,
				Description: "The encoded data",
				Required:    true,
			},
		},
	}
}

func (t *CryptoEncodeTask) Execute(ctx types.TaskContext, params map[string]any) (any, error) {
	data, ok := params["data"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid data format")
	}

	algo, ok := params["algorithm"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid algorithm format")
	}

	var result string
	switch algo {
	case "base64":
		result = base64.StdEncoding.EncodeToString([]byte(data))
	case "hex":
		result = hex.EncodeToString([]byte(data))
	default:
		return nil, fmt.Errorf("unsupported algorithm: %s", algo)
	}

	return map[string]any{
		"data": result,
	}, nil
}
