"""Protected acceptance evidence. Command success never implies case acceptance."""
import datetime as dt
import hashlib
import json
import math
import os
from pathlib import Path, PurePosixPath
import re
import signal
import subprocess
import threading
import time
import uuid

HERE = Path(__file__).resolve().parent
CASES = {case["id"]: case for case in json.loads((HERE / "cases.json").read_text(encoding="utf-8"))}


def now():
    return dt.datetime.now(dt.timezone.utc).isoformat()


def encode(value):
    return (json.dumps(value, ensure_ascii=False, indent=2, allow_nan=False) + "\n").encode("utf-8")


def write(path, data):
    path = Path(path)
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "wb") as stream:
        stream.write(data)


def load(path):
    path = Path(path)
    if path.is_symlink() or not path.is_file() or path.stat().st_size > 2 * 1024 * 1024:
        raise ValueError("invalid JSON evidence file")
    def unique(pairs):
        result = {}
        for key, value in pairs:
            if key in result:
                raise ValueError("duplicate JSON evidence field")
            result[key] = value
        return result
    return json.loads(path.read_text(encoding="utf-8-sig"), object_pairs_hook=unique)


def digest(path):
    with Path(path).open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def environment(value, allow_unprepared=False):
    if not isinstance(value, dict) or value.get("schema") != 1 or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]{0,79}", value.get("id", "")):
        raise ValueError("environment schema 1 and a stable lab ID required")
    kind = value.get("kind")
    if kind == "unprepared" and allow_unprepared:
        return value
    if kind not in ("real", "local-automation") or value.get("ready") is not True:
        raise ValueError("environment is not prepared; use kind=real or local-automation and ready=true")
    if not value.get("operator") or not value.get("description") or not value.get("timezone"):
        raise ValueError("environment operator, description and timezone required")
    def check(item):
        if isinstance(item, dict):
            for key, child in item.items():
                if key.lower() in {"password", "token", "private_key", "secret", "access_token", "refresh_token", "authorization"}:
                    raise ValueError("environment contains a credential field")
                check(child)
        elif isinstance(item, list):
            for child in item:
                check(child)
    check(value)
    if kind == "real":
        hosts = value.get("hosts", {})
        if not all(isinstance(hosts.get(key), dict) for key in ("L", "C1", "D")):
            raise ValueError("real evidence requires local Linux, remote client and LAN target metadata")
        if not re.fullmatch(r"[0-9a-fA-F]{64}", hosts["L"].get("config_sha256", "")):
            raise ValueError("real evidence requires the local configuration SHA-256")
        if not value.get("network", {}).get("recovery_procedure"):
            raise ValueError("prepare a fault recovery procedure before collecting real evidence")
    return value


def stop(process):
    if os.name == "nt":
        try:
            subprocess.run(["taskkill", "/PID", str(process.pid), "/T", "/F"], capture_output=True,
                           timeout=5, creationflags=subprocess.CREATE_NO_WINDOW)
        except (OSError, subprocess.TimeoutExpired):
            pass
    else:
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
    if process.poll() is None:
        process.kill()
    try:
        process.wait(timeout=5)
    except subprocess.TimeoutExpired:
        pass


def execute(command, output, timeout, limit):
    flags = {"start_new_session": True} if os.name != "nt" else {
        "creationflags": subprocess.CREATE_NEW_PROCESS_GROUP | subprocess.CREATE_NO_WINDOW}
    overflow = threading.Event()
    streams = []
    errors = []
    def pump(source, path):
        count = 0
        try:
            fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(fd, "wb") as target:
                while chunk := source.read1(65536):
                    remaining = max(0, limit - count)
                    target.write(chunk[:remaining])
                    count += len(chunk)
                    if count > limit:
                        overflow.set()
        except (OSError, ValueError) as error:
            errors.append(type(error).__name__)
            overflow.set()
        finally:
            source.close()
    try:
        process = subprocess.Popen(command, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                                   stderr=subprocess.PIPE, **flags)
    except OSError as error:
        write(output / "stdout.bin", b"")
        write(output / "stderr.bin", ("Execution failed: " + type(error).__name__ + "\n").encode())
        return "EXEC-ERROR", 127
    for source, name in ((process.stdout, "stdout.bin"), (process.stderr, "stderr.bin")):
        thread = threading.Thread(target=pump, args=(source, output / name), daemon=True)
        thread.start()
        streams.append(thread)
    deadline = time.monotonic() + timeout
    status = "REVIEW-REQUIRED"
    try:
        while process.poll() is None:
            if overflow.is_set():
                status = "OUTPUT-LIMIT"
                stop(process)
                break
            if time.monotonic() >= deadline:
                status = "TIMEOUT"
                stop(process)
                break
            time.sleep(0.05)
    except KeyboardInterrupt:
        status = "INTERRUPTED"
        stop(process)
    for thread in streams:
        thread.join(timeout=2)
    if any(thread.is_alive() for thread in streams):
        stop(process)
        for thread in streams:
            thread.join(timeout=2)
        if any(thread.is_alive() for thread in streams):
            raise ValueError("output pipes did not close; capture incomplete, do not review as acceptance")
    if errors:
        raise ValueError("could not persist complete command output")
    if overflow.is_set():
        status = "OUTPUT-LIMIT"
    if status == "REVIEW-REQUIRED" and process.returncode != 0:
        status = "COMMAND-FAILED"
    return status, {"TIMEOUT": 124, "INTERRUPTED": 130, "OUTPUT-LIMIT": 125}.get(status, process.returncode)


def collect(case, env_path, output, command, timeout=300, max_log_bytes=16 * 1024 * 1024, attachments=()):
    if case not in CASES or not command:
        raise ValueError("known case and an actual command required")
    if not math.isfinite(timeout) or not 0 < timeout <= 7 * 86400 or not 1024 <= max_log_bytes <= 256 * 1024 * 1024:
        raise ValueError("invalid timeout or log bound")
    env = environment(load(env_path))
    output = Path(output).resolve()
    output.mkdir(mode=0o700, parents=True, exist_ok=False)
    write(output / "environment.json", encode(env))
    for index, source in enumerate(attachments):
        source = Path(source)
        if source.is_symlink() or not source.is_file() or source.stat().st_size > 64 * 1024 * 1024:
            raise ValueError("attachment must be a regular file of at most 64 MB")
        name = re.sub(r"[^A-Za-z0-9_.-]", "_", source.name)
        write(output / "attachments" / (f"{index:02d}-" + name), source.read_bytes())
    started, tick = now(), time.monotonic()
    status, code = execute(command, output, timeout, max_log_bytes)
    # Hash exactly the bytes saved, including the separator and any invalid UTF-8.
    write(output / "verification.log", (output / "stdout.bin").read_bytes() + b"\n--- STDERR ---\n" + (output / "stderr.bin").read_bytes())
    files = {file.relative_to(output).as_posix(): digest(file) for file in sorted(output.rglob("*")) if file.is_file()}
    record = {"schema": 1, "run_id": uuid.uuid4().hex, "case": case, "start": started, "finished": now(),
              "duration_seconds": round(time.monotonic() - tick, 6), "timeout_seconds": timeout,
              "environment_kind": env["kind"], "command": list(command), "exit_code": code, "status": status,
              "files": files, "verification_sha256": files["verification.log"],
              "note": "Manual review of identity, path, access control, recovery and redaction is required. SHA-256 detects changes; it is not a signature."}
    write(output / "result.json", encode(record))
    return record


def safe_file(root, name):
    relative = PurePosixPath(name)
    if not name or "\\" in name or ":" in name or relative.is_absolute() or any(part in ("..", ".") for part in relative.parts):
        raise ValueError("unsafe evidence path")
    file = root / relative
    if not file.resolve().is_relative_to(root.resolve()) or any(parent.is_symlink() for parent in (file, *file.parents) if parent != root.parent):
        raise ValueError("evidence cannot use symlinks or escape its run directory")
    if not file.is_file():
        raise ValueError("missing evidence file")
    return file


def verify(root):
    root = Path(root)
    record = load(root / "result.json")
    if record.get("schema") != 1 or record.get("case") not in CASES or not isinstance(record.get("files"), dict):
        raise ValueError("unsupported evidence record")
    required = {"stdout.bin", "stderr.bin", "verification.log", "environment.json"}
    if not required.issubset(record["files"]):
        raise ValueError("evidence record lacks required files")
    for name, expected in record["files"].items():
        if not isinstance(expected, str) or not re.fullmatch(r"[0-9a-f]{64}", expected) or digest(safe_file(root, name)) != expected:
            raise ValueError("evidence digest mismatch: " + name)
    if record.get("verification_sha256") != record["files"]["verification.log"]:
        raise ValueError("verification log digest mismatch")
    combined = hashlib.sha256()
    for index, name in enumerate(("stdout.bin", "stderr.bin")):
        if index:
            combined.update(b"\n--- STDERR ---\n")
        with (root / name).open("rb") as stream:
            while chunk := stream.read(65536):
                combined.update(chunk)
    if combined.hexdigest() != record["verification_sha256"]:
        raise ValueError("verification log is inconsistent with captured streams")
    actual = {file.relative_to(root).as_posix() for file in root.rglob("*") if file.is_file() or file.is_symlink()}
    if actual - set(record["files"]) - {"result.json", "review.json"}:
        raise ValueError("unrecorded files in evidence directory")
    env = environment(load(root / "environment.json"))
    if record.get("environment_kind") != env["kind"]:
        raise ValueError("environment kind mismatch")
    if record.get("status") not in {"REVIEW-REQUIRED", "COMMAND-FAILED", "TIMEOUT", "EXEC-ERROR", "INTERRUPTED", "OUTPUT-LIMIT"}:
        raise ValueError("a collection record cannot declare acceptance")
    stamps = []
    for key in ("start", "finished"):
        stamp = dt.datetime.fromisoformat(record[key])
        if stamp.tzinfo is None:
            raise ValueError("evidence timestamps require a timezone")
        stamps.append(stamp)
    duration = record.get("duration_seconds")
    if (type(record.get("exit_code")) is not int or not isinstance(duration, (int, float)) or isinstance(duration, bool)
            or not math.isfinite(duration) or duration < 0 or stamps[1] < stamps[0]):
        raise ValueError("invalid execution metadata")
    if (record["status"] == "REVIEW-REQUIRED") != (record["exit_code"] == 0):
        raise ValueError("execution status and exit code disagree")
    return record


CHECKS = {"case-complete", "identity", "path", "permissions", "recovery", "redaction"}


def review(root, verdict, reviewer, notes, checks):
    root = Path(root)
    record = verify(root)
    if verdict not in ("PASS", "FAIL") or not reviewer.strip() or len(notes.strip()) < 10:
        raise ValueError("PASS/FAIL, reviewer and meaningful review notes required")
    checks = set(checks)
    if checks - CHECKS:
        raise ValueError("unknown review confirmation")
    if verdict == "PASS":
        if record["environment_kind"] != "real" or record["status"] != "REVIEW-REQUIRED" or record["exit_code"] != 0:
            raise ValueError("only successful commands in a real environment can be reviewed PASS")
        if checks != CHECKS:
            raise ValueError("PASS requires all case, identity, path, permissions, recovery and redaction confirmations")
    value = {"schema": 1, "case": record["case"], "verdict": verdict, "reviewer": reviewer,
             "reviewed_at": now(), "notes": notes, "confirmations": sorted(checks),
             "result_sha256": digest(root / "result.json")}
    write(root / "review.json", encode(value))
    return value


def verdict(root, record):
    root = Path(root)
    status = "REVIEW-REQUIRED" if record["status"] == "REVIEW-REQUIRED" else "EXECUTION-FAILED"
    if record["environment_kind"] != "real":
        status = "AUTOMATION-ONLY"
    if not (root / "review.json").exists():
        return status
    value = load(root / "review.json")
    if (value.get("schema") != 1 or value.get("case") != record["case"] or not value.get("reviewer")
            or value.get("result_sha256") != digest(root / "result.json") or value.get("verdict") not in ("PASS", "FAIL")
            or len(value.get("notes", "").strip()) < 10):
        raise ValueError("review does not match its evidence record")
    if value["verdict"] == "PASS" and (record["environment_kind"] != "real" or record["status"] != "REVIEW-REQUIRED"
            or record["exit_code"] != 0 or set(value.get("confirmations", [])) != CHECKS):
        raise ValueError("review cannot promote this evidence to PASS")
    return value["verdict"] if record["environment_kind"] == "real" else "AUTOMATION-ONLY"


def initialize(output, env_path=None):
    value = load(env_path or HERE / "environment.example.json")
    environment(value, allow_unprepared=True)
    root = Path(output).resolve()
    root.mkdir(mode=0o700, parents=True, exist_ok=False)
    write(root / "environment.json", encode(value))
    for case, entry in CASES.items():
        write(root / case / "case.json", encode({**entry, "status": "NOT-RUN",
              "instructions": "NETWORK_ACCEPTANCE.zh-CN.md " + case,
              "note": "Collect each actual attempt in a new runs/<name> directory; never overwrite earlier evidence."}))
    write(root / "suite.json", encode({"schema": 1, "created": now(), "cases": list(CASES),
          "environment": "environment.json", "real_network_acceptance": "NOT-RUN"}))
    return {"cases": len(CASES), "status": "NOT-RUN"}


def report(source, output):
    source = Path(source).resolve()
    if not source.is_dir():
        raise ValueError("suite directory does not exist")
    entries = {case: {**entry, "status": "NOT-RUN", "runs": []} for case, entry in CASES.items()}
    errors = []
    for path in sorted(source.rglob("result.json")):
        root = path.parent
        name = root.relative_to(source).as_posix()
        case = None
        try:
            # A symlink is never an input to a report, even if it points inside.
            safe_file(source, path.relative_to(source).as_posix())
            raw = load(path)
            case = raw.get("case")
            record = verify(root)
            status = verdict(root, record)
            entry = {"path": name, "status": status, "collection_status": record["status"], "finished": record["finished"]}
            entries[record["case"]]["runs"].append(entry)
        except (ValueError, KeyError, TypeError, OSError) as error:
            errors.append({"path": name, "error": str(error)})
            if case not in CASES:
                case = next((part for part in root.parts if part in CASES), None)
            if case in CASES:
                entries[case]["status"] = "INTEGRITY-ERROR"
    for entry in entries.values():
        if entry["status"] == "INTEGRITY-ERROR" or not entry["runs"]:
            continue
        # A new failed or unreviewed attempt supersedes an old passing review.
        latest = max(entry["runs"], key=lambda r: (dt.datetime.fromisoformat(r["finished"]), r["path"]))
        entry["status"], entry["latest_run"] = latest["status"], latest["path"]
    counts = {}
    for entry in entries.values():
        counts[entry["status"]] = counts.get(entry["status"], 0) + 1
    requirements = {}
    for requirement in (f"R{i:02d}" for i in range(1, 13)):
        cases = [case for case, entry in entries.items() if requirement in entry["requirements"]]
        requirements[requirement] = {"cases": cases, "status": "PASS" if all(entries[c]["status"] == "PASS" for c in cases) else "NOT-PASSED"}
    result = {"schema": 1, "generated": now(), "counts": counts, "cases": entries, "requirements": requirements,
              "integrity_errors": errors, "release_gate": "PASS" if counts.get("PASS") == 33 and not errors else "CLOSED"}
    lines = ["# 网络验收证据汇总", "", "生成时间（UTC）：" + result["generated"], "",
             "发行门槛：**" + result["release_gate"] + "**。完整性校验不能替代人工检查网络行为。", "",
             "| 用例 | 场景 | 状态 | 最近证据 |", "| --- | --- | --- | --- |"]
    for case, entry in entries.items():
        lines.append(f"| {case} | {entry['title']} | {entry['status']} | {entry.get('latest_run', '—')} |")
    lines += ["", "## 需求追踪", "", "| 需求 | 用例 | 状态 |", "| --- | --- | --- |"]
    for requirement, entry in requirements.items():
        lines.append(f"| {requirement} | {', '.join(entry['cases'])} | {entry['status']} |")
    if errors:
        lines += ["", f"完整性错误：{len(errors)}；详细原因见 summary.json。"]
    output = Path(output).resolve()
    output.mkdir(mode=0o700, parents=True, exist_ok=False)
    write(output / "summary.json", encode(result))
    write(output / "REPORT.zh-CN.md", ("\n".join(lines) + "\n").encode("utf-8"))
    return result


def metrics(rows):
    groups = {}
    for row in rows:
        if row.get("path") not in ("direct", "local_relay", "auxiliary_relay") or type(row.get("success")) is not bool:
            raise ValueError("metric path and success flag required")
        value = row.get("business_seconds")
        if not isinstance(value, (int, float)) or isinstance(value, bool) or not math.isfinite(value) or value < 0:
            raise ValueError("metric durations must be finite nonnegative seconds")
        key = (row["path"], row.get("fault", "unspecified"))
        groups.setdefault(key, []).append(row)
    result = []
    for (path, fault), samples in sorted(groups.items()):
        successes = sorted(row["business_seconds"] for row in samples if row["success"])
        def percentile(q):
            return successes[max(0, math.ceil(q * len(successes)) - 1)] if successes else None
        result.append({"path": path, "fault": fault, "samples": len(samples), "successes": len(successes),
                       "failure_rate": 1 - len(successes) / len(samples), "p50_seconds": percentile(.5),
                       "p95_seconds": percentile(.95), "max_seconds": max(successes) if successes else None,
                       "sample_target_met": len(samples) >= 20, "note": "Successful attempts only; failures reported separately. Not an acceptance verdict."})
    return result
