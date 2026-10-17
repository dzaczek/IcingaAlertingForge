## Performance Optimizations

### Action
**What:** Replaced inefficient repeated string concatenations (`html += ...`) inside hot JavaScript loops (`loadAlerts` and `fetchServiceHistory` in `handler/dashboard.go`) with an array pre-allocation and `join()` pattern.
**Why:** Constructing large HTML strings inside loops using `+=` can trigger repeated string allocations and garbage collection overhead in browsers, particularly for long lists of history entries or alerts.
**Impact:** Our benchmarks showed that pre-allocating an array and assigning the complete row HTML by index (`htmlParts[i] = ...`), followed by joining and appending, is ~8% faster in Node/V8 than the previous multiple `+=` concatenation approach per row.
## 2026-05-29 - Optimize SSE PublishRaw string formatting
**Learning:** In Go, repeated string concatenation using `fmt.Sprintf` is slower and creates more memory allocations because of reflection overhead. When building strings in hot loops or high-throughput event paths (like SSE broadcasting), using `strings.Builder` with a pre-allocated buffer (`Grow()`) significantly reduces allocations and speeds up execution.
**Action:** When constructing strings in performance-critical code paths, avoid `fmt.Sprintf` and instead use `strings.Builder` with a known or calculated capacity.
## 2026-05-30 - Slice Flattening Pre-allocation
**Learning:** Growing slices incrementally through `append()` without pre-allocating capacity triggers costly underlying array reallocations and memory copies. When flattening multidimensional slices or appending multiple arrays in a loop, pre-calculating the exact required capacity and allocating a slice with `make([]T, 0, capacity)` eliminates these allocations. Benchmarks in Go show this can reduce time per operation by ~50% in tight loops (e.g., from ~18000ns to ~9000ns) and drastically lowers garbage collection overhead.
**Action:** When aggregating or flattening multiple arrays/slices, always calculate the total required length upfront and pre-allocate the destination slice's capacity before appending.
## 2026-10-17 - Optimize SSE ServeHTTP memory allocations
**Learning:** Writing dynamically constructed content to an `io.Writer` (like an HTTP response in an SSE loop) using `fmt.Fprint` or `fmt.Fprintf` incurs reflection overhead and string conversion allocations. Using sequential `w.Write()` calls for static strings and byte slices reduces memory allocations to zero in the hot loop.
**Action:** Replace `fmt.Fprintf` with multiple `w.Write()` calls, and write the contents of a `bytes.Buffer` using `w.Write(buf.Bytes())` rather than converting it via `buf.String()`.
