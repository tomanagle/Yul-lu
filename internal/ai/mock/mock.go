// Package mock provides testify/mock-based test doubles for the
// interfaces declared in internal/ai. Tests configure expectations with
// On(...).Return(...), call AssertExpectations(t) at the end. Real
// embedders / reasoners hit the network and need API keys; these mocks
// drive behaviour deterministically in-process.
//
// Usage:
//
//	m := &mock.EmbedderMock{}
//	m.On("Dim").Return(4)
//	m.On("ID").Return("mock:test")
//	m.On("Embed", mock.Anything, []string{"hi"}).
//	  Return([][]float32{{1, 0, 0, 0}}, nil)
//
//	srv := server.New(store, m, reasoner, cfg, logger)
//	// … exercise ...
//	m.AssertExpectations(t)
package mock

import (
	"context"

	"github.com/stretchr/testify/mock"

	"github.com/tomanagle/yullu/internal/ai"
)

// EmbedderMock is a testify-mock satisfying ai.Embedder.
type EmbedderMock struct {
	mock.Mock
}

func (m *EmbedderMock) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	args := m.Called(ctx, texts)
	// args.Get returns interface{}; the Return(...) wiring is expected
	// to pass [][]float32 (nil is fine).
	if v := args.Get(0); v != nil {
		return v.([][]float32), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *EmbedderMock) Dim() int {
	args := m.Called()
	return args.Int(0)
}

func (m *EmbedderMock) ID() string {
	args := m.Called()
	return args.String(0)
}

// ReasonerMock is a testify-mock satisfying ai.Reasoner.
type ReasonerMock struct {
	mock.Mock
}

func (m *ReasonerMock) Reason(ctx context.Context, req ai.ReasonRequest) (string, error) {
	args := m.Called(ctx, req)
	return args.String(0), args.Error(1)
}

func (m *ReasonerMock) ID() string {
	args := m.Called()
	return args.String(0)
}

// UsageRecorderMock is a testify-mock satisfying ai.UsageRecorder.
type UsageRecorderMock struct {
	mock.Mock
}

func (m *UsageRecorderMock) RecordUsage(ctx context.Context, e ai.UsageEvent) error {
	args := m.Called(ctx, e)
	return args.Error(0)
}

// Compile-time interface checks: a missing method on the real interface
// surfaces here, not at the test callsite.
var (
	_ ai.Embedder      = (*EmbedderMock)(nil)
	_ ai.Reasoner      = (*ReasonerMock)(nil)
	_ ai.UsageRecorder = (*UsageRecorderMock)(nil)
)
