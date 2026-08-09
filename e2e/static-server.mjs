// The published application, served as a plain directory — no API behind it.
// That is exactly what GitHub Pages does, and it is the only way to exercise
// browser mode for real: the interface must decide, on its own, that no server
// is listening.

import { createReadStream, statSync } from "node:fs";
import { createServer } from "node:http";
import { extname, join, normalize } from "node:path";

const [root, port] = process.argv.slice(2);

const TYPES = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".csv": "text/csv; charset=utf-8",
  ".svg": "image/svg+xml",
  ".json": "application/json; charset=utf-8",
};

createServer((request, response) => {
  const url = new URL(request.url, "http://localhost");
  // `normalize` on an absolute path already neutralises « .. » ; the prefix
  // check costs nothing and survives the day this root becomes a variable.
  const path = join(root, normalize(url.pathname));
  if (!path.startsWith(root)) {
    response.writeHead(403).end("forbidden");
    return;
  }
  let file = path;
  try {
    if (statSync(file).isDirectory()) file = join(file, "index.html");
  } catch {
    // a single-page application: an unknown path is a route, not a 404
    file = join(root, "index.html");
  }
  try {
    statSync(file);
  } catch {
    response.writeHead(404).end("not found");
    return;
  }
  response.writeHead(200, {
    "content-type": TYPES[extname(file)] ?? "application/octet-stream",
    "cache-control": "no-store",
  });
  createReadStream(file).pipe(response);
}).listen(Number(port), "127.0.0.1");
