package executor

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	log "github.com/sirupsen/logrus"
)

// retryStreamParams carries the request context needed to replay a Codex stream
// after the reasoning context has been stripped from the payload.
type retryStreamParams struct {
	from                  sdktranslator.Format
	url                   string
	auth                  *cliproxyauth.Auth
	req                   cliproxyexecutor.Request
	opts                  cliproxyexecutor.Options
	originalPayloadSource []byte
	strippedBody          []byte
	apiKey                string
	baseModel             string
	authID                string
	authLabel             string
	authType              string
	authValue             string
	httpClient            *http.Client
	identityState         *codexIdentityConfuseState
	headersReceivedAt     *time.Time
}

func (p retryStreamParams) withBody(strippedBody []byte) retryStreamParams {
	p.strippedBody = strippedBody
	return p
}

// retryCodexStreamWithoutReasoning replays the streaming request once with the
// reasoning context removed and returns the live upstream response on success.
func (e *CodexExecutor) retryCodexStreamWithoutReasoning(ctx context.Context, p retryStreamParams) (*http.Response, error) {
	retryReq, retryUpstreamBody, retryIdentityState, retryBuildErr := e.cacheHelper(ctx, p.from, p.url, p.auth, p.req, p.originalPayloadSource, p.strippedBody, p.opts.Headers)
	if retryBuildErr != nil {
		return nil, retryBuildErr
	}
	applyCodexHeaders(retryReq, p.auth, p.apiKey, true, e.cfg)
	applyModelHeaderOverrides(retryReq.Header, p.baseModel)
	applyCodexIdentityConfuseHeaders(retryReq.Header, &retryIdentityState)
	helps.RecordAPIRequest(ctx, e.cfg, helps.UpstreamRequestLog{
		URL:       p.url,
		Method:    http.MethodPost,
		Headers:   retryReq.Header.Clone(),
		Body:      retryUpstreamBody,
		Provider:  e.Identifier(),
		AuthID:    p.authID,
		AuthLabel: p.authLabel,
		AuthType:  p.authType,
		AuthValue: p.authValue,
	})
	retryStarted := time.Now()
	helps.LogWithRequestID(ctx).Infof("codex stream executor: encrypted reasoning stream retry upstream request started | model=%s", p.baseModel)
	retryResp, retryErr := doCodexRequestWithHeaderTimeout(p.httpClient, retryReq, codexHeaderTimeout(e.cfg))
	if retryErr != nil {
		helps.RecordAPIResponseError(ctx, e.cfg, retryErr)
		elapsed := time.Since(retryStarted).Round(time.Millisecond)
		if ctxErr := ctx.Err(); ctxErr != nil {
			helps.LogWithRequestID(ctx).Warnf("codex stream executor: downstream context canceled before encrypted reasoning stream retry upstream headers | elapsed=%s err=%v context_err=%v", elapsed, retryErr, ctxErr)
		} else {
			helps.LogWithRequestID(ctx).Warnf("codex stream executor: encrypted reasoning stream retry upstream request failed before headers | elapsed=%s err=%v", elapsed, retryErr)
		}
		return nil, retryErr
	}
	headersAt := time.Now()
	if p.headersReceivedAt != nil {
		*p.headersReceivedAt = headersAt
	}
	helps.LogWithRequestID(ctx).Infof("codex stream executor: encrypted reasoning stream retry upstream headers received | status=%d elapsed=%s", retryResp.StatusCode, headersAt.Sub(retryStarted).Round(time.Millisecond))
	helps.RecordAPIResponseMetadata(ctx, e.cfg, retryResp.StatusCode, retryResp.Header.Clone())
	if retryResp.StatusCode < 200 || retryResp.StatusCode >= 300 {
		retryData, retryReadErr := io.ReadAll(retryResp.Body)
		if errClose := retryResp.Body.Close(); errClose != nil {
			log.Errorf("codex executor: close encrypted reasoning stream retry response body error: %v", errClose)
		}
		if retryReadErr != nil {
			helps.RecordAPIResponseError(ctx, e.cfg, retryReadErr)
			return nil, retryReadErr
		}
		retryData = applyCodexIdentityConfuseResponsePayload(retryData, retryIdentityState)
		helps.AppendAPIResponseChunk(ctx, e.cfg, retryData)
		helps.LogWithRequestID(ctx).Debugf("request encrypted reasoning stream retry error, error status: %d, error message: %s", retryResp.StatusCode, helps.SummarizeErrorBody(retryResp.Header.Get("Content-Type"), retryData))
		return nil, newCodexStatusErr(retryResp.StatusCode, retryData)
	}
	if p.identityState != nil {
		*p.identityState = retryIdentityState
	}
	return retryResp, nil
}
