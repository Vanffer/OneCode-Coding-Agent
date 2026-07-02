package mcp

import (
	"encoding/json"
	"fmt"
)

type JSONRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id,omitempty"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

type JSONRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func NewRequest(id int64, method string, params interface{}) JSONRPCRequest {
	return JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
}

func DecodeResponse(data []byte) (JSONRPCResponse, error) {
	var resp JSONRPCResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return JSONRPCResponse{}, fmt.Errorf("解析 JSON-RPC 响应失败: %w", err)
	}
	if resp.JSONRPC != "" && resp.JSONRPC != "2.0" {
		return JSONRPCResponse{}, fmt.Errorf("JSON-RPC 版本无效: %s", resp.JSONRPC)
	}
	return resp, nil
}

func ErrorFromResponse(resp JSONRPCResponse) error {
	if resp.Error == nil {
		return nil
	}
	return fmt.Errorf("JSON-RPC 错误 %d: %s", resp.Error.Code, resp.Error.Message)
}

func DecodeResult(resp JSONRPCResponse, result interface{}) error {
	if err := ErrorFromResponse(resp); err != nil {
		return err
	}
	if result == nil {
		return nil
	}
	if len(resp.Result) == 0 {
		return fmt.Errorf("JSON-RPC 响应缺少 result")
	}
	if err := json.Unmarshal(resp.Result, result); err != nil {
		return fmt.Errorf("解析 JSON-RPC result 失败: %w", err)
	}
	return nil
}
