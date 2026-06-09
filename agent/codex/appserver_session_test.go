package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

func TestAppServerSession_UsesConfiguredCLIPath(t *testing.T) {
	s := &appServerSession{
		cliBin:       "omx",
		cliExtraArgs: []string{"--profile", "test"},
		url:          "stdio://",
		model:        "gpt-5.4",
		effort:       "high",
	}

	got := s.appServerCommandArgs()
	wantPrefix := []string{"--profile", "test", "app-server", "--listen", "stdio://"}
	if len(got) < len(wantPrefix) {
		t.Fatalf("command args = %v, want prefix %v", got, wantPrefix)
	}
	for i, want := range wantPrefix {
		if got[i] != want {
			t.Fatalf("command args = %v, want prefix %v", got, wantPrefix)
		}
	}
}

func TestAppServerSession_AppServerCommandDefaultsToCodex(t *testing.T) {
	s := &appServerSession{
		url:    "stdio://",
		model:  "gpt-5.4",
		effort: "high",
	}

	gotBin, gotArgs := s.appServerCommand()
	if gotBin != "codex" {
		t.Fatalf("command binary = %q, want codex", gotBin)
	}
	wantPrefix := []string{"app-server", "--listen", "stdio://"}
	if len(gotArgs) < len(wantPrefix) {
		t.Fatalf("command args = %v, want prefix %v", gotArgs, wantPrefix)
	}
	for i, want := range wantPrefix {
		if gotArgs[i] != want {
			t.Fatalf("command args = %v, want prefix %v", gotArgs, wantPrefix)
		}
	}
}

func TestAppServerSession_AppServerCommandUsesConfiguredBinary(t *testing.T) {
	s := &appServerSession{
		cliBin:       "omx",
		cliExtraArgs: []string{"--profile", "test"},
		url:          "stdio://",
	}

	gotBin, gotArgs := s.appServerCommand()
	if gotBin != "omx" {
		t.Fatalf("command binary = %q, want omx", gotBin)
	}
	wantPrefix := []string{"--profile", "test", "app-server", "--listen", "stdio://"}
	if len(gotArgs) < len(wantPrefix) {
		t.Fatalf("command args = %v, want prefix %v", gotArgs, wantPrefix)
	}
	for i, want := range wantPrefix {
		if gotArgs[i] != want {
			t.Fatalf("command args = %v, want prefix %v", gotArgs, wantPrefix)
		}
	}
}

func TestAppServerSession_ApplyThreadRuntimeState(t *testing.T) {
	s := &appServerSession{}
	effort := "xhigh"

	s.applyThreadRuntimeState("/tmp/project", "gpt-5.4", &effort)

	if got := s.GetWorkDir(); got != "/tmp/project" {
		t.Fatalf("GetWorkDir() = %q, want /tmp/project", got)
	}
	if got := s.GetModel(); got != "gpt-5.4" {
		t.Fatalf("GetModel() = %q, want gpt-5.4", got)
	}
	if got := s.GetReasoningEffort(); got != "xhigh" {
		t.Fatalf("GetReasoningEffort() = %q, want xhigh", got)
	}
}

func TestAppServerSession_HandleRateLimitsUpdatedCachesUsage(t *testing.T) {
	s := &appServerSession{}
	raw, err := json.Marshal(appServerRateLimitsResponse{
		RateLimits: appServerRateLimitSnapshot{
			LimitID:   "codex",
			PlanType:  "pro",
			Primary:   &appServerRateLimitWindow{UsedPercent: 25, WindowDurationMins: 15, ResetsAt: 1730947200},
			Secondary: &appServerRateLimitWindow{UsedPercent: 42, WindowDurationMins: 60, ResetsAt: 1730950800},
			Credits:   &appServerCreditsSnapshot{HasCredits: true, Unlimited: false},
		},
	})
	if err != nil {
		t.Fatalf("marshal notification: %v", err)
	}

	s.handleNotification("account/rateLimits/updated", raw)

	report, err := s.GetUsage(context.Background())
	if err != nil {
		t.Fatalf("GetUsage() returned error: %v", err)
	}
	if report.Provider != "codex" {
		t.Fatalf("provider = %q, want codex", report.Provider)
	}
	if report.Plan != "pro" {
		t.Fatalf("plan = %q, want pro", report.Plan)
	}
	if len(report.Buckets) != 1 {
		t.Fatalf("buckets = %d, want 1", len(report.Buckets))
	}
	if got := report.Buckets[0].Name; got != "codex" {
		t.Fatalf("bucket name = %q, want codex", got)
	}
	if got := report.Buckets[0].Windows[0].WindowSeconds; got != 15*60 {
		t.Fatalf("primary window seconds = %d, want %d", got, 15*60)
	}
	if got := report.Buckets[0].Windows[1].UsedPercent; got != 42 {
		t.Fatalf("secondary used percent = %d, want 42", got)
	}
	if report.Credits == nil || !report.Credits.HasCredits {
		t.Fatalf("credits = %#v, want has credits", report.Credits)
	}
}

func TestAppServerSession_HandleThreadTokenUsageUpdatedCachesContextUsage(t *testing.T) {
	s := &appServerSession{}
	raw, err := json.Marshal(appServerThreadTokenUsageNotification{
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		TokenUsage: struct {
			Total              codexTokenUsage `json:"total"`
			Last               codexTokenUsage `json:"last"`
			ModelContextWindow int             `json:"modelContextWindow"`
		}{
			Total: codexTokenUsage{
				TotalTokens:           52011395,
				InputTokens:           51847383,
				CachedInputTokens:     48187904,
				OutputTokens:          164012,
				ReasoningOutputTokens: 78910,
			},
			Last: codexTokenUsage{
				TotalTokens:           41061,
				InputTokens:           40849,
				CachedInputTokens:     36864,
				OutputTokens:          212,
				ReasoningOutputTokens: 32,
			},
			ModelContextWindow: 258400,
		},
	})
	if err != nil {
		t.Fatalf("marshal notification: %v", err)
	}

	s.handleNotification("thread/tokenUsage/updated", raw)

	usage := s.GetContextUsage()
	if usage == nil {
		t.Fatal("GetContextUsage() = nil, want cached context usage")
	}
	if usage.UsedTokens != 41061 {
		t.Fatalf("used tokens = %d, want 41061", usage.UsedTokens)
	}
	if usage.BaselineTokens != codexContextBaselineTokens {
		t.Fatalf("baseline tokens = %d, want %d", usage.BaselineTokens, codexContextBaselineTokens)
	}
	if usage.TotalTokens != 41061 {
		t.Fatalf("total tokens = %d, want 41061", usage.TotalTokens)
	}
	if usage.ContextWindow != 258400 {
		t.Fatalf("context window = %d, want 258400", usage.ContextWindow)
	}
	if usage.CachedInputTokens != 36864 {
		t.Fatalf("cached input tokens = %d, want 36864", usage.CachedInputTokens)
	}
	if usage.InputTokens != 40849 {
		t.Fatalf("input tokens = %d, want 40849", usage.InputTokens)
	}
}

func TestMapAppServerRateLimits_PrefersMultiBucketView(t *testing.T) {
	report := mapAppServerRateLimits(appServerRateLimitsResponse{
		RateLimits: appServerRateLimitSnapshot{
			LimitID:  "legacy",
			PlanType: "team",
			Primary:  &appServerRateLimitWindow{UsedPercent: 99, WindowDurationMins: 15},
		},
		RateLimitsByLimitID: map[string]appServerRateLimitSnapshot{
			"codex": {
				LimitID:   "codex",
				LimitName: "Codex",
				PlanType:  "team",
				Primary:   &appServerRateLimitWindow{UsedPercent: 10, WindowDurationMins: 15},
			},
			"codex_other": {
				LimitID:  "codex_other",
				PlanType: "team",
				Primary:  &appServerRateLimitWindow{UsedPercent: 20, WindowDurationMins: 60},
			},
		},
	})

	if report.Plan != "team" {
		t.Fatalf("plan = %q, want team", report.Plan)
	}
	if len(report.Buckets) != 2 {
		t.Fatalf("buckets = %d, want 2", len(report.Buckets))
	}
	if report.Buckets[0].Name != "Codex" {
		t.Fatalf("first bucket = %q, want Codex", report.Buckets[0].Name)
	}
	if report.Buckets[1].Name != "codex_other" {
		t.Fatalf("second bucket = %q, want codex_other", report.Buckets[1].Name)
	}
}

func TestAppServerSession_AgentMessageDeltaEmitsTextImmediately(t *testing.T) {
	s := &appServerSession{events: make(chan core.Event, 4)}

	s.handleNotification("item/agentMessage/delta", mustMarshalRaw(t, map[string]any{
		"threadId": "thread-1",
		"turnId":   "turn-1",
		"itemId":   "item-1",
		"delta":    "hello",
	}))

	event := recvCoreEvent(t, s.events)
	if event.Type != core.EventText {
		t.Fatalf("event type = %s, want %s", event.Type, core.EventText)
	}
	if event.Content != "hello" {
		t.Fatalf("content = %q, want hello", event.Content)
	}
	if event.SessionID != "thread-1" {
		t.Fatalf("session id = %q, want thread-1", event.SessionID)
	}
	if event.Metadata["turn_id"] != "turn-1" || event.Metadata["item_id"] != "item-1" || event.Metadata["is_delta"] != true {
		t.Fatalf("metadata = %#v", event.Metadata)
	}
}

func TestAppServerSession_AgentMessageCompletedDoesNotDuplicateStreamedDelta(t *testing.T) {
	s := &appServerSession{events: make(chan core.Event, 8)}

	s.handleNotification("turn/started", mustMarshalRaw(t, map[string]any{
		"turn": map[string]any{"id": "turn-1"},
	}))
	s.handleNotification("item/agentMessage/delta", mustMarshalRaw(t, map[string]any{
		"threadId": "thread-1",
		"turnId":   "turn-1",
		"itemId":   "item-1",
		"delta":    "hello",
	}))
	s.handleNotification("item/completed", mustMarshalRaw(t, map[string]any{
		"threadId": "thread-1",
		"turnId":   "turn-1",
		"item": map[string]any{
			"id":   "item-1",
			"type": "agentMessage",
			"text": "hello",
		},
	}))
	s.handleNotification("turn/completed", mustMarshalRaw(t, map[string]any{
		"turn": map[string]any{"id": "turn-1"},
	}))

	first := recvCoreEvent(t, s.events)
	if first.Type != core.EventText || first.Content != "hello" {
		t.Fatalf("first event = %#v, want streamed hello text", first)
	}
	second := recvCoreEvent(t, s.events)
	if second.Type != core.EventResult {
		t.Fatalf("second event = %#v, want EventResult without duplicate text", second)
	}
	assertNoCoreEvent(t, s.events)
}

func TestAppServerSession_AgentMessageCompletedFallsBackWhenNoDelta(t *testing.T) {
	s := &appServerSession{events: make(chan core.Event, 8)}

	s.handleNotification("turn/started", mustMarshalRaw(t, map[string]any{
		"turn": map[string]any{"id": "turn-1"},
	}))
	s.handleNotification("item/completed", mustMarshalRaw(t, map[string]any{
		"threadId": "thread-1",
		"turnId":   "turn-1",
		"item": map[string]any{
			"id":   "item-1",
			"type": "agentMessage",
			"text": "final answer",
		},
	}))
	s.handleNotification("turn/completed", mustMarshalRaw(t, map[string]any{
		"turn": map[string]any{"id": "turn-1"},
	}))

	first := recvCoreEvent(t, s.events)
	if first.Type != core.EventText || first.Content != "final answer" {
		t.Fatalf("first event = %#v, want fallback final text", first)
	}
	second := recvCoreEvent(t, s.events)
	if second.Type != core.EventResult {
		t.Fatalf("second event = %#v, want EventResult", second)
	}
}

func TestAppServerSession_ToolEventsIncludeStableItemMetadata(t *testing.T) {
	s := &appServerSession{events: make(chan core.Event, 8)}

	s.handleNotification("item/started", mustMarshalRaw(t, map[string]any{
		"threadId": "thread-1",
		"turnId":   "turn-1",
		"item": map[string]any{
			"id":      "tool-item-1",
			"type":    "commandExecution",
			"command": "pwd",
		},
	}))
	s.handleNotification("item/completed", mustMarshalRaw(t, map[string]any{
		"threadId": "thread-1",
		"turnId":   "turn-1",
		"item": map[string]any{
			"id":               "tool-item-1",
			"type":             "commandExecution",
			"command":          "pwd",
			"status":           "completed",
			"aggregatedOutput": "/tmp/project",
		},
	}))

	started := recvCoreEvent(t, s.events)
	if started.Type != core.EventToolUse || started.ToolName != "Bash" {
		t.Fatalf("started event = %#v, want Bash tool use", started)
	}
	if started.SessionID != "thread-1" ||
		started.Metadata["thread_id"] != "thread-1" ||
		started.Metadata["turn_id"] != "turn-1" ||
		started.Metadata["item_id"] != "tool-item-1" {
		t.Fatalf("started metadata = session %q metadata %#v", started.SessionID, started.Metadata)
	}

	completed := recvCoreEvent(t, s.events)
	if completed.Type != core.EventToolResult || completed.ToolName != "Bash" {
		t.Fatalf("completed event = %#v, want Bash tool result", completed)
	}
	if completed.SessionID != "thread-1" ||
		completed.Metadata["thread_id"] != "thread-1" ||
		completed.Metadata["turn_id"] != "turn-1" ||
		completed.Metadata["item_id"] != "tool-item-1" {
		t.Fatalf("completed metadata = session %q metadata %#v", completed.SessionID, completed.Metadata)
	}
}

func TestAppServerSession_HandleRequestUserInputEmitsAskQuestion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stdin := &lockedWriteCloser{}
	s := &appServerSession{
		events:           make(chan core.Event, 4),
		ctx:              ctx,
		pendingApprovals: make(map[string]chan core.PermissionResult),
		stdin:            stdin,
	}

	s.handleServerRequest(serverRequestProbe(t, `"rui-1"`, "item/tool/requestUserInput", map[string]any{
		"threadId": "thread-1",
		"turnId":   "turn-1",
		"itemId":   "call-1",
		"questions": []any{
			map[string]any{
				"id":       "database",
				"header":   "Database",
				"question": "Which database should we use?",
				"isOther":  true,
				"isSecret": false,
				"options": []any{
					map[string]any{"label": "Postgres", "description": "Use the existing relational database"},
					map[string]any{"label": "SQLite", "description": "Keep it embedded"},
				},
			},
		},
	}))

	var event core.Event
	select {
	case event = <-s.events:
	case <-time.After(time.Second):
		t.Fatal("expected AskUserQuestion event")
	}
	if event.Type != core.EventPermissionRequest {
		t.Fatalf("event type = %s, want %s", event.Type, core.EventPermissionRequest)
	}
	if event.ToolName != "AskUserQuestion" {
		t.Fatalf("tool name = %q, want AskUserQuestion", event.ToolName)
	}
	if event.RequestID != `"rui-1"` {
		t.Fatalf("request id = %q, want raw JSON id", event.RequestID)
	}
	if len(event.Questions) != 1 {
		t.Fatalf("questions = %d, want 1", len(event.Questions))
	}
	q := event.Questions[0]
	if q.Question != "Which database should we use?" || q.Header != "Database" {
		t.Fatalf("question = %#v", q)
	}
	if len(q.Options) != 2 || q.Options[0].Label != "Postgres" || q.Options[1].Description != "Keep it embedded" {
		t.Fatalf("options = %#v", q.Options)
	}
	if stdin.String() != "" {
		t.Fatalf("request_user_input should not write before the answer, got %q", stdin.String())
	}
}

func TestAppServerSession_HandleRequestUserInputWritesCodexResponse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stdin := &lockedWriteCloser{}
	s := &appServerSession{
		events:           make(chan core.Event, 4),
		ctx:              ctx,
		pendingApprovals: make(map[string]chan core.PermissionResult),
		stdin:            stdin,
	}

	s.handleServerRequest(serverRequestProbe(t, `"rui-2"`, "item/tool/requestUserInput", map[string]any{
		"threadId": "thread-1",
		"turnId":   "turn-1",
		"itemId":   "call-2",
		"questions": []any{
			map[string]any{
				"id":       "database",
				"header":   "Database",
				"question": "Which database should we use?",
				"options": []any{
					map[string]any{"label": "Postgres", "description": "Use the existing relational database"},
					map[string]any{"label": "SQLite", "description": "Keep it embedded"},
				},
			},
		},
	}))

	var event core.Event
	select {
	case event = <-s.events:
	case <-time.After(time.Second):
		t.Fatal("expected AskUserQuestion event")
	}
	if err := s.RespondPermission(event.RequestID, core.PermissionResult{
		Behavior: "allow",
		UpdatedInput: map[string]any{
			"answers": map[string]any{
				"Which database should we use?": "Postgres",
			},
		},
	}); err != nil {
		t.Fatalf("RespondPermission() error = %v", err)
	}

	line := waitForWrittenJSONLine(t, stdin)
	var envelope struct {
		JSONRPC string `json:"jsonrpc"`
		ID      string `json:"id"`
		Result  struct {
			Answers map[string]struct {
				Answers []string `json:"answers"`
			} `json:"answers"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(line), &envelope); err != nil {
		t.Fatalf("decode response %q: %v", line, err)
	}
	if envelope.JSONRPC != "2.0" || envelope.ID != "rui-2" {
		t.Fatalf("envelope = %#v", envelope)
	}
	got := envelope.Result.Answers["database"].Answers
	if len(got) != 1 || got[0] != "Postgres" {
		t.Fatalf("answers[database] = %#v, want [Postgres]", got)
	}
}

var _ interface {
	GetUsage(context.Context) (*core.UsageReport, error)
} = (*appServerSession)(nil)

var _ interface {
	GetContextUsage() *core.ContextUsage
} = (*appServerSession)(nil)

type lockedWriteCloser struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *lockedWriteCloser) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *lockedWriteCloser) Close() error { return nil }

func (w *lockedWriteCloser) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

var _ io.WriteCloser = (*lockedWriteCloser)(nil)

func serverRequestProbe(t *testing.T, idJSON, method string, params any) map[string]json.RawMessage {
	t.Helper()
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	methodJSON, err := json.Marshal(method)
	if err != nil {
		t.Fatalf("marshal method: %v", err)
	}
	return map[string]json.RawMessage{
		"id":     json.RawMessage(idJSON),
		"method": methodJSON,
		"params": paramsJSON,
	}
}

func mustMarshalRaw(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal raw: %v", err)
	}
	return data
}

func recvCoreEvent(t *testing.T, events <-chan core.Event) core.Event {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for core event")
	}
	return core.Event{}
}

func assertNoCoreEvent(t *testing.T, events <-chan core.Event) {
	t.Helper()
	select {
	case event := <-events:
		t.Fatalf("unexpected core event: %#v", event)
	case <-time.After(50 * time.Millisecond):
	}
}

func waitForWrittenJSONLine(t *testing.T, w *lockedWriteCloser) string {
	t.Helper()
	deadline := time.After(time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for JSON response, buffer=%q", w.String())
		case <-ticker.C:
			for _, line := range strings.Split(w.String(), "\n") {
				line = strings.TrimSpace(line)
				if line != "" {
					return line
				}
			}
		}
	}
}
