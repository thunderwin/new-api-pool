package claude

import (
	"encoding/json"

	"github.com/QuantumNous/new-api/dto"
)

// 本文件负责给 OpenAI→Claude 转换后的请求自动补 cache_control 断点。
// 只由渠道设置 claude_auto_cache_control 打开时生效，默认不参与任何请求路径。

// maxAutoCacheControlBreakpoints 上游单请求最多接受 4 个 cache_control 断点。
const maxAutoCacheControlBreakpoints = 4

// newEphemeralCacheControl 返回 5 分钟缓存断点标记（不带 ttl，上游默认即 5 分钟）。
// 每次返回新的切片，避免多个字段共享同一份 RawMessage。
func newEphemeralCacheControl() json.RawMessage {
	return json.RawMessage(`{"type":"ephemeral"}`)
}

// ApplyAutoCacheControl 给 OpenAI→Claude 转换结果补缓存断点。
// 请求里已经存在任何 cache_control 时整体不做改动（尊重客户端自己的标记）。
// 只加字段，不改文本、顺序和角色。
func ApplyAutoCacheControl(request *dto.ClaudeRequest) {
	if request == nil {
		return
	}
	if hasAnyCacheControl(request) {
		return
	}

	count := 0
	// 1. 最后一个函数工具
	if count < maxAutoCacheControlBreakpoints && applyToolCacheControl(request) {
		count++
	}
	// 2. system 最后一个非空 text 块
	if count < maxAutoCacheControlBreakpoints && applySystemCacheControl(request) {
		count++
	}
	// 3. 最后一条消息的最后一个内容块
	if count < maxAutoCacheControlBreakpoints && applyLastMessageCacheControl(request) {
		count++
	}
	// 4. 上一轮结尾：倒数第二条往前最近的一条 user 消息的最后一个内容块
	if count < maxAutoCacheControlBreakpoints && applyPreviousUserMessageCacheControl(request) {
		count++
	}
}

// hasAnyCacheControl 检查请求里是否已有任意 cache_control（顶层 / tools / system 块 / 消息内容块）。
func hasAnyCacheControl(request *dto.ClaudeRequest) bool {
	if len(request.CacheControl) > 0 {
		return true
	}
	for _, tool := range request.GetTools() {
		switch typed := tool.(type) {
		case *dto.Tool:
			if typed != nil && len(typed.CacheControl) > 0 {
				return true
			}
		case dto.Tool:
			if len(typed.CacheControl) > 0 {
				return true
			}
		}
	}
	if system, ok := request.System.([]dto.ClaudeMediaMessage); ok {
		for _, block := range system {
			if len(block.CacheControl) > 0 {
				return true
			}
		}
	}
	for _, message := range request.Messages {
		if blocks, ok := message.Content.([]dto.ClaudeMediaMessage); ok {
			for _, block := range blocks {
				if len(block.CacheControl) > 0 {
					return true
				}
			}
		}
	}
	return false
}

// applyToolCacheControl 给最后一个函数工具（*dto.Tool）加断点，跳过 web search 等非函数工具。
func applyToolCacheControl(request *dto.ClaudeRequest) bool {
	tools := request.GetTools()
	lastIndex := -1
	for index, tool := range tools {
		switch typed := tool.(type) {
		case *dto.Tool:
			if typed != nil {
				lastIndex = index
			}
		case dto.Tool:
			lastIndex = index
		}
	}
	if lastIndex < 0 {
		return false
	}

	switch typed := tools[lastIndex].(type) {
	case *dto.Tool:
		typed.CacheControl = newEphemeralCacheControl()
	case dto.Tool:
		typed.CacheControl = newEphemeralCacheControl()
		tools[lastIndex] = typed
	}
	return true
}

// applySystemCacheControl 给 system 最后一个非空 text 块加断点。
// system 只有在是块数组时才处理；纯字符串 system 不加（没有可靠的空文本判断）。
func applySystemCacheControl(request *dto.ClaudeRequest) bool {
	system, ok := request.System.([]dto.ClaudeMediaMessage)
	if !ok {
		return false
	}
	for index := len(system) - 1; index >= 0; index-- {
		block := &system[index]
		if block.Type != "text" || block.Text == nil || *block.Text == "" {
			continue
		}
		block.CacheControl = newEphemeralCacheControl()
		request.System = system
		return true
	}
	return false
}

// applyLastMessageCacheControl 给最后一条消息的最后一个内容块加断点。
func applyLastMessageCacheControl(request *dto.ClaudeRequest) bool {
	if len(request.Messages) == 0 {
		return false
	}
	return applyMessageLastBlockCacheControl(&request.Messages[len(request.Messages)-1])
}

// applyPreviousUserMessageCacheControl 从倒数第二条消息往前找最近的一条 user 消息，
// 给它的最后一个内容块加断点，让下一次请求在这个位置命中上一轮写下的缓存。
func applyPreviousUserMessageCacheControl(request *dto.ClaudeRequest) bool {
	for index := len(request.Messages) - 2; index >= 0; index-- {
		if request.Messages[index].Role != "user" {
			continue
		}
		return applyMessageLastBlockCacheControl(&request.Messages[index])
	}
	return false
}

// applyMessageLastBlockCacheControl 给单条消息的最后一个内容块加断点。
// 字符串内容会先转成单个 text 块（文本本身不变）；空字符串和空 text 块不加
// （上游拒绝空 text 块上的 cache_control）。
func applyMessageLastBlockCacheControl(message *dto.ClaudeMessage) bool {
	if message == nil {
		return false
	}

	switch content := message.Content.(type) {
	case string:
		if content == "" {
			return false
		}
		text := content
		message.Content = []dto.ClaudeMediaMessage{
			{
				Type:         "text",
				Text:         &text,
				CacheControl: newEphemeralCacheControl(),
			},
		}
		return true
	case []dto.ClaudeMediaMessage:
		if len(content) == 0 {
			return false
		}
		lastBlock := &content[len(content)-1]
		if lastBlock.Type == "text" && (lastBlock.Text == nil || *lastBlock.Text == "") {
			return false
		}
		lastBlock.CacheControl = newEphemeralCacheControl()
		message.Content = content
		return true
	default:
		return false
	}
}
