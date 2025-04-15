# a2aserver - Go A2A Server Library

[![Go Reference](https://pkg.go.dev/badge/a2aserver/pkg/a2a.svg)](https://pkg.go.dev/a2aserver/pkg/a2a)

This repository contains a Go library (`pkg/a2a`) for building servers that implement the Agent-to-Agent (A2A) communication protocol, along with example server implementations (`cmd/a2aserver`, `examples/`). The official A2A protocol specification can be found at [https://google.github.io/A2A/](https://google.github.io/A2A/).

The goal is to provide a flexible and easy-to-use framework for creating A2A agents in Go.

## Features

*   **A2A Protocol Compliance:** Implements core A2A RPC methods (`tasks/send`, `tasks/get`, `tasks/cancel`, `tasks/sendSubscribe`, etc.) and the agent discovery endpoint (`/.well-known/agent.json`).
*   **Flexible Handler Configuration:** Use the `HandlerFuncs` struct to provide implementations only for the RPC methods your agent needs to handle. The server provides sensible defaults for others via `BaseHandler`.
*   **Functional Options:** Configure the server (listening address, logger, task store, RPC base path) using the `Option` pattern (`WithAddress`, `WithLogger`, `WithStore`, `WithBasePath`).
*   **Configurable Base Path:** Set the base path for the RPC endpoint using `WithBasePath` (defaults to `/`).
*   **Pluggable Task Storage:** Includes an `InMemoryTaskStore` (default) and a `FileTaskStore` (with atomic saves). Define your own storage by implementing the `TaskStore` interface.
*   **Server-Sent Events (SSE):** Supports real-time task updates for `tasks/sendSubscribe` and `tasks/resubscribe` methods.
*   **Default Implementations:** Provides default logic for `SendTask`, `SendTaskSubscribe` (creates pending tasks), `GetTask`, `CancelTask`, and `Resubscribe` that automatically interacts with the configured `TaskStore`.
*   **Consolidated Schema:** Core A2A data structures (Task, Message, Part, Artifact, etc.) are defined in `pkg/a2a/schema.go`.


## Getting Started

### Prerequisites

*   Go 1.18 or later

### Build

Build the example servers:

```bash
go build ./cmd/a2aserver/...
go build ./examples/.../...
```

### Run Examples

*   **HelloWorld Example (Simple SendTask):**
    ```bash
    go run ./examples/helloworld/main.go
    # or ./helloworld
    ```
    Test with:
    ```bash
    # Get Agent Card
    curl http://localhost:8080/.well-known/agent.json | jq .
    # Send Task (assuming default base path /)
    curl -X POST http://localhost:8080/ -d '{"jsonrpc":"2.0","method":"tasks/send","params":{"message":{"role":"user","parts":[{"type":"text","text":"Hi"}]}},"id":1}' | jq .
    ```

*   **Simple Example (Streaming SendTaskSubscribe):**
    ```bash
    go run ./examples/simple/main.go
    # or ./simple
    ```
    Test with:
    ```bash
    # Send Streaming Task (keeps connection open for SSE, assuming default base path /)
    curl -X POST http://localhost:8080/ -d '{"jsonrpc":"2.0","method":"tasks/sendSubscribe","params":{"message":{"role":"user","parts":[{"type":"text","text":"Stream test"}]}},"id":2}'
    ```

## Usage 

Here's a basic example of using the library:

```go
package main

import (
	"log"
	"github.com/a2aserver/a2a-go"// Adjust import path
	"os"
)

// 1. Implement required GetAgentCardFunc
func myAgentCard() (*a2a.AgentCard, error) {
	// ... return your agent card ...
	return &a2a.AgentCard{
		Name: "My Custom Agent",
		// ... other fields ...
	}, nil
}

// 2. Implement optional handler funcs (e.g., SendTaskFunc)
// Access the current task state via ctx.CurrentTask
// For streaming (SendTaskSubscribeFunc), use ctx.UpdateFn to send updates.
func mySendTask(ctx *a2a.TaskContext) (*a2a.Task, error) {
	log.Printf("Handling task %s", ctx.ID)
	// ... your logic here, potentially modifying ctx.CurrentTask ...
	finalTask := ctx.CurrentTask // Use provided task as base
	finalTask.Status.State = a2a.StateCompleted
	// ... set status message/artifacts ...
	log.Printf("Task %s completed by custom handler.", ctx.ID)
	return finalTask, nil
}

func main() {
	logger := log.New(os.Stderr, "[MyAgent] ", log.LstdFlags)

	// 3. Define HandlerFuncs
	handlerFuncs := a2a.HandlerFuncs{
		GetAgentCardFunc: myAgentCard,
		SendTaskFunc:     mySendTask,
		// Other funcs like SendTaskSubscribeFunc, GetTaskFunc, CancelTaskFunc
		// left nil will use server defaults (interacting with TaskStore).
	}

	// 4. Create and configure the server
	store, err := a2a.NewFileTaskStore("my_agent_tasks") // Use file store
	if err != nil {
		logger.Fatalf("Failed to create file store: %v", err)
	}

	server, err := a2a.NewServer(
		handlerFuncs,
		a2a.WithAddress(":8080"),
		a2a.WithLogger(logger),
		a2a.WithStore(store),
		// a2a.WithBasePath("/myagent/rpc"), // Optional: Set custom RPC base path
	)
	if err != nil {
		logger.Fatalf("Failed to create server: %v", err)
	}

	// 5. Start the server (add graceful shutdown if needed)
	logger.Println("Starting server...")
	if err := server.Serve(); err != nil {
		logger.Fatalf("Server failed: %v", err)
	}
}
```
