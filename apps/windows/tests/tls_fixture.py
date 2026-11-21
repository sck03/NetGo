"""Loopback TLS tests; these do not claim LAN/NAT/Relay network acceptance."""
import argparse, base64, datetime, hashlib, http.server, json, pathlib, ssl, subprocess, tempfile, threading
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
    class Handler(http.server.BaseHTTPRequestHandler):
        protocol_version="HTTP/1.1"
        def log_message(self,*args): pass
        def reply(self,value):
            data=json.dumps(value,separators=(",",":")).encode()
            self.send_response(200);self.send_header("Content-Type","application/json");self.send_header("Content-Length",str(len(data)));self.end_headers();self.wfile.write(data)
        def do_GET(self):
            events.append(("health",self.headers["Host"]))
            self.reply({"protocol":1,"site_id":"fixture"})
        def do_POST(self):
            body=self.rfile.read(int(self.headers["Content-Length"]))
            token="fixture-token";stamp=self.headers["X-NetGo-Time"];nonce=self.headers["X-NetGo-Nonce"]
            message="\n".join(["POST",self.path,stamp,nonce,hashlib.sha256(body).hexdigest(),hashlib.sha256(token.encode()).hexdigest()])
            ed25519.Ed25519PrivateKey.from_private_bytes(b"a"*32).public_key().verify(unb64(self.headers["X-NetGo-Signature"]),message.encode())
            assert self.headers["Authorization"]=="Bearer "+token
            assert len(nonce)>=32 and abs(int(stamp)-now.timestamp())<60
            assert self.headers["Host"]=="netgo.test" and self.path=="/fixture"
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
                p["Origin"]="https://wrong.test"
                file.write_text(json.dumps(p),encoding="utf-8");subprocess.run([binary,"--tls-reject",str(file)],check=True,timeout=30)
                p["Origin"]="https://netgo.test";p["CA"]=""
                file.write_text(json.dumps(p),encoding="utf-8");subprocess.run([binary,"--tls-reject",str(file)],check=True,timeout=30)
            server.shutdown();server.server_close();thread.join(timeout=3)
    assert ("verified","netgo.test") in events and ("sni","netgo.test") in events
    assert all(host=="netgo.test" for action,host in events if action in ("health","verified"))
    print("PASS loopback TLS: canonical SNI/Host, auxiliary fallback, Ed25519, wrong host, unknown CA, expired certificate")

if __name__=="__main__":
    parser=argparse.ArgumentParser();parser.add_argument("binary");args=parser.parse_args();run(args.binary)
