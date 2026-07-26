package executor

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	internalcache "github.com/router-for-me/CLIProxyAPI/v7/internal/cache"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

const defaultCodexHeaderTimeout = 180 * time.Second

// newCodexHTTPClient builds the Codex HTTP client with the self-use response
// header timeout applied. The streaming body remains unlimited once headers land.
func newCodexHTTPClient(ctx context.Context, cfg *config.Config, auth *cliproxyauth.Auth) *http.Client {
	httpClient := helps.NewUtlsHTTPClient(ctx, cfg, auth, 0)
	return helps.WithResponseHeaderTimeout(httpClient, codexHeaderTimeout(cfg))
}

func codexHeaderTimeout(cfg *config.Config) time.Duration {
	seconds := 0
	if cfg != nil {
		seconds = cfg.CodexResponseHeaderTimeoutSeconds
	}

	if raw := strings.TrimSpace(os.Getenv("CPA_CODEX_RESPONSE_HEADER_TIMEOUT_SECONDS")); raw != "" {
		parsed, errParse := strconv.Atoi(raw)
		if errParse != nil {
			log.WithError(errParse).Warn("invalid CPA_CODEX_RESPONSE_HEADER_TIMEOUT_SECONDS; using config/default value")
		} else {
			seconds = parsed
		}
	}

	switch {
	case seconds < 0:
		return 0
	case seconds == 0:
		return defaultCodexHeaderTimeout
	default:
		return time.Duration(seconds) * time.Second
	}
}

// doCodexRequestWithHeaderTimeout bounds only the wait for upstream response
// headers; the response body stream is left untouched afterwards.
func doCodexRequestWithHeaderTimeout(client *http.Client, req *http.Request, timeout time.Duration) (*http.Response, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if req == nil || timeout <= 0 {
		return client.Do(req)
	}

	reqCtx, cancel := context.WithCancel(req.Context())
	timer := time.AfterFunc(timeout, cancel)
	resp, err := client.Do(req.WithContext(reqCtx))
	if timer.Stop() {
		if err != nil {
			cancel()
		}
		return resp, err
	}
	cancel()
	if err != nil && req.Context().Err() == nil {
		return resp, fmt.Errorf("codex response header timeout after %s: %w", timeout.Round(time.Millisecond), err)
	}
	return resp, err
}

// codexStreamEventCanStartDownstream reports whether a Codex stream event is a
// terminal marker that allows buffered lines to be flushed downstream.
func codexStreamEventCanStartDownstream(eventType string) bool {
	return eventType == "response.completed" || eventType == "response.incomplete"
}

// clearCodexReasoningReplayOnRejectedContext widens the upstream invalid-signature
// cleanup to also cover stale stored reasoning item rejections.
func clearCodexReasoningReplayOnRejectedContext(ctx context.Context, scope codexReasoningReplayScope, statusCode int, body []byte) error {
	if !scope.valid() {
		return nil
	}
	code, _, ok := codexStatusErrorClassification(statusCode, body)
	if (ok && code == "thinking_signature_invalid") || isMissingStoredResponsesReasoningItemError(statusCode, body) {
		return internalcache.DeleteCodexReasoningReplayItemRequired(ctx, scope.modelName, scope.sessionKey)
	}
	return nil
}
