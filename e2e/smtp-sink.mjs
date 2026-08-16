// A throwaway SMTP relay, and an HTTP window onto what it received.
//
// Signing in by email is the one feature whose proof cannot live in the
// application: the token leaves through a socket the process does not own,
// and reappears in a browser. Everything else about it can be faked, and
// faking it is exactly how one would ship a link that never arrives.
//
// It speaks only as much SMTP as Go's net/smtp client needs — greeting,
// EHLO, MAIL, RCPT, DATA, QUIT — and no authentication and no TLS, because
// it listens on the loopback and holds nothing. What the message must LOOK
// like is pinned in api/mail_test.go; what this proves is that it goes out
// and that its link opens a session.

import { createServer as createHttpServer } from "node:http";
import { createServer } from "node:net";

const [smtpPort, httpPort] = process.argv.slice(2);

/** Every message received, newest last. */
const received = [];

createServer((socket) => {
  socket.setEncoding("utf8");
  let inData = false;
  let buffer = "";
  let message = "";
  const recipients = [];

  socket.write("220 sink ready\r\n");
  socket.on("data", (chunk) => {
    buffer += chunk;
    for (;;) {
      const end = buffer.indexOf("\r\n");
      if (end < 0) break;
      const line = buffer.slice(0, end);
      buffer = buffer.slice(end + 2);

      if (inData) {
        if (line === ".") {
          inData = false;
          received.push({ recipients: [...recipients], message });
          recipients.length = 0;
          message = "";
          socket.write("250 taken\r\n");
        } else {
          // dot-stuffing, undone: a body line starting with a dot arrives
          // doubled
          message += `${line.startsWith("..") ? line.slice(1) : line}\n`;
        }
        continue;
      }

      const verb = line.slice(0, 4).toUpperCase();
      if (verb === "EHLO" || verb === "HELO") {
        // one extension line then the terminator, which is the shape a
        // client parses; none of them is offered
        socket.write("250-sink\r\n250 SIZE 10485760\r\n");
      } else if (verb === "MAIL") {
        socket.write("250 sender ok\r\n");
      } else if (verb === "RCPT") {
        const address = /<([^>]*)>/.exec(line);
        recipients.push(address ? address[1] : line.slice(8).trim());
        socket.write("250 recipient ok\r\n");
      } else if (verb === "DATA") {
        inData = true;
        socket.write("354 go ahead\r\n");
      } else if (verb === "RSET") {
        recipients.length = 0;
        message = "";
        socket.write("250 reset\r\n");
      } else if (verb === "QUIT") {
        socket.write("221 bye\r\n");
        socket.end();
      } else {
        socket.write("250 ok\r\n");
      }
    }
  });
  socket.on("error", () => socket.destroy());
}).listen(Number(smtpPort), "127.0.0.1");

createHttpServer((request, response) => {
  if (request.method === "DELETE") {
    received.length = 0;
    response.writeHead(204).end();
    return;
  }
  response.writeHead(200, {
    "content-type": "application/json; charset=utf-8",
    "cache-control": "no-store",
  });
  // The bodies are base64 (see api/mail.go): decoded here, so a test reads
  // the sentence a volunteer reads.
  response.end(
    JSON.stringify(
      received.map(({ recipients, message }) => {
        const [head, body = ""] = message.split("\n\n");
        return {
          recipients,
          headers: head,
          body: Buffer.from(body.replace(/\n/g, ""), "base64").toString("utf8"),
        };
      }),
    ),
  );
}).listen(Number(httpPort), "127.0.0.1");
