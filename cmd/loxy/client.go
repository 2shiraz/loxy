package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"
)

// API client mode (Phase 2): talks to a Loxy API with an API token.
//   LOXY_URL   (default http://127.0.0.1:9090)
//   LOXY_TOKEN loxy_<prefix>_<secret>

func apiCall(method, path string, body io.Reader) ([]byte, int, error) {
	base := strings.TrimRight(os.Getenv("LOXY_URL"), "/")
	if base == "" {
		base = "http://127.0.0.1:9090"
	}
	if !strings.HasPrefix(path, "/api/") {
		path = "/api/v1/" + strings.TrimPrefix(path, "/")
	}
	req, err := http.NewRequest(strings.ToUpper(method), base+path, body)
	if err != nil {
		return nil, 0, err
	}
	tok := os.Getenv("LOXY_TOKEN")
	if tok == "" {
		return nil, 0, fmt.Errorf("set LOXY_TOKEN (create one under Settings → API tokens)")
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return b, resp.StatusCode, err
}

// runAPI implements `loxy api METHOD PATH [JSON|-]`.
func runAPI(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: loxy api METHOD PATH [JSON body | - for stdin]")
	}
	var body io.Reader
	if len(args) > 2 {
		if args[2] == "-" {
			body = os.Stdin
		} else {
			body = strings.NewReader(args[2])
		}
	}
	b, status, err := apiCall(args[0], args[1], body)
	if err != nil {
		return err
	}
	var out bytes.Buffer
	if json.Indent(&out, b, "", "  ") == nil {
		b = out.Bytes()
	}
	fmt.Println(string(b))
	if status >= 400 {
		return fmt.Errorf("HTTP %d", status)
	}
	return nil
}

// runProjects implements `loxy projects ls`.
func runProjects(args []string) error {
	if len(args) == 0 || args[0] != "ls" {
		return fmt.Errorf("usage: loxy projects ls   (other operations: loxy api ...)")
	}
	b, status, err := apiCall("GET", "/projects", nil)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("HTTP %d: %s", status, b)
	}
	var res struct {
		Projects []struct {
			ID        int64  `json:"id"`
			Name      string `json:"name"`
			Status    string `json:"status"`
			Endpoints []struct {
				URL string `json:"url"`
			} `json:"endpoints"`
			Health struct {
				Status  string `json:"status"`
				Healthy int    `json:"healthy"`
				Total   int    `json:"total"`
			} `json:"health"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(b, &res); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tSTATUS\tHEALTH\tENDPOINTS")
	for _, p := range res.Projects {
		var eps []string
		for _, e := range p.Endpoints {
			eps = append(eps, e.URL)
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s %d/%d\t%s\n", p.ID, p.Name, p.Status, p.Health.Status, p.Health.Healthy, p.Health.Total, strings.Join(eps, " "))
	}
	return tw.Flush()
}
