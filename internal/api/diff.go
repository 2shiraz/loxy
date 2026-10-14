package api

import (
	"fmt"
	"strings"
)

// UnifiedDiff returns a minimal unified diff (LCS-based, with 3 lines of context).
func UnifiedDiff(nameA, nameB, a, b string) string {
	x, y := strings.Split(a, "\n"), strings.Split(b, "\n")
	n, m := len(x), len(y)
	if n*m > 25_000_000 {
		return "diff too large\n"
	}
	lcs := make([][]int32, n+1)
	for i := range lcs {
		lcs[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if x[i] == y[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	type op struct {
		kind byte
		text string
	}
	var ops []op
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case x[i] == y[j]:
			ops = append(ops, op{' ', x[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, op{'-', x[i]})
			i++
		default:
			ops = append(ops, op{'+', y[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, op{'-', x[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, op{'+', y[j]})
	}
	var b2 strings.Builder
	fmt.Fprintf(&b2, "--- %s\n+++ %s\n", nameA, nameB)
	const ctx = 3
	changed := false
	for k := 0; k < len(ops); k++ {
		if ops[k].kind == ' ' {
			continue
		}
		changed = true
		start := max(0, k-ctx)
		end := k
		for end < len(ops) {
			if ops[end].kind != ' ' {
				end++
				continue
			}
			run := end
			for run < len(ops) && ops[run].kind == ' ' {
				run++
			}
			if run-end > 2*ctx || run == len(ops) {
				end = min(len(ops), end+ctx)
				break
			}
			end = run
		}
		b2.WriteString("@@\n")
		for _, o := range ops[start:end] {
			b2.WriteByte(o.kind)
			b2.WriteString(o.text + "\n")
		}
		k = end - 1
	}
	if !changed {
		b2.WriteString("(no differences)\n")
	}
	return b2.String()
}
