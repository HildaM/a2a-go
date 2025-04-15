package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	// Assuming the example is run from the root directory of the a2aserver module
	"github.com/a2aserver/a2a-go"
)

// handleGetAgentCard defines the agent card for the HelloWorld example.
func handleGetAgentCard() (*a2a.AgentCard, error) {
	return &a2a.AgentCard{
		Name:        "Go HelloWorld Agent",
		Description: "A simple Go agent that says Hello World via SendTaskFunc.", // Updated description
		URL:         "http://localhost:8080",
		Version:     "0.0.2", // Bump version
		Capabilities: a2a.AgentCapabilities{
			Streaming:         false, // Not implementing subscribe
			PushNotifications: false,
		},
		Authentication: a2a.AgentAuthentication{Schemes: []string{"None"}},
		Skills: []a2a.AgentSkill{
			{
				ID:          "hello_world",
				Name:        "Hello World Task",
				Description: "Receives any message via SendTask and immediately completes with 'Hello World!'", // Added skill
			},
		},
	}, nil
}

// handleHelloWorldTask implements the simplest possible SendTaskFunc.
// It ignores the input and immediately returns a completed task with a "Hello World!" message.
func handleHelloWorldTask(ctx *a2a.TaskContext) (*a2a.Task, error) {
	log.Printf("[Task %s] Received HelloWorld request.", ctx.ID)

	// Prepare the "Hello World!" response message
	helloMessage := a2a.Message{
		Role:  a2a.RoleAgent,
		Parts: []a2a.Part{a2a.TextPart{Text: "Hello World!"}},
	}

	// Create the final Task object using the context's CurrentTask as a base
	finalTask := ctx.CurrentTask
	if finalTask == nil {
		return nil, fmt.Errorf("internal error: CurrentTask not found in context for task %s", ctx.ID)
	}

	finalTask.Status.State = a2a.StateCompleted
	finalTask.Status.Message = &helloMessage
	finalTask.Status.SetTimestamp(time.Now())
	finalTask.Artifacts = nil // No artifacts for this simple task

	log.Printf("[Task %s] Responded with Hello World.", ctx.ID)

	// Return the final task object. Server adapter will save it.
	return finalTask, nil
}

func main() {
	addr := ":8080"
	logger := log.New(os.Stderr, "[HelloWorld A2A] ", log.LstdFlags|log.Lshortfile)
	logger.Printf("Starting HelloWorld A2A Server on %s", addr)

	// Define handler functions
	handlerFuncs := a2a.HandlerFuncs{
		GetAgentCardFunc: handleGetAgentCard,
		SendTaskFunc:     handleHelloWorldTask, // Assign the HelloWorld handler
		// Other funcs are nil, using server defaults (e.g., GetTask, CancelTask will use store)
	}

	// Create server with minimal configuration
	server, err := a2a.NewServer(
		handlerFuncs,
		a2a.WithAddress(addr),
		a2a.WithLogger(logger),
	)
	if err != nil {
		logger.Fatalf("Failed to create server: %v", err)
	}

	if err := server.Serve(); err != nil && err != http.ErrServerClosed {
		logger.Fatalf("Server failed: %v", err)
	}
}
