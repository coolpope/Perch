package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

// SymbolKind is what a symbol is, as the protocol numbers its kinds.
type SymbolKind int

// The kinds of symbols Perch names. Other protocol kinds read as other.
const (
	SymbolClass         SymbolKind = 5
	SymbolMethod        SymbolKind = 6
	SymbolProperty      SymbolKind = 7
	SymbolField         SymbolKind = 8
	SymbolConstructor   SymbolKind = 9
	SymbolEnum          SymbolKind = 10
	SymbolInterface     SymbolKind = 11
	SymbolFunction      SymbolKind = 12
	SymbolVariable      SymbolKind = 13
	SymbolConstant      SymbolKind = 14
	SymbolEnumMember    SymbolKind = 22
	SymbolStruct        SymbolKind = 23
	SymbolEvent         SymbolKind = 24
	SymbolOperator      SymbolKind = 25
	SymbolTypeParameter SymbolKind = 26
)

// Symbol is one symbol of a document, with its container when it is nested.
type Symbol struct {
	Name string
	Kind SymbolKind
	// Container is the name of the symbol that holds this one, empty for a
	// top-level symbol.
	Container string
	// Range is the whole symbol, and SelectionRange its name.
	Range          Range
	SelectionRange Range
}

// wireSymbol reads both reply shapes of textDocument/documentSymbol:
// DocumentSymbol, which nests children, and SymbolInformation, which names
// its container and locates itself in a file.
type wireSymbol struct {
	Name           string              `json:"name"`
	Kind           SymbolKind          `json:"kind"`
	Range          *Range              `json:"range"`
	SelectionRange *Range              `json:"selectionRange"`
	Children       []wireSymbol        `json:"children"`
	Location       *wireSymbolLocation `json:"location"`
	ContainerName  string              `json:"containerName"`
}

type wireSymbolLocation struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// DocumentSymbols returns the symbols of a document. It sends the text of
// document, so that unsaved edits count.
func (c *Client) DocumentSymbols(ctx context.Context, document Document) ([]Symbol, error) {
	const method = "textDocument/documentSymbol"
	if !c.capabilities.DocumentSymbol {
		return nil, fmt.Errorf("%s: %w", method, ErrUnsupported)
	}
	if err := c.sync(document); err != nil {
		return nil, err
	}
	params := mustRaw(map[string]json.RawMessage{
		"textDocument": mustRaw(map[string]string{"uri": pathToURI(document.Path)}),
	})
	result, err := c.call(ctx, method, params)
	if err != nil {
		return nil, err
	}
	return parseSymbols(result)
}

// ProjectSymbol is a symbol found anywhere in a server's workspace.
type ProjectSymbol struct {
	Name      string
	Kind      SymbolKind
	Container string
	// Path is absolute, and Range the symbol's place in the file, empty when
	// the server names the file alone.
	Path  string
	Range Range
}

// wireProjectSymbol reads both reply shapes of workspace/symbol:
// SymbolInformation, and WorkspaceSymbol, whose location may lack a range.
type wireProjectSymbol struct {
	Name          string     `json:"name"`
	Kind          SymbolKind `json:"kind"`
	ContainerName string     `json:"containerName"`
	Location      struct {
		URI   string `json:"uri"`
		Range *Range `json:"range"`
	} `json:"location"`
}

// WorkspaceSymbols asks the server for the symbols of its workspace that
// match query.
func (c *Client) WorkspaceSymbols(ctx context.Context, query string) ([]ProjectSymbol, error) {
	const method = "workspace/symbol"
	if !c.capabilities.WorkspaceSymbol {
		return nil, fmt.Errorf("%s: %w", method, ErrUnsupported)
	}
	result, err := c.call(ctx, method, mustRaw(map[string]string{"query": query}))
	if err != nil {
		return nil, err
	}
	return parseProjectSymbols(result)
}

func parseProjectSymbols(result json.RawMessage) ([]ProjectSymbol, error) {
	trimmed := bytes.TrimSpace(result)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	var wire []wireProjectSymbol
	if err := json.Unmarshal(trimmed, &wire); err != nil {
		return nil, fmt.Errorf("read workspace symbols: %w", err)
	}
	symbols := make([]ProjectSymbol, 0, len(wire))
	for _, symbol := range wire {
		path, err := uriToPath(symbol.Location.URI)
		if err != nil {
			return nil, fmt.Errorf("read workspace symbol %q: %w", symbol.Name, err)
		}
		found := ProjectSymbol{Name: symbol.Name, Kind: symbol.Kind, Container: symbol.ContainerName, Path: path}
		if symbol.Location.Range != nil {
			found.Range = *symbol.Location.Range
		}
		symbols = append(symbols, found)
	}
	return symbols, nil
}

// parseSymbols reads a documentSymbol reply of either shape, flattening the
// descendants after their container.
func parseSymbols(result json.RawMessage) ([]Symbol, error) {
	trimmed := bytes.TrimSpace(result)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	var wire []wireSymbol
	if err := json.Unmarshal(trimmed, &wire); err != nil {
		return nil, fmt.Errorf("read document symbols: %w", err)
	}
	symbols := make([]Symbol, 0, len(wire))
	if err := appendSymbols(&symbols, wire, ""); err != nil {
		return nil, err
	}
	return symbols, nil
}

func appendSymbols(symbols *[]Symbol, wire []wireSymbol, container string) error {
	for _, item := range wire {
		if item.Location != nil {
			if item.Location.URI == "" {
				return fmt.Errorf("read symbol %q: its location has no file", item.Name)
			}
			if _, err := uriToPath(item.Location.URI); err != nil {
				return fmt.Errorf("read symbol %q: %w", item.Name, err)
			}
			*symbols = append(*symbols, Symbol{
				Name: item.Name, Kind: item.Kind, Container: item.ContainerName,
				Range: item.Location.Range, SelectionRange: item.Location.Range,
			})
			continue
		}
		if item.Range == nil && item.SelectionRange == nil {
			return fmt.Errorf("read symbol %q: it has no range", item.Name)
		}
		span, selection := item.Range, item.SelectionRange
		if span == nil {
			span = selection
		}
		if selection == nil {
			selection = span
		}
		*symbols = append(*symbols, Symbol{
			Name: item.Name, Kind: item.Kind, Container: container,
			Range: *span, SelectionRange: *selection,
		})
		if err := appendSymbols(symbols, item.Children, item.Name); err != nil {
			return err
		}
	}
	return nil
}
