package httpx

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// RunHealthcheck GETs url and maps the result to a process exit code: 0 on
// HTTP 200, 1 otherwise. Used by `<binary> --healthcheck` in Docker
// healthchecks (the runtime image has no curl).
func RunHealthcheck(url string) int {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode == http.StatusOK {
		return 0
	}
	fmt.Fprintln(os.Stderr, "healthcheck status:", resp.StatusCode)
	return 1
}
