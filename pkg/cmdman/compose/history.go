package compose

import (
	"context"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/go-common/contextkey"
)

// recordProject records a project and the compose file it was brought up from,
// so the launcher can offer it again after its commands are gone. spec.WorkDir
// is already the canonical form the history key requires.
func (s *Service) recordProject(ctx context.Context, spec ComposeSpec) {
	s.recordHistory(ctx, cmdman.ComposeHistoryRequest{
		WorkDir: spec.WorkDir,
		Project: spec.Project,
		File:    spec.ComposeFile,
	})
}

// bumpRecency marks an already-recorded project as used now. Rows come into
// existence through recordProject alone, so a path that only starts what a
// previous create left behind must not invent one — nor claim a compose file it
// was not asked to use.
func (s *Service) bumpRecency(ctx context.Context, workDir, project string) {
	s.recordHistory(ctx, cmdman.ComposeHistoryRequest{
		WorkDir: workDir,
		Project: project,
	})
}

// recordHistory is best-effort: history is auxiliary state, so a failed write
// warns and never fails the operation that triggered it.
func (s *Service) recordHistory(ctx context.Context, req cmdman.ComposeHistoryRequest) {
	if err := s.svc.RecordComposeHistory(ctx, req); err != nil {
		contextkey.ValueSlogLoggerDefault(ctx).WarnContext(ctx,
			"compose: record project history",
			"project", req.Project,
			"workdir", req.WorkDir,
			"error", err,
		)
	}
}
