package lsp

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestDocumentSymbolsOfEveryReplyShape(t *testing.T) {
	hierarchical := `[
		{"name":"Store","kind":23,
		 "range":{"start":{"line":2,"character":0},"end":{"line":4,"character":1}},
		 "selectionRange":{"start":{"line":2,"character":5},"end":{"line":2,"character":10}},
		 "children":[
			{"name":"Save","kind":6,
			 "range":{"start":{"line":3,"character":0},"end":{"line":3,"character":20}},
			 "selectionRange":{"start":{"line":3,"character":16},"end":{"line":3,"character":20}}}
		 ]}
	]`
	flat := `[
		{"name":"Save","kind":6,"containerName":"Store",
		 "location":{"uri":"file:///a%20b.go",
		 "range":{"start":{"line":8,"character":1},"end":{"line":8,"character":5}}}}
	]`
	cases := []struct {
		name       string
		reply      string
		names      []string
		containers []string
		kinds      []SymbolKind
		lines      []int
		columns    []int
	}{
		{"hierarchical", hierarchical, []string{"Store", "Save"}, []string{"", "Store"},
			[]SymbolKind{SymbolStruct, SymbolMethod}, []int{2, 3}, []int{5, 16}},
		{"flat", flat, []string{"Save"}, []string{"Store"},
			[]SymbolKind{SymbolMethod}, []int{8}, []int{1}},
	}
	for _, test := range cases {
		symbols, err := parseSymbols(json.RawMessage(test.reply))
		if err != nil {
			t.Errorf("%s: %v", test.name, err)
			continue
		}
		names := make([]string, 0, len(symbols))
		containers := make([]string, 0, len(symbols))
		kinds := make([]SymbolKind, 0, len(symbols))
		lines := make([]int, 0, len(symbols))
		columns := make([]int, 0, len(symbols))
		for _, symbol := range symbols {
			names = append(names, symbol.Name)
			containers = append(containers, symbol.Container)
			kinds = append(kinds, symbol.Kind)
			lines = append(lines, symbol.SelectionRange.Start.Line)
			columns = append(columns, symbol.SelectionRange.Start.Character)
		}
		if !slices.Equal(names, test.names) || !slices.Equal(containers, test.containers) ||
			!slices.Equal(kinds, test.kinds) || !slices.Equal(lines, test.lines) ||
			!slices.Equal(columns, test.columns) {
			t.Errorf("%s parsed %+v", test.name, symbols)
		}
	}
}

func TestSymbolColumnsAreUTF16(t *testing.T) {
	line := "a中😀Save"
	reply := `[{"name":"Save","kind":12,
		"range":{"start":{"line":0,"character":4},"end":{"line":0,"character":8}},
		"selectionRange":{"start":{"line":0,"character":4},"end":{"line":0,"character":8}}}]`
	symbols, err := parseSymbols(json.RawMessage(reply))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ByteColumn(line, symbols[0].SelectionRange.Start.Character), strings.Index(line, "Save"); got != want {
		t.Errorf("byte column %d, want %d", got, want)
	}
	if got, want := UTF16Column(line, strings.Index(line, "Save")), 4; got != want {
		t.Errorf("UTF-16 column %d, want %d", got, want)
	}
}

func TestDocumentSymbolsOfAnEmptyReply(t *testing.T) {
	for _, reply := range []string{"", "null", "[]"} {
		symbols, err := parseSymbols(json.RawMessage(reply))
		if err != nil || len(symbols) != 0 {
			t.Errorf("%q gave %+v, %v", reply, symbols, err)
		}
	}
}
