package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
)

type rpcEnvelope struct {
	resp JSONRPCResponse
	err  error
}

type StdioTransport struct {
	name    string
	command string
	args    []string
	env     map[string]string

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	done   chan error

	nextID  int64
	pending map[int64]chan rpcEnvelope
	closed  bool

	mu        sync.Mutex
	writeMu   sync.Mutex
	closeOnce sync.Once
}

func NewStdioTransport(name, command string, args []string, env map[string]string) *StdioTransport {
	return &StdioTransport{
		name:    name,
		command: command,
		args:    args,
		env:     env,
		pending: map[int64]chan rpcEnvelope{},
	}
}

func (t *StdioTransport) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if t.command == "" {
		return fmt.Errorf("MCP stdio server %s command 为空", t.name)
	}
	cmd := exec.Command(t.command, t.args...)
	cmd.Env = append(os.Environ(), envMapToList(t.env)...)
	cmd.Stderr = io.Discard

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("创建 MCP stdio stdin 失败: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("创建 MCP stdio stdout 失败: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 MCP stdio server 失败: %w", err)
	}

	t.mu.Lock()
	t.cmd = cmd
	t.stdin = stdin
	t.stdout = stdout
	t.done = make(chan error, 1)
	t.mu.Unlock()

	go t.readLoop(stdout)
	go func() {
		err := cmd.Wait()
		t.done <- err
		t.failPending(fmt.Errorf("MCP stdio server 已退出: %w", err))
	}()

	return nil
}

func (t *StdioTransport) Request(ctx context.Context, method string, params interface{}, result interface{}) error {
	id, ch, err := t.registerPending()
	if err != nil {
		return err
	}

	request := NewRequest(id, method, params)
	data, err := json.Marshal(request)
	if err != nil {
		t.removePending(id)
		return fmt.Errorf("编码 MCP stdio 请求失败: %w", err)
	}
	data = append(data, '\n')

	t.writeMu.Lock()
	_, err = t.stdin.Write(data)
	t.writeMu.Unlock()
	if err != nil {
		t.removePending(id)
		return fmt.Errorf("写入 MCP stdio 请求失败: %w", err)
	}

	select {
	case envelope := <-ch:
		if envelope.err != nil {
			return envelope.err
		}
		return DecodeResult(envelope.resp, result)
	case <-ctx.Done():
		t.removePending(id)
		return ctx.Err()
	}
}

func (t *StdioTransport) Close() error {
	var closeErr error
	t.closeOnce.Do(func() {
		t.failPending(fmt.Errorf("MCP stdio transport 已关闭"))

		t.mu.Lock()
		stdin := t.stdin
		stdout := t.stdout
		cmd := t.cmd
		done := t.done
		t.mu.Unlock()

		if stdin != nil {
			_ = stdin.Close()
		}
		if stdout != nil {
			_ = stdout.Close()
		}
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		if done != nil {
			<-done
		}
	})
	return closeErr
}

func (t *StdioTransport) registerPending() (int64, chan rpcEnvelope, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.stdin == nil {
		return 0, nil, fmt.Errorf("MCP stdio transport 未启动或已关闭")
	}
	t.nextID++
	id := t.nextID
	ch := make(chan rpcEnvelope, 1)
	t.pending[id] = ch
	return id, ch, nil
}

func (t *StdioTransport) removePending(id int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.pending, id)
}

func (t *StdioTransport) readLoop(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		resp, err := DecodeResponse(scanner.Bytes())
		if err != nil {
			t.failPending(err)
			return
		}
		t.deliver(resp)
	}
	if err := scanner.Err(); err != nil {
		t.failPending(fmt.Errorf("读取 MCP stdio 响应失败: %w", err))
	}
}

func (t *StdioTransport) deliver(resp JSONRPCResponse) {
	t.mu.Lock()
	ch, ok := t.pending[resp.ID]
	if ok {
		delete(t.pending, resp.ID)
	}
	t.mu.Unlock()
	if ok {
		ch <- rpcEnvelope{resp: resp}
	}
}

func (t *StdioTransport) failPending(err error) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closed = true
	pending := t.pending
	t.pending = map[int64]chan rpcEnvelope{}
	t.mu.Unlock()

	for _, ch := range pending {
		ch <- rpcEnvelope{err: err}
	}
}

func envMapToList(values map[string]string) []string {
	out := make([]string, 0, len(values))
	for key, value := range values {
		out = append(out, key+"="+value)
	}
	return out
}
