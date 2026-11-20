package claude

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
)

// 本文件覆盖「Claude 渠道自动补 5 分钟缓存断点」的规则。
// 断点值固定 {"type":"ephemeral"}（上游默认 TTL 即 5 分钟），单请求最多 4 个。

func testStringPtr(value string) *string {
	return &value
}

// countCacheControl 统计请求里所有 cache_control 断点数量（顶层 / tools / system / 消息块）。
func countCacheControl(request *dto.ClaudeRequest) int {
	count := 0
	if len(request.CacheControl) > 0 {
		count++
	}
	for _, tool := range request.GetTools() {
		if typed, ok := tool.(*dto.Tool); ok && typed != nil && len(typed.CacheControl) > 0 {
			count++
		}
	}
	if system, ok := request.System.([]dto.ClaudeMediaMessage); ok {
		for _, block := range system {
			if len(block.CacheControl) > 0 {
				count++
			}
		}
	}
	for _, message := range request.Messages {
		if blocks, ok := message.Content.([]dto.ClaudeMediaMessage); ok {
			for _, block := range blocks {
				if len(block.CacheControl) > 0 {
					count++
				}
			}
		}
	}
	return count
}

// buildAutoCacheFullScenarioRequest 构造 2 个工具 + system + user/assistant(tool_calls)/tool/user 的多轮请求。
func buildAutoCacheFullScenarioRequest() *dto.GeneralOpenAIRequest {
	return &dto.GeneralOpenAIRequest{
		Model: "claude-sonnet-4-5",
		Messages: []dto.Message{
			{Role: "system", Content: "you are a coding agent"},
			{Role: "user", Content: "fix the bug"},
			{
				Role:      "assistant",
				ToolCalls: json.RawMessage(`[{"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"a.go\"}"}}]`),
			},
			{Role: "tool", ToolCallId: "call_1", Content: "package main"},
			{Role: "user", Content: "now explain"},
		},
		Tools: []dto.ToolCallRequest{
			{
				Type: "function",
				Function: dto.FunctionRequest{
					Name:        "read_file",
					Description: "read a file",
					Parameters: map[string]any{
						"type":       "object",
						"properties": map[string]any{"path": map[string]any{"type": "string"}},
						"required":   []string{"path"},
					},
				},
			},
			{
				Type: "function",
				Function: dto.FunctionRequest{
					Name:        "write_file",
					Description: "write a file",
					Parameters: map[string]any{
						"type":       "object",
						"properties": map[string]any{"path": map[string]any{"type": "string"}},
						"required":   []string{"path"},
					},
				},
			},
		},
	}
}

// 端到端：开关关闭时转换结果里没有任何 cache_control（默认行为完全不变）。
func TestConvertOpenAIRequestAutoCacheControlDisabledByDefault(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{}

	result, err := adaptor.ConvertOpenAIRequest(nil, info, buildAutoCacheFullScenarioRequest())
	require.NoError(t, err)

	claudeRequest, ok := result.(*dto.ClaudeRequest)
	require.True(t, ok)

	payload, err := common.Marshal(claudeRequest)
	require.NoError(t, err)
	require.NotContains(t, string(payload), "cache_control")
}

// 端到端：开关打开、完整场景，恰好 4 个断点且位置正确。
func TestConvertOpenAIRequestAutoCacheControlFullScenario(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelOtherSettings: dto.ChannelOtherSettings{ClaudeAutoCacheControl: true},
		},
	}

	result, err := adaptor.ConvertOpenAIRequest(nil, info, buildAutoCacheFullScenarioRequest())
	require.NoError(t, err)

	claudeRequest, ok := result.(*dto.ClaudeRequest)
	require.True(t, ok)
	require.Equal(t, 4, countCacheControl(claudeRequest))

	// 1. 只有最后一个函数工具加断点
	tools := claudeRequest.GetTools()
	require.Len(t, tools, 2)
	firstTool, ok := tools[0].(*dto.Tool)
	require.True(t, ok)
	require.Empty(t, firstTool.CacheControl)
	lastTool, ok := tools[1].(*dto.Tool)
	require.True(t, ok)
	require.JSONEq(t, `{"type":"ephemeral"}`, string(lastTool.CacheControl))

	// 2. system 最后一个非空 text 块
	system, ok := claudeRequest.System.([]dto.ClaudeMediaMessage)
	require.True(t, ok)
	require.Len(t, system, 1)
	require.Equal(t, "text", system[0].Type)
	require.Equal(t, `{"type":"ephemeral"}`, string(system[0].CacheControl))

	require.Len(t, claudeRequest.Messages, 4)
	// 第一条 user 消息保持原样（字符串内容不被改动）
	require.True(t, claudeRequest.Messages[0].IsStringContent())

	// 3. 最后一条消息：字符串 content 改成单个 text 块，文本不变
	lastBlocks, ok := claudeRequest.Messages[3].Content.([]dto.ClaudeMediaMessage)
	require.True(t, ok)
	require.Len(t, lastBlocks, 1)
	require.Equal(t, "text", lastBlocks[0].Type)
	require.NotNil(t, lastBlocks[0].Text)
	require.Equal(t, "now explain", *lastBlocks[0].Text)
	require.Equal(t, `{"type":"ephemeral"}`, string(lastBlocks[0].CacheControl))

	// 4. 上一轮结尾：往前的最近一条 user 消息（tool_result 那条）的最后一个内容块
	previousBlocks, ok := claudeRequest.Messages[2].Content.([]dto.ClaudeMediaMessage)
	require.True(t, ok)
	require.Len(t, previousBlocks, 1)
	require.Equal(t, "tool_result", previousBlocks[0].Type)
	require.Equal(t, `{"type":"ephemeral"}`, string(previousBlocks[0].CacheControl))

	// 中间那条 assistant 消息不带断点
	assistantBlocks, ok := claudeRequest.Messages[1].Content.([]dto.ClaudeMediaMessage)
	require.True(t, ok)
	for _, block := range assistantBlocks {
		require.Empty(t, block.CacheControl)
	}
}

// 请求里已经有任意 cache_control 时，整个函数什么都不做。
func TestApplyAutoCacheControlSkipsWhenCacheControlAlreadyPresent(t *testing.T) {
	request := &dto.ClaudeRequest{
		Tools: []any{
			&dto.Tool{
				Name:         "read_file",
				CacheControl: json.RawMessage(`{"type":"ephemeral","ttl":"1h"}`),
			},
		},
		System: []dto.ClaudeMediaMessage{
			{Type: "text", Text: testStringPtr("you are a coding agent")},
		},
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: []dto.ClaudeMediaMessage{
				{Type: "text", Text: testStringPtr("hi")},
			}},
		},
	}

	ApplyAutoCacheControl(request)

	// 只有客户端原本那一个断点
	require.Equal(t, 1, countCacheControl(request))
	tools := request.GetTools()
	require.Len(t, tools, 1)
	tool, ok := tools[0].(*dto.Tool)
	require.True(t, ok)
	require.JSONEq(t, `{"type":"ephemeral","ttl":"1h"}`, string(tool.CacheControl))

	require.Empty(t, request.System.([]dto.ClaudeMediaMessage)[0].CacheControl)
	require.True(t, request.Messages[0].IsStringContent())
	require.Empty(t, request.Messages[1].Content.([]dto.ClaudeMediaMessage)[0].CacheControl)
	require.Empty(t, request.CacheControl)
}

// 无工具、无 system、只有一条 user 字符串消息：只有 1 个断点。
func TestApplyAutoCacheControlOnlySingleUserMessage(t *testing.T) {
	request := &dto.ClaudeRequest{
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: "hello"},
		},
	}

	ApplyAutoCacheControl(request)

	require.Equal(t, 1, countCacheControl(request))
	blocks, ok := request.Messages[0].Content.([]dto.ClaudeMediaMessage)
	require.True(t, ok)
	require.Len(t, blocks, 1)
	require.Equal(t, "text", blocks[0].Type)
	require.Equal(t, "hello", *blocks[0].Text)
	require.Equal(t, `{"type":"ephemeral"}`, string(blocks[0].CacheControl))
}

// 非函数工具（web search）不加断点；没有函数工具就不加。
func TestApplyAutoCacheControlSkipsNonFunctionTools(t *testing.T) {
	request := &dto.ClaudeRequest{
		Tools: []any{
			&dto.ClaudeWebSearchTool{Type: "web_search_20250305", Name: "web_search"},
		},
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: "hello"},
		},
	}

	ApplyAutoCacheControl(request)

	// 只有最后一条消息那一个断点
	require.Equal(t, 1, countCacheControl(request))
	webSearchTool, ok := request.GetTools()[0].(*dto.ClaudeWebSearchTool)
	require.True(t, ok)
	require.Equal(t, "web_search", webSearchTool.Name)
}

// 最后一个内容块是 tool_result 时，断点加在 tool_result 块上。
func TestApplyAutoCacheControlLastBlockIsToolResult(t *testing.T) {
	request := &dto.ClaudeRequest{
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: "fix the bug"},
			{Role: "assistant", Content: []dto.ClaudeMediaMessage{
				{Type: "tool_use", Id: "call_1", Name: "read_file", Input: map[string]any{"path": "a.go"}},
			}},
			{Role: "user", Content: []dto.ClaudeMediaMessage{
				{Type: "tool_result", ToolUseId: "call_1", Content: "ok"},
				{Type: "tool_result", ToolUseId: "call_2", Content: "ok again"},
			}},
		},
	}

	ApplyAutoCacheControl(request)

	lastBlocks, ok := request.Messages[2].Content.([]dto.ClaudeMediaMessage)
	require.True(t, ok)
	require.Len(t, lastBlocks, 2)
	require.Empty(t, lastBlocks[0].CacheControl)
	require.Equal(t, "tool_result", lastBlocks[1].Type)
	require.Equal(t, `{"type":"ephemeral"}`, string(lastBlocks[1].CacheControl))
}

// 空字符串消息、空 text 块都不加断点（上游拒绝空 text 块上的 cache_control）。
func TestApplyAutoCacheControlSkipsEmptyText(t *testing.T) {
	emptyStringRequest := &dto.ClaudeRequest{
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: ""},
		},
	}
	ApplyAutoCacheControl(emptyStringRequest)
	require.Equal(t, 0, countCacheControl(emptyStringRequest))
	require.True(t, emptyStringRequest.Messages[0].IsStringContent())

	emptyBlockRequest := &dto.ClaudeRequest{
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: []dto.ClaudeMediaMessage{
				{Type: "text", Text: testStringPtr("")},
			}},
		},
	}
	ApplyAutoCacheControl(emptyBlockRequest)
	require.Equal(t, 0, countCacheControl(emptyBlockRequest))
}

// 序列化后 tools 里出现 5 分钟断点标记。
func TestApplyAutoCacheControlSerializesToolBreakpoint(t *testing.T) {
	request := &dto.ClaudeRequest{
		Tools: []any{
			&dto.Tool{
				Name:        "read_file",
				InputSchema: map[string]interface{}{"type": "object"},
			},
		},
	}

	ApplyAutoCacheControl(request)

	payload, err := common.Marshal(request)
	require.NoError(t, err)
	require.Contains(t, string(payload), `"cache_control":{"type":"ephemeral"}`)
}
