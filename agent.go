package a2a

// AgentProvider represents the service provider of the agent.
type AgentProvider struct {
	Organization string `json:"organization"`
	URL          string `json:"url"`
}

// AgentCapabilities, AgentAuthentication, AgentSkill, AgentCard
// definitions have been moved to schema.go to consolidate types.
