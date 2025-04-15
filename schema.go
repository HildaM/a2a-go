package a2a

import (
	"encoding/json"
	"fmt"
	"log"
	"time"
)

// --- Core Schema Types based on schema.ts ---

// TaskState defines the possible states of a task.
type TaskState string

const (
	StateUnknown    TaskState = "unknown"
	StatePending    TaskState = "pending"
	StateProcessing TaskState = "processing"
	StateCompleted  TaskState = "completed"
	StateFailed     TaskState = "failed"
	StateCanceled   TaskState = "canceled"
)

// Role defines the originator of a message (user or agent).
type Role string

const (
	RoleUser  Role = "user"
	RoleAgent Role = "agent"
)

// Part defines the interface for different types of message content.
type Part interface {
	Type() string
	// TODO: Consider adding GetMetadata() back if needed by handlers?
}

// --- Concrete Part Types (Moved from task.go logic) ---

// TextPart represents plain text content.
type TextPart struct {
	Text string `json:"text"`
}

func (t TextPart) Type() string { return "text" }

// FileData represents the content of a file, either as bytes or a URI.
type FileData struct {
	Bytes *[]byte `json:"bytes,omitempty"` // Base64 encoded bytes
	URI   *string `json:"uri,omitempty"`
}

// FilePart represents a file attachment.
type FilePart struct {
	File     FileData `json:"file"`
	MimeType string   `json:"mimeType,omitempty"`
}

func (f FilePart) Type() string { return "file" }

// DataPart represents generic structured data.
type DataPart struct {
	Data     interface{} `json:"data"`
	MimeType string      `json:"mimeType,omitempty"`
}

func (d DataPart) Type() string { return "data" }

// ToolCallPart represents a request from the agent to call a tool.
// TODO: Define ToolCallPart if needed based on schema/usage

// ToolResultPart represents the result of a tool call.
// TODO: Define ToolResultPart if needed based on schema/usage

// --- Message Struct (Moved from task.go, Role type corrected) ---

// Message represents a message exchanged within a task.
type Message struct {
	Role     Role                   `json:"role"` // Corrected to use schema.Role
	Parts    []Part                 `json:"parts"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// Alias type to avoid recursion during marshaling/unmarshaling
type messageAlias Message

// MarshalJSON implements custom marshaling for Message.
func (m Message) MarshalJSON() ([]byte, error) {
	rawBytes, err := json.Marshal(messageAlias(m))
	if err != nil {
		return nil, fmt.Errorf("failed to marshal message alias: %w", err)
	}
	var temp map[string]interface{}
	if err := json.Unmarshal(rawBytes, &temp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal message alias to map: %w", err)
	}

	marshaledParts := make([]json.RawMessage, 0, len(m.Parts))
	for _, p := range m.Parts {
		partBytes, err := marshalPartWithType(p) // Use helper
		if err != nil {
			return nil, fmt.Errorf("failed to marshal part: %w", err)
		}
		marshaledParts = append(marshaledParts, partBytes)
	}
	temp["parts"] = marshaledParts

	return json.Marshal(temp)
}

// UnmarshalJSON implements custom unmarshaling for Message.
func (m *Message) UnmarshalJSON(data []byte) error {
	var alias messageAlias
	if err := json.Unmarshal(data, &alias); err != nil {
		return fmt.Errorf("failed to unmarshal message alias: %w", err)
	}
	*m = Message(alias) // Copy non-Part fields

	var temp map[string]json.RawMessage
	if err := json.Unmarshal(data, &temp); err != nil {
		return fmt.Errorf("failed to unmarshal message to map: %w", err)
	}

	rawParts, ok := temp["parts"]
	if !ok || string(rawParts) == "null" {
		m.Parts = nil
		return nil
	}

	var rawPartsSlice []json.RawMessage
	if err := json.Unmarshal(rawParts, &rawPartsSlice); err != nil {
		return fmt.Errorf("failed to unmarshal parts array: %w", err)
	}

	m.Parts = make([]Part, 0, len(rawPartsSlice))
	for i, rawPart := range rawPartsSlice {
		p, err := unmarshalPart(rawPart) // Use helper
		if err != nil {
			return fmt.Errorf("failed to unmarshal part at index %d: %w", i, err)
		}
		m.Parts = append(m.Parts, p)
	}

	return nil
}

// --- Artifact, TaskStatus, Task (Moved from task.go) ---

// Artifact represents data generated or used during a task.
type Artifact struct {
	Name        string                 `json:"name,omitempty"`
	Description string                 `json:"description,omitempty"`
	Parts       []Part                 `json:"parts"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
	Index       int                    `json:"index,omitempty"`
	LastChunk   bool                   `json:"lastChunk,omitempty"`
	Append      bool                   `json:"append,omitempty"`
}

// TaskStatus represents the status of a task.
type TaskStatus struct {
	State     TaskState `json:"state"`
	Message   *Message  `json:"message,omitempty"`
	Timestamp string    `json:"timestamp,omitempty"`
}

// Helper to set TaskStatus timestamp with proper formatting
func (ts *TaskStatus) SetTimestamp(t time.Time) {
	tsStr := t.UTC().Format(time.RFC3339Nano)
	ts.Timestamp = tsStr
}

// Task represents a stateful entity for collaboration.
type Task struct {
	ID        string                 `json:"id"`
	SessionID string                 `json:"sessionId,omitempty"`
	Status    TaskStatus             `json:"status"`
	Artifacts []Artifact             `json:"artifacts,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// --- Agent Configuration Types ---

// AgentAuthentication defines authentication methods supported/required.
type AgentAuthentication struct {
	Schemes []string `json:"schemes"` // e.g., ["None"], ["Bearer"], ["OAuth2"]
	// Add fields for specific schemes if needed, e.g., OAuth2 URLs
}

// AgentCapabilities describes optional features the agent supports.
type AgentCapabilities struct {
	Streaming         bool `json:"streaming,omitempty"`
	PushNotifications bool `json:"pushNotifications,omitempty"`
}

// AgentSkill describes a specific capability or function of the agent.
type AgentSkill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Examples    []string `json:"examples,omitempty"`
}

// AgentCard provides metadata about the A2A agent.
type AgentCard struct {
	Name               string              `json:"name"`
	Description        string              `json:"description,omitempty"`
	URL                string              `json:"url"` // Base URL where the agent is hosted
	Version            string              `json:"version"`
	IconURI            string              `json:"iconUri,omitempty"`
	Capabilities       AgentCapabilities   `json:"capabilities"`
	Authentication     AgentAuthentication `json:"authentication"`
	DefaultInputModes  []string            `json:"defaultInputModes,omitempty"`
	DefaultOutputModes []string            `json:"defaultOutputModes,omitempty"`
	Skills             []AgentSkill        `json:"skills,omitempty"`
}

// --- RPC Request/Response Structures (Single definition) ---
type RPCRequest struct {
	Jsonrpc string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	ID      interface{}     `json:"id"`
}
type RPCResponse struct {
	Jsonrpc string      `json:"jsonrpc"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
	ID      interface{} `json:"id"`
}
type RPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// --- Part Marshal/Unmarshal Helpers (Moved from task.go) ---

// Helper function to marshal a Part interface with its type field.
func marshalPartWithType(p Part) (json.RawMessage, error) {
	concreteBytes, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("marshal concrete part (%T): %w", p, err)
	}
	var temp map[string]interface{}
	if err := json.Unmarshal(concreteBytes, &temp); err != nil {
		return nil, fmt.Errorf("unmarshal concrete part to map: %w", err)
	}
	temp["type"] = p.Type()
	return json.Marshal(temp)
}

// Helper function to unmarshal a raw JSON message into a Part interface.
func unmarshalPart(rawPart json.RawMessage) (Part, error) {
	var typeDetect struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(rawPart, &typeDetect); err != nil {
		log.Printf("Warning: Part type detection failed or type missing in JSON: %v. Assuming text part.", err)
		typeDetect.Type = "text"
	}
	if typeDetect.Type == "" {
		typeDetect.Type = "text"
	}

	var concretePart Part
	switch typeDetect.Type {
	case "text":
		var tp TextPart
		if err := json.Unmarshal(rawPart, &tp); err != nil {
			return nil, fmt.Errorf("unmarshal TextPart: %w", err)
		}
		concretePart = tp
	case "file":
		var fp FilePart
		if err := json.Unmarshal(rawPart, &fp); err != nil {
			return nil, fmt.Errorf("unmarshal FilePart: %w", err)
		}
		if (fp.File.Bytes == nil && fp.File.URI == nil) || (fp.File.Bytes != nil && fp.File.URI != nil) {
			return nil, fmt.Errorf("invalid FilePart: exactly one of 'bytes' or 'uri' must be provided")
		}
		concretePart = fp
	case "data":
		var dp DataPart
		if err := json.Unmarshal(rawPart, &dp); err != nil {
			return nil, fmt.Errorf("unmarshal DataPart: %w", err)
		}
		concretePart = dp
	// TODO: Add cases for ToolCallPart, ToolResultPart if defined
	default:
		return nil, fmt.Errorf("unknown part type: %s", typeDetect.Type)
	}
	return concretePart, nil
}

// --- RPC Error Constants (Single definition) ---
const (
	ParseErrorCode             = -32700
	InvalidRequestCode         = -32600
	MethodNotFoundCode         = -32601
	InvalidParamsCode          = -32602
	InternalErrorCode          = -32603
	TaskNotFoundCode           = -32001
	TaskCannotBeCanceledCode   = -32002
	PushNotifyNotSupportedCode = -32003
	UnsupportedOperationCode   = -32004
)

// --- RPC Error Constructor (Single definition) ---
func NewRPCError(code int, message string, data interface{}) *RPCError {
	return &RPCError{Code: code, Message: message, Data: data}
}

// --- Parameter and Result Structs for RPC Methods (from server.go initially) ---

// TaskContext, TaskIdParams, TaskGetParams, TaskCancelParams should be defined elsewhere
// (likely in server.go or a dedicated params.go, but NOT duplicated here)

// End of file
