package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
)

type HTTPTransport struct {
	name    string
	url     string
	headers map[string]string
	client  *http.Client
	nextID  int64
}

func NewHTTPTransport(name, url string, headers map[string]string, client *http.Client) *HTTPTransport {
	if client == nil {
		client = http.DefaultClient
	}
	return &HTTPTransport{
		name:    name,
		url:     url,
		headers: headers,
		client:  client,
	}
}

func (t *HTTPTransport) Start(context.Context) error {
	if t.url == "" {
		return fmt.Errorf("MCP HTTP server %s url 为空", t.name)
	}
	return nil
}

func (t *HTTPTransport) Request(ctx context.Context, method string, params interface{}, result interface{}) error {
	id := atomic.AddInt64(&t.nextID, 1)
	request := NewRequest(id, method, params)
	body, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("编码 MCP HTTP 请求失败: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("创建 MCP HTTP 请求失败: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	for key, value := range t.headers {
		httpReq.Header.Set(key, value)
	}

	resp, err := t.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("发送 MCP HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024))
	if err != nil {
		return fmt.Errorf("读取 MCP HTTP 响应失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("MCP HTTP 响应状态异常: %d", resp.StatusCode)
	}

	rpcResp, err := DecodeResponse(data)
	if err != nil {
		return err
	}
	if rpcResp.ID != id {
		return fmt.Errorf("MCP HTTP 响应 id 不匹配: want %d got %d", id, rpcResp.ID)
	}
	return DecodeResult(rpcResp, result)
}

func (t *HTTPTransport) Close() error {
	return nil
}
