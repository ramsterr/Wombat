package lsp

import "encoding/json"

type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

type LocationLink struct {
	TargetURI            string `json:"targetUri"`
	TargetRange          Range  `json:"targetRange"`
	TargetSelectionRange Range  `json:"targetSelectionRange"`
}

type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

type WorkspaceEdit struct {
	Changes         map[string][]TextEdit `json:"changes,omitempty"`
	DocumentChanges []DocumentChange      `json:"documentChanges,omitempty"`
}

type DocumentChange struct {
	TextDocument *TextDocumentIdentifier `json:"textDocument,omitempty"`
	Edits        []TextEdit              `json:"edits,omitempty"`
	Kind         string                  `json:"kind,omitempty"`
	URI          string                  `json:"uri,omitempty"`
	OldURI       string                  `json:"oldUri,omitempty"`
	NewURI       string                  `json:"newUri,omitempty"`
}

type TextDocumentIdentifier struct {
	URI string `json:"uri"`
}

type Diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity,omitempty"`
	Code     any    `json:"code,omitempty"`
	Source   string `json:"source,omitempty"`
	Message  string `json:"message"`
}

type publishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

type documentDiagnosticParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type documentDiagnosticReport struct {
	Kind  string       `json:"kind"`
	Items []Diagnostic `json:"items,omitempty"`
}

type Symbol struct {
	Name          string    `json:"name"`
	Kind          int       `json:"kind"`
	ContainerName string    `json:"containerName,omitempty"`
	Location      *Location `json:"location,omitempty"`
}

type DocumentSymbol struct {
	Name           string           `json:"name"`
	Kind           int              `json:"kind"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children,omitempty"`
}

type serverCapabilities struct {
	TextDocumentSync        json.RawMessage `json:"textDocumentSync"`
	DefinitionProvider      json.RawMessage `json:"definitionProvider"`
	ReferencesProvider      json.RawMessage `json:"referencesProvider"`
	RenameProvider          json.RawMessage `json:"renameProvider"`
	DocumentSymbolProvider  json.RawMessage `json:"documentSymbolProvider"`
	WorkspaceSymbolProvider json.RawMessage `json:"workspaceSymbolProvider"`
	DiagnosticProvider      json.RawMessage `json:"diagnosticProvider"`
}

type initializeResult struct {
	Capabilities serverCapabilities `json:"capabilities"`
}

type initializeParams struct {
	ProcessID        int                `json:"processId"`
	RootURI          string             `json:"rootUri"`
	Capabilities     clientCapabilities `json:"capabilities"`
	WorkspaceFolders []workspaceFolder  `json:"workspaceFolders"`
}

type workspaceFolder struct {
	URI  string `json:"uri"`
	Name string `json:"name"`
}

type clientCapabilities struct {
	Workspace    workspaceCapabilities    `json:"workspace"`
	TextDocument textDocumentCapabilities `json:"textDocument"`
	Window       windowCapabilities       `json:"window"`
}

type workspaceCapabilities struct {
	WorkspaceFolders      bool           `json:"workspaceFolders"`
	Symbol                map[string]any `json:"symbol"`
	Configuration         bool           `json:"configuration"`
	ApplyEdit             bool           `json:"applyEdit"`
	DidChangeWatchedFiles map[string]any `json:"didChangeWatchedFiles"`
}

type textDocumentCapabilities struct {
	Synchronization    synchronizationCapabilities `json:"synchronization"`
	Definition         map[string]any              `json:"definition"`
	References         map[string]any              `json:"references"`
	Rename             map[string]any              `json:"rename"`
	Diagnostic         map[string]any              `json:"diagnostic"`
	PublishDiagnostics map[string]any              `json:"publishDiagnostics"`
	DocumentSymbol     map[string]any              `json:"documentSymbol"`
}

type synchronizationCapabilities struct {
	DynamicRegistration bool `json:"dynamicRegistration"`
	DidSave             bool `json:"didSave"`
}

type windowCapabilities struct {
	WorkDoneProgress bool `json:"workDoneProgress"`
}

type textDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

type textDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

type referenceParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	Context      referenceContext       `json:"context"`
}

type referenceContext struct {
	IncludeDeclaration bool `json:"includeDeclaration"`
}

type renameParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	NewName      string                 `json:"newName"`
}

type workspaceSymbolParams struct {
	Query string `json:"query"`
}

type documentSymbolParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type didOpenParams struct {
	TextDocument textDocumentItem `json:"textDocument"`
}

type didChangeParams struct {
	TextDocument   versionedTextDocumentIdentifier `json:"textDocument"`
	ContentChanges []contentChange                 `json:"contentChanges"`
}

type versionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version int    `json:"version"`
}

type contentChange struct {
	Range *Range `json:"range,omitempty"`
	Text  string `json:"text"`
}

type didSaveParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type didCloseParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type workspaceConfigurationParams struct {
	Items []any `json:"items"`
}
