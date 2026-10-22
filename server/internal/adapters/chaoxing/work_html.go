package chaoxing

import (
	"bytes"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	xhtml "golang.org/x/net/html"

	"github.com/onprs/ByebyeOCS/server/internal/learning"
)

// 页面解析使用标准 HTML 解析器，文本节点与元素节点按原始顺序保存在 children 中。
type htmlNode struct {
	tag      string
	attrs    map[string]string
	children []*htmlNode
	text     string
}

var htmlBlockTags = map[string]bool{
	"address": true, "article": true, "aside": true, "blockquote": true, "body": true, "br": true,
	"div": true, "footer": true, "h1": true, "h2": true, "h3": true, "h4": true,
	"h5": true, "h6": true, "head": true, "header": true, "html": true, "li": true,
	"main": true, "nav": true, "ol": true, "p": true, "section": true, "table": true,
	"td": true, "th": true, "title": true, "tr": true, "ul": true,
}

func parseHTMLDocument(raw []byte) *htmlNode {
	document, err := xhtml.Parse(bytes.NewReader(raw))
	if err != nil {
		return &htmlNode{tag: "#document", attrs: map[string]string{}}
	}
	converted := convertHTMLNode(document)
	if converted == nil {
		return &htmlNode{tag: "#document", attrs: map[string]string{}}
	}
	return converted
}

func convertHTMLNode(node *xhtml.Node) *htmlNode {
	converted := &htmlNode{tag: "#document", attrs: map[string]string{}}
	switch node.Type {
	case xhtml.DocumentNode:
	case xhtml.ElementNode:
		converted.tag = strings.ToLower(node.Data)
		for _, attribute := range node.Attr {
			converted.attrs[strings.ToLower(attribute.Key)] = attribute.Val
		}
	case xhtml.TextNode:
		converted.tag = "#text"
		converted.text = node.Data
	case xhtml.CommentNode, xhtml.DoctypeNode, xhtml.ErrorNode:
		return nil
	default:
		return nil
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if item := convertHTMLNode(child); item != nil {
			converted.children = append(converted.children, item)
		}
	}
	return converted
}

func (n *htmlNode) attr(name string) string {
	if n == nil {
		return ""
	}
	return n.attrs[strings.ToLower(name)]
}

func (n *htmlNode) hasClass(name string) bool {
	if n == nil {
		return false
	}
	for _, item := range strings.Fields(n.attr("class")) {
		if item == name {
			return true
		}
	}
	return false
}

func (n *htmlNode) walk(visit func(*htmlNode) bool) {
	if n == nil {
		return
	}
	if !visit(n) {
		return
	}
	for _, child := range n.children {
		child.walk(visit)
	}
}

func (n *htmlNode) first(match func(*htmlNode) bool) *htmlNode {
	var found *htmlNode
	n.walk(func(item *htmlNode) bool {
		if found != nil {
			return false
		}
		if item != n && match(item) {
			found = item
			return false
		}
		return true
	})
	return found
}

func (n *htmlNode) firstByClass(names ...string) *htmlNode {
	return n.first(func(item *htmlNode) bool {
		for _, name := range names {
			if item.hasClass(name) {
				return true
			}
		}
		return false
	})
}

func cleanWorkText(value string) string {
	value = strings.ReplaceAll(value, "\u00a0", " ")
	value = strings.ReplaceAll(value, "\u200b", "")
	value = strings.ReplaceAll(value, "\u200c", "")
	value = strings.ReplaceAll(value, "\u200d", "")
	return strings.Join(strings.Fields(value), " ")
}

func htmlNodeText(n *htmlNode) string {
	var builder strings.Builder
	appendHTMLNodeText(&builder, n)
	return cleanWorkText(builder.String())
}

func appendHTMLNodeText(builder *strings.Builder, n *htmlNode) {
	if n == nil || n.tag == "script" || n.tag == "style" || n.tag == "noscript" || n.tag == "template" {
		return
	}
	block := htmlBlockTags[n.tag]
	if block {
		builder.WriteByte(' ')
	}
	builder.WriteString(n.text)
	for _, child := range n.children {
		appendHTMLNodeText(builder, child)
	}
	if block {
		builder.WriteByte(' ')
	}
}

func isQuestionContainer(n *htmlNode) bool {
	return n.tag == "div" && n.hasClass("singleQuesId")
}

func collectQuestionNodes(root *htmlNode) []*htmlNode {
	var nodes []*htmlNode
	root.walk(func(n *htmlNode) bool {
		if n != root && isQuestionContainer(n) {
			nodes = append(nodes, n)
			return false
		}
		return true
	})
	return nodes
}

func hasAnswerTypePrefix(value string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "answertype")
}

func parseQuestionID(n *htmlNode) (string, error) {
	if !n.hasClass("singleQuesId") {
		return "", learning.Fail(learning.FailureCapabilityMissing, "测验题目容器结构未识别")
	}
	id := strings.TrimSpace(n.attr("data"))
	if id == "" || !safeWorkToken(id) {
		return "", learning.Fail(learning.FailureCapabilityMissing, "测验题目编号结构未识别")
	}
	mismatch := false
	lowerID := strings.ToLower(id)
	n.walk(func(item *htmlNode) bool {
		if mismatch {
			return false
		}
		if item.tag != "input" && item.tag != "textarea" {
			return true
		}
		for _, value := range []string{item.attr("id"), item.attr("name")} {
			lower := strings.ToLower(strings.TrimSpace(value))
			if strings.HasPrefix(lower, "answertype") {
				suffix := strings.TrimSpace(lower[len("answertype"):])
				if suffix != "" && !strings.EqualFold(suffix, id) {
					mismatch = true
					return false
				}
				continue
			}
			if !strings.HasPrefix(lower, "answer") || strings.HasPrefix(lower, "answercheck") {
				continue
			}
			rest := strings.TrimPrefix(lower, "answer")
			if rest == "" || rest[0] < '0' || rest[0] > '9' {
				continue
			}
			if !strings.HasPrefix(rest, lowerID) ||
				(len(rest) > len(lowerID) && !isDigits(rest[len(lowerID):])) {
				mismatch = true
				return false
			}
		}
		return true
	})
	if mismatch {
		return "", learning.Fail(learning.FailureCapabilityMissing, "测验题目编号与表单不一致")
	}
	return id, nil
}

var (
	optionPrefix             = regexp.MustCompile(`^([A-Za-z])[、.．:：)）]\s*`)
	typeLabelPattern         = regexp.MustCompile(`(单选题|单项选择题|多选题|多项选择题|判断题|填空题|简答题|名词解释|论述题|计算题)`)
	workEncryptedFontPattern = regexp.MustCompile(`(?i)(id\s*=\s*["']cxsecretstyle["']|class\s*=\s*["'][^"']*font-cxsecret)`)
)

func workPageHasEncryptedFont(payload []byte) bool {
	return workEncryptedFontPattern.Match(payload)
}

func containsPrivateUse(value string) bool {
	for _, r := range value {
		if r >= 0xE000 && r <= 0xF8FF || r >= 0xF0000 && r <= 0xFFFFD || r >= 0x100000 && r <= 0x10FFFD {
			return true
		}
	}
	return false
}

func safeWorkToken(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isContentEditable(n *htmlNode) bool {
	value, exists := n.attrs["contenteditable"]
	if !exists {
		return false
	}
	return value == "" || strings.EqualFold(value, "true") || strings.EqualFold(value, "plaintext-only")
}

type workType struct {
	Code string
	Kind string
}

func parseQuestionType(n *htmlNode) (workType, error) {
	var typeNode *htmlNode
	if n.hasClass("TiMu") && isTypeCode(n.attr("data")) {
		typeNode = n
	} else {
		n.walk(func(item *htmlNode) bool {
			if typeNode != nil {
				return false
			}
			if item == n {
				return true
			}
			if item.tag == "div" && item.hasClass("TiMu") && isTypeCode(item.attr("data")) {
				typeNode = item
				return false
			}
			return true
		})
	}
	if typeNode == nil {
		return workType{}, learning.Fail(learning.FailureCapabilityMissing, "测验题型结构未识别")
	}
	code := strings.TrimSpace(typeNode.attr("data"))
	kind, err := workTypeFromCode(code)
	if err != nil {
		return workType{}, err
	}
	var fromInput string
	n.walk(func(item *htmlNode) bool {
		if fromInput != "" || item.tag != "input" {
			return fromInput == ""
		}
		for _, value := range []string{item.attr("id"), item.attr("name")} {
			if !hasAnswerTypePrefix(value) {
				continue
			}
			fromInput = strings.TrimSpace(item.attr("value"))
			return false
		}
		return true
	})
	if fromInput != "" && fromInput != code {
		return workType{}, learning.Fail(learning.FailureCapabilityMissing, "测验题型与表单不一致")
	}
	for _, attribute := range []string{"typename", "data-type", "data-typename"} {
		if candidate := strings.TrimSpace(n.attr(attribute)); candidate != "" {
			if mapped, err := workTypeFromLabel(candidate); err == nil && !workTypeLabelCompatible(code, mapped) {
				return workType{}, learning.Fail(learning.FailureCapabilityMissing, "测验题型与标签不一致")
			}
		}
	}
	labelNode := n.firstByClass("newZy_TItle", "mark_name", "question-type", "type-tag")
	if labelNode != nil {
		if mapped, err := workTypeFromLabel(htmlNodeText(labelNode)); err == nil && !workTypeLabelCompatible(code, mapped) {
			return workType{}, learning.Fail(learning.FailureCapabilityMissing, "测验题型与标签不一致")
		}
	}
	return kind, nil
}

func isTypeCode(value string) bool {
	value = strings.TrimSpace(value)
	return len(value) == 1 && value[0] >= '0' && value[0] <= '7'
}

func workTypeFromCode(code string) (workType, error) {
	switch strings.TrimSpace(code) {
	case "0":
		return workType{Code: "0", Kind: "single"}, nil
	case "1":
		return workType{Code: "1", Kind: "multiple"}, nil
	case "2":
		return workType{Code: "2", Kind: "completion"}, nil
	case "3":
		return workType{Code: "3", Kind: "judgement"}, nil
	case "4", "5", "6", "7":
		// 简答、名词解释、论述、计算在作答形式上同为文字主观题；原始题型码仍按平台值提交。
		return workType{Code: strings.TrimSpace(code), Kind: "shortanswer"}, nil
	}
	return workType{}, learning.Fail(learning.FailureCapabilityMissing, "测验题型代码未识别")
}

// workTextAnswerCode 判断题型码是否属于文字主观题族（简答、名词解释、论述、计算）。
func workTextAnswerCode(code string) bool {
	return len(code) == 1 && code[0] >= '4' && code[0] <= '7'
}

// workTypeLabelCompatible 允许主观题族内的标签与题型码存在版本差异，其他不一致仍然停止。
func workTypeLabelCompatible(code string, mapped workType) bool {
	if mapped.Code == code {
		return true
	}
	return workTextAnswerCode(code) && workTextAnswerCode(mapped.Code)
}

func workTypeFromLabel(value string) (workType, error) {
	value = cleanWorkText(value)
	switch {
	case strings.Contains(value, "多选") || strings.Contains(value, "多项选择"):
		return workType{Code: "1", Kind: "multiple"}, nil
	case strings.Contains(value, "判断"):
		return workType{Code: "3", Kind: "judgement"}, nil
	case strings.Contains(value, "填空"):
		return workType{Code: "2", Kind: "completion"}, nil
	case strings.Contains(value, "单选") || strings.Contains(value, "单项选择"):
		return workType{Code: "0", Kind: "single"}, nil
	case strings.Contains(value, "名词解释"):
		return workType{Code: "5", Kind: "shortanswer"}, nil
	case strings.Contains(value, "论述"):
		return workType{Code: "6", Kind: "shortanswer"}, nil
	case strings.Contains(value, "计算"):
		return workType{Code: "7", Kind: "shortanswer"}, nil
	case strings.Contains(value, "简答"):
		return workType{Code: "4", Kind: "shortanswer"}, nil
	}
	if match := typeLabelPattern.FindString(value); match != "" {
		return workTypeFromLabel(match)
	}
	return workType{}, learning.Fail(learning.FailureCapabilityMissing, "测验题型标签未识别")
}

func parseQuestionTitle(n *htmlNode) (string, error) {
	node := n.firstByClass("Zy_TItle")
	if node == nil {
		return "", learning.Fail(learning.FailureCapabilityMissing, "测验题干结构未识别")
	}
	title := htmlNodeText(node)
	if len(title) > 16384 {
		return "", learning.Fail(learning.FailureCapabilityMissing, "测验题干内容超出限制")
	}
	return title, nil
}

func parseWorkOptions(n *htmlNode, questionID string, kind string) ([]learning.WorkOption, error) {
	var candidates []*htmlNode
	n.walk(func(item *htmlNode) bool {
		if item == n {
			return true
		}
		if isOptionCandidate(item) {
			candidates = append(candidates, item)
			return false
		}
		return true
	})
	candidates = outermostNodes(candidates)
	if kind == "completion" || kind == "shortanswer" {
		if len(candidates) != 0 {
			return nil, learning.Fail(learning.FailureCapabilityMissing, "主观题或填空题结构发生变化")
		}
		return nil, nil
	}
	if len(candidates) == 0 {
		return nil, learning.Fail(learning.FailureCapabilityMissing, "测验选项结构未识别")
	}
	if len(candidates) > 26 {
		return nil, learning.Fail(learning.FailureCapabilityMissing, "测验选项数量超出限制")
	}
	options := make([]learning.WorkOption, 0, len(candidates))
	seen := map[string]bool{}
	for _, candidate := range candidates {
		option, err := parseWorkOption(candidate)
		if err != nil {
			return nil, err
		}
		if seen[option.Value] {
			return nil, learning.Fail(learning.FailureCapabilityMissing, "测验选项提交值重复")
		}
		seen[option.Value] = true
		options = append(options, option)
	}
	if len(options) < 2 {
		return nil, learning.Fail(learning.FailureCapabilityMissing, "测验选项数量不足")
	}
	if kind == "judgement" && len(options) != 2 {
		return nil, learning.Fail(learning.FailureCapabilityMissing, "判断题选项数量异常")
	}
	return options, nil
}

func isOptionCandidate(n *htmlNode) bool {
	if n.tag == "li" || n.hasClass("answerBg") || n.hasClass("more-choose-item") {
		if n.hasClass("answerBg") || n.hasClass("more-choose-item") {
			return true
		}
		return n.first(func(item *htmlNode) bool {
			if item == n {
				return false
			}
			if item.hasClass("num_option") || item.hasClass("num_option_dx") || item.tag == "em" && item.attr("id-param") != "" {
				return true
			}
			return item.tag == "input" && (strings.EqualFold(item.attr("type"), "radio") ||
				strings.EqualFold(item.attr("type"), "checkbox")) && strings.HasPrefix(strings.ToLower(item.attr("name")), "answer")
		}) != nil
	}
	return false
}

func outermostNodes(nodes []*htmlNode) []*htmlNode {
	result := make([]*htmlNode, 0, len(nodes))
	for _, node := range nodes {
		nested := false
		for _, other := range nodes {
			if other != node && nodeIsDescendant(node, other) {
				nested = true
				break
			}
		}
		if !nested {
			result = append(result, node)
		}
	}
	return result
}

func nodeIsDescendant(node, ancestor *htmlNode) bool {
	found := false
	ancestor.walk(func(item *htmlNode) bool {
		if found {
			return false
		}
		if item == node {
			found = true
			return false
		}
		return true
	})
	return found
}

func parseWorkOption(n *htmlNode) (learning.WorkOption, error) {
	var input *htmlNode
	n.walk(func(item *htmlNode) bool {
		if input != nil {
			return false
		}
		if item.tag == "input" && (strings.EqualFold(item.attr("type"), "radio") ||
			strings.EqualFold(item.attr("type"), "checkbox")) {
			if strings.HasPrefix(strings.ToLower(item.attr("name")), "answer") {
				input = item
				return false
			}
		}
		return true
	})
	marker := n.first(func(item *htmlNode) bool {
		if item == n {
			return false
		}
		return item.hasClass("num_option") || item.hasClass("num_option_dx") ||
			item.tag == "em" && item.attr("id-param") != ""
	})
	value := ""
	inputValue := ""
	if input != nil {
		inputValue = strings.TrimSpace(input.attr("value"))
	}
	markerValue := ""
	label := ""
	if marker != nil {
		label = htmlNodeText(marker)
		markerValue = strings.TrimSpace(marker.attr("data"))
		if markerValue == "" {
			markerValue = strings.TrimSpace(marker.attr("id-param"))
		}
	}
	if inputValue != "" && markerValue != "" && inputValue != markerValue {
		return learning.WorkOption{}, learning.Fail(learning.FailureCapabilityMissing, "测验选项提交值不一致")
	}
	value = inputValue
	if value == "" {
		value = markerValue
	}
	text := cleanWorkText(n.attr("aria-label"))
	fromAria := text != ""
	if text == "" {
		text = htmlNodeText(n)
	}
	if value == "" {
		return learning.WorkOption{}, learning.Fail(learning.FailureCapabilityMissing, "测验选项缺少平台提交值")
	}
	value = cleanWorkText(value)
	if len(value) > 128 {
		return learning.WorkOption{}, learning.Fail(learning.FailureCapabilityMissing, "测验选项提交值超出限制")
	}
	label = cleanWorkText(label)
	if label == "" {
		if len(value) == 1 && value[0] >= 'A' && value[0] <= 'Z' {
			label = value
		} else if strings.EqualFold(value, "true") {
			label = "正确"
		} else if strings.EqualFold(value, "false") {
			label = "错误"
		}
	}
	text = stripOptionText(text, label, marker != nil && !fromAria)
	if strings.HasSuffix(text, "选择") {
		text = strings.TrimSpace(strings.TrimSuffix(text, "选择"))
	}
	if text == "" {
		text = label
	}
	if len(label) > 128 || len(text) > 4096 {
		return learning.WorkOption{}, learning.Fail(learning.FailureCapabilityMissing, "测验选项内容超出限制")
	}
	return learning.WorkOption{Label: label, Value: value, Text: text}, nil
}

func stripOptionText(text, label string, stripAlways bool) string {
	text = cleanWorkText(text)
	if label != "" && strings.HasPrefix(text, label) {
		rawRest := strings.TrimPrefix(text, label)
		rest := strings.TrimSpace(rawRest)
		if rest == "" {
			return ""
		}
		hadSeparator := rawRest != "" && (isWorkSpace(rawRest[0]) || strings.ContainsRune("、.．:：)）]】", rune(rawRest[0])))
		if stripAlways || hadSeparator {
			if strings.ContainsRune("、.．:：)）]】", rune(rest[0])) {
				rest = strings.TrimSpace(rest[1:])
			}
			text = rest
		}
	}
	if match := optionPrefix.FindStringSubmatch(text); len(match) == 2 {
		text = strings.TrimSpace(text[len(match[0]):])
	}
	return text
}

func isWorkSpace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\r' || value == '\n'
}

// workBlankPlan 记录空位数量与页面上的真实提交字段；提交只写入页面已有的字段名。
type workBlankPlan struct {
	Count  int
	Style  string
	Fields []string
}

// workBlankField 从表单字段名识别填空题空位序号。
// 序号 0 表示不编号的单字段写法，大于 0 表示分空编号写法；返回的 ok 只说明命名规则匹配。
func workBlankField(name, lowerID string) (int, bool) {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" || lowerID == "" {
		return 0, false
	}
	// answereditor 是平台富文本空位的前缀，必须先于 answer 判断。
	for _, prefix := range []string{"answereditor", "answer"} {
		if !strings.HasPrefix(lower, prefix) {
			continue
		}
		rest := strings.TrimPrefix(lower, prefix)
		if rest == lowerID {
			return 0, true
		}
		if !strings.HasPrefix(rest, lowerID) {
			return 0, false
		}
		suffix := strings.TrimPrefix(rest, lowerID)
		if suffix == "" {
			return 0, true
		}
		if !isDigits(suffix) {
			return 0, false
		}
		index, err := strconv.Atoi(suffix)
		if err != nil || index <= 0 || index > 20 {
			return 0, false
		}
		return index, true
	}
	return 0, false
}

func parseWorkBlanks(question *htmlNode, form *htmlNode, questionID, kind string) (workBlankPlan, error) {
	if kind != "completion" {
		return workBlankPlan{}, nil
	}
	lowerID := strings.ToLower(questionID)
	size := 0
	if form != nil {
		form.walk(func(item *htmlNode) bool {
			if item.tag != "input" || strings.ToLower(strings.TrimSpace(item.attr("name"))) != "tiankongsize"+lowerID {
				return true
			}
			if parsed, err := strconv.Atoi(strings.TrimSpace(item.attr("value"))); err == nil && parsed > 0 {
				size = parsed
			}
			return false
		})
	}
	// 编号字段按空位序号去重；未编号字段按页面出现顺序记录，允许同一字段名重复提交多个空位。
	numbered := map[int]string{}
	plain := make([]string, 0, 2)
	maxIndex := 0
	mobileBlanks := 0
	var conflict *learning.Failure
	question.walk(func(item *htmlNode) bool {
		if conflict != nil {
			return false
		}
		if item.tag != "input" && item.tag != "textarea" && !isContentEditable(item) {
			return true
		}
		if item.hasClass("blankInp2") {
			mobileBlanks++
		}
		name := strings.TrimSpace(item.attr("name"))
		index, matched := workBlankField(name, lowerID)
		if !matched {
			// 缺少可提交名称时只用 id 识别空位数量。
			index, matched = workBlankField(item.attr("id"), lowerID)
			if !matched {
				return true
			}
			name = ""
		}
		if index == 0 {
			plain = append(plain, name)
			return true
		}
		if index > maxIndex {
			maxIndex = index
		}
		if current, exists := numbered[index]; exists && current != name {
			conflict = learning.Fail(learning.FailureCapabilityMissing, "填空题空位字段不一致")
			return false
		}
		numbered[index] = name
		return true
	})
	if conflict != nil {
		return workBlankPlan{}, conflict
	}
	if list := question.first(func(item *htmlNode) bool { return item.hasClass("blankList2") }); list != nil {
		count := 0
		for _, child := range list.children {
			if child.tag == "li" {
				count++
			}
		}
		if count > 0 {
			mobileBlanks = count
		}
	}
	if size == 0 && maxIndex == 0 && mobileBlanks == 0 && len(plain) == 0 && len(numbered) == 0 {
		return workBlankPlan{}, learning.Fail(learning.FailureCapabilityMissing, "填空题缺少可识别的作答空位")
	}
	if size > 0 && maxIndex > 0 && size != maxIndex {
		return workBlankPlan{}, learning.Fail(learning.FailureCapabilityMissing, "填空题空位数量与表单不一致")
	}
	if size > 0 && mobileBlanks > 0 && size != mobileBlanks {
		return workBlankPlan{}, learning.Fail(learning.FailureCapabilityMissing, "填空题空位数量与页面不一致")
	}
	if len(plain) > 0 && (len(numbered) > 0 || maxIndex > 0) {
		return workBlankPlan{}, learning.Fail(learning.FailureCapabilityMissing, "填空题空位字段不一致")
	}
	count := max(size, maxIndex, mobileBlanks, len(plain), len(numbered))
	if count == 0 {
		return workBlankPlan{}, learning.Fail(learning.FailureCapabilityMissing, "填空题缺少可识别的作答空位")
	}
	if count > 20 {
		return workBlankPlan{}, learning.Fail(learning.FailureCapabilityMissing, "填空题空位数量超出限制")
	}
	plan := workBlankPlan{Count: count, Style: "indexed"}
	if len(plain) > 0 {
		if len(plain) != count {
			return workBlankPlan{}, learning.Fail(learning.FailureCapabilityMissing, "填空题空位数量与表单不一致")
		}
		plan.Fields = plain
		if count == 1 {
			plan.Style = "single"
		}
	} else {
		plan.Fields = make([]string, 0, count)
		for index := 1; index <= count; index++ {
			plan.Fields = append(plan.Fields, numbered[index])
		}
	}
	for _, name := range plan.Fields {
		if name == "" {
			return workBlankPlan{}, learning.Fail(learning.FailureCapabilityMissing, "填空题空位字段未识别")
		}
	}
	return plan, nil
}

// workAnswerFieldName 判断字段名是否是不分空的文字作答字段（简答、名词解释、论述、计算）。
func workAnswerFieldName(name, lowerID string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" || lowerID == "" {
		return false
	}
	return lower == "answer"+lowerID || lower == "answereditor"+lowerID
}

// workDisabledContainers 标记位于禁用 fieldset 内的节点：禁用 fieldset 会禁用其全部后代控件。
func workDisabledContainers(root *htmlNode) map[*htmlNode]bool {
	disabled := map[*htmlNode]bool{}
	var walk func(*htmlNode, bool)
	walk = func(node *htmlNode, inherited bool) {
		current := inherited
		if node.tag == "fieldset" {
			if _, ok := node.attrs["disabled"]; ok {
				current = true
			}
		}
		if current {
			disabled[node] = true
		}
		for _, child := range node.children {
			walk(child, current)
		}
	}
	walk(root, false)
	return disabled
}

// parseWorkAnswerField 只接受当前页面上真实存在且唯一可写的文字作答字段。
// 可选附件控件不阻止文字作答；必填上传、无可写文本字段或字段冲突时停止。
func parseWorkAnswerField(question *htmlNode, questionID string, disabled map[*htmlNode]bool) (string, error) {
	lowerID := strings.ToLower(questionID)
	fields := map[string]bool{}
	blocked := 0
	requiredUpload := false
	question.walk(func(item *htmlNode) bool {
		if requiredUpload {
			return false
		}
		kind := strings.ToLower(strings.TrimSpace(item.attr("type")))
		if item.tag == "input" && kind == "file" {
			_, required := item.attrs["required"]
			if required || strings.EqualFold(strings.TrimSpace(item.attr("aria-required")), "true") {
				requiredUpload = true
			}
			return true
		}
		if item.tag != "input" && item.tag != "textarea" && !isContentEditable(item) {
			return true
		}
		name := strings.TrimSpace(item.attr("name"))
		if !workAnswerFieldName(name, lowerID) {
			return true
		}
		if item.tag != "textarea" && !isContentEditable(item) &&
			!(item.tag == "input" && (kind == "" || kind == "text" || kind == "hidden")) {
			return true
		}
		// disabled/readonly 是布尔属性，属性存在即不可写；禁用 fieldset 的后代同样不可写。
		_, ownDisabled := item.attrs["disabled"]
		_, readonly := item.attrs["readonly"]
		if ownDisabled || readonly || disabled[item] ||
			strings.EqualFold(strings.TrimSpace(item.attr("aria-disabled")), "true") {
			blocked++
			return true
		}
		fields[name] = true
		return true
	})
	if requiredUpload {
		return "", learning.Fail(learning.FailureCapabilityMissing, "测验主观题要求上传附件，无法按文字作答")
	}
	switch len(fields) {
	case 0:
		if blocked > 0 {
			return "", learning.Fail(learning.FailureCapabilityMissing, "测验主观题作答字段不可写")
		}
		return "", learning.Fail(learning.FailureCapabilityMissing, "测验主观题缺少可写入的文本字段")
	case 1:
		for name := range fields {
			return name, nil
		}
	}
	return "", learning.Fail(learning.FailureCapabilityMissing, "测验主观题作答字段结构不一致")
}

func extractWorkForm(root *htmlNode) (*htmlNode, url.Values, error) {
	var form *htmlNode
	root.walk(func(item *htmlNode) bool {
		if item.tag != "form" {
			return true
		}
		if form == nil || strings.EqualFold(item.attr("id"), "form1") {
			form = item
		}
		if strings.EqualFold(item.attr("id"), "form1") {
			return false
		}
		return true
	})
	if form == nil {
		return nil, nil, nil
	}
	values := url.Values{}
	form.walk(func(item *htmlNode) bool {
		if item.tag != "input" {
			return true
		}
		name := strings.TrimSpace(item.attr("name"))
		if name == "" || len(name) > 128 {
			return true
		}
		lowerName := strings.ToLower(name)
		if strings.HasPrefix(lowerName, "answer") {
			return true
		}
		kind := strings.ToLower(strings.TrimSpace(item.attr("type")))
		switch kind {
		case "submit", "button", "reset", "file", "image", "password":
			return true
		}
		value := item.attr("value")
		if len(value) > 8192 {
			return true
		}
		if kind == "text" && value == "" {
			return true
		}
		values.Add(name, value)
		return true
	})
	return form, values, nil
}

func collectQuestionImageSources(n *htmlNode) []string {
	seen := map[string]bool{}
	var sources []string
	collect := func(node *htmlNode) {
		node.walk(func(item *htmlNode) bool {
			if item.tag != "img" {
				return true
			}
			source := strings.TrimSpace(item.attr("src"))
			if source == "" || strings.HasPrefix(source, "#") ||
				strings.HasPrefix(strings.ToLower(source), "javascript:") ||
				strings.EqualFold(source, "about:blank") {
				return true
			}
			if !seen[source] {
				seen[source] = true
				sources = append(sources, source)
			}
			return true
		})
	}
	// 只读取题干和选项正文中的图片，避免把工具栏图标当成题目内容。
	content := false
	n.walk(func(item *htmlNode) bool {
		if item == n {
			return true
		}
		if item.hasClass("Zy_TItle") || item.hasClass("Py-m1-title") || item.hasClass("mark_name") ||
			item.hasClass("answer_p") || item.hasClass("after") || item.hasClass("choose-desc") {
			content = true
			collect(item)
			return false
		}
		return true
	})
	if !content {
		collect(n)
	}
	return sources
}

func hasWorkEditableControls(form *htmlNode) bool {
	if form == nil {
		return false
	}
	editable := false
	form.walk(func(item *htmlNode) bool {
		if editable {
			return false
		}
		if item.hasClass("num_option") || item.hasClass("num_option_dx") || item.hasClass("answerBg") {
			editable = true
			return false
		}
		if item.tag != "input" && item.tag != "textarea" && !isContentEditable(item) {
			return true
		}
		name := strings.ToLower(strings.TrimSpace(item.attr("name")))
		id := strings.ToLower(strings.TrimSpace(item.attr("id")))
		if !strings.HasPrefix(name, "answer") && !strings.HasPrefix(id, "answereditor") {
			return true
		}
		// disabled/readonly 是布尔属性，浏览器可省略取值，必须按属性存在判断。
		if _, disabled := item.attrs["disabled"]; disabled {
			return true
		}
		if _, readonly := item.attrs["readonly"]; readonly {
			return true
		}
		if strings.EqualFold(strings.TrimSpace(item.attr("aria-disabled")), "true") {
			return true
		}
		if isContentEditable(item) {
			editable = true
			return false
		}
		kind := strings.ToLower(strings.TrimSpace(item.attr("type")))
		if item.tag == "textarea" || kind == "radio" || kind == "checkbox" || kind == "text" || kind == "" {
			editable = true
			return false
		}
		return true
	})
	return editable
}

func firstWorkValue(values url.Values, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(values.Get(name)); value != "" {
			return value
		}
	}
	return ""
}
