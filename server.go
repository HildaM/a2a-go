package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// --- Functional Options ---

// Option defines a function that configures a Server.
type Option func(*Server) error

// WithAddress sets the listening address for the server.
func WithAddress(addr string) Option {
	return func(s *Server) error {
		if addr == "" {
			return fmt.Errorf("server address cannot be empty")
		}
		s.addr = addr
		return nil
	}
}

// WithLogger sets a custom logger for the server.
// If not used, a default logger writing to os.Stderr will be used.
func WithLogger(logger *log.Logger) Option {
	return func(s *Server) error {
		if logger == nil {
			return fmt.Errorf("custom logger cannot be nil")
		}
		s.logger = logger
		return nil
	}
}

// WithStore sets the TaskStore implementation for the server.
// If not used, a default InMemoryTaskStore will be used.
func WithStore(store TaskStore) Option {
	return func(s *Server) error {
		if store == nil {
			return fmt.Errorf("task store cannot be nil")
		}
		s.store = store
		return nil
	}
}

// WithBasePath sets a custom base path for the RPC endpoint.
// If not provided, defaults to "/rpc".
func WithBasePath(path string) Option {
	return func(s *Server) error {
		if path == "" {
			return fmt.Errorf("base path cannot be empty")
		}
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		s.basePath = path
		return nil
	}
}

// --- Handler Definition (Interface in server.go, Funcs in handler.go) ---

// Handler interface defines the methods an A2A agent needs to implement.
type Handler interface {
	GetAgentCard() (*AgentCard, error)
	SendTask(ctx *TaskContext) (*Task, error)
	GetTask(params *TaskGetParams) (*Task, error)
	CancelTask(params *TaskCancelParams) (*Task, error)
	SetTaskPushNotifications(params *TaskPushNotificationSetParams) (*PushNotificationConfig, error)
	GetTaskPushNotifications(params *TaskIdParams) (*PushNotificationConfig, error)
	SendTaskSubscribe(ctx *TaskContext) (*Task, error)
	Resubscribe(params *TaskGetParams) error
}

// handlerAdapter wraps HandlerFuncs and BaseHandler to satisfy the Handler interface.
// This is an internal type used by NewServer.
type handlerAdapter struct {
	BaseHandler // Embed BaseHandler for default implementations
	funcs       HandlerFuncs
	server      *Server // Reference to the parent server to access store
}

// Ensure handlerAdapter satisfies the Handler interface.
var _ Handler = (*handlerAdapter)(nil)

// GetAgentCard calls the configured func or panics (as it's mandatory).
func (a *handlerAdapter) GetAgentCard() (*AgentCard, error) {
	if a.funcs.GetAgentCardFunc == nil {
		panic("GetAgentCardFunc is mandatory but was not provided")
	}
	return a.funcs.GetAgentCardFunc()
}

// SendTask calls the user-provided func (if any) after loading/creating the task data,
// then saves the final result. If no user func is provided, it saves the initial pending task.
func (a *handlerAdapter) SendTask(ctx *TaskContext) (*Task, error) {
	// 1. Load or create initial TaskAndHistory data
	initialData, taskID, err := a.server.loadOrCreateTaskAndHistory(ctx.ID, ctx.Message, ctx.SessionID, ctx.Metadata)
	if err != nil {
		// Error during load/create (e.g., store read error)
		return nil, fmt.Errorf("internal server error preparing task: %w", err)
	}
	// Ensure ctx.ID is updated if it was generated
	ctx.ID = taskID

	if a.funcs.SendTaskFunc != nil {
		// --- User-provided function logic ---
		a.server.logger.Printf("SendTask: Calling user func for task %s", taskID)

		// 2. Prepare context for user function
		ctx.CurrentTask = initialData.Task // Provide current task state

		// 3. Call user function
		finalTask, userErr := a.funcs.SendTaskFunc(ctx)
		if userErr != nil {
			// Error from user's handler function
			// TODO: Should we save a FAILED state here?
			a.server.logger.Printf("SendTask: Error from user func for task %s: %v", taskID, userErr)
			return nil, userErr
		}
		if finalTask == nil {
			return nil, fmt.Errorf("internal error: SendTaskFunc returned nil Task without error")
		}

		// 4. Build final TaskAndHistory using history from initialData and finalTask's message
		finalHistory := initialData.History                                                // History already includes user message from loadOrCreate
		if finalTask.Status.Message != nil && finalTask.Status.Message.Role == RoleAgent { // LINTER_ERROR: Role mismatch likely
			finalHistory = append(finalHistory, *finalTask.Status.Message)
		}
		finalSaveData := &TaskAndHistory{
			Task:    finalTask,
			History: finalHistory,
		}

		// 5. Save final state
		if err := a.server.store.Save(finalSaveData); err != nil {
			a.server.logger.Printf("SendTask (user func): Error saving final task %s: %v", taskID, err)
			return nil, fmt.Errorf("internal server error while saving task")
		}

		a.server.logger.Printf("SendTask (user func): Successfully processed and saved task %s", taskID)
		// 6. Return final task state
		return finalTask, nil
		// --- End of User-provided function logic ---
	}

	// --- Default SendTask Implementation (No user function provided) ---
	a.server.logger.Printf("SendTask (default implementation): Saving pending task %s", taskID)

	// 1. Save the initial pending data created by loadOrCreateTaskAndHistory
	if err := a.server.store.Save(initialData); err != nil {
		a.server.logger.Printf("SendTask (default): Error saving pending task %s: %v", taskID, err)
		return nil, fmt.Errorf("internal server error while saving pending task")
	}

	a.server.logger.Printf("SendTask (default): Successfully created and saved pending task %s", taskID)
	// 2. Return the pending task state
	return initialData.Task, nil
	// --- End of Default SendTask Implementation ---
}

// GetTask calls the configured func or provides a default implementation using the server's store.
func (a *handlerAdapter) GetTask(params *TaskGetParams) (*Task, error) {
	if a.funcs.GetTaskFunc != nil {
		// User provided a function, delegate fully.
		return a.funcs.GetTaskFunc(params)
	}
	// Default implementation: Load from server store.
	a.server.logger.Printf("GetTask (default store implementation) for task %s", params.ID)
	data, err := a.server.store.Load(params.ID)
	if err != nil {
		a.server.logger.Printf("Error loading task %s from store for GetTask: %v", params.ID, err)
		return nil, fmt.Errorf("failed to load task data from store")
	}
	if data == nil {
		return nil, ErrTaskNotFound
	}
	// TODO: Default historyLength handling?
	return data.Task, nil
}

// CancelTask calls the configured func or provides a default implementation using the server's store.
func (a *handlerAdapter) CancelTask(params *TaskCancelParams) (*Task, error) {
	if a.funcs.CancelTaskFunc != nil {
		return a.funcs.CancelTaskFunc(params)
	}

	// Default implementation
	a.server.logger.Printf("CancelTask (default store implementation) for task %s", params.ID)
	data, err := a.server.store.Load(params.ID)
	if err != nil {
		if errors.Is(err, ErrTaskNotFound) {
			return nil, ErrTaskNotFound
		}
		return nil, fmt.Errorf("failed to load task data for cancellation: %w", err)
	}
	if data == nil {
		return nil, ErrTaskNotFound
	}

	task := data.Task
	// Check if already in a final state *before* attempting any update or save.
	if task.Status.State == StateCompleted || task.Status.State == StateCanceled || task.Status.State == StateFailed {
		a.server.logger.Printf("Attempted to cancel task %s which is already in final state %s. No action taken.", params.ID, task.Status.State)
		return nil, fmt.Errorf("task %s cannot be canceled, already in state %s", params.ID, task.Status.State)
	}

	// Proceed with cancellation logic only if the task is not in a final state.
	cancellationMsgText := "Task canceled by request."
	cancellationMessage := Message{
		Role:  RoleAgent,
		Parts: []Part{TextPart{Text: cancellationMsgText}},
	}
	cancelStatusUpdate := TaskStatus{
		State:   StateCanceled,
		Message: &cancellationMessage,
	}

	// Apply the update
	updatedData, err := applyUpdateToTaskAndHistory(data, cancelStatusUpdate)
	if err != nil {
		a.server.logger.Printf("Error applying cancellation update for task %s: %v", params.ID, err)
		return nil, fmt.Errorf("internal server error while applying cancel update")
	}

	// Save the updated data
	if err := a.server.store.Save(updatedData); err != nil {
		a.server.logger.Printf("Error saving canceled task %s to store: %v", params.ID, err)
		return nil, fmt.Errorf("failed to save canceled task state")
	}

	a.server.logger.Printf("Task %s canceled and saved (default store implementation).", params.ID)

	// Notify listeners
	a.server.NotifyTaskUpdate(params.ID, "task_status_update", updatedData.Task.Status)

	return updatedData.Task, nil
}

// SetTaskPushNotifications calls the configured func or the BaseHandler default.
func (a *handlerAdapter) SetTaskPushNotifications(params *TaskPushNotificationSetParams) (*PushNotificationConfig, error) {
	if a.funcs.SetTaskPushNotificationsFunc != nil {
		return a.funcs.SetTaskPushNotificationsFunc(params)
	}
	return a.BaseHandler.SetTaskPushNotifications(params)
}

// GetTaskPushNotifications calls the configured func or the BaseHandler default.
func (a *handlerAdapter) GetTaskPushNotifications(params *TaskIdParams) (*PushNotificationConfig, error) {
	if a.funcs.GetTaskPushNotificationsFunc != nil {
		return a.funcs.GetTaskPushNotificationsFunc(params)
	}
	return a.BaseHandler.GetTaskPushNotifications(params)
}

// SendTaskSubscribe calls the user-provided func (if any) after setting up initial state, saving it,
// and providing the update callback. If no user func is provided, it creates and saves a pending task.
func (a *handlerAdapter) SendTaskSubscribe(ctx *TaskContext) (*Task, error) {
	if a.funcs.SendTaskSubscribeFunc != nil {
		// --- User-provided function logic (unchanged) ---
		// 1. Load or create initial TaskAndHistory data
		initialData, taskID, err := a.server.loadOrCreateTaskAndHistory(ctx.ID, ctx.Message, ctx.SessionID, ctx.Metadata)
		if err != nil {
			return nil, fmt.Errorf("internal server error preparing task for subscribe: %w", err)
		}
		ctx.ID = taskID                 // Ensure ctx has the final ID
		initialTask := initialData.Task // Keep a reference to the initial task state for return

		// 2. Save the initial state (Pending) immediately
		if err := a.server.store.Save(initialData); err != nil {
			a.server.logger.Printf("SendTaskSubscribe: Error saving initial task %s: %v", taskID, err)
			return nil, fmt.Errorf("internal server error while saving initial task state")
		}
		a.server.logger.Printf("SendTaskSubscribe: Saved initial state for task %s", taskID)

		// 3. Define UpdateFunc closure (Mutex and load/apply/save/notify logic)
		var updateMu sync.Mutex
		updateFn := func(update interface{}) error {
			updateMu.Lock()
			defer updateMu.Unlock()
			currentData, err := a.server.store.Load(taskID)
			if err != nil {
				a.server.logger.Printf("UpdateFn: Error loading task %s for update: %v", taskID, err)
				return fmt.Errorf("internal error: failed to load task for update")
			}
			if currentData == nil {
				a.server.logger.Printf("UpdateFn: Task %s not found for update", taskID)
				return fmt.Errorf("internal error: task not found for update")
			}
			updatedData, err := applyUpdateToTaskAndHistory(currentData, update)
			if err != nil {
				a.server.logger.Printf("UpdateFn: Error applying update for task %s: %v", taskID, err)
				return fmt.Errorf("internal error: failed to apply update")
			}
			if err := a.server.store.Save(updatedData); err != nil {
				a.server.logger.Printf("UpdateFn: Error saving updated task %s: %v", taskID, err)
				return fmt.Errorf("internal error: failed to save update")
			}
			var eventType string
			var eventPayload interface{}
			switch u := update.(type) {
			case TaskStatus:
				eventType = "task_status_update"
				eventPayload = updatedData.Task.Status
			case Artifact:
				eventType = "new_artifact"
				eventPayload = u
			default:
				return fmt.Errorf("unknown update type in UpdateFn: %T", update)
			}
			a.server.NotifyTaskUpdate(taskID, eventType, eventPayload)
			return nil
		}

		// 4. Prepare context for user function
		ctx.UpdateFn = updateFn
		ctx.CurrentTask = initialTask // Provide the initial task state from step 1

		// 5. Call user function (synchronously for now)
		a.server.logger.Printf("SendTaskSubscribe: Calling user func for task %s", taskID)
		_, userErr := a.funcs.SendTaskSubscribeFunc(ctx)
		if userErr != nil {
			a.server.logger.Printf("SendTaskSubscribe: Error returned directly from handler func for task %s: %v", taskID, userErr)
			failMsg := Message{Role: RoleAgent, Parts: []Part{TextPart{Text: "Processing failed: " + userErr.Error()}}}
			_ = updateFn(TaskStatus{State: StateFailed, Message: &failMsg})
			return initialTask, nil
		}

		// 6. Return the initial task state (RPC response already sent implicitly)
		return initialTask, nil
		// --- End of User-provided function logic ---
	}

	// --- Default SendTaskSubscribe Implementation (No user function provided) ---
	a.server.logger.Printf("SendTaskSubscribe (default implementation) for task ID hint: %s", ctx.ID)

	// 1. Load or create initial TaskAndHistory data
	initialData, taskID, err := a.server.loadOrCreateTaskAndHistory(ctx.ID, ctx.Message, ctx.SessionID, ctx.Metadata)
	if err != nil {
		return nil, fmt.Errorf("internal server error preparing task for subscribe: %w", err)
	}

	// 2. Save the initial pending data
	if err := a.server.store.Save(initialData); err != nil {
		a.server.logger.Printf("SendTaskSubscribe (default): Error saving pending task %s: %v", taskID, err)
		return nil, fmt.Errorf("internal server error while saving pending task")
	}

	a.server.logger.Printf("SendTaskSubscribe (default): Successfully created and saved pending task %s", taskID)
	// 3. Return the pending task state. The SSE stream will be initiated by handleRPC.
	return initialData.Task, nil
	// --- End of Default SendTaskSubscribe Implementation ---
}

// Resubscribe calls the configured func or provides a default implementation using the server's store.
func (a *handlerAdapter) Resubscribe(params *TaskGetParams) error {
	if a.funcs.ResubscribeFunc != nil {
		// User provided a function, delegate fully.
		return a.funcs.ResubscribeFunc(params)
	}
	// Default implementation: Check existence in server store.
	a.server.logger.Printf("Resubscribe (default store implementation) for task %s", params.ID)
	data, err := a.server.store.Load(params.ID)
	if err != nil {
		a.server.logger.Printf("Error loading task %s from store for Resubscribe: %v", params.ID, err)
		return fmt.Errorf("failed to check task existence for resubscription")
	}
	if data == nil {
		return ErrTaskNotFound
	}
	// Task exists, allow resubscription.
	// Replaying historical events is not implemented here.
	return nil
}

// --- Parameter and Result Structs for RPC Methods (Aligned with schema.ts) ---

// TaskContext holds context for sending/subscribing to a task.
// It includes the initial parameters and potentially callbacks for streaming.
type TaskContext struct {
	ID               string                  `json:"id"`
	SessionID        string                  `json:"sessionId,omitempty"`
	Message          Message                 `json:"message"`
	HistoryLength    int                     `json:"historyLength,omitempty"`
	PushNotification *PushNotificationConfig `json:"pushNotification,omitempty"`
	Metadata         map[string]interface{}  `json:"metadata,omitempty"`
	// Callback for streaming handlers to send updates (Status or Artifact)
	// Ignored by JSON marshalling.
	UpdateFn UpdateFunc `json:"-"`
	// Current full task state (useful for handler functions)
	// Ignored by JSON marshalling.
	CurrentTask *Task `json:"-"`
}

// TaskIdParams matches schema.ts TaskIdParams (used by cancel, getPush)
type TaskIdParams struct {
	ID       string                 `json:"id"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// TaskGetParams matches schema.ts TaskQueryParams (used by get, resubscribe)
type TaskGetParams struct {
	ID            string                 `json:"id"`
	HistoryLength int                    `json:"historyLength,omitempty"`
	Metadata      map[string]interface{} `json:"metadata,omitempty"`
}

// TaskCancelParams is essentially TaskIdParams
type TaskCancelParams = TaskIdParams

/* // Removed - CancelTaskResponse returns Task
type TaskStatusResult struct {
	ID        string                 `json:"id"`
	SessionID *string                `json:"sessionId,omitempty"`
	Status    TaskStatus             `json:"status"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}
*/

// PushNotificationConfig matches schema.ts PushNotificationConfig
// This is the actual config structure, also used as the RESPONSE for get/set.
type PushNotificationConfig struct {
	URL            string               `json:"url"`
	Token          *string              `json:"token,omitempty"`
	Authentication *AgentAuthentication `json:"authentication,omitempty"`
}

// TaskPushNotificationSetParams matches the 'params' field for the 'tasks/pushNotification/set' request.
// It contains the task ID and the PushNotificationConfig to set.
type TaskPushNotificationSetParams struct {
	ID                     string                 `json:"id"`
	PushNotificationConfig PushNotificationConfig `json:"pushNotificationConfig"`
	Metadata               map[string]interface{} `json:"metadata,omitempty"` // Allow metadata on set request
}

// Removed confusing/incorrect aliases and comments
// type TaskPushNotificationSetConfig = PushNotificationConfig // Incorrect alias
// type SetPushParams = PushNotificationConfig // Incorrect alias
// type GetPushParams = TaskIdParams // Correct, but alias unnecessary

// --- SSE Event Structures (Based on schema.ts) ---

type TaskStatusUpdateEvent struct {
	ID       string                 `json:"id"`
	Status   TaskStatus             `json:"status"`
	Final    bool                   `json:"final,omitempty"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

type TaskArtifactUpdateEvent struct {
	ID       string                 `json:"id"`
	Artifact Artifact               `json:"artifact"`
	Final    bool                   `json:"final,omitempty"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// --- Server Implementation ---

// sseClient represents a connected SSE client.
type sseClient struct {
	channel chan []byte // Channel to send messages to this client
	// Add other relevant client info if needed (e.g., remote address)
}

// Server is the A2A server implementation.
// It wraps an http.Server and uses a Handler to process A2A requests.
// It manages SSE connections for real-time updates.
type Server struct {
	addr       string // Listening address
	httpServer *http.Server
	handler    Handler
	agentCard  *AgentCard
	logger     *log.Logger // Configurable logger
	store      TaskStore   // Task storage
	basePath   string      // Base path for the RPC endpoint

	// SSE related fields
	mu           sync.RWMutex
	sseClients   map[string]map[chan []byte]bool
	shutdownChan chan struct{} // Used to signal active SSE handlers to stop
}

// NewServer creates a new A2A Server with optional configurations.
// It requires HandlerFuncs to define the agent's behavior and accepts functional options.
func NewServer(funcs HandlerFuncs, opts ...Option) (*Server, error) {
	if funcs.GetAgentCardFunc == nil {
		return nil, fmt.Errorf("HandlerFuncs.GetAgentCardFunc is mandatory but was not provided")
	}

	// Default configuration
	defaultLogger := log.New(os.Stderr, "[A2A Server] ", log.LstdFlags|log.Lshortfile)
	defaultStore := NewInMemoryTaskStore() // Default to in-memory store

	s := &Server{
		logger:       defaultLogger,
		store:        defaultStore, // Set default store
		sseClients:   make(map[string]map[chan []byte]bool),
		shutdownChan: make(chan struct{}),
		addr:         ":8080",
		basePath:     "/", // Default base path is root
	}

	// Create the internal handler adapter, passing the server reference
	adapter := &handlerAdapter{
		funcs:  funcs,
		server: s, // Pass server reference to adapter
	}
	s.handler = adapter // Now set the handler

	// Apply functional options (this might override the default store)
	for _, opt := range opts {
		if err := opt(s); err != nil {
			return nil, fmt.Errorf("failed to apply server option: %w", err)
		}
	}

	// Get Agent Card using the adapter
	card, err := s.handler.GetAgentCard()
	if err != nil {
		return nil, fmt.Errorf("failed to get agent card from handler funcs: %w", err)
	}
	if !card.Capabilities.Streaming {
		s.logger.Println("Warning: AgentCard does not explicitly enable streaming. Enabling based on server capability.")
		card.Capabilities.Streaming = true
	}
	s.agentCard = card

	// Setup Mux and HTTP Server
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/agent.json", s.handleAgentCard)
	mux.HandleFunc(s.basePath, s.handleRPC) // Use configurable base path

	s.httpServer = &http.Server{
		Addr:    s.addr,
		Handler: mux,
		// TODO: Consider adding options for ReadTimeout, WriteTimeout, etc.
	}

	s.logger.Printf("Server configured for address: %s, Store: %T", s.addr, s.store)
	return s, nil
}

// Serve starts the A2A server.
func (s *Server) Serve() error {
	s.logger.Printf("A2A Server listening on %s (RPC: %s, AgentCard: /.well-known/agent.json)", s.httpServer.Addr, s.basePath)
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Println("Shutting down server gracefully...")
	close(s.shutdownChan)
	return s.httpServer.Shutdown(ctx)
}

// handleAgentCard serves the /.well-known/agent.json endpoint.
func (s *Server) handleAgentCard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(s.agentCard); err != nil {
		s.logger.Printf("Error encoding agent card: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// NotifyTaskUpdate - Updated to send specific event types adhering to schema
func (s *Server) NotifyTaskUpdate(taskID string, eventType string, data interface{}) {
	s.mu.RLock()
	clientChannels, ok := s.sseClients[taskID]
	s.mu.RUnlock()
	if !ok || len(clientChannels) == 0 {
		return
	}

	var eventDataBytes []byte
	var sseEventName string
	var err error
	finalEvent := false // Flag to mark if this event signifies task end

	// Construct specific event based on type
	switch eventType {
	case "task_status_update":
		if status, ok := data.(TaskStatus); ok {
			finalEvent = (status.State == StateCompleted || status.State == StateCanceled || status.State == StateFailed)
			event := TaskStatusUpdateEvent{
				ID:     taskID,
				Status: status,
				Final:  finalEvent,
			}
			eventDataBytes, err = json.Marshal(event)
			sseEventName = "task_status_update" // Use a defined event name
		} else {
			s.logger.Printf("Error: Invalid data type for task_status_update: %T", data)
			return
		}
	case "new_artifact":
		if artifact, ok := data.(Artifact); ok {
			// Use artifact.LastChunk (bool) directly
			finalArtifact := artifact.LastChunk
			event := TaskArtifactUpdateEvent{
				ID:       taskID,
				Artifact: artifact,
				Final:    finalArtifact, // Assign bool directly
			}
			eventDataBytes, err = json.Marshal(event)
			sseEventName = "task_artifact_update"
		} else {
			s.logger.Printf("Error: Invalid data type for new_artifact: %T", data)
			return
		}
	// Remove "new_message" case? schema.ts doesn't define a separate SSE event for messages.
	// Messages are typically included within TaskStatus updates.
	/*
		case "new_message":
			 if msg, ok := data.(Message); ok {
				 eventDataBytes, err = json.Marshal(msg)
				 sseEventName = "new_message"
			 } else { ... }
	*/
	default:
		s.logger.Printf("Warning: Unknown or unsupported event type for SSE notification: %s", eventType)
		return
	}

	if err != nil {
		s.logger.Printf("Error marshaling SSE event data for task %s (%s): %v", taskID, sseEventName, err)
		return
	}

	// Format SSE message
	var msgBuilder strings.Builder
	msgBuilder.WriteString(fmt.Sprintf("event: %s\n", sseEventName))
	msgBuilder.WriteString(fmt.Sprintf("data: %s\n\n", string(eventDataBytes)))
	messageBytes := []byte(msgBuilder.String())

	s.logger.Printf("Notifying %d clients for task %s (event: %s, final: %t)\n", len(clientChannels), taskID, sseEventName, finalEvent)

	// Send to all subscribed clients
	s.mu.RLock()
	for clientChan := range clientChannels {
		go func(ch chan []byte) {
			select {
			case ch <- messageBytes:
			case <-time.After(1 * time.Second): // Keep timeout
				s.logger.Printf("Timeout sending SSE message to client for task %s", taskID)
			}
		}(clientChan)
	}
	s.mu.RUnlock()
}

// handleRPC is the main entry point for /rpc requests.
func (s *Server) handleRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeRPCError(w, nil, http.StatusMethodNotAllowed, NewRPCError(InvalidRequestCode, "Method Not Allowed", nil))
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.logger.Printf("Error reading request body: %v", err)
		s.writeRPCError(w, nil, http.StatusInternalServerError, NewRPCError(InternalErrorCode, "Failed to read request body", nil))
		return
	}
	defer r.Body.Close()

	var req RPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		s.logger.Printf("Error parsing JSON request: %v", err)
		s.writeRPCError(w, nil, http.StatusBadRequest, NewRPCError(ParseErrorCode, "Failed to parse JSON request", err.Error()))
		return
	}

	if req.Jsonrpc != "2.0" && req.Jsonrpc != "0" {
		s.writeRPCError(w, req.ID, http.StatusBadRequest, NewRPCError(InvalidRequestCode, "Invalid jsonrpc version", nil))
		return
	}

	// Delegate to specific handlers based on method
	var result interface{}
	var rpcErr *RPCError
	startStreaming := false
	streamTaskID := ""

	switch req.Method {
	case "tasks/send":
		result, rpcErr = s.handleSendTaskRPC(req.Params)
	case "tasks/get":
		result, rpcErr = s.handleGetTaskRPC(req.Params)
	case "tasks/cancel":
		result, rpcErr = s.handleCancelTaskRPC(req.Params)
	case "tasks/pushNotification/set":
		result, rpcErr = s.handleSetPushNotifyRPC(req.Params)
	case "tasks/pushNotification/get":
		result, rpcErr = s.handleGetPushNotifyRPC(req.Params)
	case "tasks/sendSubscribe":
		result, rpcErr = s.handleSendTaskSubscribeRPC(req.Params)
		if rpcErr == nil {
			if taskResult, ok := result.(*Task); ok {
				startStreaming = true
				streamTaskID = taskResult.ID
			} else {
				// Should not happen if handler returns correct type
				rpcErr = NewRPCError(InternalErrorCode, "Internal error: sendSubscribe handler returned unexpected type", nil)
			}
		}
	case "tasks/resubscribe":
		result, rpcErr = s.handleResubscribeRPC(req.Params)
		if rpcErr == nil {
			if paramsMap, ok := result.(map[string]string); ok && paramsMap["status"] == "resubscription initiated" {
				// Need the task ID from the original params
				var params TaskGetParams
				if err := json.Unmarshal(req.Params, &params); err == nil {
					startStreaming = true
					streamTaskID = params.ID
				} else {
					rpcErr = NewRPCError(InternalErrorCode, "Internal error: failed to re-parse params for resubscribe stream", err.Error())
				}
			} else {
				// Handler error should have been caught already, but safeguard
				rpcErr = NewRPCError(InternalErrorCode, "Internal error: resubscribe handler returned unexpected result or error", nil)
			}
		}
	default:
		rpcErr = NewRPCError(MethodNotFoundCode, fmt.Sprintf("Method '%s' not found", req.Method), nil)
	}

	// Send response or initiate stream
	if rpcErr != nil {
		s.writeRPCError(w, req.ID, http.StatusOK, rpcErr)
		return
	}

	if !startStreaming {
		s.writeRPCSuccess(w, req.ID, result)
		return
	}

	s.initiateSSEStream(w, r, req.ID, streamTaskID, result)
}

// --- RPC Method Handlers (Private Helpers) ---

func (s *Server) handleSendTaskRPC(rawParams json.RawMessage) (interface{}, *RPCError) {
	var params TaskContext
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return nil, NewRPCError(InvalidParamsCode, "Invalid params for tasks/send", err.Error())
	}
	respTask, handlerErr := s.handler.SendTask(&params)
	if handlerErr != nil {
		return nil, s.mapHandlerErrorToRPCError(handlerErr)
	}
	return respTask, nil
}

func (s *Server) handleGetTaskRPC(rawParams json.RawMessage) (interface{}, *RPCError) {
	var params TaskGetParams
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return nil, NewRPCError(InvalidParamsCode, "Invalid params for tasks/get", err.Error())
	}
	task, handlerErr := s.handler.GetTask(&params)
	if handlerErr != nil {
		return nil, s.mapHandlerErrorToRPCError(handlerErr)
	}
	return task, nil
}

func (s *Server) handleCancelTaskRPC(rawParams json.RawMessage) (interface{}, *RPCError) {
	var params TaskCancelParams
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return nil, NewRPCError(InvalidParamsCode, "Invalid params for tasks/cancel", err.Error())
	}
	taskResult, handlerErr := s.handler.CancelTask(&params)
	if handlerErr != nil {
		return nil, s.mapHandlerErrorToRPCError(handlerErr)
	}
	return taskResult, nil
}

func (s *Server) handleSetPushNotifyRPC(rawParams json.RawMessage) (interface{}, *RPCError) {
	var params TaskPushNotificationSetParams
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return nil, NewRPCError(InvalidParamsCode, "Invalid params for tasks/pushNotification/set", err.Error())
	}
	configResult, handlerErr := s.handler.SetTaskPushNotifications(&params)
	if handlerErr != nil {
		return nil, s.mapHandlerErrorToRPCError(handlerErr)
	}
	return configResult, nil
}

func (s *Server) handleGetPushNotifyRPC(rawParams json.RawMessage) (interface{}, *RPCError) {
	var params TaskIdParams
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return nil, NewRPCError(InvalidParamsCode, "Invalid params for tasks/pushNotification/get", err.Error())
	}
	configResult, handlerErr := s.handler.GetTaskPushNotifications(&params)
	if handlerErr != nil {
		return nil, s.mapHandlerErrorToRPCError(handlerErr)
	}
	return configResult, nil
}

func (s *Server) handleSendTaskSubscribeRPC(rawParams json.RawMessage) (interface{}, *RPCError) {
	var params TaskContext
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return nil, NewRPCError(InvalidParamsCode, "Invalid params for tasks/sendSubscribe", err.Error())
	}
	initialTask, handlerErr := s.handler.SendTaskSubscribe(&params)
	if handlerErr != nil {
		return nil, s.mapHandlerErrorToRPCError(handlerErr)
	}
	s.logger.Printf("RPC: sendSubscribe request successful for task %s. Will initiate SSE stream.", initialTask.ID)
	return initialTask, nil // Return initial task, handleRPC determines streaming
}

func (s *Server) handleResubscribeRPC(rawParams json.RawMessage) (interface{}, *RPCError) {
	var params TaskGetParams
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return nil, NewRPCError(InvalidParamsCode, "Invalid params for tasks/resubscribe", err.Error())
	}
	handlerErr := s.handler.Resubscribe(&params)
	if handlerErr != nil {
		return nil, s.mapHandlerErrorToRPCError(handlerErr)
	}
	s.logger.Printf("RPC: resubscribe request successful for task %s. Will initiate SSE stream.", params.ID)
	// Return a standard success indicator, handleRPC determines streaming
	return map[string]string{"status": "resubscription initiated"}, nil
}

// --- SSE Stream Handling ---

// initiateSSEStream handles the setup and event loop for an SSE connection.
func (s *Server) initiateSSEStream(w http.ResponseWriter, r *http.Request, requestID interface{}, taskID string, initialResult interface{}) {
	s.logger.Printf("Initiating SSE stream for task %s (request ID: %v)", taskID, requestID)

	// Set headers for SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*") // Adjust CORS if needed

	// Ensure flusher is available
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.logger.Printf("Error: Streaming unsupported for task %s!", taskID)
		// Can't send HTTP error now.
		return
	}

	// Send the initial RPC Success Result as the first SSE event.
	rpcSuccessResp := RPCResponse{
		Jsonrpc: "2.0",
		Result:  initialResult,
		ID:      requestID,
	}
	rpcResponseBytes, err := json.Marshal(rpcSuccessResp)
	if err != nil {
		s.logger.Printf("Error marshaling initial RPC response for SSE stream task %s: %v", taskID, err)
		// Attempt to proceed without initial result?
	} else {
		fmt.Fprintf(w, "event: rpc_result\ndata: %s\n\n", string(rpcResponseBytes))
		flusher.Flush() // Flush after sending the initial result
	}

	// Create and register SSE client channel
	clientChan := make(chan []byte, 10) // Buffer size
	s.mu.Lock()
	if _, ok := s.sseClients[taskID]; !ok {
		s.sseClients[taskID] = make(map[chan []byte]bool)
	}
	s.sseClients[taskID][clientChan] = true
	s.mu.Unlock()
	s.logger.Printf("SSE client channel registered for task %s", taskID)

	// Start listening on the channel and sending events
	ctx := r.Context()
	for {
		select {
		case msg, ok := <-clientChan:
			if !ok {
				// Channel closed, likely by removeSSEClient during shutdown or error
				s.logger.Printf("SSE client channel closed for task %s", taskID)
				return
			}
			_, err := fmt.Fprintf(w, "%s", msg) // msg should already be formatted SSE event
			if err != nil {
				s.logger.Printf("SSE client disconnected for task %s (write error: %v)\n", taskID, err)
				s.removeSSEClient(taskID, clientChan)
				return
			}
			flusher.Flush()
		case <-ctx.Done():
			s.logger.Printf("SSE client disconnected for task %s (context done)\n", taskID)
			s.removeSSEClient(taskID, clientChan)
			return
		case <-s.shutdownChan:
			s.logger.Printf("SSE connection closed for task %s due to server shutdown\n", taskID)
			return
		}
	}
}

// mapHandlerErrorToRPCError translates errors from the handler into appropriate RPC errors.
func (s *Server) mapHandlerErrorToRPCError(err error) *RPCError {
	msg := err.Error()

	// Use errors.Is for specific known error variables
	if errors.Is(err, ErrTaskNotFound) {
		return NewRPCError(TaskNotFoundCode, "Task not found", msg)
	} else if errors.Is(err, ErrUnsupportedOperation) {
		return NewRPCError(UnsupportedOperationCode, "Unsupported operation", msg)
	} else if errors.Is(err, ErrPushNotificationsNotSupported) {
		return NewRPCError(PushNotifyNotSupportedCode, "Push notifications not supported", msg)
	} else if strings.Contains(strings.ToLower(msg), "cannot be canceled") {
		// Keep string check for this one as there's no specific error variable yet
		return NewRPCError(TaskCannotBeCanceledCode, "Task cannot be canceled", msg)
	}
	// Add more specific mappings here if needed using errors.Is or custom error types

	// Default to internal error for unmapped handler errors
	s.logger.Printf("Internal handler error: %v", err)
	return NewRPCError(InternalErrorCode, "Internal server error", msg)
}

// writeRPCError sends a JSON-RPC error response.
func (s *Server) writeRPCError(w http.ResponseWriter, id interface{}, httpStatusCode int, rpcErr *RPCError) {
	resp := RPCResponse{
		Jsonrpc: "2.0", // Standardize response to 2.0
		Error:   rpcErr,
		ID:      id,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatusCode)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		s.logger.Printf("Error writing RPC error response: %v\n", err)
	}
}

// writeRPCSuccess sends a JSON-RPC success response.
func (s *Server) writeRPCSuccess(w http.ResponseWriter, id interface{}, result interface{}) {
	resp := RPCResponse{
		Jsonrpc: "2.0", // Standardize response to 2.0
		Result:  result,
		ID:      id,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		s.logger.Printf("Error writing RPC success response: %v\n", err)
	}
}

// removeSSEClient removes a client channel from the subscriptions.
func (s *Server) removeSSEClient(taskID string, clientChan chan []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if clients, ok := s.sseClients[taskID]; ok {
		delete(clients, clientChan)
		close(clientChan) // Close the channel
		if len(clients) == 0 {
			delete(s.sseClients, taskID) // Remove task entry if no clients left
		}
	}
}

// TODO: Implement graceful shutdown for the server, including closing s.shutdownChan
// and waiting for active SSE handlers to finish.

// TODO: Add methods for graceful shutdown.
// TODO: Add support for SSE for streaming updates.
// TODO: Implement detailed JSON-RPC handling based on A2A methods.
// TODO: Implement Authentication/Authorization checks.

// --- Helper Functions ---

// applyUpdateToTaskAndHistory takes the current task data and an update (status or artifact)
// and returns a *new* TaskAndHistory object with the update applied immutably.
// It also updates the history based on the update type.
func applyUpdateToTaskAndHistory(current *TaskAndHistory, update interface{}) (*TaskAndHistory, error) {
	if current == nil || current.Task == nil {
		return nil, fmt.Errorf("cannot apply update to nil task data")
	}

	// Create shallow copies to work with
	newTask := *current.Task
	newHistory := make([]Message, len(current.History))
	copy(newHistory, current.History)
	newArtifacts := make([]Artifact, 0, len(current.Task.Artifacts)+1) // Pre-allocate slightly larger potentially
	newArtifacts = append(newArtifacts, current.Task.Artifacts...)     // Use append for clean copy
	newTask.Artifacts = newArtifacts                                   // Point newTask to the copied artifact slice

	switch u := update.(type) {
	case TaskStatus:
		// Merge status update
		newStatus := newTask.Status
		newStatus.State = u.State
		if u.Message != nil {
			newStatus.Message = u.Message
			if u.Message.Role == RoleAgent { // Add agent message to history
				newHistory = append(newHistory, *u.Message)
			}
		}
		newStatus.SetTimestamp(time.Now())
		newTask.Status = newStatus

	case Artifact:
		// Handle artifact update (append or replace)
		artifactUpdate := u
		replaced := false
		// Use 0 as the indicator for unspecified/append, similar to how optional ints work with omitempty.
		// Assume index provided in the artifact is 0-based (matching Go slice indices).
		updateIndex := artifactUpdate.Index // Index is now int

		if updateIndex >= 0 && updateIndex < len(newTask.Artifacts) {
			// Valid index provided for update/append
			if artifactUpdate.Append { // Check bool directly
				// NOTE: True appending logic (merging parts) is complex and depends
				// on part types. Replacing based on index+append flag for simplicity now.
				newTask.Artifacts[updateIndex] = artifactUpdate
				replaced = true
			} else {
				// Overwrite artifact at index
				newTask.Artifacts[updateIndex] = artifactUpdate
				replaced = true
			}
		}
		// If index is negative or not provided (zero value), or out of bounds, try matching by name or append.

		if !replaced && artifactUpdate.Name != "" { // Check name only if not replaced by index
			for i := range newTask.Artifacts {
				if newTask.Artifacts[i].Name == artifactUpdate.Name {
					newTask.Artifacts[i] = artifactUpdate // Replace by name
					replaced = true
					break
				}
			}
		}

		if !replaced { // Append if not replaced by index or name
			newTask.Artifacts = append(newTask.Artifacts, artifactUpdate)
		}
	default:
		return nil, fmt.Errorf("unknown update type: %T", update)
	}

	return &TaskAndHistory{
		Task:    &newTask,
		History: newHistory,
	}, nil
}

// loadOrCreateTaskAndHistory attempts to load a task by ID.
// If not found, it creates a new TaskAndHistory object in a Pending state
// with the initial message added to the history.
// It does *not* save the new task to the store; the caller is responsible for saving.
func (s *Server) loadOrCreateTaskAndHistory(taskIDHint string, initialMessage Message, sessionID string, metadata map[string]interface{}) (*TaskAndHistory, string, error) {
	taskID := taskIDHint
	var existingData *TaskAndHistory
	var loadErr error

	if taskID != "" { // Only attempt to load if a hint was provided
		existingData, loadErr = s.store.Load(taskID)
		if loadErr != nil {
			s.logger.Printf("Error loading task %s during loadOrCreate: %v", taskID, loadErr)
			// Consider load errors fatal for this operation unless it's simply not found.
			if !errors.Is(loadErr, ErrTaskNotFound) { // Don't return error if just not found
				return nil, taskID, fmt.Errorf("failed to load potential existing task data: %w", loadErr)
			}
			// If ErrTaskNotFound, proceed to create below.
			existingData = nil // Ensure existingData is nil if not found
		}
	} else { // No ID hint, generate one
		taskID = uuid.NewString()
	}

	if existingData != nil {
		s.logger.Printf("Task %s found, using existing data.", taskID)
		if existingData.History == nil {
			existingData.History = make([]Message, 0)
		}
		existingData.History = append(existingData.History, initialMessage)
		return existingData, taskID, nil
	}

	// Not found or no hint provided, create a new one
	s.logger.Printf("Task %s not found or no hint provided, creating new pending task.", taskID)
	now := time.Now()
	pendingStatus := TaskStatus{State: StatePending}
	pendingStatus.SetTimestamp(now)

	newTask := &Task{
		ID:        taskID,
		SessionID: sessionID,
		Status:    pendingStatus,
		Metadata:  metadata,
		Artifacts: []Artifact{},
	}

	newHistory := []Message{initialMessage}

	newData := &TaskAndHistory{
		Task:    newTask,
		History: newHistory,
	}

	return newData, taskID, nil
}
