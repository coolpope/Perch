package agentlink

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"
	"time"
)

const (
	serverName    = "perch"
	serverVersion = "0.1.0"

	toolContext = "perch_context"
	toolShow    = "perch_show"
	toolTour    = "perch_tour"

	jsonRPCVersion = "2.0"

	codeParseError     int = -32700
	codeMethodNotFound int = -32601
	codeInvalidParams  int = -32602

	maxMessageBytes int = 4 << 20

	launchTimeout  time.Duration = 15 * time.Second
	launchInterval time.Duration = 100 * time.Millisecond

	appBundleMark = ".app/Contents/MacOS/"
)

// protocolVersions are the versions of the protocol this server speaks, newest
// first.
var protocolVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

const serverInstructions = "Perch is the code viewer the person keeps open beside this terminal. " +
	"When they say \"this\", \"here\" or \"these lines\" without naming a place, call perch_context first. " +
	"Use perch_show to put the code you are talking about in front of them, and perch_tour to walk them through several places."

type schemaProperty struct {
	Type        string `json:"type"`
	Description string `json:"description"`
	Minimum     *int   `json:"minimum,omitempty"`
	// Items, MinItems and MaxItems describe an array.
	Items    *inputSchema `json:"items,omitempty"`
	MinItems *int         `json:"minItems,omitempty"`
	MaxItems *int         `json:"maxItems,omitempty"`
}

type inputSchema struct {
	Type                 string                    `json:"type"`
	Properties           map[string]schemaProperty `json:"properties"`
	Required             []string                  `json:"required"`
	AdditionalProperties bool                      `json:"additionalProperties"`
}

type toolAnnotations struct {
	ReadOnlyHint    bool `json:"readOnlyHint"`
	DestructiveHint bool `json:"destructiveHint"`
	OpenWorldHint   bool `json:"openWorldHint"`
}

type toolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema inputSchema     `json:"inputSchema"`
	Annotations toolAnnotations `json:"annotations"`
}

// stopSchema describes one place to show.
func stopSchema() inputSchema {
	firstLine := 1
	return inputSchema{
		Type: "object",
		Properties: map[string]schemaProperty{
			"path":    {Type: "string", Description: "File to show, absolute or relative to the working directory."},
			"line":    {Type: "integer", Description: "First line to select, from 1.", Minimum: &firstLine},
			"endLine": {Type: "integer", Description: "Last line to select, from 1. Needs line.", Minimum: &firstLine},
			"note":    {Type: "string", Description: "One or two sentences on why this code matters, in the person's language."},
		},
		Required: []string{"path"},
	}
}

func toolDefinitions() []toolDefinition {
	fewestStops, mostStops := 1, MaxTourStops
	stop := stopSchema()
	return []toolDefinition{
		{
			Name: toolContext,
			Description: "Read what the person is looking at in Perch: the project, the open file, the caret and " +
				"the selected code. Call it when they refer to code without naming it.",
			InputSchema: inputSchema{Type: "object", Properties: map[string]schemaProperty{}, Required: []string{}},
			Annotations: toolAnnotations{ReadOnlyHint: true},
		},
		{
			Name: toolShow,
			Description: "Open a file of this project in Perch and select a line or a range, with an optional short " +
				"note above the code. It changes no file and leaves the keyboard in the terminal.",
			InputSchema: stop,
		},
		{
			Name: toolTour,
			Description: "Walk the person through several places in this project. Perch shows the first stop, and they " +
				"move through the rest there with Next and Previous, each with its note. Send every stop in one call, " +
				"in the order that makes the code easiest to understand.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProperty{
					"stops": {Type: "array", Description: "The places to show, in reading order.", Items: &stop, MinItems: &fewestStops, MaxItems: &mostStops},
				},
				Required: []string{"stops"},
			},
		},
	}
}

type rpcRequest struct {
	Version string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse[Result any] struct {
	Version string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  *Result         `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type initializeParams struct {
	ProtocolVersion string `json:"protocolVersion"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type toolsCapability struct {
	ListChanged bool `json:"listChanged"`
}

type serverCapabilities struct {
	Tools toolsCapability `json:"tools"`
}

type initializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    serverCapabilities `json:"capabilities"`
	ServerInfo      serverInfo         `json:"serverInfo"`
	Instructions    string             `json:"instructions"`
}

type emptyResult struct{}

type toolsListResult struct {
	Tools []toolDefinition `json:"tools"`
}

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type tourArguments struct {
	Stops []Stop `json:"stops"`
}

type textContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolResult struct {
	Content []textContent `json:"content"`
	IsError bool          `json:"isError"`
}

// Bridge passes the tool calls of one agent to the running app.
type Bridge struct {
	// Socket is where the app listens.
	Socket string
	// Folder is the agent's working directory.
	Folder string
	// Launch starts Perch on a folder when it is not running.
	Launch func(folder string) error
}

// RunMCP serves the agent that started this process on its standard streams
// until the agent closes them.
func RunMCP(ctx context.Context, input io.Reader, output io.Writer) error {
	socket, err := SocketPath()
	if err != nil {
		return err
	}
	folder, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("find the agent's working directory: %w", err)
	}
	bridge := Bridge{Socket: socket, Folder: folder, Launch: launchPerch}
	return bridge.Serve(ctx, input, output)
}

// launchPerch starts the app that holds this executable, detached from the
// agent so that it outlives it.
func launchPerch(folder string) error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find the perch executable: %w", err)
	}
	if bundle, _, inBundle := strings.Cut(executable, appBundleMark); inBundle {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("find the home folder: %w", err)
		}
		output, err := exec.Command("open", "-a", bundle+".app", "--env", "HOME="+home, "--args", folder).CombinedOutput()
		if err != nil {
			return fmt.Errorf("open %q on %q: %w: %s", bundle+".app", folder, err, output)
		}
		return nil
	}
	command := exec.Command(executable, folder)
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		return fmt.Errorf("start %q on %q: %w", executable, folder, err)
	}
	return command.Process.Release()
}

// Serve answers newline-delimited JSON-RPC messages until input ends.
func (b Bridge) Serve(ctx context.Context, input io.Reader, output io.Writer) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 0, bufio.MaxScanTokenSize), maxMessageBytes)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		if err := b.answerMessage(ctx, line, output); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read a message from the agent: %w", err)
	}
	return nil
}

func (b Bridge) answerMessage(ctx context.Context, line []byte, output io.Writer) error {
	var request rpcRequest
	if err := json.Unmarshal(line, &request); err != nil {
		return writeError(output, json.RawMessage("null"), codeParseError, fmt.Sprintf("parse the message: %v", err))
	}
	if len(request.ID) == 0 {
		return nil // Notifications have no answer.
	}
	switch request.Method {
	case "initialize":
		var params initializeParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return writeError(output, request.ID, codeInvalidParams, fmt.Sprintf("read the initialize parameters: %v", err))
		}
		return writeResult(output, request.ID, initializeResult{
			ProtocolVersion: negotiatedVersion(params.ProtocolVersion),
			ServerInfo:      serverInfo{Name: serverName, Version: serverVersion},
			Instructions:    serverInstructions,
		})
	case "ping":
		return writeResult(output, request.ID, emptyResult{})
	case "tools/list":
		return writeResult(output, request.ID, toolsListResult{Tools: toolDefinitions()})
	case "tools/call":
		var params toolCallParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return writeError(output, request.ID, codeInvalidParams, fmt.Sprintf("read the tool call: %v", err))
		}
		if !slices.Contains([]string{toolContext, toolShow, toolTour}, params.Name) {
			return writeError(output, request.ID, codeInvalidParams, fmt.Sprintf("unknown tool %q", params.Name))
		}
		return writeResult(output, request.ID, b.callTool(ctx, params))
	default:
		return writeError(output, request.ID, codeMethodNotFound, fmt.Sprintf("unknown method %q", request.Method))
	}
}

// negotiatedVersion answers with the client's version when this server speaks
// it, and otherwise with the newest one.
func negotiatedVersion(requested string) string {
	if slices.Contains(protocolVersions, requested) {
		return requested
	}
	return protocolVersions[0]
}

func writeResult[Result any](output io.Writer, id json.RawMessage, result Result) error {
	return writeMessage(output, rpcResponse[Result]{Version: jsonRPCVersion, ID: id, Result: &result})
}

func writeError(output io.Writer, id json.RawMessage, code int, message string) error {
	return writeMessage(output, rpcResponse[emptyResult]{Version: jsonRPCVersion, ID: id, Error: &rpcError{Code: code, Message: message}})
}

func writeMessage[Result any](output io.Writer, message rpcResponse[Result]) error {
	data, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("encode a message for the agent: %w", err)
	}
	if _, err := output.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write a message to the agent: %w", err)
	}
	return nil
}

func (b Bridge) callTool(ctx context.Context, params toolCallParams) toolResult {
	switch params.Name {
	case toolContext:
		return b.readContext(ctx)
	case toolShow:
		var stop Stop
		if err := decodeArguments(params, &stop); err != nil {
			return failedTool(err.Error())
		}
		return b.show(ctx, []Stop{stop})
	default:
		var tour tourArguments
		if err := decodeArguments(params, &tour); err != nil {
			return failedTool(err.Error())
		}
		return b.show(ctx, tour.Stops)
	}
}

// decodeArguments reads a tool's arguments, refusing names it does not know.
func decodeArguments[Arguments Stop | tourArguments](params toolCallParams, arguments *Arguments) error {
	decoder := json.NewDecoder(bytes.NewReader(params.Arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(arguments); err != nil {
		return fmt.Errorf("read the arguments of %s: %w", params.Name, err)
	}
	return nil
}

func (b Bridge) readContext(ctx context.Context) toolResult {
	reply, err := Ask(ctx, b.Socket, Request{Method: MethodContext, Folder: b.Folder})
	if errors.Is(err, ErrNotRunning) {
		return failedTool("Perch is not running. Call perch_show to open this project in it.")
	}
	if err != nil {
		return failedTool(err.Error())
	}
	if reply.Error != "" {
		return failedTool(reply.Error)
	}
	if reply.Context == nil {
		return failedTool("Perch did not say what is open. It may be quitting.")
	}
	return jsonTool(*reply.Context)
}

func (b Bridge) show(ctx context.Context, stops []Stop) toolResult {
	if slices.ContainsFunc(stops, func(stop Stop) bool { return stop.Path == "" }) {
		return failedTool("every place to show needs a path")
	}
	reply, err := b.askLaunching(ctx, Request{Method: MethodShow, Folder: b.Folder, Stops: stops})
	if err != nil {
		return failedTool(err.Error())
	}
	if reply.Error != "" {
		return failedTool(reply.Error)
	}
	if reply.Shown == nil {
		return failedTool("Perch did not confirm the file. It may be quitting.")
	}
	return jsonTool(*reply.Shown)
}

// askLaunching passes a call to the app, starting the app when it is not
// running.
func (b Bridge) askLaunching(ctx context.Context, request Request) (Reply, error) {
	reply, err := Ask(ctx, b.Socket, request)
	if !errors.Is(err, ErrNotRunning) {
		return reply, err
	}
	if err := b.Launch(b.Folder); err != nil {
		return Reply{}, fmt.Errorf("start perch: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, launchTimeout)
	defer cancel()
	ticker := time.NewTicker(launchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return Reply{}, fmt.Errorf("perch did not start within %s: %w", launchTimeout, ctx.Err())
		case <-ticker.C:
			reply, err := Ask(ctx, b.Socket, request)
			if !errors.Is(err, ErrNotRunning) {
				return reply, err
			}
		}
	}
}

func failedTool(message string) toolResult {
	return toolResult{Content: []textContent{{Type: "text", Text: message}}, IsError: true}
}

func jsonTool[Value Context | Shown](value Value) toolResult {
	data, err := json.Marshal(value)
	if err != nil {
		return failedTool(fmt.Sprintf("encode the answer: %v", err))
	}
	return toolResult{Content: []textContent{{Type: "text", Text: string(data)}}}
}
