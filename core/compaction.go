package core

import "context"

// CompactionResult is the result of CompactNow.
type CompactionResult struct {
	UptoSeq      int64
	TokensBefore int64
	TokensAfter  int64
}

// Compactor summarizes session history on request.
type Compactor interface {
	CompactNow(ctx context.Context, sessionID string) (CompactionResult, error)
}
