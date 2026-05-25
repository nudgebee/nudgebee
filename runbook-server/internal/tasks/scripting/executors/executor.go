package executors

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"nudgebee/runbook/internal/tasks/types"
	"regexp"
	"strings"
	"unicode/utf16"
)

var validEnvKeyRegex = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// ScriptExecutor defines the interface for executing scripts.
type ScriptExecutor interface {
	// Execute runs a script with the given configuration and returns the output (stdout/stderr combined).
	Execute(taskCtx types.TaskContext, config ExecutionConfig) (string, error)
}

// ResourceConfig defines the compute resources for the execution.
type ResourceConfig struct {
	CPURequest    string
	CPULimit      string
	MemoryRequest string
	MemoryLimit   string
}

// ExecutionConfig holds the parameters for script execution.
type ExecutionConfig struct {
	ExecutorType    string
	AccountID       string
	AccountProvider string // Provider type for auto-routing (k8s, aws, azure, gcp)
	OSType          string // "linux" or "windows"
	Script          string
	Language        string
	Args            []string
	Env             map[string]string
	Cwd             string
	K8sImage        string          // Used for K8s executor
	K8sResources    *ResourceConfig // Used for K8s executor
	K8sNamespace    *string         // Used for K8s executor

	// Fields for AWS SSM / SSH
	TargetID      string // Instance ID (SSM) or Host (SSH - via Integration?)
	Region        string // AWS Region
	IntegrationID string // For SSH or Cloud Auth
}

// BuildShellEnvPrefix builds a shell-safe "export K='V'; " prefix string.
// Returns an error if any env key contains invalid characters.
func BuildShellEnvPrefix(env map[string]string) (string, error) {
	var sb strings.Builder
	for k, v := range env {
		if !validEnvKeyRegex.MatchString(k) {
			return "", fmt.Errorf("invalid environment variable name: %q", k)
		}
		escapedVal := strings.ReplaceAll(v, "'", "'\\''")
		fmt.Fprintf(&sb, "export %s='%s'; ", k, escapedVal)
	}
	return sb.String(), nil
}

// BuildShellArgsStr builds a shell-safe arguments string with single-quote escaping.
func BuildShellArgsStr(args []string) string {
	var sb strings.Builder
	for _, arg := range args {
		fmt.Fprintf(&sb, " '%s'", strings.ReplaceAll(arg, "'", "'\\''"))
	}
	return sb.String()
}

// BuildPowerShellEnvPrefix builds a PowerShell "$env:VAR = 'val'; " prefix string.
// Single quotes inside values are escaped by doubling them (PowerShell convention).
func BuildPowerShellEnvPrefix(env map[string]string) (string, error) {
	var sb strings.Builder
	for k, v := range env {
		if !validEnvKeyRegex.MatchString(k) {
			return "", fmt.Errorf("invalid environment variable name: %q", k)
		}
		escapedVal := strings.ReplaceAll(v, "'", "''")
		fmt.Fprintf(&sb, "$env:%s = '%s'; ", k, escapedVal)
	}
	return sb.String(), nil
}

// BuildPowerShellArgsStr builds a PowerShell "$nbArgs = @('arg1','arg2'); " statement.
// Uses $nbArgs because $args is reserved in PowerShell.
func BuildPowerShellArgsStr(args []string) string {
	if len(args) == 0 {
		return ""
	}
	escaped := make([]string, len(args))
	for i, arg := range args {
		escaped[i] = fmt.Sprintf("'%s'", strings.ReplaceAll(arg, "'", "''"))
	}
	return fmt.Sprintf("$nbArgs = @(%s); ", strings.Join(escaped, ","))
}

// BuildPowerShellConfigPrefix builds a PowerShell configuration prefix string.
// This is used to disable ANSI coloring and other interactive features.
func BuildPowerShellConfigPrefix() string {
	// Disable ANSI escape codes in PowerShell 7+
	// Also set ProgressPreference to SilentlyContinue to avoid noise.
	// We use a check for $PSStyle to be compatible with PowerShell 5.1 if needed.
	return "if ($PSStyle) { $PSStyle.OutputRendering = 'PlainText' }; $ProgressPreference = 'SilentlyContinue'; "
}

// EncodePowerShellCommand encodes a script as UTF-16LE Base64 for use with
// powershell -EncodedCommand. This avoids all quoting/escaping issues when
// passing commands through SSH or other transport layers.
func EncodePowerShellCommand(script string) string {
	runes := utf16.Encode([]rune(script))
	buf := make([]byte, len(runes)*2)
	for i, r := range runes {
		binary.LittleEndian.PutUint16(buf[i*2:], r)
	}
	return base64.StdEncoding.EncodeToString(buf)
}

// BuildWindowsScriptWrapper creates a PowerShell script that writes the base64-encoded payload to a temp file
// and executes it with the target interpreter. This allows running python/node/bash on Windows VMs safely.
func BuildWindowsScriptWrapper(config ExecutionConfig) (string, error) {
	if config.Language == "powershell" {
		psEnv, err := BuildPowerShellEnvPrefix(config.Env)
		if err != nil {
			return "", err
		}
		psArgs := BuildPowerShellArgsStr(config.Args)
		// Wrap script in a block and pipe to Out-String to ensure output is captured correctly on Windows VMs
		wrappedScript := fmt.Sprintf("& { %s } | Out-String", config.Script)
		return BuildPowerShellConfigPrefix() + psEnv + psArgs + wrappedScript, nil
	}

	encodedScript := base64.StdEncoding.EncodeToString([]byte(config.Script))
	psEnv, err := BuildPowerShellEnvPrefix(config.Env)
	if err != nil {
		return "", err
	}

	interpreter := "bash"
	extension := ".sh"
	switch config.Language {
	case "python":
		interpreter = "python"
		extension = ".py"
	case "javascript":
		interpreter = "node"
		extension = ".js"
	}

	argsSlice := []string{}
	for _, arg := range config.Args {
		argsSlice = append(argsSlice, fmt.Sprintf("'%s'", strings.ReplaceAll(arg, "'", "''")))
	}
	argsDef := ""
	argsPass := ""
	if len(argsSlice) > 0 {
		argsDef = fmt.Sprintf("\n$scriptArgs = @(%s)", strings.Join(argsSlice, ", "))
		argsPass = " @scriptArgs"
	}

	wrapper := fmt.Sprintf(`%s%s
$bytes = [System.Convert]::FromBase64String('%s')
$tmpFile = [System.IO.Path]::GetTempFileName() + '%s'
[System.IO.File]::WriteAllBytes($tmpFile, $bytes)
try {
    & %s $tmpFile%s
    exit $LASTEXITCODE
} finally {
    Remove-Item $tmpFile -ErrorAction SilentlyContinue
}
`, psEnv, argsDef, encodedScript, extension, interpreter, argsPass)

	return BuildPowerShellConfigPrefix() + wrapper, nil
}

// BuildLinuxScriptWrapper creates a shell command that decodes base64 and pipes to the interpreter.
func BuildLinuxScriptWrapper(config ExecutionConfig) (string, error) {
	if config.Language == "powershell" {
		psEnv, err := BuildPowerShellEnvPrefix(config.Env)
		if err != nil {
			return "", err
		}
		psArgs := BuildPowerShellArgsStr(config.Args)
		fullScript := BuildPowerShellConfigPrefix() + psEnv + psArgs + config.Script
		encodedPS := EncodePowerShellCommand(fullScript)
		return fmt.Sprintf("pwsh -NonInteractive -EncodedCommand %s", encodedPS), nil
	}

	envPrefix, err := BuildShellEnvPrefix(config.Env)
	if err != nil {
		return "", err
	}
	encodedScript := base64.StdEncoding.EncodeToString([]byte(config.Script))
	argsStr := BuildShellArgsStr(config.Args)

	switch config.Language {
	case "python":
		return fmt.Sprintf("%secho %s | base64 -d | python3 -%s", envPrefix, encodedScript, argsStr), nil
	case "javascript":
		return fmt.Sprintf("%secho %s | base64 -d | node -%s", envPrefix, encodedScript, argsStr), nil
	default: // bash
		return fmt.Sprintf("%secho %s | base64 -d | bash -s --%s", envPrefix, encodedScript, argsStr), nil
	}
}

// shellQuote wraps a string in single quotes, escaping any embedded single quotes.
// Suitable for safely passing strings to a shell command.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}
