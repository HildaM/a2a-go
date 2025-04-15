package a2a

import (
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// --- Mock TaskStore for Testing ---

type mockTaskStore struct {
	mu            sync.Mutex
	tasks         map[string]*TaskAndHistory
	saveCalled    bool
	loadCalled    bool
	deleteCalled  bool
	lastSaved     *TaskAndHistory
	lastLoadedID  string
	lastDeletedID string
	saveErr       error // Injectable error for Save
	loadErr       error // Injectable error for Load
	deleteErr     error // Injectable error for Delete
}

func newMockTaskStore() *mockTaskStore {
	return &mockTaskStore{
		tasks: make(map[string]*TaskAndHistory),
	}
}

func (m *mockTaskStore) Save(data *TaskAndHistory) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.saveCalled = true
	m.lastSaved = data // Store ref for inspection
	if m.saveErr != nil {
		return m.saveErr
	}
	if data == nil || data.Task == nil {
		return fmt.Errorf("mock save error: nil data or task")
	}
	// Store a copy to avoid mutation issues if needed, but ref is ok for now
	m.tasks[data.Task.ID] = data
	return nil
}

func (m *mockTaskStore) Load(taskID string) (*TaskAndHistory, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadCalled = true
	m.lastLoadedID = taskID
	if m.loadErr != nil {
		return nil, m.loadErr
	}
	taskData, ok := m.tasks[taskID]
	if !ok {
		return nil, nil // Simulate not found (no error)
	}
	// Return a copy to prevent test modification? For now, just ref
	return taskData, nil
}

func (m *mockTaskStore) Delete(taskID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteCalled = true
	m.lastDeletedID = taskID
	if m.deleteErr != nil {
		return m.deleteErr
	}
	if _, ok := m.tasks[taskID]; !ok {
		return fmt.Errorf("mock delete error: task %s not found", taskID)
	}
	delete(m.tasks, taskID)
	return nil
}

// --- Test Helper to Create Server Instance ---

func newTestServer(t *testing.T, funcs HandlerFuncs, store TaskStore) *Server {
	t.Helper() // Mark as test helper

	// Ensure mandatory GetAgentCardFunc is provided if not in funcs
	if funcs.GetAgentCardFunc == nil {
		funcs.GetAgentCardFunc = func() (*AgentCard, error) {
			streaming := true // Enable streaming by default in tests
			return &AgentCard{
				Name:           "Test Agent",
				URL:            "http://test",
				Version:        "1.0",
				Capabilities:   AgentCapabilities{Streaming: streaming},
				Authentication: AgentAuthentication{Schemes: []string{"None"}},
				Skills:         []AgentSkill{{ID: "test_skill", Name: "Test Skill"}},
			}, nil
		}
	}
	// Use default logger to discard output during tests
	discardLogger := log.New(io.Discard, "", 0)

	opts := []Option{
		WithLogger(discardLogger),
		WithAddress(":0"), // Use random available port
	}
	// Use provided store or default mock store
	if store == nil {
		store = newMockTaskStore()
	}
	opts = append(opts, WithStore(store))

	srv, err := NewServer(funcs, opts...)
	if err != nil {
		t.Fatalf("Failed to create test server: %v", err)
	}
	return srv
}

// --- Unit Tests Start Here ---

func TestHandlerAdapter_DefaultSendTask(t *testing.T) {
	// 1. Setup
	mockStore := newMockTaskStore()
	// Use empty HandlerFuncs to trigger default behavior
	srv := newTestServer(t, HandlerFuncs{}, mockStore)
	adapter := srv.handler.(*handlerAdapter) // Get the internal adapter

	inputMsg := Message{
		Role:  RoleUser,
		Parts: []Part{TextPart{Text: "Test default send"}},
	}
	testCtx := &TaskContext{
		ID:      "test-default-send-task", // Provide an ID
		Message: inputMsg,
	}

	// 2. Execute
	returnedTask, err := adapter.SendTask(testCtx)

	// 3. Assert
	if err != nil {
		t.Fatalf("Default SendTask failed: %v", err)
	}

	if returnedTask == nil {
		t.Fatal("Default SendTask returned nil task")
	}

	if returnedTask.ID != testCtx.ID {
		t.Errorf("Returned task ID mismatch: got %q, want %q", returnedTask.ID, testCtx.ID)
	}

	if returnedTask.Status.State != StatePending {
		t.Errorf("Returned task state mismatch: got %q, want %q", returnedTask.Status.State, StatePending)
	}

	if !mockStore.saveCalled {
		t.Error("Expected store.Save to be called, but it wasn't")
	}

	if mockStore.lastSaved == nil {
		t.Fatal("Store.Save was called, but lastSaved data is nil")
	}

	if mockStore.lastSaved.Task.ID != testCtx.ID {
		t.Errorf("Saved task ID mismatch: got %q, want %q", mockStore.lastSaved.Task.ID, testCtx.ID)
	}

	if mockStore.lastSaved.Task.Status.State != StatePending {
		t.Errorf("Saved task state mismatch: got %q, want %q", mockStore.lastSaved.Task.Status.State, StatePending)
	}

	if len(mockStore.lastSaved.History) != 1 {
		t.Errorf("Saved history length mismatch: got %d, want 1", len(mockStore.lastSaved.History))
	} else if mockStore.lastSaved.History[0].Parts[0].(TextPart).Text != "Test default send" {
		t.Errorf("Saved history message content mismatch: got %v, want %v", mockStore.lastSaved.History[0], inputMsg)
	}
}

func TestHandlerAdapter_UserSendTaskFunc(t *testing.T) {
	// 1. Setup
	mockStore := newMockTaskStore()
	userFuncCalled := false

	// Define a simple user function for testing
	userSendTaskFunc := func(ctx *TaskContext) (*Task, error) {
		t.Helper()
		userFuncCalled = true

		if ctx == nil {
			t.Error("User func received nil context")
			return nil, fmt.Errorf("nil context")
		}
		if ctx.CurrentTask == nil {
			t.Error("User func received nil CurrentTask in context")
			return nil, fmt.Errorf("nil CurrentTask")
		}
		if ctx.CurrentTask.Status.State != StatePending {
			t.Errorf("User func expected CurrentTask state %q, got %q", StatePending, ctx.CurrentTask.Status.State)
		}

		// Simulate work and create final task state
		finalMsg := Message{Role: RoleAgent, Parts: []Part{TextPart{Text: "User func done"}}}
		finalTask := ctx.CurrentTask // Modify the provided task
		finalTask.Status.State = StateCompleted
		finalTask.Status.Message = &finalMsg
		finalTask.Status.SetTimestamp(time.Now())
		finalTask.Artifacts = []Artifact{{Name: "user-artifact"}}

		return finalTask, nil
	}

	handlerFuncs := HandlerFuncs{
		SendTaskFunc: userSendTaskFunc,
	}
	srv := newTestServer(t, handlerFuncs, mockStore)
	adapter := srv.handler.(*handlerAdapter)

	inputMsg := Message{
		Role:  RoleUser,
		Parts: []Part{TextPart{Text: "Test user send"}},
	}
	testCtx := &TaskContext{
		ID:      "test-user-send-task",
		Message: inputMsg,
	}

	// 2. Execute
	returnedTask, err := adapter.SendTask(testCtx)

	// 3. Assert
	if err != nil {
		t.Fatalf("User SendTask failed: %v", err)
	}
	if !userFuncCalled {
		t.Error("User SendTaskFunc was not called")
	}
	if returnedTask == nil {
		t.Fatal("User SendTask returned nil task")
	}
	if returnedTask.ID != testCtx.ID {
		t.Errorf("Returned task ID mismatch: got %q, want %q", returnedTask.ID, testCtx.ID)
	}
	if returnedTask.Status.State != StateCompleted {
		t.Errorf("Returned task state mismatch: got %q, want %q", returnedTask.Status.State, StateCompleted)
	}
	if len(returnedTask.Artifacts) != 1 || returnedTask.Artifacts[0].Name != "user-artifact" {
		t.Errorf("Returned task artifacts mismatch: got %v", returnedTask.Artifacts)
	}

	// Assert store interactions
	if !mockStore.saveCalled {
		t.Error("Expected store.Save to be called, but it wasn't")
	}
	if mockStore.lastSaved == nil {
		t.Fatal("Store.Save was called, but lastSaved data is nil")
	}
	if mockStore.lastSaved.Task.ID != testCtx.ID {
		t.Errorf("Saved task ID mismatch: got %q, want %q", mockStore.lastSaved.Task.ID, testCtx.ID)
	}
	if mockStore.lastSaved.Task.Status.State != StateCompleted {
		t.Errorf("Saved task state mismatch: got %q, want %q", mockStore.lastSaved.Task.Status.State, StateCompleted)
	}

	// Check history includes both user and agent messages
	if len(mockStore.lastSaved.History) != 2 {
		t.Errorf("Saved history length mismatch: got %d, want 2", len(mockStore.lastSaved.History))
	} else {
		if mockStore.lastSaved.History[0].Role != RoleUser || mockStore.lastSaved.History[0].Parts[0].(TextPart).Text != "Test user send" {
			t.Errorf("Saved history[0] (user msg) mismatch: got %v", mockStore.lastSaved.History[0])
		}
		if mockStore.lastSaved.History[1].Role != RoleAgent || mockStore.lastSaved.History[1].Parts[0].(TextPart).Text != "User func done" {
			t.Errorf("Saved history[1] (agent msg) mismatch: got %v", mockStore.lastSaved.History[1])
		}
	}
}

func TestHandlerAdapter_DefaultGetTask(t *testing.T) {
	// 1. Setup: Prepare a task in the mock store
	mockStore := newMockTaskStore()
	testTaskID := "get-task-test-id"
	preSavedTask := &Task{
		ID:     testTaskID,
		Status: TaskStatus{State: StateProcessing},
	}
	preSavedHistory := []Message{{Role: RoleUser, Parts: []Part{TextPart{Text: "Initial message"}}}}
	preSavedData := &TaskAndHistory{
		Task:    preSavedTask,
		History: preSavedHistory,
	}
	err := mockStore.Save(preSavedData) // Pre-populate the store
	if err != nil {
		t.Fatalf("Failed to pre-save task in mock store: %v", err)
	}

	// Use empty HandlerFuncs for default GetTask
	srv := newTestServer(t, HandlerFuncs{}, mockStore)
	adapter := srv.handler.(*handlerAdapter)

	// --- Test Case 1: Task Found ---
	t.Run("Task Found", func(t *testing.T) {
		params := &TaskGetParams{ID: testTaskID}

		// 2. Execute
		mockStore.loadCalled = false // Reset flag
		returnedTask, err := adapter.GetTask(params)

		// 3. Assert
		if err != nil {
			t.Fatalf("Default GetTask failed when task should exist: %v", err)
		}
		if !mockStore.loadCalled {
			t.Error("Expected store.Load to be called")
		}
		if mockStore.lastLoadedID != testTaskID {
			t.Errorf("Store loaded wrong ID: got %q, want %q", mockStore.lastLoadedID, testTaskID)
		}
		if returnedTask == nil {
			t.Fatal("Default GetTask returned nil task when task should exist")
		}
		if returnedTask.ID != testTaskID {
			t.Errorf("Returned task ID mismatch: got %q, want %q", returnedTask.ID, testTaskID)
		}
		if returnedTask.Status.State != StateProcessing {
			t.Errorf("Returned task state mismatch: got %q, want %q", returnedTask.Status.State, StateProcessing)
		}
		// NOTE: Default GetTask currently returns the Task, not TaskAndHistory, so history is not checked here.
	})

	// --- Test Case 2: Task Not Found ---
	t.Run("Task Not Found", func(t *testing.T) {
		params := &TaskGetParams{ID: "non-existent-id"}

		// 2. Execute
		mockStore.loadCalled = false // Reset flag
		returnedTask, err := adapter.GetTask(params)

		// 3. Assert
		if err == nil {
			t.Fatal("Default GetTask did not return error when task not found")
		}
		if !errors.Is(err, ErrTaskNotFound) { // Check if the error wraps ErrTaskNotFound
			// If not using wrapped errors in adapter, check specific error string:
			// if err.Error() != ErrTaskNotFound.Error() { ... }
			t.Errorf("Default GetTask returned wrong error type: got %v, want %v", err, ErrTaskNotFound)
		}
		if !mockStore.loadCalled {
			t.Error("Expected store.Load to be called for non-existent task")
		}
		if mockStore.lastLoadedID != "non-existent-id" {
			t.Errorf("Store loaded wrong ID for non-existent task: got %q, want %q", mockStore.lastLoadedID, "non-existent-id")
		}
		if returnedTask != nil {
			t.Errorf("Default GetTask returned non-nil task when task not found: %v", returnedTask)
		}
	})
}

func TestHandlerAdapter_DefaultCancelTask(t *testing.T) {
	// --- Test Case 1: Successful Cancellation ---
	t.Run("Successful Cancellation", func(t *testing.T) {
		// 1. Setup
		mockStore := newMockTaskStore()
		testTaskID := "cancel-task-test-id"
		preSavedTask := &Task{
			ID:     testTaskID,
			Status: TaskStatus{State: StateProcessing},
		}
		preSavedHistory := []Message{{Role: RoleUser, Parts: []Part{TextPart{Text: "Process this"}}}}
		preSavedData := &TaskAndHistory{
			Task:    preSavedTask,
			History: preSavedHistory,
		}
		if err := mockStore.Save(preSavedData); err != nil {
			t.Fatalf("Failed to pre-save task: %v", err)
		}

		srv := newTestServer(t, HandlerFuncs{}, mockStore)
		adapter := srv.handler.(*handlerAdapter)

		params := &TaskCancelParams{ID: testTaskID}

		// 2. Execute
		returnedTask, err := adapter.CancelTask(params)

		// 3. Assert
		if err != nil {
			t.Fatalf("Default CancelTask failed unexpectedly: %v", err)
		}
		if !mockStore.loadCalled {
			t.Error("Expected store.Load to be called")
		}
		if mockStore.lastLoadedID != testTaskID {
			t.Errorf("Store loaded wrong ID: got %q, want %q", mockStore.lastLoadedID, testTaskID)
		}
		if !mockStore.saveCalled {
			t.Error("Expected store.Save to be called")
		}
		if mockStore.lastSaved == nil {
			t.Fatal("store.Save called, but lastSaved is nil")
		}
		if mockStore.lastSaved.Task.Status.State != StateCanceled {
			t.Errorf("Saved task state mismatch: got %q, want %q", mockStore.lastSaved.Task.Status.State, StateCanceled)
		}
		// Check if cancellation message was added to history
		if len(mockStore.lastSaved.History) != 2 {
			t.Errorf("Saved history length mismatch: got %d, want 2", len(mockStore.lastSaved.History))
		} else if mockStore.lastSaved.History[1].Role != RoleAgent || len(mockStore.lastSaved.History[1].Parts) == 0 {
			t.Error("Expected cancellation message in history")
		}

		if returnedTask == nil {
			t.Fatal("Default CancelTask returned nil task")
		}
		if returnedTask.Status.State != StateCanceled {
			t.Errorf("Returned task state mismatch: got %q, want %q", returnedTask.Status.State, StateCanceled)
		}
		// TODO: Assert server.NotifyTaskUpdate was called if possible
	})

	// --- Test Case 2: Task Not Found ---
	t.Run("Task Not Found", func(t *testing.T) {
		// 1. Setup
		mockStore := newMockTaskStore() // Empty store
		srv := newTestServer(t, HandlerFuncs{}, mockStore)
		adapter := srv.handler.(*handlerAdapter)
		params := &TaskCancelParams{ID: "non-existent-id"}

		// 2. Execute
		_, err := adapter.CancelTask(params)

		// 3. Assert
		if err == nil {
			t.Fatal("Default CancelTask did not return error for non-existent task")
		}
		if !errors.Is(err, ErrTaskNotFound) {
			t.Errorf("Default CancelTask returned wrong error type: got %v, want %v", err, ErrTaskNotFound)
		}
		if !mockStore.loadCalled {
			t.Error("Expected store.Load to be called")
		}
		if mockStore.saveCalled {
			t.Error("store.Save should not have been called")
		}
	})

	// --- Test Case 3: Task Already Completed ---
	t.Run("Task Already Completed", func(t *testing.T) {
		// 1. Setup
		mockStore := newMockTaskStore()
		testTaskID := "completed-task-id"
		preSavedTask := &Task{ID: testTaskID, Status: TaskStatus{State: StateCompleted}}
		preSavedData := &TaskAndHistory{Task: preSavedTask}
		if err := mockStore.Save(preSavedData); err != nil {
			t.Fatalf("Failed to pre-save task: %v", err)
		}
		srv := newTestServer(t, HandlerFuncs{}, mockStore)
		adapter := srv.handler.(*handlerAdapter)
		params := &TaskCancelParams{ID: testTaskID}

		// 2. Execute
		_, err := adapter.CancelTask(params)

		// 3. Assert
		if err == nil {
			t.Fatal("Default CancelTask did not return error for completed task")
		}
		// Expect a specific error message, maybe TaskCannotBeCanceledCode if mapped
		// For now, just check for any error
		if !mockStore.loadCalled {
			t.Error("Expected store.Load to be called")
		}
	})
}

func TestHandlerAdapter_DefaultSendTaskSubscribe(t *testing.T) {
	// 1. Setup
	mockStore := newMockTaskStore()
	// Use empty HandlerFuncs to trigger default behavior
	srv := newTestServer(t, HandlerFuncs{}, mockStore)
	adapter := srv.handler.(*handlerAdapter) // Get the internal adapter

	inputMsg := Message{
		Role:  RoleUser,
		Parts: []Part{TextPart{Text: "Test default subscribe"}},
	}
	testCtx := &TaskContext{
		ID:      "test-default-subscribe-task", // Provide an ID
		Message: inputMsg,
	}

	// 2. Execute
	returnedTask, err := adapter.SendTaskSubscribe(testCtx)

	// 3. Assert
	if err != nil {
		t.Fatalf("Default SendTaskSubscribe failed: %v", err)
	}

	if returnedTask == nil {
		t.Fatal("Default SendTaskSubscribe returned nil task")
	}

	if returnedTask.ID != testCtx.ID {
		t.Errorf("Returned task ID mismatch: got %q, want %q", returnedTask.ID, testCtx.ID)
	}

	if returnedTask.Status.State != StatePending {
		t.Errorf("Returned task state mismatch: got %q, want %q", returnedTask.Status.State, StatePending)
	}

	// Check store interactions
	if !mockStore.saveCalled {
		t.Error("Expected store.Save to be called, but it wasn't")
	}

	if mockStore.lastSaved == nil {
		t.Fatal("Store.Save was called, but lastSaved data is nil")
	}

	if mockStore.lastSaved.Task.ID != testCtx.ID {
		t.Errorf("Saved task ID mismatch: got %q, want %q", mockStore.lastSaved.Task.ID, testCtx.ID)
	}

	if mockStore.lastSaved.Task.Status.State != StatePending {
		t.Errorf("Saved task state mismatch: got %q, want %q", mockStore.lastSaved.Task.Status.State, StatePending)
	}

	if len(mockStore.lastSaved.History) != 1 {
		t.Errorf("Saved history length mismatch: got %d, want 1", len(mockStore.lastSaved.History))
	} else if mockStore.lastSaved.History[0].Parts[0].(TextPart).Text != "Test default subscribe" {
		t.Errorf("Saved history message content mismatch: got %v, want %v", mockStore.lastSaved.History[0], inputMsg)
	}
}

func TestHandlerAdapter_UserSendTaskSubscribe(t *testing.T) {
	// 1. Setup
	mockStore := newMockTaskStore()
	userFuncCalled := false
	updateFnCalledCount := 0
	var wg sync.WaitGroup // To wait for the goroutine in user func

	// Define a user function that sends updates via UpdateFn
	userSubscribeFunc := func(ctx *TaskContext) (*Task, error) {
		t.Helper()
		userFuncCalled = true

		if ctx == nil || ctx.CurrentTask == nil || ctx.UpdateFn == nil {
			t.Errorf("User subscribe func received nil context, CurrentTask, or UpdateFn")
			return nil, fmt.Errorf("invalid context for subscribe")
		}
		if ctx.CurrentTask.Status.State != StatePending {
			t.Errorf("User subscribe func expected CurrentTask state %q, got %q", StatePending, ctx.CurrentTask.Status.State)
		}

		wg.Add(1) // Signal that the goroutine is starting
		go func() {
			defer wg.Done() // Signal completion

			// Send Processing status
			processingStatus := TaskStatus{State: StateProcessing}
			if err := ctx.UpdateFn(processingStatus); err == nil {
				updateFnCalledCount++
			} else {
				t.Errorf("UpdateFn call failed for processing status: %v", err)
			}
			time.Sleep(5 * time.Millisecond) // Small delay

			// Send Artifact
			artifact := Artifact{Name: "stream-artifact"}
			if err := ctx.UpdateFn(artifact); err == nil {
				updateFnCalledCount++
			} else {
				t.Errorf("UpdateFn call failed for artifact: %v", err)
			}
			time.Sleep(5 * time.Millisecond)

			// Send Completed status
			finalMsg := Message{Role: RoleAgent, Parts: []Part{TextPart{Text: "Stream done"}}}
			completedStatus := TaskStatus{State: StateCompleted, Message: &finalMsg}
			if err := ctx.UpdateFn(completedStatus); err == nil {
				updateFnCalledCount++
			} else {
				t.Errorf("UpdateFn call failed for completed status: %v", err)
			}
		}()

		// User subscribe func returns the initial task immediately
		return ctx.CurrentTask, nil
	}

	handlerFuncs := HandlerFuncs{
		SendTaskSubscribeFunc: userSubscribeFunc,
	}
	srv := newTestServer(t, handlerFuncs, mockStore)
	adapter := srv.handler.(*handlerAdapter)

	inputMsg := Message{
		Role:  RoleUser,
		Parts: []Part{TextPart{Text: "Test user subscribe"}},
	}
	testCtx := &TaskContext{
		ID:      "test-user-subscribe-task",
		Message: inputMsg,
	}

	// 2. Execute
	initialReturnedTask, err := adapter.SendTaskSubscribe(testCtx)

	// 3. Assert initial call results
	if err != nil {
		t.Fatalf("User SendTaskSubscribe initial call failed: %v", err)
	}
	if !userFuncCalled {
		t.Error("User SendTaskSubscribeFunc was not called")
	}
	if initialReturnedTask == nil {
		t.Fatal("User SendTaskSubscribe initial call returned nil task")
	}
	if initialReturnedTask.ID != testCtx.ID {
		t.Errorf("Initial returned task ID mismatch: got %q, want %q", initialReturnedTask.ID, testCtx.ID)
	}
	if initialReturnedTask.Status.State != StatePending {
		t.Errorf("Initial returned task state mismatch: got %q, want %q", initialReturnedTask.Status.State, StatePending)
	}

	// 4. Wait for the user function's goroutine to finish updates
	wg.Wait()

	// 5. Assert final state in store after updates
	if updateFnCalledCount != 3 {
		t.Errorf("Expected UpdateFn to be called 3 times, got %d", updateFnCalledCount)
	}

	finalStoredData, loadErr := mockStore.Load(testCtx.ID)
	if loadErr != nil {
		t.Fatalf("Failed to load final task state from store: %v", loadErr)
	}
	if finalStoredData == nil {
		t.Fatal("Final task data not found in store")
	}

	if finalStoredData.Task.Status.State != StateCompleted {
		t.Errorf("Final stored task state mismatch: got %q, want %q", finalStoredData.Task.Status.State, StateCompleted)
	}
	if finalStoredData.Task.Status.Message == nil || finalStoredData.Task.Status.Message.Parts[0].(TextPart).Text != "Stream done" {
		t.Errorf("Final stored task message mismatch: got %v", finalStoredData.Task.Status.Message)
	}
	if len(finalStoredData.Task.Artifacts) != 1 || finalStoredData.Task.Artifacts[0].Name != "stream-artifact" {
		t.Errorf("Final stored task artifacts mismatch: got %v", finalStoredData.Task.Artifacts)
	}

	// Check history contains initial user message and final agent message
	if len(finalStoredData.History) != 2 {
		// Note: Intermediate updates might or might not add to history depending on implementation
		// Current applyUpdateToTaskAndHistory only adds agent message from final status.
		t.Errorf("Final stored history length mismatch: got %d, want 2", len(finalStoredData.History))
	} else {
		if finalStoredData.History[0].Role != RoleUser {
			t.Errorf("Final stored history[0] role mismatch")
		}
		if finalStoredData.History[1].Role != RoleAgent {
			t.Errorf("Final stored history[1] role mismatch")
		}
	}
}

func TestHandlerAdapter_DefaultResubscribe(t *testing.T) {
	// --- Test Case 1: Task Found ---
	t.Run("Task Found", func(t *testing.T) {
		// 1. Setup
		mockStore := newMockTaskStore()
		testTaskID := "resubscribe-found-id"
		preSavedData := &TaskAndHistory{Task: &Task{ID: testTaskID, Status: TaskStatus{State: StateProcessing}}}
		if err := mockStore.Save(preSavedData); err != nil {
			t.Fatalf("Failed to pre-save task: %v", err)
		}

		srv := newTestServer(t, HandlerFuncs{}, mockStore)
		adapter := srv.handler.(*handlerAdapter)
		params := &TaskGetParams{ID: testTaskID} // Resubscribe uses TaskGetParams

		// 2. Execute
		err := adapter.Resubscribe(params)

		// 3. Assert
		if err != nil {
			t.Fatalf("Default Resubscribe failed when task should exist: %v", err)
		}
		if !mockStore.loadCalled {
			t.Error("Expected store.Load to be called")
		}
		if mockStore.lastLoadedID != testTaskID {
			t.Errorf("Store loaded wrong ID: got %q, want %q", mockStore.lastLoadedID, testTaskID)
		}
	})

	// --- Test Case 2: Task Not Found ---
	t.Run("Task Not Found", func(t *testing.T) {
		// 1. Setup
		mockStore := newMockTaskStore() // Empty store
		srv := newTestServer(t, HandlerFuncs{}, mockStore)
		adapter := srv.handler.(*handlerAdapter)
		params := &TaskGetParams{ID: "non-existent-resub-id"}

		// 2. Execute
		err := adapter.Resubscribe(params)

		// 3. Assert
		if err == nil {
			t.Fatal("Default Resubscribe did not return error when task not found")
		}
		if !errors.Is(err, ErrTaskNotFound) {
			t.Errorf("Default Resubscribe returned wrong error type: got %v, want %v", err, ErrTaskNotFound)
		}
		if !mockStore.loadCalled {
			t.Error("Expected store.Load to be called for non-existent task")
		}
		if mockStore.lastLoadedID != "non-existent-resub-id" {
			t.Errorf("Store loaded wrong ID for non-existent task: got %q, want %q", mockStore.lastLoadedID, "non-existent-resub-id")
		}
	})
}

// --- Helper Function Tests ---

func TestServer_loadOrCreateTaskAndHistory(t *testing.T) {
	// --- Test Case 1: Task Exists ---
	t.Run("Task Exists", func(t *testing.T) {
		// 1. Setup
		mockStore := newMockTaskStore()
		existingTaskID := "load-existing-id"
		existingTask := &Task{ID: existingTaskID, Status: TaskStatus{State: StateProcessing}}
		existingHistory := []Message{{Role: RoleUser, Parts: []Part{TextPart{Text: "Original msg"}}}}
		existingData := &TaskAndHistory{Task: existingTask, History: existingHistory}
		if err := mockStore.Save(existingData); err != nil {
			t.Fatalf("Failed to pre-save task: %v", err)
		}

		srv := newTestServer(t, HandlerFuncs{}, mockStore) // Server needed for the method
		initialMsg := Message{Role: RoleUser, Parts: []Part{TextPart{Text: "New msg"}}}

		// 2. Execute
		returnedData, returnedID, err := srv.loadOrCreateTaskAndHistory(existingTaskID, initialMsg, "session1", nil)

		// 3. Assert
		if err != nil {
			t.Fatalf("loadOrCreate failed when task exists: %v", err)
		}
		if !mockStore.loadCalled {
			t.Error("Expected store.Load to be called")
		}
		if mockStore.lastLoadedID != existingTaskID {
			t.Errorf("Loaded wrong ID: got %q, want %q", mockStore.lastLoadedID, existingTaskID)
		}
		if returnedID != existingTaskID {
			t.Errorf("Returned wrong ID: got %q, want %q", returnedID, existingTaskID)
		}
		if returnedData == nil || returnedData.Task == nil {
			t.Fatal("Returned nil data or task")
		}
		if returnedData.Task.ID != existingTaskID {
			t.Errorf("Returned task ID mismatch: got %q", returnedData.Task.ID)
		}
		if returnedData.Task.Status.State != StateProcessing { // Should return existing state
			t.Errorf("Returned task state mismatch: got %q", returnedData.Task.Status.State)
		}
		// Check history contains original + new message
		if len(returnedData.History) != 2 {
			t.Errorf("Returned history length mismatch: got %d, want 2", len(returnedData.History))
		} else {
			if returnedData.History[0].Parts[0].(TextPart).Text != "Original msg" {
				t.Error("History[0] mismatch")
			}
			if returnedData.History[1].Parts[0].(TextPart).Text != "New msg" {
				t.Error("History[1] mismatch")
			}
		}
	})

	// --- Test Case 2: Task Does Not Exist (ID hint provided) ---
	t.Run("Task Not Exist with ID Hint", func(t *testing.T) {
		// 1. Setup
		mockStore := newMockTaskStore() // Empty store
		srv := newTestServer(t, HandlerFuncs{}, mockStore)
		initialMsg := Message{Role: RoleUser, Parts: []Part{TextPart{Text: "First msg"}}}
		hintID := "create-with-hint-id"

		// 2. Execute
		returnedData, returnedID, err := srv.loadOrCreateTaskAndHistory(hintID, initialMsg, "session2", map[string]interface{}{"key": "value"})

		// 3. Assert
		if err != nil {
			t.Fatalf("loadOrCreate failed when creating new task with hint: %v", err)
		}
		if !mockStore.loadCalled {
			t.Error("Expected store.Load to be called (even for create with hint)")
		}
		if mockStore.lastLoadedID != hintID {
			t.Errorf("Loaded wrong ID: got %q, want %q", mockStore.lastLoadedID, hintID)
		}
		if returnedID != hintID {
			t.Errorf("Returned wrong ID: got %q, want %q", returnedID, hintID)
		}
		if returnedData == nil || returnedData.Task == nil {
			t.Fatal("Returned nil data or task")
		}
		if returnedData.Task.ID != hintID {
			t.Errorf("Returned task ID mismatch: got %q", returnedData.Task.ID)
		}
		if returnedData.Task.Status.State != StatePending { // Should create in Pending state
			t.Errorf("Returned task state mismatch: got %q, want %q", returnedData.Task.Status.State, StatePending)
		}
		if returnedData.Task.SessionID != "session2" {
			t.Errorf("Returned task sessionID mismatch: got %q", returnedData.Task.SessionID)
		}
		if _, ok := returnedData.Task.Metadata["key"]; !ok || returnedData.Task.Metadata["key"] != "value" {
			t.Errorf("Returned task metadata mismatch: got %v", returnedData.Task.Metadata)
		}
		if len(returnedData.History) != 1 || returnedData.History[0].Parts[0].(TextPart).Text != "First msg" {
			t.Errorf("Returned history mismatch: got %v", returnedData.History)
		}
		if mockStore.saveCalled { // Should NOT save
			t.Error("store.Save should not have been called by loadOrCreate")
		}
	})

	// --- Test Case 3: Task Does Not Exist (No ID hint) ---
	t.Run("Task Not Exist No ID Hint", func(t *testing.T) {
		// 1. Setup
		mockStore := newMockTaskStore() // Empty store
		srv := newTestServer(t, HandlerFuncs{}, mockStore)
		initialMsg := Message{Role: RoleUser, Parts: []Part{TextPart{Text: "Another first msg"}}}

		// 2. Execute
		returnedData, returnedID, err := srv.loadOrCreateTaskAndHistory("", initialMsg, "session3", nil)

		// 3. Assert
		if err != nil {
			t.Fatalf("loadOrCreate failed when creating new task without hint: %v", err)
		}
		if mockStore.loadCalled { // Should NOT load if ID hint is empty
			t.Error("store.Load should not be called when ID hint is empty")
		}
		if returnedID == "" {
			t.Error("Returned ID is empty, should have generated one")
		}
		// Validate UUID format (simple check)
		if _, uuidErr := uuid.Parse(returnedID); uuidErr != nil {
			t.Errorf("Returned ID %q is not a valid UUID: %v", returnedID, uuidErr)
		}
		if returnedData == nil || returnedData.Task == nil {
			t.Fatal("Returned nil data or task")
		}
		if returnedData.Task.ID != returnedID {
			t.Errorf("Returned task ID does not match generated ID: task=%q, generated=%q", returnedData.Task.ID, returnedID)
		}
		if returnedData.Task.Status.State != StatePending {
			t.Errorf("Returned task state mismatch: got %q, want %q", returnedData.Task.Status.State, StatePending)
		}
		if returnedData.Task.SessionID != "session3" {
			t.Errorf("Returned task sessionID mismatch: got %q", returnedData.Task.SessionID)
		}
		if len(returnedData.History) != 1 || returnedData.History[0].Parts[0].(TextPart).Text != "Another first msg" {
			t.Errorf("Returned history mismatch: got %v", returnedData.History)
		}
		if mockStore.saveCalled { // Should NOT save
			t.Error("store.Save should not have been called by loadOrCreate")
		}
	})
}

// TODO: Add test for applyUpdateToTaskAndHistory
