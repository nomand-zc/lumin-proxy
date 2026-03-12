package anthropic

import "encoding/json"

// --- 请求类型定义 ---

// MessageRequest Anthropic Messages API 请求体。
// 参考: https://docs.anthropic.com/en/api/messages
type MessageRequest struct {
	Model         string          `json:"model"`
	Messages      []Message       `json:"messages"`
	System        json.RawMessage `json:"system,omitempty"` // string 或 []SystemBlock
	MaxTokens     int             `json:"max_tokens"`
	Temperature   *float64        `json:"temperature,omitempty"`
	TopP          *float64        `json:"top_p,omitempty"`
	TopK          *int            `json:"top_k,omitempty"`
	Stream        bool            `json:"stream,omitempty"`
	StopSequences []string        `json:"stop_sequences,omitempty"`
	Tools         []Tool          `json:"tools,omitempty"`
	ToolChoice    any             `json:"tool_choice,omitempty"`
	Metadata      *Metadata       `json:"metadata,omitempty"`
	Thinking      *ThinkingConfig `json:"thinking,omitempty"`
}

// Message Anthropic 消息。
type Message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"` // string 或 []ContentBlock
}

// SystemBlock 系统提示内容块。
type SystemBlock struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// ContentBlock 消息内容块（支持多种类型）。
type ContentBlock struct {
	Type string `json:"type"`

	// text 类型
	Text string `json:"text,omitempty"`

	// image 类型
	Source *ImageSource `json:"source,omitempty"`

	// tool_use 类型
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// tool_result 类型
	ToolUseID string          `json:"tool_use_id,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"` // string 或 []ContentBlock

	// thinking 类型
	Thinking string `json:"thinking,omitempty"`

	// cache_control
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// ImageSource 图片来源。
type ImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// Tool Anthropic 工具定义。
type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	InputSchema InputSchema `json:"input_schema"`
}

// InputSchema 工具输入 schema。
type InputSchema struct {
	Type       string             `json:"type"`
	Properties map[string]any     `json:"properties,omitempty"`
	Required   []string           `json:"required,omitempty"`
}

// Metadata 请求元数据。
type Metadata struct {
	UserID string `json:"user_id,omitempty"`
}

// ThinkingConfig 思考模式配置。
type ThinkingConfig struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens,omitempty"`
}

// CacheControl 缓存控制。
type CacheControl struct {
	Type string `json:"type"`
}

// --- 响应类型定义 ---

// MessageResponse Anthropic Messages API 响应体。
type MessageResponse struct {
	ID           string                `json:"id"`
	Type         string                `json:"type"` // "message"
	Role         string                `json:"role"` // "assistant"
	Content      []ResponseContent     `json:"content"`
	Model        string                `json:"model"`
	StopReason   *string               `json:"stop_reason,omitempty"`
	StopSequence *string               `json:"stop_sequence,omitempty"`
	Usage        *MessageUsage         `json:"usage,omitempty"`
}

// ResponseContent 响应内容块。
type ResponseContent struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ID       string          `json:"id,omitempty"`
	Name     string          `json:"name,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
	Thinking string          `json:"thinking,omitempty"`
}

// MessageUsage Token 使用信息。
type MessageUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

// --- SSE 流式事件类型定义 ---

// StreamEvent SSE 流式事件。
type StreamEvent struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"-"` // 原始 JSON 数据
}

// MessageStartEvent message_start 事件。
type MessageStartEvent struct {
	Type    string          `json:"type"`
	Message MessageResponse `json:"message"`
}

// ContentBlockStartEvent content_block_start 事件。
type ContentBlockStartEvent struct {
	Type         string          `json:"type"`
	Index        int             `json:"index"`
	ContentBlock ResponseContent `json:"content_block"`
}

// ContentBlockDeltaEvent content_block_delta 事件。
type ContentBlockDeltaEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Delta Delta  `json:"delta"`
}

// Delta 增量内容。
type Delta struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Thinking string `json:"thinking,omitempty"`
	// tool_use 的 partial_json
	PartialJSON string `json:"partial_json,omitempty"`
}

// ContentBlockStopEvent content_block_stop 事件。
type ContentBlockStopEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
}

// MessageDeltaEvent message_delta 事件。
type MessageDeltaEvent struct {
	Type  string       `json:"type"`
	Delta MessageDelta `json:"delta"`
	Usage *DeltaUsage  `json:"usage,omitempty"`
}

// MessageDelta 消息级别增量。
type MessageDelta struct {
	StopReason   *string `json:"stop_reason,omitempty"`
	StopSequence *string `json:"stop_sequence,omitempty"`
}

// DeltaUsage message_delta 中的 usage。
type DeltaUsage struct {
	OutputTokens int `json:"output_tokens"`
}

// --- 错误响应类型 ---

// ErrorResponse Anthropic 格式的错误响应。
type ErrorResponse struct {
	Type  string      `json:"type"` // "error"
	Error ErrorDetail `json:"error"`
}

// ErrorDetail 错误详情。
type ErrorDetail struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}
