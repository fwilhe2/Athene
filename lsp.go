package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// lsp.go is a small, purpose-built LSP client that speaks just enough of the
// protocol to drive gopls for completion: initialize, didOpen/didChange, and
// textDocument/completion. It is deliberately synchronous — every request is
// issued and awaited under a mutex from the GTK main thread — which keeps the
// design simple and avoids cross-thread marshalling.

type lspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

type lspTextEdit struct {
	Range   lspRange `json:"range"`
	NewText string   `json:"newText"`
}

// CompletionItem is the subset of the LSP CompletionItem we consume.
type CompletionItem struct {
	Label      string       `json:"label"`
	Kind       int          `json:"kind"`
	Detail     string       `json:"detail"`
	SortText   string       `json:"sortText"`
	InsertText string       `json:"insertText"`
	TextEdit   *lspTextEdit `json:"textEdit"`
}

// kindName maps LSP CompletionItemKind to a short human label.
func (c CompletionItem) kindName() string {
	switch c.Kind {
	case 2, 3:
		return "method"
	case 4:
		return "func"
	case 5:
		return "field"
	case 6, 18:
		return "var"
	case 7:
		return "class"
	case 8:
		return "interface"
	case 9:
		return "module"
	case 10:
		return "prop"
	case 14:
		return "keyword"
	case 21:
		return "const"
	case 22:
		return "struct"
	case 23:
		return "event"
	case 25:
		return "type"
	}
	return ""
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcMessage struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *rpcError        `json:"error,omitempty"`
}

// LSPClient wraps a running gopls process.
type LSPClient struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Reader

	mu      sync.Mutex
	nextID  int
	docURI  string
	version int
	ready   bool
}

// StartGopls launches gopls for the given module directory and performs the
// initialize / initialized handshake.
func StartGopls(dir string) (*LSPClient, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("gopls", "serve")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &LSPClient{cmd: cmd, in: stdin, out: bufio.NewReader(stdout)}

	rootURI := pathToURI(abs)
	initParams := map[string]any{
		"processId": nil,
		"rootUri":   rootURI,
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"synchronization": map[string]any{"didSave": false},
				"completion": map[string]any{
					"completionItem": map[string]any{"snippetSupport": false},
					"contextSupport": true,
				},
			},
		},
	}
	if _, err := c.request("initialize", initParams, 20*time.Second); err != nil {
		c.Close()
		return nil, fmt.Errorf("gopls initialize: %w", err)
	}
	if err := c.notify("initialized", map[string]any{}); err != nil {
		c.Close()
		return nil, err
	}
	c.ready = true
	return c, nil
}

func (c *LSPClient) Close() {
	if c.in != nil {
		c.notify("shutdown", nil)
		c.in.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		c.cmd.Process.Kill()
	}
}

// DidOpen registers a document's full text with gopls.
func (c *LSPClient) DidOpen(path, text string) error {
	c.mu.Lock()
	c.docURI = pathToURI(mustAbs(path))
	c.version = 1
	uri := c.docURI
	c.mu.Unlock()
	return c.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        uri,
			"languageId": "go",
			"version":    1,
			"text":       text,
		},
	})
}

// DidChange sends the full new text (we use full-document sync for simplicity).
func (c *LSPClient) DidChange(text string) error {
	c.mu.Lock()
	c.version++
	v := c.version
	uri := c.docURI
	c.mu.Unlock()
	if uri == "" {
		return fmt.Errorf("no document opened")
	}
	return c.notify("textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": uri, "version": v},
		"contentChanges": []map[string]any{{"text": text}},
	})
}

// Complete requests completions at the given zero-based line/character.
func (c *LSPClient) Complete(line, character int) ([]CompletionItem, error) {
	c.mu.Lock()
	uri := c.docURI
	c.mu.Unlock()
	if uri == "" {
		return nil, fmt.Errorf("no document opened")
	}
	res, err := c.request("textDocument/completion", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     lspPosition{Line: line, Character: character},
	}, 90*time.Second)
	if err != nil {
		return nil, err
	}
	// Result may be a CompletionList {items:[...]} or a bare array.
	var list struct {
		Items []CompletionItem `json:"items"`
	}
	if err := json.Unmarshal(res, &list); err == nil && list.Items != nil {
		sortItems(list.Items)
		return list.Items, nil
	}
	var arr []CompletionItem
	if err := json.Unmarshal(res, &arr); err == nil {
		sortItems(arr)
		return arr, nil
	}
	return nil, nil
}

func sortItems(items []CompletionItem) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i].SortText, items[j].SortText
		if a == "" {
			a = items[i].Label
		}
		if b == "" {
			b = items[j].Label
		}
		return a < b
	})
}

// ---- protocol plumbing ----

func (c *LSPClient) writeMessage(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(c.in, "Content-Length: %d\r\n\r\n%s", len(data), data)
	return err
}

func (c *LSPClient) notify(method string, params any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writeMessage(rpcOut{JSONRPC: "2.0", Method: method, Params: params})
}

// request writes a request and reads until the matching response arrives,
// answering any server→client requests encountered along the way so gopls
// does not block. Caller must NOT hold c.mu.
func (c *LSPClient) request(method string, params any, timeout time.Duration) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.nextID++
	id := c.nextID
	if err := c.writeMessage(rpcOut{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		return nil, err
	}

	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("lsp: timeout waiting for %s", method)
		}
		msg, err := c.readMessage()
		if err != nil {
			return nil, err
		}
		switch {
		case msg.ID != nil && msg.Method == "":
			// a response
			if string(*msg.ID) == strconv.Itoa(id) {
				if msg.Error != nil {
					return nil, fmt.Errorf("lsp error %d: %s", msg.Error.Code, msg.Error.Message)
				}
				return msg.Result, nil
			}
		case msg.ID != nil && msg.Method != "":
			// server→client request: answer minimally
			c.answerServerRequest(msg)
		default:
			// notification: ignore (diagnostics, progress, logs)
		}
	}
}

func (c *LSPClient) answerServerRequest(msg *rpcMessage) {
	var result any = nil
	if msg.Method == "workspace/configuration" {
		var p struct {
			Items []json.RawMessage `json:"items"`
		}
		_ = json.Unmarshal(msg.Params, &p)
		cfg := make([]map[string]any, len(p.Items))
		for i := range cfg {
			cfg[i] = map[string]any{}
		}
		result = cfg
	}
	var rawID json.RawMessage = *msg.ID
	_ = c.writeMessage(rpcResponse{JSONRPC: "2.0", ID: &rawID, Result: result})
}

func (c *LSPClient) readMessage() (*rpcMessage, error) {
	var contentLen int
	for {
		line, err := c.out.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break // end of headers
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			v := strings.TrimSpace(line[len("content-length:"):])
			contentLen, _ = strconv.Atoi(v)
		}
	}
	if contentLen <= 0 {
		return nil, fmt.Errorf("lsp: missing content-length")
	}
	body := make([]byte, contentLen)
	if _, err := io.ReadFull(c.out, body); err != nil {
		return nil, err
	}
	var msg rpcMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// rpcOut / rpcResponse are the outbound shapes (ID must serialize as a number
// for requests, omitted for notifications).
type rpcOut struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id"`
	Result  any              `json:"result"`
}

func pathToURI(abs string) string {
	// abs is already absolute; ensure forward slashes and a leading slash.
	p := filepath.ToSlash(abs)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return "file://" + p
}

func mustAbs(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return a
}
