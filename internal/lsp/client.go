package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/marukolibro/Perch/internal/proc"
)

const (
	shutdownTimeout = 2 * time.Second
	maxMessageBytes = 64 << 20
)

// ErrServerStopped reports a server that ended before it answered.
var ErrServerStopped = errors.New("language server stopped")

// ErrUnsupported reports a request the server did not declare support for.
var ErrUnsupported = errors.New("language server does not support this request")

// Document is a version of an open file.
type Document struct {
	// Path is absolute.
	Path     string
	Language string
	Version  uint64
	Text     string
}

// Position is a place in a text: a line and a column of UTF-16 units, from 0,
// as the protocol counts them.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range is a span of a text.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Location is a span of a file.
type Location struct {
	// Path is absolute.
	Path  string
	Range Range
}

// UTF16Column converts a byte column of a line to the protocol's column.
func UTF16Column(line string, column int) int {
	units := 0
	for index, char := range line {
		if index >= column {
			break
		}
		units++
		if char > 0xFFFF {
			units++
		}
	}
	return units
}

// ByteColumn converts the protocol's column of a line to a byte column.
func ByteColumn(line string, character int) int {
	units := 0
	for index, char := range line {
		if units >= character {
			return index
		}
		units++
		if char > 0xFFFF {
			units++
		}
	}
	return len(line)
}

// pathToURI returns the file URI of an absolute path. A Windows path starts
// with its drive, which the URI puts after a slash: file:///C:/src.
func pathToURI(path string) string {
	slashed := filepath.ToSlash(path)
	if hasDrive(slashed) {
		slashed = "/" + slashed
	}
	return (&url.URL{Scheme: "file", Path: slashed}).String()
}

// hasDrive reports a path that starts with a Windows drive, as C:/.
func hasDrive(path string) bool {
	return len(path) >= 2 && path[1] == ':' && ('a' <= path[0]|0x20 && path[0]|0x20 <= 'z')
}

func uriToPath(uri string) (string, error) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("read location %q: %w", uri, err)
	}
	if parsed.Scheme != "file" {
		return "", fmt.Errorf("read location %q: not a file", uri)
	}
	path := parsed.Path
	if strings.HasPrefix(path, "/") && hasDrive(path[1:]) {
		path = path[1:]
	}
	return filepath.FromSlash(path), nil
}

// wireMessage is a message of the protocol, in either direction.
type wireMessage struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *wireError       `json:"error,omitempty"`
}

type wireError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// reply is a server's answer to a request.
type reply struct {
	result json.RawMessage
	err    error
}

// Client talks to one running server.
type Client struct {
	command *exec.Cmd
	stdin   io.WriteCloser
	done    chan struct{}

	// Capabilities are set before Start returns and never change, so no lock is needed.
	capabilities Capabilities

	writeMu sync.Mutex

	mu      sync.Mutex
	nextID  int
	pending map[int]chan reply
	opened  map[string]uint64
}

// Start runs a server for the project at root and completes its handshake. The
// goroutine that reads the server ends when the server does.
func Start(ctx context.Context, server Server, root string) (*Client, error) {
	program, err := findProgram(server)
	if err != nil {
		return nil, err
	}
	command := proc.Command(program, server.Args...)
	command.Dir = root
	command.Env = append(os.Environ(), "PATH="+toolPath())
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("start %s: %w", server.Name, err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("start %s: %w", server.Name, err)
	}
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", server.Name, err)
	}
	client := &Client{
		command: command, stdin: stdin, done: make(chan struct{}),
		pending: map[int]chan reply{}, opened: map[string]uint64{},
	}
	go client.readLoop(stdout)
	if err := client.initialize(ctx, root, server.InitializationOptions); err != nil {
		client.Close()
		return nil, fmt.Errorf("start %s: %w", server.Name, err)
	}
	return client, nil
}

// Alive reports whether the server is still running.
func (c *Client) Alive() bool {
	select {
	case <-c.done:
		return false
	default:
		return true
	}
}

func (c *Client) initialize(ctx context.Context, root string, options json.RawMessage) error {
	uri := pathToURI(root)
	fields := map[string]json.RawMessage{
		"processId":        mustRaw(os.Getpid()),
		"rootUri":          mustRaw(uri),
		"clientInfo":       json.RawMessage(`{"name":"perch"}`),
		"workspaceFolders": mustRaw([]map[string]string{{"uri": uri, "name": filepath.Base(root)}}),
		"capabilities": json.RawMessage(`{
			"general": {"positionEncodings": ["utf-16"]},
			"textDocument": {
				"synchronization": {},
				"definition": {"linkSupport": true},
				"references": {},
				"hover": {"contentFormat": ["markdown", "plaintext"]},
				"documentSymbol": {"hierarchicalDocumentSymbolSupport": true}
			},
			"workspace": {"configuration": true, "workspaceFolders": true, "symbol": {}}
		}`),
	}
	if len(options) > 0 {
		fields["initializationOptions"] = options
	}
	params, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	result, err := c.call(ctx, "initialize", params)
	if err != nil {
		return err
	}
	capabilities, err := parseCapabilities(result)
	if err != nil {
		return err
	}
	c.capabilities = capabilities
	return c.notify("initialized", json.RawMessage(`{}`))
}

func mustRaw[T any](value T) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("lsp: encode %T: %v", value, err))
	}
	return data
}

// Definition returns where the symbol at a position of a document is defined.
func (c *Client) Definition(ctx context.Context, document Document, position Position) ([]Location, error) {
	const method = "textDocument/definition"
	if !c.capabilities.Definition {
		return nil, fmt.Errorf("%s: %w", method, ErrUnsupported)
	}
	if err := c.sync(document); err != nil {
		return nil, err
	}
	result, err := c.call(ctx, method, positionParams(document, position))
	if err != nil {
		return nil, err
	}
	return parseLocations(result)
}

// sync tells the server the document's text when it has not seen this version.
func (c *Client) sync(document Document) error {
	uri := pathToURI(document.Path)
	c.mu.Lock()
	seen, opened := c.opened[uri]
	c.opened[uri] = document.Version
	c.mu.Unlock()
	if !opened {
		return c.notify("textDocument/didOpen", mustRaw(map[string]json.RawMessage{
			"textDocument": mustRaw(struct {
				URI        string `json:"uri"`
				LanguageID string `json:"languageId"`
				Version    uint64 `json:"version"`
				Text       string `json:"text"`
			}{uri, document.Language, document.Version, document.Text}),
		}))
	}
	if seen == document.Version {
		return nil
	}
	return c.notify("textDocument/didChange", mustRaw(map[string]json.RawMessage{
		"textDocument": mustRaw(struct {
			URI     string `json:"uri"`
			Version uint64 `json:"version"`
		}{uri, document.Version}),
		"contentChanges": mustRaw([]map[string]string{{"text": document.Text}}),
	}))
}

// wireLocation is a Location or a LocationLink of the protocol.
type wireLocation struct {
	URI                  string `json:"uri"`
	Range                Range  `json:"range"`
	TargetURI            string `json:"targetUri"`
	TargetSelectionRange Range  `json:"targetSelectionRange"`
}

// parseLocations reads an answer that is empty, one location, or a list.
func parseLocations(result json.RawMessage) ([]Location, error) {
	trimmed := bytes.TrimSpace(result)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	var wire []wireLocation
	if trimmed[0] == '{' {
		wire = make([]wireLocation, 1)
		if err := json.Unmarshal(trimmed, &wire[0]); err != nil {
			return nil, fmt.Errorf("read locations: %w", err)
		}
	} else if err := json.Unmarshal(trimmed, &wire); err != nil {
		return nil, fmt.Errorf("read locations: %w", err)
	}
	locations := make([]Location, 0, len(wire))
	for _, item := range wire {
		uri, span := item.URI, item.Range
		if item.TargetURI != "" {
			uri, span = item.TargetURI, item.TargetSelectionRange
		}
		path, err := uriToPath(uri)
		if err != nil {
			return nil, err
		}
		locations = append(locations, Location{Path: path, Range: span})
	}
	return locations, nil
}

func (c *Client) call(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	answer := make(chan reply, 1)
	c.pending[id] = answer
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()
	raw := json.RawMessage(strconv.Itoa(id))
	if err := c.write(wireMessage{JSONRPC: "2.0", ID: &raw, Method: method, Params: params}); err != nil {
		return nil, err
	}
	select {
	case got := <-answer:
		return got.result, got.err
	case <-c.done:
		return nil, fmt.Errorf("%s: %w", method, ErrServerStopped)
	case <-ctx.Done():
		return nil, fmt.Errorf("%s: %w", method, ctx.Err())
	}
}

func (c *Client) notify(method string, params json.RawMessage) error {
	return c.write(wireMessage{JSONRPC: "2.0", Method: method, Params: params})
}

func (c *Client) write(message wireMessage) error {
	body, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("encode %s: %w", message.Method, err)
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := fmt.Fprintf(c.stdin, "Content-Length: %d\r\n\r\n%s", len(body), body); err != nil {
		return fmt.Errorf("send %s: %w: %w", message.Method, ErrServerStopped, err)
	}
	return nil
}

func (c *Client) readLoop(stdout io.Reader) {
	defer close(c.done)
	reader := bufio.NewReader(stdout)
	for {
		body, err := readFrame(reader)
		if err != nil {
			return
		}
		var message wireMessage
		if json.Unmarshal(body, &message) == nil {
			c.handle(message)
		}
	}
}

// readFrame reads one message of the protocol's framing.
func readFrame(reader *bufio.Reader) ([]byte, error) {
	length := 0
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		name, value, found := strings.Cut(line, ":")
		if found && strings.EqualFold(name, "Content-Length") {
			if length, err = strconv.Atoi(strings.TrimSpace(value)); err != nil {
				return nil, fmt.Errorf("read frame: %w", err)
			}
		}
	}
	if length <= 0 || length > maxMessageBytes {
		return nil, fmt.Errorf("read frame: length %d", length)
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, err
	}
	return body, nil
}

func (c *Client) handle(message wireMessage) {
	switch {
	case message.Method != "" && message.ID != nil:
		c.answerServer(message)
	case message.Method == "" && message.ID != nil:
		c.deliver(message)
	}
}

// answerServer answers what a server asks of its client: it gets no settings of
// its own, and every other request succeeds with nothing.
func (c *Client) answerServer(message wireMessage) {
	result := json.RawMessage("null")
	if message.Method == "workspace/configuration" {
		var params struct {
			Items []json.RawMessage `json:"items"`
		}
		if json.Unmarshal(message.Params, &params) == nil {
			result = mustRaw(make([]*struct{}, len(params.Items)))
		}
	}
	// A failed write means the server has gone, which readLoop reports.
	_ = c.write(wireMessage{JSONRPC: "2.0", ID: message.ID, Result: result})
}

func (c *Client) deliver(message wireMessage) {
	id, err := strconv.Atoi(string(*message.ID))
	if err != nil {
		return
	}
	c.mu.Lock()
	answer := c.pending[id]
	c.mu.Unlock()
	if answer == nil {
		return
	}
	if message.Error != nil {
		answer <- reply{err: fmt.Errorf("server error %d: %s", message.Error.Code, message.Error.Message)}
		return
	}
	answer <- reply{result: message.Result}
}

// Close asks the server to leave and ends it if it does not.
func (c *Client) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if _, err := c.call(ctx, "shutdown", nil); err == nil {
		// A server that cannot read this is killed below.
		_ = c.notify("exit", nil)
	}
	_ = c.stdin.Close()
	select {
	case <-c.done:
	case <-ctx.Done():
	}
	_ = c.command.Process.Kill()
	_ = c.command.Wait()
}
