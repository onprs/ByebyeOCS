package learning

import (
	"testing"

	"github.com/onprs/ByebyeOCS/server/internal/platform/runtimeconfig"
)

func TestRuntimeConcurrencyOverridesStaticDefaults(t *testing.T) {
	settings := runtimeconfig.NewLearning(8, 5, 12, 0, 0)
	service := &Service{opts: Options{GlobalConcurrency: 4, AccountConcurrency: 1, Runtime: settings}}
	if service.globalLimit() != 8 || service.accountLimit() != 5 {
		t.Fatalf("运行中并发未覆盖静态默认值：global=%d account=%d", service.globalLimit(), service.accountLimit())
	}
	settings.Set(6, 2, 12, 0, 0)
	if service.globalLimit() != 6 || service.accountLimit() != 2 {
		t.Fatalf("并发修改未即时生效：global=%d account=%d", service.globalLimit(), service.accountLimit())
	}
}
