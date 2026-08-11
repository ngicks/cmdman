package cmdman

import (
	"context"
	"fmt"
	"time"
)

// ComposeHistoryRequest identifies the compose project whose recency is being
// recorded. WorkDir and Project are the project identity pair; WorkDir must be
// canonical (filepath.Clean(filepath.Abs(p)), no symlink resolution), which is
// the form compose.ComposeSpec.WorkDir already carries.
type ComposeHistoryRequest struct {
	WorkDir string
	Project string
	// File is the compose file the project was brought up with. An empty File
	// means "bump recency only": see RecordComposeHistory.
	File string
}

// ComposeHistoryEntry is one recorded compose project: the identity pair, the
// compose file it was last brought up with, and when that was.
type ComposeHistoryEntry struct {
	WorkDir string
	Project string
	File    string
	// LastUsed is the recorded stamp parsed back from its RFC3339 UTC storage
	// form; it is the zero time when the stored value cannot be parsed.
	LastUsed time.Time
}

// ListComposeHistory returns every recorded compose project, most recently used
// first — the launcher's recency order (D7).
func (s *Service) ListComposeHistory(ctx context.Context) ([]ComposeHistoryEntry, error) {
	st, err := s.openStore(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	rows, err := st.ListComposeHistory(ctx)
	if err != nil {
		return nil, fmt.Errorf("list compose history: %w", err)
	}
	entries := make([]ComposeHistoryEntry, 0, len(rows))
	for _, r := range rows {
		lastUsed, _ := time.Parse(time.RFC3339, r.LastUsed)
		entries = append(entries, ComposeHistoryEntry{
			WorkDir:  r.WorkDir,
			Project:  r.Project,
			File:     r.File,
			LastUsed: lastUsed,
		})
	}
	return entries, nil
}

// DeleteComposeHistory forgets a recorded compose project, reporting whether a
// row was actually removed. It is what the launcher's stale-entry removal calls
// (D10/Q12): the project's compose file no longer resolves, so the row that
// keeps offering it is the thing to drop.
//
// Forgetting a project that was never recorded is a no-op, not an error — the
// same offer is made for a discovered-but-unrecorded project whose file is gone.
func (s *Service) DeleteComposeHistory(
	ctx context.Context,
	workDir, project string,
) (bool, error) {
	st, err := s.openStore(ctx, true)
	if err != nil {
		return false, fmt.Errorf("open store: %w", err)
	}
	removed, err := st.DeleteComposeHistory(ctx, workDir, project)
	if err != nil {
		return false, fmt.Errorf("delete compose history: %w", err)
	}
	return removed, nil
}

// RecordComposeHistory stamps a compose project as used now.
//
// With a File it upserts the row, so a project that has never been recorded
// appears in history and a re-up with a different file overwrites File.
//
// With an empty File it only bumps LastUsed on an existing row and does nothing
// when there is none: a caller that resolved the project from stored labels
// rather than from a compose file knows no File, and must neither record an
// unlaunchable row nor erase the file a previous up recorded.
func (s *Service) RecordComposeHistory(ctx context.Context, req ComposeHistoryRequest) error {
	st, err := s.openStore(ctx, true)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	if req.File == "" {
		if err := st.TouchComposeHistory(ctx, req.WorkDir, req.Project); err != nil {
			return fmt.Errorf("touch compose history: %w", err)
		}
		return nil
	}
	if err := st.RecordComposeHistory(ctx, req.WorkDir, req.Project, req.File); err != nil {
		return fmt.Errorf("record compose history: %w", err)
	}
	return nil
}
