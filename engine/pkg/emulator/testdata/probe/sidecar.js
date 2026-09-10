// A stand in for engine/cmd/af-proxy's inspected path, and NOTHING more.
//
// It exists to answer one question the repository cannot answer yet, because
// the routing half of emulate mode is not built: does an UNMODIFIED Google
// client library reach an emulator when every name resolves through a proxy
// that terminates TLS with an authority the environment trusts?
//
// It is faithful to af-proxy in the two places that decide the answer:
//   1. It answers CONNECT, then terminates TLS with a leaf issued by the
//      environment's own authority, exactly as mitm.go does.
//   2. It sets NO ALPNProtocols, exactly as mitm.go's tls.Config does not,
//      and then reads HTTP/1.1 requests out of the terminated connection,
//      exactly as mitm.go's bufio + http.ReadRequest loop does.
// The second one is the whole reason a gRPC client cannot traverse it, and a
// stand in that quietly offered h2 would have proved the opposite of the
// truth.
const http = require('http');
const net = require('net');
const tls = require('tls');
const fs = require('fs');

const CA = { key: fs.readFileSync('leaf.key'), cert: fs.readFileSync('leaf.crt') };
const LOG = [];
function log(entry) { LOG.push({ at: Date.now(), ...entry }); }

// The surface: the only names routed to an emulator. Everything else is
// refused, which is the egress default and the point of the surface table.
const ROUTES = JSON.parse(process.env.AF_PROBE_ROUTES || '{}');

function routeFor(host) {
  if (ROUTES[host]) return ROUTES[host];
  for (const [pattern, addr] of Object.entries(ROUTES)) {
    if (pattern.startsWith('*.') && host.endsWith(pattern.slice(1)) &&
        host.length > pattern.length - 1) return addr;
  }
  return null;
}

const inner = http.createServer();

// The inspected path. One terminated connection, many requests, each decided
// on its own, and the Host header PRESERVED: fake-gcs-server routes on Host
// and its public host is storage.googleapis.com, so rewriting it would miss
// every route in the emulator.
inner.on('request', (req, res) => {
  const host = (req.headers.host || '').split(':')[0];
  const dest = routeFor(host);
  log({ event: 'request', host, method: req.method, path: req.url, routed: !!dest });
  if (!dest) {
    res.writeHead(403, { 'content-type': 'application/json' });
    res.end(JSON.stringify({ error: { code: 403, message:
      `af-proxy: ${host} is outside the emulated surface and no rule allows it` } }));
    return;
  }
  const [dh, dp] = dest.split(':');
  const up = http.request({ host: dh, port: Number(dp), method: req.method,
    path: req.url, headers: req.headers }, upres => {
    res.writeHead(upres.statusCode, upres.headers);
    upres.pipe(res);
  });
  up.on('error', e => { res.writeHead(502); res.end(String(e)); });
  req.pipe(up);
});

const proxy = http.createServer((req, res) => {
  res.writeHead(400); res.end('af-proxy: plain http not used by this probe');
});

proxy.on('connect', (req, clientSocket, head) => {
  const host = req.url.split(':')[0];
  const dest = routeFor(host);
  log({ event: 'connect', host, routed: !!dest });
  if (!dest) {
    // Outside the surface. Refused at CONNECT, which is what the egress
    // default does to a host no rule names.
    clientSocket.write('HTTP/1.1 403 Forbidden\r\n\r\n');
    clientSocket.end();
    return;
  }
  clientSocket.write('HTTP/1.1 200 Connection Established\r\n\r\n');
  // AF_PROBE_ALPN is the mutation, not a feature. Unset is faithful to
  // af-proxy, which sets no ALPNProtocols at mitm.go:184. Setting it to h2 is
  // the one break that tells a failure caused by the missing ALPN apart from
  // a failure caused by anything else about this stand in.
  const opts = { isServer: true, key: CA.key, cert: CA.cert };
  if (process.env.AF_PROBE_ALPN) opts.ALPNProtocols = process.env.AF_PROBE_ALPN.split(',');
  const tlsSock = new tls.TLSSocket(clientSocket, opts);
  tlsSock.on('error', e => log({ event: 'tls-error', host, error: String(e.message || e) }));
  tlsSock.on('secureConnect', () => log({ event: 'tls', host, alpn: tlsSock.alpnProtocol || null }));
  inner.emit('connection', tlsSock);
  if (head && head.length) tlsSock.unshift(head);
});

const port = Number(process.env.AF_PROBE_PORT || 18080);
proxy.listen(port, '127.0.0.1', () => {
  process.stdout.write(`sidecar listening on ${port}\n`);
});

process.on('SIGTERM', () => {
  fs.writeFileSync(process.env.AF_PROBE_LOG || 'sidecar.log.json', JSON.stringify(LOG, null, 2));
  process.exit(0);
});
process.on('SIGINT', () => process.emit('SIGTERM'));
