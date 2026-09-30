package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/YoanWai/agent-manager/internal/architecturepoc/adapters"
	"github.com/YoanWai/agent-manager/internal/architecturepoc/application"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runCLI(ctx, os.Args[1:], os.Stdin, os.Stdout); err != nil {
		if writeErr := writeCLIError(os.Stderr, err); writeErr != nil {
			fmt.Fprintln(os.Stderr, writeErr)
		}
		os.Exit(1)
	}
}

type cliErrorEnvelope struct {
	Error adapters.ErrorDocument `json:"error"`
}

func writeCLIError(writer io.Writer, err error) error {
	message := err.Error()
	var typed *application.Error
	if errors.As(err, &typed) {
		message = typed.Message
	}
	return json.NewEncoder(writer).Encode(cliErrorEnvelope{Error: adapters.ErrorDocument{Code: application.CodeOf(err), Message: message}})
}
