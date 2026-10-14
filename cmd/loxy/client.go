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

// runLB implements `loxy lb ls`.
func runLB(args []string) error {
	if len(args) == 0 || args[0] != "ls" {
		return fmt.Errorf("usage: loxy lb ls   (other operations: loxy api ...)")
	}
	b, status, err := apiCall("GET", "/load_balancers", nil)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("HTTP %d: %s", status, b)
	}
	var res struct {
		LoadBalancers []struct {
			ID        int64  `json:"id"`
			Name      string `json:"name"`
			Status    string `json:"status"`
			Algorithm string `json:"algorithm"`
			Plan      struct {
				Name string `json:"name"`
			} `json:"plan"`
			PublicNet struct {
				IPv4 *string `json:"ipv4"`
			} `json:"public_net"`
			Health struct {
				Status  string `json:"status"`
				Healthy int    `json:"healthy"`
				Total   int    `json:"total"`
			} `json:"health"`
		} `json:"load_balancers"`
	}
	if err := json.Unmarshal(b, &res); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tPLAN\tIPV4\tSTATUS\tHEALTH")
	for _, l := range res.LoadBalancers {
		ip := "-"
		if l.PublicNet.IPv4 != nil {
			ip = *l.PublicNet.IPv4
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s %d/%d\n", l.ID, l.Name, l.Plan.Name, ip, l.Status, l.Health.Status, l.Health.Healthy, l.Health.Total)
	}
	return tw.Flush()
}
