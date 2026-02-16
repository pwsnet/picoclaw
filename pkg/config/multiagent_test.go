package config

import (
	"encoding/json"
	"testing"
)

func TestResolveAgentConfig_DefaultsOnly(t *testing.T) {
	cfg := DefaultConfig()

	resolved := cfg.ResolveAgentConfig("defaults")
	if resolved.Name != "defaults" {
		t.Errorf("expected name 'defaults', got %q", resolved.Name)
	}
	if resolved.Model != cfg.Agents.Defaults.Model {
		t.Errorf("expected model %q, got %q", cfg.Agents.Defaults.Model, resolved.Model)
	}
	if resolved.Workspace != cfg.Agents.Defaults.Workspace {
		t.Errorf("expected workspace %q, got %q", cfg.Agents.Defaults.Workspace, resolved.Workspace)
	}
}

func TestResolveAgentConfig_EmptyNameFallsToDefaults(t *testing.T) {
	cfg := DefaultConfig()

	resolved := cfg.ResolveAgentConfig("")
	if resolved.Name != "defaults" {
		t.Errorf("expected name 'defaults', got %q", resolved.Name)
	}
}

func TestResolveAgentConfig_NamedAgentInheritsDefaults(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Agents.Agents = []AgentConfig{
		{
			Name:  "researcher",
			Model: "claude-sonnet",
		},
	}

	resolved := cfg.ResolveAgentConfig("researcher")
	if resolved.Name != "researcher" {
		t.Errorf("expected name 'researcher', got %q", resolved.Name)
	}
	if resolved.Model != "claude-sonnet" {
		t.Errorf("expected model 'claude-sonnet', got %q", resolved.Model)
	}
	// Should inherit workspace from defaults
	if resolved.Workspace != cfg.Agents.Defaults.Workspace {
		t.Errorf("expected inherited workspace %q, got %q", cfg.Agents.Defaults.Workspace, resolved.Workspace)
	}
	// Should inherit max_tokens from defaults
	if resolved.MaxTokens != cfg.Agents.Defaults.MaxTokens {
		t.Errorf("expected inherited max_tokens %d, got %d", cfg.Agents.Defaults.MaxTokens, resolved.MaxTokens)
	}
	// Should inherit context_window from defaults
	if resolved.ContextWindow != cfg.Agents.Defaults.ContextWindow {
		t.Errorf("expected inherited context_window %d, got %d", cfg.Agents.Defaults.ContextWindow, resolved.ContextWindow)
	}
}

func TestResolveAgentConfig_NamedAgentOverridesDefaults(t *testing.T) {
	cfg := DefaultConfig()
	restrict := false
	cfg.Agents.Agents = []AgentConfig{
		{
			Name:                "coder",
			Model:               "gpt-4o",
			Workspace:           "/tmp/coder",
			RestrictToWorkspace: &restrict,
			MaxTokens:           16384,
			ContextWindow:       200000,
		},
	}

	resolved := cfg.ResolveAgentConfig("coder")
	if resolved.Model != "gpt-4o" {
		t.Errorf("expected model 'gpt-4o', got %q", resolved.Model)
	}
	if resolved.Workspace != "/tmp/coder" {
		t.Errorf("expected workspace '/tmp/coder', got %q", resolved.Workspace)
	}
	if resolved.GetRestrictToWorkspace() != false {
		t.Error("expected restrict_to_workspace=false")
	}
	if resolved.MaxTokens != 16384 {
		t.Errorf("expected max_tokens 16384, got %d", resolved.MaxTokens)
	}
	if resolved.ContextWindow != 200000 {
		t.Errorf("expected context_window 200000, got %d", resolved.ContextWindow)
	}
}

func TestResolveAgentConfig_UnknownNameFallsToDefaults(t *testing.T) {
	cfg := DefaultConfig()

	resolved := cfg.ResolveAgentConfig("nonexistent")
	if resolved.Name != "defaults" {
		t.Errorf("expected fallback to 'defaults', got %q", resolved.Name)
	}
}

func TestGetAgentConfigs_NoAgentsDefined(t *testing.T) {
	cfg := DefaultConfig()

	configs := cfg.GetAgentConfigs()
	if len(configs) != 1 {
		t.Fatalf("expected 1 config, got %d", len(configs))
	}
	if configs[0].Name != "defaults" {
		t.Errorf("expected name 'defaults', got %q", configs[0].Name)
	}
}

func TestGetAgentConfigs_MultipleAgents(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Agents.Agents = []AgentConfig{
		{Name: "defaults", Model: "glm-4.7"},
		{Name: "researcher", Model: "claude-sonnet"},
	}

	configs := cfg.GetAgentConfigs()
	if len(configs) != 2 {
		t.Fatalf("expected 2 configs, got %d", len(configs))
	}
}

func TestGetChannelDefaultAgent(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Channels.Telegram.DefaultAgent = "researcher"
	cfg.Channels.Slack.DefaultAgent = "coder"

	if got := cfg.GetChannelDefaultAgent("telegram"); got != "researcher" {
		t.Errorf("expected 'researcher', got %q", got)
	}
	if got := cfg.GetChannelDefaultAgent("slack"); got != "coder" {
		t.Errorf("expected 'coder', got %q", got)
	}
	if got := cfg.GetChannelDefaultAgent("discord"); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
	if got := cfg.GetChannelDefaultAgent("unknown"); got != "" {
		t.Errorf("expected empty for unknown channel, got %q", got)
	}
}

func TestAgentConfig_GetRestrictToWorkspace_Defaults(t *testing.T) {
	ac := AgentConfig{Name: "test"}
	if !ac.GetRestrictToWorkspace() {
		t.Error("expected default restrict_to_workspace=true")
	}
}

func TestAgentConfig_GetRestrictToWorkspace_ExplicitFalse(t *testing.T) {
	restrict := false
	ac := AgentConfig{Name: "test", RestrictToWorkspace: &restrict}
	if ac.GetRestrictToWorkspace() {
		t.Error("expected restrict_to_workspace=false")
	}
}

func TestConfigJSON_MultiAgentParsing(t *testing.T) {
	jsonData := `{
		"agents": {
			"defaults": {
				"workspace": "~/.picoclaw/workspace",
				"model": "glm-4.7",
				"max_tokens": 8192,
				"temperature": 0.7,
				"max_tool_iterations": 20
			},
			"agents": [
				{
					"name": "defaults",
					"model": "glm-4.7"
				},
				{
					"name": "researcher",
					"model": "claude-sonnet",
					"workspace": "~/.picoclaw/agents/researcher"
				}
			]
		},
		"channels": {
			"telegram": {
				"enabled": true,
				"token": "test-token",
				"default_agent": "researcher"
			}
		}
	}`

	cfg := DefaultConfig()
	if err := json.Unmarshal([]byte(jsonData), cfg); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	// Verify agents list was parsed
	if len(cfg.Agents.Agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(cfg.Agents.Agents))
	}
	if cfg.Agents.Agents[1].Name != "researcher" {
		t.Errorf("expected second agent named 'researcher', got %q", cfg.Agents.Agents[1].Name)
	}
	if cfg.Agents.Agents[1].Workspace != "~/.picoclaw/agents/researcher" {
		t.Errorf("unexpected workspace: %q", cfg.Agents.Agents[1].Workspace)
	}

	// Verify channel default_agent was parsed
	if cfg.Channels.Telegram.DefaultAgent != "researcher" {
		t.Errorf("expected telegram default_agent 'researcher', got %q", cfg.Channels.Telegram.DefaultAgent)
	}
}

func TestDefaultConfig_ContextWindow(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Agents.Defaults.ContextWindow != 128000 {
		t.Errorf("expected default ContextWindow 128000, got %d", cfg.Agents.Defaults.ContextWindow)
	}
	// ContextWindow and MaxTokens should be separate values
	if cfg.Agents.Defaults.ContextWindow == cfg.Agents.Defaults.MaxTokens {
		t.Error("ContextWindow and MaxTokens should not be the same value")
	}
}

func TestResolveAgentConfig_ContextWindowBackwardCompat(t *testing.T) {
	// Agent without context_window should inherit default 128000
	cfg := DefaultConfig()
	cfg.Agents.Agents = []AgentConfig{
		{Name: "old-style", Model: "gpt-4o"},
	}

	resolved := cfg.ResolveAgentConfig("old-style")
	if resolved.ContextWindow != 128000 {
		t.Errorf("expected inherited context_window 128000, got %d", resolved.ContextWindow)
	}
}

func TestConfigJSON_BackwardCompatibility(t *testing.T) {
	// Old format: just defaults, no agents list
	jsonData := `{
		"agents": {
			"defaults": {
				"workspace": "~/.picoclaw/workspace",
				"model": "glm-4.7",
				"max_tokens": 8192,
				"temperature": 0.7,
				"max_tool_iterations": 20
			}
		}
	}`

	cfg := DefaultConfig()
	if err := json.Unmarshal([]byte(jsonData), cfg); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	// No agents list defined
	if len(cfg.Agents.Agents) != 0 {
		t.Fatalf("expected 0 agents in list, got %d", len(cfg.Agents.Agents))
	}

	// GetAgentConfigs should still return one "defaults" agent
	configs := cfg.GetAgentConfigs()
	if len(configs) != 1 {
		t.Fatalf("expected 1 config from GetAgentConfigs, got %d", len(configs))
	}
	if configs[0].Name != "defaults" {
		t.Errorf("expected 'defaults', got %q", configs[0].Name)
	}
	if configs[0].Model != "glm-4.7" {
		t.Errorf("expected model 'glm-4.7', got %q", configs[0].Model)
	}
}
