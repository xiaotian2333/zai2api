package internal

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Tool OpenAI 工具定义
type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function,omitempty"`
}

// ToolFunction 函数定义
type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// ToolCall 工具调用
type ToolCall struct {
	Index    int              `json:"index,omitempty"`
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction 工具调用的函数信息
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

var (
	toolCallFencePattern = regexp.MustCompile("(?s)```json\\s*(\\{.*?\\})\\s*```")
	functionCallPattern  = regexp.MustCompile(`(?s)调用函数\s*[：:]\s*([\w\-\.]+)\s*(?:参数|arguments)[：:]\s*(\{.*?\})`)
)

// GenerateToolPrompt 生成工具注入提示
func GenerateToolPrompt(tools []Tool) string {
	if len(tools) == 0 {
		return ""
	}

	var toolDefs []string
	for _, tool := range tools {
		if tool.Type != "function" {
			continue
		}

		fn := tool.Function
		toolInfo := fmt.Sprintf("## %s\n**Purpose**: %s", fn.Name, fn.Description)

		// 解析参数
		if len(fn.Parameters) > 0 {
			var params struct {
				Type       string                 `json:"type"`
				Properties map[string]interface{} `json:"properties"`
				Required   []string               `json:"required"`
			}
			if err := json.Unmarshal(fn.Parameters, &params); err == nil && len(params.Properties) > 0 {
				requiredSet := make(map[string]bool)
				for _, r := range params.Required {
					requiredSet[r] = true
				}

				toolInfo += "\n**Parameters**:"
				for name, details := range params.Properties {
					detailMap, _ := details.(map[string]interface{})
					paramType, _ := detailMap["type"].(string)
					paramDesc, _ := detailMap["description"].(string)
					reqFlag := "*Optional*"
					if requiredSet[name] {
						reqFlag = "**Required**"
					}
					toolInfo += fmt.Sprintf("\n- `%s` (%s) - %s: %s", name, paramType, reqFlag, paramDesc)
				}
			}
		}

		toolDefs = append(toolDefs, toolInfo)
	}

	if len(toolDefs) == 0 {
		return ""
	}

	return "\n\n# AVAILABLE FUNCTIONS\n" + strings.Join(toolDefs, "\n\n---\n") + `

# USAGE INSTRUCTIONS
When you need to execute a function, respond ONLY with a JSON object containing tool_calls:
` + "```json" + `
{
  "tool_calls": [
    {
      "id": "call_xxx",
      "type": "function",
      "function": {
        "name": "function_name",
        "arguments": "{\"param1\": \"value1\"}"
      }
    }
  ]
}
` + "```" + `
Important: No explanatory text before or after the JSON. The 'arguments' field must be a JSON string, not an object.
`
}

// ProcessMessagesWithTools 处理消息并注入工具提示
func ProcessMessagesWithTools(messages []Message, tools []Tool, toolChoice interface{}) []Message {
	if !Cfg.ToolSupport || len(tools) == 0 {
		return messages
	}

	// 检查 tool_choice
	if tc, ok := toolChoice.(string); ok && tc == "none" {
		return messages
	}

	toolPrompt := GenerateToolPrompt(tools)
	if toolPrompt == "" {
		return messages
	}

	processed := make([]Message, len(messages))
	copy(processed, messages)

	// 找到 system 消息并追加工具提示
	hasSystem := false
	for i, msg := range processed {
		if msg.Role == "system" {
			hasSystem = true
			content, _ := msg.ParseContent()
			processed[i].Content = content + toolPrompt
			break
		}
	}

	// 如果没有 system 消息，创建一个
	if !hasSystem {
		systemMsg := Message{
			Role:    "system",
			Content: "你是一个有用的助手。" + toolPrompt,
		}
		processed = append([]Message{systemMsg}, processed...)
	}

	// 添加工具使用提示
	if toolChoice == "required" || toolChoice == "auto" {
		if len(processed) > 0 && processed[len(processed)-1].Role == "user" {
			content, _ := processed[len(processed)-1].ParseContent()
			processed[len(processed)-1].Content = content + "\n\n请根据需要使用提供的工具函数。"
		}
	}

	return processed
}

// ExtractToolInvocations 从响应文本中提取工具调用
func ExtractToolInvocations(text string) []ToolCall {
	if text == "" {
		return nil
	}

	// 限制扫描范围
	scanText := text
	if len(scanText) > Cfg.ScanLimit {
		scanText = scanText[:Cfg.ScanLimit]
	}
	matches := toolCallFencePattern.FindAllStringSubmatch(scanText, -1)
	for _, match := range matches {
		if len(match) > 1 {
			if calls := parseToolCallsJSON(match[1]); calls != nil {
				return calls
			}
		}
	}
	if calls := extractInlineToolCalls(scanText); calls != nil {
		return calls
	}

	// 方法3: 解析自然语言函数调用
	if match := functionCallPattern.FindStringSubmatch(scanText); len(match) > 2 {
		funcName := strings.TrimSpace(match[1])
		argsStr := strings.TrimSpace(match[2])
		var args interface{}
		if err := json.Unmarshal([]byte(argsStr), &args); err == nil {
			return []ToolCall{{
				ID:   generateCallID(),
				Type: "function",
				Function: ToolCallFunction{
					Name:      funcName,
					Arguments: argsStr,
				},
			}}
		}
	}

	return nil
}

func parseToolCallsJSON(jsonStr string) []ToolCall {
	var data struct {
		ToolCalls []struct {
			ID       string      `json:"id"`
			Type     string      `json:"type"`
			Function interface{} `json:"function"`
		} `json:"tool_calls"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		return nil
	}

	if len(data.ToolCalls) == 0 {
		return nil
	}

	var calls []ToolCall
	for _, tc := range data.ToolCalls {
		call := ToolCall{
			ID:   tc.ID,
			Type: tc.Type,
		}
		if call.Type == "" {
			call.Type = "function"
		}

		// 处理 function 字段
		if fn, ok := tc.Function.(map[string]interface{}); ok {
			call.Function.Name, _ = fn["name"].(string)
			if args, ok := fn["arguments"]; ok {
				switch v := args.(type) {
				case string:
					call.Function.Arguments = v
				case map[string]interface{}:
					if b, err := json.Marshal(v); err == nil {
						call.Function.Arguments = string(b)
					}
				}
			}
		}

		calls = append(calls, call)
	}

	return calls
}

func extractInlineToolCalls(text string) []ToolCall {
	for i := 0; i < len(text); i++ {
		if text[i] != '{' {
			continue
		}

		// 找匹配的右括号
		braceCount := 1
		inString := false
		escapeNext := false
		j := i + 1

		for j < len(text) && braceCount > 0 {
			if escapeNext {
				escapeNext = false
				j++
				continue
			}

			switch text[j] {
			case '\\':
				escapeNext = true
			case '"':
				if !escapeNext {
					inString = !inString
				}
			case '{':
				if !inString {
					braceCount++
				}
			case '}':
				if !inString {
					braceCount--
				}
			}
			j++
		}

		if braceCount == 0 {
			jsonStr := text[i:j]
			if calls := parseToolCallsJSON(jsonStr); calls != nil {
				return calls
			}
		}
	}

	return nil
}

// RemoveToolJSONContent 从响应中移除工具调用 JSON
func RemoveToolJSONContent(text string) string {
	// 移除代码块中的工具调用
	result := toolCallFencePattern.ReplaceAllStringFunc(text, func(match string) string {
		submatch := toolCallFencePattern.FindStringSubmatch(match)
		if len(submatch) > 1 {
			var data map[string]interface{}
			if err := json.Unmarshal([]byte(submatch[1]), &data); err == nil {
				if _, ok := data["tool_calls"]; ok {
					return ""
				}
			}
		}
		return match
	})

	// 移除内联工具调用 JSON
	result = removeInlineToolCallJSON(result)

	return strings.TrimSpace(result)
}

func removeInlineToolCallJSON(text string) string {
	var result strings.Builder
	i := 0

	for i < len(text) {
		if text[i] != '{' {
			result.WriteByte(text[i])
			i++
			continue
		}

		// 找匹配的右括号
		braceCount := 1
		inString := false
		escapeNext := false
		j := i + 1

		for j < len(text) && braceCount > 0 {
			if escapeNext {
				escapeNext = false
				j++
				continue
			}

			switch text[j] {
			case '\\':
				escapeNext = true
			case '"':
				if !escapeNext {
					inString = !inString
				}
			case '{':
				if !inString {
					braceCount++
				}
			case '}':
				if !inString {
					braceCount--
				}
			}
			j++
		}

		if braceCount == 0 {
			jsonStr := text[i:j]
			var data map[string]interface{}
			if err := json.Unmarshal([]byte(jsonStr), &data); err == nil {
				if _, ok := data["tool_calls"]; ok {
					// 跳过工具调用 JSON
					i = j
					continue
				}
			}
		}

		result.WriteByte(text[i])
		i++
	}

	return result.String()
}

func generateCallID() string {
	return fmt.Sprintf("call_%d", time.Now().UnixNano())
}
