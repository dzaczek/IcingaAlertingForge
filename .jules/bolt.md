## 2024-04-01 - History file inline rotation bottleneck
**Learning:** `history.Logger.rotateLockedInline()` runs every 100 appends. Previously, it called `readAll()` to get the current number of entries, which meant doing O(N) unmarshaling of every JSON line in the file, resulting in 300ms+ spikes when the file grew to ~90k lines, stalling heavy load.
**Action:** Always count lines efficiently (e.g. `bytes.Count(buf, []byte{'\n'})`) to verify constraints before performing expensive parse-heavy operations in recurring/background tasks.

## 2024-04-03 - History file reading optimization
**Learning:** `history.Logger.Query()` and `history.Logger.Stats()` previously used `l.readAll()` to load the entire JSONL file into memory as a slice of `models.HistoryEntry`. This caused massive memory allocations and GC pressure for large history files.
**Action:** Implement `processAll()` for line-by-line processing with a callback. Update `Query` and `Stats` to use sliding windows (bounded slices) instead of collecting all elements. This reduced query memory complexity from O(N) to O(limit).

## 2026-04-05 - Lexicographical sorting regressions
**Learning:** Trying to optimize multi-field struct sorting by using single-string lexicographical sorting on concatenated fields (e.g., `Host + "\x1f" + Service`) introduces sorting regressions when field lengths and content differ. The ASCII value of the separator can unexpectedly evaluate higher/lower than the next character of another key.
**Action:** Never optimize `sort.Slice` comparison functions by joining multiple fields into one composite string, even if separated by low-byte characters. Use standard field-by-field conditional comparisons instead.

## 2026-04-05 - Bounded slice shifting CPU bottleneck
**Learning:** When scanning large files line-by-line to maintain a "recent N" sliding window, `history.Logger.Query()` and `history.Logger.Stats()` used `copy(slice, slice[1:])` on every processed entry beyond the limit. For a file with `L` lines and a limit `N`, this resulted in O(L * N) time complexity, leading to severe CPU usage and performance degradation during queries.
**Action:** Replace slice shifting loops with O(1) ring buffers (tracking insertion position with modulo arithmetic: `pos = (pos + 1) % N`). Unroll the buffer correctly in reverse only once at the end of the process.
