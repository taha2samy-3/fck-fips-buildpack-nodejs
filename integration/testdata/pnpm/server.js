const http = require("http");
const crypto = require("crypto");
const ms = require("ms");

let md5 = true;
try { crypto.createHash("md5"); } catch { md5 = false; }

http.createServer((req, res) => {
  res.setHeader("content-type", "application/json");
  res.end(JSON.stringify({
    node: process.version,
    openssl: process.versions.openssl,
    fips: crypto.getFips(),
    md5,
    ms: ms("1h"),
    nodeOptions: process.env.NODE_OPTIONS || "",
  }));
}).listen(process.env.PORT || 8080);
