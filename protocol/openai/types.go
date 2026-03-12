package openai

import "encoding/json"

// --- 请求类型定义 ---

// ChatCompletionRequest OpenAI Chat Completion 请求体。
type ChatCompletionRequest struct {
	Model            string        `json:"model"`
	Messages         []ChatMessage `json:"messages"`
	MaxTokens        *int          `json:"max_tokens,omitempty"`
	Temperature      *float64      `json:"temperature,omitempty"`
	TopP             *float64      `json:"top_p,omitempty"`
	Stream           bool          `json:"stream"`
	Stop             []string      `json:"stop,omitempty"`
	PresencePenalty  *float64      `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float64      `json:"frequency_penalty,omitempty"`
	Tools            []ChatTool    `json:"tools,omitempty"`
	ToolChoice       any           `json:"tool_choice,omitempty"`
	ReasoningEffort  *string       `json:"reasoning_effort,omitempty"`
	ThinkingEnabled  *bool         `json:"thinking_enabled,omitempty"`
	ThinkingTokens   *int          `json:"thinking_tokens,omitempty"`
	User             string        `json:"user,omitempty"`
}

// ChatMessage OpenAI 消息。
// Content 使用 json.RawMessage 以支持纯文本 (string) 和多模态 ([]contentPart) 两种格式。
type ChatMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content,omitempty"`
	ToolCalls  []ChatToolCall  `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Name       string          `json:"name,omitempty"`
}

// ChatToolCall 消息中的工具调用。
type ChatToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ChatToolCallFunc `json:"function"`
}

// ChatToolCallFunc 工具调用的函数信息。
type ChatToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ChatTool OpenAI 工具定义。
type ChatTool struct {
	Type     string       `json:"type"`
	Function ChatToolFunc `json:"function"`
}

// ChatToolFunc 工具函数定义。
type ChatToolFunc struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// contentPart 多模态内容块。
type contentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

// imageURL 图片 URL。
type imageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// --- 响应类型定义 ---

// ChatCompletionResponse OpenAI Chat Completion 非流式响应体。
type ChatCompletionResponse struct {
	ID                string              `json:"id"`
	Object            string              `json:"object"`
	Created           int64               `json:"created"`
	Model             string              `json:"model"`
	Choices           []ChatCompletionChoice `json:"choices"`
	Usage             *ChatCompletionUsage   `json:"usage,omitempty"`
	SystemFingerprint *string             `json:"system_fingerprint,omitempty"`
}

// ChatCompletionChoice 非流式响应中的 choice。
type ChatCompletionChoice struct {
	Index        int                    `json:"index"`
	Message      ChatCompletionMessage  `json:"message"`
	FinishReason *string                `json:"finish_reason"`
}

// ChatCompletionChunkResponse OpenAI Chat Completion 流式响应 chunk。
type ChatCompletionChunkResponse struct {
	ID                string                    `json:"id"`
	Object            string                    `json:"object"`
	Created           int64                     `json:"created"`
	Model             string                    `json:"model"`
	Choices           []ChatCompletionChunkChoice `json:"choices"`
	Usage             *ChatCompletionUsage      `json:"usage,omitempty"`
	SystemFingerprint *string                   `json:"system_fingerprint,omitempty"`
}

// ChatCompletionChunkChoice 流式响应中的 choice。
type ChatCompletionChunkChoice struct {
	Index        int                   `json:"index"`
	Delta        ChatCompletionDelta   `json:"delta"`
	FinishReason *string               `json:"finish_reason"`
}

// ChatCompletionMessage 非流式响应中的消息。
type ChatCompletionMessage struct {
	Role             string          `json:"role"`
	Content          *string         `json:"content"`
	ReasoningContent *string         `json:"reasoning_content,omitempty"`
	ToolCalls        []ChatToolCall  `json:"tool_calls,omitempty"`
}

// ChatCompletionDelta 流式响应中的增量消息。
type ChatCompletionDelta struct {
	Role             string          `json:"role,omitempty"`
	Content          *string         `json:"content,omitempty"`
	ReasoningContent *string         `json:"reasoning_content,omitempty"`
	ToolCalls        []ChatToolCallDelta `json:"tool_calls,omitempty"`
}

// ChatToolCallDelta 流式响应中的工具调用增量。
type ChatToolCallDelta struct {
	Index    *int             `json:"index,omitempty"`
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type,omitempty"`
	Function ChatToolCallFunc `json:"function,omitempty"`
}

// ChatCompletionUsage token 使用信息。
type ChatCompletionUsage struct {
	PromptTokens        int                       `json:"prompt_tokens"`
	CompletionTokens    int                       `json:"completion_tokens"`
	TotalTokens         int                       `json:"total_tokens"`
	PromptTokensDetails *ChatPromptTokensDetails  `json:"prompt_tokens_details,omitempty"`
}

// ChatPromptTokensDetails prompt token 详细信息。
type ChatPromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

// --- 错误响应类型 ---

// ErrorResponse OpenAI 格式的错误响应。
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail 错误详情。
type ErrorDetail struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Param   *string `json:"param,omitempty"`
	Code    *string `json:"code,omitempty"`
}
