package questions

import "testing"

func TestForkUsageValidatesCacheTokenBreakdown(t *testing.T) {
	input, output, total := int64(100), int64(10), int64(110)
	cacheRead, cacheWrite := int64(70), int64(20)
	usage := Usage{
		InputTokens: &input, OutputTokens: &output, TotalTokens: &total,
		CacheReadTokens: &cacheRead, CacheWriteTokens: &cacheWrite,
		Complete: true,
		Calls: []Call{{Slot: "answer", InputTokens: &input, OutputTokens: &output, TotalTokens: &total,
			CacheReadTokens: &cacheRead, CacheWriteTokens: &cacheWrite, Status: "ok"}},
	}
	if !validUsage(usage, false, 1) {
		t.Fatal("合法缓存读写用量被拒绝")
	}
	invalidWrite := int64(31)
	usage.Calls[0].CacheWriteTokens = &invalidWrite
	if validUsage(usage, false, 1) {
		t.Fatal("缓存读写 token 超过输入总量仍被接受")
	}
	usage.Calls[0].CacheWriteTokens = &cacheWrite
	wrongAggregate := int64(69)
	usage.CacheReadTokens = &wrongAggregate
	if validUsage(usage, false, 1) {
		t.Fatal("汇总缓存 token 与逐次调用不一致仍被接受")
	}
}
