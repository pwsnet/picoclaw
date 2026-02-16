package agent

import (
	"os"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
)

func newTestMultiplexerConfig(t *testing.T) (*config.Config, string) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "mux-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = tmpDir
	cfg.Agents.Defaults.Model = "test-model"
	cfg.Agents.Defaults.MaxTokens = 4096
	cfg.Agents.Defaults.MaxToolIterations = 5
	// Need a provider key for CreateProviderForAgent to work
	cfg.Providers.OpenRouter.APIKey = "test-key"
	cfg.Agents.Defaults.Provider = "openrouter"

	return cfg, tmpDir
}

func TestNewAgentMultiplexer_SingleAgent(t *testing.T) {
	cfg, tmpDir := newTestMultiplexerConfig(t)
	defer os.RemoveAll(tmpDir)

	// No agents list defined — GetAgentConfigs returns one "defaults" agent
	msgBus := bus.NewMessageBus()
	mux, err := NewAgentMultiplexer(cfg, msgBus)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	names := mux.AgentNames()
	if len(names) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(names))
	}

	defaultAgent := mux.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent to exist")
	}
	if defaultAgent.Name() != "defaults" {
		t.Errorf("expected name 'defaults', got %q", defaultAgent.Name())
	}
}

func TestNewAgentMultiplexer_MultipleAgents(t *testing.T) {
	cfg, tmpDir := newTestMultiplexerConfig(t)
	defer os.RemoveAll(tmpDir)

	cfg.Agents.Agents = []config.AgentConfig{
		{Name: "defaults", Model: "glm-4.7"},
		{Name: "researcher", Model: "claude-sonnet", Workspace: tmpDir + "/researcher"},
	}

	msgBus := bus.NewMessageBus()
	mux, err := NewAgentMultiplexer(cfg, msgBus)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	names := mux.AgentNames()
	if len(names) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(names))
	}

	researcher := mux.GetAgent("researcher")
	if researcher == nil {
		t.Fatal("expected 'researcher' agent to exist")
	}
	if researcher.Name() != "researcher" {
		t.Errorf("expected name 'researcher', got %q", researcher.Name())
	}
}

func TestNewAgentMultiplexer_NoDefaultsAgent_Errors(t *testing.T) {
	cfg, tmpDir := newTestMultiplexerConfig(t)
	defer os.RemoveAll(tmpDir)

	// Define agents without "defaults"
	cfg.Agents.Agents = []config.AgentConfig{
		{Name: "researcher", Model: "claude-sonnet"},
	}

	msgBus := bus.NewMessageBus()
	_, err := NewAgentMultiplexer(cfg, msgBus)
	if err == nil {
		t.Fatal("expected error when no 'defaults' agent is defined")
	}
}

func TestAgentMultiplexer_ResolveAgent_ChannelRouting(t *testing.T) {
	cfg, tmpDir := newTestMultiplexerConfig(t)
	defer os.RemoveAll(tmpDir)

	cfg.Agents.Agents = []config.AgentConfig{
		{Name: "defaults", Model: "glm-4.7"},
		{Name: "researcher", Model: "claude-sonnet", Workspace: tmpDir + "/researcher"},
	}
	cfg.Channels.Telegram.DefaultAgent = "researcher"

	msgBus := bus.NewMessageBus()
	mux, err := NewAgentMultiplexer(cfg, msgBus)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Telegram should route to researcher
	telegramMsg := bus.InboundMessage{Channel: "telegram", ChatID: "123"}
	agent := mux.resolveAgent(telegramMsg)
	if agent == nil {
		t.Fatal("expected agent for telegram")
	}
	if agent.Name() != "researcher" {
		t.Errorf("expected 'researcher' for telegram, got %q", agent.Name())
	}

	// Discord (no default_agent) should route to defaults
	discordMsg := bus.InboundMessage{Channel: "discord", ChatID: "456"}
	agent = mux.resolveAgent(discordMsg)
	if agent == nil {
		t.Fatal("expected agent for discord")
	}
	if agent.Name() != "defaults" {
		t.Errorf("expected 'defaults' for discord, got %q", agent.Name())
	}
}

func TestAgentMultiplexer_ResolveAgent_MetadataRouting(t *testing.T) {
	cfg, tmpDir := newTestMultiplexerConfig(t)
	defer os.RemoveAll(tmpDir)

	cfg.Agents.Agents = []config.AgentConfig{
		{Name: "defaults", Model: "glm-4.7"},
		{Name: "coder", Model: "gpt-4o", Workspace: tmpDir + "/coder"},
	}

	msgBus := bus.NewMessageBus()
	mux, err := NewAgentMultiplexer(cfg, msgBus)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Message with target_agent metadata should go to that agent
	msg := bus.InboundMessage{
		Channel:  "telegram",
		ChatID:   "123",
		Metadata: map[string]string{"target_agent": "coder"},
	}
	agent := mux.resolveAgent(msg)
	if agent == nil {
		t.Fatal("expected agent for metadata routing")
	}
	if agent.Name() != "coder" {
		t.Errorf("expected 'coder' via metadata, got %q", agent.Name())
	}
}

func TestAgentMultiplexer_ResolveAgent_InvalidChannelAgent(t *testing.T) {
	cfg, tmpDir := newTestMultiplexerConfig(t)
	defer os.RemoveAll(tmpDir)

	cfg.Agents.Agents = []config.AgentConfig{
		{Name: "defaults", Model: "glm-4.7"},
	}
	// Set a channel's default_agent to a non-existent agent
	cfg.Channels.Telegram.DefaultAgent = "nonexistent"

	msgBus := bus.NewMessageBus()
	mux, err := NewAgentMultiplexer(cfg, msgBus)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have fallen back to "defaults" in the channelAgentMap
	telegramMsg := bus.InboundMessage{Channel: "telegram", ChatID: "123"}
	agent := mux.resolveAgent(telegramMsg)
	if agent == nil {
		t.Fatal("expected agent")
	}
	if agent.Name() != "defaults" {
		t.Errorf("expected fallback to 'defaults', got %q", agent.Name())
	}
}

func TestAgentMultiplexer_RegisterDelegateTools(t *testing.T) {
	cfg, tmpDir := newTestMultiplexerConfig(t)
	defer os.RemoveAll(tmpDir)

	cfg.Agents.Agents = []config.AgentConfig{
		{Name: "defaults", Model: "glm-4.7"},
		{Name: "researcher", Model: "claude-sonnet", Workspace: tmpDir + "/researcher"},
	}

	msgBus := bus.NewMessageBus()
	mux, err := NewAgentMultiplexer(cfg, msgBus)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mux.RegisterDelegateTools()

	// Both agents should have the delegate tool
	for _, name := range []string{"defaults", "researcher"} {
		agent := mux.GetAgent(name)
		if agent == nil {
			t.Fatalf("agent %q not found", name)
		}
		_, ok := agent.ToolRegistry().Get("delegate")
		if !ok {
			t.Errorf("agent %q should have 'delegate' tool registered", name)
		}
	}
}

func TestAgentMultiplexer_GetStartupInfo(t *testing.T) {
	cfg, tmpDir := newTestMultiplexerConfig(t)
	defer os.RemoveAll(tmpDir)

	cfg.Agents.Agents = []config.AgentConfig{
		{Name: "defaults", Model: "glm-4.7"},
		{Name: "coder", Model: "gpt-4o", Workspace: tmpDir + "/coder"},
	}

	msgBus := bus.NewMessageBus()
	mux, err := NewAgentMultiplexer(cfg, msgBus)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info := mux.GetStartupInfo()

	agentsInfo, ok := info["agents"].(map[string]interface{})
	if !ok {
		t.Fatal("expected 'agents' key in startup info")
	}
	count, ok := agentsInfo["count"].(int)
	if !ok {
		t.Fatal("expected 'count' in agents info")
	}
	if count != 2 {
		t.Errorf("expected 2 agents, got %d", count)
	}
}
