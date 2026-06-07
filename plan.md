1. **Optimize `fmt.Sprintf` in `handler/work_mode.go`**
   - Replace `fmt.Sprintf("OK: %s", summary)` with direct string concatenation: `"OK: " + summary`.
   - Replace `fmt.Sprintf("%s: %s", exitStatusLabel(exitStatus), summary)` with direct string concatenation: `exitStatusLabel(exitStatus) + ": " + summary`.
   - Replace `fmt.Sprintf("%s: Alert firing", exitStatusLabel(exitStatus))` with direct string concatenation: `exitStatusLabel(exitStatus) + ": Alert firing"`.
   - Replace `fmt.Sprintf("%s-%s-%d", requestID, serviceName, time.Now().UnixNano())` with direct string concatenation using `strconv.FormatInt`: `requestID + "-" + serviceName + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)`.
   - Add `strconv` import to `handler/work_mode.go`.
2. **Optimize `fmt.Sprintf` in `handler/webhook.go`**
   - Replace `fmt.Sprintf("%d", len(payload.Alerts))` with `strconv.Itoa(len(payload.Alerts))`.
   - Add `strconv` import to `handler/webhook.go`.
3. **Add Journal Entry in `.jules/bolt.md`**
   - Document the performance pattern of avoiding `fmt.Sprintf` for simple string concatenation and integer conversion in hot paths.
4. **Complete pre-commit steps to ensure proper testing, verification, review, and reflection are done.**
5. **Submit PR**
   - Create a PR with title "⚡ Bolt: Optimize string formatting in hot paths" and the required description format.
