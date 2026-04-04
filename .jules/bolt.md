## 2024-04-01 - History file inline rotation bottleneck
**Learning:** `history.Logger.rotateLockedInline()` runs every 100 appends. Previously, it called `readAll()` to get the current number of entries, which meant doing O(N) unmarshaling of every JSON line in the file, resulting in 300ms+ spikes when the file grew to ~90k lines, stalling heavy load.
**Action:** Always count lines efficiently (e.g. `bytes.Count(buf, []byte{'\n'})`) to verify constraints before performing expensive parse-heavy operations in recurring/background tasks.

## 2024-04-03 - History file reading optimization
**Learning:** `history.Logger.Query()` and `history.Logger.Stats()` previously used `l.readAll()` to load the entire JSONL file into memory as a slice of `models.HistoryEntry`. This caused massive memory allocations and GC pressure for large history files.
**Action:** Implement `processAll()` for line-by-line processing with a callback. Update `Query` and `Stats` to use sliding windows (bounded slices) instead of collecting all elements. This reduced query memory complexity from O(N) to O(limit).

## 2024-04-04 - Lexicographical sorting optimization
**Learning:** `cache.ServiceCache.AllEntries()` sorted entries using a multi-field comparison (`Host` then `Service`). However, since `CacheEntry` already has a composite `Key` field (`Host + \x1f + Service`) with a stable, low-byte separator, a direct lexicographical comparison of the `Key` field is functionally equivalent but significantly faster, as it avoids branching and multiple string comparisons.
**Action:** When objects share a stable composite string key with a low-byte separator, use single-string lexicographical sorting over multi-field comparisons to optimize sort operations.
