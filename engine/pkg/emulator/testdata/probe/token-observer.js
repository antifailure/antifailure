// Records the token exchange an unmodified Google client library performs.
// Answers with a shaped OAuth response so the run continues past it.
const http = require('http');
const fs = require('fs');
const seen = [];
const s = http.createServer((req, res) => {
  let body = '';
  req.on('data', d => body += d);
  req.on('end', () => {
    seen.push({ host: req.headers.host, method: req.method, url: req.url, body: body.slice(0, 400) });
    res.writeHead(200, { 'content-type': 'application/json' });
    res.end(JSON.stringify({ access_token: 'af-probe-token', expires_in: 3600, token_type: 'Bearer' }));
  });
});
s.listen(19996, '127.0.0.1', () => process.stdout.write('token observer on 19996\n'));
process.on('SIGTERM', () => { fs.writeFileSync('token-observed.json', JSON.stringify(seen, null, 2)); process.exit(0); });
