package agent

import (
	"context"
	"fmt"
	"sync"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/constants"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/tools"
)

// AgentMultiplexer manages multiple named AgentLoop instances and routes
// inbound messages to the correct agent based on channel configuration.
type AgentMultiplexer struct {
	agents          map[string]*AgentLoop // name -> loop
	bus             *bus.MessageBus
	channelAgentMap map[string]string // channel name -> agent name
	mu              sync.RWMutex
	running         bool
}

// NewAgentMultiplexer creates a multiplexer with one AgentLoop per configured agent.
func NewAgentMultiplexer(cfg *config.Config, msgBus *bus.MessageBus) (*AgentMultiplexer, error) {
	agentConfigs := cfg.GetAgentConfigs()

	if len(agentConfigs) == 0 {
		return nil, fmt.Errorf("no agent configurations found")
	}

	// Ensure a "defaults" agent exists
	hasDefaults := false
	for _, ac := range agentConfigs {
		if ac.Name == "defaults" {
			hasDefaults = true
			break
		}
	}
	if !hasDefaults {
		return nil, fmt.Errorf("no 'defaults' agent defined; at least one agent must be named 'defaults'")
	}

	mux := &AgentMultiplexer{
		agents:          make(map[string]*AgentLoop),
		bus:             msgBus,
		channelAgentMap: make(map[string]string),
	}

	// Create an AgentLoop per configured agent
	for _, ac := range agentConfigs {
		resolved := cfg.ResolveAgentConfig(ac.Name)

		provider, err := providers.CreateProviderForAgent(cfg, resolved)
		if err != nil {
			return nil, fmt.Errorf("failed to create provider for agent %q: %w", ac.Name, err)
		}

		loop := NewAgentLoopFromAgentConfig(resolved, cfg, msgBus, provider)
		mux.agents[ac.Name] = loop

		logger.InfoCF("multiplexer", fmt.Sprintf("Agent %q created (model: %s, workspace: %s)", ac.Name, resolved.Model, resolved.Workspace), nil)
	}

	// Build channel -> agent mapping from channel configs
	channelNames := []string{
		"telegram", "discord", "slack", "whatsapp", "feishu",
		"dingtalk", "line", "qq", "onebot", "maixcam",
	}
	for _, ch := range channelNames {
		agentName := cfg.GetChannelDefaultAgent(ch)
		if agentName != "" {
			// Validate the agent exists
			if _, ok := mux.agents[agentName]; !ok {
				logger.WarnCF("multiplexer", fmt.Sprintf("Channel %q references unknown agent %q, falling back to 'defaults'", ch, agentName), nil)
				agentName = "defaults"
			}
			mux.channelAgentMap[ch] = agentName
		}
	}

	return mux, nil
}

// Run starts the multiplexer's main message routing loop.
func (m *AgentMultiplexer) Run(ctx context.Context) error {
	m.mu.Lock()
	m.running = true
	m.mu.Unlock()

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			msg, ok := m.bus.ConsumeInbound(ctx)
			if !ok {
				continue
			}

			agentLoop := m.resolveAgent(msg)
			if agentLoop == nil {
				logger.WarnCF("multiplexer", "No agent found for message, dropping", map[string]interface{}{
					"channel": msg.Channel,
					"chat_id": msg.ChatID,
				})
				continue
			}

			response, err := agentLoop.ProcessMessage(ctx, msg)
			if err != nil {
				response = fmt.Sprintf("Error processing message: %v", err)
			}

			if response != "" {
				// Check if the message tool already sent a response
				alreadySent := false
				if tool, ok := agentLoop.ToolRegistry().Get("message"); ok {
					if mt, ok := tool.(*tools.MessageTool); ok {
						alreadySent = mt.HasSentInRound()
					}
				}

				if !alreadySent && !constants.IsInternalChannel(msg.Channel) {
					m.bus.PublishOutbound(bus.OutboundMessage{
						Channel: msg.Channel,
						ChatID:  msg.ChatID,
						Content: response,
					})
				}
			}
		}
	}
}

// resolveAgent determines which AgentLoop should handle a message.
func (m *AgentMultiplexer) resolveAgent(msg bus.InboundMessage) *AgentLoop {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// 1. Check for inter-agent delegation via metadata
	if targetAgent := msg.Metadata["target_agent"]; targetAgent != "" {
		if agent, ok := m.agents[targetAgent]; ok {
			return agent
		}
		logger.WarnCF("multiplexer", fmt.Sprintf("Target agent %q not found in metadata", targetAgent), nil)
	}

	// 2. Route by channel's default_agent
	if agentName, ok := m.channelAgentMap[msg.Channel]; ok {
		if agent, ok := m.agents[agentName]; ok {
			return agent
		}
	}

	// 3. Fallback to "defaults"
	return m.agents["defaults"]
}

// Stop stops all agent loops.
func (m *AgentMultiplexer) Stop() {
	m.mu.Lock()
	m.running = false
	m.mu.Unlock()

	for name, agent := range m.agents {
		agent.Stop()
		logger.InfoCF("multiplexer", fmt.Sprintf("Agent %q stopped", name), nil)
	}
}

// GetAgent returns a named agent loop, or nil if not found.
func (m *AgentMultiplexer) GetAgent(name string) *AgentLoop {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.agents[name]
}

// GetDefaultAgent returns the "defaults" agent loop.
func (m *AgentMultiplexer) GetDefaultAgent() *AgentLoop {
	return m.GetAgent("defaults")
}

// AgentNames returns the names of all configured agents.
func (m *AgentMultiplexer) AgentNames() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0, len(m.agents))
	for name := range m.agents {
		names = append(names, name)
	}
	return names
}

// SetChannelManager sets the channel manager on all agent loops.
func (m *AgentMultiplexer) SetChannelManager(cm *channels.Manager) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, agent := range m.agents {
		agent.SetChannelManager(cm)
	}
}

// RegisterToolOnAll registers a tool on all agent loops.
func (m *AgentMultiplexer) RegisterToolOnAll(tool tools.Tool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, agent := range m.agents {
		agent.RegisterTool(tool)
	}
}

// DelegateTask implements tools.DelegateExecutor.
// It runs a task on the named agent synchronously and returns the result.
func (m *AgentMultiplexer) DelegateTask(ctx context.Context, agentName, task, channel, chatID string) (string, error) {
	agent := m.GetAgent(agentName)
	if agent == nil {
		return "", fmt.Errorf("agent %q not found", agentName)
	}

	sessionKey := fmt.Sprintf("delegate:%s:%s", agentName, chatID)
	return agent.RunAgentLoop(ctx, sessionKey, channel, chatID, task)
}

// AvailableAgents implements tools.DelegateExecutor.
func (m *AgentMultiplexer) AvailableAgents() []string {
	return m.AgentNames()
}

// RegisterDelegateTools registers a delegate tool on each agent so they can
// delegate tasks to other agents via the multiplexer.
func (m *AgentMultiplexer) RegisterDelegateTools() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for name, agent := range m.agents {
		delegateTool := tools.NewDelegateTool(m, name)
		agent.RegisterTool(delegateTool)
	}
}

// GetStartupInfo returns startup info from the default agent.
func (m *AgentMultiplexer) GetStartupInfo() map[string]interface{} {
	defaultAgent := m.GetDefaultAgent()
	if defaultAgent == nil {
		return map[string]interface{}{}
	}

	info := defaultAgent.GetStartupInfo()
	info["agents"] = map[string]interface{}{
		"count": len(m.agents),
		"names": m.AgentNames(),
	}
	return info
}
