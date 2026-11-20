package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Hover is what a server says about the symbol at a position: Markdown, and
// the span it covers when the server names one.
type Hover struct {
	Markdown string
	Range    *Range
}

// wireHover reads a textDocument/hover answer, whose contents are
// MarkupContent, a MarkedString, or a list of MarkedStrings.
type wireHover struct {
	Contents json.RawMessage `json:"contents"`
	Range    *Range          `json:"range"`
}

// wireMarked is a MarkedString given as an object, or MarkupContent.
type wireMarked struct {
	Kind     string `json:"kind"`
	Language string `json:"language"`
	Value    string `json:"value"`
}

// Hover returns what the server says about the symbol at a position, empty
// when it says nothing.
func (c *Client) Hover(ctx context.Context, document Document, position Position) (Hover, error) {
	const method = "textDocument/hover"
	if !c.capabilities.Hover {
		return Hover{}, fmt.Errorf("%s: %w", method, ErrUnsupported)
	}
	if err := c.sync(document); err != nil {
		return Hover{}, err
	}
	result, err := c.call(ctx, method, positionParams(document, position))
	if err != nil {
		return Hover{}, err
	}
	return parseHover(result)
}

// References returns every place the symbol at a position is used, its
// declaration included.
func (c *Client) References(ctx context.Context, document Document, position Position) ([]Location, error) {
	const method = "textDocument/references"
	if !c.capabilities.References {
		return nil, fmt.Errorf("%s: %w", method, ErrUnsupported)
	}
	if err := c.sync(document); err != nil {
		return nil, err
	}
	params := mustRaw(map[string]json.RawMessage{
		"textDocument": mustRaw(map[string]string{"uri": pathToURI(document.Path)}),
		"position":     mustRaw(position),
		"context":      json.RawMessage(`{"includeDeclaration":true}`),
	})
	result, err := c.call(ctx, method, params)
	if err != nil {
		return nil, err
	}
	return parseLocations(result)
}

// positionParams names a position of a document.
func positionParams(document Document, position Position) json.RawMessage {
	return mustRaw(map[string]json.RawMessage{
		"textDocument": mustRaw(map[string]string{"uri": pathToURI(document.Path)}),
		"position":     mustRaw(position),
	})
}

// parseHover reads a hover answer into Markdown: code that a MarkedString
// names the language of, and plain text, become fenced blocks.
func parseHover(result json.RawMessage) (Hover, error) {
	trimmed := bytes.TrimSpace(result)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return Hover{}, nil
	}
	var wire wireHover
	if err := json.Unmarshal(trimmed, &wire); err != nil {
		return Hover{}, fmt.Errorf("read hover: %w", err)
	}
	contents := bytes.TrimSpace(wire.Contents)
	if len(contents) == 0 || bytes.Equal(contents, []byte("null")) {
		return Hover{}, nil
	}
	var parts []json.RawMessage
	if contents[0] == '[' {
		if err := json.Unmarshal(contents, &parts); err != nil {
			return Hover{}, fmt.Errorf("read hover contents: %w", err)
		}
	} else {
		parts = []json.RawMessage{contents}
	}
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		text, err := markedText(part)
		if err != nil {
			return Hover{}, err
		}
		if strings.TrimSpace(text) != "" {
			texts = append(texts, text)
		}
	}
	return Hover{Markdown: strings.Join(texts, "\n\n"), Range: wire.Range}, nil
}

// markedText reads one MarkedString or MarkupContent as Markdown.
func markedText(part json.RawMessage) (string, error) {
	if len(part) > 0 && part[0] == '"' {
		var text string
		if err := json.Unmarshal(part, &text); err != nil {
			return "", fmt.Errorf("read hover text: %w", err)
		}
		return text, nil
	}
	var marked wireMarked
	if err := json.Unmarshal(part, &marked); err != nil {
		return "", fmt.Errorf("read hover text: %w", err)
	}
	switch {
	case marked.Language != "":
		return "```" + marked.Language + "\n" + strings.TrimRight(marked.Value, "\n") + "\n```", nil
	case marked.Kind == "plaintext":
		return "```text\n" + strings.TrimRight(marked.Value, "\n") + "\n```", nil
	default:
		return marked.Value, nil
	}
}
