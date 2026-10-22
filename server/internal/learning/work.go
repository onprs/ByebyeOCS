package learning

import "context"

// WorkOption 保留平台选项值与可供模型识别的标签，提交时使用原始值。
type WorkOption struct {
	Label string
	Value string
	Text  string
}

// WorkImage 是取题时读取的受限图片；内容不写入日志或提交状态表。
type WorkImage struct {
	ID       string
	MIMEType string
	Base64   string
}

// WorkQuestion 是平台题目的标准表示，ID 与答题顺序均来自当前试卷。
type WorkQuestion struct {
	ID      string
	Type    string
	Title   string
	Options []WorkOption
	Blanks  int
	Images  []WorkImage
}

// WorkSheet 的指纹覆盖题目、选项和当前答题机会，用于防止错卷提交。
type WorkSheet struct {
	Fingerprint string
	Questions   []WorkQuestion
	Submitted   bool
}

// WorkAnswer 的值由确定性映射产生，选择题使用平台选项值。
type WorkAnswer struct {
	QuestionID string
	Values     []string
}

// WorkSubmission 只描述提交响应；任务完成仍由平台独立核验。
type WorkSubmission struct {
	Accepted         bool
	AlreadySubmitted bool
}

// WorkSessionPlatform 为同一次自动作答共享平台轮换的Cookie容器。
// 返回的清理函数在本次执行结束时调用，不跨账号、任务或进程保存容器。
type WorkSessionPlatform interface {
	BeginWorkSession(context.Context, []byte) (context.Context, func(), error)
}

// WorkPlatform 通过原生 HTTP 取题并提交。SubmitWork 必须重新取题并核对指纹，
// 在实际 POST 前调用 beforePost（若非 nil），一次调用最多提交一次。
// beforePost 仅核对租约并保存提交标记，不涉及人工确认；失败时不得发送 POST。
type WorkPlatform interface {
	ReadWork(ctx context.Context, session []byte, task PlatformTaskRef) (WorkSheet, error)
	SubmitWork(ctx context.Context, session []byte, task PlatformTaskRef, fingerprint string, answers []WorkAnswer, beforePost func(context.Context) error) (WorkSubmission, error)
}
