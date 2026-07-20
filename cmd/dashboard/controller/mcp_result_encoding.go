package controller

import "encoding/json"

func marshalMCPToolResult(result any) (string, error) {
	if result == nil {
		return "{}", nil
	}
	b, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
