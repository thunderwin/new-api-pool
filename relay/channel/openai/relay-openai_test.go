package openai

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/stretchr/testify/require"
)

// Wokey 转发 Claude 时的真实形状：prompt_tokens 只含非缓存输入（样例里是 2），
// 缓存读写单独放在 cache_read_input_tokens / cache_creation_input_tokens。
const (
	wokeyClaudeStreamLastChunk = `{"id":"chatcmpl-wokey-claude","object":"chat.completion.chunk","created":1767225600,"model":"claude-opus-5-5","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":395,"total_tokens":397,"cache_read_input_tokens":35511,"cache_creation_input_tokens":419}}`

	wokeyClaudeNonStreamBody = `{"id":"chatcmpl-wokey-claude","object":"chat.completion","created":1767225600,"model":"claude-opus-5-5","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":395,"total_tokens":397,"cache_read_input_tokens":35511,"cache_creation_input_tokens":419}}`

	// 完全命中缓存时 prompt_tokens 可能为 0，用量只在缓存字段里。
	wokeyClaudeFullCacheHitBody = `{"id":"chatcmpl-wokey-claude","object":"chat.completion","created":1767225600,"model":"claude-opus-5-5","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":0,"completion_tokens":395,"total_tokens":395,"cache_read_input_tokens":35511,"cache_creation_input_tokens":0}}`

	plainOpenAIUsageBody = `{"id":"chatcmpl-plain","object":"chat.completion","created":1767225600,"model":"gpt-5.5","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1200,"completion_tokens":395,"total_tokens":1595}}`

	wokeyGPTCachedTokensBody = `{"id":"chatcmpl-wokey-gpt","object":"chat.completion","created":1767225600,"model":"gpt-5.5","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1200,"completion_tokens":395,"total_tokens":1595,"input_tokens_details":{"cached_tokens":1024}}}`
)

func newOpenAIChannelInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType: constant.ChannelTypeOpenAI,
		},
	}
}

// 流式：最后一个 chunk 同时带 finish_reason 和 usage（Wokey 的 Claude 形态）。
func TestApplyUsagePostProcessingClaudeCacheUsageFromStreamLastChunk(t *testing.T) {
	info := newOpenAIChannelInfo()
	info.ShouldIncludeUsage = true

	var (
		usage              *dto.Usage
		containStreamUsage bool
		shouldSendLastResp = true
		responseId         string
		createAt           int64
		systemFingerprint  string
		model              = "claude-opus-5-5"
	)

	require.NoError(t, handleLastResponse(wokeyClaudeStreamLastChunk, &responseId, &createAt,
		&systemFingerprint, &model, &usage, &containStreamUsage, info, &shouldSendLastResp))
	require.True(t, containStreamUsage)
	require.NotNil(t, usage)

	applyUsagePostProcessing(info, usage, common.StringToByteSlice(wokeyClaudeStreamLastChunk))

	require.Equal(t, 2, usage.PromptTokens)
	require.Equal(t, 395, usage.CompletionTokens)
	require.Equal(t, 35511, usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, 419, usage.PromptTokensDetails.CachedCreationTokens)
	require.Equal(t, 419, usage.ClaudeCacheCreation5mTokens)
	require.Equal(t, 0, usage.ClaudeCacheCreation1hTokens)
	require.Equal(t, "anthropic", usage.UsageSemantic)
}

// 非流式：prompt_tokens=2 并带缓存字段。
func TestApplyUsagePostProcessingClaudeCacheUsageNonStream(t *testing.T) {
	info := newOpenAIChannelInfo()

	usage := &dto.Usage{
		PromptTokens:     2,
		CompletionTokens: 395,
		TotalTokens:      397,
	}

	applyUsagePostProcessing(info, usage, common.StringToByteSlice(wokeyClaudeNonStreamBody))

	require.Equal(t, 2, usage.PromptTokens)
	require.Equal(t, 35511, usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, 419, usage.PromptTokensDetails.CachedCreationTokens)
	require.Equal(t, 419, usage.ClaudeCacheCreation5mTokens)
	require.Equal(t, "anthropic", usage.UsageSemantic)
}

// 没有缓存字段时，行为与改动前完全一致。
func TestApplyUsagePostProcessingKeepsPlainOpenAIUsageUntouched(t *testing.T) {
	info := newOpenAIChannelInfo()

	usage := &dto.Usage{
		PromptTokens:     1200,
		CompletionTokens: 395,
		TotalTokens:      1595,
	}

	applyUsagePostProcessing(info, usage, common.StringToByteSlice(plainOpenAIUsageBody))

	require.Equal(t, 1200, usage.PromptTokens)
	require.Equal(t, 0, usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, 0, usage.PromptTokensDetails.CachedCreationTokens)
	require.Equal(t, 0, usage.ClaudeCacheCreation5mTokens)
	require.Equal(t, "", usage.UsageSemantic)
}

// 非流式 prompt_tokens=0 但带缓存字段时，不能用本地估算覆盖用量。
func TestShouldUseEstimatedUsageSkipsLocalEstimateWhenClaudeCacheUsagePresent(t *testing.T) {
	info := newOpenAIChannelInfo()
	usageWithoutPrompt := func() *dto.Usage {
		return &dto.Usage{
			PromptTokens:     0,
			CompletionTokens: 395,
		}
	}

	t.Run("prompt_tokens=0 且带缓存字段", func(t *testing.T) {
		require.False(t, shouldUseEstimatedUsage(info, usageWithoutPrompt(), common.StringToByteSlice(wokeyClaudeFullCacheHitBody)))
	})

	t.Run("prompt_tokens=0 且无缓存字段", func(t *testing.T) {
		require.True(t, shouldUseEstimatedUsage(info, usageWithoutPrompt(), common.StringToByteSlice(plainOpenAIUsageBody)))
	})

	t.Run("prompt_tokens 非 0 时不需要估算", func(t *testing.T) {
		usage := &dto.Usage{
			PromptTokens:     2,
			CompletionTokens: 395,
		}
		require.False(t, shouldUseEstimatedUsage(info, usage, common.StringToByteSlice(wokeyClaudeNonStreamBody)))
	})

	t.Run("其它渠道类型维持原有估算逻辑", func(t *testing.T) {
		otherInfo := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType: constant.ChannelTypeZhipu_v4,
			},
		}
		require.True(t, shouldUseEstimatedUsage(otherInfo, usageWithoutPrompt(), common.StringToByteSlice(wokeyClaudeFullCacheHitBody)))
	})
}

// 非流式全命中缓存：保留上游 usage，不被估算覆盖，并被识别为 anthropic 语义。
func TestApplyUsagePostProcessingKeepsFullCacheHitUsage(t *testing.T) {
	info := newOpenAIChannelInfo()

	usage := &dto.Usage{
		PromptTokens:     0,
		CompletionTokens: 395,
		TotalTokens:      395,
	}

	applyUsagePostProcessing(info, usage, common.StringToByteSlice(wokeyClaudeFullCacheHitBody))

	require.Equal(t, 0, usage.PromptTokens)
	require.Equal(t, 395, usage.CompletionTokens)
	require.Equal(t, 35511, usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, "anthropic", usage.UsageSemantic)
}

// GPT 的缓存命中在 input_tokens_details.cached_tokens 里。
func TestApplyUsagePostProcessingCopiesGPTCachedTokensFromInputTokenDetails(t *testing.T) {
	info := newOpenAIChannelInfo()

	usage := &dto.Usage{
		PromptTokens:     1200,
		CompletionTokens: 395,
		TotalTokens:      1595,
		InputTokensDetails: &dto.InputTokenDetails{
			CachedTokens: 1024,
		},
	}

	applyUsagePostProcessing(info, usage, common.StringToByteSlice(wokeyGPTCachedTokensBody))

	require.Equal(t, 1200, usage.PromptTokens)
	require.Equal(t, 1024, usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, 0, usage.PromptTokensDetails.CachedCreationTokens)
	require.Equal(t, "", usage.UsageSemantic)
}

// 其它渠道类型不看 Claude 缓存字段，行为保持原样。
func TestApplyUsagePostProcessingLeavesOtherChannelTypesUnchanged(t *testing.T) {
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType: constant.ChannelTypeDeepSeek,
		},
	}

	usage := &dto.Usage{
		PromptTokens:     2,
		CompletionTokens: 395,
		TotalTokens:      397,
	}

	applyUsagePostProcessing(info, usage, common.StringToByteSlice(wokeyClaudeNonStreamBody))

	require.Equal(t, 2, usage.PromptTokens)
	require.Equal(t, 0, usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, 0, usage.PromptTokensDetails.CachedCreationTokens)
	require.Equal(t, 0, usage.ClaudeCacheCreation5mTokens)
	require.Equal(t, "", usage.UsageSemantic)
}
