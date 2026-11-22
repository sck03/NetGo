#!/usr/bin/env python3
"""Collect real acceptance evidence. Never infer PASS from simulated connectivity."""
import argparse
import json
from pathlib import Path
import sys
import evidence


def main():
    argv = sys.argv[1:]
    # Preserve the original --case ... -- command form.
    command = argv.pop(0) if argv and argv[0] in {"collect", "init", "verify", "review", "report", "metrics"} else "collect"
    parser = argparse.ArgumentParser(prog="run.py " + command, description=__doc__)
    if command == "collect":
        parser.add_argument("--case", required=True, choices=list(evidence.CASES))
        parser.add_argument("--environment", required=True)
        parser.add_argument("--output", required=True)
        parser.add_argument("--timeout", type=float, default=300)
        parser.add_argument("--max-log-bytes", type=int, default=16 * 1024 * 1024)
        parser.add_argument("--attach", action="append", default=[])
        parser.add_argument("command", nargs=argparse.REMAINDER)
    elif command == "init":
        parser.add_argument("--output", required=True)
        parser.add_argument("--environment")
    elif command in ("verify", "review", "report", "metrics"):
        parser.add_argument("--input", required=True)
        if command == "review":
            parser.add_argument("--status", required=True, choices=["PASS", "FAIL"])
            parser.add_argument("--reviewer", required=True)
            parser.add_argument("--notes-file", required=True)
            parser.add_argument("--confirm", action="append", choices=sorted(evidence.CHECKS), default=[])
        if command == "report":
            parser.add_argument("--output", required=True)
    args = parser.parse_args(argv)
    try:
        if command == "collect":
            actual = args.command[1:] if args.command and args.command[0] == "--" else args.command
            result = evidence.collect(args.case, args.environment, args.output, actual, args.timeout, args.max_log_bytes, args.attach)
            print(json.dumps({"case": result["case"], "exit_code": result["exit_code"], "status": result["status"]}))
            return result["exit_code"] if result["exit_code"] >= 0 else 1
        if command == "init":
            result = evidence.initialize(args.output, args.environment)
        elif command == "verify":
            record = evidence.verify(args.input)
            result = {"integrity": "PASS", "case_status": evidence.verdict(args.input, record)}
        elif command == "review":
            result = evidence.review(args.input, args.status, args.reviewer, Path(args.notes_file).read_text(encoding="utf-8"), args.confirm)
        elif command == "report":
            summary = evidence.report(args.input, args.output)
            result = {"counts": summary["counts"], "release_gate": summary["release_gate"], "integrity_errors": len(summary["integrity_errors"])}
            if summary["integrity_errors"]:
                print(json.dumps(result, ensure_ascii=False))
                return 1
        else:
            result = evidence.metrics(evidence.load(args.input))
        print(json.dumps(result, ensure_ascii=False))
        return 0
    except (ValueError, KeyError, TypeError, OSError) as error:
        print("evidence error: " + str(error), file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
