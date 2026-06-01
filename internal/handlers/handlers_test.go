package handlers

// Test mocks live here so individual test files stay focused on
// table-driven cases rather than scaffolding. Add new fake-types here
// when a new handler test needs a dependency that doesn't yet have a
// mock — keep the implementations tiny (return the field, no logic) so
// the test cases drive everything.

import (
	"context"
	"errors"

	"github.com/tomanagle/yullu/internal/server"
	"github.com/tomanagle/yullu/internal/store"
)

// assertErr is shared sugar for stubbing error returns in table rows.
// Keeps test-case literals readable: editorErr: assertErr("db locked").
func assertErr(msg string) error { return errors.New(msg) }

// fakeStatusService is the test stub for StatusService. Set the public
// fields per test case; ServeHTTP returns them as-is.
type fakeStatusService struct {
	statusOut Status
	retryOut  Status
}

func (f *fakeStatusService) Status() Status { return f.statusOut }
func (f *fakeStatusService) Retry() Status  { return f.retryOut }

// fakeRater is the test stub for MemoryRater. listOut + listErr drive
// ListUnrated; rateOut + rateErr + rateGotID + rateGotRating + rateGotComment
// drive RateMemory.
type fakeRater struct {
	listOut []store.Memory
	listErr error

	// rate inputs captured so tests can assert the handler forwarded
	// the parsed body correctly.
	rateGotID      int64
	rateGotRating  int
	rateGotComment string

	rateOut *store.Memory
	rateErr error
}

func (f *fakeRater) ListUnrated(_ context.Context, _ string, _ int) ([]store.Memory, error) {
	return f.listOut, f.listErr
}

func (f *fakeRater) RateMemory(_ context.Context, id int64, rating int, comment string) (*store.Memory, error) {
	f.rateGotID = id
	f.rateGotRating = rating
	f.rateGotComment = comment
	return f.rateOut, f.rateErr
}

// fakeMessageRecorder is the test stub for MessageRecorder. recordOut
// is the project_id echoed back; recordErr is returned to the handler.
type fakeMessageRecorder struct {
	recordOut string
	recordErr error

	// captured inputs
	gotProjectOverride string
	gotCwd             string
	gotSessionID       string
	gotMessages        []RecordedMessage
}

func (f *fakeMessageRecorder) RecordMessages(
	_ context.Context, projectOverride, cwd, sessionID string, msgs []RecordedMessage,
) (string, error) {
	f.gotProjectOverride = projectOverride
	f.gotCwd = cwd
	f.gotSessionID = sessionID
	f.gotMessages = msgs
	return f.recordOut, f.recordErr
}

// fakeMemoryReader satisfies MemoryReader. listOut/searchOut drive the
// respective methods; both share a single error knob since the handler
// treats them symmetrically.
type fakeMemoryReader struct {
	listOut   []store.Memory
	searchOut []store.Memory
	err       error

	// Captured inputs from the most recent call.
	gotProjectID string
	gotQuery     string
	gotLimit     int
}

func (f *fakeMemoryReader) List(_ context.Context, projectID string, limit int) ([]store.Memory, error) {
	f.gotProjectID = projectID
	f.gotLimit = limit
	return f.listOut, f.err
}

func (f *fakeMemoryReader) SearchSemantic(_ context.Context, projectID, query string, limit int) ([]store.Memory, error) {
	f.gotProjectID = projectID
	f.gotQuery = query
	f.gotLimit = limit
	return f.searchOut, f.err
}

// fakeMemoryEditor satisfies MemoryEditor. updateOut + updateErr drive
// UpdateMemory; deleteErr drives DeleteMemory.
type fakeMemoryEditor struct {
	updateOut *store.Memory
	updateErr error
	deleteErr error

	// Captured inputs.
	updateGotID      int64
	updateGotContent string
	updateGotTags    []string
	deleteGotID      int64
}

func (f *fakeMemoryEditor) UpdateMemory(_ context.Context, id int64, content string, tags []string) (*store.Memory, error) {
	f.updateGotID = id
	f.updateGotContent = content
	f.updateGotTags = tags
	return f.updateOut, f.updateErr
}

func (f *fakeMemoryEditor) DeleteMemory(_ context.Context, id int64) error {
	f.deleteGotID = id
	return f.deleteErr
}

// fakeMemoryRecaller satisfies MemoryRecaller. recallOut + recallErr
// drive RecallMemories; the captured inputs let tests assert that the
// handler unpacked the request body correctly.
type fakeMemoryRecaller struct {
	recallOut []store.Memory
	recallErr error

	gotProjectID  string
	gotQuery      string
	gotCategories []store.MemoryCategory
	gotLimit      int
}

func (f *fakeMemoryRecaller) RecallMemories(
	_ context.Context, projectID, query string, categories []store.MemoryCategory, limit int,
) ([]store.Memory, error) {
	f.gotProjectID = projectID
	f.gotQuery = query
	f.gotCategories = categories
	f.gotLimit = limit
	return f.recallOut, f.recallErr
}

// fakeRetrievalAnalytics satisfies RetrievalAnalytics. listOut/listErr drive
// ListRetrievals; captured inputs let tests assert the handler parsed the
// query string correctly.
type fakeRetrievalAnalytics struct {
	listOut []store.RetrievalGroup
	listErr error

	gotListProjectID string
	gotListLimit     int
}

func (f *fakeRetrievalAnalytics) ListRetrievals(_ context.Context, projectID string, limit int) ([]store.RetrievalGroup, error) {
	f.gotListProjectID = projectID
	f.gotListLimit = limit
	return f.listOut, f.listErr
}

// fakeDreamer satisfies Dreamer. dreamOut + dreamErr drive Dream; the
// captured opts let tests assert ContextMemories etc. were forwarded.
type fakeDreamer struct {
	dreamOut *server.DreamResult
	dreamErr error
	gotOpts  server.DreamOptions
}

func (f *fakeDreamer) Dream(_ context.Context, opts server.DreamOptions) (*server.DreamResult, error) {
	f.gotOpts = opts
	return f.dreamOut, f.dreamErr
}

// Compile-time interface checks so missing methods surface here, not at
// the test callsite. Add new fakes to the var block when introducing them.
var (
	_ StatusService      = (*fakeStatusService)(nil)
	_ MemoryRater        = (*fakeRater)(nil)
	_ MessageRecorder    = (*fakeMessageRecorder)(nil)
	_ MemoryReader       = (*fakeMemoryReader)(nil)
	_ MemoryEditor       = (*fakeMemoryEditor)(nil)
	_ MemoryRecaller     = (*fakeMemoryRecaller)(nil)
	_ Dreamer            = (*fakeDreamer)(nil)
	_ RetrievalAnalytics = (*fakeRetrievalAnalytics)(nil)
)
