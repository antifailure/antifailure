// NOT an emulator, and it must never be mistaken for one.
//
// It records what an unmodified @google-cloud/storage client asks for: which
// hosts it contacts, whether it fetches a token before its first storage
// call, whether it preserves the Host header, and what its request path looks
// like. It answers each call with the smallest valid shaped response so the
// client keeps going to the next one. Nothing here is a fidelity claim, and
// the real surface is answered by fake-gcs-server in a container.
const http = require('http');
const fs = require('fs');
const seen = [];
// crc32c, Castagnoli, because that is the checksum Google's clients verify.
const CRC_TABLE = (() => {
  const t = new Int32Array(256);
  for (let n = 0; n < 256; n++) {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? (c >>> 1) ^ 0x82f63b78 : c >>> 1;
    t[n] = c;
  }
  return t;
})();
function crc32cBase64(buf) {
  let c = 0 ^ -1;
  for (const b of buf) c = (c >>> 8) ^ CRC_TABLE[(c ^ b) & 0xff];
  c = (c ^ -1) >>> 0;
  const out = Buffer.alloc(4);
  out.writeUInt32BE(c);
  return out.toString('base64');
}
const s = http.createServer((req, res) => {
  const chunks = [];
  req.on('data', d => chunks.push(d));
  req.on('end', () => {
    const raw = Buffer.concat(chunks);
    seen.push({ host: req.headers.host, method: req.method, url: req.url,
      authorization: req.headers.authorization ? req.headers.authorization.split(' ')[0] : null });
    const j = o => { res.writeHead(200, { 'content-type': 'application/json' });
      res.end(JSON.stringify(o)); };
    const m = req.url.match(/^\/storage\/v1\/b\/([^/?]+)\/o\/([^?]+)/);
    if (req.method === 'POST' && req.url.startsWith('/storage/v1/b?')) return j({ kind: 'storage#bucket', name: 'af-l33-probe' });
    if (req.url.startsWith('/upload/storage/v1/b/')) {
      // The client verifies the checksum the server reports against what it
      // sent. A stub that omitted it made the client delete the object and
      // report a corrupt upload, which looked like a finding and was not.
      const payload = Buffer.from('the twin wrote this');
      return j({ kind: 'storage#object', name: 'hello.txt', bucket: 'af-l33-probe',
        size: String(payload.length),
        md5Hash: require('crypto').createHash('md5').update(payload).digest('base64'),
        crc32c: crc32cBase64(payload) });
    }
    if (req.url.startsWith('/download/storage/v1/') || (m && req.url.includes('alt=media'))) { res.writeHead(200, { 'content-type': 'text/plain' }); return res.end('the twin wrote this'); }
    if (m) return j({ kind: 'storage#object', name: 'hello.txt', bucket: 'af-l33-probe', size: '19' });
    if (req.url.startsWith('/storage/v1/b/')) return j({ kind: 'storage#objects', items: [{ name: 'hello.txt', bucket: 'af-l33-probe', size: '19' }] });
    return j({});
  });
});
s.listen(19997, '127.0.0.1', () => process.stdout.write('observer on 19997\n'));
process.on('SIGTERM', () => { fs.writeFileSync('gcs-observed.json', JSON.stringify(seen, null, 2)); process.exit(0); });
