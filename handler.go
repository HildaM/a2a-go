package a2a

import (
	"fmt"
)

// --- Handler Definition ---

// UpdateFunc defines the callback function signature used by streaming handlers
// to push status or artifact updates back to the server.
type UpdateFunc func(update interface{}) error

// Note: Handler interface is defined in server.go

// --- HandlerFuncs Definition (Used by NewServer) ---

// HandlerFuncs provides a way to configure the server with specific function
// implementations for different A2A RPC methods. This allows developers to
// implement only the functionality they need, relying on server defaults
// (often interacting with a TaskStore) for the rest.
type HandlerFuncs struct {
	// GetAgentCardFunc (Mandatory): Returns the agent's metadata card.
	// This function MUST be provided when creating a new server.
	GetAgentCardFunc func() (*AgentCard, error)

	// SendTaskFunc (Optional): Handles the 'tasks/send' RPC method for non-streaming tasks.
	// If provided, this function receives the TaskContext (including the initial
	// user message and a pre-populated CurrentTask in Pending state) and should
	// return the *final* Task object synchronously after completing the work.
	// The server adapter will automatically save the returned final Task and its
	// associated history (initial user message + final agent message) to the configured TaskStore.
	// If nil, the server provides a default implementation that:
	//  1. Creates a task with StatePending.
	//  2. Saves the task and the initial user message to the TaskStore.
	//  3. Returns the pending task state to the client.
	SendTaskFunc func(ctx *TaskContext) (*Task, error)

	// GetTaskFunc (Optional): Handles the 'tasks/get' RPC method.
	// If provided, this function receives TaskGetParams and is fully responsible
	// for retrieving and returning the *Task object (including potentially trimming history).
	// If nil, the server provides a default implementation that loads the full
	// TaskAndHistory from the configured TaskStore using the provided task ID
	// and returns the Task object (history trimming based on params is NOT implemented by default).
	// Returns ErrTaskNotFound if the task doesn't exist in the store.
	GetTaskFunc func(params *TaskGetParams) (*Task, error)

	// CancelTaskFunc (Optional): Handles the 'tasks/cancel' RPC method.
	// If provided, this function receives TaskCancelParams and is fully responsible
	// for updating the task state to Canceled, saving it, and returning the final Task.
	// It should also handle notifying any listeners if applicable.
	// If nil, the server provides a default implementation that:
	//  1. Loads the task from the configured TaskStore.
	//  2. Checks if the task is already in a final state (error if so).
	//  3. Updates the task status to StateCanceled with a default message.
	//  4. Saves the updated TaskAndHistory back to the store.
	//  5. Notifies listeners via the server's NotifyTaskUpdate method.
	//  6. Returns the canceled Task object.
	CancelTaskFunc func(params *TaskCancelParams) (*Task, error)

	// SetTaskPushNotificationsFunc (Optional): Handles 'tasks/pushNotification/set'.
	// If provided, responsible for configuring push notifications for the task.
	// If nil, the server returns an ErrUnsupportedOperation error.
	SetTaskPushNotificationsFunc func(params *TaskPushNotificationSetParams) (*PushNotificationConfig, error)

	// GetTaskPushNotificationsFunc (Optional): Handles 'tasks/pushNotification/get'.
	// If provided, responsible for retrieving push notification config for the task.
	// If nil, the server returns an ErrUnsupportedOperation error.
	GetTaskPushNotificationsFunc func(params *TaskIdParams) (*PushNotificationConfig, error)

	// SendTaskSubscribeFunc (Optional): Handles the 'tasks/sendSubscribe' RPC method for streaming tasks.
	// If provided, this function receives the TaskContext (including the initial
	// user message, a pre-populated CurrentTask in Pending state which has already been saved,
	// and a non-nil UpdateFn callback). The function should perform its work
	// (potentially asynchronously in a goroutine) and use ctx.UpdateFn to send
	// TaskStatus and Artifact updates. The return value of this function itself is
	// typically ignored for streaming tasks (the initial Task state is returned by the adapter).
	// If nil, the server provides a default implementation that:
	//  1. Creates a task with StatePending.
	//  2. Saves the task and the initial user message to the TaskStore.
	//  3. Returns the pending task state to the client to initiate the SSE stream.
	// No further updates are sent by default if this function is nil.
	SendTaskSubscribeFunc func(ctx *TaskContext) (*Task, error)

	// ResubscribeFunc (Optional): Handles the 'tasks/resubscribe' RPC method.
	// If provided, receives TaskGetParams and should validate if the client
	// can resubscribe (e.g., task exists, not completed). It might replay recent events.
	// If nil, the server provides a default implementation that checks if the task ID
	// exists in the configured TaskStore. If found, it returns nil (allowing resubscription).
	// If not found, it returns ErrTaskNotFound.
	// Default implementation does NOT replay any historical events.
	ResubscribeFunc func(params *TaskGetParams) error
}

// --- Base Handler Implementation ---

// BaseHandler provides default implementations for the Handler interface.
// Embed this type into your custom handler struct and override only the methods
// you need to implement.
type BaseHandler struct{}

// Ensure BaseHandler satisfies the Handler interface (compile-time check)
var _ Handler = (*BaseHandler)(nil)

func (h *BaseHandler) GetAgentCard() (*AgentCard, error) {
	// This method MUST be overridden by embedding types.
	return nil, fmt.Errorf("GetAgentCard must be implemented by embedding handler")
}

func (h *BaseHandler) SendTask(ctx *TaskContext) (*Task, error) {
	return nil, fmt.Errorf("%w: tasks/send not implemented", ErrUnsupportedOperation)
}

func (h *BaseHandler) GetTask(params *TaskGetParams) (*Task, error) {
	return nil, fmt.Errorf("%w: tasks/get not implemented", ErrUnsupportedOperation)
}

func (h *BaseHandler) CancelTask(params *TaskCancelParams) (*Task, error) {
	return nil, fmt.Errorf("%w: tasks/cancel not implemented", ErrUnsupportedOperation)
}

func (h *BaseHandler) SetTaskPushNotifications(params *TaskPushNotificationSetParams) (*PushNotificationConfig, error) {
	return nil, fmt.Errorf("%w: tasks/pushNotification/set", ErrPushNotificationsNotSupported)
}

func (h *BaseHandler) GetTaskPushNotifications(params *TaskIdParams) (*PushNotificationConfig, error) {
	return nil, fmt.Errorf("%w: tasks/pushNotification/get", ErrPushNotificationsNotSupported)
}

func (h *BaseHandler) SendTaskSubscribe(ctx *TaskContext) (*Task, error) {
	return nil, fmt.Errorf("%w: tasks/sendSubscribe not implemented", ErrUnsupportedOperation)
}

func (h *BaseHandler) Resubscribe(params *TaskGetParams) error {
	return fmt.Errorf("%w: tasks/resubscribe not implemented", ErrUnsupportedOperation)
}

// Define standard errors for default implementations
var (
	ErrUnsupportedOperation          = fmt.Errorf("unsupported operation")
	ErrPushNotificationsNotSupported = fmt.Errorf("push notifications not supported")
)

// --- Task Storage ---

// ... TaskAndHistory struct ...

// ... TaskStore interface ...

// ... standard storage errors ...

// --- FileTaskStore Implementation ---

// ... fileHistoryWrapper struct ...

// ... FileTaskStore struct ...

// ... NewFileTaskStore ...

// ... FileTaskStore methods (ensureDirectoryExists, sanitizeTaskID, get paths, Save, Load, Delete) ...
