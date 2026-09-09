// The same stand in, with ONE thing changed: after the handshake the
// terminated socket is piped raw to a plaintext HTTP/2 backend instead of
// being read by an HTTP/1.1 parser.
//
// This is the control for the gRPC measurement. Everything else is identical,
// including the CONNECT handling, the certificate and the surface check, so a
// difference in the result is caused by the parser and by nothing else.
const http = require('http');
const net = require('net');
const tls = require('tls');
const fs = require('fs');
const CA = { key: fs.readFileSync('leaf.key'), cert: fs.readFileSync('leaf.crt') };
const ROUTES = JSON.parse(process.env.AF_PROBE_ROUTES || '{}');
const LOG = [];

const proxy = http.createServer((req, res) => { res.writeHead(400); res.end(); });
proxy.on('connect', (req, clientSocket) => {
  const host = req.url.split(':')[0];
  const dest = ROUTES[host];
  LOG.push({ event: 'connect', host, routed: !!dest });
  if (!dest) { clientSocket.write('HTTP/1.1 403 Forbidden\r\n\r\n'); clientSocket.end(); return; }
  clientSocket.write('HTTP/1.1 200 Connection Established\r\n\r\n');
  const t = new tls.TLSSocket(clientSocket, {
    isServer: true, key: CA.key, cert: CA.cert, ALPNProtocols: ['h2'],
  });
  t.on('secureConnect', () => LOG.push({ event: 'tls', host, alpn: t.alpnProtocol || null }));
  t.on('error', e => LOG.push({ event: 'tls-error', host, error: String(e.message || e) }));
  const [dh, dp] = dest.split(':');
  const up = net.connect(Number(dp), dh, () => { t.pipe(up); up.pipe(t); });
  up.on('error', e => LOG.push({ event: 'upstream-error', error: String(e.message || e) }));
});
proxy.listen(Number(process.env.AF_PROBE_PORT || 18082), '127.0.0.1',
  () => process.stdout.write('sidecar-h2 listening\n'));
process.on('SIGTERM', () => {
  fs.writeFileSync(process.env.AF_PROBE_LOG || 'sidecar-h2.json', JSON.stringify(LOG, null, 2));
  process.exit(0);
});
