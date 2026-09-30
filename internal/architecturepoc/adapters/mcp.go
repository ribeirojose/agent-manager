package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/YoanWai/agent-manager/internal/architecturepoc/application"
	"github.com/YoanWai/agent-manager/internal/architecturepoc/extensions"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type MCPApplication interface {
	Inventory(context.Context) (application.Inventory, error)
	RenameFixture(context.Context, application.SessionTarget, application.ReaderToken, string) (application.MutationResult, error)
}

type InventoryToolOutput struct {
	Inventory *InventoryDocument `json:"inventory,omitempty"`
	Error     *ErrorDocument     `json:"error,omitempty"`
}

type RenameToolOutput struct {
	Mutation *MutationDocument `json:"mutation,omitempty"`
	Error    *ErrorDocument    `json:"error,omitempty"`
}

type ErrorDocument struct {
	Code    application.Code `json:"code"`
	Message string           `json:"message"`
}

type inventoryToolArguments struct {
	Group string `json:"group,omitempty" jsonschema:"exact fixture group to retain; omit for all groups"`
}

type renameToolArguments struct {
	TargetID string `json:"target_id" jsonschema:"namespaced target_id returned by inventory"`
	Token    string `json:"token" jsonschema:"opaque reader token returned beside that target"`
	Name     string `json:"name" jsonschema:"new fixture display name"`
}

func NewMCPServer(client MCPApplication) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: "agent-manager-architecture-workspace-poc", Version: "0"},
		&mcp.ServerOptions{Instructions: "Disposable architecture POC. Inventory-only fixture operations do not launch agents or control tmux."},
	)
	query := extensions.NewFilteredInventory(client)
	rename := extensions.NewFixtureRename(client)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "inventory",
		Description: "Read this client's fixed device or workspace scope. Workspace scope uses cached remote projections; device scope never follows saved outbound connections.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, arguments inventoryToolArguments) (*mcp.CallToolResult, InventoryToolOutput, error) {
		inventory, err := query.Query(ctx, arguments.Group)
		if err != nil {
			result, document := inventoryToolFailure(err)
			return result, document, nil
		}
		document, err := PresentInventory(inventory)
		if err != nil {
			result, failure := inventoryToolFailure(err)
			return result, failure, nil
		}
		output := InventoryToolOutput{Inventory: &document}
		return textToolResult(output), output, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "rename_fixture",
		Description: "Rename inventory-only fixture metadata using the exact target and reader token returned by inventory.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, arguments renameToolArguments) (*mcp.CallToolResult, RenameToolOutput, error) {
		target, err := DecodeSessionTarget(arguments.TargetID)
		if err != nil {
			result, document := renameToolFailure(err)
			return result, document, nil
		}
		token, err := DecodeReaderToken(arguments.Token)
		if err != nil {
			result, document := renameToolFailure(err)
			return result, document, nil
		}
		result, err := rename.Execute(ctx, extensions.RenameRequest{Target: target, Token: token, Name: arguments.Name})
		if err != nil {
			toolResult, document := renameToolFailure(err)
			return toolResult, document, nil
		}
		document, err := PresentMutation(result)
		if err != nil {
			toolResult, failure := renameToolFailure(err)
			return toolResult, failure, nil
		}
		output := RenameToolOutput{Mutation: &document}
		return textToolResult(output), output, nil
	})
	return server
}

func inventoryToolFailure(err error) (*mcp.CallToolResult, InventoryToolOutput) {
	document := errorDocument(err)
	output := InventoryToolOutput{Error: &document}
	return errorToolResult(output), output
}

func renameToolFailure(err error) (*mcp.CallToolResult, RenameToolOutput) {
	document := errorDocument(err)
	output := RenameToolOutput{Error: &document}
	return errorToolResult(output), output
}

func errorDocument(err error) ErrorDocument {
	message := err.Error()
	var typed *application.Error
	if errors.As(err, &typed) {
		message = typed.Message
	}
	return ErrorDocument{Code: application.CodeOf(err), Message: message}
}

func errorToolResult(value any) *mcp.CallToolResult {
	result := textToolResult(value)
	result.IsError = true
	return result
}

func RunMCP(ctx context.Context, client MCPApplication) error {
	return NewMCPServer(client).Run(ctx, &mcp.StdioTransport{})
}

func RunMCPIO(ctx context.Context, client MCPApplication, reader io.Reader, writer io.Writer) error {
	return NewMCPServer(client).Run(ctx, &mcp.IOTransport{Reader: readNoCloser{Reader: reader}, Writer: writeNoCloser{Writer: writer}})
}

type readNoCloser struct{ io.Reader }

func (readNoCloser) Close() error { return nil }

type writeNoCloser struct{ io.Writer }

func (writeNoCloser) Close() error { return nil }

func textToolResult(value any) *mcp.CallToolResult {
	payload, err := json.Marshal(value)
	if err != nil {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: `{"error":"encode tool output"}`}}, IsError: true}
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(payload)}}}
}
