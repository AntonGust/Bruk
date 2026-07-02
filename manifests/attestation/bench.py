#!/usr/bin/env python3
"""Single-stream + batched tok/s benchmark against a vLLM OpenAI endpoint.

Usage: bench.py <base_url> <model> [runs] [max_tokens] [batch]
Reports decode throughput = completion_tokens / wall_time per request.
"""
import json
import statistics
import sys
import time
import urllib.request
from concurrent.futures import ThreadPoolExecutor

DEFAULT_RUNS = 5
DEFAULT_MAX_TOKENS = 400
DEFAULT_BATCH = 8
PROMPT = "Write a detailed, multi-paragraph explanation of how public-key cryptography works, including key generation, encryption, and signatures."


def one_request(base_url, model, max_tokens):
    payload = json.dumps({
        "model": model,
        "prompt": PROMPT,
        "max_tokens": max_tokens,
        "temperature": 0,
        "ignore_eos": True,
    }).encode()
    req = urllib.request.Request(
        f"{base_url}/v1/completions", data=payload,
        headers={"Content-Type": "application/json"})
    start = time.monotonic()
    with urllib.request.urlopen(req, timeout=300) as resp:
        body = json.load(resp)
    elapsed = time.monotonic() - start
    tokens = body["usage"]["completion_tokens"]
    return tokens, elapsed


def main():
    if len(sys.argv) < 3:
        sys.exit(__doc__)
    base_url, model = sys.argv[1], sys.argv[2]
    runs = int(sys.argv[3]) if len(sys.argv) > 3 else DEFAULT_RUNS
    max_tokens = int(sys.argv[4]) if len(sys.argv) > 4 else DEFAULT_MAX_TOKENS
    batch = int(sys.argv[5]) if len(sys.argv) > 5 else DEFAULT_BATCH

    tokens, elapsed = one_request(base_url, model, max_tokens)
    print(f"warmup: {tokens} tok in {elapsed:.2f}s ({tokens/elapsed:.1f} tok/s)")

    rates = []
    for i in range(runs):
        tokens, elapsed = one_request(base_url, model, max_tokens)
        rate = tokens / elapsed
        rates.append(rate)
        print(f"single[{i+1}/{runs}]: {tokens} tok in {elapsed:.2f}s ({rate:.1f} tok/s)")
    print(f"SINGLE-STREAM: mean {statistics.mean(rates):.1f} tok/s, "
          f"min {min(rates):.1f}, max {max(rates):.1f} (n={runs})")

    start = time.monotonic()
    with ThreadPoolExecutor(max_workers=batch) as pool:
        results = list(pool.map(
            lambda _: one_request(base_url, model, max_tokens), range(batch)))
    wall = time.monotonic() - start
    total_tokens = sum(t for t, _ in results)
    print(f"BATCHED (n={batch}): {total_tokens} tok in {wall:.2f}s "
          f"aggregate {total_tokens/wall:.1f} tok/s")


if __name__ == "__main__":
    main()
