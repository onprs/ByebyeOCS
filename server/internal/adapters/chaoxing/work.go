package chaoxing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/learning"
)

const (
	maxWorkQuestions   = 200
	maxWorkAnswers     = 200
	maxWorkValueLen    = 4096
	maxWorkShortAnswer = 16384
)

var _ learning.WorkPlatform = (*Platform)(nil)
var _ learning.WorkSessionPlatform = (*Platform)(nil)

type workJob struct {
	CourseID    string
	ClassID     string
	CPI         string
	KnowledgeID string
	ObjectID    string
	JobID       string
	WorkID      string
	KToken      string
	Enc         string
}

type workQuestion struct {
	learning.WorkQuestion
	TypeCode    string
	BlankStyle  string
	BlankFields []string
	AnswerField string
	ImageRefs   []workImageRef
	ImageHashes []string
}

type workPage struct {
	Sheet     learning.WorkSheet
	Form      url.Values
	Job       workJob
	Questions []workQuestion
}

// ReadWork 重新读取当前卡片和测验页面，返回可作答题目与稳定试卷指纹。
// 已提交、未开放和暂不支持的题型不会返回可提交题目。
func (p *Platform) ReadWork(ctx context.Context, sessionBytes []byte, task learning.PlatformTaskRef) (learning.WorkSheet, error) {
	if !p.options.ReadOnlyEnabled {
		return learning.WorkSheet{}, learning.Fail(learning.FailureCapabilityMissing, "学习通测验适配未启用")
	}
	session, err := decodeSession(sessionBytes)
	if err != nil {
		return learning.WorkSheet{}, learning.Fail(learning.FailureIdentityInvalid, "平台会话格式无效")
	}
	// 读取过程中的卡片、试卷与图片必须复用同一 Cookie 容器，否则平台轮换的 Cookie 会丢失。
	ctx, cleanup, err := p.withSessionClient(ctx, session)
	if err != nil {
		return learning.WorkSheet{}, err
	}
	defer cleanup()
	page, err := p.readWorkPage(ctx, session, task)
	if err != nil {
		return learning.WorkSheet{}, err
	}
	return page.Sheet, nil
}

// SubmitWork 必须先按同一任务重新读取试卷并核对指纹，之后只发送一次最终提交。
// 返回的 Accepted 只表示平台接收了本次答案，任务完成仍由独立 CheckTask 确认。
func (p *Platform) SubmitWork(ctx context.Context, sessionBytes []byte, task learning.PlatformTaskRef, fingerprint string, answers []learning.WorkAnswer, beforePost func(context.Context) error) (learning.WorkSubmission, error) {
	if !p.options.ReadOnlyEnabled {
		return learning.WorkSubmission{}, learning.Fail(learning.FailureCapabilityMissing, "学习通测验适配未启用")
	}
	if strings.TrimSpace(fingerprint) == "" {
		return learning.WorkSubmission{}, learning.Fail(learning.FailurePermanent, "缺少当前试卷指纹，未提交")
	}
	if len(answers) == 0 || len(answers) > maxWorkAnswers {
		return learning.WorkSubmission{}, learning.Fail(learning.FailurePermanent, "答案数量不符合当前试卷")
	}
	session, err := decodeSession(sessionBytes)
	if err != nil {
		return learning.WorkSubmission{}, learning.Fail(learning.FailureIdentityInvalid, "平台会话格式无效")
	}
	// 重新取题和最终提交必须在同一 Cookie 容器内完成，提交请求要带上本次读取轮换后的 Cookie。
	ctx, cleanup, err := p.withSessionClient(ctx, session)
	if err != nil {
		return learning.WorkSubmission{}, err
	}
	defer cleanup()
	// 提交前重新读取一次当前表单和题目，不能复用调用方缓存。
	page, err := p.readWorkPage(ctx, session, task)
	if err != nil {
		return learning.WorkSubmission{}, err
	}
	if page.Sheet.Submitted {
		return learning.WorkSubmission{AlreadySubmitted: true}, nil
	}
	if page.Sheet.Fingerprint != fingerprint {
		return learning.WorkSubmission{}, learning.Fail(learning.FailurePermanent, "试卷或作答机会已变化，未提交")
	}
	form, err := buildWorkSubmissionForm(page, answers)
	if err != nil {
		return learning.WorkSubmission{}, err
	}
	submission, err := p.postWorkSubmission(ctx, session, page, form, beforePost)
	if err != nil {
		return learning.WorkSubmission{}, err
	}
	return submission, nil
}

func (p *Platform) readWorkPage(ctx context.Context, session Session, task learning.PlatformTaskRef) (*workPage, error) {
	job, err := p.readWorkJob(ctx, session, task)
	if err != nil {
		return nil, err
	}
	pageURL, err := p.workReadURL(job)
	if err != nil {
		return nil, err
	}
	payload, err := p.do(ctx, session, http.MethodGet, p.options.endpoints.Work, workReadQuery(job), "work_read")
	if err != nil {
		return nil, err
	}
	page, err := parseWorkPage(payload, pageURL, job)
	if err != nil {
		return nil, err
	}
	if page.Sheet.Submitted {
		return page, nil
	}
	if err := p.loadWorkImages(ctx, session, pageURL, page); err != nil {
		return nil, err
	}
	page.Sheet.Fingerprint = workFingerprint(page.Job, page.Form, page.Questions)
	return page, nil
}

func (p *Platform) readWorkJob(ctx context.Context, session Session, task learning.PlatformTaskRef) (workJob, error) {
	if task.CardIndex < 0 || task.CardIndex >= maxChapterCards {
		return workJob{}, learning.Fail(learning.FailureInvalidTask, "测验任务卡片页码无效")
	}
	for _, value := range []string{task.Course.ExternalCourseID, task.Course.ExternalClassID, task.Course.CPI,
		task.ChapterExternalID, task.ObjectID, task.TaskPointID} {
		if !safeWorkToken(value) {
			return workJob{}, learning.Fail(learning.FailureInvalidTask, "测验任务缺少有效标识")
		}
	}
	// 章节测验所在卡片可能不在 num=0 页面，必须按任务记录的实际卡片页码读取。
	payload, err := p.do(ctx, session, http.MethodGet,
		p.cardsURLAtIndex(task.Course, task.ChapterExternalID, task.CardIndex), nil, "course_cards")
	if err != nil {
		return workJob{}, err
	}
	config, err := parseCards(payload)
	if err != nil {
		return workJob{}, learning.Fail(learning.FailureCapabilityMissing, "测验任务卡片结构未识别")
	}
	defaults := mapValue(config["defaults"])
	rawAttachments, ok := config["attachments"].([]any)
	if !ok {
		return workJob{}, learning.Fail(learning.FailureCapabilityMissing, "测验任务卡片缺少附件列表")
	}
	var matched map[string]any
	for _, raw := range rawAttachments {
		item := mapValue(raw)
		if text(item["jobid"]) != task.TaskPointID {
			continue
		}
		if normalizeAttachmentKind(text(item["type"])) != "quiz" {
			continue
		}
		objectID := attachmentObjectID(item, "quiz", text(item["jobid"]))
		if objectID != task.ObjectID {
			continue
		}
		if matched != nil {
			return workJob{}, learning.Fail(learning.FailureInvalidTask, "当前章节存在重复测验任务，已停止提交")
		}
		matched = item
	}
	if matched == nil {
		return workJob{}, learning.Fail(learning.FailureInvalidTask, "当前章节卡片中未找到对应测验任务")
	}
	property := mapValue(matched["property"])
	jobID := firstText(matched["jobid"])
	workID := strings.TrimPrefix(jobID, "work-")
	if workID == jobID {
		workID = firstText(property["workid"], property["workId"], matched["workid"], matched["workId"])
	}
	knowledgeID := firstText(defaults["knowledgeid"], property["knowledgeid"])
	if knowledgeID == "" {
		return workJob{}, learning.Fail(learning.FailureInvalidTask, "测验任务缺少章节上下文")
	}
	if knowledgeID != task.ChapterExternalID {
		return workJob{}, learning.Fail(learning.FailureInvalidTask, "测验章节上下文不一致")
	}
	cpi := firstText(defaults["cpi"], property["cpi"])
	if cpi == "" {
		cpi = task.Course.CPI
	}
	if cpi != task.Course.CPI {
		return workJob{}, learning.Fail(learning.FailureInvalidTask, "测验课程上下文不一致")
	}
	for _, pair := range [][2]string{
		{text(defaults["courseid"]), task.Course.ExternalCourseID},
		{text(defaults["clazzId"]), task.Course.ExternalClassID},
	} {
		if pair[0] != "" && pair[0] != pair[1] {
			return workJob{}, learning.Fail(learning.FailureInvalidTask, "测验课程或班级上下文不一致")
		}
	}
	ktoken := firstText(defaults["ktoken"], matched["ktoken"])
	if ktoken == "" {
		return workJob{}, learning.Fail(learning.FailureInvalidTask, "测验任务缺少 ktoken")
	}
	enc := firstText(matched["enc"], property["enc"])
	for _, value := range []string{workID, jobID, knowledgeID, cpi, ktoken} {
		if !safeWorkToken(value) {
			return workJob{}, learning.Fail(learning.FailureInvalidTask, "测验任务上下文无效")
		}
	}
	if enc != "" && !safeWorkToken(enc) {
		return workJob{}, learning.Fail(learning.FailureInvalidTask, "测验签名上下文无效")
	}
	return workJob{CourseID: task.Course.ExternalCourseID, ClassID: task.Course.ExternalClassID,
		CPI: cpi, KnowledgeID: knowledgeID, ObjectID: task.ObjectID, JobID: jobID,
		WorkID: workID, KToken: ktoken, Enc: enc}, nil
}

func (p *Platform) workReadURL(job workJob) (string, error) {
	parsed, err := url.Parse(p.options.endpoints.Work)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", learning.Fail(learning.FailureCapabilityMissing, "测验入口未配置有效 HTTPS 地址")
	}
	parsed.RawQuery = mergeQuery(parsed.Query(), workReadQuery(job)).Encode()
	return parsed.String(), nil
}

func workReadQuery(job workJob) url.Values {
	return url.Values{
		"api":          {"1"},
		"workId":       {job.WorkID},
		"jobid":        {job.JobID},
		"originJobId":  {job.JobID},
		"needRedirect": {"true"},
		"skipHeader":   {"true"},
		"knowledgeid":  {job.KnowledgeID},
		"ktoken":       {job.KToken},
		"cpi":          {job.CPI},
		"ut":           {"s"},
		"clazzId":      {job.ClassID},
		"type":         {""},
		"enc":          {job.Enc},
		"mooc2":        {"1"},
		"courseid":     {job.CourseID},
	}
}

func parseWorkPage(payload []byte, baseURL string, job workJob) (*workPage, error) {
	root := parseHTMLDocument(payload)
	visible := htmlNodeText(root)
	// 验证控件与错误通知在题目区域之外，必须优先于题目和表单判定识别。
	if failure := workControlFailure(root); failure != nil {
		return nil, failure
	}
	questionNodes := collectQuestionNodes(root)
	form, formValues, err := extractWorkForm(root)
	if err != nil {
		return nil, err
	}
	// 平台把可作答题目放在表单内；表单外出现同名容器时只取表单内的真实题目。
	if form != nil {
		if scoped := collectQuestionNodes(form); len(scoped) > 0 {
			questionNodes = scoped
		}
	}
	if len(questionNodes) == 0 {
		if already, failure := classifyWorkPageMessage(visible, payload); failure != nil {
			return nil, failure
		} else if already {
			return &workPage{Sheet: learning.WorkSheet{Fingerprint: workFingerprint(job, nil, nil), Submitted: true},
				Job: job}, nil
		}
	}
	controls := form != nil && hasWorkEditableControls(form)
	fields := form != nil && hasWorkSubmitFields(formValues)
	if !controls || !fields {
		if !controls && workPageAppearsSubmitted(visible) {
			return &workPage{Sheet: learning.WorkSheet{Fingerprint: workFingerprint(job, nil, nil), Submitted: true},
				Form: formValues, Job: job}, nil
		}
		if controls && !fields {
			return nil, learning.Fail(learning.FailureCapabilityMissing, "测验表单缺少必要提交字段")
		}
		if len(questionNodes) == 0 {
			return nil, learning.Fail(learning.FailureCapabilityMissing, "测验页面没有可作答题目")
		}
		return nil, learning.Fail(learning.FailureCapabilityMissing, "测验页面不是可作答表单")
	}
	if workPageHasEncryptedFont(payload) {
		return nil, learning.Fail(learning.FailureCapabilityMissing, "测验题目使用字体加密，无法可信解码")
	}
	if len(questionNodes) == 0 {
		return nil, learning.Fail(learning.FailureCapabilityMissing, "测验页面没有可作答题目")
	}
	if len(questionNodes) > maxWorkQuestions {
		return nil, learning.Fail(learning.FailureCapabilityMissing, "测验题目数量超出限制")
	}
	if err := verifyWorkForm(formValues, job); err != nil {
		return nil, err
	}
	questions := make([]workQuestion, 0, len(questionNodes))
	seen := map[string]bool{}
	disabled := workDisabledContainers(root)
	for _, node := range questionNodes {
		question, err := parseWorkQuestion(node, form, baseURL, disabled)
		if err != nil {
			return nil, err
		}
		if seen[question.ID] {
			return nil, learning.Fail(learning.FailureCapabilityMissing, "测验题目编号重复")
		}
		seen[question.ID] = true
		questions = append(questions, question)
	}
	page := &workPage{Job: job, Form: formValues, Questions: questions}
	page.Sheet = learning.WorkSheet{Questions: workPublicQuestions(questions)}
	return page, nil
}

func parseWorkQuestion(node *htmlNode, form *htmlNode, baseURL string, disabled map[*htmlNode]bool) (workQuestion, error) {
	id, err := parseQuestionID(node)
	if err != nil {
		return workQuestion{}, err
	}
	kind, err := parseQuestionType(node)
	if err != nil {
		return workQuestion{}, err
	}
	title, err := parseQuestionTitle(node)
	if err != nil {
		return workQuestion{}, err
	}
	options, err := parseWorkOptions(node, id, kind.Kind)
	if err != nil {
		return workQuestion{}, err
	}
	blanks, err := parseWorkBlanks(node, form, id, kind.Kind)
	if err != nil {
		return workQuestion{}, err
	}
	sources := collectQuestionImageSources(node)
	if len(sources) > maxWorkImagesPerQuestion {
		return workQuestion{}, learning.Fail(learning.FailureCapabilityMissing, "测验题目图片数量超出限制")
	}
	if containsPrivateUse(title) {
		return workQuestion{}, learning.Fail(learning.FailureCapabilityMissing, "测验题干包含未解码字体字符")
	}
	for _, option := range options {
		if containsPrivateUse(option.Label) || containsPrivateUse(option.Text) {
			return workQuestion{}, learning.Fail(learning.FailureCapabilityMissing, "测验选项包含未解码字体字符")
		}
	}
	refs := make([]workImageRef, 0, len(sources))
	for _, source := range sources {
		ref, err := resolveWorkImageReference(source, baseURL)
		if err != nil {
			return workQuestion{}, err
		}
		if ref.ID == "" {
			continue
		}
		refs = append(refs, ref)
	}
	answerField := ""
	if kind.Kind == "shortanswer" {
		// 主观题只写入页面已有的文字字段，不伪造 answer{id} 之类的字段名。
		field, err := parseWorkAnswerField(node, id, disabled)
		if err != nil {
			return workQuestion{}, err
		}
		answerField = field
	}
	return workQuestion{WorkQuestion: learning.WorkQuestion{ID: id, Type: kind.Kind, Title: title,
		Options: options, Blanks: blanks.Count}, TypeCode: kind.Code, BlankStyle: blanks.Style,
		BlankFields: append([]string(nil), blanks.Fields...), AnswerField: answerField, ImageRefs: refs}, nil
}

func workPublicQuestions(questions []workQuestion) []learning.WorkQuestion {
	result := make([]learning.WorkQuestion, 0, len(questions))
	for _, question := range questions {
		copy := question.WorkQuestion
		copy.Options = append([]learning.WorkOption(nil), question.Options...)
		copy.Images = append([]learning.WorkImage(nil), question.Images...)
		result = append(result, copy)
	}
	return result
}

func hasWorkSubmitFields(values url.Values) bool {
	if values == nil {
		return false
	}
	required := []string{"workAnswerId", "totalQuestionNum", "fullScore", "knowledgeid", "jobid", "workRelationId", "enc_work"}
	for _, name := range required {
		if firstWorkValue(values, name) == "" {
			return false
		}
	}
	return firstWorkValue(values, "courseId", "courseid") != "" &&
		firstWorkValue(values, "classId", "classid") != "" &&
		firstWorkValue(values, "oldWorkId", "workId", "workid") != ""
}

func verifyWorkForm(values url.Values, job workJob) error {
	if !hasWorkSubmitFields(values) {
		return learning.Fail(learning.FailureCapabilityMissing, "测验表单缺少必要提交字段")
	}
	if knowledge := firstWorkValue(values, "knowledgeid", "knowledgeId"); knowledge != job.KnowledgeID {
		return learning.Fail(learning.FailureInvalidTask, "测验表单章节上下文不一致")
	}
	if formJob := firstWorkValue(values, "jobid"); formJob != job.JobID {
		return learning.Fail(learning.FailureInvalidTask, "测验表单任务上下文不一致")
	}
	if course := firstWorkValue(values, "courseId", "courseid"); course != job.CourseID {
		return learning.Fail(learning.FailureInvalidTask, "测验表单课程上下文不一致")
	}
	if class := firstWorkValue(values, "classId", "classid"); class != job.ClassID {
		return learning.Fail(learning.FailureInvalidTask, "测验表单班级上下文不一致")
	}
	formWorkID := false
	for _, name := range []string{"oldWorkId", "workId", "workid"} {
		for _, value := range values[name] {
			if strings.TrimSpace(value) == job.WorkID {
				formWorkID = true
			}
		}
	}
	if !formWorkID {
		return learning.Fail(learning.FailureInvalidTask, "测验表单试卷上下文不一致")
	}
	return nil
}

func workFingerprint(job workJob, form url.Values, questions []workQuestion) string {
	type fingerprintOption struct {
		Label string `json:"label"`
		Value string `json:"value"`
		Text  string `json:"text"`
	}
	type fingerprintQuestion struct {
		ID          string              `json:"id"`
		TypeCode    string              `json:"type_code"`
		Title       string              `json:"title"`
		Options     []fingerprintOption `json:"options"`
		Blanks      int                 `json:"blanks"`
		BlankFields []string            `json:"blank_fields"`
		AnswerField string              `json:"answer_field"`
		Images      []string            `json:"images"`
	}
	document := struct {
		Version         int                   `json:"version"`
		WorkID          string                `json:"work_id"`
		JobID           string                `json:"job_id"`
		WorkAnswerID    string                `json:"work_answer_id"`
		WorkRelationID  string                `json:"work_relation_id"`
		TotalQuestionNo string                `json:"total_question_num"`
		FullScore       string                `json:"full_score"`
		Questions       []fingerprintQuestion `json:"questions"`
	}{Version: 3, WorkID: job.WorkID, JobID: job.JobID}
	if form != nil {
		document.WorkAnswerID = firstWorkValue(form, "workAnswerId")
		document.WorkRelationID = firstWorkValue(form, "workRelationId")
		document.TotalQuestionNo = firstWorkValue(form, "totalQuestionNum")
		document.FullScore = firstWorkValue(form, "fullScore")
	}
	for _, question := range questions {
		item := fingerprintQuestion{ID: question.ID, TypeCode: question.TypeCode,
			Title: question.Title, Blanks: question.Blanks,
			BlankFields: append([]string(nil), question.BlankFields...),
			AnswerField: question.AnswerField}
		for _, option := range question.Options {
			item.Options = append(item.Options, fingerprintOption{Label: option.Label,
				Value: option.Value, Text: option.Text})
		}
		for _, hash := range question.ImageHashes {
			item.Images = append(item.Images, hash)
		}
		document.Questions = append(document.Questions, item)
	}
	encoded, _ := json.Marshal(document)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func buildWorkSubmissionForm(page *workPage, answers []learning.WorkAnswer) (url.Values, error) {
	if page == nil || len(page.Questions) == 0 {
		return nil, learning.Fail(learning.FailurePermanent, "当前试卷没有可提交题目")
	}
	if len(answers) != len(page.Questions) {
		return nil, learning.Fail(learning.FailurePermanent, "答案数量与当前试卷不一致")
	}
	byID := make(map[string]learning.WorkAnswer, len(answers))
	for _, answer := range answers {
		if strings.TrimSpace(answer.QuestionID) == "" {
			return nil, learning.Fail(learning.FailurePermanent, "答案缺少题目编号")
		}
		if _, exists := byID[answer.QuestionID]; exists {
			return nil, learning.Fail(learning.FailurePermanent, "答案包含重复题目")
		}
		byID[answer.QuestionID] = answer
	}
	form := cloneWorkValues(page.Form)
	for name := range form {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "answer") || lower == "pyflag" {
			form.Del(name)
		}
	}
	form.Set("pyFlag", "")
	questionIDs := make([]string, 0, len(page.Questions))
	for _, question := range page.Questions {
		answer, exists := byID[question.ID]
		if !exists {
			return nil, learning.Fail(learning.FailurePermanent, "答案缺少当前试卷题目")
		}
		values, err := validatedWorkValues(question, answer.Values)
		if err != nil {
			return nil, err
		}
		questionIDs = append(questionIDs, question.ID)
		form.Set("answertype"+question.ID, question.TypeCode)
		switch question.Type {
		case "single", "judgement":
			form.Set("answer"+question.ID, values[0])
		case "shortanswer":
			if question.AnswerField == "" {
				return nil, learning.Fail(learning.FailurePermanent, "简答题缺少当前页面的作答字段")
			}
			form.Set(question.AnswerField, values[0])
		case "multiple":
			for _, value := range values {
				form.Add("answercheck"+question.ID, value)
			}
			form.Set("answer"+question.ID, strings.Join(values, ""))
		case "completion":
			// 只写入当前页面真实存在的空位字段，不构造页面没有的字段名。
			if len(question.BlankFields) != len(values) {
				return nil, learning.Fail(learning.FailurePermanent, "填空答案数量与当前试卷不一致")
			}
			seenFields := map[string]bool{}
			for index, field := range question.BlankFields {
				if field == "" {
					return nil, learning.Fail(learning.FailurePermanent, "填空答案缺少当前页面的字段")
				}
				// 有些页面多个空位共用同一个未编号字段名，此时按空位顺序重复提交。
				if seenFields[field] {
					form.Add(field, values[index])
				} else {
					form.Set(field, values[index])
					seenFields[field] = true
				}
			}
		default:
			return nil, learning.Fail(learning.FailurePermanent, "答案题型不在支持范围")
		}
	}
	form.Set("answerwqbid", strings.Join(questionIDs, ",")+",")
	return form, nil
}

func validatedWorkValues(question workQuestion, values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, learning.Fail(learning.FailurePermanent, "答案内容为空")
	}
	if question.Type == "shortanswer" {
		if len(values) != 1 {
			return nil, learning.Fail(learning.FailurePermanent, "简答题只能提交一份原文")
		}
		value := strings.TrimSpace(values[0])
		if value == "" || len(value) > maxWorkShortAnswer {
			return nil, learning.Fail(learning.FailurePermanent, "简答题答案为空或超出限制")
		}
		for _, r := range value {
			if r == 0 {
				return nil, learning.Fail(learning.FailurePermanent, "答案包含无效字符")
			}
		}
		return []string{value}, nil
	}
	cleaned := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > maxWorkValueLen {
			return nil, learning.Fail(learning.FailurePermanent, "答案内容为空或超出限制")
		}
		for _, r := range value {
			if r == 0 || r == '\r' || r == '\n' {
				return nil, learning.Fail(learning.FailurePermanent, "答案包含无效字符")
			}
		}
		if question.Type != "completion" && seen[value] {
			return nil, learning.Fail(learning.FailurePermanent, "答案包含重复选项")
		}
		seen[value] = true
		cleaned = append(cleaned, value)
	}
	switch question.Type {
	case "single", "judgement":
		if len(cleaned) != 1 {
			return nil, learning.Fail(learning.FailurePermanent, "单选题或判断题只能提交一个选项")
		}
		if !workOptionValueExists(question.Options, cleaned[0]) {
			return nil, learning.Fail(learning.FailurePermanent, "答案选项不在当前试卷中")
		}
		return cleaned, nil
	case "multiple":
		if len(cleaned) > len(question.Options) {
			return nil, learning.Fail(learning.FailurePermanent, "多选答案数量超过当前选项")
		}
		ordered := make([]string, 0, len(cleaned))
		for _, option := range question.Options {
			if seen[option.Value] {
				ordered = append(ordered, option.Value)
			}
		}
		if len(ordered) != len(cleaned) {
			return nil, learning.Fail(learning.FailurePermanent, "答案选项不在当前试卷中")
		}
		for _, value := range ordered {
			if len(value) != 1 || !(value[0] >= 'A' && value[0] <= 'Z' || value[0] >= 'a' && value[0] <= 'z') {
				return nil, learning.Fail(learning.FailurePermanent, "多选选项提交值格式未确认")
			}
		}
		return ordered, nil
	case "completion":
		if len(cleaned) != question.Blanks {
			return nil, learning.Fail(learning.FailurePermanent, "填空答案数量与当前试卷不一致")
		}
		return cleaned, nil
	default:
		return nil, learning.Fail(learning.FailurePermanent, "答案题型不在支持范围")
	}
}

func workOptionValueExists(options []learning.WorkOption, value string) bool {
	for _, option := range options {
		if option.Value == value {
			return true
		}
	}
	return false
}

func cloneWorkValues(values url.Values) url.Values {
	result := url.Values{}
	for key, items := range values {
		result[key] = append([]string(nil), items...)
	}
	return result
}

func (p *Platform) loadWorkImages(ctx context.Context, session Session, pageURL string, page *workPage) error {
	urlCache := map[string]learning.WorkImage{}
	contentCache := map[string]learning.WorkImage{}
	total := 0
	totalRaw := 0
	for index := range page.Questions {
		question := &page.Questions[index]
		images := make([]learning.WorkImage, 0, len(question.ImageRefs))
		hashes := make([]string, 0, len(question.ImageRefs))
		for _, ref := range question.ImageRefs {
			image, cached := urlCache[ref.URL]
			if !cached {
				fetched, err := p.fetchWorkImage(ctx, session, ref, pageURL)
				if err != nil {
					return err
				}
				image = fetched
			}
			hash := workImageContentHash(image)
			if hash == "" {
				return learning.Fail(learning.FailureCapabilityMissing, "测验图片内容无法计算指纹")
			}
			if existing, exists := contentCache[hash]; exists {
				image = existing
			} else {
				if total >= maxWorkImagesPerSheet {
					return learning.Fail(learning.FailureCapabilityMissing, "测验图片总数量超出限制")
				}
				rawSize := workImageRawSize(image)
				if workImageBudgetExceeded(totalRaw, rawSize) {
					return learning.Fail(learning.FailureCapabilityMissing, "测验图片累计体积超出限制")
				}
				totalRaw += rawSize
				total++
				image.ID = hash
				contentCache[hash] = image
			}
			urlCache[ref.URL] = image
			images = append(images, image)
			hashes = append(hashes, hash)
		}
		question.Images = images
		question.ImageHashes = hashes
		page.Sheet.Questions[index].Images = append([]learning.WorkImage(nil), images...)
	}
	return nil
}

func (p *Platform) postWorkSubmission(ctx context.Context, session Session, page *workPage, form url.Values, beforePost func(context.Context) error) (learning.WorkSubmission, error) {
	endpoint, err := url.Parse(p.options.endpoints.WorkSubmit)
	if err != nil || !p.factory.allowedURL(endpoint) || endpoint.Fragment != "" {
		return learning.WorkSubmission{}, learning.Fail(learning.FailureCapabilityMissing, "测验提交入口不在允许范围")
	}
	client, cleanup, err := p.requestClient(ctx, session)
	if err != nil {
		return learning.WorkSubmission{}, err
	}
	defer cleanup()
	// 最终提交不跟随重定向，避免 POST 被改写成 GET 或转向未确认地址。
	// 该 client 只在本次任务的上下文内使用，提交之后不再发出请求。
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if err := p.factory.limiter.wait(ctx, p.options.MinRequestInterval); err != nil {
		return learning.WorkSubmission{}, learning.Fail(learning.FailureStopped, "测验提交等待被停止")
	}
	if err := ctx.Err(); err != nil {
		return learning.WorkSubmission{}, learning.Fail(learning.FailureStopped, "测验提交已停止")
	}
	body := strings.NewReader(form.Encode())
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), body)
	if err != nil {
		return learning.WorkSubmission{}, learning.Fail(learning.FailurePermanent, "无法建立测验提交请求")
	}
	request.Header.Set("User-Agent", session.UserAgent)
	request.Header.Set("Accept", "application/json, text/javascript, */*; q=0.01")
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	request.Header.Set("X-Requested-With", "XMLHttpRequest")
	request.Header.Set("Origin", "https://"+endpoint.Hostname())
	if pageURL, err := p.workReadURL(page.Job); err == nil {
		request.Header.Set("Referer", pageURL)
	} else {
		request.Header.Set("Referer", "https://"+endpoint.Hostname()+"/")
	}
	// 重新取题、图片指纹核对和表单构建均已完成，提交请求也已就绪。
	// 这里只做服务端租约/闭户/暂停状态检查，不是人工确认；失败时绝不发送 POST。
	if beforePost != nil {
		if err := beforePost(ctx); err != nil {
			return learning.WorkSubmission{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return learning.WorkSubmission{}, learning.Fail(learning.FailureStopped, "测验提交已停止")
	}
	response, err := client.Do(request)
	if err != nil {
		// POST 已可能到达平台，无法确认是否已受理，禁止自动重试。
		return learning.WorkSubmission{}, learning.Fail(learning.FailureSubmissionUnknown, "测验提交结果未知，请稍后独立核验")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusMovedPermanently || response.StatusCode == http.StatusFound ||
		response.StatusCode == http.StatusSeeOther || response.StatusCode == http.StatusTemporaryRedirect ||
		response.StatusCode == http.StatusPermanentRedirect {
		return learning.WorkSubmission{}, learning.Fail(learning.FailureSubmissionUnknown, "测验提交返回未知重定向，请独立核验")
	}
	switch response.StatusCode {
	case http.StatusUnauthorized:
		return learning.WorkSubmission{}, learning.Fail(learning.FailureIdentityInvalid, "平台身份已失效，未确认提交成功")
	case http.StatusForbidden:
		return learning.WorkSubmission{}, learning.Fail(learning.FailureForbidden, "平台拒绝测验提交，已停止")
	case http.StatusTooManyRequests:
		return learning.WorkSubmission{}, &learning.Failure{Class: learning.FailureRateLimited,
			RetryAfter: 30 * time.Second, Detail: "平台限流，已停止测验提交"}
	}
	if response.StatusCode != http.StatusOK {
		return learning.WorkSubmission{}, learning.Fail(learning.FailureSubmissionUnknown, "测验提交返回未知状态，请独立核验")
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, p.options.MaxResponseBytes+1))
	if err != nil || int64(len(payload)) > p.options.MaxResponseBytes {
		return learning.WorkSubmission{}, learning.Fail(learning.FailureSubmissionUnknown, "测验提交响应不可读，请独立核验")
	}
	var result map[string]any
	if json.Unmarshal(payload, &result) != nil {
		if failure := workFailureForMessage(htmlNodeText(parseHTMLDocument(payload))); failure != nil {
			return learning.WorkSubmission{}, failure
		}
		return learning.WorkSubmission{}, learning.Fail(learning.FailureSubmissionUnknown, "测验提交响应结构未知，请独立核验")
	}
	status, ok := result["status"].(bool)
	if !ok {
		return learning.WorkSubmission{}, learning.Fail(learning.FailureSubmissionUnknown, "测验提交响应缺少状态，请独立核验")
	}
	message := firstText(result["msg"], result["message"], result["msg2"], result["error"])
	if failure := workFailureForFields(result); failure != nil {
		return learning.WorkSubmission{}, failure
	}
	if failure := workFailureForCode(result["code"], result["status"]); failure != nil {
		return learning.WorkSubmission{}, failure
	}
	if status {
		return learning.WorkSubmission{Accepted: true}, nil
	}
	if workAlreadySubmittedMessage(message) {
		return learning.WorkSubmission{AlreadySubmitted: true}, nil
	}
	if failure := workFailureForMessage(message); failure != nil {
		return learning.WorkSubmission{}, failure
	}
	return learning.WorkSubmission{}, learning.Fail(learning.FailurePermanent, "平台拒绝本次测验提交")
}

func classifyWorkPageMessage(visible string, payload []byte) (bool, *learning.Failure) {
	var document map[string]any
	if json.Unmarshal(payload, &document) == nil {
		message := firstText(document["msg"], document["message"], document["msg2"], document["error"])
		if workAlreadySubmittedMessage(message) {
			return true, nil
		}
		if failure := workFailureForMessage(message); failure != nil {
			return false, failure
		}
		if failure := workFailureForFields(document); failure != nil {
			return false, failure
		}
		if failure := workFailureForCode(document["code"], document["status"]); failure != nil {
			return false, failure
		}
		if status, ok := document["status"].(bool); ok && !status {
			return false, learning.Fail(learning.FailurePermanent, "平台拒绝加载测验页面")
		}
	}
	if failure := workFailureForMessage(visible); failure != nil {
		return false, failure
	}
	return false, nil
}

func workFailureForFields(document map[string]any) *learning.Failure {
	for _, key := range []string{"captcha", "needCaptcha", "needVerify", "verification_required"} {
		if required, ok := document[key].(bool); ok && required {
			return learning.Fail(learning.FailureVerificationNeeded, "平台要求验证，已停止测验操作")
		}
	}
	return nil
}

func workFailureForCode(values ...any) *learning.Failure {
	for _, value := range values {
		switch typed := value.(type) {
		case float64:
			switch int(typed) {
			case http.StatusUnauthorized:
				return learning.Fail(learning.FailureIdentityInvalid, "平台身份已失效，需要重新验证")
			case http.StatusForbidden:
				return learning.Fail(learning.FailureForbidden, "平台拒绝访问测验")
			case http.StatusTooManyRequests:
				return &learning.Failure{Class: learning.FailureRateLimited, RetryAfter: 30 * time.Second,
					Detail: "平台限流，已停止测验操作"}
			}
		case string:
			if code, err := strconv.Atoi(typed); err == nil {
				return workFailureForCode(float64(code))
			}
		}
	}
	return nil
}

func workFailureForMessage(message string) *learning.Failure {
	message = strings.ToLower(cleanWorkText(message))
	if message == "" {
		return nil
	}
	switch {
	case strings.Contains(message, "验证码") || strings.Contains(message, "安全验证") ||
		strings.Contains(message, "请完成验证") || strings.Contains(message, "captcha") ||
		strings.Contains(message, "请完成人脸识别") || strings.Contains(message, "人脸识别验证"):
		return learning.Fail(learning.FailureVerificationNeeded, "平台要求验证，已停止测验操作")
	case strings.Contains(message, "频繁") || strings.Contains(message, "too many requests") ||
		strings.Contains(message, "rate limit"):
		return &learning.Failure{Class: learning.FailureRateLimited, RetryAfter: 30 * time.Second,
			Detail: "平台限流，已停止测验操作"}
	case strings.Contains(message, "请重新登录") || strings.Contains(message, "登录已失效") ||
		strings.Contains(message, "登录失效") || strings.Contains(message, "请登录") ||
		strings.Contains(message, "未登录") || strings.Contains(message, "fanyalogin") ||
		strings.Contains(message, "passport2.chaoxing.com/login") || strings.Contains(message, "账号异常") ||
		strings.Contains(message, "账号已被冻结"):
		return learning.Fail(learning.FailureIdentityInvalid, "平台身份已失效，需要重新验证")
	case strings.Contains(message, "无效的权限") || strings.Contains(message, "无权限") ||
		strings.Contains(message, "没有权限") || strings.Contains(message, "禁止访问") ||
		strings.Contains(message, "已被老师删除") || strings.Contains(message, "作业不存在") ||
		strings.Contains(message, "章节未开放") || strings.Contains(message, "该章节未开放") ||
		strings.Contains(message, "风控"):
		return learning.Fail(learning.FailureForbidden, "平台拒绝访问该测验")
	case strings.Contains(message, "教师未创建完成") || strings.Contains(message, "作业未创建完成") ||
		strings.Contains(message, "未创建完成"):
		return learning.Fail(learning.FailureCapabilityMissing, "测验尚未创建完成")
	}
	return nil
}

func workAlreadySubmittedMessage(message string) bool {
	message = strings.ToLower(cleanWorkText(message))
	return strings.Contains(message, "已提交") || strings.Contains(message, "已作答") ||
		strings.Contains(message, "重复提交") || strings.Contains(message, "无需重复") ||
		strings.Contains(message, "已经完成")
}

func workPageAppearsSubmitted(visible string) bool {
	visible = cleanWorkText(visible)
	for _, marker := range []string{"已批阅", "本次成绩", "我的答案", "正确答案", "重做", "作业详情",
		"无需重复提交", "已提交过"} {
		if strings.Contains(visible, marker) {
			return true
		}
	}
	return strings.Contains(visible, "已完成") && !strings.Contains(visible, "未完成")
}
