package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

var defaultHTTPClient = &http.Client{Timeout: 180 * time.Second}

// AiModelOption adalah satu entri daftar model dari provider.
type AiModelOption struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

func normalizeAiBaseURL(url string) string {
	return strings.TrimRight(strings.TrimSpace(url), "/")
}

func aiAuthHeader(apiKey string) string {
	return "Bearer " + apiKey
}

// FetchAiModels memanggil GET {base_url}/models dan mengembalikan daftar model.
// Kompatibel dengan Solvatra (https://solvatra.web.id/v1), OpenRouter,
// dan provider lain yang mengikuti skema OpenAI.
func FetchAiModels(ctx context.Context, baseURL, apiKey string) ([]AiModelOption, error) {
	url := normalizeAiBaseURL(baseURL) + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("buat request gagal: %w", err)
	}
	req.Header.Set("Authorization", aiAuthHeader(apiKey))

	res, err := defaultHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gagal menghubungi endpoint: %w", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("gagal mengambil daftar model — %s", aiErrorMessage(body, res.Status))
	}

	var data struct {
		Data any `json:"data"`
	}
	var list []any
	if err := json.Unmarshal(body, &data); err == nil && data.Data != nil {
		if arr, ok := data.Data.([]any); ok {
			list = arr
		}
	} else {
		var arr []any
		if err := json.Unmarshal(body, &arr); err != nil {
			return nil, fmt.Errorf("respons daftar model bukan JSON valid")
		}
		list = arr
	}

	models := make([]AiModelOption, 0, len(list))
	for _, m := range list {
		switch v := m.(type) {
		case string:
			models = append(models, AiModelOption{ID: v})
		case map[string]any:
			id, _ := v["id"].(string)
			if id == "" {
				continue
			}
			name, _ := v["name"].(string)
			models = append(models, AiModelOption{ID: id, Name: name})
		}
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}

// ChatMessage adalah satu pesan percakapan (tanpa tool).
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// CallAiChat memanggil POST {base_url}/chat/completions dan mengembalikan teks jawaban.
func CallAiChat(ctx context.Context, baseURL, apiKey, model string, messages []ChatMessage, temperature *float64) (string, error) {
	url := normalizeAiBaseURL(baseURL) + "/chat/completions"
	payload := map[string]any{
		"model":    model,
		"messages": messages,
		"stream":   true,
	}
	if temperature != nil {
		payload["temperature"] = *temperature
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("buat payload gagal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("buat request gagal: %w", err)
	}
	req.Header.Set("Authorization", aiAuthHeader(apiKey))
	req.Header.Set("Content-Type", "application/json")

	res, err := defaultHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("gagal menghubungi provider AI: %w", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", fmt.Errorf("provider AI menolak permintaan — %s", aiErrorMessage(body, res.Status))
	}

	content, toolCalls := parseAiCompletion(body)
	_ = toolCalls
	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("provider AI tidak mengembalikan jawaban teks")
	}
	return content, nil
}

func aiErrorMessage(body []byte, fallback string) string {
	var data struct {
		Error any `json:"error"`
	}
	if err := json.Unmarshal(body, &data); err == nil && data.Error != nil {
		switch e := data.Error.(type) {
		case string:
			if strings.TrimSpace(e) != "" {
				return e
			}
		case map[string]any:
			if msg, ok := e["message"].(string); ok && strings.TrimSpace(msg) != "" {
				return msg
			}
		}
	}
	var alt struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &alt); err == nil && strings.TrimSpace(alt.Message) != "" {
		return alt.Message
	}
	return fallback
}

// parseAiCompletion menangani respons JSON biasa maupun SSE ("data: {...}") —
// mengikuti perilaku lib/ai-provider.ts di BARI (sebagian router hanya
// mengembalikan content lengkap pada mode stream).
func parseAiCompletion(body []byte) (string, []string) {
	trimmed := bytes.TrimSpace(body)
	if !bytes.HasPrefix(trimmed, []byte("data:")) {
		var data struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(trimmed, &data); err == nil && len(data.Choices) > 0 {
			return data.Choices[0].Message.Content, nil
		}
		return string(trimmed), nil
	}

	var sb strings.Builder
	for _, line := range strings.Split(string(body), "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(t, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content *string `json:"content"`
				} `json:"delta"`
				Message struct {
					Content *string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		if chunk.Error != nil && strings.TrimSpace(chunk.Error.Message) != "" {
			sb.WriteString("")
		}
		if len(chunk.Choices) > 0 {
			if c := chunk.Choices[0].Delta.Content; c != nil {
				sb.WriteString(*c)
			} else if c := chunk.Choices[0].Message.Content; c != nil {
				sb.WriteString(*c)
			}
		}
	}
	return sb.String(), nil
}
