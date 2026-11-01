import http.server, json
class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        n=int(self.headers.get('Content-Length',0)); body=json.loads(self.rfile.read(n) or b'{}')
        open('<scratchpad>/sim/last.json','w').write(json.dumps({"path":self.path,"auth":self.headers.get('Authorization'),"body":body}))
        if self.path=="/key/generate":
            if body.get("key_alias")=="dup":
                code,out=400,{"error":{"message":"Key with alias 'dup' already exists. Unique key aliases across all keys are required.","type":"bad_request_error","param":"key_alias","code":"400"}}
            else: code,out=200,{"key":"sk-new123","key_alias":body.get("key_alias")}
        elif self.path=="/key/delete":
            if body.get("keys")==["sk-gone"]: code,out=404,{"error":{"message":"{'error': 'No keys found'}","type":"None","param":"None","code":"404"}}
            else: code,out=200,{"deleted_keys":body.get("keys")}
        else: code,out=404,{}
        self.send_response(code); self.send_header('Content-Type','application/json'); self.end_headers(); self.wfile.write(json.dumps(out).encode())
    def log_message(self,*a): pass
http.server.HTTPServer(('127.0.0.1',18766),H).serve_forever()
