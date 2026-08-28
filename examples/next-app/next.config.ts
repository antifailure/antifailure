import path from "node:path";
import type { NextConfig } from "next";

const config: NextConfig = {
  // Standalone, so the runtime image carries the server and the module graph
  // it actually reached rather than the whole of node_modules. It is the
  // difference between an image worth scanning and one nobody scans.
  output: "standalone",

  // Pinned, and this is not a formality. Next infers the root of the file
  // trace by walking up looking for lockfiles, so an example checked out
  // inside a repository that has its own lockfiles above it gets a root
  // several directories too high, and server.js is written to
  // .next/standalone/<the whole path back down>/server.js instead of
  // .next/standalone/server.js.
  //
  // In the Docker build the context is this directory alone, so the inference
  // is right and the Dockerfile works. On a laptop it is wrong, which means
  // the artifact shape depended on where somebody cloned the repository. This
  // makes it the same in both places.
  outputFileTracingRoot: path.join(__dirname),
};

export default config;
