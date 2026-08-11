package store

import (
	"context"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman/store/gen/query"
)

// ComposeHistoryEntry is one row of ComposeHistory: a compose project that was
// brought up at least once, keyed by the (WorkDir, Project) pair that is compose
// project identity everywhere else.
type ComposeHistoryEntry struct {
	// WorkDir is the canonical project directory: filepath.Clean(filepath.Abs(p)),
	// no symlink resolution (the compose/hash.go form).
	WorkDir string
	// Project is the compose project name, "" for an unnamed project.
	Project string
	// File is the compose file last used to bring the project up.
	File string
	// LastUsed is RFC3339 in UTC.
	LastUsed string
}

// RecordComposeHistory upserts the history row for a project, stamping LastUsed
// with the current UTC time and replacing File with the one just used.
func (s *Store) RecordComposeHistory(ctx context.Context, workDir, project, file string) error {
	return s.queries.UpsertComposeHistory(ctx, query.UpsertComposeHistoryParams{
		Workdir:  workDir,
		Project:  project,
		File:     file,
		Lastused: nowRFC3339UTC(),
	})
}

// TouchComposeHistory bumps LastUsed on an existing history row, and does
// nothing when there is none. It never inserts and never writes File, so a
// caller that knows the project but not the compose file it is running neither
// records an unlaunchable row nor clobbers the file a previous up recorded.
func (s *Store) TouchComposeHistory(ctx context.Context, workDir, project string) error {
	_, err := s.queries.TouchComposeHistory(ctx, query.TouchComposeHistoryParams{
		Lastused: nowRFC3339UTC(),
		Workdir:  workDir,
		Project:  project,
	})
	return err
}

// ListComposeHistory returns every history row, most recently used first.
func (s *Store) ListComposeHistory(ctx context.Context) ([]ComposeHistoryEntry, error) {
	rows, err := s.queries.ListComposeHistory(ctx)
	if err != nil {
		return nil, err
	}
	entries := make([]ComposeHistoryEntry, 0, len(rows))
	for _, r := range rows {
		entries = append(entries, ComposeHistoryEntry{
			WorkDir:  r.Workdir,
			Project:  r.Project,
			File:     r.File,
			LastUsed: r.Lastused,
		})
	}
	return entries, nil
}

// GetComposeHistory returns the history row for a project. The error is
// sql.ErrNoRows when the project was never recorded.
func (s *Store) GetComposeHistory(
	ctx context.Context,
	workDir, project string,
) (ComposeHistoryEntry, error) {
	r, err := s.queries.GetComposeHistory(ctx, query.GetComposeHistoryParams{
		Workdir: workDir,
		Project: project,
	})
	if err != nil {
		return ComposeHistoryEntry{}, err
	}
	return ComposeHistoryEntry{
		WorkDir:  r.Workdir,
		Project:  r.Project,
		File:     r.File,
		LastUsed: r.Lastused,
	}, nil
}

// DeleteComposeHistory removes a project's history row and reports whether
// there was one. A project the launcher offers to forget need not be recorded
// at all — a never-run named def whose compose file is gone reaches the same
// offer — so a missing row is a no-op, not an error.
func (s *Store) DeleteComposeHistory(
	ctx context.Context,
	workDir, project string,
) (bool, error) {
	n, err := s.queries.DeleteComposeHistory(ctx, query.DeleteComposeHistoryParams{
		Workdir: workDir,
		Project: project,
	})
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func nowRFC3339UTC() string {
	return time.Now().UTC().Format(time.RFC3339)
}
