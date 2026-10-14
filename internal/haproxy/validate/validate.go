// Package validate wraps `haproxy -c` (spec §7.7). Nothing reaches `live` without passing it.
package validate

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type Validator struct {
	// Cmd is the haproxy command, e.g. ["haproxy"] or a docker wrapper.
	Cmd []string
}

func New(bin string) *Validator { return &Validator{Cmd: strings.Fields(bin)} }

// Check runs `haproxy -c -f file` and returns combined output.
func (v *Validator) Check(ctx context.Context, file string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	args := append(append([]string{}, v.Cmd[1:]...), "-c", "-f", file)
	cmd := exec.CommandContext(ctx, v.Cmd[0], args...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if err != nil {
		return out.String(), fmt.Errorf("haproxy -c failed: %w", err)
	}
	return out.String(), nil
}

// Version returns the first line of `haproxy -vv` and the feature list.
func (v *Validator) Version(ctx context.Context) (version string, features []string, err error) {
	args := append(append([]string{}, v.Cmd[1:]...), "-vv")
	out, err := exec.CommandContext(ctx, v.Cmd[0], args...).CombinedOutput()
	if err != nil {
		return "", nil, err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "HAProxy version ") {
			version = strings.Fields(line)[2]
		}
		if strings.HasPrefix(line, "Feature list :") {
			for _, f := range strings.Fields(strings.TrimPrefix(line, "Feature list :")) {
				if strings.HasPrefix(f, "+") {
					features = append(features, f[1:])
				}
			}
		}
	}
	return version, features, nil
}
