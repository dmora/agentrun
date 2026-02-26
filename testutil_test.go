package agentrun

import "context"

// mockProcess is a test double for Process.
// Shared across root-package test files.
type mockProcess struct {
	output  chan Message
	sendFn  func(ctx context.Context, message string) error
	stopFn  func(ctx context.Context) error
	termErr error
	done    chan struct{}
}

func newMockProcess() *mockProcess {
	return &mockProcess{
		output: make(chan Message, 16),
		done:   make(chan struct{}),
	}
}

func (m *mockProcess) Output() <-chan Message { return m.output }

func (m *mockProcess) Send(ctx context.Context, message string) error {
	if m.sendFn != nil {
		return m.sendFn(ctx, message)
	}
	return nil
}

func (m *mockProcess) Stop(ctx context.Context) error {
	if m.stopFn != nil {
		return m.stopFn(ctx)
	}
	return nil
}

func (m *mockProcess) Wait() error {
	<-m.done
	return m.termErr
}

func (m *mockProcess) Err() error {
	select {
	case <-m.done:
		return m.termErr
	default:
		return nil
	}
}

// close closes the output channel and done channel.
func (m *mockProcess) close() {
	close(m.output)
	close(m.done)
}
