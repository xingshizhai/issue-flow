package envfile

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strings"
)

const maximumSize = 64 << 10

// Load reads a deliberately small dotenv subset without shell evaluation.
func Load(path string) (map[string]string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("read env file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("env file must be a regular file, not a symlink")
	}
	if info.Size() > maximumSize {
		return nil, fmt.Errorf("env file exceeds %d bytes", maximumSize)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("env file permissions must not allow group or other access")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open env file: %w", err)
	}
	defer f.Close()
	openedInfo, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect env file: %w", err)
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return nil, fmt.Errorf("env file changed while opening")
	}
	values := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || !validKey(key) {
			return nil, fmt.Errorf("env file line %d is invalid", lineNumber)
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && ((value[0] == '\'' && value[len(value)-1] == '\'') ||
			(value[0] == '"' && value[len(value)-1] == '"')) {
			value = value[1 : len(value)-1]
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read env file: %w", err)
	}
	return values, nil
}

func validKey(key string) bool {
	if key == "" || !((key[0] >= 'A' && key[0] <= 'Z') || key[0] == '_') {
		return false
	}
	for i := 1; i < len(key); i++ {
		if (key[i] >= 'A' && key[i] <= 'Z') || (key[i] >= '0' && key[i] <= '9') || key[i] == '_' {
			continue
		}
		return false
	}
	return true
}
