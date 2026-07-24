package runtime_test

import (
	"os"
	"testing"

	appruntime "github.com/jay/da-agents/internal/runtime"
)

func TestEnvDetection(t *testing.T) {
	t.Setenv("DA_AGENT_ENV", "")
	t.Setenv("AWS_LAMBDA_FUNCTION_NAME", "")
	t.Setenv("LAMBDA_TASK_ROOT", "")
	if appruntime.IsLambda() {
		t.Fatal("expected local")
	}
	t.Setenv("DA_AGENT_ENV", "lambda")
	if !appruntime.IsLambda() {
		t.Fatal("expected lambda override")
	}
	t.Setenv("DA_AGENT_ENV", "local")
	t.Setenv("AWS_LAMBDA_FUNCTION_NAME", "fn")
	if appruntime.IsLambda() {
		t.Fatal("DA_AGENT_ENV=local should force local")
	}
	_ = os.Unsetenv("DA_AGENT_ENV")
}

func TestRootDirPrefersDAAgentRoot(t *testing.T) {
	t.Setenv("DA_AGENT_ROOT", "/tmp/da-agents-root")
	root, err := appruntime.RootDir()
	if err != nil {
		t.Fatal(err)
	}
	if root != "/tmp/da-agents-root" {
		t.Fatalf("got %s", root)
	}
}
