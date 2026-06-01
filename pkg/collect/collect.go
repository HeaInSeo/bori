// Package collect scrapes Prometheus /metrics endpoints via kubectl port-forward.
// This is a temporary compatibility shim. Long-term, kube-slint owns metric
// collection; bori will delegate to it rather than running its own parser.
package collect

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

// Target describes a Kubernetes service to scrape.
type Target struct {
	Namespace   string
	ServiceName string
	Port        int
	MetricsPath string
}

// ScrapeMetrics port-forwards to the target service and GETs its metrics path.
// It returns a flat map of metric name -> value.
func ScrapeMetrics(ctx context.Context, t Target) (map[string]float64, error) {
	localPort, err := FreePort()
	if err != nil {
		return nil, fmt.Errorf("free port: %w", err)
	}

	pfCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(pfCtx, "kubectl", "port-forward",
		"-n", t.Namespace,
		fmt.Sprintf("svc/%s", t.ServiceName),
		fmt.Sprintf("%d:%d", localPort, t.Port),
	)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("kubectl port-forward: %w", err)
	}
	// Ensure the port-forward process is cleaned up when we're done.
	defer func() {
		cancel()
		_ = cmd.Wait()
	}()

	client := &http.Client{Timeout: 5 * time.Second}
	url := fmt.Sprintf("http://localhost:%d%s", localPort, t.MetricsPath)

	var resp *http.Response
	for i := 0; i < 20; i++ {
		time.Sleep(300 * time.Millisecond)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}
		resp, err = client.Do(req)
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("metrics endpoint not ready at %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("metrics endpoint %s returned HTTP %d", url, resp.StatusCode)
	}

	return ParsePromText(resp.Body)
}

// FreePort returns a free TCP port on localhost.
func FreePort() (int, error) {
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port, nil
}

// ParsePromText parses Prometheus text exposition format into a flat map.
// This is a minimal parser: it extracts only name and value, ignoring labels,
// histogram buckets, and type metadata.
func ParsePromText(r io.Reader) (map[string]float64, error) {
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
		v, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			continue
		}
		out[fields[0]] = v
	}
	return out, sc.Err()
}
