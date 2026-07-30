package executor

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/thinking"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func (e *CodexExecutor) ExecuteStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (_ *cliproxyexecutor.StreamResult, err error) {
	if opts.Alt == "responses/compact" {
		return nil, statusErr{code: http.StatusBadRequest, msg: "streaming not supported for /responses/compact"}
	}
	if isCodexOpenAIImageRequest(opts) {
		return e.executeOpenAIImageStream(ctx, auth, req, opts)
	}
	baseModel := thinking.ParseSuffix(req.Model).ModelName

	apiKey, baseURL := codexCreds(auth)
	if baseURL == "" {
		baseURL = "https://chatgpt.com/backend-api/codex"
	}

	reporter := helps.NewExecutorUsageReporter(ctx, e, baseModel, auth)
	defer reporter.TrackFailure(ctx, &err)

	from := opts.SourceFormat
	responseFormat := cliproxyexecutor.ResponseFormatOrSource(opts)
	to := sdktranslator.FromString("codex")
	originalPayloadSource := req.Payload
	if len(opts.OriginalRequest) > 0 {
		originalPayloadSource = opts.OriginalRequest
	}
	originalPayload := originalPayloadSource
	originalTranslated, body := translateCodexRequestPair(from, to, baseModel, originalPayload, req.Payload, true)

	body, err = helps.ApplyRequestThinking(body, req, opts, from.String(), to.String(), e.Identifier())
	if err != nil {
		return nil, err
	}

	requestedModel := helps.PayloadRequestedModel(opts, req.Model)
	requestPath := helps.PayloadRequestPath(opts)
	body = helps.ApplyPayloadConfigWithRequest(e.cfg, baseModel, to.String(), from.String(), "", body, originalTranslated, requestedModel, requestPath, opts.Headers)
	body, _ = sjson.DeleteBytes(body, "previous_response_id")
	body, _ = sjson.DeleteBytes(body, "generate")
	body, _ = sjson.DeleteBytes(body, "prompt_cache_retention")
	body, _ = sjson.DeleteBytes(body, "safety_identifier")
	body, _ = sjson.DeleteBytes(body, "stream_options")
	body = helps.SetStringIfDifferent(body, "model", baseModel)
	body = normalizeCodexInstructions(body)
	if e.cfg == nil || e.cfg.DisableImageGeneration == config.DisableImageGenerationOff {
		body = ensureImageGenerationTool(body, baseModel, auth, opts.Headers)
	}
	body = sanitizeOpenAIResponsesReasoningEncryptedContent(ctx, "codex executor", body)
	body = normalizeCodexParallelToolCalls(body, opts.Headers)
	body, optimizeMultiAgentV2 := helps.OptimizeCodexMultiAgentV2Request(ctx, opts.Headers, body, e.cfg)
	body, replayScope, errReplay := applyCodexReasoningReplayCacheRequired(ctx, from, req, opts, body)
	if errReplay != nil {
		return nil, errReplay
	}
	reporter.SetTranslatedReasoningEffort(body, to.String())

	url := strings.TrimSuffix(baseURL, "/") + "/responses"
	var identityState codexIdentityConfuseState
	httpReq, upstreamBody, identityState, err := e.cacheHelper(ctx, from, url, auth, req, originalPayloadSource, body, opts.Headers)
	if err != nil {
		return nil, err
	}
	applyCodexHeaders(httpReq, auth, apiKey, true, e.cfg)
	applyModelHeaderOverrides(httpReq.Header, baseModel)
	applyCodexIdentityConfuseHeaders(httpReq.Header, &identityState)
	var authID, authLabel, authType, authValue string
	if auth != nil {
		authID = auth.ID
		authLabel = auth.Label
		authType, authValue = auth.AccountInfo()
	}
	helps.RecordAPIRequest(ctx, e.cfg, helps.UpstreamRequestLog{
		URL:       url,
		Method:    http.MethodPost,
		Headers:   httpReq.Header.Clone(),
		Body:      upstreamBody,
		Provider:  e.Identifier(),
		AuthID:    authID,
		AuthLabel: authLabel,
		AuthType:  authType,
		AuthValue: authValue,
	})

	httpClient := newCodexHTTPClient(ctx, e.cfg, auth)
	upstreamStarted := time.Now()
	helps.LogWithRequestID(ctx).Infof("codex stream executor: upstream request started | model=%s", baseModel)
	httpClient = reporter.TrackHTTPClient(httpClient)
	httpResp, err := doCodexRequestWithHeaderTimeout(httpClient, httpReq, codexHeaderTimeout(e.cfg))
	if err != nil {
		helps.RecordAPIResponseError(ctx, e.cfg, err)
		elapsed := time.Since(upstreamStarted).Round(time.Millisecond)
		if ctxErr := ctx.Err(); ctxErr != nil {
			helps.LogWithRequestID(ctx).Warnf("codex stream executor: downstream context canceled before upstream headers | elapsed=%s err=%v context_err=%v", elapsed, err, ctxErr)
		} else {
			helps.LogWithRequestID(ctx).Warnf("codex stream executor: upstream request failed before headers | elapsed=%s err=%v", elapsed, err)
		}
		return nil, err
	}
	streamHeadersReceivedAt := time.Now()
	helps.LogWithRequestID(ctx).Infof("codex stream executor: upstream headers received | status=%d elapsed=%s", httpResp.StatusCode, streamHeadersReceivedAt.Sub(upstreamStarted).Round(time.Millisecond))
	helps.RecordAPIResponseMetadata(ctx, e.cfg, httpResp.StatusCode, httpResp.Header.Clone())
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		data, readErr := io.ReadAll(httpResp.Body)
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("codex executor: close response body error: %v", errClose)
		}
		if readErr != nil {
			helps.RecordAPIResponseError(ctx, e.cfg, readErr)
			return nil, readErr
		}
		data = applyCodexIdentityConfuseResponsePayload(data, identityState)
		if errClearReplay := clearCodexReasoningReplayOnInvalidSignature(ctx, replayScope, httpResp.StatusCode, data); errClearReplay != nil {
			return nil, errClearReplay
		}
		helps.AppendAPIResponseChunk(ctx, e.cfg, data)
		helps.LogWithRequestID(ctx).Debugf("request error, error status: %d, error message: %s", httpResp.StatusCode, helps.SummarizeErrorBody(httpResp.Header.Get("Content-Type"), data))
		if shouldRetryResponsesWithoutEncryptedReasoning(httpResp.StatusCode, data) {
			strippedBody, changed := stripReasoningContextForRetry(body, data)
			if !changed {
				err = newCodexStatusErr(httpResp.StatusCode, data)
				return nil, err
			}
			helps.LogWithRequestID(ctx).Warn("codex stream executor: retrying once without encrypted reasoning context after upstream request error")
			retryResp, retryErr := e.retryCodexStreamWithoutReasoning(ctx, retryStreamParams{
				from:                  from,
				url:                   url,
				auth:                  auth,
				req:                   req,
				opts:                  opts,
				originalPayloadSource: originalPayloadSource,
				strippedBody:          strippedBody,
				apiKey:                apiKey,
				baseModel:             baseModel,
				authID:                authID,
				authLabel:             authLabel,
				authType:              authType,
				authValue:             authValue,
				httpClient:            httpClient,
				identityState:         &identityState,
				headersReceivedAt:     &streamHeadersReceivedAt,
			})
			if retryErr != nil {
				return nil, retryErr
			}
			body = strippedBody
			httpResp = retryResp
		} else {
			err = newCodexStatusErr(httpResp.StatusCode, data)
			return nil, err
		}
	}
	retryParams := retryStreamParams{
		from:                  from,
		url:                   url,
		auth:                  auth,
		req:                   req,
		opts:                  opts,
		originalPayloadSource: originalPayloadSource,
		apiKey:                apiKey,
		baseModel:             baseModel,
		authID:                authID,
		authLabel:             authLabel,
		authType:              authType,
		authValue:             authValue,
		httpClient:            httpClient,
		identityState:         &identityState,
		headersReceivedAt:     &streamHeadersReceivedAt,
	}

	out := make(chan cliproxyexecutor.StreamChunk)
	go func() {
		defer close(out)
		defer func() {
			if errClose := httpResp.Body.Close(); errClose != nil {
				log.Errorf("codex executor: close response body error: %v", errClose)
			}
		}()
		retriedWithoutEncryptedReasoning := false
	attempt:
		scanner := bufio.NewScanner(httpResp.Body)
		scanner.Buffer(nil, 52_428_800) // 50MB
		claudeInputTokens := helps.NewClaudeInputTokenState(from, to, responseFormat, originalPayload)
		var param any
		outputItemsByIndex := make(map[int64][]byte)
		var outputItemsFallback [][]byte
		firstUpstreamChunk := false
		sawResponseCompleted := false
		downstreamStarted := false
		var pendingLines [][]byte
		terminalSuccess := false
		// sendLine translates one buffered upstream line and forwards it downstream.
		// It returns false once the stream must stop (context cancel or terminal error).
		sendLine := func(line []byte) bool {
			translatedLine := bytes.Clone(line)

			if bytes.HasPrefix(line, dataTag) {
				data := bytes.TrimSpace(line[5:])
				data = helps.RestoreCodexMultiAgentV2Response(data, optimizeMultiAgentV2)
				translatedLine = append([]byte("data: "), data...)
				eventType := gjson.GetBytes(data, "type").String()
				switch eventType {
				case "response.completed", "response.incomplete":
					terminalSuccess = true
					data = patchCodexCompletedOutput(data, outputItemsByIndex, outputItemsFallback)
					if eventType == "response.completed" {
						cacheCodexReasoningReplayFromCompleted(replayScope, data)
					}
					translatedLine = append([]byte("data: "), data...)
				}
			}

			translatedLine = applyCodexIdentityExposeResponsePayload(translatedLine, identityState)
			chunks := helps.TranslateStreamWithClaudeInputTokens(ctx, to, responseFormat, req.Model, originalPayload, body, translatedLine, &param, claudeInputTokens)
			for i := range chunks {
				select {
				case out <- cliproxyexecutor.StreamChunk{Payload: chunks[i]}:
				case <-ctx.Done():
					helps.LogWithRequestID(ctx).Warnf("codex stream executor: downstream context canceled while forwarding stream chunk | total_elapsed=%s context_err=%v", time.Since(upstreamStarted).Round(time.Millisecond), ctx.Err())
					return false
				}
			}
			return true
		}
		flushPending := func() bool {
			for _, pendingLine := range pendingLines {
				if !sendLine(pendingLine) {
					return false
				}
			}
			pendingLines = nil
			return true
		}
		for scanner.Scan() {
			line := bytes.Clone(applyCodexIdentityConfuseResponsePayload(scanner.Bytes(), identityState))
			if !firstUpstreamChunk {
				firstUpstreamChunk = true
				helps.LogWithRequestID(ctx).Infof("codex stream executor: first upstream stream chunk received | since_headers=%s total_elapsed=%s bytes=%d", time.Since(streamHeadersReceivedAt).Round(time.Millisecond), time.Since(upstreamStarted).Round(time.Millisecond), len(line))
			}
			helps.AppendAPIResponseChunk(ctx, e.cfg, line)

			canStartDownstream := false
			if bytes.HasPrefix(line, dataTag) {
				data := bytes.TrimSpace(line[5:])
				eventType := gjson.GetBytes(data, "type").String()
				if streamErr, terminalBody, ok := codexTerminalFailureErr(data); ok {
					if errClearReplay := clearCodexReasoningReplayOnRejectedContext(ctx, replayScope, streamErr.StatusCode(), terminalBody); errClearReplay != nil {
						helps.RecordAPIResponseError(ctx, e.cfg, errClearReplay)
						reporter.PublishFailure(ctx, errClearReplay)
						select {
						case out <- cliproxyexecutor.StreamChunk{Err: errClearReplay}:
						case <-ctx.Done():
							helps.LogWithRequestID(ctx).Warnf("codex stream executor: downstream context canceled while forwarding replay cache clear error | total_elapsed=%s context_err=%v", time.Since(upstreamStarted).Round(time.Millisecond), ctx.Err())
						}
						return
					}
					helps.RecordAPIResponseError(ctx, e.cfg, streamErr)
					reporter.PublishFailure(ctx, streamErr)
					select {
					case out <- cliproxyexecutor.StreamChunk{Err: streamErr}:
					case <-ctx.Done():
						helps.LogWithRequestID(ctx).Warnf("codex stream executor: downstream context canceled while forwarding terminal stream error | total_elapsed=%s context_err=%v", time.Since(upstreamStarted).Round(time.Millisecond), ctx.Err())
					}
					return
				}
				switch eventType {
				case "response.output_item.done":
					collectCodexOutputItemDone(data, outputItemsByIndex, &outputItemsFallback)
				case "response.completed", "response.incomplete":
					sawResponseCompleted = true
					if detail, ok := helps.ParseCodexUsage(data); ok {
						reporter.Publish(ctx, detail)
					}
					publishCodexImageToolUsage(ctx, reporter, body, data)
				}
				canStartDownstream = codexStreamEventCanStartDownstream(eventType)
			}

			pendingLines = append(pendingLines, line)
			if !downstreamStarted {
				if !canStartDownstream {
					continue
				}
				downstreamStarted = true
			}
			if !flushPending() {
				return
			}
			if terminalSuccess {
				return
			}
		}
		if errScan := scanner.Err(); errScan != nil {
			if ctx.Err() != nil {
				return
			}
			helps.RecordAPIResponseError(ctx, e.cfg, errScan)
			if !downstreamStarted && !retriedWithoutEncryptedReasoning {
				if strippedBody, changed := stripReasoningItemsFromResponsesBody(body); changed {
					helps.LogWithRequestID(ctx).Warn("codex stream executor: stream read failed before downstream output; retrying once without reasoning context")
					if errClose := httpResp.Body.Close(); errClose != nil {
						log.Errorf("codex executor: close read-failed stream response body error: %v", errClose)
					}
					retryResp, retryErr := e.retryCodexStreamWithoutReasoning(ctx, retryParams.withBody(strippedBody))
					if retryErr != nil {
						helps.RecordAPIResponseError(ctx, e.cfg, retryErr)
						reporter.PublishFailure(ctx, retryErr)
						select {
						case out <- cliproxyexecutor.StreamChunk{Err: retryErr}:
						case <-ctx.Done():
							helps.LogWithRequestID(ctx).Warnf("codex stream executor: downstream context canceled while forwarding read-failed stream retry error | total_elapsed=%s context_err=%v", time.Since(upstreamStarted).Round(time.Millisecond), ctx.Err())
						}
						return
					}
					httpResp = retryResp
					body = strippedBody
					retriedWithoutEncryptedReasoning = true
					pendingLines = nil
					goto attempt
				}
			}
			streamErr := newCodexIncompleteStreamError()
			reporter.PublishFailure(ctx, streamErr)
			helps.LogWithRequestID(ctx).Warnf("codex stream executor: upstream stream read failed | first_chunk=%t total_elapsed=%s err=%v", firstUpstreamChunk, time.Since(upstreamStarted).Round(time.Millisecond), errScan)
			select {
			case out <- cliproxyexecutor.StreamChunk{Err: streamErr}:
			case <-ctx.Done():
				helps.LogWithRequestID(ctx).Warnf("codex stream executor: downstream context canceled while forwarding stream read error | total_elapsed=%s context_err=%v", time.Since(upstreamStarted).Round(time.Millisecond), ctx.Err())
			}
			return
		}
		if !firstUpstreamChunk {
			helps.LogWithRequestID(ctx).Infof("codex stream executor: upstream stream ended without chunks | total_elapsed=%s", time.Since(upstreamStarted).Round(time.Millisecond))
		}
		if !sawResponseCompleted {
			streamErr := newCodexIncompleteStreamError()
			helps.RecordAPIResponseError(ctx, e.cfg, streamErr)
			helps.LogWithRequestID(ctx).Warnf("codex stream executor: upstream stream closed before response.completed | downstream_started=%t first_chunk=%t total_elapsed=%s", downstreamStarted, firstUpstreamChunk, time.Since(upstreamStarted).Round(time.Millisecond))
			if !downstreamStarted && !retriedWithoutEncryptedReasoning {
				if strippedBody, changed := stripReasoningItemsFromResponsesBody(body); changed {
					helps.LogWithRequestID(ctx).Warn("codex stream executor: closed before response.completed; retrying once without reasoning context")
					if errClose := httpResp.Body.Close(); errClose != nil {
						log.Errorf("codex executor: close incomplete stream response body error: %v", errClose)
					}
					retryResp, retryErr := e.retryCodexStreamWithoutReasoning(ctx, retryParams.withBody(strippedBody))
					if retryErr != nil {
						helps.RecordAPIResponseError(ctx, e.cfg, retryErr)
						reporter.PublishFailure(ctx, retryErr)
						select {
						case out <- cliproxyexecutor.StreamChunk{Err: retryErr}:
						case <-ctx.Done():
							helps.LogWithRequestID(ctx).Warnf("codex stream executor: downstream context canceled while forwarding incomplete stream retry error | total_elapsed=%s context_err=%v", time.Since(upstreamStarted).Round(time.Millisecond), ctx.Err())
						}
						return
					}
					httpResp = retryResp
					body = strippedBody
					retriedWithoutEncryptedReasoning = true
					pendingLines = nil
					goto attempt
				}
			}
			reporter.PublishFailure(ctx, streamErr)
			select {
			case out <- cliproxyexecutor.StreamChunk{Err: streamErr}:
			case <-ctx.Done():
				helps.LogWithRequestID(ctx).Warnf("codex stream executor: downstream context canceled while forwarding incomplete stream error | total_elapsed=%s context_err=%v", time.Since(upstreamStarted).Round(time.Millisecond), ctx.Err())
			}
			return
		}
		if len(pendingLines) > 0 {
			flushPending()
		}
	}()
	return &cliproxyexecutor.StreamResult{Headers: httpResp.Header.Clone(), Chunks: out}, nil
}
