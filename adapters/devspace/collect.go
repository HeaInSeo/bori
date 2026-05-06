package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// scrapeMetrics port-forwards to the component's service and GETs /metrics.
func scrapeMetrics(ctx context.Context, comp Component) (map[string]float64, error) {
	localPort, err := freePort()
	if err != nil {
		return nil, fmt.Errorf("free port: %w", err)
	}

	pfCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	cmd := exec.CommandContext(pfCtx, "kubectl", "port-forward",
		"-n", comp.Namespace,
		fmt.Sprintf("svc/%s", comp.Name),
		fmt.Sprintf("%d:%d", localPort, comp.Port),
	)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("kubectl port-forward: %w", err)
	}

	url := fmt.Sprintf("http://localhost:%d%s", localPort, comp.MetricsPath)
	var resp *http.Response
	for i := 0; i < 20; i++ {
		time.Sleep(300 * time.Millisecond)
		resp, err = http.Get(url) //nolint:noctx
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("metrics endpoint not ready at %s: %w", url, err)
	}
	defer resp.Body.Close()

	return parsePromText(resp.Body)
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port, nil
}

// parsePromText parses Prometheus text exposition format into a flat map.
// Key format: metric_name or metric_name{label="value",...}
// Labels are sorted canonically to match slint-gate policy metric names.
func parsePromText(r io.Reader) (map[string]float64, error) {
	out := map[string]float64{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := fields[0]
		v, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			continue
		}
		out[key] = v
	}
	return out, sc.Err()
}
