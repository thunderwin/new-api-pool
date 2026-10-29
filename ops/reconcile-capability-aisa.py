#!/usr/bin/env python3
"""Reconcile AISA charged micro-USD against recorded capability usage.

The AISA key is read only inside the running gateway container. Database
credentials stay inside the Postgres container. This script never reads .env,
prints credentials, user data, request bodies, or response bodies.
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import os
import subprocess
import sys
import urllib.parse
from pathlib import Path


MIN_DELTA_USD = 0.05
PERCENT_DELTA = 0.02
DEFAULT_OUTPUT = "logs/aisa-capability-reconcile.jsonl"


def parse_date(value: str) -> dt.date:
    try:
        return dt.date.fromisoformat(value)
    except ValueError as exc:
        raise argparse.ArgumentTypeError("date must be YYYY-MM-DD") from exc


def run(command: list[str]) -> str:
    result = subprocess.run(command, check=False, text=True, capture_output=True)
    if result.returncode != 0:
        raise RuntimeError("container command failed")
    return result.stdout


def compose_command(compose_dir: Path, service: str, command: list[str]) -> list[str]:
    return [
        "docker",
        "compose",
        "--project-directory",
        str(compose_dir),
        "exec",
        "-T",
        service,
        *command,
    ]


def fetch_aisa_buckets(compose_dir: Path, start: dt.date, end: dt.date) -> dict[str, int]:
    start_ts = int(dt.datetime.combine(start, dt.time(), tzinfo=dt.timezone.utc).timestamp())
    end_ts = int(dt.datetime.combine(end, dt.time(), tzinfo=dt.timezone.utc).timestamp())
    query = urllib.parse.urlencode(
        {
            "start_time": start_ts,
            "end_time": end_ts,
            "bucket_width": "1d",
            "scope": "key",
        }
    )
    url = "https://api.aisa.one/v1/usage?" + query
    command = compose_command(
        compose_dir,
        "new-api",
        [
            "sh",
            "-c",
            'exec wget -q -O - --header="Authorization: Bearer $CAPABILITY_AISA_API_KEY" "$1"',
            "sh",
            url,
        ],
    )
    payload = json.loads(run(command))
    if payload.get("currency", "USD") != "USD" or payload.get("scope", "key") != "key":
        raise RuntimeError("AISA usage response is not the expected USD key scope")
    result: dict[str, int] = {}
    for bucket in payload.get("buckets", []):
        bucket_date = dt.datetime.fromtimestamp(
            int(bucket["start_time"]), tz=dt.timezone.utc
        ).date().isoformat()
        result[bucket_date] = result.get(bucket_date, 0) + int(
            bucket.get("charged_micros_usd", 0)
        )
    return result


def fetch_recorded_micros(compose_dir: Path, start: dt.date, end: dt.date) -> dict[str, int]:
    start_ts = int(dt.datetime.combine(start, dt.time(), tzinfo=dt.timezone.utc).timestamp())
    end_ts = int(dt.datetime.combine(end, dt.time(), tzinfo=dt.timezone.utc).timestamp())
    sql = (
        "SELECT created_at, other FROM logs "
        f"WHERE type = 2 AND created_at >= {start_ts} AND created_at < {end_ts};"
    )
    command = compose_command(
        compose_dir,
        "postgres",
        [
            "sh",
            "-c",
            'exec psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -At -F "|" -c "$1"',
            "sh",
            sql,
        ],
    )
    result: dict[str, int] = {}
    for line in run(command).splitlines():
        timestamp_raw, separator, other_raw = line.partition("|")
        if not separator:
            continue
        try:
            other = json.loads(other_raw)
            quantity = float(other.get("usage_quantity", 0))
        except (TypeError, ValueError, json.JSONDecodeError):
            continue
        if other.get("provider") != "aisa" or other.get("usage_unit") != "micro_usd":
            continue
        if quantity <= 0:
            continue
        day = dt.datetime.fromtimestamp(int(timestamp_raw), tz=dt.timezone.utc).date().isoformat()
        result[day] = result.get(day, 0) + int(quantity + 0.5)
    return result


def thresholds(upstream_micros: int) -> int:
    return max(
        int(MIN_DELTA_USD * 1_000_000),
        int(abs(upstream_micros) * PERCENT_DELTA),
    )


def append_record(path: Path, rows: list[dict[str, object]]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("a", encoding="utf-8") as handle:
        for row in rows:
            handle.write(json.dumps(row, separators=(",", ":"), sort_keys=True) + "\n")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--compose-dir",
        default=os.environ.get("NEW_API_COMPOSE_DIR", "/opt/new-api-pool"),
        type=Path,
    )
    parser.add_argument(
        "--since",
        default=(dt.datetime.now(dt.timezone.utc).date() - dt.timedelta(days=7)),
        type=parse_date,
    )
    parser.add_argument(
        "--until",
        default=dt.datetime.now(dt.timezone.utc).date(),
        type=parse_date,
        help="inclusive UTC date",
    )
    parser.add_argument("--output", default=DEFAULT_OUTPUT, type=Path)
    parser.add_argument("--json", action="store_true")
    args = parser.parse_args()
    if args.until < args.since:
        parser.error("--until must be on or after --since")
    if (args.until - args.since).days > 30:
        parser.error("AISA supports at most 31 days per request")

    end = args.until + dt.timedelta(days=1)
    upstream = fetch_aisa_buckets(args.compose_dir, args.since, end)
    recorded = fetch_recorded_micros(args.compose_dir, args.since, end)
    rows: list[dict[str, object]] = []
    current = args.since
    while current <= args.until:
        date = current.isoformat()
        upstream_micros = upstream.get(date, 0)
        recorded_micros = recorded.get(date, 0)
        difference_micros = recorded_micros - upstream_micros
        row = {
            "date": date,
            "upstream_usd": upstream_micros / 1_000_000,
            "recorded_usd": recorded_micros / 1_000_000,
            "difference_usd": difference_micros / 1_000_000,
        }
        rows.append(row)
        if abs(difference_micros) > thresholds(upstream_micros):
            print(
                "WARN capability AISA reconciliation mismatch "
                f"date={date} upstream_usd={row['upstream_usd']:.6f} "
                f"recorded_usd={row['recorded_usd']:.6f} "
                f"difference_usd={row['difference_usd']:.6f}",
                file=sys.stderr,
            )
        current += dt.timedelta(days=1)

    append_record(args.output, rows)
    if args.json:
        for row in rows:
            print(json.dumps(row, separators=(",", ":"), sort_keys=True))
    else:
        print("date upstream_usd recorded_usd difference_usd")
        for row in rows:
            print(
                f"{row['date']} {row['upstream_usd']:.6f} "
                f"{row['recorded_usd']:.6f} {row['difference_usd']:.6f}"
            )
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except RuntimeError as exc:
        print(f"reconciliation failed: {exc}", file=sys.stderr)
        raise SystemExit(1)
