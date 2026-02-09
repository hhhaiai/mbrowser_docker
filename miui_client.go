package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

const miuiEndpoint = "https://ai.search.miui.com/api/llm/browser/query"

type MiuiClient struct {
	httpClient *http.Client
	headers    map[string]string
}

func NewMiuiClient() *MiuiClient {
	return &MiuiClient{
		httpClient: &http.Client{
			Timeout: 0,
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				MaxIdleConns:          512,
				MaxIdleConnsPerHost:   256,
				MaxConnsPerHost:       256,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
			},
		},
		headers: map[string]string{
			"sec-ch-ua-platform": `"Android"`,
			"user-agent":         "Mozilla/5.0 (Linux; U; Android 11; zh-cn; M2012K11AC Build/RKQ1.200826.002) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/135.0.7049.79 Mobile Safari/537.36 XiaoMi/MiuiBrowser/20.11.1010115",
			"accept":             "text/event-stream",
			"content-type":       "application/json",
			"origin":             "https://ai.search.miui.com",
			"referer":            "https://ai.search.miui.com/browserAiSearch/?source=homepage",
		},
	}
}

type miuiStreamChunk struct {
	Answer        string `json:"answer"`
	IntentionInfo *struct {
		IntentionText string `json:"intentionText"`
		End           bool   `json:"end"`
	} `json:"intentionInfo"`
}

func compressHistory(history []Message) ([]int, error) {
	data, err := json.Marshal(history)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(data); err != nil {
		_ = gz.Close()
		return nil, err
	}
	_ = gz.Close()

	raw := buf.Bytes()
	out := make([]int, len(raw))
	for i, b := range raw {
		out[i] = int(b)
	}
	return out, nil
}

type MiuiPayload struct {
	Content          string                 `json:"content"`
	OAID             string                 `json:"oaid"`
	ChatType         string                 `json:"chatType"`
	SearchID         string                 `json:"searchId"`
	MiID             string                 `json:"miId"`
	Model            string                 `json:"model"`
	Business         string                 `json:"business"`
	ConversationID   string                 `json:"conversationId"`
	SupportVideo     bool                   `json:"supportVideo"`
	AppVersionCode   string                 `json:"appVersionCode"`
	DeviceType       string                 `json:"deviceType"`
	DeviceModel      string                 `json:"deviceModel"`
	Scene            string                 `json:"scene"`
	RawLastQueryList []int                  `json:"rawLastQueryList"`
	OnlineSearch     bool                   `json:"onlineSearch"`
	AiShootingMode   map[string]interface{} `json:"aiShootingMode"`
	IsUnLoginSystem  bool                   `json:"isUnLoginSystem"`
	QuerySource      string                 `json:"querySource"`
	IsDeepThinking   bool                   `json:"isDeepThinking,omitempty"`
}

func (c *MiuiClient) Chat(ctx context.Context, conv *Conversation, query string, deepThinking, onlineSearch bool, onChunk func(string)) (string, error) {
	rawHistory, err := compressHistory(conv.History)
	if err != nil {
		return "", err
	}

	payload := MiuiPayload{
		Content:          query,
		OAID:             conv.OAID,
		ChatType:         "SUMMARY",
		SearchID:         newSearchID(conv.OAID),
		MiID:             conv.MiID,
		Model:            "DOUBAO",
		Business:         "BROWSER",
		ConversationID:   conv.InternalID,
		SupportVideo:     true,
		AppVersionCode:   "201110100",
		DeviceType:       "phone",
		DeviceModel:      "M2012K11AC",
		Scene:            "main",
		RawLastQueryList: rawHistory,
		OnlineSearch:     onlineSearch,
		AiShootingMode:   map[string]interface{}{},
		IsUnLoginSystem:  false,
		QuerySource:      "operationWord",
	}
	if deepThinking {
		payload.IsDeepThinking = true
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, miuiEndpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", errors.New("miui upstream http " + resp.Status)
	}

	reader := bufio.NewReader(resp.Body)
	var full strings.Builder

	for {
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return full.String(), err
		}
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			jsonStr := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if jsonStr == "[DONE]" {
				break
			}
			var chunk miuiStreamChunk
			if err := json.Unmarshal([]byte(jsonStr), &chunk); err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				if err == io.ErrUnexpectedEOF {
					continue
				}
				// ignore malformed chunk
				continue
			}
			if chunk.Answer != "" {
				full.WriteString(chunk.Answer)
				if onChunk != nil {
					onChunk(chunk.Answer)
				}
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}

	return full.String(), nil
}
