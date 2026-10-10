#!/usr/bin/env python3
"""从 apimaster 同步定价到本系统，并按倍率加价。

## 为什么不用内置的「上游定价同步」功能

本 fork 自带 `/api/ratio_sync/fetch`（`controller/ratio_sync.go`），但它读的是上游的
**`/api/pricing` —— 官方标价**。apimaster 的官方标价和实际售价差得很远：

    gpt-image-2   /api/pricing 标 model_price = $0.25
                  实际每次只扣                $0.0075   （33 倍差）

直接用会把自己的报价定高 33 倍。真实售价只在 **`/api/public/marketplace?model=X`**
里（按渠道返回 `user_price`）。

## 定价基准

apimaster 有 79 个模型，其中 57 个存在多渠道价差（例如 gpt-image-2：$0.0075 ↔ $0.325，
差 43 倍）。它的调度是**便宜优先**，所以**常态成本 = 最低价**。
本脚本默认按「最低价 × (1+markup)」定价，这也是最有竞争力的选择。

⚠️ 风险：一旦便宜渠道耗尽、它降级到贵渠道，那一笔就会亏。
上游日志里有 `fallback_triggered` 字段可以监控（见 ops/reconcile-upstream.py）。
想稳妥就把起始价改成最高价：`--base max`。

## 单位换算（两边都是 new-api，QuotaPerUnit = 500000）

  - 按次计费（quota_type=1）：直接用 `model_price`，单位 USD/次
  - 按 token 计费（quota_type=0）：marketplace 的 `user_price` 是 **USD / 百万输入 token**
    （核验：gpt-5.6-sol 官方输入价 $5/M ↔ model_ratio 2.5）
    本系统的 `model_ratio` 满足 `$/M = 2 × model_ratio`，
    因此 `our_model_ratio = user_price_per_M / 2`
  - 相对倍率（`completion_ratio` / `cache_ratio` / `image_ratio` / `create_cache_ratio`）
    **不加价**，原样照搬

## 用法

    python3 ops/sync-upstream-pricing.py                     # 默认 dry-run，只打印
    python3 ops/sync-upstream-pricing.py --markup 1.25       # 加价 25%
    python3 ops/sync-upstream-pricing.py --apply             # 真正写入本系统
    python3 ops/sync-upstream-pricing.py --base max --apply  # 按最贵渠道定价
"""

import argparse
import json
import os
import subprocess
import sys
import urllib.parse
import urllib.request
from concurrent.futures import ThreadPoolExecutor

UPSTREAM = "https://apimaster.ai"
TOKEN_FILE = "/root/upstream-apimaster-admin-token.txt"
ADMIN_TOKEN_FILE = "/root/newapi-admin-token.txt"


def find_repo():
    here = os.path.dirname(os.path.abspath(__file__))
    for d in (here, os.path.dirname(here), "/opt/new-api-pool"):
        if os.path.isfile(os.path.join(d, ".env")):
            return d
    sys.exit("找不到 .env")


def load_env():
    env = {}
    with open(os.path.join(find_repo(), ".env"), encoding="utf-8") as fh:
        for line in fh:
            line = line.strip()
            if line and not line.startswith("#") and "=" in line:
                k, v = line.split("=", 1)
                env[k.strip()] = v.strip()
    return env


def get_json(url):
    req = urllib.request.Request(url, headers={"User-Agent": "pricing-sync/1.0"})
    with urllib.request.urlopen(req, timeout=25) as r:
        return json.load(r)


def psql_json(sql, env):
    cmd = ["docker", "exec", "-e", f"PGPASSWORD={env['PG_PASSWORD']}",
           "new-api-postgres", "psql", "-U", env["PG_USER"], "-d", env["PG_DB"],
           "-At", "-c", sql]
    return subprocess.run(cmd, capture_output=True, text=True, check=True).stdout.strip()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--markup", type=float, default=1.25,
                    help="加价倍率，1.25 = 加价 25%%")
    ap.add_argument("--base", choices=["min", "median", "max"], default="min",
                    help="以哪一档渠道价作为基准（默认最低价 = 上游常态成本）")
    ap.add_argument("--variant", choices=["base", "max"], default="base",
                    help="图片模型有 1K/2K/4K 多档规格时取哪档（base=基准规格，max=最贵规格）")
    ap.add_argument("--apply", action="store_true", help="真正写入（默认只预览）")
    args = ap.parse_args()

    env = load_env()
    pricing = get_json(f"{UPSTREAM}/api/pricing")["data"]
    names = [x["model_name"] for x in pricing]

    def market(m):
        """返回 (模型, 基准价, 计价单位, 规格数)。

        ⚠️ 关键：marketplace 的 `user_price` 单位随模型类型变化
          - 有 `media_pricing` → 按张（unit=image），价格是「每张」
          - 没有              → 按 token，价格是「$ / 百万输入 token」
        不区分就会把图片模型的价格当成 $/M token，算出来的 ratio 差几个数量级。
        """
        try:
            d = get_json(f"{UPSTREAM}/api/public/marketplace?model={urllib.parse.quote(m)}")["data"]
            cands = []
            for c in d:
                mp = c.get("media_pricing")
                if mp:
                    prices = mp.get("billing_prices") or mp.get("official_prices") or {}
                    if not prices:
                        continue
                    base_variant = mp.get("base_variant") or sorted(prices)[0]
                    if args.variant == "max":
                        price = max(prices.values())
                    else:
                        price = prices.get(base_variant) or sorted(prices.values())[0]
                    cands.append((price, "image", len(prices)))
                elif c.get("user_price") is not None:
                    cands.append((c["user_price"], "token", 1))
            if not cands:
                return m, None, None, 0
            cands.sort(key=lambda x: x[0])
            pick = {"min": cands[0], "max": cands[-1],
                    "median": cands[len(cands) // 2]}[args.base]
            return m, pick[0], pick[1], pick[2]
        except Exception:
            return m, None, None, 0

    with ThreadPoolExecutor(max_workers=8) as ex:
        mk = {r[0]: (r[1], r[2], r[3]) for r in ex.map(market, names)}
    base_price = {k: v[0] for k, v in mk.items()}
    unit_of = {k: v[1] for k, v in mk.items()}
    variants_of = {k: v[2] for k, v in mk.items()}

    model_price, model_ratio, rel_ratio = {}, {}, {}
    missing, skipped, multi_variant = [], [], []
    for item in pricing:
        m = item["model_name"]
        p = base_price.get(m)
        if p is None:
            missing.append(m)
            continue
        price = round(p * args.markup, 8)
        if item.get("billing_mode"):
            # 分段/表达式计费（如 gpt-5.6-sol 的长上下文阶梯价），无法用单一
            # ratio/price 表达，跳过交人工处理，避免把价格写错
            skipped.append(m)
            continue
        if unit_of.get(m) == "image" or item.get("quota_type") == 1:
            # 按张计费 → 用本系统的固定价 ModelPrice（每次调用收这么多）
            model_price[m] = price
            if variants_of.get(m, 1) > 1:
                multi_variant.append(m)
        else:
            # 按 token 计费：marketplace 给的是 $/M 输入价，本系统 $/M = 2 × model_ratio
            model_ratio[m] = round(price / 2.0, 8)
        for f in ("completion_ratio", "cache_ratio", "create_cache_ratio", "image_ratio"):
            v = item.get(f)
            if v:
                rel_ratio.setdefault(f, {})[m] = v

    print(f"\n上游 {UPSTREAM}   模型 {len(pricing)} 个   加价 ×{args.markup}   基准={args.base}\n")
    print(f"{'模型':34s} {'类型':6s} {'上游基准价':>12s} {'我们的价':>12s}")
    print("-" * 72)
    for m in names[:20]:
        p = base_price.get(m)
        if p is None:
            continue
        kind = "按次" if m in model_price else "按token"
        ours = model_price.get(m) or (model_ratio.get(m, 0) * 2)
        print(f"{m:34s} {kind:6s} {p:>12.6f} {ours:>12.6f}")
    print(f"  …（共 {len(model_price)+len(model_ratio)} 个模型，未全部展示）\n")
    print(f"按次计费 {len(model_price)} 个 / 按 token 计费 {len(model_ratio)} 个"
          f" / 跳过（分段表达式）{len(skipped)} 个 / 无定价 {len(missing)} 个")
    if skipped:
        print(f"  跳过: {', '.join(skipped[:8])}{' …' if len(skipped) > 8 else ''}")
    if missing:
        print(f"  无定价: {', '.join(missing[:8])}{' …' if len(missing) > 8 else ''}")
    if multi_variant:
        print(f"\n  ⚠️ 多规格图片模型 {len(multi_variant)} 个："
              f"{', '.join(multi_variant[:6])}{' …' if len(multi_variant) > 6 else ''}")
        print("     它们的 1K/2K/4K 售价不同，而本系统的固定价是「每次一个价」——"
              "按 1K 定价会在大尺寸上亏。要保守就用 --variant max。")

    payload = {"ModelPrice": model_price, "ModelRatio": model_ratio}
    out = "/tmp/upstream-pricing.json"
    with open(out, "w", encoding="utf-8") as fh:
        json.dump(payload, fh, ensure_ascii=False, indent=2)
    print(f"\n定价 JSON 已写到 {out}")

    if not args.apply:
        print("（dry-run，未写入。加 --apply 才会写进本系统）")
        return

    with open(ADMIN_TOKEN_FILE, encoding="utf-8") as fh:
        admin = fh.read().strip()

    def put_option(key, value_obj):
        body = json.dumps({"key": key, "value": json.dumps(value_obj, ensure_ascii=False)}).encode()
        req = urllib.request.Request(
            "http://127.0.0.1:3000/api/option/", data=body, method="PUT",
            headers={"Authorization": f"Bearer {admin}", "New-API-User": "1",
                     "Content-Type": "application/json"})
        with urllib.request.urlopen(req, timeout=30) as r:
            return json.load(r)

    for f, mapping in rel_ratio.items():
        key = "".join(w.capitalize() for w in f.split("_"))
        if key == "CreateCacheRatio":
            key = "CreateCacheRatio"
        print(f"写入 {key}: {len(mapping)} 项")
        put_option(key, mapping)
    for key, mapping in (("ModelPrice", model_price), ("ModelRatio", model_ratio)):
        print(f"写入 {key}: {len(mapping)} 项")
        res = put_option(key, mapping)
        print("  →", res.get("success"), res.get("message", ""))
    print("\n✅ 同步完成。到「系统设置 → 分组与模型定价设置」或直接跑一次调用验证。")


if __name__ == "__main__":
    main()
