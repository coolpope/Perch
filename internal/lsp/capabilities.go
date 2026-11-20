package lsp

import (
	"encoding/json"
	"fmt"
)

// Capabilities are the requests a server declares among those Perch makes.
type Capabilities struct {
	Definition      bool
	References      bool
	Hover           bool
	DocumentSymbol  bool
	WorkspaceSymbol bool
}

// providerCapability reads whether the server offers a protocol provider.
type providerCapability bool

// UnmarshalJSON reads a provider value whose syntax encoding/json has checked.
func (p *providerCapability) UnmarshalJSON(data []byte) error {
	switch data[0] {
	case 't', '{':
		*p = true
	case 'f', 'n':
		*p = false
	default:
		return fmt.Errorf("expected a boolean, an options object, or null, got %q", data[0])
	}
	return nil
}

// parseCapabilities reads the provider declarations from an initialize result.
func parseCapabilities(result json.RawMessage) (Capabilities, error) {
	var wire struct {
		Capabilities struct {
			Definition      providerCapability `json:"definitionProvider"`
			References      providerCapability `json:"referencesProvider"`
			Hover           providerCapability `json:"hoverProvider"`
			DocumentSymbol  providerCapability `json:"documentSymbolProvider"`
			WorkspaceSymbol providerCapability `json:"workspaceSymbolProvider"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(result, &wire); err != nil {
		return Capabilities{}, fmt.Errorf("read initialize capabilities: %w", err)
	}
	providers := &wire.Capabilities
	return Capabilities{
		Definition:      bool(providers.Definition),
		References:      bool(providers.References),
		Hover:           bool(providers.Hover),
		DocumentSymbol:  bool(providers.DocumentSymbol),
		WorkspaceSymbol: bool(providers.WorkspaceSymbol),
	}, nil
}
