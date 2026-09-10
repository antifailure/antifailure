// A plaintext HTTP/2 backend standing in for a gRPC emulator.
//
// It answers every stream with a gRPC Trailers-Only response carrying status
// 12, UNIMPLEMENTED. That is not a useful emulator; it is a way to tell one
// question from another. If the client reports UNIMPLEMENTED it means the
// gRPC transport reached a server and the server answered, so the transport
// is not the problem. If it reports UNAVAILABLE the transport never got
// there.
const http2 = require('http2');
const s = http2.createServer();
s.on('stream', stream => {
  stream.respond({
    ':status': 200,
    'content-type': 'application/grpc',
    'grpc-status': '12',
    'grpc-message': 'the h2 sink answers nothing, and answering is the point',
  }, { endStream: true });
});
s.listen(19998, '127.0.0.1', () => process.stdout.write('h2 sink on 19998\n'));
