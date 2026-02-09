package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

type Server struct {
	store *Store
	miui  *MiuiClient
}

type RequestOptions struct {
	Stream       bool
	DeepThinking bool
	OnlineSearch bool
	Model        string
}

func NewServer(store *Store, miui *MiuiClient) *Server {
	return &Server{store: store, miui: miui}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]interface{}{
		"object": "list",
		"data": []map[string]interface{}{
			{
				"id":       "DOUBAO",
				"object":   "model",
				"created":  time.Now().Unix(),
				"owned_by": "miui",
			},
		},
	})
}

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	body, err := readJSONBody(r)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_json")
		return
	}

	systemPrompt, userText := extractMessages(body["messages"])
	if userText == "" {
		writeOpenAIError(w, http.StatusBadRequest, "missing_user_message")
		return
	}

	opts := parseRequestOptions(body, r)

	userKey := extractUserKey(r)
	conversationID := r.Header.Get("ConversationId")

	conv, err := s.store.GetConversation(userKey, conversationID)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "store_error")
		return
	}

	finalQuery := buildFinalQuery(systemPrompt, userText)
	model := opts.Model

	if opts.Stream {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeOpenAIError(w, http.StatusInternalServerError, "stream_unsupported")
			return
		}

		id := newID("chatcmpl")
		created := time.Now().Unix()
		sentRole := false

		onChunk := func(text string) {
			if !sentRole {
				chunk := newChatChunk(id, created, model, "", true)
				writeSSEData(w, chunk)
				sentRole = true
			}
			chunk := newChatChunk(id, created, model, text, false)
			writeSSEData(w, chunk)
			flusher.Flush()
		}

		full, err := s.performChat(r.Context(), conv, finalQuery, opts.DeepThinking, opts.OnlineSearch, onChunk)
		if err != nil {
			return
		}

		finishChunk := newChatChunk(id, created, model, "", false)
		finishReason := "stop"
		finishChunk.Choices[0].FinishReason = &finishReason
		writeSSEData(w, finishChunk)
		writeSSELine(w, "data: [DONE]\n\n")
		flusher.Flush()
		_ = full
		return
	}

	full, err := s.performChat(r.Context(), conv, finalQuery, opts.DeepThinking, opts.OnlineSearch, nil)
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "upstream_error")
		return
	}

	resp := newChatCompletionResponse(model, full)
	writeJSON(w, resp)
}

func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	body, err := readJSONBody(r)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_json")
		return
	}

	systemPrompt, userText := extractResponsesInput(body["input"])
	if userText == "" {
		writeOpenAIError(w, http.StatusBadRequest, "missing_input")
		return
	}

	opts := parseRequestOptions(body, r)

	userKey := extractUserKey(r)
	conversationID := r.Header.Get("ConversationId")
	conv, err := s.store.GetConversation(userKey, conversationID)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "store_error")
		return
	}

	finalQuery := buildFinalQuery(systemPrompt, userText)
	model := opts.Model

	if opts.Stream {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeOpenAIError(w, http.StatusInternalServerError, "stream_unsupported")
			return
		}

		respID := newID("resp")
		msgID := newID("msg")
		created := time.Now().Unix()
		base := newResponsesBase(respID, msgID, model, created)
		writeSSEEvent(w, "response.created", base)
		flusher.Flush()

		onChunk := func(text string) {
			delta := responseDeltaEvent(msgID, text)
			writeSSEEvent(w, "response.output_text.delta", delta)
			flusher.Flush()
		}

		full, err := s.performChat(r.Context(), conv, finalQuery, opts.DeepThinking, opts.OnlineSearch, onChunk)
		if err != nil {
			return
		}

		done := responseDoneEvent(msgID, full)
		writeSSEEvent(w, "response.output_text.done", done)

		final := newResponsesFinal(respID, msgID, model, created, full)
		writeSSEEvent(w, "response.completed", map[string]interface{}{
			"type":     "response.completed",
			"response": final,
		})
		flusher.Flush()
		return
	}

	full, err := s.performChat(r.Context(), conv, finalQuery, opts.DeepThinking, opts.OnlineSearch, nil)
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "upstream_error")
		return
	}

	resp := newResponsesFinal(newID("resp"), newID("msg"), model, time.Now().Unix(), full)
	writeJSON(w, resp)
}

func (s *Server) handleClaudeMessages(w http.ResponseWriter, r *http.Request) {
	body, err := readJSONBody(r)
	if err != nil {
		writeClaudeError(w, http.StatusBadRequest, "invalid_json")
		return
	}

	systemPrompt, userText := extractClaudeMessages(body)
	if userText == "" {
		writeClaudeError(w, http.StatusBadRequest, "missing_user_message")
		return
	}

	opts := parseRequestOptions(body, r)

	userKey := extractUserKey(r)
	conversationID := r.Header.Get("ConversationId")
	conv, err := s.store.GetConversation(userKey, conversationID)
	if err != nil {
		writeClaudeError(w, http.StatusInternalServerError, "store_error")
		return
	}

	finalQuery := buildFinalQuery(systemPrompt, userText)
	model := opts.Model

	if opts.Stream {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeClaudeError(w, http.StatusInternalServerError, "stream_unsupported")
			return
		}

		msgID := newID("msg")
		messageStart := newClaudeMessageStart(msgID, model)
		writeSSEEvent(w, "message_start", messageStart)
		writeSSEEvent(w, "content_block_start", newClaudeContentStart())
		flusher.Flush()

		onChunk := func(text string) {
			writeSSEEvent(w, "content_block_delta", newClaudeContentDelta(text))
			flusher.Flush()
		}

		full, err := s.performChat(r.Context(), conv, finalQuery, opts.DeepThinking, opts.OnlineSearch, onChunk)
		if err != nil {
			return
		}

		writeSSEEvent(w, "content_block_stop", newClaudeContentStop())
		writeSSEEvent(w, "message_delta", newClaudeMessageDelta())
		writeSSEEvent(w, "message_stop", map[string]interface{}{"type": "message_stop"})
		flusher.Flush()
		_ = full
		return
	}

	full, err := s.performChat(r.Context(), conv, finalQuery, opts.DeepThinking, opts.OnlineSearch, nil)
	if err != nil {
		writeClaudeError(w, http.StatusBadGateway, "upstream_error")
		return
	}

	resp := newClaudeMessage(full, model)
	writeJSON(w, resp)
}

func (s *Server) performChat(ctx context.Context, conv *Conversation, query string, deepThinking, onlineSearch bool, onChunk func(string)) (string, error) {
	atomic.AddInt32(&conv.InUse, 1)
	defer atomic.AddInt32(&conv.InUse, -1)

	conv.mu.Lock()
	conv.LastActive = time.Now()
	full, err := s.miui.Chat(ctx, conv, query, deepThinking, onlineSearch, onChunk)
	if err == nil && strings.TrimSpace(full) != "" {
		conv.History = append(conv.History, Message{Source: "user", Content: query})
		conv.History = append(conv.History, Message{Source: "assistant", Content: full})
		conv.Dirty = true
	}
	conv.LastActive = time.Now()
	conv.mu.Unlock()

	return full, err
}

func readJSONBody(r *http.Request) (map[string]interface{}, error) {
	defer r.Body.Close()
	data, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return map[string]interface{}{}, nil
	}
	var body map[string]interface{}
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, err
	}
	return body, nil
}

func parseRequestOptions(body map[string]interface{}, r *http.Request) RequestOptions {
	opts := RequestOptions{
		Stream: getBool(body, "stream"),
		Model:  normalizeModel(body["model"]),
	}

	deepThinking, ok := getBoolOptional(body, "deep_thinking", "deepThinking", "isDeepThinking")
	if !ok {
		deepThinking = true
	}
	onlineSearch, ok := getBoolOptional(body, "online_search", "onlineSearch")
	if !ok {
		onlineSearch = true
	}

	if headerBool(r, "X-Deep-Thinking") {
		deepThinking = true
	}
	if headerBool(r, "X-Online-Search") {
		onlineSearch = true
	}
	if headerBool(r, "X-Disable-Search") {
		onlineSearch = false
	}

	modelDeep, modelSearch, modelHasFlag := parseModelFlags(body["model"])
	if modelHasFlag {
		if modelDeep && modelSearch {
			deepThinking = true
			onlineSearch = true
		} else if modelDeep {
			deepThinking = true
			onlineSearch = false
		} else if modelSearch {
			deepThinking = false
			onlineSearch = true
		}
	}

	opts.DeepThinking = deepThinking
	opts.OnlineSearch = onlineSearch
	return opts
}

func extractUserKey(r *http.Request) string {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if auth == "" {
		return newUserKey()
	}
	lower := strings.ToLower(auth)
	if strings.HasPrefix(lower, "bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	return auth
}

func normalizeModel(model any) string {
	modelStr, _ := model.(string)
	if modelStr == "" {
		return "DOUBAO"
	}
	return "DOUBAO"
}

func parseModelFlags(model any) (bool, bool, bool) {
	modelStr, _ := model.(string)
	if modelStr == "" {
		return false, false, false
	}
	modelStr = strings.ToLower(modelStr)
	deep := strings.Contains(modelStr, "-thinking")
	search := strings.Contains(modelStr, "-search")
	return deep, search, deep || search
}

func buildFinalQuery(systemPrompt, userText string) string {
	if systemPrompt != "" {
		return systemPrompt + "\n\n用户输入：" + userText
	}
	return userText
}

func getBool(body map[string]interface{}, keys ...string) bool {
	val, _ := getBoolOptional(body, keys...)
	return val
}

func getBoolOptional(body map[string]interface{}, keys ...string) (bool, bool) {
	for _, key := range keys {
		if v, ok := body[key]; ok {
			if b, ok := v.(bool); ok {
				return b, true
			}
		}
	}
	return false, false
}

func headerBool(r *http.Request, key string) bool {
	val := strings.TrimSpace(r.Header.Get(key))
	if val == "" {
		return false
	}
	val = strings.ToLower(val)
	return val == "1" || val == "true" || val == "yes" || val == "on"
}

func extractMessages(raw interface{}) (string, string) {
	msgs, ok := raw.([]interface{})
	if !ok {
		return "", ""
	}

	var systemParts []string
	var userText string
	for _, item := range msgs {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		role, _ := m["role"].(string)
		content := extractContent(m["content"])
		switch role {
		case "system":
			if content != "" {
				systemParts = append(systemParts, content)
			}
		case "user":
			if content != "" {
				userText = content
			}
		}
	}
	return strings.Join(systemParts, "\n"), userText
}

func extractResponsesInput(raw interface{}) (string, string) {
	switch v := raw.(type) {
	case string:
		return "", v
	case []interface{}:
		if len(v) == 0 {
			return "", ""
		}
		if msg, ok := v[0].(map[string]interface{}); ok {
			if _, hasRole := msg["role"]; hasRole {
				return extractMessages(v)
			}
		}
		return "", extractContent(v)
	default:
		return "", ""
	}
}

func extractClaudeMessages(body map[string]interface{}) (string, string) {
	systemPrompt := extractContent(body["system"])
	systemParts := []string{}
	if systemPrompt != "" {
		systemParts = append(systemParts, systemPrompt)
	}

	msgsRaw, ok := body["messages"]
	if !ok {
		return strings.Join(systemParts, "\n"), ""
	}
	msgs, ok := msgsRaw.([]interface{})
	if !ok {
		return strings.Join(systemParts, "\n"), ""
	}

	var userText string
	for _, item := range msgs {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		role, _ := m["role"].(string)
		if role != "user" {
			continue
		}
		content := extractContent(m["content"])
		if content != "" {
			userText = content
		}
	}

	return strings.Join(systemParts, "\n"), userText
}

func extractContent(raw interface{}) string {
	switch v := raw.(type) {
	case string:
		return v
	case []interface{}:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			part := extractContent(item)
			if part != "" {
				parts = append(parts, part)
			}
		}
		return strings.Join(parts, "")
	case map[string]interface{}:
		if text, ok := v["text"].(string); ok {
			return text
		}
		if content, ok := v["content"]; ok {
			return extractContent(content)
		}
		return ""
	default:
		return ""
	}
}

func writeJSON(w http.ResponseWriter, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	data, _ := json.Marshal(payload)
	_, _ = w.Write(data)
}

func writeOpenAIError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resp := map[string]interface{}{
		"error": map[string]interface{}{
			"message": msg,
			"type":    "invalid_request_error",
			"param":   nil,
			"code":    nil,
		},
	}
	data, _ := json.Marshal(resp)
	_, _ = w.Write(data)
}

func writeClaudeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resp := map[string]interface{}{
		"type": "error",
		"error": map[string]interface{}{
			"type":    "invalid_request_error",
			"message": msg,
		},
	}
	data, _ := json.Marshal(resp)
	_, _ = w.Write(data)
}

func writeSSEData(w http.ResponseWriter, payload interface{}) {
	data, _ := json.Marshal(payload)
	writeSSELine(w, "data: "+string(data)+"\n\n")
}

func writeSSEEvent(w http.ResponseWriter, event string, payload interface{}) {
	data, _ := json.Marshal(payload)
	writeSSELine(w, "event: "+event+"\n")
	writeSSELine(w, "data: "+string(data)+"\n\n")
}

func writeSSELine(w http.ResponseWriter, line string) {
	_, _ = w.Write([]byte(line))
}

func newChatCompletionResponse(model, content string) map[string]interface{} {
	return map[string]interface{}{
		"id":      newID("chatcmpl"),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"message": map[string]interface{}{
					"role":    "assistant",
					"content": content,
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]interface{}{
			"prompt_tokens":     0,
			"completion_tokens": 0,
			"total_tokens":      0,
		},
	}
}

type chatChunk struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role    string `json:"role,omitempty"`
			Content string `json:"content,omitempty"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
}

func newChatChunk(id string, created int64, model string, content string, includeRole bool) chatChunk {
	chunk := chatChunk{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: make([]struct {
			Index int `json:"index"`
			Delta struct {
				Role    string `json:"role,omitempty"`
				Content string `json:"content,omitempty"`
			} `json:"delta"`
			FinishReason *string `json:"finish_reason"`
		}, 1),
	}
	chunk.Choices[0].Index = 0
	if includeRole {
		chunk.Choices[0].Delta.Role = "assistant"
	}
	chunk.Choices[0].Delta.Content = content
	return chunk
}

func newResponsesBase(respID, msgID, model string, created int64) map[string]interface{} {
	return map[string]interface{}{
		"id":         respID,
		"object":     "response",
		"created_at": created,
		"model":      model,
		"output":     []interface{}{},
	}
}

func newResponsesFinal(respID, msgID, model string, created int64, content string) map[string]interface{} {
	return map[string]interface{}{
		"id":         respID,
		"object":     "response",
		"created_at": created,
		"model":      model,
		"output": []map[string]interface{}{
			{
				"id":   msgID,
				"type": "message",
				"role": "assistant",
				"content": []map[string]interface{}{
					{
						"type": "output_text",
						"text": content,
					},
				},
			},
		},
		"output_text": content,
		"usage": map[string]interface{}{
			"input_tokens":  0,
			"output_tokens": 0,
			"total_tokens":  0,
		},
	}
}

func responseDeltaEvent(msgID, text string) map[string]interface{} {
	return map[string]interface{}{
		"type":          "response.output_text.delta",
		"delta":         text,
		"output_index":  0,
		"content_index": 0,
		"item_id":       msgID,
	}
}

func responseDoneEvent(msgID, text string) map[string]interface{} {
	return map[string]interface{}{
		"type":          "response.output_text.done",
		"text":          text,
		"output_index":  0,
		"content_index": 0,
		"item_id":       msgID,
	}
}

func newClaudeMessage(content, model string) map[string]interface{} {
	return map[string]interface{}{
		"id":    newID("msg"),
		"type":  "message",
		"role":  "assistant",
		"model": model,
		"content": []map[string]interface{}{
			{"type": "text", "text": content},
		},
		"stop_reason":   "end_turn",
		"stop_sequence": nil,
		"usage": map[string]interface{}{
			"input_tokens":  0,
			"output_tokens": 0,
		},
	}
}

func newClaudeMessageStart(msgID, model string) map[string]interface{} {
	return map[string]interface{}{
		"type": "message_start",
		"message": map[string]interface{}{
			"id":      msgID,
			"type":    "message",
			"role":    "assistant",
			"model":   model,
			"content": []map[string]interface{}{},
		},
	}
}

func newClaudeContentStart() map[string]interface{} {
	return map[string]interface{}{
		"type":  "content_block_start",
		"index": 0,
		"content_block": map[string]interface{}{
			"type": "text",
			"text": "",
		},
	}
}

func newClaudeContentDelta(text string) map[string]interface{} {
	return map[string]interface{}{
		"type":  "content_block_delta",
		"index": 0,
		"delta": map[string]interface{}{
			"type": "text_delta",
			"text": text,
		},
	}
}

func newClaudeContentStop() map[string]interface{} {
	return map[string]interface{}{
		"type":  "content_block_stop",
		"index": 0,
	}
}

func newClaudeMessageDelta() map[string]interface{} {
	return map[string]interface{}{
		"type": "message_delta",
		"delta": map[string]interface{}{
			"stop_reason":   "end_turn",
			"stop_sequence": nil,
		},
	}
}

func newID(prefix string) string {
	return prefix + "_" + strings.TrimPrefix(newUserKey(), "anon_")
}
