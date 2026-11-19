package agentlink

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type toolAnswer struct {
	Result struct {
		ProtocolVersion string           `json:"protocolVersion"`
		Tools           []toolDefinition `json:"tools"`
		Content         []textContent    `json:"content"`
		IsError         bool             `json:"isError"`
	} `json:"result"`
	Error *rpcError `json:"error"`
}

// agentSession talks to a bridge the way an agent does.
type agentSession struct {
	t       *testing.T
	writer  io.Writer
	replies *bufio.Scanner
}

func startBridge(t *testing.T, socket string, launch func(string) error) agentSession {
	t.Helper()
	agentReads, bridgeWrites := io.Pipe()
	bridgeReads, agentWrites := io.Pipe()
	bridge := Bridge{Socket: socket, Folder: "/work/aurora", Launch: launch}
	go func() {
		err := bridge.Serve(context.Background(), bridgeReads, bridgeWrites)
		_ = bridgeWrites.CloseWithError(err)
	}()
	t.Cleanup(func() { _ = agentWrites.Close() })
	return agentSession{t: t, writer: agentWrites, replies: bufio.NewScanner(agentReads)}
}

func (s agentSession) call(message string) toolAnswer {
	s.t.Helper()
	if _, err := io.WriteString(s.writer, message+"\n"); err != nil {
		s.t.Fatal(err)
	}
	if !s.replies.Scan() {
		s.t.Fatalf("no answer to %s: %v", message, s.replies.Err())
	}
	var answer toolAnswer
	if err := json.Unmarshal(s.replies.Bytes(), &answer); err != nil {
		s.t.Fatal(err)
	}
	return answer
}

// shortSocket returns a socket path within the length Unix allows.
func shortSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "perch")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "link", socketFileName)
}

func TestAgentReachesPerchThroughTheSocket(t *testing.T) {
	socket := shortSocket(t)
	listener, err := Listen(socket)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(socket)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != privateSocketMode {
		t.Errorf("socket mode %o, want %o", mode, privateSocketMode)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	asked := make(chan Request, 3)
	go Serve(ctx, listener, func(request Request) Reply {
		asked <- request
		if request.Method == MethodContext {
			return Reply{Context: &Context{Project: request.Folder, File: "main.go", View: ViewSource}}
		}
		first := request.Stops[0]
		return Reply{Shown: &Shown{Project: request.Folder, File: first.Path, Line: first.Line, EndLine: first.Line, Stops: len(request.Stops)}}
	})
	session := startBridge(t, socket, func(string) error { return errors.New("perch is already running") })

	initialized := session.call(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	if initialized.Result.ProtocolVersion != "2025-06-18" {
		t.Errorf("protocol %q", initialized.Result.ProtocolVersion)
	}
	listed := session.call(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if len(listed.Result.Tools) != 3 {
		t.Fatalf("tools %+v", listed.Result.Tools)
	}

	context := session.call(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"perch_context","arguments":{}}}`)
	if context.Result.IsError || !strings.Contains(context.Result.Content[0].Text, `"file":"main.go"`) {
		t.Errorf("context %+v", context.Result)
	}
	if request := <-asked; request.Folder != "/work/aurora" {
		t.Errorf("asked from %q", request.Folder)
	}

	shown := session.call(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"perch_show","arguments":{"path":"main.go","line":3,"note":"Start here"}}}`)
	if shown.Result.IsError {
		t.Fatalf("show %+v", shown.Result)
	}
	if request := <-asked; len(request.Stops) != 1 || request.Stops[0].Line != 3 || request.Stops[0].Note != "Start here" {
		t.Errorf("show asked %+v", request)
	}

	toured := session.call(`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"perch_tour","arguments":{"stops":[{"path":"a.go","line":1},{"path":"b.go","line":2,"note":"Then this"}]}}}`)
	if toured.Result.IsError || !strings.Contains(toured.Result.Content[0].Text, `"stops":2`) {
		t.Errorf("tour %+v", toured.Result)
	}
	if request := <-asked; len(request.Stops) != 2 || request.Stops[1].Path != "b.go" {
		t.Errorf("tour asked %+v", request)
	}

	unknown := session.call(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"perch_edit","arguments":{}}}`)
	if unknown.Error == nil || unknown.Error.Code != codeInvalidParams {
		t.Errorf("unknown tool %+v", unknown)
	}
}

func TestContextSaysWhenPerchIsNotRunning(t *testing.T) {
	launched := false
	session := startBridge(t, shortSocket(t), func(string) error {
		launched = true
		return nil
	})
	answer := session.call(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"perch_context","arguments":{}}}`)
	if !answer.Result.IsError || !strings.Contains(answer.Result.Content[0].Text, "not running") {
		t.Errorf("answer %+v", answer.Result)
	}
	if launched {
		t.Error("reading the context started Perch")
	}
}
