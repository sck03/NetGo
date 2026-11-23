"""Loopback TLS tests; these do not claim LAN/NAT/Relay network acceptance."""
import argparse, base64, datetime, hashlib, http.server, json, pathlib, ssl, socketserver, subprocess, tempfile, threading, time
from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import rsa, ed25519
from cryptography.x509.oid import NameOID, ExtendedKeyUsageOID

def unb64(s):
    return base64.urlsafe_b64decode(s + "=" * (-len(s) % 4))

def run(binary):
    now = datetime.datetime.now(datetime.timezone.utc)
    key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
    name = x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, "NetGo loopback CA")])
    ca = (x509.CertificateBuilder().subject_name(name).issuer_name(name).public_key(key.public_key())
          .serial_number(x509.random_serial_number()).not_valid_before(now-datetime.timedelta(days=1))
          .not_valid_after(now+datetime.timedelta(days=2)).add_extension(x509.BasicConstraints(ca=True,path_length=0),True)
          .sign(key, hashes.SHA256()))
    cert_key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
    def certificate(expired=False):
        return (x509.CertificateBuilder().subject_name(x509.Name([x509.NameAttribute(NameOID.COMMON_NAME,"netgo.test")]))
                .issuer_name(name).public_key(cert_key.public_key()).serial_number(x509.random_serial_number())
                .not_valid_before(now-datetime.timedelta(days=3)).not_valid_after(now-datetime.timedelta(days=1) if expired else now+datetime.timedelta(days=1))
                .add_extension(x509.SubjectAlternativeName([x509.DNSName("netgo.test")]),False)
                .add_extension(x509.ExtendedKeyUsage([ExtendedKeyUsageOID.SERVER_AUTH]),False).sign(key,hashes.SHA256()))
    events=[]
    expected_host="netgo.test"
    wan_ready=threading.Event()
    class Handler(http.server.BaseHTTPRequestHandler):
        protocol_version="HTTP/1.1"
        def log_message(self,*args): pass
        def reply(self,value):
            data=json.dumps(value,separators=(",",":")).encode()
            self.send_response(200);self.send_header("Content-Type","application/json");self.send_header("Content-Length",str(len(data)));self.end_headers();self.wfile.write(data)
        def do_GET(self):
            events.append(("health",self.headers["Host"]))
            self.reply({"protocol":1,"site_id":"unavailable" if getattr(self.server,"wan",False) and not wan_ready.is_set() else "fixture",
                        "capabilities":["native-auth-v1","device-signature-v1","leases-v1","profile-revision-v1","admin-v1","diagnostics-v1"]})
        def do_POST(self):
            body=self.rfile.read(int(self.headers["Content-Length"]))
            token="fixture-token";stamp=self.headers["X-NetGo-Time"];nonce=self.headers["X-NetGo-Nonce"]
            message="\n".join(["POST",self.path,stamp,nonce,hashlib.sha256(body).hexdigest(),hashlib.sha256(token.encode()).hexdigest()])
            ed25519.Ed25519PrivateKey.from_private_bytes(b"a"*32).public_key().verify(unb64(self.headers["X-NetGo-Signature"]),message.encode())
            assert self.headers["Authorization"]=="Bearer "+token
            assert self.headers["X-NetGo-Protocol"]=="1"
            assert len(nonce)>=32 and abs(int(stamp)-now.timestamp())<60
            assert self.headers["Host"]==expected_host and self.path in ("/fixture","/enable-wan")
            if self.path=="/enable-wan": wan_ready.set()
            if getattr(self.server,'response_delay',0):time.sleep(self.server.response_delay)
            events.append(("verified",self.headers["Host"]));self.reply({"verified":True})
    with tempfile.TemporaryDirectory(prefix="netgo-tls-") as directory:
        root=pathlib.Path(directory)
        (root/"key.pem").write_bytes(cert_key.private_bytes(serialization.Encoding.PEM,serialization.PrivateFormat.PKCS8,serialization.NoEncryption()))
        for expired in (False,True):
            (root/"cert.pem").write_bytes(certificate(expired).public_bytes(serialization.Encoding.PEM))
            server=http.server.ThreadingHTTPServer(("127.0.0.1",0),Handler)
            context=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);context.load_cert_chain(root/"cert.pem",root/"key.pem")
            context.set_servername_callback(lambda sock, host, ctx: events.append(("sni",host)))
            server.socket=context.wrap_socket(server.socket,server_side=True)
            thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start()
            p={"Protocol":1,"ID":"fixture","Name":"Loopback TLS","Origin":"https://netgo.test","Candidates":[
                {"ID":"direct","Address":"127.0.0.1:1","Kind":"direct"},
                {"ID":"fallback","Address":f"127.0.0.1:{server.server_port}","Kind":"auxiliary"}],
               "CA":ca.public_bytes(serialization.Encoding.PEM).decode(),"GatewayIP":"100.96.0.1"}
            file=root/"profile.json"
            if expired:
                file.write_text(json.dumps(p),encoding="utf-8");subprocess.run([binary,"--tls-reject",str(file)],check=True,timeout=30)
            else:
                file.write_text(json.dumps(p),encoding="utf-8");subprocess.run([binary,"--tls",str(file)],check=True,timeout=30)
                server.response_delay=3.5
                try:
                    subprocess.run([binary,"--tls",str(file)],check=True,timeout=30)
                    print('PASS signed API response slower than health-probe deadline')
                finally:server.response_delay=0
                expected_host="netgo.test:10443"
                p["Origin"]="https://"+expected_host
                file.write_text(json.dumps(p),encoding="utf-8");subprocess.run([binary,"--tls",str(file)],check=True,timeout=30)
                expected_host="netgo.test"
                p["Origin"]="https://"+expected_host
                direct=http.server.ThreadingHTTPServer(("127.0.0.1",0),Handler)
                direct.wan=True;direct.socket=context.wrap_socket(direct.socket,server_side=True)
                direct_thread=threading.Thread(target=direct.serve_forever,daemon=True);direct_thread.start()
                original_candidates=p["Candidates"]
                try:
                    p["Origin"]="https://netgo.test:10443";expected_host="netgo.test:10443"
                    p["Candidates"]=[{"ID":"wan1","Kind":"direct","Address":"127.0.0.1:1"},
                                     {"ID":"wan2","Kind":"direct","Address":f"127.0.0.1:{direct.server_port}"},
                                     {"ID":"wan3","Kind":"direct","Address":"127.0.0.1:1"},original_candidates[-1]]
                    file.write_text(json.dumps(p),encoding="utf-8")
                    subprocess.run([binary,"--tls-multiwan",str(file)],check=True,timeout=30)
                finally:
                    p["Candidates"]=original_candidates;p["Origin"]="https://netgo.test";expected_host="netgo.test"
                    direct.shutdown();direct.server_close();direct_thread.join(timeout=3)
                # Seven TCP listeners stall TLS; fallback must stay within a
                # single bounded probe window, rather than seven serial waits.
                class Stall(socketserver.BaseRequestHandler):
                    def handle(self):
                        self.request.settimeout(5)
                        try:
                            self.request.recv(8192)
                            self.request.recv(8192)
                        except OSError:
                            pass
                class StallServer(socketserver.ThreadingTCPServer):
                    daemon_threads=True
                stall=StallServer(("127.0.0.1",0),Stall)
                stall_thread=threading.Thread(target=stall.serve_forever,daemon=True);stall_thread.start()
                original=p["Candidates"]
                try:
                    p["Candidates"]=[{"ID":f"stall-{i}","Address":f"127.0.0.1:{stall.server_address[1]}","Kind":"direct"} for i in range(7)]+[original[-1]]
                    file.write_text(json.dumps(p),encoding="utf-8")
                    started=time.monotonic();subprocess.run([binary,"--tls",str(file)],check=True,timeout=10)
                    assert time.monotonic()-started < 6, "fallback exceeded concurrent probe window"
                    print("PASS seven stalled TLS candidates: bounded parallel fallback")
                finally:
                    p["Candidates"]=original;stall.shutdown();stall.server_close();stall_thread.join(timeout=3)
                p["Origin"]="https://wrong.test"
                file.write_text(json.dumps(p),encoding="utf-8");subprocess.run([binary,"--tls-reject",str(file)],check=True,timeout=30)
                p["Origin"]="https://netgo.test";p["CA"]=""
                file.write_text(json.dumps(p),encoding="utf-8");subprocess.run([binary,"--tls-reject",str(file)],check=True,timeout=30)
            server.shutdown();server.server_close();thread.join(timeout=3)
    assert ("verified","netgo.test") in events and ("sni","netgo.test") in events
    assert ("verified","netgo.test:10443") in events
    assert all(host in ("netgo.test", "netgo.test:10443") for action,host in events if action in ("health","verified"))
    print("PASS loopback TLS: canonical SNI/Host including port 10443, auxiliary fallback, Ed25519, wrong host, unknown CA, expired certificate")

if __name__=="__main__":
    parser=argparse.ArgumentParser();parser.add_argument("binary");args=parser.parse_args();run(args.binary)
