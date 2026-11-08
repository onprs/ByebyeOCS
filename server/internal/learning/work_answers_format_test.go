package learning

import "testing"

func TestWorkAnswerLabelAndTextMustAgree(t *testing.T) {
	options := []WorkOption{{Label: "A", Value: "wire-voltage", Text: "电压"}, {Label: "B", Value: "wire-current", Text: "电流"}}
	for _, raw := range []string{"A. 电压", "a：电压", "A、电压", "A) 电压", "A 电压", "电压", "A"} {
		answer, err := MapWorkAnswer(WorkQuestion{ID: "choice", Type: "single", Options: options}, raw)
		if err != nil || len(answer.Values) != 1 || answer.Values[0] != "wire-voltage" {
			t.Fatalf("完整答案被拒绝：%q/%+v/%v", raw, answer, err)
		}
	}
	for _, raw := range []string{"B. 电压", "A. 电流", "W", "WHO"} {
		if _, err := MapWorkAnswer(WorkQuestion{ID: "choice", Type: "single", Options: options}, raw); err == nil {
			t.Fatalf("冲突或不完整答案被猜测：%q", raw)
		}
	}
	shuffled := []WorkOption{{Label: "A", Value: "B", Text: "B 非线性区"}, {Label: "B", Value: "A", Text: "A 线性区"}, {Label: "C", Value: "C", Text: "C 放大区"}}
	answer, err := MapWorkAnswer(WorkQuestion{ID: "shuffled", Type: "single", Options: shuffled}, "A. 非线性区")
	if err != nil || answer.Values[0] != "B" {
		t.Fatalf("重排选项未保持平台值：%+v/%v", answer, err)
	}
	judgement := []WorkOption{{Label: "A", Value: "true", Text: "正确"}, {Label: "B", Value: "false", Text: "错误"}}
	answer, err = MapWorkAnswer(WorkQuestion{ID: "judgement", Type: "judgement", Options: judgement}, "B. 错")
	if err != nil || answer.Values[0] != "false" {
		t.Fatalf("带标签判断答案未识别：%+v/%v", answer, err)
	}
}
