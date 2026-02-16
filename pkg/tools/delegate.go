package tools

import (
	"context"
	"fmt"
	"strconv"

	"github.com/sipeed/picoclaw/pkg/bus"
)

// DelegateExecutor is the interface used by DelegateTool to run a task on a
// named agent. The agent package implements this on AgentMultiplexer to avoid
// circular imports.
type DelegateExecutor interface {
	// DelegateTask runs a task on the named agent synchronously and returns the result.
	DelegateTask(ctx context.Context, agentName, task, channel, chatID string) (string, error)
	// AvailableAgents returns the names of all agents that can be delegated to.
	AvailableAgents() []string
	// DelegateBus returns the message bus for publishing async completion messages.
	DelegateBus() *bus.MessageBus
}

// DelegateTool allows an agent to delegate a task to another named agent.
// The target agent processes the task asynchronously in the background and
// delivers results via the message bus.
type DelegateTool struct {
	executor    DelegateExecutor
	originAgent string // name of the agent that owns this tool
	channel     string
	chatID      string
	callback    AsyncCallback
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
	return "Delegate a task to another named agent. The target agent processes the task asynchronously in the background with its own model, tools, and context. Results are delivered to the user when ready. Use this for collaboration between specialized agents."
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

// SetCallback implements AsyncTool interface for async completion notification.
func (t *DelegateTool) SetCallback(cb AsyncCallback) {
	t.callback = cb
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

	// Capture channel/chatID for the goroutine
	channel := t.channel
	chatID := t.chatID
	callback := t.callback
	msgBus := t.executor.DelegateBus()

	// Launch async delegation in background
	go func() {
		result, err := t.executor.DelegateTask(delegateCtx, agentName, task, channel, chatID)

		var toolResult *ToolResult
		if err != nil {
			toolResult = ErrorResult(fmt.Sprintf("Delegation to agent %q failed: %v", agentName, err))
		} else {
			toolResult = &ToolResult{
				ForLLM:  fmt.Sprintf("Agent %q responded:\n%s", agentName, result),
				ForUser: result,
			}
		}

		// Invoke callback if set
		if callback != nil {
			callback(delegateCtx, toolResult)
		}

		// Publish completion to system channel for the agent loop to handle
		if msgBus != nil {
			content := fmt.Sprintf("Delegation to %q completed.\n\nResult:\n%s", agentName, result)
			if err != nil {
				content = fmt.Sprintf("Delegation to %q failed: %v", agentName, err)
			}
			msgBus.PublishInbound(bus.InboundMessage{
				Channel:  "system",
				SenderID: fmt.Sprintf("delegate:%s", agentName),
				ChatID:   fmt.Sprintf("%s:%s", channel, chatID),
				Content:  content,
			})
		}
	}()

	return AsyncResult(fmt.Sprintf("Task delegated to agent %q. It will process in the background.", agentName))
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
