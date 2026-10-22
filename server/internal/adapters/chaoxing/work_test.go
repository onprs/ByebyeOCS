package chaoxing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/learning"
)

const (
	workCourseID    = "101"
	workClassID     = "202"
	workCPI         = "303"
	workChapterID   = "5001"
	workJobSuffix   = "62df20e345ff464d9bbe4ea021025cc5"
	workJobID       = "work-" + workJobSuffix
	workObjectID    = workJobID
	workAnswerID    = "90001"
	workPaperHash   = "cd8c29480d9c16f5a675e8c2c459245c"
	workRelationID  = "11020928"
	workSubmitToken = "5219a574b7571639ea6b9770bbc23da2"
)

type workFixture struct {
	t              *testing.T
	server         *httptest.Server
	mu             sync.Mutex
	pageHTML       string
	getStatus      int
	submitStatus   int
	submitLocation string
	submitBody     string
	submitDelay    time.Duration
	clientTimeout  time.Duration
	postCount      int
	workGetCount   int
	cardGetCount   int
	cardNum        string
	quizCookie     string
	submitCookie   string
	rejectedSubmit int
	rejectedImage  int
	requireScope   bool
	scopeCookie    string
	scopeIssued    int
	rejectedScope  int
	imageGetCount  int
	lastForm       url.Values
	image1         []byte
	image2         []byte
}

func newWorkFixture(t *testing.T) *workFixture {
	t.Helper()
	fixture := &workFixture{t: t, pageHTML: workPageHTML(), submitBody: `{"status":true,"msg":"success!"}`,
		clientTimeout: 2 * time.Second}
	fixture.image1 = workTestPNG(t, 2, 2)
	fixture.image2 = workTestPNG(t, 3, 2)
	fixture.server = httptest.NewTLSServer(http.HandlerFunc(fixture.handle))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func (f *workFixture) handle(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/cards":
		f.mu.Lock()
		f.cardGetCount++
		f.cardNum = r.URL.Query().Get("num")
		f.mu.Unlock()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, `var mArg = {"attachments":[
			{"jobid":"`+workJobID+`","type":"workid","isPassed":false,"enc":"cardenc","property":{"workid":"ca62b882b279427b9d24876daba4e2ba","name":"章节测验"}}
		],"defaults":{"ktoken":"ktoken1","knowledgeid":"`+workChapterID+`","cpi":"`+workCPI+`"}};`)
	case "/work":
		f.mu.Lock()
		f.workGetCount++
		status := f.getStatus
		csrf := f.workGetCount
		f.quizCookie = strconv.Itoa(csrf)
		cookie := f.quizCookie
		scope := ""
		if f.requireScope && f.scopeCookie == "" {
			// 会话 Cookie 只在第一次读取时下发，之后不再重发：只有共享 Cookie 容器才能带到提交。
			f.scopeCookie = "scope-" + strconv.Itoa(f.workGetCount)
			f.scopeIssued = f.workGetCount
			scope = f.scopeCookie
		}
		page := strings.ReplaceAll(f.pageHTML, "__CSRF__", strconv.Itoa(csrf))
		f.mu.Unlock()
		// 平台在读取试卷时轮换会话 Cookie，后续图片与最终提交都必须携带新值。
		http.SetCookie(w, &http.Cookie{Name: "quiz", Value: cookie, Path: "/"})
		if scope != "" {
			http.SetCookie(w, &http.Cookie{Name: "scope", Value: scope, Path: "/"})
		}
		if status != 0 {
			http.Error(w, "platform error", status)
			return
		}
		if r.URL.Query().Get("workId") != workJobSuffix || r.URL.Query().Get("jobid") != workJobID ||
			r.URL.Query().Get("knowledgeid") != workChapterID || r.URL.Query().Get("ktoken") != "ktoken1" ||
			r.URL.Query().Get("cpi") != workCPI || r.URL.Query().Get("clazzId") != workClassID ||
			r.URL.Query().Get("courseid") != workCourseID || r.URL.Query().Get("enc") != "cardenc" {
			f.t.Errorf("测验读取参数错误: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, page)
	case "/submit":
		if err := r.ParseForm(); err != nil {
			f.t.Errorf("提交表单无法解析: %v", err)
		}
		cookie, _ := r.Cookie("quiz")
		f.mu.Lock()
		f.postCount++
		f.lastForm = cloneWorkValues(r.PostForm)
		status := f.submitStatus
		body := f.submitBody
		delay := f.submitDelay
		location := f.submitLocation
		f.submitCookie = ""
		if cookie != nil {
			f.submitCookie = cookie.Value
		}
		rejected := f.submitCookie != f.quizCookie
		current := f.quizCookie
		if rejected {
			f.rejectedSubmit++
		}
		scopeCookie, _ := r.Cookie("scope")
		if f.requireScope && (scopeCookie == nil || scopeCookie.Value != f.scopeCookie) {
			f.rejectedScope++
			rejected = true
		}
		f.mu.Unlock()
		if rejected {
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, "missing rotated cookie "+current)
			return
		}
		if delay > 0 {
			time.Sleep(delay)
		}
		if status != 0 {
			if location != "" {
				w.Header().Set("Location", location)
			}
			w.WriteHeader(status)
			io.WriteString(w, "submit error")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	case "/img/1.png", "/img/2.png", "/img/large.png":
		cookie, _ := r.Cookie("quiz")
		f.mu.Lock()
		if cookie == nil || cookie.Value != f.quizCookie {
			f.rejectedImage++
			f.mu.Unlock()
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, "missing rotated cookie")
			return
		}
		f.imageGetCount++
		var image []byte
		switch r.URL.Path {
		case "/img/1.png":
			image = f.image1
		case "/img/2.png":
			image = f.image2
		default:
			image = bytes.Repeat([]byte{0x89}, maxWorkImageBytes+1)
		}
		f.mu.Unlock()
		w.Header().Set("Content-Type", "image/png")
		w.Write(image)
	default:
		http.NotFound(w, r)
	}
}

func workPageHTML() string {
	return `<html><head><title>章节测验</title></head><body>
<h1>章节测验</h1>
<form id="form1" action="/mooc-ans/work/addStudentWorkNew" method="post">
<input type="hidden" name="courseId" value="` + workCourseID + `">
<input type="hidden" name="classId" value="` + workClassID + `">
<input type="hidden" name="api" value="1">
<input type="hidden" id="workAnswerId" name="workAnswerId" value="` + workAnswerID + `">
<input type="hidden" id="totalQuestionNum" name="totalQuestionNum" value="` + workPaperHash + `">
<input type="hidden" id="fullScore" name="fullScore" value="100.0">
<input type="hidden" name="knowledgeid" value="` + workChapterID + `">
<input type="hidden" name="oldSchoolId" value="">
<input type="hidden" id="oldWorkId" name="oldWorkId" value="` + workJobSuffix + `">
<input type="hidden" name="jobid" value="` + workJobID + `">
<input type="hidden" id="workRelationId" name="workRelationId" value="` + workRelationID + `">
<input type="hidden" name="enc" value="">
<input type="hidden" id="enc_work" name="enc_work" value="` + workSubmitToken + `">
<input type="hidden" name="userId" value="7001">
<input type="hidden" name="pyFlag" value="1">
<input type="hidden" name="rd" value="__CSRF__">
<input type="hidden" name="_edt" value="__CSRF__">
<input type="hidden" name="tiankongsize110004" value="2">

<div class="questionLi singleQuesId" data="110001" id="question110001">
  <div class="TiMu" data="0">
    <input type="hidden" name="answertype110001" value="0">
    <div class="Zy_TItle"><div class="clearfix"><i class="fl">1</i><div class="fl"><span class="newZy_TItle">[单选题]</span>单选题题干<img src="/img/1.png"></div></div></div>
    <ul class="stem_answer clearfix">
      <li class="clearfix"><input type="radio" name="answer110001" value="A"><span class="num_option" data="A">A</span><a class="fl after">甲</a></li>
      <li class="clearfix"><input type="radio" name="answer110001" value="B"><span class="num_option" data="B">B</span><div class="answer_p">乙</div></li>
    </ul>
  </div>
</div>

<div class="questionLi singleQuesId" data="110002" id="question110002">
  <div class="TiMu" data="1">
    <div class="Zy_TItle"><div class="clearfix"><i class="fl">2</i><div class="fl"><span class="newZy_TItle">[多选题]</span>多选题题干</div></div></div>
    <div class="stem_answer">
      <div class="answerBg"><span class="num_option_dx" data="A">A</span><div class="answer_p">甲</div></div>
      <div class="answerBg"><span class="num_option_dx" data="B">B</span><div class="answer_p">乙</div></div>
      <div class="answerBg"><span class="num_option_dx" data="C">C</span><div class="answer_p">丙<img src="/img/2.png"></div></div>
    </div>
  </div>
</div>

<div class="questionLi singleQuesId" data="110003" id="question110003">
  <div class="TiMu" data="3">
    <div class="Zy_TItle"><div class="clearfix"><i class="fl">3</i><div class="fl"><span class="newZy_TItle">[判断题]</span>判断题题干</div></div></div>
    <ul class="stem_answer clearfix">
      <li class="clearfix"><input type="radio" name="answer110003" value="true"><span class="after">正确</span></li>
      <li class="clearfix"><input type="radio" name="answer110003" value="false"><span class="after">错误</span></li>
    </ul>
  </div>
</div>

<div class="questionLi singleQuesId" data="110004" id="question110004">
  <div class="TiMu" data="2">
    <div class="Zy_TItle"><div class="clearfix"><i class="fl">4</i><div class="fl"><span class="newZy_TItle">[填空题]</span>填空题题干</div></div></div>
    <div class="stem_answer">
      <div class="Answer"><input type="text" name="answer1100041" value=""></div>
      <div class="Answer"><input type="text" name="answer1100042" value=""></div>
    </div>
  </div>
</div>

<div class="questionLi singleQuesId" data="110005" id="question110005">
  <div class="TiMu" data="4">
    <div class="Zy_TItle"><div class="clearfix"><i class="fl">5</i><div class="fl"><span class="newZy_TItle">[简答题]</span>简答题题干</div></div></div>
    <textarea name="answer110005"></textarea>
  </div>
</div>
</form>
</body></html>`
}

func workTestPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func (f *workFixture) platform(t *testing.T) *Platform {
	t.Helper()
	options := Options{ReadOnlyEnabled: true, Endpoints: Endpoints{
		CourseList: f.server.URL + "/courselist",
		Catalog:    f.server.URL + "/catalog",
		Cards:      f.server.URL + "/cards",
		Work:       f.server.URL + "/work",
		WorkSubmit: f.server.URL + "/submit",
	}}
	options.AllowedHosts = []string{"127.0.0.1"}
	options.allowPrivateNetworks = true
	options.MinRequestInterval = 50 * time.Millisecond
	options.Timeout = 2 * time.Second
	clientTimeout := f.clientTimeout
	options.newClient = func(jar http.CookieJar) *http.Client {
		return &http.Client{Jar: jar, Transport: f.server.Client().Transport, Timeout: clientTimeout}
	}
	platform, err := NewPlatform(options)
	if err != nil {
		t.Fatal(err)
	}
	return platform
}

func workTaskRef() learning.PlatformTaskRef {
	return learning.PlatformTaskRef{
		Course: learning.PlatformCourseRef{ExternalCourseID: workCourseID,
			ExternalClassID: workClassID, CPI: workCPI, CycleKey: "current"},
		ChapterExternalID: workChapterID,
		ObjectID:          workObjectID,
		TaskPointID:       workJobID,
	}
}

func workAnswers() []learning.WorkAnswer {
	return []learning.WorkAnswer{
		{QuestionID: "110001", Values: []string{"A"}},
		{QuestionID: "110002", Values: []string{"C", "A"}},
		{QuestionID: "110003", Values: []string{"true"}},
		{QuestionID: "110004", Values: []string{"第一空", "第二空"}},
		{QuestionID: "110005", Values: []string{"简答原文第一行\n简答原文第二行"}},
	}
}

func requireWorkFailure(t *testing.T, err error, class learning.FailureClass) *learning.Failure {
	t.Helper()
	if err == nil {
		t.Fatalf("期望失败 %s，实际成功", class)
	}
	var failure *learning.Failure
	if !errors.As(err, &failure) {
		t.Fatalf("错误不是可分类的 learning.Failure: %v", err)
	}
	if failure.Class != class {
		t.Fatalf("失败分类为 %s，期望 %s: %v", failure.Class, class, err)
	}
	return failure
}

func TestWorkReadAndSubmitProtocol(t *testing.T) {
	fixture := newWorkFixture(t)
	platform := fixture.platform(t)
	session := encodeTestSession(t)
	sheet, err := platform.ReadWork(context.Background(), session, workTaskRef())
	if err != nil {
		t.Fatal(err)
	}
	if sheet.Submitted || sheet.Fingerprint == "" || len(sheet.Questions) != 5 {
		t.Fatalf("试卷结构错误: %+v", sheet)
	}
	types := []string{"single", "multiple", "judgement", "completion", "shortanswer"}
	for index, question := range sheet.Questions {
		if question.Type != types[index] {
			t.Fatalf("题目 %d 类型错误: %s", index, question.Type)
		}
	}
	if len(sheet.Questions[0].Options) != 2 || sheet.Questions[0].Options[0].Value != "A" ||
		sheet.Questions[0].Options[0].Text != "甲" {
		t.Fatalf("单选题选项错误: %+v", sheet.Questions[0].Options)
	}
	if len(sheet.Questions[1].Options) != 3 || sheet.Questions[1].Options[2].Value != "C" {
		t.Fatalf("多选题选项错误: %+v", sheet.Questions[1].Options)
	}
	if len(sheet.Questions[2].Options) != 2 || sheet.Questions[2].Options[0].Value != "true" {
		t.Fatalf("判断题选项错误: %+v", sheet.Questions[2].Options)
	}
	if sheet.Questions[3].Blanks != 2 {
		t.Fatalf("填空题空位数量错误: %+v", sheet.Questions[3])
	}
	if len(sheet.Questions[0].Images) != 1 || sheet.Questions[0].Images[0].MIMEType != "image/png" ||
		len(sheet.Questions[1].Images) != 1 {
		t.Fatalf("题目图片错误: %+v", sheet.Questions)
	}
	decoded, err := base64.StdEncoding.DecodeString(sheet.Questions[0].Images[0].Base64)
	if err != nil || !bytes.Equal(decoded, fixture.image1) {
		t.Fatalf("题目图片内容不一致")
	}

	second, err := platform.ReadWork(context.Background(), session, workTaskRef())
	if err != nil {
		t.Fatal(err)
	}
	if second.Fingerprint != sheet.Fingerprint {
		t.Fatalf("动态 CSRF 改变了试卷指纹: %s != %s", second.Fingerprint, sheet.Fingerprint)
	}

	submission, err := platform.SubmitWork(context.Background(), session, workTaskRef(), sheet.Fingerprint, workAnswers(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !submission.Accepted || submission.AlreadySubmitted {
		t.Fatalf("提交结果错误: %+v", submission)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.postCount != 1 {
		t.Fatalf("最终提交次数为 %d，期望 1", fixture.postCount)
	}
	form := fixture.lastForm
	if form.Get("pyFlag") != "" {
		t.Fatalf("最终提交不是非暂存: %q", form.Get("pyFlag"))
	}
	if form.Get("answerwqbid") != "110001,110002,110003,110004,110005," {
		t.Fatalf("answerwqbid 错误: %q", form.Get("answerwqbid"))
	}
	if form.Get("answer110001") != "A" || form.Get("answertype110001") != "0" {
		t.Fatalf("单选题提交字段错误: %v", form)
	}
	if form.Get("answer110002") != "AC" || form.Get("answertype110002") != "1" {
		t.Fatalf("多选题提交字段错误: %v", form)
	}
	if strings.Join(form["answercheck110002"], ",") != "A,C" {
		t.Fatalf("多选 answercheck 错误: %v", form["answercheck110002"])
	}
	if form.Get("answer110003") != "true" || form.Get("answertype110003") != "3" {
		t.Fatalf("判断题提交字段错误: %v", form)
	}
	if form.Get("tiankongsize110004") != "2" || form.Get("answer1100041") != "第一空" ||
		form.Get("answer1100042") != "第二空" || form.Get("answertype110004") != "2" {
		t.Fatalf("填空题提交字段错误: %v", form)
	}
	if form.Get("answer110005") != "简答原文第一行\n简答原文第二行" || form.Get("answertype110005") != "4" {
		t.Fatalf("简答题提交字段错误: %v", form)
	}
	if form.Get("totalQuestionNum") != workPaperHash || form.Get("workAnswerId") != workAnswerID ||
		form.Get("oldWorkId") != workJobSuffix || form.Get("enc_work") != workSubmitToken ||
		form.Get("courseId") != workCourseID || form.Get("classId") != workClassID {
		t.Fatalf("当前表单令牌未保留: %v", form)
	}
	latestCSRF := strconv.Itoa(fixture.workGetCount)
	if form.Get("_edt") != latestCSRF || form.Get("rd") != latestCSRF {
		t.Fatalf("未使用提交前重新读取的表单时间戳: %v", form)
	}
}

func TestWorkSubmitRejectsChangedAttempt(t *testing.T) {
	fixture := newWorkFixture(t)
	platform := fixture.platform(t)
	session := encodeTestSession(t)
	sheet, err := platform.ReadWork(context.Background(), session, workTaskRef())
	if err != nil {
		t.Fatal(err)
	}
	fixture.mu.Lock()
	fixture.pageHTML = strings.Replace(fixture.pageHTML, `name="workAnswerId" value="90001"`, `name="workAnswerId" value="90002"`, 1)
	fixture.mu.Unlock()
	_, err = platform.SubmitWork(context.Background(), session, workTaskRef(), sheet.Fingerprint, workAnswers(), nil)
	requireWorkFailure(t, err, learning.FailurePermanent)
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.postCount != 0 {
		t.Fatalf("作答机会变化后仍发送提交: %d", fixture.postCount)
	}
}

func TestWorkBeforePostCallback(t *testing.T) {
	t.Run("拒绝时不发送", func(t *testing.T) {
		fixture := newWorkFixture(t)
		platform := fixture.platform(t)
		session := encodeTestSession(t)
		sheet, err := platform.ReadWork(context.Background(), session, workTaskRef())
		if err != nil {
			t.Fatal(err)
		}
		fixture.mu.Lock()
		initialWorkGets := fixture.workGetCount
		initialImageGets := fixture.imageGetCount
		fixture.mu.Unlock()
		calls := 0
		var callbackWorkGets, callbackImageGets, callbackPostCount int
		beforePost := func(context.Context) error {
			calls++
			fixture.mu.Lock()
			callbackWorkGets = fixture.workGetCount
			callbackImageGets = fixture.imageGetCount
			callbackPostCount = fixture.postCount
			fixture.mu.Unlock()
			return learning.Fail(learning.FailureStopped, "协调器状态检查拒绝提交")
		}
		_, err = platform.SubmitWork(context.Background(), session, workTaskRef(), sheet.Fingerprint, workAnswers(), beforePost)
		requireWorkFailure(t, err, learning.FailureStopped)
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		if calls != 1 || callbackPostCount != 0 || fixture.postCount != 0 {
			t.Fatalf("回调拒绝后仍发送 POST: calls=%d callbackPost=%d post=%d", calls, callbackPostCount, fixture.postCount)
		}
		if callbackWorkGets <= initialWorkGets || callbackImageGets <= initialImageGets {
			t.Fatalf("回调不在重新取题和图片之后: work %d->%d image %d->%d",
				initialWorkGets, callbackWorkGets, initialImageGets, callbackImageGets)
		}
	})

	t.Run("允许时只调用一次", func(t *testing.T) {
		fixture := newWorkFixture(t)
		platform := fixture.platform(t)
		session := encodeTestSession(t)
		sheet, err := platform.ReadWork(context.Background(), session, workTaskRef())
		if err != nil {
			t.Fatal(err)
		}
		calls := 0
		submission, err := platform.SubmitWork(context.Background(), session, workTaskRef(), sheet.Fingerprint, workAnswers(),
			func(context.Context) error { calls++; return nil })
		if err != nil {
			t.Fatal(err)
		}
		if !submission.Accepted || calls != 1 {
			t.Fatalf("回调次数或提交结果错误: %+v calls=%d", submission, calls)
		}
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		if fixture.postCount != 1 {
			t.Fatalf("提交次数错误: %d", fixture.postCount)
		}
	})

	t.Run("图片变化不调用回调", func(t *testing.T) {
		fixture := newWorkFixture(t)
		platform := fixture.platform(t)
		session := encodeTestSession(t)
		sheet, err := platform.ReadWork(context.Background(), session, workTaskRef())
		if err != nil {
			t.Fatal(err)
		}
		fixture.mu.Lock()
		fixture.image1 = workTestPNG(t, 4, 4)
		fixture.mu.Unlock()
		calls := 0
		_, err = platform.SubmitWork(context.Background(), session, workTaskRef(), sheet.Fingerprint, workAnswers(),
			func(context.Context) error { calls++; return nil })
		requireWorkFailure(t, err, learning.FailurePermanent)
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		if calls != 0 || fixture.postCount != 0 {
			t.Fatalf("图片变化后调用了回调或发送 POST: calls=%d post=%d", calls, fixture.postCount)
		}
	})
}

func TestWorkSubmitRejectsChangedPaper(t *testing.T) {
	fixture := newWorkFixture(t)
	platform := fixture.platform(t)
	session := encodeTestSession(t)
	sheet, err := platform.ReadWork(context.Background(), session, workTaskRef())
	if err != nil {
		t.Fatal(err)
	}
	fixture.mu.Lock()
	fixture.pageHTML = strings.Replace(fixture.pageHTML, "<div class=\"answer_p\">甲</div>",
		"<div class=\"answer_p\">新甲</div>", 1)
	fixture.mu.Unlock()
	_, err = platform.SubmitWork(context.Background(), session, workTaskRef(), sheet.Fingerprint, workAnswers(), nil)
	requireWorkFailure(t, err, learning.FailurePermanent)
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.postCount != 0 {
		t.Fatalf("试卷变化后仍发送提交: %d", fixture.postCount)
	}
}

func TestWorkSubmitRejectsInvalidAnswers(t *testing.T) {
	cases := []struct {
		name    string
		answers []learning.WorkAnswer
	}{
		{name: "数量不足", answers: workAnswers()[:3]},
		{name: "题目编号不存在", answers: replaceWorkAnswer(workAnswers(), "110001", "999999", []string{"A"})},
		{name: "选项值非法", answers: replaceWorkAnswer(workAnswers(), "110001", "110001", []string{"Z"})},
		{name: "多选重复", answers: replaceWorkAnswer(workAnswers(), "110002", "110002", []string{"A", "A"})},
		{name: "填空数量不一致", answers: replaceWorkAnswer(workAnswers(), "110004", "110004", []string{"只有一空"})},
		{name: "简答多值", answers: replaceWorkAnswer(workAnswers(), "110005", "110005", []string{"第一份", "第二份"})},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newWorkFixture(t)
			platform := fixture.platform(t)
			session := encodeTestSession(t)
			sheet, err := platform.ReadWork(context.Background(), session, workTaskRef())
			if err != nil {
				t.Fatal(err)
			}
			_, err = platform.SubmitWork(context.Background(), session, workTaskRef(), sheet.Fingerprint, testCase.answers, nil)
			requireWorkFailure(t, err, learning.FailurePermanent)
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if fixture.postCount != 0 {
				t.Fatalf("非法答案仍发送提交: %d", fixture.postCount)
			}
		})
	}
}

func replaceWorkAnswer(answers []learning.WorkAnswer, oldID, newID string, values []string) []learning.WorkAnswer {
	result := append([]learning.WorkAnswer(nil), answers...)
	for index := range result {
		if result[index].QuestionID == oldID {
			result[index] = learning.WorkAnswer{QuestionID: newID, Values: values}
		}
	}
	return result
}

func TestWorkAlreadySubmittedDoesNotPost(t *testing.T) {
	fixture := newWorkFixture(t)
	fixture.pageHTML = `<html><body><div class="TiMu singleQuesId" data="110001"><div class="Zy_TItle">1. 题目</div></div>
		<div>本次成绩 100 分</div><div>我的答案 A</div><div>正确答案 A</div></body></html>`
	platform := fixture.platform(t)
	session := encodeTestSession(t)
	sheet, err := platform.ReadWork(context.Background(), session, workTaskRef())
	if err != nil {
		t.Fatal(err)
	}
	if !sheet.Submitted || sheet.Fingerprint == "" {
		t.Fatalf("已提交页面未识别: %+v", sheet)
	}
	submission, err := platform.SubmitWork(context.Background(), session, workTaskRef(), sheet.Fingerprint,
		[]learning.WorkAnswer{{QuestionID: "110001", Values: []string{"A"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !submission.AlreadySubmitted || submission.Accepted {
		t.Fatalf("已提交结果错误: %+v", submission)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.postCount != 0 {
		t.Fatalf("已提交页面仍发送提交: %d", fixture.postCount)
	}
}

func TestWorkPageFailures(t *testing.T) {
	cases := []struct {
		name   string
		page   string
		status int
		class  learning.FailureClass
	}{
		{name: "验证码", page: `<html><body><h1>请输入验证码</h1></body></html>`, class: learning.FailureVerificationNeeded},
		{name: "教师未创建", page: `<html><body>教师未创建完成该测验</body></html>`, class: learning.FailureCapabilityMissing},
		{name: "权限拒绝", page: `<html><body>无效的权限</body></html>`, class: learning.FailureForbidden},
		{name: "身份失效", page: `<html><body><form action="https://passport2.chaoxing.com/login"></form>fanyalogin</body></html>`, class: learning.FailureIdentityInvalid},
		{name: "GET 403", status: http.StatusForbidden, class: learning.FailureForbidden},
		{name: "GET 429", status: http.StatusTooManyRequests, class: learning.FailureRateLimited},
		{name: "题型代码未识别", page: strings.Replace(workPageHTML(), `<div class="TiMu" data="0">`, `<div class="TiMu" data="9">`, 1), class: learning.FailureCapabilityMissing},
		{name: "题型与标签不一致", page: strings.Replace(workPageHTML(), `<div class="TiMu" data="0">`, `<div class="TiMu" data="5">`, 1), class: learning.FailureCapabilityMissing},
		{name: "字体加密", page: `<html><head><style id="cxSecretStyle">@font-face{font-family:x}</style></head><body>题目</body></html>`, class: learning.FailureCapabilityMissing},
		{name: "PUA 字符", page: strings.Replace(workPageHTML(), `单选题题干<img`, "\ue001单选题题干<img", 1), class: learning.FailureCapabilityMissing},
		{name: "缺少题目编号", page: strings.Replace(workPageHTML(), `<div class="questionLi singleQuesId" data="110001"`, `<div class="questionLi singleQuesId"`, 1), class: learning.FailureCapabilityMissing},
		{name: "编号不一致", page: strings.Replace(workPageHTML(), `name="answertype110001" value="0"`, `name="answertype999999" value="0"`, 1), class: learning.FailureCapabilityMissing},
		{name: "表单章节不一致", page: strings.Replace(workPageHTML(), `name="knowledgeid" value="5001"`, `name="knowledgeid" value="5002"`, 1), class: learning.FailureInvalidTask},
		{name: "表单缺少令牌", page: strings.Replace(workPageHTML(), `id="workRelationId" name="workRelationId" value="11020928"`, `id="workRelationId" name="workRelationId" value=""`, 1), class: learning.FailureCapabilityMissing},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newWorkFixture(t)
			if testCase.page != "" {
				fixture.pageHTML = testCase.page
			}
			fixture.getStatus = testCase.status
			platform := fixture.platform(t)
			_, err := platform.ReadWork(context.Background(), encodeTestSession(t), workTaskRef())
			requireWorkFailure(t, err, testCase.class)
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if fixture.postCount != 0 {
				t.Fatalf("读取失败时仍发送提交: %d", fixture.postCount)
			}
		})
	}
}

func TestWorkSubmitResponseClassification(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		location   string
		body       string
		delay      time.Duration
		timeout    time.Duration
		class      learning.FailureClass
		accepted   bool
		already    bool
		failureNil bool
	}{
		{name: "成功", body: `{"status":true,"msg":"success!"}`, accepted: true, failureNil: true},
		{name: "验证码", body: `{"status":false,"msg":"验证码错误"}`, class: learning.FailureVerificationNeeded},
		{name: "身份失效", status: http.StatusUnauthorized, class: learning.FailureIdentityInvalid},
		{name: "403", status: http.StatusForbidden, class: learning.FailureForbidden},
		{name: "429", status: http.StatusTooManyRequests, class: learning.FailureRateLimited},
		{name: "风险码 403", body: `{"status":false,"code":403,"msg":""}`, class: learning.FailureForbidden},
		{name: "风险码 429", body: `{"status":false,"code":429,"msg":""}`, class: learning.FailureRateLimited},
		{name: "验证字段", body: `{"status":false,"needCaptcha":true,"msg":""}`, class: learning.FailureVerificationNeeded},
		{name: "未知 JSON", body: `{"status":false,"msg":"答案校验失败"}`, class: learning.FailurePermanent},
		{name: "已提交", body: `{"status":false,"msg":"该作业已提交"}`, already: true, failureNil: true},
		{name: "未知响应", body: `<html><body>unknown</body></html>`, class: learning.FailureSubmissionUnknown},
		{name: "未知状态", status: http.StatusInternalServerError, class: learning.FailureSubmissionUnknown},
		{name: "登录重定向", status: http.StatusFound, location: "/passport2/login", class: learning.FailureSubmissionUnknown},
		{name: "未知重定向", status: http.StatusFound, location: "/moved", class: learning.FailureSubmissionUnknown},
		{name: "空响应", body: ``, class: learning.FailureSubmissionUnknown},
		{name: "缺少状态", body: `{"msg":"success"}`, class: learning.FailureSubmissionUnknown},
		{name: "响应丢失", body: `{"status":true,"msg":"success!"}`, delay: 300 * time.Millisecond,
			timeout: 100 * time.Millisecond, class: learning.FailureSubmissionUnknown},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newWorkFixture(t)
			if testCase.timeout > 0 {
				fixture.clientTimeout = testCase.timeout
			}
			platform := fixture.platform(t)
			session := encodeTestSession(t)
			sheet, err := platform.ReadWork(context.Background(), session, workTaskRef())
			if err != nil {
				t.Fatal(err)
			}
			fixture.mu.Lock()
			fixture.submitStatus = testCase.status
			fixture.submitLocation = testCase.location
			fixture.submitBody = testCase.body
			fixture.submitDelay = testCase.delay
			fixture.mu.Unlock()
			submission, err := platform.SubmitWork(context.Background(), session, workTaskRef(), sheet.Fingerprint, workAnswers(), nil)
			if testCase.failureNil {
				if err != nil {
					t.Fatalf("提交不应失败: %v", err)
				}
				if submission.Accepted != testCase.accepted || submission.AlreadySubmitted != testCase.already {
					t.Fatalf("提交结果错误: %+v", submission)
				}
			} else {
				requireWorkFailure(t, err, testCase.class)
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if fixture.postCount != 1 {
				t.Fatalf("最终提交次数为 %d，期望 1", fixture.postCount)
			}
		})
	}
}

func TestWorkImageLimits(t *testing.T) {
	fixture := newWorkFixture(t)
	fixture.pageHTML = strings.Replace(fixture.pageHTML, "/img/1.png", "/img/large.png", 1)
	platform := fixture.platform(t)
	_, err := platform.ReadWork(context.Background(), encodeTestSession(t), workTaskRef())
	requireWorkFailure(t, err, learning.FailureCapabilityMissing)

	fixture2 := newWorkFixture(t)
	fixture2.pageHTML = strings.Replace(fixture2.pageHTML, "/img/1.png", "https://evil.example/x.png", 1)
	platform2 := fixture2.platform(t)
	_, err = platform2.ReadWork(context.Background(), encodeTestSession(t), workTaskRef())
	requireWorkFailure(t, err, learning.FailureCapabilityMissing)
}

func TestWorkImageContentFingerprint(t *testing.T) {
	fixture := newWorkFixture(t)
	platform := fixture.platform(t)
	session := encodeTestSession(t)
	sheet, err := platform.ReadWork(context.Background(), session, workTaskRef())
	if err != nil {
		t.Fatal(err)
	}
	fixture.mu.Lock()
	fixture.image1 = workTestPNG(t, 4, 4)
	fixture.mu.Unlock()
	_, err = platform.SubmitWork(context.Background(), session, workTaskRef(), sheet.Fingerprint, workAnswers(), nil)
	requireWorkFailure(t, err, learning.FailurePermanent)
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.postCount != 0 {
		t.Fatalf("图片内容变化后仍发送提交: %d", fixture.postCount)
	}
}

func TestWorkSignedImageURLKeepsFingerprint(t *testing.T) {
	fixture := newWorkFixture(t)
	fixture.pageHTML = strings.ReplaceAll(fixture.pageHTML, "/img/1.png", "/img/1.png?token=__CSRF__")
	platform := fixture.platform(t)
	session := encodeTestSession(t)
	first, err := platform.ReadWork(context.Background(), session, workTaskRef())
	if err != nil {
		t.Fatal(err)
	}
	second, err := platform.ReadWork(context.Background(), session, workTaskRef())
	if err != nil {
		t.Fatal(err)
	}
	if first.Fingerprint != second.Fingerprint {
		t.Fatalf("短期图片地址变化改变了内容指纹: %s != %s", first.Fingerprint, second.Fingerprint)
	}
}

func TestCompletionSingleIndexedBlank(t *testing.T) {
	root := parseHTMLDocument([]byte(`<html><body><form id="form1">
		<input type="hidden" name="tiankongsize110004" value="1">
		<div class="singleQuesId" data="110004"><div class="TiMu" data="2">
		<div class="Zy_TItle">填空题</div><input type="text" name="answer1100041">
		</div></div></form></body></html>`))
	_, values, err := extractWorkForm(root)
	if err != nil || values == nil {
		t.Fatalf("表单解析失败: %v", err)
	}
	form, _, _ := extractWorkForm(root)
	questions := collectQuestionNodes(root)
	if len(questions) != 1 {
		t.Fatalf("题目数量错误: %d", len(questions))
	}
	plan, err := parseWorkBlanks(questions[0], form, "110004", "completion")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Count != 1 || plan.Style != "indexed" {
		t.Fatalf("单空编号字段未按编号表单识别: %+v", plan)
	}
}

func TestHTMLTextNodeOrder(t *testing.T) {
	root := parseHTMLDocument([]byte(`<html><body><div class="Zy_TItle">甲<b>乙</b>丙</div>
		<ul><li><input type="radio" name="answer1" value="A">甲<b>乙</b>丙</li></ul></body></html>`))
	titleNode := root.firstByClass("Zy_TItle")
	if titleNode == nil || htmlNodeText(titleNode) != "甲乙丙" {
		t.Fatalf("题干文本节点顺序错误: %q", htmlNodeText(titleNode))
	}
	optionNode := root.first(func(node *htmlNode) bool { return node.tag == "li" })
	if optionNode == nil {
		t.Fatal("选项节点缺失")
	}
	option, err := parseWorkOption(optionNode)
	if err != nil {
		t.Fatal(err)
	}
	if option.Text != "甲乙丙" || option.Value != "A" {
		t.Fatalf("选项文本节点顺序错误: %+v", option)
	}
}

func TestValidateWorkImageDimensions(t *testing.T) {
	payload := workTestPNG(t, maxWorkImageSide+1, 1)
	_, err := validateWorkImage(payload, int64(maxWorkImageBytes), "image")
	requireWorkFailure(t, err, learning.FailureCapabilityMissing)
}

func TestWorkImageRawSize(t *testing.T) {
	image := learning.WorkImage{Base64: base64.StdEncoding.EncodeToString(make([]byte, 1234))}
	if size := workImageRawSize(image); size != 1234 {
		t.Fatalf("图片原始体积计算错误: %d", size)
	}
	if workImageBudgetExceeded(maxWorkImageTotalBytes-2, 2) {
		t.Fatal("图片累计体积边界判断错误")
	}
	if !workImageBudgetExceeded(maxWorkImageTotalBytes-1, 2) {
		t.Fatal("图片累计体积超限未被拒绝")
	}
}

func TestReadCardsRecognizesQuiz(t *testing.T) {
	fixture := newWorkFixture(t)
	platform := fixture.platform(t)
	attachments, _, err := platform.readCards(context.Background(), testSession(),
		workTaskRef().Course, workChapterID)
	if err != nil {
		t.Fatal(err)
	}
	if len(attachments) != 1 || attachments[0].Kind != "quiz" || attachments[0].TaskPointID != workJobID ||
		attachments[0].ObjectID != workJobID {
		t.Fatalf("测验任务未识别为 quiz 或缺少对象标识回退: %+v", attachments)
	}
}

func TestNormalizeAttachmentKindQuiz(t *testing.T) {
	for _, value := range []string{"workid", "work", "WORKID", "quiz"} {
		if kind := normalizeAttachmentKind(value); kind != "quiz" {
			t.Fatalf("%s 未映射为 quiz: %s", value, kind)
		}
	}
	if kind := normalizeAttachmentKind("video"); kind != "video" {
		t.Fatalf("视频类型被改变: %s", kind)
	}
}

// 保证测试夹具生成的图片与提交内容哈希稳定，避免误用外部资源。
func TestWorkImageReferenceDoesNotExposeSource(t *testing.T) {
	ref, err := workImageReference("https://p.ananas.chaoxing.com/star3/origin/example.png")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("https://p.ananas.chaoxing.com/star3/origin/example.png"))
	if ref.ID != hex.EncodeToString(sum[:8]) || fmt.Sprint(ref.URL) == "" {
		t.Fatalf("图片引用错误: %+v", ref)
	}
}

// workSingleChoiceHTML 组合一个最小可作答页面，答案控件可按需标记为 disabled。
func workSingleChoiceHTML(inputSuffix, tail string) string {
	return `<html><head><title>章节测验</title></head><body>
<form id="form1" action="/mooc-ans/work/addStudentWorkNew" method="post">
<input type="hidden" name="courseId" value="` + workCourseID + `">
<input type="hidden" name="classId" value="` + workClassID + `">
<input type="hidden" name="api" value="1">
<input type="hidden" name="workAnswerId" value="` + workAnswerID + `">
<input type="hidden" name="totalQuestionNum" value="` + workPaperHash + `">
<input type="hidden" name="fullScore" value="100.0">
<input type="hidden" name="knowledgeid" value="` + workChapterID + `">
<input type="hidden" name="oldWorkId" value="` + workJobSuffix + `">
<input type="hidden" name="jobid" value="` + workJobID + `">
<input type="hidden" name="workRelationId" value="` + workRelationID + `">
<input type="hidden" name="enc_work" value="` + workSubmitToken + `">
<input type="hidden" name="userId" value="7001">
<input type="hidden" name="pyFlag" value="1">
<div class="questionLi singleQuesId" data="110001" id="question110001">
  <div class="TiMu" data="0">
    <div class="Zy_TItle"><div class="clearfix"><span class="newZy_TItle">[单选题]</span>单选题题干</div></div>
    <ul class="stem_answer clearfix">
      <li class="clearfix"><input type="radio" name="answer110001" value="A"` + inputSuffix + `><a class="fl after">甲</a></li>
      <li class="clearfix"><input type="radio" name="answer110001" value="B"` + inputSuffix + `><a class="fl after">乙</a></li>
    </ul>
  </div>
</div>
</form>` + tail + `</body></html>`
}

// 结果页仍保留完整表单，答案控件带布尔 disabled 属性；不能按可作答页面重复提交。
func TestWorkDisabledAnswerControlsAreAlreadySubmitted(t *testing.T) {
	markers := `<div>本次成绩 100 分</div><div>我的答案 A</div><div>正确答案 A</div>`
	fixture := newWorkFixture(t)
	fixture.pageHTML = workSingleChoiceHTML("", markers)
	platform := fixture.platform(t)
	session := encodeTestSession(t)
	answerable, err := platform.ReadWork(context.Background(), session, workTaskRef())
	if err != nil {
		t.Fatal(err)
	}
	if answerable.Submitted || len(answerable.Questions) != 1 {
		t.Fatalf("可作答页面被误判: %+v", answerable)
	}

	disabled := newWorkFixture(t)
	disabled.pageHTML = workSingleChoiceHTML(` disabled`, markers)
	disabledPlatform := disabled.platform(t)
	sheet, err := disabledPlatform.ReadWork(context.Background(), session, workTaskRef())
	if err != nil {
		t.Fatal(err)
	}
	if !sheet.Submitted || len(sheet.Questions) != 0 {
		t.Fatalf("disabled 结果页被当作可作答: %+v", sheet)
	}
	submission, err := disabledPlatform.SubmitWork(context.Background(), session, workTaskRef(), sheet.Fingerprint,
		[]learning.WorkAnswer{{QuestionID: "110001", Values: []string{"A"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !submission.AlreadySubmitted || submission.Accepted {
		t.Fatalf("disabled 结果页提交结果错误: %+v", submission)
	}
	disabled.mu.Lock()
	defer disabled.mu.Unlock()
	if disabled.postCount != 0 {
		t.Fatalf("disabled 结果页仍发送最终提交: %d", disabled.postCount)
	}
}

// 已提交结果页即使表单令牌完整也不存在可编辑控件，必须停止作答而不是猜测重交。
func TestWorkDisabledControlsWithoutResultMarkersStop(t *testing.T) {
	fixture := newWorkFixture(t)
	fixture.pageHTML = workSingleChoiceHTML(` disabled readonly`, `<div>作业说明</div>`)
	_, err := fixture.platform(t).ReadWork(context.Background(), encodeTestSession(t), workTaskRef())
	requireWorkFailure(t, err, learning.FailureCapabilityMissing)
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.postCount != 0 {
		t.Fatalf("不可作答页面仍发送提交: %d", fixture.postCount)
	}
}

// 没有题目文字时，页面上的验证控件仍必须按平台验证停止。
func TestWorkVerificationControlWithoutQuestionText(t *testing.T) {
	fixture := newWorkFixture(t)
	fixture.pageHTML = `<html><body><div id="captchaContainer"><input name="verifyCode" type="text"></div>
	<p>请按提示完成校验后继续。</p></body></html>`
	_, err := fixture.platform(t).ReadWork(context.Background(), encodeTestSession(t), workTaskRef())
	requireWorkFailure(t, err, learning.FailureVerificationNeeded)
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.postCount != 0 {
		t.Fatalf("验证页面仍发送提交: %d", fixture.postCount)
	}
}

// 平台富文本空位的真实字段名是 answerEditor{id}{n}，提交必须写入页面已有字段。
func TestWorkCompletionUsesPageBlankFields(t *testing.T) {
	fixture := newWorkFixture(t)
	fixture.pageHTML = strings.ReplaceAll(fixture.pageHTML,
		`<div class="Answer"><input type="text" name="answer1100041" value=""></div>`,
		`<div class="Answer"><textarea id="answerEditor1100041" name="answerEditor1100041"></textarea></div>`)
	fixture.pageHTML = strings.ReplaceAll(fixture.pageHTML,
		`<div class="Answer"><input type="text" name="answer1100042" value=""></div>`,
		`<div class="Answer"><textarea id="answerEditor1100042" name="answerEditor1100042"></textarea></div>`)
	fixture.pageHTML = strings.Replace(fixture.pageHTML,
		`<input type="hidden" name="tiankongsize110004" value="2">`, ``, 1)
	platform := fixture.platform(t)
	session := encodeTestSession(t)
	sheet, err := platform.ReadWork(context.Background(), session, workTaskRef())
	if err != nil {
		t.Fatal(err)
	}
	if sheet.Questions[3].Blanks != 2 {
		t.Fatalf("富文本空位数量错误: %+v", sheet.Questions[3])
	}
	submission, err := platform.SubmitWork(context.Background(), session, workTaskRef(), sheet.Fingerprint, workAnswers(), nil)
	if err != nil || !submission.Accepted {
		t.Fatalf("提交结果错误: %+v %v", submission, err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	form := fixture.lastForm
	if form.Get("answerEditor1100041") != "第一空" || form.Get("answerEditor1100042") != "第二空" {
		t.Fatalf("富文本空位未写入页面字段: %v", form)
	}
	for _, invented := range []string{"answer1100041", "answer1100042", "tiankongsize110004"} {
		if _, exists := form[invented]; exists {
			t.Fatalf("提交包含页面没有的字段 %s: %v", invented, form)
		}
	}
}

// 同一空位控件的 name 与 id 属于不同命名空间时只能计为一个空位。
func TestWorkBlankCountUsesSubmitFieldNames(t *testing.T) {
	root := parseHTMLDocument([]byte(`<html><body><form id="form1">
		<input type="hidden" name="tiankongsize110004" value="1">
		<div class="singleQuesId" data="110004"><div class="TiMu" data="2">
		<div class="Zy_TItle">填空题</div>
		<input type="text" name="answer110004" id="answerEditor1100041">
		</div></div></form></body></html>`))
	form, _, err := extractWorkForm(root)
	if err != nil || form == nil {
		t.Fatalf("表单解析失败: %v", err)
	}
	questions := collectQuestionNodes(root)
	if len(questions) != 1 {
		t.Fatalf("题目数量错误: %d", len(questions))
	}
	plan, err := parseWorkBlanks(questions[0], form, "110004", "completion")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Count != 1 || plan.Style != "single" || len(plan.Fields) != 1 || plan.Fields[0] != "answer110004" {
		t.Fatalf("空位识别错误: %+v", plan)
	}
}

// 章节测验所在卡片按任务记录页码读取，不能用固定的 num=0 掩盖同章后续卡片。
func TestWorkReadsTaskCardIndex(t *testing.T) {
	fixture := newWorkFixture(t)
	platform := fixture.platform(t)
	session := encodeTestSession(t)
	task := workTaskRef()
	task.CardIndex = 2
	if _, err := platform.ReadWork(context.Background(), session, task); err != nil {
		t.Fatal(err)
	}
	fixture.mu.Lock()
	got := fixture.cardNum
	fixture.mu.Unlock()
	if got != "2" {
		t.Fatalf("测验卡片页码错误: %q", got)
	}

	defaultFixture := newWorkFixture(t)
	if _, err := defaultFixture.platform(t).ReadWork(context.Background(), session, workTaskRef()); err != nil {
		t.Fatal(err)
	}
	defaultFixture.mu.Lock()
	defer defaultFixture.mu.Unlock()
	if defaultFixture.cardNum != "0" {
		t.Fatalf("默认卡片页码错误: %q", defaultFixture.cardNum)
	}
}

// 无效卡片页码在读取卡片前停止。
func TestWorkRejectsInvalidCardIndex(t *testing.T) {
	fixture := newWorkFixture(t)
	task := workTaskRef()
	task.CardIndex = -1
	_, err := fixture.platform(t).ReadWork(context.Background(), encodeTestSession(t), task)
	requireWorkFailure(t, err, learning.FailureInvalidTask)
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.cardGetCount != 0 {
		t.Fatalf("无效页码仍读取卡片: %d", fixture.cardGetCount)
	}
}

// 多个空位共用未编号字段名时按页面顺序重复提交同一字段，不编号不存在的字段。
func TestWorkCompletionRepeatedBlankFieldSubmitsBothValues(t *testing.T) {
	fixture := newWorkFixture(t)
	fixture.pageHTML = strings.ReplaceAll(fixture.pageHTML,
		`<div class="Answer"><input type="text" name="answer1100041" value=""></div>`,
		`<div class="Answer"><input type="text" name="answer110004" class="blankInp2" value=""></div>`)
	fixture.pageHTML = strings.ReplaceAll(fixture.pageHTML,
		`<div class="Answer"><input type="text" name="answer1100042" value=""></div>`,
		`<div class="Answer"><input type="text" name="answer110004" class="blankInp2" value=""></div>`)
	platform := fixture.platform(t)
	session := encodeTestSession(t)
	sheet, err := platform.ReadWork(context.Background(), session, workTaskRef())
	if err != nil {
		t.Fatal(err)
	}
	if sheet.Questions[3].Blanks != 2 {
		t.Fatalf("重复字段空位数量错误: %+v", sheet.Questions[3])
	}
	submission, err := platform.SubmitWork(context.Background(), session, workTaskRef(), sheet.Fingerprint, workAnswers(), nil)
	if err != nil || !submission.Accepted {
		t.Fatalf("提交结果错误: %+v %v", submission, err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	values := fixture.lastForm["answer110004"]
	if len(values) != 2 || values[0] != "第一空" || values[1] != "第二空" {
		t.Fatalf("重复空位字段提交错误: %v", fixture.lastForm)
	}
	if _, exists := fixture.lastForm["answer1100041"]; exists {
		t.Fatalf("提交包含页面没有的编号字段: %v", fixture.lastForm)
	}
}

// 表单外的同名容器只属于页面辅助内容，不能进入本次试卷。
func TestWorkIgnoresQuestionContainersOutsideForm(t *testing.T) {
	decoy := `<div class="questionLi singleQuesId" data="110099"><div class="TiMu" data="0">
		<div class="Zy_TItle">题目导航摘要</div></div></div>`
	fixture := newWorkFixture(t)
	fixture.pageHTML = workSingleChoiceHTML("", decoy)
	sheet, err := fixture.platform(t).ReadWork(context.Background(), encodeTestSession(t), workTaskRef())
	if err != nil {
		t.Fatal(err)
	}
	if len(sheet.Questions) != 1 || sheet.Questions[0].ID != "110001" {
		t.Fatalf("表单外容器进入试卷: %+v", sheet.Questions)
	}
}

// 页面上的暂存答案属于草稿，最终提交必须只包含本次答案。
func TestWorkSubmitDropsSavedDraftAnswers(t *testing.T) {
	fixture := newWorkFixture(t)
	fixture.pageHTML = strings.Replace(fixture.pageHTML, `<li class="clearfix"><input type="radio" name="answer110001" value="B">`,
		`<input type="hidden" name="answer110001" value="B"><li class="clearfix"><input type="radio" name="answer110001" value="B" checked>`, 1)
	platform := fixture.platform(t)
	session := encodeTestSession(t)
	sheet, err := platform.ReadWork(context.Background(), session, workTaskRef())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := platform.SubmitWork(context.Background(), session, workTaskRef(), sheet.Fingerprint, workAnswers(), nil); err != nil {
		t.Fatal(err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if values := fixture.lastForm["answer110001"]; len(values) != 1 || values[0] != "A" {
		t.Fatalf("暂存答案进入最终提交: %v", fixture.lastForm)
	}
}

// workPageWithQuestions 组合带完整提交令牌的表单，题目由调用方提供。
func workPageWithQuestions(questions string) string {
	return `<html><head><title>章节测验</title></head><body>
<form id="form1" action="/mooc-ans/work/addStudentWorkNew" method="post">
<input type="hidden" name="courseId" value="` + workCourseID + `">
<input type="hidden" name="classId" value="` + workClassID + `">
<input type="hidden" name="api" value="1">
<input type="hidden" name="workAnswerId" value="` + workAnswerID + `">
<input type="hidden" name="totalQuestionNum" value="` + workPaperHash + `">
<input type="hidden" name="fullScore" value="100.0">
<input type="hidden" name="knowledgeid" value="` + workChapterID + `">
<input type="hidden" name="oldWorkId" value="` + workJobSuffix + `">
<input type="hidden" name="jobid" value="` + workJobID + `">
<input type="hidden" name="workRelationId" value="` + workRelationID + `">
<input type="hidden" name="enc_work" value="` + workSubmitToken + `">
<input type="hidden" name="pyFlag" value="1">
` + questions + `
</form></body></html>`
}

// workSubjectiveQuestion 生成一个主观题容器，字段由调用方按页面真实结构给出。
func workSubjectiveQuestion(id, code, label, field string) string {
	return `<div class="questionLi singleQuesId" data="` + id + `" id="question` + id + `">
  <div class="TiMu" data="` + code + `">
    <div class="Zy_TItle"><div class="clearfix"><span class="newZy_TItle">` + label + `</span>主观题题干</div></div>
    ` + field + `
  </div>
</div>`
}

// 简答、名词解释、论述、计算按文字主观题处理，但题型码仍保留平台原值。
func TestWorkSubjectiveTypeCodesKeepPlatformAnswerType(t *testing.T) {
	fixture := newWorkFixture(t)
	fixture.pageHTML = workPageWithQuestions(
		workSubjectiveQuestion("100004", "4", "[简答题]", `<textarea name="answer100004"></textarea>`) +
			workSubjectiveQuestion("100005", "5", "[名词解释]", `<textarea id="answerEditor100005" name="answerEditor100005"></textarea>`) +
			workSubjectiveQuestion("100006", "6", "[论述题]", `<textarea name="answer100006"></textarea>`) +
			workSubjectiveQuestion("100007", "7", "[计算题]", `<textarea name="answer100007"></textarea>`))
	platform := fixture.platform(t)
	session := encodeTestSession(t)
	sheet, err := platform.ReadWork(context.Background(), session, workTaskRef())
	if err != nil {
		t.Fatal(err)
	}
	if len(sheet.Questions) != 4 {
		t.Fatalf("主观题数量错误: %+v", sheet.Questions)
	}
	for _, question := range sheet.Questions {
		if question.Type != "shortanswer" {
			t.Fatalf("题型映射错误: %+v", question)
		}
		if len(question.Options) != 0 || question.Blanks != 0 {
			t.Fatalf("主观题结构错误: %+v", question)
		}
	}
	answers := []learning.WorkAnswer{
		{QuestionID: "100004", Values: []string{"简答原文"}},
		{QuestionID: "100005", Values: []string{"名词解释原文"}},
		{QuestionID: "100006", Values: []string{"论述原文"}},
		{QuestionID: "100007", Values: []string{"计算过程"}},
	}
	submission, err := platform.SubmitWork(context.Background(), session, workTaskRef(), sheet.Fingerprint, answers, nil)
	if err != nil || !submission.Accepted {
		t.Fatalf("提交结果错误: %+v %v", submission, err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	form := fixture.lastForm
	for id, code := range map[string]string{"100004": "4", "100005": "5", "100006": "6", "100007": "7"} {
		if got := form.Get("answertype" + id); got != code {
			t.Fatalf("题型码 %s 未按平台原值提交: %q", id, got)
		}
	}
	if form.Get("answer100004") != "简答原文" || form.Get("answerEditor100005") != "名词解释原文" ||
		form.Get("answer100006") != "论述原文" || form.Get("answer100007") != "计算过程" {
		t.Fatalf("主观题答案未写入页面字段: %v", form)
	}
	if _, exists := form["answer100005"]; exists {
		t.Fatalf("提交伪造了页面没有的字段: %v", form)
	}
	if form.Get("answerwqbid") != "100004,100005,100006,100007," {
		t.Fatalf("answerwqbid 错误: %q", form.Get("answerwqbid"))
	}
}

// workSingleChoiceQuestionHTML 生成一个可作答的单选题，用于让页面整体保持可作答表单。
func workSingleChoiceQuestionHTML() string {
	return `<div class="questionLi singleQuesId" data="110001" id="question110001">
  <div class="TiMu" data="0">
    <div class="Zy_TItle"><div class="clearfix"><span class="newZy_TItle">[单选题]</span>单选题题干</div></div>
    <ul class="stem_answer clearfix">
      <li class="clearfix"><input type="radio" name="answer110001" value="A"><a class="fl after">甲</a></li>
      <li class="clearfix"><input type="radio" name="answer110001" value="B"><a class="fl after">乙</a></li>
    </ul>
  </div>
</div>`
}

// 主观题没有可写入文本字段、含上传结构或字段冲突时停止，不强行按文字题提交。
func TestWorkSubjectiveRequiresRealTextField(t *testing.T) {
	cases := []struct {
		name    string
		page    string
		keyword string
	}{
		{name: "没有字段", page: workPageWithQuestions(
			workSingleChoiceQuestionHTML() + workSubjectiveQuestion("100005", "5", "[名词解释]", "")), keyword: "文本字段"},
		{name: "必填上传", page: workPageWithQuestions(
			workSingleChoiceQuestionHTML() + workSubjectiveQuestion("100005", "5", "[名词解释]",
				`<input type="file" name="attachment" required><textarea name="answer100005"></textarea>`)), keyword: "上传"},
		{name: "只有上传", page: workPageWithQuestions(
			workSingleChoiceQuestionHTML() + workSubjectiveQuestion("100007", "7", "[计算题]",
				`<input type="file" name="attachment">`)), keyword: "文本字段"},
		{name: "禁用字段", page: workPageWithQuestions(
			workSingleChoiceQuestionHTML() + workSubjectiveQuestion("100006", "6", "[论述题]",
				`<textarea name="answer100006" disabled></textarea>`)), keyword: "不可写"},
		{name: "只读字段", page: workPageWithQuestions(
			workSingleChoiceQuestionHTML() + workSubjectiveQuestion("100007", "7", "[计算题]",
				`<textarea name="answer100007" readonly></textarea>`)), keyword: "不可写"},
		{name: "禁用 fieldset", page: workPageWithQuestions(
			workSingleChoiceQuestionHTML() + `<fieldset disabled>` +
				workSubjectiveQuestion("100004", "4", "[简答题]", `<textarea name="answer100004"></textarea>`) + `</fieldset>`),
			keyword: "不可写"},
		{name: "字段冲突", page: workPageWithQuestions(
			workSingleChoiceQuestionHTML() + workSubjectiveQuestion("100006", "6", "[论述题]",
				`<textarea name="answer100006"></textarea><textarea name="answerEditor100006"></textarea>`)), keyword: "字段"},
		{name: "标签不一致", page: workPageWithQuestions(
			workSingleChoiceQuestionHTML() + workSubjectiveQuestion("100005", "5", "[单选题]",
				`<textarea name="answer100005"></textarea>`)), keyword: "标签"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newWorkFixture(t)
			fixture.pageHTML = testCase.page
			_, err := fixture.platform(t).ReadWork(context.Background(), encodeTestSession(t), workTaskRef())
			failure := requireWorkFailure(t, err, learning.FailureCapabilityMissing)
			if failure.Detail == "" || !strings.Contains(failure.Detail, testCase.keyword) {
				t.Fatalf("失败原因不是结构判定: %v", err)
			}
			if strings.Contains(failure.Detail, "暂不支持") {
				t.Fatalf("主观题仍被当作不支持的题型: %v", err)
			}
		})
	}
}

// 简答题的真实字段可能是富文本编辑器字段，提交只能写页面已有字段。
func TestWorkShortanswerUsesEditorField(t *testing.T) {
	fixture := newWorkFixture(t)
	fixture.pageHTML = workPageWithQuestions(
		workSubjectiveQuestion("110005", "4", "[简答题]", `<textarea id="answerEditor110005" name="answerEditor110005"></textarea>`))
	platform := fixture.platform(t)
	session := encodeTestSession(t)
	sheet, err := platform.ReadWork(context.Background(), session, workTaskRef())
	if err != nil {
		t.Fatal(err)
	}
	submission, err := platform.SubmitWork(context.Background(), session, workTaskRef(), sheet.Fingerprint,
		[]learning.WorkAnswer{{QuestionID: "110005", Values: []string{"编辑器中立答案"}}}, nil)
	if err != nil || !submission.Accepted {
		t.Fatalf("提交结果错误: %+v %v", submission, err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.lastForm.Get("answerEditor110005") != "编辑器中立答案" {
		t.Fatalf("未写入编辑器字段: %v", fixture.lastForm)
	}
	if _, exists := fixture.lastForm["answer110005"]; exists {
		t.Fatalf("提交伪造了页面没有的字段: %v", fixture.lastForm)
	}
}

// 可选附件控件不阻止文字作答，必填上传与不可写字段才停止。
func TestWorkSubjectiveOptionalUploadAllowsTextAnswer(t *testing.T) {
	cases := []struct {
		name, code, label, field string
	}{
		{"简答", "4", "[简答题]", `<input type="file" name="attachment"><textarea name="answer100004"></textarea>`},
		{"名词解释", "5", "[名词解释]", `<input type="file" name="attachment"><textarea id="answerEditor100005" name="answerEditor100005"></textarea>`},
		{"论述", "6", "[论述题]", `<input type="file" name="attachment"><textarea name="answer100006"></textarea>`},
		{"计算", "7", "[计算题]", `<input type="file" name="attachment"><textarea name="answer100007"></textarea>`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			id := map[string]string{"4": "100004", "5": "100005", "6": "100006", "7": "100007"}[testCase.code]
			fixture := newWorkFixture(t)
			fixture.pageHTML = workPageWithQuestions(
				workSubjectiveQuestion(id, testCase.code, testCase.label, testCase.field))
			platform := fixture.platform(t)
			session := encodeTestSession(t)
			sheet, err := platform.ReadWork(context.Background(), session, workTaskRef())
			if err != nil {
				t.Fatal(err)
			}
			if len(sheet.Questions) != 1 || sheet.Questions[0].Type != "shortanswer" {
				t.Fatalf("可选附件主观题读取错误: %+v", sheet.Questions)
			}
			submission, err := platform.SubmitWork(context.Background(), session, workTaskRef(), sheet.Fingerprint,
				[]learning.WorkAnswer{{QuestionID: id, Values: []string{"文字答案"}}}, nil)
			if err != nil || !submission.Accepted {
				t.Fatalf("提交结果错误: %+v %v", submission, err)
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			field := "answer" + id
			if testCase.code == "5" {
				field = "answerEditor" + id
			}
			if fixture.lastForm.Get(field) != "文字答案" {
				t.Fatalf("文字答案未写入真实字段: %v", fixture.lastForm)
			}
			if _, exists := fixture.lastForm["attachment"]; exists {
				t.Fatalf("提交包含附件控件: %v", fixture.lastForm)
			}
			if got := fixture.lastForm.Get("answertype" + id); got != testCase.code {
				t.Fatalf("题型码未保留平台原值: %q", got)
			}
		})
	}
}

// 平台在读取试卷时轮换 Cookie：同一次读取的图片与最终提交都必须带新 Cookie。
func TestWorkSessionCookieSurvivesReadAndSubmit(t *testing.T) {
	cases := []struct {
		name string
		page func() string
	}{
		{name: "含图片", page: func() string { return workPageHTML() }},
		{name: "无图片直接提交", page: func() string {
			page := strings.ReplaceAll(workPageHTML(), `<img src="/img/1.png">`, "")
			return strings.ReplaceAll(page, `<img src="/img/2.png">`, "")
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newWorkFixture(t)
			fixture.pageHTML = testCase.page()
			platform := fixture.platform(t)
			session := encodeTestSession(t)
			sheet, err := platform.ReadWork(context.Background(), session, workTaskRef())
			if err != nil {
				t.Fatal(err)
			}
			if len(sheet.Questions) != 5 || sheet.Fingerprint == "" {
				t.Fatalf("试卷读取错误: %+v", sheet)
			}
			submission, err := platform.SubmitWork(context.Background(), session, workTaskRef(), sheet.Fingerprint, workAnswers(), nil)
			if err != nil || !submission.Accepted {
				t.Fatalf("提交结果错误: %+v %v", submission, err)
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if fixture.rejectedImage != 0 || fixture.rejectedSubmit != 0 {
				t.Fatalf("请求缺少轮换后的 Cookie: image=%d submit=%d", fixture.rejectedImage, fixture.rejectedSubmit)
			}
			if fixture.postCount != 1 || fixture.submitCookie == "" || fixture.submitCookie != fixture.quizCookie {
				t.Fatalf("最终提交未携带本次读取的 Cookie: submit=%q current=%q", fixture.submitCookie, fixture.quizCookie)
			}
		})
	}
}

// BeginWorkSession 让同一次作答的取题与最终提交共享 Cookie 容器；
// 没有父作用域时两次调用各自隔离，读取时下发的会话 Cookie 无法带到提交。
func TestWorkBeginSessionKeepsCookieAcrossMethods(t *testing.T) {
	session := encodeTestSession(t)

	isolated := newWorkFixture(t)
	isolated.requireScope = true
	isolatedPlatform := isolated.platform(t)
	sheet, err := isolatedPlatform.ReadWork(context.Background(), session, workTaskRef())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := isolatedPlatform.SubmitWork(context.Background(), session, workTaskRef(), sheet.Fingerprint, workAnswers(), nil); err == nil {
		t.Fatal("没有作答作用域时提交不应成功")
	}
	isolated.mu.Lock()
	isolatedRejected := isolated.rejectedScope
	isolatedPostCount := isolated.postCount
	isolated.mu.Unlock()
	if isolatedRejected != 1 || isolatedPostCount != 1 {
		t.Fatalf("隔离调用未按预期拒绝: scope=%d post=%d", isolatedRejected, isolatedPostCount)
	}

	scoped := newWorkFixture(t)
	scoped.requireScope = true
	scopedPlatform := scoped.platform(t)
	sessionCtx, cleanup, err := scopedPlatform.BeginWorkSession(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	sheet, err = scopedPlatform.ReadWork(sessionCtx, session, workTaskRef())
	if err != nil {
		t.Fatal(err)
	}
	submission, err := scopedPlatform.SubmitWork(sessionCtx, session, workTaskRef(), sheet.Fingerprint, workAnswers(), nil)
	if err != nil || !submission.Accepted {
		t.Fatalf("共享作用域提交结果错误: %+v %v", submission, err)
	}
	scoped.mu.Lock()
	defer scoped.mu.Unlock()
	if scoped.rejectedScope != 0 || scoped.rejectedSubmit != 0 || scoped.postCount != 1 {
		t.Fatalf("共享作用域请求被拒绝: scope=%d submit=%d post=%d",
			scoped.rejectedScope, scoped.rejectedSubmit, scoped.postCount)
	}
	if scoped.scopeIssued != 1 {
		t.Fatalf("会话 Cookie 下发次数异常: %d", scoped.scopeIssued)
	}
	if scoped.submitCookie != scoped.quizCookie {
		t.Fatalf("提交未携带本次读取的 Cookie: %q != %q", scoped.submitCookie, scoped.quizCookie)
	}
}
