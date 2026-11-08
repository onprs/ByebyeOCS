package chaoxing

import (
	"context"
	"github.com/onprs/ByebyeOCS/server/internal/learning"
	"testing"
)

func TestHTTPExecutorReportsProgressDuringExecution(t *testing.T) {
	f := newHTTPExecutionFixture(t)
	var values []float64
	f.request.ReportObservation = func(_ context.Context, observation learning.Observation) error {
		if observation.Fraction == nil || observation.Source != "chaoxing-http" {
			t.Fatal("观察缺少进度与来源")
		}
		values = append(values, *observation.Fraction)
		return nil
	}
	result, err := f.executor.Execute(context.Background(), f.request)
	if err != nil || !result.Completed || len(values) < 2 || values[0] >= values[len(values)-1] || values[len(values)-1] != 1 || result.Media.Available {
		t.Fatalf("执行中观察没有回传：%v/%+v/%v", values, result, err)
	}
}
