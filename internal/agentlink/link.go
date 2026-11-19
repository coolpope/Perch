// Package agentlink lets a coding agent see what the person is reading in
// Perch and show them a place in the code. The agent starts "perch mcp", which
// speaks the Model Context Protocol on its standard streams and passes each
// call to the running app over a socket only the current user can reach.
package agentlink

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const (
	socketDirName  = "perch"
	socketFileName = "agent.sock"
	// exchangeTimeout bounds one call, which opens at most one file.
	exchangeTimeout time.Duration = 10 * time.Second

	// MaxTourStops keeps a walkthrough short enough to follow.
	MaxTourStops int = 30

	privateDirMode    fs.FileMode = 0o700
	privateSocketMode fs.FileMode = 0o600
)

// ErrNotRunning reports that no Perch is listening on the socket.
var ErrNotRunning = errors.New("perch is not running")

// Method names what a Request asks of the app.
type Method string

const (
	// MethodContext asks what the person is looking at.
	MethodContext Method = "context"
	// MethodShow asks the app to show one or more places in the code.
	MethodShow Method = "show"
)

// Stop is one place to show. Lines count from 1.
type Stop struct {
	// Path is the file, absolute or relative to the agent's folder.
	Path    string `json:"path"`
	Line    int    `json:"line,omitempty"`
	EndLine int    `json:"endLine,omitempty"`
	Note    string `json:"note,omitempty"`
}

// Request is one call from an agent.
type Request struct {
	Method Method `json:"method"`
	// Folder is where the agent works, which picks the project's window.
	Folder string `json:"folder"`
	Stops  []Stop `json:"stops,omitempty"`
}

// Caret is the position of the caret. Lines and columns count from 1, and
// columns count bytes.
type Caret struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

// Selection is the code the person has selected.
type Selection struct {
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
	Text      string `json:"text"`
	// Truncated says that Text holds only the start of a long selection.
	Truncated bool `json:"truncated"`
}

// View says how the current file is showing.
type View string

const (
	// ViewEmpty is a window with no file open.
	ViewEmpty View = "empty"
	// ViewSource is the text of a file.
	ViewSource View = "source"
	// ViewPreview is rendered Markdown.
	ViewPreview View = "preview"
	// ViewImage is a picture.
	ViewImage View = "image"
	// ViewDiff is the review of a file's changes.
	ViewDiff View = "diff"
)

// TourPosition says how far the person has come through a walkthrough.
type TourPosition struct {
	Stop  int `json:"stop"`
	Stops int `json:"stops"`
}

// Context is what the person is looking at.
type Context struct {
	Project string `json:"project"`
	// File is relative to Project, or absolute for a file outside it.
	File      string     `json:"file,omitempty"`
	View      View       `json:"view"`
	Caret     *Caret     `json:"caret,omitempty"`
	Selection *Selection `json:"selection,omitempty"`
	// Unsaved says that the file has edits that are not on disk yet.
	Unsaved bool `json:"unsaved"`
	// Tour is set during a walkthrough of several stops.
	Tour *TourPosition `json:"tour,omitempty"`
}

// Shown is the first place the app has brought into view.
type Shown struct {
	Project string `json:"project"`
	File    string `json:"file"`
	Line    int    `json:"line,omitempty"`
	EndLine int    `json:"endLine,omitempty"`
	// Stops counts the places the person can move through.
	Stops int `json:"stops"`
}

// Reply answers a Request with one of its fields set.
type Reply struct {
	Error   string   `json:"error,omitempty"`
	Context *Context `json:"context,omitempty"`
	Shown   *Shown   `json:"shown,omitempty"`
}

// SocketPath returns where the running app listens.
func SocketPath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("find the folder of the agent socket: %w", err)
	}
	return filepath.Join(dir, socketDirName, socketFileName), nil
}

// Listen opens the socket at path for the current user alone, replacing what
// an earlier run left behind. Only the single running instance may call it.
func Listen(path string) (net.Listener, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, privateDirMode); err != nil {
		return nil, fmt.Errorf("create agent socket folder %q: %w", dir, err)
	}
	if err := os.Chmod(dir, privateDirMode); err != nil {
		return nil, fmt.Errorf("protect agent socket folder %q: %w", dir, err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("remove old agent socket %q: %w", path, err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen on agent socket %q: %w", path, err)
	}
	if err := os.Chmod(path, privateSocketMode); err != nil {
		if closeErr := listener.Close(); closeErr != nil {
			slog.Warn("close agent socket", "path", path, "error", closeErr)
		}
		return nil, fmt.Errorf("protect agent socket %q: %w", path, err)
	}
	return listener, nil
}

// Serve answers calls until ctx ends, which also closes listener. Each call
// has its own connection and goroutine, which end with the answer or the
// exchange timeout.
func Serve(ctx context.Context, listener net.Listener, answer func(Request) Reply) {
	stop := context.AfterFunc(ctx, func() {
		if err := listener.Close(); err != nil {
			slog.Warn("close agent socket", "error", err)
		}
	})
	defer stop()
	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() == nil {
				slog.Warn("agent socket stopped", "error", err)
			}
			return
		}
		go serveCall(connection, answer)
	}
}

func serveCall(connection net.Conn, answer func(Request) Reply) {
	defer closeConnection(connection)
	if err := connection.SetDeadline(time.Now().Add(exchangeTimeout)); err != nil {
		slog.Warn("agent call deadline", "error", err)
		return
	}
	var request Request
	if err := json.NewDecoder(bufio.NewReader(connection)).Decode(&request); err != nil {
		slog.Warn("read agent call", "error", err)
		return
	}
	if err := json.NewEncoder(connection).Encode(answer(request)); err != nil {
		slog.Warn("answer agent call", "method", request.Method, "error", err)
	}
}

func closeConnection(connection net.Conn) {
	if err := connection.Close(); err != nil {
		slog.Warn("close agent call", "error", err)
	}
}

// Ask passes one call to the running app. It returns ErrNotRunning when no app
// is listening.
func Ask(ctx context.Context, socket string, request Request) (Reply, error) {
	ctx, cancel := context.WithTimeout(ctx, exchangeTimeout)
	defer cancel()
	var dialer net.Dialer
	connection, err := dialer.DialContext(ctx, "unix", socket)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
		return Reply{}, fmt.Errorf("%w: nothing listens on %q", ErrNotRunning, socket)
	}
	if err != nil {
		return Reply{}, fmt.Errorf("reach perch on %q: %w", socket, err)
	}
	defer closeConnection(connection)
	if deadline, ok := ctx.Deadline(); ok {
		if err := connection.SetDeadline(deadline); err != nil {
			return Reply{}, fmt.Errorf("set the deadline of a %s call: %w", request.Method, err)
		}
	}
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		return Reply{}, fmt.Errorf("send a %s call to perch: %w", request.Method, err)
	}
	var reply Reply
	if err := json.NewDecoder(bufio.NewReader(connection)).Decode(&reply); err != nil {
		return Reply{}, fmt.Errorf("read the answer to a %s call: %w", request.Method, err)
	}
	return reply, nil
}
