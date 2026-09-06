---
tags: logdriver k8sfile writer concurrency bug
---

# k8s-file writer can split log entries larger than 4096 bytes

Recorded 2026-08-22 in the legacy backlog `issue.md`; migrated 2026-09-03.

`cmdman/logdriver/k8sfile/writer.go` buffers through `bufio.NewWriter`'s
default 4096-byte buffer; an entry larger than that splits into several
`write(2)` calls and can interleave with a concurrent appender to the same
file. Normal-length lines are safe: the whole entry is assembled and flushed
in one write. Today the only shared-file appenders are mux op workers, and
those are serialized per file by the deterministic `muxop-<identity>`
command-name lock, so the hazard is latent.

Fix direction: enlarge or replace the buffering strategy if multi-writer
log files ever become a supported pattern. The fix touches the writer's hot
path around `CurrentOffset`/rotation accounting, which is why it was
deferred.
