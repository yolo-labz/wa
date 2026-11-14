#!/usr/bin/env python3
"""Reject missing contracted Go tests, any failure, or malformed JSON events."""
import json
import sys


def main():
    if len(sys.argv) < 3:
        raise ValueError("usage: assert-tests-ran.py STREAM TEST [TEST ...]")
    passed = set()
    with open(sys.argv[1], encoding="utf-8") as stream:
        for line in stream:
            event = json.loads(line)
            if event.get("Action") == "fail":
                raise ValueError(f"failure event: {event.get('Test', event.get('Package'))}")
            if event.get("Action") == "pass" and event.get("Test"):
                passed.add(event["Test"])
    missing = set(sys.argv[2:]) - passed
    if missing:
        raise ValueError(f"tests did not pass: {', '.join(sorted(missing))}")
    print(f"PASS: {len(set(sys.argv[2:]))} contracted tests")


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, TypeError, AttributeError) as error:
        sys.exit(str(error))
