## 2024-05-31 - [Optimize `escHtml`]
**Learning:** In a codebase embedding JavaScript inside a Go template for a dashboard, `escHtml` was previously using a DOM-mutating method to encode text entities (`var div = document.createElement('div'); div.appendChild(document.createTextNode(s)); return div.innerHTML;`). This caused significant performance overhead, as generating and altering a DOM element on-the-fly inside mapping functions or loops (like history log iterations) is much slower than simple string replacements.
**Action:** Always favor native string replacement or regular expression substitutions for HTML escaping logic inside browser execution, rather than instantiating explicit DOM nodes repeatedly.

## 2024-04-01 - History file inline rotation bottleneck
**Learning:** `history.Logger.rotateLockedInline()` runs every 100 appends. Previously, it called `readAll()` to get the current number of entries, which meant doing O(N) unmarshaling of every JSON line in the file, resulting in 300ms+ spikes when the file grew to ~90k lines, stalling heavy load.
**Action:** Always count lines efficiently (e.g. `bytes.Count(buf, []byte{'\n'})`) to verify constraints before performing expensive parse-heavy operations in recurring/background tasks.

## 2024-04-03 - History file reading optimization
**Learning:** `history.Logger.Query()` and `history.Logger.Stats()` previously used `l.readAll()` to load the entire JSONL file into memory as a slice of `models.HistoryEntry`. This caused massive memory allocations and GC pressure for large history files.
**Action:** Implement `processAll()` for line-by-line processing with a callback. Update `Query` and `Stats` to use sliding windows (bounded slices) instead of collecting all elements. This reduced query memory complexity from O(N) to O(limit).

## 2024-04-04 - Lexicographical sorting optimization
**Learning:** `cache.ServiceCache.AllEntries()` sorted entries using a multi-field comparison (`Host` then `Service`). However, since `CacheEntry` already has a composite `Key` field (`Host + \x1f + Service`) with a stable, low-byte separator, a direct lexicographical comparison of the `Key` field is functionally equivalent but significantly faster, as it avoids branching and multiple string comparisons.
**Action:** When objects share a stable composite string key with a low-byte separator, use single-string lexicographical sorting over multi-field comparisons to optimize sort operations.

## 2026-04-05 - Bounded slice shifting CPU bottleneck
**Learning:** When scanning large files line-by-line to maintain a "recent N" sliding window, `history.Logger.Query()` and `history.Logger.Stats()` used `copy(slice, slice[1:])` on every processed entry beyond the limit. For a file with `L` lines and a limit `N`, this resulted in O(L * N) time complexity, leading to severe CPU usage and performance degradation during queries.
**Action:** Replace slice shifting loops with O(1) ring buffers (tracking insertion position with modulo arithmetic: `pos = (pos + 1) % N`). Unroll the buffer correctly in reverse only once at the end of the process.

## 2024-04-06 - Stream raw bytes during history rotation
**Learning:** `history.Logger.rotateLockedInline()` used to load the entire JSONL file by unmarshaling each line into objects, slicing off the old entries, and marshaling the remainder back into JSON strings. This was a severe memory and CPU bottleneck causing massive GC pressure and latency spikes when rotating large logs.
**Action:** Always process line-based formats using raw byte streaming when no data mutation is required. For file truncation (like log rotation), scan and skip bytes/lines, then write the remaining raw bytes directly to a temporary file before renaming, bypassing unmarshaling/marshaling entirely.

## 2026-04-08 - Dashboard target services fetch bottleneck
**Learning:** `DashboardHandler.ServeHTTP` fetched services for each target sequentially (`h.API.ListServices(target.HostName)`) in a single thread. This sequential I/O caused an N+1 API call bottleneck where the total dashboard rendering time scaled linearly with the number of configured targets, potentially leading to unacceptable delays on larger setups.
**Action:** When a single request needs to retrieve independent data from multiple remote services or nodes (like listing configurations across distinct targets), use concurrency structures (e.g., `sync.WaitGroup` and `sync.Mutex`) to parallelize the requests, collapsing the total wait time to O(1) relative to target count.

## 2024-05-31 - Sequential N+1 API call bottleneck
**Learning:** `admin.HandleListServices` fetched services from multiple target hosts sequentially inside a loop (`for _, target := range targets`). In deployments with multiple configured Icinga2 targets, this caused the API response time to scale linearly with the number of targets (N), introducing significant latency delays for dashboard users.
**Action:** When fetching independent data from multiple remote targets or services, use concurrency structures (`sync.WaitGroup` and `sync.Mutex`) to execute the fetches simultaneously. This reduces the wait time from O(N) to roughly O(1) relative to the number of targets.

## 2024-04-11 - Concurrent service fetching to avoid N+1 API bottlenecks
**Learning:** Sequential loops for querying multiple targets (like Icinga2 hosts) introduce N+1 API call bottlenecks, significantly degrading performance as the number of targets grows.
**Action:** When fetching independent data from multiple remote targets or services, use concurrency structures (e.g., `sync.WaitGroup` and `sync.Mutex`) instead of sequential loops. This reduces wait times to O(1) relative to the target count, bounded by the slowest individual request.

## 2024-05-31 - Sequential synchronous webhook alert processing bottleneck
**Learning:** `handler.WebhookHandler.ServeHTTP` previously processed incoming alerts sequentially in a loop. For large webhook payloads, the linear O(N) waiting time for HTTP API calls to Icinga2 resulted in severe throughput bottlenecks and potential timeouts from the sending service (like Grafana).
**Action:** When processing batches of external requests mapped to independent endpoints, utilize bounded concurrent execution (using a semaphore channel like `sem := make(chan struct{}, limit)` alongside `sync.WaitGroup`). This parallelizes network delays and significantly improves response time without overwhelming the downstream API. To preserve order without contention, pre-allocate the results slice and write using indices concurrently.

## 2024-06-05 - Avoid slice append contention with bounded concurrency
**Learning:** Appending to a single shared slice inside a concurrent loop (e.g., fetching services across many Icinga2 targets) introduces severe mutex lock contention, degrading parallelization benefits.
**Action:** When executing batch fan-out operations, use a bounded concurrency pattern (`sem := make(chan struct{}, limit)`) and eliminate mutexes entirely by pre-allocating a result slice-of-slices scaled to the target count. Have goroutines write directly to their assigned index (`results[index] = data`), and flatten the array only after all routines finish (`wg.Wait()`).

## 2026-04-13 - [Optimize IP extraction by replacing net.SplitHostPort]
**Learning:** The `history.Logger.Stats()` method loops over every history entry and calls `stripPort` on the `RemoteAddr` to extract the IP address for aggregation. Previously, `stripPort` used the standard library function `net.SplitHostPort(addr)`. Inside a hot loop processing thousands or tens of thousands of lines, `net.SplitHostPort` becomes a performance bottleneck due to its complex IPv6 parsing logic and memory allocations (it creates substrings and returns an error interface).
**Action:** When you only need to strip a trailing port from a string representation of an IP address or host (and strict validation is not required), avoid `net.SplitHostPort`. Instead, use a direct string manipulation like `strings.LastIndexByte(addr, ':')` to slice off the port. This avoids branching, function overhead, and heap allocations, resulting in a significantly faster operation (~4x speedup).
