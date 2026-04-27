package reports

import "time"

// SyncProtocol is an anemic data model representing a single sync cycle as
// persisted in the database.
//
// EndedAt is a pointer so an in-progress cycle can be represented as nil. A
// row is inserted with EndedAt == nil at the start of a cycle and updated
// with a non-nil EndedAt when the cycle finishes.
//
// LastActivityAt is updated on every per-repo result write so the health
// check can detect a stuck cycle by observing that no progress has been made
// recently — even before Complete is ever reached.
type SyncProtocol struct {
	ID             int64
	StartedAt      time.Time
	EndedAt        *time.Time
	NextRunAt      time.Time
	LastActivityAt time.Time
	TotalRepos     int
	SucceededRepos int
	FailedRepos    int
	Results        []RepoResult
}

// RepoResult records the outcome of mirroring a single repository within a
// SyncProtocol. Error is empty when Success is true.
type RepoResult struct {
	RepoFullName string
	Operation    string
	Duration     time.Duration
	Success      bool
	Error        string
}
