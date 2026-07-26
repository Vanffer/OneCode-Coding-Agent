package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"onecode/internal/llm"
)

func TestWorkerFilterAndDuplicate(t *testing.T) {
	store := testNoteStore(t, time.Now())
	provider := &fakeMemoryProvider{response: `{"mutations":[{"operation":"skip"}]}`}
	worker := NewWorker(store)
	defer worker.Close()

	if worker.Enqueue(provider, TurnCandidate{Messages: []llm.Message{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "hello"}}}) {
		t.Fatal("expected greeting to be filtered")
	}
	candidate := usefulTurn("Remember that this project uses go test for verification.")
	if !worker.Enqueue(provider, candidate) {
		t.Fatal("expected useful candidate to enqueue")
	}
	if worker.Enqueue(provider, candidate) {
		t.Fatal("expected exact duplicate candidate to be filtered")
	}
}

func TestWorkerExtractCreateAndCodeFence(t *testing.T) {
	store := testNoteStore(t, time.Now())
	provider := &fakeMemoryProvider{response: "```json\n" + `{"mutations":[{"operation":"create","note":{"scope":"project","category":"project_knowledge","title":"Tests","summary":"Use go test","body":"Run go test ./... before finishing."}}]}` + "\n```"}
	worker := NewWorker(store)
	defer worker.Close()
	if err := worker.process(memoryJob{provider: provider, candidate: usefulTurn("The repository verification command is go test ./...")}); err != nil {
		t.Fatalf("process returned error: %v", err)
	}
	notes, err := store.readAllNotes(ScopeProject)
	if err != nil || len(notes) != 1 || !strings.Contains(notes[0].Body, "go test") {
		t.Fatalf("unexpected notes: %+v err=%v", notes, err)
	}
	if provider.toolsWereNonNil {
		t.Fatal("memory extraction must not expose tools")
	}
}

func TestWorkerExtractRejectsInvalidResponses(t *testing.T) {
	tests := []struct {
		name     string
		provider *fakeMemoryProvider
	}{
		{name: "invalid json", provider: &fakeMemoryProvider{response: "not-json"}},
		{name: "unknown operation", provider: &fakeMemoryProvider{response: `{"mutations":[{"operation":"delete"}]}`}},
		{name: "unknown field", provider: &fakeMemoryProvider{response: `{"mutations":[],"path":"outside"}`}},
		{name: "tool call", provider: &fakeMemoryProvider{toolCall: &llm.ToolCall{ID: "x", Name: "read_file"}}},
		{name: "stream error", provider: &fakeMemoryProvider{err: errors.New("stream failed")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := testNoteStore(t, time.Now())
			worker := NewWorker(store)
			defer worker.Close()
			if err := worker.process(memoryJob{provider: tt.provider, candidate: usefulTurn("A durable project fact that should be considered.")}); err == nil {
				t.Fatal("expected invalid extraction to fail")
			}
			notes, _ := store.readAllNotes(ScopeProject)
			if len(notes) != 0 {
				t.Fatalf("invalid extraction wrote notes: %+v", notes)
			}
		})
	}
}

func TestWorkerSensitiveMutationIsSkipped(t *testing.T) {
	store := testNoteStore(t, time.Now())
	provider := &fakeMemoryProvider{response: `{"mutations":[{"operation":"create","note":{"scope":"project","category":"reference","title":"Token","summary":"secret","body":"api_key=sk-abcdefghijklmnopqrstuvwxyz"}}]}`}
	worker := NewWorker(store)
	defer worker.Close()
	if err := worker.process(memoryJob{provider: provider, candidate: usefulTurn("Store this test credential only for this scenario.")}); err != nil {
		t.Fatalf("sensitive mutation should be skipped without failing other work: %v", err)
	}
	notes, _ := store.readAllNotes(ScopeProject)
	if len(notes) != 0 {
		t.Fatalf("sensitive mutation was written: %+v", notes)
	}
}

func TestWorkerUserScopeRequiresExplicitGlobalPreference(t *testing.T) {
	mutation := NoteMutation{Operation: MutationCreate, Note: Note{
		Scope: ScopeUser, Category: CategoryPreference, Title: "Style", Summary: "Concise", Body: "Prefer concise replies.",
	}}
	local, err := validateMemoryMutations([]NoteMutation{mutation}, usefulTurn("For this task, keep the answer concise."))
	if err != nil || local[0].Note.Scope != ScopeProject {
		t.Fatalf("temporary preference should become project scope: %+v err=%v", local, err)
	}
	global, err := validateMemoryMutations([]NoteMutation{mutation}, usefulTurn("Across all projects, always keep the answer concise."))
	if err != nil || global[0].Note.Scope != ScopeUser {
		t.Fatalf("explicit global preference should remain user scope: %+v err=%v", global, err)
	}
}

func TestWorkerSerializesJobs(t *testing.T) {
	store := testNoteStore(t, time.Now())
	provider := &fakeMemoryProvider{response: `{"mutations":[{"operation":"skip"}]}`, delay: 20 * time.Millisecond}
	worker := NewWorker(store)
	defer worker.Close()
	for i := 0; i < 3; i++ {
		candidate := usefulTurn(fmt.Sprintf("Durable project fact number %d should be evaluated for memory.", i))
		if !worker.Enqueue(provider, candidate) {
			t.Fatalf("failed to enqueue candidate %d", i)
		}
	}
	waitFor(t, time.Second, func() bool { return provider.callCount() == 3 })
	if provider.maxConcurrentCalls() != 1 {
		t.Fatalf("expected serialized provider calls, max=%d", provider.maxConcurrentCalls())
	}
}

func TestWorkerQueueIsBoundedAndCloseCancels(t *testing.T) {
	store := testNoteStore(t, time.Now())
	provider := &fakeMemoryProvider{blockUntilCancel: true}
	worker := NewWorker(store)
	if !worker.Enqueue(provider, usefulTurn("First durable fact blocks the background extractor until cancellation.")) {
		t.Fatal("expected first job to enqueue")
	}
	waitFor(t, time.Second, func() bool { return provider.callCount() == 1 })
	accepted := 0
	for i := 0; i < defaultMemoryQueueSize+2; i++ {
		if worker.Enqueue(provider, usefulTurn(fmt.Sprintf("Queued durable fact %d with enough descriptive content.", i))) {
			accepted++
		}
	}
	if accepted != defaultMemoryQueueSize {
		t.Fatalf("expected queue capacity %d, accepted %d", defaultMemoryQueueSize, accepted)
	}
	done := make(chan struct{})
	go func() {
		worker.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker Close did not cancel in-flight extraction")
	}
	if worker.Enqueue(provider, usefulTurn("A new durable fact after close must be rejected.")) {
		t.Fatal("enqueue after close should fail")
	}
}

func TestWorkerReportsBackgroundError(t *testing.T) {
	store := testNoteStore(t, time.Now())
	provider := &fakeMemoryProvider{response: "invalid"}
	worker := NewWorker(store)
	defer worker.Close()
	if !worker.Enqueue(provider, usefulTurn("This durable fact will produce an invalid extraction response.")) {
		t.Fatal("expected enqueue")
	}
	select {
	case err := <-worker.Errors():
		if err == nil || !strings.Contains(err.Error(), "JSON") {
			t.Fatalf("unexpected worker error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("expected observable background error")
	}
}

type fakeMemoryProvider struct {
	response         string
	err              error
	toolCall         *llm.ToolCall
	delay            time.Duration
	blockUntilCancel bool
	toolsWereNonNil  bool

	mu            sync.Mutex
	calls         int
	active        int
	maxConcurrent int
}

func (p *fakeMemoryProvider) Name() string  { return "fake" }
func (p *fakeMemoryProvider) Model() string { return "fake-memory" }

func (p *fakeMemoryProvider) Stream(ctx context.Context, _ []llm.Message, tools []llm.ToolDefinition, _ llm.StreamOptions) (<-chan llm.StreamEvent, <-chan error) {
	events := make(chan llm.StreamEvent, 2)
	errs := make(chan error, 1)
	p.mu.Lock()
	p.calls++
	p.active++
	if p.active > p.maxConcurrent {
		p.maxConcurrent = p.active
	}
	if tools != nil {
		p.toolsWereNonNil = true
	}
	p.mu.Unlock()
	go func() {
		defer close(events)
		defer close(errs)
		defer func() {
			p.mu.Lock()
			p.active--
			p.mu.Unlock()
		}()
		if p.blockUntilCancel {
			<-ctx.Done()
			return
		}
		if p.delay > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(p.delay):
			}
		}
		if p.err != nil {
			errs <- p.err
			return
		}
		if p.toolCall != nil {
			events <- llm.StreamEvent{ToolCall: p.toolCall}
			return
		}
		events <- llm.StreamEvent{Text: p.response}
		events <- llm.StreamEvent{Done: true, FinishReason: llm.FinishStop}
	}()
	return events, errs
}

func (p *fakeMemoryProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *fakeMemoryProvider) maxConcurrentCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.maxConcurrent
}

func usefulTurn(user string) TurnCandidate {
	return TurnCandidate{
		SessionID: "20260717-100000-abcd",
		Messages: []llm.Message{
			{Role: "user", Content: user},
			{Role: "assistant", Content: "I inspected the repository and confirmed this durable project information for future work."},
		},
		StoppedAt: time.Now(),
	}
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}
