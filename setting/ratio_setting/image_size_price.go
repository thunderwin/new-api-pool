package ratio_setting

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// ImageSizePrice —— 给「按次计费的图片模型」**按输出规格**定价。
//
// 结构：{模型名: {规格: 单次价格 USD}}
//
//	例 {"gpt-image-2": {"1K": 0.009375, "2K": 0.01125, "4K": 0.0225}}
//
// 为什么需要它：本系统原本一个模型只有一个固定价（ModelPrice），
// 但上游对同一模型的不同尺寸收不同的钱 —— 例如 gemini-3.1-flash-image 在
// apimaster 那边 0.5K/1K/2K/4K 分别是 $0.009 / $0.0134 / $0.0202 / $0.0302。
// 只按一个价收，客户传大尺寸时就亏、传小尺寸时就贵。
//
// 规格名沿用上游的档位：0.5K / 1K / 2K / 4K。请求里的 size（如 "1024x1024"）
// 由 MatchImageSizeVariant 按**长边**归到某一档。
var (
	imageSizePriceMu  sync.RWMutex
	imageSizePriceMap = make(map[string]map[string]float64)
)

// 规格档位，从低到高。归类的边界按长边像素。
var imageSizeVariants = []struct {
	Name    string
	MaxSide int
}{
	{"0.5K", 640},
	{"1K", 1280},
	{"2K", 2560},
	{"4K", 1 << 30}, // 兜底：比 2560 大的都算 4K
}

func ImageSizePrice2JSONString() string {
	imageSizePriceMu.RLock()
	defer imageSizePriceMu.RUnlock()
	if len(imageSizePriceMap) == 0 {
		return "{}"
	}
	b, err := json.Marshal(imageSizePriceMap)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func UpdateImageSizePriceByJSONString(jsonStr string) error {
	parsed := make(map[string]map[string]float64)
	if strings.TrimSpace(jsonStr) != "" && strings.TrimSpace(jsonStr) != "{}" {
		if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
			return fmt.Errorf("解析 ImageSizePrice 失败: %w", err)
		}
	}
	imageSizePriceMu.Lock()
	imageSizePriceMap = parsed
	imageSizePriceMu.Unlock()
	InvalidateExposedDataCache()
	return nil
}

// GetImageSizePrice 取某个模型的尺寸价表；没有配则返回 false。
func GetImageSizePrice(model string) (map[string]float64, bool) {
	imageSizePriceMu.RLock()
	defer imageSizePriceMu.RUnlock()
	v, ok := imageSizePriceMap[model]
	return v, ok
}

func GetImageSizePriceCopy() map[string]map[string]float64 {
	imageSizePriceMu.RLock()
	defer imageSizePriceMu.RUnlock()
	out := make(map[string]map[string]float64, len(imageSizePriceMap))
	for k, v := range imageSizePriceMap {
		inner := make(map[string]float64, len(v))
		for kk, vv := range v {
			inner[kk] = vv
		}
		out[k] = inner
	}
	return out
}

// MatchImageSizeVariant 按请求里的 size（"1024x1024" / "2048x2048" / "auto"）
// 和 quality（"low" / "medium" / "high"，可空）在给定价表里选一档价格。
//
//   - size 空或 "auto" → 用价表里最低档（上游 auto 通常按 1K 出图）
//   - 解析不出来       → 用最低档，并返回 matched=false 供调用方决定是否兜底
//   - 算出的档位不在价表里 → 向下找最接近的可用档；都没有则返回 false
//
// 上游的价表并不只有「1K / 2K / 4K」这一种形状，还见过：
//
//	"1K low" / "1K medium"（grok-imagine-image-2.0，同尺寸按质量分档）
//	"fast" / "relax" / "turbo"（midjourney，按速度分档）
//	"480P" / "720P" / "1080P"（视频模型，按分辨率分档）
//
// 所以命中档位时按「精确名 → 档位名 + 质量 → 档位名开头的任意键（取其中最低价）」
// 三级去找。最后一级是有意取**低价**：请求里没带质量时我们无从知道上游会按哪一档出图，
// 与其按最高档向客户多收，不如按最低档收（宁可少赚，不要事后争议）。
// 非标准档位名（midjourney / 视频模型那种）目前只能退化成全局最低价。
func MatchImageSizeVariant(size, quality string, prices map[string]float64) (float64, string, bool) {
	if len(prices) == 0 {
		return 0, "", false
	}

	pickCheapest := func() (float64, string, bool) {
		best, bestName := 0.0, ""
		found := false
		for _, v := range imageSizeVariants {
			if p, ok := prices[v.Name]; ok && (!found || p < best) {
				best, bestName, found = p, v.Name, true
			}
		}
		if !found {
			// 价表里用了非标准档位名，随便取一个最小的
			for n, p := range prices {
				if !found || p < best {
					best, bestName, found = p, n, true
				}
			}
		}
		return best, bestName, found
	}

	// 在某个档位名下面找价：精确 → 档位名 + 质量 → 档位名开头的键里挑最低
	pickInVariant := func(variant string) (float64, string, bool) {
		if p, ok := prices[variant]; ok {
			return p, variant, true
		}
		q := strings.ToLower(strings.TrimSpace(quality))
		if q != "" {
			for _, sep := range []string{" ", "-", "_"} {
				name := variant + sep + q
				for k, p := range prices {
					if strings.EqualFold(k, name) {
						return p, k, true
					}
				}
			}
		}
		prefix := strings.ToLower(variant) + " "
		best, bestName := 0.0, ""
		found := false
		for k, p := range prices {
			if !strings.HasPrefix(strings.ToLower(k), prefix) {
				continue
			}
			if !found || p < best {
				best, bestName, found = p, k, true
			}
		}
		return best, bestName, found
	}

	size = strings.TrimSpace(strings.ToLower(size))
	if size == "" || size == "auto" {
		return pickCheapest()
	}

	parts := strings.FieldsFunc(size, func(r rune) bool { return r == 'x' || r == '*' || r == '×' })
	if len(parts) != 2 {
		return pickCheapest()
	}
	w, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	h, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return pickCheapest()
	}
	maxSide := w
	if h > maxSide {
		maxSide = h
	}

	// 找到该长边所属档位，然后从这档往低档找第一个有价的
	idx := len(imageSizeVariants) - 1
	for i, v := range imageSizeVariants {
		if maxSide <= v.MaxSide {
			idx = i
			break
		}
	}
	for i := idx; i >= 0; i-- {
		if p, name, ok := pickInVariant(imageSizeVariants[i].Name); ok {
			return p, name, true
		}
	}
	return pickCheapest()
}
