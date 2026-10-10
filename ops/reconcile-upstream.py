#!/usr/bin/env python3
"""上游成本对账 —— 把 apimaster 的逐笔费用和本系统的下游调用配对，算真实毛利。

## 为什么要这样做

两边**没有共同的 request id**：本系统和 apimaster 各跳各自生成 id。
实测同一笔调用：

  本系统：202610100341176 369241458268d9d6uUejzAJq    tokens 3/1650
  上游：  202610100341176 516005558268d9d6ejqe9RrL    tokens 3/1650
         └─ 时间戳前缀相同（同一秒）        └─ token 数完全一致

所以配对键是 **(模型, prompt_tokens, completion_tokens, 秒级时间戳)**。
token 数在两边是同一个值（都是上游返回的 usage），同一模型下几乎唯一，
再叠加秒级时间窗，实践中可以做到 1:1。

## 用法（在服务器上跑）

    python3 /opt/new-api-pool/reconcile-upstream.py --hours 24
    python3 /opt/new-api-pool/reconcile-upstream.py --since 2026-10-10 --until 2026-10-11

依赖：
  - /root/upstream-apimaster-admin-token.txt  （apimaster 账号的 access token，非 sk- key）
  - /opt/new-api-pool/.env                    （读 PG 连接信息）
"""

import argparse
import json
import os
import subprocess
import sys
import time
import urllib.request
from collections import defaultdict

UPSTREAM = "https://apimaster.ai"
TOKEN_FILE = "/root/upstream-apimaster-admin-token.txt"
TOLERANCE_SEC = 5  # 配对时间窗


def find_repo():
    """找到含 .env 的目录。

    脚本在仓库里是 ops/reconcile-upstream.py，部署到服务器后是 /opt/new-api-pool/ 下，
    两种情况向上找一层就够；再兜底到服务器的固定路径。
    """
    here = os.path.dirname(os.path.abspath(__file__))
    for d in (here, os.path.dirname(here), "/opt/new-api-pool"):
        if os.path.isfile(os.path.join(d, ".env")):
            return d
    sys.exit("找不到 .env（需要 PG_USER/PG_PASSWORD/PG_DB）")


def load_env():
    env = {}
    path = os.path.join(find_repo(), ".env")
    with open(path, encoding="utf-8") as fh:
        for line in fh:
            line = line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            k, v = line.split("=", 1)
            env[k.strip()] = v.strip()
    return env


def psql(sql, env):
    """在 new-api-postgres 容器里执行 SQL，返回按 | 分隔的行。"""
    cmd = [
        "docker", "exec", "-e", f"PGPASSWORD={env['PG_PASSWORD']}",
        "new-api-postgres", "psql", "-U", env["PG_USER"], "-d", env["PG_DB"],
        "-At", "-F", "|", "-c", sql,
    ]
    out = subprocess.run(cmd, capture_output=True, text=True, check=True).stdout
    return [ln for ln in out.splitlines() if ln]


def fetch_upstream(start_ts, end_ts):
    """拉 apimaster 的逐笔日志（用户级接口 /api/log/self）。"""
    with open(TOKEN_FILE, encoding="utf-8") as fh:
        token = fh.read().strip()
    items, page = [], 1
    while True:
        url = (f"{UPSTREAM}/api/log/self?p={page}&page_size=100"
               f"&start_timestamp={start_ts}&end_timestamp={end_ts}")
        req = urllib.request.Request(url, headers={"Authorization": f"Bearer {token}"})
        with urllib.request.urlopen(req, timeout=30) as resp:
            body = json.load(resp)
        if not body.get("success"):
            sys.exit(f"上游接口失败: {body.get('message')}")
        data = body["data"]
        items.extend(data["items"])
        if len(items) >= data["total"] or not data["items"]:
            break
        page += 1
    return items


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--hours", type=float, default=24)
    ap.add_argument("--since", help="YYYY-MM-DD（本地时区），优先于 --hours")
    ap.add_argument("--until", help="YYYY-MM-DD")
    ap.add_argument("--tol", type=int, default=TOLERANCE_SEC, help="配对时间窗（秒）")
    args = ap.parse_args()

    now = int(time.time())
    if args.since:
        start = int(time.mktime(time.strptime(args.since, "%Y-%m-%d")))
        end = int(time.mktime(time.strptime(args.until, "%Y-%m-%d"))) if args.until else now
    else:
        start, end = now - int(args.hours * 3600), now

    env = load_env()
    unit = 500000.0  # new-api 的 QuotaPerUnit，两边一致（已核实）

    # 上游：逐笔费用
    up = fetch_upstream(start, end)
    up_by_key = defaultdict(list)
    for it in up:
        other = {}
        try:
            other = json.loads(it.get("other") or "{}")
        except Exception:
            pass
        it["_usd"] = it["quota"] / unit
        it["_fallback"] = bool(other.get("fallback_triggered"))
        it["_content"] = it.get("content") or ""
        up_by_key[(it["model_name"], it["prompt_tokens"], it["completion_tokens"])].append(it)

    # 本系统：逐笔收入
    rows = psql(
        "SELECT id, model_name, prompt_tokens, completion_tokens, quota, "
        "EXTRACT(EPOCH FROM to_timestamp(created_at))::bigint, channel_id "
        f"FROM logs WHERE created_at >= {start} AND created_at <= {end} "
        "ORDER BY created_at", env)
    mine = []
    for r in rows:
        f = r.split("|")
        mine.append({
            "id": int(f[0]), "model": f[1], "pt": int(f[2]), "ct": int(f[3]),
            "quota": int(f[4]), "ts": int(float(f[5])), "channel_id": int(f[6]),
            "_usd": int(f[4]) / unit,
        })

    # 配对：同 (模型, token 数)，时间差在容差内；每笔只配一次
    used = set()
    for m in mine:
        cands = up_by_key.get((m["model"], m["pt"], m["ct"]), [])
        best = None
        for c in cands:
            if id(c) in used:
                continue
            if abs(c["created_at"] - m["ts"]) <= args.tol:
                if best is None or abs(c["created_at"] - m["ts"]) < abs(best["created_at"] - m["ts"]):
                    best = c
        if best is not None:
            used.add(id(best))
            m["cost"] = best["_usd"]
            m["fallback"] = best["_fallback"]
            m["up_quota"] = best["quota"]
        else:
            m["cost"] = None

    matched = [m for m in mine if m["cost"] is not None]
    orphan_up = [c for c in up if id(c) not in used]

    # ── 输出 ──
    print(f"\n时间窗 {time.strftime('%F %T', time.localtime(start))} → "
          f"{time.strftime('%F %T', time.localtime(end))}\n")
    print(f"{'时间':19s} {'模型':22s} {'tokens':>11s} {'上游成本':>12s} "
          f"{'你的收入':>12s} {'毛利':>12s} {'毛利率':>7s}  降级")
    print("-" * 108)
    for m in matched:
        rev, cost = m["_usd"], m["cost"]
        margin = (rev - cost) / rev * 100 if rev else 0
        print(f"{time.strftime('%m-%d %H:%M:%S', time.localtime(m['ts'])):19s} "
              f"{m['model']:22s} {str(m['pt'])+'/'+str(m['ct']):>11s} "
              f"${cost:>11.6f} ${rev:>11.6f} ${rev-cost:>11.6f} "
              f"{margin:>6.1f}%  {'⚠️' if m.get('fallback') else ''}")

    tot_rev = sum(m["_usd"] for m in mine)
    tot_cost = sum(m["cost"] for m in matched)
    print("\n" + "=" * 60)
    print(f"下游调用   : {len(mine)} 笔   其中配对成功 {len(matched)} 笔"
          f"，未配对 {len(mine)-len(matched)} 笔")
    print(f"上游记录   : {len(up)} 笔   未被认领 {len(orphan_up)} 笔")
    print(f"你的收入   : ${tot_rev:.6f}")
    print(f"上游成本   : ${tot_cost:.6f}")
    if tot_rev > 0:
        print(f"毛利       : ${tot_rev-tot_cost:.6f}   毛利率 {(tot_rev-tot_cost)/tot_rev*100:.1f}%")
        if tot_cost > 0:
            print(f"平均加价   : {tot_rev/tot_cost:.1f}x")

    # 按模型
    agg = defaultdict(lambda: [0, 0.0, 0.0])
    for m in mine:
        a = agg[m["model"]]
        a[0] += 1
        a[1] += m["_usd"]
        a[2] += m["cost"] or 0
    if agg:
        print(f"\n按模型：")
        print(f"  {'模型':24s} {'笔数':>5s} {'收入':>12s} {'成本':>12s} {'毛利率':>7s}")
        for model, (n, rev, cost) in sorted(agg.items(), key=lambda x: -x[1][1]):
            mr = (rev - cost) / rev * 100 if rev else 0
            print(f"  {model:24s} {n:>5d} ${rev:>11.6f} ${cost:>11.6f} {mr:>6.1f}%")

    if orphan_up:
        print(f"\n⚠️ 有 {len(orphan_up)} 笔上游记录没配到下游调用（可能是别的 sk- key 用的，"
              f"或超出时间窗）：")
        for c in orphan_up[:5]:
            print(f"   {time.strftime('%m-%d %H:%M:%S', time.localtime(c['created_at']))} "
                  f"{c['model_name']} {c['prompt_tokens']}/{c['completion_tokens']} "
                  f"${c['_usd']:.6f}")
    print()


if __name__ == "__main__":
    main()
