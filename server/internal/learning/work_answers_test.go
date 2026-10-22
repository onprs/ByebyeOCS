package learning

import (
	"errors"
	"reflect"
	"testing"
)

func TestMapWorkAnswersUsePlatformValues(t *testing.T) {
	options := []WorkOption{{Label: "A", Value: "option-11", Text: "第一项"},
		{Label: "B", Value: "option-22", Text: "第二项"}, {Label: "C", Value: "option-33", Text: "第三项"}}
	cases := []struct {
		name, kind, raw string
		blanks          int
		options         []WorkOption
		want            []string
	}{
		{"单选标签", "single", "B", 0, options, []string{"option-22"}},
		{"完整选项", "single", "第二项", 0, options, []string{"option-22"}},
		{"标准前缀", "single", "答案：b", 0, options, []string{"option-22"}},
		{"多选按平台排序", "multiple", "C#A", 0, options, []string{"option-11", "option-33"}},
		{"紧凑多选", "multiple", "CA", 0, options, []string{"option-11", "option-33"}},
		{"多选去重", "multiple", "C#A#C", 0, options, []string{"option-11", "option-33"}},
		{"判断标准值", "judgement", "对", 0, nil, []string{"true"}},
		{"判断反向选项", "judgement", "错误", 0, []WorkOption{{Label: "A", Value: "a", Text: "错误"}, {Label: "B", Value: "b", Text: "正确"}}, []string{"a"}},
		{"判断标签", "judgement", "B", 0, []WorkOption{{Label: "A", Value: "true", Text: "正确"}, {Label: "B", Value: "false", Text: "错误"}}, []string{"false"}},
		{"填空顺序", "completion", "甲#乙", 2, nil, []string{"甲", "乙"}},
		{"单空保留字符", "completion", "C#", 1, nil, []string{"C#"}},
		{"简答保留完整内容", "shortanswer", "A 与 B 的关系如下：\n第一行。", 0, nil, []string{"A 与 B 的关系如下：\n第一行。"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := MapWorkAnswer(WorkQuestion{ID: "question-1", Type: tc.kind, Options: tc.options, Blanks: tc.blanks}, tc.raw)
			if err != nil || got.QuestionID != "question-1" || !reflect.DeepEqual(got.Values, tc.want) {
				t.Fatalf("答案映射不正确: %+v, %v", got, err)
			}
		})
	}
}

func TestMapWorkAnswersRejectAmbiguityAndMissingAnswers(t *testing.T) {
	options := []WorkOption{{Label: "A", Value: "1", Text: "相同文本"}, {Label: "B", Value: "2", Text: "相同文本"}}
	cases := []struct {
		name, kind, answer string
		blanks             int
	}{
		{"空答案", "single", "", 0},
		{"模糊文本", "single", "相同", 0},
		{"重复选项文本", "single", "相同文本", 0},
		{"单选多个标签", "single", "A#B", 0},
		{"多选部分无效", "multiple", "A#D", 0},
		{"含糊判断", "judgement", "大概正确", 0},
		{"填空缺项", "completion", "甲", 2},
		{"填空空值", "completion", "甲#", 2},
		{"未知空数", "completion", "甲", 0},
		{"未知题型", "matching", "A", 0},
		{"控制字符", "shortanswer", "正文\x00后续", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := MapWorkAnswer(WorkQuestion{ID: "question-1", Type: tc.kind, Options: options, Blanks: tc.blanks}, tc.answer)
			var failure *Failure
			if !errors.As(err, &failure) {
				t.Fatalf("错误答案没有分类停止: %v", err)
			}
		})
	}
}
