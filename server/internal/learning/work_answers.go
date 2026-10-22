package learning

import (
	"html"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var workAnswerPrefix = regexp.MustCompile(`(?i)^(?:答案|正确答案|answer)\s*[:：]\s*`)

func answerText(value string) string {
	return strings.Join(strings.Fields(html.UnescapeString(strings.TrimSpace(value))), " ")
}

// MapWorkAnswer 只接受无歧义的选项标签、完整选项文本或对应题型的标准答案。
// 选项的排序和原始值来自当前平台试卷，不使用相似度匹配或随机兜底。
func MapWorkAnswer(question WorkQuestion, raw string) (WorkAnswer, error) {
	result := WorkAnswer{QuestionID: question.ID}
	raw = strings.TrimSpace(workAnswerPrefix.ReplaceAllString(strings.TrimSpace(raw), ""))
	if question.ID == "" || raw == "" || !utf8.ValidString(raw) || len(raw) > 32768 ||
		strings.IndexFunc(raw, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' && r != '\r' }) >= 0 {
		return result, Fail(FailureAnswerUnavailable, "题目没有可填写的有效答案")
	}
	switch question.Type {
	case "single":
		value, ok := matchWorkOption(question.Options, raw)
		if !ok {
			return result, Fail(FailureAnswerUnavailable, "单选答案无法唯一对应平台选项")
		}
		result.Values = []string{value}
	case "multiple":
		values, ok := matchMultipleOptions(question.Options, raw)
		if !ok {
			return result, Fail(FailureAnswerUnavailable, "多选答案无法完整对应平台选项")
		}
		result.Values = values
	case "judgement":
		value, ok := judgementValue(raw)
		if !ok {
			if matched, found := matchWorkOption(question.Options, raw); found {
				result.Values = []string{matched}
				return result, nil
			}
			return result, Fail(FailureAnswerUnavailable, "判断答案无法识别")
		}
		if len(question.Options) == 0 {
			result.Values = []string{value}
			return result, nil
		}
		matches := []string{}
		for _, option := range question.Options {
			fromValue, valueOK := judgementValue(option.Value)
			fromText, textOK := judgementValue(option.Text)
			if valueOK && textOK && fromValue != fromText {
				return result, Fail(FailureInvalidTask, "判断选项内容与平台值不一致")
			}
			if (valueOK && fromValue == value) || (textOK && fromText == value) {
				matches = append(matches, option.Value)
			}
		}
		if len(matches) != 1 {
			return result, Fail(FailureAnswerUnavailable, "判断答案无法唯一对应平台选项")
		}
		result.Values = matches
	case "shortanswer":
		result.Values = []string{raw}
	case "completion":
		if question.Blanks < 1 || question.Blanks > 64 {
			return result, Fail(FailureInvalidTask, "填空题的空格数量不明确")
		}
		values := []string{raw}
		if question.Blanks > 1 {
			values = strings.Split(raw, "#")
			if len(values) != question.Blanks {
				return result, Fail(FailureAnswerUnavailable, "填空答案数量与平台空格数量不一致")
			}
		}
		for i, value := range values {
			values[i] = strings.TrimSpace(value)
			if values[i] == "" {
				return result, Fail(FailureAnswerUnavailable, "填空答案存在空值")
			}
		}
		result.Values = values
	default:
		return result, Fail(FailureCapabilityMissing, "当前题型尚不能自动填写")
	}
	return result, nil
}

func judgementValue(raw string) (string, bool) {
	switch strings.ToLower(answerText(raw)) {
	case "true", "对", "正确", "是", "√", "✓":
		return "true", true
	case "false", "错", "错误", "否", "×", "✗":
		return "false", true
	default:
		return "", false
	}
}

func matchWorkOption(options []WorkOption, raw string) (string, bool) {
	label := strings.ToUpper(strings.TrimSpace(raw))
	matched := []string{}
	for _, option := range options {
		if strings.ToUpper(option.Label) == label && option.Label != "" {
			matched = append(matched, option.Value)
		}
	}
	if len(matched) > 0 {
		return matched[0], len(matched) == 1
	}
	for _, option := range options {
		if answerText(option.Text) == answerText(raw) && option.Text != "" {
			matched = append(matched, option.Value)
		}
	}
	if len(matched) != 1 {
		return "", false
	}
	return matched[0], true
}

func matchMultipleOptions(options []WorkOption, raw string) ([]string, bool) {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return strings.ContainsRune("#、,，;；|/\n\r", r)
	})
	if len(parts) == 1 {
		// 模型常以 AC 表示多选；只有各字符都是已有单字母标签时才拆开。
		compact := strings.ToUpper(strings.TrimSpace(parts[0]))
		allLabels := len(compact) > 1
		for _, r := range compact {
			if r < 'A' || r > 'Z' {
				allLabels = false
				break
			}
			found := false
			for _, option := range options {
				if option.Label == string(r) {
					found = true
					break
				}
			}
			allLabels = allLabels && found
		}
		if allLabels {
			parts = make([]string, 0, len(compact))
			for _, r := range compact {
				parts = append(parts, string(r))
			}
		}
	}
	selected := map[string]bool{}
	for _, part := range parts {
		value, ok := matchWorkOption(options, part)
		if !ok || value == "" {
			return nil, false
		}
		selected[value] = true
	}
	if len(selected) == 0 {
		return nil, false
	}
	result := make([]string, 0, len(selected))
	for _, option := range options {
		if selected[option.Value] {
			result = append(result, option.Value)
		}
	}
	return result, len(result) == len(selected)
}
