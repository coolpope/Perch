package lsp

import (
	"context"
	"strings"
	"sync"
	"time"
)

// Pool keeps the running servers of one project.
type Pool struct {
	root string

	mu      sync.Mutex
	clients map[string]*poolEntry
	closed  bool
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
	return client.DocumentSymbols(ctx, document)
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

// clientKey tells apart servers by what runs and where it works from.
func clientKey(server Server, root string) string {
	return strings.Join(append([]string{root, server.Program}, server.Args...), "\x00")
}

// client returns a running server. Asking for one that is starting waits for it.
func (p *Pool) client(ctx context.Context, server Server, root string) (*Client, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
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
	return client, nil
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

// Close ends every server. The pool starts no more.
func (p *Pool) Close() {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	p.CloseIdle(0)
}
