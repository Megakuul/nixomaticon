# swyxtap — SwyxWare client-protocol tap (development only)

`swyxtap` decrypts and records the SwyxWare client channel so the protocol can
be reverse engineered. SwyxIt! talks to the server over **WCF `net.tcp` on TCP
9094**, which upgrades to TLS (`application/ssl-tls`) and carries binary SOAP —
status, phonebook and chat all ride it, and a plain packet capture only sees
ciphertext. `swyxtap` sits in the middle: it forwards every configured port to
the real server, and on 9094 it terminates the TLS toward SwyxIt!, opens its own
TLS to the server, and logs the decrypted framing records both ways.

It is **not** part of the shipped application and ships no credentials. It works
only because SwyxWare uses a self-signed certificate that SwyxIt! does not
validate.

## Run it

On a host the SwyxIt! machine can reach and that can itself reach the server:

```
go run ./cmd/swyxtap -listen 0.0.0.0 -upstream 10.1.1.17 -log ./swyxtap-logs
```

Then in **SwyxIt! → settings → server**, replace the server address with the IP
of the host running `swyxtap` (keep everything else). SwyxIt! will send SIP,
RTP and the WCF channel at the tap; SIP/RTP pass straight through, 9094 is
intercepted. Log back in and reproduce what you want captured.

Flags: `-ports` (forwarded TCP ports, default `9094,5060,9100,9090`),
`-tls-ports` (intercepted as WCF, default `9094`), `-sni`/`-cert-cn` (TLS names,
default `IpPbx`).

## What to capture

Keeping to the standing constraint (contact **100** only, no real calls):

1. Start the tap, start SwyxIt! pointed at it, log in.
2. Set a status / status text, toggle away and do-not-disturb.
3. Send a chat message to extension **100** and, if possible, receive one back.
4. Stop the tap and send the `swyxtap-logs/` directory back.

Each 9094 connection writes `conn-NNN-<Facade>.log`: per message, a timestamp,
direction (`C->S` / `S->C`), the printable strings (SOAP element names and
values), and a hex dump. `CFilesFacadeImpl`, `CPhoneClientFacadeImpl` and
`CUserPhoneBookEnumImpl` are the facades seen so far; chat should surface a new
one or a callback on the phone-client facade.

## If it does not work

- **SwyxIt! refuses the certificate** — it is validating the server cert after
  all. Fall back to WCF message logging: add a `System.ServiceModel`
  `messageLogging` block (`logEntireMessage=true`) to the CLMgr/SwyxIt! config
  and collect the `.svclog`.
- **The server faults every message** — it is enforcing the WS-Addressing `To`
  header, which now names the tap. The preamble `Via` is already rewritten to
  the real server; the in-message `To` is not. Tell me and I will add
  `To`-rewriting (needs the binary-SOAP codec, which the real client needs
  anyway). A quick alternative is transparent interception (route `10.1.1.17` on
  the client to this host) so the client keeps addressing the real server.
