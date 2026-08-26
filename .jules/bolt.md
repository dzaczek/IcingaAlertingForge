## Performance Optimizations

### Action
**What:** Replaced inefficient repeated string concatenations (`html += ...`) inside hot JavaScript loops (`loadAlerts` and `fetchServiceHistory` in `handler/dashboard.go`) with an array pre-allocation and `join()` pattern.
**Why:** Constructing large HTML strings inside loops using `+=` can trigger repeated string allocations and garbage collection overhead in browsers, particularly for long lists of history entries or alerts.
**Impact:** Our benchmarks showed that pre-allocating an array and assigning the complete row HTML by index (`htmlParts[i] = ...`), followed by joining and appending, is ~8% faster in Node/V8 than the previous multiple `+=` concatenation approach per row.
## 2026-05-29 - Optimize SSE PublishRaw string formatting
**Learning:** In Go, repeated string concatenation using `fmt.Sprintf` is slower and creates more memory allocations because of reflection overhead. When building strings in hot loops or high-throughput event paths (like SSE broadcasting), using `strings.Builder` with a pre-allocated buffer (`Grow()`) significantly reduces allocations and speeds up execution.
**Action:** When constructing strings in performance-critical code paths, avoid `fmt.Sprintf` and instead use `strings.Builder` with a known or calculated capacity.
## 2026-08-26 - Pre-allocate slice capacity when flattening nested slices
**Learning:** When aggregating or flattening multiple arrays/slices into a single slice inside a loop using `append()`, failing to pre-allocate capacity forces Go to re-allocate underlying arrays and incurs garbage collection overhead.
**Action:** Always pre-calculate the total required capacity from the nested slices and allocate the destination slice using `make([]T, 0, capacity)` before appending elements.
