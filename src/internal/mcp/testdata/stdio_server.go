package main

import (
	"bufio"
	"encoding/json"
	"os"
	"time"
)

type request struct {
	JSONRPC string                 `json:"jsonrpc"`
	ID      int64                  `json:"id"`
	Method  string                 `json:"method"`
	Params  map[string]interface{} `json:"params"`
}

func main() {
	if os.Getenv("MCP_EXIT_ON_START") == "1" {
		os.Exit(2)
	}

	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var req request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			continue
		}
		if os.Getenv("MCP_DELAY") != "" {
			time.Sleep(200 * time.Millisecond)
		}
		if os.Getenv("MCP_ERROR") == "1" {
			_ = encoder.Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"error": map[string]interface{}{
					"code":    -32000,
					"message": "forced error",
				},
			})
			continue
		}
		switch req.Method {
		case "initialize":
			_ = encoder.Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]interface{}{
					"protocolVersion": "2025-06-18",
					"capabilities":    map[string]interface{}{},
					"serverInfo": map[string]interface{}{
						"name": "test-stdio",
					},
				},
			})
		case "tools/list":
			_ = encoder.Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]interface{}{
					"tools": []map[string]interface{}{
						{
							"name":        "echo",
							"description": "echo input",
							"inputSchema": map[string]interface{}{
								"type":       "object",
								"properties": map[string]interface{}{},
							},
						},
					},
				},
			})
		case "tools/call":
			_ = encoder.Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]interface{}{
					"content": []map[string]interface{}{
						{"type": "text", "text": "echo ok"},
					},
				},
			})
		}
	}
}
