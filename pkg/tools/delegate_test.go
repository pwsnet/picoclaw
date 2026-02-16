package tools

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
)

// mockDelegateExecutor implements DelegateExecutor for testing
type mockDelegateExecutor struct {
	agents    []string
	lastAgent string
	lastTask  string
	response  string
	err       error
	bus       *bus.MessageBus
	delay     time.Duration // optional delay to simulate async work
}

func (m *mockDelegateExecutor) DelegateTask(ctx context.Context, agentName, task, channel, chatID string) (string, error) {
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
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

func (m *mockDelegateExecutor) DelegateBus() *bus.MessageBus {
	return m.bus
}

func TestDelegateTool_ReturnsAsync(t *testing.T) {
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	executor := &mockDelegateExecutor{
		agents:   []string{"defaults", "researcher"},
		response: "research result",
		bus:      msgBus,
		delay:    50 * time.Millisecond,
	}

	tool := NewDelegateTool(executor, "defaults")

	start := time.Now()
	result := tool.Execute(context.Background(), map[string]interface{}{
		"agent": "researcher",
		"task":  "find papers on AI",
	})
	elapsed := time.Since(start)

	// Should return immediately (before the 50ms delay completes)
	if elapsed > 30*time.Millisecond {
		t.Errorf("Execute took %v, expected near-instant return for async", elapsed)
	}

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}

	if !result.Async {
		t.Fatal("expected Async flag to be true")
	}

	if result.ForLLM == "" {
		t.Fatal("expected non-empty ForLLM message")
	}
}

func TestDelegateTool_CallbackInvoked(t *testing.T) {
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	executor := &mockDelegateExecutor{
		agents:   []string{"defaults", "researcher"},
		response: "callback result",
		bus:      msgBus,
	}

	tool := NewDelegateTool(executor, "defaults")

	callbackCh := make(chan *ToolResult, 1)
	tool.SetCallback(func(ctx context.Context, result *ToolResult) {
		callbackCh <- result
	})

	result := tool.Execute(context.Background(), map[string]interface{}{
		"agent": "researcher",
		"task":  "test callback",
	})

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}

	// Wait for callback
	select {
	case cbResult := <-callbackCh:
		if cbResult.IsError {
			t.Fatalf("callback received error result: %s", cbResult.ForLLM)
		}
		if cbResult.ForUser != "callback result" {
			t.Errorf("expected ForUser 'callback result', got %q", cbResult.ForUser)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("callback was not invoked within timeout")
	}
}

func TestDelegateTool_BusPublish(t *testing.T) {
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	executor := &mockDelegateExecutor{
		agents:   []string{"defaults", "researcher"},
		response: "bus result",
		bus:      msgBus,
	}

	tool := NewDelegateTool(executor, "defaults")
	tool.SetContext("telegram", "chat123")

	result := tool.Execute(context.Background(), map[string]interface{}{
		"agent": "researcher",
		"task":  "test bus publish",
	})

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}

	// Read the system message from the bus
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	msg, ok := msgBus.ConsumeInbound(ctx)
	if !ok {
		t.Fatal("no inbound message published to bus")
	}

	if msg.Channel != "system" {
		t.Errorf("expected channel 'system', got %q", msg.Channel)
	}
	if msg.SenderID != "delegate:researcher" {
		t.Errorf("expected senderID 'delegate:researcher', got %q", msg.SenderID)
	}
	if msg.ChatID != "telegram:chat123" {
		t.Errorf("expected chatID 'telegram:chat123', got %q", msg.ChatID)
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
	captureExecutor := &contextCaptureDelegateExecutor{
		inner:       executor,
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

	// Wait for goroutine to execute and capture context
	time.Sleep(100 * time.Millisecond)

	// Verify depth was incremented to 2
	if capturedCtx == nil {
		t.Fatal("context was not captured (goroutine may not have run)")
	}
	depth := DelegationDepth(capturedCtx)
	if depth != 2 {
		t.Errorf("expected depth 2, got %d", depth)
	}
}

func TestDelegateTool_ExecutorError(t *testing.T) {
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	executor := &mockDelegateExecutor{
		agents: []string{"defaults", "researcher"},
		err:    fmt.Errorf("agent crashed"),
		bus:    msgBus,
	}

	tool := NewDelegateTool(executor, "defaults")

	callbackCh := make(chan *ToolResult, 1)
	tool.SetCallback(func(ctx context.Context, result *ToolResult) {
		callbackCh <- result
	})

	result := tool.Execute(context.Background(), map[string]interface{}{
		"agent": "researcher",
		"task":  "crash me",
	})

	// Execute itself returns async (not error)
	if result.IsError {
		t.Fatal("expected async result, not synchronous error")
	}

	// But the callback should receive the error
	select {
	case cbResult := <-callbackCh:
		if !cbResult.IsError {
			t.Fatal("expected error in callback result")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("callback was not invoked within timeout")
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

func (c *contextCaptureDelegateExecutor) DelegateBus() *bus.MessageBus {
	return c.inner.DelegateBus()
}
