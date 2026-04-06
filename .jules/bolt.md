## 2024-04-01 - History file inline rotation bottleneck
**Learning:** `history.Logger.rotateLockedInline()` runs every 100 appends. Previously, it called `readAll()` to get the current number of entries, which meant doing O(N) unmarshaling of every JSON line in the file, resulting in 300ms+ spikes when the file grew to ~90k lines, stalling heavy load.
**Action:** Always count lines efficiently (e.g. `bytes.Count(buf, []byte{'\n'})`) to verify constraints before performing expensive parse-heavy operations in recurring/background tasks.

## 2024-04-03 - History file reading optimization
**Learning:** `history.Logger.Query()` and `history.Logger.Stats()` previously used `l.readAll()` to load the entire JSONL file into memory as a slice of `models.HistoryEntry`. This caused massive memory allocations and GC pressure for large history files.
**Action:** Implement `processAll()` for line-by-line processing with a callback. Update `Query` and `Stats` to use sliding windows (bounded slices) instead of collecting all elements. This reduced query memory complexity from O(N) to O(limit).

## 2024-04-04 - Bounded slice shifting pattern
**Learning:** Bounded lists of recent items inside line-by-line stream processing used `copy(slice, slice[1:])` shifting inside loops. While keeping memory at O(limit), shifting slices on each iteration creates O(limit) CPU overhead for each of the O(N) history entries processed.
**Action:** Avoid O(N) slice-shifting inside loops. Implement O(1) ring buffers using a position index (`pos = (pos + 1) % limit`) instead, significantly reducing CPU overhead during sequential processing tasks.
