#!/usr/bin/env python3
"""Deterministic TCP/UDP target for an isolated network namespace, never a VPN peer."""
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import socketserver
import threading

PAYLOAD=bytes(range(256))*1024


class HTTP(BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.send_header('Content-Length',str(len(PAYLOAD)))
        self.send_header('X-NetGo-Test-SHA256',hashlib.sha256(PAYLOAD).hexdigest())
        self.send_header('X-NetGo-Test-Source',self.client_address[0])
        self.end_headers()
        self.wfile.write(PAYLOAD)
    def log_message(self,*args):pass


class TCP(socketserver.BaseRequestHandler):
    def handle(self):
        while True:
            data=self.request.recv(4096)
            if not data:return
            self.request.sendall(data)


class UDP(socketserver.BaseRequestHandler):
    def handle(self):
        data,sock=self.request
        sock.sendto(b'netgo-source:'+self.client_address[0].encode()+b'\n'+data,self.client_address)


if __name__=='__main__':
    servers=[ThreadingHTTPServer(('0.0.0.0',port),HTTP) for port in (18080,18082)]
    servers += [socketserver.ThreadingTCPServer(('0.0.0.0',18083),TCP),socketserver.ThreadingUDPServer(('0.0.0.0',18081),UDP)]
    for server in servers:
        threading.Thread(target=server.serve_forever,daemon=True).start()
    threading.Event().wait()
