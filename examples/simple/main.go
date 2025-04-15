package main

import (
	// ... imports ...
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/a2aserver/a2a-go"
)

// handleGetAgentCard defines the agent card.
func handleGetAgentCard() (*a2a.AgentCard, error) {
	streaming := false // Set streaming to false as we are not implementing Subscribe
	return &a2a.AgentCard{
		Name:        "Go Simple Echo Agent",                   // Updated name
		Description: "A minimal Go agent using SendTaskFunc.", // Updated description
		URL:         "http://localhost:8080",
		Version:     "0.0.2", // Bump version
		Capabilities: a2a.AgentCapabilities{
			Streaming:         streaming,
			PushNotifications: false,
		},
		Authentication: a2a.AgentAuthentication{Schemes: []string{"None"}},
		Skills: []a2a.AgentSkill{
			{
				ID:          "simple_echo",
				Name:        "Simple Echo",
				Description: "Receives text via SendTask, completes task, echoes artifact.", // Updated skill desc
			},
		},
	}, nil
}

// handleSimpleEchoTask implements a non-streaming task handler for SendTaskFunc.
func handleSimpleEchoTask(ctx *a2a.TaskContext) (*a2a.Task, error) {
	log.Printf("[Task %s] Received non-stream request.", ctx.ID)

	// 1. Simulate some work
	time.Sleep(500 * time.Millisecond)

	// 2. Prepare the final response message and artifact
	inputText := "(No text part found)"
	if len(ctx.Message.Parts) > 0 {
		if textPart, ok := ctx.Message.Parts[0].(a2a.TextPart); ok {
			inputText = textPart.Text
		}
	}

	responseText := fmt.Sprintf("Echoing back: '%s'", inputText)
	completionMessage := a2a.Message{
		Role:  a2a.RoleAgent,
		Parts: []a2a.Part{a2a.TextPart{Text: responseText}},
	}

	responseArtifact := a2a.Artifact{
		Name:  "echo_result",
		Parts: []a2a.Part{a2a.TextPart{Text: fmt.Sprintf("Processed: %s", inputText)}},
	}

	// 3. Create the final Task object with Completed state
	// We use the ctx.CurrentTask provided by the adapter as a base
	// (it contains the correct ID and potentially SessionID/Metadata)
	finalTask := ctx.CurrentTask // Start with the pending task created by the adapter
	if finalTask == nil {
		// Should not happen if adapter logic is correct, but safety check
		return nil, fmt.Errorf("internal error: CurrentTask not found in context")
	}

	finalTask.Status.State = a2a.StateCompleted
	finalTask.Status.Message = &completionMessage
	finalTask.Status.SetTimestamp(time.Now())              // Set final timestamp
	finalTask.Artifacts = []a2a.Artifact{responseArtifact} // Set final artifacts

	log.Printf("[Task %s] Completed synchronously.", ctx.ID)

	// 4. Return the final task object. The server adapter will save it.
	return finalTask, nil
}

func main() {
	addr := ":8080"
	logger := log.New(os.Stderr, "[Simple A2A] ", log.LstdFlags|log.Lshortfile)
	logger.Printf("Starting Simple A2A Server on %s", addr)

	// Define handler functions
	handlerFuncs := a2a.HandlerFuncs{
		GetAgentCardFunc: handleGetAgentCard,
		SendTaskFunc:     handleSimpleEchoTask, // Assign to SendTaskFunc
		// SendTaskSubscribeFunc: handleSimpleStreamTask, // REMOVED
		// Other handlers use default server logic
	}

	// Create server with default options (InMemoryTaskStore)
	server, err := a2a.NewServer(
		handlerFuncs,
		a2a.WithAddress(addr),
		a2a.WithLogger(logger),
	)
	if err != nil {
		logger.Fatalf("Failed to create server: %v", err)
	}

	// Start server in a goroutine
	if err := server.Serve(); err != nil && err != http.ErrServerClosed {
		logger.Fatalf("Server failed: %v", err)
	}
}
