package a2a

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// TaskAndHistory bundles a Task with its associated message history.
// This mirrors the structure used in the FileStore implementation.
type TaskAndHistory struct {
	Task    *Task     `json:"task"`    // Pointer to the main task object
	History []Message `json:"history"` // Slice containing the message history
}

// TaskStore defines the interface for saving and loading task data.
// Implementations handle the underlying storage mechanism (e.g., memory, files).
type TaskStore interface {
	// Save stores the task and its history.
	// It should overwrite existing data if a task with the same ID exists.
	Save(data *TaskAndHistory) error

	// Load retrieves the task and its history by task ID.
	// Returns the data and nil error if found.
	// Returns nil data and nil error if not found.
	// Returns nil data and an error for other issues (e.g., read errors).
	Load(taskID string) (*TaskAndHistory, error)

	// Delete removes the task and its associated history.
	// Returns nil error if successful or if the task didn't exist.
	// Returns an error for other issues (e.g., delete errors).
	Delete(taskID string) error

	// TODO: Consider adding methods for incremental updates if needed,
	// e.g., AddMessageToHistory(taskID string, message Message) error
}

// Define standard errors related to storage
var (
	ErrTaskNotFound = fmt.Errorf("task not found in store")
	// Add other potential store-related errors here
)

// --- InMemoryTaskStore Implementation ---

// InMemoryTaskStore implements TaskStore using an in-memory map.
// It is safe for concurrent use.
type InMemoryTaskStore struct {
	mu    sync.RWMutex
	store map[string]*TaskAndHistory
}

// Ensure InMemoryTaskStore satisfies the TaskStore interface
var _ TaskStore = (*InMemoryTaskStore)(nil)

// NewInMemoryTaskStore creates a new in-memory task store.
func NewInMemoryTaskStore() *InMemoryTaskStore {
	return &InMemoryTaskStore{
		store: make(map[string]*TaskAndHistory),
	}
}

// Save stores the task and history data in the map.
func (ms *InMemoryTaskStore) Save(data *TaskAndHistory) error {
	if data == nil || data.Task == nil {
		return fmt.Errorf("cannot save nil task data to memory store")
	}
	ms.mu.Lock()
	defer ms.mu.Unlock()
	// Store copies to prevent external mutation issues if caller reuses Task/History slices
	storeCopy := &TaskAndHistory{}
	if data.Task != nil {
		taskCopy := *data.Task // Shallow copy of task is usually ok
		storeCopy.Task = &taskCopy
	}
	if data.History != nil {
		historyCopy := make([]Message, len(data.History))
		copy(historyCopy, data.History) // Copy slice contents
		storeCopy.History = historyCopy
	}

	ms.store[data.Task.ID] = storeCopy
	return nil
}

// Load retrieves task and history data from the map.
func (ms *InMemoryTaskStore) Load(taskID string) (*TaskAndHistory, error) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	data, ok := ms.store[taskID]
	if !ok {
		return nil, nil // Not found
	}
	// Return copies to prevent external mutation issues
	returnCopy := &TaskAndHistory{}
	if data.Task != nil {
		taskCopy := *data.Task
		returnCopy.Task = &taskCopy
	}
	if data.History != nil {
		historyCopy := make([]Message, len(data.History))
		copy(historyCopy, data.History)
		returnCopy.History = historyCopy
	}
	return returnCopy, nil
}

// Delete removes the task data from the map.
func (ms *InMemoryTaskStore) Delete(taskID string) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	delete(ms.store, taskID)
	return nil
}

// --- FileTaskStore Implementation ---

// fileHistoryWrapper is used to match the JS structure for the history file.
type fileHistoryWrapper struct {
	History []Message `json:"history"`
}

// FileTaskStore implements TaskStore using the local filesystem.
// Tasks are stored as <baseDir>/<taskID>.json
// History is stored as <baseDir>/<taskID>.history.json
// It is safe for concurrent use.
type FileTaskStore struct {
	baseDir string
	mu      sync.Mutex // Mutex to protect file access for the same task ID
}

// Ensure FileTaskStore satisfies the TaskStore interface
var _ TaskStore = (*FileTaskStore)(nil)

// NewFileTaskStore creates a new file-based task store.
// It ensures the base directory exists.
// If baseDir is empty, it defaults to ".a2a-tasks" in the current directory.
func NewFileTaskStore(baseDir string) (*FileTaskStore, error) {
	if baseDir == "" {
		baseDir = ".a2a-tasks"
	}
	store := &FileTaskStore{
		baseDir: baseDir,
	}
	if err := store.ensureDirectoryExists(); err != nil {
		return nil, fmt.Errorf("failed to create base directory for FileTaskStore: %w", err)
	}
	return store, nil
}

// ensureDirectoryExists creates the base directory if it doesn't exist.
func (fs *FileTaskStore) ensureDirectoryExists() error {
	// Use MkdirAll which is idempotent (like recursive: true)
	if err := os.MkdirAll(fs.baseDir, 0750); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", fs.baseDir, err)
	}
	return nil
}

// sanitizeTaskID ensures the task ID is safe to use as a filename.
// It prevents directory traversal and returns an error for invalid IDs.
func sanitizeTaskID(taskID string) (string, error) {
	if taskID == "" {
		return "", fmt.Errorf("task ID cannot be empty")
	}
	// filepath.Base removes leading path components and trailing slashes.
	safeID := filepath.Base(taskID)
	// Double-check it didn't change the original ID in unexpected ways
	// and doesn't contain path separators after Base().
	if safeID != taskID || strings.ContainsAny(safeID, "/\\") {
		return "", fmt.Errorf("invalid task ID format: %s", taskID)
	}
	return safeID, nil
}

// getTaskFilePath returns the full path for the task JSON file.
func (fs *FileTaskStore) getTaskFilePath(taskID string) (string, error) {
	safeID, err := sanitizeTaskID(taskID)
	if err != nil {
		return "", err
	}
	return filepath.Join(fs.baseDir, safeID+".json"), nil
}

// getHistoryFilePath returns the full path for the history JSON file.
func (fs *FileTaskStore) getHistoryFilePath(taskID string) (string, error) {
	safeID, err := sanitizeTaskID(taskID)
	if err != nil {
		return "", err
	}
	return filepath.Join(fs.baseDir, safeID+".history.json"), nil
}

// Save stores the task and history data to separate JSON files.
func (fs *FileTaskStore) Save(data *TaskAndHistory) error {
	if data == nil || data.Task == nil {
		return fmt.Errorf("cannot save nil task data")
	}

	fs.mu.Lock()         // Lock before accessing files for this task
	defer fs.mu.Unlock() // Unlock when done

	taskFilePath, err := fs.getTaskFilePath(data.Task.ID)
	if err != nil {
		return fmt.Errorf("failed to get task file path: %w", err)
	}
	historyFilePath, err := fs.getHistoryFilePath(data.Task.ID)
	if err != nil {
		return fmt.Errorf("failed to get history file path: %w", err)
	}

	// Ensure directory exists (might be redundant if constructor succeeded, but safe)
	if err := fs.ensureDirectoryExists(); err != nil {
		return err
	}

	// Write Task file
	taskBytes, err := json.MarshalIndent(data.Task, "", "  ") // Use MarshalIndent for readability
	if err != nil {
		return fmt.Errorf("failed to marshal task data for %s: %w", data.Task.ID, err)
	}
	// Use a temporary file and rename for more atomic write
	tempTaskFile := taskFilePath + ".tmp"
	if err := os.WriteFile(tempTaskFile, taskBytes, 0640); err != nil {
		return fmt.Errorf("failed to write temporary task file %s: %w", tempTaskFile, err)
	}

	// Write History file
	historyWrapper := fileHistoryWrapper{History: data.History}
	historyBytes, err := json.MarshalIndent(historyWrapper, "", "  ")
	if err != nil {
		_ = os.Remove(tempTaskFile) // Attempt cleanup on error
		return fmt.Errorf("failed to marshal history data for %s: %w", data.Task.ID, err)
	}
	// Use a temporary file and rename for more atomic write
	tempHistoryFile := historyFilePath + ".tmp"
	if err := os.WriteFile(tempHistoryFile, historyBytes, 0640); err != nil {
		_ = os.Remove(tempTaskFile) // Attempt cleanup on error
		return fmt.Errorf("failed to write temporary history file %s: %w", tempHistoryFile, err)
	}

	// Rename temporary files to final names (atomic on most OSes)
	if err := os.Rename(tempTaskFile, taskFilePath); err != nil {
		_ = os.Remove(tempTaskFile)
		_ = os.Remove(tempHistoryFile)
		return fmt.Errorf("failed to rename temporary task file %s to %s: %w", tempTaskFile, taskFilePath, err)
	}
	if err := os.Rename(tempHistoryFile, historyFilePath); err != nil {
		// Attempt to rollback task file rename?
		// For now, log and return error, leaving task file possibly updated
		// A more robust solution might involve transactional logic or state tracking
		log.Printf("WARN: Failed to rename history file %s, task file %s might be updated: %v", tempHistoryFile, taskFilePath, err)
		_ = os.Remove(tempHistoryFile) // Clean up remaining temp history file
		return fmt.Errorf("failed to rename temporary history file %s to %s: %w", tempHistoryFile, historyFilePath, err)
	}

	return nil
}

// Load retrieves task and history data from JSON files.
func (fs *FileTaskStore) Load(taskID string) (*TaskAndHistory, error) {
	fs.mu.Lock()         // Lock before reading files for this task
	defer fs.mu.Unlock() // Unlock when done

	taskFilePath, err := fs.getTaskFilePath(taskID)
	if err != nil {
		return nil, fmt.Errorf("failed to get task file path for load: %w", err)
	}
	historyFilePath, err := fs.getHistoryFilePath(taskID)
	if err != nil {
		return nil, fmt.Errorf("failed to get history file path for load: %w", err)
	}

	// Load Task file
	taskBytes, err := os.ReadFile(taskFilePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil // Task not found, not an error for Load
		}
		return nil, fmt.Errorf("failed to read task file %s: %w", taskFilePath, err)
	}

	var task Task
	if err := json.Unmarshal(taskBytes, &task); err != nil {
		return nil, fmt.Errorf("failed to unmarshal task data from %s: %w", taskFilePath, err)
	}

	// Load History file
	var history []Message
	historyBytes, err := os.ReadFile(historyFilePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			history = make([]Message, 0) // History file doesn't exist, use empty history
		} else {
			// Other read error for history file
			return nil, fmt.Errorf("failed to read history file %s: %w", historyFilePath, err)
		}
	} else {
		// History file exists, try to unmarshal
		var historyWrapper fileHistoryWrapper
		if err := json.Unmarshal(historyBytes, &historyWrapper); err != nil {
			// Log warning similar to JS version if history file is malformed
			log.Printf("Warning: Malformed history file found for task %s at %s. Ignoring content. Error: %v", taskID, historyFilePath, err)
			history = make([]Message, 0) // Use empty history on unmarshal error
		} else {
			history = historyWrapper.History
		}
	}

	return &TaskAndHistory{
		Task:    &task,
		History: history,
	}, nil
}

// Delete removes the task and its associated history files.
func (fs *FileTaskStore) Delete(taskID string) error {
	fs.mu.Lock()         // Lock before deleting files for this task
	defer fs.mu.Unlock() // Unlock when done

	taskFilePath, err := fs.getTaskFilePath(taskID)
	if err != nil {
		return fmt.Errorf("failed to get task file path for delete: %w", err)
	}
	historyFilePath, err := fs.getHistoryFilePath(taskID)
	if err != nil {
		return fmt.Errorf("failed to get history file path for delete: %w", err)
	}

	// Attempt to remove task file, ignore ErrNotExist
	if err := os.Remove(taskFilePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to delete task file %s: %w", taskFilePath, err)
	}

	// Attempt to remove history file, ignore ErrNotExist
	if err := os.Remove(historyFilePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to delete history file %s: %w", historyFilePath, err)
	}

	return nil
}
