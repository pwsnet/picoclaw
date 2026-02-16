package tools

import (
	"context"
	"fmt"
	"testing"
)

// mockDelegateExecutor implements DelegateExecutor for testing
type mockDelegateExecutor struct {
	agents    []string
	lastAgent string
	lastTask  string
	response  string
	err       error
}

func (m *mockDelegateExecutor) DelegateTask(ctx context.Context, agentName, task, channel, chatID string) (string, error) {
	m.lastAgent = agentName
	m.lastTask = task
	if m.err != nil {
		return "", m.err
	}
	return m.response, nil
}

func (m *mockDelegateExecutor) AvailableAgents() []string {
	return m.agents
}

func TestDelegateTool_BasicExecution(t *testing.T) {
	executor := &mockDelegateExecutor{
		agents:   []string{"defaults", "researcher"},
		response: "research result",
	}

	tool := NewDelegateTool(executor, "defaults")

	result := tool.Execute(context.Background(), map[string]interface{}{
		"agent": "researcher",
		"task":  "find papers on AI",
	})

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
	if executor.lastAgent != "researcher" {
		t.Errorf("expected agent 'researcher', got %q", executor.lastAgent)
	}
	if executor.lastTask != "find papers on AI" {
		t.Errorf("expected task 'find papers on AI', got %q", executor.lastTask)
	}
}

func TestDelegateTool_PreventsSelfDelegation(t *testing.T) {
	executor := &mockDelegateExecutor{
		agents:   []string{"defaults"},
		response: "ok",
	}

	tool := NewDelegateTool(executor, "defaults")

	result := tool.Execute(context.Background(), map[string]interface{}{
		"agent": "defaults",
		"task":  "do something",
	})

	if !result.IsError {
		t.Fatal("expected error for self-delegation")
	}
}

func TestDelegateTool_MissingAgent(t *testing.T) {
	executor := &mockDelegateExecutor{}
	tool := NewDelegateTool(executor, "defaults")

	result := tool.Execute(context.Background(), map[string]interface{}{
		"task": "do something",
	})

	if !result.IsError {
		t.Fatal("expected error for missing agent parameter")
	}
}

func TestDelegateTool_MissingTask(t *testing.T) {
	executor := &mockDelegateExecutor{}
	tool := NewDelegateTool(executor, "defaults")

	result := tool.Execute(context.Background(), map[string]interface{}{
		"agent": "researcher",
	})

	if !result.IsError {
		t.Fatal("expected error for missing task parameter")
	}
}

func TestDelegateTool_DelegationDepthLimit(t *testing.T) {
	executor := &mockDelegateExecutor{
		agents:   []string{"defaults", "researcher"},
		response: "ok",
	}

	tool := NewDelegateTool(executor, "defaults")

	// Simulate being at max depth
	ctx := context.WithValue(context.Background(), delegationDepthKey{}, maxDelegationDepth)

	result := tool.Execute(ctx, map[string]interface{}{
		"agent": "researcher",
		"task":  "do something",
	})

	if !result.IsError {
		t.Fatal("expected error when delegation depth is exceeded")
	}
}

func TestDelegateTool_DelegationDepthIncrement(t *testing.T) {
	var capturedCtx context.Context
	executor := &mockDelegateExecutor{
		agents:   []string{"defaults", "researcher"},
		response: "ok",
	}
	// Override to capture context
	originalExecutor := executor
	captureExecutor := &contextCaptureDelegateExecutor{
		inner:       originalExecutor,
		capturedCtx: &capturedCtx,
	}

	tool := NewDelegateTool(captureExecutor, "defaults")

	// Start at depth 1
	ctx := context.WithValue(context.Background(), delegationDepthKey{}, 1)

	result := tool.Execute(ctx, map[string]interface{}{
		"agent": "researcher",
		"task":  "do something",
	})

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}

	// Verify depth was incremented to 2
	depth := DelegationDepth(capturedCtx)
	if depth != 2 {
		t.Errorf("expected depth 2, got %d", depth)
	}
}

func TestDelegateTool_ExecutorError(t *testing.T) {
	executor := &mockDelegateExecutor{
		agents: []string{"defaults", "researcher"},
		err:    fmt.Errorf("agent crashed"),
	}

	tool := NewDelegateTool(executor, "defaults")

	result := tool.Execute(context.Background(), map[string]interface{}{
		"agent": "researcher",
		"task":  "crash me",
	})

	if !result.IsError {
		t.Fatal("expected error when executor fails")
	}
}

func TestDelegateTool_Name(t *testing.T) {
	tool := NewDelegateTool(nil, "test")
	if tool.Name() != "delegate" {
		t.Errorf("expected name 'delegate', got %q", tool.Name())
	}
}

func TestDelegateTool_SetContext(t *testing.T) {
	executor := &mockDelegateExecutor{
		agents:   []string{"defaults", "researcher"},
		response: "ok",
	}
	tool := NewDelegateTool(executor, "defaults")

	tool.SetContext("telegram", "chat123")

	if tool.channel != "telegram" {
		t.Errorf("expected channel 'telegram', got %q", tool.channel)
	}
	if tool.chatID != "chat123" {
		t.Errorf("expected chatID 'chat123', got %q", tool.chatID)
	}
}

func TestDelegationDepth_DefaultZero(t *testing.T) {
	depth := DelegationDepth(context.Background())
	if depth != 0 {
		t.Errorf("expected depth 0, got %d", depth)
	}
}

// contextCaptureDelegateExecutor wraps a DelegateExecutor and captures the context
type contextCaptureDelegateExecutor struct {
	inner       *mockDelegateExecutor
	capturedCtx *context.Context
}

func (c *contextCaptureDelegateExecutor) DelegateTask(ctx context.Context, agentName, task, channel, chatID string) (string, error) {
	*c.capturedCtx = ctx
	return c.inner.DelegateTask(ctx, agentName, task, channel, chatID)
}

func (c *contextCaptureDelegateExecutor) AvailableAgents() []string {
	return c.inner.AvailableAgents()
}
