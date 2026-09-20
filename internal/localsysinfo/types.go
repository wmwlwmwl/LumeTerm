package localsysinfo

import (
	"os"
	"path/filepath"
	"strings"
)

type Session struct {
	WSLDistro string
}

type Dependencies struct {
	ProbeScript      string
	ParseProbe       func(output string, includeNetwork bool) (map[string]interface{}, error)
	ParseProcessList func(output string) ([]map[string]interface{}, error)
	ParseStaticInfo  func(output string) (map[string]interface{}, error)
}

func deployProbeScript(script string) (string, func(), error) {
	directory, err := os.MkdirTemp("", "lumeterm-probe-*")
	if err != nil {
		return "", nil, err
	}
	scriptPath := filepath.Join(directory, "probe.sh")
	script = strings.ReplaceAll(script, "\r\n", "\n")
	if err := os.WriteFile(scriptPath, []byte(script), 0o644); err != nil {
		_ = os.RemoveAll(directory)
		return "", nil, err
	}
	return scriptPath, func() { _ = os.RemoveAll(directory) }, nil
}
