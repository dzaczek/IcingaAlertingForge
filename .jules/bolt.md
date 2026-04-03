## 2024-04-01 - History file inline rotation bottleneck
**Learning:** `history.Logger.rotateLockedInline()` runs every 100 appends. Previously, it called `readAll()` to get the current number of entries, which meant doing O(N) unmarshaling of every JSON line in the file, resulting in 300ms+ spikes when the file grew to ~90k lines, stalling heavy load.
**Action:** Always count lines efficiently (e.g. `bytes.Count(buf, []byte{'\n'})`) to verify constraints before performing expensive parse-heavy operations in recurring/background tasks.

## 2024-04-03 - History file reading optimization
**Learning:** `history.Logger.Query()` and `history.Logger.Stats()` previously used `l.readAll()` to load the entire JSONL file into memory as a slice of `models.HistoryEntry`. This caused massive memory allocations and GC pressure for large history files.
**Action:** Implement `processAll()` for line-by-line processing with a callback. Update `Query` and `Stats` to use sliding windows (bounded slices) instead of collecting all elements. This reduced query memory complexity from O(N) to O(limit).

## 2024-04-03 - Lexicographical Sort Optimization
**Learning:** In Go, string sorting can be significantly faster than struct field comparison in `sort.Slice`. By leveraging the application's stable cache key format (`host` + `\x1f` + `service`), we were able to avoid multi-field comparison logic because the separator (`\x1f`) ensures correct lexicographical ordering.
**Action:** When working with structured keys separated by consistent low-byte characters, prefer single string comparisons over parsing/splitting and multi-field comparisons to achieve better sorting performance.
