package core

import "context"

// CompactionResult adalah hasil CompactNow.
type CompactionResult struct {
	UptoSeq      int64
	TokensBefore int64
	TokensAfter  int64
}

// Compactor meringkas riwayat session saat diminta.
type Compactor interface {
	CompactNow(ctx context.Context, sessionID string) (CompactionResult, error)
}
