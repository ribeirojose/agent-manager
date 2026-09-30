package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const bridgeTimeout = 10 * time.Second

func main() {
	if err := runCLI(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "architecture-poc:", err)
		os.Exit(1)
	}
}

func runCLI(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: architecture-poc <serve|rpc|demo>")
	}
	switch args[0] {
	case "migration-cli", "migration-mcp", "migration-tui":
		return runMigrationClient(args[0], args[1:])
	case "serve-migration":
		return runMigrationOwner(args[1:], stderr)
	case "serve":
		set := flag.NewFlagSet("serve", flag.ContinueOnError)
		set.SetOutput(io.Discard)
		dir := set.String("dir", "", "profile directory")
		revision := set.Int("revision", 0, "owner revision")
		if err := set.Parse(args[1:]); err != nil {
			return fmt.Errorf("serve flags: %w", err)
		}
		if set.NArg() != 0 || *dir == "" || *revision < 1 || *revision > 3 {
			return errors.New("usage: architecture-poc serve --dir PATH --revision 1|2|3")
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return serve(ctx, *dir, *revision, stderr)
	case "rpc":
		set := flag.NewFlagSet("rpc", flag.ContinueOnError)
		set.SetOutput(io.Discard)
		dir := set.String("dir", "", "profile directory")
		if err := set.Parse(args[1:]); err != nil {
			return fmt.Errorf("rpc flags: %w", err)
		}
		if set.NArg() != 0 || *dir == "" {
			return errors.New("usage: architecture-poc rpc --dir PATH")
		}
		request, err := decodeRequest(stdin)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), bridgeTimeout)
		defer cancel()
		response, err := callProfile(ctx, *dir, request)
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(response)
	case "demo":
		if len(args) != 2 || args[1] != "local" {
			return errors.New("usage: architecture-poc demo local")
		}
		return runLocalDemo(stdout, stderr)
	default:
		return fmt.Errorf("unknown mode %q; usage: architecture-poc <serve|rpc|demo>", args[0])
	}
}

func callProfile(ctx context.Context, dir string, request Request) (Response, error) {
	socket, err := socketPathForProfile(dir)
	if err != nil {
		return Response{}, err
	}
	var dialer net.Dialer
	connection, err := dialer.DialContext(ctx, "unix", socket)
	if err != nil {
		return Response{}, fmt.Errorf("connect to profile owner: %w", err)
	}
	defer connection.Close()
	stop := context.AfterFunc(ctx, func() { connection.Close() })
	defer stop()

	if err := json.NewEncoder(connection).Encode(request); err != nil {
		return Response{}, fmt.Errorf("send owner request: %w", err)
	}
	if unixConnection, ok := connection.(*net.UnixConn); ok {
		if err := unixConnection.CloseWrite(); err != nil {
			return Response{}, fmt.Errorf("finish owner request: %w", err)
		}
	}
	response, err := decodeResponse(connection)
	if err != nil {
		return Response{}, err
	}
	if response.RequestID != request.RequestID {
		return Response{}, fmt.Errorf("owner response request_id %q does not match %q", response.RequestID, request.RequestID)
	}
	return response, nil
}
