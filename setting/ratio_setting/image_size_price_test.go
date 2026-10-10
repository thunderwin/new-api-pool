package ratio_setting

import "testing"

// 上游价表有几种形状，回归时都要照顾到：
//   - 标准档位：1K / 2K / 4K
//   - 档位 + 质量：1K low / 1K medium（grok-imagine-image-2.0）
//   - 非标准档位：fast / relax / turbo（midjourney）、480P / 720P（视频）
func TestMatchImageSizeVariant(t *testing.T) {
	standard := map[string]float64{"1K": 0.0234375, "2K": 0.028125, "4K": 0.05625}
	withQuality := map[string]float64{
		"1K low":      0.048,
		"1K medium":   0.072,
		"2K low":      0.072,
		"2K medium":   0.096,
		"Image Input": 0.012,
	}
	nonStandard := map[string]float64{"fast": 0.2064, "relax": 0.1689, "turbo": 0.375}

	cases := []struct {
		name    string
		size    string
		quality string
		prices  map[string]float64
		want    float64
	}{
		{"standard 1K", "1024x1024", "", standard, 0.0234375},
		{"standard 2K", "2048x2048", "", standard, 0.028125},
		{"standard 4K", "4096x4096", "", standard, 0.05625},
		{"standard auto 取最低", "auto", "", standard, 0.0234375},
		{"档位不在表里时向下取", "4096x4096", "", map[string]float64{"1K": 0.01}, 0.01},
		// 质量档：显式 quality 要命中对应档，否则按该档最低价
		{"quality medium 命中", "1024x1024", "medium", withQuality, 0.072},
		{"quality low 命中", "1024x1024", "low", withQuality, 0.048},
		{"未给 quality 取该档最低", "1024x1024", "", withQuality, 0.048},
		{"2K 未给 quality 取该档最低", "2048x2048", "", withQuality, 0.072},
		{"quality 不在表里退回该档最低", "1024x1024", "high", withQuality, 0.048},
		// 非标准档位名：真实档位解析不出来，只能退化成全局最低价
		{"非标准档位退化为全局最低", "1024x1024", "", nonStandard, 0.1689},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, ok := MatchImageSizeVariant(tc.size, tc.quality, tc.prices)
			if !ok {
				t.Fatalf("MatchImageSizeVariant(%q, %q) 没匹配到", tc.size, tc.quality)
			}
			if got != tc.want {
				t.Fatalf("MatchImageSizeVariant(%q, %q) = %v, 期望 %v", tc.size, tc.quality, got, tc.want)
			}
		})
	}
}

// 空价表必须明确返回 false，调用方靠它决定要不要回退到固定价。
func TestMatchImageSizeVariantEmptyPrices(t *testing.T) {
	if _, _, ok := MatchImageSizeVariant("1024x1024", "", map[string]float64{}); ok {
		t.Fatal("空价表不应该匹配成功")
	}
}
