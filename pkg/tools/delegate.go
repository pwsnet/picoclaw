package tools

import (
	"context"
	"fmt"
	"strconv"
)

// DelegateExecutor is the interface used by DelegateTool to run a task on a
// named agent. The agent package implements this on AgentMultiplexer to avoid
// circular imports.
type DelegateExecutor interface {
	// DelegateTask runs a task on the named agent synchronously and returns the result.
	DelegateTask(ctx context.Context, agentName, task, channel, chatID string) (string, error)
	// AvailableAgents returns the names of all agents that can be delegated to.
	AvailableAgents() []string
}

// DelegateTool allows an agent to delegate a task to another named agent.
// The target agent runs its full loop synchronously and returns the result.
type DelegateTool struct {
	executor    DelegateExecutor
	originAgent string // name of the agent that owns this tool
	channel     string
	chatID      string
}

// NewDelegateTool creates a delegate tool for a specific agent.
func NewDelegateTool(executor DelegateExecutor, originAgent string) *DelegateTool {
	return &DelegateTool{
		executor:    executor,
		originAgent: originAgent,
		channel:     "cli",
		chatID:      "direct",
	}
}

func (t *DelegateTool) Name() string {
	return "delegate"
}

func (t *DelegateTool) Description() string {
	return "Delegate a task to another named agent. The target agent processes the task with its own model, tools, and context, then returns the result. Use this for collaboration between specialized agents."
}

func (t *DelegateTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"agent": map[string]interface{}{
				"type":        "string",
				"description": "Name of the target agent to delegate to",
			},
			"task": map[string]interface{}{
				"type":        "string",
				"description": "The task or message to send to the target agent",
			},
		},
		"required": []string{"agent", "task"},
	}
}

func (t *DelegateTool) SetContext(channel, chatID string) {
	t.channel = channel
	t.chatID = chatID
}

const maxDelegationDepth = 3

func (t *DelegateTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	agentName, ok := args["agent"].(string)
	if !ok || agentName == "" {
		return ErrorResult("'agent' parameter is required")
	}

	task, ok := args["task"].(string)
	if !ok || task == "" {
		return ErrorResult("'task' parameter is required")
	}

	// Prevent self-delegation
	if agentName == t.originAgent {
		return ErrorResult(fmt.Sprintf("Cannot delegate to self (%s)", t.originAgent))
	}

	// Check delegation depth from context to prevent circular delegation
	depth := 0
	if depthVal := ctx.Value(delegationDepthKey{}); depthVal != nil {
		depth = depthVal.(int)
	}
	if depth >= maxDelegationDepth {
		return ErrorResult(fmt.Sprintf("Maximum delegation depth (%d) exceeded", maxDelegationDepth))
	}

	// Run with incremented depth
	delegateCtx := context.WithValue(ctx, delegationDepthKey{}, depth+1)

	result, err := t.executor.DelegateTask(delegateCtx, agentName, task, t.channel, t.chatID)
	if err != nil {
		return ErrorResult(fmt.Sprintf("Delegation to agent %q failed: %v", agentName, err))
	}

	return &ToolResult{
		ForLLM:  fmt.Sprintf("Agent %q responded:\n%s", agentName, result),
		ForUser: "",
		Silent:  true,
		IsError: false,
	}
}

// DelegationDepth returns the current delegation depth from context.
func DelegationDepth(ctx context.Context) int {
	if v := ctx.Value(delegationDepthKey{}); v != nil {
		return v.(int)
	}
	return 0
}

// DelegationDepthStr returns the current delegation depth as a string for metadata.
func DelegationDepthStr(ctx context.Context) string {
	return strconv.Itoa(DelegationDepth(ctx))
}

type delegationDepthKey struct{}
