#!/usr/bin/env python3
"""Validate generated configurations with product and pinned upstream executables."""
import argparse
import copy
import json
import os
from pathlib import Path
import subprocess
import sys
from deploy_config import ROOT, render


def verify(args):
    output = Path(args.output).resolve()
    output.mkdir(mode=0o700, parents=True, exist_ok=False)
    lock = json.loads((ROOT / "upstream/versions.lock.json").read_text())
    records = []

    def run(label, command, version=None):
        result = subprocess.run([str(p) for p in command], capture_output=True, timeout=30)
        ok = result.returncode == 0
        if version:
            ok = ok and version in result.stdout.decode("utf-8", errors="replace")
        records.append({"check": label, "status": "PASS" if ok else "FAIL", "exit_code": result.returncode})
        if not ok:
            diagnostic = output / ("failed-validator-" + str(len(records)) + ".log")
            descriptor = os.open(diagnostic, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(descriptor, "wb") as stream:
                stream.write(result.stdout + b"\n--- STDERR ---\n" + result.stderr)
            # Do not publish configuration dumps or command arguments containing paths.
            raise ValueError(label + " failed (protected failed-validator log retained)")

    try:
        run("Caddy locked version", [args.caddy, "version"], "v" + lock["caddy"]["version"])
        run("frpc locked version", [args.frpc, "--version"], lock["frp"]["version"])
        run("frps locked version", [args.frps, "--version"], lock["frp"]["version"])
        base = json.loads((ROOT / "examples/site.json").read_text(encoding="utf-8-sig"))
        for mode in ("diagnostic", "443"):
            for auxiliary_mode in (None, "diagnostic", "443"):
                name = mode + "-" + (auxiliary_mode or "standalone")
                config = copy.deepcopy(base)
                config["Deployment"]["Mode"] = mode
                if mode == "443":
                    config["Deployment"]["LocalRelayPort"] = 443
                    config["Site"]["Origin"] = "https://control.site.example"
                    config["Site"]["Candidates"][0]["Address"] = "control.site.example:443"
                else:
                    # Internal services are deliberately moved off the defaults.
                    config["Listen"] = "[::1]:18081"
                    config["ManagementURL"] = "http://[::1]:18080"
                if auxiliary_mode:
                    filename = "auxiliary-443.json" if auxiliary_mode == "443" else "auxiliary.json"
                    aux = json.loads((ROOT / "examples" / filename).read_text())
                    config["Auxiliaries"] = [aux]
                    config["Site"]["AuxiliaryRelay"] = aux["Relay"]
                    config["Site"]["Candidates"].append({"ID": aux["ID"], "Kind": "auxiliary",
                        "Address": aux["Host"] + ":" + str(aux.get("ControlPort", 443))})
                source = output / (name + ".json")
                source.write_text(json.dumps(config), encoding="utf-8")
                run(name + " product config", [args.netgoctl, "config", "validate", "--config", source])
                bundle = output / name
                render(source, bundle)
                run(name + " Caddy adapt", [args.caddy, "adapt", "--config", bundle / "local/Caddyfile", "--adapter", "caddyfile"])
                if auxiliary_mode:
                    run(name + " frpc verify", [args.frpc, "verify", "-c", bundle / "local/frpc.toml"])
                    run(name + " frps verify", [args.frps, "verify", "-c", bundle / "auxiliary/frps.toml"])
                if args.haproxy:
                    for node in ("local", "auxiliary"):
                        file = bundle / node / "haproxy.cfg"
                        if file.exists():
                            run(name + " " + node + " HAProxy", [args.haproxy, "-c", "-f", file])
        return 0
    finally:
        report = {"schema": 1, "checks": records, "real_network_acceptance": "NOT-RUN",
                  "haproxy": "checked" if args.haproxy else "NOT-RUN: requires Linux HAProxy",
                  "note": "Configuration parsing only; no listeners, firewall changes, certificate issuance or LAN traffic."}
        (output / "validation.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("netgoctl", "caddy", "frpc", "frps", "output"):
        parser.add_argument("--" + name, required=True)
    parser.add_argument("--haproxy")
    args = parser.parse_args()
    try:
        result = verify(args)
    except (ValueError, OSError, subprocess.SubprocessError) as error:
        print("FAIL " + str(error), file=sys.stderr)
        return 1
    print("PASS six deployment combinations and locked validators; real network acceptance NOT-RUN")
    return result


if __name__ == "__main__":
    sys.exit(main())
