package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
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

// runList implements `loxy frontends ls` and `loxy backends ls`.
func runList(kind string, args []string) error {
	if len(args) == 0 || args[0] != "ls" {
		return fmt.Errorf("usage: loxy %s ls   (other operations: loxy api ...)", kind)
	}
	b, status, err := apiCall("GET", "/"+kind, nil)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("HTTP %d: %s", status, b)
	}
	type health struct {
		Status  string `json:"status"`
		Healthy int    `json:"healthy"`
		Total   int    `json:"total"`
	}
	type bind struct {
		Address string `json:"address"`
		Port    int64  `json:"port"`
	}
	var res struct {
		Frontends []struct {
			ID     int64  `json:"id"`
			Name   string `json:"name"`
			Mode   string `json:"mode"`
			TLS    bool   `json:"tls"`
			Binds  []bind `json:"binds"`
			Status string `json:"status"`
			Health health `json:"health"`
		} `json:"frontends"`
		Backends []struct {
			ID        int64  `json:"id"`
			Name      string `json:"name"`
			Mode      string `json:"mode"`
			Algorithm string `json:"algorithm"`
			Servers   int    `json:"servers_count"`
			Health    health `json:"health"`
		} `json:"backends"`
	}
	if err := json.Unmarshal(b, &res); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	if kind == "frontends" {
		fmt.Fprintln(tw, "ID\tNAME\tMODE\tBINDS\tSTATUS\tHEALTH")
		for _, f := range res.Frontends {
			mode := f.Mode
			if f.TLS {
				mode += "+tls"
			}
			var bs []string
			for _, x := range f.Binds {
				bs = append(bs, net.JoinHostPort(x.Address, strconv.FormatInt(x.Port, 10)))
			}
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s %d/%d\n", f.ID, f.Name, mode, strings.Join(bs, ","), f.Status, f.Health.Status, f.Health.Healthy, f.Health.Total)
		}
	} else {
		fmt.Fprintln(tw, "ID\tNAME\tMODE\tALGORITHM\tSERVERS\tHEALTH")
		for _, x := range res.Backends {
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%d\t%s %d/%d\n", x.ID, x.Name, x.Mode, x.Algorithm, x.Servers, x.Health.Status, x.Health.Healthy, x.Health.Total)
		}
	}
	return tw.Flush()
}
