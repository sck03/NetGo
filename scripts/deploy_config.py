"""Configuration validation and deterministic template rendering for render-deploy.py."""
import argparse
import base64
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import re
import secrets
import sys
from urllib.parse import urlsplit

ROOT = Path(__file__).resolve().parents[1]
MODES = ("diagnostic", "443")


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate JSON field")
        result[key] = value
    return result


def read_json(path):
    raw = Path(path).read_bytes()
    if len(raw) > 1024 * 1024:
        raise ValueError("configuration exceeds 1 MB")
    return json.loads(raw.decode("utf-8-sig"), object_pairs_hook=unique_object), raw


def port(value, default=None):
    if value in (None, 0) and not isinstance(value, bool):
        value = default
    if not isinstance(value, int) or isinstance(value, bool) or not 1 <= value <= 65535:
        raise ValueError("port must be an integer in 1..65535")
    return value


def hostname(value):
    if not isinstance(value, str) or not 1 <= len(value) <= 253:
        raise ValueError("DNS hostname required")
    if any(not re.fullmatch(r"[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?", label)
           for label in value.split(".")):
        raise ValueError("invalid DNS hostname")
    return value


def authority(value, scheme):
    if not isinstance(value, str) or re.search(r'[\s\\"<>`{}]', value):
        raise ValueError("invalid authority characters")
    parsed = urlsplit(value)
    if (parsed.scheme != scheme or not parsed.hostname or parsed.username is not None
            or parsed.password is not None or parsed.path or parsed.query or parsed.fragment
            or parsed.netloc.endswith(":")):
        raise ValueError("invalid " + scheme + " authority")
    if parsed.port is not None and not 1 <= parsed.port <= 65535:
        raise ValueError("invalid authority port")
    return parsed


def loopback(value):
    parsed = authority("http://" + value, "http")
    if not ipaddress.ip_address(parsed.hostname).is_loopback or parsed.port is None:
        raise ValueError("internal listener must be loopback with an explicit port")
    port(parsed.port)
    return parsed.netloc


def distinct(values, label):
    if len(values) != len(set(values)):
        raise ValueError(label + " listener ports conflict")


def secrets_config(path):
    if path:
        values, _ = read_json(path)
    else:
        values = {"relay_secret": secrets.token_hex(32), "frp_secret": secrets.token_hex(32),
                  "store_key": base64.b64encode(secrets.token_bytes(32)).decode(),
                  "native_secret": secrets.token_hex(32)}
    if set(values) != {"relay_secret", "frp_secret", "store_key", "native_secret"}:
        raise ValueError("secrets require exactly relay_secret, frp_secret, store_key and native_secret")
    for value in values.values():
        if not isinstance(value, str) or not re.fullmatch(r"[A-Za-z0-9+/=_-]{32,128}", value):
            raise ValueError("invalid secret format")
    if len(base64.b64decode(values["store_key"], validate=True)) != 32:
        raise ValueError("store_key must encode exactly 32 bytes")
    return values


def configuration(config, selected_mode=None):
    allowed = {"Site", "Listen", "Database", "ManagementURL", "ManagementTokenFile", "NativeSecretFile",
               "HelperSocket", "Interface", "LANInterface", "OverlayCIDR", "LocalCIDRs", "LeaseSeconds",
               "Auxiliaries", "Deployment"}
    if not isinstance(config, dict) or set(config) - allowed:
        raise ValueError("unknown server configuration field")
    site = config["Site"]
    revision = site.get("ConfigRevision", 0)
    if type(revision) is not int or not 0 <= revision <= 9007199254740991:
        raise ValueError("invalid configuration revision")
    if site["Protocol"] != 1 or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]{0,62}", site["ID"]):
        raise ValueError("invalid site protocol or ID")
    origin = authority(site["Origin"], "https")
    control_host = hostname(origin.hostname)
    origin_port = port(origin.port, 443)
    local_relay = hostname(site["LocalRelay"])
    deployment = config.get("Deployment") or {}
    internal_defaults = {"ManagementHealthPort": 9000, "ManagementMetricsPort": 9090,
                         "RelayHealthPort": 9001, "RelayMetricsPort": 9091, "CaddyAdminPort": 2019}
    if set(deployment) - {"Mode", "LocalRelayPort", "LocalSTUNPort", *internal_defaults}:
        raise ValueError("unknown deployment field")
    configured_mode = deployment.get("Mode") or None
    if selected_mode and configured_mode and selected_mode != configured_mode:
        raise ValueError("--mode conflicts with Deployment.Mode")
    mode = selected_mode or configured_mode or "diagnostic"
    if mode not in MODES:
        raise ValueError("deployment mode must be diagnostic or 443")
    relay_port = port(deployment.get("LocalRelayPort"), 443 if mode == "443" else 8443)
    stun_port = port(deployment.get("LocalSTUNPort"), 3478)
    if mode == "443" and (origin_port != 443 or relay_port != 443):
        raise ValueError("443 mode requires public control and Relay port 443; use diagnostic for custom ports")
    if mode == "443" and control_host.lower() == local_relay.lower():
        raise ValueError("control and Relay SNI must differ")
    control_port = 9443 if mode == "443" else origin_port
    relay_bind_port = 8443 if mode == "443" else relay_port
    product = loopback(config["Listen"])
    management = authority(config["ManagementURL"], "http")
    management_port = port(management.port, 80)
    management_host = "[" + management.hostname + "]" if ":" in management.hostname else management.hostname
    management_listen = loopback(management_host + ":" + str(management_port))
    internal_ports = {key: port(deployment.get(key), fallback) for key, fallback in internal_defaults.items()}
    tcp = [control_port, relay_bind_port, urlsplit("http://" + product).port, management_port,
           *internal_ports.values()]
    if mode == "443":
        tcp.append(443)
    distinct(tcp, "local TCP")
    if stun_port in (relay_port, relay_bind_port):
        raise ValueError("local STUN and Relay QUIC ports conflict")
    candidates = site["Candidates"]
    if not 1 <= len(candidates) <= 8 or not any(c["Kind"] == "direct" for c in candidates):
        raise ValueError("one direct candidate and at most eight total candidates required")
    if len({c["ID"] for c in candidates}) != len(candidates):
        raise ValueError("duplicate candidate ID")
    for candidate in candidates:
        if candidate["Kind"] not in ("direct", "auxiliary"):
            raise ValueError("invalid candidate kind")
        parsed = authority("https://" + candidate["Address"], "https")
        port(parsed.port)
    values = {"CONTROL_HOST": control_host, "CONTROL_ORIGIN": site["Origin"],
              "CONTROL_PORT": str(control_port), "CONTROL_BIND_DIRECTIVE": " bind 127.0.0.1" if mode == "443" else "",
              "PRODUCT_LISTEN": product, "MANAGEMENT_LISTEN": management_listen,
              "LOCAL_RELAY": local_relay, "PUBLIC_RELAY_PORT": str(relay_port),
              "RELAY_BIND_PORT": str(relay_bind_port), "STUN_PORT": str(stun_port),
              "AUX_STUN": "", "AUX_RELAY": ""}
    values.update({"MANAGEMENT_HEALTH_PORT": str(internal_ports["ManagementHealthPort"]),
                   "MANAGEMENT_METRICS_PORT": str(internal_ports["ManagementMetricsPort"]),
                   "RELAY_HEALTH_PORT": str(internal_ports["RelayHealthPort"]),
                   "RELAY_METRICS_PORT": str(internal_ports["RelayMetricsPort"]),
                   "CADDY_ADMIN_PORT": str(internal_ports["CaddyAdminPort"])})
    local = {"mode": mode, "control_origin": site["Origin"], "control_port": origin_port,
             "control_backend_port": control_port, "product_listen": product, "management_listen": management_listen,
             "relay_endpoint": f"rels://{local_relay}:{relay_port}", "relay_port": relay_port,
             "relay_bind_port": relay_bind_port, "stun_port": stun_port, "internal_ports": internal_ports}
    auxiliaries = config.get("Auxiliaries") or []
    if len(auxiliaries) > 1:
        raise ValueError("only one auxiliary is supported")
    auxiliary = None
    if auxiliaries:
        a = auxiliaries[0]
        if set(a) - {"ID", "Host", "Relay", "STUN", "TunnelHost", "TokenFile", "Mode", "ControlPort", "TunnelPort"}:
            raise ValueError("unknown auxiliary field")
        aux_mode = a["Mode"]
        if aux_mode not in MODES:
            raise ValueError("invalid auxiliary mode")
        aux_host, tunnel_host = hostname(a["Host"]), hostname(a["TunnelHost"])
        relay = authority(a["Relay"], "rels")
        relay_host = hostname(relay.hostname)
        aux_relay_port = port(relay.port)
        if not a["STUN"].startswith("stun:"):
            raise ValueError("auxiliary STUN must be stun:relay-host:port")
        stun = authority("http://" + a["STUN"][5:], "http")
        aux_stun_port = port(stun.port)
        if stun.hostname.lower() != relay_host.lower():
            raise ValueError("auxiliary STUN must use the Relay host")
        public_control = port(a.get("ControlPort"), 443 if aux_mode == "443" else 8443)
        public_tunnel = port(a.get("TunnelPort"), 443 if aux_mode == "443" else 7000)
        if aux_mode == "443" and (public_control, public_tunnel, aux_relay_port) != (443, 443, 443):
            raise ValueError("443 auxiliary requires public control, Relay and tunnel port 443")
        if aux_mode == "443":
            distinct([control_host.lower(), relay_host.lower(), tunnel_host.lower()], "auxiliary SNI")
        remote_control = 24443 if aux_mode == "443" else public_control
        tunnel_bind = 7000 if aux_mode == "443" else public_tunnel
        aux_relay_bind = 8443 if aux_mode == "443" else aux_relay_port
        aux_tcp = [remote_control, tunnel_bind, aux_relay_bind, 9001, 9091]
        if aux_mode == "443":
            aux_tcp.append(443)
        distinct(aux_tcp, "auxiliary TCP")
        if aux_stun_port in (aux_relay_port, aux_relay_bind):
            raise ValueError("auxiliary STUN and Relay QUIC ports conflict")
        expected = f"{aux_host}:{public_control}".lower()
        aux_candidates = [c for c in candidates if c["Kind"] == "auxiliary"]
        if len(aux_candidates) != 1 or aux_candidates[0]["ID"] != a["ID"] or aux_candidates[0]["Address"].lower() != expected:
            raise ValueError("auxiliary candidate must match its ID, Host and ControlPort; run netgoctl auxiliary add")
        if site.get("AuxiliaryRelay") != a["Relay"]:
            raise ValueError("Site.AuxiliaryRelay must match the configured auxiliary Relay")
        values.update({"AUX_HOST": aux_host, "AUX_RELAY_HOST": relay_host, "TUNNEL_HOST": tunnel_host,
                       "TUNNEL_PORT": str(public_tunnel), "TUNNEL_BIND_PORT": str(tunnel_bind),
                       "REMOTE_CONTROL_PORT": str(remote_control), "AUX_RELAY_PORT": str(aux_relay_port),
                       "AUX_RELAY_BIND_PORT": str(aux_relay_bind), "AUX_STUN_PORT": str(aux_stun_port),
                       "PROXY_BIND": "127.0.0.1" if aux_mode == "443" else "0.0.0.0",
                       "TUNNEL_BIND": "127.0.0.1" if aux_mode == "443" else "0.0.0.0",
                       "AUX_STUN": "    - uri: " + json.dumps(a["STUN"]),
                       "AUX_RELAY": "      - " + json.dumps(a["Relay"])})
        auxiliary = {"mode": aux_mode, "host": aux_host, "control_port": public_control,
                     "control_backend_port": remote_control, "tunnel_port": public_tunnel,
                     "tunnel_bind_port": tunnel_bind, "relay_endpoint": a["Relay"],
                     "relay_port": aux_relay_port, "relay_bind_port": aux_relay_bind, "stun_port": aux_stun_port}
    elif any(c["Kind"] == "auxiliary" for c in candidates) or site.get("AuxiliaryRelay"):
        raise ValueError("auxiliary candidates/Relay exist without an auxiliary deployment")
    return values, {"schema": 1, "site_id": site["ID"], "local": local, "auxiliary": auxiliary}


def expand(template, values):
    text = template.read_text(encoding="utf-8")
    for key, value in values.items():
        text = text.replace("@@" + key + "@@", value)
    if re.search(r"@@[A-Z_]+@@", text):
        raise ValueError("unexpanded template: " + template.name)
    return text


def render(config_path, output, mode=None, secret_path=None):
    config, raw = read_json(config_path)
    values, manifest = configuration(config, mode)
    secret = secrets_config(secret_path)
    values.update({"RELAY_SECRET": secret["relay_secret"], "FRP_SECRET": secret["frp_secret"], "STORE_KEY": secret["store_key"]})
    files = {}
    templates = ROOT / "deploy/templates"
    for source in sorted(templates.rglob("*.tmpl")):
        rel = source.relative_to(templates)
        node = manifest[rel.parts[0]]
        if node is None or (source.name == "frpc.toml.tmpl" and manifest["auxiliary"] is None):
            continue
        if source.name in ("haproxy.cfg.tmpl", "relay-port.nft.tmpl") and node["mode"] != "443":
            continue
        files[rel.as_posix().removesuffix(".tmpl")] = expand(source, values)
    if manifest["local"]["mode"] == "443":
        files["local/relay-port.nft"] = expand(templates / "auxiliary/relay-port.nft.tmpl",
                                                {"AUX_RELAY_PORT": "443", "AUX_RELAY_BIND_PORT": values["RELAY_BIND_PORT"]})
    files["secrets.json"] = json.dumps(secret, indent=2) + "\n"
    files["local/native.secret"] = secret["native_secret"] + "\n"
    files["README.txt"] = ("Generated for review. No services or firewall rules applied. Validate site.json with netgoctl config validate.\n"
                           "Keep secrets.json for repeat renders. Copy only auxiliary/ to the VPS. Control TLS and native.secret stay local.\n"
                           "deployment.json records actual ports and file digests. Open only intended public ports; keep backends private.\n"
                           "Validate using the pinned Caddy/frp/HAProxy executables before activation. Protect this entire directory.\n")
    manifest["source_sha256"] = hashlib.sha256(raw).hexdigest()
    manifest["files"] = {name: hashlib.sha256(value.encode("utf-8")).hexdigest() for name, value in sorted(files.items())}
    files["deployment.json"] = json.dumps(manifest, indent=2, ensure_ascii=False) + "\n"
    out = Path(output).resolve()
    out.mkdir(mode=0o700, parents=True, exist_ok=False)
    for name, value in files.items():
        target = out / name
        target.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        descriptor = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(descriptor, "wb") as stream:
            stream.write(value.encode("utf-8"))
    return manifest


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--mode", choices=MODES, help="default: Deployment.Mode or diagnostic")
    parser.add_argument("--secrets", help="protected secrets.json from an earlier render")
    args = parser.parse_args()
    try:
        manifest = render(args.config, args.output, args.mode, args.secrets)
    except (ValueError, KeyError, TypeError, OSError) as error:
        print("render failed: " + (str(error) if isinstance(error, ValueError) else type(error).__name__), file=sys.stderr)
        return 2
    print(json.dumps({"output": str(Path(args.output).resolve()), "mode": manifest["local"]["mode"],
                      "auxiliary": manifest["auxiliary"] is not None, "secrets": "protected files; not printed"}))
    return 0
