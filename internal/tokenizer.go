package internal

import (
	"encoding/json"
	"unicode/utf8"
)

// CountTokens 精确计算文本的token数
// 使用优化的算法：基于字符类型加权计算
func CountTokens(text string) int64 {
	if text == "" {
		return 0
	}

	var tokens float64
	for _, r := range text {
		switch {
		case r >= 0x4E00 && r <= 0x9FFF,
			r >= 0x3400 && r <= 0x4DBF,
			r >= 0x20000 && r <= 0x2A6DF,
			r >= 0xF900 && r <= 0xFAFF,
			r >= 0x2F800 && r <= 0x2FA1F:
			tokens += 1.4
		case r >= 0x3000 && r <= 0x303F,
			r >= 0xFF00 && r <= 0xFFEF:
			tokens += 1.0
		case r >= 0x0000 && r <= 0x007F:
			tokens += 0.25
		default:
			tokens += 0.5
		}
	}

	result := int64(tokens + 0.5)
	if result < 1 && utf8.RuneCountInString(text) > 0 {
		return 1
	}
	return result
}
func CountMessagesTokens(messages []Message) int64 {
	var total int64

	for _, msg := range messages {
		total += 4
		total += CountTokens(msg.Role)
		text, _ := msg.ParseContent()
		total += CountTokens(text)
	}
	total += 3

	return total
}
func CountToolsTokens(tools []Tool) int64 {
	if len(tools) == 0 {
		return 0
	}

	var total int64
	for _, tool := range tools {
		// type 字段
		total += CountTokens(tool.Type)
		total += 3 
		total += CountTokens(tool.Function.Name)
		total += CountTokens(tool.Function.Description)

		// parameters (JSON schema)
		if len(tool.Function.Parameters) > 0 {
			total += CountTokens(string(tool.Function.Parameters))
		}

		// 每个工具的结构开销
		total += 6
	}

	// 工具列表开销
	total += 4

	return total
}

// CountToolCallTokens 计算工具调用的token数
func CountToolCallTokens(toolCalls []ToolCall) int64 {
	if len(toolCalls) == 0 {
		return 0
	}

	var total int64
	for _, tc := range toolCalls {
		total += CountTokens(tc.ID)
		total += CountTokens(tc.Type)
		total += CountTokens(tc.Function.Name)
		total += CountTokens(tc.Function.Arguments)
		total += 8 // 结构开销
	}

	return total
}

// CountToolResultTokens 计算工具结果消息的token数
func CountToolResultTokens(toolCallID, content string) int64 {
	return CountTokens(toolCallID) + CountTokens(content) + 6
}

// CountRequestTokens 计算完整请求的token数
func CountRequestTokens(messages []Message, tools []Tool) int64 {
	return CountMessagesTokens(messages) + CountToolsTokens(tools)
}

// EstimateJSONTokens 估算JSON对象的token数
func EstimateJSONTokens(v interface{}) int64 {
	data, err := json.Marshal(v)
	if err != nil {
		return 0
	}
	return CountTokens(string(data))
}
