import copy
import hashlib
import json
from pathlib import Path
import sys
import tempfile
import tomllib
import unittest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
import deploy_config as deploy


class DeploymentTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.config = json.loads((ROOT / "examples/site.json").read_text(encoding="utf-8-sig"))

    def auxiliary(self, mode):
        aux = json.loads((ROOT / "examples/auxiliary.json").read_text())
        aux.update(Mode=mode, ControlPort=0, TunnelPort=0, Relay="rels://relay.aux.example:443")
        self.config["Auxiliaries"] = [aux]
        self.config["Site"]["AuxiliaryRelay"] = aux["Relay"]
        self.config["Site"]["Candidates"].append({"ID": aux["ID"], "Kind": "auxiliary",
                                                "Address": aux["Host"] + (":443" if mode == "443" else ":8443")})
        return aux

    def render(self, mode=None, suffix="out", secrets=None):
        source = self.root / "site.json"
        source.write_text(json.dumps(self.config), encoding="utf-8")
        output = self.root / suffix
        manifest = deploy.render(source, output, mode, secrets)
        return output, manifest

    def test_modes_with_and_without_auxiliary(self):
        base = copy.deepcopy(self.config)
        for local_mode in deploy.MODES:
            for auxiliary_mode in (None, *deploy.MODES):
                with self.subTest(local=local_mode, auxiliary=auxiliary_mode):
                    self.config = copy.deepcopy(base)
                    self.config["Deployment"] = {"Mode": local_mode}
                    if local_mode == "443":
                        self.config["Site"]["Origin"] = "https://control.site.example"
                        self.config["Site"]["Candidates"][0]["Address"] = "control.site.example:443"
                    if auxiliary_mode:
                        self.auxiliary(auxiliary_mode)
                    out, manifest = self.render(local_mode, f"{local_mode}-{auxiliary_mode}")
                    self.assertEqual((out / "local/haproxy.cfg").exists(), local_mode == "443")
                    self.assertEqual((out / "auxiliary/haproxy.cfg").exists(), auxiliary_mode == "443")
                    self.assertEqual((out / "auxiliary/relay-port.nft").exists(), auxiliary_mode == "443")
                    self.assertEqual((out / "local/relay-port.nft").exists(), local_mode == "443")
                    self.assertEqual((out / "local/frpc.toml").exists(), auxiliary_mode is not None)
                    for name, digest in manifest["files"].items():
                        raw = (out / name).read_bytes()
                        self.assertEqual(hashlib.sha256(raw).hexdigest(), digest)
                        self.assertNotIn(b"@@", raw)
                    if auxiliary_mode:
                        client = tomllib.loads((out / "local/frpc.toml").read_text())
                        server = tomllib.loads((out / "auxiliary/frps.toml").read_text())
                        self.assertEqual(client["proxies"][0]["remotePort"], server["allowPorts"][0]["single"])
                        self.assertEqual(client["proxies"][0]["localPort"], manifest["local"]["control_backend_port"])
                        if auxiliary_mode == "diagnostic":
                            relay = (out / "auxiliary/relay.env").read_text()
                            self.assertIn("NB_LISTEN_ADDRESS=:443\n", relay)
                            self.assertEqual(client["serverPort"], server["bindPort"])
                    else:
                        self.assertFalse((out / "auxiliary").exists())
                        self.assertNotIn("aux.example", (out / "local/netbird.yaml").read_text())

    def test_custom_ports_and_internal_ipv6(self):
        self.config["Site"]["Origin"] = "https://control.site.example:10443"
        self.config["Site"]["Candidates"][0]["Address"] = "control.site.example:10443"
        self.config["Listen"] = "[::1]:18081"
        self.config["ManagementURL"] = "http://[::1]:18080"
        self.config["Deployment"] = {"Mode": "diagnostic", "LocalRelayPort": 18443, "LocalSTUNPort": 13478}
        aux = self.auxiliary("diagnostic")
        aux.update(Relay="rels://relay.aux.example:28443", STUN="stun:relay.aux.example:23478", ControlPort=24444, TunnelPort=27000)
        self.config["Site"]["AuxiliaryRelay"] = aux["Relay"]
        self.config["Site"]["Candidates"][-1]["Address"] = aux["Host"] + ":24444"
        out, _ = self.render()
        caddy = (out / "local/Caddyfile").read_text()
        self.assertIn("https://control.site.example:10443", caddy)
        self.assertIn("reverse_proxy [::1]:18081", caddy)
        self.assertIn("reverse_proxy h2c://[::1]:18080", caddy)
        core = (out / "local/netbird.yaml").read_text()
        self.assertIn('issuer: "https://control.site.example:10443/oauth2"', core)
        self.assertIn('listenAddress: "[::1]:18080"', core)
        self.assertIn("NB_LISTEN_ADDRESS=:28443", (out / "auxiliary/relay.env").read_text())
        self.assertIn("NB_STUN_PORTS=23478", (out / "auxiliary/relay.env").read_text())
        client = tomllib.loads((out / "local/frpc.toml").read_text())
        self.assertEqual(client["serverPort"], 27000)
        self.assertEqual(client["proxies"][0]["remotePort"], 24444)

    def test_invalid_configuration_leaves_no_partial_bundle(self):
        base = copy.deepcopy(self.config)
        changes = [lambda c: c.update(Listen="0.0.0.0:8081"),
                   lambda c: c.update(ManagementURL="http://127.0.0.1:70000"),
                   lambda c: c.update(Deployment={"LocalRelayPort": 10443}),
                   lambda c: c.update(Deployment={"Mode": "443", "LocalRelayPort": 9443}),
                   lambda c: c["Site"].update(LocalRelay='relay.example\nNB_TLS_KEY_FILE=bad'),
                   lambda c: c["Site"].update(Origin="https://control.site.example:0"),
                   lambda c: c.update(Deployment={"LocalSTUNPort": 8443}),
                   lambda c: c.update(Deployment={"LocalRelayPort": True})]
        for index, change in enumerate(changes):
            with self.subTest(index=index):
                self.config = copy.deepcopy(base)
                change(self.config)
                with self.assertRaises(ValueError):
                    self.render(suffix=f"invalid-{index}")
                self.assertFalse((self.root / f"invalid-{index}").exists())
        self.config = copy.deepcopy(base)
        self.auxiliary("443")
        self.config["Site"]["Candidates"][-1]["Address"] = "aux.example:8443"
        with self.assertRaises(ValueError):
            self.render()

    def test_repeat_render_preserves_secrets_and_rejects_overwrite(self):
        first, _ = self.render()
        second, _ = self.render(suffix="second", secrets=first / "secrets.json")
        self.assertEqual((first / "local/native.secret").read_bytes(), (second / "local/native.secret").read_bytes())
        self.assertEqual((first / "local/netbird.yaml").read_bytes(), (second / "local/netbird.yaml").read_bytes())
        with self.assertRaises(FileExistsError):
            self.render(secrets=first / "secrets.json")

    def test_invalid_secret_and_duplicate_json_rejected(self):
        secrets = self.root / "secrets.json"
        secrets.write_text('{"relay_secret":"short"}')
        with self.assertRaises(ValueError):
            self.render(secrets=secrets)
        self.assertFalse((self.root / "out").exists())
        source = self.root / "duplicate.json"
        source.write_text('{"Site":{},"Site":{}}')
        with self.assertRaises(ValueError):
            deploy.read_json(source)


if __name__ == "__main__":
    unittest.main()
