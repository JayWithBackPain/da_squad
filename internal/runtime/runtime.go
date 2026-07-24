package runtime

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	EnvLocal  = "local"
	EnvLambda = "lambda"
)

// IsLambda reports whether the process is running inside AWS Lambda.
func IsLambda() bool {
	if v := strings.TrimSpace(os.Getenv("DA_AGENT_ENV")); v != "" {
		return strings.EqualFold(v, EnvLambda)
	}
	return os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" || os.Getenv("LAMBDA_TASK_ROOT") != ""
}

// EnvName returns "lambda" or "local".
func EnvName() string {
	if IsLambda() {
		return EnvLambda
	}
	return EnvLocal
}

// RootDir returns the project root used to resolve config/ and queries/.
// Prefer DA_AGENT_ROOT, then LAMBDA_TASK_ROOT, then cwd.
func RootDir() (string, error) {
	if v := strings.TrimSpace(os.Getenv("DA_AGENT_ROOT")); v != "" {
		return filepath.Abs(v)
	}
	if IsLambda() {
		if root := os.Getenv("LAMBDA_TASK_ROOT"); root != "" {
			return root, nil
		}
	}
	return os.Getwd()
}

// Resolve joins RootDir with relative path segments.
func Resolve(parts ...string) (string, error) {
	root, err := RootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{root}, parts...)...), nil
}
