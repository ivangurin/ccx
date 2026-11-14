package copilot

// Test-only re-exports of the request-classification helpers.
var (
	IsCompactRequestForTest = isCompactRequest
	QuotaIDsForTest         = quotaIDs
	MergeToolResultsForTest = mergeToolResults
	ClassifyQuotaForTest    = classifyQuota
	DetectSubagentForTest   = detectSubagent
)

// SwapRandReadForTest replaces the crypto/rand seam the random UUIDs read
// through and returns the restore func (the random-fallback tests force the
// read to fail to reach the fallback path; t.Cleanup(restore) puts it back).
func SwapRandReadForTest(read func([]byte) (int, error)) (restore func()) {
	old := randRead
	randRead = read

	return func() { randRead = old }
}
