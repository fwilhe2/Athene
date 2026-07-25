package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
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
// textDocument/completion.
//
// A single reader goroutine owns gopls' stdout and demultiplexes responses to
// per-request channels, so requests are asynchronous and cancellable. That
// matters for completion: the GTK main thread must never block waiting for
// gopls, and a request superseded by further typing is cancelled via
// $/cancelRequest rather than left to time out.

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

// lspDebug turns on gopls' stderr and a trace of its requests to us. Set
// ATHENE_LSP_DEBUG=1 when a completion misbehaves — rejected settings and failed
// package loads are only visible there.
var lspDebug = os.Getenv("ATHENE_LSP_DEBUG") != ""

// Completion trigger kinds (LSP CompletionTriggerKind).
const (
	triggerInvoked   = 1 // explicit request, e.g. Ctrl+Space
	triggerCharacter = 2 // typed a trigger character, e.g. "."
)

// CompletionItem is the subset of the LSP CompletionItem we consume.
type CompletionItem struct {
	Label      string        `json:"label"`
	Kind       int           `json:"kind"`
	Detail     string        `json:"detail"`
	SortText   string        `json:"sortText"`
	FilterText string        `json:"filterText"`
	InsertText string        `json:"insertText"`
	Deprecated bool          `json:"deprecated"`
	TextEdit   *lspTextEdit  `json:"textEdit"`
	AdditEdits []lspTextEdit `json:"additionalTextEdits"`
}

// bestScore ranks a typed prefix against both the label and gopls' filterText —
// which for an unimported symbol is qualified, e.g. label "Sprint" with
// filterText "fmt.Sprint" — and keeps whichever matches better, so neither form
// can hide a good candidate.
func (c CompletionItem) bestScore(prefix string) (int, bool) {
	best, ok := fuzzyScore(prefix, c.Label)
	if c.FilterText != "" && c.FilterText != c.Label {
		if s, alt := fuzzyScore(prefix, c.FilterText); alt && (!ok || s > best) {
			best, ok = s, true
		}
	}
	return best, ok
}

// insertion is the text to put in the buffer when this item is accepted.
// textEdit wins because its newText can be qualified where the label is not:
// completing a bare "Sprin" against unimported fmt yields label "Sprint" but
// newText "fmt.Sprint", and pasting the label alone would not compile.
// The edit's *range* is deliberately ignored — by the time the user accepts,
// they have typed more and the server's range is stale, so the editor replaces
// its own start-of-word-to-caret range instead.
func (c CompletionItem) insertion() string {
	if c.TextEdit != nil && c.TextEdit.NewText != "" {
		return c.TextEdit.NewText
	}
	if c.InsertText != "" {
		return c.InsertText
	}
	return c.Label
}

// isCallable reports whether accepting this item should insert a call's
// parentheses.
func (c CompletionItem) isCallable() bool {
	switch c.Kind {
	case 2, 3, 4: // method, function, constructor
		return strings.HasPrefix(c.Detail, "func")
	}
	return false
}

// takesArgs reports whether the item's signature has at least one parameter,
// which decides where the caret lands after the parens are inserted. gopls spells
// a signature as "func(a int) string", but for an unimported symbol it only says
// `func (from "fmt")` — with no arity to read, assume arguments, since leaving
// the caret between the parens is the cheaper mistake.
func (c CompletionItem) takesArgs() bool {
	if !strings.HasPrefix(c.Detail, "func(") {
		return true
	}
	return !strings.HasPrefix(c.Detail, "func()")
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

	writeMu sync.Mutex // serializes writes to gopls' stdin

	mu       sync.Mutex // guards everything below
	nextID   int
	pending  map[int]chan *rpcMessage
	docURI   string
	version  int
	closed   bool
	closeErr error

	// encoding is the position encoding gopls agreed to: "utf-8" (columns are
	// byte offsets) or "utf-16" (columns are UTF-16 code units, the default).
	encoding string
}

// goplsSettings is the configuration handed to gopls when it asks for it via
// workspace/configuration. Keep every key in `gopls api-json` — gopls rejects
// settings it does not recognise, and options do get retired (completeUnimported
// and deepCompletion became unconditional and were dropped in gopls 0.21).
//
// The one real tuning knob is completionBudget: gopls' 100ms default cuts the
// search scope short, which is exactly how deep candidates (a method on a field
// of a field) go missing. usePlaceholders is off because we do not advertise
// snippet support.
func goplsSettings() map[string]any {
	return map[string]any{
		"matcher":          "Fuzzy",
		"symbolMatcher":    "FastFuzzy",
		"completionBudget": "500ms",
		"usePlaceholders":  false,
	}
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
	// gopls' own log is noise in normal use, but it is the only place server-side
	// errors (bad settings, failed package loads) show up, so keep it reachable.
	cmd.Stderr = io.Discard
	if lspDebug {
		cmd.Stderr = os.Stderr
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &LSPClient{
		cmd:      cmd,
		in:       stdin,
		out:      bufio.NewReader(stdout),
		pending:  map[int]chan *rpcMessage{},
		encoding: "utf-16",
	}
	go c.readLoop()

	rootURI := pathToURI(abs)
	initParams := map[string]any{
		"processId": nil,
		"rootUri":   rootURI,
		"capabilities": map[string]any{
			// Prefer UTF-8 columns: they are plain byte offsets, which is far
			// easier to derive from a GtkTextIter than UTF-16 code units.
			"general": map[string]any{
				"positionEncodings": []string{"utf-8", "utf-16"},
			},
			"workspace": map[string]any{
				"configuration": true,
			},
			"textDocument": map[string]any{
				"synchronization": map[string]any{"didSave": false},
				"completion": map[string]any{
					"contextSupport": true,
					"completionItem": map[string]any{
						// No snippetSupport: the editor inserts plain text and
						// adds a call's parens itself (see acceptCompletion).
						"snippetSupport":      false,
						"deprecatedSupport":   true,
						"documentationFormat": []string{"plaintext"},
					},
				},
			},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := c.request(ctx, "initialize", initParams)
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("gopls initialize: %w", err)
	}
	var initRes struct {
		Capabilities struct {
			PositionEncoding string `json:"positionEncoding"`
		} `json:"capabilities"`
	}
	if json.Unmarshal(res, &initRes) == nil && initRes.Capabilities.PositionEncoding != "" {
		c.mu.Lock()
		c.encoding = initRes.Capabilities.PositionEncoding
		c.mu.Unlock()
	}
	if err := c.notify("initialized", map[string]any{}); err != nil {
		c.Close()
		return nil, err
	}
	// Push our settings too, in case gopls never asks for them.
	_ = c.notify("workspace/didChangeConfiguration", map[string]any{
		"settings": map[string]any{"gopls": goplsSettings()},
	})
	return c, nil
}

func (c *LSPClient) Close() {
	c.mu.Lock()
	already := c.closed
	c.closed = true
	c.mu.Unlock()
	if already {
		return
	}
	if c.in != nil {
		_ = c.notify("shutdown", nil)
		c.in.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
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

// Encoding returns the negotiated position encoding ("utf-8" or "utf-16").
func (c *LSPClient) Encoding() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.encoding
}

// Column converts a zero-based rune column within lineText into the column
// units gopls expects. GtkTextIter counts characters; LSP counts UTF-8 bytes or
// UTF-16 code units, so any non-ASCII earlier on the line shifts the position.
func (c *LSPClient) Column(lineText string, runeCol int) int {
	runes := []rune(lineText)
	if runeCol > len(runes) {
		runeCol = len(runes)
	}
	prefix := string(runes[:runeCol])
	if c.Encoding() == "utf-8" {
		return len(prefix)
	}
	return utf16Len(prefix)
}

// RuneColumn is the inverse of Column: it maps an LSP column back to a rune
// offset within lineText, for turning a server text edit into GtkTextIters.
func (c *LSPClient) RuneColumn(lineText string, col int) int {
	utf8Enc := c.Encoding() == "utf-8"
	n := 0
	for i, r := range []rune(lineText) {
		if n >= col {
			return i
		}
		if utf8Enc {
			n += len(string(r))
		} else if r < 0x10000 {
			n++
		} else {
			n += 2
		}
	}
	return len([]rune(lineText))
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r < 0x10000 {
			n++
		} else {
			n += 2
		}
	}
	return n
}

// Complete requests completions at the given zero-based line and LSP column
// (see Column). trigger is triggerInvoked or triggerCharacter. The call blocks
// until gopls answers, ctx is cancelled, or the connection drops — callers on
// the GTK main thread must run it in a goroutine.
func (c *LSPClient) Complete(ctx context.Context, line, column, trigger int) ([]CompletionItem, error) {
	c.mu.Lock()
	uri := c.docURI
	c.mu.Unlock()
	if uri == "" {
		return nil, fmt.Errorf("no document opened")
	}
	params := map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     lspPosition{Line: line, Character: column},
		"context":      map[string]any{"triggerKind": trigger},
	}
	if trigger == triggerCharacter {
		params["context"] = map[string]any{"triggerKind": trigger, "triggerCharacter": "."}
	}
	res, err := c.request(ctx, "textDocument/completion", params)
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

// sortItems puts the list in gopls' own relevance order, which it encodes as a
// zero-padded rank in sortText.
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

func (c *LSPClient) write(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = fmt.Fprintf(c.in, "Content-Length: %d\r\n\r\n%s", len(data), data)
	return err
}

func (c *LSPClient) notify(method string, params any) error {
	return c.write(rpcOut{JSONRPC: "2.0", Method: method, Params: params})
}

// request writes a request and waits for the matching response. Superseded or
// timed-out requests are cancelled server-side so gopls stops working on them.
func (c *LSPClient) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	if c.closed {
		err := c.closeErr
		c.mu.Unlock()
		if err == nil {
			err = fmt.Errorf("lsp: connection closed")
		}
		return nil, err
	}
	c.nextID++
	id := c.nextID
	ch := make(chan *rpcMessage, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	if err := c.write(rpcOut{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		c.forget(id)
		return nil, err
	}

	select {
	case msg := <-ch:
		if msg.Error != nil {
			return nil, fmt.Errorf("lsp %s: %s", method, msg.Error.Message)
		}
		return msg.Result, nil
	case <-ctx.Done():
		c.forget(id)
		_ = c.notify("$/cancelRequest", map[string]any{"id": id})
		return nil, ctx.Err()
	}
}

func (c *LSPClient) forget(id int) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// readLoop owns gopls' stdout: it routes responses to the waiting request and
// answers server→client requests so gopls never blocks on us.
func (c *LSPClient) readLoop() {
	for {
		msg, err := c.readMessage()
		if err != nil {
			c.failPending(fmt.Errorf("lsp: gopls connection lost: %w", err))
			return
		}
		switch {
		case msg.ID != nil && msg.Method == "":
			id, convErr := strconv.Atoi(strings.TrimSpace(string(*msg.ID)))
			if convErr != nil {
				continue
			}
			c.mu.Lock()
			ch := c.pending[id]
			delete(c.pending, id)
			c.mu.Unlock()
			if ch != nil {
				ch <- msg
			}
		case msg.ID != nil && msg.Method != "":
			c.answerServerRequest(msg)
		default:
			// notification: ignore (diagnostics, progress, logs)
		}
	}
}

// failPending wakes every in-flight request with err, so callers get an error
// instead of hanging when gopls dies.
func (c *LSPClient) failPending(err error) {
	c.mu.Lock()
	c.closed = true
	c.closeErr = err
	pending := c.pending
	c.pending = map[int]chan *rpcMessage{}
	c.mu.Unlock()
	for _, ch := range pending {
		ch <- &rpcMessage{Error: &rpcError{Message: err.Error()}}
	}
}

func (c *LSPClient) answerServerRequest(msg *rpcMessage) {
	if lspDebug {
		fmt.Fprintf(os.Stderr, "lsp: server request %s\n", msg.Method)
	}
	var result any = nil
	if msg.Method == "workspace/configuration" {
		var p struct {
			Items []json.RawMessage `json:"items"`
		}
		_ = json.Unmarshal(msg.Params, &p)
		cfg := make([]map[string]any, len(p.Items))
		for i := range cfg {
			cfg[i] = goplsSettings()
		}
		result = cfg
	}
	var rawID json.RawMessage = *msg.ID
	_ = c.write(rpcResponse{JSONRPC: "2.0", ID: &rawID, Result: result})
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
