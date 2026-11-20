package lsp

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Pool keeps the running servers of one project.
type Pool struct {
	root string

	mu      sync.Mutex
	clients map[string]*poolEntry
	closed  bool

	starts atomic.Uint64
}

type poolEntry struct {
	client   *Client
	lastUsed time.Time
}

// NewPool returns a pool for the project at root with no servers running.
func NewPool(root string) *Pool {
	return &Pool{root: root, clients: map[string]*poolEntry{}}
}

// Definition asks the file's server where a symbol is defined, starting the
// server when it is not running.
func (p *Pool) Definition(ctx context.Context, server Server, document Document, position Position) ([]Location, error) {
	client, err := p.client(ctx, server, serverRoot(p.root, document.Path, server.RootMarkers))
	if err != nil {
		return nil, err
	}
	return client.Definition(ctx, document, position)
}

// DocumentSymbols asks the file's server for its symbols, starting the server
// when it is not running.
func (p *Pool) DocumentSymbols(ctx context.Context, server Server, document Document) ([]Symbol, error) {
	client, err := p.client(ctx, server, serverRoot(p.root, document.Path, server.RootMarkers))
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return client.DocumentSymbols(ctx, document)
}

// DocumentSymbolsIfRunning asks for the file's symbols without starting a
// server. It reports whether it found a running server, even if the request fails.
func (p *Pool) DocumentSymbolsIfRunning(ctx context.Context, server Server, document Document) ([]Symbol, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	client, running := p.runningClient(server, serverRoot(p.root, document.Path, server.RootMarkers))
	if err := ctx.Err(); err != nil {
		return nil, running, err
	}
	if !running {
		return nil, false, nil
	}
	symbols, err := client.DocumentSymbols(ctx, document)
	return symbols, true, err
}

// WorkspaceSymbols asks the server of file for the project's symbols that
// match query, starting the server when it is not running.
func (p *Pool) WorkspaceSymbols(ctx context.Context, server Server, file string, query string) ([]ProjectSymbol, error) {
	client, err := p.client(ctx, server, serverRoot(p.root, file, server.RootMarkers))
	if err != nil {
		return nil, err
	}
	return client.WorkspaceSymbols(ctx, query)
}

// References asks the file's server where the symbol at a position is used,
// starting the server when it is not running.
func (p *Pool) References(ctx context.Context, server Server, document Document, position Position) ([]Location, error) {
	client, err := p.client(ctx, server, serverRoot(p.root, document.Path, server.RootMarkers))
	if err != nil {
		return nil, err
	}
	return client.References(ctx, document, position)
}

// HoverIfRunning asks the file's server about the symbol at a position only
// when the server already runs, as resting the pointer is no reason to start
// one, and reports whether it asked.
func (p *Pool) HoverIfRunning(ctx context.Context, server Server, document Document, position Position) (Hover, bool, error) {
	client, running := p.runningClient(server, serverRoot(p.root, document.Path, server.RootMarkers))
	if !running {
		return Hover{}, false, nil
	}
	hover, err := client.Hover(ctx, document, position)
	return hover, true, err
}

// clientKey tells apart servers by what runs and where it works from.
func clientKey(server Server, root string) string {
	return strings.Join(append([]string{root, server.Program}, server.Args...), "\x00")
}

// client returns a running server. Asking for one that is starting waits for it.
func (p *Pool) client(ctx context.Context, server Server, root string) (*Client, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.closed {
		return nil, ErrServerStopped
	}
	key := clientKey(server, root)
	if entry := p.clients[key]; entry != nil && entry.client.Alive() {
		entry.lastUsed = time.Now()
		return entry.client, nil
	}
	client, err := Start(ctx, server, root)
	if err != nil {
		return nil, err
	}
	p.clients[key] = &poolEntry{client: client, lastUsed: time.Now()}
	p.starts.Add(1)
	return client, nil
}

// runningClient returns a server that already runs, marking it used.
func (p *Pool) runningClient(server Server, root string) (*Client, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry := p.clients[clientKey(server, root)]
	if p.closed || entry == nil || !entry.client.Alive() {
		return nil, false
	}
	entry.lastUsed = time.Now()
	return entry.client, true
}

// CloseIdle ends the servers unused for at least idle.
func (p *Pool) CloseIdle(idle time.Duration) {
	p.mu.Lock()
	leaving := make([]*Client, 0, len(p.clients))
	for name, entry := range p.clients {
		if time.Since(entry.lastUsed) >= idle {
			leaving = append(leaving, entry.client)
			delete(p.clients, name)
		}
	}
	p.mu.Unlock()
	for _, client := range leaving {
		client.Close()
	}
}

// Running returns how many servers are running.
func (p *Pool) Running() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.clients)
}

// StartCount reports how many servers this pool has started, including restarts.
func (p *Pool) StartCount() uint64 {
	return p.starts.Load()
}

// Close ends every server. The pool starts no more.
func (p *Pool) Close() {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	p.CloseIdle(0)
}
