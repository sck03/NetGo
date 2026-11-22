#!/usr/bin/env python3
"""Check the certificate hook with disposable keys, without deployment or services."""
import argparse
import os
from pathlib import Path
import shutil
import subprocess
import tempfile


def posix(path):
    value = Path(path).resolve().as_posix()
    return "/" + value[0].lower() + value[2:] if os.name == "nt" else value


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bash", required=True)
    parser.add_argument("--openssl", default=shutil.which("openssl"))
    args = parser.parse_args()
    if not args.openssl:
        parser.error("OpenSSL is required")
    root = Path(__file__).resolve().parents[1]
    with tempfile.TemporaryDirectory(prefix="netgo-certificate-check-") as temporary:
        directory = Path(temporary)
        def run(command):
            subprocess.run([str(v) for v in command], check=True, capture_output=True, timeout=30)
        def issue(days):
            run([args.openssl, "req", "-x509", "-newkey", "rsa:2048", "-sha256", "-nodes",
                 "-keyout", directory / "privkey.pem", "-out", directory / "fullchain.pem", "-days", str(days),
                 "-subj", "/CN=control.site.example", "-addext", "subjectAltName=DNS:control.site.example",
                 "-addext", "extendedKeyUsage=serverAuth"])
        env = dict(os.environ, RENEWED_LINEAGE=posix(directory))
        def check(host, success):
            result = subprocess.run([args.bash, posix(root / "deploy/linux/deploy-certificate.sh"), "--check", "control", host],
                                    env=env, capture_output=True, timeout=30)
            if (result.returncode == 0) != success:
                raise RuntimeError("certificate hook returned an unexpected validation result")
        issue(30)
        check("control.site.example", True)
        print("PASS matching hostname, usable validity and private key")
        check("wrong.site.example", False)
        print("PASS wrong certificate hostname rejected")
        run([args.openssl, "genpkey", "-algorithm", "RSA", "-pkeyopt", "rsa_keygen_bits:2048", "-out", directory / "wrong-key.pem"])
        shutil.copyfile(directory / "wrong-key.pem", directory / "privkey.pem")
        check("control.site.example", False)
        print("PASS mismatched certificate/private key rejected")
        issue(1)
        check("control.site.example", False)
        print("PASS certificate near expiry rejected")
    print("Certificate deployment and systemd recovery NOT-RUN; validation mode made no service changes")


if __name__ == "__main__":
    main()
