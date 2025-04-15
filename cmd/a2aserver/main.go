package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof" // Import for side effect: registers pprof handlers
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/a2aserver/a2a-go"
)

// handleGetAgentCard returns the agent's static information.
func handleGetAgentCard() (*a2a.AgentCard, error) {
	streaming := true
	push := false
	return &a2a.AgentCard{
		Name:        "Go Simple Example Agent",
		Description: "A basic Go agent demonstrating the a2a-go library.",
		URL:         "http://localhost:8080", // Assuming default address
		Version:     "0.1.0",
		Capabilities: a2a.AgentCapabilities{
			Streaming:         streaming,
			PushNotifications: push,
		},
		Authentication: a2a.AgentAuthentication{Schemes: []string{"None"}},
		Skills: []a2a.AgentSkill{
			{
				ID:          "simple_echo",
				Name:        "Simple Echo Task",
				Description: "Echoes back the input message as an agent message and completes.",
			},
			{
				ID:          "simple_stream",
				Name:        "Simple Streaming Task",
				Description: "Sends a few status updates and an artifact via SSE.",
			},
		},
	}, nil
}

// handleSendTaskSubscribe demonstrates a simple streaming task.
func handleSendTaskSubscribe(ctx *a2a.TaskContext) (*a2a.Task, error) {
	log.Printf("Handling SendTaskSubscribe for task %s", ctx.ID)

	// Use the provided UpdateFn to send progress
	go func() {
		time.Sleep(1 * time.Second) // Simulate work
		log.Printf("[Task %s] Sending working status...", ctx.ID)
		workingStatus := a2a.TaskStatus{State: a2a.StateProcessing}
		err := ctx.UpdateFn(workingStatus)
		if err != nil {
			log.Printf("[Task %s] Error sending working status update: %v", ctx.ID, err)
			// No return here, attempt to send final state anyway
		}

		time.Sleep(2 * time.Second) // Simulate more work
		log.Printf("[Task %s] Sending text artifact...", ctx.ID)
		artifact := a2a.Artifact{
			Parts: []a2a.Part{a2a.TextPart{Text: fmt.Sprintf("Processed input: %s", ctx.Message.Parts[0].(a2a.TextPart).Text)}},
		}
		err = ctx.UpdateFn(artifact)
		if err != nil {
			log.Printf("[Task %s] Error sending artifact update: %v", ctx.ID, err)
		}

		time.Sleep(1 * time.Second)
		log.Printf("[Task %s] Sending completed status...", ctx.ID)
		completionMessage := a2a.Message{
			Role:  a2a.RoleAgent,
			Parts: []a2a.Part{a2a.TextPart{Text: "Task completed successfully after streaming."}},
		}
		completedStatus := a2a.TaskStatus{
			State:   a2a.StateCompleted,
			Message: &completionMessage,
		}
		err = ctx.UpdateFn(completedStatus)
		if err != nil {
			log.Printf("[Task %s] Error sending completed status update: %v", ctx.ID, err)
		}
		log.Printf("[Task %s] Streaming finished.", ctx.ID)
	}()

	// Return the initial task state provided by the server (already saved as Pending)
	// The actual processing happens in the goroutine above.
	log.Printf("[Task %s] Returning initial task state to start stream.", ctx.ID)
	return ctx.CurrentTask, nil
}

func main() {
	addr := flag.String("addr", ":8080", "Address to listen on")
	pprofAddr := flag.String("pprof", "localhost:6060", "Address for pprof endpoint")
	// logFile := flag.String("log", "", "Path to log file (optional, defaults to stderr)")
	flag.Parse()

	// Setup logger (example uses standard logger)
	logger := log.New(os.Stderr, "[A2A Simple Server] ", log.LstdFlags|log.Lshortfile)

	// Configure pprof endpoint (optional)
	if *pprofAddr != "" {
		go func() {
			logger.Printf("Starting pprof server on %s", *pprofAddr)
			if err := http.ListenAndServe(*pprofAddr, nil); err != nil {
				logger.Printf("pprof server failed: %v", err)
			}
		}()
	}

	// Define Handler Functions
	handlerFuncs := a2a.HandlerFuncs{
		GetAgentCardFunc: handleGetAgentCard,
		// SendTaskFunc: handleSendTask, // REMOVED - Using default server implementation
		SendTaskSubscribeFunc: handleSendTaskSubscribe, // ADDED simple streaming handler
		// GetTaskFunc: handleGetTask, // Rely on default store implementation
		// CancelTaskFunc: handleCancelTask, // Rely on default store implementation
	}

	// Create Server with options
	// No explicit store needed, defaults to InMemoryTaskStore
	server, err := a2a.NewServer(
		handlerFuncs,
		a2a.WithAddress(*addr),
		a2a.WithLogger(logger),
		// a2a.WithStore(taskStore), // REMOVED
	)
	if err != nil {
		logger.Fatalf("Failed to create server: %v", err)
	}

	// Start server in a goroutine
	go func() {
		if err := server.Serve(); err != nil && err != http.ErrServerClosed {
			logger.Fatalf("Server failed: %v", err)
		}
	}()

	// Graceful Shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Println("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.Fatalf("Server forced to shutdown: %v", err)
	}

	logger.Println("Server exiting")
}
